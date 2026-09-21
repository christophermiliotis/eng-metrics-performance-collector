package github

import "time"

// User is the subset of a GitHub user we care about.
type User struct {
	Login string `json:"login"`
}

// PullRequest mirrors the fields of the PR list/detail payloads used by metrics.
type PullRequest struct {
	Number    int        `json:"number"`
	State     string     `json:"state"` // "open" | "closed"
	Title     string     `json:"title"`
	User      User       `json:"user"`
	Draft     bool       `json:"draft"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	MergedAt  *time.Time `json:"merged_at"` // nil if never merged
	ClosedAt  *time.Time `json:"closed_at"`
	MergeSHA  string     `json:"merge_commit_sha"`
	Base      struct {
		Ref string `json:"ref"` // target branch, e.g. "main"
	} `json:"base"`
}

// IsMerged reports whether the PR was merged.
func (p PullRequest) IsMerged() bool { return p.MergedAt != nil }

// Review is a pull request review event.
type Review struct {
	User        User      `json:"user"`
	State       string    `json:"state"` // APPROVED, COMMENTED, CHANGES_REQUESTED, DISMISSED
	SubmittedAt time.Time `json:"submitted_at"`
}

// IssueComment is a top-level comment on the PR conversation. (PR review
// threads also exist; for "left at least one comment" the conversation-level
// issue comments plus review COMMENTED states are the pragmatic signal.)
type IssueComment struct {
	User      User      `json:"user"`
	CreatedAt time.Time `json:"created_at"`
}

// WorkflowRun is a single GitHub Actions run.
type WorkflowRun struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	HeadBranch   string    `json:"head_branch"`
	HeadSHA      string    `json:"head_sha"`
	Path         string    `json:"path"` // ".github/workflows/deploy-production.yml"
	Event        string    `json:"event"`
	Status       string    `json:"status"`     // "completed", ...
	Conclusion   string    `json:"conclusion"` // "success", "failure", ...
	RunStartedAt time.Time `json:"run_started_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	CreatedAt    time.Time `json:"created_at"`
}

// Repository is the subset of repo metadata we use (default branch).
type Repository struct {
	DefaultBranch string `json:"default_branch"`
}

// CommitRef identifies a commit within a compare result.
type CommitRef struct {
	SHA string `json:"sha"`
}

// Comparison is the response of the compare-two-commits API.
type Comparison struct {
	Status       string      `json:"status"` // "ahead", "behind", "identical", "diverged"
	AheadBy      int         `json:"ahead_by"`
	BehindBy     int         `json:"behind_by"`
	TotalCommits int         `json:"total_commits"`
	Commits      []CommitRef `json:"commits"`
}
