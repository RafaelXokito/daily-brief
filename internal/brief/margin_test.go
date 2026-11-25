package brief

import (
	"slices"
	"testing"

	"github.com/RafaelXokito/daily-brief/internal/config"
)

var (
	_ Composer = (*apiBackend)(nil)
	_ Composer = (*claudeCodeBackend)(nil)
)

func TestMarginPercent(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		pct      int
		alloc    int64
		search   bool
		want     int64
	}{
		{"default pct", "claude-code", 10, 100_000, false, 10_000},
		{"wider pct", "claude-code", 30, 100_000, false, 30_000},
		{"search adds a search", "claude-code", 30, 100_000, true, 30_000 + ccSearchCost},
		{"api has no margin", "anthropic", 30, 100_000, true, 0},
	}
	for _, tt := range tests {
		if got := marginPct(tt.provider, tt.pct, tt.alloc, tt.search); got != tt.want {
			t.Errorf("%s: marginPct = %d, want %d", tt.name, got, tt.want)
		}
	}
	if got := spendablePct("claude-code", 10, 110_000, 1, false); got != 100_000 {
		t.Errorf("spendablePct 10 = %d", got)
	}
	if got := spendablePct("claude-code", 30, 130_000, 1, false); got != 100_000 {
		t.Errorf("spendablePct 30 = %d", got)
	}
	if got := spendablePct("claude-code", 30, 130_000+ccSearchCost, 1, true); got != 100_000 {
		t.Errorf("spendablePct 30 search = %d", got)
	}
	if got := spendablePct("anthropic", 30, 130_000, 1, true); got != 130_000 {
		t.Errorf("api spendablePct = %d", got)
	}
}

func TestDefaultMarginUnchanged(t *testing.T) {
	// Literals from the code before the margin became configurable.
	if got := margin("claude-code", 99_999, false); got != 9_999 {
		t.Errorf("margin = %d, want 9999", got)
	}
	if got := margin("claude-code", 99_999, true); got != 9_999+ccSearchCost {
		t.Errorf("margin with search = %d", got)
	}
	if got, want := spendable("claude-code", 333_333, 2, true), int64((333_333-2*ccSearchCost)*10/11); got != want || got != 221_211 {
		t.Errorf("spendable = %d, want %d", got, want)
	}
	if got := spendable("claude-code", 333_333, 2, false); got != 303_030 {
		t.Errorf("spendable no search = %d, want 303030", got)
	}

	// Pass budgets planned by the pre-change code (commit 7ddfc72) for this run.
	cfg := testConfig("claude-code", 0, "A", "B", "C")
	cfg.TokenBudget = 600_000
	cfg.Concurrency = 1
	f := &fakeBackend{spend: spendFraction(0.6)}
	runFake(cfg, f)
	var got []int64
	for _, task := range f.tasks {
		got = append(got, task.Budget)
	}
	// Sorted: the order of the narrow passes is not deterministic.
	slices.Sort(got)
	want := []int64{19085, 19085, 30171, 60354, 132779, 163636, 200826, 292112}
	if !slices.Equal(got, want) {
		t.Errorf("pass budgets = %v, want %v", got, want)
	}

	g := NewGenerator(cfg, &fakeBackend{})
	if g.marginPercent != 10 {
		t.Errorf("default marginPercent = %d", g.marginPercent)
	}
	if got := g.WithMarginPercent(30).margin(100_000, false); got != 30_000 {
		t.Errorf("WithMarginPercent(30) margin = %d", got)
	}
}

func TestWithMarginPercentClampsNegative(t *testing.T) {
	cfg := testConfig("claude-code", 0, "A")
	for _, pct := range []int{0, -50, -100, -1000} {
		g := NewGenerator(cfg, &fakeBackend{}).WithMarginPercent(pct)
		if g.marginPercent != 0 {
			t.Errorf("pct %d: marginPercent = %d, want 0", pct, g.marginPercent)
		}
		if got := g.margin(100_000, false); got != 0 {
			t.Errorf("pct %d: margin = %d, want 0", pct, got)
		}
		if got := g.spendable(100_000, 1, false); got != 100_000 {
			t.Errorf("pct %d: spendable = %d, want 100000", pct, got)
		}
	}
}

func TestMaxItems(t *testing.T) {
	cfg := config.Config{Provider: config.Provider{Type: "claude-code"}, TokenBudget: 500_000, MaxReadMinutes: 10,
		Topics: []config.Topic{{Name: "a"}, {Name: "b"}}}
	base := MaxItems(cfg)
	if base != 20 {
		t.Errorf("MaxItems = %d, want 20", base)
	}
	cfg.General = true
	if got := MaxItems(cfg); got != 30 {
		t.Errorf("MaxItems with general = %d, want 30", got)
	}
	if n := len(itemCaps(cfg)); n != 3 {
		t.Errorf("general adds the World topic: %d caps, want 3", n)
	}
}

func TestEstimateTokens(t *testing.T) {
	if got := EstimateTokens("hello"); got != 3 {
		t.Errorf("EstimateTokens = %d, want 3", got)
	}
}
