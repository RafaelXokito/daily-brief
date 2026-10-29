package tui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rafaelpereira/daily-brief/internal/brief"
)

func TestViewShowsSectionsAndContent(t *testing.T) {
	j := &brief.Journal{
		Date:        "2026-10-29",
		TokenBudget: 400_000,
		BudgetLevel: brief.LevelLow,
		Overview:    brief.Section{Topic: "Overview", Status: brief.StatusDone, Markdown: "## Today's highlights\n\n- Quarkus release", TokensUsed: 3_200},
		Sections: []*brief.Section{
			{Topic: "Java & Quarkus", Status: brief.StatusDone, Markdown: "### Quarkus 3.x\nFaster startup.", TokensUsed: 61_000},
			{Topic: "Security", Status: brief.StatusFailed, Error: "rate limited"},
		},
	}
	m := New(nil, nil, j, "notty")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	view := m.View()
	for _, want := range []string{"Daily Brief", "64.2k / 400.0k tokens (16%)", "min read", "⚠ budget low", "Java & Quarkus", "61.0k", "Security", "highlights"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q", want)
		}
	}

	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if view := m.View(); !strings.Contains(view, "rate limited") {
		t.Errorf("security section should show its error")
	}
	if os.Getenv("SHOW_VIEW") != "" {
		t.Log("\n" + m.View())
	}
}
