// Package brief researches topics with Claude and stores the daily journal.
package brief

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type Status string

const (
	StatusPending Status = "pending"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

type Source struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type Section struct {
	Topic      string   `json:"topic"`
	Status     Status   `json:"status"`
	Markdown   string   `json:"markdown"`
	Sources    []Source `json:"sources,omitempty"`
	TokensUsed int64    `json:"tokens_used"`
	Searches   int64    `json:"searches"`
	Budget     int64    `json:"budget"`
	Error      string   `json:"error,omitempty"`
	// Truncated is set when the topic ran out of budget before finishing.
	Truncated bool `json:"truncated,omitempty"`
	// Passes counts top-up passes that deepened or widened the digest.
	Passes int `json:"passes,omitempty"`
	// Stale marks an overview kept from before some topics were refreshed.
	Stale bool `json:"stale,omitempty"`
}

type Journal struct {
	Date        string     `json:"date"`
	GeneratedAt time.Time  `json:"generated_at"`
	Model       string     `json:"model"`
	Provider    string     `json:"provider,omitempty"`
	TokenBudget int64      `json:"token_budget"`
	Overview    Section    `json:"overview"`
	Sections    []*Section `json:"sections"`
	// BudgetLevel and BudgetWarnings come from Assess when the run started.
	BudgetLevel    Level    `json:"budget_level,omitempty"`
	BudgetWarnings []string `json:"budget_warnings,omitempty"`
	// Overshoot is how far the run went over its budget, which its safety
	// margins make very unlikely but cannot rule out (see Run). A Subset run
	// merged into today's journal adds the earlier runs' overshoot to its
	// own, since those tokens were spent too.
	Overshoot int64 `json:"overshoot,omitempty"`
	// Subset is set on a run over only some of the configured topics:
	// saving merges it into today's journal instead of replacing it.
	Subset bool `json:"-"`
	// Caps are the current item caps of every configured topic. Saving
	// fits what it keeps from earlier today to them; nil keeps it as is.
	Caps map[string]int `json:"-"`
}

// Utilization is the share of the budget spent, in percent.
func (j *Journal) Utilization() float64 {
	if j.TokenBudget == 0 {
		return 0
	}
	return 100 * float64(j.TokensUsed()) / float64(j.TokenBudget)
}

// Over is how far the journal's run went over its budget: as recorded when
// it finished, or as spent so far while it runs.
func (j *Journal) Over() int64 {
	return max(j.Overshoot, j.TokensUsed()-j.TokenBudget)
}

// OvershootNote describes a run that went over its budget, or is "".
func (j *Journal) OvershootNote() string {
	over := j.Over()
	if over <= 0 || j.TokenBudget <= 0 {
		return ""
	}
	return fmt.Sprintf("This run went over its token budget by %s tokens (%.1f%%); it stopped starting requests as soon as it did.",
		HumanTokens(over), 100*float64(over)/float64(j.TokenBudget))
}

// ReadMinutes estimates the journal's reading time, excluding source lists.
func (j *Journal) ReadMinutes() float64 {
	text := j.Overview.Markdown
	for _, s := range j.Sections {
		text += "\n" + s.Markdown
	}
	return readMinutes(text)
}

func countWords(s string) int { return len(strings.Fields(s)) }

// countItems counts a digest's items, as capItems recognises them.
func countItems(md string) int {
	n := 0
	for _, b := range splitDigest(md) {
		if b.item {
			n++
		}
	}
	return n
}

func (j *Journal) TokensUsed() int64 {
	total := j.Overview.TokensUsed
	for _, s := range j.Sections {
		total += s.TokensUsed
	}
	return total
}

// Markdown renders the whole journal as one document, each topic under
// its own heading.
func (j *Journal) Markdown() string {
	var b strings.Builder
	b.WriteString("# Daily Brief — " + j.Date + "\n\n")
	if note := j.OvershootNote(); note != "" {
		b.WriteString("> ⚠ " + note + "\n\n")
	}
	if j.Overview.Markdown != "" {
		b.WriteString(j.Overview.Markdown + "\n\n")
	}
	for _, s := range j.Sections {
		b.WriteString("## " + s.Topic + "\n\n" + s.Render() + "\n\n")
	}
	return b.String()
}

// Render returns the section's markdown with its source list appended.
func (s *Section) Render() string {
	var b strings.Builder
	b.WriteString(s.Markdown)
	if s.Error != "" {
		b.WriteString("\n\n> **Error:** " + s.Error)
	}
	if s.Truncated {
		b.WriteString("\n\n> _Token budget for this topic ran out; the summary may be incomplete._")
	}
	if s.Stale {
		b.WriteString("\n\n> _Written before some topics were refreshed; it may be out of date._")
	}
	if len(s.Sources) > 0 {
		b.WriteString("\n\n**Sources**\n\n")
		for _, src := range s.Sources {
			title := src.Title
			if title == "" {
				title = src.URL
			}
			b.WriteString("- [" + title + "](" + src.URL + ")\n")
		}
	}
	return b.String()
}

func DataDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "daily-brief")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".daily-brief"
	}
	return filepath.Join(home, ".local", "share", "daily-brief")
}

func journalPath(date string) string {
	return filepath.Join(DataDir(), "journals", date+".json")
}

func Today() string { return time.Now().Format("2006-01-02") }

// ErrNothingDone is returned by SaveJournal for a journal with no finished
// topic.
var ErrNothingDone = errors.New("no topic was summarized; journal not saved")

// done reports whether any topic of j was summarized.
func (j *Journal) done() bool {
	for _, s := range j.Sections {
		if s.Status == StatusDone {
			return true
		}
	}
	return false
}

// LoadJournal returns the saved journal for date, or nil if there is none
// or it has no finished topic.
func LoadJournal(date string) (*Journal, error) {
	j, err := readJournal(journalPath(date))
	if err != nil || j == nil || !j.done() {
		return nil, err
	}
	return j, nil
}

// errUnreadable wraps a saved journal that exists but cannot be parsed.
var errUnreadable = errors.New("cannot be parsed")

// readJournal reads the journal at path, or nil if there is none.
func readJournal(path string) (*Journal, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var j Journal
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, fmt.Errorf("journal %s %w: %v", path, errUnreadable, err)
	}
	return &j, nil
}

// SaveJournal saves j over the journal already saved for its date, merged
// with it so nothing written earlier today is lost: topics that failed in j
// keep that journal's digest, a Subset run keeps its other topics, and a
// failed overview keeps the earlier one, marked as possibly stale. j is
// updated to what was saved, and not saved at all if no topic is done.
//
// The journal is written to a temporary file and renamed into place, so a
// crash never leaves it half-written. A saved journal that cannot be parsed
// is not merged or silently replaced: it is first moved aside to
// <date>.json.bak (or .bak.2 and so on, never over an earlier backup), and
// stderr says so. One that cannot be read at all fails the save.
func SaveJournal(j *Journal) error {
	path := journalPath(j.Date)
	prev, err := readJournal(path)
	switch {
	case errors.Is(err, errUnreadable):
		if !j.done() {
			return ErrNothingDone
		}
		bak, berr := backup(path)
		if berr != nil {
			return fmt.Errorf("%w; not overwriting it: %v", err, berr)
		}
		fmt.Fprintf(os.Stderr, "daily-brief: %v; moved it to %s and saved a new one\n", err, bak)
	case err != nil:
		return err
	case prev != nil && prev.done():
		merge(j, prev)
	}
	if !j.done() {
		return ErrNothingDone
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, data)
}

// backup moves path to the first free <path>.bak, <path>.bak.2, ... and
// returns where.
func backup(path string) (string, error) {
	for n := 1; ; n++ {
		bak := path + ".bak"
		if n > 1 {
			bak += fmt.Sprintf(".%d", n)
		}
		if _, err := os.Lstat(bak); errors.Is(err, os.ErrNotExist) {
			return bak, os.Rename(path, bak)
		} else if err != nil {
			return "", err
		}
	}
}

// writeAtomic writes data to a temporary file beside path and renames it
// over path.
func writeAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, 0o644)
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// merge folds prev into j. What j keeps from a Subset run's prev counts
// toward both its tokens and its budget, as it was spent earlier today.
func merge(j, prev *Journal) {
	refreshed := j.done()
	fitCaps(prev, j.Caps)
	keepEarlier(j, prev)
	if j.Subset {
		// Earlier runs' overshoot was spent too: keep it, added to this one's.
		j.Overshoot += prev.Overshoot
		for _, s := range combine(j.Sections, prev.Sections) {
			if !slices.Contains(j.Sections, s) {
				j.TokenBudget += s.TokensUsed
			}
		}
		j.Sections = combine(j.Sections, prev.Sections)
	}
	if j.Overview.Status != StatusDone && prev.Overview.Status == StatusDone {
		ov := prev.Overview
		ov.Stale = ov.Stale || refreshed
		if j.Subset {
			j.TokenBudget += ov.TokensUsed
			ov.TokensUsed += j.Overview.TokensUsed
		} else {
			ov.TokensUsed = j.Overview.TokensUsed
		}
		j.Overview = ov
	}
}

// combine lists today's sections after a run over own: prev's, with own's
// in place of the same topic (unless own's failed and prev's did not), then
// own's new topics.
func combine(own, prev []*Section) []*Section {
	if len(prev) == 0 {
		return own
	}
	var out []*Section
	for _, p := range prev {
		k := slices.IndexFunc(own, func(s *Section) bool { return s.Topic == p.Topic })
		if k >= 0 && (own[k].Status == StatusDone || p.Status != StatusDone) {
			out = append(out, own[k])
		} else {
			out = append(out, p)
		}
	}
	for _, s := range own {
		if !slices.ContainsFunc(prev, func(p *Section) bool { return p.Topic == s.Topic }) {
			out = append(out, s)
		}
	}
	return out
}

// fitCaps fits prev's sections to the current item caps, so what a merge
// keeps from earlier today stays within max_read_minutes: sections of topics
// no longer configured are dropped, and digests are cut to their topic's
// cap, which may be none. It does nothing if caps is nil.
func fitCaps(prev *Journal, caps map[string]int) {
	if caps == nil {
		return
	}
	prev.Sections = slices.DeleteFunc(prev.Sections, func(p *Section) bool {
		_, ok := caps[p.Topic]
		return !ok
	})
	for _, p := range prev.Sections {
		if p.Status != StatusDone {
			continue
		}
		p.Markdown = capItems(p.Markdown, caps[p.Topic])
		p.Sources = pruneSources(p.Markdown, p.Sources)
		if p.Markdown == "" {
			p.Status, p.Error, p.Sources = StatusFailed, skippedRead, nil
		}
	}
}

// keepEarlier fills topics that failed in j with prev's digest, if prev
// has one, noting the failure. Tokens stay those j spent. Topics the
// reading cap skipped stay empty.
func keepEarlier(j, prev *Journal) {
	for _, s := range j.Sections {
		if s.Status != StatusFailed || s.Error == skippedRead {
			continue
		}
		for _, p := range prev.Sections {
			if p.Topic == s.Topic && p.Status == StatusDone {
				s.Status, s.Markdown, s.Sources, s.Truncated = StatusDone, p.Markdown, p.Sources, p.Truncated
				s.Error = "refreshing failed (" + s.Error + "); showing the earlier digest"
				break
			}
		}
	}
}
