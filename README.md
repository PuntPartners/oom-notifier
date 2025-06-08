# OOM Notifier (Go Version)

A lightweight daemon to monitor Linux OOM (Out of Memory) killer events and send notifications to Slack. This is a Go implementation of the original Rust-based oom-notifier, simplified to support only Slack notifications.

## Features

- Monitors `/dev/kmsg` for OOM killer events
- Captures full command line of killed processes
- Sends real-time notifications to Slack
- Lightweight and efficient with minimal dependencies

## Prerequisites

- Linux system with access to `/dev/kmsg` (requires root/privileged access)
- Go 1.21 or higher (for building from source)
- Slack webhook URL

## Building from Source

```bash
cd go/
go mod download
go build -o oom-notifier ./cmd/oom-notifier
```

## Running with Docker

Build the Docker image:
```bash
cd go/
docker build -t oom-notifier-go .
```

Run the container:
```bash
docker run --privileged \
  -v /proc:/proc:ro \
  -v /dev/kmsg:/dev/kmsg:ro \
  oom-notifier-go \
  --slack-webhook "https://hooks.slack.com/services/YOUR/WEBHOOK/URL" \
  --slack-channel "#alerts"
```

## Usage

The daemon requires root privileges to access `/dev/kmsg`:

```bash
sudo ./oom-notifier \
  --slack-webhook "https://hooks.slack.com/services/YOUR/WEBHOOK/URL" \
  --slack-channel "#oom-notifications"
```

### Command Line Options

- `--slack-webhook`: Slack webhook URL (required)
- `--slack-channel`: Slack channel to send notifications (default: "#alerts")
- `--process-refresh`: Process cache refresh interval in seconds (default: 5)
- `--kernel-log-refresh`: Kernel log check interval in seconds (default: 10)

### Environment Variables

- `LOGGING_LEVEL`: Set logging verbosity (default: "info")

## Kubernetes Deployment

To run as a DaemonSet on Kubernetes:

1. Create a ConfigMap with your Slack webhook:
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: oom-notifier-config
data:
  slack-webhook: "https://hooks.slack.com/services/YOUR/WEBHOOK/URL"
  slack-channel: "#oom-alerts"
```

2. Deploy the DaemonSet (see `k8s/daemonset.yaml` in the parent directory for reference)

## Architecture

The Go implementation maintains the core architecture of the original Rust version:

1. **Process Cache**: Continuously refreshes a cache of running processes and their command lines
2. **Kernel Log Monitor**: Monitors `/dev/kmsg` for OOM killer messages
3. **Event Processing**: When an OOM event is detected, retrieves the full command line from the cache
4. **Slack Notification**: Sends formatted notifications to the configured Slack channel

## Differences from Rust Version

This Go implementation:
- Supports only Slack notifications (removed Syslog, Elasticsearch, and Kafka)
- Simplified codebase for easier maintenance
- Uses standard Go libraries and minimal dependencies

## License

Same as the original project.