package main

import (
	"bytes"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/RafaelXokito/daily-brief/internal/brief"
)

type quitModel struct{}

func (quitModel) Init() tea.Cmd                       { return tea.Quit }
func (quitModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return quitModel{}, nil }
func (quitModel) View() string                        { return "" }

// The program uses the alternate screen but never captures the mouse, so
// native text selection always works.
func TestProgramOptionsLeaveTheMouseAlone(t *testing.T) {
	var out bytes.Buffer
	opts := append(programOptions(), tea.WithInput(nil), tea.WithOutput(&out))
	if _, err := tea.NewProgram(quitModel{}, opts...).Run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\x1b[?1049h") {
		t.Errorf("the program did not enter the alternate screen: %q", out.String())
	}
	// 1000-1003 turn on mouse tracking; 1005, 1006, 1015 and 1016 its
	// encodings. Any of them would take the mouse from the terminal, and
	// with it native selection, OSC 8 hover and Cmd-click.
	for _, mode := range []string{"\x1b[?1000h", "\x1b[?1001h", "\x1b[?1002h", "\x1b[?1003h",
		"\x1b[?1005h", "\x1b[?1006h", "\x1b[?1015h", "\x1b[?1016h"} {
		if strings.Contains(out.String(), mode) {
			t.Errorf("the program captures the mouse with %q", mode)
		}
	}
}

// -print writes the journal's Markdown exactly when piped, and renders it
// with OSC 8 links in the terminal's width on a terminal.
func TestPrintOutput(t *testing.T) {
	const url = "https://www.example.com/a/very/long/path/that/would/wrap/badly?utm_source=x"
	j := &brief.Journal{Date: "2026-10-29", TokenBudget: 100_000,
		Overview: brief.Section{Status: brief.StatusDone, Markdown: "## Today's highlights\n\n- Thing [source](" + url + ")"},
		Sections: []*brief.Section{{Topic: "AI", Status: brief.StatusDone, Markdown: "### Thing happened today\nWhy it matters. [source](" + url + ")",
			Sources: []brief.Source{{Title: "Example", URL: url}}}}}
	if got := printOutput(j, false, 80, "dark"); got != j.Markdown() {
		t.Errorf("piped output differs from the Markdown:\n%q\n%q", got, j.Markdown())
	}
	for _, width := range []int{40, 80, 200} {
		got := printOutput(j, true, width, "dark")
		plain := ansi.Strip(got)
		if !strings.Contains(got, "\x1b]8;") || !strings.Contains(got, url) || strings.Contains(plain, "http") ||
			strings.Contains(plain, "## ") || !strings.Contains(plain, "↗ example.com") {
			t.Errorf("width %d: terminal output:\n%s", width, plain)
		}
		for _, l := range strings.Split(got, "\n") {
			if lipgloss.Width(l) > width {
				t.Errorf("width %d: line %q too wide", width, ansi.Strip(l))
			}
		}
	}
}
