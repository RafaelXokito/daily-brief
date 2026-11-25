package paper

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RafaelXokito/daily-brief/internal/brief"
	"github.com/RafaelXokito/daily-brief/internal/config"
	"github.com/RafaelXokito/daily-brief/internal/editor"
	"github.com/RafaelXokito/daily-brief/internal/fetch"
)

const runDate = "2026-11-25"

var runNow = time.Date(2026, 11, 25, 5, 45, 0, 0, time.Local)

// runBackend answers research with a fixed two-item digest per topic, the
// overview with a highlight, and the editor's chapter and front passes
// with valid JSON built from the ids in the prompt.
type runBackend struct {
	mu                         sync.Mutex
	tasks                      []brief.Task
	research, editor, overview int
	researchErr                error
	researchMul                int64 // research spends Budget*researchMul/10
	editorTokens               int64
	onEditor                   func() // called on each editor pass
}

func (f *runBackend) Research(_ context.Context, t brief.Task) brief.Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks = append(f.tasks, t)
	f.research++
	if f.researchErr != nil {
		return brief.Result{Err: f.researchErr, Tokens: 100}
	}
	slug := strings.ToLower(t.Topic.Name)
	md := fmt.Sprintf("### %s widget released\nThe widget team shipped a release. [source](https://example.com/%s/a)\n\n"+
		"### %s gadget recalled\nA gadget maker recalled a batch. [source](https://example.com/%s/b)\n", t.Topic.Name, slug, t.Topic.Name, slug)
	return brief.Result{Markdown: md, Tokens: t.Budget * max(f.researchMul, 1) / 10, Searches: t.Searches}
}

func (f *runBackend) Summarize(_ context.Context, system, input string) brief.Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.onEditor != nil && (strings.Contains(system, "copy editor") || strings.Contains(system, "front page editor")) {
		f.onEditor()
	}
	switch {
	case strings.Contains(system, "copy editor"):
		f.editor++
		return brief.Result{Markdown: chapterReply(input), Tokens: f.editorTokens}
	case strings.Contains(system, "front page editor"):
		f.editor++
		return brief.Result{Markdown: `{"leads":[{"id":"t0i0","headline":"Front one","paragraphs":["Opening."]},{"id":"t1i0","headline":"Front two","paragraphs":["Opening."]}],"in_brief":[],"cover_line":"A quiet morning"}`, Tokens: f.editorTokens}
	}
	f.overview++
	return brief.Result{Markdown: "## Today's highlights\n\n- Widgets shipped.", Tokens: 500}
}

func (f *runBackend) Name() string { return "fake" }

func (f *runBackend) calls() (research, editor, overview int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.research, f.editor, f.overview
}

func chapterReply(input string) string {
	type story struct {
		ID         string   `json:"id"`
		Headline   string   `json:"headline"`
		Paragraphs []string `json:"paragraphs"`
		Placement  string   `json:"placement"`
	}
	var out struct {
		Stories []story `json:"stories"`
	}
	for _, line := range strings.Split(input, "\n") {
		if id, ok := strings.CutPrefix(line, "ITEM "); ok {
			p := "story"
			if len(out.Stories) == 0 {
				p = "lead"
			}
			out.Stories = append(out.Stories, story{id, "Edited story", []string{"The team shipped it."}, p})
		}
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// fakeFetch returns an article for every "/a" URL and a failure otherwise.
type fakeFetch struct {
	mu   sync.Mutex
	urls [][]string
}

func (f *fakeFetch) fetch(_ context.Context, urls []string) map[string]fetch.Article {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.urls = append(f.urls, urls)
	out := map[string]fetch.Article{}
	for _, u := range urls {
		if strings.HasSuffix(u, "/a") {
			out[u] = fetch.Article{Text: "The widget team shipped a release after a long wait.", Words: 10}
		} else {
			out[u] = fetch.Article{Err: fetch.ErrTooShort}
		}
	}
	return out
}

type fakeDeliverer struct {
	mu    sync.Mutex
	names []string
	data  [][]byte
	err   error
	warn  []string
}

func (f *fakeDeliverer) Deliver(_ context.Context, name string, data []byte) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.names = append(f.names, name)
	f.data = append(f.data, data)
	return f.warn, f.err
}

func (f *fakeDeliverer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.names)
}

type runEnv struct {
	cfg     config.Config
	backend *runBackend
	fetch   *fakeFetch
	deliver *fakeDeliverer
	dir     string
	log     *bytes.Buffer
}

func newRunEnv(t *testing.T) *runEnv {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	cfg := config.Default()
	cfg.HistoryDays = 0
	cfg.Topics = []config.Topic{{Name: "Alpha"}, {Name: "Beta"}}
	cfg.Paper.TokenBudget = 2_000_000
	return &runEnv{cfg: cfg, backend: &runBackend{editorTokens: 1000}, fetch: &fakeFetch{}, deliver: &fakeDeliverer{}, dir: t.TempDir(), log: &bytes.Buffer{}}
}

func (e *runEnv) run(o Options) (Report, error) {
	return e.runCtx(context.Background(), o)
}

func (e *runEnv) runCtx(ctx context.Context, o Options) (Report, error) {
	o.Log = e.log
	d := Deps{Backend: e.backend, Fetch: e.fetch.fetch, Now: func() time.Time { return runNow }, Dir: e.dir}
	if !o.NoDeliver {
		d.Deliverer = e.deliver
	}
	return Run(ctx, e.cfg, o, d)
}

func (e *runEnv) state(t *testing.T) State {
	t.Helper()
	s, err := LoadState(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// epubFile reads one file out of an EPUB.
func epubFile(t *testing.T, book []byte, name string) []byte {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(book), int64(len(book)))
	if err != nil {
		t.Fatal(err)
	}
	f, err := z.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(f)
	return raw
}

// identifier reads dc:identifier out of an EPUB's package document.
func identifier(t *testing.T, book []byte) string {
	t.Helper()
	raw := epubFile(t, book, "EPUB/content.opf")
	m := regexp.MustCompile(`<dc:identifier[^>]*>([^<]+)</dc:identifier>`).FindSubmatch(raw)
	if m == nil {
		t.Fatalf("no identifier in %s", raw)
	}
	return string(m[1])
}

func TestRunNoBudget(t *testing.T) {
	for _, tc := range []struct {
		name          string
		paper, option int64
	}{
		{"both zero", 0, 0},
		{"negative option", 400_000, -1},
		{"too small", 1000, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newRunEnv(t)
			e.cfg.Paper.TokenBudget = tc.paper
			_, err := e.run(Options{Budget: tc.option})
			if !errors.Is(err, ErrNoBudget) || ExitCode(err) != 2 {
				t.Fatalf("err = %v, exit %d", err, ExitCode(err))
			}
			if r, ed, ov := e.backend.calls(); r+ed+ov != 0 {
				t.Fatalf("backend called: %d %d %d", r, ed, ov)
			}
		})
	}
}

func TestRunConfigErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		noBackend bool
		noDeliver bool
	}{
		{"no deliverer without -no-deliver", false, false},
		{"no backend", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newRunEnv(t)
			d := Deps{Backend: e.backend, Fetch: e.fetch.fetch, Now: func() time.Time { return runNow }, Dir: e.dir}
			if tc.noBackend {
				d.Backend = nil
			}
			_, err := Run(context.Background(), e.cfg, Options{NoDeliver: tc.noDeliver}, d)
			if !errors.Is(err, ErrConfig) || ExitCode(err) != 2 {
				t.Fatalf("err = %v, exit %d", err, ExitCode(err))
			}
			if r, ed, ov := e.backend.calls(); r+ed+ov != 0 || len(e.fetch.urls) != 0 {
				t.Fatal("work done before the config was checked")
			}
		})
	}
}

func TestRunCancelled(t *testing.T) {
	for _, tc := range []struct {
		name   string
		during string
	}{
		{"before research", "research"},
		{"while editing", "editing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newRunEnv(t)
			prev := State{Issue: 7, Date: "2026-11-24", File: "The Daily Brief 2026-11-24.epub", Delivered: true}
			if err := SaveState(e.dir, prev); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(filepath.Join(e.dir, stateFile))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.during == "research" {
				cancel()
			} else {
				e.backend.onEditor = cancel
			}
			_, err := e.runCtx(ctx, Options{})
			if !errors.Is(err, context.Canceled) || errors.Is(err, ErrNoStories) || ExitCode(err) != 1 {
				t.Fatalf("err = %v, exit %d", err, ExitCode(err))
			}
			if books, _ := filepath.Glob(filepath.Join(e.dir, "*.epub")); len(books) != 0 {
				t.Fatalf("books written: %v", books)
			}
			after, _ := os.ReadFile(filepath.Join(e.dir, stateFile))
			if !bytes.Equal(before, after) || e.deliver.count() != 0 {
				t.Fatalf("state changed or delivered: %s", after)
			}
		})
	}
}

func TestRunResearchOverspendComesOutOfEditor(t *testing.T) {
	e := newRunEnv(t)
	e.backend.researchMul = 25 // each pass spends 2.5 times its allocation
	var got []int64
	orig := runEditor
	runEditor = func(ctx context.Context, b brief.Backend, in editor.Input, budget editor.Budget, n int) editor.Paper {
		got = append(got, budget.Tokens)
		return orig(ctx, b, in, budget, n)
	}
	defer func() { runEditor = orig }()
	r, err := e.run(Options{})
	if err != nil {
		t.Fatal(err)
	}
	split, err := SplitBudget(e.cfg.Paper.TokenBudget, e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	j, err := brief.LoadJournal(runDate)
	if err != nil || j == nil {
		t.Fatal(j, err)
	}
	research := j.TokensUsed()
	if research <= split.Research {
		t.Fatalf("research spent %d of %d: no overspend to test", research, split.Research)
	}
	want := max(0, e.cfg.Paper.TokenBudget-research)
	if len(got) != 1 || got[0] != want || got[0] >= split.Editor {
		t.Fatalf("editor budget %v, want %d (editor share %d)", got, want, split.Editor)
	}
	if r.Over != max(0, r.Spent-r.Budget) {
		t.Fatalf("report = %+v", r)
	}
}

func TestRunOptionBudgetOverrides(t *testing.T) {
	e := newRunEnv(t)
	e.cfg.Paper.TokenBudget = 0
	r, err := e.run(Options{Budget: 1_500_000})
	if err != nil {
		t.Fatal(err)
	}
	if r.Budget != 1_500_000 {
		t.Fatalf("budget = %d", r.Budget)
	}
}

func TestRunHappyPath(t *testing.T) {
	e := newRunEnv(t)
	e.deliver.warn = []string{"could not delete older edition X"}
	r, err := e.run(Options{})
	if err != nil {
		t.Fatal(err)
	}
	name := "The Daily Brief 2026-11-25.epub"
	if e.deliver.count() != 1 || e.deliver.names[0] != name {
		t.Fatalf("delivered %v", e.deliver.names)
	}
	saved, err := os.ReadFile(filepath.Join(e.dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(saved, e.deliver.data[0]) {
		t.Fatal("the delivered bytes are not the saved book")
	}
	if got := identifier(t, saved); !regexp.MustCompile(`^urn:uuid:[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(got) {
		t.Fatalf("identifier = %q", got)
	}
	if s := e.state(t); s != (State{Issue: 1, Date: runDate, File: name, Delivered: true}) {
		t.Fatalf("state = %+v", s)
	}
	if r.Action != Build || !r.Delivered || r.Date != runDate || r.File != filepath.Join(e.dir, name) {
		t.Fatalf("report = %+v", r)
	}
	if r.Stories != 4 || r.Summarised != 2 || r.Failed != 2 || r.Fallbacks != 0 || r.Over != 0 || r.Budget != 2_000_000 || r.Spent <= 0 {
		t.Fatalf("report = %+v", r)
	}
	if !slices.Contains(r.Warnings, "could not delete older edition X") || !strings.Contains(e.log.String(), "warning: could not delete older edition X") {
		t.Fatalf("warnings %v, log %q", r.Warnings, e.log.String())
	}
	want := regexp.MustCompile(`^paper 2026-11-25: [0-9.]+k of 2M tokens, 4 stories, 2 of 4 sources summarised, 0 editor fallbacks, delivered as The Daily Brief 2026-11-25\.epub$`)
	if !want.MatchString(r.Line()) {
		t.Fatalf("line = %q", r.Line())
	}
	// The journal was saved for the paper's date.
	if j, err := brief.LoadJournal(runDate); err != nil || j == nil {
		t.Fatalf("journal: %v %v", j, err)
	}
}

func TestRunNoDeliver(t *testing.T) {
	e := newRunEnv(t)
	r, err := e.run(Options{NoDeliver: true})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.dir, "The Daily Brief 2026-11-25.epub")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if r.Delivered || !strings.HasSuffix(r.Line(), "saved to "+path) {
		t.Fatalf("line = %q", r.Line())
	}
	if s := e.state(t); s != (State{}) {
		t.Fatalf("-no-deliver changed the state: %+v", s)
	}
}

func TestRunReusesJournal(t *testing.T) {
	e := newRunEnv(t)
	j := &brief.Journal{Date: runDate, Sections: []*brief.Section{{Topic: "Alpha", Status: brief.StatusDone,
		Markdown: "### Saved item\nAn earlier run found this. [source](https://example.com/saved/a)\n"}}}
	if err := brief.SaveJournal(j); err != nil {
		t.Fatal(err)
	}
	r, err := e.run(Options{})
	if err != nil {
		t.Fatal(err)
	}
	research, editor, overview := e.backend.calls()
	if research != 0 || overview != 0 || editor == 0 {
		t.Fatalf("calls: research %d, editor %d, overview %d", research, editor, overview)
	}
	if r.Stories != 1 || len(e.fetch.urls) != 1 || !slices.Equal(e.fetch.urls[0], []string{"https://example.com/saved/a"}) {
		t.Fatalf("report %+v, fetched %v", r, e.fetch.urls)
	}
}

func TestRunNoStories(t *testing.T) {
	e := newRunEnv(t)
	e.backend.researchErr = errors.New("no network")
	prev := State{Issue: 7, Date: "2026-11-24", File: "The Daily Brief 2026-11-24.epub", Delivered: true}
	if err := SaveState(e.dir, prev); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(e.dir, stateFile))
	_, err := e.run(Options{})
	if !errors.Is(err, ErrNoStories) || ExitCode(err) != 3 {
		t.Fatalf("err = %v, exit %d", err, ExitCode(err))
	}
	if e.deliver.count() != 0 {
		t.Fatal("delivered with no stories")
	}
	after, _ := os.ReadFile(filepath.Join(e.dir, stateFile))
	if !bytes.Equal(before, after) {
		t.Fatalf("state changed: %s -> %s", before, after)
	}
	if books, _ := filepath.Glob(filepath.Join(e.dir, "*.epub")); len(books) != 0 {
		t.Fatalf("books written: %v", books)
	}
}

func TestRunDeliveryFails(t *testing.T) {
	e := newRunEnv(t)
	if err := SaveState(e.dir, State{Issue: 3, Date: "2026-11-24", File: "The Daily Brief 2026-11-24.epub", Delivered: true}); err != nil {
		t.Fatal(err)
	}
	e.deliver.err = errors.New("dropbox is down")
	r, err := e.run(Options{})
	if !errors.Is(err, ErrDelivery) || ExitCode(err) != 4 {
		t.Fatalf("err = %v, exit %d", err, ExitCode(err))
	}
	name := "The Daily Brief 2026-11-25.epub"
	book, err := os.ReadFile(filepath.Join(e.dir, name))
	if err != nil {
		t.Fatal("book not kept:", err)
	}
	if s := e.state(t); s != (State{Issue: 3, Date: runDate, File: name}) {
		t.Fatalf("state = %+v", s)
	}
	if r.Delivered || !strings.HasSuffix(r.Line(), "delivery failed, saved to "+filepath.Join(e.dir, name)) {
		t.Fatalf("report %+v, line %q", r, r.Line())
	}

	research, editor, overview := e.backend.calls()
	e.deliver.err = nil
	r, err = e.run(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r2, ed2, ov2 := e.backend.calls(); r2 != research || ed2 != editor || ov2 != overview {
		t.Fatal("redelivery called the backend")
	}
	if r.Action != Redeliver || !r.Delivered || e.deliver.count() != 2 || e.deliver.names[1] != name || !bytes.Equal(e.deliver.data[1], book) {
		t.Fatalf("report %+v, delivered %v", r, e.deliver.names)
	}
	if s := e.state(t); s != (State{Issue: 4, Date: runDate, File: name, Delivered: true}) {
		t.Fatalf("state = %+v", s)
	}
}

func TestRunSkipWhenDelivered(t *testing.T) {
	e := newRunEnv(t)
	if _, err := e.run(Options{}); err != nil {
		t.Fatal(err)
	}
	research, editor, overview := e.backend.calls()
	fetches := len(e.fetch.urls)
	r, err := e.run(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Action != Skip {
		t.Fatalf("action = %v", r.Action)
	}
	if r2, ed2, ov2 := e.backend.calls(); r2 != research || ed2 != editor || ov2 != overview || len(e.fetch.urls) != fetches || e.deliver.count() != 1 {
		t.Fatal("a skipped run did work")
	}
	if !strings.Contains(r.Line(), "already delivered") {
		t.Fatalf("line = %q", r.Line())
	}
}

func TestRunRefreshRebuilds(t *testing.T) {
	e := newRunEnv(t)
	if _, err := e.run(Options{}); err != nil {
		t.Fatal(err)
	}
	research, _, _ := e.backend.calls()
	r, err := e.run(Options{Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if r2, _, _ := e.backend.calls(); r2 <= research {
		t.Fatal("refresh reused the journal")
	}
	name := "The Daily Brief 2026-11-25 (2).epub"
	if r.Action != RebuildToday || e.deliver.count() != 2 || e.deliver.names[1] != name {
		t.Fatalf("report %+v, delivered %v", r, e.deliver.names)
	}
	if identifier(t, e.deliver.data[0]) == identifier(t, e.deliver.data[1]) {
		t.Fatal("the rebuild reused the identifier")
	}
	if s := e.state(t); s != (State{Issue: 2, Date: runDate, File: name, Rebuild: 2, Delivered: true}) {
		t.Fatalf("state = %+v", s)
	}
	// A third build of the day takes the next number.
	if _, err := e.run(Options{Refresh: true}); err != nil {
		t.Fatal(err)
	}
	if s := e.state(t); s.Rebuild != 3 || s.File != "The Daily Brief 2026-11-25 (3).epub" || s.Issue != 3 {
		t.Fatalf("state = %+v", s)
	}
}

func TestRunOvershootReported(t *testing.T) {
	e := newRunEnv(t)
	e.backend.editorTokens = 3_000_000
	r, err := e.run(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Over <= 0 || r.Over != r.Spent-r.Budget {
		t.Fatalf("report = %+v", r)
	}
	if !strings.Contains(r.Line(), "over by") || !strings.Contains(e.log.String(), "warning:") {
		t.Fatalf("line %q, log %q", r.Line(), e.log.String())
	}
	colophon := epubFile(t, e.deliver.data[0], "EPUB/colophon.xhtml")
	if !bytes.Contains(colophon, []byte("over by "+HumanTokens(r.Over))) {
		t.Fatalf("colophon does not record the overshoot: %s", colophon)
	}
}

func TestRunCorruptStateWarns(t *testing.T) {
	e := newRunEnv(t)
	if err := os.WriteFile(filepath.Join(e.dir, stateFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := e.run(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Warnings) == 0 || !strings.Contains(e.log.String(), "warning:") || !strings.Contains(e.log.String(), "corrupt") {
		t.Fatalf("warnings %v, log %q", r.Warnings, e.log.String())
	}
	if s := e.state(t); s.Issue != 1 || !s.Delivered {
		t.Fatalf("state = %+v", s)
	}
}

// firstBudgets is the budget of each topic's first research pass.
func firstBudgets(tasks []brief.Task) map[string]int64 {
	out := map[string]int64{}
	for _, t := range tasks {
		if _, ok := out[t.Topic.Name]; !ok {
			out[t.Topic.Name] = t.Budget
		}
	}
	return out
}

func TestRunUsesPaperMargin(t *testing.T) {
	e := newRunEnv(t)
	e.cfg.Concurrency = 1
	if _, err := e.run(Options{NoDeliver: true}); err != nil {
		t.Fatal(err)
	}
	got := firstBudgets(e.backend.tasks)

	split, err := SplitBudget(e.cfg.Paper.TokenBudget, e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	research := e.cfg
	research.TokenBudget = split.Research
	expect := func(g *brief.Generator, f *runBackend) map[string]int64 {
		j := g.NewJournal()
		updates := make(chan brief.Update)
		go g.Run(context.Background(), j, updates)
		for range updates {
		}
		return firstBudgets(f.tasks)
	}
	withMargin := &runBackend{}
	want := expect(brief.NewGenerator(research, withMargin).WithMarginPercent(MarginPercent), withMargin)
	plain := &runBackend{}
	def := expect(brief.NewGenerator(research, plain), plain)
	if maps.Equal(want, def) {
		t.Fatalf("the margin makes no difference to the plan (%v); the test cannot tell", want)
	}
	if !maps.Equal(got, want) {
		t.Fatalf("first pass budgets %v, want %v (default margin gives %v)", got, want, def)
	}
}

func TestReportLine(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    Report
		want string
	}{
		{"delivered", Report{Date: "2026-11-25", Spent: 312_000, Budget: 400_000, Stories: 41, Summarised: 28, Failed: 11, Fallbacks: 2,
			File: "/papers/The Daily Brief 2026-11-25.epub", Delivered: true},
			"paper 2026-11-25: 312k of 400k tokens, 41 stories, 28 of 39 sources summarised, 2 editor fallbacks, delivered as The Daily Brief 2026-11-25.epub"},
		{"over", Report{Date: "2026-11-25", Spent: 412_000, Budget: 400_000, Over: 12_000, Stories: 41, Summarised: 28, Failed: 11, Fallbacks: 2,
			File: "/papers/The Daily Brief 2026-11-25.epub", Delivered: true},
			"paper 2026-11-25: 412k of 400k tokens (over by 12k), 41 stories, 28 of 39 sources summarised, 2 editor fallbacks, delivered as The Daily Brief 2026-11-25.epub"},
		{"saved", Report{Date: "2026-11-25", Spent: 312_000, Budget: 400_000, Stories: 41, Summarised: 28, Failed: 11, Fallbacks: 2,
			File: "/papers/The Daily Brief 2026-11-25.epub"},
			"paper 2026-11-25: 312k of 400k tokens, 41 stories, 28 of 39 sources summarised, 2 editor fallbacks, saved to /papers/The Daily Brief 2026-11-25.epub"},
		{"delivery failed", Report{Date: "2026-11-25", Spent: 312_000, Budget: 400_000, Stories: 41, Summarised: 28, Failed: 11, Fallbacks: 2,
			File: "/papers/The Daily Brief 2026-11-25.epub", deliveryFailed: true},
			"paper 2026-11-25: 312k of 400k tokens, 41 stories, 28 of 39 sources summarised, 2 editor fallbacks, delivery failed, saved to /papers/The Daily Brief 2026-11-25.epub"},
		{"skip", Report{Date: "2026-11-25", Action: Skip, File: "/papers/The Daily Brief 2026-11-25.epub", Delivered: true},
			"paper 2026-11-25: already delivered as The Daily Brief 2026-11-25.epub"},
		{"redeliver", Report{Date: "2026-11-25", Action: Redeliver, File: "/papers/The Daily Brief 2026-11-25.epub", Delivered: true},
			"paper 2026-11-25: no tokens spent, delivered as The Daily Brief 2026-11-25.epub (built earlier)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.r.Line(); got != tc.want {
				t.Errorf("Line() =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

func TestExitCode(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{nil, 0},
		{ErrNoBudget, 2},
		{fmt.Errorf("wrapped: %w", ErrNoBudget), 2},
		{fmt.Errorf("wrapped: %w", ErrConfig), 2},
		{ErrNoStories, 3},
		{fmt.Errorf("wrapped: %w", ErrDelivery), 4},
		{errors.New("other"), 1},
	} {
		if got := ExitCode(tc.err); got != tc.want {
			t.Errorf("ExitCode(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
}

func TestEditionIdentifier(t *testing.T) {
	a, b := editionID(runDate, 0), editionID(runDate, 2)
	if a == b || a != editionID(runDate, 0) || editionID("2026-11-26", 0) == a {
		t.Fatalf("identifiers %q %q", a, b)
	}
	// RFC 4122 test vector style check: version 5, RFC variant.
	if !regexp.MustCompile(`^urn:uuid:[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(a) {
		t.Fatalf("identifier %q", a)
	}
}

func TestUUID5KnownVector(t *testing.T) {
	// uuid.uuid5(uuid.NAMESPACE_DNS, "python.org") in Python.
	dns := [16]byte{0x6b, 0xa7, 0xb8, 0x10, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}
	if got := uuid5(dns, "python.org"); got != "886313e1-3b8a-5372-9b90-0c9aee199e5d" {
		t.Fatalf("uuid5 = %s", got)
	}
}

func TestRunLogsResearchFailures(t *testing.T) {
	e := newRunEnv(t)
	e.backend.researchErr = errors.New("no network")

	_, err := e.run(Options{NoDeliver: true})

	if !errors.Is(err, ErrNoStories) {
		t.Fatalf("err = %v", err)
	}
	for _, topic := range []string{"Alpha", "Beta"} {
		if want := "warning: " + topic + ": "; !strings.Contains(e.log.String(), want) || !strings.Contains(e.log.String(), "no network") {
			t.Errorf("log lacks %q with the error:\n%s", want, e.log.String())
		}
	}
}

func TestRunLogsSkippedTopics(t *testing.T) {
	e := newRunEnv(t)
	e.cfg.MaxReadMinutes = 1
	e.cfg.Topics = append(e.cfg.Topics, config.Topic{Name: "Gamma"}, config.Topic{Name: "Delta"}, config.Topic{Name: "Epsilon"})

	_, _ = e.run(Options{NoDeliver: true})

	if !strings.Contains(e.log.String(), ": skipped: ") {
		t.Errorf("log names no skipped topic:\n%s", e.log.String())
	}
}

// The editor's margin, like the generator's, applies only to claude-code.
func TestRunEditorMarginOnlyForClaudeCode(t *testing.T) {
	for provider, want := range map[string]int{"claude-code": MarginPercent, "anthropic": 0} {
		t.Run(provider, func(t *testing.T) {
			e := newRunEnv(t)
			e.cfg.Provider.Type = provider
			var got []int
			orig := runEditor
			runEditor = func(ctx context.Context, b brief.Backend, in editor.Input, budget editor.Budget, n int) editor.Paper {
				got = append(got, budget.MarginPct)
				return orig(ctx, b, in, budget, n)
			}
			defer func() { runEditor = orig }()

			if _, err := e.run(Options{NoDeliver: true}); err != nil {
				t.Fatal(err)
			}

			if len(got) != 1 || got[0] != want {
				t.Errorf("editor margins %v, want [%d]", got, want)
			}
		})
	}
}
