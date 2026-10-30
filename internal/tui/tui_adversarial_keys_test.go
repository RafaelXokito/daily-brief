package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/RafaelXokito/daily-brief/internal/brief"
	"github.com/RafaelXokito/daily-brief/internal/config"
)

// fitErrors lists how view overflows a width×height terminal.
func fitErrors(view string, width, height int) []string {
	var errs []string
	lines := strings.Split(view, "\n")
	if len(lines) > height {
		errs = append(errs, fmt.Sprintf("%d lines for a %d-line terminal", len(lines), height))
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w > width {
			errs = append(errs, fmt.Sprintf("line %d is %d cells wide (max %d): %q", i, w, width, l))
		}
	}
	return errs
}

func checkFits(t *testing.T, label string, m *Model) {
	t.Helper()
	for _, e := range fitErrors(m.View(), m.width, m.height) {
		t.Errorf("%s (%dx%d): %s", label, m.width, m.height, e)
	}
}

func journalWith(sections ...*brief.Section) *brief.Journal {
	return &brief.Journal{Date: "2026-10-29", TokenBudget: 400_000,
		Overview: brief.Section{Topic: "Overview", Status: brief.StatusDone, Markdown: longMarkdown("Overview")},
		Sections: sections}
}

func manySections(n int) []*brief.Section {
	var s []*brief.Section
	for i := range n {
		name := fmt.Sprintf("Topic %02d", i+1)
		s = append(s, &brief.Section{Topic: name, Status: brief.StatusDone, Markdown: longMarkdown(name)})
	}
	return s
}

// journals is a set of awkward section contents.
func journals() map[string]*brief.Journal {
	return map[string]*brief.Journal{
		"long": journalWith(manySections(2)...),
		"empty": journalWith(&brief.Section{Topic: "Empty", Status: brief.StatusDone},
			&brief.Section{Topic: "Also empty", Status: brief.StatusDone, Markdown: ""}),
		"failed": journalWith(&brief.Section{Topic: "Failed", Status: brief.StatusFailed, Error: strings.Repeat("rate limited ", 30)},
			&brief.Section{Topic: "Failed too", Status: brief.StatusFailed}),
		"pending": journalWith(&brief.Section{Topic: "Pending", Status: brief.StatusPending},
			&brief.Section{Topic: "Running", Status: brief.StatusRunning}),
		"one":    journalWith(manySections(1)...),
		"twelve": journalWith(manySections(12)...),
		"none":   {Date: "2026-10-29", Overview: brief.Section{Topic: "Overview", Status: brief.StatusPending}},
		"longname": journalWith(&brief.Section{Topic: strings.Repeat("Very long topic name ", 10), Status: brief.StatusDone,
			TokensUsed: 123_456, Markdown: "### " + strings.Repeat("Wide heading ", 40) + "\n" + strings.Repeat("|a|b|\n|-|-|\n|x|y|\n", 3)}),
	}
}

func newSized(j *brief.Journal, w, h int) *Model {
	m := New(nil, nil, j, "notty")
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

// exercise drives a model through focus, topics, scrolling and the
// overlay, checking the view fits after every step.
func exercise(t *testing.T, label string, m *Model) {
	t.Helper()
	steps := []struct {
		name string
		key  tea.KeyMsg
	}{
		{"start", tea.KeyMsg{}},
		{"pgdown", tea.KeyMsg{Type: tea.KeyPgDown}},
		{"down", tea.KeyMsg{Type: tea.KeyDown}},
		{"end", tea.KeyMsg{Type: tea.KeyEnd}},
		{"left", tea.KeyMsg{Type: tea.KeyLeft}},
		{"j", runes("j")},
		{"j", runes("j")},
		{"k", runes("k")},
		{"9", runes("9")},
		{"G", runes("G")},
		{"?", runes("?")},
		{"? close", runes("?")},
		{"tab", tea.KeyMsg{Type: tea.KeyTab}},
		{"u", runes("u")},
	}
	for _, s := range steps {
		if s.name != "start" {
			m.Update(s.key)
		}
		checkFits(t, label+" after "+s.name, m)
		if m.viewport.YOffset < 0 || (m.viewport.TotalLineCount() > 0 && m.viewport.YOffset > max(0, m.viewport.TotalLineCount()-m.viewport.Height)) {
			t.Errorf("%s after %s: YOffset %d out of range (lines %d, height %d)", label, s.name, m.viewport.YOffset, m.viewport.TotalLineCount(), m.viewport.Height)
		}
	}
}

// Sizes at which the layout has room: every journal fits.
func TestAdv_ViewFitsAtSupportedSizes(t *testing.T) {
	for _, size := range [][2]int{{50, 24}, {51, 24}, {52, 24}, {80, 24}, {300, 80}, {52, 40}} {
		for name, j := range journals() {
			if name == "twelve" && size[1] < 20 {
				continue
			}
			exercise(t, fmt.Sprintf("%s", name), newSized(j, size[0], size[1]))
		}
	}
}

// Below the full layout's 50x10 the View falls back to the summary alone,
// then to a one-line note, and never overflows the terminal.
func TestTinyTerminalFallsBackAndFits(t *testing.T) {
	for _, size := range [][2]int{{20, 5}, {40, 10}} {
		m := newSized(journals()["long"], size[0], size[1])
		checkFits(t, "tiny", m)
		m.Update(runes("?"))
		checkFits(t, "tiny overlay", m)
	}
	for _, size := range [][2]int{{49, 24}, {80, 9}, {20, 4}, {40, 6}} {
		m := newSized(journals()["long"], size[0], size[1])
		if m.layout() != layoutCompact {
			t.Errorf("%v: layout %d, want compact", size, m.layout())
		}
		exercise(t, fmt.Sprintf("compact %v", size), m)
		if v := m.View(); strings.Contains(v, "│") || !strings.Contains(v, "Daily Brief") {
			t.Errorf("%v: compact view has a sidebar or lacks the header:\n%s", size, v)
		}
	}
	for _, size := range [][2]int{{0, 0}, {5, 0}} {
		if v := newSized(journals()["long"], size[0], size[1]).View(); v != "" {
			t.Errorf("%v: view %q", size, v)
		}
	}
	for _, size := range [][2]int{{19, 5}, {80, 3}, {10, 1}, {1, 1}} {
		m := newSized(journals()["long"], size[0], size[1])
		exercise(t, fmt.Sprintf("too small %v", size), m)
		if v := m.View(); size[0] >= 15 && size[1] >= 1 && !strings.HasPrefix(v, "terminal too small") {
			t.Errorf("%v: view %q", size, v)
		}
	}
	// Every size from 0x1 to 60x14 fits, with the overlay open or not
	// (twelve short topics, to keep the sweep quick).
	short := journalWith(manySections(12)...)
	for _, s := range append(short.Sections, &short.Overview) {
		s.Markdown = "### " + s.Topic + "\n\n" + strings.Repeat("A line of news. ", 20)
	}
	m := newSized(short, 0, 1)
	for w := 0; w <= 60; w++ {
		for h := 1; h <= 14; h++ {
			m.Update(tea.WindowSizeMsg{Width: w, Height: h})
			checkFits(t, "sweep", m)
			m.Update(runes("?"))
			checkFits(t, "sweep overlay", m)
			m.Update(runes("?"))
		}
	}
}

// With many topics and a short terminal, the sidebar is cut to the pane
// and scrolls so the selected topic always shows, with markers for the
// rows hidden above and below.
func TestManyTopicsSidebarScrollsAndFits(t *testing.T) {
	m := newSized(journalWith(manySections(12)...), 100, 14)
	checkFits(t, "12 topics", m)
	for _, size := range [][2]int{{80, 15}, {100, 14}} {
		for n := 10; n <= 12; n++ {
			label := fmt.Sprintf("%d topics", n)
			exercise(t, label, newSized(journalWith(manySections(n)...), size[0], size[1]))
			m := newSized(journalWith(manySections(n)...), size[0], size[1])
			m.Update(tea.KeyMsg{Type: tea.KeyLeft})
			if v := m.View(); !strings.Contains(v, "↓") || !strings.Contains(v, "more") {
				t.Errorf("%v, %s: no marker for the hidden topics:\n%s", size, label, v)
			}
			// Down through every topic and the wrap, then back up.
			for i, k := range append(slices.Repeat([]tea.KeyMsg{runes("j")}, n+1), slices.Repeat([]tea.KeyMsg{runes("k")}, n+1)...) {
				m.Update(k)
				checkFits(t, fmt.Sprintf("%s, key %d", label, i), m)
				want := "Overview"
				if m.selected > 0 {
					want = fmt.Sprintf("Topic %02d", m.selected)
				}
				if v := m.View(); !strings.Contains(v, "▸ ✓ "+want) {
					t.Errorf("%v, %s, key %d: selected %q not shown in the sidebar:\n%s", size, label, i, want, v)
				}
			}
			if m.selected != 0 {
				t.Errorf("%v, %s: selected %d after a full lap each way", size, label, m.selected)
			}
		}
	}
	// The sidebar itself has no trailing newline.
	if s := newSized(journalWith(manySections(3)...), 100, 24).sidebar(); strings.HasSuffix(s, "\n") {
		t.Errorf("sidebar ends with a newline: %q", s)
	}
}

// Resizing keeps the topic, focus and a valid scroll position, and the
// view fits at the new size.
func TestAdv_ResizeMidSession(t *testing.T) {
	m := newSized(journals()["long"], 120, 30)
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m.Update(runes("j"))
	m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	y := m.viewport.YOffset
	if y == 0 {
		t.Fatal("did not scroll")
	}
	for _, size := range [][2]int{{52, 24}, {300, 80}, {50, 12}, {51, 40}, {120, 30}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		if m.selected != 1 || m.focus != focusTopics {
			t.Errorf("resize to %v: selected %d, focus %d", size, m.selected, m.focus)
		}
		if max := m.viewport.TotalLineCount() - m.viewport.Height; m.viewport.YOffset > max || m.viewport.YOffset < 0 {
			t.Errorf("resize to %v: YOffset %d beyond the bottom %d", size, m.viewport.YOffset, max)
		}
		checkFits(t, "resize", m)
		m.Update(runes("?"))
		checkFits(t, "resize overlay", m)
		m.Update(runes("?"))
	}
	// Scrolled to the bottom, a much taller terminal must not leave the
	// view past the end of the content.
	m.Update(runes("G"))
	m.Update(tea.WindowSizeMsg{Width: 300, Height: 80})
	if !m.viewport.AtBottom() && m.viewport.YOffset+m.viewport.Height > m.viewport.TotalLineCount() {
		t.Errorf("after growing: YOffset %d, height %d, lines %d", m.viewport.YOffset, m.viewport.Height, m.viewport.TotalLineCount())
	}
	checkFits(t, "grown at the bottom", m)
}

func TestAdv_SectionContents(t *testing.T) {
	m := newSized(journals()["empty"], 100, 24)
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m.Update(runes("j"))
	for _, k := range []tea.KeyMsg{{Type: tea.KeyPgDown}, runes("G"), runes("d"), runes("u"), {Type: tea.KeyEnd}, {Type: tea.KeyHome}} {
		m.Update(k)
		if m.viewport.YOffset != 0 || m.selected != 1 {
			t.Errorf("empty section, %q: YOffset %d, selected %d", k, m.viewport.YOffset, m.selected)
		}
	}
	if !strings.Contains(m.View(), "Empty") {
		t.Errorf("empty section view lacks its title:\n%s", m.View())
	}

	m = newSized(journals()["failed"], 100, 24)
	m.Update(runes("2"))
	if v := m.View(); !strings.Contains(v, "rate limited") {
		t.Errorf("failed section lacks its error:\n%s", v)
	}
	checkFits(t, "failed", m)

	m = newSized(journals()["pending"], 100, 24)
	m.Update(runes("2"))
	if v := m.View(); !strings.Contains(v, "Waiting to start") {
		t.Errorf("pending section:\n%s", v)
	}
	m.Update(runes("3"))
	if v := m.View(); !strings.Contains(v, "Searching the web") {
		t.Errorf("running section:\n%s", v)
	}

	// One section: j/k wrap between the overview and it; 3-9 do nothing.
	m = newSized(journals()["one"], 100, 24)
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	for i, want := range []int{1, 0, 1} {
		m.Update(runes("j"))
		if m.selected != want {
			t.Errorf("one section, j #%d: selected %d, want %d", i, m.selected, want)
		}
	}
	m.Update(runes("k"))
	m.Update(runes("k"))
	if m.selected != 1 {
		t.Errorf("one section, k k: selected %d", m.selected)
	}
	for _, k := range []string{"3", "4", "9"} {
		m.Update(runes(k))
		if m.selected != 1 {
			t.Errorf("one section, %s: selected %d", k, m.selected)
		}
	}
	m.Update(runes("1"))
	m.Update(runes("2"))
	if m.selected != 1 {
		t.Errorf("one section, 2: selected %d", m.selected)
	}

	// No sections at all: only the overview, nothing to move to.
	m = newSized(journals()["none"], 100, 24)
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	for _, k := range []tea.KeyMsg{runes("j"), runes("k"), runes("1"), runes("2"), runes("9"), {Type: tea.KeyPgDown}, runes("G")} {
		m.Update(k)
		if m.selected != 0 {
			t.Errorf("no sections, %q: selected %d", k, m.selected)
		}
	}
	checkFits(t, "no sections", m)
}

// With 12 topics, 1-9 reach the overview and topics 1-8; the rest are
// reached with ↑/↓ and the wrap.
func TestAdv_TwelveSections(t *testing.T) {
	m := newSized(journalWith(manySections(12)...), 120, 30)
	for k := 1; k <= 9; k++ {
		m.Update(runes(fmt.Sprint(k)))
		if m.selected != k-1 {
			t.Errorf("%d: selected %d", k, m.selected)
		}
	}
	if !strings.Contains(m.View(), "Topic 08") {
		t.Error("9 does not show Topic 08")
	}
	m.Update(runes("0"))
	if m.selected != 8 {
		t.Errorf("0 changed the topic to %d", m.selected)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	for range 4 {
		m.Update(runes("j"))
	}
	if m.selected != 12 || !strings.Contains(m.View(), "Topic 12") {
		t.Errorf("j to the last topic: selected %d", m.selected)
	}
	m.Update(runes("j"))
	if m.selected != 0 {
		t.Errorf("j past topic 12: selected %d", m.selected)
	}
	m.Update(runes("k"))
	if m.selected != 12 {
		t.Errorf("k from the overview: selected %d", m.selected)
	}
	checkFits(t, "twelve", m)
}

// The scroll position: a topic change or number jump goes to the top; a
// focus change keeps it.
func TestAdv_ScrollPositionAcrossNavigation(t *testing.T) {
	m := newSized(journalWith(manySections(4)...), 120, 30)
	scroll := func() int {
		m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
		m.Update(runes("j"))
		return m.viewport.YOffset
	}
	m.focus = focusSummary
	y := scroll()
	for _, k := range []tea.KeyMsg{{Type: tea.KeyTab}, {Type: tea.KeyShiftTab}, {Type: tea.KeyLeft}, {Type: tea.KeyRight},
		{Type: tea.KeyEsc}, {Type: tea.KeyEnter}, runes("h"), runes("l")} {
		m.Update(k)
		if m.viewport.YOffset != y || m.selected != 0 {
			t.Errorf("%q changed YOffset %d -> %d (selected %d)", k, y, m.viewport.YOffset, m.selected)
		}
	}
	m.Update(runes("?"))
	m.Update(runes("?"))
	if m.viewport.YOffset != y {
		t.Errorf("overlay open/close moved YOffset %d -> %d", y, m.viewport.YOffset)
	}
	for _, k := range []string{"3", "5", "1", "2"} {
		m.focus = focusSummary
		scroll()
		m.Update(runes(k))
		if m.viewport.YOffset != 0 {
			t.Errorf("jump %s: YOffset %d", k, m.viewport.YOffset)
		}
	}
	// Same section again: the jump still lands at the top.
	m.focus = focusSummary
	scroll()
	m.Update(runes("2"))
	if m.selected != 1 || m.viewport.YOffset != 0 {
		t.Errorf("jump to the current section: selected %d, YOffset %d", m.selected, m.viewport.YOffset)
	}
	for _, k := range []tea.KeyMsg{{Type: tea.KeyDown}, {Type: tea.KeyUp}, runes("j"), runes("k")} {
		m.focus = focusSummary
		scroll()
		m.focus = focusTopics
		m.Update(k)
		if m.viewport.YOffset != 0 || m.focus != focusTopics {
			t.Errorf("topic change %q: YOffset %d, focus %d", k, m.viewport.YOffset, m.focus)
		}
	}
}

// Terminals send the wheel as ↑/↓ in the alternate screen: with focus on
// Summary they scroll and never change the topic.
func TestAdv_WheelAsArrowKeys(t *testing.T) {
	m := newSized(journals()["long"], 120, 30)
	m.Update(runes("2"))
	bottom := m.viewport.TotalLineCount() - m.viewport.Height
	for i := 1; i <= bottom+20; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
		if want := min(i, bottom); m.viewport.YOffset != want || m.selected != 1 || m.focus != focusSummary {
			t.Fatalf("wheel down %d: YOffset %d want %d, selected %d", i, m.viewport.YOffset, want, m.selected)
		}
	}
	for i := 1; i <= bottom+20; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyUp})
		if want := max(0, bottom-i); m.viewport.YOffset != want || m.selected != 1 {
			t.Fatalf("wheel up %d: YOffset %d want %d, selected %d", i, m.viewport.YOffset, want, m.selected)
		}
	}
	// Bursts in alternating direction.
	for range 50 {
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	if m.viewport.YOffset != 50 || m.selected != 1 {
		t.Errorf("mixed wheel: YOffset %d, selected %d", m.viewport.YOffset, m.selected)
	}
}

func TestAdv_OverlayListsEveryBindingAndFits(t *testing.T) {
	for _, size := range [][2]int{{50, 24}, {52, 12}, {80, 24}} {
		m := newSized(journals()["long"], size[0], size[1])
		m.Update(runes("?"))
		checkFits(t, "overlay", m)
	}
	for _, size := range [][2]int{{120, 30}, {300, 80}} {
		m := newSized(journals()["long"], size[0], size[1])
		m.Update(runes("?"))
		v := m.View()
		checkFits(t, "overlay", m)
		for _, b := range keys.all() {
			if !strings.Contains(v, b.Help().Desc) {
				t.Errorf("%v overlay lacks %q:\n%s", size, b.Help().Desc, v)
			}
			if !strings.Contains(v, keyNames(b)) {
				t.Errorf("%v overlay lacks keys %q:\n%s", size, keyNames(b), v)
			}
		}
		// Every key of every binding is named in the overlay.
		for _, b := range keys.all() {
			if b.Help().Key != "" {
				continue
			}
			for _, k := range b.Keys() {
				if !strings.Contains(v, keyName(k)) {
					t.Errorf("%v overlay does not name key %q", size, k)
				}
			}
		}
	}
	// The overlay ignores every key but ?, esc, q and ctrl+c.
	m := newSized(journals()["long"], 80, 24)
	m.Update(runes("?"))
	for _, k := range []tea.KeyMsg{runes("j"), runes("h"), {Type: tea.KeyTab}, runes(" "), {Type: tea.KeyPgDown}, runes("G"),
		runes("d"), {Type: tea.KeyCtrlD}, runes("2"), runes("r"), {Type: tea.KeyEnter}, {Type: tea.KeyShiftDown}} {
		if _, cmd := m.Update(k); cmd != nil || !m.showHelp || m.selected != 0 || m.viewport.YOffset != 0 || m.focus != focusSummary {
			t.Errorf("%q with the overlay open changed the model", k)
		}
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC}); cmd == nil {
		t.Error("ctrl+c with the overlay open did not quit")
	}
}

// Every binding's keys and full description can be read in the overlay
// at common sizes, scrolling it if it does not fit; a hint says when keys
// are hidden, and the scrolling keys leave the summary alone.
func TestOverlayShowsEveryBindingByScrolling(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {52, 24}, {100, 16}, {52, 12}, {80, 15}, {40, 8}} {
		for _, step := range []tea.KeyMsg{{Type: tea.KeyDown}, runes("j"), {Type: tea.KeyPgDown}, runes(" ")} {
			m := newSized(journals()["long"], size[0], size[1])
			m.Update(runes("?"))
			seen := m.View()
			if !m.keysView.AtBottom() && !strings.Contains(seen, "↓ more") {
				t.Errorf("%v: keys hidden below without a hint:\n%s", size, seen)
			}
			for i := 0; !m.keysView.AtBottom(); i++ {
				if i > 50 {
					t.Fatalf("%v: %q never reaches the bottom", size, step)
				}
				m.Update(step)
				checkFits(t, "scrolled overlay", m)
				seen += "\n" + m.View()
			}
			for _, b := range keys.all() {
				if !strings.Contains(seen, keyNames(b)) || !strings.Contains(seen, b.Help().Desc) {
					t.Errorf("%dx%d, %q: overlay never fully shows %q %q", size[0], size[1], step, keyNames(b), b.Help().Desc)
				}
			}
			if !m.keysView.AtTop() && !strings.Contains(m.View(), "↑ more") {
				t.Errorf("%v: keys hidden above without a hint:\n%s", size, m.View())
			}
			if m.viewport.YOffset != 0 || m.selected != 0 || m.focus != focusSummary || !m.showHelp {
				t.Errorf("%v: scrolling the overlay changed the model", size)
			}
			m.Update(runes("g"))
			if !m.keysView.AtTop() {
				t.Errorf("%v: g did not go to the top of the overlay", size)
			}
			m.Update(runes("G"))
			if !m.keysView.AtBottom() {
				t.Errorf("%v: G did not go to the bottom of the overlay", size)
			}
			// The overlay opens at the top again.
			m.Update(runes("?"))
			m.Update(runes("?"))
			if !m.keysView.AtTop() {
				t.Errorf("%v: the reopened overlay is not at the top", size)
			}
		}
	}
	// At 80 columns every description is beside its key, uncut.
	m := newSized(journals()["long"], 80, 24)
	m.Update(runes("?"))
	top := m.View()
	m.Update(runes("G"))
	both := top + "\n" + m.View()
	m.Update(runes("g"))
	for _, b := range keys.all() {
		if !strings.Contains(both, keyNames(b)+strings.Repeat(" ", 2)) {
			t.Errorf("80x24: %q is not in the key column", keyNames(b))
		}
		if lipgloss.Width(b.Help().Desc) > 22 {
			t.Errorf("description %q is over 22 cells", b.Help().Desc)
		}
	}
	if v := m.View(); strings.Contains(v, "…") {
		t.Errorf("80x24 overlay is cut:\n%s", v)
	}
	lines := strings.Split(m.View(), "\n")
	if help := lines[len(lines)-1]; !strings.Contains(help, "scroll") || !strings.Contains(help, "?/esc close") {
		t.Errorf("help line with the overlay open: %q", help)
	}
	for w := 0; w <= 120; w++ {
		if l := overlayHelpLine(w); lipgloss.Width(l) > w || (w >= 20 && !strings.HasSuffix(l, "?/esc close") && !strings.Contains(l, "?/esc close ·")) {
			t.Errorf("width %d: overlay help line %q", w, l)
		}
	}
}

func TestAdv_HelpLineFitsEveryWidth(t *testing.T) {
	for w := 0; w <= 160; w++ {
		for _, focus := range []helpMode{modeSummary, modeTopics, modeHighlights, modeItems, modeItemsBack} {
			for _, gen := range []bool{false, true} {
				l := helpLine(focus, gen, w)
				if lipgloss.Width(l) > w {
					t.Errorf("width %d, focus %d, gen %v: %q is %d cells", w, focus, gen, l, lipgloss.Width(l))
				}
				if w >= 20 && !strings.HasSuffix(l, "? help") && !strings.Contains(l, "? help ·") {
					t.Errorf("width %d: %q loses \"? help\"", w, l)
				}
			}
		}
	}
}

// Keys while a fake run streams updates: focus, scroll and the overlay
// stay put, r is ignored, q cancels and saves finished topics.
func TestAdv_KeysWhileStreaming(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := config.Default()
	cfg.Provider.Type = "anthropic"
	cfg.TokenBudget = 400_000
	cfg.Concurrency = 1
	cfg.Topics = []config.Topic{{Name: "A"}, {Name: "B"}, {Name: "C"}}
	b := &splitBackend{gatedBackend{started: make(chan struct{}, 1)}}
	m := New(brief.NewGenerator(cfg, b), nil, nil, "notty")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	wait := m.start()
	<-b.started
	for m.journal.Sections[0].Status != brief.StatusDone {
		_, wait = m.Update(wait())
	}

	fake := func(i int, status brief.Status) {
		topic := m.journal.Overview.Topic
		if i >= 0 {
			topic = m.journal.Sections[i].Topic
		}
		m.Update(updateMsg(brief.Update{Index: i, Section: brief.Section{Topic: topic, Status: status, Markdown: longMarkdown(topic)}}))
	}
	// Scroll the overview while it streams.
	fake(-1, brief.StatusRunning)
	for range 5 {
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	y := m.viewport.YOffset
	for _, i := range []int{-1, 1, 2, -1, 1} {
		fake(i, brief.StatusRunning)
		if m.selected != 0 || m.focus != focusSummary || m.viewport.YOffset != y {
			t.Errorf("update %d moved the view: selected %d, focus %d, YOffset %d want %d", i, m.selected, m.focus, m.viewport.YOffset, y)
		}
		checkFits(t, "streaming", m)
	}

	// The overlay stays open through updates.
	m.Update(runes("?"))
	for _, i := range []int{-1, 1, 2} {
		fake(i, brief.StatusRunning)
		if !m.showHelp || !strings.Contains(m.View(), keys.Up.Help().Desc) {
			t.Errorf("update %d closed the overlay", i)
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.showHelp || m.focus != focusSummary || m.viewport.YOffset != y {
		t.Errorf("esc after streaming: overlay %v, focus %d, YOffset %d", m.showHelp, m.focus, m.viewport.YOffset)
	}

	// Topics focus stays through updates.
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m.Update(runes("j"))
	fake(0, brief.StatusDone)
	fake(1, brief.StatusRunning)
	if m.focus != focusTopics || m.selected != 1 {
		t.Errorf("after updates in Topics: focus %d, selected %d", m.focus, m.selected)
	}

	running := m.running
	for range 3 {
		if _, cmd := m.Update(runes("r")); cmd != nil || m.running != running || m.selected != 1 {
			t.Error("r during a run did something")
		}
	}

	_, cmd := m.Update(runes("q"))
	if cmd == nil {
		t.Fatal("q returned no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("q did not quit")
	}
	saved, err := brief.LoadJournal(m.journal.Date)
	if err != nil || saved == nil {
		t.Fatalf("q during a run saved nothing: %v", err)
	}
	if a := saved.Sections[0]; a.Status != brief.StatusDone || a.Markdown == "" {
		t.Errorf("saved A: %s %q", a.Status, a.Markdown)
	}
	for _, s := range saved.Sections[1:] {
		if s.Status == brief.StatusRunning || s.Status == brief.StatusPending {
			t.Errorf("saved %s still %s", s.Topic, s.Status)
		}
	}
	drain(t, m, wait)
}
