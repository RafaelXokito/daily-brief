package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/RafaelXokito/daily-brief/internal/brief"
	"github.com/RafaelXokito/daily-brief/internal/config"
)

const notice = "↑ v0.1.1 available · U to update"

// offered is a model at width whose update check found v0.1.1.
func offered(t *testing.T, width int) *Model {
	t.Helper()
	m := longModel(t, width)
	m.Update(updateAvailableMsg("v0.1.1"))
	return m
}

// The check runs as a command from Init, never in it, and its answer
// shows in the header; a bad tag or "" shows nothing.
func TestUpdateCheckRunsInBackground(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		release := make(chan struct{})
		m := longModel(t, 120)
		m.WithUpdateCheck(func(context.Context) string {
			<-release
			return "v0.1.1"
		})
		start := time.Now()
		cmd := m.Init()
		if time.Since(start) > time.Second {
			t.Fatal("Init waited for the check")
		}
		got := make(chan []tea.Msg, 1)
		go func() { got <- cmdMsgs(cmd) }()
		close(release)
		var found bool
		for _, msg := range <-got {
			if v, ok := msg.(updateAvailableMsg); ok && v == "v0.1.1" {
				found = true
				m.Update(msg)
			}
		}
		if !found || !strings.Contains(ansi.Strip(m.View()), notice) {
			t.Errorf("notice not shown:\n%s", ansi.Strip(m.View()))
		}
		// Without a check, Init asks nothing.
		if longModel(t, 120).updateCheck() != nil {
			t.Error("a check without WithUpdateCheck")
		}
		for _, v := range []string{"", "v0.1.1\x1b[2J", "v0.2.0-rc.1"} {
			m := longModel(t, 120)
			m.Update(updateAvailableMsg(v))
			if m.available != "" || strings.Contains(ansi.Strip(m.View()), "available") {
				t.Errorf("%q offered", v)
			}
		}
	})
}

// The notice ends the header in the warning colour, the rest of the
// header giving way to it; it is dropped when even the title can't stay,
// and no line is ever wider than the terminal.
func TestUpdateNoticeByWidth(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		lipgloss.SetHasDarkBackground(true)
		for _, w := range []int{200, 120, 80, 60, 50, 46, 45, 44, 40, 30, 20} {
			m := offered(t, w)
			view := m.View()
			first := strings.Split(view, "\n")[0]
			shown := strings.Contains(ansi.Strip(first), notice)
			if want := w-ansi.StringWidth(notice)-2 >= minNoticeHeader; shown != want {
				t.Errorf("width %d: notice shown %v, want %v: %q", w, shown, want, ansi.Strip(first))
			}
			if shown {
				if !strings.HasPrefix(ansi.Strip(first), "Daily Brief") {
					t.Errorf("width %d: header lost its title: %q", w, ansi.Strip(first))
				}
				if x := strings.Index(ansi.Strip(first), "↑"); ansi.StringWidth(ansi.Strip(first)[:x]) != m.notice[0] ||
					m.notice[1] != m.notice[0]+ansi.StringWidth(notice) {
					t.Errorf("width %d: notice recorded at %v", w, m.notice)
				}
			} else if m.notice != [2]int{} {
				t.Errorf("width %d: a hidden notice recorded at %v", w, m.notice)
			}
			for _, l := range strings.Split(view, "\n") {
				if lipgloss.Width(l) > w {
					t.Errorf("width %d: line too wide: %q", w, ansi.Strip(l))
				}
			}
		}
		// Without an update, no notice.
		m := longModel(t, 120)
		if strings.Contains(ansi.Strip(m.View()), "available") || m.notice != [2]int{} {
			t.Error("a notice without an update")
		}
	})
}

// U asks; n and esc cancel; y records the release and quits.
func TestUpdateKeyPrompt(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		m := offered(t, 120)
		for _, cancel := range []tea.KeyMsg{runes("n"), {Type: tea.KeyEsc}} {
			if _, cmd := m.Update(runes("U")); cmd != nil || !m.askUpdate {
				t.Fatal("U did not ask")
			}
			if help := ansi.Strip(m.helpText()); help != "Update to v0.1.1? y/n" {
				t.Errorf("prompt %q", help)
			}
			// Other keys neither answer nor act.
			sel := m.selected
			m.Update(runes("j"))
			m.Update(runes("2"))
			if !m.askUpdate || m.selected != sel {
				t.Error("another key answered the prompt")
			}
			_, cmd := m.Update(cancel)
			if m.askUpdate || m.WantUpdate() != "" || isQuit(cmd) {
				t.Errorf("%v did not cancel", cancel)
			}
			if strings.Contains(ansi.Strip(m.helpText()), "Update to") {
				t.Error("prompt still shown")
			}
		}
		m.Update(runes("U"))
		_, cmd := m.Update(runes("y"))
		if m.WantUpdate() != "v0.1.1" || !isQuit(cmd) {
			t.Errorf("y: want %q, quit %v", m.WantUpdate(), isQuit(cmd))
		}

		// q at the prompt quits without updating.
		m = offered(t, 120)
		m.Update(runes("U"))
		if _, cmd := m.Update(runes("q")); !isQuit(cmd) || m.WantUpdate() != "" {
			t.Error("q at the prompt")
		}
		// Without an update, U says so.
		m = longModel(t, 120)
		if m.Update(runes("U")); m.askUpdate || m.flash != "No update available" {
			t.Errorf("U without an update: flash %q", m.flash)
		}
	})
}

// A click on the notice asks; elsewhere on the header it does not.
func TestUpdateNoticeClick(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		m := offered(t, 120)
		m.WithMouse(true)
		m.View()
		if m.Update(click(m.notice[0]-2, 0)); m.askUpdate {
			t.Error("a click left of the notice asked")
		}
		if m.Update(click(m.notice[1], 0)); m.askUpdate {
			t.Error("a click right of the notice asked")
		}
		if m.Update(click(m.notice[0]+3, 1)); m.askUpdate {
			t.Error("a click below the notice asked")
		}
		if m.Update(click(m.notice[0], 0)); !m.askUpdate {
			t.Error("a click on the notice did not ask")
		}
		m.Update(runes("n"))
		if m.Update(click(m.notice[1]-1, 0)); !m.askUpdate {
			t.Error("a click on the notice's end did not ask")
		}
		// hitTest finds it from the recorded geometry alone.
		g := geometry{top: 3, height: 10, notice: [2]int{40, 72}}
		for x, want := range map[int]region{39: regionNone, 40: regionNotice, 71: regionNotice, 72: regionNone} {
			if got := hitTest(g, x, 0).region; got != want {
				t.Errorf("hitTest(%d, 0) = %v, want %v", x, got, want)
			}
		}
		// Too narrow for the notice: nothing to click.
		m = offered(t, 40)
		m.View()
		for x := range 40 {
			if m.Update(click(x, 0)); m.askUpdate {
				t.Fatalf("a click at %d on a header without the notice asked", x)
			}
		}
	})
}

// During a run U and the notice refuse, and a y can't slip through.
func TestUpdateRefusedDuringRun(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		t.Setenv("XDG_DATA_HOME", t.TempDir())
		cfg := config.Default()
		cfg.Provider.Type = "anthropic"
		cfg.TokenBudget = 400_000
		cfg.Topics = []config.Topic{{Name: "A"}}
		b := &gatedBackend{started: make(chan struct{}, 1)}
		m := New(brief.NewGenerator(cfg, b), nil, nil, "notty")
		m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
		m.Update(updateAvailableMsg("v0.1.1"))
		wait := m.start()
		<-b.started

		m.Update(runes("U"))
		if m.askUpdate || !strings.Contains(m.flash, "wait for the run") {
			t.Errorf("U during a run: ask %v, flash %q", m.askUpdate, m.flash)
		}
		m.View()
		m.WithMouse(true)
		if m.Update(click(m.notice[0], 0)); m.askUpdate {
			t.Error("a click on the notice asked during a run")
		}
		// A run started while asked (it can't be, but if it were) refuses y.
		m.askUpdate = true
		if _, cmd := m.Update(runes("y")); m.WantUpdate() != "" || isQuit(cmd) {
			t.Error("y during a run")
		}
		m.cancel()
		drain(t, m, wait)
		m.Update(runes("U"))
		if !m.askUpdate {
			t.Error("U after the run did not ask")
		}
	})
}

// update_check is an on/off field that round-trips through the file.
func TestSettingsUpdateCheckRoundTrip(t *testing.T) {
	m, path, _ := settingsModel(t, settingsConfig, 120, 40)
	press(t, m, runes("s"))
	goTo(t, m, "update_check")
	if v := enumValue(m.set.field(), &m.set.cfg); v != "on" {
		t.Fatalf("update_check shows %q, want on", v)
	}
	if !strings.Contains(ansi.Strip(m.View()), "api.github.com") {
		t.Error("no hint for update_check")
	}
	press(t, m, keyOf(tea.KeyRight))
	if m.set.cfg.UpdateCheckOn() || enumValue(m.set.field(), &m.set.cfg) != "off" {
		t.Fatal("update_check not turned off")
	}
	press(t, m, keyOf(tea.KeyCtrlS))
	got, _, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.UpdateCheckOn() {
		t.Error("update_check: false not saved")
	}
	if file := readFile(t, path); !strings.Contains(file, "update_check: false") || !strings.Contains(file, "my_note: keep me") {
		t.Errorf("saved file:\n%s", file)
	}
	press(t, m, keyOf(tea.KeyLeft), keyOf(tea.KeyCtrlS))
	if got, _, _ := config.Load(path); !got.UpdateCheckOn() {
		t.Error("update_check not turned back on")
	}
}

// Quitting cancels a check still waiting on GitHub, so it is not cached.
func TestQuitCancelsUpdateCheck(t *testing.T) {
	advTimeout(t, 30*time.Second, func() {
		canceled := make(chan bool, 1)
		m := longModel(t, 120)
		m.WithUpdateCheck(func(ctx context.Context) string {
			select {
			case <-ctx.Done():
				canceled <- true
			case <-time.After(20 * time.Second):
				canceled <- false
			}
			return ""
		})
		cmd := m.updateCheck()
		go cmd()
		m.Update(runes("q"))
		if !<-canceled {
			t.Error("quitting did not cancel the check")
		}
	})
}
