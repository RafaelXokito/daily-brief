package paper

import (
	"errors"
	"fmt"
	"path/filepath"
)

var (
	// ErrNoBudget: the run has no usable token budget (exit 2).
	ErrNoBudget = errors.New("paper: no usable token budget")
	// ErrConfig: the run is missing something it needs, such as a
	// deliverer, and was refused before spending tokens (exit 2, like
	// ErrNoBudget: both are configuration errors).
	ErrConfig = errors.New("paper: configuration error")
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
	case errors.Is(err, ErrNoBudget), errors.Is(err, ErrConfig):
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

	deliveryFailed bool // delivery was tried and failed
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
		return fmt.Sprintf("paper %s: no tokens spent, %s", r.Date, r.notDelivered())
	}
	tokens := fmt.Sprintf("%s of %s tokens", HumanTokens(r.Spent), HumanTokens(r.Budget))
	if r.Over > 0 {
		tokens += fmt.Sprintf(" (over by %s)", HumanTokens(r.Over))
	}
	end := r.notDelivered()
	if r.Delivered {
		end = "delivered as " + name
	}
	return fmt.Sprintf("paper %s: %s, %d stories, %d of %d sources summarised, %d editor fallbacks, %s",
		r.Date, tokens, r.Stories, r.Summarised, r.Summarised+r.Failed, r.Fallbacks, end)
}

func (r Report) notDelivered() string {
	if r.deliveryFailed {
		return "delivery failed, saved to " + r.File
	}
	return "saved to " + r.File
}
