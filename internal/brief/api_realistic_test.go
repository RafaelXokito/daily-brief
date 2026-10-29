package brief

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// realistic models the Messages API with web search max_uses 1 as it
// behaves: the model runs one search and then writes its digest and ends
// the turn (no pause_turn), in two sampling iterations. Every retryEvery-th
// search request, the first included, the model also tries further searches
// (retries of them, at least one), each refused with max_uses_exceeded at
// the cost of another sampling iteration over the whole context; before the
// first it may write draft tokens of text. Results take their sizes from
// results in turn. Usage is cumulative over the iterations, which are
// listed too. Context is tracked per conversation, keyed by its first
// message; a request with only that message starts it afresh.
type realistic struct {
	results    []int64
	retryEvery int
	retries    int
	draft      int64
	// quietAfter, if set, makes the model add nothing (reply unchanged,
	// without searching) once a conversation has had that many searches.
	quietAfter int
	// fallback reports the final iteration as served by a fallback model.
	fallback bool

	mu       sync.Mutex
	searches int              // search requests so far, across conversations
	convs    map[string]*conv // by first message
}

type conv struct {
	last     int64 // context after the last response: its last iteration's input plus output
	searches int
	digest   string
}

const (
	simCallOut   = 400   // a search call, with a little thinking
	simRetryOut  = 300   // the second, refused search call
	simErrBlock  = 100   // the max_uses_exceeded error block
	simDigestOut = 1_500 // writing the digest
)

func (s *realistic) handle(_ int, r rawRequest) reply {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, _ := json.Marshal(r.Body)
	if sys, _ := json.Marshal(r.Body["system"]); strings.Contains(string(sys), "opening of a personal morning briefing") {
		in := int64(len(body)) / 4
		return reply{content: "[" + textBlock("## Today's highlights\n\n- x") + "]", stop: "end_turn", input: in, output: 200,
			iterations: "[" + iteration(in, 200) + "]"}
	}
	if s.convs == nil {
		s.convs = map[string]*conv{}
	}
	key := string(r.Messages[0])
	c := s.convs[key]
	// A request with one message starts a pass: a new conversation, even if
	// an earlier pass sent the same first message.
	if c == nil || len(r.Messages) == 1 {
		c = &conv{last: int64(len(body))/4 + searchToolTokens}
		s.convs[key] = c
	} else if last := r.Messages[len(r.Messages)-1]; strings.Contains(string(last), `"role":"user"`) {
		c.last += int64(len(last)) / 4
	}
	w := c.last

	if !toolsOn(r) || s.quietAfter > 0 && c.searches >= s.quietAfter {
		out := min(int64(simDigestOut), r.MaxTokens)
		stop, text := "end_turn", c.digest
		if text == "" || !toolsOn(r) {
			text = fmt.Sprintf("### Written %d\nA thing [source](https://w.example).", len(r.Messages))
		}
		if out < simDigestOut {
			stop = "max_tokens"
		}
		c.last = w + out
		c.digest = text
		return reply{content: "[" + textBlock(text) + "]", stop: stop, input: w, output: out,
			iterations: "[" + iteration(w, out) + "]"}
	}

	retry := s.retryEvery > 0 && s.searches%s.retryEvery == 0
	size := s.results[s.searches%len(s.results)]
	s.searches++
	c.searches++
	id := fmt.Sprintf("srv_%d_%d", s.searches, size)
	ins := []int64{w, w + simCallOut + size}
	outs := []int64{simCallOut, 0}
	content := searchBlocks(id)
	refused := 0
	if retry {
		refused = max(1, s.retries)
	}
	for k := range refused {
		last := len(ins) - 1
		outs[last] = simRetryOut
		if k == 0 {
			outs[last] += s.draft
		}
		ins = append(ins, ins[last]+outs[last]+simErrBlock)
		outs = append(outs, 0)
		eid := fmt.Sprintf("err_%d_%d_%d", s.searches, k, simErrBlock)
		content += fmt.Sprintf(`,{"type":"server_tool_use","id":%q,"name":"web_search","input":{"query":"q2"}},`+
			`{"type":"web_search_tool_result","tool_use_id":%q,"content":{"type":"web_search_tool_result_error","error_code":"max_uses_exceeded"}}`, eid, eid)
	}
	// The digest gets whatever max_tokens leaves after the search calls.
	var spent int64
	for _, o := range outs {
		spent += o
	}
	out := max(0, min(int64(simDigestOut), r.MaxTokens-spent))
	stop := "end_turn"
	if out < simDigestOut {
		stop = "max_tokens"
	}
	outs[len(outs)-1] += out
	c.digest = fmt.Sprintf("### Item %d\nA thing [source](https://a%d.example).\n\n### Item %d.b\nAnother [source](https://b%d.example).", s.searches, s.searches, s.searches, s.searches)
	content += "," + textBlock(c.digest)

	var in, total int64
	iters := make([]string, len(ins))
	for k := range ins {
		in += ins[k]
		total += outs[k]
		iters[k] = iteration(ins[k], outs[k])
	}
	if s.fallback {
		last := len(iters) - 1
		iters[last] = strings.Replace(iters[last], `"type":"message"`, `"type":"fallback_message"`, 1)
	}
	c.last = ins[len(ins)-1] + outs[len(outs)-1]
	return reply{content: "[" + content + "]", stop: stop, input: in, output: total, searches: 1,
		iterations: "[" + strings.Join(iters, ",") + "]"}
}

// One pass with room for several searches makes several, one per request,
// each answered by end_turn.
func TestAPIPassSearchesRepeatedly(t *testing.T) {
	sim := &realistic{results: []int64{15_000}, retryEvery: 1}
	b, reqs := scriptedAPI(t, apiBackend{}, sim.handle)
	task := Task{Topic: b.cfg.Topics[0], Budget: 400_000, Searches: planSearches(b.cfg, 400_000), Items: 8}
	res := researchWithin(t, context.Background(), b, task, 5*time.Second)
	t.Logf("planned %d searches, made %d in %d requests, spent %d of %d", task.Searches, res.Searches, len(reqs()), res.Tokens, task.Budget)
	if res.Searches < 2 || res.Searches > task.Searches {
		t.Errorf("a %d-token pass planned for %d searches made %d", task.Budget, task.Searches, res.Searches)
	}
	if res.Tokens > task.Budget || res.Err != nil || res.Truncated || !strings.Contains(res.Markdown, fmt.Sprintf("### Item %d\n", res.Searches)) {
		t.Errorf("spent %d, err %v, truncated %v, digest %q; want the last digest", res.Tokens, res.Err, res.Truncated, res.Markdown)
	}
	for i, r := range reqs() {
		if !toolsOn(r) || r.Tools[0].MaxUses != 1 {
			t.Errorf("request %d: tools %+v, want one search offered", i, r.Tools)
		}
		if wantMsgs := 2*i + 1; len(r.Messages) != wantMsgs {
			t.Errorf("request %d sent %d messages, want %d", i, len(r.Messages), wantMsgs)
		}
		if i > 0 && !strings.Contains(string(r.Messages[len(r.Messages)-1]), "Run one more search") {
			t.Errorf("request %d does not ask for one more search", i)
		}
		if strings.Contains(string(r.Messages[0]), "up to") {
			t.Errorf("prompt names a search count: %s", r.Messages[0])
		}
	}
}

// Results larger than measured so far, and further search attempts
// answered by max_uses_exceeded, stay within the pass's budget: each search
// request reserves searchIterations reads of its context and the largest
// result, and as many of its max_tokens.
func TestAPIPassStaysWithinBudgetWithLargeResultsAndRetries(t *testing.T) {
	for _, c := range []struct {
		budget int64
		result int64
		retry  int
	}{
		{150_000, 15_000, 1},
		{200_000, 25_000, 0},
		{210_000, 30_000, 1},
		{250_000, 30_000, 1},
		{300_000, 30_000, 1},
		{450_000, 30_000, 1},
		{600_000, 30_000, 1},
	} {
		sim := &realistic{results: []int64{c.result}, retryEvery: c.retry}
		b, reqs := scriptedAPI(t, apiBackend{}, sim.handle)
		task := Task{Topic: b.cfg.Topics[0], Budget: c.budget, Searches: planSearches(b.cfg, c.budget), Items: 4}
		res := researchWithin(t, context.Background(), b, task, 5*time.Second)
		t.Logf("budget %d, results %d, retry every %d: %d searches, spent %d in %d requests", c.budget, c.result, c.retry, res.Searches, res.Tokens, len(reqs()))
		if c.budget < newCalibration().firstSearch(task) {
			// Too small for one search: the pass does not start.
			if !errors.Is(res.Err, errNoSearchRoom) || res.Tokens != 0 || len(reqs()) != 0 {
				t.Errorf("budget %d: err %v, %d tokens in %d requests; want errNoSearchRoom and nothing sent", c.budget, res.Err, res.Tokens, len(reqs()))
			}
			continue
		}
		if res.Tokens > c.budget {
			t.Errorf("budget %d, results %d: pass spent %d (+%d)", c.budget, c.result, res.Tokens, res.Tokens-c.budget)
		}
		if res.Markdown == "" || res.Err != nil {
			t.Errorf("budget %d: no digest (%v)", c.budget, res.Err)
		}
	}
}

// A request that fails after a digest was written keeps that digest.
func TestAPILaterFailureKeepsLatestDigest(t *testing.T) {
	sim := &realistic{results: []int64{15_000}}
	b, _ := scriptedAPI(t, apiBackend{}, func(n int, r rawRequest) reply {
		if n == 2 {
			return reply{status: 500}
		}
		return sim.handle(n, r)
	})
	res := researchWithin(t, context.Background(), b, Task{Topic: b.cfg.Topics[0], Budget: 600_000, Searches: 6, Items: 5}, 5*time.Second)
	if res.Err != nil || !res.Truncated || !strings.HasPrefix(res.Markdown, "### Item 2\n") || res.Searches != 2 {
		t.Errorf("err %v, truncated %v, %d searches, digest %q; want the second digest", res.Err, res.Truncated, res.Searches, res.Markdown)
	}
}

// When asked for more, a model that finds nothing replies unchanged without
// searching; the loop stops there and keeps the digest.
func TestAPIStopsWhenNothingIsAdded(t *testing.T) {
	sim := &realistic{results: []int64{15_000}, quietAfter: 2}
	b, reqs := scriptedAPI(t, apiBackend{}, sim.handle)
	res := researchWithin(t, context.Background(), b, Task{Topic: b.cfg.Topics[0], Budget: 3_000_000, Searches: 10, Items: 5}, 5*time.Second)
	if n := len(reqs()); n != 3 || res.Searches != 2 || !strings.HasPrefix(res.Markdown, "### Item 2\n") || res.Truncated {
		t.Errorf("%d requests, %d searches, truncated %v, digest %q; want a stop after the unchanged reply", n, res.Searches, res.Truncated, res.Markdown)
	}
}

// A fallback-served final iteration still measures the context.
func TestAPIMeasuresFallbackIterations(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		sim := &realistic{results: []int64{20_000}, fallback: fallback}
		b, reqs := scriptedAPI(t, apiBackend{}, sim.handle)
		res := researchWithin(t, context.Background(), b, Task{Topic: b.cfg.Topics[0], Budget: 400_000, Searches: 10, Items: 5}, 5*time.Second)
		if res.Searches < 2 || res.Tokens > 400_000 {
			t.Errorf("fallback %v: %d searches in %d requests, %d tokens", fallback, res.Searches, len(reqs()), res.Tokens)
		}
	}
}

// simBudgets are the budgets the realistic simulation sweeps.
var simBudgets = []int64{30_000, 60_000, 365_000, 730_000, 3_000_000}

// One pass at each budget never spends more than it, and searches more as
// the budget grows.
func TestAPIRealisticPassSweep(t *testing.T) {
	for _, results := range [][]int64{{15_000}, {30_000}, {10_000, 30_000, 20_000}} {
		for _, retry := range []int{0, 2, 1} {
			prev := int64(0)
			for _, budget := range simBudgets {
				sim := &realistic{results: results, retryEvery: retry}
				b, _ := scriptedAPI(t, apiBackend{}, sim.handle)
				task := Task{Topic: b.cfg.Topics[0], Budget: budget, Searches: planSearches(b.cfg, budget), Items: 6}
				res := researchWithin(t, context.Background(), b, task, 10*time.Second)
				label := fmt.Sprintf("results %v retry every %d budget %s", results, retry, HumanTokens(budget))
				t.Logf("%s: %d searches (planned %d), spent %d (%.0f%%)", label, res.Searches, task.Searches, res.Tokens, 100*float64(res.Tokens)/float64(budget))
				if res.Tokens > budget {
					t.Errorf("%s: spent %d, over by %d", label, res.Tokens, res.Tokens-budget)
				}
				if res.Searches < prev {
					t.Errorf("%s: %d searches, fewer than %d at a smaller budget", label, res.Searches, prev)
				}
				prev = res.Searches
			}
		}
	}
}

// Whole runs of five topics through the generator against the realistic
// API at the baseline, twice it and 3M. Every search request, or every
// other one, makes 0-4 refused attempts (2-6 sampling iterations), with and
// without 4k of draft text before them. A run never goes over its budget
// while requests take at most searchIterations; with more, any overshoot is
// recorded. At least 80% is spent at the baseline and 85% at twice it
// (with more iterations, long drafts can leave no room in max_tokens for
// the digest, which ends passes early).
func TestRunRealisticAPI(t *testing.T) {
	base := Baseline(testConfig("anthropic", 0, "A", "B", "C", "D", "E"))
	for _, budget := range []int64{base, 2 * base, 3_000_000} {
		for _, results := range [][]int64{{15_000}, {30_000}, {10_000, 30_000, 20_000}} {
			for retries := range 5 {
				for _, every := range []int{1, 2} {
					for _, draft := range []int64{0, 4_000} {
						if retries == 0 && (draft > 0 || every > 1) {
							continue
						}
						sim := &realistic{results: results, retryEvery: every, retries: retries, draft: draft}
						if retries == 0 {
							sim.retryEvery = 0
						}
						b, reqs := scriptedAPI(t, apiBackend{}, sim.handle)
						cfg := testConfig("anthropic", budget, "A", "B", "C", "D", "E")
						b.cfg = cfg
						j, ups := runWithin(t, context.Background(), cfg, b, 60*time.Second)
						var searches int64
						for _, s := range j.Sections {
							searches += s.Searches
						}
						iters := 2 + retries
						label := fmt.Sprintf("budget %s results %v iterations %d (every %d) draft %s",
							HumanTokens(budget), results, iters, every, HumanTokens(draft))
						t.Logf("%s: %.1f%% used, overshoot %d, %d searches, %d requests", label, j.Utilization(), j.Overshoot, searches, len(reqs()))
						if j.Overshoot != max(0, j.TokensUsed()-budget) {
							t.Errorf("%s: overshoot recorded as %d, spent %d", label, j.Overshoot, j.TokensUsed())
						}
						if iters > searchIterations {
							continue
						}
						checkRun(t, label, cfg, j, nil, ups)
						u := j.Utilization()
						switch {
						case budget == base && u < 80:
							t.Errorf("%s (baseline): only %.1f%% used", label, u)
						case budget == 2*base && u < 85:
							t.Errorf("%s (2× baseline): only %.1f%% used", label, u)
						}
					}
				}
			}
		}
	}
}

// Budgets in small steps around where searches start to fit: the worst
// case (30k results, every search retried) must never overshoot.
func TestAPIPassNeverOvershootsNearReservationEdges(t *testing.T) {
	for _, results := range [][]int64{{30_000}, {10_000, 30_000}} {
		for budget := int64(40_000); budget <= 420_000; budget += 2_500 {
			sim := &realistic{results: results, retryEvery: 1}
			b, _ := scriptedAPI(t, apiBackend{}, sim.handle)
			task := Task{Topic: b.cfg.Topics[0], Budget: budget, Searches: planSearches(b.cfg, budget), Items: maxTopicItems}
			if res := researchWithin(t, context.Background(), b, task, 5*time.Second); res.Tokens > budget {
				t.Errorf("results %v budget %d: spent %d (+%d) in %d searches", results, budget, res.Tokens, res.Tokens-budget, res.Searches)
			}
		}
	}
}

// Assess plans each topic's first pass by the API loop's own rules: at
// typical (20k) results, the loop makes the searches Assess shows.
func TestAssessFirstPassMatchesAPILoop(t *testing.T) {
	for _, budget := range []int64{365_000, 730_000, 1_500_000, 3_000_000} {
		cfg := testConfig("anthropic", budget, "A", "B", "C", "D", "E")
		a := Assess(cfg)
		g := NewGenerator(cfg, &fakeBackend{})
		for i, tp := range a.Topics {
			if tp.Budget < passCost("anthropic") {
				continue
			}
			sim := &realistic{results: []int64{typicalResult}}
			b, _ := scriptedAPI(t, apiBackend{}, sim.handle)
			task := g.task(i, tp.Budget, "")
			res := researchWithin(t, context.Background(), b, task, 5*time.Second)
			if res.Searches != tp.Searches || task.Items != tp.Items {
				t.Errorf("budget %s, %s with %s: Assess shows %d searches and %d items, the loop made %d (task items %d)",
					HumanTokens(budget), tp.Name, HumanTokens(tp.Budget), tp.Searches, tp.Items, res.Searches, task.Items)
			}
		}
		t.Logf("budget %s: %d first-pass searches, %d in all, spend %.0f%%", HumanTokens(budget), a.Searches, a.RunSearches, 100*float64(a.Spend)/float64(budget))
	}
}
