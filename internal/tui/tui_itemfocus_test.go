package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/RafaelXokito/daily-brief/internal/brief"
)

// focusJournal has a topic T of eight items between an intro and a
// Related part, with sources, and a second topic U of five.
func focusJournal() *brief.Journal {
	t := &brief.Section{Topic: "T", Status: brief.StatusDone,
		Markdown: "Intro paragraph before any item.\n\n" + filler(8) +
			"**Related**\n\n### Related story one\nRelated body. [source](https://r.example/1)",
		Sources: []brief.Source{{Title: "First source", URL: "https://s.example/1"}, {Title: "Second source", URL: "https://s.example/2"}}}
	u := &brief.Section{Topic: "U", Status: brief.StatusDone, Markdown: filler(5)}
	return journalWith(t, u)
}

func focusModel(t *testing.T, j *brief.Journal, w, h int, theme string) (*Model, *fakeIO) {
	t.Helper()
	m := New(nil, nil, j, theme)
	f := &fakeIO{}
	m.open = func(u string) error { f.opened = append(f.opened, u); return nil }
	m.copy = func(s string) error { f.copied = append(f.copied, s); return nil }
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m, f
}

var (
	keyDown = tea.KeyMsg{Type: tea.KeyDown}
	keyUp   = tea.KeyMsg{Type: tea.KeyUp}
)

// focused is the focused item's heading; the test fails if there is none.
func focused(t *testing.T, m *Model) heading {
	t.Helper()
	k := m.focusPos()
	if k < 0 {
		t.Fatalf("no focused item in %q", m.current().Topic)
	}
	return m.itemHeadings()[k]
}

// inView reports whether any line of h shows in the viewport.
func inView(m *Model, h heading) bool {
	top := m.viewport.YOffset
	return h.end >= top && h.line <= top+m.viewport.Height-1
}

// ↓ through a topic visits each item once, in order, with the focused
// item always at least partly in view, then scrolls on to Related and
// Sources; ↑ comes back the same way to the very top. Also at narrow
// widths and in the compact layout.
func TestItemFocusDownVisitsEveryItem(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		for _, size := range [][2]int{{120, 30}, {80, 14}, {50, 10}, {40, 8}, {24, 6}} {
			m, _ := focusModel(t, focusJournal(), size[0], size[1], "notty")
			m.Update(runes("2"))
			n := len(m.itemHeadings())
			if n != 9 {
				t.Errorf("%v: %d items, want 9", size, n)
				continue
			}
			visited := []int{m.focusPos()}
			sawSources, seen := false, false
			for range 2000 {
				m.Update(keyDown)
				if k := m.focusPos(); k != visited[len(visited)-1] {
					visited = append(visited, k)
				}
				// Below the intro the first item comes into view a line at
				// a time; from then on the focused item stays in view,
				// until ↓ scrolls past the last one.
				in := inView(m, m.itemHeadings()[m.focusPos()])
				if seen && !in && m.focusPos() != n-1 {
					t.Errorf("%v: focused item %d out of view at offset %d", size, m.focusPos(), m.viewport.YOffset)
				}
				seen = seen || in
				sawSources = sawSources || strings.Contains(ansi.Strip(m.viewport.View()), "Sources")
				checkFits(t, fmt.Sprintf("%v down", size), m)
				if m.viewport.AtBottom() && m.focusPos() == n-1 {
					break
				}
			}
			if fmt.Sprint(visited) != fmt.Sprint([]int{0, 1, 2, 3, 4, 5, 6, 7, 8}) {
				t.Errorf("%v: ↓ visited %v", size, visited)
			}
			if !m.viewport.AtBottom() || !sawSources {
				t.Errorf("%v: ↓ did not reach the Sources (bottom %v)", size, m.viewport.AtBottom())
			}
			for range 2000 {
				m.Update(keyUp)
				if k := m.focusPos(); k != visited[len(visited)-1] {
					visited = append(visited, k)
				}
				if m.viewport.YOffset == 0 && m.focusPos() == 0 {
					break
				}
			}
			if want := "[0 1 2 3 4 5 6 7 8 7 6 5 4 3 2 1 0]"; fmt.Sprint(visited) != want {
				t.Errorf("%v: ↓ then ↑ visited %v", size, visited)
			}
			if m.viewport.YOffset != 0 {
				t.Errorf("%v: ↑ did not reach the top: %d", size, m.viewport.YOffset)
			}
		}
	})
}

// An item taller than the viewport scrolls a line per ↓ until its end
// shows, then focus moves on; ↑ scrolls back a line at a time until its
// start shows.
func TestItemFocusTallItemScrollsLineByLine(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		var b strings.Builder
		b.WriteString("### Short first\nOne line.\n\n### Tall item\n")
		for i := range 30 {
			fmt.Fprintf(&b, "Paragraph %d of the tall item.\n\n", i)
		}
		b.WriteString("### After the tall one\nEnd.")
		j := journalWith(&brief.Section{Topic: "T", Status: brief.StatusDone, Markdown: b.String()})
		m, _ := focusModel(t, j, 100, 14, "notty")
		m.Update(runes("2"))
		m.Update(keyDown)
		tall := focused(t, m)
		if tall.item != 1 || tall.end-tall.line < 2*m.viewport.Height {
			t.Fatalf("focused %+v, viewport %d", tall, m.viewport.Height)
		}
		if m.viewport.YOffset != tall.line {
			t.Errorf("the tall item's start is not at the top: offset %d, start %d", m.viewport.YOffset, tall.line)
		}
		steps := 0
		for m.viewport.YOffset+m.viewport.Height-1 < tall.end {
			y := m.viewport.YOffset
			m.Update(keyDown)
			steps++
			if m.viewport.YOffset != y+1 || focused(t, m).item != 1 {
				t.Fatalf("step %d: offset %d after %d, item %d", steps, m.viewport.YOffset, y, focused(t, m).item)
			}
		}
		m.Update(keyDown)
		if focused(t, m).item != 2 {
			t.Errorf("focus did not move on once the tall item's end showed: %d", focused(t, m).item)
		}
		m.Update(keyUp)
		if focused(t, m).item != 1 {
			t.Fatalf("↑ did not go back to the tall item")
		}
		for m.viewport.YOffset > tall.line {
			y := m.viewport.YOffset
			m.Update(keyUp)
			if m.viewport.YOffset != y-1 || focused(t, m).item != 1 {
				t.Fatalf("↑: offset %d after %d, item %d", m.viewport.YOffset, y, focused(t, m).item)
			}
		}
		m.Update(keyUp)
		if focused(t, m).item != 0 {
			t.Errorf("focus did not move up once the tall item's start showed")
		}
	})
}

// An item ends on its last line with text: before the next item, the
// Related rule, the note after the digest or the Sources.
func TestItemEnds(t *testing.T) {
	lines := []string{"", " A", "", " body", "", "", " B", " b", "", " ────", "", " Related", "", " C", " c", "", " Sources", " x"}
	got := itemEnds(lines, []int{1, 6, 13}, []int{11, 16})
	if fmt.Sprint(got) != "[3 7 14]" {
		t.Errorf("itemEnds = %v, want [3 7 14]", got)
	}
	if got := itemEnds([]string{" A", "", ""}, []int{0}, nil); fmt.Sprint(got) != "[0]" {
		t.Errorf("one-line item at the end: %v", got)
	}

	for _, theme := range []string{"dark", "light", "notty"} {
		for _, w := range []int{30, 60, 120} {
			m, _ := richModel(t, w+sidebarWidth+2, 40, theme)
			m.Update(runes("2"))
			plain := strings.Split(ansi.Strip(m.content), "\n")
			hs := m.itemHeadings()
			if len(hs) != 3 {
				t.Fatalf("%s %d: %d items", theme, w, len(hs))
			}
			ends := map[int]string{0: "theregister.com", 1: "spring.io", 2: "Kotlin blog"}
			for k, h := range hs {
				if !strings.Contains(plain[h.end], ends[k]) {
					t.Errorf("%s %d: item %d ends on %q", theme, w, k, plain[h.end])
				}
				if k+1 < len(hs) && h.end >= hs[k+1].line {
					t.Errorf("%s %d: item %d runs into the next", theme, w, k)
				}
			}
		}
	}
}

// Each topic keeps its focused item: back at a topic, it is focused and
// in view.
func TestItemFocusRememberedPerTopic(t *testing.T) {
	m, _ := focusModel(t, focusJournal(), 100, 14, "notty")
	m.Update(runes("2"))
	for range 4 {
		m.Update(keyDown)
	}
	tItem := focused(t, m).item
	if tItem == 0 {
		t.Fatal("↓ did not move the focus")
	}
	m.Update(runes("3"))
	if focused(t, m).item != 0 {
		t.Errorf("U starts at item %d", focused(t, m).item)
	}
	m.Update(keyDown)
	m.Update(runes("2"))
	if h := focused(t, m); h.item != tItem || !inView(m, h) {
		t.Errorf("back in T: item %d (want %d), in view %v", h.item, tItem, inView(m, h))
	}
	m.Update(runes("3"))
	if focused(t, m).item != 1 {
		t.Errorf("back in U: item %d, want 1", focused(t, m).item)
	}
	// Resizing keeps the focus, in view.
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
	if h := focused(t, m); h.item != 1 || !inView(m, h) {
		t.Errorf("after resize: item %d, in view %v", h.item, inView(m, h))
	}
}

// Paging, half pages, top/bottom and the wheel scroll freely; once the
// focused item is out of view the first item in view is focused.
func TestItemFocusReanchorsAfterPaging(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		m, _ := focusModel(t, focusJournal(), 100, 14, "notty")
		m.WithMouse(true)
		m.Update(runes("2"))
		// After a free scroll the focus stays if its item is in view, and
		// is otherwise the first item in view, if any.
		step := func(label string, msg tea.Msg) {
			before := focused(t, m)
			m.Update(msg)
			after := focused(t, m)
			if inView(m, before) {
				if after.item != before.item {
					t.Errorf("%s: focus moved from %d to %d though it stayed in view", label, before.item, after.item)
				}
				return
			}
			for _, o := range m.itemHeadings() {
				if inView(m, o) {
					if after.item != o.item {
						t.Errorf("%s: focused %d, want %d, the first in view", label, after.item, o.item)
					}
					return
				}
			}
		}
		for _, k := range []tea.KeyMsg{runes(" "), runes(" "), runes("b"), runes("d"), runes("d"), runes("u"), runes("G"), runes("g"),
			{Type: tea.KeyPgDown}, {Type: tea.KeyPgDown}, {Type: tea.KeyPgUp}} {
			step(k.String(), k)
		}
		m.Update(runes("g"))
		if focused(t, m).item != 0 {
			t.Errorf("g: item %d", focused(t, m).item)
		}
		m.View()
		for range 6 {
			step("wheel", wheel(sidebarWidth+5, m.bodyTop+1, 1))
		}
		if focused(t, m).item == 0 {
			t.Errorf("the wheel never moved the focus: offset %d", m.viewport.YOffset)
		}
	})
}

// o and y act on the focused item, however the viewport scrolled.
func TestItemFocusOpenCopyUseFocusedItem(t *testing.T) {
	m, f := focusModel(t, focusJournal(), 100, 14, "notty")
	m.WithMouse(true)
	m.Update(runes("2"))
	items := m.current().Items()
	m.Update(keyDown)
	m.Update(keyDown)
	m.Update(keyDown)
	want := items[focused(t, m).item]
	// A wheel step that keeps the item in view leaves it focused.
	m.View()
	m.Update(wheel(sidebarWidth+5, m.bodyTop+1, 1))
	if inView(m, focused(t, m)) {
		want = items[focused(t, m).item]
	}
	m.Update(runes("o"))
	m.Update(runes("y"))
	if len(f.opened) != 1 || f.opened[0] != want.URL || len(f.copied) != 1 || !strings.HasPrefix(f.copied[0], want.Headline) {
		t.Errorf("want %q (%s): opened %v, copied %q", want.Headline, want.URL, f.opened, f.copied)
	}
	// Scrolled so the item's heading is above the top, it is still the one.
	h := focused(t, m)
	m.viewport.SetYOffset(h.line + 1)
	m.Update(runes("o"))
	if got := f.opened[len(f.opened)-1]; got != items[h.item].URL {
		t.Errorf("mid-item opened %s, want %s", got, items[h.item].URL)
	}
	m.Update(runes("G"))
	m.Update(runes("y"))
	if got := f.copied[len(f.copied)-1]; !strings.HasPrefix(got, items[focused(t, m).item].Headline) {
		t.Errorf("after G copied %q, focused %q", got, items[focused(t, m).item].Headline)
	}
}

// A click on an item's lines focuses it; on the topic heading, Related
// or the Sources it only focuses the pane.
func TestItemFocusClick(t *testing.T) {
	for _, reader := range []bool{false, true} {
		m, f := richModel(t, 120, 40, "dark")
		m.WithMouse(true)
		m.Update(runes("2"))
		if reader {
			m.Update(runes("z"))
		}
		m.focus = focusTopics
		view := m.View()
		x := 0
		if !reader {
			x = sidebarWidth + 2
		}
		y := rowContaining(view, x, 1<<20, "Since Mon")
		if y < 0 {
			t.Fatalf("no Spring body row in\n%s", ansi.Strip(view))
		}
		m.Update(click(x+5, y))
		if m.focus != focusSummary || focused(t, m).item != 1 {
			t.Errorf("reader %v: click on Spring's body: focus %d, item %d", reader, m.focus, focused(t, m).item)
		}
		for _, text := range []string{"Related", "Sources", "Java & Quarkus"} {
			view = m.View()
			if y := rowContaining(view, x+1, 1<<20, text); y >= 0 {
				m.Update(click(x+3, y))
				if focused(t, m).item != 1 {
					t.Errorf("reader %v: click on %q focused item %d", reader, text, focused(t, m).item)
				}
			} else {
				t.Errorf("reader %v: no %q row", reader, text)
			}
		}
		y = rowContaining(m.View(), x, 1<<20, "Kotlin 2.3")
		m.Update(click(x+4, y))
		m.Update(runes("o"))
		if focused(t, m).item != 2 || len(f.opened) != 1 || f.opened[0] != "https://kotlinlang.org/x" {
			t.Errorf("reader %v: click on Kotlin: item %d, opened %v", reader, focused(t, m).item, f.opened)
		}
	}
}

// focusGolden is the Summary of a two-item topic in the notty theme with
// the second item focused: ">" in the margin of each of its lines.
var focusGolden = []string{
	"",
	" Two items",
	"",
	"",
	" One",
	"",
	" Alpha.",
	"",
	"",
	">Two",
	">",
	">Beta, which wraps onto a",
	">second line here.",
	"",
	" **Sources**",
	"",
	" • Src · s.example",
}

func twoItems() *brief.Journal {
	return journalWith(&brief.Section{Topic: "Two items", Status: brief.StatusDone,
		Markdown: "### One\nAlpha.\n\n### Two\nBeta, which wraps onto a second line here.",
		Sources:  []brief.Source{{Title: "Src", URL: "https://s.example"}}})
}

// The marker in the notty theme, by line; in the dark theme an accent
// "▎" that resets only the foreground, before any hyperlink.
func TestItemFocusMarkerGolden(t *testing.T) {
	m, _ := focusModel(t, twoItems(), 26+sidebarWidth+2, 30, "notty")
	m.Update(runes("2"))
	m.Update(keyDown)
	var got []string
	for _, l := range strings.Split(m.viewport.View(), "\n") {
		got = append(got, strings.TrimRight(ansi.Strip(l), " "))
	}
	for len(got) > 0 && got[len(got)-1] == "" {
		got = got[:len(got)-1]
	}
	if strings.Join(got, "\n") != strings.Join(focusGolden, "\n") {
		t.Errorf("notty view:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(focusGolden, "\n"))
	}
	if strings.Contains(m.content, ">Two") {
		t.Error("the marker is in the cached content")
	}

	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	const mark = "\x1b[38;2;169;155;255m▎\x1b[39m"
	if got := focusMark(themeDark); got != mark {
		t.Fatalf("dark mark %q, want %q", got, mark)
	}
	if got := focusMark(themeLight); got != "\x1b[38;2;89;79;207m▎\x1b[39m" {
		t.Errorf("light mark %q", got)
	}
	d, _ := focusModel(t, twoItems(), 26+sidebarWidth+2, 30, "dark")
	d.Update(runes("2"))
	lines := strings.Split(d.viewport.View(), "\n")
	h := focused(t, d)
	for i, l := range lines {
		in := i >= h.line && i <= h.end
		if strings.HasPrefix(l, mark) != in || strings.Count(l, "▎") != map[bool]int{true: 1, false: 0}[in] {
			t.Errorf("dark line %d (item %v): %q", i, in, l)
		}
		if w := lipgloss.Width(l); w > d.viewport.Width {
			t.Errorf("dark line %d is %d cells", i, w)
		}
	}
	if got := strings.TrimRight(ansi.Strip(lines[h.line]), " "); got != "▎One" {
		t.Errorf("dark first line %q", got)
	}
	// Moving the focus never renders again: the content is the same string.
	before := d.content
	d.Update(keyDown)
	if d.content != before {
		t.Error("moving the focus changed the rendered content")
	}
	// Reader mode shows the marker too.
	d.Update(runes("z"))
	if !strings.Contains(d.View(), mark+"\x1b") && !strings.Contains(d.View(), mark+"Two") {
		t.Errorf("no marker in reader mode:\n%s", ansi.Strip(d.View()))
	}
}

// With the marker on items full of links, every line of the View still
// balances its hyperlinks and holds only SGR and OSC 8, at every focus and
// scroll position, in every theme and at narrow widths.
func TestItemFocusMarkerKeepsLinksBalanced(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		for _, theme := range []string{"dark", "light", "notty"} {
			for _, size := range [][2]int{{120, 20}, {60, 12}, {30, 8}} {
				m, _ := richModel(t, size[0], size[1], theme)
				m.WithMouse(true)
				m.Update(runes("2"))
				for i := 0; i < 60 && !m.viewport.AtBottom(); i++ {
					label := fmt.Sprintf("%s %v step %d", theme, size, i)
					checkViewLines(t, label, m.View())
					checkLinkLines(t, m.viewport.View())
					checkFits(t, label, m)
					m.Update(keyDown)
				}
				m.Update(runes("z"))
				checkViewLines(t, theme+" reader", m.View())
			}
		}
	})
}

// The help line in a topic says ↑/↓ move between items.
func TestItemFocusHelpLine(t *testing.T) {
	m, _ := focusModel(t, focusJournal(), 200, 30, "notty")
	m.Update(runes("2"))
	if v := ansi.Strip(m.View()); !strings.Contains(v, "↑/↓ item") || strings.Contains(v, "↑/↓ scroll") {
		t.Errorf("help line: %q", v[strings.LastIndex(v, "\n")+1:])
	}
	m.Update(runes("1"))
	if v := ansi.Strip(m.View()); !strings.Contains(v, "↑/↓ scroll") {
		t.Errorf("Overview help line: %q", v[strings.LastIndex(v, "\n")+1:])
	}
}

// fillerOrder is filler's items numbered as given, in that order.
func fillerOrder(nums ...int) string {
	var b strings.Builder
	for _, i := range nums {
		fmt.Fprintf(&b, "### Filler story number %d about nothing\nA sentence of padding text here. [source](https://f.example/%d)\n\n", i, i)
	}
	return b.String()
}

// longSources is a topic of three items with a long list of sources.
func longSources() *brief.Journal {
	var src []brief.Source
	for i := range 30 {
		src = append(src, brief.Source{Title: fmt.Sprintf("Source %d", i), URL: fmt.Sprintf("https://s.example/%d", i)})
	}
	var intro strings.Builder
	for i := range 20 {
		fmt.Fprintf(&intro, "Intro paragraph %d.\n\n", i)
	}
	return journalWith(
		&brief.Section{Topic: "T", Status: brief.StatusDone, Markdown: filler(3), Sources: src},
		&brief.Section{Topic: "I", Status: brief.StatusDone, Markdown: intro.String() + filler(3)})
}

// With no item in view after a free scroll, the nearest item above is
// focused, else the nearest below: o acts on it, and ↓ in the Sources
// scrolls on rather than jumping back up.
func TestItemFocusReanchorWithNoItemInView(t *testing.T) {
	m, f := focusModel(t, longSources(), 100, 14, "notty")
	m.Update(runes("2"))
	items := m.current().Items()
	m.Update(runes("G"))
	h := focused(t, m)
	if inView(m, h) || h.item != 2 {
		t.Fatalf("G: focused %d, in view %v", h.item, inView(m, h))
	}
	m.Update(runes("o"))
	if len(f.opened) != 1 || f.opened[0] != items[2].URL {
		t.Errorf("o in the Sources opened %v, want %s", f.opened, items[2].URL)
	}
	m.Update(runes("b"))
	y := m.viewport.YOffset
	for range 3 {
		m.Update(keyDown)
		if m.viewport.YOffset != y+1 || focused(t, m).item != 2 {
			t.Fatalf("↓ in the Sources: offset %d after %d, item %d", m.viewport.YOffset, y, focused(t, m).item)
		}
		y = m.viewport.YOffset
	}

	// Above every item, in a long intro: the first item below.
	m.Update(runes("3"))
	m.Update(runes("G"))
	if h := focused(t, m); h.item == 0 || !inView(m, h) {
		t.Fatalf("G in I: item %d, in view %v", h.item, inView(m, h))
	}
	m.Update(runes("g"))
	if h := focused(t, m); h.item != 0 || inView(m, h) {
		t.Errorf("g above the items: item %d, in view %v", h.item, inView(m, h))
	}
}

// Resizing or reader mode with the focused item out of view keeps the
// offset rather than snapping back to the item.
func TestItemFocusResizeOutOfViewKeepsOffset(t *testing.T) {
	m, _ := focusModel(t, longSources(), 100, 14, "notty")
	m.Update(runes("2"))
	m.Update(runes("G"))
	m.Update(runes("b"))
	y := m.viewport.YOffset
	if inView(m, focused(t, m)) {
		t.Fatal("the focused item is in view")
	}
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 14})
	if m.viewport.YOffset != y {
		t.Errorf("resize: offset %d, want %d", m.viewport.YOffset, y)
	}
	m.Update(runes("z"))
	if m.viewport.YOffset != y || inView(m, focused(t, m)) {
		t.Errorf("reader mode: offset %d, want %d", m.viewport.YOffset, y)
	}
	m.Update(runes("z"))
	if m.viewport.YOffset != y {
		t.Errorf("reader mode off: offset %d, want %d", m.viewport.YOffset, y)
	}
}

// The focus follows its topic and headline when a run replaces the
// journal with topics and items in another order, and falls back to the
// first item when the headline is gone.
func TestItemFocusSurvivesNewJournal(t *testing.T) {
	m, _ := focusModel(t, journalWith(
		&brief.Section{Topic: "A", Status: brief.StatusDone, Markdown: filler(4)},
		&brief.Section{Topic: "B", Status: brief.StatusDone, Markdown: filler(4)}), 100, 30, "notty")
	m.Update(runes("3"))
	m.Update(keyDown)
	m.Update(keyDown)
	want := "Filler story number 2 about nothing"
	if it, _ := m.currentItem(); it.Headline != want {
		t.Fatalf("focused %q", it.Headline)
	}
	m.running = journalWith(
		&brief.Section{Topic: "B", Status: brief.StatusDone, Markdown: fillerOrder(3, 2, 1, 0)},
		&brief.Section{Topic: "A", Status: brief.StatusDone, Markdown: filler(4)},
		&brief.Section{Topic: "C", Status: brief.StatusDone, Markdown: filler(2)})
	m.generating = true
	m.Update(doneMsg{})
	m.Update(runes("2"))
	if it, _ := m.currentItem(); m.current().Topic != "B" || it.Headline != want || focused(t, m).item != 1 {
		t.Errorf("after the run: %q focused %q (item %d)", m.current().Topic, it.Headline, focused(t, m).item)
	}
	m.Update(runes("3"))
	if focused(t, m).item != 0 {
		t.Errorf("A: item %d", focused(t, m).item)
	}
	m.Update(runes("1"))
	m.running = journalWith(&brief.Section{Topic: "B", Status: brief.StatusDone, Markdown: fillerOrder(7, 8)})
	m.generating = true
	m.Update(doneMsg{})
	m.Update(runes("2"))
	if it, _ := m.currentItem(); focused(t, m).item != 0 || it.Headline != "Filler story number 7 about nothing" {
		t.Errorf("headline gone: item %d, %q", focused(t, m).item, it.Headline)
	}
}

// "**Sources**" and "**Related**" inside a fenced code block neither end
// an item nor start a heading.
func TestItemEndsIgnoreFencedStops(t *testing.T) {
	md := "### One\nBefore.\n\n```\n**Sources**\n**Related**\n## Not a heading\n```\n\nAfter the code.\n\n### Two\nLast."
	for _, theme := range []string{"dark", "notty"} {
		m, _ := focusModel(t, journalWith(&brief.Section{Topic: "T", Status: brief.StatusDone, Markdown: md}), 100, 40, theme)
		m.Update(runes("2"))
		plain := strings.Split(ansi.Strip(m.content), "\n")
		hs := m.itemHeadings()
		if len(hs) != 2 || len(m.headings) != 2 {
			t.Fatalf("%s: %d items, %d headings", theme, len(hs), len(m.headings))
		}
		if !strings.Contains(plain[hs[0].end], "After the code.") {
			t.Errorf("%s: item One ends on %q", theme, plain[hs[0].end])
		}
		if !strings.Contains(ansi.Strip(m.content), "**Sources**") {
			t.Errorf("%s: the fenced **Sources** was rewritten", theme)
		}
	}
}

// At the first item ↑ scrolls a line at a time up to the top, so a
// jittery wheel sent as arrows still makes progress.
func TestItemFocusUpAtFirstItemIsGradual(t *testing.T) {
	m, _ := focusModel(t, focusJournal(), 100, 14, "notty")
	m.Update(runes("2"))
	first := m.itemHeadings()[0]
	if first.line < 3 {
		t.Fatalf("first item at line %d", first.line)
	}
	m.viewport.SetYOffset(first.line)
	for y := first.line - 1; y >= 0; y-- {
		m.Update(keyUp)
		if m.viewport.YOffset != y || focused(t, m).item != 0 {
			t.Fatalf("↑: offset %d want %d, item %d", m.viewport.YOffset, y, focused(t, m).item)
		}
	}
	m.Update(keyUp)
	if m.viewport.YOffset != 0 {
		t.Errorf("↑ at the top: offset %d", m.viewport.YOffset)
	}
}
