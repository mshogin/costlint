package webhook_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mshogin/costlint/pkg/webhook"
)

func TestCheckAndNotify_NoAnomaly(t *testing.T) {
	n := webhook.NewNotifier(webhook.Config{
		ThresholdMultiplier: 2.0,
	})
	triggered, err := n.CheckAndNotify(1.0, 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if triggered {
		t.Fatal("expected no trigger when cost equals baseline")
	}
}

func TestCheckAndNotify_BelowThreshold(t *testing.T) {
	n := webhook.NewNotifier(webhook.Config{
		ThresholdMultiplier: 2.0,
	})
	triggered, err := n.CheckAndNotify(1.5, 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if triggered {
		t.Fatal("expected no trigger when multiplier is below 2x")
	}
}

func TestCheckAndNotify_ExactThreshold(t *testing.T) {
	n := webhook.NewNotifier(webhook.Config{
		ThresholdMultiplier: 2.0,
	})
	// 2.0 >= 2.0 -> should trigger
	triggered, err := n.CheckAndNotify(2.0, 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !triggered {
		t.Fatal("expected trigger at exactly 2x threshold")
	}
}

func TestCheckAndNotify_AboveThreshold(t *testing.T) {
	n := webhook.NewNotifier(webhook.Config{
		ThresholdMultiplier: 2.0,
	})
	triggered, err := n.CheckAndNotify(5.0, 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !triggered {
		t.Fatal("expected trigger when cost is 5x baseline")
	}
}

func TestCheckAndNotify_NoBaseline(t *testing.T) {
	n := webhook.NewNotifier(webhook.Config{
		ThresholdMultiplier: 2.0,
	})
	triggered, err := n.CheckAndNotify(100.0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if triggered {
		t.Fatal("expected no trigger when baseline is zero")
	}
}

func TestCheckAndNotify_NoURLConfigured(t *testing.T) {
	n := webhook.NewNotifier(webhook.Config{
		URL:                 "",
		ThresholdMultiplier: 2.0,
	})
	triggered, err := n.CheckAndNotify(5.0, 1.0)
	if err != nil {
		t.Fatalf("expected no error when URL is empty, got: %v", err)
	}
	if !triggered {
		t.Fatal("expected triggered=true even without URL (anomaly detected)")
	}
}

func TestCheckAndNotify_PostsPayload(t *testing.T) {
	var received webhook.AnomalyPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := webhook.NewNotifier(webhook.Config{
		URL:                 srv.URL,
		Agent:               "test-agent",
		ThresholdMultiplier: 2.0,
		TimeoutSeconds:      5,
	})

	triggered, err := n.CheckAndNotify(4.0, 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !triggered {
		t.Fatal("expected trigger")
	}

	if received.Event != "cost_anomaly" {
		t.Errorf("expected event=cost_anomaly, got %q", received.Event)
	}
	if received.Agent != "test-agent" {
		t.Errorf("expected agent=test-agent, got %q", received.Agent)
	}
	if received.TodayCost != 4.0 {
		t.Errorf("expected today_cost=4.0, got %f", received.TodayCost)
	}
	if received.BaselineCost != 1.0 {
		t.Errorf("expected baseline_cost=1.0, got %f", received.BaselineCost)
	}
	if received.MultiplierX != 4.0 {
		t.Errorf("expected multiplier_x=4.0, got %f", received.MultiplierX)
	}
	if received.Timestamp == "" {
		t.Error("expected non-empty timestamp")
	}
}

func TestCheckAndNotify_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	n := webhook.NewNotifier(webhook.Config{
		URL:                 srv.URL,
		ThresholdMultiplier: 2.0,
	})

	triggered, err := n.CheckAndNotify(5.0, 1.0)
	if !triggered {
		t.Fatal("expected triggered=true even on HTTP error")
	}
	if err == nil {
		t.Fatal("expected error on HTTP 500")
	}
}

func TestDefaultConfig_ReadsEnvVars(t *testing.T) {
	t.Setenv("COSTLINT_WEBHOOK_URL", "http://example.com/hook")
	t.Setenv("COSTLINT_WEBHOOK_AGENT", "my-agent")

	cfg := webhook.DefaultConfig()
	if cfg.URL != "http://example.com/hook" {
		t.Errorf("expected URL from env, got %q", cfg.URL)
	}
	if cfg.Agent != "my-agent" {
		t.Errorf("expected agent from env, got %q", cfg.Agent)
	}
	if cfg.ThresholdMultiplier != 2.0 {
		t.Errorf("expected default multiplier 2.0, got %f", cfg.ThresholdMultiplier)
	}
}
