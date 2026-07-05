package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// Send posts a short status message to WEBHOOK_URL. The payload carries both
// "content" (Discord) and "text" (Slack/Mattermost) keys so either webhook
// flavor renders it without configuration. Failures are logged, never fatal —
// a monitoring hiccup must not fail a publish run.
func Send(webhookURL, message string) {
	webhookURL = strings.TrimSpace(webhookURL)
	if webhookURL == "" {
		return
	}
	payload, err := json.Marshal(map[string]string{
		"content": message,
		"text":    message,
	})
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(payload))
	if err != nil {
		log.Warn().Err(err).Msg("webhook request build failed")
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Warn().Err(err).Msg("webhook delivery failed")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Warn().Int("status", resp.StatusCode).Msg("webhook rejected")
		return
	}
	log.Info().Msg("webhook notification sent")
}

// RunResult formats and sends the outcome of a command run.
func RunResult(webhookURL, cmd string, elapsed time.Duration, manifestVersion int, runErr error) {
	if strings.TrimSpace(webhookURL) == "" {
		return
	}
	var msg string
	if runErr != nil {
		msg = fmt.Sprintf("❌ vps-sync %s failed after %s: %v", cmd, elapsed.Round(time.Second), runErr)
	} else if manifestVersion > 0 {
		msg = fmt.Sprintf("✅ vps-sync %s completed in %s — published manifest v%d", cmd, elapsed.Round(time.Second), manifestVersion)
	} else {
		msg = fmt.Sprintf("✅ vps-sync %s completed in %s", cmd, elapsed.Round(time.Second))
	}
	Send(webhookURL, msg)
}
