package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/oom-notifier/go/internal/monitor"
	"github.com/oom-notifier/go/internal/notifier"
	flag "github.com/spf13/pflag"
)

var (
	slackWebhook     string
	slackChannel     string
	processRefresh   int
	kernelLogRefresh int
	procDir          string
)

func init() {
	flag.StringVar(&slackWebhook, "slack-webhook", "", "Slack webhook URL")
	flag.StringVar(&slackChannel, "slack-channel", "#alerts", "Slack channel to send notifications")
	flag.IntVar(&processRefresh, "process-refresh", 5, "Process cache refresh interval in seconds")
	flag.IntVar(&kernelLogRefresh, "kernel-log-refresh", 10, "Kernel log check interval in seconds")
	flag.StringVar(&procDir, "proc-dir", "/proc", "Path to proc directory")
}

func main() {
	flag.Parse()

	// Validate required parameters
	if slackWebhook == "" {
		fmt.Fprintf(os.Stderr, "Error: --slack-webhook is required\n")
		flag.Usage()
		os.Exit(1)
	}

	// Set up logging
	logLevel := os.Getenv("LOGGING_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}
	log.Printf("[INFO] Starting oom-notifier with log level: %s", logLevel)
	log.Printf("[DEBUG] Configuration: slack-webhook=%s, slack-channel=%s, process-refresh=%ds, kernel-log-refresh=%ds, proc-dir=%s",
		slackWebhook, slackChannel, processRefresh, kernelLogRefresh, procDir)

	// Create Slack notifier
	log.Printf("[DEBUG] Creating Slack notifier")
	slackNotifier := notifier.NewSlackNotifier(slackWebhook, slackChannel)

	// Create OOM monitor
	log.Printf("[DEBUG] Creating OOM monitor")
	oomMonitor, err := monitor.NewOOMMonitor(
		procDir,
		time.Duration(kernelLogRefresh)*time.Second,
		time.Duration(processRefresh)*time.Second,
	)
	if err != nil {
		log.Fatalf("[ERROR] Failed to create OOM monitor: %v", err)
	}
	defer oomMonitor.Close()
	log.Printf("[DEBUG] OOM monitor created successfully")

	// Create event channel
	log.Printf("[DEBUG] Creating event channel with buffer size 10")
	eventChan := make(chan monitor.OOMEventData, 10)

	// Start OOM monitor in a goroutine
	log.Printf("[DEBUG] Starting OOM monitor goroutine")
	go func() {
		if err := oomMonitor.Start(eventChan); err != nil {
			log.Fatalf("[ERROR] OOM monitor error: %v", err)
		}
	}()

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Main event loop
	log.Printf("[INFO] oom-notifier started successfully, entering main event loop")
	for {
		select {
		case event := <-eventChan:
			log.Printf("[INFO] OOM event received in main loop: PID=%s, Command=%s", event.PID, event.Cmdline)

			// Convert to notifier event format
			notifierEvent := notifier.OOMEvent{
				Cmdline:  event.Cmdline,
				PID:      event.PID,
				Hostname: event.Hostname,
				Kernel:   event.Kernel,
				Time:     event.Time,
			}

			log.Printf("[DEBUG] Sending Slack notification")
			// Send notification
			if err := slackNotifier.Notify(notifierEvent); err != nil {
				log.Printf("[ERROR] Failed to send Slack notification: %v", err)
			} else {
				log.Printf("[INFO] Slack notification sent successfully")
			}

		case sig := <-sigChan:
			log.Printf("[INFO] Received signal %v, shutting down...", sig)
			return
		}
	}
}
