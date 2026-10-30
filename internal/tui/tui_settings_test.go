package tui

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/rafaelpereira/daily-brief/internal/brief"
	"github.com/rafaelpereira/daily-brief/internal/config"
)

// settingsConfig is a config file as a user keeps it: comments, a key
// daily-brief doesn't know, a mute list.
const settingsConfig = `# my config
provider: claude-code   # subscription
theme: light
token_budget: 0
history_days: 3
my_note: keep me
mute: [crypto]
topics:
  - name: A
    focus: a things   # first
  - name: B
`

// factory is a fake Rebuild: it records each config and builds a
// generator on doneBackend, or fails with err.
type factory struct {
	mu   sync.Mutex
	cfgs []config.Config
	gens []*brief.Generator
	err  error
}

func (f *factory) build(cfg config.Config) (*brief.Generator, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cfgs = append(f.cfgs, cfg)
	if f.err != nil {
		return nil, f.err
	}
	g := brief.NewGenerator(cfg, doneBackend{})
	f.gens = append(f.gens, g)
	return g, nil
}

func (f *factory) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.cfgs)
}

// settingsModel shows a saved journal at w×h with settings over a
// temporary config holding body.
func settingsModel(t *testing.T, body string, w, h int) (*Model, string, *factory) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &factory{}
	m := New(nil, nil, journals()["long"], "light").WithSettings(Settings{
		Path:    path,
		Rebuild: f.build,
		Theme: func(name string) string {
			if name != "dark" {
				return "light"
			}
			return name
		},
	})
	m.editor = func(string, func(error) tea.Msg) tea.Cmd {
		t.Error("the editor ran")
		return nil
	}
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m, path, f
}

func keyOf(k tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: k} }

// press sends keys and runs what they return.
func press(t *testing.T, m *Model, ks ...tea.KeyMsg) {
	t.Helper()
	for _, k := range ks {
		_, cmd := m.Update(k)
		run(t, m, cmd)
	}
}

// run runs cmd and feeds its messages back, but for a quit (an error) and
// the flash's timer, which is left to expire.
func run(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msgs := make(chan tea.Msg, 1)
	go func() { msgs <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-msgs:
	case <-time.After(500 * time.Millisecond):
		return // a timer
	}
	switch msg := msg.(type) {
	case tea.QuitMsg:
		t.Fatal("quit")
	case tea.BatchMsg:
		for _, c := range msg {
			run(t, m, c)
		}
	case rebuiltMsg, editorDoneMsg:
		_, next := m.Update(msg)
		run(t, m, next)
	}
}

// goTo selects the field named name.
func goTo(t *testing.T, m *Model, name string) {
	t.Helper()
	for range len(fields) {
		press(t, m, keyOf(tea.KeyUp))
	}
	for range len(fields) {
		if m.set.field().key == name {
			return
		}
		press(t, m, keyOf(tea.KeyDown))
	}
	t.Fatalf("no field %s among %v", name, m.set.shown())
}

// setNumber types v into the selected number field.
func setNumber(t *testing.T, m *Model, name, v string) {
	t.Helper()
	goTo(t, m, name)
	press(t, m, keyOf(tea.KeyEnter), keyOf(tea.KeyCtrlU), runes(v), keyOf(tea.KeyEnter))
	if m.set.mode != setList {
		t.Fatalf("%s=%s: still editing", name, v)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// s and , open the pane in place of the summary, keeping Topics; esc
// closes it. Neither quits, nor does esc at any depth.
func TestSettingsOpenAndClose(t *testing.T) {
	for _, opener := range []tea.KeyMsg{runes("s"), runes(",")} {
		m, _, _ := settingsModel(t, settingsConfig, 120, 30)
		normal := m.View()
		_, cmd := m.Update(opener)
		if cmd != nil || m.set == nil {
			t.Fatalf("%q did not just open settings", opener)
		}
		v := m.View()
		if !strings.Contains(v, "Settings") || !strings.Contains(v, "provider.type") || !strings.Contains(v, "Topics") ||
			strings.Contains(v, "Overview item 5") {
			t.Errorf("settings view:\n%s", v)
		}
		// esc out of an edit, of the mute list, then of the pane.
		goTo(t, m, "history_days")
		press(t, m, keyOf(tea.KeyEnter), runes("q"))
		if m.set.mode != setEdit {
			t.Fatal("q quit an edit")
		}
		if _, cmd := m.Update(keyOf(tea.KeyEsc)); cmd != nil || m.set == nil || m.set.mode != setList || m.set.cfg.HistoryDays != 3 {
			t.Fatal("esc did not cancel the edit")
		}
		goTo(t, m, "mute")
		press(t, m, keyOf(tea.KeyEnter))
		if _, cmd := m.Update(keyOf(tea.KeyEsc)); cmd != nil || m.set == nil || m.set.mode != setList {
			t.Fatal("esc did not leave the mute list")
		}
		if _, cmd := m.Update(keyOf(tea.KeyEsc)); cmd != nil || m.set != nil {
			t.Fatal("esc did not close settings")
		}
		if v := m.View(); v != normal {
			t.Errorf("closing did not restore the view:\n%s", v)
		}
		if _, cmd := m.Update(keyOf(tea.KeyEsc)); cmd != nil || m.focus != focusTopics {
			t.Error("esc after settings did not focus Topics")
		}
	}
}

// Unsaved changes make esc ask first: n keeps them, y drops them and
// leaves the file alone.
func TestSettingsDiscardPrompt(t *testing.T) {
	m, path, _ := settingsModel(t, settingsConfig, 120, 30)
	press(t, m, runes("s"))
	setNumber(t, m, "history_days", "5")
	press(t, m, keyOf(tea.KeyEsc))
	if m.set == nil || m.set.mode != setConfirm || !strings.Contains(m.View(), "Discard changes? y/n") {
		t.Fatalf("esc with changes did not ask:\n%s", m.View())
	}
	press(t, m, runes("n"))
	if m.set == nil || m.set.mode != setList || m.set.cfg.HistoryDays != 5 {
		t.Fatal("n did not keep the changes")
	}
	press(t, m, keyOf(tea.KeyEsc), runes("y"))
	if m.set != nil {
		t.Fatal("y did not close settings")
	}
	if readFile(t, path) != settingsConfig {
		t.Error("discarding wrote the file")
	}
	// q with changes asks too; y quits.
	press(t, m, runes("s"))
	setNumber(t, m, "history_days", "5")
	if _, cmd := m.Update(runes("q")); cmd != nil || m.set.mode != setConfirm {
		t.Fatal("q with changes did not ask")
	}
	if _, cmd := m.Update(runes("y")); cmd == nil {
		t.Error("y did not quit")
	} else if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("y did not quit")
	}
}

// Invalid values show their error and block saving; nothing is written.
func TestSettingsInvalidValuesBlockSaving(t *testing.T) {
	mute30 := "mute:\n"
	for i := range config.MaxMuteEntries {
		mute30 += "  - m" + string(rune('A'+i)) + "\n"
	}
	body30 := strings.Replace(settingsConfig, "mute: [crypto]\n", mute30, 1)
	for _, c := range []struct {
		name, body, want string
		edit             func(t *testing.T, m *Model)
	}{
		{"history_days=15", settingsConfig, "history_days must be between 0", func(t *testing.T, m *Model) { setNumber(t, m, "history_days", "15") }},
		{"budget 5000", settingsConfig, "token_budget must be 0", func(t *testing.T, m *Model) { setNumber(t, m, "token_budget", "5000") }},
		{"profile 501", settingsConfig, "profile is 501 characters", func(t *testing.T, m *Model) {
			goTo(t, m, "profile")
			press(t, m, keyOf(tea.KeyEnter), runes(strings.Repeat("x", 501)))
			if v := ansi.Strip(m.View()); !strings.Contains(v, "501/500") {
				t.Errorf("no live count:\n%s", v)
			}
			press(t, m, keyOf(tea.KeyEnter))
		}},
		{"31st mute entry", body30, "mute has 31 entries", func(t *testing.T, m *Model) {
			goTo(t, m, "mute")
			press(t, m, keyOf(tea.KeyEnter), runes("a"), runes("one more"), keyOf(tea.KeyEnter))
			if len(m.set.cfg.Mute) != 31 {
				t.Fatalf("mute has %d entries", len(m.set.cfg.Mute))
			}
		}},
	} {
		m, path, f := settingsModel(t, c.body, 120, 40)
		press(t, m, runes("s"))
		c.edit(t, m)
		v := ansi.Strip(m.View())
		if !strings.Contains(v, c.want) || !strings.Contains(v, "not saved until fixed") {
			t.Errorf("%s: no error shown:\n%s", c.name, v)
		}
		press(t, m, keyOf(tea.KeyCtrlS))
		if got := readFile(t, path); got != c.body {
			t.Errorf("%s: the file was written:\n%s", c.name, got)
		}
		if f.calls() != 0 || m.flash != "Fix the error before saving" {
			t.Errorf("%s: rebuilt %d times, flash %q", c.name, f.calls(), m.flash)
		}
	}
}

// Number inputs take digits only.
func TestSettingsNumberInputTakesDigits(t *testing.T) {
	m, _, _ := settingsModel(t, settingsConfig, 120, 30)
	press(t, m, runes("s"))
	goTo(t, m, "history_days")
	press(t, m, keyOf(tea.KeyEnter), keyOf(tea.KeyCtrlU), runes("1a-"), keyOf(tea.KeySpace), runes("2"), keyOf(tea.KeyEnter))
	if m.set.cfg.HistoryDays != 12 {
		t.Errorf("history_days %d", m.set.cfg.HistoryDays)
	}
}

// A theme change shows at once, and goes back if the change is dropped.
func TestSettingsThemeAppliesAtOnce(t *testing.T) {
	m, _, _ := settingsModel(t, settingsConfig, 120, 30)
	press(t, m, runes("s"))
	goTo(t, m, "theme")
	before := m.content
	press(t, m, keyOf(tea.KeyLeft))
	if m.set.cfg.Theme != "dark" || m.theme != "dark" {
		t.Fatalf("theme %q, shown %q", m.set.cfg.Theme, m.theme)
	}
	if m.content == before {
		t.Error("the journal was not rendered again")
	}
	press(t, m, keyOf(tea.KeyEsc), runes("y"))
	if m.theme != "light" || m.content != before {
		t.Errorf("dropped theme still shown: %q", m.theme)
	}
}

// The budget panel follows budget edits: auto shows the baseline, and a
// budget below it warns as the Overview does.
func TestSettingsBudgetPanelFollowsEdits(t *testing.T) {
	m, _, _ := settingsModel(t, settingsConfig, 120, 40)
	press(t, m, runes("s"))
	a := m.set.assess
	if a == nil || a.Budget != a.Baseline {
		t.Fatalf("auto budget assessed as %+v", a)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "auto → "+brief.HumanTokens(a.Baseline)) {
		t.Errorf("auto not shown with its baseline:\n%s", v)
	}
	setNumber(t, m, "token_budget", "20000")
	b := m.set.assess
	if b == nil || b.Budget != 20_000 || b.Level != brief.LevelLow {
		t.Fatalf("20000 assessed as %+v", b)
	}
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "⚠ budget low") || !strings.Contains(v, "Warning: ") || !strings.Contains(v, "Budget 20.0k") {
		t.Errorf("low budget not warned:\n%s", v)
	}
	setNumber(t, m, "token_budget", "0")
	if m.set.assess.Budget != a.Baseline || m.set.cfg.TokenBudget != 0 {
		t.Error("auto not restored")
	}
}

// Saving writes only the edits: comments and unknown keys stay, the file
// loads as the edited config, and this session's overrides are named but
// never saved.
func TestSettingsSaveRoundTrip(t *testing.T) {
	m, path, f := settingsModel(t, settingsConfig, 120, 40)
	m.settings.Overrides = []string{"provider=aws", "budget=150000"}
	press(t, m, runes("s"))
	if v := ansi.Strip(m.View()); !strings.Contains(v, "this session overrides: provider=aws, budget=150000") {
		t.Errorf("overrides not named:\n%s", v)
	}
	if m.set.cfg.Provider.Type != "claude-code" || m.set.cfg.TokenBudget != 0 {
		t.Fatalf("the pane edits the session's config: %+v", m.set.cfg.Provider)
	}
	setNumber(t, m, "history_days", "7")
	goTo(t, m, "mute")
	press(t, m, keyOf(tea.KeyEnter), runes("a"), runes("sports"), keyOf(tea.KeyEnter), keyOf(tea.KeyEsc))
	goTo(t, m, "effort")
	press(t, m, keyOf(tea.KeyLeft))
	press(t, m, keyOf(tea.KeyCtrlS))
	if m.flash != "saved · press r to regenerate" || m.set.dirty() {
		t.Errorf("flash %q, dirty %v", m.flash, m.set.dirty())
	}
	got, _, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, m.set.cfg) || got.HistoryDays != 7 || got.Effort != "low" || strings.Join(got.Mute, ",") != "crypto,sports" {
		t.Errorf("saved %+v, edited %+v", got, m.set.cfg)
	}
	file := readFile(t, path)
	for _, want := range []string{"# my config", "# subscription", "my_note: keep me", "# first", "provider: claude-code", "token_budget: 0"} {
		if !strings.Contains(file, want) {
			t.Errorf("saved file lacks %q:\n%s", want, file)
		}
	}
	if f.calls() != 1 || !reflect.DeepEqual(f.cfgs[0], got) {
		t.Errorf("rebuilt %d times", f.calls())
	}
	if m.gen != f.gens[0] {
		t.Error("the rebuilt generator was not taken")
	}
	if m.generating {
		t.Error("saving started a run")
	}
}

// A file changed on disk since the pane opened is not overwritten; R
// reloads it.
func TestSettingsRefusesExternalChange(t *testing.T) {
	m, path, f := settingsModel(t, settingsConfig, 120, 40)
	press(t, m, runes("s"))
	setNumber(t, m, "history_days", "7")
	external := strings.Replace(settingsConfig, "history_days: 3", "history_days: 9", 1)
	if err := os.WriteFile(path, []byte(external), 0o600); err != nil {
		t.Fatal(err)
	}
	press(t, m, keyOf(tea.KeyCtrlS))
	if got := readFile(t, path); got != external {
		t.Fatalf("the changed file was overwritten:\n%s", got)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "changed on disk") || !strings.Contains(v, "R reload") {
		t.Errorf("refusal not shown:\n%s", v)
	}
	press(t, m, runes("R"))
	if m.set.mode != setConfirm {
		t.Fatal("R with changes did not ask")
	}
	press(t, m, runes("y"))
	if m.set.cfg.HistoryDays != 9 || m.set.dirty() {
		t.Errorf("reloaded history_days %d", m.set.cfg.HistoryDays)
	}
	if f.calls() != 1 || f.cfgs[0].HistoryDays != 9 {
		t.Errorf("the reloaded file was not applied: %d rebuilds", f.calls())
	}
}

// e opens the file in the editor, then reloads and applies it.
func TestSettingsEditorReloads(t *testing.T) {
	m, path, f := settingsModel(t, settingsConfig, 120, 40)
	edited := strings.Replace(settingsConfig, "history_days: 3", "history_days: 11", 1)
	var opened string
	m.editor = func(p string, done func(error) tea.Msg) tea.Cmd {
		opened = p
		os.WriteFile(p, []byte(edited), 0o600)
		return func() tea.Msg { return done(nil) }
	}
	press(t, m, runes("s"))
	goTo(t, m, "topics")
	press(t, m, keyOf(tea.KeyEnter))
	if !strings.Contains(m.flash, "Press e to edit topics") {
		t.Errorf("topics flash %q", m.flash)
	}
	press(t, m, runes("e"))
	if opened != path || m.set.cfg.HistoryDays != 11 || f.calls() != 1 {
		t.Errorf("editor: opened %q, history_days %d, %d rebuilds", opened, m.set.cfg.HistoryDays, f.calls())
	}
	// An invalid edit shows its error and is not applied.
	edited = strings.Replace(settingsConfig, "history_days: 3", "history_days: 99", 1)
	press(t, m, runes("e"))
	if v := ansi.Strip(m.View()); !strings.Contains(v, "history_days must be") || f.calls() != 1 {
		t.Errorf("invalid edit: %d rebuilds:\n%s", f.calls(), v)
	}
}

// Saving during a run keeps the run's generator until it ends; the next
// r runs with the saved config. A failed rebuild keeps the generator, and
// the file is still saved.
func TestSettingsSaveWhileGenerating(t *testing.T) {
	m, path, f := settingsModel(t, settingsConfig, 120, 40)
	cfg := config.Default()
	cfg.Provider.Type = "anthropic"
	cfg.TokenBudget = 400_000
	b := &gatedBackend{started: make(chan struct{}, 1)}
	old := brief.NewGenerator(cfg, b)
	m.gen = old
	wait := m.start()
	<-b.started

	press(t, m, runes("s"))
	setNumber(t, m, "history_days", "7")
	press(t, m, keyOf(tea.KeyCtrlS))
	if f.calls() != 1 || m.gen != old || m.pending != f.gens[0] {
		t.Fatalf("during a run: gen swapped %v, pending %v", m.gen != old, m.pending != nil)
	}
	press(t, m, keyOf(tea.KeyEsc))
	m.cancel()
	drain(t, m, wait)
	if m.gen != f.gens[0] || m.pending != nil {
		t.Fatal("the rebuilt generator was not taken when the run ended")
	}
	_, cmd := m.Update(runes("r"))
	if cmd == nil || !m.generating {
		t.Fatal("r did not start a run")
	}
	drain(t, m, m.wait())
	if f.cfgs[0].HistoryDays != 7 || m.journal.Provider != "done" {
		t.Errorf("the next run used history_days %d, provider %q", f.cfgs[0].HistoryDays, m.journal.Provider)
	}

	f.err = errors.New("provider anthropic: no API key in the Keychain")
	press(t, m, runes("s"))
	setNumber(t, m, "history_days", "2")
	press(t, m, keyOf(tea.KeyCtrlS))
	if m.gen != f.gens[0] || !strings.Contains(m.set.notice, "no API key") || !strings.Contains(ansi.Strip(m.View()), "previous connection") {
		t.Errorf("failed rebuild: notice %q", m.set.notice)
	}
	if got, _, _ := config.Load(path); got.HistoryDays != 2 {
		t.Error("the file was not saved")
	}
}

// The pane never shows an API key: not the environment's, not one pasted
// into a field. It says only whether ANTHROPIC_API_KEY is set.
func TestSettingsNeverShowsKeys(t *testing.T) {
	const secret = "sk-ant-api03-SECRETSECRETSECRET"
	t.Setenv("ANTHROPIC_API_KEY", secret)
	body := strings.Replace(settingsConfig, "provider: claude-code   # subscription", "provider:\n  type: anthropic\n  auth: env", 1) +
		"model: " + secret + "\n"
	m, _, _ := settingsModel(t, body, 120, 60)
	press(t, m, runes("s"))
	seen := ""
	for range len(fields) {
		seen += m.View()
		press(t, m, keyOf(tea.KeyDown))
	}
	if strings.Contains(seen, "sk-ant") || strings.Contains(seen, "SECRET") {
		t.Errorf("a key is shown:\n%s", seen)
	}
	if !strings.Contains(ansi.Strip(seen), "ANTHROPIC_API_KEY is set") {
		t.Errorf("key status not shown:\n%s", seen)
	}
}

// The pane fits every size, full and compact, in every mode.
func TestSettingsFitsNarrowWidths(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {60, 15}, {50, 10}, {49, 20}, {30, 8}, {20, 4}} {
		m, _, _ := settingsModel(t, settingsConfig, size[0], size[1])
		m.settings.Overrides = []string{"provider=aws", "topics=" + strings.Repeat("Topic,", 20)}
		press(t, m, runes("s"))
		if !strings.Contains(m.View(), "Settin") {
			t.Errorf("%v: no Settings title:\n%s", size, m.View())
		}
		checkFits(t, "settings", m)
		for range len(fields) {
			press(t, m, keyOf(tea.KeyDown))
			checkFits(t, "settings", m)
		}
		goTo(t, m, "profile")
		press(t, m, keyOf(tea.KeyEnter), runes(strings.Repeat("long words ", 60)))
		checkFits(t, "profile edit", m)
		press(t, m, keyOf(tea.KeyEnter))
		setNumber(t, m, "token_budget", "20000")
		checkFits(t, "low budget", m)
		goTo(t, m, "mute")
		press(t, m, keyOf(tea.KeyEnter), runes("a"), runes(strings.Repeat("y", 100)))
		checkFits(t, "mute edit", m)
		press(t, m, keyOf(tea.KeyEnter))
		checkFits(t, "mute list", m)
		press(t, m, keyOf(tea.KeyEsc), keyOf(tea.KeyEsc))
		checkFits(t, "discard prompt", m)
		if m.set == nil || m.set.mode != setConfirm {
			t.Errorf("%v: no discard prompt", size)
		}
	}
}

// s and , open settings and nothing else; the pane's own keys don't
// clash with each other or with the keys it shares.
func TestSettingsKeysDoNotConflict(t *testing.T) {
	for _, b := range keys.all() {
		if b.Help().Desc == keys.Settings.Help().Desc {
			continue
		}
		for _, k := range b.Keys() {
			if k == "s" || k == "," {
				t.Errorf("%q is bound to %q too", k, b.Help().Desc)
			}
		}
	}
	s := settingsKeys
	list := map[string][]string{"up": keys.Up.Keys(), "down": keys.Down.Keys(), "esc/close": append(keys.Esc.Keys(), keys.Settings.Keys()...),
		"quit": keys.Quit.Keys(), "edit": s.Edit.Keys(), "prev": s.Prev.Keys(), "next": s.Next.Keys(), "save": s.Save.Keys(),
		"editor": s.Editor.Keys(), "reload": s.Reload.Keys()}
	mute := map[string][]string{"up": keys.Up.Keys(), "down": keys.Down.Keys(), "back": append(keys.Esc.Keys(), s.Prev.Keys()...),
		"quit": keys.Quit.Keys(), "add": s.Add.Keys(), "delete": s.Delete.Keys(), "edit": s.Edit.Keys(), "save": s.Save.Keys()}
	for name, table := range map[string]map[string][]string{"list": list, "mute": mute} {
		seen := map[string]string{}
		for action, ks := range table {
			for _, k := range ks {
				if other, ok := seen[k]; ok && other != action {
					t.Errorf("%s: %q is both %s and %s", name, k, other, action)
				}
				seen[k] = action
			}
		}
	}
	if slicesOverlap(s.Yes.Keys(), s.No.Keys()) {
		t.Error("y/n overlap")
	}
}

func slicesOverlap(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

// Without settings from main, s says so and changes nothing.
func TestSettingsUnavailable(t *testing.T) {
	m := longModel(t, 100)
	if _, cmd := m.Update(runes("s")); cmd == nil || m.set != nil || m.flash != "Settings are not available" {
		t.Errorf("s without settings: flash %q", m.flash)
	}
}

// A theme being tried goes back to the file's when edits are dropped by a
// reload or the editor, even with the file unchanged, and when a pane
// over an unreadable file closes.
func TestSettingsThemeRevertsOnReloadAndClose(t *testing.T) {
	m, path, _ := settingsModel(t, settingsConfig, 120, 30)
	m.editor = func(_ string, done func(error) tea.Msg) tea.Cmd { return func() tea.Msg { return done(nil) } }
	for _, discard := range []tea.KeyMsg{runes("R"), runes("e")} {
		press(t, m, runes("s"))
		goTo(t, m, "theme")
		press(t, m, keyOf(tea.KeyLeft))
		if m.theme != "dark" {
			t.Fatalf("theme %q", m.theme)
		}
		press(t, m, discard, runes("y"))
		if m.theme != "light" || m.set.cfg.Theme != "light" {
			t.Errorf("%q: theme %q after dropping edits", discard, m.theme)
		}
		press(t, m, keyOf(tea.KeyEsc))
	}
	press(t, m, runes("s"))
	goTo(t, m, "theme")
	press(t, m, keyOf(tea.KeyLeft))
	os.WriteFile(path, []byte("topics: [\n"), 0o600)
	press(t, m, runes("R"), runes("y"))
	if m.set.broken == nil || m.theme != "light" {
		t.Fatalf("broken reload: theme %q, err %v", m.theme, m.set.broken)
	}
	press(t, m, keyOf(tea.KeyEsc))
	if m.set != nil || m.theme != "light" {
		t.Errorf("closing over a broken file: theme %q", m.theme)
	}
}

// A field holding a key-shaped value opens empty, never showing it; an
// empty input keeps the value, typing replaces it.
func TestSettingsEditingHiddenValue(t *testing.T) {
	const secret = "sk-ant-api03-SECRETSECRET"
	m, _, _ := settingsModel(t, strings.Replace(settingsConfig, "mute: [crypto]", "mute: [\""+secret+"\"]", 1)+"model: "+secret+"\n", 120, 40)
	m.settings.Overrides = nil
	press(t, m, runes("s"))
	goTo(t, m, "model")
	press(t, m, keyOf(tea.KeyEnter))
	if v := m.View(); m.set.input.Value() != "" || strings.Contains(v, "SECRET") || !strings.Contains(ansi.Strip(v), "value hidden") {
		t.Errorf("hidden value shown or no hint:\n%s", v)
	}
	press(t, m, keyOf(tea.KeyEnter))
	if m.set.cfg.Model != secret {
		t.Errorf("empty input changed the value to %q", m.set.cfg.Model)
	}
	press(t, m, keyOf(tea.KeyEnter), runes("claude-x"), keyOf(tea.KeyEnter))
	if m.set.cfg.Model != "claude-x" {
		t.Errorf("model %q", m.set.cfg.Model)
	}
	// A mute entry too.
	goTo(t, m, "mute")
	press(t, m, keyOf(tea.KeyEnter), keyOf(tea.KeyEnter))
	if v := m.View(); m.set.input.Value() != "" || strings.Contains(v, "SECRET") {
		t.Errorf("hidden mute entry shown:\n%s", v)
	}
	press(t, m, keyOf(tea.KeyEnter))
	if len(m.set.cfg.Mute) != 1 || m.set.cfg.Mute[0] != secret {
		t.Errorf("mute %q", m.set.cfg.Mute)
	}
}

// EditorCommand passes $EDITOR's arguments and a path with spaces intact.
func TestEditorCommand(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "a b", "c d")
	t.Setenv("EDITOR", "mkdir -p")
	if out, err := EditorCommand(target).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if fi, err := os.Stat(target); err != nil || !fi.IsDir() {
		t.Errorf("editor did not get the path: %v", err)
	}
}
