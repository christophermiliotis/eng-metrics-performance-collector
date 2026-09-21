package metrics

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/soundcloud/engineering-performance-metrics/internal/github"
)

// CollectOptions configures a window collection.
type CollectOptions struct {
	Owner              string
	Repo               string
	From               time.Time
	To                 time.Time
	DeployWorkflowFile string // base name, e.g. "deploy-production.yml"; "" disables deploy metrics
	DeployBranch       string // empty => repo default branch

	// NeedReviews / NeedComments let the collector skip per-PR fetches when no
	// selected metric needs them, saving a lot of API calls.
	NeedReviews  bool
	NeedComments bool
	// NeedDeployments enables deploy collection + SHA attribution.
	NeedDeployments bool
}

// Logf is an optional progress logger.
type Logf func(format string, args ...any)

// Collect builds the shared Window by querying GitHub once for everything the
// selected metrics need.
func Collect(ctx context.Context, gh *github.Client, opt CollectOptions, logf Logf) (*Window, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}

	repoMeta, err := gh.Repository(ctx, opt.Owner, opt.Repo)
	if err != nil {
		return nil, fmt.Errorf("resolving repository: %w", err)
	}
	w := &Window{
		Owner:         opt.Owner,
		Repo:          opt.Repo,
		From:          opt.From,
		To:            opt.To,
		DefaultBranch: repoMeta.DefaultBranch,
	}

	// --- Pull requests -----------------------------------------------------
	// We fetch PRs updated within the window. This covers PRs opened, reviewed,
	// or merged in the period. (A PR merged in-window but opened long ago still
	// has updated_at >= From because merging updates it.)
	logf("fetching pull requests updated since %s", opt.From.Format(time.RFC3339))
	prs, err := gh.PullRequestsUpdatedSince(ctx, opt.Owner, opt.Repo, opt.From)
	if err != nil {
		return nil, fmt.Errorf("fetching pull requests: %w", err)
	}
	logf("found %d candidate PRs", len(prs))

	w.PullRequests = make([]PRWithActivity, 0, len(prs))
	for _, pr := range prs {
		item := PRWithActivity{PR: pr}
		if opt.NeedReviews {
			rv, err := gh.Reviews(ctx, opt.Owner, opt.Repo, pr.Number)
			if err != nil {
				return nil, fmt.Errorf("fetching reviews for PR #%d: %w", pr.Number, err)
			}
			item.Reviews = rv
		}
		if opt.NeedComments {
			cm, err := gh.IssueComments(ctx, opt.Owner, opt.Repo, pr.Number)
			if err != nil {
				return nil, fmt.Errorf("fetching comments for PR #%d: %w", pr.Number, err)
			}
			item.Comments = cm
		}
		w.PullRequests = append(w.PullRequests, item)
	}

	// --- Deployments + SHA attribution ------------------------------------
	if opt.NeedDeployments && opt.DeployWorkflowFile != "" {
		if err := collectDeployments(ctx, gh, opt, w, logf); err != nil {
			return nil, err
		}
	}

	return w, nil
}

// collectDeployments fetches successful deploy-workflow runs in the window,
// sorts them ascending, and fills CommitSHAs for each via the compare API
// (prev.SHA...this.SHA). It also resolves PrevDeploymentSHA: the most recent
// successful deploy strictly before the window, so the earliest in-window
// deploy can still be attributed.
func collectDeployments(ctx context.Context, gh *github.Client, opt CollectOptions, w *Window, logf Logf) error {
	// deploy_branch is optional: only filter by it when explicitly set.
	// When deploying via tags, head_branch is the tag name (e.g. "v1.2.3"),
	// not the default branch, so defaulting it would silently drop every run.
	branchFilter := opt.DeployBranch
	if branchFilter != "" {
		logf("fetching deploy workflow runs (%s, branch=%s)", opt.DeployWorkflowFile, branchFilter)
	} else {
		logf("fetching deploy workflow runs (%s, all branches/tags)", opt.DeployWorkflowFile)
	}

	// Look back a bit further than the window so we can find the deployment
	// immediately preceding the window for attribution of the first in-window
	// deploy. One extra window length is a pragmatic margin.
	lookbackStart := opt.From.Add(-w.To.Sub(w.From))
	runs, err := gh.WorkflowRunsSince(ctx, opt.Owner, opt.Repo, opt.DeployWorkflowFile, lookbackStart)
	if err != nil {
		return fmt.Errorf("fetching workflow runs: %w", err)
	}

	logf("fetched %d total workflow runs", len(runs))

	// Keep only successful runs. Only filter by branch when explicitly configured.
	var success []github.WorkflowRun
	for _, r := range runs {
		if r.Conclusion != "success" {
			continue
		}
		if branchFilter != "" && r.HeadBranch != branchFilter {
			logf("skipping run %d: branch %q != %q", r.ID, r.HeadBranch, branchFilter)
			continue
		}
		success = append(success, r)
	}
	logf("%d runs passed conclusion+branch filter", len(success))
	// Sort ascending by completion time (UpdatedAt ~= when the run finished).
	sort.Slice(success, func(i, j int) bool { return success[i].UpdatedAt.Before(success[j].UpdatedAt) })

	// Partition into "before window" (for prev SHA) and "in window".
	var prevSHA string
	var inWindow []github.WorkflowRun
	for _, r := range success {
		if r.UpdatedAt.Before(w.From) {
			prevSHA = r.HeadSHA // keep advancing; last one before window wins
			continue
		}
		if r.UpdatedAt.After(w.To) {
			continue
		}
		inWindow = append(inWindow, r)
	}
	w.PrevDeploymentSHA = prevSHA
	logf("found %d successful in-window deployments", len(inWindow))

	// Build Deployment records with SHA attribution.
	priorSHA := prevSHA
	for _, r := range inWindow {
		d := Deployment{
			RunID:    r.ID,
			SHA:      r.HeadSHA,
			DeployAt: r.UpdatedAt,
		}
		if priorSHA != "" && priorSHA != r.HeadSHA {
			cmp, err := gh.Compare(ctx, opt.Owner, opt.Repo, priorSHA, r.HeadSHA)
			if err != nil {
				// Don't fail the whole run on one compare; note and continue
				// with no attribution for this deployment.
				logf("warning: compare %s...%s failed: %v", short(priorSHA), short(r.HeadSHA), err)
			} else {
				for _, c := range cmp.Commits {
					d.CommitSHAs = append(d.CommitSHAs, c.SHA)
				}
			}
		}
		w.Deployments = append(w.Deployments, d)
		priorSHA = r.HeadSHA
	}
	return nil
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
