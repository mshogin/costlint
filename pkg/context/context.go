// Package context provides token cost estimation for nassau context snapshots.
// A context snapshot is a YAML file produced by "nassau context save" and
// contains components and/or messages with pre-computed token counts.
package context

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/mshogin/costlint/pkg/counter"
	"github.com/mshogin/costlint/pkg/pricing"
)

// Snapshot represents a nassau context snapshot YAML file.
// It supports two token sources:
//  1. Explicit token counts on messages/components (fastest, no re-counting).
//  2. Inline content strings that are counted on the fly.
type Snapshot struct {
	Name       string      `yaml:"name"`
	Version    string      `yaml:"version"`
	Metadata   interface{} `yaml:"metadata"`
	Messages   []Message   `yaml:"messages"`
	Components []Component `yaml:"components"`
}

// Message is a single conversation turn inside the snapshot.
type Message struct {
	Role    string `yaml:"role"`
	Content string `yaml:"content"`
	Tokens  int    `yaml:"tokens"` // pre-computed; if >0 it is used directly
}

// Component is a named context block (e.g. a file, tool result, system prompt).
type Component struct {
	Name    string `yaml:"name"`
	Content string `yaml:"content"`
	Tokens  int    `yaml:"tokens"` // pre-computed; if >0 it is used directly
}

// CostPerModel holds the cost estimate for a single model.
type CostPerModel struct {
	Model       string  `json:"model"`
	InputTokens int     `json:"input_tokens"`
	CostUSD     float64 `json:"cost_usd"`
}

// Result is the output of EstimateFile / Estimate.
type Result struct {
	SnapshotName  string         `json:"snapshot_name"`
	TotalTokens   int            `json:"total_tokens"`
	MessageTokens int            `json:"message_tokens"`
	ComponentTokens int          `json:"component_tokens"`
	Models        []CostPerModel `json:"models"`
}

// ParseFile reads and parses a YAML snapshot from the given file path.
func ParseFile(path string) (*Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading snapshot %q: %w", path, err)
	}
	return Parse(data)
}

// Parse parses a YAML snapshot from raw bytes.
func Parse(data []byte) (*Snapshot, error) {
	var snap Snapshot
	if err := yaml.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("parsing snapshot YAML: %w", err)
	}
	return &snap, nil
}

// tokenCount returns the token count for a message, using the explicit value
// when available and falling back to counting the content string.
func messageTokens(m Message) int {
	if m.Tokens > 0 {
		return m.Tokens
	}
	if m.Content == "" {
		return 0
	}
	return counter.Count(m.Content).Input
}

// componentTokens returns the token count for a component.
func componentTokens(c Component) int {
	if c.Tokens > 0 {
		return c.Tokens
	}
	if c.Content == "" {
		return 0
	}
	return counter.Count(c.Content).Input
}

// Estimate calculates the token cost for a snapshot across all models.
func Estimate(snap *Snapshot) Result {
	msgTokens := 0
	for _, m := range snap.Messages {
		msgTokens += messageTokens(m)
	}

	compTokens := 0
	for _, c := range snap.Components {
		compTokens += componentTokens(c)
	}

	total := msgTokens + compTokens

	models := make([]CostPerModel, 0, len(pricing.Models))
	for key := range pricing.Models {
		cost := pricing.Estimate(key, total, 0)
		models = append(models, CostPerModel{
			Model:       key,
			InputTokens: total,
			CostUSD:     cost,
		})
	}

	// Sort for stable output: haiku, sonnet, opus.
	sortModels(models)

	return Result{
		SnapshotName:    snap.Name,
		TotalTokens:     total,
		MessageTokens:   msgTokens,
		ComponentTokens: compTokens,
		Models:          models,
	}
}

// EstimateFile is a convenience wrapper that parses and estimates in one call.
func EstimateFile(path string) (Result, error) {
	snap, err := ParseFile(path)
	if err != nil {
		return Result{}, err
	}
	return Estimate(snap), nil
}

// sortModels sorts model results in a stable order: haiku < sonnet < opus,
// so the output is deterministic regardless of map iteration order.
func sortModels(models []CostPerModel) {
	order := map[string]int{"haiku": 0, "sonnet": 1, "opus": 2}
	for i := 0; i < len(models); i++ {
		for j := i + 1; j < len(models); j++ {
			oi, ok1 := order[models[i].Model]
			oj, ok2 := order[models[j].Model]
			if ok1 && ok2 && oi > oj {
				models[i], models[j] = models[j], models[i]
			} else if !ok1 && ok2 {
				models[i], models[j] = models[j], models[i]
			}
		}
	}
}
