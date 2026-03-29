package context

import (
	"testing"
)

func TestParse_EmptySnapshot(t *testing.T) {
	yaml := `name: empty-snap`
	snap, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.Name != "empty-snap" {
		t.Errorf("expected name %q, got %q", "empty-snap", snap.Name)
	}
	if len(snap.Messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(snap.Messages))
	}
	if len(snap.Components) != 0 {
		t.Errorf("expected 0 components, got %d", len(snap.Components))
	}
}

func TestParse_WithMessages(t *testing.T) {
	yaml := `
name: chat-snap
messages:
  - role: user
    content: "Hello world"
    tokens: 3
  - role: assistant
    content: "Hi there, how are you?"
    tokens: 6
`
	snap, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(snap.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(snap.Messages))
	}
	if snap.Messages[0].Tokens != 3 {
		t.Errorf("expected tokens=3, got %d", snap.Messages[0].Tokens)
	}
}

func TestParse_WithComponents(t *testing.T) {
	yaml := `
name: comp-snap
components:
  - name: system_prompt
    content: "You are a helpful assistant."
    tokens: 7
  - name: file_context
    content: "package main\n\nfunc main() {}"
    tokens: 12
`
	snap, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(snap.Components) != 2 {
		t.Fatalf("expected 2 components, got %d", len(snap.Components))
	}
}

func TestEstimate_ExplicitTokens(t *testing.T) {
	// Snapshot with explicit token counts - should use those directly.
	snap := &Snapshot{
		Name: "test",
		Messages: []Message{
			{Role: "user", Content: "...", Tokens: 100},
			{Role: "assistant", Content: "...", Tokens: 200},
		},
		Components: []Component{
			{Name: "ctx", Content: "...", Tokens: 500},
		},
	}

	result := Estimate(snap)

	if result.TotalTokens != 800 {
		t.Errorf("expected total=800, got %d", result.TotalTokens)
	}
	if result.MessageTokens != 300 {
		t.Errorf("expected message_tokens=300, got %d", result.MessageTokens)
	}
	if result.ComponentTokens != 500 {
		t.Errorf("expected component_tokens=500, got %d", result.ComponentTokens)
	}
	if len(result.Models) != 3 {
		t.Errorf("expected 3 models, got %d", len(result.Models))
	}

	// haiku should be cheapest
	if result.Models[0].Model != "haiku" {
		t.Errorf("expected first model=haiku, got %q", result.Models[0].Model)
	}
	// all costs should be > 0
	for _, m := range result.Models {
		if m.CostUSD <= 0 {
			t.Errorf("expected cost>0 for model %q, got %f", m.Model, m.CostUSD)
		}
		if m.InputTokens != 800 {
			t.Errorf("expected input_tokens=800 for model %q, got %d", m.Model, m.InputTokens)
		}
	}
}

func TestEstimate_InferredTokens(t *testing.T) {
	// Snapshot without explicit token counts - should count content.
	snap := &Snapshot{
		Name: "inferred",
		Messages: []Message{
			{Role: "user", Content: "Hello world this is a test message"},
		},
	}

	result := Estimate(snap)

	if result.TotalTokens <= 0 {
		t.Errorf("expected total>0 for inferred tokens, got %d", result.TotalTokens)
	}
}

func TestEstimate_EmptySnapshot(t *testing.T) {
	snap := &Snapshot{Name: "empty"}
	result := Estimate(snap)

	if result.TotalTokens != 0 {
		t.Errorf("expected total=0, got %d", result.TotalTokens)
	}
	// Costs should be 0 for empty snapshot.
	for _, m := range result.Models {
		if m.CostUSD != 0 {
			t.Errorf("expected cost=0 for empty snapshot model %q, got %f", m.Model, m.CostUSD)
		}
	}
}

func TestEstimate_ModelOrder(t *testing.T) {
	snap := &Snapshot{
		Name:     "order-test",
		Messages: []Message{{Tokens: 1000}},
	}
	result := Estimate(snap)

	expected := []string{"haiku", "sonnet", "opus"}
	for i, m := range result.Models {
		if m.Model != expected[i] {
			t.Errorf("position %d: expected %q, got %q", i, expected[i], m.Model)
		}
	}
}

func TestParse_InvalidYAML(t *testing.T) {
	_, err := Parse([]byte("}{invalid yaml"))
	if err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}
}

func TestSortModels(t *testing.T) {
	models := []CostPerModel{
		{Model: "opus", CostUSD: 3.0},
		{Model: "haiku", CostUSD: 0.5},
		{Model: "sonnet", CostUSD: 1.5},
	}
	sortModels(models)
	if models[0].Model != "haiku" || models[1].Model != "sonnet" || models[2].Model != "opus" {
		t.Errorf("unexpected sort order: %v %v %v", models[0].Model, models[1].Model, models[2].Model)
	}
}
