package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/RafaelXokito/daily-brief/internal/brief"
)

// The marker replaces every plain "Update:" heading prefix the prompt asks
// for, at heading levels 2-4, in any case, and nothing else.
func TestMarkUpdatesPlainForms(t *testing.T) {
	for in, want := range map[string]string{
		"### Update: Widget":                "### ↻ Update · Widget",
		"## update:Widget":                  "## ↻ Update · Widget",
		"#### UPDATE:   Widget":             "#### ↻ Update · Widget",
		"### Updated: Widget":               "### Updated: Widget",
		"### Widget Update: v2":             "### Widget Update: v2",
		"Update: not a heading":             "Update: not a heading",
		"# Update: top level":               "# Update: top level",
		"x\n### Update: A\ny\n## Update: B": "x\n### ↻ Update · A\ny\n## ↻ Update · B",
	} {
		if got := markUpdates(in); got != want {
			t.Errorf("markUpdates(%q) = %q, want %q", in, got, want)
		}
	}
}

// The marker handles the bold, spaced and bullet forms brief counts as
// updates (see Section.Updates), leaving no stray bold marker.
func TestUpdateMarkerDropsBoldMarkers(t *testing.T) {
	for in, want := range map[string]string{
		"### **Update:** Widget breach grows": "### ↻ Update · Widget breach grows",
		"### **Update: Widget breach grows**": "### ↻ Update · Widget breach grows",
		"### Update : Widget":                 "### ↻ Update · Widget",
		"- **Update:** Widget":                "- ↻ Update · Widget",
		"1. **Update: Widget**":               "1. ↻ Update · Widget",
		"**Update:** Widget":                  "↻ Update · Widget",
	} {
		got := markUpdates(in)
		if got != want || strings.Count(got, "**")%2 == 1 || strings.Contains(got, "· **") {
			t.Errorf("markUpdates(%q) = %q, want %q (no stray **)", in, got, want)
		}
	}
}

// The sidebar count (brief's Section.Updates) and the marker recognise
// the same forms.
func TestUpdateCountMatchesMarker(t *testing.T) {
	for _, md := range []string{
		"### Update : Widget\nx", "- **Update:** Widget\n- Other", "### **Update: A**\nx\n\n### B\ny\n\n### update:C\nz",
		"Update: not an item\n\n### Updated: no\n\n# Update: top level", "**Update:** Bold lead\n\n**Other** x",
	} {
		s := brief.Section{Markdown: md}
		if marked := strings.Count(markUpdates(md), "↻ Update"); marked != s.Updates() {
			t.Errorf("%q: counted %d, marked %d", md, s.Updates(), marked)
		}
	}
}

// A topic row of the sidebar stays on one line, the name cut by cell
// width to leave room for the update and token counts: a long name (the
// default "Finance & Investing" is 19 runes) with a six-character token
// count (e.g. "150.0k"), and names of wide emoji or CJK characters.
func TestSidebarRowsFitTheBox(t *testing.T) {
	for _, name := range []string{"Finance & Investing", "🚀🌍 Space and Rockets 🛰️ Launches", "日本の経済ニュースと市場の動向まとめ", "Ünïcödé 🇵🇹 Portugal"} {
		for _, n := range []int{0, 1, 12} {
			var md strings.Builder
			for i := range n {
				fmt.Fprintf(&md, "### Update: item %d\nx\n\n", i)
			}
			md.WriteString("### New\nx")
			j := &brief.Journal{Date: "2026-10-29", TokenBudget: 740_000,
				Overview: brief.Section{Topic: "Overview", Status: brief.StatusDone, Markdown: "x"},
				Sections: []*brief.Section{{Topic: name, Status: brief.StatusDone, Markdown: md.String(), TokensUsed: 150_000}}}
			m := New(nil, nil, j, "notty")
			m.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
			for _, selected := range []int{0, 1} {
				m.selected = selected
				rows := strings.Split(strings.TrimSuffix(m.sidebar(), "\n"), "\n")
				if len(rows) != 2 || !strings.Contains(rows[1], "150.0k") {
					t.Errorf("%q, %d updates: rows %q", name, n, rows)
				}
				for _, l := range rows {
					if w := lipgloss.Width(l); w > sidebarWidth-1 {
						t.Errorf("%q, %d updates: sidebar row %q is %d cells, the box holds %d", name, n, l, w, sidebarWidth-1)
					}
				}
			}
			if v := m.View(); !strings.Contains(v, "150.0k │") {
				t.Errorf("%q, %d updates: the token count is not at the end of its row:\n%s", name, n, v)
			}
		}
	}
}

// The header after several runs, the last of them failed, shows that run
// against its own budget, never over 100%, and today's total.
func TestHeaderAfterFailedRerun(t *testing.T) {
	j := &brief.Journal{Date: "2026-10-29", TokenBudget: 390_000,
		Overview: brief.Section{Topic: "Overview", Status: brief.StatusDone, Markdown: "## Today's highlights\n\n- x", TokensUsed: 4_000},
		Sections: []*brief.Section{
			{Topic: "A", Status: brief.StatusDone, Markdown: "### Second A\nx", TokensUsed: 139_900, Error: "refreshing failed (boom); showing the earlier digest"},
			{Topic: "B", Status: brief.StatusDone, Markdown: "### First B\nx", TokensUsed: 170_000, Error: "refreshing failed (boom); showing the earlier digest"},
		},
		Runs: []brief.RunRecord{
			{Budget: 585_000, Used: 525_800},
			{Topics: []string{"A", "B"}, Budget: 390_000, Used: 268_100, Failed: 268_100},
			{Topics: []string{"A"}, Budget: 195_000, Used: 139_900},
			{Topics: []string{"A", "B"}, Budget: 390_000, Used: 268_100, Failed: 268_100},
		}}
	m := New(nil, nil, j, "notty")
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 20})
	header := strings.SplitN(m.View(), "\n", 2)[0]
	for _, want := range []string{"268.1k / 390.0k tokens (69%)", "268.1k on failed topics", "1.2M today in 4 runs"} {
		if !strings.Contains(header, want) {
			t.Errorf("header %q lacks %q", header, want)
		}
	}
	if strings.Contains(header, "over budget") {
		t.Errorf("header %q claims an overshoot", header)
	}
}

// The TUI's tests never see the user's journals.
func TestTUIDataDirIsIsolated(t *testing.T) {
	home, _ := os.UserHomeDir()
	real := filepath.Join(home, ".local", "share", "daily-brief")
	if d := brief.DataDir(); strings.HasPrefix(d, real) || !strings.Contains(d, "daily-brief-test-") {
		t.Errorf("DataDir %q is not isolated", d)
	}
}
