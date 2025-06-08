# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Common Development Commands

### Building
```bash
go build -o oom-notifier ./cmd/oom-notifier
```

### Running Tests
```bash
go test ./...
```

### Running with Docker
```bash
# Build the Docker image
docker build -t oom-notifier-go .

# Run the container (requires privileged access for /dev/kmsg)
docker run --privileged \
  -v /proc:/proc:ro \
  -v /dev/kmsg:/dev/kmsg:ro \
  oom-notifier-go \
  --slack-webhook "https://hooks.slack.com/services/YOUR/WEBHOOK/URL" \
  --slack-channel "#alerts"
```

### Development Run
```bash
# Run locally (requires root privileges)
sudo ./oom-notifier \
  --slack-webhook "https://hooks.slack.com/services/YOUR/WEBHOOK/URL" \
  --slack-channel "#oom-notifications"
```

## Architecture Overview

This is a Go implementation of an OOM (Out of Memory) killer monitor that sends notifications to Slack. The codebase follows an event-driven architecture with clear separation of concerns.

### Core Components

1. **monitor.OOMMonitor** (`internal/monitor/process.go`): 
   - Orchestrates the monitoring process
   - Combines KmsgReader and ProcessCache
   - Emits OOMEventData through channels

2. **monitor.KmsgReader** (`internal/monitor/kmsg.go`):
   - Reads and parses `/dev/kmsg` for OOM killer messages
   - Uses regex patterns to detect OOM events and extract PIDs
   - Handles kernel message format parsing

3. **monitor.ProcessCache** (`internal/monitor/process.go`):
   - LRU cache for process command lines indexed by PID
   - Refreshes periodically to maintain current process information
   - Size based on system's `pid_max` value

4. **notifier.SlackNotifier** (`internal/notifier/slack.go`):
   - Implements Slack webhook notifications
   - Formats OOM events into readable Slack messages
   - Handles HTTP communication with Slack API

### Event Flow

1. OOMMonitor runs in a goroutine, continuously monitoring kernel messages
2. When an OOM event is detected:
   - KmsgReader extracts the PID from the kernel message
   - ProcessCache provides the full command line for the killed process
   - OOMEventData is created and sent through the event channel
3. Main loop receives events and forwards them to SlackNotifier
4. SlackNotifier formats and sends the notification to Slack

### Key Design Patterns

- **Channel-based Communication**: Events flow through channels for non-blocking operation
- **Graceful Shutdown**: Handles SIGINT/SIGTERM signals properly
- **Error Resilience**: Failures in one component don't crash the entire application
- **Configurable Intervals**: Process refresh and kernel log check intervals are configurable via CLI flags

### CLI Flags

- `--slack-webhook` (required): Slack webhook URL
- `--slack-channel`: Slack channel to send notifications (default: "#alerts")
- `--process-refresh`: Process cache refresh interval in seconds (default: 5)
- `--kernel-log-refresh`: Kernel log check interval in seconds (default: 10)

### Important Notes

- The application requires root/privileged access to read `/dev/kmsg`
- This Go version only supports Slack notifications (simplified from the original Rust version)
- Uses minimal dependencies: only `golang-lru/v2` for caching and `spf13/pflag` for CLI parsing