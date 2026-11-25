package editor

import (
	"context"
	"strings"
	"sync"

	"github.com/RafaelXokito/daily-brief/internal/brief"
	"github.com/RafaelXokito/daily-brief/internal/fetch"
)

const (
	passOverhead      = 3000 // tokens a pass costs beyond its prompt text
	outputWithBody    = 650  // planned output tokens per item with an article
	outputWithoutBody = 200  // planned output tokens per item without one
	outputPerPass     = 300

	// The front pass's planned output: a base, each lead it may write and
	// one In Brief paragraph per front page chapter.
	frontOutputBase     = 300
	frontOutputPerLead  = 250
	frontOutputPerBrief = 160

	// Reserve's bounds on text it cannot see before research.
	articleBytesPerWord = 7    // a fetched article's bytes per word; English runs about 6 with the space
	itemFieldsBytes     = 2000 // one item's headline, tag, facts, digest and host, with their labels
	frontBytesPerItem   = 1500 // one story's headline and opening on the front page, plus a highlight
)

// Budget limits what the editor may spend. Provider is carried for callers
// and not used here: margin sizing per provider is the caller's choice, made
// when it fills MarginPct.
type Budget struct {
	Tokens    int64
	MarginPct int
	Provider  string
}

// frontOutputFor is the front pass's planned output for chapters front page
// chapters: the most leads it may write and an In Brief paragraph each.
func frontOutputFor(chapters int) int64 {
	return frontOutputBase + frontOutputPerLead*maxFrontLeads + frontOutputPerBrief*int64(chapters)
}

// Reserve is an upper bound on the editor's cost, known before research and
// estimated as Run plans its passes: every item's prompt with an article
// cut at fetch's default word limit and its planned reply, one chapter pass
// per chapter, and the front page's prompt and reply, all with marginPct
// on top as Run claims them.
func Reserve(chapters, items, marginPct int) int64 {
	n := int64(items)
	item := brief.EstimateTokens(strings.Repeat("x", fetch.DefaultMaxWords*articleBytesPerWord+itemFieldsBytes)) + outputWithBody
	chapter := brief.EstimateTokens(chapterSystem) + passOverhead + outputPerPass
	front := brief.EstimateTokens(frontSystem+strings.Repeat("x", items*frontBytesPerItem)) + passOverhead + frontOutputFor(chapters)
	return withMargin(n*item+int64(chapters)*chapter+front, marginPct)
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
	held      int64 // reserved for the front page, outside remaining
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

// reserve sets aside n for a later pass if it fits now.
func (p *pool) reserve(n int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n <= p.remaining {
		p.remaining -= n
		p.held = n
	}
}

// release returns the reservation to the pool.
func (p *pool) release() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.remaining += p.held
	p.held = 0
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

// itemsByID indexes every item of in by its id.
func itemsByID(in Input) map[string]ItemIn {
	out := map[string]ItemIn{}
	for _, c := range in.Chapters {
		for _, it := range c.Items {
			out[it.ID] = it
		}
	}
	return out
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
	items := itemsByID(Input{Chapters: []ChapterIn{c}})
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
// takes its place. Chapters kept off the front page are edited as usual but
// never offered to, nor accepted from, the front page pass.
func Run(ctx context.Context, b brief.Backend, in Input, budget Budget, concurrency int) Paper {
	if concurrency < 1 {
		concurrency = 1
	}
	p := &pool{remaining: budget.Tokens, marginPct: max(budget.MarginPct, 0)}
	// The front page is the most visible page: set its estimated cost aside
	// before the chapters can use the budget up.
	frontIn := frontOnly(in)
	var asFallback []Chapter
	for _, c := range frontIn.Chapters {
		asFallback = append(asFallback, fallbackChapter(c))
	}
	fs, fi := frontPrompt(frontIn, asFallback)
	p.reserve(withMargin(newPlan(fs, fi, frontOutputFor(len(frontIn.Chapters))).planned, p.marginPct))
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

	p.release()
	front, fallbacks := editFront(ctx, b, p, in, chapters)
	for _, c := range chapters {
		for _, s := range c.Stories {
			if s.Fallback {
				fallbacks++
			}
		}
	}
	return Paper{Date: in.Date, Front: front, Chapters: chapters, Fallbacks: fallbacks, Tokens: p.spent}
}

// editFront runs the front page pass on the edited chapters and returns the
// front page with its fallback count. The leads and the cover line stand or
// fall together: when the pass cannot run, its reply is unusable, or a lead
// or the cover line fails the fact check, the whole mechanical front page
// takes its place and counts as one fallback. Otherwise each topic whose In
// Brief paragraph is missing or fails its checks gets its mechanical
// paragraph, one fallback each.
func editFront(ctx context.Context, b brief.Backend, p *pool, in Input, chapters []Chapter) (FrontPage, int) {
	frontIn, onFront := frontOnly(in), frontChapters(in, chapters)
	if ctx.Err() != nil || len(onFront) == 0 {
		return FallbackFront(in), 1
	}
	system, input := frontPrompt(frontIn, onFront)
	reply, ok := p.pass(ctx, b, newPlan(system, input, frontOutputFor(len(onFront))))
	if !ok {
		return FallbackFront(in), 1
	}
	fp, err := parseFront(reply, onFront)
	src := newFrontSources(onFront, itemsByID(in))
	if err != nil || !src.leadsOK(fp) {
		return FallbackFront(in), 1
	}
	return withInBrief(fp, frontIn.Chapters, src)
}

// withInBrief is fp with one In Brief paragraph for each front page chapter
// that has items besides fp's leads, in chapter order: the model's when it
// passed the parser and the fact check, else the chapter's mechanical
// paragraph. It also returns how many topics fell back.
func withInBrief(fp FrontPage, chapters []ChapterIn, src frontSources) (FrontPage, int) {
	model := map[string]BriefParagraph{}
	for _, para := range fp.InBrief {
		model[para.Topic] = para
	}
	leads := map[string]bool{}
	for _, l := range fp.Leads {
		leads[l.ID] = true
	}
	var out []BriefParagraph
	fell := 0
	for _, c := range chapters {
		mech := fallbackParagraph(c, leads)
		if len(mech.Sentences) == 0 {
			continue
		}
		if para, ok := model[c.Topic]; ok && src.briefOK(para) {
			out = append(out, para)
			continue
		}
		out = append(out, mech)
		fell++
	}
	fp.InBrief = out
	return fp, fell
}
