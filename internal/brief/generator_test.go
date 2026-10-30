package brief

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"

	"github.com/RafaelXokito/daily-brief/internal/config"
)

func TestNewJournalSplitsBudgetByWeight(t *testing.T) {
	cfg := config.Default()
	cfg.TokenBudget = 200_000
	cfg.Topics = []config.Topic{{Name: "A"}, {Name: "B", Weight: 3}}
	j := NewGenerator(cfg, &claudeCodeBackend{cfg: cfg}).NewJournal()

	if j.Overview.Allocated != 10_000 {
		t.Fatalf("overview reserve = %d, want 10000", j.Overview.Allocated)
	}
	if a, b := j.Sections[0].Allocated, j.Sections[1].Allocated; a != 47_500 || b != 142_500 {
		t.Fatalf("budgets = %d, %d; want 47500, 142500", a, b)
	}
}

func TestDigestKeepsTextAfterLastSearch(t *testing.T) {
	raw := `[
	  {"type":"text","text":"Let me search.","citations":null},
	  {"type":"server_tool_use","id":"s1","name":"web_search","input":{"query":"x"}},
	  {"type":"web_search_tool_result","tool_use_id":"s1","content":[]},
	  {"type":"text","text":"### Big CVE\nPatch now [source](https://a.example).","citations":null},
	  {"type":"text","text":" Also relevant.","citations":[
	    {"type":"web_search_result_location","url":"https://a.example","title":"A","cited_text":"x","encrypted_index":"e"},
	    {"type":"web_search_result_location","url":"https://b.example","title":"B","cited_text":"y","encrypted_index":"e"}
	  ]}
	]`
	var content []anthropic.BetaContentBlockUnion
	if err := json.Unmarshal([]byte(raw), &content); err != nil {
		t.Fatal(err)
	}
	md, sources := digest(content)

	if want := "### Big CVE\nPatch now [source](https://a.example). Also relevant."; md != want {
		t.Fatalf("markdown = %q, want %q", md, want)
	}
	if len(sources) != 1 || sources[0].URL != "https://b.example" {
		t.Fatalf("sources = %+v, want only the uncited-inline b.example", sources)
	}
}

// fakeBackend records each pass and spends spend(task) tokens on it.
type fakeBackend struct {
	mu    sync.Mutex
	spend func(Task) int64
	tasks []Task
}

func (f *fakeBackend) Research(_ context.Context, t Task) Result {
	f.mu.Lock()
	f.tasks = append(f.tasks, t)
	f.mu.Unlock()
	return Result{Markdown: "### Item\nSomething happened.", Tokens: f.spend(t), Searches: t.Searches}
}

func (f *fakeBackend) Summarize(context.Context, string, string) Result {
	return Result{Markdown: "## Today's highlights\n\n- x", Tokens: 2_000}
}

func (f *fakeBackend) Name() string { return "fake" }

func spendFraction(frac float64) func(Task) int64 {
	return func(t Task) int64 { return int64(frac * float64(t.Budget)) }
}

func testConfig(provider string, budget int64, topics ...string) config.Config {
	cfg := config.Default()
	cfg.Provider.Type = provider
	cfg.TokenBudget = budget
	cfg.Topics = nil
	for _, n := range topics {
		cfg.Topics = append(cfg.Topics, config.Topic{Name: n})
	}
	return cfg
}

func runFake(cfg config.Config, f *fakeBackend) *Journal {
	g := NewGenerator(cfg, f)
	j := g.NewJournal()
	updates := make(chan Update)
	go g.Run(context.Background(), j, updates)
	for range updates {
	}
	return j
}

func TestRunSpendsMostOfBudgetWithoutExceedingIt(t *testing.T) {
	for _, provider := range []string{"anthropic", "claude-code"} {
		for _, mult := range []float64{1, 2, 3} {
			for _, conc := range []int{1, 3} {
				cfg := testConfig(provider, 0, "A", "B", "C", "D", "E")
				cfg.TokenBudget = int64(mult * float64(Baseline(cfg)))
				cfg.Concurrency = conc
				f := &fakeBackend{spend: spendFraction(0.6)}
				j := runFake(cfg, f)

				used := float64(j.TokensUsed()) / float64(j.TokenBudget)
				t.Logf("%s ×%.0f conc %d: %.1f%% in %d passes", provider, mult, conc, 100*used, len(f.tasks))
				if used < 0.85 || used > 1 {
					t.Errorf("%s ×%.0f conc %d: spent %.1f%% of the budget, want 85-100%%", provider, mult, conc, 100*used)
				}
				for _, task := range f.tasks {
					if task.Searches > 0 && task.Budget < passCost(provider, planWindow) || task.Budget < narrowMin {
						t.Errorf("%s: started a pass with %d tokens, under one pass cost", provider, task.Budget)
					}
				}
				if j.Overview.Status != StatusDone {
					t.Errorf("%s: overview not written: %s", provider, j.Overview.Error)
				}
			}
		}
	}
}

func TestUnspentBudgetFundsLaterTopics(t *testing.T) {
	cfg := testConfig("anthropic", 1_200_000, "A", "B", "C")
	cfg.Concurrency = 1
	f := &fakeBackend{spend: func(t Task) int64 {
		if t.Topic.Name == "A" {
			return 0
		}
		return int64(0.6 * float64(t.Budget))
	}}
	runFake(cfg, f)

	pool := cfg.TokenBudget - overviewReserve(cfg.TokenBudget)
	a, b := f.tasks[0], f.tasks[1]
	if a.Topic.Name != "A" || b.Topic.Name != "B" {
		t.Fatalf("unexpected order: %s, %s", a.Topic.Name, b.Topic.Name)
	}
	if a.Budget != pool/3 || b.Budget != pool/2 {
		t.Fatalf("budgets A=%d B=%d; want %d then %d", a.Budget, b.Budget, pool/3, pool/2)
	}
}

func TestNoPassStartsUnderOnePassCost(t *testing.T) {
	// The budget covers one pass beside claude-code's margin: the first
	// topic runs (a narrow pass may follow), the rest are skipped.
	cfg := testConfig("claude-code", 120_000, "A", "B", "C")
	f := &fakeBackend{spend: spendFraction(1)}
	j := runFake(cfg, f)
	var search int
	for _, task := range f.tasks {
		if task.Topic.Name != "A" {
			t.Fatalf("passes = %+v, want only topic A", f.tasks)
		}
		if task.Searches > 0 {
			search++
		}
	}
	if search != 1 {
		t.Fatalf("%d search passes, want one", search)
	}
	for _, s := range j.Sections[1:] {
		if s.Status != StatusFailed || !strings.HasPrefix(s.Error, "skipped") {
			t.Errorf("%s: status %s %q, want skipped", s.Topic, s.Status, s.Error)
		}
	}

	// Under one pass cost in total: nothing may start.
	cfg.TokenBudget = 50_000
	f = &fakeBackend{spend: spendFraction(1)}
	runFake(cfg, f)
	if len(f.tasks) != 0 {
		t.Fatalf("started %d passes with only %d tokens", len(f.tasks), cfg.TokenBudget)
	}

	// Topics spend all but a sliver: no top-up search pass fits.
	cfg = testConfig("anthropic", 200_000, "A", "B")
	f = &fakeBackend{spend: spendFraction(0.95)}
	runFake(cfg, f)
	for _, task := range f.tasks {
		if task.Prior != "" && task.Searches > 0 {
			t.Fatalf("top-up pass started with %d tokens left", task.Budget)
		}
	}
}

func TestTopUpPassesReviseTheDigest(t *testing.T) {
	cfg := testConfig("anthropic", 400_000, "A", "B")
	f := &fakeBackend{spend: spendFraction(0.3)}
	j := runFake(cfg, f)
	var passes int
	for _, task := range f.tasks {
		if task.Prior != "" {
			passes++
			if !strings.Contains(taskPrompt(task), task.Prior) {
				t.Errorf("pass prompt lacks the prior digest")
			}
		}
	}
	if passes == 0 {
		t.Fatal("no top-up pass ran despite a large leftover")
	}
	for _, s := range j.Sections {
		if s.Status != StatusDone || s.Passes == 0 {
			t.Errorf("%s: status %s, passes %d", s.Topic, s.Status, s.Passes)
		}
	}
}

func TestTopUpKeepsReadingCapAndReplacesSources(t *testing.T) {
	cfg := testConfig("anthropic", 400_000, "A")
	g := NewGenerator(cfg, &fakeBackend{})
	f := &scriptBackend{research: func(_ context.Context, t Task, _ int) Result {
		if t.Prior == "" {
			return Result{Markdown: "### One\nx", Sources: []Source{{URL: "https://old.example"}}, Tokens: 30_000, Searches: 1}
		}
		return Result{Markdown: "### Two\ny", Sources: []Source{{URL: "https://new.example"}}, Tokens: 30_000, Searches: 1}
	}}
	j, _ := runWithin(t, context.Background(), cfg, f, 30*time.Second)
	for _, task := range f.snapshot() {
		if task.Prior != "" && task.Items != g.caps[0] {
			t.Errorf("top-up asked for %d items, want the reading cap %d", task.Items, g.caps[0])
		}
	}
	if s := j.Sections[0]; len(s.Sources) != 1 || s.Sources[0].URL != "https://new.example" {
		t.Errorf("sources after revision: %v", s.Sources)
	}
}

func TestClaudeCodeUnreadableOutputCountsPlannedBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'not json'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig("claude-code", 0, "A")
	b := &claudeCodeBackend{cfg: cfg, path: path}
	res := b.Research(context.Background(), Task{Topic: cfg.Topics[0], Budget: 90_000, Searches: 1, Items: 3})
	if res.Err == nil || res.Tokens != 90_000 {
		t.Errorf("err %v, %d tokens; want an error counting the 90000 planned", res.Err, res.Tokens)
	}
}
