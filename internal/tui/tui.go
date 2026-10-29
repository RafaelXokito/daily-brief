// Package tui renders the daily journal with Bubble Tea.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/rafaelpereira/daily-brief/internal/brief"
)

const sidebarWidth = 28

// Below the full layout's minimum size the TUI shows only the summary;
// below the compact one, only a note that the terminal is too small.
const (
	minWidth, minHeight               = 50, 10
	compactMinWidth, compactMinHeight = 20, 4
)

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

// pane is the pane that the arrow keys act on.
type pane int

const (
	focusSummary pane = iota
	focusTopics
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

	selected   int // 0 = overview, i+1 = Sections[i]
	focus      pane
	sidebarTop int  // the first topic row shown when they don't all fit
	showHelp   bool // the key list replaces the viewport
	viewport   viewport.Model
	keysView   viewport.Model // the key list, scrolled on its own
	spinner    spinner.Model
	glamStyle  string
	width      int
	height     int
	ready      bool
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
			m.keysView = viewport.New(w, h)
			m.ready = true
		} else {
			m.viewport.Width, m.viewport.Height = w, h
		}
		m.refresh()
		m.refreshKeys()
		return m, nil

	case tea.KeyMsg:
		// Keys never reach the viewport, so ← and → can't scroll it sideways.
		return m.handleKey(msg)

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

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, keys.Quit) {
		if m.cancel != nil {
			m.cancel()
		}
		if m.generating {
			m.savePartial()
		}
		return m, tea.Quit
	}
	if m.showHelp {
		// Only the keys that scroll or close the list do anything.
		switch {
		case key.Matches(msg, keys.Help, keys.Esc):
			m.showHelp = false
		case key.Matches(msg, keys.Up):
			m.keysView.ScrollUp(1)
		case key.Matches(msg, keys.Down):
			m.keysView.ScrollDown(1)
		case key.Matches(msg, keys.PageDown):
			m.keysView.PageDown()
		case key.Matches(msg, keys.PageUp):
			m.keysView.PageUp()
		case key.Matches(msg, keys.Top):
			m.keysView.GotoTop()
		case key.Matches(msg, keys.Bottom):
			m.keysView.GotoBottom()
		}
		return m, nil
	}
	switch {
	case key.Matches(msg, keys.Help):
		m.showHelp = true
		m.keysView.GotoTop()
	case key.Matches(msg, keys.Topics, keys.Esc):
		m.focus = focusTopics
	case key.Matches(msg, keys.Summary):
		m.focus = focusSummary
	case key.Matches(msg, keys.Toggle):
		m.focus = 1 - m.focus
	case key.Matches(msg, keys.Up):
		if m.focus == focusTopics {
			m.selectSection(m.selected - 1)
		} else {
			m.viewport.ScrollUp(1)
		}
	case key.Matches(msg, keys.Down):
		if m.focus == focusTopics {
			m.selectSection(m.selected + 1)
		} else {
			m.viewport.ScrollDown(1)
		}
	case key.Matches(msg, keys.PageDown):
		m.viewport.PageDown()
	case key.Matches(msg, keys.PageUp):
		m.viewport.PageUp()
	case key.Matches(msg, keys.HalfDown):
		m.viewport.HalfPageDown()
	case key.Matches(msg, keys.HalfUp):
		m.viewport.HalfPageUp()
	case key.Matches(msg, keys.Top):
		m.viewport.GotoTop()
	case key.Matches(msg, keys.Bottom):
		m.viewport.GotoBottom()
	case key.Matches(msg, keys.Jump):
		if i := int(msg.String()[0] - '1'); i <= len(m.journal.Sections) {
			m.selectSection(i)
		}
	case key.Matches(msg, keys.Regenerate):
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
	}
	return m, nil
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

// layout is how much of the TUI the terminal has room for.
type layout int

const (
	layoutFull    layout = iota // header, pane titles, sidebar, summary, help line
	layoutCompact               // one-line header, summary, help line
	layoutTooSmall
)

func (m *Model) layout() layout {
	switch {
	case m.width >= minWidth && m.height >= minHeight:
		return layoutFull
	case m.width >= compactMinWidth && m.height >= compactMinHeight:
		return layoutCompact
	}
	return layoutTooSmall
}

// contentSize is the size of the summary pane. The sidebar box is
// sidebarWidth cells and its border, then a one-cell gap.
func (m *Model) contentSize() (int, int) {
	switch m.layout() {
	case layoutFull:
		return m.width - sidebarWidth - 2, m.height - 5
	case layoutCompact:
		return m.width, m.height - 2
	}
	return compactMinWidth, compactMinHeight - 2
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
	switch m.layout() {
	case layoutCompact:
		return m.compactView()
	case layoutTooSmall:
		if m.height < 1 {
			return ""
		}
		return truncate(fmt.Sprintf("terminal too small (need %d×%d)", compactMinWidth, compactMinHeight), m.width)
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

	box, content := sidebarBox, m.viewport.View()
	if m.focus == focusTopics {
		box = box.BorderForeground(accent)
	}
	if m.showHelp {
		content = m.keysPane()
	}
	titles := lipgloss.NewStyle().Width(sidebarWidth+2).Render(paneTitle("Topics", m.focus == focusTopics)) +
		paneTitle(truncate(m.paneName(), m.viewport.Width), m.focus == focusSummary || m.showHelp)
	body := lipgloss.JoinHorizontal(lipgloss.Top,
		box.Height(m.viewport.Height).Width(sidebarWidth).Render(m.sidebarView(m.viewport.Height)),
		" ",
		content,
	)
	return truncate(header, m.width) + "\n\n" + titles + "\n" + body + "\n" + m.helpText()
}

// compactView is the View for a terminal too small for the sidebar: a
// one-line header naming the pane, the summary or key list, the help line.
func (m *Model) compactView() string {
	header := titleStyle.Render("Daily Brief") + "  " + mutedStyle.Render(m.paneName())
	if m.generating {
		header += "  " + m.spinner.View()
	}
	content := m.viewport.View()
	if m.showHelp {
		content = m.keysPane()
	}
	return truncate(header, m.width) + "\n" + content + "\n" + m.helpText()
}

// paneName is the title of the summary pane.
func (m *Model) paneName() string {
	if m.showHelp {
		return "Keys"
	}
	if title := m.current().Topic; m.selected != 0 || title != "" {
		return title
	}
	return "Overview"
}

// helpText is the styled help line for the open pane or key list.
func (m *Model) helpText() string {
	if m.showHelp {
		return helpStyle.Render(overlayHelpLine(m.width))
	}
	return helpStyle.Render(helpLine(m.focus, m.generating, m.width))
}

// paneTitle is a pane's title, in the accent colour when it has focus.
func paneTitle(s string, focused bool) string {
	if focused {
		return titleStyle.Render(s)
	}
	return mutedStyle.Render(s)
}

// keyLines lists every key in width cells: each description beside the
// key column if they all fit, otherwise on the line under its key.
func keyLines(width int) []string {
	all := keys.all()
	col, desc := 0, 0
	for _, b := range all {
		col = max(col, lipgloss.Width(keyNames(b)))
		desc = max(desc, lipgloss.Width(b.Help().Desc))
	}
	var lines []string
	if col+2+desc <= width {
		for _, b := range all {
			name := keyNames(b)
			lines = append(lines, titleStyle.Render(name)+strings.Repeat(" ", col-lipgloss.Width(name)+2)+b.Help().Desc)
		}
		return lines
	}
	indent := 2
	if desc > width-indent {
		indent = 0
	}
	for _, b := range all {
		for _, l := range strings.Split(ansi.Wrap(keyNames(b), width, ""), "\n") {
			lines = append(lines, titleStyle.Render(l))
		}
		for _, l := range strings.Split(ansi.Wrap(b.Help().Desc, width-indent, ""), "\n") {
			lines = append(lines, strings.Repeat(" ", indent)+l)
		}
	}
	return lines
}

// refreshKeys fits the key list to the summary pane, keeping a line for
// the "more" hint if it doesn't all fit.
func (m *Model) refreshKeys() {
	w, h := m.viewport.Width, m.viewport.Height
	lines := keyLines(w)
	m.keysView.Width, m.keysView.Height = w, h
	if len(lines) > h {
		m.keysView.Height = max(1, h-1)
	}
	m.keysView.SetContent(strings.Join(lines, "\n"))
	m.keysView.SetYOffset(m.keysView.YOffset)
}

// keysPane is the key list in place of the viewport, with a hint when
// keys are hidden above or below.
func (m *Model) keysPane() string {
	v := m.keysView.View()
	if m.keysView.Height == m.viewport.Height {
		return v
	}
	var hint string
	switch top, bottom := m.keysView.AtTop(), m.keysView.AtBottom(); {
	case top && !bottom:
		hint = "↓ more"
	case bottom && !top:
		hint = "↑ more"
	case !top && !bottom:
		hint = "↑↓ more"
	}
	return v + "\n" + mutedStyle.Render(truncate(hint, m.viewport.Width))
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
		switch {
		case i == m.selected && m.focus == focusTopics:
			line = selStyle.Render("▸ ") + icon + " " + selStyle.Render(name)
		case i == m.selected:
			line = mutedStyle.Render("▸ ") + icon + " " + lipgloss.NewStyle().Bold(true).Render(name)
		}
		pad := max(1, sidebarWidth-1-lipgloss.Width(line)-lipgloss.Width(count))
		b.WriteString(line + strings.Repeat(" ", pad) + mutedStyle.Render(count) + "\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// sidebarView is the sidebar cut to h lines, scrolled so that the
// selected topic shows, with a marker for the rows hidden on each side.
func (m *Model) sidebarView(h int) string {
	rows := strings.Split(m.sidebar(), "\n")
	n := len(rows)
	if n <= h {
		m.sidebarTop = 0
		return strings.Join(rows, "\n")
	}
	// The window holds h rows less a line for each marker; the top row
	// never goes past what fills the window with only the ↑ marker.
	top := min(max(0, m.sidebarTop), n-(h-1))
	var slots int
	for {
		slots = h
		if top > 0 {
			slots--
		}
		if top+slots < n {
			slots--
		}
		if m.selected < top {
			top = m.selected
		} else if m.selected >= top+max(1, slots) {
			top++
		} else {
			break
		}
	}
	m.sidebarTop = top
	var out []string
	if top > 0 {
		out = append(out, mutedStyle.Render(fmt.Sprintf("  ↑ %d more", top)))
	}
	out = append(out, rows[top:top+slots]...)
	if below := n - top - slots; below > 0 {
		out = append(out, mutedStyle.Render(fmt.Sprintf("  ↓ %d more", below)))
	}
	return strings.Join(out, "\n")
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
