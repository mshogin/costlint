// Package webhook provides anomaly alerting via HTTP POST notifications.
// When agent spend exceeds the configured threshold (default: 2x daily average),
// a JSON payload is sent to the configured webhook URL so another agent can
// decide whether to stop, switch model, or alert the owner.
package webhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// AnomalyPayload is the body sent to the webhook endpoint.
type AnomalyPayload struct {
	Event       string  `json:"event"`
	Timestamp   string  `json:"timestamp"`
	Agent       string  `json:"agent,omitempty"`
	TodayCost   float64 `json:"today_cost_usd"`
	BaselineCost float64 `json:"baseline_cost_usd"`
	MultiplierX float64 `json:"multiplier_x"`
	Reason      string  `json:"reason"`
}

// Config holds webhook configuration.
type Config struct {
	// URL is the HTTP endpoint to POST anomaly payloads to.
	// Reads COSTLINT_WEBHOOK_URL env var when empty.
	URL string

	// ThresholdMultiplier is the factor above baseline that triggers the webhook.
	// Defaults to 2.0 (2x daily average).
	ThresholdMultiplier float64

	// TimeoutSeconds is the HTTP client timeout. Defaults to 10.
	TimeoutSeconds int

	// Agent is an optional identifier for the sending agent (included in payload).
	Agent string
}

// DefaultConfig returns a Config populated from environment variables.
// COSTLINT_WEBHOOK_URL   - webhook endpoint (required for actual delivery)
// COSTLINT_WEBHOOK_AGENT - optional agent identifier
func DefaultConfig() Config {
	return Config{
		URL:                 os.Getenv("COSTLINT_WEBHOOK_URL"),
		Agent:               os.Getenv("COSTLINT_WEBHOOK_AGENT"),
		ThresholdMultiplier: 2.0,
		TimeoutSeconds:      10,
	}
}

// Notifier sends anomaly webhook notifications.
type Notifier struct {
	cfg    Config
	client *http.Client
}

// NewNotifier creates a Notifier with the given config.
// ThresholdMultiplier and TimeoutSeconds are set to defaults when zero.
func NewNotifier(cfg Config) *Notifier {
	if cfg.ThresholdMultiplier <= 0 {
		cfg.ThresholdMultiplier = 2.0
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 10
	}
	return &Notifier{
		cfg: cfg,
		client: &http.Client{
			Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second,
		},
	}
}

// CheckAndNotify evaluates whether todayCost exceeds the threshold relative to
// baselineCost, and posts an AnomalyPayload when it does.
//
// Returns (true, nil) when the webhook was triggered successfully.
// Returns (false, nil) when spend is within normal bounds.
// Returns (true, err) when the anomaly was detected but the HTTP call failed.
func (n *Notifier) CheckAndNotify(todayCost, baselineCost float64) (triggered bool, err error) {
	if baselineCost <= 0 {
		// No baseline available; skip detection.
		return false, nil
	}

	multiplier := todayCost / baselineCost
	if multiplier < n.cfg.ThresholdMultiplier {
		return false, nil
	}

	payload := AnomalyPayload{
		Event:       "cost_anomaly",
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		Agent:       n.cfg.Agent,
		TodayCost:   todayCost,
		BaselineCost: baselineCost,
		MultiplierX: multiplier,
		Reason: fmt.Sprintf(
			"today $%.4f exceeds %.1fx baseline $%.4f",
			todayCost, n.cfg.ThresholdMultiplier, baselineCost,
		),
	}

	if n.cfg.URL == "" {
		// Webhook not configured; return triggered=true so callers know an
		// anomaly was detected, but don't treat the missing URL as an error.
		return true, nil
	}

	body, merr := json.Marshal(payload)
	if merr != nil {
		return true, fmt.Errorf("marshal webhook payload: %w", merr)
	}

	resp, herr := n.client.Post(n.cfg.URL, "application/json", bytes.NewReader(body))
	if herr != nil {
		return true, fmt.Errorf("post webhook: %w", herr)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return true, fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}

	return true, nil
}
