package paper

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/RafaelXokito/daily-brief/internal/brief"
	"github.com/RafaelXokito/daily-brief/internal/config"
	"github.com/RafaelXokito/daily-brief/internal/cover"
	"github.com/RafaelXokito/daily-brief/internal/editor"
	"github.com/RafaelXokito/daily-brief/internal/epub"
	"github.com/RafaelXokito/daily-brief/internal/fetch"
)

// Options are the paper command's flags. Budget, when positive, overrides
// paper.token_budget for this run; Log receives the progress lines and
// warnings (nil discards them).
type Options struct {
	Budget             int64
	Refresh, NoDeliver bool
	Log                io.Writer
}

// Deps are what a run talks to. Fetch defaults to fetch.Articles, Now to
// time.Now and Dir to brief.DataDir()/papers. Deliverer may be nil only
// with NoDeliver.
type Deps struct {
	Backend   brief.Backend
	Fetch     func(context.Context, []string) map[string]fetch.Article
	Deliverer Deliverer
	Now       func() time.Time
	Dir       string
}

func (d Deps) withDefaults() Deps {
	if d.Fetch == nil {
		d.Fetch = func(ctx context.Context, urls []string) map[string]fetch.Article {
			return fetch.Articles(ctx, urls, fetch.Options{})
		}
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Dir == "" {
		d.Dir = filepath.Join(brief.DataDir(), "papers")
	}
	return d
}

// run is one paper run in progress.
type run struct {
	ctx  context.Context
	cfg  config.Config
	o    Options
	d    Deps
	date string
	day  time.Time
	r    Report
}

func (x *run) logf(format string, args ...any) { fmt.Fprintf(x.o.Log, format+"\n", args...) }

// warn records a warning in the report and logs it.
func (x *run) warn(msg string) {
	x.r.Warnings = append(x.r.Warnings, msg)
	x.logf("warning: %s", msg)
}

// Run makes one edition end to end: check and split the budget, decide
// what today needs, then research, fetch, edit, render, save and deliver,
// recording the outcome in the local state. With no story to print it
// returns ErrNoStories and leaves the state and yesterday's paper alone.
func Run(ctx context.Context, cfg config.Config, o Options, d Deps) (Report, error) {
	if o.Log == nil {
		o.Log = io.Discard
	}
	d = d.withDefaults()
	total := cfg.Paper.TokenBudget
	if o.Budget != 0 {
		total = o.Budget
	}
	if total <= 0 {
		return Report{}, fmt.Errorf("%w: set paper.token_budget or -budget to a positive number of tokens (0, auto, is not allowed for the paper)", ErrNoBudget)
	}
	split, err := SplitBudget(total, cfg)
	if err != nil {
		return Report{}, fmt.Errorf("%w: %v", ErrNoBudget, err)
	}
	now := d.Now()
	date := now.Format("2006-01-02")
	day, _ := time.Parse("2006-01-02", date)
	x := &run{ctx: ctx, cfg: cfg, o: o, d: d, date: date, day: day, r: Report{Date: date, Budget: total}}

	s, err := LoadState(d.Dir)
	if errors.Is(err, ErrCorruptState) {
		x.warn(err.Error())
	} else if err != nil {
		return x.r, err
	}
	x.r.Action = Decide(s, date, o.Refresh, s.File != "" && exists(filepath.Join(d.Dir, s.File)))
	switch x.r.Action {
	case Skip:
		x.r.File, x.r.Delivered = filepath.Join(d.Dir, s.File), true
		x.logf("paper %s: already delivered as %s", date, s.File)
		return x.r, nil
	case Redeliver:
		return x.redeliver(s)
	}
	return x.build(s, split)
}

// redeliver resends today's book, built by an earlier run, spending no
// tokens.
func (x *run) redeliver(s State) (Report, error) {
	x.r.File = filepath.Join(x.d.Dir, s.File)
	if x.o.NoDeliver {
		return x.r, nil
	}
	data, err := os.ReadFile(x.r.File)
	if err != nil {
		return x.r, fmt.Errorf("paper: read %s: %w", s.File, err)
	}
	x.logf("redelivering %s, built earlier today", s.File)
	return x.r, x.deliver(s, data)
}

// build makes a new edition for today and delivers it.
func (x *run) build(s State, split Split) (Report, error) {
	rebuild := 0
	if x.r.Action == RebuildToday {
		// The day's first edition is implicitly number 1.
		rebuild = max(s.Rebuild, 1) + 1
	}
	name := FileName(x.cfg.Paper.TitleOrDefault(), x.date, rebuild)
	data, err := x.edition(s.Issue+1, rebuild, split)
	if err != nil {
		return x.r, err
	}
	x.r.File = filepath.Join(x.d.Dir, name)
	if err := writeFile(x.d.Dir, name, data); err != nil {
		return x.r, err
	}
	x.logf("saved %s", x.r.File)
	if x.o.NoDeliver {
		return x.r, nil
	}
	// Record the book before delivering, so a failed or interrupted
	// delivery is retried next time without spending tokens.
	next := State{Issue: s.Issue, Date: x.date, File: name, Rebuild: rebuild}
	if err := SaveState(x.d.Dir, next); err != nil {
		return x.r, err
	}
	return x.r, x.deliver(next, data)
}

// deliver sends the book named by s.File and, once it arrived, counts the
// issue and saves the state.
func (x *run) deliver(s State, data []byte) error {
	if x.d.Deliverer == nil {
		return errors.New("paper: no deliverer configured")
	}
	warnings, err := x.d.Deliverer.Deliver(x.ctx, s.File, data)
	for _, w := range warnings {
		x.warn(w)
	}
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDelivery, err)
	}
	x.r.Delivered = true
	s.Issue++
	s.Delivered = true
	return SaveState(x.d.Dir, s)
}

// edition researches (or reuses) today's journal, edits it and returns the
// finished EPUB.
func (x *run) edition(issue, rebuild int, split Split) ([]byte, error) {
	j, researchSpent := x.journal(split.Research)
	if j == nil || len(editor.BuildInput(j, nil).Chapters) == 0 {
		return nil, ErrNoStories
	}
	in := x.fetchArticles(j)
	unspent := max(0, split.Research-researchSpent)
	x.logf("editing %d chapters", len(in.Chapters))
	p := editor.Run(x.ctx, x.d.Backend, in, editor.Budget{Tokens: split.Editor + unspent, MarginPct: MarginPercent, Provider: x.cfg.Provider.Type}, x.cfg.Concurrency)

	x.r.Spent = researchSpent + p.Tokens
	x.r.Over = max(0, x.r.Spent-x.r.Budget)
	x.r.Fallbacks = p.Fallbacks
	if x.r.Over > 0 {
		x.warn(fmt.Sprintf("this paper went over its token budget by %s tokens (%s of %s)", humanTokens(x.r.Over), humanTokens(x.r.Spent), humanTokens(x.r.Budget)))
	}
	var topics []string
	for _, c := range p.Chapters {
		if len(c.Stories) > 0 {
			topics = append(topics, c.Topic)
			x.r.Stories += len(c.Stories)
		}
	}
	title := x.cfg.Paper.TitleOrDefault()
	pages := Render(p, Meta{Title: title, Issue: issue, Model: j.Model, Spent: x.r.Spent, Budget: x.r.Budget, Over: x.r.Over,
		Summarised: x.r.Summarised, Failed: x.r.Failed, Cut: cut(j)})
	png, err := cover.Render(cover.Content{Title: title, Issue: issue, Date: x.day, Topics: topics, Lead: p.Front.CoverLine})
	if err != nil {
		return nil, fmt.Errorf("paper: cover: %w", err)
	}
	return epub.Build(epub.Book{Title: title + " " + x.date, Identifier: editionID(x.date, rebuild), Date: x.day, Cover: png, Chapters: pages})
}

// journal returns today's journal and what this run spent on it: the
// saved one, unless refreshing, else a new one researched within budget.
// A journal with no finished topic comes back as it is; the caller checks.
func (x *run) journal(budget int64) (*brief.Journal, int64) {
	if !x.o.Refresh {
		j, err := brief.LoadJournal(x.date)
		if err != nil {
			x.warn(fmt.Sprintf("could not read today's journal, researching anew: %v", err))
		}
		if j != nil {
			x.logf("reusing today's journal")
			return j, 0
		}
	}
	cfg := x.cfg
	cfg.TokenBudget = budget
	g := brief.NewGenerator(cfg, x.d.Backend).WithMarginPercent(MarginPercent)
	j := g.NewJournal()
	j.Date = x.date
	updates := make(chan brief.Update)
	go g.Run(x.ctx, j, updates)
	for u := range updates {
		if u.Section.Status == brief.StatusRunning {
			x.logf("researching %s", u.Section.Topic)
		}
	}
	spent := j.TokensUsed()
	if note := j.OvershootNote(); note != "" {
		x.warn("research: " + note)
	}
	if err := brief.SaveJournal(j); err != nil && !errors.Is(err, brief.ErrNothingDone) {
		x.warn(fmt.Sprintf("could not save today's journal: %v", err))
	}
	return j, spent
}

// fetchArticles fetches each item's source once and returns the editor's
// input with the articles that came through, counting both outcomes.
func (x *run) fetchArticles(j *brief.Journal) editor.Input {
	var urls []string
	seen := map[string]bool{}
	for _, c := range editor.BuildInput(j, nil).Chapters {
		for _, it := range c.Items {
			if it.URL != "" && !seen[it.URL] {
				seen[it.URL] = true
				urls = append(urls, it.URL)
			}
		}
	}
	if len(urls) == 0 {
		return editor.BuildInput(j, nil)
	}
	x.logf("fetching %d sources", len(urls))
	articles := x.d.Fetch(x.ctx, urls)
	for _, u := range urls {
		if a := articles[u]; a.Err == nil && strings.TrimSpace(a.Text) != "" {
			x.r.Summarised++
		} else {
			x.r.Failed++
		}
	}
	return editor.BuildInput(j, articles)
}

// cut names the topics that failed or were cut by the budget.
func cut(j *brief.Journal) []string {
	var out []string
	for _, s := range j.Sections {
		if s != nil && s.Status != brief.StatusDone {
			out = append(out, s.Topic)
		}
	}
	return out
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// writeFile writes the book atomically: a temp file in dir, then rename.
func writeFile(dir, name string, data []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("paper: save book: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".book-*.tmp")
	if err != nil {
		return fmt.Errorf("paper: save book: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("paper: save book: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("paper: save book: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("paper: save book: %w", err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		return fmt.Errorf("paper: save book: %w", err)
	}
	return nil
}

// namespaceURL is RFC 4122's URL namespace, used for edition identifiers.
var namespaceURL = [16]byte{0x6b, 0xa7, 0xb8, 0x11, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}

// editionID is the EPUB identifier of date's edition number rebuild: a
// name-based UUID, so a rebuild never reuses an earlier edition's.
func editionID(date string, rebuild int) string {
	return "urn:uuid:" + uuid5(namespaceURL, fmt.Sprintf("daily-brief:%s:%d", date, rebuild))
}

// uuid5 is the RFC 4122 version 5 (SHA-1, name-based) UUID of name in ns.
func uuid5(ns [16]byte, name string) string {
	h := sha1.New()
	h.Write(ns[:])
	h.Write([]byte(name))
	var u [16]byte
	copy(u[:], h.Sum(nil))
	u[6] = u[6]&0x0f | 0x50
	u[8] = u[8]&0x3f | 0x80
	s := hex.EncodeToString(u[:])
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}
