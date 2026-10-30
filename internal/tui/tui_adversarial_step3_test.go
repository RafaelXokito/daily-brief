package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/RafaelXokito/daily-brief/internal/brief"
	"github.com/RafaelXokito/daily-brief/internal/config"
)

// hlModel shows j with fake opener and clipboard.
func hlModel(t *testing.T, j *brief.Journal, w, h int) (*Model, *fakeIO) {
	t.Helper()
	m := New(nil, nil, j, "dark")
	f := &fakeIO{}
	m.open = func(u string) error { f.opened = append(f.opened, u); return nil }
	m.copy = func(s string) error { f.copied = append(f.copied, s); return nil }
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m, f
}

// oneHighlight is a journal whose Overview has a single highlight naming
// headline in topic.
func oneHighlight(topic, headline string, secs ...*brief.Section) *brief.Journal {
	return &brief.Journal{Date: "2026-10-29", Sections: secs,
		Overview: brief.Section{Topic: "Overview", Status: brief.StatusDone, Markdown: "## Today's highlights\n\n- x",
			Highlights: []brief.Highlight{{Tier: "act", Topic: topic, Headline: headline, Text: "Do the thing.", Why: "Because."}}}}
}

func filler(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "### Filler story number %d about nothing\nA sentence of padding text here. [source](https://f.example/%d)\n\n", i, i)
	}
	return b.String()
}

// topLine is the first non-blank line the viewport shows, without styling.
func topLine(m *Model) string {
	return strings.TrimSpace(ansi.Strip(strings.Split(m.viewport.View(), "\n")[0]))
}

// Enter lands with the target heading on the viewport's top line, for
// headings that wrap, carry markdown or links, or are updates.
func TestAdv_JumpLandsOnHeading(t *testing.T) {
	cases := []struct {
		name, heading, headline, want string
	}{
		{"wrapping", "### " + strings.Repeat("Extremely long headline words that wrap ", 4) + "end",
			strings.Repeat("Extremely long headline words that wrap ", 4) + "end", "Extremely long"},
		{"markdown", "### Log4j **2.25.1** fixes `CVE-2026-1` per [NVD advisory](https://nvd.example/x) [source](https://a.example)",
			"Log4j 2.25.1 fixes CVE-2026-1 per NVD advisory", "Log4j 2.25.1"},
		{"update", "### Update: Spring Boot 4 milestone slips again", "Spring Boot 4 milestone slips again", "↻ Update · Spring"},
		{"bold-line", "**Kafka 4.2 drops ZooKeeper entirely** — big change. [source](https://k.example)", "Kafka 4.2 drops ZooKeeper entirely", "Kafka 4.2"},
	}
	for _, c := range cases {
		for _, size := range [][2]int{{60, 14}, {80, 14}, {120, 20}, {200, 12}} {
			md := filler(4) + c.heading + "\nBody of the target.\n\n" + filler(3)
			j := oneHighlight("T", c.headline, &brief.Section{Topic: "T", Status: brief.StatusDone, Markdown: md})
			m, _ := hlModel(t, j, size[0], size[1])
			m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			if m.selected != 1 || !strings.HasPrefix(topLine(m), c.want) {
				t.Errorf("%s %v: selected %d, top line %q, want %q…", c.name, size, m.selected, topLine(m), c.want)
			}
		}
	}
}

// The same headline in two topics: Enter goes to the highlight's topic.
func TestAdv_JumpDuplicateHeadlineAcrossTopics(t *testing.T) {
	h := "OpenSSL 3.6 fixes a high severity flaw"
	a := &brief.Section{Topic: "A", Status: brief.StatusDone, Markdown: filler(2) + "### " + h + "\nIn A. [source](https://a.example)"}
	b := &brief.Section{Topic: "B", Status: brief.StatusDone, Markdown: filler(5) + "### " + h + "\nIn B. [source](https://b.example)\n\n" + filler(3)}
	m, f := hlModel(t, oneHighlight("B", h, a, b), 100, 14)
	m.Update(runes("o"))
	if len(f.opened) != 1 || f.opened[0] != "https://b.example" {
		t.Errorf("opened %v", f.opened)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.selected != 2 || !strings.HasPrefix(topLine(m), "OpenSSL") {
		t.Errorf("selected %d, top %q", m.selected, topLine(m))
	}
	m.Update(runes("o"))
	if f.opened[len(f.opened)-1] != "https://b.example" {
		t.Errorf("o after the jump opened %v", f.opened)
	}
}

// Enter on a highlight of a topic's last item cannot scroll its heading
// to the top (the viewport stops at the end of the content), yet o and y
// act on the item jumped to, not an earlier one.
func TestAdv_OpenAfterJumpToLastItem(t *testing.T) {
	h := "OpenSSL 3.6 fixes a high severity flaw"
	b := &brief.Section{Topic: "B", Status: brief.StatusDone, Markdown: filler(5) + "### " + h + "\nIn B. [source](https://b.example)"}
	m, f := hlModel(t, oneHighlight("B", h, b), 100, 14)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(runes("o"))
	m.Update(runes("y"))
	if len(f.opened) != 1 || f.opened[0] != "https://b.example" || len(f.copied) != 1 || !strings.HasPrefix(f.copied[0], h) {
		t.Errorf("after jumping to %q: opened %v, copied %q", h, f.opened, f.copied)
	}
}

// Enter, o and y find the item with the highlight's very headline, not an
// earlier one whose heading extends it ("Spring Boot 4 released: migration
// guide" for "Spring Boot 4 released") or whose body starts with its words.
func TestAdv_JumpToExactHeadlineNotPrefixOrBody(t *testing.T) {
	for name, earlier := range map[string]string{
		"heading": "### Spring Boot 4 released: migration guide published\nBody. [source](https://wrong.example)",
		"body":    "### Something else\nSpring Boot 4 released notes are long, see below. [source](https://wrong.example)",
	} {
		md := earlier + "\n\n" + filler(3) + "### Spring Boot 4 released\nThe target. [source](https://right.example)\n\n" + filler(3)
		j := oneHighlight("T", "Spring Boot 4 released", &brief.Section{Topic: "T", Status: brief.StatusDone, Markdown: md})
		m, f := hlModel(t, j, 120, 12)
		m.Update(runes("o"))
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m.Update(runes("o"))
		if top := topLine(m); top != "Spring Boot 4 released" || len(f.opened) != 2 || f.opened[0] != "https://right.example" || f.opened[1] != "https://right.example" {
			t.Errorf("%s: top line %q, opened %v", name, top, f.opened)
		}
	}
}

// A highlight whose item capItems cut: Enter opens the topic at the top,
// o and y find nothing rather than another item.
func TestAdv_HighlightOfCutItem(t *testing.T) {
	sec := &brief.Section{Topic: "T", Status: brief.StatusDone, Markdown: filler(3)}
	m, f := hlModel(t, oneHighlight("T", "A story the cap removed entirely", sec), 100, 14)
	m.Update(runes("o"))
	m.Update(runes("y"))
	if len(f.opened)+len(f.copied) != 0 || !strings.Contains(m.View(), "No item") {
		t.Errorf("opened %v copied %v, view:\n%s", f.opened, f.copied, ansi.Strip(m.View()))
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.selected != 1 || m.viewport.YOffset != 0 {
		t.Errorf("selected %d, YOffset %d", m.selected, m.viewport.YOffset)
	}
	// A highlight naming a topic that no longer exists does nothing.
	m2, _ := hlModel(t, oneHighlight("Gone", "x y z", sec), 100, 14)
	m2.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m2.selected != 0 {
		t.Errorf("missing topic: selected %d", m2.selected)
	}
}

// After jumping back to the Overview, resizes keep the selected highlight
// in view and the content fitting; Enter still jumps.
func TestAdv_ResizeKeepsCursorInView(t *testing.T) {
	m, _ := richModel(t, 120, 14, "notty")
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	want := m.highlights()[3]
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	for _, size := range [][2]int{{60, 12}, {200, 50}, {52, 10}, {45, 8}, {120, 14}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		checkFits(t, fmt.Sprint("resize ", size), m)
		if m.cursor != 3 || m.selected != 0 {
			t.Fatalf("%v: cursor %d selected %d", size, m.cursor, m.selected)
		}
		if v := ansi.Strip(m.viewport.View()); !strings.Contains(v, "▸") {
			t.Errorf("%v: the cursor is out of view:\n%s", size, v)
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.selected != 1 || !strings.Contains(topLine(m), strings.Fields(want.Headline)[0]) {
		t.Errorf("enter after resize: selected %d top %q", m.selected, topLine(m))
	}
	// Backspace on the Overview is a no-op.
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	cur := m.cursor
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if m.selected != 0 || m.cursor != cur {
		t.Errorf("second backspace: selected %d cursor %d", m.selected, m.cursor)
	}
}

// o on an item without a URL opens nothing; y copies it without a link.
func TestAdv_OpenCopyItemWithoutURL(t *testing.T) {
	sec := &brief.Section{Topic: "T", Status: brief.StatusDone, Markdown: "### No link here at all\nJust text."}
	m, f := hlModel(t, oneHighlight("T", "No link here at all", sec), 100, 14)
	m.Update(runes("o"))
	m.Update(runes("y"))
	if len(f.opened) != 0 || len(f.copied) != 1 || f.copied[0] != "No link here at all\nJust text." {
		t.Errorf("opened %v copied %q", f.opened, f.copied)
	}
	m.Update(runes("2"))
	m.Update(runes("o"))
	if len(f.opened) != 0 || !strings.Contains(m.View(), "No link") {
		t.Errorf("topic: opened %v", f.opened)
	}
}

// o hands only http(s) URLs to the system opener: never javascript:,
// file:, data:, ftp:, smb: or an argument-like "--help".
func TestAdv_OpenOnlyWebURLs(t *testing.T) {
	for _, u := range []string{"javascript:alert(1)", "file:///etc/passwd", "data:text/html,hi", "--help", "ftp://x.example/a", "smb://evil.example/share"} {
		sec := &brief.Section{Topic: "T", Status: brief.StatusDone, Markdown: "### Odd link item here\nBody. [source](" + u + ")"}
		m, f := hlModel(t, oneHighlight("T", "Odd link item here", sec), 100, 14)
		m.Update(runes("o"))
		m.Update(runes("2"))
		m.Update(runes("o"))
		if len(f.opened) != 0 {
			t.Errorf("%s reached open: %v", u, f.opened)
		}
	}
}

// When open fails, the flash carries none of the URL's control
// characters to the terminal.
func TestAdv_OpenFailureFlashHasNoEscapes(t *testing.T) {
	u := "https://evil.example/\x1b]52;c;ZXZpbA==\x07x"
	sec := &brief.Section{Topic: "T", Status: brief.StatusDone, Markdown: "### Odd link item here\nBody. [source](" + u + ")"}
	m, _ := hlModel(t, oneHighlight("T", "Odd link item here", sec), 100, 14)
	m.open = func(string) error { return errors.New("no opener") }
	m.Update(runes("o"))
	if v := m.View(); strings.Contains(v, "\x1b]52") || strings.Contains(v, "\x07") {
		t.Errorf("view carries the URL's escapes: %q", m.helpText())
	}
}

// o and y work alike in reader mode and are ignored while the key list is
// open; the Overview without highlights has nothing to open.
func TestAdv_OpenCopyReaderOverlayOverview(t *testing.T) {
	m, f := richModel(t, 120, 20, "notty")
	m.Update(runes("z"))
	m.Update(runes("o"))
	m.Update(runes("y"))
	if len(f.opened) != 1 || f.opened[0] != "https://logging.apache.org/security" || len(f.copied) != 1 {
		t.Errorf("reader on the Overview: opened %v copied %d", f.opened, len(f.copied))
	}
	m.Update(runes("?"))
	m.Update(runes("o"))
	m.Update(runes("y"))
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if len(f.opened) != 1 || len(f.copied) != 1 || m.selected != 0 || !m.showHelp {
		t.Errorf("overlay: opened %v copied %d selected %d help %v", f.opened, len(f.copied), m.selected, m.showHelp)
	}
	m.Update(runes("?"))
	m.Update(runes("3"))
	m.Update(runes("o"))
	if f.opened[len(f.opened)-1] != "https://github.blog/npm" {
		t.Errorf("reader topic: opened %v", f.opened)
	}
	// Overview without highlights: no item.
	m.journal.Overview.Highlights = nil
	m.Update(runes("1"))
	n := len(f.opened)
	m.Update(runes("o"))
	m.Update(runes("y"))
	if len(f.opened) != n || len(f.copied) != 1 {
		t.Errorf("plain Overview: opened %v copied %d", f.opened, len(f.copied))
	}
}

// Reader mode and the key list fit at every size, including the compact
// and too-small layouts, and z/? toggles in any order keep them fitting.
func TestAdv_ReaderOverlaySmallSizes(t *testing.T) {
	for _, size := range [][2]int{{20, 4}, {20, 5}, {40, 6}, {40, 10}, {49, 24}, {50, 10}, {51, 11}, {80, 9}, {80, 24}, {300, 80}} {
		m, _ := richModel(t, size[0], size[1], "notty")
		for i, k := range []string{"z", "?", "z", "?", "z", "2", "?", "j", "?", "z", "z", "h", "z", "?", "?", "1"} {
			m.Update(runes(k))
			checkFits(t, fmt.Sprintf("step %d %q", i, k), m)
			if m.viewport.Width < 1 || m.viewport.Height < 0 {
				t.Errorf("%v step %d: viewport %dx%d", size, i, m.viewport.Width, m.viewport.Height)
			}
		}
	}
}

// streamBackend writes digests with two items and answers the overview
// with highlights, pausing so keys interleave with updates.
type streamBackend struct{ mu sync.Mutex }

func (b *streamBackend) Research(_ context.Context, t brief.Task) brief.Result {
	time.Sleep(2 * time.Millisecond)
	md := "### " + t.Topic.Name + " story one is here\nBody [source](https://one.example/" + t.Topic.Name + ")\n\n" +
		"### " + t.Topic.Name + " story two is here\nBody https://two.example/x"
	return brief.Result{Markdown: md, Tokens: t.Budget / 4, Searches: 1}
}

func (b *streamBackend) Summarize(_ context.Context, _, _ string) brief.Result {
	return brief.Result{Markdown: `{"highlights":[{"tier":"know","topic":"A","headline":"A story one is here","text":"One.","why":"x"},` +
		`{"tier":"act","topic":"B","headline":"B story two is here","text":"Two.","why":"y"}]}`, Tokens: 1_000}
}

func (b *streamBackend) Name() string { return "stream" }

// Toggling reader mode, the key list, the cursor and jumps while a fake
// run streams in never overflows, panics or calls the opener twice.
func TestAdv_TogglesDuringStreamingRun(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := config.Default()
	cfg.Provider.Type = "anthropic"
	cfg.TokenBudget = 400_000
	cfg.Topics = []config.Topic{{Name: "A"}, {Name: "B"}, {Name: "C"}}
	for _, size := range [][2]int{{120, 30}, {50, 10}, {40, 8}} {
		m := New(brief.NewGenerator(cfg, &streamBackend{}), nil, nil, "notty")
		f := &fakeIO{}
		m.open = func(u string) error { f.opened = append(f.opened, u); return nil }
		m.copy = func(s string) error { f.copied = append(f.copied, s); return nil }
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		cmd := m.start()
		keySeq := []tea.KeyMsg{runes("z"), tea.KeyMsg{Type: tea.KeyDown}, runes("?"), runes("o"), runes("?"),
			tea.KeyMsg{Type: tea.KeyEnter}, runes("z"), tea.KeyMsg{Type: tea.KeyBackspace}, runes("y"), runes("1")}
		deadline := time.After(10 * time.Second)
		step := 0
		for cmd != nil {
			msgs := make(chan tea.Msg, 1)
			go func(c tea.Cmd) { msgs <- c() }(cmd)
			var msg tea.Msg
			select {
			case msg = <-msgs:
			case <-deadline:
				t.Fatal("run did not finish")
			}
			m.Update(keySeq[step%len(keySeq)])
			step++
			checkFits(t, fmt.Sprintf("%v step %d", size, step), m)
			if _, ok := msg.(doneMsg); ok {
				m.Update(msg)
				break
			}
			_, cmd = m.Update(msg)
		}
		checkFits(t, fmt.Sprint(size, " done"), m)
		if !m.hasHighlights() || len(m.journal.Overview.Highlights) != 2 || m.journal.Overview.Highlights[0].Tier != "act" {
			t.Errorf("%v: highlights %+v", size, m.journal.Overview.Highlights)
		}
		// After the run: the cursor still works.
		m.Update(runes("1"))
		m.Update(tea.KeyMsg{Type: tea.KeyRight})
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m.Update(runes("o"))
		if len(f.opened) == 0 || f.opened[len(f.opened)-1] != "https://one.example/A" {
			t.Errorf("%v: opened %v", size, f.opened)
		}
		for _, u := range f.opened {
			if !strings.HasPrefix(u, "https://") {
				t.Errorf("opened %q", u)
			}
		}
	}
}

// The viewport cuts rendered content between lines: every window of it
// keeps OSC 8 balanced and carries no stray escapes.
func TestAdv_ViewportWindowsKeepOSC8Balanced(t *testing.T) {
	md := ""
	for _, c := range hostileLinkCases {
		md += c + "\n\n"
	}
	sec := &brief.Section{Topic: "Hostile", Status: brief.StatusDone, Markdown: md,
		Sources: []brief.Source{{Title: strings.Repeat("Long source title ", 8), URL: longURL}}}
	j := &brief.Journal{Date: "2026-10-29", Overview: brief.Section{Status: brief.StatusDone, Markdown: "- x"}, Sections: []*brief.Section{sec}}
	for _, w := range []int{30, 52, 80, 131, 200} {
		m, _ := hlModel(t, j, w, 12)
		m.Update(runes("2"))
		for y := 0; y < m.viewport.TotalLineCount(); y += 3 {
			m.viewport.SetYOffset(y)
			v := m.viewport.View()
			for _, issue := range escapeIssues(v) {
				t.Errorf("width %d offset %d: %s", w, y, issue)
			}
			if p := ansi.Strip(v); strings.Contains(p, "http") || strings.Contains(p, "QZX") {
				t.Errorf("width %d offset %d: visible URL or token:\n%s", w, y, p)
			}
		}
	}
}

// The Overview's list shows a highlight's text and why without raw URLs
// or escapes, even as an older journal stored them.
func TestAdv_OverviewListCleansHighlightText(t *testing.T) {
	sec := &brief.Section{Topic: "T", Status: brief.StatusDone, Markdown: "### Patch it now please\nBody. [source](https://a.example)"}
	j := oneHighlight("T", "Patch it now please", sec)
	j.Overview.Highlights[0].Text = "Patch now, see [advisory](https://evil.example/x) \x1b]52;c;ZXZpbA==\x07"
	j.Overview.Highlights[0].Why = "Because \x1b[2J"
	m, _ := hlModel(t, j, 100, 20)
	v := m.viewport.View()
	if strings.Contains(ansi.Strip(v), "http") || strings.Contains(v, "\x1b]52") || strings.Contains(v, "\x1b[2J") {
		t.Errorf("overview list: %q", ansi.Strip(v))
	}
}
