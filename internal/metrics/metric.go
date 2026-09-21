// Package metrics defines the pluggable metric framework and the built-in
// metrics. Adding a new metric is intentionally a one-file change:
//
//  1. Create a type implementing the Metric interface.
//  2. Call Register(&yourMetric{}) from an init() in that file.
//
// The runner discovers everything via the registry, so no central list needs
// editing. Metrics share a single pre-fetched data set (the Window) to avoid
// each one re-hitting the GitHub API.
package metrics

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/soundcloud/engineering-performance-metrics/internal/github"
)

// Window is the read-only, pre-collected data set every metric computes over.
// Collecting it once and sharing it keeps API usage low and makes metrics pure
// functions of their input, which is easy to test.
type Window struct {
	Owner string
	Repo  string

	// From and To bound the rolling window. To is the run time.
	From time.Time
	To   time.Time

	// DefaultBranch resolved from the repo (e.g. "main").
	DefaultBranch string

	// PullRequests are all PRs whose updated_at falls within the window.
	// Metrics apply their own created/merged filtering on top.
	PullRequests []PRWithActivity

	// Deployments are successful production deployment runs within the window,
	// sorted ascending by deploy time. May be empty if no deploy workflow is
	// configured or none ran.
	Deployments []Deployment

	// PrevDeploymentSHA is the head SHA of the most recent successful
	// deployment strictly before the window (or "" if none / unknown). It lets
	// lead-time attribution work for the earliest in-window deployment.
	PrevDeploymentSHA string
}

// PRWithActivity bundles a pull request with its reviews and comments so review
// metrics don't each re-fetch.
type PRWithActivity struct {
	PR       github.PullRequest
	Reviews  []github.Review
	Comments []github.IssueComment
}

// Deployment is a successful production deployment derived from a workflow run.
type Deployment struct {
	RunID    int64
	SHA      string
	DeployAt time.Time // when the deploy completed (run UpdatedAt)

	// CommitSHAs are the commit SHAs this deployment introduced relative to the
	// immediately preceding successful deployment (from the compare API). Used
	// to attribute merged PRs to the deployment that shipped them. May be nil
	// if the comparison could not be resolved (e.g. first-ever deploy).
	CommitSHAs []string
}

// Result is the output of a single metric. Value holds the headline number;
// Breakdown holds optional per-key detail (e.g. per author, per reviewer).
// Series holds optional per-item data points (e.g. each PR's review time) so
// downstream tools can compute their own aggregates.
type Result struct {
	ID        string             `json:"id"`
	Title     string             `json:"title"`
	Unit      string             `json:"unit"`
	Value     float64            `json:"value"`
	Breakdown map[string]float64 `json:"breakdown,omitempty"`
	Series    []DataPoint        `json:"series,omitempty"`
	Notes     string             `json:"notes,omitempty"`
}

// DataPoint is one labelled observation within a metric's series.
type DataPoint struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
}

// Metric is the contract every metric implements.
type Metric interface {
	// ID is a stable machine identifier (used in config allow-lists and JSON).
	ID() string
	// Title is a human-readable name.
	Title() string
	// Compute derives the result from the shared window.
	Compute(ctx context.Context, w *Window) (Result, error)
}

// registry holds all registered metrics, keyed by ID.
var registry = map[string]Metric{}

// Register adds a metric to the global registry. Intended to be called from
// init(). Panics on duplicate IDs to catch programming errors early.
func Register(m Metric) {
	if _, dup := registry[m.ID()]; dup {
		panic(fmt.Sprintf("metrics: duplicate metric id %q", m.ID()))
	}
	registry[m.ID()] = m
}

// All returns the registered metrics sorted by ID for deterministic output.
func All() []Metric {
	out := make([]Metric, 0, len(registry))
	for _, m := range registry {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// Selected returns registered metrics whose ID passes the want predicate,
// sorted by ID.
func Selected(want func(id string) bool) []Metric {
	var out []Metric
	for _, m := range All() {
		if want(m.ID()) {
			out = append(out, m)
		}
	}
	return out
}

// isBot reports whether a GitHub login belongs to a bot / GitHub App account.
// GitHub App accounts (e.g. code reviewers like gemini-code-assist, dependabot,
// renovate) always surface with a "[bot]" suffix on their login, so a single
// suffix check catches them all without any configuration. Matching is
// case-insensitive and tolerant of surrounding whitespace.
func isBot(login string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(login)), "[bot]")
}
