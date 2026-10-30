package tui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/RafaelXokito/daily-brief/internal/brief"
)

const longURL = "https://www.theregister.com/2026/10/29/very_long_article_slug_about_quarkus_native_builds_and_graalvm/?utm_source=feed&utm_medium=rss"

// richJournal has highlights, links of every kind, an update, a Related
// label, a Sources list and blockquote notes.
func richJournal() *brief.Journal {
	j := &brief.Journal{Date: "2026-10-29", TokenBudget: 400_000,
		BudgetWarnings: []string{"budget is too low (40% of the 1.5M baseline): topics will be thin or skipped"},
		Sections: []*brief.Section{
			{Topic: "Java & Quarkus", Status: brief.StatusDone, Truncated: true, Markdown: "### Quarkus 3.20 released with faster native builds\n" +
				"Startup is down by a third for serverless deployments. [source](" + longURL + ")\n\n" +
				"### Update: Spring Boot 4 milestone slips again\nSince Mon: delayed, see https://spring.io/blog/m3.\n\n" +
				"**Related**\n\n### Kotlin 2.3 ships with a new compiler\nShort. [Kotlin blog](https://kotlinlang.org/x)",
				Sources: []brief.Source{{Title: "The Register: Quarkus native builds get much faster", URL: longURL}, {URL: "https://spring.io/blog/m3"}}},
			{Topic: "Security", Status: brief.StatusDone, Markdown: "### npm token theft campaign hits maintainers\nRotate tokens now. [source](https://github.blog/npm)\n\n" +
				"### Critical Log4j flaw exploited in the wild\nPatch to 2.25.1. [source](https://logging.apache.org/security)"},
			{Topic: "AI", Status: brief.StatusDone, Markdown: "Nothing notable today."},
		},
	}
	j.Overview = brief.Section{Topic: "Overview", Status: brief.StatusDone, Markdown: "## Today's highlights\n\n- x [source](https://a.example)",
		Highlights: []brief.Highlight{
			{Tier: "know", Topic: "Java & Quarkus", Headline: "Quarkus 3.20 released with faster native builds", Text: "Quarkus 3.20 cuts native build times.", Why: "Faster CI and cold starts."},
			{Tier: "act", Topic: "Security", Headline: "Critical Log4j flaw exploited in the wild", Text: "Patch Log4j to 2.25.1 now.", Why: "Exploited remote code execution."},
			{Tier: "act", Topic: "Security", Headline: "npm token theft campaign hits maintainers", Text: "Rotate npm tokens.", Why: "Stolen tokens publish malware."},
			{Tier: "know", Topic: "Java & Quarkus", Headline: "Kotlin 2.3 ships with a new compiler", Text: "Kotlin 2.3 ships K3.", Why: ""},
		}}
	return j
}

// fakeIO records what o and y hand to the opener and the clipboard.
type fakeIO struct{ opened, copied []string }

func richModel(t *testing.T, width, height int, theme string) (*Model, *fakeIO) {
	t.Helper()
	m := New(nil, nil, richJournal(), theme)
	f := &fakeIO{}
	m.open = func(u string) error { f.opened = append(f.opened, u); return nil }
	m.copy = func(s string) error { f.copied = append(f.copied, s); return nil }
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return m, f
}

// checkRendered: no heading markers or visible URLs, the full URL in an
// OSC 8 hyperlink, and every line within width.
func checkRendered(t *testing.T, label, out string, width int, urls ...string) {
	t.Helper()
	plain := ansi.Strip(out)
	for _, bad := range []string{"## ", "### ", "http"} {
		if strings.Contains(plain, bad) {
			t.Errorf("%s: visible %q:\n%s", label, bad, plain)
		}
	}
	for _, u := range urls {
		if !strings.Contains(out, ";"+u+"\x1b\\") || !strings.Contains(out, "\x1b]8;") {
			t.Errorf("%s: no OSC 8 link to %s", label, u)
		}
	}
	for i, l := range strings.Split(out, "\n") {
		if w := lipgloss.Width(l); w > width {
			t.Errorf("%s: line %d is %d cells, over %d: %q", label, i, w, width, ansi.Strip(l))
		}
	}
}

func TestRenderTopicHasNoMarkersOrRawLinksAndFits(t *testing.T) {
	s := richJournal().Sections[0]
	md := "## " + s.Topic + "\n\n" + s.Render()
	for _, theme := range []string{"dark", "light", "notty"} {
		step := 1
		if theme != "dark" {
			step = 7
		}
		for width := 30; width <= 200; width += step {
			out := Render(md, width, theme)
			checkRendered(t, theme, out, width, longURL, "https://spring.io/blog/m3", "https://kotlinlang.org/x")
			plain := ansi.Strip(out)
			for _, want := range []string{"↗ theregister.com", "↻ Update · Spring Boot", "Related", "Sources", "─"} {
				if !strings.Contains(plain, want) {
					t.Fatalf("%s %d: lacks %q:\n%s", theme, width, want, plain)
				}
			}
			// The note keeps its bar on every line it wraps to.
			for _, l := range strings.Split(plain, "\n") {
				if strings.Contains(l, "incomplete") && !strings.HasPrefix(strings.TrimSpace(l), "│") {
					t.Fatalf("%s %d: quote line without its bar %q:\n%s", theme, width, l, plain)
				}
			}
		}
	}
	plain := ansi.Strip(Render(md, 120, "dark"))
	if !strings.Contains(plain, "The Register: Quarkus native builds get much faster · theregister.com") || strings.Contains(plain, "www.") {
		t.Errorf("sources list:\n%s", plain)
	}
	if !strings.Contains(plain, "Kotlin blog") {
		t.Errorf("titled link lost its title:\n%s", plain)
	}
}

// Warnings wrap with their bar, and the warning sign stays out of quotes.
func TestRenderWarningsKeepTheirBar(t *testing.T) {
	m, _ := richModel(t, 100, 30, "dark")
	m.journal.Overview.Highlights = nil
	m.refresh()
	for width := 30; width <= 120; width += 7 {
		md, _, _ := m.markdown()
		out := Render(md, width, "dark")
		checkRendered(t, "warnings", out, width)
		plain := ansi.Strip(out)
		if strings.Contains(plain, "⚠") {
			t.Errorf("a quote has ⚠:\n%s", plain)
		}
		for _, l := range strings.Split(plain, "\n") {
			if t2 := strings.TrimSpace(l); strings.Contains(t2, "baseline") && !strings.HasPrefix(t2, "│") {
				t.Errorf("width %d: warning line lost its bar: %q", width, l)
			}
		}
	}
}

func TestThemeStyles(t *testing.T) {
	light, dark := styleConfig("light", 80), styleConfig("dark", 80)
	if light.Document.Color == nil || *light.Document.Color != *styles.LightStyleConfig.Document.Color ||
		*light.H2.Color != accent.Light || *dark.H2.Color != accent.Dark {
		t.Errorf("light theme does not use the light config")
	}
	for _, c := range []string{light.H1.Prefix, light.H2.Prefix, light.H3.Prefix, light.H4.Prefix, dark.H2.Prefix, dark.H3.Prefix} {
		if strings.Contains(c, "#") {
			t.Errorf("heading prefix %q", c)
		}
	}
	if *dark.Document.Margin != 1 || dark.H3.BlockPrefix != "\n" || !*dark.H3.Bold {
		t.Errorf("dark: margin %d, H3 %+v", *dark.Document.Margin, dark.H3)
	}
	// The global styles are not changed.
	if styles.DarkStyleConfig.H2.Prefix != "## " {
		t.Error("styleConfig changed glamour's dark style")
	}
	m, _ := richModel(t, 100, 30, "light")
	if m.theme != "light" {
		t.Errorf("model theme %q", m.theme)
	}
}

func TestOverviewListRendersHighlights(t *testing.T) {
	for width := 30; width <= 200; width += 3 {
		m, _ := richModel(t, width+sidebarWidth+2, 30, "dark")
		checkRendered(t, "overview", m.content, m.viewport.Width)
		for i, l := range strings.Split(m.View(), "\n") {
			if w := lipgloss.Width(l); w > m.width {
				t.Errorf("width %d: view line %d is %d cells", m.width, i, w)
			}
		}
	}
	m, _ := richModel(t, 140, 40, "dark")
	plain := ansi.Strip(m.content)
	for _, want := range []string{"Act now", "Worth knowing", "▸ Patch Log4j to 2.25.1 now.", "Exploited remote code execution.", "Security",
		"5 items · 1 update · 3 topics", "Java & Quarkus — 3 items, 1 update", "AI — quiet day", "Warning: budget is too low"} {
		if !strings.Contains(plain, want) {
			t.Errorf("overview lacks %q:\n%s", want, plain)
		}
	}
	if strings.Index(plain, "Act now") > strings.Index(plain, "Worth knowing") {
		t.Errorf("act is not first:\n%s", plain)
	}
	if os.Getenv("SHOW_VIEW") != "" {
		t.Log("\n" + m.View())
	}
}

// ↓ ↓ enter opens the topic of the highlight two below the first, at its
// heading;
// backspace comes back with the cursor kept.
func TestOverviewClickToGo(t *testing.T) {
	m, _ := richModel(t, 120, 16, "dark")
	hl := m.highlights()
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 2 || m.selected != 0 {
		t.Fatalf("cursor %d, selected %d", m.cursor, m.selected)
	}
	if !strings.Contains(ansi.Strip(m.viewport.View()), "▸ "+hl[2].Text) {
		t.Errorf("the selected highlight is not in view:\n%s", ansi.Strip(m.viewport.View()))
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.selected != 1 || m.focus != focusSummary {
		t.Fatalf("enter: selected %d (want Java & Quarkus), focus %d", m.selected, m.focus)
	}
	top := ansi.Strip(strings.Split(m.viewport.View(), "\n")[0])
	if !strings.Contains(top, "Quarkus 3.20 released") || m.viewport.YOffset == 0 {
		t.Errorf("viewport does not start at the heading: %q", top)
	}
	if !strings.Contains(m.helpText(), "⌫ back") {
		t.Errorf("help line %q", m.helpText())
	}
	// Paging still works in the topic, and b still pages up.
	m.Update(runes("g"))
	m.Update(runes(" "))
	y := m.viewport.YOffset
	m.Update(runes("b"))
	if y == 0 || m.viewport.YOffset != 0 || m.selected != 1 {
		t.Errorf("paging: down to %d, b to %d, selected %d", y, m.viewport.YOffset, m.selected)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if m.selected != 0 || m.cursor != 2 || m.focus != focusSummary {
		t.Errorf("backspace: selected %d, cursor %d, focus %d", m.selected, m.cursor, m.focus)
	}
	// ↑ moves the cursor, not the page; it stops at the first highlight.
	for range 5 {
		m.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	if m.cursor != 0 || m.selected != 0 {
		t.Errorf("up: cursor %d", m.cursor)
	}
	// Paging works on the Overview too.
	m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if m.viewport.YOffset == 0 {
		t.Errorf("page down did not scroll the Overview")
	}
	// Enter from Topics only focuses the Summary.
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.selected != 0 || m.focus != focusSummary {
		t.Errorf("enter in Topics: selected %d, focus %d", m.selected, m.focus)
	}
}

// A highlight whose item is no longer in the topic opens it at the top.
func TestOverviewClickToGoMissingHeadline(t *testing.T) {
	m, _ := richModel(t, 120, 16, "dark")
	m.journal.Sections[1].Markdown = "### Something else entirely\nBody."
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.selected != 2 || m.viewport.YOffset != 0 {
		t.Errorf("selected %d, YOffset %d", m.selected, m.viewport.YOffset)
	}
}

func TestOpenAndCopy(t *testing.T) {
	m, f := richModel(t, 120, 12, "dark")
	m.Update(tea.KeyMsg{Type: tea.KeyDown}) // npm
	m.Update(runes("o"))
	m.Update(runes("y"))
	if len(f.opened) != 1 || f.opened[0] != "https://github.blog/npm" {
		t.Errorf("opened %v", f.opened)
	}
	if len(f.copied) != 1 || f.copied[0] != "npm token theft campaign hits maintainers\nRotate tokens now.\nhttps://github.blog/npm" {
		t.Errorf("copied %q", f.copied)
	}
	if !strings.Contains(m.View(), "Copied") {
		t.Errorf("no Copied flash")
	}
	_, cmd := m.Update(runes("y"))
	if cmd == nil {
		t.Fatal("no command to clear the flash")
	}
	m.Update(cmd())
	if m.flash != "" {
		t.Errorf("flash %q not cleared", m.flash)
	}

	// In a topic, the item nearest the top of the viewport.
	m.Update(runes("2"))
	m.Update(runes("o"))
	if got := f.opened[len(f.opened)-1]; got != longURL {
		t.Errorf("at the top opened %s", got)
	}
	m.viewport.SetYOffset(m.itemLine(brief.ItemIndex(m.current().Items(), "Spring Boot 4 milestone slips again")) + 1)
	m.Update(runes("o"))
	m.Update(runes("y"))
	if got := f.opened[len(f.opened)-1]; got != "https://spring.io/blog/m3" {
		t.Errorf("mid-item opened %s", got)
	}
	if got := f.copied[len(f.copied)-1]; got != "Update: Spring Boot 4 milestone slips again\nSince Mon: delayed, see https://spring.io/blog/m3.\nhttps://spring.io/blog/m3" {
		t.Errorf("copied %q", got)
	}
	// A topic with no items has nothing to open.
	n := len(f.opened)
	m.Update(runes("4"))
	m.Update(runes("o"))
	if len(f.opened) != n || !strings.Contains(m.View(), "No link") {
		t.Errorf("opened %v on a quiet topic", f.opened)
	}
}

func TestReaderMode(t *testing.T) {
	m, _ := richModel(t, 120, 30, "dark")
	m.Update(runes("2"))
	before := m.viewport.Width
	m.Update(runes("z"))
	v := m.View()
	if !m.reader || m.viewport.Width != 120 || before >= 120 || strings.Contains(v, "✓ Overview") {
		t.Errorf("reader: width %d (was %d), view:\n%s", m.viewport.Width, before, v)
	}
	checkRendered(t, "reader", m.content, 120)
	for i, l := range strings.Split(v, "\n") {
		if lipgloss.Width(l) > 120 {
			t.Errorf("line %d too wide", i)
		}
	}
	if len(strings.Split(v, "\n")) > 30 {
		t.Errorf("view is %d lines", len(strings.Split(v, "\n")))
	}
	if !strings.Contains(m.helpText(), "z reader") {
		t.Errorf("help line %q", m.helpText())
	}
	m.Update(runes("z"))
	if m.reader || m.viewport.Width != before || !strings.Contains(m.View(), "✓ Overview") {
		t.Errorf("z again: width %d", m.viewport.Width)
	}
	// ← in reader mode brings the Topics pane back.
	m.Update(runes("z"))
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.reader || m.focus != focusTopics {
		t.Errorf("left in reader mode: reader %v, focus %d", m.reader, m.focus)
	}
}
