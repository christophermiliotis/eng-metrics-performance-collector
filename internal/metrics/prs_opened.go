package metrics

import "context"

func init() { Register(&prsOpened{}) }

// prsOpened counts PRs opened within the window, broken down by author. A PR
// counts toward the window by its created_at time. PRs that were opened as
// drafts still count once they reflect real work; GitHub does not expose
// "became ready" time on the list payload, so we count any PR created in the
// window and note the caveat. Bot-authored PRs (e.g. dependabot, renovate) are
// excluded — we only measure human authorship.
type prsOpened struct{}

func (prsOpened) ID() string    { return "prs_opened" }
func (prsOpened) Title() string { return "Pull requests opened" }

// Needs only the PR list, no per-PR fetches.
func (prsOpened) Needs() DataNeeds { return DataNeeds{} }

func (prsOpened) Compute(_ context.Context, w *Window) (Result, error) {
	byAuthor := map[string]float64{}
	var total float64
	for _, p := range w.PullRequests {
		pr := p.PR
		if pr.CreatedAt.Before(w.From) || pr.CreatedAt.After(w.To) {
			continue
		}
		if isBot(pr.User.Login) {
			continue // exclude bot-authored PRs (dependabot, renovate, ...)
		}
		author := pr.User.Login
		if author == "" {
			author = "(unknown)"
		}
		byAuthor[author]++
		total++
	}
	return Result{
		ID:        "prs_opened",
		Title:     "Pull requests opened",
		Unit:      "count",
		Value:     total,
		Breakdown: byAuthor,
		Notes:     "Counts human-authored PRs by created_at within the window; breakdown is per author. Bot authors excluded.",
	}, nil
}
