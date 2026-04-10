package optimizer

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the yaml schema for the 'optimize' section of .costlint.yaml.
type Config struct {
	Targets struct {
		TotalMonthlyCost  string `yaml:"total_monthly_cost"`
		MaxPerRequestCost string `yaml:"max_per_request_cost"`
	} `yaml:"targets"`
	Constraints struct {
		PreserveQuality      bool   `yaml:"preserve_quality"`
		PreserveLatencyP95   string `yaml:"preserve_latency_p95"`
	} `yaml:"constraints"`
	Workflows []WorkflowConfig `yaml:"workflows"`
}

// WorkflowConfig is the yaml representation of a single workflow entry.
type WorkflowConfig struct {
	Name            string  `yaml:"name"`
	Model           string  `yaml:"model"`
	CallsPerMonth   int     `yaml:"calls_per_month"`
	AvgInputTokens  int     `yaml:"avg_input_tokens"`
	AvgOutputTokens int     `yaml:"avg_output_tokens"`
	RepeatRate      float64 `yaml:"repeat_rate"`
	BatchSize       int     `yaml:"batch_size"`
}

// fullConfig is a minimal envelope for unmarshalling only the optimize section.
type fullConfig struct {
	Optimize Config `yaml:"optimize"`
}

// LoadConfig reads and parses the optimize section from a .costlint.yaml file.
// Returns an empty Config (no targets, no workflows) if the file does not exist
// or has no optimize section — the command will then print a helpful message.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec // user-provided path
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("cannot read %s: %w", path, err)
	}

	var full fullConfig
	if err := yaml.Unmarshal(data, &full); err != nil {
		return Config{}, fmt.Errorf("cannot parse %s: %w", path, err)
	}

	return full.Optimize, nil
}

// ParseTargets converts the raw string config into typed Constraint values.
func ParseTargets(cfg Config) (Targets, error) {
	var t Targets

	if cfg.Targets.TotalMonthlyCost != "" {
		c, err := ParseConstraint(cfg.Targets.TotalMonthlyCost)
		if err != nil {
			return t, fmt.Errorf("total_monthly_cost: %w", err)
		}
		t.TotalMonthlyCost = c
	}

	if cfg.Targets.MaxPerRequestCost != "" {
		c, err := ParseConstraint(cfg.Targets.MaxPerRequestCost)
		if err != nil {
			return t, fmt.Errorf("max_per_request_cost: %w", err)
		}
		t.MaxPerRequestCost = c
	}

	return t, nil
}

// WorkflowsFromConfig converts WorkflowConfig slice to Workflow slice.
func WorkflowsFromConfig(cfgs []WorkflowConfig) []Workflow {
	workflows := make([]Workflow, 0, len(cfgs))
	for _, c := range cfgs {
		w := Workflow{
			Name:            c.Name,
			Model:           c.Model,
			CallsPerMonth:   c.CallsPerMonth,
			AvgInputTokens:  c.AvgInputTokens,
			AvgOutputTokens: c.AvgOutputTokens,
			RepeatRate:      c.RepeatRate,
			BatchSize:       c.BatchSize,
		}
		// Apply defaults.
		if w.Model == "" {
			w.Model = "sonnet"
		}
		if w.CallsPerMonth <= 0 {
			w.CallsPerMonth = 1
		}
		workflows = append(workflows, w)
	}
	return workflows
}
