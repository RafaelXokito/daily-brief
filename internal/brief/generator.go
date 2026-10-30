package brief

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/rafaelpereira/daily-brief/internal/config"
)

const researchSystemPrompt = `You are the editor of a short personal morning briefing for a software engineer.
For the topic you are given, use web search to find what actually happened recently and write a compact, skimmable digest.

Guidelines:
- Cover only news from the last few days. Rank items by importance, most consequential first.
- Stay within the item and word limits you are given. Fewer items is fine; never pad with stale, evergreen or marketing content.
- If the topic itself is quiet, widen to closely related areas that had notable news (e.g. if Java is quiet, other languages and frameworks with notable releases or problems). Put those items last, after a line "**Related**".
- Strongly prefer primary sources: vendor advisories, release notes, official blogs, CISA/NVD, regulator or company announcements, then reputable news outlets. Avoid newsletter digests, directory sites and unofficial trackers; if only a secondary source exists, say so briefly in the item.
- For security items, name the affected product and versions, severity/CVE if known, and the recommended action.
- Do your research first and write nothing until you are done; your whole reply is the digest itself.

Format (Markdown, no top-level heading, no preamble or closing remarks):
- Each item: "### <headline>" then at most 2 tight sentences on why it matters, ending with an inline link to the best source like [source](https://...).
- If nothing notable happened, even in related areas, say so in one line.`

const overviewSystemPrompt = `You pick the highlights of a personal morning briefing. You are given digests for several topics: each topic starts with a line "# <topic>", and each item with its headline, usually a line "### <headline>".

Reply with JSON only: no prose, no Markdown and no code fence. Use exactly this shape:
{"highlights":[{"tier":"act","topic":"<exact topic name>","headline":"<exact item headline>","text":"<at most 18 words>","why":"<at most 14 words>"}]}

Rules:
- Pick 3-6 of the most important items across all topics, most important first. Use only items from the digests; never invent one.
- tier is "act" for something to do: a security action such as patching, upgrading or rotating credentials, or a deadline. Otherwise tier is "know": worth knowing.
- topic is the topic name exactly as given after "# ". headline is the item's headline exactly as written, without "### ".
- text says what happened in at most 18 words. why says why it matters to me in at most 14 words.
- Prefer new stories over updates to earlier ones (items headed "Update:") unless an update is major.`

// historyInstruction follows the items earlier days' journals reported.
const historyInstruction = `These were already in my journal on the dates shown. Do not repeat them. ` +
	`Include one only if there is a material new development since then: a new fact, such as a release or patch shipped, a decision, ruling or vote, ` +
	`new figures or scope (more affected versions, larger breach), an official response, or a reversal. ` +
	`Commentary, recaps, opinion pieces or the same news from another outlet are not new. ` +
	`Write such an item as "### Update: <headline>", and start it with what is new since the date shown (e.g. "Since Mon: …"). ` +
	`Updates count toward the item limit and rank like any other item.`

// taskPrompt is the user prompt for a research, top-up or narrow pass. With
// history, it lists what earlier days reported (see LoadHistory). Angle
// brackets in a prior digest are neutralised, as in history lines, so it
// cannot close its own tag.
func taskPrompt(t Task) string {
	prior := untag(t.Prior)
	var b strings.Builder
	fmt.Fprintf(&b, "Today is %s.\n\nTopic: %s\nWhat I care about: %s\n\n",
		time.Now().Format("Monday, 2 January 2006"), t.Topic.Name, t.Topic.Focus)
	if t.Searches > 0 {
		fmt.Fprintf(&b, "You have about %s tokens for this. Use web search to find the most consequential news.\n", HumanTokens(t.Budget))
	} else {
		fmt.Fprintf(&b, "You have about %s tokens for this and no web searches.\n", HumanTokens(t.Budget))
	}
	fmt.Fprintf(&b, "Write at most %d items and at most %d words in total.", t.Items, t.Items*wordsPerItem)
	if len(t.History) > 0 {
		b.WriteString("\n\n<previously_reported>\n")
		for _, r := range t.History {
			b.WriteString(r.line() + "\n")
		}
		b.WriteString("</previously_reported>\n\n" + historyInstruction)
	}
	switch {
	case t.Prior != "" && t.Searches > 0:
		fmt.Fprintf(&b, "\n\nThis digest was written earlier today:\n\n<digest>\n%s\n</digest>\n\n"+
			"Search for consequential items it missed. If the topic itself was quiet, widen to adjacent areas with notable news and put those items after a line \"**Related**\". "+
			"Then reply with the full revised digest within the limits above, dropping weaker items to make room for stronger ones. If you find nothing better, return it unchanged.", prior)
	case t.Prior != "":
		fmt.Fprintf(&b, "\n\nThis digest was written earlier today:\n\n<digest>\n%s\n</digest>\n\n"+
			"Do not search. Tighten it: rank the most consequential items first, cut weak or stale ones and make every item concise. "+
			"Reply with the full revised digest within the limits above.", prior)
	}
	return b.String()
}

// Update reports progress on one section. Index -1 is the overview.
type Update struct {
	Index   int
	Section Section
}

const (
	skippedBudget = "skipped: not enough token budget left for one search"
	skippedRead   = "skipped: max_read_minutes leaves no room for this topic"
	skippedOver   = "skipped: the run went over its token budget"
	noSearchNote  = "written without a web search, so it may miss today's news"
)

// maxPasses bounds top-up passes per topic, so a run always ends. The API
// gets more: each search needs its worst case free, which leaves a pass
// fewer searches than its budget pays for, and a fresh pass, with a small
// context again, makes the next search cheaper.
func maxPasses(provider string) int {
	if provider == "claude-code" {
		return 6
	}
	return 15
}

type Generator struct {
	backend Backend
	cfg     config.Config
	caps    []int // items per topic, from max_read_minutes
	// partial is set when cfg covers only some of the configured topics:
	// the run is merged into today's journal.
	partial bool
	// history is what earlier days reported, per topic, loaded by Run.
	history map[string][]Reported
}

// NewGenerator resolves an auto (0) token budget to the baseline.
func NewGenerator(cfg config.Config, backend Backend) *Generator {
	ResolveBudget(&cfg)
	return &Generator{backend: backend, cfg: cfg, caps: itemCaps(cfg), partial: len(cfg.AllTopics) > len(cfg.Topics)}
}

// NewJournal returns an empty journal with the budget split across topics.
// The split is an estimate: Run fixes each topic's allocation as it starts.
func (g *Generator) NewJournal() *Journal {
	j := g.emptyJournal()
	a := Assess(g.cfg)
	j.BudgetLevel, j.BudgetWarnings = a.Level, a.Messages
	return j
}

func (g *Generator) emptyJournal() *Journal {
	j := &Journal{
		Date:        Today(),
		Model:       g.cfg.Model,
		Provider:    g.backend.Name(),
		TokenBudget: g.cfg.TokenBudget,
		Overview:    Section{Topic: "Overview", Status: StatusPending},
		Subset:      g.partial,
		Caps:        topicCaps(g.cfg),
	}
	reserve, shares := split(g.cfg)
	j.Overview.Allocated = reserve
	for i, t := range g.cfg.Topics {
		j.Sections = append(j.Sections, &Section{Topic: t.Name, Status: StatusPending, Allocated: shares[i]})
	}
	return j
}

// task plans a search pass over topic i with budget. A first pass gets as
// many items as its searches support; a top-up keeps the topic's cap.
func (g *Generator) task(i int, budget int64, prior string) Task {
	s := planSearches(g.cfg, budget)
	items := g.caps[i]
	if prior == "" {
		items = min(itemCeiling(s), items)
	}
	return Task{Topic: g.cfg.Topics[i], Budget: budget, Searches: s, Items: items, Prior: prior, History: g.history[g.cfg.Topics[i].Name]}
}

// needBudget is the budget a pass's prompt is sized with when working out
// what its first search needs: the longest HumanTokens text below 1M.
const needBudget = 999_999

// need is the least a search pass over topic i, revising prior, can be
// allocated: one pass cost, and on the API at least what its first search
// reserves by the run's calibration so far, with the window it really
// sends, so a pass is never started that cannot search. A first pass is
// sized for the topic's full item cap, the most it may write.
func (g *Generator) need(cal *calibration, i int, prior string) int64 {
	p := g.cfg.Provider.Type
	if p == "claude-code" {
		return passCost(p)
	}
	t := Task{Topic: g.cfg.Topics[i], Budget: needBudget, Searches: 1, Items: g.caps[i], Prior: prior, History: g.history[g.cfg.Topics[i].Name]}
	return max(passCost(p), cal.firstSearch(t))
}

func (g *Generator) margin(alloc int64, search bool) int64 {
	return margin(g.cfg.Provider.Type, alloc, search)
}

func (g *Generator) spendable(room int64, n int, search bool) int64 {
	return spendable(g.cfg.Provider.Type, room, n, search)
}

// pool is the topic budget shared by concurrent passes. A pass holds its
// allocation plus margin as a claim when it starts, may charge what it
// spends as it goes, and settles with its actual use. reserve is the rest
// of the run's budget, the overview's: topic spending beyond total plus
// reserve means the run went over its budget. cal is the run's calibration
// of search requests, shared by its passes.
type pool struct {
	mu          sync.Mutex
	total       int64
	reserve     int64
	spent       int64 // by settled passes
	outstanding int64 // held by running passes
	live        int64 // charged so far by running passes
	excess      int64 // charged by running passes beyond what they hold
	cal         *calibration
}

// claim is one running pass's hold on the pool.
type claim struct {
	p          *pool
	held, used int64
}

func (p *pool) free() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.total - p.spent - p.outstanding - p.excess
}

func (p *pool) used() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.spent
}

// over reports whether the run has spent more than its budget.
func (p *pool) over() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.overLocked()
}

func (p *pool) overLocked() bool {
	return p.spent+p.live > p.total+p.reserve
}

// hold sets aside exactly n tokens if they are free.
func (p *pool) hold(n int64) *claim {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.total-p.spent-p.outstanding-p.excess < n {
		return nil
	}
	p.outstanding += n
	return &claim{p: p, held: n}
}

// charge records tokens the pass has spent so far.
func (c *claim) charge(n int64) {
	p := c.p
	p.mu.Lock()
	defer p.mu.Unlock()
	p.excess -= max(0, c.used-c.held)
	c.used += n
	p.live += n
	p.excess += max(0, c.used-c.held)
}

// room is what the pass may still spend: what it holds less what it has
// charged, less any overshoot of other passes that has eaten into the
// pool. It is 0 once the run has gone over its budget, so no request
// starts after that.
func (c *claim) room() int64 {
	p := c.p
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.overLocked() {
		return 0
	}
	return c.held - c.used + min(0, p.total-p.spent-p.outstanding-p.excess)
}

// settle releases the hold and books what the pass spent.
func (c *claim) settle(used int64) {
	p := c.p
	p.mu.Lock()
	defer p.mu.Unlock()
	p.outstanding -= c.held
	p.live -= c.used
	p.excess -= max(0, c.used-c.held)
	p.spent += used
}

// Run researches every topic of j, spends leftover budget on top-up passes
// and writes the overview, sending an Update whenever a section changes.
// It fills j in place and closes updates.
//
// No pass starts unless its allocation, plus a safety margin for
// claude-code, fits the budget left. The API backend enforces each
// allocation per request; claude-code cannot be stopped mid-run, so the
// margin absorbs an overshoot of up to 10% plus one search per pass.
func (g *Generator) Run(ctx context.Context, j *Journal, updates chan<- Update) {
	defer close(updates)
	send := func(i int, s *Section) {
		select {
		case updates <- Update{Index: i, Section: *s}:
		case <-ctx.Done():
		}
	}
	p := &pool{total: j.TokenBudget - j.Overview.Allocated, reserve: j.Overview.Allocated, cal: newCalibration()}
	others := g.others(j.Date)
	g.history = g.loadHistory(j.Date)
	g.research(ctx, j, p, send)
	g.topUp(ctx, j, p, send)
	g.narrow(ctx, j, p, others, send)
	g.overview(ctx, j, others, send)
	// Defence in depth: should a run go over its budget anyway, no pass or
	// request starts after it does, and the journal says by how much.
	j.Overshoot = max(0, j.TokensUsed()-j.TokenBudget)
	j.GeneratedAt = time.Now()
}

// research runs the first pass of every topic. Each allocation is the
// topic's weighted share of what is free when it starts, so topics that
// finish under budget fund later ones.
func (g *Generator) research(ctx context.Context, j *Journal, p *pool, send func(int, *Section)) {
	ws, weights := activeWeights(g.cfg, g.caps)
	sem := make(chan struct{}, g.cfg.Concurrency)
	var wg sync.WaitGroup
	defer wg.Wait()
	for i, sec := range j.Sections {
		w := ws[i]
		if w == 0 {
			sec.Allocated, sec.Status, sec.Error = 0, StatusFailed, skippedRead
			send(i, sec)
			continue
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		frac := w / max(weights, w)
		weights -= w
		least := g.need(p.cal, i, "")
		room := g.spendable(p.free(), 1, true)
		alloc := min(room, max(least, int64(frac*float64(room))))
		var c *claim
		if alloc >= least {
			c = p.hold(alloc + g.margin(alloc, true))
		}
		if c == nil {
			<-sem
			sec.Status, sec.Error = StatusFailed, skippedBudget
			if p.over() {
				sec.Error = skippedOver
			}
			send(i, sec)
			continue
		}
		sec.Allocated, sec.Status = alloc, StatusRunning
		send(i, sec)
		g.estimatePending(j, ws, i+1, p.free(), weights, send)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			g.pass(ctx, c, i, sec, g.task(i, alloc, ""))
			send(i, sec)
		}()
	}
}

// estimatePending refreshes the display budget of topics not started yet.
func (g *Generator) estimatePending(j *Journal, ws []float64, from int, free int64, weights float64, send func(int, *Section)) {
	for i := from; i < len(j.Sections); i++ {
		if ws[i] == 0 {
			continue
		}
		j.Sections[i].Allocated = 0
		if weights > 0 && free > 0 {
			j.Sections[i].Allocated = min(free, int64(float64(free)*ws[i]/weights))
		}
		send(i, j.Sections[i])
	}
}

// topUp spends the leftover topic budget in rounds of passes: topics
// skipped for budget get their first pass, the rest deepen or widen their
// digest. Rounds go on while one covers at least a full search pass and the
// last one made progress, or found its picks could not search because the
// calibration grew meanwhile (the next round is sized by it).
func (g *Generator) topUp(ctx context.Context, j *Journal, p *pool, send func(int, *Section)) {
	for ctx.Err() == nil {
		k, r := p.cal.get()
		picks, allocs := g.pickPasses(j, p)
		if len(picks) == 0 {
			return
		}
		if !g.round(ctx, j, p, send, picks, allocs, true) {
			if k2, r2 := p.cal.get(); k2 == k && r2 == r {
				return
			}
		}
	}
}

// narrow spends what is left, if the run would end short of the middle of
// its target band, on a last pass per topic that rewrites the digest
// without searching. It is the only pass that does not search, so a topic
// gets at most one per run: a top-up pass that cannot search does not run.
func (g *Generator) narrow(ctx context.Context, j *Journal, p *pool, others []*Section, send func(int, *Section)) {
	if ctx.Err() != nil || float64(p.used()+overviewNeed(overviewInput(combine(j.Sections, others)))) >= narrowBelow*float64(j.TokenBudget) {
		return
	}
	cands, score := g.candidates(j, false)
	least := make([]int64, len(cands))
	for k, i := range cands {
		least[k] = g.narrowNeed(i, j.Sections[i].Markdown)
	}
	for n := len(cands); n > 0; n-- {
		if room := g.spendable(p.free(), n, false); room >= sum(least[:n]) {
			g.round(ctx, j, p, send, cands[:n], spread(room, least[:n], cands[:n], score), false)
			return
		}
	}
}

// narrowNeed is the least a narrow pass over topic i, rewriting prior, can
// be allocated: narrowMin, and on the API at least its request's window,
// history and prior digest included, plus the least room a turn is started
// with, so a funded narrow pass always sends its request.
func (g *Generator) narrowNeed(i int, prior string) int64 {
	if g.cfg.Provider.Type == "claude-code" {
		return narrowMin
	}
	t := g.task(i, needBudget, prior)
	t.Searches = 0
	return max(narrowMin, firstWindow(t)+minTurnTokens)
}

// round runs a pass for each pick, at most Concurrency at a time, and
// reports whether any pass made progress (see pass).
func (g *Generator) round(ctx context.Context, j *Journal, p *pool, send func(int, *Section), picks []int, allocs []int64, search bool) bool {
	sem := make(chan struct{}, g.cfg.Concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	progress := false
	for k, i := range picks {
		alloc := allocs[k]
		c := p.hold(alloc + g.margin(alloc, search))
		if c == nil {
			break
		}
		sec := j.Sections[i]
		t := g.task(i, alloc, sec.Markdown)
		retry := sec.Status == StatusFailed
		if !search {
			t.Searches = 0
		}
		if sec.Status == StatusFailed {
			// A topic skipped for budget gets its first pass.
			sec.Allocated, sec.Error = 0, ""
		}
		sec.Allocated += alloc
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				c.settle(0)
				if retry {
					sec.Error = "cancelled"
				}
				return
			}
			defer func() { <-sem }()
			status := sec.Status
			sec.Status = StatusRunning
			send(i, sec)
			ok := g.pass(ctx, c, i, sec, t)
			if !retry && sec.Status == StatusRunning {
				// A top-up that could not search leaves the digest as it was.
				sec.Status = status
			}
			send(i, sec)
			if ok {
				mu.Lock()
				progress = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return progress
}

// pass runs t over topic i, folds the result into sec and caps its items.
// It reports progress: the pass spent tokens without error and either
// searched or changed the digest. A pass that found no room for its first
// search spends nothing and changes nothing, except that a first pass is
// marked skipped for budget, to be retried by a top-up if room allows.
func (g *Generator) pass(ctx context.Context, c *claim, i int, sec *Section, t Task) bool {
	t.meter, t.cal = c, c.p.cal
	res := g.backend.Research(ctx, t)
	c.settle(res.Tokens)
	if errors.Is(res.Err, errNoSearchRoom) && res.Tokens == 0 {
		sec.Allocated -= t.Budget
		if t.Prior == "" {
			sec.Allocated, sec.Status, sec.Error = 0, StatusFailed, skippedBudget
			if c.p.over() {
				sec.Error = skippedOver
			}
		}
		return false
	}
	before := sec.Markdown
	if t.Prior == "" {
		apply(sec, res)
		if sec.Status == StatusDone && t.Searches > 0 && res.Searches == 0 {
			sec.Error = noSearchNote
		}
	} else {
		applyPass(sec, res)
	}
	sec.Markdown = capItems(sec.Markdown, g.caps[i])
	sec.Sources = pruneSources(sec.Markdown, sec.Sources)
	return res.Tokens > 0 && res.Err == nil && (res.Searches > 0 || sec.Markdown != before)
}

// candidates lists the topics that may get another pass, best first:
// topics skipped for budget (when retry is set), then by score.
func (g *Generator) candidates(j *Journal, retry bool) ([]int, map[int]float64) {
	var cands []int
	score := map[int]float64{}
	first := map[int]bool{}
	for i, sec := range j.Sections {
		switch {
		case retry && sec.Status == StatusFailed && sec.Error == skippedBudget:
			first[i] = true
		case sec.Status == StatusDone && sec.Passes < maxPasses(g.cfg.Provider.Type):
		default:
			continue
		}
		cands = append(cands, i)
		score[i] = g.passScore(i, sec)
	}
	slices.SortStableFunc(cands, func(a, b int) int {
		if first[a] != first[b] {
			if first[a] {
				return -1
			}
			return 1
		}
		return cmp.Compare(score[b], score[a])
	})
	return cands, score
}

// pickPasses chooses which topics get a search pass and how much each
// gets: as many of the best candidates as can each get what its first
// search needs by the run's calibration (see need), with the rest split by
// score.
func (g *Generator) pickPasses(j *Journal, p *pool) ([]int, []int64) {
	free := p.free()
	cands, score := g.candidates(j, true)
	least := make([]int64, len(cands))
	for k, i := range cands {
		least[k] = g.need(p.cal, i, j.Sections[i].Markdown)
	}
	for n := len(cands); n > 0; n-- {
		if room := g.spendable(free, n, true); room >= sum(least[:n]) {
			return cands[:n], spread(room, least[:n], cands[:n], score)
		}
	}
	return nil, nil
}

// spread gives each pick its least plus a share of the rest of room by
// score.
func spread(room int64, least []int64, picks []int, score map[int]float64) []int64 {
	var total float64
	for _, i := range picks {
		total += score[i]
	}
	extra := room - sum(least)
	allocs := make([]int64, len(picks))
	for k, i := range picks {
		allocs[k] = least[k]
		if total > 0 {
			allocs[k] += int64(float64(extra) * score[i] / total)
		}
	}
	return allocs
}

func sum(xs []int64) int64 {
	var s int64
	for _, x := range xs {
		s += x
	}
	return s
}

// passScore favours heavy topics, topics that used all their searches and
// topics that came back thin, and decays with each pass already run.
func (g *Generator) passScore(i int, sec *Section) float64 {
	s := 1.0
	if sec.Searches >= planSearches(g.cfg, sec.Allocated) {
		s += 0.5
	}
	if countItems(sec.Markdown)*2 <= g.caps[i] {
		s += 0.5
	}
	return weight(g.cfg.Topics[i]) * s / float64(1+sec.Passes)
}

// others are the sections of today's saved journal that a run over some
// of the topics is merged with.
func (g *Generator) others(date string) []*Section {
	if !g.partial {
		return nil
	}
	if prev, err := LoadJournal(date); err == nil && prev != nil {
		return prev.Sections
	}
	return nil
}

// loadHistory loads what the history_days before date reported on the
// topics this run researches.
func (g *Generator) loadHistory(date string) map[string][]Reported {
	names := make([]string, len(g.cfg.Topics))
	for i, t := range g.cfg.Topics {
		names[i] = t.Name
	}
	return LoadHistory(date, g.cfg.HistoryDays, names)
}

// overview summarizes the finished topics, and those of today's journal a
// partial run is merged with, from the reserve, if what is left of the
// budget still covers it. Otherwise saving keeps the earlier overview,
// marked as possibly stale.
func (g *Generator) overview(ctx context.Context, j *Journal, others []*Section, send func(int, *Section)) {
	ov := &j.Overview
	sections := combine(j.Sections, others)
	input := overviewInput(sections)
	switch {
	case ctx.Err() != nil:
		ov.Status, ov.Error = StatusFailed, "cancelled"
	case input == "":
		ov.Status, ov.Error = StatusFailed, "no topics were summarized"
	case j.TokenBudget-j.TokensUsed() < overviewNeed(input):
		ov.Status, ov.Error = StatusFailed, "skipped: not enough token budget left for the overview"
	default:
		ov.Status = StatusRunning
		send(-1, ov)
		apply(ov, g.backend.Summarize(ctx, overviewSystemPrompt, input))
		if ov.Status == StatusDone {
			ov.Markdown, ov.Highlights = capOverviewWithin(ov.Markdown, sections, overviewAllowance(g.cfg.MaxReadMinutes))
		}
	}
	send(-1, ov)
}

func overviewInput(sections []*Section) string {
	var b strings.Builder
	for _, s := range sections {
		if s.Status == StatusDone {
			fmt.Fprintf(&b, "# %s\n\n%s\n\n", s.Topic, s.Markdown)
		}
	}
	return b.String()
}

// overviewNeed is what the overview may cost: roughly 3 characters per
// input token, plus the output cap.
func overviewNeed(input string) int64 {
	return int64(len(input)/3) + 4_000
}

// apply records a backend's result in sec, without control characters
// (see StripControl): what is saved is clean, piped or not.
func apply(sec *Section, res Result) {
	sec.TokensUsed += res.Tokens
	sec.Searches += res.Searches
	sec.Truncated = res.Truncated
	sec.Markdown, sec.Sources = StripControl(res.Markdown), cleanSources(res.Sources)
	switch {
	case res.Err != nil:
		sec.Status, sec.Error = StatusFailed, StripControl(res.Err.Error())
	case strings.TrimSpace(res.Markdown) == "":
		sec.Status, sec.Error = StatusFailed, "no summary was produced within the token budget"
	default:
		sec.Status = StatusDone
	}
}

// applyPass replaces the digest and its sources with a top-up pass's
// revision, keeping the earlier one if the pass failed or was cut short.
func applyPass(sec *Section, res Result) {
	sec.TokensUsed += res.Tokens
	sec.Searches += res.Searches
	sec.Passes++
	sec.Status = StatusDone
	if res.Err != nil || res.Truncated || strings.TrimSpace(res.Markdown) == "" {
		return
	}
	sec.Markdown, sec.Sources, sec.Truncated, sec.Error = StripControl(res.Markdown), cleanSources(res.Sources), false, ""
}

// cleanSources is sources without control characters in their titles and
// URLs.
func cleanSources(sources []Source) []Source {
	if sources == nil {
		return nil
	}
	out := make([]Source, len(sources))
	for i, s := range sources {
		out[i] = Source{Title: StripControl(s.Title), URL: StripControl(s.URL)}
	}
	return out
}

// capItems keeps a digest within n items, Related ones included, and
// within n items' words (plus a Related label), whatever its format. Items
// are kept whole and dropped from the end; one item always stays, trimmed
// at a sentence if it alone is too long. A digest without recognisable
// items is trimmed the same way. Non-empty input never comes back empty:
// if trimming would leave nothing, as when a heading alone is longer than
// the budget, the first line itself is cut and marked. Control characters
// are removed first (see StripControl).
func capItems(md string, n int) string {
	if n <= 0 {
		return ""
	}
	md = StripControl(md)
	blocks := splitDigest(md)
	budget := n * wordsPerItem
	hasItems := slices.ContainsFunc(blocks, func(b block) bool { return b.item })
	var out []string
	used, items := 0, 0
	label := "" // a Related label, kept only if an item follows it
	for _, b := range blocks {
		// Words beyond the Related allowance count against the budget.
		w := countWords(b.text) + max(0, countWords(label)-relatedAllowance)
		switch {
		case b.label:
			label = b.text
			continue
		case !b.item && hasItems:
			// A preamble: allow it little, so items keep their room.
			out = append(out, trimWords(b.text, budget/4))
			used += countWords(out[len(out)-1])
			continue
		case !b.item:
			return keepSome(strings.TrimSpace(trimWords(md, budget)), md, budget)
		case items == n:
		case used+w > budget && items == 0:
			if t := trimWords(b.text, budget-used); t != "" {
				out = append(out, label, t)
				items++
			}
		case used+w <= budget:
			out = append(out, label, b.text)
			used += w
			items++
			label = ""
			continue
		}
		break
	}
	return keepSome(strings.TrimSpace(strings.Join(slices.DeleteFunc(out, func(s string) bool { return s == "" }), "\n")), md, budget)
}

// keepSome returns out, unless trimming md left nothing of it: then md's
// first line, cut to limit words with the mark.
func keepSome(out, md string, limit int) string {
	if out != "" || strings.TrimSpace(md) == "" {
		return out
	}
	line, _, _ := strings.Cut(strings.TrimSpace(md), "\n")
	words := strings.Fields(line)
	return strings.Join(words[:min(len(words), max(1, limit-1))], " ") + " …"
}

// block is a digest's preamble, one of its items or a Related label.
type block struct {
	text        string
	item, label bool
	line        int // where it starts in the trimmed digest
}

// splitDigest splits a digest into blocks. Items start at "### " headings,
// else "## " headings, else top-level bullets, else bold-led lines; a
// Related label takes the non-item lines after it.
func splitDigest(md string) []block {
	lines := strings.Split(strings.TrimSpace(md), "\n")
	var start func(string) bool
	for _, f := range []func(string) bool{
		func(l string) bool { return strings.HasPrefix(l, "### ") },
		func(l string) bool { return strings.HasPrefix(l, "## ") },
		isBullet,
		func(l string) bool { return strings.HasPrefix(l, "**") },
	} {
		if slices.ContainsFunc(lines, func(l string) bool { return f(l) && !isRelated(l) }) {
			start = f
			break
		}
	}
	var blocks []block
	var cur block
	var text []string
	flush := func(next block, line string) {
		if len(text) > 0 {
			cur.text = strings.Join(text, "\n")
			blocks = append(blocks, cur)
		}
		cur, text = next, []string{line}
	}
	for i, l := range lines {
		switch {
		case isRelated(l):
			flush(block{label: true, line: i}, l)
		case start != nil && start(l):
			flush(block{item: true, line: i}, l)
		default:
			text = append(text, l)
		}
	}
	flush(block{}, "")
	return blocks
}

func isBullet(l string) bool {
	if strings.HasPrefix(l, "- ") || strings.HasPrefix(l, "* ") || strings.HasPrefix(l, "+ ") {
		return true
	}
	digits := len(l) - len(strings.TrimLeft(l, "0123456789"))
	return digits > 0 && (strings.HasPrefix(l[digits:], ". ") || strings.HasPrefix(l[digits:], ") "))
}

// isRelated reports whether a line is a Related label, however spelled:
// **Related**, **Related:**, ### Related, Related: and the like.
func isRelated(l string) bool {
	t := strings.ToLower(strings.Trim(l, "#*_: \t"))
	return t == "related" || t == "related areas" || t == "related news"
}

// trimWords cuts text to at most limit words, marking the cut with "…".
// It cuts at the last sentence or line end that keeps at least half of
// them and does not end on a heading, else between words. The cut never
// splits a [link](url) or leaves a code fence open, and headings left with
// no body after it are dropped; if nothing is left, it returns "".
func trimWords(text string, limit int) string {
	if countWords(text) <= limit {
		return text
	}
	if limit < 2 {
		return ""
	}
	head := text[:wordEnd(text, limit-1)] // room for the mark
	if i := sentenceEnd(head); i > 0 && countWords(head[:i]) >= (limit-1)/2 && !isHeading(lastLine(head[:i])) {
		head = head[:i]
	}
	// Back off to before a link the cut would split.
	if i := strings.LastIndex(head, "["); i >= 0 {
		if m := linkRE.FindStringIndex(text[i:]); m != nil && i+m[1] > len(head) {
			head = head[:i]
		}
	}
	// Back off to before a code block the cut would leave open.
	lines := strings.Split(head, "\n")
	open := -1
	for k, l := range lines {
		if t := strings.TrimSpace(l); strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			if open < 0 {
				open = k
			} else {
				open = -1
			}
		}
	}
	if open >= 0 {
		lines = lines[:open]
	}
	// Drop headings the cut left with no body.
	for len(lines) > 0 && (strings.TrimSpace(lines[len(lines)-1]) == "" || isHeading(lines[len(lines)-1])) {
		lines = lines[:len(lines)-1]
	}
	head = strings.TrimRight(strings.Join(lines, "\n"), " \t\n")
	if head == "" {
		return ""
	}
	return head + " …"
}

// linkRE matches a Markdown link at the start of text.
var linkRE = regexp.MustCompile(`^\[[^\]]*\]\([^)]*\)`)

// isHeading reports whether a line is a Markdown ATX heading.
func isHeading(l string) bool {
	t := strings.TrimLeft(strings.TrimSpace(l), "#")
	return strings.HasPrefix(strings.TrimSpace(l), "#") && (t == "" || t[0] == ' ')
}

func lastLine(s string) string {
	s = strings.TrimRight(s, " \t\n")
	return s[strings.LastIndex(s, "\n")+1:]
}

// wordEnd is the byte offset just after text's nth word.
func wordEnd(text string, n int) int {
	words, in := 0, false
	for i, r := range text {
		space := unicode.IsSpace(r)
		if in && space {
			if words++; words == n {
				return i
			}
		}
		in = !space
	}
	return len(text)
}

// sentenceEnd is the offset after the last sentence or line end in text.
func sentenceEnd(text string) int {
	for i := len(text) - 1; i > 0; i-- {
		if text[i] == '\n' || strings.ContainsRune(".!?", rune(text[i-1])) && unicode.IsSpace(rune(text[i])) {
			return i
		}
	}
	return 0
}

// pruneSources keeps no more sources than the digest has items.
func pruneSources(md string, sources []Source) []Source {
	return sources[:min(len(sources), max(1, countItems(md)))]
}
