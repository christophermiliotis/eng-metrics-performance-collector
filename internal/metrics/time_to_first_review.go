package metrics

import (
	"context"
	"fmt"
	"time"
)

func init() { Register(&timeToFirstReview{}) }

// timeToFirstReview measures how long a PR waits for its first human review:
// from when a non-draft PR is opened until the first review signal from a
// non-author, non-bot user. This is the "PR waiting for first review" latency,
// a leading indicator of review responsiveness.
//
// The first review signal is the earliest of:
//   - a submitted PR review (APPROVED / COMMENTED / CHANGES_REQUESTED), or
//   - a conversation (issue) comment,
//
// from a user who is neither the PR author nor in the ignored-commenters list
// (bots such as gemini-code-assist). DISMISSED reviews are ignored as they are
// not first-touch review activity.
//
// A PR is included when it was opened within the window and has received at
// least one qualifying human review. PRs opened in-window but still awaiting a
// first human review are reported as a separate count (they have no measurable
// value yet) rather than skewing the median.
//
// The headline Value is the P75 hours-to-first-review: it surfaces the slow
// tail (PRs left waiting) that a median would hide, which is the more useful
// signal for review-responsiveness. On small samples (fewer than
// minSampleForP75 reviewed PRs) P75 is dominated by a single tail value, so the
// headline falls back to the median; breakdown key "headline_is_p75" records
// which was used. Median and mean are always kept in the breakdown for
// reference, alongside a per-PR series.
//
// Caveats (documented in Notes):
//   - GitHub's PR list payload does not expose the draft->ready transition, so
//     for PRs opened as drafts we measure from created_at.
//   - Standalone inline diff comments not attached to a submitted review live on
//     a separate API the tool does not fetch; a review submitted with inline
//     comments is still captured via its submitted_at, covering the common case.
type timeToFirstReview struct{}

// minSampleForP75 is the number of reviewed PRs required before P75 is used as
// the headline. Below it, P75 is dominated by a single tail value (its
// statistical floor is 1/(1-0.75) = 4), so the metric falls back to the median,
// which is stable on small samples. 8 gives one doubling of headroom over that
// floor. For a healthy 2-week sprint the reviewed-PR count is normally well
// above this, so the fallback should rarely trigger — it guards quiet sprints,
// holiday weeks, or a narrowed lookback window.
const minSampleForP75 = 8

func (timeToFirstReview) ID() string    { return "time_to_first_review_hours" }
func (timeToFirstReview) Title() string { return "Time to first review (hours)" }

// Needs reviews and conversation comments per PR.
func (timeToFirstReview) Needs() DataNeeds { return DataNeeds{Reviews: true, Comments: true} }

func (timeToFirstReview) Compute(_ context.Context, w *Window) (Result, error) {
	var hours []float64
	var series []DataPoint
	var awaitingReview int

	for _, p := range w.PullRequests {
		pr := p.PR
		// Only PRs opened within the window, measured from when they were opened.
		if pr.CreatedAt.Before(w.From) || pr.CreatedAt.After(w.To) {
			continue
		}
		author := pr.User.Login

		first, ok := firstHumanReviewAt(p, author)
		if !ok {
			awaitingReview++
			continue
		}
		h := first.Sub(pr.CreatedAt).Hours()
		if h < 0 {
			continue // clock skew / data anomaly; skip defensively
		}
		hours = append(hours, h)
		series = append(series, DataPoint{
			Label: fmt.Sprintf("#%d", pr.Number),
			Value: round2(h),
		})
	}

	p75 := round2(percentileOf(hours, 75))
	median := round2(medianOf(hours))
	mean := round2(meanOf(hours))

	// Headline is P75, but on small samples P75 is dominated by a single tail
	// value, so fall back to the median for stability. `headlineStat` records
	// which was used so downstream (Grafana / summary) can label it correctly.
	n := len(hours)
	headline := p75
	headlineStat := "p75"
	if n < minSampleForP75 {
		headline = median
		headlineStat = "median"
	}

	var notes string
	if n < minSampleForP75 {
		notes = fmt.Sprintf(
			"Median across %d PRs opened in-window that received a first human review (P75=%.2f, mean=%.2f). "+
				"Headline fell back to median because only %d PR(s) were reviewed (<%d needed for a stable P75). "+
				"%d PR(s) opened in-window still awaiting a first human review (excluded). "+
				"Measured open->first review; bot commenters excluded; for PRs opened as drafts, from created_at.",
			n, p75, mean, n, minSampleForP75, awaitingReview)
	} else {
		notes = fmt.Sprintf(
			"P75 across %d PRs opened in-window that received a first human review (median=%.2f, mean=%.2f). "+
				"%d PR(s) opened in-window still awaiting a first human review (excluded). "+
				"Measured open->first review; bot commenters excluded; for PRs opened as drafts, from created_at.",
			n, median, mean, awaitingReview)
	}

	res := Result{
		ID:     "time_to_first_review_hours",
		Title:  "Time to first review (hours)",
		Unit:   "hours",
		Value:  headline,
		Series: series,
		Notes:  notes,
	}
	if res.Breakdown == nil {
		res.Breakdown = map[string]float64{}
	}
	res.Breakdown["p75_hours"] = p75
	res.Breakdown["median_hours"] = median
	res.Breakdown["mean_hours"] = mean
	res.Breakdown["reviewed_pr_count"] = float64(n)
	res.Breakdown["awaiting_first_review"] = float64(awaitingReview)
	// headline_is_p75 = 1 when the headline used P75, 0 when it fell back to the
	// median (small sample). Lets a dashboard flag which statistic is shown.
	if headlineStat == "p75" {
		res.Breakdown["headline_is_p75"] = 1
	} else {
		res.Breakdown["headline_is_p75"] = 0
	}
	return res, nil
}

// firstHumanReviewAt returns the earliest timestamp at which a non-author,
// non-bot user reviewed or commented on the PR, and whether any such signal
// exists.
func firstHumanReviewAt(p PRWithActivity, author string) (time.Time, bool) {
	var first time.Time
	found := false

	consider := func(login string, at time.Time) {
		if login == "" || login == author {
			return
		}
		if isBot(login) {
			return
		}
		if !found || at.Before(first) {
			first = at
			found = true
		}
	}

	for _, r := range p.Reviews {
		switch r.State {
		case "APPROVED", "COMMENTED", "CHANGES_REQUESTED":
			consider(r.User.Login, r.SubmittedAt)
		}
	}
	for _, c := range p.Comments {
		consider(c.User.Login, c.CreatedAt)
	}
	return first, found
}
