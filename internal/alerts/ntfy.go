package alerts

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// NtfyChannel delivers alerts as ntfy push notifications.
// See https://docs.ntfy.sh/publish/.
type NtfyChannel struct {
	server string
	topic  string
	client *http.Client
}

// NewNtfyChannel returns a channel posting to <server>/<topic>.
func NewNtfyChannel(server, topic string) *NtfyChannel {
	return &NtfyChannel{
		server: strings.TrimRight(server, "/"),
		topic:  topic,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *NtfyChannel) Name() string { return "ntfy" }

func (c *NtfyChannel) Send(ctx context.Context, a Alert) error {
	url := c.server + "/" + c.topic
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(a.Title))
	if err != nil {
		return err
	}
	req.Header.Set("Title", encodeHeader(a.Headline()))
	if a.URL != "" {
		req.Header.Set("Click", a.URL)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ntfy returned %d", resp.StatusCode)
	}
	return nil
}

// encodeHeader returns s unchanged if it's pure ASCII, otherwise RFC 2047
// encodes it (e.g. "=?UTF-8?B?...?="), which ntfy requires for header values
// like Title that contain non-ASCII characters such as "·".
// See https://docs.ntfy.sh/publish/#emojis-tags.
func encodeHeader(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(s)) + "?="
		}
	}
	return s
}

func reasonText(reason string) string {
	switch reason {
	case "review_requested":
		return "Review requested"
	case "assigned":
		return "Issue assigned"
	case "mention":
		return "You were mentioned"
	case "":
		return "GitHub notification"
	default:
		return "GitHub: " + reason
	}
}
