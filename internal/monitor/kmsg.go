package monitor

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/oom-notifier/go/internal/logger"
)

type KmsgReader struct {
	file          *os.File
	scanner       *bufio.Scanner
	oomPattern    *regexp.Regexp
	pidPattern    *regexp.Regexp
	lastTimestamp uint64
	entryBuffer   chan KmsgEntry
	done          chan struct{}
}

type KmsgEntry struct {
	Priority    int
	SequenceNum uint64
	Timestamp   uint64
	Message     string
}

func NewKmsgReader() (*KmsgReader, error) {
	logger.Debug("Opening /dev/kmsg for reading")
	file, err := os.Open("/dev/kmsg")
	if err != nil {
		return nil, fmt.Errorf("failed to open /dev/kmsg: %v", err)
	}

	// Seek to end to skip historical messages and only read new ones
	logger.Debug("Seeking to end of /dev/kmsg to skip historical messages")
	if _, err := file.Seek(0, 2); err != nil {
		logger.Warn("Failed to seek to end of kmsg, will process historical messages: %v", err)
	}

	reader := &KmsgReader{
		file:        file,
		scanner:     bufio.NewScanner(file),
		oomPattern:  regexp.MustCompile(`(?i)out of memory:`),
		pidPattern:  regexp.MustCompile(`\bkilled process (\d+)\b`),
		entryBuffer: make(chan KmsgEntry, 100),
		done:        make(chan struct{}),
	}

	// Start background goroutine to read kmsg
	go reader.readLoop()

	logger.Debug("KmsgReader initialized successfully")
	return reader, nil
}

func (k *KmsgReader) Close() error {
	close(k.done)
	return k.file.Close()
}

func (k *KmsgReader) readLoop() {
	logger.Debug("Starting kmsg read loop")
	for {
		select {
		case <-k.done:
			logger.Debug("Stopping kmsg read loop")
			return
		default:
			if k.scanner.Scan() {
				line := k.scanner.Text()
				entry, err := k.parseKmsgLine(line)
				if err != nil {
					logger.Debug("Failed to parse kmsg line: %v", err)
					continue
				}

				select {
				case k.entryBuffer <- *entry:
				case <-k.done:
					return
				}
			}
		}
	}
}

func (k *KmsgReader) ReadEntries() ([]KmsgEntry, error) {
	var entries []KmsgEntry

	// Drain available entries from buffer
	for {
		select {
		case entry := <-k.entryBuffer:
			entries = append(entries, entry)
		default:
			// No more entries available
			if len(entries) > 0 {
				logger.Debug("Retrieved %d entries from buffer", len(entries))
			}
			return entries, nil
		}
	}
}

func (k *KmsgReader) parseKmsgLine(line string) (*KmsgEntry, error) {
	// kmsg format: priority,sequence,timestamp[,flag];message
	parts := strings.SplitN(line, ";", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid kmsg format")
	}

	metadata := strings.Split(parts[0], ",")
	if len(metadata) < 3 {
		return nil, fmt.Errorf("invalid kmsg metadata")
	}

	priority, err := strconv.Atoi(metadata[0])
	if err != nil {
		return nil, err
	}

	sequence, err := strconv.ParseUint(metadata[1], 10, 64)
	if err != nil {
		return nil, err
	}

	// Timestamp might have flags after it
	timestampStr := strings.Split(metadata[2], ",")[0]
	timestamp, err := strconv.ParseUint(timestampStr, 10, 64)
	if err != nil {
		return nil, err
	}

	return &KmsgEntry{
		Priority:    priority,
		SequenceNum: sequence,
		Timestamp:   timestamp,
		Message:     parts[1],
	}, nil
}

func (k *KmsgReader) IsOOMMessage(entry KmsgEntry) bool {
	isOOM := k.oomPattern.MatchString(entry.Message)
	if isOOM {
		logger.Debug("Detected OOM message: %s", entry.Message)
	}
	return isOOM
}

func (k *KmsgReader) ExtractPID(message string) (int, error) {
	matches := k.pidPattern.FindStringSubmatch(strings.ToLower(message))
	if len(matches) < 2 {
		logger.Debug("No PID pattern found in message: %s", message)
		return 0, fmt.Errorf("no PID found in OOM message")
	}

	pid, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, fmt.Errorf("failed to parse PID: %v", err)
	}

	logger.Debug("Extracted PID %d from OOM message", pid)
	return pid, nil
}

type OOMMonitor struct {
	kmsgReader       *KmsgReader
	processCache     *ProcessCache
	checkInterval    time.Duration
	refreshInterval  time.Duration
	startupTimestamp uint64
	bootTime         time.Time
}

func NewOOMMonitor(procDir string, checkInterval, refreshInterval time.Duration) (*OOMMonitor, error) {
	kmsgReader, err := NewKmsgReader()
	if err != nil {
		return nil, err
	}

	processCache, err := NewProcessCache(procDir)
	if err != nil {
		kmsgReader.Close()
		return nil, err
	}

	// Get boot time to convert kmsg timestamps (which are since boot) to Unix epoch
	bootTime, err := getBootTime()
	if err != nil {
		logger.Warn("Failed to get boot time, using current time as baseline: %v", err)
		bootTime = time.Now()
	}
	logger.Debug("System boot time: %s", bootTime.Format("2006-01-02 15:04:05"))

	// Store startup time as microseconds since boot (same as kmsg timestamps)
	startupTimestamp := uint64(time.Since(bootTime).Microseconds())
	logger.Debug("OOMMonitor startup timestamp (since boot): %d microseconds", startupTimestamp)

	return &OOMMonitor{
		kmsgReader:       kmsgReader,
		processCache:     processCache,
		checkInterval:    checkInterval,
		refreshInterval:  refreshInterval,
		startupTimestamp: startupTimestamp,
		bootTime:         bootTime,
	}, nil
}

func (m *OOMMonitor) Close() error {
	return m.kmsgReader.Close()
}

func (m *OOMMonitor) Start(eventChan chan<- OOMEventData) error {
	logger.Debug("Starting OOM monitor with check interval: %v, refresh interval: %v", m.checkInterval, m.refreshInterval)

	// Start process cache refresh routine
	go m.refreshProcessCache()

	// Monitor kernel messages
	ticker := time.NewTicker(m.checkInterval)
	defer ticker.Stop()

	logger.Debug("Starting kernel message monitoring loop")
	for range ticker.C {
		logger.Debug("Checking for new kernel messages")
		entries, err := m.kmsgReader.ReadEntries()
		if err != nil {
			logger.Error("Error reading kmsg entries: %v", err)
			continue
		}

		for _, entry := range entries {
			if m.kmsgReader.IsOOMMessage(entry) {
				logger.Info("OOM message detected! Processing...")

				// Filter out events that occurred before process startup
				if entry.Timestamp < m.startupTimestamp {
					logger.Debug("Skipping OOM event from before startup: timestamp=%d, startup=%d",
						entry.Timestamp, m.startupTimestamp)
					continue
				}

				pid, err := m.kmsgReader.ExtractPID(entry.Message)
				if err != nil {
					logger.Error("Failed to extract PID from OOM message: %v", err)
					continue
				}

				event := m.createOOMEvent(pid, entry.Timestamp)
				logger.Info("Sending OOM event: PID=%d, Process=%s, Timestamp=%d",
					pid, event.Cmdline, entry.Timestamp)
				eventChan <- event
			}
		}
	}

	return nil
}

func (m *OOMMonitor) refreshProcessCache() {
	ticker := time.NewTicker(m.refreshInterval)
	defer ticker.Stop()

	for range ticker.C {
		if err := m.processCache.Refresh(); err != nil {
			logger.Error("Failed to refresh process cache: %v", err)
		}
	}
}

func (m *OOMMonitor) createOOMEvent(pid int, timestamp uint64) OOMEventData {
	logger.Debug("Creating OOM event for PID %d", pid)
	cmdline := m.processCache.GetCommandLine(pid)
	if cmdline == "" {
		cmdline = fmt.Sprintf("<unknown process %d>", pid)
		logger.Debug("Process not found in cache, using fallback name: %s", cmdline)
	}

	hostname, _ := os.Hostname()

	// Convert kernel timestamp (microseconds since boot) to Unix epoch time (milliseconds)
	eventTime := m.bootTime.Add(time.Duration(timestamp) * time.Microsecond)
	eventTimeMillis := eventTime.UnixNano() / int64(time.Millisecond)

	event := OOMEventData{
		Cmdline:  cmdline,
		PID:      strconv.Itoa(pid),
		Hostname: hostname,
		Kernel:   getKernelVersion(),
		Time:     eventTimeMillis,
	}

	logger.Debug("Created OOM event: %+v (kernel timestamp: %d, converted time: %s)",
		event, timestamp, eventTime.Format("2006-01-02 15:04:05"))
	return event
}

func getBootTime() (time.Time, error) {
	// Read /proc/stat to get boot time
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, err
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "btime ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				bootTimeUnix, err := strconv.ParseInt(fields[1], 10, 64)
				if err != nil {
					return time.Time{}, err
				}
				return time.Unix(bootTimeUnix, 0), nil
			}
		}
	}

	return time.Time{}, fmt.Errorf("btime not found in /proc/stat")
}

func getKernelVersion() string {
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return "unknown"
	}
	fields := strings.Fields(string(data))
	if len(fields) >= 3 {
		return fields[2]
	}
	return "unknown"
}

type OOMEventData struct {
	Cmdline  string
	PID      string
	Hostname string
	Kernel   string
	Time     int64
}
