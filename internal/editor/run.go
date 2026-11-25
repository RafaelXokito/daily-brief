package editor

import (
	"context"
	"sync"

	"github.com/RafaelXokito/daily-brief/internal/brief"
)

const (
	passOverhead      = 3000 // tokens a pass costs beyond its prompt text
	outputWithBody    = 650  // planned output tokens per item with an article
	outputWithoutBody = 200  // planned output tokens per item without one
	outputPerPass     = 300
	frontOutput       = 1500
	inputPerItem      = 3300 // upper bound on one item's prompt tokens, for Reserve
)

// Budget limits what the editor may spend. Provider is informational.
type Budget struct {
	Tokens    int64
	MarginPct int
	Provider  string
}

// Reserve is an upper bound on the editor's cost, known before research:
// every item's prompt and reply, plus the overhead of one pass per chapter
// and one for the front page.
func Reserve(chapters, items int) int64 {
	return int64(items)*(inputPerItem+outputWithBody) + int64(chapters+1)*passOverhead
}

type plan struct {
	system, input string
	output        int64
	planned       int64
}

func newPlan(system, input string, output int64) plan {
	return plan{system, input, output, brief.EstimateTokens(system+input) + passOverhead + output}
}

func chapterPlan(c ChapterIn) plan {
	system, input := chapterPrompt(c)
	out := int64(outputPerPass)
	for _, it := range c.Items {
		if it.Article != "" {
			out += outputWithBody
		} else {
			out += outputWithoutBody
		}
	}
	return newPlan(system, input, out)
}

func withMargin(planned int64, pct int) int64 { return planned + planned*int64(pct)/100 }

// pool is the budget not yet claimed.
type pool struct {
	mu        sync.Mutex
	remaining int64
	spent     int64
	marginPct int
}

// claim reserves a pass's planned cost plus margin, or reports that it does
// not fit.
func (p *pool) claim(planned int64) (claimed int64, ok bool) {
	claimed = withMargin(planned, p.marginPct)
	p.mu.Lock()
	defer p.mu.Unlock()
	if claimed > p.remaining {
		return 0, false
	}
	p.remaining -= claimed
	return claimed, true
}

// settle replaces a claim with what the pass reported.
func (p *pool) settle(claimed, actual int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.remaining += claimed - actual
	p.spent += actual
}

func call(ctx context.Context, b brief.Backend, pl plan) brief.Result {
	if c, ok := b.(brief.Composer); ok {
		return c.Compose(ctx, pl.system, pl.input, pl.output)
	}
	return b.Summarize(ctx, pl.system, pl.input)
}

// pass runs one planned pass if it fits the pool, and returns its reply.
func (p *pool) pass(ctx context.Context, b brief.Backend, pl plan) (string, bool) {
	claimed, ok := p.claim(pl.planned)
	if !ok {
		return "", false
	}
	r := call(ctx, b, pl)
	p.settle(claimed, r.Tokens)
	if r.Err != nil {
		return "", false
	}
	return r.Markdown, true
}

func fallbackChapter(c ChapterIn) Chapter {
	ch := Chapter{Topic: c.Topic}
	for i, it := range c.Items {
		placement := "story"
		if i == 0 {
			placement = "lead"
		}
		ch.Stories = append(ch.Stories, FallbackStory(it, placement))
	}
	return ch
}

// editChapter runs one chapter pass. Stories that fail the fact check, or the
// whole chapter if the pass cannot run or its reply is unusable, fall back.
func editChapter(ctx context.Context, b brief.Backend, p *pool, c ChapterIn) Chapter {
	reply, ok := p.pass(ctx, b, chapterPlan(c))
	if !ok {
		return fallbackChapter(c)
	}
	ch, err := parseChapter(reply, c)
	if err != nil {
		return fallbackChapter(c)
	}
	items := map[string]ItemIn{}
	for _, it := range c.Items {
		items[it.ID] = it
	}
	for i, s := range ch.Stories {
		if !checkStory(s, items[s.ID]) {
			ch.Stories[i] = FallbackStory(items[s.ID], s.Placement)
		}
	}
	return ch
}

// Run edits the day's journal into a Paper: one pass per chapter, up to
// concurrency at a time, then the front page from the edited chapters. A pass
// starts only if its planned cost plus margin fits what is left of the
// budget; otherwise, or when its reply is unusable, the mechanical fallback
// takes its place.
func Run(ctx context.Context, b brief.Backend, in Input, budget Budget, concurrency int) Paper {
	if concurrency < 1 {
		concurrency = 1
	}
	p := &pool{remaining: budget.Tokens, marginPct: budget.MarginPct}
	chapters := make([]Chapter, len(in.Chapters))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i, c := range in.Chapters {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			chapters[i] = editChapter(ctx, b, p, c)
		}()
	}
	wg.Wait()

	front := FallbackFront(in)
	system, input := frontPrompt(in, chapters)
	if reply, ok := p.pass(ctx, b, newPlan(system, input, frontOutput)); ok {
		if fp, err := parseFront(reply, chapters); err == nil {
			front = fp
		}
	}

	fallbacks := 0
	for _, c := range chapters {
		for _, s := range c.Stories {
			if s.Fallback {
				fallbacks++
			}
		}
	}
	return Paper{Date: in.Date, Front: front, Chapters: chapters, Fallbacks: fallbacks, Tokens: p.spent}
}
