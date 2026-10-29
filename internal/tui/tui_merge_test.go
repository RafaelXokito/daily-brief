package tui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rafaelpereira/daily-brief/internal/brief"
	"github.com/rafaelpereira/daily-brief/internal/config"
)

// doneBackend finishes every pass at once.
type doneBackend struct{}

func (doneBackend) Research(_ context.Context, t brief.Task) brief.Result {
	return brief.Result{Markdown: "### New " + t.Topic.Name, Tokens: 1_000, Searches: 1}
}

func (doneBackend) Summarize(context.Context, string, string) brief.Result {
	return brief.Result{Markdown: "- new", Tokens: 500}
}

func (doneBackend) Name() string { return "done" }

// A TUI run over some topics saves into today's journal without losing the
// others, and shows the merged journal when done.
func TestFilteredRunInTUIKeepsOtherTopics(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	prev := &brief.Journal{Date: brief.Today(), TokenBudget: 600_000,
		Overview: brief.Section{Topic: "Overview", Status: brief.StatusDone, Markdown: "- old"},
		Sections: []*brief.Section{
			{Topic: "A", Status: brief.StatusDone, Markdown: "### Old A"},
			{Topic: "B", Status: brief.StatusDone, Markdown: "### Old B"},
		}}
	if err := brief.SaveJournal(prev); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Provider.Type = "anthropic"
	cfg.TokenBudget = 700_000
	cfg.Topics = []config.Topic{{Name: "A"}, {Name: "B"}}
	if err := cfg.FilterTopics([]string{"B"}); err != nil {
		t.Fatal(err)
	}
	m := New(brief.NewGenerator(cfg, doneBackend{}), nil, nil, "notty")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	drain(t, m, m.start())

	saved, err := brief.LoadJournal(brief.Today())
	if err != nil || saved == nil || len(saved.Sections) != 2 {
		t.Fatalf("saved %+v, %v", saved, err)
	}
	if a, b := saved.Sections[0], saved.Sections[1]; a.Markdown != "### Old A" || b.Markdown != "### New B" {
		t.Errorf("saved A %q, B %q", a.Markdown, b.Markdown)
	}
	if len(m.journal.Sections) != 2 || m.journal.Sections[0].Markdown != "### Old A" {
		t.Errorf("display shows %d sections", len(m.journal.Sections))
	}
}
