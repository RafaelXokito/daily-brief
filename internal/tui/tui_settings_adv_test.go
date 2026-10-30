package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/RafaelXokito/daily-brief/internal/brief"
	"github.com/RafaelXokito/daily-brief/internal/config"
)

// advSettingsConfig shows every text field the tests type into: an aws
// provider (profile and region), a model, a profile and a mute list.
const advSettingsConfig = `# adv
provider:
  type: aws
  region: eu-west-1
  profile: work
model: m0
profile: me
mute: [crypto, sports]
topics:
  - name: A
    focus: a
  - name: B
    focus: b
`

func advDeadline(t *testing.T, d time.Duration) func() {
	t.Helper()
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-time.After(d):
			panic(fmt.Sprintf("%s: exceeded %v", t.Name(), d))
		}
	}()
	return func() { close(done) }
}

// typed are keys that act in the list or elsewhere and must only type
// while a text field is edited.
func typed() []tea.KeyMsg {
	var ks []tea.KeyMsg
	for _, r := range "sqeRy,?rxanN" {
		ks = append(ks, runes(string(r)))
	}
	return append(ks, tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
}

const typedText = "sqeRy,?rxanN "

// pressNoQuit sends keys, failing if any returns a quit.
func pressNoQuit(t *testing.T, m *Model, ks ...tea.KeyMsg) {
	t.Helper()
	for _, k := range ks {
		_, cmd := m.Update(k)
		if cmd != nil {
			if isQuit(cmd) {
				t.Fatalf("%q quit", k.String())
			}
			run(t, m, cmd)
		}
	}
}

// isQuit reports whether cmd (or any command in its batch) quits.
func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		switch msg := msg.(type) {
		case tea.QuitMsg:
			return true
		case tea.BatchMsg:
			for _, c := range msg {
				if isQuit(c) {
					return true
				}
			}
		}
	case <-time.After(200 * time.Millisecond):
	}
	return false
}

// editValue is what the open input holds.
func editValue(m *Model) string {
	if m.set.mode == setEdit && m.set.field().kind == kindProfile {
		return m.set.area.Value()
	}
	return m.set.input.Value()
}

// Keys that act elsewhere only type while a text field or mute entry is
// edited; esc restores the old value; nothing quits or saves.
func TestSettingsAdvKeysTypeInTextFields(t *testing.T) {
	defer advDeadline(t, 30*time.Second)()
	type target struct {
		name  string
		open  func(t *testing.T, m *Model)
		value func(m *Model) string
	}
	textField := func(name string, get func(c *Model) string) target {
		return target{name, func(t *testing.T, m *Model) {
			goTo(t, m, name)
			press(t, m, keyOf(tea.KeyEnter))
		}, get}
	}
	targets := []target{
		textField("provider.profile", func(m *Model) string { return m.set.cfg.Provider.Profile }),
		textField("provider.region", func(m *Model) string { return m.set.cfg.Provider.Region }),
		textField("model", func(m *Model) string { return m.set.cfg.Model }),
		textField("profile", func(m *Model) string { return m.set.cfg.Profile }),
		{"mute entry", func(t *testing.T, m *Model) {
			if m.set.mode == setMute {
				press(t, m, keyOf(tea.KeyEsc))
			}
			goTo(t, m, "mute")
			press(t, m, keyOf(tea.KeyEnter), keyOf(tea.KeyEnter))
		}, func(m *Model) string { return strings.Join(m.set.cfg.Mute, "|") }},
		{"mute add", func(t *testing.T, m *Model) {
			if m.set.mode == setMute {
				press(t, m, keyOf(tea.KeyEsc))
			}
			goTo(t, m, "mute")
			press(t, m, keyOf(tea.KeyEnter), runes("a"))
		}, func(m *Model) string { return strings.Join(m.set.cfg.Mute, "|") }},
	}
	for _, tg := range targets {
		t.Run(tg.name, func(t *testing.T) {
			m, path, f := settingsModel(t, advSettingsConfig, 120, 40)
			press(t, m, runes("s"))
			before := tg.value(m)
			tg.open(t, m)
			mode := m.set.mode
			if mode != setEdit && mode != setMuteEdit {
				t.Fatalf("not editing: mode %v", mode)
			}
			start := editValue(m)
			pressNoQuit(t, m, typed()...)
			if m.set == nil || m.set.mode != mode {
				t.Fatalf("a typed key left the input (pane %v)", m.set != nil)
			}
			if got := editValue(m); got != start+typedText {
				t.Errorf("input holds %q, want %q", got, start+typedText)
			}
			if readFile(t, path) != advSettingsConfig || f.calls() != 0 {
				t.Error("typing saved")
			}
			// esc drops the edit.
			pressNoQuit(t, m, keyOf(tea.KeyEsc))
			if m.set == nil || m.set.mode == mode {
				t.Fatal("esc did not leave the input")
			}
			if got := tg.value(m); got != before {
				t.Errorf("esc kept %q, want %q", got, before)
			}
			if m.set.dirty() {
				t.Error("esc left the pane dirty")
			}
			// enter takes it.
			tg.open(t, m)
			pressNoQuit(t, m, typed()...)
			pressNoQuit(t, m, keyOf(tea.KeyEnter))
			if got := tg.value(m); !strings.Contains(got, strings.TrimSpace(typedText)) {
				t.Errorf("enter took %q", got)
			}
		})
	}
}

// ctrl+c quits from every mode, dirty or not.
func TestSettingsAdvCtrlCQuitsEverywhere(t *testing.T) {
	defer advDeadline(t, 30*time.Second)()
	setups := map[string]func(t *testing.T, m *Model){
		"list":       func(t *testing.T, m *Model) {},
		"dirty list": func(t *testing.T, m *Model) { setNumber(t, m, "history_days", "5") },
		"text edit":  func(t *testing.T, m *Model) { goTo(t, m, "model"); press(t, m, keyOf(tea.KeyEnter)) },
		"num edit":   func(t *testing.T, m *Model) { goTo(t, m, "concurrency"); press(t, m, keyOf(tea.KeyEnter)) },
		"profile":    func(t *testing.T, m *Model) { goTo(t, m, "profile"); press(t, m, keyOf(tea.KeyEnter)) },
		"mute":       func(t *testing.T, m *Model) { goTo(t, m, "mute"); press(t, m, keyOf(tea.KeyEnter)) },
		"mute edit": func(t *testing.T, m *Model) {
			goTo(t, m, "mute")
			press(t, m, keyOf(tea.KeyEnter), runes("a"))
		},
		"confirm": func(t *testing.T, m *Model) {
			setNumber(t, m, "history_days", "5")
			press(t, m, keyOf(tea.KeyEsc))
			if m.set.mode != setConfirm {
				t.Fatal("no confirm")
			}
		},
		"input error": func(t *testing.T, m *Model) {
			goTo(t, m, "price_input_per_mtok")
			press(t, m, keyOf(tea.KeyEnter), keyOf(tea.KeyCtrlU), runes("1.2.3"), keyOf(tea.KeyEnter))
			if m.set.inputErr == "" {
				t.Fatal("no input error")
			}
		},
	}
	for name, setup := range setups {
		t.Run(name, func(t *testing.T) {
			m, _, _ := settingsModel(t, advSettingsConfig, 100, 30)
			press(t, m, runes("s"))
			setup(t, m)
			_, cmd := m.Update(keyOf(tea.KeyCtrlC))
			if !isQuit(cmd) {
				t.Errorf("ctrl+c did not quit in %s", name)
			}
		})
	}
	t.Run("broken file", func(t *testing.T) {
		m, _, _ := settingsModel(t, "topics: [unclosed\n", 100, 30)
		press(t, m, runes("s"))
		if m.set == nil || m.set.broken == nil {
			t.Fatal("not broken")
		}
		_, cmd := m.Update(keyOf(tea.KeyCtrlC))
		if !isQuit(cmd) {
			t.Error("ctrl+c did not quit on a broken file")
		}
	})
}

// payloads are pastes holding terminal controls.
var payloads = map[string]string{
	"esc-csi":   "a\x1b[31mred\x1b[0m\x1b[2J\x1b[H",
	"osc8":      "\x1b]8;;https://evil.example\x1b\\click\x1b]8;;\x1b\\",
	"osc8-bel":  "\x1b]8;;https://evil.example\aclick\x1b]8;;\a",
	"osc0-bel":  "\x1b]0;pwned\a title",
	"osc52":     "\x1b]52;c;cHduZWQ=\a",
	"bel":       "ding\a\a",
	"c1":        "c1 \u009b31m \u009d0;x\u009c \u0085 nel",
	"bidi":      "‮moc.live‬ ⁦x⁩ ​‎‏؜⁠",
	"bs-cr-del": "abc\b\b\rX\x7f",
	"dcs":       "\x1bP+q544e\x1b\\",
	"nul-tab":   "a\x00b\tc\x0bd\x0ce",
	"lone-esc":  "\x1b",
	"apc":       "\x1b_Gf=24;AAAA\x1b\\",
}

// Pasting controls into a text field never puts a raw escape (other than
// SGR and our own OSC 8) or a control on screen: not while editing, not
// once taken, not in the mute list.
func TestSettingsAdvPastedControls(t *testing.T) {
	for name, p := range payloads {
		for _, field := range []string{"model", "provider.region", "profile", "mute"} {
			t.Run(name+"/"+field, func(t *testing.T) {
				defer advDeadline(t, 30*time.Second)()
				m, _, _ := settingsModel(t, advSettingsConfig, 100, 40)
				press(t, m, runes("s"))
				goTo(t, m, field)
				press(t, m, keyOf(tea.KeyEnter))
				if field == "mute" {
					press(t, m, runes("a"))
				}
				paste := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(p), Paste: true}
				pressNoQuit(t, m, paste)
				checkStrict(t, "editing", m.View())
				pressNoQuit(t, m, keyOf(tea.KeyEnter))
				checkStrict(t, "taken", m.View())
				for _, l := range strings.Split(m.View(), "\n") {
					if strings.Contains(l, "evil.example") && strings.Contains(l, "\x1b]8;;https://evil") {
						t.Errorf("pasted link is live: %q", l)
					}
				}
				if field == "mute" {
					pressNoQuit(t, m, keyOf(tea.KeyEsc))
				}
				checkStrict(t, "list", m.View())
				// Help line and any flash too.
				checkStrict(t, "help", m.settingsHelpLine(100))
			})
		}
	}
}

// Overrides and a broken file's error, which can come from outside the
// pane, are shown safely too.
func TestSettingsAdvControlsFromOutside(t *testing.T) {
	defer advDeadline(t, 30*time.Second)()
	m, _, _ := settingsModel(t, "my: \"\x1b]0;x\a\"\ntopics: [\x1b[31m\n", 100, 30)
	m.settings.Overrides = []string{"provider=\x1b[2Jaws", "topics=\x1b]8;;http://x\x1b\\y"}
	press(t, m, runes("s"))
	checkStrict(t, "broken", m.View())
}

// collect runs cmd, returning the rebuilt messages it yields; the flash's
// timer and others are dropped.
func collect(t *testing.T, cmd tea.Cmd) []rebuiltMsg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	var out []rebuiltMsg
	select {
	case msg := <-ch:
		switch msg := msg.(type) {
		case rebuiltMsg:
			out = append(out, msg)
		case tea.BatchMsg:
			for _, c := range msg {
				out = append(out, collect(t, c)...)
			}
		case tea.QuitMsg:
			t.Fatal("quit")
		}
	case <-time.After(300 * time.Millisecond):
	}
	return out
}

// saveHistory edits history_days to v and saves without delivering the
// rebuild, which it returns.
func saveHistory(t *testing.T, m *Model, v int) rebuiltMsg {
	t.Helper()
	goTo(t, m, "history_days")
	for _, k := range []tea.KeyMsg{keyOf(tea.KeyEnter), keyOf(tea.KeyCtrlU), runes(fmt.Sprint(v)), keyOf(tea.KeyEnter)} {
		m.Update(k)
	}
	_, cmd := m.Update(keyOf(tea.KeyCtrlS))
	rs := collect(t, cmd)
	if len(rs) != 1 {
		t.Fatalf("save %d: %d rebuilds (flash %q, notice %q)", v, len(rs), m.flash, m.set.notice)
	}
	return rs[0]
}

// genHistory is the history_days the factory built gen for.
func genHistory(t *testing.T, f *factory, gen *brief.Generator) int {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, g := range f.gens {
		if g == gen {
			return f.cfgs[i].HistoryDays
		}
	}
	return -1
}

// gatedRun starts a run that blocks until cancelled.
func gatedRun(t *testing.T, m *Model) (*brief.Generator, tea.Cmd) {
	t.Helper()
	cfg := config.Default()
	cfg.Provider.Type = "anthropic"
	cfg.TokenBudget = 400_000
	b := &gatedBackend{started: make(chan struct{}, 1)}
	old := brief.NewGenerator(cfg, b)
	m.gen = old
	wait := m.start()
	select {
	case <-b.started:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not start")
	}
	return old, wait
}

// Rebuilt generators are taken in save order whatever order they arrive
// in; none is swapped in mid-run; the final generator is the last save's.
func TestSettingsAdvRebuildOrdering(t *testing.T) {
	perms := [][]int{{0, 1, 2}, {2, 1, 0}, {1, 2, 0}, {2, 0, 1}, {0, 2, 1}, {1, 0, 2}}
	for _, during := range []bool{false, true} {
		for _, perm := range perms {
			t.Run(fmt.Sprintf("during=%v/%v", during, perm), func(t *testing.T) {
				defer advDeadline(t, 30*time.Second)()
				m, _, f := settingsModel(t, advSettingsConfig, 120, 40)
				var old *brief.Generator
				var wait tea.Cmd
				if during {
					old, wait = gatedRun(t, m)
				}
				press(t, m, runes("s"))
				msgs := []rebuiltMsg{saveHistory(t, m, 4), saveHistory(t, m, 5), saveHistory(t, m, 6)}
				for _, i := range perm {
					m.Update(msgs[i])
					if during && m.gen != old {
						t.Fatalf("generator swapped mid-run after rebuild %d", i)
					}
				}
				if during {
					m.cancel()
					drain(t, m, wait)
					if m.generating {
						t.Fatal("still generating")
					}
				}
				if h := genHistory(t, f, m.gen); h != 6 {
					t.Errorf("final generator built for history_days %d, want 6", h)
				}
				if m.pending != nil {
					t.Error("a pending generator is left over")
				}
			})
		}
	}
}

// A rebuild that lands after the run that was going when it was saved
// ended is taken at once; one landing after a newer rebuild is not.
func TestSettingsAdvRebuildAfterRunEnds(t *testing.T) {
	defer advDeadline(t, 30*time.Second)()
	m, _, f := settingsModel(t, advSettingsConfig, 120, 40)
	old, wait := gatedRun(t, m)
	press(t, m, runes("s"))
	r1 := saveHistory(t, m, 4)
	r2 := saveHistory(t, m, 5)
	m.cancel()
	drain(t, m, wait)
	if m.gen != old {
		t.Fatal("generator changed with no rebuild delivered")
	}
	m.Update(r2)
	if genHistory(t, f, m.gen) != 5 {
		t.Fatal("rebuild after the run not taken")
	}
	m.Update(r1) // stale
	if genHistory(t, f, m.gen) != 5 {
		t.Error("a stale rebuild replaced the latest")
	}
}

// Save, close, r: the run r starts uses the saved config even if its
// rebuild has not landed yet (the flash says "press r to regenerate").
func TestSettingsAdvRunRightAfterSaveUsesSavedConfig(t *testing.T) {
	defer advDeadline(t, 30*time.Second)()
	m, _, f := settingsModel(t, advSettingsConfig, 120, 40)
	cfg := config.Default()
	start := brief.NewGenerator(cfg, doneBackend{})
	m.gen = start
	press(t, m, runes("s"))
	r := saveHistory(t, m, 4)
	pressNoQuit(t, m, keyOf(tea.KeyEsc))
	if m.set != nil {
		t.Fatal("pane did not close")
	}
	_, cmd := m.Update(runes("r"))
	if !m.generating {
		t.Fatal("r did not start a run")
	}
	ranWith := m.gen
	m.Update(r)
	drain(t, m, cmd)
	if genHistory(t, f, m.gen) != 4 {
		t.Errorf("final generator built for history_days %d", genHistory(t, f, m.gen))
	}
	if ranWith != f.gens[0] {
		t.Errorf("the run r started right after saving used the pre-save generator (history_days %d), not the saved config",
			genHistory(t, f, ranWith))
	}
}

// A rebuild failing after a successful one keeps the generator there is
// and never swaps mid-run.
func TestSettingsAdvFailedRebuildDuringRun(t *testing.T) {
	defer advDeadline(t, 30*time.Second)()
	m, _, f := settingsModel(t, advSettingsConfig, 120, 40)
	old, wait := gatedRun(t, m)
	press(t, m, runes("s"))
	r1 := saveHistory(t, m, 4)
	f.err = fmt.Errorf("no creds")
	r2 := saveHistory(t, m, 5)
	m.Update(r1) // stale: r2 is the latest
	if m.gen != old || m.pending != nil {
		t.Fatal("a stale rebuild was taken")
	}
	m.Update(r2)
	if m.gen != old {
		t.Fatal("swapped mid-run")
	}
	if !strings.Contains(m.set.notice, "no creds") {
		t.Errorf("notice %q", m.set.notice)
	}
	m.cancel()
	drain(t, m, wait)
	if m.gen != old {
		t.Error("a failed latest rebuild still changed the generator")
	}
}

// Every mode fits every terminal from 20×4 to 200×60.
func TestSettingsAdvLayoutFits(t *testing.T) {
	widths := []int{20, 31, 45, 49, 63, 200}
	heights := []int{4, 6, 9, 24, 60}
	long := strings.Repeat("wide 日本語 🦀 ", 30)
	bigMute := "mute:\n"
	for i := range config.MaxMuteEntries {
		bigMute += fmt.Sprintf("  - \"%s %d\"\n", strings.Repeat("日", 40), i)
	}
	body := strings.Replace(advSettingsConfig, "mute: [crypto, sports]\n", bigMute, 1)
	body = strings.Replace(body, "model: m0", "model: "+strings.Repeat("m", 150), 1)
	modes := map[string]func(t *testing.T, m *Model){
		"list top": func(t *testing.T, m *Model) {},
		"list bottom": func(t *testing.T, m *Model) {
			for range len(fields) {
				press(t, m, keyOf(tea.KeyDown))
			}
		},
		"text edit": func(t *testing.T, m *Model) {
			goTo(t, m, "provider.region")
			press(t, m, keyOf(tea.KeyEnter), runes(long))
		},
		"num edit": func(t *testing.T, m *Model) {
			goTo(t, m, "token_budget")
			press(t, m, keyOf(tea.KeyEnter))
		},
		"profile edit": func(t *testing.T, m *Model) {
			goTo(t, m, "profile")
			press(t, m, keyOf(tea.KeyEnter), runes(long))
		},
		"mute list": func(t *testing.T, m *Model) {
			goTo(t, m, "mute")
			press(t, m, keyOf(tea.KeyEnter))
			for range 15 {
				press(t, m, keyOf(tea.KeyDown))
			}
		},
		"mute edit": func(t *testing.T, m *Model) {
			goTo(t, m, "mute")
			press(t, m, keyOf(tea.KeyEnter), runes("a"), runes(long))
		},
		"confirm": func(t *testing.T, m *Model) {
			setNumber(t, m, "concurrency", "4")
			press(t, m, keyOf(tea.KeyEsc))
		},
		"input error": func(t *testing.T, m *Model) {
			goTo(t, m, "price_per_1k_searches")
			press(t, m, keyOf(tea.KeyEnter), keyOf(tea.KeyCtrlU), runes("1.2.3"), keyOf(tea.KeyEnter))
		},
		"validation error": func(t *testing.T, m *Model) {
			setNumber(t, m, "history_days", "99")
			setNumber(t, m, "token_budget", "20000")
		},
		"notice": func(t *testing.T, m *Model) {
			m.set.notice = "Not saved: " + long
		},
	}
	for name, setup := range modes {
		t.Run(name, func(t *testing.T) {
			defer advDeadline(t, 30*time.Second)()
			m, _, _ := settingsModel(t, body, 120, 40)
			m.settings.Overrides = []string{"provider=aws", "topics=" + long}
			press(t, m, runes("s"))
			setup(t, m)
			mode := m.set.mode
			for _, w := range widths {
				for _, h := range heights {
					m.Update(tea.WindowSizeMsg{Width: w, Height: h})
					checkFits(t, name, m)
					if m.set == nil || m.set.mode != mode {
						t.Fatalf("resizing changed the mode")
					}
				}
			}
		})
	}
	t.Run("broken", func(t *testing.T) {
		defer advDeadline(t, 30*time.Second)()
		m, _, _ := settingsModel(t, "topics: [unclosed "+long+"\n", 120, 40)
		press(t, m, runes("s"))
		for _, w := range widths {
			for _, h := range heights {
				m.Update(tea.WindowSizeMsg{Width: w, Height: h})
				checkFits(t, "broken", m)
			}
		}
	})
}

// The selected field stays on screen at every size, so an edit is never
// typed blind.
func TestSettingsAdvSelectionVisible(t *testing.T) {
	defer advDeadline(t, 30*time.Second)()
	for _, size := range [][2]int{{20, 6}, {30, 8}, {50, 10}, {80, 12}, {200, 7}} {
		m, _, _ := settingsModel(t, advSettingsConfig, size[0], size[1])
		press(t, m, runes("s"))
		for range len(fields) {
			v := ansi.Strip(m.View())
			if !strings.Contains(v, "▸") {
				t.Errorf("%v: %s not shown:\n%s", size, m.set.field().key, v)
				break
			}
			press(t, m, keyOf(tea.KeyDown))
		}
	}
}

var _ = os.Getenv
