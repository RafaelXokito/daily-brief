package paper

import (
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
	want := editor.Reserve(9, items)
	got, err := SplitBudget(400_000, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got.Editor != want || got.Research != 400_000-want {
		t.Errorf("split %+v, want editor %d", got, want)
	}
	if got.Research < 200_000 {
		t.Errorf("research %d below half", got.Research)
	}
}

func TestSplitBudgetGeneralOffCountsConfiguredTopics(t *testing.T) {
	cfg := budgetConfig(8, false)
	got, err := SplitBudget(10_000_000, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if want := editor.Reserve(8, brief.MaxItems(cfg)); got.Editor != want {
		t.Errorf("editor %d, want %d", got.Editor, want)
	}
}

func TestSplitBudgetTooSmall(t *testing.T) {
	cfg := budgetConfig(8, true)
	reserve := editor.Reserve(9, brief.MaxItems(cfg))
	_, err := SplitBudget(2*reserve-1, cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), itoa(2*reserve)) {
		t.Errorf("error should name the smallest budget %d: %v", 2*reserve, err)
	}
	if _, err := SplitBudget(2*reserve, cfg); err != nil {
		t.Errorf("exactly twice the reserve must work: %v", err)
	}
}
