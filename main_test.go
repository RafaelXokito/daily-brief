package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/RafaelXokito/daily-brief/internal/brief"
)

type quitModel struct{}

func (quitModel) Init() tea.Cmd                       { return tea.Quit }
func (quitModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return quitModel{}, nil }
func (quitModel) View() string                        { return "" }

// With the mouse on, the program takes it with cell-motion tracking only
// (1002, SGR 1006), never all-motion (1003), and gives it back on quit.
// With it off, it never captures the mouse, so native text selection, OSC 8
// hover and Cmd-click work as without the TUI.
func TestProgramOptionsMouse(t *testing.T) {
	// 1000-1003 turn on mouse tracking; 1005, 1006, 1015 and 1016 its
	// encodings.
	modes := []string{"\x1b[?1000h", "\x1b[?1001h", "\x1b[?1002h", "\x1b[?1003h",
		"\x1b[?1005h", "\x1b[?1006h", "\x1b[?1015h", "\x1b[?1016h"}
	for _, mouse := range []bool{true, false} {
		done := make(chan string, 1)
		go func() {
			var out bytes.Buffer
			opts := append(programOptions(mouse), tea.WithInput(nil), tea.WithOutput(&out))
			if _, err := tea.NewProgram(quitModel{}, opts...).Run(); err != nil {
				t.Error(err)
			}
			done <- out.String()
		}()
		var out string
		select {
		case out = <-done:
		case <-time.After(30 * time.Second):
			t.Fatalf("mouse %v: the program did not quit", mouse)
		}
		if !strings.Contains(out, "\x1b[?1049h") {
			t.Errorf("mouse %v: the program did not enter the alternate screen: %q", mouse, out)
		}
		if !mouse {
			for _, mode := range modes {
				if strings.Contains(out, mode) {
					t.Errorf("mouse off: the program captures the mouse with %q", mode)
				}
			}
			continue
		}
		on := strings.LastIndex(out, "\x1b[?1002h")
		if on < 0 || !strings.Contains(out, "\x1b[?1006h") {
			t.Errorf("mouse on: no cell-motion tracking in SGR mode: %q", out)
		}
		for _, mode := range []string{"\x1b[?1000h", "\x1b[?1001h", "\x1b[?1003h", "\x1b[?1005h", "\x1b[?1015h", "\x1b[?1016h"} {
			if strings.Contains(out, mode) {
				t.Errorf("mouse on: the program uses %q", mode)
			}
		}
		// Quitting resets tracking and its encoding, and leaves the
		// alternate screen.
		rest := out[max(0, on):]
		for _, reset := range []string{"\x1b[?1002l", "\x1b[?1006l", "\x1b[?1049l"} {
			if !strings.Contains(rest, reset) {
				t.Errorf("mouse on: %q not sent on quit: %q", reset, out)
			}
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
