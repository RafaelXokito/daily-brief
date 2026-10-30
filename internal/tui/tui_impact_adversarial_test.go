package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/RafaelXokito/daily-brief/internal/brief"
)

// Badges at every width from 20 to 200, in every theme: no line overflows,
// no escape is left open or injected, and the tag's word stays visible.
func TestAdvBadgesAtAllWidths(t *testing.T) {
	withColour(t)
	md := taggedDigest + "\n### Update: `ACT` Widget patched again\nSince Mon: fixed in 3.2.2. [source](https://w.example/u)\n\n" +
		"### Gadget\n`WATCH` CVE-2026-9 · CVSS 7.5 · " + strings.Repeat("no fix yet · ", 6) + "end\nText.\n\n" +
		"**Related**\n\n### 日本の市場\n`FYI` 日本株 · 為替 · 円安\n説明。"
	for _, theme := range []string{"dark", "light", "notty"} {
		for w := 20; w <= 200; w++ {
			out := Render(md, w, theme)
			label := fmt.Sprintf("%s/%d", theme, w)
			for _, issue := range escapeIssues(out) {
				t.Errorf("%s: %s", label, issue)
			}
			for i, l := range strings.Split(out, "\n") {
				if lw := lipgloss.Width(l); lw > w {
					t.Errorf("%s: line %d is %d cells: %q", label, i, lw, ansi.Strip(l))
				}
			}
			plain := ansi.Strip(out)
			for _, want := range []string{"ACT", "WATCH", "FYI"} {
				if !strings.Contains(plain, want) {
					t.Errorf("%s: no %s badge:\n%s", label, want, plain)
				}
			}
			if strings.ContainsAny(plain, "`") {
				t.Errorf("%s: ticks or placeholders visible:\n%s", label, plain)
			}
		}
	}
}

// Tagged hostile content passes the strict escape scanner.
func TestAdvTaggedHostileContent(t *testing.T) {
	withColour(t)
	cases := map[string]string{
		"osc-after-tag":    "### Item\n`ACT` \x1b]8;;https://evil.example\x07click\x1b]8;;\x07 · x\nText.",
		"csi-in-facts":     "### Item\n`WATCH` CVE-1 · \x1b[2J\x1b[H · \u009b31m red\nText.",
		"tag-link-heading": "### `ACT` [Patch](https://evil.example/" + strings.Repeat("a", 200) + ") now\nText.",
		"bracket-osc":      "### Item\n[ACT\x1b]52;c;ZXZpbA==\x07] x\nText.",
		"bold-tag-open":    "### Item\n__`WATCH` never closed\n**`ACT`** also **open\nText.",
		"many-tags":        strings.Repeat("### I\n`ACT` a · b\nT.\n\n### J\n[WATCH] c\n\n", 20),
		"tag-in-quote":     "### Item\n> `ACT` quoted\n> [WATCH] too\nText.",
		"tag-in-list":      "### Item\n- `ACT` one · two\n  - [FYI] nested · deeper\nText.",
		"bidi-facts":       "### Item\n`ACT` ‮CVE-1‬ · ⁦x⁩ · y\nText.",
	}
	for name, md := range cases {
		renderHostile(t, name, md)
	}
}

// The sidebar shows !N and ↻N side by side with names that must be cut,
// at small terminal widths too, and never wraps a row.
func TestAdvSidebarActAndUpdatesWithLongNames(t *testing.T) {
	names := []string{strings.Repeat("Extremely long topic name ", 4), "🚀🌍🛰️🚀🌍🛰️🚀🌍🛰️🚀🌍🛰️", "日本の経済ニュースと市場の動向まとめ日本の経済", "x"}
	for _, name := range names {
		var md strings.Builder
		for i := range 123 {
			fmt.Fprintf(&md, "### Update: item %d\n`ACT` x\n\n", i)
		}
		sec := &brief.Section{Topic: name, Status: brief.StatusDone, Markdown: md.String(), TokensUsed: 1_234_567}
		j := &brief.Journal{Date: "2026-10-29", TokenBudget: 3_000_000,
			Overview: brief.Section{Topic: "Overview", Status: brief.StatusDone, Markdown: "x"},
			Sections: []*brief.Section{sec, {Topic: name + "2", Status: brief.StatusDone, Markdown: "### A\n`WATCH` x"}}}
		for _, w := range []int{20, 30, 40, 60, 200} {
			for _, theme := range []string{"notty", "dark"} {
				m := New(nil, nil, j, theme)
				m.Update(tea.WindowSizeMsg{Width: w, Height: 15})
				rows := strings.Split(m.sidebar(), "\n")
				if !strings.Contains(ansi.Strip(rows[1]), "!123 ↻123") {
					t.Errorf("%q w%d %s: row %q", name, w, theme, ansi.Strip(rows[1]))
				}
				if strings.Contains(ansi.Strip(rows[2]), "!") {
					t.Errorf("%q: WATCH-only topic shows a count: %q", name, ansi.Strip(rows[2]))
				}
				for _, r := range rows {
					if lw := lipgloss.Width(r); lw > sidebarWidth-1 {
						t.Errorf("%q w%d %s: row %q is %d cells", name, w, theme, ansi.Strip(r), lw)
					}
				}
				for _, issue := range escapeIssues(m.View()) {
					t.Errorf("%q w%d %s: %s", name, w, theme, issue)
				}
			}
		}
	}
}

// Enter on a highlight lands on its tagged heading; o opens its link; y
// copies the item as plain text, its tag as "[WATCH]". A second leading
// tag, on the body line, is text: it loses its ticks like any code span.
func TestAdvJumpOpenCopyTaggedItems(t *testing.T) {
	md := filler(4) + "### `WATCH` Rates may rise\n`WATCH` ECB · Thursday\nThe ECB meets. [source](https://ecb.example/x)\n\n" + filler(3)
	j := oneHighlight("Finance", "Rates may rise", &brief.Section{Topic: "Finance", Status: brief.StatusDone, Markdown: md})
	m, f := hlModel(t, j, 100, 14)
	m.Update(runes("y"))
	m.Update(runes("o"))
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.selected != 1 || !strings.Contains(topLine(m), "Rates may rise") {
		t.Errorf("jump: selected %d, top %q", m.selected, topLine(m))
	}
	m.Update(runes("y"))
	m.Update(runes("o"))
	if len(f.opened) != 2 || f.opened[0] != "https://ecb.example/x" || f.opened[1] != f.opened[0] {
		t.Errorf("opened %v", f.opened)
	}
	want := "[WATCH] Rates may rise\nWATCH ECB · Thursday\nThe ECB meets.\nhttps://ecb.example/x"
	if len(f.copied) != 2 || f.copied[0] != want || f.copied[1] != want {
		t.Errorf("copied %q, want %q twice", f.copied, want)
	}
	for _, c := range f.copied {
		if strings.Contains(c, "`") {
			t.Errorf("copy keeps code ticks: %q", c)
		}
	}
}

// Code blocks show verbatim: a line in one that starts with what looks
// like a tag, canonical or not, never becomes a badge.
func TestAdvTagInsideCodeFenceIsNotABadge(t *testing.T) {
	withColour(t)
	md := "### Code\nExample config:\n\n```\n[WATCH] neither\nlevel = 1\n```\n"
	out := Render(md, 60, "dark")
	if !strings.Contains(ansi.Strip(out), "[WATCH] neither") {
		t.Errorf("code block changed:\n%s", ansi.Strip(out))
	}
	if badge := strings.TrimSuffix(watchBadge.Render("x"), "x\x1b[0m"); strings.Contains(out, badge) {
		t.Error("code line became a WATCH badge")
	}
	md = "### Code\n```\n`WATCH` neither\n```\n\n### Other\n```md\n### `ACT` Inside\n```"
	out = Render(md, 60, "dark")
	for _, want := range []string{"`WATCH` neither", "### `ACT` Inside"} {
		if !strings.Contains(ansi.Strip(out), want) {
			t.Errorf("code block lost %q:\n%s", want, ansi.Strip(out))
		}
	}
	for _, st := range []lipgloss.Style{actBadge, watchBadge} {
		if badge := strings.TrimSuffix(st.Render("x"), "x\x1b[0m"); strings.Contains(out, badge) {
			t.Errorf("a fenced tag became a badge:\n%s", ansi.Strip(out))
		}
	}
}

// Badges show only where brief finds the item's tag (its headline or
// first body line), so a tag on a later line, which brief ranks and
// counts as untagged FYI, shows as plain text, not as an ACT badge.
func TestAdvLaterLineTagIsNotABadge(t *testing.T) {
	withColour(t)
	md := "### Item\nBody.\n`ACT` on the third line"
	sec := &brief.Section{Topic: "T", Status: brief.StatusDone, Markdown: md}
	if n := sec.ActCount(); n != 0 {
		t.Fatalf("precondition: ActCount %d", n)
	}
	out := Render(md, 60, "dark")
	if badge := strings.TrimSuffix(actBadge.Render("x"), "x\x1b[0m"); strings.Contains(out, badge) {
		t.Errorf("an ACT badge shows on an item counted as FYI (ActCount 0):\n%s", ansi.Strip(out))
	}
}

// The section view badges the same lines Render does, offset by the
// topic heading above the digest, including a tag before "Update:".
func TestAdvSectionViewBadgesTagLines(t *testing.T) {
	withColour(t)
	md := "### `ACT` Update: Widget patched\nSince Mon: 3.2.2.\n\n### Gadget\nBody.\n`WATCH` later line"
	sec := &brief.Section{Topic: "T", Status: brief.StatusDone, Markdown: md}
	j := &brief.Journal{Date: "2026-10-29", TokenBudget: 1_000_000,
		Overview: brief.Section{Topic: "Overview", Status: brief.StatusDone, Markdown: "x"},
		Sections: []*brief.Section{sec}}
	m := New(nil, nil, j, "dark")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.selectSection(1)
	plain := ansi.Strip(m.content)
	if !strings.Contains(plain, " ACT  ↻ Update · Widget patched") {
		t.Errorf("no ACT badge on the update:\n%s", plain)
	}
	if !strings.Contains(m.content, strings.TrimSuffix(actBadge.Render("x"), "x\x1b[0m")) {
		t.Error("ACT badge not coloured")
	}
	if strings.Contains(m.content, strings.TrimSuffix(watchBadge.Render("x"), "x\x1b[0m")) {
		t.Errorf("a later-line tag became a badge:\n%s", plain)
	}
}
