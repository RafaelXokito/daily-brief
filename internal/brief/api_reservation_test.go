package brief

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// reservationServer models search requests (max_uses 1) in which the model,
// after the results, tries retries more searches, each refused with
// max_uses_exceeded at the cost of another sampling iteration over the
// whole context. Before the first refused attempt (or every one, with
// draftEach) it writes draft tokens of text. Output is capped cumulatively
// by max_tokens unless draftEach is set, which models max_tokens capping
// each iteration instead; later iterations read earlier output again, as
// the API does. The first request's context is start tokens, the client's
// own estimate, as if it were exact.
func reservationServer(start, draft int64, retries int, draftEach bool) func(int, rawRequest) reply {
	const o1, oRetry, errBlock, result = 400, 150, 100, 30_000
	window := start
	return func(n int, r rawRequest) reply {
		if !toolsOn(r) {
			out := min(1_000, r.MaxTokens)
			in := window
			window += out
			return reply{content: "[" + textBlock("### W\nx [s](https://w.example)") + "]", stop: "end_turn",
				input: in, output: out, iterations: "[" + iteration(in, out) + "]"}
		}
		ins := []int64{window, window + o1 + result}
		outs := []int64{o1, 0}
		for k := range retries {
			last := len(ins) - 1
			outs[last] = oRetry
			if k == 0 || draftEach {
				outs[last] += draft
			}
			ins = append(ins, ins[last]+outs[last]+errBlock)
			outs = append(outs, 0)
		}
		var spent int64
		for _, o := range outs {
			spent += o
		}
		outs[len(outs)-1] = max(0, min(1_500, r.MaxTokens-spent))
		var in, out int64
		var iters []string
		for k := range ins {
			in += ins[k]
			out += outs[k]
			iters = append(iters, iteration(ins[k], outs[k]))
		}
		window = ins[len(ins)-1] + outs[len(outs)-1]
		return reply{content: "[" + searchBlocks(fmt.Sprintf("s%d", n)) + "," + textBlock("### Item\nA thing [source](https://a.example).") + "]",
			stop: "end_turn", input: in, output: out, searches: 1, iterations: "[" + strings.Join(iters, ",") + "]"}
	}
}

// edgeTask is a one-search task whose budget is exactly its first search
// request's reservation, and the first context the client estimates.
func edgeTask(t *testing.T) (Task, int64) {
	probe, _ := scriptedAPI(t, apiBackend{}, func(int, rawRequest) reply { return reply{} })
	task := Task{Topic: probe.cfg.Topics[0], Searches: 1, Items: 10}
	var w int64
	// The budget appears in the prompt; iterate to a fixed point.
	for range 4 {
		prompt := taskPrompt(task) + "\n\nRun one web search per turn, then write the digest."
		w = estimateTokens(researchSystemPrompt+prompt) + searchToolTokens
		task.Budget = searchReserve(searchIterations, w, initialResult, searchMaxTokens(task.Items))
	}
	return task, w
}

// Up to searchIterations sampling iterations per search request (three
// refused attempts), with draft text before them, under either reading of
// max_tokens, never take a pass over its budget: at exactly one
// reservation, and in steps across the edges where a second and third
// search start to fit.
func TestAPISearchRequestStaysWithinReservation(t *testing.T) {
	cases := []struct {
		name      string
		draft     int64
		retries   int
		draftEach bool
	}{
		{"no retry", 0, 0, false},
		{"one retry", 0, 1, false},
		{"one retry after 1.5k of draft text", 1_500, 1, false},
		{"one retry after 4k of draft text", 4_000, 1, false},
		{"two retries", 0, 2, false},
		{"three retries", 0, 3, false},
		{"three retries after 4k of draft text", 4_000, 3, false},
		{"three retries, 4k of draft text before each (max_tokens per iteration)", 4_000, 3, true},
	}
	edge, w := edgeTask(t)
	for _, tc := range cases {
		if iters := 2 + tc.retries; iters > searchIterations {
			t.Fatalf("%s: %d iterations, more than the %d reserved", tc.name, iters, searchIterations)
		}
		budgets := []int64{edge.Budget}
		for b := edge.Budget - 10_000; b <= edge.Budget+600_000; b += 5_000 {
			budgets = append(budgets, b)
		}
		var headroom int64
		for _, budget := range budgets {
			task := edge
			task.Budget, task.Searches = budget, 6
			b, reqs := scriptedAPI(t, apiBackend{}, reservationServer(w, tc.draft, tc.retries, tc.draftEach))
			res := researchWithin(t, context.Background(), b, task, 5*time.Second)
			if budget == edge.Budget {
				headroom = budget - res.Tokens
			}
			if res.Tokens > budget {
				t.Errorf("%s: budget %d, spent %d in %d requests (%d searches), over by %d",
					tc.name, budget, res.Tokens, len(reqs()), res.Searches, res.Tokens-budget)
			}
			if budget == edge.Budget && res.Searches != 1 {
				t.Errorf("%s: %d searches at exactly one reservation, want 1", tc.name, res.Searches)
			}
		}
		t.Logf("%s: %d tokens to spare at exactly one reservation (%d)", tc.name, headroom, edge.Budget)
	}
}

// A search request that takes more iterations than reserved (K+1 refused
// attempts) can overshoot. The run detects it, stops starting requests and
// passes, records the overshoot in the journal and shows it.
func TestRunOvershootIsDetectedRecordedAndStops(t *testing.T) {
	_, w := edgeTask(t)
	// One topic whose pass gets about one pass cost; the overview's reserve
	// (5%) is too small to absorb the overshoot.
	budget := (passCost("anthropic") + 2_000) * 20 / 19
	cfg := testConfig("anthropic", budget, "A")
	cfg.Concurrency = 1
	b, reqs := scriptedAPI(t, apiBackend{}, reservationServer(w, 0, searchIterations+1, false))
	b.cfg = cfg
	j, _ := runWithin(t, context.Background(), cfg, b, 10*time.Second)
	got := reqs()
	t.Logf("budget %d: spent %d in %d requests, overshoot %d", budget, j.TokensUsed(), len(got), j.Overshoot)
	if j.TokensUsed() <= budget {
		t.Fatalf("spent %d of %d: the scenario no longer overshoots", j.TokensUsed(), budget)
	}
	if j.Overshoot != j.TokensUsed()-budget {
		t.Errorf("overshoot recorded as %d, want %d", j.Overshoot, j.TokensUsed()-budget)
	}
	if len(got) != 1 {
		t.Errorf("%d requests, want none after the one that went over", len(got))
	}
	if j.Overview.Status != StatusFailed {
		t.Errorf("overview %s after the run went over its budget", j.Overview.Status)
	}
	if md := j.Markdown(); !strings.Contains(md, "went over its token budget by "+HumanTokens(j.Overshoot)) {
		t.Errorf("journal does not show the overshoot:\n%s", md)
	}
}

// A request that takes more iterations than reserved raises the run's
// calibration, so later requests reserve for that many and stay within
// their budgets.
func TestAPICalibrationLearnsFromOvershoot(t *testing.T) {
	retries := searchIterations + 1
	_, w := edgeTask(t)
	cal := newCalibration()
	b, _ := scriptedAPI(t, apiBackend{}, reservationServer(w, 0, retries, false))
	task := Task{Topic: b.cfg.Topics[0], Budget: 1_000_000, Searches: 1, Items: 10, cal: cal}
	researchWithin(t, context.Background(), b, task, 5*time.Second)
	if iters, _ := cal.get(); iters < int64(2+retries) {
		t.Fatalf("calibration after a %d-iteration request: %d iterations", 2+retries, iters)
	}
	for budget := int64(150_000); budget <= 1_500_000; budget += 10_000 {
		b, _ := scriptedAPI(t, apiBackend{}, reservationServer(w, 0, retries, false))
		task := Task{Topic: b.cfg.Topics[0], Budget: budget, Searches: 6, Items: 10, cal: cal}
		if res := researchWithin(t, context.Background(), b, task, 5*time.Second); res.Tokens > budget {
			t.Errorf("budget %d: calibrated pass spent %d (+%d)", budget, res.Tokens, res.Tokens-budget)
		}
	}
}

// Running passes see each other's spending: once the run as a whole is
// over its budget, no pass has room for another request.
func TestPoolRoomStopsAllPassesOnceOver(t *testing.T) {
	p := &pool{total: 100_000, reserve: 5_000}
	a, b := p.hold(60_000), p.hold(40_000)
	if a == nil || b == nil {
		t.Fatal("holds within the pool failed")
	}
	a.charge(70_000) // 10k over its hold: eats into the pool
	if got := b.room(); got != 30_000 {
		t.Errorf("room %d beside an overshooting pass, want 40k less its 10k", got)
	}
	if p.hold(1) != nil {
		t.Error("a hold succeeded with the pool overdrawn")
	}
	b.charge(36_000) // 106k charged: the run is over by 1k
	if got := b.room(); got != 0 || !p.over() {
		t.Errorf("room %d, over %v once the run went over its budget", got, p.over())
	}
	a.settle(70_000)
	b.settle(36_000)
	if p.spent != 106_000 || p.live != 0 || p.excess != 0 || p.outstanding != 0 {
		t.Errorf("after settling: %+v", p)
	}
}
