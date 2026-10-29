package main

import (
	"bytes"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
	// 1000, 1002 and 1003 turn on mouse tracking; 1006 its SGR encoding.
	for _, mode := range []string{"\x1b[?1000h", "\x1b[?1002h", "\x1b[?1003h", "\x1b[?1006h"} {
		if strings.Contains(out.String(), mode) {
			t.Errorf("the program captures the mouse with %q", mode)
		}
	}
}
