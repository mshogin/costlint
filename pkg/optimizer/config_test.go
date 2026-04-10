package optimizer_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mshogin/costlint/pkg/optimizer"
)

func TestLoadConfig_NotFound(t *testing.T) {
	cfg, err := optimizer.LoadConfig("/nonexistent/.costlint.yaml")
	if err != nil {
		t.Errorf("LoadConfig for missing file should not error, got: %v", err)
	}
	if cfg.Targets.TotalMonthlyCost != "" {
		t.Error("missing file should return empty config")
	}
}

func TestLoadConfig_ValidYAML(t *testing.T) {
	content := `
optimize:
  targets:
    total_monthly_cost: "<= $100"
    max_per_request_cost: "<= $0.05"
  constraints:
    preserve_quality: true
    preserve_latency_p95: "<= 2s"
  workflows:
    - name: reports/daily-summary
      model: sonnet
      calls_per_month: 3000
      avg_input_tokens: 500
      avg_output_tokens: 200
      repeat_rate: 0.3
    - name: chat/inference
      model: opus
      calls_per_month: 1000
      avg_input_tokens: 2000
      avg_output_tokens: 500
      batch_size: 5
`
	dir := t.TempDir()
	path := filepath.Join(dir, ".costlint.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	cfg, err := optimizer.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}

	if cfg.Targets.TotalMonthlyCost != "<= $100" {
		t.Errorf("TotalMonthlyCost = %q, want %q", cfg.Targets.TotalMonthlyCost, "<= $100")
	}
	if cfg.Targets.MaxPerRequestCost != "<= $0.05" {
		t.Errorf("MaxPerRequestCost = %q, want %q", cfg.Targets.MaxPerRequestCost, "<= $0.05")
	}
	if !cfg.Constraints.PreserveQuality {
		t.Error("preserve_quality should be true")
	}
	if len(cfg.Workflows) != 2 {
		t.Errorf("expected 2 workflows, got %d", len(cfg.Workflows))
	}
	if cfg.Workflows[0].Name != "reports/daily-summary" {
		t.Errorf("first workflow name = %q", cfg.Workflows[0].Name)
	}
	if cfg.Workflows[1].BatchSize != 5 {
		t.Errorf("second workflow batch_size = %d, want 5", cfg.Workflows[1].BatchSize)
	}
}

func TestParseTargets_Valid(t *testing.T) {
	cfg := optimizer.Config{}
	cfg.Targets.TotalMonthlyCost = "<= $100"
	cfg.Targets.MaxPerRequestCost = "<= $0.05"

	targets, err := optimizer.ParseTargets(cfg)
	if err != nil {
		t.Fatalf("ParseTargets error: %v", err)
	}
	if targets.TotalMonthlyCost == nil {
		t.Error("TotalMonthlyCost should not be nil")
	}
	if targets.MaxPerRequestCost == nil {
		t.Error("MaxPerRequestCost should not be nil")
	}
	if targets.TotalMonthlyCost.Value != 100 {
		t.Errorf("TotalMonthlyCost.Value = %v, want 100", targets.TotalMonthlyCost.Value)
	}
	if targets.MaxPerRequestCost.Value != 0.05 {
		t.Errorf("MaxPerRequestCost.Value = %v, want 0.05", targets.MaxPerRequestCost.Value)
	}
}

func TestParseTargets_Empty(t *testing.T) {
	targets, err := optimizer.ParseTargets(optimizer.Config{})
	if err != nil {
		t.Fatalf("ParseTargets error: %v", err)
	}
	if targets.TotalMonthlyCost != nil {
		t.Error("empty config should give nil TotalMonthlyCost")
	}
	if targets.MaxPerRequestCost != nil {
		t.Error("empty config should give nil MaxPerRequestCost")
	}
}

func TestParseTargets_InvalidConstraint(t *testing.T) {
	cfg := optimizer.Config{}
	cfg.Targets.TotalMonthlyCost = "not-a-constraint"

	_, err := optimizer.ParseTargets(cfg)
	if err == nil {
		t.Error("expected error for invalid constraint")
	}
}
