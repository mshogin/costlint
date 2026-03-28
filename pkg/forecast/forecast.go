// Package forecast provides branch-level cost prediction for AI-assisted development.
// It analyses git commits in a branch versus a base branch, estimates the number of
// AI prompts required, and multiplies by the average cost-per-prompt derived from
// historical telemetry.
package forecast

import (
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/mshogin/costlint/pkg/feature"
	"github.com/mshogin/costlint/pkg/pricing"
	"github.com/mshogin/costlint/pkg/telemetry"
)

// defaultTokensPerPrompt is used when no telemetry is available to estimate
// a reasonable average prompt size (input + output tokens).
const defaultTokensPerPrompt = 200

// BreakdownEntry holds per-model prompt count and estimated cost.
type BreakdownEntry struct {
	Prompts int     `json:"prompts"`
	Cost    float64 `json:"cost"`
}

// Forecast is the result of a branch cost prediction.
type Forecast struct {
	Branch           string                     `json:"branch"`
	Base             string                     `json:"base"`
	Commits          int                        `json:"commits"`
	FilesChanged     int                        `json:"files_changed"`
	LinesAdded       int                        `json:"lines_added"`
	LinesRemoved     int                        `json:"lines_removed"`
	EstimatedPrompts int                        `json:"estimated_prompts"`
	AvgCostPerPrompt float64                    `json:"avg_cost_per_prompt"`
	ModelMix         map[string]float64         `json:"model_mix"`
	ForecastUSD      float64                    `json:"forecast_usd"`
	Confidence       string                     `json:"confidence"`
	Breakdown        map[string]*BreakdownEntry `json:"breakdown"`

	// Feature tracking integration (only set when --issue is used)
	SpentUSD     *float64 `json:"spent_usd,omitempty"`
	RemainingUSD *float64 `json:"remaining_usd,omitempty"`
	TotalUSD     *float64 `json:"total_usd,omitempty"`
}

// Options controls the forecast calculation.
type Options struct {
	Branch         string
	Base           string
	Model          string // override model (empty = use telemetry mix)
	IssueID        string // optional issue for feature tracking integration
	TelemetryPath  string // defaults to ~/.costlint-telemetry.jsonl
	FeatureHistory string // defaults to ~/.costlint-features.jsonl
	FeatureState   string // defaults to ~/.costlint-features-active.json
}

// defaultTelemetryPath returns the path to the shared telemetry log file.
func defaultTelemetryPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".costlint-telemetry.jsonl"
	}
	return home + "/.costlint-telemetry.jsonl"
}

// defaultFeaturePaths returns feature tracker file paths.
func defaultFeaturePaths() (history, state string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".costlint-features.jsonl", ".costlint-features-active.json"
	}
	return home + "/.costlint-features.jsonl", home + "/.costlint-features-active.json"
}

// Calculate runs the full forecast algorithm for the given options and returns a Forecast.
func Calculate(opts Options) (*Forecast, error) {
	// Resolve branch defaults
	if opts.Branch == "" {
		b, err := currentBranch()
		if err != nil {
			return nil, err
		}
		opts.Branch = b
	}
	if opts.Base == "" {
		opts.Base = "main"
	}
	if opts.TelemetryPath == "" {
		opts.TelemetryPath = defaultTelemetryPath()
	}

	// Step 1: collect git data
	commits, err := gitLog(opts.Base, opts.Branch)
	if err != nil {
		return nil, fmt.Errorf("reading git log: %w", err)
	}

	stats, err := gitDiffStat(opts.Base, opts.Branch)
	if err != nil {
		// Non-fatal: diff stats are best-effort
		stats = BranchStats{}
	}

	// Warn on very large branches
	if len(commits) > 100 {
		_, _ = fmt.Fprintf(os.Stderr, "warning: large branch (%d commits), consider splitting\n", len(commits))
	}

	// Step 2: estimate prompts
	estimatedPrompts := estimatePrompts(commits)

	// Step 3: load telemetry and compute averages
	tlog := telemetry.NewTelemetryLog(opts.TelemetryPath)
	events, _ := tlog.Load() // ignore error - fall back to defaults

	avgCost, modelMix, telemetryDays := analysetelemetry(events, opts.Model)

	// Step 4: compute forecast
	forecastUSD := float64(estimatedPrompts) * avgCost
	forecastUSD = math.Round(forecastUSD*10000) / 10000

	// Step 5: per-model breakdown
	breakdown := computeBreakdown(estimatedPrompts, modelMix, avgCost)

	// Step 6: confidence
	conf := confidence(telemetryDays, len(commits))

	f := &Forecast{
		Branch:           opts.Branch,
		Base:             opts.Base,
		Commits:          len(commits),
		FilesChanged:     stats.FilesChanged,
		LinesAdded:       stats.Insertions,
		LinesRemoved:     stats.Deletions,
		EstimatedPrompts: estimatedPrompts,
		AvgCostPerPrompt: avgCost,
		ModelMix:         modelMix,
		ForecastUSD:      forecastUSD,
		Confidence:       conf,
		Breakdown:        breakdown,
	}

	// Step 7: feature tracking integration
	if opts.IssueID != "" {
		histPath, statePath := defaultFeaturePaths()
		if opts.FeatureHistory != "" {
			histPath = opts.FeatureHistory
		}
		if opts.FeatureState != "" {
			statePath = opts.FeatureState
		}
		tracker := feature.NewFeatureTrackerWithPath(histPath, statePath)
		sessions, err := tracker.ActiveSessions()
		if err == nil {
			for _, s := range sessions {
				if s.IssueID == opts.IssueID {
					spent := s.CostTotal
					remaining := forecastUSD
					total := spent + remaining
					f.SpentUSD = &spent
					f.RemainingUSD = &remaining
					f.TotalUSD = &total
					break
				}
			}
		}
	}

	return f, nil
}

// analysetelemetry computes average cost per prompt and model distribution from
// historical events, filtered to the last window. If model is non-empty it
// overrides the mix. Returns (avgCost, modelMix, telemetryDays).
func analysetelemetry(events []telemetry.TelemetryEvent, modelOverride string) (float64, map[string]float64, int) {
	const window = 7 * 24 * time.Hour

	cutoff := time.Now().Add(-window)

	var recent []telemetry.TelemetryEvent
	oldestTime := time.Now()

	for _, ev := range events {
		ts, err := time.Parse(time.RFC3339, ev.Timestamp)
		if err != nil {
			continue
		}
		if ts.After(cutoff) {
			recent = append(recent, ev)
			if ts.Before(oldestTime) {
				oldestTime = ts
			}
		}
	}

	telemetryDays := 0
	if len(recent) > 0 {
		d := time.Since(oldestTime)
		telemetryDays = int(d.Hours()/24) + 1
	}

	// Default model mix
	defaultMix := map[string]float64{"sonnet": 1.0}
	defaultCost := pricing.Estimate("sonnet", defaultTokensPerPrompt/2, defaultTokensPerPrompt/2)

	if modelOverride != "" {
		mix := map[string]float64{modelOverride: 1.0}
		cost := pricing.Estimate(modelOverride, defaultTokensPerPrompt/2, defaultTokensPerPrompt/2)
		return cost, mix, telemetryDays
	}

	if len(recent) == 0 {
		return defaultCost, defaultMix, 0
	}

	// Model distribution
	modelCounts := make(map[string]int)
	totalCost := 0.0
	for _, ev := range recent {
		model := normaliseModel(ev.Model)
		if model != "" {
			modelCounts[model]++
		}
		totalCost += ev.CostUSD
	}

	avgCost := totalCost / float64(len(recent))

	mix := make(map[string]float64)
	total := 0
	for _, n := range modelCounts {
		total += n
	}
	if total > 0 {
		for m, n := range modelCounts {
			mix[m] = math.Round(float64(n)/float64(total)*100) / 100
		}
	} else {
		mix = defaultMix
	}

	return avgCost, mix, telemetryDays
}

// normaliseModel maps model names to canonical keys (haiku/sonnet/opus).
func normaliseModel(m string) string {
	m = strings.ToLower(m)
	switch {
	case strings.Contains(m, "haiku"):
		return "haiku"
	case strings.Contains(m, "opus"):
		return "opus"
	case strings.Contains(m, "sonnet"), m == "":
		return "sonnet"
	default:
		return m
	}
}

// computeBreakdown distributes estimated prompts across models according to mix
// and calculates per-model cost.
func computeBreakdown(totalPrompts int, mix map[string]float64, avgCost float64) map[string]*BreakdownEntry {
	bd := make(map[string]*BreakdownEntry)

	if len(mix) == 0 {
		return bd
	}

	// Distribute prompts according to model mix fractions
	remaining := totalPrompts
	models := make([]string, 0, len(mix))
	for m := range mix {
		models = append(models, m)
	}

	for i, m := range models {
		var prompts int
		if i == len(models)-1 {
			// Give all remaining to the last model to avoid rounding loss
			prompts = remaining
		} else {
			prompts = int(math.Round(float64(totalPrompts) * mix[m]))
			if prompts > remaining {
				prompts = remaining
			}
		}
		remaining -= prompts

		modelCost := float64(prompts) * avgCost
		// Adjust by model price ratio if we know the model
		if ref, ok := pricing.Models[m]; ok {
			if sonnet, ok2 := pricing.Models["sonnet"]; ok2 && sonnet.InputPerMToken > 0 {
				modelCost = float64(prompts) * avgCost * (ref.InputPerMToken / sonnet.InputPerMToken)
			}
		}
		modelCost = math.Round(modelCost*10000) / 10000

		bd[m] = &BreakdownEntry{
			Prompts: prompts,
			Cost:    modelCost,
		}
	}

	return bd
}

// FormatText produces a human-readable forecast report.
func FormatText(f *Forecast) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "Branch: %s (vs %s)\n", f.Branch, f.Base)
	fmt.Fprintf(&sb, "Commits: %d | Files changed: %d | Lines: +%d / -%d\n\n",
		f.Commits, f.FilesChanged, f.LinesAdded, f.LinesRemoved)

	fmt.Fprintf(&sb, "Estimated prompts: %d\n", f.EstimatedPrompts)
	fmt.Fprintf(&sb, "Avg cost per prompt: $%.4f (last 7 days)\n", f.AvgCostPerPrompt)

	if len(f.ModelMix) > 0 {
		parts := make([]string, 0, len(f.ModelMix))
		for m, pct := range f.ModelMix {
			parts = append(parts, fmt.Sprintf("%s %.0f%%", m, pct*100))
		}
		fmt.Fprintf(&sb, "Model mix: %s\n", strings.Join(parts, " | "))
	}

	fmt.Fprintf(&sb, "\nFORECAST: ~$%.4f\n", f.ForecastUSD)
	fmt.Fprintf(&sb, "Confidence: %s\n", f.Confidence)

	if len(f.Breakdown) > 0 {
		fmt.Fprintf(&sb, "\nBreakdown:\n")
		for m, entry := range f.Breakdown {
			fmt.Fprintf(&sb, "  %-8s %d prompts x $%.4f = $%.4f\n",
				m+":", entry.Prompts, f.AvgCostPerPrompt, entry.Cost)
		}
	}

	if f.SpentUSD != nil {
		fmt.Fprintf(&sb, "\nFeature tracking:\n")
		fmt.Fprintf(&sb, "  Spent: $%.4f | Remaining forecast: $%.4f | Total: $%.4f\n",
			*f.SpentUSD, *f.RemainingUSD, *f.TotalUSD)
	}

	return sb.String()
}
