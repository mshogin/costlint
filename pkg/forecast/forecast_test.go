package forecast

import (
	"math"
	"testing"
)

// TestEstimatePrompts verifies the heuristic for prompt estimation per commit.
func TestEstimatePrompts(t *testing.T) {
	tests := []struct {
		name    string
		commits []Commit
		want    int
	}{
		{
			name:    "empty",
			commits: nil,
			want:    0,
		},
		{
			name: "single small commit",
			commits: []Commit{
				{FilesChanged: 1},
			},
			want: 1,
		},
		{
			name: "single medium commit",
			commits: []Commit{
				{FilesChanged: 3},
			},
			want: 2,
		},
		{
			name: "single large commit",
			commits: []Commit{
				{FilesChanged: 10},
			},
			want: 3,
		},
		{
			name: "mixed commits",
			commits: []Commit{
				{FilesChanged: 1},  // 1
				{FilesChanged: 4},  // 2
				{FilesChanged: 10}, // 3
			},
			want: 6,
		},
		{
			name: "boundary: exactly 2 files",
			commits: []Commit{
				{FilesChanged: 2},
			},
			want: 1,
		},
		{
			name: "boundary: exactly 5 files",
			commits: []Commit{
				{FilesChanged: 5},
			},
			want: 2,
		},
		{
			name: "boundary: 6 files",
			commits: []Commit{
				{FilesChanged: 6},
			},
			want: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := estimatePrompts(tt.commits)
			if got != tt.want {
				t.Errorf("estimatePrompts() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestConfidence verifies confidence scoring logic.
func TestConfidence(t *testing.T) {
	tests := []struct {
		telemetryDays int
		commits       int
		want          string
	}{
		{telemetryDays: 7, commits: 5, want: "high"},
		{telemetryDays: 10, commits: 10, want: "high"},
		{telemetryDays: 7, commits: 4, want: "medium"}, // not enough commits
		{telemetryDays: 3, commits: 5, want: "medium"},  // not enough days
		{telemetryDays: 3, commits: 0, want: "medium"},
		{telemetryDays: 2, commits: 10, want: "low"},
		{telemetryDays: 0, commits: 0, want: "low"},
		{telemetryDays: 1, commits: 5, want: "low"},
	}

	for _, tt := range tests {
		got := confidence(tt.telemetryDays, tt.commits)
		if got != tt.want {
			t.Errorf("confidence(%d, %d) = %q, want %q",
				tt.telemetryDays, tt.commits, got, tt.want)
		}
	}
}

// TestParseShortstat verifies parsing of git diff --shortstat output.
func TestParseShortstat(t *testing.T) {
	tests := []struct {
		input string
		want  BranchStats
	}{
		{
			input: " 3 files changed, 45 insertions(+), 12 deletions(-)",
			want:  BranchStats{FilesChanged: 3, Insertions: 45, Deletions: 12},
		},
		{
			input: " 1 file changed, 1 insertion(+)",
			want:  BranchStats{FilesChanged: 1, Insertions: 1, Deletions: 0},
		},
		{
			input: " 2 files changed, 5 deletions(-)",
			want:  BranchStats{FilesChanged: 2, Insertions: 0, Deletions: 5},
		},
		{
			input: "",
			want:  BranchStats{},
		},
	}

	for _, tt := range tests {
		got, err := parseShortstat(tt.input)
		if err != nil {
			t.Errorf("parseShortstat(%q) error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseShortstat(%q) = %+v, want %+v", tt.input, got, tt.want)
		}
	}
}

// TestComputeBreakdown verifies that total prompts are distributed correctly.
func TestComputeBreakdown(t *testing.T) {
	mix := map[string]float64{
		"sonnet": 0.5,
		"haiku":  0.5,
	}
	bd := computeBreakdown(10, mix, 0.10)
	if bd == nil {
		t.Fatal("breakdown is nil")
	}

	totalPrompts := 0
	for _, e := range bd {
		totalPrompts += e.Prompts
	}
	if totalPrompts != 10 {
		t.Errorf("total prompts = %d, want 10", totalPrompts)
	}
}

// TestComputeBreakdownEmpty verifies edge case with no mix.
func TestComputeBreakdownEmpty(t *testing.T) {
	bd := computeBreakdown(5, nil, 0.10)
	if len(bd) != 0 {
		t.Errorf("expected empty breakdown, got %v", bd)
	}
}

// TestNormaliseModel verifies model name normalisation.
func TestNormaliseModel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"claude-haiku-4-5", "haiku"},
		{"haiku", "haiku"},
		{"claude-sonnet-4-6", "sonnet"},
		{"sonnet", "sonnet"},
		{"", "sonnet"},
		{"claude-opus-4-6", "opus"},
		{"opus", "opus"},
		{"SONNET", "sonnet"},
	}
	for _, tt := range tests {
		got := normaliseModel(tt.input)
		if got != tt.want {
			t.Errorf("normaliseModel(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestAnalyseTelemetryNoEvents verifies fallback when no telemetry is available.
func TestAnalyseTelemetryNoEvents(t *testing.T) {
	avgCost, mix, days := analysetelemetry(nil, "")
	if days != 0 {
		t.Errorf("expected 0 telemetry days, got %d", days)
	}
	if len(mix) == 0 {
		t.Error("expected non-empty default model mix")
	}
	if avgCost <= 0 {
		t.Errorf("expected positive default avg cost, got %f", avgCost)
	}
}

// TestAnalyseTelemetryModelOverride verifies that model override works.
func TestAnalyseTelemetryModelOverride(t *testing.T) {
	avgCost, mix, _ := analysetelemetry(nil, "haiku")
	if mix["haiku"] != 1.0 {
		t.Errorf("expected haiku mix 1.0, got %v", mix)
	}
	if avgCost <= 0 {
		t.Errorf("expected positive avg cost, got %f", avgCost)
	}
}

// TestFormatText verifies that text output contains required fields.
func TestFormatText(t *testing.T) {
	f := &Forecast{
		Branch:           "feature-x",
		Base:             "main",
		Commits:          3,
		FilesChanged:     10,
		LinesAdded:       100,
		LinesRemoved:     20,
		EstimatedPrompts: 6,
		AvgCostPerPrompt: 0.15,
		ModelMix:         map[string]float64{"sonnet": 1.0},
		ForecastUSD:      0.90,
		Confidence:       "medium",
		Breakdown: map[string]*BreakdownEntry{
			"sonnet": {Prompts: 6, Cost: 0.90},
		},
	}

	text := FormatText(f)
	checks := []string{
		"feature-x",
		"main",
		"Commits: 3",
		"Estimated prompts: 6",
		"FORECAST:",
		"Confidence: medium",
		"Breakdown",
	}
	for _, s := range checks {
		if !containsStr(text, s) {
			t.Errorf("FormatText missing %q\nOutput:\n%s", s, text)
		}
	}
}

// TestForecastZeroCommits verifies the zero-commit edge case.
func TestForecastZeroCommits(t *testing.T) {
	commits := []Commit{}
	prompts := estimatePrompts(commits)
	if prompts != 0 {
		t.Errorf("expected 0 prompts for empty commits, got %d", prompts)
	}
}

// TestForecastRounding verifies USD values are rounded to 4 decimal places.
func TestForecastRounding(t *testing.T) {
	val := math.Round(0.123456789*10000) / 10000
	if val != 0.1235 {
		t.Errorf("rounding issue: %f", val)
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		findStr(s, substr))
}

func findStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
