package monitor

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	lru "github.com/hashicorp/golang-lru/v2"
)

type ProcessInfo struct {
	PID     int
	Cmdline string
}

type ProcessCache struct {
	cache *lru.Cache[int, string]
	mu    sync.RWMutex
}

func NewProcessCache() (*ProcessCache, error) {
	// Get system's pid_max
	pidMax := getPIDMax()
	
	cache, err := lru.New[int, string](pidMax)
	if err != nil {
		return nil, fmt.Errorf("failed to create LRU cache: %v", err)
	}

	pc := &ProcessCache{
		cache: cache,
	}

	// Initial population
	if err := pc.Refresh(); err != nil {
		return nil, fmt.Errorf("failed to populate process cache: %v", err)
	}

	return pc, nil
}

func (pc *ProcessCache) Refresh() error {
	processes, err := getAllProcesses()
	if err != nil {
		return err
	}

	pc.mu.Lock()
	defer pc.mu.Unlock()

	for _, proc := range processes {
		pc.cache.Add(proc.PID, proc.Cmdline)
	}

	return nil
}

func (pc *ProcessCache) GetCommandLine(pid int) string {
	pc.mu.RLock()
	defer pc.mu.RUnlock()

	cmdline, found := pc.cache.Get(pid)
	if !found {
		return ""
	}
	return cmdline
}

func getAllProcesses() ([]ProcessInfo, error) {
	procDir := "/proc"
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read /proc: %v", err)
	}

	var processes []ProcessInfo

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Check if directory name is a PID
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue // Not a PID directory
		}

		cmdline := getProcessCmdline(pid)
		if cmdline != "" {
			processes = append(processes, ProcessInfo{
				PID:     pid,
				Cmdline: cmdline,
			})
		}
	}

	return processes, nil
}

func getProcessCmdline(pid int) string {
	cmdlinePath := filepath.Join("/proc", strconv.Itoa(pid), "cmdline")
	data, err := ioutil.ReadFile(cmdlinePath)
	if err != nil {
		return ""
	}

	// cmdline uses null bytes as separators
	cmdline := strings.ReplaceAll(string(data), "\x00", " ")
	cmdline = strings.TrimSpace(cmdline)
	
	if cmdline == "" {
		// Try to get process name from comm file
		commPath := filepath.Join("/proc", strconv.Itoa(pid), "comm")
		commData, err := ioutil.ReadFile(commPath)
		if err == nil {
			cmdline = fmt.Sprintf("[%s]", strings.TrimSpace(string(commData)))
		}
	}

	return cmdline
}

func getPIDMax() int {
	data, err := ioutil.ReadFile("/proc/sys/kernel/pid_max")
	if err != nil {
		return 32768 // Default value
	}

	pidMax, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 32768
	}

	return pidMax
}