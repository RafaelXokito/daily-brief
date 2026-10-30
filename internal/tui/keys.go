package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/x/ansi"
)

// keyMap is the one table of keys: Update matches against it, and the
// help overlay and the help line are built from it.
type keyMap struct {
	Up, Down, Topics, Summary, Go, Back, Toggle     key.Binding
	PageDown, PageUp, HalfDown, HalfUp, Top, Bottom key.Binding
	Jump, Open, Copy, Reader                        key.Binding
	Help, Esc, Regenerate, Quit                     key.Binding
}

// A binding's first key is the one the help line shows. Descriptions are
// at most 22 cells, so that at 80 columns they fit beside the widest key
// column, and in a narrow pane on the line under their key.
var keys = keyMap{
	Up:         key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("", "prev topic/line/item")),
	Down:       key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("", "next topic/line/item")),
	Topics:     key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("", "focus Topics")),
	Summary:    key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("", "focus Summary to read")),
	Go:         key.NewBinding(key.WithKeys("enter"), key.WithHelp("", "read / go to highlight")),
	Back:       key.NewBinding(key.WithKeys("backspace"), key.WithHelp("", "back to the Overview")),
	Toggle:     key.NewBinding(key.WithKeys("tab", "shift+tab"), key.WithHelp("", "switch pane")),
	PageDown:   key.NewBinding(key.WithKeys(" ", "pgdown", "f", "shift+down"), key.WithHelp("", "page down")),
	PageUp:     key.NewBinding(key.WithKeys("b", "pgup", "shift+up"), key.WithHelp("", "page up")),
	HalfDown:   key.NewBinding(key.WithKeys("d", "ctrl+d"), key.WithHelp("", "half page down")),
	HalfUp:     key.NewBinding(key.WithKeys("u", "ctrl+u"), key.WithHelp("", "half page up")),
	Top:        key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("", "top")),
	Bottom:     key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("", "bottom")),
	Jump:       key.NewBinding(key.WithKeys("1", "2", "3", "4", "5", "6", "7", "8", "9"), key.WithHelp("1-9", "jump (1 = Overview)")),
	Open:       key.NewBinding(key.WithKeys("o"), key.WithHelp("", "open the item's source")),
	Copy:       key.NewBinding(key.WithKeys("y"), key.WithHelp("", "copy item and link")),
	Reader:     key.NewBinding(key.WithKeys("z"), key.WithHelp("", "reader mode on/off")),
	Help:       key.NewBinding(key.WithKeys("?"), key.WithHelp("", "show/close this help")),
	Esc:        key.NewBinding(key.WithKeys("esc"), key.WithHelp("", "close help / Topics")),
	Regenerate: key.NewBinding(key.WithKeys("r"), key.WithHelp("", "regenerate the journal")),
	Quit:       key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("", "quit (saves progress)")),
}

// all lists the bindings in the order the help overlay shows them.
func (k keyMap) all() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Topics, k.Summary, k.Go, k.Back, k.Toggle, k.PageDown, k.PageUp, k.HalfDown, k.HalfUp,
		k.Top, k.Bottom, k.Jump, k.Open, k.Copy, k.Reader, k.Help, k.Esc, k.Regenerate, k.Quit}
}

// keyName is how the help shows a key.
func keyName(k string) string {
	switch k {
	case "up":
		return "↑"
	case "down":
		return "↓"
	case "left":
		return "←"
	case "right":
		return "→"
	case "shift+up":
		return "shift+↑"
	case "shift+down":
		return "shift+↓"
	case " ":
		return "space"
	case "backspace":
		return "⌫"
	}
	return k
}

// keyNames is a binding's column in the help overlay: every key it has.
func keyNames(b key.Binding) string {
	if h := b.Help().Key; h != "" {
		return h
	}
	var names []string
	for _, k := range b.Keys() {
		names = append(names, keyName(k))
	}
	return strings.Join(names, " ")
}

// helpItem is an entry of the help line: the first key of each binding,
// then a word.
type helpItem struct {
	word     string
	bindings []key.Binding
}

func (h helpItem) String() string {
	var names []string
	for _, b := range h.bindings {
		names = append(names, keyName(b.Keys()[0]))
	}
	return strings.Join(names, "/") + " " + h.word
}

// helpMode is what the help line describes: the Topics pane, or what the
// Summary pane shows.
type helpMode int

const (
	modeSummary    helpMode = iota // the Overview as Markdown
	modeTopics                     // the Topics pane has focus
	modeHighlights                 // the Overview's highlights
	modeItems                      // a topic
	modeItemsBack                  // a topic, with highlights to go back to
)

// helpLine is the help line for mode, cut to width cells so that it still
// ends with "? help".
func helpLine(mode helpMode, generating bool, width int) string {
	var (
		scroll = helpItem{"scroll", []key.Binding{keys.Up, keys.Down}}
		page   = helpItem{"page", []key.Binding{keys.PageDown, keys.PageUp}}
		topics = helpItem{"topics", []key.Binding{keys.Topics}}
		open   = helpItem{"open", []key.Binding{keys.Open}}
		yank   = helpItem{"copy", []key.Binding{keys.Copy}}
		reader = helpItem{"reader", []key.Binding{keys.Reader}}
	)
	var items []helpItem
	switch mode {
	case modeTopics:
		items = []helpItem{{"topic", []key.Binding{keys.Up, keys.Down}}, {"read", []key.Binding{keys.Summary}}, page, reader}
	case modeHighlights:
		items = []helpItem{{"highlight", []key.Binding{keys.Up, keys.Down}}, {"go", []key.Binding{keys.Go}}, open, yank, topics, reader}
	case modeItems:
		items = []helpItem{scroll, page, open, yank, topics, reader}
	case modeItemsBack:
		items = []helpItem{scroll, page, {"back", []key.Binding{keys.Back}}, open, yank, topics, reader}
	default:
		items = []helpItem{scroll, page, topics, reader}
	}
	end := []helpItem{{"regenerate", []key.Binding{keys.Regenerate}}, {"quit", []key.Binding{keys.Quit}}}
	if generating {
		end = []helpItem{{"quit (cancels run)", []key.Binding{keys.Quit}}}
	}
	help := helpItem{"help", []key.Binding{keys.Help}}.String()
	var shown, rest []string
	for _, it := range items {
		shown = append(shown, it.String())
	}
	shown = append(shown, help)
	for _, it := range append(items, end...) {
		rest = append(rest, it.String())
	}
	for _, it := range end {
		shown = append(shown, it.String())
	}
	return fitHelp(shown, rest, help, width)
}

// overlayHelpLine is the help line while the key list is open: how to
// scroll it and how to close it.
func overlayHelpLine(width int) string {
	items := []helpItem{{"scroll", []key.Binding{keys.Up, keys.Down}}, {"page", []key.Binding{keys.PageDown, keys.PageUp}},
		{"top/bottom", []key.Binding{keys.Top, keys.Bottom}}}
	closer := helpItem{"close", []key.Binding{keys.Help, keys.Esc}}.String()
	quit := helpItem{"quit", []key.Binding{keys.Quit}}.String()
	var shown []string
	for _, it := range items {
		shown = append(shown, it.String())
	}
	return fitHelp(append(shown, closer, quit), append(shown, quit), closer, width)
}

// fitHelp joins shown if it fits in width cells, and otherwise cuts rest
// to end with "… keep".
func fitHelp(shown, rest []string, keep string, width int) string {
	if full := strings.Join(shown, " · "); ansi.StringWidth(full) <= width {
		return full
	}
	tail := "… " + keep
	return ansi.Truncate(strings.Join(rest, " · "), max(0, width-ansi.StringWidth(tail)), "") + truncate(tail, width)
}
