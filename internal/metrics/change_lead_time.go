package metrics

import (
	"context"
	"fmt"
)

func init() { Register(&changeLeadTime{}) }

// changeLeadTime is the DORA metric: the time from a change being merged to
// main until it is deployed to production, in days (often called "lead time
// for changes", merge->deploy variant).
//
// Attribution is precise via commit SHAs: the collector populates each
// Deployment with the set of commit SHAs it introduced relative to the prior
// deployment. We map each merged PR to a deployment by matching the PR's merge
// commit SHA against those sets. Lead time for a PR = deployAt - mergedAt.
//
// The headline Value is the median lead time across all PRs that were both
// merged and deployed within the window. Mean and a per-PR series are included.
//
// PRs merged but not yet deployed (no matching deployment) are reported as a
// count in the breakdown but excluded from the median, since their lead time is
// not yet known.
type changeLeadTime struct{}

func (changeLeadTime) ID() string    { return "change_lead_time_days" }
func (changeLeadTime) Title() string { return "Change lead time (DORA, days)" }

// Needs deployment data (with SHA attribution) and the PR list.
func (changeLeadTime) Needs() DataNeeds { return DataNeeds{Deployments: true} }

func (changeLeadTime) Compute(_ context.Context, w *Window) (Result, error) {
	// Build SHA -> deployment time index from deployments. A commit can only
	// belong to the first deployment that shipped it; deployments are sorted
	// ascending, so the earliest wins.
	deployOf := map[string]Deployment{}
	for _, d := range w.Deployments {
		for _, sha := range d.CommitSHAs {
			if _, seen := deployOf[sha]; !seen {
				deployOf[sha] = d
			}
		}
	}

	var leadTimes []float64
	var series []DataPoint
	var mergedNotDeployed int

	for _, p := range w.PullRequests {
		pr := p.PR
		if !pr.IsMerged() || pr.MergeSHA == "" {
			continue
		}
		merged := *pr.MergedAt
		// Only consider PRs merged within the window to keep attribution bounded.
		if merged.Before(w.From) || merged.After(w.To) {
			continue
		}
		d, ok := deployOf[pr.MergeSHA]
		if !ok {
			mergedNotDeployed++
			continue
		}
		days := d.DeployAt.Sub(merged).Hours() / 24.0
		if days < 0 {
			// Deploy recorded before merge: data anomaly (e.g. squashed/rebased
			// SHA mismatch). Skip rather than pollute the median.
			continue
		}
		leadTimes = append(leadTimes, days)
		series = append(series, DataPoint{
			Label: fmt.Sprintf("#%d", pr.Number),
			Value: round2(days),
		})
	}

	median := round2(medianOf(leadTimes))
	mean := round2(meanOf(leadTimes))

	notes := fmt.Sprintf(
		"Median across %d PRs merged & deployed in-window (mean=%.2f). "+
			"%d PR(s) merged but not yet matched to a deployment. "+
			"Attribution by merge-commit SHA via the compare API.",
		len(leadTimes), mean, mergedNotDeployed)
	if len(w.Deployments) == 0 {
		notes = "No deployments available, so lead time cannot be computed. Configure deploy_workflow_file."
	}

	return Result{
		ID:     "change_lead_time_days",
		Title:  "Change lead time (DORA, days)",
		Unit:   "days",
		Value:  median,
		Series: series,
		Breakdown: map[string]float64{
			"median_days":         median,
			"mean_days":           mean,
			"deployed_pr_count":   float64(len(leadTimes)),
			"merged_not_deployed": float64(mergedNotDeployed),
		},
		Notes: notes,
	}, nil
}
