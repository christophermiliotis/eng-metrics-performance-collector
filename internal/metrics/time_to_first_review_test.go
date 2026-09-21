package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/soundcloud/engineering-performance-metrics/internal/github"
)

func TestTimeToFirstReview_ExcludesBotsAndAuthor(t *testing.T) {
	w := newWindow()

	opened := winFrom.Add(24 * time.Hour)
	// A bot comments 1h after open, the author self-comments at 2h, and a human
	// reviews at 5h. First *human* review should be measured at 5h.
	w.PullRequests = []PRWithActivity{
		{
			PR: github.PullRequest{Number: 1, User: github.User{Login: "author"}, CreatedAt: opened},
			Comments: []github.IssueComment{
				{User: github.User{Login: "gemini-code-assist[bot]"}, CreatedAt: opened.Add(1 * time.Hour)},
				{User: github.User{Login: "author"}, CreatedAt: opened.Add(2 * time.Hour)},
			},
			Reviews: []github.Review{
				{User: github.User{Login: "alice"}, State: "COMMENTED", SubmittedAt: opened.Add(5 * time.Hour)},
			},
		},
	}

	res, err := timeToFirstReview{}.Compute(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	// Single PR: every statistic (P75/median/mean) collapses to 5h.
	if res.Value != 5 {
		t.Errorf("headline (P75) hours = %v, want 5 (bot + author comments must be ignored)", res.Value)
	}
	if res.Breakdown["median_hours"] != 5 {
		t.Errorf("median hours = %v, want 5", res.Breakdown["median_hours"])
	}
	if res.Breakdown["reviewed_pr_count"] != 1 {
		t.Errorf("reviewed count = %v, want 1", res.Breakdown["reviewed_pr_count"])
	}
}

func TestTimeToFirstReview_P75Headline(t *testing.T) {
	w := newWindow()
	opened := winFrom.Add(24 * time.Hour)
	// 9 PRs (>= minSampleForP75) with latencies 1..8 and one 100h tail.
	// Sorted: [1,2,3,4,5,6,7,8,100], ranks 0..8. Median (rank 4) = 5.
	// P75: rank = 0.75*8 = 6.0 -> value at index 6 = 7. Distinct from median.
	lat := []int{1, 2, 3, 4, 5, 6, 7, 8, 100}
	for i, h := range lat {
		w.PullRequests = append(w.PullRequests, PRWithActivity{
			PR: github.PullRequest{Number: i + 1, User: github.User{Login: "author"}, CreatedAt: opened},
			Reviews: []github.Review{
				{User: github.User{Login: "alice"}, State: "COMMENTED", SubmittedAt: opened.Add(time.Duration(h) * time.Hour)},
			},
		})
	}

	res, err := timeToFirstReview{}.Compute(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	if res.Value != 7 {
		t.Errorf("headline (P75) = %v, want 7", res.Value)
	}
	if res.Breakdown["p75_hours"] != 7 {
		t.Errorf("p75_hours = %v, want 7", res.Breakdown["p75_hours"])
	}
	if res.Breakdown["median_hours"] != 5 {
		t.Errorf("median_hours = %v, want 5", res.Breakdown["median_hours"])
	}
	if res.Breakdown["headline_is_p75"] != 1 {
		t.Errorf("headline_is_p75 = %v, want 1", res.Breakdown["headline_is_p75"])
	}
}

func TestTimeToFirstReview_SmallSampleFallsBackToMedian(t *testing.T) {
	w := newWindow()
	opened := winFrom.Add(24 * time.Hour)
	// Only 5 reviewed PRs (< minSampleForP75) -> headline must be the median.
	// Sorted [1,2,3,4,20]: median=3, P75 = value at rank 3.0 = 4.
	lat := []int{1, 2, 3, 4, 20}
	for i, h := range lat {
		w.PullRequests = append(w.PullRequests, PRWithActivity{
			PR: github.PullRequest{Number: i + 1, User: github.User{Login: "author"}, CreatedAt: opened},
			Reviews: []github.Review{
				{User: github.User{Login: "alice"}, State: "COMMENTED", SubmittedAt: opened.Add(time.Duration(h) * time.Hour)},
			},
		})
	}

	res, err := timeToFirstReview{}.Compute(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	if res.Value != 3 {
		t.Errorf("headline = %v, want 3 (median fallback on small sample)", res.Value)
	}
	if res.Breakdown["headline_is_p75"] != 0 {
		t.Errorf("headline_is_p75 = %v, want 0 (fell back to median)", res.Breakdown["headline_is_p75"])
	}
	// P75 is still recorded in the breakdown even when not the headline.
	if res.Breakdown["p75_hours"] != 4 {
		t.Errorf("p75_hours = %v, want 4", res.Breakdown["p75_hours"])
	}
}

func TestTimeToFirstReview_EarliestSignalWins(t *testing.T) {
	w := newWindow()
	opened := winFrom.Add(24 * time.Hour)
	// A human comment at 3h precedes a formal review at 8h -> first review = 3h.
	w.PullRequests = []PRWithActivity{
		{
			PR: github.PullRequest{Number: 2, User: github.User{Login: "author"}, CreatedAt: opened},
			Comments: []github.IssueComment{
				{User: github.User{Login: "bob"}, CreatedAt: opened.Add(3 * time.Hour)},
			},
			Reviews: []github.Review{
				{User: github.User{Login: "alice"}, State: "APPROVED", SubmittedAt: opened.Add(8 * time.Hour)},
			},
		},
	}

	res, err := timeToFirstReview{}.Compute(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	if res.Value != 3 {
		t.Errorf("median hours = %v, want 3 (earliest human signal wins)", res.Value)
	}
}

func TestTimeToFirstReview_AwaitingReviewExcluded(t *testing.T) {
	w := newWindow()
	opened := winFrom.Add(24 * time.Hour)

	w.PullRequests = []PRWithActivity{
		// Reviewed by a human at 4h.
		{
			PR: github.PullRequest{Number: 1, User: github.User{Login: "author"}, CreatedAt: opened},
			Reviews: []github.Review{
				{User: github.User{Login: "alice"}, State: "COMMENTED", SubmittedAt: opened.Add(4 * time.Hour)},
			},
		},
		// Only a bot has touched it -> awaiting human review, excluded from median.
		{
			PR: github.PullRequest{Number: 2, User: github.User{Login: "author"}, CreatedAt: opened},
			Comments: []github.IssueComment{
				{User: github.User{Login: "gemini-code-assist[bot]"}, CreatedAt: opened.Add(1 * time.Hour)},
			},
		},
		// A DISMISSED review does not count as a first-touch review.
		{
			PR: github.PullRequest{Number: 3, User: github.User{Login: "author"}, CreatedAt: opened},
			Reviews: []github.Review{
				{User: github.User{Login: "carol"}, State: "DISMISSED", SubmittedAt: opened.Add(2 * time.Hour)},
			},
		},
	}

	res, err := timeToFirstReview{}.Compute(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	if res.Value != 4 {
		t.Errorf("median hours = %v, want 4", res.Value)
	}
	if res.Breakdown["reviewed_pr_count"] != 1 {
		t.Errorf("reviewed count = %v, want 1", res.Breakdown["reviewed_pr_count"])
	}
	if res.Breakdown["awaiting_first_review"] != 2 {
		t.Errorf("awaiting = %v, want 2 (bot-only PR + DISMISSED-only PR)", res.Breakdown["awaiting_first_review"])
	}
}

func TestIsBot(t *testing.T) {
	cases := map[string]bool{
		"gemini-code-assist[bot]": true,
		"dependabot[bot]":         true,
		"  renovate[bot] ":        true, // whitespace tolerant
		"Some-App[BOT]":           true, // case-insensitive
		"alice":                   false,
		"bob[bot]-not-suffix":     false, // "[bot]" only matched as a suffix
		"":                        false,
	}
	for in, want := range cases {
		if got := isBot(in); got != want {
			t.Errorf("isBot(%q) = %v, want %v", in, got, want)
		}
	}
}
