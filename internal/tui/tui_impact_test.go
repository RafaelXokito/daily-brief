package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/rafaelpereira/daily-brief/internal/brief"
)

const taggedDigest = "### Widget server flaw exploited\n" +
	"`ACT` CVE-2026-1234 · CVSS 9.8 · KEV due Nov 10 · fixed in 3.2.1\n" +
	"Attackers run code on exposed servers. [source](https://widget.example/a)\n\n" +
	"### `WATCH` Rates may rise\nThe ECB meets Thursday.\n\n" +
	"### Third item\n`FYI` Worth knowing.\n"

// withColour renders with a true-colour profile, as on a terminal, and
// restores the test default after.
func withColour(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

// Tags render as badges, without their ticks or brackets, coloured by
// impact; a facts line is muted on a line of its own.
func TestTagsRenderAsBadges(t *testing.T) {
	withColour(t)
	for _, theme := range []string{"dark", "light"} {
		out := Render(taggedDigest, 80, theme)
		plain := ansi.Strip(out)
		for _, want := range []string{" ACT  CVE-2026-1234 · CVSS 9.8 · KEV due Nov 10 · fixed in 3.2.1", " WATCH  Rates may rise", " FYI  Worth knowing."} {
			if !strings.Contains(plain, want) {
				t.Errorf("%s: no %q in:\n%s", theme, want, plain)
			}
		}
		for _, bad := range []string{"`", "[FYI]", "**"} {
			if strings.Contains(plain, bad) {
				t.Errorf("%s: shows %q:\n%s", theme, bad, plain)
			}
		}
		for _, l := range strings.Split(plain, "\n") {
			if strings.Contains(l, "CVSS") && strings.Contains(l, "Attackers") {
				t.Errorf("%s: facts share a line with the text: %q", theme, l)
			}
		}
		for _, st := range []lipgloss.Style{actBadge, watchBadge, fyiBadge} {
			if !strings.Contains(out, strings.TrimSuffix(st.Render("x"), "x\x1b[0m")) {
				t.Errorf("%s: a badge's colour is missing", theme)
			}
		}
		if facts := strings.TrimSuffix(mutedStyle.Render("CVE-2026-1234"), "\x1b[0m"); !strings.Contains(out, facts) {
			t.Errorf("%s: the facts are not muted", theme)
		}
		// The heading stays bold after its badge.
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, "Rates may rise") {
				i := strings.Index(l, "WATCH")
				if rest := l[i:]; !strings.Contains(rest[:strings.Index(rest, "Rates")], ";1m") {
					t.Errorf("%s: heading style not resumed after the badge: %q", theme, l)
				}
			}
		}
		for _, issue := range escapeIssues(out) {
			t.Errorf("%s: %s", theme, issue)
		}
	}
	if plain := Render(taggedDigest, 80, "notty"); !strings.Contains(plain, "[ACT] CVE-2026-1234") || !strings.Contains(plain, "[WATCH] Rates may rise") {
		t.Errorf("notty:\n%s", plain)
	}
}

// Untagged digests and text that only looks like a tag render as before.
func TestNonTagsAreNotBadges(t *testing.T) {
	withColour(t)
	md := "### Item\n`URGENT` code span stays code.\n[ACT](https://a.example/x) is a link.\nBody `ACT` mid-line."
	out := Render(md, 80, "dark")
	plain := ansi.Strip(out)
	for _, want := range []string{"URGENT", "ACT is a link", "mid-line"} {
		if !strings.Contains(plain, want) {
			t.Errorf("no %q in:\n%s", want, plain)
		}
	}
	if badge := strings.TrimSuffix(actBadge.Render("x"), "x\x1b[0m"); strings.Contains(out, badge) {
		t.Errorf("text became a badge:\n%q", out)
	}
}

// Hostile content around and inside tags never injects escapes, shows a
// URL or placeholder, or overflows, at any width and theme.
func TestAdversarialTaggedContent(t *testing.T) {
	withColour(t)
	cases := map[string]string{
		"esc-in-tag":     "### Item\n`ACT\x1b]8;;https://evil.example\x07` body\n`WATCH\x1b[2J` x",
		"bidi-in-tag":    "### Item\n`A\u202ECT` body\n`\u200BFYI` x",
		"entity-tag":     "### Item\n`&#27;ACT` body &#x1b;[31m",
		"facts-escape":   "### Item\n`ACT` CVE-1 · CVSS \x1b[31m9.8 · fixed\x07 in 3.2.1\nText. [source](https://a.example/x)",
		"facts-link":     "### Item\n`ACT` CVE-1 · [NVD](https://nvd.example/x) · fixed\nText.",
		"placeholders":   "### Item\n`ACT` \uE000ZZ0\uE002 · \uE001\uE004 · x\nText.",
		"long-facts":     "### Item\n`ACT` " + strings.Repeat("CVE-2026-12345 · ", 30) + "end\nText.",
		"tag-in-heading": "### `ACT` " + strings.Repeat("Long headline ", 20) + "\nText.",
		"bold-open":      "### Item\n**`ACT` bold that opens and never closes\nText.",
		"tag-only":       "### Item\n`ACT`\n\n`FYI`",
	}
	for name, md := range cases {
		renderHostile(t, name, md)
	}
}

// The sidebar shows a topic's ACT count, in its own colour, and every row
// still fits the box, with long and wide names and large counts.
func TestSidebarActCountFits(t *testing.T) {
	for _, name := range []string{"Finance & Investing", "🚀🌍 Space and Rockets 🛰️ Launches", "日本の経済ニュースと市場の動向まとめ", "Security"} {
		for _, acts := range []int{0, 1, 12} {
			var md strings.Builder
			for i := range acts {
				fmt.Fprintf(&md, "### Update: item %d\n`ACT` x\n\n", i)
			}
			md.WriteString("### New\n`FYI` x")
			sec := &brief.Section{Topic: name, Status: brief.StatusDone, Markdown: md.String(), TokensUsed: 150_000}
			j := &brief.Journal{Date: "2026-10-29", TokenBudget: 740_000,
				Overview: brief.Section{Topic: "Overview", Status: brief.StatusDone, Markdown: "x"},
				Sections: []*brief.Section{sec}}
			for _, w := range []int{50, 80, 120, 200} {
				m := New(nil, nil, j, "notty")
				m.Update(tea.WindowSizeMsg{Width: w, Height: 20})
				for _, selected := range []int{0, 1} {
					m.selected = selected
					rows := strings.Split(m.sidebar(), "\n")
					want := fmt.Sprintf("!%d ↻%d 150.0k", acts, acts)
					if acts == 0 {
						want = "150.0k"
					}
					if !strings.HasSuffix(ansi.Strip(rows[1]), " "+want) || strings.Contains(rows[0], "!") {
						t.Errorf("%q, %d acts: rows %q", name, acts, rows)
					}
					for _, l := range rows {
						if lw := lipgloss.Width(l); lw > sidebarWidth-1 {
							t.Errorf("%q, %d acts, width %d: row %q is %d cells", name, acts, w, l, lw)
						}
					}
				}
				checkFits(t, fmt.Sprintf("%q/%d acts", name, acts), m)
			}
		}
	}
}

// The Overview's numbers line counts the items to act on.
func TestOverviewListShowsActCount(t *testing.T) {
	sec := &brief.Section{Topic: "Security", Status: brief.StatusDone, Markdown: taggedDigest}
	j := oneHighlight("Security", "Widget server flaw exploited", sec)
	m, _ := hlModel(t, j, 100, 30)
	if v := ansi.Strip(m.View()); !strings.Contains(v, "3 items · 1 to act ·") {
		t.Errorf("no act count in:\n%s", v)
	}
}
