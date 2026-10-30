package tui

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/RafaelXokito/daily-brief/internal/brief"
	"github.com/RafaelXokito/daily-brief/internal/config"
)

// longMarkdown is about 200 lines, with one line too long to wrap.
func longMarkdown(topic string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### %s\n\n```\n%s\n```\n\n", topic, strings.Repeat("x", 300))
	for i := range 200 {
		fmt.Fprintf(&b, "- %s item %d\n", topic, i)
	}
	return b.String()
}

// longModel shows a long overview and two long topics.
func longModel(t *testing.T, width int) *Model {
	t.Helper()
	j := &brief.Journal{Date: "2026-10-29", TokenBudget: 400_000,
		Overview: brief.Section{Topic: "Overview", Status: brief.StatusDone, Markdown: longMarkdown("Overview")},
		Sections: []*brief.Section{
			{Topic: "A", Status: brief.StatusDone, Markdown: longMarkdown("A")},
			{Topic: "B", Status: brief.StatusDone, Markdown: longMarkdown("B")},
		}}
	m := New(nil, nil, j, "notty")
	m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
	return m
}

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func TestFocusStartsOnSummaryAndArrowsScroll(t *testing.T) {
	m := longModel(t, 100)
	if m.focus != focusSummary {
		t.Fatal("focus does not start on the Summary pane")
	}
	for i := 1; i <= 3; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
		if m.viewport.YOffset != i || m.selected != 0 {
			t.Fatalf("down %d: YOffset %d, selected %d", i, m.viewport.YOffset, m.selected)
		}
	}
	m.Update(runes("k"))
	if m.viewport.YOffset != 2 || m.selected != 0 {
		t.Errorf("k: YOffset %d, selected %d", m.viewport.YOffset, m.selected)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.viewport.YOffset != 0 || m.selected != 0 {
		t.Errorf("up at the top: YOffset %d, selected %d", m.viewport.YOffset, m.selected)
	}
}

func TestTopicsPaneChangesTopicAndKeepsFocus(t *testing.T) {
	for _, keys := range [][2]tea.KeyMsg{
		{{Type: tea.KeyLeft}, {Type: tea.KeyRight}},
		{{Type: tea.KeyLeft}, {Type: tea.KeyEnter}},
		{runes("h"), runes("l")},
	} {
		toTopics, toSummary := keys[0], keys[1]
		m := longModel(t, 100)
		m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
		m.Update(toTopics)
		if m.focus != focusTopics {
			t.Fatalf("%s did not focus Topics", toTopics)
		}
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
		if m.selected != 1 || m.viewport.YOffset != 0 || m.focus != focusTopics {
			t.Errorf("down in Topics: selected %d, YOffset %d, focus %d", m.selected, m.viewport.YOffset, m.focus)
		}
		m.Update(runes("j"))
		m.Update(runes("j"))
		if m.selected != 0 {
			t.Errorf("j past the last topic: selected %d, want a wrap to 0", m.selected)
		}
		m.Update(runes("k"))
		if m.selected != 2 || m.focus != focusTopics {
			t.Errorf("k from the first topic: selected %d, want a wrap to 2", m.selected)
		}
		m.Update(toSummary)
		if m.focus != focusSummary || m.selected != 2 {
			t.Errorf("%s: focus %d, selected %d", toSummary, m.focus, m.selected)
		}
	}

	m := longModel(t, 100)
	for i, k := range []tea.KeyMsg{{Type: tea.KeyTab}, {Type: tea.KeyTab}, {Type: tea.KeyShiftTab}, {Type: tea.KeyShiftTab}} {
		m.Update(k)
		if want := []pane{focusTopics, focusSummary, focusTopics, focusSummary}[i]; m.focus != want {
			t.Errorf("%s #%d: focus %d, want %d", k, i, m.focus, want)
		}
	}
}

func TestScrollKeysInBothPanes(t *testing.T) {
	for _, focus := range []pane{focusSummary, focusTopics} {
		m := longModel(t, 100)
		page, half := m.viewport.Height, m.viewport.Height/2
		for _, c := range []struct {
			key   tea.KeyMsg
			delta int
		}{
			{tea.KeyMsg{Type: tea.KeyPgDown}, page}, {tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}, page}, {runes("f"), page},
			{tea.KeyMsg{Type: tea.KeyCtrlD}, half}, {runes("d"), half}, {tea.KeyMsg{Type: tea.KeyShiftDown}, page},
			{tea.KeyMsg{Type: tea.KeyPgUp}, -page}, {runes("b"), -page}, {tea.KeyMsg{Type: tea.KeyCtrlU}, -half},
			{runes("u"), -half}, {tea.KeyMsg{Type: tea.KeyShiftUp}, -page},
		} {
			m.viewport.SetYOffset(60)
			m.focus = focus
			m.Update(c.key)
			if got := m.viewport.YOffset - 60; got != c.delta || m.selected != 0 || m.focus != focus {
				t.Errorf("focus %d, %q: moved %d, want %d (selected %d, focus %d)", focus, c.key, got, c.delta, m.selected, m.focus)
			}
		}
		for _, bottom := range []tea.KeyMsg{runes("G"), {Type: tea.KeyEnd}} {
			for _, top := range []tea.KeyMsg{runes("g"), {Type: tea.KeyHome}} {
				m.viewport.SetYOffset(10)
				m.Update(bottom)
				if !m.viewport.AtBottom() || m.viewport.YOffset == 10 {
					t.Errorf("focus %d, %q: not at the bottom", focus, bottom)
				}
				m.Update(top)
				if m.viewport.YOffset != 0 || m.selected != 0 {
					t.Errorf("focus %d, %q: YOffset %d", focus, top, m.viewport.YOffset)
				}
			}
		}
	}
}

func TestLeftRightNeverScrollSideways(t *testing.T) {
	m := longModel(t, 100)
	// Rendering never makes a line wider than the pane: give the viewport
	// one that is, as it would scroll sideways.
	m.viewport.SetContent(strings.Repeat("0123456789", 30))
	m.viewport.SetHorizontalStep(4)
	// The viewport alone would scroll this content sideways on →.
	if vp, _ := m.viewport.Update(tea.KeyMsg{Type: tea.KeyRight}); vp.View() == m.viewport.View() {
		t.Fatal("test content does not scroll sideways in the bare viewport")
	}
	before := m.viewport.View()
	for _, k := range []tea.KeyMsg{{Type: tea.KeyRight}, {Type: tea.KeyLeft}, {Type: tea.KeyRight}, {Type: tea.KeyRight},
		runes("l"), runes("l"), {Type: tea.KeyLeft}, {Type: tea.KeyLeft}} {
		m.Update(k)
		if m.viewport.View() != before || m.viewport.HorizontalScrollPercent() != 0 {
			t.Fatalf("%q scrolled the viewport sideways", k)
		}
	}
}

func TestNumberKeysJumpToSections(t *testing.T) {
	for _, focus := range []pane{focusSummary, focusTopics} {
		m := longModel(t, 100)
		m.focus = focus
		m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
		m.Update(runes("3"))
		if m.selected != 2 || m.focus != focus || m.viewport.YOffset != 0 {
			t.Errorf("3: selected %d, focus %d, YOffset %d", m.selected, m.focus, m.viewport.YOffset)
		}
		m.Update(runes("1"))
		if m.selected != 0 {
			t.Errorf("1: selected %d", m.selected)
		}
		m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
		y := m.viewport.YOffset
		for _, k := range []string{"4", "9"} {
			m.Update(runes(k))
			if m.selected != 0 || m.viewport.YOffset != y {
				t.Errorf("%s with 3 sections: selected %d, YOffset %d", k, m.selected, m.viewport.YOffset)
			}
		}
	}
}

func TestHelpOverlayAndEsc(t *testing.T) {
	m := longModel(t, 120)
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	normal := m.View()
	for _, closer := range []tea.KeyMsg{runes("?"), {Type: tea.KeyEsc}} {
		m.Update(runes("?"))
		v := m.View()
		if strings.Contains(v, "Overview item 5") {
			t.Errorf("overlay does not replace the summary:\n%s", v)
		}
		for _, b := range keys.all() {
			if !strings.Contains(v, keyNames(b)) || !strings.Contains(v, b.Help().Desc) {
				t.Errorf("overlay lacks %q %q:\n%s", keyNames(b), b.Help().Desc, v)
			}
		}
		// Other keys are ignored while it is open.
		for _, k := range []tea.KeyMsg{{Type: tea.KeyDown}, {Type: tea.KeyLeft}, runes("3"), runes("G"), runes("r")} {
			if _, cmd := m.Update(k); cmd != nil || m.selected != 0 || m.focus != focusSummary || m.viewport.YOffset != 1 || !m.showHelp {
				t.Errorf("%q with the overlay open changed the model", k)
			}
		}
		if _, cmd := m.Update(closer); cmd != nil || m.showHelp || m.focus != focusSummary {
			t.Errorf("%q did not just close the overlay", closer)
		}
		if v := m.View(); v != normal {
			t.Errorf("%q did not restore the view:\n%s", closer, v)
		}
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc}); cmd != nil || m.focus != focusTopics {
		t.Errorf("esc with no overlay: focus %d, cmd %v", m.focus, cmd != nil)
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc}); cmd != nil || m.focus != focusTopics {
		t.Error("esc in Topics quit or moved focus")
	}
	for _, k := range []tea.KeyMsg{runes("q"), {Type: tea.KeyCtrlC}} {
		m := longModel(t, 100)
		if _, cmd := m.Update(k); cmd == nil {
			t.Errorf("%q did not quit", k)
		} else if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("%q did not quit", k)
		}
	}
}

func TestHelpLineFollowsFocusAndViewFits(t *testing.T) {
	for _, width := range []int{140, 100, 70, 60} {
		m := longModel(t, width)
		for _, c := range []struct {
			key        tea.KeyMsg
			want, not  string
			focusTitle string
		}{
			{tea.KeyMsg{Type: tea.KeyLeft}, "→ read", "← topics", "Topics"},
			{tea.KeyMsg{Type: tea.KeyRight}, "← topics", "→ read", "Overview"},
		} {
			m.Update(c.key)
			v := m.View()
			lines := strings.Split(v, "\n")
			help := lines[len(lines)-1]
			if len(lines) > 30 {
				t.Errorf("width %d: view is %d lines, the terminal %d", width, len(lines), 30)
			}
			if width >= 100 && (!strings.Contains(help, c.want) || strings.Contains(help, c.not) || !strings.Contains(help, "? help")) {
				t.Errorf("width %d, %q: help line %q", width, c.key, help)
			}
			if width < 70 && !strings.HasSuffix(help, "… ? help") {
				t.Errorf("width %d: help line %q is not cut to end with \"… ? help\"", width, help)
			}
			if !strings.Contains(lines[2], c.focusTitle) || !strings.Contains(lines[2], "Topics") {
				t.Errorf("width %d: pane titles %q", width, lines[2])
			}
			for i, l := range lines {
				if w := lipgloss.Width(l); w > width {
					t.Errorf("width %d, %q: line %d is %d cells: %q", width, c.key, i, w, l)
				}
			}
			m.Update(runes("?"))
			for i, l := range strings.Split(m.View(), "\n") {
				if w := lipgloss.Width(l); w > width {
					t.Errorf("width %d, overlay: line %d is %d cells: %q", width, i, w, l)
				}
			}
			m.Update(runes("?"))
		}
	}
	for _, mode := range []helpMode{modeSummary, modeTopics, modeHighlights, modeItems, modeItemsBack} {
		if l := helpLine(mode, true, 200); !strings.HasSuffix(l, "? help · s settings · q quit (cancels run)") || strings.Contains(l, "regenerate") {
			t.Errorf("help line during a run: %q", l)
		}
	}
	for mode, want := range map[helpMode]string{
		modeTopics:     "↑/↓ topic · → read · space/b page · z reader · ? help · s settings · r regenerate · q quit",
		modeSummary:    "↑/↓ scroll · space/b page · ← topics · z reader · ? help · s settings · r regenerate · q quit",
		modeHighlights: "↑/↓ highlight · enter go · o open · y copy · ← topics · z reader · ? help · s settings · r regenerate · q quit",
		modeItems:      "↑/↓ item · space/b page · o open · y copy · ← topics · z reader · ? help · s settings · r regenerate · q quit",
		modeItemsBack:  "↑/↓ item · space/b page · ⌫ back · o open · y copy · ← topics · z reader · ? help · s settings · r regenerate · q quit",
	} {
		if l := helpLine(mode, false, 200); l != want {
			t.Errorf("help line %d: %q, want %q", mode, l, want)
		}
	}
}

// The key table lists every binding once and no key twice, with o, y and
// z for the links and reader mode, and b still paging up.
func TestKeyTable(t *testing.T) {
	all := keys.all()
	if n := reflect.TypeOf(keys).NumField(); len(all) != n {
		t.Errorf("keys.all() lists %d of %d bindings", len(all), n)
	}
	seen := map[string]bool{}
	for _, b := range all {
		for _, k := range b.Keys() {
			if seen[k] {
				t.Errorf("key %q is bound twice", k)
			}
			seen[k] = true
		}
	}
	for k, b := range map[string]key.Binding{"o": keys.Open, "y": keys.Copy, "z": keys.Reader, "b": keys.PageUp, "backspace": keys.Back, "enter": keys.Go} {
		if !slices.Contains(b.Keys(), k) {
			t.Errorf("key %q is not bound to %q", k, b.Help().Desc)
		}
	}
}

// Navigation works while a run streams updates, and the updates never
// move focus or scroll.
func TestNavigationDuringRun(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := config.Default()
	cfg.Provider.Type = "anthropic"
	cfg.TokenBudget = 400_000
	cfg.Concurrency = 1
	cfg.Topics = []config.Topic{{Name: "A"}, {Name: "B"}}
	b := &gatedBackend{started: make(chan struct{}, 1)}
	m := New(brief.NewGenerator(cfg, b), nil, nil, "notty")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	wait := m.start()
	<-b.started

	stream := func(i int, status brief.Status) {
		m.Update(updateMsg(brief.Update{Index: i, Section: brief.Section{Topic: m.journal.Sections[i].Topic, Status: status, Markdown: longMarkdown("A")}}))
	}
	stream(0, brief.StatusRunning)
	m.Update(runes("2"))
	for range 3 {
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	y := m.viewport.YOffset
	if m.selected != 1 || m.focus != focusSummary || y != 3+m.viewport.Height {
		t.Fatalf("during a run: selected %d, focus %d, YOffset %d", m.selected, m.focus, y)
	}
	stream(0, brief.StatusDone)
	stream(1, brief.StatusRunning)
	if m.selected != 1 || m.focus != focusSummary || m.viewport.YOffset != y {
		t.Errorf("updates moved the view: selected %d, focus %d, YOffset %d, want %d", m.selected, m.focus, m.viewport.YOffset, y)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	stream(1, brief.StatusDone)
	if m.selected != 2 || m.focus != focusTopics || m.viewport.YOffset != 0 {
		t.Errorf("after an update in Topics: selected %d, focus %d, YOffset %d", m.selected, m.focus, m.viewport.YOffset)
	}
	if h := helpLine(m.helpMode(), m.generating, 200); !strings.Contains(m.View(), "q quit (cancels run)") || !strings.Contains(h, "→ read") {
		t.Errorf("help line during a run: %q", h)
	}

	_, cmd := m.Update(runes("q"))
	if cmd == nil {
		t.Fatal("q during a run returned no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("q during a run did not quit")
	}
	drain(t, m, wait)
}
