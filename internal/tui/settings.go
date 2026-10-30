package tui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/RafaelXokito/daily-brief/internal/brief"
	"github.com/RafaelXokito/daily-brief/internal/config"
)

// Settings is what the Settings pane needs from main. Without a Path, s
// only says that settings are unavailable.
type Settings struct {
	// Path is the config file the pane edits.
	Path string
	// Overrides are this session's one-off flags, such as "provider=aws".
	// The pane names them; it never saves them.
	Overrides []string
	// Rebuild builds a generator for a saved config the way main built
	// the first one, this session's overrides included. Its error (such as
	// missing credentials) keeps the generator there is.
	Rebuild func(config.Config) (*brief.Generator, error)
	// Theme resolves a configured theme (auto, dark or light) to dark or
	// light, and sets the colours that follow it.
	Theme func(string) string
}

// WithSettings lets s open the Settings pane over the config main loaded.
func (m *Model) WithSettings(s Settings) *Model {
	m.settings = s
	return m
}

// settingsKeys are the Settings pane's own keys; keys.Up and keys.Down
// select a field, and keys.Quit quits as everywhere.
var settingsKeys = struct {
	Edit, Prev, Next, Save, Editor, Reload, Add, Delete, Yes, No key.Binding
}{
	Edit:   key.NewBinding(key.WithKeys("enter", "right")),
	Prev:   key.NewBinding(key.WithKeys("left")),
	Next:   key.NewBinding(key.WithKeys(" ")),
	Save:   key.NewBinding(key.WithKeys("ctrl+s")),
	Editor: key.NewBinding(key.WithKeys("e")),
	Reload: key.NewBinding(key.WithKeys("R")),
	Add:    key.NewBinding(key.WithKeys("a")),
	Delete: key.NewBinding(key.WithKeys("x")),
	Yes:    key.NewBinding(key.WithKeys("y", "Y")),
	No:     key.NewBinding(key.WithKeys("n", "N", "esc")),
}

type fieldKind int

const (
	kindEnum    fieldKind = iota
	kindInt               // digits only
	kindFloat             // digits and a point
	kindText              // one line
	kindProfile           // the reader profile: a text area
	kindMute              // the mute list
	kindTopics            // read-only here
)

// field is a config key the pane shows; ptr points at its value in a
// config (a *string, *int, *int64 or *float64, or a **bool for an on/off
// kindEnum).
type field struct {
	key   string
	kind  fieldKind
	opts  []string // kindEnum's values; the first stands for ""
	rng   string   // kindInt's range, shown while editing
	show  func(config.Config) bool
	ptr   func(*config.Config) any
	limit int // characters a kindText takes
}

func providerIs(types ...string) func(config.Config) bool {
	return func(c config.Config) bool { return slices.Contains(types, c.Provider.Type) }
}

// fields are the settings in the order the pane lists them.
var fields = []field{
	{key: "provider.type", kind: kindEnum, opts: []string{"claude-code", "anthropic", "aws", "vertex"},
		ptr: func(c *config.Config) any { return &c.Provider.Type }},
	{key: "provider.auth", kind: kindEnum, opts: []string{"keychain", "profile", "env"}, show: providerIs("anthropic"),
		ptr: func(c *config.Config) any { return &c.Provider.Auth }},
	{key: "provider.profile", kind: kindText, limit: 200, show: func(c config.Config) bool {
		return c.Provider.Type == "aws" || c.Provider.Type == "anthropic" && c.Provider.Auth == "profile"
	}, ptr: func(c *config.Config) any { return &c.Provider.Profile }},
	{key: "provider.region", kind: kindText, limit: 100, show: providerIs("aws", "vertex"),
		ptr: func(c *config.Config) any { return &c.Provider.Region }},
	{key: "provider.workspace_id", kind: kindText, limit: 200, show: providerIs("aws"),
		ptr: func(c *config.Config) any { return &c.Provider.WorkspaceID }},
	{key: "provider.project_id", kind: kindText, limit: 200, show: providerIs("vertex"),
		ptr: func(c *config.Config) any { return &c.Provider.ProjectID }},
	{key: "provider.claude_path", kind: kindText, limit: 1000, show: providerIs("claude-code"),
		ptr: func(c *config.Config) any { return &c.Provider.ClaudePath }},
	{key: "model", kind: kindText, limit: 200, ptr: func(c *config.Config) any { return &c.Model }},
	{key: "effort", kind: kindEnum, opts: []string{"low", "medium", "high", "xhigh", "max"},
		ptr: func(c *config.Config) any { return &c.Effort }},
	{key: "theme", kind: kindEnum, opts: []string{"auto", "dark", "light"}, ptr: func(c *config.Config) any { return &c.Theme }},
	{key: "mouse", kind: kindEnum, opts: []string{"on", "off"}, ptr: func(c *config.Config) any { return &c.Mouse }},
	{key: "token_budget", kind: kindInt, rng: "0 = auto, or at least 10000", ptr: func(c *config.Config) any { return &c.TokenBudget }},
	{key: "max_searches_per_topic", kind: kindInt, rng: "0 = auto, or 1-50", ptr: func(c *config.Config) any { return &c.MaxSearchesPerTopic }},
	{key: "max_read_minutes", kind: kindInt, rng: "1-120", ptr: func(c *config.Config) any { return &c.MaxReadMinutes }},
	{key: "history_days", kind: kindInt, rng: "0-14, 0 = off", ptr: func(c *config.Config) any { return &c.HistoryDays }},
	{key: "concurrency", kind: kindInt, rng: "1-16", ptr: func(c *config.Config) any { return &c.Concurrency }},
	{key: "country", kind: kindText, limit: 100, ptr: func(c *config.Config) any { return &c.Country }},
	{key: "city", kind: kindText, limit: 100, ptr: func(c *config.Config) any { return &c.City }},
	{key: "profile", kind: kindProfile},
	{key: "mute", kind: kindMute},
	{key: "price_input_per_mtok", kind: kindFloat, rng: "USD, 0-1000", ptr: func(c *config.Config) any { return &c.PriceInputPerMTok }},
	{key: "price_output_per_mtok", kind: kindFloat, rng: "USD, 0-1000", ptr: func(c *config.Config) any { return &c.PriceOutputPerMTok }},
	{key: "price_per_1k_searches", kind: kindFloat, rng: "USD, 0-1000", ptr: func(c *config.Config) any { return &c.PricePer1KSearches }},
	{key: "topics", kind: kindTopics},
}

// labelWidth is the widest key, the label column's width.
var labelWidth = func() int {
	w := 0
	for _, f := range fields {
		w = max(w, len(f.key))
	}
	return w
}()

// setMode is what the Settings pane's keys act on.
type setMode int

const (
	setList     setMode = iota // the list of fields
	setEdit                    // a field's input
	setMute                    // the mute list's entries
	setMuteEdit                // an entry's input
	setConfirm                 // a y/n question
)

// settingsPane is the open Settings pane: a copy of the config file being
// edited, never the session's config with its overrides.
type settingsPane struct {
	cfg, saved config.Config // as edited, and as in the file
	stamp      config.Stamp  // the file as loaded or last saved
	broken     error         // why the file could not be read; only e, R and esc work
	err        error         // cfg.Validate(): saving is blocked while set
	inputErr   string        // why the input could not be taken
	notice     string        // what happened to the file, such as a refused save
	assess     *brief.Assessment

	sel, top int // the selected field among those shown, the first line shown
	mode     setMode
	input    textinput.Model
	area     textarea.Model
	entry    int // the selected mute entry
	adding   bool
	hidden   bool // the input started empty over a value that looks like a key

	loaded bool   // saved holds the file as last read
	theme0 string // the theme shown when the pane opened
	// mouse0 is the session's mouse setting when the pane opened, or as
	// last saved or reloaded: what closing without saving goes back to.
	mouse0 bool

	question string         // the y/n question in setConfirm
	answer   func() tea.Cmd // what y does
	back     setMode        // where n goes
}

type rebuiltMsg struct {
	seq int
	gen *brief.Generator
	err error
}

// rebuildJob builds a generator once, for whichever asks first: the
// command that reports it, or r wanting it before that report arrives.
type rebuildJob struct {
	seq   int
	once  sync.Once
	build func() (*brief.Generator, error)
	gen   *brief.Generator
	err   error
}

func (j *rebuildJob) get() rebuiltMsg {
	j.once.Do(func() { j.gen, j.err = j.build() })
	return rebuiltMsg{j.seq, j.gen, j.err}
}

type editorDoneMsg struct{ err error }

// EditorCommand runs $EDITOR (vi if unset) on path through the shell, as
// git does: $EDITOR may hold arguments or quoting ("code --wait"), and
// path, passed as $1, may hold spaces. The pane and -edit both use it.
func EditorCommand(path string) *exec.Cmd {
	return exec.Command("sh", "-c", `eval "exec ${EDITOR:-vi}"' "$1"'`, "sh", path)
}

// openEditor runs EditorCommand in the terminal and reports when it exits.
func openEditor(path string, done func(error) tea.Msg) tea.Cmd {
	return tea.ExecProcess(EditorCommand(path), done)
}

func (p *settingsPane) shown() []field {
	var out []field
	for _, f := range fields {
		if f.show == nil || f.show(p.cfg) {
			out = append(out, f)
		}
	}
	return out
}

func (p *settingsPane) field() field {
	fs := p.shown()
	p.sel = min(max(0, p.sel), len(fs)-1)
	return fs[p.sel]
}

func (p *settingsPane) dirty() bool { return !reflect.DeepEqual(p.cfg, p.saved) }

// check cleans and validates the edited config and estimates its run.
func (p *settingsPane) check() {
	p.cfg.Clean()
	p.err = p.cfg.Validate()
	p.assess = nil
	if p.err == nil {
		c := p.cfg.Clone()
		brief.ResolveBudget(&c)
		a := brief.Assess(c)
		p.assess = &a
	}
}

// openSettings opens the pane over the config file as it is now.
func (m *Model) openSettings() tea.Cmd {
	if m.settings.Path == "" {
		return m.flashMsg("Settings are not available")
	}
	m.set = &settingsPane{theme0: m.theme, mouse0: m.mouse}
	m.focus = focusSummary
	m.loadSettings()
	return nil
}

// loadSettings (re)reads the file into the pane, discarding its edits and
// showing the file's theme. It reports whether the file differs from what
// the pane had loaded before.
func (m *Model) loadSettings() bool {
	p := m.set
	cfg, st, err := config.LoadStamped(m.settings.Path)
	changed := st.Sum != p.stamp.Sum
	p.broken, p.notice, p.inputErr, p.mode = err, "", "", setList
	if err == nil {
		if p.loaded && cfg.MouseOn() != p.saved.MouseOn() {
			// The file's mouse setting changed on disk: it replaces the
			// session's, as a save of it would.
			p.mouse0 = cfg.MouseOn()
		}
		p.cfg, p.saved, p.stamp, p.loaded = cfg, cfg.Clone(), st, true
		p.check()
	} else {
		p.cfg = p.saved.Clone()
	}
	m.showSavedTheme()
	return changed && err == nil
}

// showSavedTheme drops a theme being tried: the file's as last read, or
// the one shown at opening if it could not be read.
func (m *Model) showSavedTheme() {
	if p := m.set; p.loaded {
		m.applyTheme(p.saved.Theme)
	} else if m.theme != p.theme0 {
		m.theme = p.theme0
		m.refresh()
		m.refreshKeys()
	}
}

// closeSettings closes the pane, dropping its edits and any theme or
// mouse setting being tried.
func (m *Model) closeSettings() tea.Cmd {
	m.showSavedTheme()
	mouse := m.set.mouse0
	m.set = nil
	return m.applyMouse(mouse)
}

// applyTheme shows the journal in the configured theme name.
func (m *Model) applyTheme(name string) {
	t := name
	if m.settings.Theme != nil {
		t = m.settings.Theme(name)
	} else if t != themeDark && t != themeLight {
		return
	}
	if t != m.theme {
		m.theme = t
		m.refresh()
		m.refreshKeys()
	}
}

// saveSettings writes the edits to the file, keeping its comments, and
// rebuilds the generator for the next run.
func (m *Model) saveSettings() tea.Cmd {
	p := m.set
	switch {
	case p.broken != nil:
		return m.flashMsg("Nothing to save")
	case p.err != nil:
		return m.flashMsg("Fix the error before saving")
	case !p.dirty():
		return m.flashMsg("No changes to save")
	}
	st, err := config.SaveEdits(m.settings.Path, p.stamp, p.cfg)
	switch {
	case errors.Is(err, config.ErrChanged):
		p.notice = "Not saved: the file changed on disk since it was opened. Press R to reload it (your edits here are dropped)."
		return nil
	case err != nil:
		p.notice = "Not saved: " + err.Error()
		return nil
	}
	if p.cfg.MouseOn() != p.saved.MouseOn() {
		// Only a saved change of the mouse setting replaces the session's,
		// which m may have toggled.
		p.mouse0 = p.cfg.MouseOn()
	}
	p.stamp, p.saved, p.notice = st, p.cfg.Clone(), ""
	return tea.Batch(m.rebuild(p.saved), m.flashMsg("saved · press r to regenerate"))
}

// rebuild builds a generator for cfg off the event loop; Update swaps it
// in once no run is using the old one. Until then r takes it from the
// job (see building), so a run started right after saving uses it.
func (m *Model) rebuild(cfg config.Config) tea.Cmd {
	build := m.settings.Rebuild
	if build == nil {
		return nil
	}
	m.rebuilds++
	cfg = cfg.Clone()
	job := &rebuildJob{seq: m.rebuilds, build: func() (*brief.Generator, error) { return build(cfg) }}
	m.building = job
	return func() tea.Msg { return job.get() }
}

// rebuilt takes the latest save's generator: at once if no run is in
// progress, else when it ends. The latest save wins: if its generator
// could not be built, an older one waiting for the run to end is dropped
// too, and runs keep the generator there is.
func (m *Model) rebuilt(msg rebuiltMsg) tea.Cmd {
	if msg.seq != m.rebuilds {
		return nil
	}
	m.building = nil
	if msg.err != nil || msg.gen == nil {
		why := "no generator"
		if msg.err != nil {
			why = msg.err.Error()
		}
		m.pending = nil
		if m.gen == nil {
			m.genErr = errors.New(why)
		}
		note := "Saved, but these settings can't be used (" + why + "); runs keep the previous connection"
		if m.set != nil {
			m.set.notice = note
		}
		if !m.generating {
			m.status = note
		}
		return m.flashMsg(note)
	}
	switch {
	case msg.gen == m.gen:
	case m.generating:
		m.pending = msg.gen
	default:
		m.gen, m.genErr, m.pending, m.status = msg.gen, nil, nil, ""
	}
	return nil
}

// ask puts a y/n question; y runs answer.
func (p *settingsPane) ask(question string, answer func() tea.Cmd) {
	p.back, p.mode = p.mode, setConfirm
	p.question, p.answer = question, answer
}

// settingsKey handles a key while the pane is open. Esc steps back and
// never quits; ctrl+c always quits.
func (m *Model) settingsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.set
	if msg.String() == "ctrl+c" {
		return m, m.quit()
	}
	switch p.mode {
	case setConfirm:
		switch {
		case key.Matches(msg, settingsKeys.Yes):
			p.mode = p.back
			return m, p.answer()
		case key.Matches(msg, settingsKeys.No):
			p.mode = p.back
		}
		return m, nil
	case setEdit, setMuteEdit:
		return m, m.inputKey(msg)
	case setMute:
		return m, m.muteKey(msg)
	}
	f := p.field()
	if p.broken != nil {
		f = field{kind: kindTopics}
	}
	switch {
	case key.Matches(msg, keys.Quit):
		if !p.dirty() {
			return m, m.quit()
		}
		p.ask("Discard changes and quit? y/n", m.quit)
	case key.Matches(msg, keys.Esc, keys.Settings):
		if !p.dirty() {
			return m, m.closeSettings()
		}
		p.ask("Discard changes? y/n", m.closeSettings)
	case key.Matches(msg, keys.Up):
		p.sel = max(0, p.sel-1)
		p.inputErr = ""
	case key.Matches(msg, keys.Down):
		p.sel = min(len(p.shown())-1, p.sel+1)
		p.inputErr = ""
	case key.Matches(msg, settingsKeys.Save):
		return m, m.saveSettings()
	case key.Matches(msg, settingsKeys.Editor):
		edit := func() tea.Cmd {
			return m.editor(m.settings.Path, func(err error) tea.Msg { return editorDoneMsg{err} })
		}
		if p.dirty() {
			p.ask("Discard changes and open the file in $EDITOR? y/n", edit)
			return m, nil
		}
		return m, edit()
	case key.Matches(msg, settingsKeys.Reload):
		reload := func() tea.Cmd { return m.reloaded(m.loadSettings()) }
		if p.dirty() {
			p.ask("Discard changes and reload the file? y/n", reload)
			return m, nil
		}
		return m, reload()
	case f.kind == kindEnum && key.Matches(msg, settingsKeys.Prev):
		return m, m.cycle(f, -1)
	case f.kind == kindEnum && key.Matches(msg, settingsKeys.Edit, settingsKeys.Next):
		return m, m.cycle(f, 1)
	case f.kind == kindTopics && p.broken == nil && key.Matches(msg, settingsKeys.Edit):
		return m, m.flashMsg("Press e to edit topics in $EDITOR")
	case f.kind == kindMute && key.Matches(msg, settingsKeys.Edit):
		p.mode, p.entry = setMute, 0
	case key.Matches(msg, settingsKeys.Edit) && p.broken == nil && f.kind != kindTopics:
		m.startEdit(f)
	}
	return m, nil
}

// reloaded applies a file that changed on disk as a save does. The
// session's mouse setting stays unless the file's changed (see
// loadSettings), dropping one being tried.
func (m *Model) reloaded(changed bool) tea.Cmd {
	p := m.set
	if !changed || p.broken != nil || p.err != nil {
		return m.applyMouse(p.mouse0)
	}
	return tea.Batch(m.applyMouse(p.mouse0), m.rebuild(p.saved), m.flashMsg("reloaded · press r to regenerate"))
}

// editorDone reloads the file the editor may have changed. Bubble Tea
// turned mouse reporting off to run the editor and does not turn it back
// on, so it is enabled again whenever the session has the mouse.
func (m *Model) editorDone(msg editorDoneMsg) tea.Cmd {
	var cmd tea.Cmd
	if m.set != nil {
		cmd = m.reloaded(m.loadSettings())
		if msg.err != nil {
			m.set.notice = "Editor: " + msg.err.Error()
		}
	}
	if m.mouse {
		cmd = tea.Batch(cmd, tea.EnableMouseCellMotion)
	}
	return cmd
}

// cycle steps an enum field by d through its values.
func (m *Model) cycle(f field, d int) tea.Cmd {
	p := m.set
	v := enumValue(f, &p.cfg)
	i := slices.Index(f.opts, v)
	if v == "" {
		i = 0
	}
	n := len(f.opts)
	if i < 0 {
		i = n - 1
		if d < 0 {
			i = 0
		}
	}
	setEnum(f, &p.cfg, f.opts[((i+d)%n+n)%n])
	return m.committed(f)
}

// enumValue is a kindEnum field's value in c: its string, or on/off for
// a *bool, where unset is the first option.
func enumValue(f field, c *config.Config) string {
	switch v := f.ptr(c).(type) {
	case **bool:
		if *v == nil || **v {
			return f.opts[0]
		}
		return f.opts[1]
	case *string:
		return *v
	}
	return ""
}

// setEnum sets a kindEnum field in c to opt. A *bool gets a new pointer,
// never written through, as a config's clone may share it.
func setEnum(f field, c *config.Config, opt string) {
	switch v := f.ptr(c).(type) {
	case **bool:
		*v = new(opt == f.opts[0])
	case *string:
		*v = opt
	}
}

// committed checks the edited config after f changed; the theme and the
// mouse setting apply at once.
func (m *Model) committed(f field) tea.Cmd {
	m.set.check()
	m.set.inputErr = ""
	switch f.key {
	case "theme":
		m.applyTheme(m.set.cfg.Theme)
	case "mouse":
		return m.applyMouse(m.set.cfg.MouseOn())
	}
	return nil
}

func newInput(value string, limit int) textinput.Model {
	in := textinput.New()
	in.Prompt = ""
	in.CharLimit = limit
	in.Cursor.SetMode(cursor.CursorStatic)
	in.SetValue(value)
	in.CursorEnd()
	in.Focus()
	return in
}

// startEdit opens f's input.
func (m *Model) startEdit(f field) {
	p := m.set
	p.mode, p.inputErr = setEdit, ""
	switch f.kind {
	case kindProfile:
		p.area = textarea.New()
		p.area.Prompt = ""
		p.area.ShowLineNumbers = false
		p.area.KeyMap.InsertNewline.SetEnabled(false)
		p.area.Cursor.SetMode(cursor.CursorStatic)
		p.area.SetValue(p.unhidden(p.cfg.Profile))
		p.area.Focus()
	case kindInt:
		p.input = newInput(fmt.Sprint(reflect.ValueOf(f.ptr(&p.cfg)).Elem().Int()), 10)
	case kindFloat:
		p.input = newInput(strconv.FormatFloat(*f.ptr(&p.cfg).(*float64), 'f', -1, 64), 12)
	default:
		p.input = newInput(p.unhidden(*f.ptr(&p.cfg).(*string)), f.limit)
	}
}

// unhidden is v to edit: "" if it looks like a key, which is never shown,
// noting that an empty input keeps it.
func (p *settingsPane) unhidden(v string) string {
	p.hidden = looksLikeKey(v)
	if p.hidden {
		return ""
	}
	return v
}

// inputKey handles a key while a field or mute entry is being edited:
// enter takes it, esc drops it, ctrl+s takes it and saves.
func (m *Model) inputKey(msg tea.KeyMsg) tea.Cmd {
	p := m.set
	back := setList
	if p.mode == setMuteEdit {
		back = setMute
	}
	switch msg.String() {
	case "esc":
		p.mode, p.inputErr = back, ""
		return nil
	case "enter", "ctrl+s":
		if !m.takeInput() {
			return nil
		}
		p.mode = back
		if msg.String() == "ctrl+s" {
			return m.saveSettings()
		}
		return nil
	}
	if p.mode == setEdit {
		switch f := p.field(); f.kind {
		case kindProfile:
			p.area, _ = p.area.Update(msg)
			return nil
		case kindInt, kindFloat:
			if msg = numeric(msg, f.kind == kindFloat); msg.Type == tea.KeyRunes && len(msg.Runes) == 0 {
				return nil
			}
		}
	}
	p.input, _ = p.input.Update(msg)
	return nil
}

// numeric keeps only the digits (and points, if point) a key types; keys
// that don't type pass.
func numeric(msg tea.KeyMsg, point bool) tea.KeyMsg {
	if msg.Type == tea.KeySpace {
		return tea.KeyMsg{Type: tea.KeyRunes}
	}
	if msg.Type != tea.KeyRunes {
		return msg
	}
	var rs []rune
	for _, r := range msg.Runes {
		if r >= '0' && r <= '9' || point && r == '.' {
			rs = append(rs, r)
		}
	}
	msg.Runes = rs
	return msg
}

// takeInput puts the input's value in the edited config, or says why it
// can't.
func (m *Model) takeInput() bool {
	p := m.set
	if p.mode == setMuteEdit {
		v := strings.TrimSpace(p.input.Value())
		mute := slices.Clone(p.cfg.Mute)
		switch {
		case p.hidden && v == "":
		case p.adding && v != "":
			mute = append(mute, v)
			p.entry = len(mute) - 1
		case !p.adding && p.entry < len(mute):
			mute[p.entry] = v
		}
		p.cfg.Mute = mute
		m.committed(field{key: "mute"})
		p.entry = min(p.entry, max(0, len(p.cfg.Mute)-1))
		return true
	}
	f := p.field()
	v := strings.TrimSpace(p.input.Value())
	switch {
	case p.hidden && (f.kind == kindProfile && strings.TrimSpace(p.area.Value()) == "" || f.kind == kindText && v == ""):
		// An empty input over a hidden value keeps it.
		p.mode = setList
		return true
	}
	switch f.kind {
	case kindProfile:
		p.cfg.Profile = p.area.Value()
	case kindInt:
		if v == "" {
			v = "0"
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			p.inputErr = f.key + ": not a whole number"
			return false
		}
		switch ptr := f.ptr(&p.cfg).(type) {
		case *int64:
			*ptr = n
		case *int:
			*ptr = int(n)
		}
	case kindFloat:
		if v == "" {
			v = "0"
		}
		x, err := strconv.ParseFloat(v, 64)
		if err != nil {
			p.inputErr = f.key + ": not a number"
			return false
		}
		*f.ptr(&p.cfg).(*float64) = x
	default:
		*f.ptr(&p.cfg).(*string) = v
	}
	m.committed(f)
	return true
}

// muteKey handles a key on the mute list's entries.
func (m *Model) muteKey(msg tea.KeyMsg) tea.Cmd {
	p := m.set
	n := len(p.cfg.Mute)
	switch {
	case key.Matches(msg, keys.Esc, settingsKeys.Prev):
		p.mode = setList
	case key.Matches(msg, keys.Up):
		p.entry = max(0, p.entry-1)
	case key.Matches(msg, keys.Down):
		p.entry = min(max(0, n-1), p.entry+1)
	case key.Matches(msg, settingsKeys.Add):
		p.mode, p.adding, p.inputErr, p.hidden = setMuteEdit, true, "", false
		p.input = newInput("", 0)
	case key.Matches(msg, settingsKeys.Edit) && n > 0:
		p.mode, p.adding, p.inputErr = setMuteEdit, false, ""
		p.input = newInput(p.unhidden(p.cfg.Mute[p.entry]), 0)
	case key.Matches(msg, settingsKeys.Delete) && n > 0:
		p.cfg.Mute = slices.Delete(slices.Clone(p.cfg.Mute), p.entry, p.entry+1)
		m.committed(field{key: "mute"})
		p.entry = min(p.entry, max(0, len(p.cfg.Mute)-1))
	case key.Matches(msg, settingsKeys.Save):
		return m.saveSettings()
	case key.Matches(msg, keys.Quit):
		if !p.dirty() {
			return m.quit()
		}
		p.ask("Discard changes and quit? y/n", m.quit)
	}
	return nil
}

// looksLikeKey reports whether s may hold an API key, which the pane
// never shows.
func looksLikeKey(s string) bool {
	return strings.Contains(s, "sk-ant-")
}

// display is a value as the pane shows it: control characters stripped,
// key-shaped values hidden.
func display(s string) string {
	if looksLikeKey(s) {
		return "(hidden: looks like an API key)"
	}
	return brief.StripControl(s)
}

// value is f's value as the list shows it, and whether it is unset.
func (p *settingsPane) value(f field) (string, bool) {
	c := &p.cfg
	switch f.kind {
	case kindEnum:
		v := enumValue(f, c)
		if v == "" {
			return f.opts[0], false
		}
		return display(v), false
	case kindInt:
		n := reflect.ValueOf(f.ptr(c)).Elem().Int()
		switch {
		case n == 0 && f.key == "token_budget" && p.assess != nil:
			return "auto → " + brief.HumanTokens(p.assess.Baseline), false
		case n == 0 && f.key == "token_budget", n == 0 && f.key == "max_searches_per_topic":
			return "auto", false
		case n == 0 && f.key == "history_days":
			return "0 (off)", false
		}
		return strconv.FormatInt(n, 10), false
	case kindFloat:
		return strconv.FormatFloat(*f.ptr(c).(*float64), 'f', -1, 64), false
	case kindProfile:
		if c.Profile == "" {
			return "not set", true
		}
		return fmt.Sprintf("%d/%d %s", utf8.RuneCountInString(c.Profile), config.MaxProfileChars, display(c.Profile)), false
	case kindMute:
		if len(c.Mute) == 0 {
			return fmt.Sprintf("0/%d", config.MaxMuteEntries), true
		}
		return fmt.Sprintf("%d/%d %s", len(c.Mute), config.MaxMuteEntries, display(strings.Join(c.Mute, ", "))), false
	case kindTopics:
		var names []string
		for _, t := range c.Topics {
			names = append(names, t.Name)
		}
		return display(strings.Join(names, ", ")), false
	}
	if v := *f.ptr(c).(*string); v != "" {
		return display(v), false
	}
	return "not set", true
}

// hint is f's one-line description, with whether ANTHROPIC_API_KEY is set
// for auth env (never its value).
func (p *settingsPane) hint(f field) string {
	h := config.Hints[f.key]
	if p.mode == setEdit && p.hidden {
		h = "value hidden; type a new one, or leave it empty to keep it · " + h
	}
	if p.mode == setEdit && f.rng != "" {
		// The range first, where a narrow pane still shows it.
		h = f.rng + " · " + h
	}
	switch {
	case f.key == "provider.auth" && p.cfg.Provider.Auth == "env":
		state := "is not set"
		if os.Getenv("ANTHROPIC_API_KEY") != "" {
			state = "is set"
		}
		h += " · ANTHROPIC_API_KEY " + state + " here"
	case f.key == "topics":
		h = "press e to edit topics in $EDITOR"
	case f.kind == kindEnum:
		h += " (←/→ to change)"
	case f.kind == kindMute:
		h += " (enter to edit the list)"
	}
	return h
}

// settingsView is the pane in w×h cells: the session's overrides, the
// fields scrolled to the selected one, then the budget estimate and any
// error, notice or question.
func (m *Model) settingsView(w, h int) string {
	p := m.set
	var head, body, foot []string
	wrap := func(style lipgloss.Style, indent, text string) []string {
		var out []string
		for _, l := range strings.Split(ansi.Wrap(text, max(1, w-ansi.StringWidth(indent)), ""), "\n") {
			out = append(out, indent+style.Render(l))
		}
		return out
	}
	if len(m.settings.Overrides) > 0 && h >= 6 {
		head = append(head, mutedStyle.Render(truncate("this session overrides: "+brief.StripControl(strings.Join(m.settings.Overrides, ", "))+" (not saved)", w)))
	}

	selStart, selEnd := 0, 0
	if p.broken != nil {
		body = append(body, wrap(warnStyle, "", "Could not read "+m.settings.Path+": "+p.broken.Error())...)
		body = append(body, wrap(mutedStyle, "", "Press e to fix it in $EDITOR, R to read it again, esc to close.")...)
	} else {
		for i, f := range p.shown() {
			if i == p.sel {
				selStart = len(body)
			}
			body = append(body, m.fieldLines(f, i == p.sel, w)...)
			if i == p.sel {
				body = append(body, wrap(mutedStyle, "    ", p.hint(f))...)
				selEnd = len(body)
			}
		}
	}

	switch {
	case p.mode == setConfirm:
		foot = append(foot, wrap(selStyle, "", p.question)...)
	case p.inputErr != "":
		foot = append(foot, wrap(warnStyle, "", p.inputErr)...)
	case p.err != nil:
		foot = append(foot, wrap(warnStyle, "", "✗ "+strings.TrimPrefix(p.err.Error(), "config: ")+" (not saved until fixed)")...)
	}
	if p.notice != "" {
		foot = append(foot, wrap(warnStyle, "", p.notice)...)
	}
	if a := p.assess; a != nil && p.broken == nil {
		panel := m.budgetPanel(*a, w, wrap)
		// The estimate gives way to the fields on a short pane.
		if room := h - len(head) - len(foot) - 3; room > 0 {
			foot = append(panel[:min(len(panel), room)], foot...)
		}
	}
	foot = foot[max(0, len(foot)-max(0, h-len(head)-1)):]
	if len(head)+len(foot)+1 > h {
		head = nil
	}
	room := max(1, h-len(head)-len(foot))
	// Scroll so the selected field and its hint show.
	switch {
	case selStart < p.top:
		p.top = selStart
	case selEnd > p.top+room:
		p.top = min(selStart, selEnd-room)
	}
	p.top = min(max(0, p.top), max(0, len(body)-room))
	body = body[p.top:min(len(body), p.top+room)]
	for len(body) < room {
		body = append(body, "")
	}
	lines := append(append(head, body...), foot...)
	for i, l := range lines {
		lines[i] = truncate(l, w)
	}
	return termSafe(strings.Join(lines[:min(len(lines), h)], "\n"))
}

// budgetPanel estimates the edited config's run: its budget, what it buys
// and the warnings Assess gives, styled as the Overview's.
func (m *Model) budgetPanel(a brief.Assessment, w int, wrap func(lipgloss.Style, string, string) []string) []string {
	budget := brief.HumanTokens(a.Budget)
	if m.set.cfg.TokenBudget == 0 {
		budget = "auto → " + budget
	}
	cost := fmt.Sprintf("~$%.2f/run", a.CostUSD)
	if a.Subscription {
		cost = "subscription quota"
	}
	line := fmt.Sprintf("Budget %s · ~%d searches · up to %d items, ~%.0f min · %s", budget, a.RunSearches, a.Items, a.ReadMinutes, cost)
	out := []string{""}
	out = append(out, wrap(mutedStyle, "", line)...)
	switch a.Level {
	case "", brief.LevelOK:
	case brief.LevelCostly:
		out = append(out, wrap(warnStyle, "", "⚠ cost")...)
	default:
		out = append(out, wrap(warnStyle, "", "⚠ budget "+string(a.Level))...)
	}
	for _, msg := range a.Messages {
		out = append(out, wrap(warnStyle, "│ ", "Warning: "+msg)...)
	}
	return out
}

// fieldLines is a field's row, with its input while it is edited and the
// mute list's entries while they are.
func (m *Model) fieldLines(f field, sel bool, w int) []string {
	p := m.set
	marker, label := "  ", mutedStyle
	if sel {
		marker, label = selStyle.Render("▸ "), selStyle
	}
	// The label column, or the label on a line of its own if that leaves
	// the value too little room.
	var first, indent string
	col := labelWidth + 1
	if w-2-col >= 20 {
		first = marker + label.Render(f.key) + strings.Repeat(" ", col-len(f.key))
		indent = strings.Repeat(" ", 2+col)
	} else {
		first = marker + label.Render(f.key)
		indent = "    "
	}
	vw := max(1, w-ansi.StringWidth(indent))
	stacked := indent != strings.Repeat(" ", 2+col)
	row := func(v string) []string {
		if stacked {
			return []string{first, indent + v}
		}
		return []string{first + v}
	}
	editing := sel && p.mode == setEdit
	switch {
	case editing && f.kind == kindProfile:
		p.area.SetWidth(max(1, w-4))
		p.area.SetHeight(3)
		n := utf8.RuneCountInString(p.area.Value())
		style := mutedStyle
		if n > config.MaxProfileChars {
			style = warnStyle
		}
		lines := []string{first + style.Render(fmt.Sprintf("%d/%d", n, config.MaxProfileChars))}
		for _, l := range strings.Split(p.area.View(), "\n") {
			lines = append(lines, "    "+l)
		}
		return lines
	case editing:
		p.input.Width = max(1, vw-1)
		return row(p.input.View())
	}
	v, unset := p.value(f)
	style := lipgloss.NewStyle()
	if unset {
		style = mutedStyle
	}
	lines := row(style.Render(truncate(v, vw)))
	if f.kind == kindMute && sel && (p.mode == setMute || p.mode == setMuteEdit || p.mode == setConfirm && p.back == setMute) {
		lines = append(lines, m.muteLines(w)...)
	}
	return lines
}

// muteLines are the mute list's entries, the selected one marked or
// replaced by its input.
func (m *Model) muteLines(w int) []string {
	p := m.set
	var out []string
	entries := p.cfg.Mute
	if p.mode == setMuteEdit && p.adding {
		entries = append(slices.Clone(entries), "")
	}
	if len(entries) == 0 {
		return []string{mutedStyle.Render("    no entries: a adds one")}
	}
	for i, e := range entries {
		cur := i == p.entry && !p.adding || p.adding && i == len(entries)-1
		marker := "    "
		if cur {
			marker = "  " + selStyle.Render("▸ ")
		}
		if cur && p.mode == setMuteEdit {
			n := utf8.RuneCountInString(p.input.Value())
			style := mutedStyle
			if n > config.MaxMuteChars {
				style = warnStyle
			}
			count := fmt.Sprintf(" %d/%d", n, config.MaxMuteChars)
			p.input.Width = max(1, w-4-len(count)-1)
			out = append(out, marker+p.input.View()+style.Render(count))
			continue
		}
		style := lipgloss.NewStyle()
		if utf8.RuneCountInString(e) > config.MaxMuteChars {
			style = warnStyle
		}
		out = append(out, marker+style.Render(truncate(display(e), max(1, w-4))))
	}
	return out
}

// settingsHelpLine is the help line while the pane is open.
func (m *Model) settingsHelpLine(width int) string {
	p := m.set
	var shown []string
	keep := "esc close"
	switch p.mode {
	case setConfirm:
		shown, keep = []string{"y yes"}, "n no"
	case setEdit, setMuteEdit:
		shown, keep = []string{"enter done", "ctrl+s save"}, "esc cancel"
	case setMute:
		shown, keep = []string{"↑/↓ entry", "a add", "x delete", "enter edit", "ctrl+s save"}, "esc back"
	default:
		shown = []string{"↑/↓ select", "enter edit", "←/→ change", "ctrl+s save", "e editor"}
		if strings.Contains(p.notice, "R to reload") {
			shown = append(shown, "R reload")
		}
	}
	return fitHelp(append(shown, keep), shown, keep, width)
}
