// Package brief researches topics with Claude and stores the daily journal.
package brief

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
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
	// Allocated is the sum of the allocations of the section's passes: its
	// first pass plus any top-up and narrow passes, so it may exceed the
	// topic's share, or even the run's budget, as each top-up is allocated
	// from what is free when it starts. While the topic waits to start it is
	// an estimate of its share. Journals saved before it was renamed call it
	// "budget".
	Allocated int64  `json:"allocated"`
	Error     string `json:"error,omitempty"`
	// Truncated is set when the topic ran out of budget before finishing.
	Truncated bool `json:"truncated,omitempty"`
	// Passes counts top-up passes that deepened or widened the digest.
	Passes int `json:"passes,omitempty"`
	// Stale marks an overview kept from before some topics were refreshed.
	Stale bool `json:"stale,omitempty"`
	// Highlights are the overview's entries, each an item of a topic's
	// digest; empty for a topic, or an overview whose reply was not JSON.
	Highlights []Highlight `json:"highlights,omitempty"`
}

// UnmarshalJSON also reads the "budget" of journals saved before it was
// renamed to "allocated".
func (s *Section) UnmarshalJSON(data []byte) error {
	type plain Section
	var v struct {
		plain
		Budget int64 `json:"budget"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*s = Section(v.plain)
	if s.Allocated == 0 {
		s.Allocated = v.Budget
	}
	return nil
}

// RunRecord is what one run spent toward today's journal.
type RunRecord struct {
	At time.Time `json:"at"`
	// Topics are the topics of a run over only some of them (-topics).
	Topics []string `json:"topics,omitempty"`
	Budget int64    `json:"budget"`
	// Used is everything the run spent, Failed the part of it spent on
	// topics (or the overview) that failed in that run, whether the journal
	// shows an earlier digest in their place or the failure.
	Used   int64 `json:"used"`
	Failed int64 `json:"failed,omitempty"`
	// Overshoot is how far the run went over its own budget.
	Overshoot int64 `json:"overshoot,omitempty"`
}

type Journal struct {
	Date        string    `json:"date"`
	GeneratedAt time.Time `json:"generated_at"`
	Model       string    `json:"model"`
	Provider    string    `json:"provider,omitempty"`
	// TokenBudget is the budget of the latest run. Earlier runs' budgets are
	// in Runs; they are never added up (journals saved before Runs existed
	// may hold such a sum).
	TokenBudget int64      `json:"token_budget"`
	Overview    Section    `json:"overview"`
	Sections    []*Section `json:"sections"`
	// Runs lists every run saved into today's journal, oldest first. Each
	// section's tokens, searches and passes are those of the digest it
	// shows, so a digest kept from an earlier run keeps that run's figures,
	// and what a later run spent on it is counted in that run's record.
	Runs []RunRecord `json:"runs,omitempty"`
	// BudgetLevel and BudgetWarnings come from Assess when the run started.
	BudgetLevel    Level    `json:"budget_level,omitempty"`
	BudgetWarnings []string `json:"budget_warnings,omitempty"`
	// Overshoot is how far the latest run went over its budget, which its
	// safety margins make very unlikely but cannot rule out (see Run).
	// Earlier runs' overshoot is in their run records (see EarlierOver).
	Overshoot int64 `json:"overshoot,omitempty"`
	// Subset is set on a run over only some of the configured topics:
	// saving merges it into today's journal instead of replacing it.
	Subset bool `json:"-"`
	// Caps are the current item caps of every configured topic. Saving
	// fits what it keeps from earlier today to them; nil keeps it as is.
	Caps map[string]int `json:"-"`
}

// RunUsed is what the latest run spent: as recorded when it was saved, or
// as spent so far while it runs (its journal then holds only its own
// sections).
func (j *Journal) RunUsed() int64 {
	if n := len(j.Runs); n > 0 {
		return j.Runs[n-1].Used
	}
	return j.TokensUsed()
}

// RunFailed is what the latest run spent on topics that failed in it.
func (j *Journal) RunFailed() int64 {
	if n := len(j.Runs); n > 0 {
		return j.Runs[n-1].Failed
	}
	return failedSpend(j)
}

// TodayUsed is what every run saved into today's journal spent.
func (j *Journal) TodayUsed() int64 {
	if len(j.Runs) == 0 {
		return j.TokensUsed()
	}
	var total int64
	for _, r := range j.Runs {
		total += r.Used
	}
	return total
}

// Utilization is the share of the latest run's budget it spent, in percent.
func (j *Journal) Utilization() float64 {
	if j.TokenBudget == 0 {
		return 0
	}
	return 100 * float64(j.RunUsed()) / float64(j.TokenBudget)
}

// SpendSummary describes the latest run's spend against its budget and,
// when several runs were saved today, what they spent together.
func (j *Journal) SpendSummary() string {
	s := fmt.Sprintf("%s / %s tokens (%.0f%%)", HumanTokens(j.RunUsed()), HumanTokens(j.TokenBudget), j.Utilization())
	if f := j.RunFailed(); f > 0 {
		s += fmt.Sprintf(", %s on failed topics", HumanTokens(f))
	}
	if n := len(j.Runs); n > 1 {
		s += fmt.Sprintf(" · %s today in %d runs", HumanTokens(j.TodayUsed()), n)
		if over := j.EarlierOver(); over > 0 {
			s += fmt.Sprintf(" · %s over earlier today", HumanTokens(over))
		}
	}
	return s
}

// Over is how far the latest run went over its own budget: as recorded
// when it finished, or as spent so far while it runs.
func (j *Journal) Over() int64 {
	if n := len(j.Runs); n > 0 {
		r := j.Runs[n-1]
		return max(r.Overshoot, r.Used-r.Budget)
	}
	return max(j.Overshoot, j.RunUsed()-j.TokenBudget)
}

// EarlierOver is how far the runs saved today before the latest went over
// their own budgets, together.
func (j *Journal) EarlierOver() int64 {
	var over int64
	for k := 0; k < len(j.Runs)-1; k++ {
		r := j.Runs[k]
		over += max(0, r.Overshoot, r.Used-r.Budget)
	}
	return over
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
		b.WriteString("> **Warning:** " + note + "\n\n")
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

// Reported is an item an earlier day's journal reported, which research
// passes are told not to repeat unless it has developed (see taskPrompt).
type Reported struct {
	Date     string // YYYY-MM-DD
	Headline string
	URL      string // as written, or "" if the item had none
}

const (
	// Most entries, and about the most characters of rendered lines, of one
	// topic's history.
	maxHistoryItems = 12
	maxHistoryChars = 1_500
	// Longest headline and link a history line shows.
	maxHistoryHeadline = 100
	maxHistoryLink     = 80
)

// LoadHistory returns, for each of topics, the items its finished digests
// reported on the days calendar days before today (a YYYY-MM-DD date), most
// recent first: never today's journal or older ones. Items are recognised
// as capItems does. An item reported on several days with the same link
// and headline (compared normalised), or without a link the same headline,
// is listed once, at its latest date: stories sharing a link, such as a
// releases page, stay apart. Each
// topic keeps at most 12 entries and about 1,500 characters of rendered
// lines. Missing, unreadable or corrupt journals are skipped silently.
func LoadHistory(today string, days int, topics []string) map[string][]Reported {
	day, err := time.Parse("2006-01-02", today)
	if err != nil || days <= 0 || len(topics) == 0 {
		return nil
	}
	out := map[string][]Reported{}
	seen := map[string]map[string]bool{}
	size := map[string]int{}
	full := map[string]bool{}
	for d := 1; d <= days; d++ {
		date := day.AddDate(0, 0, -d).Format("2006-01-02")
		j, err := readJournal(journalPath(date))
		if err != nil || j == nil {
			continue
		}
		for _, s := range j.Sections {
			if s == nil || s.Status != StatusDone || !slices.Contains(topics, s.Topic) {
				continue
			}
			if seen[s.Topic] == nil {
				seen[s.Topic] = map[string]bool{}
			}
			for _, b := range splitDigest(s.Markdown) {
				if !b.item {
					continue
				}
				r := Reported{Date: date, Headline: itemHeadline(b.text), URL: itemURL(b.text, s.Sources)}
				key := normalizeURL(r.URL) + "|" + strings.Join(words(r.Headline), " ")
				if r.Headline == "" || seen[s.Topic][key] || full[s.Topic] {
					continue
				}
				seen[s.Topic][key] = true
				n := len(r.line()) + 1
				if len(out[s.Topic]) == maxHistoryItems || size[s.Topic]+n > maxHistoryChars {
					// Older items would be listed after this one: stop here.
					full[s.Topic] = true
					continue
				}
				size[s.Topic] += n
				out[s.Topic] = append(out[s.Topic], r)
			}
		}
	}
	return out
}

// line renders r for a prompt: "Mon 27 Oct · headline · host/path",
// with angle brackets neutralised so it cannot close or open a tag.
func (r Reported) line() string {
	l := r.Headline
	if d, err := time.Parse("2006-01-02", r.Date); err == nil {
		l = d.Format("Mon 2 Jan") + " · " + l
	}
	if u := normalizeURL(r.URL); u != "" {
		l += " · " + clip(u, maxHistoryLink)
	}
	return untag(l)
}

// untag replaces s's angle brackets with look-alikes, so text from a model
// or the web placed inside a prompt's tags cannot close or open one.
func untag(s string) string {
	return tagReplacer.Replace(s)
}

var tagReplacer = strings.NewReplacer("<", "‹", ">", "›")

// clip cuts s to at most n runes, marking a cut with "…".
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

var (
	// mdLinkRE matches [text](url) links; the URL may hold balanced
	// parentheses, as Wikipedia's do.
	mdLinkRE = regexp.MustCompile(`\[([^\]]*)\]\(((?:[^()\s]|\([^()\s]*\))+)\)`)
	boldRE   = regexp.MustCompile(`^\*\*(.+?)\*\*`)
	// trailingLinksRE matches the source links that end a line.
	trailingLinksRE = regexp.MustCompile(`(?:\s*\[[^\]]*\]\((?:[^()\s]|\([^()\s]*\))+\))+\s*$`)
)

// Updates counts the section's items written as updates to news an
// earlier day reported: lines SplitUpdate recognises, which the TUI marks.
func (s *Section) Updates() int {
	n := 0
	for _, l := range strings.Split(s.Markdown, "\n") {
		if _, _, ok := SplitUpdate(l); ok {
			n++
		}
	}
	return n
}

// updateLineRE matches the start of an item written as an update: a level
// 2-4 heading, a bullet or a bold line headed "Update:", bold or not.
var updateLineRE = regexp.MustCompile(`(?i)^(#{2,4} +|(?:[-*+]|\d+[.)]) +)?(\*\*)?\s*update\s*:\s*(\*\*)?\s*`)

// SplitUpdate splits a digest line that starts an item written as an
// update into its heading or bullet marker and its headline, without the
// "Update:" prefix or its bold markers, and reports whether it is one.
func SplitUpdate(line string) (lead, headline string, ok bool) {
	m := updateLineRE.FindStringSubmatchIndex(line)
	if m == nil {
		return "", "", false
	}
	open, closed := m[4] >= 0, m[6] >= 0
	if m[2] < 0 && !open {
		// A plain "Update:" line is not an item.
		return "", "", false
	}
	if m[2] >= 0 {
		lead = line[m[2]:m[3]]
	}
	headline = line[m[1]:]
	if open && !closed {
		// "**Update: headline**": drop the bold's closing marker.
		headline = strings.Replace(headline, "**", "", 1)
	}
	return lead, headline, true
}

// updateRE matches an "Update:" prefix, bold or not.
var updateRE = regexp.MustCompile(`(?i)^(\*\*)?\s*update\s*:\s*(\*\*)?\s*`)

// itemLine is an item's first line without heading, bullet or bold markers
// or an "Update:" prefix, and whether it had one.
func itemLine(item string) (string, bool) {
	line, _, _ := strings.Cut(strings.TrimSpace(item), "\n")
	line = strings.TrimSpace(strings.TrimLeft(line, "#"))
	if isBullet(line) {
		_, line, _ = strings.Cut(line, " ")
	}
	line = strings.TrimSpace(line)
	m := updateRE.FindStringSubmatchIndex(line)
	if m != nil {
		// Keep the bold of "**Update: headline**".
		bold := m[2] >= 0 && m[4] < 0
		line = line[m[1]:]
		if bold {
			line = "**" + line
		}
	}
	if b := boldRE.FindStringSubmatch(line); b != nil {
		line = b[1]
	}
	return strings.TrimSpace(strings.ReplaceAll(line, "**", "")), m != nil
}

// itemHeadline is an item's first line without its heading, bullet or bold
// markers, an "Update:" prefix or the source links that end it; links
// within it keep their text.
func itemHeadline(item string) string {
	return clip(fullHeadline(item), maxHistoryHeadline)
}

// fullHeadline is itemHeadline, uncut.
func fullHeadline(item string) string {
	line, _ := itemLine(item)
	if loc := trailingLinksRE.FindStringIndex(line); loc != nil && loc[0] > 0 {
		line = line[:loc[0]]
	}
	line = mdLinkRE.ReplaceAllString(line, "$1")
	return strings.TrimRight(line, " :—-")
}

// itemURL is the first link in an item, else the first source whose title
// contains the item's headline, or is contained in it, word for word; else
// "". Sources are listed apart from the items, so none is matched by
// position.
func itemURL(item string, sources []Source) string {
	if m := mdLinkRE.FindStringSubmatch(item); m != nil {
		return m[2]
	}
	h := words(itemHeadline(item))
	for _, s := range sources {
		if t := words(s.Title); len(t) > 0 && len(h) > 0 && (containsWords(t, h) || containsWords(h, t)) {
			return s.URL
		}
	}
	return ""
}

// words are s's words and numbers, lowercase.
func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
}

// containsWords reports whether sub appears in ws as a run of whole words.
func containsWords(ws, sub []string) bool {
	for i := 0; i+len(sub) <= len(ws); i++ {
		if slices.Equal(ws[i:i+len(sub)], sub) {
			return true
		}
	}
	return false
}

// normalizeURL is the form links are compared in, also shown in prompts:
// host (lowercase, without www. or a default port) and path, without the
// scheme, fragment, utm_* parameters or a trailing slash. It is "" for
// anything but an http(s) URL with a host.
func normalizeURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
	host = strings.TrimSuffix(strings.TrimSuffix(host, ":443"), ":80")
	q := u.Query()
	for k := range q {
		if strings.HasPrefix(strings.ToLower(k), "utm_") {
			q.Del(k)
		}
	}
	out := strings.TrimSuffix(host+u.EscapedPath(), "/")
	if e := q.Encode(); e != "" {
		out += "?" + e
	}
	return out
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
// failed overview keeps the earlier one, marked as possibly stale. What
// is kept keeps its own tokens, searches and passes; j's run is added to
// the journal's run records with what it spent, and on what failed. j is
// updated to what was saved, and not saved at all if no topic is done.
//
// The journal is written to a temporary file and renamed into place, so a
// crash never leaves it half-written. A saved journal that cannot be parsed
// is not merged or silently replaced: it is first moved aside to
// <date>.json.bak (or .bak.2 and so on, never over an earlier backup), and
// stderr says so. One that cannot be read at all fails the save.
func SaveJournal(j *Journal) error {
	// A run's own journal has no run records yet: record what it spent
	// before merging changes its sections.
	own := len(j.Runs) == 0
	if own {
		j.Runs = []RunRecord{j.record()}
	}
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
		if own {
			j.Runs = append(prev.runs(), j.Runs...)
		}
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

// record is the run record of a run's own journal.
func (j *Journal) record() RunRecord {
	r := RunRecord{At: j.GeneratedAt, Budget: j.TokenBudget, Used: j.TokensUsed(), Failed: failedSpend(j), Overshoot: j.Overshoot}
	if j.Subset {
		for _, s := range j.Sections {
			r.Topics = append(r.Topics, s.Topic)
		}
	}
	return r
}

// runs are j's run records; a journal saved before they existed counts as
// one run that spent what its sections show.
func (j *Journal) runs() []RunRecord {
	if len(j.Runs) > 0 {
		return j.Runs
	}
	return []RunRecord{{At: j.GeneratedAt, Budget: j.TokenBudget, Used: j.TokensUsed(), Overshoot: j.Overshoot}}
}

// failedSpend is what a run's own journal spent on topics, and the
// overview, that failed.
func failedSpend(j *Journal) int64 {
	var n int64
	for _, s := range append([]*Section{&j.Overview}, j.Sections...) {
		if s.Status == StatusFailed {
			n += s.TokensUsed
		}
	}
	return n
}

// merge folds prev into j. Whatever j keeps from prev keeps prev's tokens,
// searches and passes; what j's run spent on topics that failed is in its
// run record (see SaveJournal), not added to them.
func merge(j, prev *Journal) {
	refreshed := j.done()
	fitCaps(prev, j.Caps)
	keepEarlier(j, prev)
	if j.Subset {
		j.Sections = combine(j.Sections, prev.Sections)
	}
	if j.Overview.Status != StatusDone && prev.Overview.Status == StatusDone {
		ov := prev.Overview
		ov.Stale = ov.Stale || refreshed
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
// has one, noting the failure. The section keeps prev's tokens, searches,
// passes and allocation, the figures of the digest it shows. Topics the
// reading cap skipped stay empty.
func keepEarlier(j, prev *Journal) {
	for _, s := range j.Sections {
		if s.Status != StatusFailed || s.Error == skippedRead {
			continue
		}
		for _, p := range prev.Sections {
			if p.Topic == s.Topic && p.Status == StatusDone {
				failed := s.Error
				*s = *p
				s.Error = "refreshing failed (" + failed + "); showing the earlier digest"
				break
			}
		}
	}
}
