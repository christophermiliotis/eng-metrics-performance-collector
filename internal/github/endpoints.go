package github

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Repository fetches repo metadata (used to resolve the default branch).
func (c *Client) Repository(ctx context.Context, owner, repo string) (Repository, error) {
	var out Repository
	_, err := c.get(ctx, fmt.Sprintf("/repos/%s/%s", owner, repo), &out)
	return out, err
}

// PullRequestsUpdatedSince lists pull requests sorted by update time
// (descending) and stops paging once it walks past `since`. GitHub's PR list
// endpoint cannot filter by date server-side, so we sort by updated and cut the
// tail client-side. Returns PRs whose updated_at >= since; callers apply
// metric-specific window logic (created vs merged) on top.
//
// We request state=all so both open and merged/closed PRs are visible.
func (c *Client) PullRequestsUpdatedSince(ctx context.Context, owner, repo string, since time.Time) ([]PullRequest, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls?%s", owner, repo,
		q("state", "all", "sort", "updated", "direction", "desc", "per_page", "100"))

	var all []PullRequest
	err := c.getPaginated(ctx, path, func(body []byte) (bool, error) {
		var page []PullRequest
		if err := json.Unmarshal(body, &page); err != nil {
			return false, fmt.Errorf("decoding pulls page: %w", err)
		}
		for _, pr := range page {
			// Sorted desc by updated_at: the first PR older than the window
			// means every subsequent PR is older too.
			if pr.UpdatedAt.Before(since) {
				return true, nil // stop paging
			}
			all = append(all, pr)
		}
		return false, nil
	})
	return all, err
}

// Reviews lists all reviews on a pull request.
func (c *Client) Reviews(ctx context.Context, owner, repo string, number int) ([]Review, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews?%s", owner, repo, number, q("per_page", "100"))
	var all []Review
	err := c.getPaginated(ctx, path, func(body []byte) (bool, error) {
		var page []Review
		if err := json.Unmarshal(body, &page); err != nil {
			return false, fmt.Errorf("decoding reviews page: %w", err)
		}
		all = append(all, page...)
		return false, nil
	})
	return all, err
}

// IssueComments lists conversation-level comments on a pull request (PRs are
// issues for the comments endpoint).
func (c *Client) IssueComments(ctx context.Context, owner, repo string, number int) ([]IssueComment, error) {
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments?%s", owner, repo, number, q("per_page", "100"))
	var all []IssueComment
	err := c.getPaginated(ctx, path, func(body []byte) (bool, error) {
		var page []IssueComment
		if err := json.Unmarshal(body, &page); err != nil {
			return false, fmt.Errorf("decoding comments page: %w", err)
		}
		all = append(all, page...)
		return false, nil
	})
	return all, err
}

// WorkflowRunsSince lists workflow runs created on/after `since` for a single
// workflow file, newest first. The workflowFile is the base name, e.g.
// "deploy-production.yml". GitHub supports a `created:>=` range filter.
func (c *Client) WorkflowRunsSince(ctx context.Context, owner, repo, workflowFile string, since time.Time) ([]WorkflowRun, error) {
	path := fmt.Sprintf("/repos/%s/%s/actions/workflows/%s/runs?%s",
		owner, repo, workflowFile,
		q("created", ">="+since.UTC().Format("2006-01-02T15:04:05Z"), "per_page", "100"))

	var all []WorkflowRun
	err := c.getPaginated(ctx, path, func(body []byte) (bool, error) {
		var page struct {
			WorkflowRuns []WorkflowRun `json:"workflow_runs"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return false, fmt.Errorf("decoding workflow runs page: %w", err)
		}
		all = append(all, page.WorkflowRuns...)
		return false, nil
	})
	return all, err
}

// Compare returns the commit comparison between base and head
// (GET /repos/{o}/{r}/compare/{base}...{head}). Used to attribute the commits
// shipped by one deployment relative to the previous one.
func (c *Client) Compare(ctx context.Context, owner, repo, base, head string) (Comparison, error) {
	var out Comparison
	_, err := c.get(ctx, fmt.Sprintf("/repos/%s/%s/compare/%s...%s", owner, repo, base, head), &out)
	return out, err
}
