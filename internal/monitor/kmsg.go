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
	file           *os.File
	scanner        *bufio.Scanner
	oomPattern     *regexp.Regexp
	pidPattern     *regexp.Regexp
	lastTimestamp  uint64
}

type KmsgEntry struct {
	Priority    int
	SequenceNum uint64
	Timestamp   uint64
	Message     string
}

func NewKmsgReader() (*KmsgReader, error) {
	file, err := os.Open("/dev/kmsg")
	if err != nil {
		return nil, fmt.Errorf("failed to open /dev/kmsg: %v", err)
	}

	reader := &KmsgReader{
		file:       file,
		scanner:    bufio.NewScanner(file),
		oomPattern: regexp.MustCompile(`(?i)out of memory:`),
		pidPattern: regexp.MustCompile(`\bkilled process (\d+)\b`),
	}

	// Skip old messages by seeking to the end
	if _, err := file.Seek(0, 2); err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to seek to end of kmsg: %v", err)
	}

	return reader, nil
}

func (k *KmsgReader) Close() error {
	return k.file.Close()
}

func (k *KmsgReader) ReadEntries() ([]KmsgEntry, error) {
	var entries []KmsgEntry

	for k.scanner.Scan() {
		line := k.scanner.Text()
		entry, err := k.parseKmsgLine(line)
		if err != nil {
			log.Printf("Failed to parse kmsg line: %v", err)
			continue
		}

		if entry.Timestamp > k.lastTimestamp {
			entries = append(entries, *entry)
			k.lastTimestamp = entry.Timestamp
		}
	}

	if err := k.scanner.Err(); err != nil {
		return entries, fmt.Errorf("error reading kmsg: %v", err)
	}

	return entries, nil
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
	return k.oomPattern.MatchString(entry.Message)
}

func (k *KmsgReader) ExtractPID(message string) (int, error) {
	matches := k.pidPattern.FindStringSubmatch(strings.ToLower(message))
	if len(matches) < 2 {
		return 0, fmt.Errorf("no PID found in OOM message")
	}

	pid, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, fmt.Errorf("failed to parse PID: %v", err)
	}

	return pid, nil
}

type OOMMonitor struct {
	kmsgReader       *KmsgReader
	processCache     *ProcessCache
	checkInterval    time.Duration
	refreshInterval  time.Duration
}

func NewOOMMonitor(checkInterval, refreshInterval time.Duration) (*OOMMonitor, error) {
	kmsgReader, err := NewKmsgReader()
	if err != nil {
		return nil, err
	}

	processCache, err := NewProcessCache()
	if err != nil {
		kmsgReader.Close()
		return nil, err
	}

	return &OOMMonitor{
		kmsgReader:      kmsgReader,
		processCache:    processCache,
		checkInterval:   checkInterval,
		refreshInterval: refreshInterval,
	}, nil
}

func (m *OOMMonitor) Close() error {
	return m.kmsgReader.Close()
}

func (m *OOMMonitor) Start(eventChan chan<- OOMEventData) error {
	// Start process cache refresh routine
	go m.refreshProcessCache()

	// Monitor kernel messages
	ticker := time.NewTicker(m.checkInterval)
	defer ticker.Stop()

	for range ticker.C {
		entries, err := m.kmsgReader.ReadEntries()
		if err != nil {
			log.Printf("Error reading kmsg entries: %v", err)
			continue
		}

		for _, entry := range entries {
			if m.kmsgReader.IsOOMMessage(entry) {
				pid, err := m.kmsgReader.ExtractPID(entry.Message)
				if err != nil {
					log.Printf("Failed to extract PID from OOM message: %v", err)
					continue
				}

				event := m.createOOMEvent(pid, entry.Timestamp)
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
	cmdline := m.processCache.GetCommandLine(pid)
	if cmdline == "" {
		cmdline = fmt.Sprintf("<unknown process %d>", pid)
	}

	hostname, _ := os.Hostname()
	
	return OOMEventData{
		Cmdline:  cmdline,
		PID:      strconv.Itoa(pid),
		Hostname: hostname,
		Kernel:   getKernelVersion(),
		Time:     int64(timestamp / 1000), // Convert microseconds to milliseconds
	}
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