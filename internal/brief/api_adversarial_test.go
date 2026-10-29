package brief

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

// rawRequest is one recorded request: decoded fields plus the raw body and
// beta header.
type rawRequest struct {
	request
	Body  map[string]any
	Betas string
}

// reply is what the scripted server answers one request with.
type reply struct {
	status        int    // HTTP status; 0 means 200
	content       string // JSON array of content blocks
	stop          string
	input, output int64
	searches      int64
	iterations    string // JSON array of per-iteration usage; empty for none
}

func textBlock(s string) string {
	b, _ := json.Marshal(map[string]any{"type": "text", "text": s, "citations": nil})
	return string(b)
}

func searchBlocks(id string) string {
	return fmt.Sprintf(`{"type":"server_tool_use","id":%q,"name":"web_search","input":{"query":"q"}},`+
		`{"type":"web_search_tool_result","tool_use_id":%q,"content":[]}`, id, id)
}

// scriptedAPI serves replies computed from each request and records them.
func scriptedAPI(t *testing.T, flags apiBackend, handle func(n int, req rawRequest) reply) (*apiBackend, func() []rawRequest) {
	t.Helper()
	var mu sync.Mutex
	var reqs []rawRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var rr rawRequest
		if err := json.Unmarshal(body, &rr.request); err != nil {
			t.Error(err)
		}
		_ = json.Unmarshal(body, &rr.Body)
		rr.Betas = r.Header.Get("anthropic-beta")
		mu.Lock()
		reqs = append(reqs, rr)
		n := len(reqs) - 1
		mu.Unlock()
		rep := handle(n, rr)
		if rep.status != 0 && rep.status != 200 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(rep.status)
			fmt.Fprint(w, `{"type":"error","error":{"type":"api_error","message":"scripted failure"}}`)
			return
		}
		content := rep.content
		if content == "" {
			content = "[]"
		}
		iterations := rep.iterations
		if iterations == "" {
			iterations = "[]"
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"msg_%d","type":"message","role":"assistant","model":"m","content":%s,
			"stop_reason":%q,"usage":{"input_tokens":%d,"output_tokens":%d,"server_tool_use":{"web_search_requests":%d},"iterations":%s}}`,
			n, content, rep.stop, rep.input, rep.output, rep.searches, iterations)
	}))
	t.Cleanup(srv.Close)
	client := anthropic.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("test"), option.WithMaxRetries(0))
	b := flags
	b.cfg = testConfig("anthropic", 0, "A")
	b.messages = client.Beta.Messages
	b.name = "scripted"
	return &b, func() []rawRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]rawRequest(nil), reqs...)
	}
}

// researchWithin runs Research, failing the test if it does not return.
func researchWithin(t *testing.T, ctx context.Context, b *apiBackend, task Task, d time.Duration) Result {
	t.Helper()
	ch := make(chan Result, 1)
	go func() { ch <- b.Research(ctx, task) }()
	select {
	case res := <-ch:
		return res
	case <-time.After(d):
		t.Fatalf("Research did not return within %s", d)
		return Result{}
	}
}

func toolsOn(r rawRequest) bool {
	return len(r.Tools) > 0 && (r.ToolChoice == nil || r.ToolChoice.Type != "none")
}

func TestAPIPauseTurnChain(t *testing.T) {
	b, reqs := scriptedAPI(t, apiBackend{}, func(n int, _ rawRequest) reply {
		if n < 3 {
			return reply{content: "[" + textBlock("Searching.") + "," + searchBlocks(fmt.Sprintf("s%d", n)) + "]",
				stop: "pause_turn", input: 20_000, output: 500, searches: 1}
		}
		return reply{content: "[" + searchBlocks("s3") + "," + textBlock("### Final\nDone.") + "]",
			stop: "end_turn", input: 22_000, output: 1_500, searches: 1}
	})
	task := Task{Topic: b.cfg.Topics[0], Budget: 400_000, Searches: 4, Items: 5}
	res := researchWithin(t, context.Background(), b, task, 5*time.Second)
	got := reqs()
	if res.Err != nil || len(got) != 4 {
		t.Fatalf("err %v after %d requests, want 4", res.Err, len(got))
	}
	for i, r := range got {
		if len(r.Messages) != i+1 {
			t.Errorf("request %d resent %d messages, want %d", i, len(r.Messages), i+1)
		}
		if !toolsOn(r) || r.Tools[0].MaxUses > int64(4-i) {
			t.Errorf("request %d tools %+v, want max_uses at most %d", i, r.Tools, 4-i)
		}
	}
	if res.Markdown != "### Final\nDone." || res.Searches != 4 || res.Tokens != 3*20_500+23_500 || res.Truncated {
		t.Errorf("result %q, %d searches, %d tokens, truncated %v", res.Markdown, res.Searches, res.Tokens, res.Truncated)
	}
}

// A server that keeps pausing without reporting usage must not keep the
// loop going forever: every turn would be another paid request.
func TestAPIPauseTurnChainIsBounded(t *testing.T) {
	const cutoff = 300
	b, reqs := scriptedAPI(t, apiBackend{}, func(n int, _ rawRequest) reply {
		if n < cutoff {
			return reply{content: "[" + searchBlocks(fmt.Sprintf("s%d", n)) + "]", stop: "pause_turn"}
		}
		return reply{content: "[" + textBlock("### x") + "]", stop: "end_turn"}
	})
	res := researchWithin(t, context.Background(), b, Task{Topic: b.cfg.Topics[0], Budget: 1_000_000, Searches: 4, Items: 5}, 30*time.Second)
	if n := len(reqs()); n != maxRequests || !res.Truncated {
		t.Errorf("Research made %d requests on zero-usage pause_turns (server gave up pausing at %d); err %v", n, cutoff, res.Err)
	}
}

func iteration(in, out int64) string {
	return fmt.Sprintf(`{"type":"message","input_tokens":%d,"output_tokens":%d}`, in, out)
}

// pauseServer simulates a server that pauses (pause_turn) after every
// search, with per-iteration usage: a turn with a search bills the context
// twice (before and after the results) plus the results; a writing turn
// bills the context and writes writeLen tokens, or max_tokens when writeLen
// is 0. The realistic model, ending the turn after one search, is in
// api_realistic_test.go.
func pauseServer(writeLen int64) func(int, rawRequest) reply {
	ctxTokens := int64(1_500)
	return func(n int, r rawRequest) reply {
		if toolsOn(r) {
			second := ctxTokens + 250 + 9_950
			iters := "[" + iteration(ctxTokens, 250) + "," + iteration(second, 250) + "]"
			in := ctxTokens + second
			ctxTokens = second + 250
			return reply{content: "[" + searchBlocks(fmt.Sprintf("s%d", n)) + "]", stop: "pause_turn", input: in, output: 500, searches: 1, iterations: iters}
		}
		out := writeLen
		stop := "end_turn"
		if out == 0 || out >= r.MaxTokens {
			out, stop = r.MaxTokens, "max_tokens"
		}
		in := ctxTokens
		ctxTokens += out
		return reply{content: "[" + textBlock("### Item\nx") + "]", stop: stop, input: in, output: out, iterations: "[" + iteration(in, out) + "]"}
	}
}

func TestAPIPausingLoopStaysWithinBudget(t *testing.T) {
	for _, budget := range []int64{25_000, 31_000, 47_000, 60_000, 100_000, 250_000, 1_000_000} {
		for _, writeLen := range []int64{0, 2_500} {
			b, reqs := scriptedAPI(t, apiBackend{}, pauseServer(writeLen))
			task := Task{Topic: b.cfg.Topics[0], Budget: budget, Searches: planSearches(b.cfg, budget), Items: 5}
			res := researchWithin(t, context.Background(), b, task, 10*time.Second)
			label := fmt.Sprintf("budget %d write %d", budget, writeLen)
			if budget < newCalibration().firstSearch(task) {
				// Too small for one search: nothing is sent or spent.
				if !errors.Is(res.Err, errNoSearchRoom) || res.Tokens != 0 || len(reqs()) != 0 {
					t.Errorf("%s: err %v, %d tokens in %d requests; want errNoSearchRoom and nothing sent", label, res.Err, res.Tokens, len(reqs()))
				}
				continue
			}
			if res.Err != nil {
				t.Fatalf("%s: %v", label, res.Err)
			}
			if res.Tokens > budget {
				t.Errorf("%s: spent %d tokens in %d requests", label, res.Tokens, len(reqs()))
			}
			// Replay the usage to check each request against what was left.
			var spent int64
			sim := pauseServer(writeLen)
			for i, r := range reqs() {
				if r.MaxTokens > budget-spent {
					t.Errorf("%s: request %d max_tokens %d, only %d left", label, i, r.MaxTokens, budget-spent)
				}
				if toolsOn(r) && r.Tools[0].MaxUses != 1 {
					t.Errorf("%s: request %d offered %d searches, want 1", label, i, r.Tools[0].MaxUses)
				}
				rep := sim(i, r)
				if spent += rep.input + rep.output; spent > budget {
					t.Errorf("%s: request %d brought the pass to %d", label, i, spent)
				}
			}
			t.Logf("%s: %d searches, %d of %d tokens", label, res.Searches, res.Tokens, budget)
			if res.Searches > task.Searches {
				t.Errorf("%s: %d searches, planned %d", label, res.Searches, task.Searches)
			}
		}
	}
}

// A response whose usage blows past the budget ends the pass at once.
func TestAPIStopsAfterOverBudgetTurn(t *testing.T) {
	b, reqs := scriptedAPI(t, apiBackend{}, func(int, rawRequest) reply {
		return reply{content: "[" + searchBlocks("s") + "]", stop: "pause_turn", input: 400_000, output: 500, searches: 1}
	})
	res := researchWithin(t, context.Background(), b, Task{Topic: b.cfg.Topics[0], Budget: 250_000, Searches: 3, Items: 5}, 5*time.Second)
	if n := len(reqs()); n != 1 || !res.Truncated {
		t.Fatalf("%d requests, truncated %v; want one request then stop", n, res.Truncated)
	}
	if first := reqs()[0]; first.MaxTokens > 250_000 {
		t.Errorf("max_tokens %d over the budget", first.MaxTokens)
	}
}

func TestAPIRefusalFirstTurn(t *testing.T) {
	b, reqs := scriptedAPI(t, apiBackend{}, func(int, rawRequest) reply {
		return reply{content: "[" + textBlock("I can't help with that.") + "]", stop: "refusal", input: 3_000, output: 50}
	})
	res := researchWithin(t, context.Background(), b, Task{Topic: b.cfg.Topics[0], Budget: 250_000, Searches: 4, Items: 5}, 5*time.Second)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "declined") || res.Markdown != "" || res.Tokens != 3_050 || len(reqs()) != 1 {
		t.Errorf("refusal: err %v, markdown %q, tokens %d, %d requests", res.Err, res.Markdown, res.Tokens, len(reqs()))
	}
}

// An API error on a later request keeps the partial digest written so far
// rather than failing the topic; with nothing written, the error stands.
func TestAPIErrorAfterPauseKeepsPartialDigest(t *testing.T) {
	for _, first := range []string{
		"[" + searchBlocks("s") + "," + textBlock("### Found\nSo far.") + "]",
		"[" + searchBlocks("s") + "]",
	} {
		for _, status := range []int{500, 529} {
			b, reqs := scriptedAPI(t, apiBackend{}, func(n int, _ rawRequest) reply {
				if n == 0 {
					return reply{content: first, stop: "pause_turn", input: 10_000, output: 1_000, searches: 1}
				}
				return reply{status: status}
			})
			res := researchWithin(t, context.Background(), b, Task{Topic: b.cfg.Topics[0], Budget: 250_000, Searches: 4, Items: 5}, 5*time.Second)
			if len(reqs()) != 2 || res.Tokens != 11_000 || res.Searches != 1 {
				t.Errorf("%d: %d requests, %d tokens, %d searches", status, len(reqs()), res.Tokens, res.Searches)
			}
			partial := strings.Contains(first, "Found")
			switch {
			case partial && (res.Markdown != "### Found\nSo far." || !res.Truncated || res.Err != nil):
				t.Errorf("%d: markdown %q truncated %v err %v; want the partial digest", status, res.Markdown, res.Truncated, res.Err)
			case !partial && (res.Err == nil || res.Markdown != ""):
				t.Errorf("%d: no digest yet, err %v markdown %q", status, res.Err, res.Markdown)
			}
		}
	}
}

func TestAPIVariantRequestShape(t *testing.T) {
	endTurn := func(int, rawRequest) reply {
		return reply{content: "[" + textBlock("### A") + "]", stop: "end_turn", input: 1_000, output: 100}
	}
	full := apiBackend{dynamicSearch: true, fallbacks: true, taskBudget: true}
	cases := []struct {
		name   string
		flags  apiBackend
		budget int64
		tool   string
		extras bool // fallbacks and task budget expected
		task   bool
	}{
		{"anthropic", full, 250_000, "web_search_20260209", true, true},
		{"anthropic small task", full, 19_999, "", true, false}, // a narrow pass
		{"vertex", apiBackend{}, 250_000, "web_search_20250305", false, false},
	}
	for _, c := range cases {
		b, reqs := scriptedAPI(t, c.flags, endTurn)
		task := Task{Topic: b.cfg.Topics[0], Budget: c.budget, Searches: 4, Items: 5}
		if c.tool == "" {
			task.Searches, task.Prior = 0, "### A\nx"
		}
		researchWithin(t, context.Background(), b, task, 5*time.Second)
		r := reqs()[0]
		tools, _ := r.Body["tools"].([]any)
		if c.tool != "" {
			if len(tools) != 1 || tools[0].(map[string]any)["type"] != c.tool {
				t.Errorf("%s: tools %v, want %s", c.name, tools, c.tool)
			}
		} else if len(tools) != 0 {
			t.Errorf("%s: tools offered on a %d budget: %v", c.name, c.budget, tools)
		}
		_, hasFallbacks := r.Body["fallbacks"]
		oc, _ := r.Body["output_config"].(map[string]any)
		_, hasTask := oc["task_budget"]
		if hasFallbacks != c.extras || strings.Contains(r.Betas, "server-side-fallback") != c.extras {
			t.Errorf("%s: fallbacks %v, betas %q", c.name, hasFallbacks, r.Betas)
		}
		if hasTask != c.task || strings.Contains(r.Betas, "task-budgets") != c.task {
			t.Errorf("%s: task_budget %v, betas %q", c.name, hasTask, r.Betas)
		}
		if c.task {
			if total, _ := oc["task_budget"].(map[string]any)["total"].(float64); int64(total) != c.budget {
				t.Errorf("%s: task_budget total %v, want %d", c.name, total, c.budget)
			}
		}
		if c.name == "vertex" && strings.Contains(r.Betas, "task-budgets") {
			t.Errorf("vertex sent the task budget beta")
		}
	}
}

func TestAPICancelledRequestReturns(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	entered := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(entered) })
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	// Runs before srv.Close, so a handler the cancel did not reach is freed.
	defer close(release)
	client := anthropic.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("test"), option.WithMaxRetries(0))
	cfg := testConfig("anthropic", 0, "A")
	b := &apiBackend{cfg: cfg, messages: client.Beta.Messages, name: "blocked"}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-entered; cancel() }()
	res := researchWithin(t, ctx, b, Task{Topic: cfg.Topics[0], Budget: 250_000, Searches: 4, Items: 5}, 5*time.Second)
	if res.Err == nil || res.Err.Error() != "cancelled" {
		t.Errorf("err = %v, want cancelled", res.Err)
	}
}

func TestAPIDescribesErrors(t *testing.T) {
	for status, want := range map[int]string{401: "authentication failed", 403: "authentication failed", 429: "rate limited", 529: "overloaded", 400: "API error 400"} {
		b, _ := scriptedAPI(t, apiBackend{}, func(int, rawRequest) reply { return reply{status: status} })
		res := researchWithin(t, context.Background(), b, Task{Topic: b.cfg.Topics[0], Budget: 250_000, Searches: 4, Items: 5}, 5*time.Second)
		if res.Err == nil || !strings.Contains(res.Err.Error(), want) {
			t.Errorf("%d: err %v, want %q", status, res.Err, want)
		}
	}
}

// The generator and the API backend together, through the scripted server:
// the whole run must stay within the budget.
func TestRunWithPausingAPIStaysWithinBudget(t *testing.T) {
	for _, budget := range []int64{100_000, 365_000, 1_500_000} {
		var mu sync.Mutex
		sims := map[string]func(int, rawRequest) reply{}
		counts := map[string]int{}
		b, _ := scriptedAPI(t, apiBackend{}, func(_ int, r rawRequest) reply {
			// Key each conversation by its first user message.
			key := string(r.Messages[0])
			mu.Lock()
			defer mu.Unlock()
			if r.MaxTokens == 4_000 && len(r.Tools) == 0 && len(r.Messages) == 1 && !strings.Contains(key, "Topic:") {
				return reply{content: "[" + textBlock("## Today's highlights\n\n- x") + "]", stop: "end_turn", input: 3_000, output: 200}
			}
			if sims[key] == nil {
				sims[key] = pauseServer(2_500)
			}
			n := counts[key]
			counts[key]++
			return sims[key](n, r)
		})
		cfg := testConfig("anthropic", budget, "A", "B", "C", "D", "E")
		b.cfg = cfg
		j, _ := runWithin(t, context.Background(), cfg, b, 30*time.Second)
		t.Logf("budget %d: %.1f%% used", budget, j.Utilization())
		if j.TokensUsed() > budget {
			t.Errorf("budget %d: run spent %d", budget, j.TokensUsed())
		}
	}
}
