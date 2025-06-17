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

type SlackField struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Short bool   `json:"short"`
}

type SlackAttachment struct {
	Color  string       `json:"color"`
	Title  string       `json:"title"`
	Fields []SlackField `json:"fields"`
}

type SlackPayload struct {
	Channel     string            `json:"channel"`
	Text        string            `json:"text"`
	Username    string            `json:"username"`
	IconEmoji   string            `json:"icon_emoji"`
	Attachments []SlackAttachment `json:"attachments,omitempty"`
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
	// Convert timestamp to IST timezone
	ist, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		ist = time.UTC // Fallback to UTC if IST loading fails
	}

	// Convert milliseconds to time and format in IST
	eventTime := time.Unix(0, event.Time*int64(time.Millisecond)).In(ist)
	timeStr := eventTime.Format("2006-01-02 15:04:05 IST")

	attachment := SlackAttachment{
		Color: "danger",
		Title: "🚨 Out of Memory (OOM) Event Detected",
		Fields: []SlackField{
			{
				Title: "Process Command",
				Value: event.Cmdline,
				Short: false,
			},
			{
				Title: "Process ID",
				Value: event.PID,
				Short: true,
			},
			{
				Title: "Hostname",
				Value: event.Hostname,
				Short: true,
			},
			{
				Title: "Kernel Version",
				Value: event.Kernel,
				Short: true,
			},
			{
				Title: "Time (IST)",
				Value: timeStr,
				Short: true,
			},
		},
	}

	payload := SlackPayload{
		Channel:     s.Channel,
		Text:        "OOM Killer Alert",
		Username:    "oom-notifier",
		IconEmoji:   ":firecracker:",
		Attachments: []SlackAttachment{attachment},
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
