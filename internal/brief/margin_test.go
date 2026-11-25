package brief

import (
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
	cfg := config.Config{Provider: config.Provider{Type: "claude-code"}, TokenBudget: 500_000}
	g := NewGenerator(cfg, &fakeBackend{})
	for _, alloc := range []int64{0, 1, 99_999, 250_000} {
		for _, search := range []bool{false, true} {
			if got, want := g.margin(alloc, search), margin("claude-code", alloc, search); got != want {
				t.Errorf("margin(%d,%v) = %d, want %d", alloc, search, got, want)
			}
		}
	}
	if got, want := g.spendable(333_333, 2, true), spendable("claude-code", 333_333, 2, true); got != want {
		t.Errorf("spendable = %d, want %d", got, want)
	}
	if got := g.WithMarginPercent(30).margin(100_000, false); got != 30_000 {
		t.Errorf("WithMarginPercent(30) margin = %d", got)
	}
}

func TestMaxItemsAndEstimateTokens(t *testing.T) {
	cfg := config.Config{Provider: config.Provider{Type: "claude-code"}, TokenBudget: 500_000, MaxReadMinutes: 10,
		Topics: []config.Topic{{Name: "a"}, {Name: "b"}}}
	want := 0
	for _, c := range itemCaps(cfg) {
		want += c
	}
	if got := MaxItems(cfg); got != want || got == 0 {
		t.Errorf("MaxItems = %d, want %d", got, want)
	}
	if EstimateTokens("hello") != estimateTokens("hello") {
		t.Error("EstimateTokens differs")
	}
}
