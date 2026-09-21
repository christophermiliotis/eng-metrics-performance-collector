package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/soundcloud/engineering-performance-metrics/internal/github"
)

// base window spanning 30 days ending at a fixed instant. We use a fixed clock
// so tests are deterministic.
var (
	winTo   = time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	winFrom = winTo.Add(-30 * 24 * time.Hour)
)

func tp(t time.Time) *time.Time { return &t }

func newWindow() *Window {
	return &Window{
		Owner: "soundcloud", Repo: "media-streaming",
		From: winFrom, To: winTo, DefaultBranch: "main",
	}
}

func TestPRsOpened_CountsByAuthorWithinWindow(t *testing.T) {
	w := newWindow()
	w.PullRequests = []PRWithActivity{
		{PR: github.PullRequest{Number: 1, User: github.User{Login: "alice"}, CreatedAt: winFrom.Add(time.Hour)}},
		{PR: github.PullRequest{Number: 2, User: github.User{Login: "alice"}, CreatedAt: winTo.Add(-time.Hour)}},
		{PR: github.PullRequest{Number: 3, User: github.User{Login: "bob"}, CreatedAt: winTo.Add(-2 * time.Hour)}},
		// Outside window: created before From.
		{PR: github.PullRequest{Number: 4, User: github.User{Login: "bob"}, CreatedAt: winFrom.Add(-time.Hour)}},
	}

	res, err := prsOpened{}.Compute(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	if res.Value != 3 {
		t.Errorf("total = %v, want 3", res.Value)
	}
	if res.Breakdown["alice"] != 2 || res.Breakdown["bob"] != 1 {
		t.Errorf("breakdown = %v, want alice:2 bob:1", res.Breakdown)
	}
}

func TestReviewsPerReviewer_CreditsApprovalsAndCommentsOnce(t *testing.T) {
	w := newWindow()
	mid := winFrom.Add(24 * time.Hour)
	w.PullRequests = []PRWithActivity{
		{
			PR: github.PullRequest{Number: 10, User: github.User{Login: "author"}},
			Reviews: []github.Review{
				{User: github.User{Login: "alice"}, State: "APPROVED", SubmittedAt: mid},
				{User: github.User{Login: "alice"}, State: "COMMENTED", SubmittedAt: mid}, // same PR, still 1
				{User: github.User{Login: "author"}, State: "APPROVED", SubmittedAt: mid}, // self-review ignored
				{User: github.User{Login: "carol"}, State: "DISMISSED", SubmittedAt: mid}, // not active review
			},
			Comments: []github.IssueComment{
				{User: github.User{Login: "bob"}, CreatedAt: mid}, // comment credits bob
			},
		},
		{
			PR: github.PullRequest{Number: 11, User: github.User{Login: "author"}},
			Reviews: []github.Review{
				{User: github.User{Login: "alice"}, State: "CHANGES_REQUESTED", SubmittedAt: mid}, // 2nd PR for alice
			},
		},
	}

	res, err := reviewsPerReviewer{}.Compute(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Breakdown["alice"]; got != 2 {
		t.Errorf("alice = %v, want 2", got)
	}
	if got := res.Breakdown["bob"]; got != 1 {
		t.Errorf("bob = %v, want 1", got)
	}
	if _, ok := res.Breakdown["author"]; ok {
		t.Errorf("author should not be credited for self-review")
	}
	if _, ok := res.Breakdown["carol"]; ok {
		t.Errorf("carol (DISMISSED only) should not be credited")
	}
}

func TestPRTimeInReview_MedianOpenToMerged(t *testing.T) {
	w := newWindow()
	open := winFrom.Add(time.Hour)
	w.PullRequests = []PRWithActivity{
		// 2-day, 4-day, 6-day review times -> median 4.
		{PR: github.PullRequest{Number: 1, CreatedAt: open, MergedAt: tp(open.Add(2 * 24 * time.Hour))}},
		{PR: github.PullRequest{Number: 2, CreatedAt: open, MergedAt: tp(open.Add(4 * 24 * time.Hour))}},
		{PR: github.PullRequest{Number: 3, CreatedAt: open, MergedAt: tp(open.Add(6 * 24 * time.Hour))}},
		// Not merged -> excluded.
		{PR: github.PullRequest{Number: 4, CreatedAt: open}},
	}

	res, err := prTimeInReview{}.Compute(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	if res.Value != 4 {
		t.Errorf("median = %v, want 4", res.Value)
	}
	if res.Breakdown["merged_pr_count"] != 3 {
		t.Errorf("merged count = %v, want 3", res.Breakdown["merged_pr_count"])
	}
}

func TestDeploymentFrequency_CountAndRates(t *testing.T) {
	w := newWindow()
	w.Deployments = []Deployment{
		{RunID: 1, DeployAt: winFrom.Add(1 * 24 * time.Hour)},
		{RunID: 2, DeployAt: winFrom.Add(10 * 24 * time.Hour)},
		{RunID: 3, DeployAt: winFrom.Add(20 * 24 * time.Hour)},
	}
	res, err := deploymentFrequency{}.Compute(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	if res.Value != 3 {
		t.Errorf("count = %v, want 3", res.Value)
	}
	// 3 deploys / 30 days = 0.1/day.
	if res.Breakdown["per_day"] != 0.1 {
		t.Errorf("per_day = %v, want 0.1", res.Breakdown["per_day"])
	}
}

func TestChangeLeadTime_AttributesByMergeSHA(t *testing.T) {
	w := newWindow()
	merge1 := winFrom.Add(2 * 24 * time.Hour)
	merge2 := winFrom.Add(5 * 24 * time.Hour)
	deploy := winFrom.Add(6 * 24 * time.Hour)

	w.PullRequests = []PRWithActivity{
		{PR: github.PullRequest{Number: 1, MergedAt: tp(merge1), MergeSHA: "sha1"}},
		{PR: github.PullRequest{Number: 2, MergedAt: tp(merge2), MergeSHA: "sha2"}},
		// Merged but not in any deployment -> counted as merged_not_deployed.
		{PR: github.PullRequest{Number: 3, MergedAt: tp(merge2), MergeSHA: "sha3"}},
	}
	// One deployment shipping sha1 and sha2.
	w.Deployments = []Deployment{
		{RunID: 1, SHA: "dep", DeployAt: deploy, CommitSHAs: []string{"sha1", "sha2"}},
	}

	res, err := changeLeadTime{}.Compute(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	// PR1: 4 days, PR2: 1 day -> median 2.5.
	if res.Value != 2.5 {
		t.Errorf("median lead time = %v, want 2.5", res.Value)
	}
	if res.Breakdown["deployed_pr_count"] != 2 {
		t.Errorf("deployed count = %v, want 2", res.Breakdown["deployed_pr_count"])
	}
	if res.Breakdown["merged_not_deployed"] != 1 {
		t.Errorf("merged_not_deployed = %v, want 1", res.Breakdown["merged_not_deployed"])
	}
}

func TestNeedsFor_Union(t *testing.T) {
	got := needsFor([]Metric{prsOpened{}, reviewsPerReviewer{}, deploymentFrequency{}})
	if !got.Reviews || !got.Comments || !got.Deployments {
		t.Errorf("union needs = %+v, want all true", got)
	}
	// prsOpened alone needs nothing extra.
	if n := needsFor([]Metric{prsOpened{}}); n.Reviews || n.Comments || n.Deployments {
		t.Errorf("prsOpened needs = %+v, want all false", n)
	}
}
