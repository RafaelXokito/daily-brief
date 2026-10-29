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

	m.Update(tea.KeyMsg{Type: tea.KeyLeft}) // focus Topics
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if view := m.View(); !strings.Contains(view, "rate limited") {
		t.Errorf("security section should show its error")
	}
	if os.Getenv("SHOW_VIEW") != "" {
		t.Log("\n" + m.View())
	}
}

// Updates to earlier news get a visible marker, and the sidebar a muted
// count of them; the header shows the latest run against its budget and
// what today's runs spent.
func TestViewMarksUpdates(t *testing.T) {
	j := &brief.Journal{
		Date: "2026-10-29", TokenBudget: 200_000,
		Overview: brief.Section{Topic: "Overview", Status: brief.StatusDone, Markdown: "## Today's highlights\n\n- x"},
		Sections: []*brief.Section{
			{Topic: "Security", Status: brief.StatusDone, Markdown: "### Update: Widget breach grows\nSince Mon: more.\n\n### New CVE\nPatch."},
		},
		Runs: []brief.RunRecord{{Budget: 700_000, Used: 600_000}, {Budget: 200_000, Used: 150_000, Failed: 150_000}},
	}
	m := New(nil, nil, j, "notty")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	view := m.View()
	for _, want := range []string{"↻1", "150.0k / 200.0k tokens (75%)", "on failed topics", "750.0k today in 2 runs"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyLeft}) // focus Topics
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if view := m.View(); !strings.Contains(view, "↻ Update · Widget breach grows") || strings.Contains(view, "Update: Widget") {
		t.Errorf("update heading not marked:\n%s", view)
	}
}
