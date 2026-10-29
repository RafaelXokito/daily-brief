package tui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rafaelpereira/daily-brief/internal/brief"
	"github.com/rafaelpereira/daily-brief/internal/config"
)

func TestHeaderShowsUtilizationReadTimeAndLevel(t *testing.T) {
	for level, marker := range map[brief.Level]string{
		brief.LevelOK: "", "": "", brief.LevelLow: "⚠ budget low",
		brief.LevelHigh: "⚠ budget high", brief.LevelVeryHigh: "⚠ budget very-high", brief.LevelCostly: "⚠ cost",
	} {
		j := &brief.Journal{
			Date: "2026-10-29", TokenBudget: 200_000, BudgetLevel: level,
			Overview: brief.Section{Topic: "Overview", Status: brief.StatusDone, TokensUsed: 10_000, Markdown: strings.Repeat("word ", 460)},
			Sections: []*brief.Section{{Topic: "A", Status: brief.StatusDone, TokensUsed: 180_000, Markdown: strings.Repeat("word ", 230)}},
		}
		m := New(nil, nil, j, "notty")
		m.Update(tea.WindowSizeMsg{Width: 160, Height: 20})
		header := strings.SplitN(m.View(), "\n", 2)[0]
		for _, want := range []string{"190.0k / 200.0k tokens (95%)", "~3 min read"} {
			if !strings.Contains(header, want) {
				t.Errorf("level %q: header %q missing %q", level, header, want)
			}
		}
		if marker == "" && strings.Contains(header, "⚠") || marker != "" && !strings.Contains(header, marker) {
			t.Errorf("level %q: header %q, want marker %q", level, header, marker)
		}
	}
}

// A run that went over its budget says so in the header and, in full, at
// the top of the Overview pane.
func TestHeaderAndOverviewShowOvershoot(t *testing.T) {
	j := &brief.Journal{
		Date: "2026-10-29", TokenBudget: 200_000, Overshoot: 12_000,
		Overview: brief.Section{Topic: "Overview", Status: brief.StatusFailed, Error: "skipped"},
		Sections: []*brief.Section{{Topic: "A", Status: brief.StatusDone, TokensUsed: 212_000, Markdown: "### A\nx"}},
	}
	m := New(nil, nil, j, "notty")
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 20})
	v := m.View()
	if header := strings.SplitN(v, "\n", 2)[0]; !strings.Contains(header, "⚠ over budget by 12.0k") {
		t.Errorf("header %q lacks the overshoot", header)
	}
	if !strings.Contains(v, "went over its token budget by 12.0k") {
		t.Errorf("overview pane lacks the overshoot note:\n%s", v)
	}
}

// A legacy journal saved before budgets has no token_budget.
func TestHeaderWithLegacyJournal(t *testing.T) {
	j := &brief.Journal{Date: "2026-10-01", Sections: []*brief.Section{{Topic: "A", Status: brief.StatusDone, TokensUsed: 5_000}}}
	m := New(nil, nil, j, "notty")
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 20})
	if h := strings.SplitN(m.View(), "\n", 2)[0]; !strings.Contains(h, "(0%)") || strings.Contains(h, "NaN") || strings.Contains(h, "Inf") {
		t.Errorf("legacy header %q", h)
	}
}

// gatedBackend blocks every research pass until its context is cancelled.
type gatedBackend struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
}

func (b *gatedBackend) Research(ctx context.Context, _ brief.Task) brief.Result {
	b.mu.Lock()
	b.calls++
	b.mu.Unlock()
	select {
	case b.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return brief.Result{Err: context.Canceled, Tokens: 500}
}

func (b *gatedBackend) Summarize(context.Context, string, string) brief.Result {
	return brief.Result{Markdown: "- x", Tokens: 100}
}

func (b *gatedBackend) Name() string { return "gated" }

// drain feeds the model's wait commands until the run ends.
func drain(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for cmd != nil {
		msgs := make(chan tea.Msg, 1)
		go func(c tea.Cmd) { msgs <- c() }(cmd)
		select {
		case msg := <-msgs:
			if _, ok := msg.(doneMsg); ok {
				m.Update(msg)
				return
			}
			_, cmd = m.Update(msg)
		case <-deadline:
			t.Fatal("run did not end after quitting")
		}
	}
}

func TestRegenerateAndQuitDuringRun(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := config.Default()
	cfg.Provider.Type = "anthropic"
	cfg.TokenBudget = 400_000
	cfg.Concurrency = 2
	b := &gatedBackend{started: make(chan struct{}, 1)}
	m := New(brief.NewGenerator(cfg, b), nil, nil, "notty")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	wait := m.start()
	<-b.started

	running := m.running
	for i := 0; i < 3; i++ {
		if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}); cmd != nil {
			t.Fatal("regenerate during a run returned a command")
		}
	}
	if m.running != running || !m.generating {
		t.Fatal("regenerate during a run replaced the running journal")
	}
	if v := m.View(); !strings.Contains(v, "q quit (cancels run)") || strings.Contains(v, "r regenerate") {
		t.Errorf("help during a run: %q", v[strings.LastIndex(v, "\n")+1:])
	}

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("quit returned no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("q did not quit")
	}
	drain(t, m, wait)
	if m.generating {
		t.Error("still generating after the run ended")
	}
	b.mu.Lock()
	calls := b.calls
	b.mu.Unlock()
	if calls > cfg.Concurrency {
		t.Errorf("%d passes started, more than were in flight when quitting", calls)
	}
	if m.journal.TokensUsed() > m.journal.TokenBudget {
		t.Errorf("over budget after cancel")
	}

	// After the cancelled run, regenerate is allowed again.
	b2 := &gatedBackend{started: make(chan struct{}, 1)}
	m.gen = brief.NewGenerator(cfg, b2)
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil || !m.generating {
		t.Fatal("regenerate after the run did not start a new one")
	}
	<-b2.started
	m.cancel()
	drain(t, m, m.wait())
}

func TestOverviewShowsFullWarnings(t *testing.T) {
	j := &brief.Journal{
		Date: "2026-10-29", TokenBudget: 200_000, BudgetLevel: brief.LevelCostly,
		BudgetWarnings: []string{"estimated cost is about $9.00 per run"},
		Overview:       brief.Section{Topic: "Overview", Status: brief.StatusDone, Markdown: "## Today's highlights\n\n- x"},
		Sections:       []*brief.Section{{Topic: "A", Status: brief.StatusDone, Markdown: "### A"}},
	}
	m := New(nil, nil, j, "notty")
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 20})
	v := m.View()
	if !strings.Contains(v, "estimated cost is about $9.00 per run") || strings.Contains(v, "budget high") {
		t.Errorf("overview pane lacks the warning text or mislabels cost:\n%s", v)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if strings.Contains(m.View(), "$9.00") {
		t.Error("warning shown on a topic pane")
	}
}

func TestRegenerateWithoutGeneratorOrError(t *testing.T) {
	j := &brief.Journal{Date: "2026-10-29", Sections: []*brief.Section{{Topic: "A", Status: brief.StatusDone}}}
	m := New(nil, nil, j, "notty")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}); cmd != nil || !strings.Contains(m.status, "Cannot regenerate") {
		t.Errorf("r with no generator: status %q", m.status)
	}
}

// splitBackend finishes topic A and blocks the rest until cancelled.
type splitBackend struct{ gatedBackend }

func (b *splitBackend) Research(ctx context.Context, task brief.Task) brief.Result {
	if task.Topic.Name == "A" && task.Prior == "" {
		return brief.Result{Markdown: "### Done A\nx", Tokens: 1_000, Searches: 1}
	}
	return b.gatedBackend.Research(ctx, task)
}

func TestQuitDuringRunSavesFinishedTopics(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := config.Default()
	cfg.Provider.Type = "anthropic"
	cfg.TokenBudget = 400_000
	cfg.Concurrency = 1
	cfg.Topics = []config.Topic{{Name: "A"}, {Name: "B"}}
	b := &splitBackend{gatedBackend{started: make(chan struct{}, 1)}}
	m := New(brief.NewGenerator(cfg, b), nil, nil, "notty")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	wait := m.start()
	<-b.started
	// Feed updates until A shows as done in the display copy.
	for m.journal.Sections[0].Status != brief.StatusDone {
		_, wait = m.Update(wait())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	saved, err := brief.LoadJournal(m.journal.Date)
	if err != nil || saved == nil {
		t.Fatalf("no journal saved on quit: %v", err)
	}
	if a, bs := saved.Sections[0], saved.Sections[1]; a.Status != brief.StatusDone || a.Markdown != "### Done A\nx" || bs.Status != brief.StatusFailed {
		t.Errorf("saved A %s %q, B %s", a.Status, a.Markdown, bs.Status)
	}
	drain(t, m, wait)
}
