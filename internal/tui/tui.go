// Package tui renders the daily journal with Bubble Tea.
package tui

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"

	"github.com/RafaelXokito/daily-brief/internal/brief"
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
	actStyle    = lipgloss.NewStyle().Bold(true).Foreground(errColor)
	warnStyle   = lipgloss.NewStyle().Foreground(errColor)
	sidebarBox  = lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderRight(true).BorderForeground(muted).PaddingRight(1)
	helpStyle   = mutedStyle
	statusStyle = lipgloss.NewStyle().Foreground(muted).Italic(true)
)

// Impact tag badges: ACT urgent, WATCH amber, FYI muted.
var (
	watchColor = lipgloss.AdaptiveColor{Light: "#9A6700", Dark: "#E3B341"}
	badgeText  = lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#1A1A1A"}
	actBadge   = lipgloss.NewStyle().Bold(true).Foreground(badgeText).Background(errColor)
	watchBadge = lipgloss.NewStyle().Bold(true).Foreground(badgeText).Background(watchColor)
	fyiBadge   = lipgloss.NewStyle().Foreground(muted)
)

// pane is the pane that the arrow keys act on.
type pane int

const (
	focusSummary pane = iota
	focusTopics
)

type updateMsg brief.Update

type doneMsg struct{ err error }

// clearFlashMsg ends the flash message with this id.
type clearFlashMsg int

// flashTime is how long a flash message replaces the help line.
const flashTime = 2 * time.Second

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
	reader     bool // reader mode: no sidebar, the summary at full width
	viewport   viewport.Model
	keysView   viewport.Model // the key list, scrolled on its own
	spinner    spinner.Model
	theme      string
	width      int
	height     int
	ready      bool

	content string // the viewport's rendered lines
	// cursor is the selected highlight on the Overview, in display order;
	// hlLines is the first line of each highlight in content.
	cursor  int
	hlLines []int
	// headings are where a topic's items and other headings start in
	// content; item is the item Enter jumped to, or -1, while the viewport
	// stays at itemAt.
	headings     []heading
	item, itemAt int
	flash        string // replaces the help line for a moment
	flashID      int

	// open opens a URL in the browser, copy puts text on the clipboard and
	// editor runs $EDITOR on a file; tests replace them.
	open   func(url string) error
	copy   func(text string) error
	editor func(path string, done func(error) tea.Msg) tea.Cmd

	settings Settings
	set      *settingsPane // the open Settings pane, or nil
	// pending is a generator rebuilt for saved settings while a run was
	// using gen; it replaces gen when the run ends. rebuilds counts the
	// rebuilds started, so a late one never replaces a newer one.
	pending  *brief.Generator
	rebuilds int
	building *rebuildJob // the latest rebuild, until Update has its result
}

// New shows journal if it is non-nil, otherwise generates a fresh one.
// gen may be nil when no connection could be set up (genErr says why); the
// saved journal is then shown read-only. theme is "dark" or "light" (any
// other name renders without colour).
func New(gen *brief.Generator, genErr error, journal *brief.Journal, theme string) *Model {
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	sp.Style = lipgloss.NewStyle().Foreground(accent)
	return &Model{gen: gen, genErr: genErr, journal: journal, spinner: sp, theme: theme, item: -1, open: openURL, copy: copyText, editor: openEditor}
}

// openURL opens url with the system's opener, if it is an http(s) URL
// (see brief.SafeWebURL); nothing else is handed to it.
func openURL(raw string) error {
	u, ok := brief.SafeWebURL(raw)
	if !ok {
		return errors.New("not a web link")
	}
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	cmd := exec.Command(name, u)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

// Output is the terminal the TUI draws on, stdout, for tea.WithOutput:
// its writes are serialised, and Bubble Tea writes each frame at once, so
// the OSC 52 sequence copyText sends never lands inside a frame. (Bubble
// Tea v1 has no command to write a raw sequence, and tea.Printf prints
// nothing in the alternate screen.)
var Output = &syncFile{File: os.Stdout}

type syncFile struct {
	mu sync.Mutex
	*os.File
}

func (f *syncFile) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.File.Write(p)
}

// maxOSC52 is the most base64 copyText sends with OSC 52; terminals cap
// what they accept at about this.
const maxOSC52 = 75_000

// copyText puts text on the clipboard: with pbcopy on a Mac outside SSH,
// else with OSC 52 through Output, which works over SSH where the
// terminal allows it.
func copyText(text string) error {
	if runtime.GOOS == "darwin" && os.Getenv("SSH_TTY") == "" {
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	}
	enc := base64.StdEncoding.EncodeToString([]byte(text))
	switch {
	case len(enc) > maxOSC52:
		return errors.New("too long for the terminal's clipboard")
	case !term.IsTerminal(int(Output.Fd())):
		return errors.New("no terminal to copy through")
	}
	_, err := fmt.Fprintf(Output, "\x1b]52;c;%s\a", enc)
	return err
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
		m.resize()
		return m, nil

	case clearFlashMsg:
		if int(msg) == m.flashID {
			m.flash = ""
		}
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
		if m.pending != nil {
			// Saved settings take effect now that no run uses gen.
			m.gen, m.genErr, m.pending = m.pending, nil, nil
		}
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

	case rebuiltMsg:
		return m, m.rebuilt(msg)

	case editorDoneMsg:
		return m, m.editorDone(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// quit cancels a run, saving what it has done, and quits.
func (m *Model) quit() tea.Cmd {
	if m.cancel != nil {
		m.cancel()
	}
	if m.generating {
		m.savePartial()
	}
	return tea.Quit
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.set != nil {
		return m.settingsKey(msg)
	}
	if key.Matches(msg, keys.Quit) {
		return m, m.quit()
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
	list := m.focus == focusSummary && m.onHighlights()
	switch {
	case key.Matches(msg, keys.Help):
		m.showHelp = true
		m.keysView.GotoTop()
	case key.Matches(msg, keys.Settings):
		return m, m.openSettings()
	case key.Matches(msg, keys.Topics, keys.Esc, keys.Toggle) && m.reader:
		// The Topics pane is hidden in reader mode: show it again.
		m.reader = false
		m.focus = focusTopics
		m.resize()
	case key.Matches(msg, keys.Topics, keys.Esc):
		m.focus = focusTopics
	case key.Matches(msg, keys.Summary):
		m.focus = focusSummary
	case key.Matches(msg, keys.Go):
		if list {
			m.jump()
		}
		m.focus = focusSummary
	case key.Matches(msg, keys.Toggle):
		m.focus = 1 - m.focus
	case key.Matches(msg, keys.Up):
		switch {
		case m.focus == focusTopics:
			m.selectSection(m.selected - 1)
		case list:
			m.moveCursor(-1)
		default:
			m.viewport.ScrollUp(1)
		}
	case key.Matches(msg, keys.Down):
		switch {
		case m.focus == focusTopics:
			m.selectSection(m.selected + 1)
		case list:
			m.moveCursor(1)
		default:
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
	case key.Matches(msg, keys.Back):
		if m.selected != 0 {
			m.selectSection(0)
			m.focus = focusSummary
			m.showCursor()
		}
	case key.Matches(msg, keys.Open):
		it, ok := m.currentItem()
		if !ok || it.URL == "" {
			return m, m.flashMsg("No link to open here")
		}
		u, ok := brief.SafeWebURL(it.URL)
		switch {
		case !ok:
			return m, m.flashMsg("No web link")
		case m.open(u) != nil:
			return m, m.flashMsg("Could not open ↗ " + brief.Domain(u))
		}
		return m, m.flashMsg("Opened ↗ " + brief.Domain(u))
	case key.Matches(msg, keys.Copy):
		it, ok := m.currentItem()
		if !ok {
			return m, m.flashMsg("No item to copy here")
		}
		text := brief.StripControl(brief.PlainText(it.Markdown))
		if it.URL != "" {
			text += "\n" + it.URL
		}
		if err := m.copy(text); err != nil {
			return m, m.flashMsg("Could not copy: " + err.Error())
		}
		return m, m.flashMsg("Copied")
	case key.Matches(msg, keys.Reader):
		m.reader = !m.reader
		m.focus = focusSummary
		m.resize()
	case key.Matches(msg, keys.Regenerate):
		var note tea.Cmd
		if m.building != nil && !m.generating {
			// Saved settings apply to this run, even before their rebuild
			// has been reported.
			note = m.rebuilt(m.building.get())
		}
		if m.gen == nil {
			m.status = "Cannot regenerate: no connection to Claude"
			if m.genErr != nil {
				m.status = "Cannot regenerate: " + m.genErr.Error()
			}
			return m, note
		}
		if !m.generating {
			m.selected = 0
			return m, tea.Batch(note, m.start())
		}
	}
	return m, nil
}

// flashMsg shows s in place of the help line for a moment.
func (m *Model) flashMsg(s string) tea.Cmd {
	m.flash = brief.StripControl(s)
	m.flashID++
	id := m.flashID
	return tea.Tick(flashTime, func(time.Time) tea.Msg { return clearFlashMsg(id) })
}

// resize fits the viewport and the key list to the terminal and layout.
func (m *Model) resize() {
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
	if line := m.itemLine(m.item); line >= 0 {
		m.viewport.SetYOffset(line)
		m.itemAt = m.viewport.YOffset
	}
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
	m.item = -1
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
		if m.reader {
			return m.width, m.height - 5
		}
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
	m.headings = nil
	if m.selected == 0 && m.hasHighlights() {
		m.cursor = min(max(0, m.cursor), len(m.journal.Overview.Highlights)-1)
		m.content, m.hlLines = m.overviewList(m.viewport.Width)
		m.viewport.SetContent(m.content)
		m.showCursor()
		return
	}
	md, heads, tags := m.markdown()
	m.hlLines = nil
	lines := make([]int, len(heads))
	for i, h := range heads {
		lines[i] = h.line
	}
	var starts []int
	m.content, starts = renderItems(md, m.viewport.Width, m.theme, lines, tags)
	if len(starts) == len(heads) {
		for i := range heads {
			heads[i].line = starts[i]
		}
		m.headings = heads
	}
	m.viewport.SetContent(m.content)
}

// heading is the start of an item of a topic's digest, or of another
// heading or bold-led line in it.
type heading struct {
	line     int    // in the Markdown shown, then in content
	item     int    // its index in Items, or -1
	headline string // for another heading
}

// itemLine is the first line of the kth item in content, or -1.
func (m *Model) itemLine(k int) int {
	for _, h := range m.headings {
		if h.item == k && k >= 0 {
			return h.line
		}
	}
	return -1
}

// markdown is what the summary pane shows of the selected section, the
// lines of it that start a topic's items and other headings, and those
// that hold its items' tags (see brief.TagLines).
func (m *Model) markdown() (string, []heading, map[int]bool) {
	s := m.current()
	var md, head string
	switch {
	case s.Status == brief.StatusPending:
		md = "_Waiting to start…_"
	case s.Status == brief.StatusRunning && s.Markdown == "":
		md = "_Searching the web and summarizing…_"
	case m.selected == 0 && s.Status == brief.StatusDone:
		md = s.Render()
	case s.Status == brief.StatusRunning:
		head = "## " + s.Topic + "\n\n_Looking for more…_\n\n"
	default:
		head = "## " + s.Topic + "\n\n"
	}
	var heads []heading
	tags := map[int]bool{}
	if md == "" {
		md = head + s.Render()
		off, items := strings.Count(head, "\n"), s.ItemLines()
		for l, h := range brief.TagLines(s.Markdown) {
			tags[off+l] = h
		}
		for i, l := range strings.Split(s.Markdown, "\n") {
			t := strings.TrimSpace(l)
			switch k := slices.Index(items, i); {
			case k >= 0:
				heads = append(heads, heading{line: off + i, item: k})
			case (strings.HasPrefix(t, "#") || strings.HasPrefix(t, "**")) && !brief.IsRelated(t):
				heads = append(heads, heading{line: off + i, item: -1, headline: brief.Headline(t)})
			}
		}
	}
	if m.selected == 0 {
		if w := m.warnings(); len(w) > 0 {
			var b strings.Builder
			for _, msg := range w {
				b.WriteString("> **Warning:** " + msg + "\n>\n")
			}
			md = strings.TrimSuffix(b.String(), ">\n") + "\n" + md
		}
	}
	return md, heads, tags
}

// warnings are the run's overshoot and budget warnings, shown atop the
// Overview.
func (m *Model) warnings() []string {
	w := m.journal.BudgetWarnings
	if note := m.journal.OvershootNote(); note != "" {
		w = append([]string{note}, w...)
	}
	return w
}

// hasHighlights reports whether the Overview is shown as its list of
// highlights rather than as Markdown.
func (m *Model) hasHighlights() bool {
	ov := &m.journal.Overview
	return ov.Status == brief.StatusDone && len(ov.Highlights) > 0
}

// onHighlights reports whether the summary pane shows the highlights.
func (m *Model) onHighlights() bool {
	return m.selected == 0 && m.hasHighlights() && !m.showHelp && m.set == nil
}

// highlights are the Overview's highlights in display order: "act" first.
func (m *Model) highlights() []brief.Highlight {
	hl := slices.Clone(m.journal.Overview.Highlights)
	slices.SortStableFunc(hl, func(a, b brief.Highlight) int {
		switch {
		case a.Tier == b.Tier:
			return 0
		case a.Tier == brief.TierAct:
			return -1
		case b.Tier == brief.TierAct:
			return 1
		}
		return 0
	})
	return hl
}

// overviewList renders the Overview's highlights in width cells, under
// "Act now" and "Worth knowing", between the warnings and numbers and the
// status of each topic, all computed from the journal as it is now. It
// returns the lines and where each highlight starts.
func (m *Model) overviewList(width int) (string, []int) {
	w := max(1, width-2*docMargin)
	pad := strings.Repeat(" ", docMargin)
	var lines []string
	add := func(style lipgloss.Style, indent, text string) {
		text = brief.StripControl(text)
		for _, l := range strings.Split(ansi.Wrap(text, max(1, w-lipgloss.Width(indent)), ""), "\n") {
			lines = append(lines, pad+indent+style.Render(l))
		}
	}
	lines = append(lines, "")
	for _, msg := range m.warnings() {
		add(warnStyle, "│ ", "Warning: "+msg)
		lines = append(lines, "")
	}
	add(titleStyle, "", "Today's highlights")
	add(mutedStyle, "", brief.NumbersLine(m.journal.Sections, m.journal.ReadMinutes()))
	hl := m.highlights()
	starts := make([]int, len(hl))
	tier := ""
	for i, h := range hl {
		if h.Tier != tier {
			tier = h.Tier
			lines = append(lines, "")
			if tier == brief.TierAct {
				add(actStyle, "", "Act now")
			} else {
				add(titleStyle, "", "Worth knowing")
			}
		}
		lines = append(lines, "")
		starts[i] = len(lines)
		marker, text := "  ", lipgloss.NewStyle()
		if i == m.cursor {
			marker, text = "▸ ", lipgloss.NewStyle().Bold(true)
		}
		first := len(lines)
		add(text, "  ", brief.CleanText(h.Text))
		lines[first] = pad + selStyle.Render(marker) + strings.TrimPrefix(lines[first], pad+"  ")
		if why := brief.CleanText(h.Why); why != "" {
			add(mutedStyle, "  ", why)
		}
		add(lipgloss.NewStyle().Foreground(accent), "  ", h.Topic)
	}
	lines = append(lines, "")
	for _, l := range brief.StatusLines(m.journal.Sections) {
		add(mutedStyle, "", l)
	}
	if ov := m.journal.Overview; ov.Stale {
		lines = append(lines, "")
		add(mutedStyle, "│ ", "Written before some topics were refreshed; it may be out of date.")
	}
	return termSafe(strings.Join(lines, "\n")), starts
}

// moveCursor moves the highlight cursor by d, keeping it in view.
func (m *Model) moveCursor(d int) {
	m.cursor = min(max(0, m.cursor+d), len(m.journal.Overview.Highlights)-1)
	m.refresh()
	m.showCursor()
}

// showCursor scrolls the Overview so the selected highlight shows, or to
// the top if that shows it.
func (m *Model) showCursor() {
	if m.cursor >= len(m.hlLines) {
		return
	}
	start, end := m.hlLines[m.cursor], len(strings.Split(m.content, "\n"))
	if m.cursor+1 < len(m.hlLines) {
		end = m.hlLines[m.cursor+1] - 1
	}
	switch {
	case end <= m.viewport.Height:
		m.viewport.GotoTop()
	case start < m.viewport.YOffset:
		m.viewport.SetYOffset(start)
	case end > m.viewport.YOffset+m.viewport.Height:
		m.viewport.SetYOffset(min(start, end-m.viewport.Height))
	}
}

// jump opens the topic of the selected highlight at its item, or at the
// top if the item is no longer there.
func (m *Model) jump() {
	hl := m.highlights()
	if m.cursor >= len(hl) {
		return
	}
	h := hl[m.cursor]
	i := slices.IndexFunc(m.journal.Sections, func(s *brief.Section) bool { return s.Topic == h.Topic })
	if i < 0 {
		return
	}
	items := m.journal.Sections[i].Items()
	m.selectSection(i + 1)
	if k := brief.ItemIndex(items, h.Headline); k >= 0 {
		if line := m.itemLine(k); line >= 0 {
			m.viewport.SetYOffset(line)
			m.item, m.itemAt = k, m.viewport.YOffset
		}
		return
	}
	// A highlight saved before it had to name an item: another heading
	// with its headline, never a body line.
	var others []brief.Item
	var lines []int
	for _, hd := range m.headings {
		if hd.item < 0 {
			others, lines = append(others, brief.Item{Headline: hd.headline}), append(lines, hd.line)
		}
	}
	if k := brief.ItemIndex(others, h.Headline); k >= 0 {
		m.viewport.SetYOffset(lines[k])
	}
}

// currentItem is the item the summary pane points at: the selected
// highlight's on the Overview, else the one Enter jumped to while the
// viewport has not moved, else the one whose heading is the last at or
// above the top of the viewport (the first if none is).
func (m *Model) currentItem() (brief.Item, bool) {
	if m.onHighlights() {
		h := m.highlights()[m.cursor]
		for _, s := range m.journal.Sections {
			if s.Topic != h.Topic {
				continue
			}
			items := s.Items()
			if k := brief.ItemIndex(items, h.Headline); k >= 0 {
				return items[k], true
			}
		}
		return brief.Item{}, false
	}
	if m.selected == 0 {
		return brief.Item{}, false
	}
	items := m.current().Items()
	if len(items) == 0 {
		return brief.Item{}, false
	}
	if m.item >= 0 && m.item < len(items) && m.viewport.YOffset == m.itemAt {
		return items[m.item], true
	}
	best := 0
	for _, h := range m.headings {
		if h.item >= 0 && h.item < len(items) && h.line <= m.viewport.YOffset {
			best = h.item
		}
	}
	return items[best], true
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
	header := titleStyle.Render("Daily Brief") + "  " + mutedStyle.Render(brief.StripControl(j.Date)) +
		"  " + mutedStyle.Render("· "+brief.StripControl(j.Provider)) +
		"  " + mutedStyle.Render(fmt.Sprintf("· %s · ~%.0f min read", j.SpendSummary(), max(1, j.ReadMinutes())))
	switch j.BudgetLevel {
	case "", brief.LevelOK:
	case brief.LevelCostly:
		header += "  " + lipgloss.NewStyle().Foreground(errColor).Render("⚠ cost")
	default:
		header += "  " + lipgloss.NewStyle().Foreground(errColor).Render("⚠ budget "+brief.StripControl(string(j.BudgetLevel)))
	}
	if over := j.Over(); over > 0 && j.TokenBudget > 0 {
		header += "  " + lipgloss.NewStyle().Foreground(errColor).Render("⚠ over budget by "+brief.HumanTokens(over))
	}
	if m.generating {
		header += "  " + m.spinner.View() + " " + statusStyle.Render(brief.StripControl(m.status))
	} else if m.status != "" {
		header += "  " + lipgloss.NewStyle().Foreground(errColor).Render(brief.StripControl(m.status))
	}

	box, content := sidebarBox, m.viewport.View()
	if m.focus == focusTopics {
		box = box.BorderForeground(accent)
	}
	if m.showHelp {
		content = m.keysPane()
	}
	if m.set != nil {
		content = m.settingsView(m.viewport.Width, m.viewport.Height)
	}
	title := paneTitle(truncate(m.paneName(), m.viewport.Width), m.focus == focusSummary || m.showHelp || m.set != nil)
	if m.reader {
		return truncate(header, m.width) + "\n\n" + title + "\n" + content + "\n" + m.helpText()
	}
	titles := lipgloss.NewStyle().Width(sidebarWidth+2).Render(paneTitle("Topics", m.focus == focusTopics)) + title
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
	if m.set != nil {
		content = m.settingsView(m.viewport.Width, m.viewport.Height)
	}
	return truncate(header, m.width) + "\n" + content + "\n" + m.helpText()
}

// paneName is the title of the summary pane.
func (m *Model) paneName() string {
	if m.showHelp {
		return "Keys"
	}
	if m.set != nil {
		return "Settings"
	}
	if title := brief.StripControl(m.current().Topic); m.selected != 0 || title != "" {
		return title
	}
	return "Overview"
}

// helpText is the styled help line for the open pane or key list, or a
// flash message in its place.
func (m *Model) helpText() string {
	switch {
	case m.flash != "":
		return titleStyle.Render(truncate(m.flash, m.width))
	case m.showHelp:
		return helpStyle.Render(overlayHelpLine(m.width))
	case m.set != nil:
		return helpStyle.Render(m.settingsHelpLine(m.width))
	}
	return helpStyle.Render(helpLine(m.helpMode(), m.generating, m.width))
}

// helpMode is what the help line describes.
func (m *Model) helpMode() helpMode {
	switch {
	case m.focus == focusTopics:
		return modeTopics
	case m.onHighlights():
		return modeHighlights
	case m.selected == 0:
		return modeSummary
	case m.hasHighlights():
		return modeItemsBack
	}
	return modeItems
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
		act := ""
		if n := s.ActCount(); n > 0 && i > 0 {
			act = fmt.Sprintf("!%d", n)
			counts = append(counts, act)
		}
		if n := s.Updates(); n > 0 {
			counts = append(counts, fmt.Sprintf("↻%d", n))
		}
		if s.TokensUsed > 0 {
			counts = append(counts, brief.HumanTokens(s.TokensUsed))
		}
		count := strings.Join(counts, " ")
		// The row is "▸ ✓ name  !A ↻N 12.3k" within the box's
		// sidebarWidth-1 cells, with at least one space before the counts:
		// A items to act on, N updates.
		room := sidebarWidth - 1 - 2 - lipgloss.Width(icon) - 1 - 1 - lipgloss.Width(count)
		name := truncate(brief.StripControl(s.Topic), room)
		line := "  " + icon + " " + name
		switch {
		case i == m.selected && m.focus == focusTopics:
			line = selStyle.Render("▸ ") + icon + " " + selStyle.Render(name)
		case i == m.selected:
			line = mutedStyle.Render("▸ ") + icon + " " + lipgloss.NewStyle().Bold(true).Render(name)
		}
		pad := max(1, sidebarWidth-1-lipgloss.Width(line)-lipgloss.Width(count))
		shown := mutedStyle.Render(count)
		if act != "" {
			shown = actStyle.Render(act) + mutedStyle.Render(strings.TrimPrefix(count, act))
		}
		b.WriteString(line + strings.Repeat(" ", pad) + shown + "\n")
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
			// A tag stays first, where it shows as a badge.
			tag := ""
			if _, imp, rest, ok := brief.SplitTag(headline); ok {
				tag, headline = "`"+imp.Label()+"` ", rest
			}
			lines[i] = lead + tag + "↻ Update · " + headline
		}
	}
	return strings.Join(lines, "\n")
}

// truncate cuts s to at most n cells, marking a cut with "…".
func truncate(s string, n int) string {
	return ansi.Truncate(s, max(0, n), "…")
}
