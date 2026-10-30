package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// wheelLines is how many lines a wheel step scrolls, as the viewport's
// own wheel handling would.
const wheelLines = 3

// region is the part of the screen a mouse event is over.
type region int

const (
	regionNone    region = iota // the header, pane titles, help line, a border or the gap
	regionTopic                 // a topic row of the Topics pane
	regionSidebar               // the Topics pane off its topic rows: a "more" marker, the rows below
	regionSummary               // the Summary pane's body
)

// geometry is where View drew the panes, as hitTest needs it.
type geometry struct {
	top        int  // the first row of the panes' bodies; -1 if none was drawn
	height     int  // their rows
	sidebar    bool // the Topics pane shows: the full layout, not reader mode
	topics     int  // the Topics pane's entries, the Overview first
	sidebarTop int  // its first entry shown, when they don't all fit
	selected   int  // the selected entry, which the window keeps in view
}

// hit is what a mouse event at a cell is over: its region and, for
// regionTopic, the entry (0 = Overview), for regionSummary, the row in the
// Summary pane's body.
type hit struct {
	region region
	index  int
}

// geometry is the layout View last drew.
func (m *Model) geometry() geometry {
	return geometry{
		top:        m.bodyTop,
		height:     m.viewport.Height,
		sidebar:    m.layout() == layoutFull && !m.reader,
		topics:     len(m.journal.Sections) + 1,
		sidebarTop: m.sidebarTop,
		selected:   m.selected,
	}
}

// hitTest is what the cell at column x, row y is over in g. The Topics
// pane is sidebarWidth cells, its right border and a one-cell gap, and the
// Summary pane fills the rest of the width; both are g.height rows from
// g.top. Its topic rows are those sidebarView shows, between its "↑ more"
// and "↓ more" markers.
func hitTest(g geometry, x, y int) hit {
	row := y - g.top
	if g.top < 0 || x < 0 || row < 0 || row >= g.height {
		return hit{region: regionNone}
	}
	if !g.sidebar {
		return hit{regionSummary, row}
	}
	switch {
	case x < sidebarWidth:
	case x < sidebarWidth+2:
		return hit{region: regionNone}
	default:
		return hit{regionSummary, row}
	}
	top, slots := sidebarWindow(g.topics, g.height, g.sidebarTop, g.selected)
	if top > 0 {
		row-- // the ↑ marker
	}
	if row < 0 || row >= slots {
		return hit{region: regionSidebar}
	}
	return hit{regionTopic, top + row}
}

// highlightAt is the highlight the content line is on, given the lines of
// content and where each highlight starts, or -1: a highlight's lines run
// from its start to the blank line after it.
func highlightAt(content string, starts []int, line int) int {
	lines := strings.Split(content, "\n")
	for k := len(starts) - 1; k >= 0; k-- {
		if starts[k] > line {
			continue
		}
		for l := starts[k]; l <= line; l++ {
			if l >= len(lines) || strings.TrimSpace(ansi.Strip(lines[l])) == "" {
				return -1
			}
		}
		return k
	}
	return -1
}

// handleMouse acts on a press of the left button or a wheel step; motion,
// releases and other buttons do nothing. A click on a topic opens it, a
// click on the Summary focuses it, on the Overview's highlights goes to
// the one clicked, as enter does, and on a topic's item focuses that item.
// The wheel moves the selected topic over the Topics pane and scrolls the
// Summary elsewhere, refocusing an item if the focused one leaves view. While Settings
// is open clicks and the wheel do nothing; over the list of keys the wheel
// scrolls it.
func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if msg.Action != tea.MouseActionPress || !m.ready || m.journal == nil {
		return nil
	}
	wheel := 0
	switch msg.Button {
	case tea.MouseButtonLeft:
	case tea.MouseButtonWheelUp:
		wheel = -1
	case tea.MouseButtonWheelDown:
		wheel = 1
	default:
		return nil
	}
	switch {
	case m.set != nil:
		return nil
	case m.showHelp:
		if wheel < 0 {
			m.keysView.ScrollUp(wheelLines)
		} else if wheel > 0 {
			m.keysView.ScrollDown(wheelLines)
		}
		return nil
	}
	h := hitTest(m.geometry(), msg.X, msg.Y)
	if wheel != 0 {
		switch {
		case h.region == regionTopic || h.region == regionSidebar:
			// Unlike ↑/↓ the wheel stops at the ends rather than wrapping.
			if i := m.selected + wheel; i >= 0 && i <= len(m.journal.Sections) {
				m.selectSection(i)
			}
		case wheel < 0:
			m.viewport.ScrollUp(wheelLines)
			m.reanchor()
		default:
			m.viewport.ScrollDown(wheelLines)
			m.reanchor()
		}
		return nil
	}
	switch h.region {
	case regionTopic:
		if h.index != m.selected {
			m.selectSection(h.index)
		}
		m.focus = focusSummary
	case regionSummary:
		m.focus = focusSummary
		if m.onHighlights() {
			if k := highlightAt(m.content, m.hlLines, m.viewport.YOffset+h.index); k >= 0 {
				m.cursor = k
				m.jump()
			}
		} else if k := m.itemAtLine(m.viewport.YOffset + h.index); k >= 0 {
			// A click on an item of a topic focuses it; one on a heading,
			// Related or Sources only focuses the pane.
			m.setFocus(k)
		}
	}
	return nil
}

// applyMouse turns the mouse on or off for this session.
func (m *Model) applyMouse(on bool) tea.Cmd {
	if on == m.mouse {
		return nil
	}
	m.mouse = on
	if m.ready {
		m.refreshKeys()
	}
	if on {
		return tea.EnableMouseCellMotion
	}
	return tea.DisableMouse
}
