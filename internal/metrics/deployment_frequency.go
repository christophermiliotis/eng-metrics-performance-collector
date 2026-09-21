package metrics

import (
	"context"
	"fmt"
)

func init() { Register(&deploymentFrequency{}) }

// deploymentFrequency is the DORA metric: the number of production deployments
// within the window. We report the raw count as the headline value, plus a
// derived per-day and per-week rate in the breakdown for convenience.
//
// A "production deployment" is a successful run of the configured deploy
// workflow (see the collector). If no deploy workflow is configured, the metric
// reports zero and says so in Notes.
type deploymentFrequency struct{}

func (deploymentFrequency) ID() string    { return "deployment_frequency" }
func (deploymentFrequency) Title() string { return "Deployment frequency (DORA)" }

// Needs deployment data.
func (deploymentFrequency) Needs() DataNeeds { return DataNeeds{Deployments: true} }

func (deploymentFrequency) Compute(_ context.Context, w *Window) (Result, error) {
	count := float64(len(w.Deployments))
	windowDays := w.To.Sub(w.From).Hours() / 24.0
	if windowDays <= 0 {
		windowDays = 1
	}

	perDay := round2(count / windowDays)
	perWeek := round2(count / (windowDays / 7.0))

	notes := fmt.Sprintf("%d production deployments over %.1f days.", int(count), windowDays)
	if w.PrevDeploymentSHA == "" && count == 0 {
		notes = "No deployments found. Ensure deploy_workflow_file is configured and the workflow has run in this window."
	}

	return Result{
		ID:    "deployment_frequency",
		Title: "Deployment frequency (DORA)",
		Unit:  "count",
		Value: count,
		Breakdown: map[string]float64{
			"per_day":  perDay,
			"per_week": perWeek,
		},
		Notes: notes,
	}, nil
}
