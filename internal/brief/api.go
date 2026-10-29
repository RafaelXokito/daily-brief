package brief

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/shared/constant"

	"github.com/rafaelpereira/daily-brief/internal/config"
)

const (
	// Turns are not started when less than this is left in a topic's budget.
	minTurnTokens = 4_000
	// Smallest total the API accepts for an advisory task budget.
	minTaskBudget = 20_000
	// Most requests one pass makes, pause_turn continuations included.
	maxRequests = 12
	// searchToolTokens is a generous allowance for what the server adds to
	// a search request's first context beyond our own text: the search
	// tool's definition and system prompt, and dynamic filtering's
	// scaffolding. It only matters until the first response is measured.
	searchToolTokens = 4_000
	// Most output a writing-only request may reserve.
	maxOutTokens = 16_000
	// initialResult is the size assumed for one search's results until a
	// larger one is measured. Results usually run 10-30k tokens.
	initialResult = 30_000
	// searchIterations (K) is how many sampling iterations a request
	// offering one search is assumed to take at most. The usual request
	// takes two (one issues the search, one reads the results and writes);
	// each attempt at a further search costs another, refused with
	// max_uses_exceeded, and dynamic filtering may add steps. Five covers
	// three refused attempts, and the reservation's overcount (below)
	// covers more. A run raises it when it measures more.
	searchIterations = 5
	// searchSlack covers what the iterations write and later ones read
	// again: draft text written before a refused search, the search calls
	// and their error blocks.
	searchSlack = 6_000
)

// searchMaxTokens is max_tokens for a request that may search. It must
// hold the whole digest at the item cap (about 45 words of 1.5 tokens per
// item, doubled for links and Markdown) plus about 4k for the search call
// and any thinking. Keeping it well under 16k lets a search fit smaller
// budgets, since every search request reserves it once per iteration.
func searchMaxTokens(items int) int64 {
	return 4_000 + 2*digestTokens(items)
}

func digestTokens(items int) int64 {
	return int64(items) * wordsPerItem * 3 / 2
}

// searchReserve is the most a request offering one search is assumed to
// spend with k sampling iterations, a context of c tokens, results of r
// and max_tokens o:
//
//	k·(c + r) + k·o + searchSlack
//
// Every iteration is counted as reading the whole context and the results,
// although the first reads only the context: that overcount (r, at least
// 30k) is the margin for output read again by later iterations. Every
// iteration may write up to o, so the bound holds whether max_tokens caps
// each iteration or the whole response.
func searchReserve(k, c, r, o int64) int64 {
	return k*(c+r) + k*o + searchSlack
}

// estimateTokens is a generous token count for text: 2.5 characters a
// token, where English prose runs about 4.
func estimateTokens(s string) int64 {
	return int64(len(s))*2/5 + 1
}

// calibration is what a run has learned about search requests: the most
// sampling iterations one has taken (at least searchIterations) and the
// largest result measured (at least initialResult). It only grows, so a
// server that takes more iterations than assumed makes every later request
// of the run, in any pass, reserve more.
type calibration struct {
	mu     sync.Mutex
	iters  int64
	result int64
}

func newCalibration() *calibration {
	return &calibration{iters: searchIterations, result: initialResult}
}

func (c *calibration) get() (iters, result int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.iters, c.result
}

// observe raises the estimates to a search request's measurements.
func (c *calibration) observe(iters, result int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.iters = max(c.iters, iters)
	c.result = max(c.result, result)
}

// errNoSearchRoom is returned, with nothing spent, by a pass planned to
// search whose first search does not fit its budget: it never falls back to
// writing a digest without one.
var errNoSearchRoom = errors.New("not enough token budget left for one search")

// researchPrompt is the first user message of t's search loop.
func researchPrompt(t Task) string {
	prompt := taskPrompt(t)
	if t.Searches > 0 {
		prompt += "\n\nRun one web search per turn, then write the digest."
	}
	return prompt
}

// firstWindow estimates the context of t's first request, a top-up's prior
// digest included.
func firstWindow(t Task) int64 {
	return estimateTokens(researchSystemPrompt+researchPrompt(t)) + searchToolTokens
}

// firstSearch is what t's first search reserves by what the run has
// measured so far: the least budget with which it can search at all.
func (c *calibration) firstSearch(t Task) int64 {
	k, r := c.get()
	return searchReserve(k, firstWindow(t), r, searchMaxTokens(t.Items))
}

// continuePrompt asks for one more search after a digest is written.
func continuePrompt(left, searches int64) string {
	return fmt.Sprintf("About %s tokens and %d searches are left. Run one more search for a consequential item you haven't covered "+
		"(widen to related areas if the topic is quiet, listing those after a line \"**Related**\"), then rewrite the full digest within the same limits. "+
		"If nothing more is worth adding, reply with the digest unchanged.", HumanTokens(left), searches)
}

// apiBackend talks to the Messages API directly: the Claude API, Claude
// Platform on AWS or Vertex AI, which differ only in features supported.
type apiBackend struct {
	cfg      config.Config
	messages anthropic.BetaMessageService
	name     string

	dynamicSearch bool // web_search_20260209 rather than the basic tool
	fallbacks     bool // server-side refusal fallbacks
	taskBudget    bool
}

func (b *apiBackend) Name() string { return b.name }

func (b *apiBackend) params(maxTokens int64, system string) anthropic.BetaMessageNewParams {
	p := anthropic.BetaMessageNewParams{
		Model:     b.cfg.Model,
		MaxTokens: maxTokens,
		System:    []anthropic.BetaTextBlockParam{{Text: system}},
		OutputConfig: anthropic.BetaOutputConfigParam{
			Effort: anthropic.BetaOutputConfigEffort(b.cfg.Effort),
		},
	}
	if b.fallbacks {
		// If a request is declined by a safety classifier, let the API
		// re-serve it on a fallback model instead of returning nothing.
		p.Fallbacks = anthropic.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()}
		p.Betas = append(p.Betas, anthropic.AnthropicBetaServerSideFallback2026_07_01)
	}
	return p
}

func (b *apiBackend) searchTool(maxUses int64) anthropic.BetaToolUnionParam {
	var loc anthropic.BetaUserLocationParam
	if b.cfg.Country != "" {
		loc.Country = anthropic.String(b.cfg.Country)
	}
	if b.cfg.City != "" {
		loc.City = anthropic.String(b.cfg.City)
	}
	if b.dynamicSearch {
		return anthropic.BetaToolUnionParam{OfWebSearchTool20260209: &anthropic.BetaWebSearchTool20260209Param{MaxUses: anthropic.Int(maxUses), UserLocation: loc}}
	}
	return anthropic.BetaToolUnionParam{OfWebSearchTool20250305: &anthropic.BetaWebSearchTool20250305Param{MaxUses: anthropic.Int(maxUses), UserLocation: loc}}
}

// Research runs a client-driven search loop. Each request offers one
// search; after Claude writes its digest, the loop asks for one more search
// and a rewritten digest while searches remain and the next request's worst
// case fits what is left. The digest is the last complete one written.
//
// What is left is the pass's budget less what it spent, and less any
// overshoot of the run it belongs to (task.meter), which stops the pass at
// once if the run as a whole has gone over its budget.
func (b *apiBackend) Research(ctx context.Context, task Task) Result {
	var res Result
	budget := task.Budget
	cal := task.cal
	if cal == nil {
		cal = newCalibration()
	}
	left := func() int64 {
		l := budget - res.Tokens
		if task.meter != nil {
			l = min(l, task.meter.room())
		}
		return l
	}
	searchOut := searchMaxTokens(task.Items)
	reserve := func(c int64) int64 {
		k, r := cal.get()
		return searchReserve(k, c, r, searchOut)
	}
	messages := []anthropic.BetaMessageParam{
		anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(researchPrompt(task))),
	}
	// window is what the next request sends: estimated for the first, then
	// measured from the last sampling iteration of the previous response.
	window := firstWindow(task)
	searchesLeft := task.Searches

	var good string // the latest complete digest
	var goodSources []Source
	var content []anthropic.BetaContentBlockUnion // this turn's, across pause_turn
	var turnSearches int64
loop:
	for n := 0; ; n++ {
		room := left()
		search := searchesLeft > 0 && reserve(window) <= room
		switch {
		case n == 0 && task.Searches > 0 && !search:
			// A search pass that cannot search would only rewrite under a
			// prompt asking for searches: spend nothing instead.
			res.Err = errNoSearchRoom
			return res
		case len(content) == 0 && good != "" && !search:
			// The calibration grew since more was asked for: keep the digest
			// rather than answer the request for a search without one.
			break loop
		}
		if n == maxRequests || !search && room-window < minTurnTokens {
			// Cut off mid-turn.
			res.Truncated = true
			break
		}
		// A writing-only request is one sampling iteration: it spends at
		// most its context plus max_tokens.
		params := b.params(min(maxOutTokens, room-window), researchSystemPrompt)
		params.Messages = messages
		if search {
			params.MaxTokens = searchOut
			params.Tools = []anthropic.BetaToolUnionParam{b.searchTool(1)}
		} else if len(messages) > 1 {
			// Earlier turns used the tool, so keep it declared but disabled.
			params.Tools = []anthropic.BetaToolUnionParam{b.searchTool(1)}
			params.ToolChoice = anthropic.BetaToolChoiceUnionParam{OfNone: &anthropic.BetaToolChoiceNoneParam{}}
		}
		// Advisory: lets Claude pace its searching to the topic's budget.
		// The hard limit is enforced by this loop.
		if b.taskBudget && budget >= minTaskBudget {
			params.OutputConfig.TaskBudget = anthropic.BetaTokenTaskBudgetParam{Total: budget}
			params.Betas = append(params.Betas, anthropic.AnthropicBetaTaskBudgets2026_03_13)
		}

		resp, err := b.messages.New(ctx, params)
		if err != nil {
			// Keep the digest written so far rather than nothing.
			if good == "" {
				if good, goodSources = digest(content); good == "" {
					res.Err = describeError(err)
					return res
				}
			}
			res.Truncated = true
			break
		}
		u := resp.Usage
		turn := u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens + u.OutputTokens
		res.Tokens += turn
		if task.meter != nil {
			task.meter.charge(turn)
		}
		next, grown, iters := measure(u.Iterations, turn)
		s := u.ServerToolUse.WebSearchRequests
		if len(u.Iterations) == 0 && s > 0 {
			// Without a breakdown, the growth is at most the whole turn's.
			grown = (next - window) / s
		}
		if s == 0 {
			grown = 0
		}
		if search {
			// Self-calibration: assume at least the iterations this request
			// took, and enough of them to cover what it cost.
			_, r := cal.get()
			r = max(r, grown)
			k := max(1, iters)
			for searchReserve(k, window, r, searchOut) < turn {
				k++
			}
			cal.observe(k, r)
		}
		if s > 0 {
			res.Searches += s
			turnSearches += s
			searchesLeft = max(0, searchesLeft-s)
		}
		window = next
		content = append(content, resp.Content...)

		switch resp.StopReason {
		case anthropic.BetaStopReasonPauseTurn:
			// Server-side search loop paused; resend to let it continue.
			messages = append(messages, resp.ToParam())
			continue
		case anthropic.BetaStopReasonRefusal:
			if good == "" {
				res.Err = errors.New("the model declined to cover this topic")
				return res
			}
			break loop
		case anthropic.BetaStopReasonMaxTokens:
			// A cut-off rewrite gives way to the last complete digest.
			res.Truncated = good == ""
			break loop
		}

		md, sources := digest(content)
		if good != "" && (turnSearches == 0 || md == "" || md == good) {
			// Nothing more was worth adding.
			break
		}
		good, goodSources = md, sources
		content, turnSearches = nil, 0

		// Ask for another search only if one is left and the next
		// request's worst case fits.
		room = left()
		more := continuePrompt(room, searchesLeft)
		next = window + estimateTokens(more)
		if searchesLeft == 0 || n+1 == maxRequests || reserve(next) > room {
			break
		}
		messages = append(messages, resp.ToParam(), anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(more)))
		window = next
	}
	if good == "" {
		good, goodSources = digest(content)
	}
	res.Markdown, res.Sources = good, goodSources
	return res
}

// measure reads a response's sampling iterations: the context the next
// request sends (the last message iteration's input and output, a
// fallback-served one included), the most any tool result grew the context
// between iterations, and how many sampling iterations there were. Without
// a breakdown, the whole turn is an upper bound on the context.
func measure(iters anthropic.BetaIterationsUsage, turn int64) (window, grown, n int64) {
	window = turn
	var prev int64
	for _, it := range iters {
		if it.Type != "message" && it.Type != "fallback_message" {
			continue
		}
		n++
		in := it.InputTokens + it.CacheCreationInputTokens + it.CacheReadInputTokens
		if prev > 0 {
			grown = max(grown, in-prev)
		}
		window = in + it.OutputTokens
		prev = window
	}
	return window, grown, n
}

func (b *apiBackend) Summarize(ctx context.Context, system, input string) Result {
	params := b.params(4_000, system)
	params.OutputConfig.Effort = anthropic.BetaOutputConfigEffortLow
	params.Messages = []anthropic.BetaMessageParam{
		anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(input)),
	}
	resp, err := b.messages.New(ctx, params)
	if err != nil {
		return Result{Err: describeError(err)}
	}
	u := resp.Usage
	md, _ := digest(resp.Content)
	return Result{Markdown: md, Tokens: u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens + u.OutputTokens}
}

// digest extracts the final answer: the text written after the last web
// search, plus any cited sources that are not already linked inline.
func digest(content []anthropic.BetaContentBlockUnion) (string, []Source) {
	start := 0
	for i, b := range content {
		if b.Type == "web_search_tool_result" || b.Type == "server_tool_use" {
			start = i + 1
		}
	}
	var text strings.Builder
	var sources []Source
	seen := map[string]bool{}
	for _, b := range content[start:] {
		tb, ok := b.AsAny().(anthropic.BetaTextBlock)
		if !ok {
			continue
		}
		text.WriteString(tb.Text)
		for _, c := range tb.Citations {
			if c.Type == "web_search_result_location" && c.URL != "" && !seen[c.URL] {
				seen[c.URL] = true
				sources = append(sources, Source{Title: c.Title, URL: c.URL})
			}
		}
	}
	md := strings.TrimSpace(text.String())
	var extra []Source
	for _, s := range sources {
		if !strings.Contains(md, s.URL) {
			extra = append(extra, s)
		}
	}
	return md, extra
}

func describeError(err error) error {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 401, 403:
			return fmt.Errorf("authentication failed (%d): check the credentials for your configured provider", apiErr.StatusCode)
		case 429:
			return errors.New("rate limited by the API; try again in a minute")
		case 529:
			return errors.New("the API is overloaded; try again shortly")
		}
		return fmt.Errorf("API error %d: %s", apiErr.StatusCode, apiErr.Error())
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("cancelled")
	}
	return err
}
