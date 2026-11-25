package paper

import (
	"errors"
	"fmt"
	"path/filepath"
)

var (
	// ErrNoBudget: the run has no usable token budget (exit 2).
	ErrNoBudget = errors.New("paper: no usable token budget")
	// ErrNoStories: research produced no story, so nothing was built or
	// delivered and yesterday's paper stays (exit 3).
	ErrNoStories = errors.New("paper: no stories today; nothing delivered, yesterday's paper stays")
	// ErrDelivery: the book was built and kept locally but did not reach
	// the reader (exit 4).
	ErrDelivery = errors.New("paper: delivery failed; the book is kept locally")
)

// ExitCode maps a Run error to the process exit code.
func ExitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrNoBudget):
		return 2
	case errors.Is(err, ErrNoStories):
		return 3
	case errors.Is(err, ErrDelivery):
		return 4
	}
	return 1
}

// Report is what one paper run did. File is the book's path; Summarised
// and Failed count distinct source URLs whose article was or was not
// fetched.
type Report struct {
	Date                                   string
	Spent, Budget, Over                    int64
	Stories, Summarised, Failed, Fallbacks int
	File                                   string
	Delivered                              bool
	Action                                 Action
	Warnings                               []string
}

// Line is the run's one-line summary for the log.
func (r Report) Line() string {
	name := filepath.Base(r.File)
	switch r.Action {
	case Skip:
		return fmt.Sprintf("paper %s: already delivered as %s", r.Date, name)
	case Redeliver:
		if r.Delivered {
			return fmt.Sprintf("paper %s: no tokens spent, delivered as %s (built earlier)", r.Date, name)
		}
		return fmt.Sprintf("paper %s: no tokens spent, not delivered, saved to %s", r.Date, r.File)
	}
	tokens := fmt.Sprintf("%s of %s tokens", humanTokens(r.Spent), humanTokens(r.Budget))
	if r.Over > 0 {
		tokens += fmt.Sprintf(" (over by %s)", humanTokens(r.Over))
	}
	end := "saved to " + r.File
	if r.Delivered {
		end = "delivered as " + name
	}
	return fmt.Sprintf("paper %s: %s, %d stories, %d of %d sources summarised, %d editor fallbacks, %s",
		r.Date, tokens, r.Stories, r.Summarised, r.Summarised+r.Failed, r.Fallbacks, end)
}
