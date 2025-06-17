package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/oom-notifier/go/internal/logger"
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
	debug            bool
)

func init() {
	flag.StringVar(&slackWebhook, "slack-webhook", "", "Slack webhook URL")
	flag.StringVar(&slackChannel, "slack-channel", "#alerts", "Slack channel to send notifications")
	flag.IntVar(&processRefresh, "process-refresh", 5, "Process cache refresh interval in seconds")
	flag.IntVar(&kernelLogRefresh, "kernel-log-refresh", 10, "Kernel log check interval in seconds")
	flag.StringVar(&procDir, "proc-dir", "/proc", "Path to proc directory")
	flag.BoolVar(&debug, "debug", false, "Enable debug logging")
}

func main() {
	flag.Parse()

	// Validate required parameters
	if slackWebhook == "" {
		fmt.Fprintf(os.Stderr, "Error: --slack-webhook is required\n")
		flag.Usage()
		os.Exit(1)
	}

	// Initialize logging
	logger.Init(debug)

	logger.Info("Starting oom-notifier")
	logger.Debug("Configuration: slack-webhook=%s, slack-channel=%s, process-refresh=%ds, kernel-log-refresh=%ds, proc-dir=%s, debug=%t",
		slackWebhook, slackChannel, processRefresh, kernelLogRefresh, procDir, debug)

	// Create Slack notifier
	logger.Debug("Creating Slack notifier")
	slackNotifier := notifier.NewSlackNotifier(slackWebhook, slackChannel)

	// Create OOM monitor
	logger.Debug("Creating OOM monitor")
	oomMonitor, err := monitor.NewOOMMonitor(
		procDir,
		time.Duration(kernelLogRefresh)*time.Second,
		time.Duration(processRefresh)*time.Second,
	)
	if err != nil {
		logger.Error("Failed to create OOM monitor: %v", err)
		os.Exit(1)
	}
	defer oomMonitor.Close()
	logger.Debug("OOM monitor created successfully")

	// Create event channel
	logger.Debug("Creating event channel with buffer size 10")
	eventChan := make(chan monitor.OOMEventData, 10)

	// Start OOM monitor in a goroutine
	logger.Debug("Starting OOM monitor goroutine")
	go func() {
		if err := oomMonitor.Start(eventChan); err != nil {
			logger.Error("OOM monitor error: %v", err)
			os.Exit(1)
		}
	}()

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Main event loop
	logger.Info("oom-notifier started successfully, entering main event loop")
	for {
		select {
		case event := <-eventChan:
			logger.Info("OOM event received: PID=%s, Command=%s", event.PID, event.Cmdline)

			// Convert to notifier event format
			notifierEvent := notifier.OOMEvent{
				Cmdline:  event.Cmdline,
				PID:      event.PID,
				Hostname: event.Hostname,
				Kernel:   event.Kernel,
				Time:     event.Time,
			}

			logger.Debug("Sending Slack notification")
			// Send notification
			if err := slackNotifier.Notify(notifierEvent); err != nil {
				logger.Error("Failed to send Slack notification: %v", err)
			} else {
				logger.Info("Slack notification sent successfully")
			}

		case sig := <-sigChan:
			logger.Info("Received signal %v, shutting down...", sig)
			return
		}
	}
}
