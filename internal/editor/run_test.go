package editor

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RafaelXokito/daily-brief/internal/brief"
)

type fakeBackend struct {
	mu         sync.Mutex
	chapter    func(system, input string) string // reply for a chapter pass
	front      string
	tokens     int64
	calls      []string // "chapter" or "front", in call order
	maxOutput  []int64
	viaCompose bool
}

func (f *fakeBackend) Research(context.Context, brief.Task) brief.Result { return brief.Result{} }
func (f *fakeBackend) Name() string                                      { return "fake" }

func (f *fakeBackend) reply(system, input string, maxOutput int64) brief.Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.maxOutput = append(f.maxOutput, maxOutput)
	if system == frontSystem {
		f.calls = append(f.calls, "front")
		return brief.Result{Markdown: f.front, Tokens: f.tokens}
	}
	f.calls = append(f.calls, "chapter")
	return brief.Result{Markdown: f.chapter(system, input), Tokens: f.tokens}
}

func (f *fakeBackend) Summarize(_ context.Context, system, input string) brief.Result {
	return f.reply(system, input, -1)
}

type composeBackend struct{ *fakeBackend }

func (c composeBackend) Compose(_ context.Context, system, input string, maxOutput int64) brief.Result {
	c.fakeBackend.viaCompose = true
	return c.reply(system, input, maxOutput)
}

func runInput() Input {
	return Input{
		Date: time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC),
		Chapters: []ChapterIn{
			{Topic: "Alpha", FrontPage: true, Items: []ItemIn{
				{ID: "t0i0", Headline: "Alpha one", Facts: "fact a", Prose: "Prose a.", Host: "a.example"},
				{ID: "t0i1", Headline: "Alpha two", Facts: "fact b", Prose: "Prose b.", Host: "a.example"},
			}},
			{Topic: "Beta", FrontPage: true, Items: []ItemIn{
				{ID: "t1i0", Headline: "Beta one", Facts: "fact c", Prose: "Prose c.", Host: "b.example"},
			}},
		},
	}
}

// goodReplies answers a chapter pass with one lead and briefs for the rest,
// adding extra to the lead's body.
func goodReplies(extra string) func(system, input string) string {
	return func(_, input string) string {
		type st struct {
			ID         string   `json:"id"`
			Headline   string   `json:"headline"`
			Paragraphs []string `json:"paragraphs"`
			Placement  string   `json:"placement"`
		}
		var out struct {
			Stories []st `json:"stories"`
		}
		for _, line := range strings.Split(input, "\n") {
			id, ok := strings.CutPrefix(line, "ITEM ")
			if !ok {
				continue
			}
			p := "brief"
			if len(out.Stories) == 0 {
				p = "lead"
			}
			body := "Edited " + strings.ToUpper(id[:1]) + "."
			if p == "lead" {
				body += extra
			}
			out.Stories = append(out.Stories, st{id, "Edited head " + strings.ToUpper(id[:1]), []string{body}, p})
		}
		b, _ := json.Marshal(out)
		return string(b)
	}
}

const goodFront = `{"leads":[{"id":"t0i0","headline":"Front A","paragraphs":["Opening."]},{"id":"t1i0","headline":"Front B","paragraphs":["Opening."]}],"in_brief":[{"topic":"Alpha","sentences":[{"id":"t0i1","text":"Short."}]}],"cover_line":"Cover"}`

func newFake(tokens int64) *fakeBackend {
	return &fakeBackend{chapter: goodReplies(""), front: goodFront, tokens: tokens}
}

func bigBudget() Budget { return Budget{Tokens: 1_000_000, MarginPct: 20} }

func TestRunUsesReplies(t *testing.T) {
	f := newFake(1000)
	p := Run(context.Background(), f, runInput(), bigBudget(), 2)
	if p.Fallbacks != 0 {
		t.Fatalf("fallbacks = %d", p.Fallbacks)
	}
	if p.Tokens != 3000 {
		t.Fatalf("tokens = %d, want 3000", p.Tokens)
	}
	if len(p.Chapters) != 2 || p.Chapters[0].Topic != "Alpha" || p.Chapters[1].Topic != "Beta" {
		t.Fatalf("chapters: %+v", p.Chapters)
	}
	if got := p.Chapters[0].Stories[0].Headline; got != "Edited head T" {
		t.Fatalf("headline = %q", got)
	}
	if p.Front.CoverLine != "Cover" {
		t.Fatalf("front = %+v", p.Front)
	}
	if !p.Date.Equal(runInput().Date) {
		t.Fatalf("date = %v", p.Date)
	}
}

func TestRunFallsBackOnBadJSON(t *testing.T) {
	f := newFake(10)
	good := f.chapter
	f.chapter = func(system, input string) string {
		if strings.Contains(input, "Chapter: Alpha") {
			return "nonsense"
		}
		return good(system, input)
	}
	p := Run(context.Background(), f, runInput(), bigBudget(), 1)
	if p.Fallbacks != 2 {
		t.Fatalf("fallbacks = %d, want 2", p.Fallbacks)
	}
	a := p.Chapters[0].Stories
	if len(a) != 2 || !a[0].Fallback || !a[1].Fallback || a[0].Placement != "lead" || a[1].Placement != "story" {
		t.Fatalf("alpha: %+v", a)
	}
	if b := p.Chapters[1].Stories; len(b) != 1 || b[0].Fallback {
		t.Fatalf("beta: %+v", b)
	}
}

func TestRunFallsBackOnFailedFactCheck(t *testing.T) {
	f := newFake(10)
	f.chapter = func(system, input string) string {
		if strings.Contains(input, "Chapter: Alpha") {
			return goodReplies(" Up 9.9 percent.")(system, input)
		}
		return goodReplies("")(system, input)
	}
	p := Run(context.Background(), f, runInput(), bigBudget(), 1)
	if p.Fallbacks != 1 {
		t.Fatalf("fallbacks = %d, want 1", p.Fallbacks)
	}
	a := p.Chapters[0].Stories
	if !a[0].Fallback || a[0].Placement != "lead" || a[0].Headline != "Alpha one" {
		t.Fatalf("lead should be fallback: %+v", a[0])
	}
	if a[1].Fallback {
		t.Fatalf("second story should be kept: %+v", a[1])
	}
}

// frontMargin is the front page's reservation for in.
func frontMargin(in Input) int64 { return frontWithMargin(in, 20) }

func frontWithMargin(in Input, pct int) int64 {
	var fb []Chapter
	for _, c := range in.Chapters {
		fb = append(fb, fallbackChapter(c))
	}
	fs, fi := frontPrompt(in, fb)
	return withMargin(newPlan(fs, fi, frontOutput).planned, pct)
}

func TestRunSkipsPassThatDoesNotFit(t *testing.T) {
	in := runInput()
	m1 := withMargin(chapterPlan(in.Chapters[0]).planned, 20)
	m2 := withMargin(chapterPlan(in.Chapters[1]).planned, 20)
	// Chapter 1 fits and spends what it claimed; one token short of chapter 2.
	f := newFake(m1)
	p := Run(context.Background(), f, in, Budget{Tokens: frontMargin(in) + m1 + m2 - 1, MarginPct: 20}, 1)
	if len(f.calls) != 2 || f.calls[0] != "chapter" || f.calls[1] != "front" {
		t.Fatalf("calls = %v, want one chapter call then front", f.calls)
	}
	if b := p.Chapters[1].Stories; len(b) != 1 || !b[0].Fallback || b[0].Placement != "lead" {
		t.Fatalf("beta should fall back: %+v", b)
	}
	if p.Fallbacks != 1 {
		t.Fatalf("fallbacks = %d", p.Fallbacks)
	}
	if p.Tokens != 2*m1 {
		t.Fatalf("tokens = %d", p.Tokens)
	}
}

func TestRunNeverStartsPastBudget(t *testing.T) {
	in := runInput()
	m1 := withMargin(chapterPlan(in.Chapters[0]).planned, 20)
	m2 := withMargin(chapterPlan(in.Chapters[1]).planned, 20)
	// Enough for both chapters as planned, but the first reports 101 tokens
	// more than it claimed, so the second no longer fits.
	over := m1 + 101
	f := newFake(over)
	p := Run(context.Background(), f, in, Budget{Tokens: frontMargin(in) + m1 + m2 + 100, MarginPct: 20}, 1)
	if len(f.calls) != 2 || f.calls[1] != "front" {
		t.Fatalf("calls = %v, want first chapter then front", f.calls)
	}
	if p.Tokens != 2*over {
		t.Fatalf("tokens = %d", p.Tokens)
	}
	if !p.Chapters[1].Stories[0].Fallback {
		t.Fatalf("beta should fall back")
	}
}

func TestRunPrefersCompose(t *testing.T) {
	in := runInput()
	f := newFake(1)
	p := Run(context.Background(), composeBackend{f}, in, bigBudget(), 1)
	if p.Fallbacks != 0 || !f.viaCompose {
		t.Fatalf("compose not used: fallbacks=%d", p.Fallbacks)
	}
	if want := chapterPlan(in.Chapters[0]).output; f.maxOutput[0] != want {
		t.Fatalf("maxOutput = %d, want %d", f.maxOutput[0], want)
	}
	if f.maxOutput[len(f.maxOutput)-1] != frontOutput {
		t.Fatalf("front maxOutput = %d", f.maxOutput[len(f.maxOutput)-1])
	}
	g := newFake(1)
	Run(context.Background(), g, in, bigBudget(), 1)
	if g.viaCompose || g.maxOutput[0] != -1 {
		t.Fatalf("summarize should be used without Compose")
	}
}

func TestRunFrontFallsBackOnBadReply(t *testing.T) {
	f := newFake(1)
	f.front = "nope"
	p := Run(context.Background(), f, runInput(), bigBudget(), 1)
	if p.Front.CoverLine == "" || p.Fallbacks != 1 {
		t.Fatalf("front = %+v fallbacks = %d", p.Front, p.Fallbacks)
	}
}

func TestOutputPlan(t *testing.T) {
	c := ChapterIn{Items: []ItemIn{{Article: "x"}, {}}}
	if got := chapterPlan(c).output; got != 650+200+300 {
		t.Fatalf("output = %d", got)
	}
}

func TestRunReservesFrontPage(t *testing.T) {
	in := runInput()
	var fb []Chapter
	for _, c := range in.Chapters {
		fb = append(fb, fallbackChapter(c))
	}
	fs, fi := frontPrompt(in, fb)
	mFront := withMargin(newPlan(fs, fi, frontOutput).planned, 20)
	m2 := withMargin(chapterPlan(in.Chapters[1]).planned, 20)
	f := newFake(1)
	// Room for the front page and only the smaller chapter.
	p := Run(context.Background(), f, in, Budget{Tokens: mFront + m2 + 20, MarginPct: 20}, 1)
	if len(f.calls) != 2 || f.calls[0] != "chapter" || f.calls[1] != "front" {
		t.Fatalf("calls = %v, want one chapter then front", f.calls)
	}
	if !p.Chapters[0].Stories[0].Fallback || p.Chapters[1].Stories[0].Fallback {
		t.Fatalf("alpha should fall back, beta not: %+v", p.Chapters)
	}
	if p.Front.CoverLine != "Cover" {
		t.Fatalf("front pass should have run: %+v", p.Front)
	}
}

func TestRunClampsNegativeMargin(t *testing.T) {
	in := runInput()
	in.Chapters = in.Chapters[:1]
	need := chapterPlan(in.Chapters[0]).planned
	// An unclamped -50% margin would claim need/2, which fits; clamped to
	// zero the claim is need, which does not fit one token short.
	f := newFake(1)
	Run(context.Background(), f, in, Budget{Tokens: need - 1 + frontWithMargin(in, 0), MarginPct: -50}, 1)
	for _, c := range f.calls {
		if c == "chapter" {
			t.Fatalf("chapter pass ran although its planned cost exceeds the budget: %v", f.calls)
		}
	}
	// And it does run with exactly its planned cost.
	g := newFake(1)
	Run(context.Background(), g, in, Budget{Tokens: need + frontWithMargin(in, 0), MarginPct: -50}, 1)
	if len(g.calls) == 0 || g.calls[0] != "chapter" {
		t.Fatalf("chapter should fit: %v", g.calls)
	}
}

func TestRunSkipsFrontWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := newFake(1)
	f.chapter = func(system, input string) string {
		cancel()
		return goodReplies("")(system, input)
	}
	p := Run(ctx, f, runInput(), bigBudget(), 1)
	for _, c := range f.calls {
		if c == "front" {
			t.Fatalf("front called after cancel: %v", f.calls)
		}
	}
	if p.Front.CoverLine == "" || p.Front.CoverLine == "Cover" {
		t.Fatalf("want fallback front: %+v", p.Front)
	}
}
