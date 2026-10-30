package brief

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rafaelpereira/daily-brief/internal/config"
)

func TestBaseline(t *testing.T) {
	weighted := testConfig("anthropic", 0, "A", "B", "C", "D")
	weighted.Topics = append(weighted.Topics, config.Topic{Name: "E", Weight: 2})
	// An API topic plans 4 searches of 34k plus 10k to write, but a run of
	// 1-5 topics gets the floor that keeps its unspendable end under 20%
	// after one 45k result: (287.75k search reserve at 45k + 15k overview)
	// / 0.2.
	const apiFloor = 1_513_750
	tests := []struct {
		name string
		cfg  config.Config
		want int64
	}{
		{"api", testConfig("anthropic", 0, "A", "B", "C", "D", "E"), apiFloor},
		{"aws floor", testConfig("aws", 0, "A"), apiFloor},
		{"vertex floor", testConfig("vertex", 0, "A", "B"), apiFloor},
		{"api many topics: one pass cost each", testConfig("anthropic", 0, topicNames(8)...), 8*212_750 + 15_000},
		{"claude-code", testConfig("claude-code", 0, "A", "B", "C", "D", "E"), 5*145_000 + 15_000},
		{"weights only split it", weighted, apiFloor},
	}
	for _, tt := range tests {
		if got := Baseline(tt.cfg); got != tt.want {
			t.Errorf("%s: Baseline = %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestResolveBudget(t *testing.T) {
	cfg := testConfig("claude-code", 0, "A", "B")
	if !ResolveBudget(&cfg) || cfg.TokenBudget != Baseline(cfg) {
		t.Fatalf("token_budget 0 resolved to %d, want baseline %d", cfg.TokenBudget, Baseline(cfg))
	}
	cfg.TokenBudget = 123_000
	if ResolveBudget(&cfg) || cfg.TokenBudget != 123_000 {
		t.Fatal("an explicit budget must be kept")
	}
	if g := NewGenerator(testConfig("anthropic", 0, "A"), &fakeBackend{}); g.cfg.TokenBudget != 1_513_750 {
		t.Fatalf("generator budget = %d, want the 1513750 baseline", g.cfg.TokenBudget)
	}
}

func TestAssess(t *testing.T) {
	expensive := testConfig("anthropic", 0, "A", "B", "C")
	expensive.PriceInputPerMTok = 40
	lopsided := testConfig("claude-code", 0, "A")
	lopsided.Topics = append(lopsided.Topics, config.Topic{Name: "B", Weight: 0.1})
	tests := []struct {
		name    string
		cfg     config.Config
		mult    float64
		level   Level
		message string
	}{
		{"api baseline", testConfig("anthropic", 0, "A", "B", "C"), 1, LevelOK, ""},
		{"cc baseline", testConfig("claude-code", 0, "A", "B", "C"), 1, LevelOK, ""},
		{"api low", testConfig("anthropic", 0, "A", "B", "C"), 0.4, LevelLow, "too low"},
		{"cc low", testConfig("claude-code", 0, "A", "B", "C"), 0.4, LevelLow, "too low"},
		{"light topic under one search", lopsided, 1, LevelOK, `topic "B" has too small a weight`},
		{"api high", testConfig("anthropic", 0, "A", "B", "C"), 2.5, LevelHigh, "stays capped at ~9 min"},
		{"cc very high", testConfig("claude-code", 0, "A", "B", "C"), 5, LevelVeryHigh, "very high"},
		{"api costly", expensive, 1, LevelCostly, "estimated cost"},
		{"api very high and costly", expensive, 5, LevelVeryHigh, "very high"},
	}
	for _, tt := range tests {
		tt.cfg.TokenBudget = int64(tt.mult * float64(Baseline(tt.cfg)))
		a := Assess(tt.cfg)
		if a.Level != tt.level {
			t.Errorf("%s: level %s, want %s (%v)", tt.name, a.Level, tt.level, a.Messages)
		}
		joined := strings.Join(a.Messages, "\n")
		if tt.message == "" && joined != "" || !strings.Contains(joined, tt.message) {
			t.Errorf("%s: messages %q, want %q", tt.name, joined, tt.message)
		}
		if cc := tt.cfg.Provider.Type == "claude-code"; cc != a.Subscription || cc && a.CostUSD != 0 || !cc && a.CostUSD <= 0 {
			t.Errorf("%s: subscription %v cost $%.2f", tt.name, a.Subscription, a.CostUSD)
		}
		if a.ReadMinutes > float64(tt.cfg.MaxReadMinutes) {
			t.Errorf("%s: %.1f min read exceeds the %d min cap", tt.name, a.ReadMinutes, tt.cfg.MaxReadMinutes)
		}
	}
}

// On the API a pass's plan follows its search loop, whose searches cost
// more as the context grows; claude-code plans 45k per search.
func TestPlanSearchesGrowsWithBudgetAndRespectsCap(t *testing.T) {
	for _, provider := range []string{"anthropic", "claude-code"} {
		cfg := testConfig(provider, 0, "A")
		prev := int64(0)
		for budget := int64(10_000); budget <= 2_000_000; budget *= 2 {
			n := planSearches(cfg, budget)
			if n < max(1, prev) {
				t.Errorf("%s: %d searches at %d tokens, fewer than %d before", provider, n, budget, prev)
			}
			spend, limit := n*costPerSearch(provider, planWindow), planFactor*float64(budget)
			if provider != "claude-code" {
				_, spend = apiPlan(budget, maxTopicItems, n, planWindow)
				limit = float64(budget)
			}
			if n > 1 && float64(spend) > limit {
				t.Errorf("%s: %d searches cost %d, over %.0f of %d", provider, n, spend, limit, budget)
			}
			prev = n
		}
		if want := map[string]int64{"anthropic": 5, "claude-code": 10}[provider]; prev < want {
			t.Errorf("%s: only %d searches at 1.3M tokens, want %d", provider, prev, want)
		}
		cfg.MaxSearchesPerTopic = 3
		if n := planSearches(cfg, 2_000_000); n != 3 {
			t.Errorf("%s: explicit cap ignored: %d searches", provider, n)
		}
	}
}

func TestItemCapsRespectReadingTime(t *testing.T) {
	for _, minutes := range []int{3, 5, 8, 15} {
		cfg := testConfig("anthropic", 400_000, "A", "B", "C", "D", "E")
		cfg.Topics[0].Weight = 3
		cfg.MaxReadMinutes = minutes
		caps := itemCaps(cfg)
		words := overviewWords
		for _, c := range caps {
			if c < minTopicItems || c > maxTopicItems {
				t.Errorf("%d min: cap %d outside %d..%d", minutes, c, minTopicItems, maxTopicItems)
			}
			words += c * wordsPerItem
		}
		if limit := minutes * readingWPM; words > limit && caps[1] > minTopicItems {
			t.Errorf("%d min: caps %v need %d words, over %d", minutes, caps, words, limit)
		}
		if caps[0] < caps[1] {
			t.Errorf("%d min: heavier topic got fewer items: %v", minutes, caps)
		}
	}

	// Each pass's item limit is bounded by its topic's cap.
	cfg := testConfig("anthropic", 4_000_000, "A", "B", "C", "D", "E")
	cfg.MaxReadMinutes = 4
	g := NewGenerator(cfg, &fakeBackend{})
	if task := g.task(0, 3_000_000, ""); task.Items != g.caps[0] || task.Items >= maxTopicItems {
		t.Errorf("items %d, want the reading cap %d", task.Items, g.caps[0])
	}
}

func TestAssessWeightThinTopicKeepsVeryHigh(t *testing.T) {
	cfg := testConfig("anthropic", 0, "A")
	cfg.Topics = append(cfg.Topics, config.Topic{Name: "Light", Weight: 0.001})
	cfg.TokenBudget = 5 * Baseline(cfg)
	a := Assess(cfg)
	joined := strings.Join(a.Messages, "\n")
	if a.Level != LevelVeryHigh || !strings.Contains(joined, `"Light"`) || strings.Contains(joined, "too low") {
		t.Errorf("level %s, messages %q", a.Level, joined)
	}
}

func TestAssessCostCountsTopUpSearches(t *testing.T) {
	cfg := testConfig("anthropic", 3_000_000, "A")
	cfg.MaxSearchesPerTopic = 4
	cfg.PriceInputPerMTok, cfg.PriceOutputPerMTok, cfg.PricePer1KSearches = 0, 0, 1000
	a := Assess(cfg)
	// The first pass searches 4 times, and each top-up 4 more: $1 each.
	if a.Searches != 4 || a.RunSearches <= 4 || a.CostUSD != float64(a.RunSearches) {
		t.Errorf("first pass %d searches, %d in all, cost $%.2f; want 4, more and $1 each", a.Searches, a.RunSearches, a.CostUSD)
	}
}

func TestAssessWarnsWhenSearchCapStrandsBudget(t *testing.T) {
	cfg := testConfig("anthropic", 50_000_000, "A", "B")
	cfg.MaxSearchesPerTopic = 4
	if a := Assess(cfg); !strings.Contains(strings.Join(a.Messages, "\n"), "max_searches_per_topic 4") {
		t.Errorf("no search cap warning: %v", a.Messages)
	}
	cfg.TokenBudget = Baseline(cfg)
	if a := Assess(cfg); strings.Contains(strings.Join(a.Messages, "\n"), "max_searches_per_topic") {
		t.Errorf("search cap warning at the baseline: %v", a.Messages)
	}
}

// claude-code's safety margin leaves few topics short of 85% at the
// per-topic sum, so the baseline is raised until the plan reaches it; a
// run at the baseline does, and Assess warns whenever it predicts less.
func TestClaudeCodeBaselineReaches85WithFewTopics(t *testing.T) {
	for _, n := range []int{1, 2, 3} {
		cfg := testConfig("claude-code", 0, topicNames(n)...)
		ResolveBudget(&cfg)
		a := Assess(cfg)
		share := 100 * float64(a.Spend) / float64(a.Budget)
		t.Logf("%d topics: baseline %s, Assess spend %.0f%%, warnings %v", n, HumanTokens(cfg.TokenBudget), share, a.Messages)
		for _, frac := range []float64{0.6, 1.0} {
			f := &scriptBackend{research: spending(frac)}
			j, _ := runWithin(t, context.Background(), cfg, f, 30*time.Second)
			if j.Utilization() < 85 {
				t.Errorf("%d topics at baseline, spending %.0f%%: utilization %.1f%%", n, 100*frac, j.Utilization())
			}
		}
		if share < 85 && len(a.Messages) == 0 {
			t.Errorf("%d topics: Assess predicts %.0f%% at the baseline with no warning", n, share)
		}
	}
	// Below the baseline, Assess says the run will fall short.
	cfg := testConfig("claude-code", 350_000, topicNames(2)...)
	if a := Assess(cfg); !strings.Contains(strings.Join(a.Messages, "\n"), "spend only about") {
		t.Errorf("claude-code 2 topics at 350k: no warning about spending under 85%%: %v", a.Messages)
	}
}

// The auto budget of a run over some of the topics is their weighted share
// of the baseline of all of them, raised to one search pass per topic plus
// the overview reserve, and Assess shows the full baseline beside it.
func TestSubsetBaselineIsWeightedShare(t *testing.T) {
	all := testConfig("anthropic", 0, "A", "B", "C", "D")
	all.Topics = append(all.Topics, config.Topic{Name: "E", Weight: 2}, config.Topic{Name: "F", Weight: 0.1})
	full := Baseline(all)
	share := func(w float64) int64 { return int64(float64(full) * w / 6.1) }
	need := passNeed("anthropic", planWindow)
	for _, tt := range []struct {
		names []string
		want  int64
	}{
		{[]string{"A"}, share(1)},
		{[]string{"E"}, share(2)},
		{[]string{"A", "E"}, share(3)},
		{[]string{"F"}, need + maxOverviewReserve}, // its share is under one pass
		{[]string{"F", "A"}, 2*need + maxOverviewReserve},
	} {
		cfg := all
		if err := cfg.FilterTopics(tt.names); err != nil {
			t.Fatal(err)
		}
		got, gotFull := SubsetBaseline(cfg)
		if got != tt.want || gotFull != full {
			t.Errorf("%v: subset baseline %d of %d, want %d of %d", tt.names, got, gotFull, tt.want, full)
		}
		if Baseline(cfg) != got {
			t.Errorf("%v: Baseline %d, want the subset's %d", tt.names, Baseline(cfg), got)
		}
		if !ResolveBudget(&cfg) || cfg.TokenBudget != got {
			t.Errorf("%v: auto budget resolved to %d, want %d", tt.names, cfg.TokenBudget, got)
		}
		if a := Assess(cfg); a.Baseline != got || a.FullBaseline != full {
			t.Errorf("%v: Assess baseline %d of %d, want %d of %d", tt.names, a.Baseline, a.FullBaseline, got, full)
		}
	}
	all.TokenBudget = full
	if a := Assess(all); a.FullBaseline != 0 {
		t.Errorf("a run over all topics shows a full baseline %d", a.FullBaseline)
	}
}
