package brief

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RafaelXokito/daily-brief/internal/config"
)

// scriptBackend is a fake backend whose research behaviour is scripted per
// call, so tests can overspend, fail, block or return nothing.
type scriptBackend struct {
	mu        sync.Mutex
	research  func(ctx context.Context, t Task, n int) Result
	summarize func(ctx context.Context) Result
	tasks     []Task
	summaries int
	inflight  int
	peak      int
}

func (f *scriptBackend) Research(ctx context.Context, t Task) Result {
	f.mu.Lock()
	n := len(f.tasks)
	f.tasks = append(f.tasks, t)
	f.inflight++
	f.peak = max(f.peak, f.inflight)
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.inflight--
		f.mu.Unlock()
	}()
	return f.research(ctx, t, n)
}

func (f *scriptBackend) Summarize(ctx context.Context, _, _ string) Result {
	f.mu.Lock()
	f.summaries++
	f.mu.Unlock()
	if f.summarize != nil {
		return f.summarize(ctx)
	}
	return Result{Markdown: "## Today's highlights\n\n- x", Tokens: 2_000}
}

func (f *scriptBackend) Name() string { return "script" }

func (f *scriptBackend) snapshot() []Task {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Task(nil), f.tasks...)
}

// spending returns a research func that spends frac of each allocation and
// writes a digest with one item.
func spending(frac float64) func(context.Context, Task, int) Result {
	return func(_ context.Context, t Task, _ int) Result {
		return Result{Markdown: "### Item\nSomething happened.", Tokens: int64(frac * float64(t.Budget)), Searches: t.Searches}
	}
}

// runWithin runs a generation to the end, failing the test if it does not
// finish in time. It returns the journal and every update sent.
func runWithin(t *testing.T, ctx context.Context, cfg config.Config, b Backend, timeout time.Duration) (*Journal, []Update) {
	t.Helper()
	g := NewGenerator(cfg, b)
	j := g.NewJournal()
	updates := make(chan Update)
	done := make(chan []Update)
	go func() {
		var got []Update
		for u := range updates {
			got = append(got, u)
		}
		done <- got
	}()
	go g.Run(ctx, j, updates)
	select {
	case got := <-done:
		return j, got
	case <-time.After(timeout):
		t.Fatalf("run did not finish within %s", timeout)
		return nil, nil
	}
}

func topicNames(n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("T%02d", i)
	}
	return names
}

// checkRun asserts the invariants every run must keep.
func checkRun(t *testing.T, label string, cfg config.Config, j *Journal, tasks []Task, updates []Update) {
	t.Helper()
	g := NewGenerator(cfg, &fakeBackend{})
	if used := j.TokensUsed(); used > j.TokenBudget {
		t.Errorf("%s: spent %d of %d tokens (%.1f%%), over the budget", label, used, j.TokenBudget, j.Utilization())
	}
	least := passCost(cfg.Provider.Type, planWindow)
	for _, task := range tasks {
		// A narrow pass rewrites an existing digest without searching.
		narrow := task.Searches == 0 && task.Prior != ""
		if !narrow && task.Budget < least || narrow && task.Budget < narrowMin {
			t.Errorf("%s: pass for %s (narrow %v) started with %d tokens, under its least cost", label, task.Topic.Name, narrow, task.Budget)
		}
		if task.Items > maxTopicItems || task.Items < 1 {
			t.Errorf("%s: pass for %s asked for %d items", label, task.Topic.Name, task.Items)
		}
		if task.Searches < 1 && !narrow {
			t.Errorf("%s: pass for %s planned %d searches", label, task.Topic.Name, task.Searches)
		}
	}
	for i, s := range j.Sections {
		if s.Passes > maxPasses(cfg.Provider.Type) {
			t.Errorf("%s: %s ran %d top-up passes, over %d", label, s.Topic, s.Passes, maxPasses(cfg.Provider.Type))
		}
		if n := countItems(s.Markdown); n > g.caps[i] {
			t.Errorf("%s: %s has %d items, over its cap %d", label, s.Topic, n, g.caps[i])
		}
		if s.Status == StatusPending || s.Status == StatusRunning {
			t.Errorf("%s: %s left in status %s after the run", label, s.Topic, s.Status)
		}
	}
	for _, u := range updates {
		if u.Section.Allocated < 0 {
			t.Errorf("%s: update for %q shows a negative budget %d", label, u.Section.Topic, u.Section.Allocated)
			break
		}
	}
}

// TestRunBudgetSweep runs topic counts, budgets, concurrency and spending
// patterns through the generator and checks the cap, the 85% target and
// that no pass starts under one pass cost.
func TestRunBudgetSweep(t *testing.T) {
	for _, provider := range []string{"anthropic", "claude-code"} {
		for _, n := range []int{1, 2, 3, 5, 30} {
			for _, mult := range []float64{1, 3} {
				for _, conc := range []int{1, 10} {
					for _, frac := range []float64{0.6, 1.0} {
						cfg := testConfig(provider, 0, topicNames(n)...)
						cfg.TokenBudget = int64(mult * float64(Baseline(cfg)))
						cfg.Concurrency = conc
						f := &scriptBackend{research: spending(frac)}
						j, ups := runWithin(t, context.Background(), cfg, f, 30*time.Second)
						label := fmt.Sprintf("%s %d topics ×%.0f conc %d spend %.0f%%", provider, n, mult, conc, 100*frac)
						checkRun(t, label, cfg, j, f.snapshot(), ups)
						if u := j.Utilization(); u < 85 {
							t.Errorf("%s: utilization %.1f%%, want at least 85%%", label, u)
						}
						if f.peak > conc {
							t.Errorf("%s: %d passes ran at once, over concurrency %d", label, f.peak, conc)
						}
						if j.Overview.Status != StatusDone {
							t.Errorf("%s: overview %s: %s", label, j.Overview.Status, j.Overview.Error)
						}
						for _, s := range j.Sections {
							if s.Status != StatusDone {
								t.Errorf("%s: %s %s: %s", label, s.Topic, s.Status, s.Error)
							}
						}
					}
				}
			}
		}
	}
}

// Heavy under-spenders (a backend that uses 30-45% of each allocation)
// still land in the 85-100% target band at the baseline budget: top-up
// rounds go on while they make progress.
func TestHeavyUnderspendReachesTarget(t *testing.T) {
	for _, provider := range []string{"anthropic", "claude-code"} {
		for _, frac := range []float64{0.3, 0.45} {
			cfg := testConfig(provider, 0, "A", "B", "C", "D", "E")
			ResolveBudget(&cfg)
			f := &scriptBackend{research: spending(frac)}
			j, ups := runWithin(t, context.Background(), cfg, f, 30*time.Second)
			label := fmt.Sprintf("%s spend %.0f%%", provider, 100*frac)
			checkRun(t, label, cfg, j, f.snapshot(), ups)
			t.Logf("%s: %.1f%% in %d passes", label, j.Utilization(), len(f.snapshot()))
			if u := j.Utilization(); u < 85 {
				t.Errorf("%s: utilization %.1f%%, want 85-100%% when every pass under-spends", label, u)
			}
		}
	}
}

func TestRunExtremeWeights(t *testing.T) {
	for _, weights := range [][]float64{{0.001, 1000}, {1000, 0.001}, {0.001, 1, 1000}} {
		for _, conc := range []int{1, 10} {
			for _, provider := range []string{"anthropic", "claude-code"} {
				cfg := testConfig(provider, 0)
				for i, w := range weights {
					cfg.Topics = append(cfg.Topics, config.Topic{Name: fmt.Sprintf("W%d", i), Weight: w})
				}
				cfg.Concurrency = conc
				for _, budget := range []int64{400_000, 50_000_000} {
					cfg.TokenBudget = budget
					f := &scriptBackend{research: spending(0.6)}
					j, ups := runWithin(t, context.Background(), cfg, f, 30*time.Second)
					label := fmt.Sprintf("%s weights %v conc %d budget %s", provider, weights, conc, HumanTokens(budget))
					checkRun(t, label, cfg, j, f.snapshot(), ups)
					t.Logf("%s: %.1f%% used", label, j.Utilization())
				}
			}
		}
	}
}

// With concurrency, a light topic starting while a heavy one holds almost
// the whole pool is skipped at first; the leftover the heavy topic hands
// back must research it before funding the heavy topic's top-ups.
func TestLightTopicSkippedIsRetriedFromLeftover(t *testing.T) {
	for _, conc := range []int{1, 3} {
		cfg := testConfig("anthropic", 2_000_000)
		cfg.Topics = []config.Topic{{Name: "Heavy", Weight: 1000}, {Name: "Light", Weight: 0.001}}
		cfg.Concurrency = conc
		f := &scriptBackend{research: spending(0.5)}
		j, _ := runWithin(t, context.Background(), cfg, f, 30*time.Second)
		light := j.Sections[1]
		var topUps int
		for _, task := range f.snapshot() {
			if task.Prior != "" {
				topUps++
			}
		}
		t.Logf("conc %d: light topic %s %q; %d top-up passes; %.1f%% used", conc, light.Status, light.Error, topUps, j.Utilization())
		if light.Status != StatusDone {
			t.Errorf("conc %d: light topic %s (%s) while %d top-up passes spent the leftover", conc, light.Status, light.Error, topUps)
		}
	}
}

// Weights so far apart that their float sum drops the small one would make
// the last topic's share w/weights divide by zero. Validation rejects them,
// and allocations are clamped to what is free anyway.
func TestExtremeWeightRatioRejectedAndClamped(t *testing.T) {
	cfg := testConfig("anthropic", 1_000_000)
	cfg.Topics = []config.Topic{{Name: "Huge", Weight: 1e17}, {Name: "Tiny", Weight: 1}}
	cfg.Concurrency = 1
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "1000") {
		t.Errorf("weights 1e17/1 accepted: %v", err)
	}
	f := &scriptBackend{research: spending(0.5)}
	j, ups := runWithin(t, context.Background(), cfg, f, 30*time.Second)
	for _, task := range f.snapshot() {
		if task.Budget > cfg.TokenBudget || task.Budget < 0 {
			t.Errorf("pass for %s allocated %d tokens of a %d budget", task.Topic.Name, task.Budget, cfg.TokenBudget)
		}
	}
	checkRun(t, "weights 1e17/1", cfg, j, f.snapshot(), ups)
}

// When the leftover after the search passes is under one pass cost but
// over 15% of the budget, a final narrow pass (no searches) spends it.
//
// Limit: with claude-code, a budget that funds only one search pass beside
// its safety margin (one search plus 10% per pass) cannot reliably reach 85%:
// the margin is never allocated, and a real narrow pass costs only what a
// rewrite does. Assess warns about such budgets; the baseline is raised
// above them.
func TestSmallBudgetUtilization(t *testing.T) {
	weighted := testConfig("claude-code", 400_000)
	weighted.Topics = []config.Topic{{Name: "W0", Weight: 0.001}, {Name: "W1", Weight: 1}, {Name: "W2", Weight: 1000}}
	cases := map[string]config.Config{
		"anthropic 1 topic at baseline":    testConfig("anthropic", 0, "A"),
		"claude-code weights 0.001/1/1000": weighted,
	}
	for name, cfg := range cases {
		ResolveBudget(&cfg)
		cfg.Concurrency = 1
		f := &scriptBackend{research: spending(0.6)}
		j, _ := runWithin(t, context.Background(), cfg, f, 30*time.Second)
		left := j.TokenBudget - j.TokensUsed()
		if u := j.Utilization(); u < 85 {
			t.Errorf("%s: %.1f%% used, %d tokens left unspent (pass cost %d) after %d passes",
				name, u, left, passCost(cfg.Provider.Type, planWindow), len(f.snapshot()))
		}
		if strings.Contains(strings.Join(Assess(cfg).Messages, "\n"), "safety margin") {
			t.Errorf("%s: warned about the safety margin", name)
		}
	}

	// One topic's per-topic sum, 3 searches plus writing and the overview.
	cfg := testConfig("claude-code", 160_000, "A")
	f := &scriptBackend{research: spending(0.6)}
	j, _ := runWithin(t, context.Background(), cfg, f, 30*time.Second)
	t.Logf("claude-code 1 topic at 160k: %.1f%% used", j.Utilization())
	if j.TokensUsed() > j.TokenBudget {
		t.Errorf("claude-code 1 topic: over budget")
	}
	if !strings.Contains(strings.Join(Assess(cfg).Messages, "\n"), "safety margin") {
		t.Errorf("claude-code 1 topic at 160k: no warning that 85%% is out of reach: %v", Assess(cfg).Messages)
	}
}

func TestRunBudgetEdges(t *testing.T) {
	t.Run("minimum 10k", func(t *testing.T) {
		for _, provider := range []string{"anthropic", "claude-code"} {
			cfg := testConfig(provider, 10_000, "A", "B")
			f := &scriptBackend{research: spending(1)}
			j, ups := runWithin(t, context.Background(), cfg, f, 30*time.Second)
			checkRun(t, provider, cfg, j, f.snapshot(), ups)
			if len(f.snapshot()) != 0 || f.summaries != 0 {
				t.Errorf("%s: %d passes and %d overviews ran on a 10k budget", provider, len(f.snapshot()), f.summaries)
			}
			for _, s := range j.Sections {
				if s.Status != StatusFailed || !strings.HasPrefix(s.Error, "skipped") {
					t.Errorf("%s: %s %s %q, want skipped", provider, s.Topic, s.Status, s.Error)
				}
			}
			if j.Overview.Status != StatusFailed {
				t.Errorf("%s: overview %s", provider, j.Overview.Status)
			}
			if a := Assess(cfg); a.Level != LevelLow {
				t.Errorf("%s: 10k assessed %s", provider, a.Level)
			}
		}
	})

	t.Run("exactly one pass cost", func(t *testing.T) {
		for _, provider := range []string{"anthropic", "claude-code"} {
			// The overview reserve (and claude-code's margin) comes off the
			// top, so one pass cost is not enough on its own: nothing may
			// start, and Assess must say low.
			cfg := testConfig(provider, passCost(provider, planWindow), "A")
			f := &scriptBackend{research: spending(1)}
			j, ups := runWithin(t, context.Background(), cfg, f, 30*time.Second)
			checkRun(t, provider, cfg, j, f.snapshot(), ups)
			if a := Assess(cfg); a.Level != LevelLow || a.Topics[0].Searches != 0 {
				t.Errorf("%s: one pass cost assessed %s with %d searches", provider, a.Level, a.Topics[0].Searches)
			}
			// The smallest budget whose topic pool covers one pass runs exactly one.
			b := passCost(provider, planWindow)
			for b-overviewReserve(b) < passNeed(provider, planWindow) {
				b++
			}
			cfg.TokenBudget = b
			f = &scriptBackend{research: spending(1)}
			j, ups = runWithin(t, context.Background(), cfg, f, 30*time.Second)
			checkRun(t, provider, cfg, j, f.snapshot(), ups)
			// A narrow pass may follow; only one search pass may run.
			var search []Task
			for _, task := range f.snapshot() {
				if task.Searches > 0 {
					search = append(search, task)
				}
			}
			if len(search) != 1 || search[0].Searches != 1 || search[0].Prior != "" {
				t.Errorf("%s: budget %d ran %d search passes, want one first pass with one search", provider, b, len(search))
			}
		}
	})

	t.Run("huge 50M", func(t *testing.T) {
		for _, provider := range []string{"anthropic", "claude-code"} {
			cfg := testConfig(provider, 50_000_000, topicNames(5)...)
			f := &scriptBackend{research: spending(0.6)}
			j, ups := runWithin(t, context.Background(), cfg, f, 30*time.Second)
			checkRun(t, provider, cfg, j, f.snapshot(), ups)
			if u := j.Utilization(); u < 85 {
				t.Errorf("%s: 50M utilization %.1f%%", provider, u)
			}
			if j.Overview.Allocated != maxOverviewReserve {
				t.Errorf("%s: overview reserve %d, want the %d cap", provider, j.Overview.Allocated, maxOverviewReserve)
			}
			a := Assess(cfg)
			if a.Level != LevelVeryHigh || a.ReadMinutes > float64(cfg.MaxReadMinutes) {
				t.Errorf("%s: 50M assessed %s, %.1f min", provider, a.Level, a.ReadMinutes)
			}
		}
	})
}

// A claude-code style backend cannot be stopped mid-pass and spends more
// than its allocation. The margin held beside each pass (10% plus one
// search) keeps the run within the budget for an overspend of up to 15%.
func TestOverspendingBackendStaysWithinBudget(t *testing.T) {
	for _, conc := range []int{1, 3, 10} {
		cfg := testConfig("claude-code", 0, topicNames(5)...)
		ResolveBudget(&cfg)
		cfg.Concurrency = conc
		f := &scriptBackend{research: spending(1.15)}
		j, _ := runWithin(t, context.Background(), cfg, f, 30*time.Second)
		if j.TokensUsed() > j.TokenBudget {
			t.Errorf("conc %d: a backend overspending each pass by 15%% drove the run to %d of %d tokens (%.1f%%)",
				conc, j.TokensUsed(), j.TokenBudget, j.Utilization())
		}
	}
}

// Whatever a backend overspends, the generator must stop starting passes,
// skip the overview rather than add to the overshoot, and finish.
func TestOverspendStopsFurtherPasses(t *testing.T) {
	cfg := testConfig("claude-code", 0, topicNames(5)...)
	ResolveBudget(&cfg)
	cfg.Concurrency = 1
	f := &scriptBackend{research: func(_ context.Context, t Task, n int) Result {
		spend := t.Budget / 2
		if n == 0 {
			spend = 10 * t.Budget // the first pass blows through everything
		}
		return Result{Markdown: "### x\ny", Tokens: spend, Searches: 1}
	}}
	j, ups := runWithin(t, context.Background(), cfg, f, 30*time.Second)
	if n := len(f.snapshot()); n != 1 {
		t.Errorf("%d passes ran after the first one overspent the whole budget", n)
	}
	if f.summaries != 0 || j.Overview.Status != StatusFailed {
		t.Errorf("overview ran (%d calls, %s) with the budget already exceeded", f.summaries, j.Overview.Status)
	}
	for _, s := range j.Sections[1:] {
		if s.Status != StatusFailed || !strings.HasPrefix(s.Error, "skipped") {
			t.Errorf("%s: %s %q, want skipped", s.Topic, s.Status, s.Error)
		}
	}
	for _, u := range ups {
		if u.Section.Allocated < 0 {
			t.Errorf("update shows %q with a negative budget %d", u.Section.Topic, u.Section.Allocated)
			break
		}
	}
}

func TestRunWithFailingBackends(t *testing.T) {
	cases := []struct {
		name string
		res  Result
	}{
		{"error with tokens", Result{Err: errors.New("boom"), Tokens: 7_000}},
		{"error no tokens", Result{Err: errors.New("boom")}},
		{"empty markdown", Result{Markdown: "  \n", Tokens: 5_000}},
		{"truncated empty", Result{Truncated: true, Tokens: 30_000}},
	}
	for _, tc := range cases {
		for _, conc := range []int{1, 10} {
			cfg := testConfig("anthropic", 0, topicNames(5)...)
			ResolveBudget(&cfg)
			cfg.Concurrency = conc
			f := &scriptBackend{research: func(context.Context, Task, int) Result { return tc.res }}
			j, ups := runWithin(t, context.Background(), cfg, f, 30*time.Second)
			label := fmt.Sprintf("%s conc %d", tc.name, conc)
			checkRun(t, label, cfg, j, f.snapshot(), ups)
			if n := len(f.snapshot()); n != 5 {
				t.Errorf("%s: %d passes, want one per topic and no top-ups of failed topics", label, n)
			}
			for _, s := range j.Sections {
				if s.Status != StatusFailed || s.Error == "" {
					t.Errorf("%s: %s status %s %q", label, s.Topic, s.Status, s.Error)
				}
			}
			if f.summaries != 0 || j.Overview.Status != StatusFailed {
				t.Errorf("%s: overview ran with nothing to summarize", label)
			}
			if want := 5 * tc.res.Tokens; j.TokensUsed() != want {
				t.Errorf("%s: counted %d tokens, want %d", label, j.TokensUsed(), want)
			}
		}
	}
}

// A backend that spends nothing must still terminate: top-ups are bounded.
func TestRunZeroTokenBackendTerminates(t *testing.T) {
	for _, n := range []int{1, 30} {
		cfg := testConfig("anthropic", 0, topicNames(n)...)
		ResolveBudget(&cfg)
		cfg.Concurrency = 10
		f := &scriptBackend{research: spending(0)}
		j, ups := runWithin(t, context.Background(), cfg, f, 30*time.Second)
		checkRun(t, "zero tokens", cfg, j, f.snapshot(), ups)
		if got, max := len(f.snapshot()), n*(1+maxPasses("anthropic")); got > max {
			t.Errorf("%d topics: %d passes, want at most %d", n, got, max)
		}
	}
}

// A failed, truncated or empty top-up pass keeps the earlier digest.
func TestTopUpFailureKeepsEarlierDigest(t *testing.T) {
	first := Result{Markdown: "### Original\nKept [s](https://orig.example).", Sources: []Source{{URL: "https://extra.example"}}, Tokens: 10_000, Searches: 1}
	for _, bad := range []Result{
		{Err: errors.New("rate limited"), Tokens: 3_000},
		{Markdown: "### Half writ", Truncated: true, Tokens: 30_000},
		{Markdown: "", Tokens: 2_000},
		{Err: errors.New("the model declined to cover this topic")},
	} {
		cfg := testConfig("anthropic", 400_000, "A")
		f := &scriptBackend{research: func(_ context.Context, t Task, _ int) Result {
			if t.Prior == "" {
				return first
			}
			return bad
		}}
		j, _ := runWithin(t, context.Background(), cfg, f, 30*time.Second)
		s := j.Sections[0]
		if s.Passes == 0 {
			t.Fatalf("no top-up pass ran")
		}
		if s.Markdown != first.Markdown || len(s.Sources) != 1 || s.Status != StatusDone || s.Error != "" {
			t.Errorf("after bad top-up %+v: section %s %q %q sources %v", bad, s.Status, s.Markdown, s.Error, s.Sources)
		}
		if s.Truncated {
			t.Errorf("a truncated top-up marked the kept digest as truncated")
		}
	}
}

// A truncated first pass is flagged; a successful top-up that replaces the
// digest with a complete one clears the "ran out" note.
func TestTopUpClearsTruncatedFlag(t *testing.T) {
	cfg := testConfig("anthropic", 400_000, "A")
	f := &scriptBackend{research: func(_ context.Context, t Task, _ int) Result {
		if t.Prior == "" {
			return Result{Markdown: "### Cut", Truncated: true, Tokens: 30_000, Searches: 1}
		}
		return Result{Markdown: "### Full\nComplete digest.", Tokens: 30_000, Searches: 1}
	}}
	j, _ := runWithin(t, context.Background(), cfg, f, 30*time.Second)
	s := j.Sections[0]
	if s.Markdown != "### Full\nComplete digest." {
		t.Fatalf("top-up revision not applied: %q", s.Markdown)
	}
	if s.Truncated || strings.Contains(s.Render(), "ran out") {
		t.Errorf("complete revised digest still rendered as truncated: %q", s.Render())
	}
}

// waitGoroutines waits for the goroutine count to fall back to want.
func waitGoroutines(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for runtime.NumGoroutine() > want {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			t.Fatalf("%d goroutines still running, want %d:\n%s", runtime.NumGoroutine(), want, buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// blockingResearch blocks every pass matching block until ctx is cancelled.
func blockingResearch(started chan<- struct{}, block func(Task) bool) func(context.Context, Task, int) Result {
	return func(ctx context.Context, t Task, _ int) Result {
		if !block(t) {
			return Result{Markdown: "### First\nx", Tokens: t.Budget / 4, Searches: 1}
		}
		select {
		case started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return Result{Err: errors.New("cancelled"), Tokens: 1_000}
	}
}

func TestRunCancelledDuringResearch(t *testing.T) {
	for _, conc := range []int{1, 10} {
		before := runtime.NumGoroutine()
		cfg := testConfig("claude-code", 0, topicNames(30)...)
		ResolveBudget(&cfg)
		cfg.Concurrency = conc
		started := make(chan struct{}, 1)
		f := &scriptBackend{research: blockingResearch(started, func(Task) bool { return true })}
		ctx, cancel := context.WithCancel(context.Background())
		go func() { <-started; cancel() }()
		// A consumer that stops reading after the cancel must not block Run.
		g := NewGenerator(cfg, f)
		j := g.NewJournal()
		updates := make(chan Update)
		finished := make(chan struct{})
		go func() { g.Run(ctx, j, updates); close(finished) }()
		go func() {
			for range updates {
				if ctx.Err() != nil {
					return
				}
			}
		}()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Fatalf("conc %d: Run hung after cancellation", conc)
		}
		cancel()
		if n := len(f.snapshot()); n > conc {
			t.Errorf("conc %d: %d passes started, more than were in flight at cancel", conc, n)
		}
		if f.summaries != 0 {
			t.Errorf("conc %d: overview ran after cancellation", conc)
		}
		if j.TokensUsed() > j.TokenBudget {
			t.Errorf("conc %d: over budget", conc)
		}
		waitGoroutines(t, before)
	}
}

func TestRunCancelledDuringTopUp(t *testing.T) {
	before := runtime.NumGoroutine()
	cfg := testConfig("anthropic", 1_000_000, "A", "B", "C")
	cfg.Concurrency = 10
	started := make(chan struct{}, 1)
	f := &scriptBackend{research: blockingResearch(started, func(t Task) bool { return t.Prior != "" })}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { <-started; cancel() }()
	j, _ := runWithin(t, ctx, cfg, f, 30*time.Second)
	for _, s := range j.Sections {
		if s.Status != StatusDone || s.Markdown != "### First\nx" {
			t.Errorf("%s: %s %q, want the first digest kept after a cancelled top-up", s.Topic, s.Status, s.Markdown)
		}
	}
	var topUps int
	for _, task := range f.snapshot() {
		if task.Prior != "" {
			topUps++
		}
	}
	if topUps > 3 {
		t.Errorf("%d top-up passes started; a new round started after cancellation", topUps)
	}
	waitGoroutines(t, before)
}

func TestAssessAdversarial(t *testing.T) {
	t.Run("ratio boundaries", func(t *testing.T) {
		cfg := testConfig("anthropic", 0, "A")
		cfg.PriceInputPerMTok, cfg.PriceOutputPerMTok, cfg.PricePer1KSearches = 0, 0, 0
		base := Baseline(cfg)
		cases := []struct {
			budget int64
			level  Level
		}{
			{base / 2, LevelOK},
			{base/2 - 1, LevelLow},
			{2 * base, LevelOK},
			{2*base + 1, LevelHigh},
			{4 * base, LevelHigh},
			{4*base + 1, LevelVeryHigh},
		}
		for _, c := range cases {
			cfg.TokenBudget = c.budget
			if a := Assess(cfg); a.Level != c.level {
				t.Errorf("budget %d (base %d): level %s, want %s %v", c.budget, base, a.Level, c.level, a.Messages)
			}
		}
	})

	t.Run("very high API budget warns about level and cost", func(t *testing.T) {
		for _, p := range []string{"anthropic", "aws", "vertex"} {
			cfg := testConfig(p, 50_000_000, "A", "B")
			a := Assess(cfg)
			joined := strings.Join(a.Messages, "\n")
			if a.Level != LevelVeryHigh || !strings.Contains(joined, "very high") || !strings.Contains(joined, "estimated cost") {
				t.Errorf("%s 50M: level %s, messages %q", p, a.Level, joined)
			}
		}
		cfg := testConfig("claude-code", 50_000_000, "A", "B")
		if a := Assess(cfg); strings.Contains(strings.Join(a.Messages, "\n"), "estimated cost") || a.CostUSD != 0 {
			t.Errorf("claude-code got a dollar cost warning: %v", a.Messages)
		}
	})

	t.Run("cost warning alone raises level to costly", func(t *testing.T) {
		cfg := testConfig("anthropic", 0, "A")
		cfg.PricePer1KSearches = 10_000
		ResolveBudget(&cfg)
		a := Assess(cfg)
		if a.Level != LevelCostly || len(a.Messages) != 1 || !strings.Contains(a.Messages[0], "estimated cost") {
			t.Errorf("level %s messages %v", a.Level, a.Messages)
		}
	})

	t.Run("one and thirty topics at baseline", func(t *testing.T) {
		for _, p := range []string{"anthropic", "claude-code"} {
			for _, n := range []int{1, 30} {
				cfg := testConfig(p, 0, topicNames(n)...)
				if !ResolveBudget(&cfg) {
					t.Fatal("auto budget not resolved")
				}
				a := Assess(cfg)
				if a.Level == LevelLow {
					t.Errorf("%s %d topics: the auto budget is assessed low: %v", p, n, a.Messages)
				}
				for _, tp := range a.Topics {
					if tp.Searches < 1 {
						t.Errorf("%s %d topics: %s planned 0 searches at baseline", p, n, tp.Name)
					}
				}
			}
		}
	})
}

// The journal stays within max_read_minutes with many topics or a tight
// cap: the per-topic floor drops to 1 item, then to 0 by weight, and Assess
// says so.
func TestReadMinutesCapHoldsWithManyTopics(t *testing.T) {
	cases := []struct {
		topics, minutes int
	}{{30, 8}, {5, 1}, {12, 3}}
	for _, c := range cases {
		cfg := testConfig("anthropic", 0, topicNames(c.topics)...)
		cfg.MaxReadMinutes = c.minutes
		ResolveBudget(&cfg)
		caps := itemCaps(cfg)
		total := 0
		for _, n := range caps {
			total += n
		}
		a := Assess(cfg)
		if a.ReadMinutes > float64(c.minutes) || total > totalItems(cfg) {
			t.Errorf("%d topics, cap %d min: %d items planned, ~%.1f min read", c.topics, c.minutes, total, a.ReadMinutes)
		}
		if !strings.Contains(strings.Join(a.Messages, "\n"), "max_read_minutes") {
			t.Errorf("%d topics, cap %d min: no reading cap warning in %v", c.topics, c.minutes, a.Messages)
		}
		// Topics without items are skipped and named; the run stays within the cap.
		f := &scriptBackend{research: func(_ context.Context, t Task, _ int) Result {
			return Result{Markdown: strings.Repeat("### Item\nSomething happened.\n\n", 12), Tokens: t.Budget / 2, Searches: 1}
		}}
		j, ups := runWithin(t, context.Background(), cfg, f, 30*time.Second)
		checkRun(t, fmt.Sprintf("%d topics cap %d min", c.topics, c.minutes), cfg, j, f.snapshot(), ups)
		for i, s := range j.Sections {
			if caps[i] == 0 && (s.Status != StatusFailed || !strings.Contains(strings.Join(a.Messages, "\n"), s.Topic)) {
				t.Errorf("%s has no items yet is %s or unnamed in %v", s.Topic, s.Status, a.Messages)
			}
		}
		if m := j.ReadMinutes(); m > float64(c.minutes) {
			t.Errorf("%d topics, cap %d min: journal reads in %.1f min", c.topics, c.minutes, m)
		}
	}
}

func TestCapItems(t *testing.T) {
	md := "### A\na.\n\n### B\nb.\n\n**Related**\n\n### C\nc.\n\n### D\nd."
	for n, want := range map[int]string{
		1: "### A\na.",
		2: "### A\na.\n\n### B\nb.",
		3: "### A\na.\n\n### B\nb.\n\n**Related**\n\n### C\nc.",
		4: md,
		9: md,
	} {
		if got := capItems(md, n); got != want {
			t.Errorf("cap %d: %q, want %q", n, got, want)
		}
	}
	if got := capItems("Nothing notable today.", 2); got != "Nothing notable today." {
		t.Errorf("no items: %q", got)
	}
	// Items over their word allowance are dropped from the end, keeping one.
	long := "### A\n" + strings.Repeat("w ", 60) + "\n### B\n" + strings.Repeat("w ", 60)
	if got := capItems(long, 2); countItems(got) != 1 {
		t.Errorf("long items kept %d", countItems(got))
	}
}

// Weights are relative shares: uniformly small weights give the same auto
// budget as none.
func TestAutoBudgetUsesRelativeWeights(t *testing.T) {
	plain := testConfig("anthropic", 0, "A", "B", "C", "D", "E")
	small := testConfig("anthropic", 0)
	for _, n := range []string{"A", "B", "C", "D", "E"} {
		small.Topics = append(small.Topics, config.Topic{Name: n, Weight: 0.1})
	}
	ResolveBudget(&small)
	if small.TokenBudget != Baseline(plain) {
		t.Errorf("weights 0.1: auto budget %d, want the plain %d", small.TokenBudget, Baseline(plain))
	}
	a := Assess(small)
	t.Logf("weights 0.1: auto budget %d (plain %d), level %s", small.TokenBudget, Baseline(plain), a.Level)
	if a.Level == LevelLow || a.Searches == 0 {
		t.Errorf("auto budget %s for five 0.1-weight topics is assessed %s with %d searches: the baseline cannot fund one pass per topic",
			HumanTokens(small.TokenBudget), a.Level, a.Searches)
	}
	f := &scriptBackend{research: spending(0.6)}
	runWithin(t, context.Background(), small, f, 30*time.Second)
	if len(f.snapshot()) == 0 {
		t.Errorf("auto budget with weights 0.1 researched nothing")
	}
}

func TestReadMinutesAndUtilization(t *testing.T) {
	j := &Journal{TokenBudget: 0}
	if j.Utilization() != 0 {
		t.Errorf("zero budget utilization = %v", j.Utilization())
	}
	j = &Journal{TokenBudget: 100, Overview: Section{TokensUsed: 40, Markdown: strings.Repeat("w ", 230)}, Sections: []*Section{{TokensUsed: 60, Markdown: strings.Repeat("w ", 230)}}}
	if j.Utilization() != 100 {
		t.Errorf("utilization = %v, want 100", j.Utilization())
	}
	if m := j.ReadMinutes(); m != 2 {
		t.Errorf("read minutes = %v, want 2", m)
	}
}

// A first pass that finds no room for its first search (the calibration
// grew while it waited) spends nothing and is marked skipped for budget,
// then retried as a first pass by the top-ups; it is never done without a
// search.
func TestFirstPassWithoutRoomToSearchIsRetried(t *testing.T) {
	cfg := testConfig("anthropic", 0, "A", "B", "C")
	var once sync.Once
	b := &scriptBackend{research: func(_ context.Context, task Task, _ int) Result {
		refuse := false
		if task.Topic.Name == "A" {
			once.Do(func() { refuse = true })
		}
		if refuse {
			return Result{Err: errNoSearchRoom}
		}
		return Result{Markdown: "### Item\nx", Tokens: task.Budget / 4, Searches: max(1, task.Searches)}
	}}
	j, ups := runWithin(t, context.Background(), cfg, b, 30*time.Second)
	checkRun(t, "retried", cfg, j, b.snapshot(), ups)
	a := j.Sections[0]
	if a.Status != StatusDone || a.Searches == 0 || a.Error != "" {
		t.Errorf("A: %s, %d searches, error %q; want done after a retry", a.Status, a.Searches, a.Error)
	}
	firsts := 0
	for _, task := range b.snapshot() {
		if task.Topic.Name == "A" && task.Prior == "" {
			firsts++
		}
	}
	if firsts != 2 {
		t.Errorf("A had %d first passes, want the refused one and its retry", firsts)
	}
}

// Top-up passes that cannot search spend nothing, change nothing and do
// not count as passes or progress, so the run ends; the only pass without
// a search is one narrow rewrite per topic.
func TestTopUpWithoutRoomToSearchIsNotProgress(t *testing.T) {
	cfg := testConfig("anthropic", 0, "A", "B", "C")
	b := &scriptBackend{research: func(_ context.Context, task Task, _ int) Result {
		switch {
		case task.Prior != "" && task.Searches > 0:
			return Result{Err: errNoSearchRoom}
		case task.Searches == 0:
			return Result{Markdown: "### Narrowed\nx", Tokens: 5_000}
		}
		return Result{Markdown: "### Item\nx", Tokens: task.Budget / 4, Searches: 1}
	}}
	j, ups := runWithin(t, context.Background(), cfg, b, 30*time.Second)
	checkRun(t, "no room", cfg, j, b.snapshot(), ups)
	narrow := map[string]int{}
	for _, task := range b.snapshot() {
		if task.Searches == 0 {
			narrow[task.Topic.Name]++
		}
	}
	for _, s := range j.Sections {
		if s.Status != StatusDone || s.Passes != narrow[s.Topic] || narrow[s.Topic] > 1 {
			t.Errorf("%s: %s after %d passes, %d narrow; want done with only the narrow pass counted", s.Topic, s.Status, s.Passes, narrow[s.Topic])
		}
	}
}
