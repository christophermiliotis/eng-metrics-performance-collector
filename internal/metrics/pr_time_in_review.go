package metrics

import (
	"context"
	"fmt"
	"sort"
)

func init() { Register(&prTimeInReview{}) }

// prTimeInReview measures how long a PR spends in review: from when a non-draft
// PR is opened until it is merged, expressed in days. Only merged PRs whose
// merge time falls within the window are included, so the metric reflects work
// that completed in the period.
//
// The headline Value is the median (robust to outliers like a PR left open for
// months). The mean and per-PR series are also returned for downstream use.
//
// Caveat: GitHub's list payload does not expose the moment a PR transitioned
// from draft to ready, so for PRs opened as drafts we measure from created_at.
// This is noted in the output.
type prTimeInReview struct{}

func (prTimeInReview) ID() string    { return "pr_time_in_review_days" }
func (prTimeInReview) Title() string { return "Time a PR spent in review (days)" }

// Needs only the PR list (created/merged timestamps).
func (prTimeInReview) Needs() DataNeeds { return DataNeeds{} }

func (prTimeInReview) Compute(_ context.Context, w *Window) (Result, error) {
	var durations []float64
	var series []DataPoint

	for _, p := range w.PullRequests {
		pr := p.PR
		if !pr.IsMerged() {
			continue
		}
		merged := *pr.MergedAt
		if merged.Before(w.From) || merged.After(w.To) {
			continue
		}
		days := merged.Sub(pr.CreatedAt).Hours() / 24.0
		if days < 0 {
			continue // clock skew / data anomaly; skip defensively
		}
		durations = append(durations, days)
		series = append(series, DataPoint{
			Label: fmt.Sprintf("#%d", pr.Number),
			Value: round2(days),
		})
	}

	median := round2(medianOf(durations))
	mean := round2(meanOf(durations))

	res := Result{
		ID:     "pr_time_in_review_days",
		Title:  "Time a PR spent in review (days)",
		Unit:   "days",
		Value:  median,
		Series: series,
		Notes: fmt.Sprintf(
			"Median across %d PRs merged in-window (mean=%.2f). Measured open->merged; "+
				"for PRs opened as drafts, measured from created_at.",
			len(durations), mean),
	}
	if res.Breakdown == nil {
		res.Breakdown = map[string]float64{}
	}
	res.Breakdown["median_days"] = median
	res.Breakdown["mean_days"] = mean
	res.Breakdown["merged_pr_count"] = float64(len(durations))
	return res, nil
}

func medianOf(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func meanOf(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

// percentileOf returns the p-th percentile (0..100) of xs using linear
// interpolation between closest ranks (the same method as NumPy's default and
// Excel's PERCENTILE.INC). Returns 0 for an empty slice.
func percentileOf(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	if len(s) == 1 {
		return s[0]
	}
	if p <= 0 {
		return s[0]
	}
	if p >= 100 {
		return s[len(s)-1]
	}
	// Rank in [0, n-1].
	rank := (p / 100) * float64(len(s)-1)
	lo := int(rank)
	frac := rank - float64(lo)
	if lo+1 >= len(s) {
		return s[lo]
	}
	return s[lo] + frac*(s[lo+1]-s[lo])
}

func round2(f float64) float64 {
	return float64(int64(f*100+0.5)) / 100
}
