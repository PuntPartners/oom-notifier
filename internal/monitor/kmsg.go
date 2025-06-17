package monitor

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
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
	log.Printf("[DEBUG] Opening /dev/kmsg for reading")
	file, err := os.Open("/dev/kmsg")
	if err != nil {
		return nil, fmt.Errorf("failed to open /dev/kmsg: %v", err)
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

	log.Printf("[DEBUG] KmsgReader initialized successfully")
	return reader, nil
}

func (k *KmsgReader) Close() error {
	close(k.done)
	return k.file.Close()
}

func (k *KmsgReader) readLoop() {
	log.Printf("[DEBUG] Starting kmsg read loop")
	for {
		select {
		case <-k.done:
			log.Printf("[DEBUG] Stopping kmsg read loop")
			return
		default:
			if k.scanner.Scan() {
				line := k.scanner.Text()
				entry, err := k.parseKmsgLine(line)
				if err != nil {
					log.Printf("[DEBUG] Failed to parse kmsg line: %v", err)
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
				log.Printf("[DEBUG] Retrieved %d entries from buffer", len(entries))
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
		log.Printf("[DEBUG] Detected OOM message: %s", entry.Message)
	}
	return isOOM
}

func (k *KmsgReader) ExtractPID(message string) (int, error) {
	matches := k.pidPattern.FindStringSubmatch(strings.ToLower(message))
	if len(matches) < 2 {
		log.Printf("[DEBUG] No PID pattern found in message: %s", message)
		return 0, fmt.Errorf("no PID found in OOM message")
	}

	pid, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, fmt.Errorf("failed to parse PID: %v", err)
	}

	log.Printf("[DEBUG] Extracted PID %d from OOM message", pid)
	return pid, nil
}

type OOMMonitor struct {
	kmsgReader       *KmsgReader
	processCache     *ProcessCache
	checkInterval    time.Duration
	refreshInterval  time.Duration
	startupTimestamp uint64
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

	// Get current time in microseconds (same as kmsg timestamp format)
	startupTimestamp := uint64(time.Now().UnixNano() / 1000)
	log.Printf("[DEBUG] OOMMonitor startup timestamp: %d", startupTimestamp)

	return &OOMMonitor{
		kmsgReader:       kmsgReader,
		processCache:     processCache,
		checkInterval:    checkInterval,
		refreshInterval:  refreshInterval,
		startupTimestamp: startupTimestamp,
	}, nil
}

func (m *OOMMonitor) Close() error {
	return m.kmsgReader.Close()
}

func (m *OOMMonitor) Start(eventChan chan<- OOMEventData) error {
	log.Printf("[DEBUG] Starting OOM monitor with check interval: %v, refresh interval: %v", m.checkInterval, m.refreshInterval)

	// Start process cache refresh routine
	go m.refreshProcessCache()

	// Monitor kernel messages
	ticker := time.NewTicker(m.checkInterval)
	defer ticker.Stop()

	log.Printf("[DEBUG] Starting kernel message monitoring loop")
	for range ticker.C {
		log.Printf("[DEBUG] Checking for new kernel messages")
		entries, err := m.kmsgReader.ReadEntries()
		if err != nil {
			log.Printf("[ERROR] Error reading kmsg entries: %v", err)
			continue
		}

		for _, entry := range entries {
			if m.kmsgReader.IsOOMMessage(entry) {
				log.Printf("[INFO] OOM message detected! Processing...")

				// Filter out events that occurred before process startup
				if entry.Timestamp < m.startupTimestamp {
					log.Printf("[DEBUG] Skipping OOM event from before startup: timestamp=%d, startup=%d",
						entry.Timestamp, m.startupTimestamp)
					continue
				}

				pid, err := m.kmsgReader.ExtractPID(entry.Message)
				if err != nil {
					log.Printf("[ERROR] Failed to extract PID from OOM message: %v", err)
					continue
				}

				event := m.createOOMEvent(pid, entry.Timestamp)
				log.Printf("[INFO] Sending OOM event: PID=%d, Process=%s, Timestamp=%d",
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
			log.Printf("Failed to refresh process cache: %v", err)
		}
	}
}

func (m *OOMMonitor) createOOMEvent(pid int, timestamp uint64) OOMEventData {
	log.Printf("[DEBUG] Creating OOM event for PID %d", pid)
	cmdline := m.processCache.GetCommandLine(pid)
	if cmdline == "" {
		cmdline = fmt.Sprintf("<unknown process %d>", pid)
		log.Printf("[DEBUG] Process not found in cache, using fallback name: %s", cmdline)
	}

	hostname, _ := os.Hostname()

	event := OOMEventData{
		Cmdline:  cmdline,
		PID:      strconv.Itoa(pid),
		Hostname: hostname,
		Kernel:   getKernelVersion(),
		Time:     int64(timestamp / 1000), // Convert microseconds to milliseconds
	}

	log.Printf("[DEBUG] Created OOM event: %+v", event)
	return event
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
