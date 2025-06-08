package notifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type SlackNotifier struct {
	WebhookURL string
	Channel    string
	client     *http.Client
}

type SlackPayload struct {
	Channel   string `json:"channel"`
	Text      string `json:"text"`
	Username  string `json:"username"`
	IconEmoji string `json:"icon_emoji"`
}

type OOMEvent struct {
	Cmdline  string `json:"cmdline"`
	PID      string `json:"pid"`
	Hostname string `json:"hostname"`
	Kernel   string `json:"kernel"`
	Time     int64  `json:"time"`
}

func NewSlackNotifier(webhookURL, channel string) *SlackNotifier {
	return &SlackNotifier{
		WebhookURL: webhookURL,
		Channel:    channel,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (s *SlackNotifier) Notify(event OOMEvent) error {
	message := fmt.Sprintf("OOM event detected!\nCommand: %s\nPID: %s\nHostname: %s\nKernel: %s",
		event.Cmdline, event.PID, event.Hostname, event.Kernel)

	payload := SlackPayload{
		Channel:   s.Channel,
		Text:      message,
		Username:  "oom-notifier",
		IconEmoji: ":firecracker:",
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal slack payload: %v", err)
	}

	req, err := http.NewRequest("POST", s.WebhookURL, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send slack notification: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack API returned non-200 status: %d", resp.StatusCode)
	}

	return nil
}