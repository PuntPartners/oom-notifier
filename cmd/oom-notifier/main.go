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
	slackWebhook    string
	slackChannel    string
	processRefresh  int
	kernelLogRefresh int
)

func init() {
	flag.StringVar(&slackWebhook, "slack-webhook", "", "Slack webhook URL")
	flag.StringVar(&slackChannel, "slack-channel", "#alerts", "Slack channel to send notifications")
	flag.IntVar(&processRefresh, "process-refresh", 5, "Process cache refresh interval in seconds")
	flag.IntVar(&kernelLogRefresh, "kernel-log-refresh", 10, "Kernel log check interval in seconds")
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
	log.Printf("Starting oom-notifier with log level: %s", logLevel)

	// Create Slack notifier
	slackNotifier := notifier.NewSlackNotifier(slackWebhook, slackChannel)

	// Create OOM monitor
	oomMonitor, err := monitor.NewOOMMonitor(
		time.Duration(kernelLogRefresh)*time.Second,
		time.Duration(processRefresh)*time.Second,
	)
	if err != nil {
		log.Fatalf("Failed to create OOM monitor: %v", err)
	}
	defer oomMonitor.Close()

	// Create event channel
	eventChan := make(chan monitor.OOMEventData, 10)

	// Start OOM monitor in a goroutine
	go func() {
		if err := oomMonitor.Start(eventChan); err != nil {
			log.Fatalf("OOM monitor error: %v", err)
		}
	}()

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Main event loop
	log.Println("oom-notifier started successfully")
	for {
		select {
		case event := <-eventChan:
			log.Printf("OOM event detected: PID=%s, Command=%s", event.PID, event.Cmdline)
			
			// Convert to notifier event format
			notifierEvent := notifier.OOMEvent{
				Cmdline:  event.Cmdline,
				PID:      event.PID,
				Hostname: event.Hostname,
				Kernel:   event.Kernel,
				Time:     event.Time,
			}

			// Send notification
			if err := slackNotifier.Notify(notifierEvent); err != nil {
				log.Printf("Failed to send Slack notification: %v", err)
			} else {
				log.Printf("Slack notification sent successfully")
			}

		case sig := <-sigChan:
			log.Printf("Received signal %v, shutting down...", sig)
			return
		}
	}
}