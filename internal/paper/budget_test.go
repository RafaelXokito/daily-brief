package paper

import (
	"strconv"
	"strings"
	"testing"

	"github.com/RafaelXokito/daily-brief/internal/brief"
	"github.com/RafaelXokito/daily-brief/internal/config"
	"github.com/RafaelXokito/daily-brief/internal/editor"
)

func budgetConfig(topics int, general bool) config.Config {
	cfg := config.Default()
	cfg.General = general
	cfg.Topics = nil
	for i := 0; i < topics; i++ {
		cfg.Topics = append(cfg.Topics, config.Topic{Name: string(rune('A' + i))})
	}
	return cfg
}

func TestSplitBudget(t *testing.T) {
	cfg := budgetConfig(8, true)
	items := brief.MaxItems(cfg)
	want := editor.Reserve(9, items, MarginPercent)
	got, err := SplitBudget(1_200_000, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got.Editor != want || got.Research != 1_200_000-want {
		t.Errorf("split %+v, want editor %d", got, want)
	}
	if got.Research < 600_000 {
		t.Errorf("research %d below half", got.Research)
	}
}

func TestSplitBudgetGeneralOffCountsConfiguredTopics(t *testing.T) {
	cfg := budgetConfig(8, false)
	got, err := SplitBudget(10_000_000, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if want := editor.Reserve(8, brief.MaxItems(cfg), MarginPercent); got.Editor != want {
		t.Errorf("editor %d, want %d", got.Editor, want)
	}
}

func TestSplitBudgetTooSmall(t *testing.T) {
	cfg := budgetConfig(8, true)
	reserve := editor.Reserve(9, brief.MaxItems(cfg), MarginPercent)
	_, err := SplitBudget(2*reserve-1, cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), strconv.FormatInt(2*reserve, 10)) {
		t.Errorf("error should name the smallest budget %d: %v", 2*reserve, err)
	}
	if _, err := SplitBudget(2*reserve, cfg); err != nil {
		t.Errorf("exactly twice the reserve must work: %v", err)
	}
}

// The editor's margin, like the generator's, applies only to claude-code.
func TestSplitBudgetMarginOnlyForClaudeCode(t *testing.T) {
	cfg := budgetConfig(8, true)
	cfg.Provider.Type = "anthropic"
	got, err := SplitBudget(10_000_000, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if want := editor.Reserve(9, brief.MaxItems(cfg), 0); got.Editor != want {
		t.Errorf("editor %d, want %d with no margin", got.Editor, want)
	}
}
