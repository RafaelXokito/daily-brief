package tui

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/RafaelXokito/daily-brief/internal/brief"
	"github.com/RafaelXokito/daily-brief/internal/config"
)

func click(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
}

func wheel(x, y, d int) tea.MouseMsg {
	b := tea.MouseButtonWheelDown
	if d < 0 {
		b = tea.MouseButtonWheelUp
	}
	return tea.MouseMsg{X: x, Y: y, Button: b, Action: tea.MouseActionPress}
}

// rowContaining is the first row of view whose cells in [from, to) hold
// text, or -1.
func rowContaining(view string, from, to int, text string) int {
	for y, l := range strings.Split(view, "\n") {
		if strings.Contains(ansi.Strip(ansi.Cut(l, from, to)), text) {
			return y
		}
	}
	return -1
}

// cmdMsgs runs cmd and the commands of a batch it returns, and collects
// their messages; a timer (the flash's) is left to expire.
func cmdMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		if b, ok := msg.(tea.BatchMsg); ok {
			var out []tea.Msg
			for _, c := range b {
				out = append(out, cmdMsgs(c)...)
			}
			return out
		}
		return []tea.Msg{msg}
	case <-time.After(200 * time.Millisecond):
		return nil
	}
}

// hasMsgLike reports whether msgs hold a message of the type want gives.
func hasMsgLike(msgs []tea.Msg, want tea.Cmd) bool {
	wt := reflect.TypeOf(want())
	for _, m := range msgs {
		if reflect.TypeOf(m) == wt {
			return true
		}
	}
	return false
}

// The hit test of a sidebar that shows every topic: rows from the body's
// top, nothing on the border, the gap or below the last topic, the
// Summary from the gap on.
func TestHitTestSidebarUnscrolled(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		g := geometry{top: 3, height: 10, sidebar: true, topics: 4}
		for _, c := range []struct {
			x, y int
			want hit
		}{
			{0, 3, hit{regionTopic, 0}},
			{5, 4, hit{regionTopic, 1}},
			{sidebarWidth - 1, 6, hit{regionTopic, 3}},
			{5, 7, hit{region: regionSidebar}},  // below the last topic
			{5, 12, hit{region: regionSidebar}}, // the body's last row
			{sidebarWidth, 4, hit{region: regionNone}},
			{sidebarWidth + 1, 4, hit{region: regionNone}},
			{sidebarWidth + 2, 4, hit{regionSummary, 1}},
			{79, 12, hit{regionSummary, 9}},
			{5, 0, hit{region: regionNone}}, // header
			{5, 2, hit{region: regionNone}}, // pane titles
			{5, 13, hit{region: regionNone}},
			{-1, 4, hit{region: regionNone}},
		} {
			if got := hitTest(g, c.x, c.y); got != c.want {
				t.Errorf("(%d,%d): %+v, want %+v", c.x, c.y, got, c.want)
			}
		}
		if got := hitTest(geometry{top: -1, height: 10, sidebar: true, topics: 4}, 5, 3); got.region != regionNone {
			t.Errorf("nothing drawn: %+v", got)
		}
	})
}

// A sidebar with more topics than rows: the "↑ more" and "↓ more" rows
// hit no topic, and the rows between map to the window sidebarView shows.
func TestHitTestSidebarScrolled(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		// At the top: topics 0-8, then the ↓ marker.
		g := geometry{top: 3, height: 10, sidebar: true, topics: 20, selected: 2}
		for row, want := range map[int]hit{0: {regionTopic, 0}, 8: {regionTopic, 8}, 9: {region: regionSidebar}} {
			if got := hitTest(g, 3, 3+row); got != want {
				t.Errorf("unscrolled row %d: %+v, want %+v", row, got, want)
			}
		}
		// Scrolled: the ↑ marker, topics 5-12, the ↓ marker.
		g = geometry{top: 3, height: 10, sidebar: true, topics: 20, sidebarTop: 5, selected: 7}
		for row, want := range map[int]hit{0: {region: regionSidebar}, 1: {regionTopic, 5}, 8: {regionTopic, 12}, 9: {region: regionSidebar}} {
			if got := hitTest(g, 3, 3+row); got != want {
				t.Errorf("scrolled row %d: %+v, want %+v", row, got, want)
			}
		}
		// At the bottom: the ↑ marker, then topics 11-19.
		g = geometry{top: 3, height: 10, sidebar: true, topics: 20, sidebarTop: 15, selected: 19}
		for row, want := range map[int]hit{0: {region: regionSidebar}, 1: {regionTopic, 11}, 9: {regionTopic, 19}} {
			if got := hitTest(g, 3, 3+row); got != want {
				t.Errorf("bottom row %d: %+v, want %+v", row, got, want)
			}
		}
	})
}

// Without the sidebar, in compact layout or reader mode, every body cell
// is the Summary.
func TestHitTestWithoutSidebar(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		for name, g := range map[string]geometry{
			"compact": {top: 1, height: 6, topics: 4},
			"reader":  {top: 3, height: 10, topics: 4},
		} {
			if got := hitTest(g, 2, g.top); got != (hit{regionSummary, 0}) {
				t.Errorf("%s: left column %+v", name, got)
			}
			if got := hitTest(g, sidebarWidth, g.top+2); got != (hit{regionSummary, 2}) {
				t.Errorf("%s: border column %+v", name, got)
			}
			if got := hitTest(g, 2, g.top-1); got.region != regionNone {
				t.Errorf("%s: header %+v", name, got)
			}
			if got := hitTest(g, 2, g.top+g.height); got.region != regionNone {
				t.Errorf("%s: help line %+v", name, got)
			}
		}
	})
}

// The geometry is measured from what View draws: each row the Topics pane
// shows a topic on hits that topic, and its marker rows hit none, scrolled
// or not, in a full view; compact and reader views have no Topics pane.
func TestHitTestMatchesView(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		m := newSized(journalWith(manySections(20)...), 100, 16)
		for _, sel := range []int{0, 9, 20, 3} {
			m.selectSection(sel)
			view := m.View()
			g := m.geometry()
			found := 0
			for y, l := range strings.Split(view, "\n") {
				side := ansi.Strip(ansi.Cut(l, 0, sidebarWidth))
				h := hitTest(g, 4, y)
				switch {
				case y < g.top || y >= g.top+g.height:
					if h.region != regionNone {
						t.Errorf("selected %d: row %d %q hits %+v", sel, y, side, h)
					}
				case strings.Contains(side, "more"):
					if h.region != regionSidebar {
						t.Errorf("selected %d: marker row %d %q hits %+v", sel, y, side, h)
					}
				case strings.Contains(side, "Topic "):
					found++
					want := 0
					if i := strings.Index(side, "Topic "); i >= 0 {
						var n int
						for _, r := range side[i+6 : i+8] {
							n = n*10 + int(r-'0')
						}
						want = n
					}
					if h != (hit{regionTopic, want}) {
						t.Errorf("selected %d: row %d %q hits %+v, want topic %d", sel, y, side, h, want)
					}
				case strings.Contains(side, "Overview"):
					found++
					if h != (hit{regionTopic, 0}) {
						t.Errorf("selected %d: row %d %q hits %+v", sel, y, side, h)
					}
				}
				if y == g.top && hitTest(g, sidebarWidth, y).region != regionNone {
					t.Errorf("selected %d: the border is clickable", sel)
				}
			}
			if found == 0 {
				t.Errorf("selected %d: no topic rows found in\n%s", sel, ansi.Strip(view))
			}
		}

		// Compact: a click on the left column focuses the Summary, never
		// selects a topic.
		c := newSized(journalWith(manySections(3)...), 40, 8)
		c.focus = focusTopics
		c.View()
		if c.layout() != layoutCompact {
			t.Fatalf("layout %d", c.layout())
		}
		c.Update(click(2, 2))
		if c.selected != 0 || c.focus != focusSummary {
			t.Errorf("compact click: selected %d, focus %d", c.selected, c.focus)
		}

		// Reader mode: the same.
		r := newSized(journalWith(manySections(3)...), 100, 20)
		r.Update(runes("z"))
		r.focus = focusTopics
		r.View()
		r.Update(click(2, r.bodyTop+1))
		if r.selected != 0 || r.focus != focusSummary || !r.reader {
			t.Errorf("reader click: selected %d, focus %d, reader %v", r.selected, r.focus, r.reader)
		}
	})
}

// A click on the Security row opens Security; a click on a marker row, a
// border or the help line does nothing.
func TestClickSelectsTopic(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		m, _ := richModel(t, 120, 30, "dark")
		m.WithMouse(true)
		m.focus = focusTopics
		view := m.View()
		y := rowContaining(view, 0, sidebarWidth, "Security")
		if y < 0 {
			t.Errorf("no Security row in\n%s", ansi.Strip(view))
			return
		}
		m.Update(click(sidebarWidth+1, y)) // the gap
		m.Update(click(sidebarWidth, y))   // the border
		m.Update(click(4, m.height-1))     // the help line
		if m.selected != 0 || m.focus != focusTopics {
			t.Errorf("a click off the rows acted: selected %d, focus %d", m.selected, m.focus)
		}
		m.Update(click(4, y))
		if m.current().Topic != "Security" || m.focus != focusSummary {
			t.Errorf("click on Security: %q, focus %d", m.current().Topic, m.focus)
		}
		m.View()
		if y := rowContaining(m.View(), 0, sidebarWidth, "Overview"); y < 0 {
			t.Error("no Overview row")
		} else if m.Update(click(1, y)); m.selected != 0 {
			t.Errorf("click on Overview: selected %d", m.selected)
		}

		// Motion (a drag), a release and other buttons do nothing.
		m.selectSection(0)
		m.View()
		for _, msg := range []tea.MouseMsg{
			{X: 4, Y: y + 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion},
			{X: 4, Y: y + 2, Button: tea.MouseButtonNone, Action: tea.MouseActionMotion},
			{X: 4, Y: y + 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease},
			{X: 4, Y: y + 2, Button: tea.MouseButtonRight, Action: tea.MouseActionPress},
			{X: 4, Y: y + 2, Button: tea.MouseButtonMiddle, Action: tea.MouseActionPress},
			{X: 4, Y: y + 2, Button: tea.MouseButtonWheelLeft, Action: tea.MouseActionPress},
		} {
			if m.Update(msg); m.selected != 0 {
				t.Errorf("%v selected %d", msg, m.selected)
				m.selectSection(0)
			}
		}
	})
}

func TestHighlightAt(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		content := "\nTitle\n\nAct now\n\n▸ one\n  why\n  Topic\n\n  two\n  Topic\n\nstatus"
		starts := []int{5, 9}
		for line, want := range map[int]int{0: -1, 1: -1, 4: -1, 5: 0, 6: 0, 7: 0, 8: -1, 9: 1, 10: 1, 11: -1, 12: -1, 40: -1} {
			if got := highlightAt(content, starts, line); got != want {
				t.Errorf("line %d: %d, want %d", line, got, want)
			}
		}
	})
}

// A click on a highlight goes where enter on it goes; one between
// highlights only focuses the Summary.
func TestClickHighlightJumps(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		want, _ := richModel(t, 120, 30, "dark")
		for want.highlights()[want.cursor].Text != "Rotate npm tokens." {
			want.Update(tea.KeyMsg{Type: tea.KeyDown})
		}
		want.Update(tea.KeyMsg{Type: tea.KeyEnter})

		m, _ := richModel(t, 120, 30, "dark")
		m.WithMouse(true)
		m.focus = focusTopics
		view := m.View()
		x := sidebarWidth + 2
		y := rowContaining(view, x, 1<<20, "Rotate npm tokens.")
		if y < 0 {
			t.Errorf("no highlight row in\n%s", ansi.Strip(view))
			return
		}
		m.Update(click(x+1, y-1)) // the blank line above it
		if m.selected != 0 || m.focus != focusSummary {
			t.Errorf("click between highlights: selected %d, focus %d", m.selected, m.focus)
		}
		m.Update(click(x+4, y+1)) // its why line
		if m.selected != want.selected || m.current().Topic != "Security" || m.item != want.item || m.viewport.YOffset != want.viewport.YOffset {
			t.Errorf("click: selected %d item %d offset %d, enter: %d %d %d",
				m.selected, m.item, m.viewport.YOffset, want.selected, want.item, want.viewport.YOffset)
		}

		// Scrolled, the click still maps through the viewport's offset.
		s, _ := richModel(t, 120, 14, "dark")
		s.WithMouse(true)
		s.viewport.GotoBottom()
		view = s.View()
		y = rowContaining(view, x, 1<<20, "Kotlin 2.3 ships K3.")
		if y < 0 {
			t.Errorf("no Kotlin row in\n%s", ansi.Strip(view))
			return
		}
		s.Update(click(x+4, y))
		if s.current().Topic != "Java & Quarkus" || s.item < 0 {
			t.Errorf("scrolled click: %q item %d", s.current().Topic, s.item)
		}
	})
}

// The wheel moves the selected topic over the Topics pane, stopping at
// the ends, and scrolls the Summary anywhere else, also in reader mode.
func TestWheel(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		m := newSized(journalWith(manySections(3)...), 100, 30)
		m.View()
		top := m.bodyTop
		m.Update(wheel(3, top+10, 1)) // below the rows: still the pane
		m.Update(wheel(3, top, 1))
		if m.selected != 2 || m.viewport.YOffset != 0 {
			t.Errorf("wheel down over Topics: selected %d, offset %d", m.selected, m.viewport.YOffset)
		}
		m.Update(wheel(3, top, 1))
		m.Update(wheel(3, top, 1))
		if m.selected != 3 {
			t.Errorf("wheel past the last topic: selected %d", m.selected)
		}
		for range 5 {
			m.Update(wheel(3, top, -1))
		}
		if m.selected != 0 {
			t.Errorf("wheel past the Overview: selected %d", m.selected)
		}
		m.Update(wheel(sidebarWidth+5, top+2, 1))
		m.Update(wheel(sidebarWidth+5, top+2, 1))
		if m.viewport.YOffset != 2*wheelLines || m.selected != 0 {
			t.Errorf("wheel over Summary: offset %d, selected %d", m.viewport.YOffset, m.selected)
		}
		m.Update(wheel(sidebarWidth+5, 0, -1)) // the header scrolls the Summary too
		if m.viewport.YOffset != wheelLines {
			t.Errorf("wheel up over the header: offset %d", m.viewport.YOffset)
		}

		m.Update(runes("z"))
		m.View()
		before := m.viewport.YOffset
		m.Update(wheel(3, m.bodyTop+1, 1))
		if m.viewport.YOffset != before+wheelLines || m.selected != 0 {
			t.Errorf("reader wheel: offset %d (was %d), selected %d", m.viewport.YOffset, before, m.selected)
		}

		c := newSized(journalWith(manySections(3)...), 40, 8)
		c.View()
		c.Update(wheel(2, 2, 1))
		if c.viewport.YOffset != wheelLines || c.selected != 0 {
			t.Errorf("compact wheel: offset %d, selected %d", c.viewport.YOffset, c.selected)
		}
	})
}

// While the list of keys, the Settings pane or its y/n question is open,
// clicks do nothing; the wheel scrolls the list of keys only.
func TestMouseOverlays(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		m := newSized(journalWith(manySections(3)...), 100, 14)
		m.WithMouse(true)
		m.Update(runes("?"))
		m.View()
		top := m.bodyTop
		m.Update(click(3, top+1))
		m.Update(click(sidebarWidth+4, top+1))
		m.Update(wheel(3, top+1, 1))
		if m.selected != 0 || !m.showHelp {
			t.Errorf("keys open: selected %d, showHelp %v", m.selected, m.showHelp)
		}
		if m.keysView.YOffset != wheelLines {
			t.Errorf("wheel over the keys: offset %d", m.keysView.YOffset)
		}
		m.Update(wheel(sidebarWidth+4, top+1, -1))
		if m.keysView.YOffset != 0 || m.viewport.YOffset != 0 {
			t.Errorf("wheel up over the keys: keys %d, viewport %d", m.keysView.YOffset, m.viewport.YOffset)
		}

		s, _, _ := settingsModel(t, settingsConfig, 120, 30)
		s.WithMouse(true)
		s.Update(runes("s"))
		s.View()
		sel := s.set.sel
		for _, msg := range []tea.MouseMsg{click(3, s.bodyTop+1), click(sidebarWidth+4, s.bodyTop+2), wheel(3, s.bodyTop, 1), wheel(sidebarWidth+4, s.bodyTop, 1)} {
			s.Update(msg)
		}
		if s.set == nil || s.selected != 0 || s.set.sel != sel || s.viewport.YOffset != 0 {
			t.Errorf("settings open: clicks acted")
		}
		// The y/n question.
		goTo(t, s, "effort")
		s.Update(keyOf(tea.KeyLeft))
		s.Update(keyOf(tea.KeyEsc))
		if s.set == nil || s.set.mode != setConfirm {
			t.Errorf("no question asked")
			return
		}
		s.Update(click(3, s.bodyTop+1))
		s.Update(click(sidebarWidth+4, s.bodyTop+1))
		if s.set == nil || s.set.mode != setConfirm || s.selected != 0 {
			t.Errorf("a click answered the question")
		}
	})
}

// m turns the mouse off and on for the session, saying so, and never
// saves it; the help says how to select text while it is on.
func TestMouseToggleKey(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		for _, b := range keys.all() {
			if b.Keys()[0] == "m" && b.Help().Desc != "mouse on/off" {
				t.Errorf("m is bound to %q", b.Help().Desc)
			}
		}
		m, path, _ := settingsModel(t, settingsConfig, 200, 40)
		m.WithMouse(true)
		before, _ := os.ReadFile(path)
		if v := m.View(); !strings.Contains(v, mouseNote) {
			t.Errorf("mouse on: the help line has no note: %q", ansi.Strip(v))
		}
		_, cmd := m.Update(runes("m"))
		if m.mouse || m.flash != "mouse off" || !hasMsgLike(cmdMsgs(cmd), tea.DisableMouse) {
			t.Errorf("m: mouse %v, flash %q", m.mouse, m.flash)
		}
		m.flash = ""
		if v := m.View(); strings.Contains(v, mouseNote) || strings.Contains(v, "shift-") {
			t.Errorf("mouse off: the help line mentions shift: %q", ansi.Strip(v))
		}
		m.Update(runes("?"))
		if v := ansi.Strip(m.View()); strings.Contains(v, "shift-drag") || !strings.Contains(v, "mouse on/off") {
			t.Errorf("mouse off: keys pane:\n%s", v)
		}
		m.Update(runes("?"))
		_, cmd = m.Update(runes("m"))
		if !m.mouse || m.flash != "mouse on · shift-drag to select" || !hasMsgLike(cmdMsgs(cmd), tea.EnableMouseCellMotion) {
			t.Errorf("m again: mouse %v, flash %q", m.mouse, m.flash)
		}
		m.Update(runes("?"))
		if v := ansi.Strip(m.View()); !strings.Contains(v, "shift-drag selects") || !strings.Contains(v, "Cmd in Ghostty") {
			t.Errorf("mouse on: keys pane:\n%s", v)
		}
		if after, _ := os.ReadFile(path); string(after) != string(before) {
			t.Errorf("m changed the config:\n%s", after)
		}
		// A narrow help line keeps its keys rather than the note.
		n := newSized(journalWith(manySections(2)...), 80, 20)
		n.WithMouse(true)
		if l := ansi.Strip(n.helpText()); strings.Contains(l, "shift-") || !strings.HasSuffix(l, "? help") {
			t.Errorf("80 columns: %q", l)
		}
	})
}

// The Settings pane's mouse field applies at once, saves with ctrl+s,
// and reverts when the pane is closed unsaved.
func TestSettingsMouseField(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		m, path, _ := settingsModel(t, settingsConfig, 120, 30)
		m.WithMouse(true)
		m.Update(runes("s"))
		goTo(t, m, "mouse")
		if v, _ := m.set.value(m.set.field()); v != "on" {
			t.Errorf("unset mouse shows %q", v)
		}
		_, cmd := m.Update(keyOf(tea.KeyRight))
		if m.mouse || m.set.cfg.MouseOn() || !hasMsgLike(cmdMsgs(cmd), tea.DisableMouse) {
			t.Errorf("→: mouse %v, cfg %v", m.mouse, m.set.cfg.MouseOn())
		}
		if v, _ := m.set.value(m.set.field()); v != "off" {
			t.Errorf("shows %q", v)
		}
		if !strings.Contains(m.set.hint(m.set.field()), "shift-click opens links") {
			t.Error("the field's hint does not say how links open")
		}
		// Dropped: back on.
		_, cmd = m.Update(keyOf(tea.KeyEsc))
		_, cmd2 := m.Update(runes("y"))
		if m.set != nil || !m.mouse || !hasMsgLike(append(cmdMsgs(cmd), cmdMsgs(cmd2)...), tea.EnableMouseCellMotion) {
			t.Errorf("discarded: pane %v, mouse %v", m.set != nil, m.mouse)
		}

		// Saved: stays off, also after closing, and the file says so.
		m.Update(runes("s"))
		goTo(t, m, "mouse")
		m.Update(keyOf(tea.KeySpace))
		_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
		cmdMsgs(cmd)
		m.Update(keyOf(tea.KeyEsc))
		if m.set != nil || m.mouse {
			t.Errorf("saved and closed: pane %v, mouse %v", m.set != nil, m.mouse)
		}
		data, _ := os.ReadFile(path)
		if !strings.Contains(string(data), "mouse: false") || !strings.Contains(string(data), "my_note: keep me") {
			t.Errorf("saved file:\n%s", data)
		}
		cfg, _, err := config.LoadStamped(path)
		if err != nil || cfg.MouseOn() {
			t.Errorf("reloaded: mouse %v, %v", cfg.MouseOn(), err)
		}

		// The session's m toggle is kept when the pane opens and closes.
		m.Update(runes("m"))
		m.Update(runes("s"))
		m.Update(keyOf(tea.KeyEsc))
		if !m.mouse {
			t.Error("opening and closing Settings dropped the m toggle")
		}
	})
}

// The mouse is off unless main says otherwise, and a model without it
// still handles a stray event harmlessly.
func TestMouseDefaultOffInModel(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		m := New(nil, nil, &brief.Journal{Date: "2026-10-29"}, "notty")
		if m.mouse {
			t.Error("New turned the mouse on")
		}
		m.Update(click(1, 1)) // before any size
		m.Update(tea.WindowSizeMsg{Width: 10, Height: 2})
		m.View()
		m.Update(click(1, 1)) // too small to draw
	})
}

// Bubble Tea turns mouse reporting off to run $EDITOR and leaves it off,
// so coming back from the editor turns it on again while the session has
// the mouse; a file whose mouse the editor set to false turns it off.
func TestEditorRestoresMouse(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		for _, edit := range []string{"", "mouse: false\n"} {
			m, path, _ := settingsModel(t, settingsConfig, 120, 30)
			m.WithMouse(true)
			m.editor = func(_ string, done func(error) tea.Msg) tea.Cmd {
				return func() tea.Msg {
					if edit != "" {
						if err := os.WriteFile(path, []byte(settingsConfig+edit), 0o600); err != nil {
							t.Error(err)
						}
					}
					return done(nil)
				}
			}
			m.Update(runes("s"))
			_, cmd := m.Update(runes("e"))
			msgs := cmdMsgs(cmd)
			if len(msgs) != 1 {
				t.Errorf("%q: editor gave %v", edit, msgs)
				continue
			}
			_, cmd = m.Update(msgs[0])
			got := cmdMsgs(cmd)
			if edit == "" && (!m.mouse || !hasMsgLike(got, tea.EnableMouseCellMotion)) {
				t.Errorf("unchanged file: mouse %v, msgs %v", m.mouse, got)
			}
			if edit != "" && (m.mouse || hasMsgLike(got, tea.EnableMouseCellMotion) || !hasMsgLike(got, tea.DisableMouse)) {
				t.Errorf("mouse: false: mouse %v, msgs %v", m.mouse, got)
			}
		}
	})
}

// Saving or reloading other settings keeps the session's m toggle; only a
// changed mouse setting replaces it.
func TestSaveKeepsMouseToggle(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		m, path, _ := settingsModel(t, settingsConfig, 120, 30)
		m.WithMouse(true)
		m.Update(runes("m"))
		m.Update(runes("s"))
		goTo(t, m, "effort")
		m.Update(keyOf(tea.KeyRight))
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
		cmdMsgs(cmd)
		_, cmd = m.Update(keyOf(tea.KeyEsc))
		if m.set != nil || m.mouse || hasMsgLike(cmdMsgs(cmd), tea.EnableMouseCellMotion) {
			t.Errorf("saving effort undid m: pane %v, mouse %v", m.set != nil, m.mouse)
		}

		// A reload of a file changed elsewhere, but not its mouse, keeps it.
		m.Update(runes("s"))
		if err := os.WriteFile(path, []byte(strings.Replace(settingsConfig, "history_days: 3", "history_days: 4", 1)), 0o600); err != nil {
			t.Error(err)
			return
		}
		m.Update(runes("R"))
		m.Update(keyOf(tea.KeyEsc))
		if m.mouse {
			t.Error("reloading another setting undid m")
		}

		// Trying the mouse field and saving it as it was keeps the
		// session's off; saving a change of it replaces the session's on.
		m.Update(runes("s"))
		goTo(t, m, "mouse")
		m.Update(keyOf(tea.KeyRight)) // on → off in the file
		m.Update(keyOf(tea.KeyRight)) // and back on
		_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
		cmdMsgs(cmd)
		m.Update(keyOf(tea.KeyEsc))
		if m.mouse {
			t.Error("a saved mouse field left as in the file undid m")
		}
		m.Update(runes("m")) // on for the session
		m.Update(runes("s"))
		goTo(t, m, "mouse")
		m.Update(keyOf(tea.KeyRight))
		_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
		cmdMsgs(cmd)
		m.Update(keyOf(tea.KeyEsc))
		data, _ := os.ReadFile(path)
		if m.mouse || !strings.Contains(string(data), "mouse: false") {
			t.Errorf("saved mouse off: mouse %v, file:\n%s", m.mouse, data)
		}
	})
}
