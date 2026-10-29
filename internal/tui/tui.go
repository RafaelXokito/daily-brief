// Package tui renders the daily journal with Bubble Tea.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/rafaelpereira/daily-brief/internal/brief"
)

const sidebarWidth = 28

var (
	accent      = lipgloss.AdaptiveColor{Light: "#5A4FCF", Dark: "#A99BFF"}
	muted       = lipgloss.AdaptiveColor{Light: "#7A7A7A", Dark: "#8A8A8A"}
	okColor     = lipgloss.AdaptiveColor{Light: "#1F8A4C", Dark: "#5FD38D"}
	errColor    = lipgloss.AdaptiveColor{Light: "#C0392B", Dark: "#FF7B72"}
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(accent)
	mutedStyle  = lipgloss.NewStyle().Foreground(muted)
	selStyle    = lipgloss.NewStyle().Bold(true).Foreground(accent)
	sidebarBox  = lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderRight(true).BorderForeground(muted).PaddingRight(1)
	helpStyle   = mutedStyle
	statusStyle = lipgloss.NewStyle().Foreground(muted).Italic(true)
)

type updateMsg brief.Update

type doneMsg struct{ err error }

type Model struct {
	gen        *brief.Generator
	genErr     error          // why gen is nil, if it is
	journal    *brief.Journal // display copy, updated from generator events
	running    *brief.Journal // the generator's copy while a run is active
	updates    chan brief.Update
	cancel     context.CancelFunc
	generating bool
	status     string

	selected  int // 0 = overview, i+1 = Sections[i]
	viewport  viewport.Model
	spinner   spinner.Model
	glamStyle string
	width     int
	height    int
	ready     bool
}

// New shows journal if it is non-nil, otherwise generates a fresh one.
// gen may be nil when no connection could be set up (genErr says why); the
// saved journal is then shown read-only.
func New(gen *brief.Generator, genErr error, journal *brief.Journal, glamStyle string) *Model {
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	sp.Style = lipgloss.NewStyle().Foreground(accent)
	return &Model{gen: gen, genErr: genErr, journal: journal, spinner: sp, glamStyle: glamStyle}
}

func (m *Model) Init() tea.Cmd {
	if m.journal == nil {
		return tea.Batch(m.spinner.Tick, m.start())
	}
	return m.spinner.Tick
}

func (m *Model) start() tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.running = m.gen.NewJournal()
	m.journal = cloneJournal(m.running)
	m.updates = make(chan brief.Update, 16)
	m.generating = true
	m.status = "Researching today's news…"
	go m.gen.Run(ctx, m.running, m.updates)
	m.refresh()
	return m.wait()
}

func (m *Model) wait() tea.Cmd {
	ch := m.updates
	return func() tea.Msg {
		u, ok := <-ch
		if !ok {
			return doneMsg{}
		}
		return updateMsg(u)
	}
}

func cloneJournal(j *brief.Journal) *brief.Journal {
	c := *j
	c.Sections = make([]*brief.Section, len(j.Sections))
	for i, s := range j.Sections {
		cp := *s
		c.Sections[i] = &cp
	}
	return &c
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		w, h := m.contentSize()
		if !m.ready {
			m.viewport = viewport.New(w, h)
			m.ready = true
		} else {
			m.viewport.Width, m.viewport.Height = w, h
		}
		m.refresh()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			if m.cancel != nil {
				m.cancel()
			}
			if m.generating {
				m.savePartial()
			}
			return m, tea.Quit
		case "up", "k", "shift+tab":
			m.selectSection(m.selected - 1)
			return m, nil
		case "down", "j", "tab":
			m.selectSection(m.selected + 1)
			return m, nil
		case "r":
			if m.gen == nil {
				m.status = "Cannot regenerate: no connection to Claude"
				if m.genErr != nil {
					m.status = "Cannot regenerate: " + m.genErr.Error()
				}
				return m, nil
			}
			if !m.generating {
				m.selected = 0
				return m, m.start()
			}
			return m, nil
		case "g", "home":
			m.viewport.GotoTop()
			return m, nil
		case "G", "end":
			m.viewport.GotoBottom()
			return m, nil
		}

	case updateMsg:
		if msg.Index < 0 {
			m.journal.Overview = msg.Section
		} else {
			s := msg.Section
			m.journal.Sections[msg.Index] = &s
		}
		if m.selected == msg.Index+1 {
			m.refresh()
		}
		return m, m.wait()

	case doneMsg:
		m.generating = false
		// Saving fills failed topics from the journal saved earlier today.
		switch err := brief.SaveJournal(m.running); {
		case errors.Is(err, brief.ErrNothingDone):
			m.status = "Nothing was summarized; journal not saved"
		case err != nil:
			m.status = "Could not save journal: " + err.Error()
		default:
			m.status = ""
		}
		m.journal = cloneJournal(m.running)
		m.refresh()
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// savePartial saves the topics a run being quit has finished, if any.
// A top-up pass in flight keeps the digest it started from.
func (m *Model) savePartial() {
	j := cloneJournal(m.journal)
	for _, s := range append([]*brief.Section{&j.Overview}, j.Sections...) {
		switch {
		case s.Status == brief.StatusRunning && s.Markdown != "":
			s.Status = brief.StatusDone
		case s.Status == brief.StatusRunning || s.Status == brief.StatusPending:
			s.Status, s.Error = brief.StatusFailed, "cancelled"
		}
	}
	j.GeneratedAt = time.Now()
	_ = brief.SaveJournal(j)
}

func (m *Model) selectSection(i int) {
	n := len(m.journal.Sections) + 1
	m.selected = (i%n + n) % n
	m.refresh()
	m.viewport.GotoTop()
}

func (m *Model) current() *brief.Section {
	if m.selected == 0 {
		return &m.journal.Overview
	}
	return m.journal.Sections[m.selected-1]
}

func (m *Model) contentSize() (int, int) {
	return max(20, m.width-sidebarWidth-3), max(3, m.height-4)
}

func (m *Model) refresh() {
	if !m.ready || m.journal == nil {
		return
	}
	s := m.current()
	var md string
	switch {
	case s.Status == brief.StatusPending:
		md = "_Waiting to start…_"
	case s.Status == brief.StatusRunning && s.Markdown == "":
		md = "_Searching the web and summarizing…_"
	case s.Status == brief.StatusRunning:
		md = "## " + s.Topic + "\n\n_Looking for more…_\n\n" + s.Render()
	case m.selected == 0 && s.Status == brief.StatusDone:
		md = s.Render()
	default:
		md = "## " + s.Topic + "\n\n" + s.Render()
	}
	md = markUpdates(md)
	warnings := m.journal.BudgetWarnings
	if note := m.journal.OvershootNote(); note != "" {
		warnings = append([]string{note}, warnings...)
	}
	if m.selected == 0 && len(warnings) > 0 {
		var w strings.Builder
		for _, msg := range warnings {
			w.WriteString("> **Warning:** " + msg + "\n>\n")
		}
		md = strings.TrimSuffix(w.String(), ">\n") + "\n" + md
	}
	r, err := glamour.NewTermRenderer(glamour.WithStandardStyle(m.glamStyle), glamour.WithWordWrap(m.viewport.Width-2))
	if err == nil {
		if out, err := r.Render(md); err == nil {
			md = out
		}
	}
	m.viewport.SetContent(md)
}

func (m *Model) View() string {
	if !m.ready || m.journal == nil {
		return ""
	}
	j := m.journal
	header := titleStyle.Render("Daily Brief") + "  " + mutedStyle.Render(j.Date) +
		"  " + mutedStyle.Render("· "+j.Provider) +
		"  " + mutedStyle.Render(fmt.Sprintf("· %s · ~%.0f min read", j.SpendSummary(), max(1, j.ReadMinutes())))
	switch j.BudgetLevel {
	case "", brief.LevelOK:
	case brief.LevelCostly:
		header += "  " + lipgloss.NewStyle().Foreground(errColor).Render("⚠ cost")
	default:
		header += "  " + lipgloss.NewStyle().Foreground(errColor).Render("⚠ budget "+string(j.BudgetLevel))
	}
	if over := j.Over(); over > 0 && j.TokenBudget > 0 {
		header += "  " + lipgloss.NewStyle().Foreground(errColor).Render("⚠ over budget by "+brief.HumanTokens(over))
	}
	if m.generating {
		header += "  " + m.spinner.View() + " " + statusStyle.Render(m.status)
	} else if m.status != "" {
		header += "  " + lipgloss.NewStyle().Foreground(errColor).Render(m.status)
	}

	body := lipgloss.JoinHorizontal(lipgloss.Top,
		sidebarBox.Height(m.viewport.Height).Width(sidebarWidth).Render(m.sidebar()),
		" ",
		m.viewport.View(),
	)
	help := "↑/↓ topic · pgup/pgdn scroll · r regenerate · q quit"
	if m.generating {
		help = "↑/↓ topic · pgup/pgdn scroll · q quit (cancels run)"
	}
	return header + "\n\n" + body + "\n" + helpStyle.Render(help)
}

func (m *Model) sidebar() string {
	var b strings.Builder
	entries := append([]*brief.Section{&m.journal.Overview}, m.journal.Sections...)
	for i, s := range entries {
		icon := mutedStyle.Render("·")
		switch s.Status {
		case brief.StatusRunning:
			icon = m.spinner.View()
		case brief.StatusDone:
			icon = lipgloss.NewStyle().Foreground(okColor).Render("✓")
		case brief.StatusFailed:
			icon = lipgloss.NewStyle().Foreground(errColor).Render("✗")
		}
		var counts []string
		if n := s.Updates(); n > 0 {
			counts = append(counts, fmt.Sprintf("↻%d", n))
		}
		if s.TokensUsed > 0 {
			counts = append(counts, brief.HumanTokens(s.TokensUsed))
		}
		count := strings.Join(counts, " ")
		// The row is "▸ ✓ name  ↻N 12.3k" within the box's sidebarWidth-1
		// cells, with at least one space before the counts.
		room := sidebarWidth - 1 - 2 - lipgloss.Width(icon) - 1 - 1 - lipgloss.Width(count)
		name := truncate(s.Topic, room)
		line := "  " + icon + " " + name
		if i == m.selected {
			line = selStyle.Render("▸ ") + icon + " " + selStyle.Render(name)
		}
		pad := max(1, sidebarWidth-1-lipgloss.Width(line)-lipgloss.Width(count))
		b.WriteString(line + strings.Repeat(" ", pad) + mutedStyle.Render(count) + "\n")
	}
	return b.String()
}

// markUpdates shows items written as updates to news an earlier day
// reported as "↻ Update · …": the lines brief.Section.Updates counts.
func markUpdates(md string) string {
	lines := strings.Split(md, "\n")
	for i, l := range lines {
		if lead, headline, ok := brief.SplitUpdate(l); ok {
			lines[i] = lead + "↻ Update · " + headline
		}
	}
	return strings.Join(lines, "\n")
}

// truncate cuts s to at most n cells, marking a cut with "…".
func truncate(s string, n int) string {
	return ansi.Truncate(s, max(0, n), "…")
}
