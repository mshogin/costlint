// Package optimizer suggests cost-reduction changes to hit spending targets.
//
// Algorithm (v1):
//  1. Compute baseline cost metrics from workflow entries.
//  2. For each workflow, simulate three optimization types:
//     - ROUTE: switch to a cheaper model.
//     - CACHE: add prompt caching for repeated calls.
//     - BATCH: merge multiple calls into one batched call.
//  3. Score each simulation by how many target constraints move closer to satisfied.
//  4. Return the top-N suggestions ranked by monthly cost impact.
package optimizer

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mshogin/costlint/pkg/pricing"
)

// ---- Constraint parsing ----------------------------------------------------

// Constraint represents a comparison constraint like "<= $100" or "<= $0.05".
type Constraint struct {
	Op    string  // "<=" or ">="
	Value float64 // numeric threshold in USD
}

// ParseConstraint parses a string like "<= $100" or "<= $0.05" into a Constraint.
// The dollar sign is optional.
func ParseConstraint(s string) (*Constraint, error) {
	s = strings.TrimSpace(s)
	for _, op := range []string{"<=", ">=", "<", ">"} {
		if strings.HasPrefix(s, op) {
			raw := strings.TrimSpace(s[len(op):])
			raw = strings.TrimPrefix(raw, "$")
			v, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return nil, fmt.Errorf("cannot parse numeric value %q in constraint %q: %w", raw, s, err)
			}
			return &Constraint{Op: op, Value: v}, nil
		}
	}
	return nil, fmt.Errorf("constraint must start with <=, >=, < or > (got %q)", s)
}

// Satisfied returns true when the measured value satisfies the constraint.
func (c *Constraint) Satisfied(v float64) bool {
	switch c.Op {
	case "<=":
		return v <= c.Value
	case ">=":
		return v >= c.Value
	case "<":
		return v < c.Value
	case ">":
		return v > c.Value
	}
	return false
}

// ---- Targets ---------------------------------------------------------------

// Targets holds user-defined cost goals from .costlint.yaml.
type Targets struct {
	TotalMonthlyCost  *Constraint // total_monthly_cost: "<= $100"
	MaxPerRequestCost *Constraint // max_per_request_cost: "<= $0.05"
}

// ---- Workflow entry --------------------------------------------------------

// Workflow represents a named AI call pattern with usage data.
// The optimizer uses this to simulate changes.
type Workflow struct {
	// Name identifies the workflow (e.g. "reports/daily-summary").
	Name string

	// Model is the current model key ("haiku", "sonnet", "opus").
	Model string

	// CallsPerMonth is the estimated number of calls per month.
	CallsPerMonth int

	// AvgInputTokens is the average input token count per call.
	AvgInputTokens int

	// AvgOutputTokens is the average output token count per call.
	AvgOutputTokens int

	// RepeatRate is the fraction of calls that use nearly-identical prompts (0.0-1.0).
	// Used for CACHE simulation. 0 means no repeated prompts.
	RepeatRate float64

	// BatchSize is how many calls could realistically be merged into one batch.
	// Used for BATCH simulation. 0 or 1 means batching is not applicable.
	BatchSize int
}

// CostPerCall returns the estimated cost per individual call.
func (w *Workflow) CostPerCall() float64 {
	return pricing.Estimate(w.Model, w.AvgInputTokens, w.AvgOutputTokens)
}

// MonthlyCost returns the estimated monthly cost.
func (w *Workflow) MonthlyCost() float64 {
	return w.CostPerCall() * float64(w.CallsPerMonth)
}

// ---- Metrics ---------------------------------------------------------------

// Metrics holds the aggregated cost values the optimizer tracks.
type Metrics struct {
	TotalMonthlyCost  float64
	MaxPerRequestCost float64
}

// ComputeMetrics computes baseline metrics from a slice of workflows.
func ComputeMetrics(workflows []Workflow) Metrics {
	m := Metrics{}
	for _, w := range workflows {
		monthly := w.MonthlyCost()
		m.TotalMonthlyCost += monthly

		perReq := w.CostPerCall()
		if perReq > m.MaxPerRequestCost {
			m.MaxPerRequestCost = perReq
		}
	}
	return m
}

// ---- Suggestion type -------------------------------------------------------

// SuggestionType classifies the optimization action.
type SuggestionType string

const (
	SuggestionRoute SuggestionType = "ROUTE"
	SuggestionCache SuggestionType = "CACHE"
	SuggestionBatch SuggestionType = "BATCH"
)

// Suggestion is a recommended change with its projected impact.
type Suggestion struct {
	Type          SuggestionType
	Workflow      string  // workflow name
	Detail        string  // human-readable change description (e.g. "sonnet -> haiku")
	MonthlySaving float64 // projected monthly cost reduction (positive = saving)
	PerCallDelta  float64 // change in per-call cost (negative = cheaper)
	ImpactScore   int     // number of target constraints moved closer to satisfied
	LatencyNote   string  // optional latency note (e.g. "+200ms")
	Reason        string  // why this change is safe/recommended
}

// ---- Optimizer -------------------------------------------------------------

// Optimizer analyses workflows and produces Suggestions.
type Optimizer struct {
	targets Targets
	topN    int
}

// New creates a new Optimizer with the given targets.
func New(targets Targets, topN int) *Optimizer {
	if topN <= 0 {
		topN = 10
	}
	return &Optimizer{targets: targets, topN: topN}
}

// Optimize runs the impact simulation and returns ranked suggestions.
func (o *Optimizer) Optimize(workflows []Workflow) []Suggestion {
	baseline := ComputeMetrics(workflows)

	var suggestions []Suggestion

	for _, w := range workflows {
		suggestions = append(suggestions, o.simulateRoute(w, baseline)...)
		suggestions = append(suggestions, o.simulateCache(w, baseline)...)
		suggestions = append(suggestions, o.simulateBatch(w, baseline)...)
	}

	// Filter to only impactful suggestions.
	var impactful []Suggestion
	for _, s := range suggestions {
		if s.ImpactScore > 0 || s.MonthlySaving > 0 {
			impactful = append(impactful, s)
		}
	}

	// Rank: primary = impact score (desc), secondary = monthly saving (desc).
	sort.Slice(impactful, func(i, j int) bool {
		if impactful[i].ImpactScore != impactful[j].ImpactScore {
			return impactful[i].ImpactScore > impactful[j].ImpactScore
		}
		return impactful[i].MonthlySaving > impactful[j].MonthlySaving
	})

	if len(impactful) > o.topN {
		impactful = impactful[:o.topN]
	}
	return impactful
}

// simulateRoute generates ROUTE suggestions by trying cheaper models.
func (o *Optimizer) simulateRoute(w Workflow, baseline Metrics) []Suggestion {
	// Model order from cheapest to most expensive.
	cheaper := cheaperModels(w.Model)
	if len(cheaper) == 0 {
		return nil
	}

	var suggestions []Suggestion
	for _, target := range cheaper {
		newCostPerCall := pricing.Estimate(target, w.AvgInputTokens, w.AvgOutputTokens)
		newMonthly := newCostPerCall * float64(w.CallsPerMonth)
		oldMonthly := w.MonthlyCost()
		saving := oldMonthly - newMonthly

		if saving <= 0 {
			continue
		}

		// Compute projected total monthly cost.
		projectedTotal := baseline.TotalMonthlyCost - saving

		score := o.scoreImpact(baseline, projectedTotal, newCostPerCall)

		latency := latencyNote(w.Model, target)

		s := Suggestion{
			Type:          SuggestionRoute,
			Workflow:      w.Name,
			Detail:        fmt.Sprintf("%s -> %s", w.Model, target),
			MonthlySaving: saving,
			PerCallDelta:  newCostPerCall - w.CostPerCall(),
			ImpactScore:   score,
			LatencyNote:   latency,
			Reason:        routeReason(w, target),
		}
		suggestions = append(suggestions, s)
	}
	return suggestions
}

// simulateCache generates a CACHE suggestion when repeat rate is significant.
func (o *Optimizer) simulateCache(w Workflow, baseline Metrics) []Suggestion {
	if w.RepeatRate <= 0 {
		return nil
	}

	// Cache read discount: 90% cheaper for cached tokens.
	const cacheDiscount = 0.9

	// Fraction of calls that benefit from caching.
	cachedCalls := float64(w.CallsPerMonth) * w.RepeatRate
	regularCalls := float64(w.CallsPerMonth) * (1 - w.RepeatRate)

	// Cost without cache.
	oldMonthly := w.MonthlyCost()

	// Cost with cache: regular calls + discounted cached calls (input only).
	inputCostPerCall := pricing.Estimate(w.Model, w.AvgInputTokens, 0)
	outputCostPerCall := pricing.Estimate(w.Model, 0, w.AvgOutputTokens)

	regularCost := (regularCalls + cachedCalls) * outputCostPerCall           // output always full price
	regularCost += regularCalls * inputCostPerCall                             // non-cached input full price
	regularCost += cachedCalls * inputCostPerCall * (1 - cacheDiscount)        // cached input discounted

	saving := oldMonthly - regularCost
	if saving <= 0 {
		return nil
	}

	projectedTotal := baseline.TotalMonthlyCost - saving
	newCostPerCall := regularCost / float64(w.CallsPerMonth)
	score := o.scoreImpact(baseline, projectedTotal, newCostPerCall)

	return []Suggestion{{
		Type:          SuggestionCache,
		Workflow:      w.Name,
		Detail:        fmt.Sprintf("add prompt caching (%.0f%% repeat rate)", w.RepeatRate*100),
		MonthlySaving: saving,
		PerCallDelta:  newCostPerCall - w.CostPerCall(),
		ImpactScore:   score,
		Reason:        fmt.Sprintf("%.0f%% of calls use similar prompts; cached tokens cost 90%% less", w.RepeatRate*100),
	}}
}

// simulateBatch generates a BATCH suggestion when BatchSize > 1.
func (o *Optimizer) simulateBatch(w Workflow, baseline Metrics) []Suggestion {
	if w.BatchSize <= 1 {
		return nil
	}

	// Batching reduces per-call overhead (approximated as 20% overhead per extra call).
	// Simplified model: overhead per call = 10% of input tokens; batching eliminates
	// N-1 overheads when merging N calls.
	overheadTokensPerCall := int(float64(w.AvgInputTokens) * 0.10)
	savedTokensPerBatch := overheadTokensPerCall * (w.BatchSize - 1)

	if savedTokensPerBatch <= 0 {
		return nil
	}

	batchedCallsPerMonth := w.CallsPerMonth / w.BatchSize
	if batchedCallsPerMonth == 0 {
		batchedCallsPerMonth = 1
	}

	newInputPerBatch := w.AvgInputTokens*w.BatchSize - savedTokensPerBatch
	newOutputPerBatch := w.AvgOutputTokens * w.BatchSize

	costPerBatch := pricing.Estimate(w.Model, newInputPerBatch, newOutputPerBatch)
	newMonthly := costPerBatch * float64(batchedCallsPerMonth)
	oldMonthly := w.MonthlyCost()
	saving := oldMonthly - newMonthly

	if saving <= 0 {
		return nil
	}

	projectedTotal := baseline.TotalMonthlyCost - saving
	newCostPerCall := newMonthly / float64(w.CallsPerMonth)
	score := o.scoreImpact(baseline, projectedTotal, newCostPerCall)

	return []Suggestion{{
		Type:          SuggestionBatch,
		Workflow:      w.Name,
		Detail:        fmt.Sprintf("batch %d calls into 1", w.BatchSize),
		MonthlySaving: saving,
		PerCallDelta:  newCostPerCall - w.CostPerCall(),
		ImpactScore:   score,
		Reason:        fmt.Sprintf("merging %d calls removes %d repeated overhead tokens per batch", w.BatchSize, savedTokensPerBatch),
	}}
}

// scoreImpact counts how many targets move closer to satisfied after the change.
func (o *Optimizer) scoreImpact(baseline Metrics, newTotal, newPerCall float64) int {
	score := 0

	if o.targets.TotalMonthlyCost != nil {
		beforeOK := o.targets.TotalMonthlyCost.Satisfied(baseline.TotalMonthlyCost)
		afterOK := o.targets.TotalMonthlyCost.Satisfied(newTotal)
		if !beforeOK && afterOK {
			score += 2 // newly satisfied
		} else if !beforeOK && newTotal < baseline.TotalMonthlyCost {
			score++ // moved closer
		}
	}

	if o.targets.MaxPerRequestCost != nil {
		beforeOK := o.targets.MaxPerRequestCost.Satisfied(baseline.MaxPerRequestCost)
		afterOK := o.targets.MaxPerRequestCost.Satisfied(newPerCall)
		if !beforeOK && afterOK {
			score += 2
		} else if !beforeOK && newPerCall < baseline.MaxPerRequestCost {
			score++
		}
	}

	return score
}

// cheaperModels returns model keys that are cheaper than the given model,
// sorted cheapest first.
func cheaperModels(model string) []string {
	order := []string{"haiku", "sonnet", "opus"}
	idx := -1
	for i, m := range order {
		if m == model {
			idx = i
			break
		}
	}
	if idx <= 0 {
		return nil // already cheapest or unknown
	}
	return order[:idx]
}

// latencyNote returns a human-readable note about expected latency change.
func latencyNote(from, to string) string {
	// Haiku is fastest, Opus is slowest.
	order := map[string]int{"haiku": 0, "sonnet": 1, "opus": 2}
	fromRank := order[from]
	toRank := order[to]
	if toRank < fromRank {
		return "-latency (faster model)"
	}
	if toRank > fromRank {
		return "+latency (slower model)"
	}
	return ""
}

// routeReason generates a reason string for a ROUTE suggestion.
func routeReason(w Workflow, targetModel string) string {
	switch targetModel {
	case "haiku":
		return "haiku handles simple tasks at 90%+ cost reduction; validate output quality before routing all calls"
	case "sonnet":
		return "sonnet provides strong reasoning at lower cost than opus; suitable for most non-trivial tasks"
	default:
		return fmt.Sprintf("switching to %s reduces cost", targetModel)
	}
}
