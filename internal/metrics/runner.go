package metrics

import (
	"context"
	"time"

	"github.com/soundcloud/engineering-performance-metrics/internal/github"
)

// DataNeeds declares which (potentially expensive) data a metric requires. A
// metric that implements Needs lets the collector skip per-PR review/comment
// fetches and deploy/compare calls when nothing selected needs them. Metrics
// that don't implement it are assumed to need everything, which is safe.
type DataNeeds struct {
	Reviews     bool
	Comments    bool
	Deployments bool
}

// Needs is the optional interface a Metric can implement to declare DataNeeds.
type Needs interface {
	Needs() DataNeeds
}

// needsFor returns the union of data needs across the given metrics. Metrics
// not implementing Needs default to requiring all data.
func needsFor(ms []Metric) DataNeeds {
	var union DataNeeds
	for _, m := range ms {
		if n, ok := m.(Needs); ok {
			d := n.Needs()
			union.Reviews = union.Reviews || d.Reviews
			union.Comments = union.Comments || d.Comments
			union.Deployments = union.Deployments || d.Deployments
		} else {
			return DataNeeds{Reviews: true, Comments: true, Deployments: true}
		}
	}
	return union
}

// RunOptions configures a full metric run.
type RunOptions struct {
	Owner              string
	Repo               string
	From               time.Time
	To                 time.Time
	DeployWorkflowFile string
	DeployBranch       string
}

// Snapshot is the complete output of one run: window metadata plus every
// metric's result.
type Snapshot struct {
	GeneratedAt    time.Time `json:"generated_at"`
	Owner          string    `json:"owner"`
	Repo           string    `json:"repo"`
	WindowFrom     time.Time `json:"window_from"`
	WindowTo       time.Time `json:"window_to"`
	WindowDays     int       `json:"window_days"`
	DefaultBranch  string    `json:"default_branch"`
	DeployWorkflow string    `json:"deploy_workflow,omitempty"`
	Results        []Result  `json:"results"`
}

// Run collects the shared window for exactly what the selected metrics need,
// then computes each metric. generatedAt is passed in (rather than read from the
// clock here) so the caller controls timestamping and the function stays
// deterministic for tests.
func Run(ctx context.Context, gh *github.Client, selected []Metric, opt RunOptions, generatedAt time.Time, logf Logf) (Snapshot, error) {
	needs := needsFor(selected)

	w, err := Collect(ctx, gh, CollectOptions{
		Owner:              opt.Owner,
		Repo:               opt.Repo,
		From:               opt.From,
		To:                 opt.To,
		DeployWorkflowFile: opt.DeployWorkflowFile,
		DeployBranch:       opt.DeployBranch,
		NeedReviews:        needs.Reviews,
		NeedComments:       needs.Comments,
		NeedDeployments:    needs.Deployments,
	}, logf)
	if err != nil {
		return Snapshot{}, err
	}

	snap := Snapshot{
		GeneratedAt:   generatedAt,
		Owner:         opt.Owner,
		Repo:          opt.Repo,
		WindowFrom:    opt.From,
		WindowTo:      opt.To,
		WindowDays:    int(opt.To.Sub(opt.From).Hours()/24 + 0.5),
		DefaultBranch: w.DefaultBranch,
	}
	if needs.Deployments {
		snap.DeployWorkflow = opt.DeployWorkflowFile
	}

	for _, m := range selected {
		res, err := m.Compute(ctx, w)
		if err != nil {
			return Snapshot{}, err
		}
		snap.Results = append(snap.Results, res)
	}
	return snap, nil
}
