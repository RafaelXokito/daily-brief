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
			{Topic: "Alpha", Items: []ItemIn{
				{ID: "t0i0", Headline: "Alpha one", Facts: "fact a", Prose: "Prose a.", Host: "a.example"},
				{ID: "t0i1", Headline: "Alpha two", Facts: "fact b", Prose: "Prose b.", Host: "a.example"},
			}},
			{Topic: "Beta", Items: []ItemIn{
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

const goodFront = `{"leads":[{"id":"t0i0","headline":"Front A","paragraphs":["Opening."]},{"id":"t1i0","headline":"Front B","paragraphs":["Opening."]}],"in_brief":[{"id":"t0i1","sentence":"Short."}],"cover_line":"Cover"}`

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

func TestRunSkipsPassThatDoesNotFit(t *testing.T) {
	in := runInput()
	m1 := withMargin(chapterPlan(in.Chapters[0]).planned, 20)
	m2 := withMargin(chapterPlan(in.Chapters[1]).planned, 20)
	// Chapter 1 fits and spends what it claimed; one token short of chapter 2.
	f := newFake(m1)
	p := Run(context.Background(), f, in, Budget{Tokens: m1 + m2 - 1, MarginPct: 20}, 1)
	if len(f.calls) != 1 || f.calls[0] != "chapter" {
		t.Fatalf("calls = %v, want one chapter call", f.calls)
	}
	if b := p.Chapters[1].Stories; len(b) != 1 || !b[0].Fallback || b[0].Placement != "lead" {
		t.Fatalf("beta should fall back: %+v", b)
	}
	if p.Fallbacks != 1 {
		t.Fatalf("fallbacks = %d", p.Fallbacks)
	}
	if p.Front.CoverLine == "" || len(p.Front.Leads) == 0 {
		t.Fatalf("front should be the fallback front: %+v", p.Front)
	}
	if p.Tokens != m1 {
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
	p := Run(context.Background(), f, in, Budget{Tokens: m1 + m2 + 100, MarginPct: 20}, 1)
	if len(f.calls) != 1 {
		t.Fatalf("calls = %v, want only the first chapter", f.calls)
	}
	if p.Tokens != over {
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
	if p.Front.CoverLine == "" || p.Fallbacks != 0 {
		t.Fatalf("front = %+v fallbacks = %d", p.Front, p.Fallbacks)
	}
}

func TestOutputPlan(t *testing.T) {
	c := ChapterIn{Items: []ItemIn{{Article: "x"}, {}}}
	if got := chapterPlan(c).output; got != 650+200+300 {
		t.Fatalf("output = %d", got)
	}
}

func TestReserve(t *testing.T) {
	if got, want := Reserve(8, 30), int64(30*(3300+650)+9*3000); got != want {
		t.Fatalf("Reserve = %d, want %d", got, want)
	}
}
