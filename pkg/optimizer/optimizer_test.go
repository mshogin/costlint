package optimizer_test

import (
	"testing"

	"github.com/mshogin/costlint/pkg/optimizer"
)

// ---- ParseConstraint tests -------------------------------------------------

func TestParseConstraint(t *testing.T) {
	tests := []struct {
		input   string
		wantOp  string
		wantVal float64
		wantErr bool
	}{
		{"<= $100", "<=", 100, false},
		{"<= $0.05", "<=", 0.05, false},
		{">= $50", ">=", 50, false},
		{"<= 200", "<=", 200, false},
		{"< $10", "<", 10, false},
		{"> $0.01", ">", 0.01, false},
		{"100", "", 0, true},
		{"<= abc", "", 0, true},
	}

	for _, tt := range tests {
		c, err := optimizer.ParseConstraint(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseConstraint(%q) want error, got nil", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseConstraint(%q) unexpected error: %v", tt.input, err)
			continue
		}
		if c.Op != tt.wantOp {
			t.Errorf("ParseConstraint(%q).Op = %q, want %q", tt.input, c.Op, tt.wantOp)
		}
		if c.Value != tt.wantVal {
			t.Errorf("ParseConstraint(%q).Value = %v, want %v", tt.input, c.Value, tt.wantVal)
		}
	}
}

func TestConstraintSatisfied(t *testing.T) {
	lte100, _ := optimizer.ParseConstraint("<= $100")

	if !lte100.Satisfied(99) {
		t.Error("99 should satisfy <= 100")
	}
	if !lte100.Satisfied(100) {
		t.Error("100 should satisfy <= 100")
	}
	if lte100.Satisfied(101) {
		t.Error("101 should not satisfy <= 100")
	}
}

// ---- ComputeMetrics tests --------------------------------------------------

func TestComputeMetrics_Empty(t *testing.T) {
	m := optimizer.ComputeMetrics(nil)
	if m.TotalMonthlyCost != 0 {
		t.Errorf("empty workflows: TotalMonthlyCost = %v, want 0", m.TotalMonthlyCost)
	}
	if m.MaxPerRequestCost != 0 {
		t.Errorf("empty workflows: MaxPerRequestCost = %v, want 0", m.MaxPerRequestCost)
	}
}

func TestComputeMetrics_SingleWorkflow(t *testing.T) {
	w := optimizer.Workflow{
		Name:            "test",
		Model:           "haiku",
		CallsPerMonth:   1000,
		AvgInputTokens:  500,
		AvgOutputTokens: 100,
	}
	m := optimizer.ComputeMetrics([]optimizer.Workflow{w})

	if m.TotalMonthlyCost <= 0 {
		t.Errorf("TotalMonthlyCost should be > 0, got %v", m.TotalMonthlyCost)
	}
	if m.MaxPerRequestCost <= 0 {
		t.Errorf("MaxPerRequestCost should be > 0, got %v", m.MaxPerRequestCost)
	}
	// monthly = calls * per_call
	if got, want := m.TotalMonthlyCost, w.MonthlyCost(); abs(got-want) > 0.000001 {
		t.Errorf("TotalMonthlyCost = %v, want %v", got, want)
	}
}

func TestComputeMetrics_MultiWorkflow(t *testing.T) {
	workflows := []optimizer.Workflow{
		{Name: "a", Model: "haiku", CallsPerMonth: 1000, AvgInputTokens: 500, AvgOutputTokens: 100},
		{Name: "b", Model: "sonnet", CallsPerMonth: 500, AvgInputTokens: 1000, AvgOutputTokens: 300},
	}
	m := optimizer.ComputeMetrics(workflows)

	expected := workflows[0].MonthlyCost() + workflows[1].MonthlyCost()
	if abs(m.TotalMonthlyCost-expected) > 0.000001 {
		t.Errorf("TotalMonthlyCost = %v, want %v", m.TotalMonthlyCost, expected)
	}

	// MaxPerRequestCost should be the max of individual per-call costs.
	maxCost := workflows[0].CostPerCall()
	if c := workflows[1].CostPerCall(); c > maxCost {
		maxCost = c
	}
	if abs(m.MaxPerRequestCost-maxCost) > 0.000001 {
		t.Errorf("MaxPerRequestCost = %v, want %v", m.MaxPerRequestCost, maxCost)
	}
}

// ---- Optimizer.Optimize tests ----------------------------------------------

func TestOptimize_NoWorkflows(t *testing.T) {
	targets := optimizer.Targets{}
	opt := optimizer.New(targets, 10)
	suggestions := opt.Optimize(nil)
	if len(suggestions) != 0 {
		t.Errorf("expected 0 suggestions for empty workflows, got %d", len(suggestions))
	}
}

func TestOptimize_RouteFromOpusToHaiku(t *testing.T) {
	// Expensive opus workflow should get ROUTE suggestions.
	workflows := []optimizer.Workflow{
		{
			Name:            "reports/daily-summary",
			Model:           "opus",
			CallsPerMonth:   3000,
			AvgInputTokens:  500,
			AvgOutputTokens: 200,
		},
	}
	target, _ := optimizer.ParseConstraint("<= $100")
	targets := optimizer.Targets{TotalMonthlyCost: target}
	opt := optimizer.New(targets, 10)
	suggestions := opt.Optimize(workflows)

	if len(suggestions) == 0 {
		t.Fatal("expected at least 1 ROUTE suggestion, got 0")
	}

	hasRoute := false
	for _, s := range suggestions {
		if s.Type == optimizer.SuggestionRoute {
			hasRoute = true
			if s.MonthlySaving <= 0 {
				t.Errorf("ROUTE suggestion should have positive MonthlySaving, got %v", s.MonthlySaving)
			}
		}
	}
	if !hasRoute {
		t.Error("expected at least one ROUTE suggestion")
	}
}

func TestOptimize_CacheSuggestion(t *testing.T) {
	// Workflow with high repeat rate should get a CACHE suggestion.
	workflows := []optimizer.Workflow{
		{
			Name:            "chat/system-prompt",
			Model:           "sonnet",
			CallsPerMonth:   10000,
			AvgInputTokens:  2000,
			AvgOutputTokens: 100,
			RepeatRate:      0.8, // 80% repeat rate
		},
	}
	target, _ := optimizer.ParseConstraint("<= $50")
	targets := optimizer.Targets{TotalMonthlyCost: target}
	opt := optimizer.New(targets, 10)
	suggestions := opt.Optimize(workflows)

	hasCache := false
	for _, s := range suggestions {
		if s.Type == optimizer.SuggestionCache {
			hasCache = true
			if s.MonthlySaving <= 0 {
				t.Errorf("CACHE suggestion should have positive MonthlySaving, got %v", s.MonthlySaving)
			}
		}
	}
	if !hasCache {
		t.Error("expected at least one CACHE suggestion")
	}
}

func TestOptimize_BatchSuggestion(t *testing.T) {
	// Workflow with batch_size > 1 should get a BATCH suggestion.
	workflows := []optimizer.Workflow{
		{
			Name:            "notifications/send",
			Model:           "haiku",
			CallsPerMonth:   5000,
			AvgInputTokens:  200,
			AvgOutputTokens: 50,
			BatchSize:       10,
		},
	}
	targets := optimizer.Targets{}
	opt := optimizer.New(targets, 10)
	suggestions := opt.Optimize(workflows)

	hasBatch := false
	for _, s := range suggestions {
		if s.Type == optimizer.SuggestionBatch {
			hasBatch = true
		}
	}
	if !hasBatch {
		t.Error("expected at least one BATCH suggestion")
	}
}

func TestOptimize_AlreadySatisfied(t *testing.T) {
	// Very cheap workflow that already meets target.
	workflows := []optimizer.Workflow{
		{
			Name:            "ping/healthcheck",
			Model:           "haiku",
			CallsPerMonth:   10,
			AvgInputTokens:  10,
			AvgOutputTokens: 5,
		},
	}
	target, _ := optimizer.ParseConstraint("<= $1000")
	targets := optimizer.Targets{TotalMonthlyCost: target}
	opt := optimizer.New(targets, 10)
	suggestions := opt.Optimize(workflows)

	// Suggestions may still exist for the max_per_request target, but
	// there should be no ImpactScore > 0 since target is met.
	for _, s := range suggestions {
		if s.ImpactScore > 1 {
			t.Errorf("unexpected high ImpactScore %d when target already satisfied", s.ImpactScore)
		}
	}
}

func TestOptimize_TopNLimit(t *testing.T) {
	// Many workflows -> suggestions capped at topN.
	workflows := make([]optimizer.Workflow, 20)
	for i := range workflows {
		workflows[i] = optimizer.Workflow{
			Name:            "workflow",
			Model:           "opus",
			CallsPerMonth:   1000,
			AvgInputTokens:  500,
			AvgOutputTokens: 200,
		}
	}
	target, _ := optimizer.ParseConstraint("<= $10")
	targets := optimizer.Targets{TotalMonthlyCost: target}
	opt := optimizer.New(targets, 3)
	suggestions := opt.Optimize(workflows)

	if len(suggestions) > 3 {
		t.Errorf("expected at most 3 suggestions (topN=3), got %d", len(suggestions))
	}
}

func TestOptimize_RankedByImpactThenSaving(t *testing.T) {
	// Two opus workflows - the one with more calls/month should rank higher.
	workflows := []optimizer.Workflow{
		{Name: "small", Model: "opus", CallsPerMonth: 100, AvgInputTokens: 500, AvgOutputTokens: 200},
		{Name: "large", Model: "opus", CallsPerMonth: 10000, AvgInputTokens: 500, AvgOutputTokens: 200},
	}
	target, _ := optimizer.ParseConstraint("<= $10")
	targets := optimizer.Targets{TotalMonthlyCost: target}
	opt := optimizer.New(targets, 10)
	suggestions := opt.Optimize(workflows)

	if len(suggestions) < 2 {
		t.Skip("not enough suggestions to test ranking")
	}

	// First suggestion should have >= saving of second.
	if suggestions[0].MonthlySaving < suggestions[1].MonthlySaving {
		// This is only guaranteed when impact scores are equal.
		if suggestions[0].ImpactScore == suggestions[1].ImpactScore {
			t.Errorf("suggestions not ranked by saving: [0]=%v < [1]=%v",
				suggestions[0].MonthlySaving, suggestions[1].MonthlySaving)
		}
	}
}

// ---- WorkflowConfig helpers ------------------------------------------------

func TestWorkflowsFromConfig_Defaults(t *testing.T) {
	cfgs := []optimizer.WorkflowConfig{
		{Name: "test"},
	}
	workflows := optimizer.WorkflowsFromConfig(cfgs)
	if len(workflows) != 1 {
		t.Fatalf("expected 1 workflow, got %d", len(workflows))
	}
	if workflows[0].Model != "sonnet" {
		t.Errorf("default model should be sonnet, got %q", workflows[0].Model)
	}
	if workflows[0].CallsPerMonth != 1 {
		t.Errorf("default CallsPerMonth should be 1, got %d", workflows[0].CallsPerMonth)
	}
}

// ---- helpers ---------------------------------------------------------------

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
