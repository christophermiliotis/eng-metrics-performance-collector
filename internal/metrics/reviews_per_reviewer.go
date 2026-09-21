package metrics

import "context"

func init() { Register(&reviewsPerReviewer{}) }

// reviewsPerReviewer counts, per reviewer, the number of PRs they reviewed
// within the window. A reviewer is credited with reviewing a PR if they either
// submitted an APPROVED review or otherwise engaged by leaving at least one
// comment (a COMMENTED/CHANGES_REQUESTED review, or a conversation comment).
//
// Crediting is per (reviewer, PR): reviewing the same PR three times counts
// once. The PR author is never credited for reviewing their own PR. A PR is
// in-window by the review/comment activity time, so review work is attributed
// to when it happened.
type reviewsPerReviewer struct{}

func (reviewsPerReviewer) ID() string    { return "reviews_per_reviewer" }
func (reviewsPerReviewer) Title() string { return "PR reviews per reviewer" }

// Needs reviews and conversation comments per PR.
func (reviewsPerReviewer) Needs() DataNeeds { return DataNeeds{Reviews: true, Comments: true} }

func (reviewsPerReviewer) Compute(_ context.Context, w *Window) (Result, error) {
	// reviewer -> set of PR numbers they reviewed in-window.
	credited := map[string]map[int]struct{}{}

	credit := func(reviewer string, pr int) {
		if credited[reviewer] == nil {
			credited[reviewer] = map[int]struct{}{}
		}
		credited[reviewer][pr] = struct{}{}
	}

	for _, p := range w.PullRequests {
		author := p.PR.User.Login
		num := p.PR.Number

		for _, r := range p.Reviews {
			if r.User.Login == "" || r.User.Login == author || isBot(r.User.Login) {
				continue
			}
			if r.SubmittedAt.Before(w.From) || r.SubmittedAt.After(w.To) {
				continue
			}
			// APPROVED counts; COMMENTED / CHANGES_REQUESTED count as "left at
			// least one comment". DISMISSED does not represent active review.
			switch r.State {
			case "APPROVED", "COMMENTED", "CHANGES_REQUESTED":
				credit(r.User.Login, num)
			}
		}

		for _, c := range p.Comments {
			if c.User.Login == "" || c.User.Login == author || isBot(c.User.Login) {
				continue
			}
			if c.CreatedAt.Before(w.From) || c.CreatedAt.After(w.To) {
				continue
			}
			credit(c.User.Login, num)
		}
	}

	byReviewer := map[string]float64{}
	var total float64
	for reviewer, prs := range credited {
		byReviewer[reviewer] = float64(len(prs))
		total += float64(len(prs))
	}

	return Result{
		ID:        "reviews_per_reviewer",
		Title:     "PR reviews per reviewer",
		Unit:      "count",
		Value:     total,
		Breakdown: byReviewer,
		Notes:     "Per reviewer: number of distinct PRs they approved or commented on within the window. Self-reviews and bot reviewers excluded.",
	}, nil
}
