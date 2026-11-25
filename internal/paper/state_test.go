package paper

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestDecide(t *testing.T) {
	for _, tc := range []struct {
		name           string
		s              State
		refresh, local bool
		want           Action
	}{
		{"first run", State{}, false, false, Build},
		{"yesterday delivered", State{Date: "2026-11-24", Delivered: true}, false, false, Build},
		{"yesterday undelivered", State{Date: "2026-11-24"}, false, true, Build},
		{"today delivered", State{Date: "2026-11-25", Delivered: true}, false, true, Skip},
		{"today delivered book gone", State{Date: "2026-11-25", Delivered: true}, false, false, Skip},
		{"today built not delivered", State{Date: "2026-11-25"}, false, true, Redeliver},
		{"today not delivered book gone", State{Date: "2026-11-25"}, false, false, Build},
		{"refresh", State{Date: "2026-11-25", Delivered: true}, true, true, RebuildToday},
		{"refresh undelivered", State{Date: "2026-11-25"}, true, true, RebuildToday},
		{"refresh book gone", State{Date: "2026-11-25", Delivered: true}, true, false, RebuildToday},
		{"refresh undelivered book gone", State{Date: "2026-11-25"}, true, false, RebuildToday},
		{"refresh on a new day", State{Date: "2026-11-24", Delivered: true}, true, true, Build},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Decide(tc.s, "2026-11-25", tc.refresh, tc.local); got != tc.want {
				t.Errorf("Decide = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFileName(t *testing.T) {
	for _, tc := range []struct {
		title   string
		rebuild int
		want    string
	}{
		{"T", 0, "T 2026-11-25.epub"},
		{"T", 1, "T 2026-11-25 (1).epub"},
		{"T", 2, "T 2026-11-25 (2).epub"},
		{"  T  ", 0, "T 2026-11-25.epub"},
		{"", 0, "The Daily Brief 2026-11-25.epub"},
		{"   ", 2, "The Daily Brief 2026-11-25 (2).epub"},
	} {
		if got := FileName(tc.title, "2026-11-25", tc.rebuild); got != tc.want {
			t.Errorf("FileName(%q, %d) = %q, want %q", tc.title, tc.rebuild, got, tc.want)
		}
	}
}

func TestStale(t *testing.T) {
	names := []string{
		"How to add books to your eReader using Dropbox.pdf",
		"The Daily Brief 2026-11-24.epub",
		"The Daily Brief 2026-11-25.epub",
		"The Daily Brief 2026-11-25 (2).epub",
		"The Daily Brief notes.epub",
		"My Book.epub",
	}
	got := Stale(names, "The Daily Brief", "The Daily Brief 2026-11-25 (2).epub")
	want := []string{"The Daily Brief 2026-11-24.epub", "The Daily Brief 2026-11-25.epub"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Stale = %q, want %q", got, want)
	}
}

func TestStaleRegexTitle(t *testing.T) {
	names := []string{
		"A+B (x) 2026-11-24.epub",
		"AAB x 2026-11-24.epub",
		"A+B (x) 2026-11-24.epub.bak",
		"A+B (x) 2026-11-2.epub",
		"A+B (x) 2026-11-24 (0).epub",
		"A+B (x) 2026-11-24 (01).epub",
	}
	got := Stale(names, "A+B (x)", "A+B (x) 2026-11-25.epub")
	if len(got) != 1 || got[0] != names[0] {
		t.Errorf("Stale = %q, want only %q", got, names[0])
	}
}

func TestStaleBlankTitleUsesDefault(t *testing.T) {
	got := Stale([]string{"The Daily Brief 2026-11-24.epub"}, " ", "x.epub")
	if len(got) != 1 {
		t.Errorf("Stale = %q", got)
	}
}

func TestStateRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	zero, err := LoadState(dir)
	if err != nil || zero != (State{}) {
		t.Fatalf("missing file: %+v, %v", zero, err)
	}
	want := State{Issue: 7, Date: "2026-11-25", File: "T 2026-11-25 (2).epub", Rebuild: 2, Delivered: true}
	if err := SaveState(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState(dir)
	if err != nil || got != want {
		t.Fatalf("LoadState = %+v, %v; want %+v", got, err, want)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("leftover files after atomic save: %v", entries)
	}
}

func TestLoadStateCorruptIsSetAside(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, stateFile)
	if err := os.WriteFile(bad, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := LoadState(dir)
	if !errors.Is(err, ErrCorruptState) {
		t.Fatalf("err = %v, want ErrCorruptState", err)
	}
	if s != (State{}) {
		t.Errorf("state %+v, want zero", s)
	}
	if _, err := os.Stat(bad); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("bad file still in place: %v", err)
	}
	if raw, err := os.ReadFile(bad + ".corrupt"); err != nil || string(raw) != "{nope" {
		t.Errorf(".corrupt file: %q, %v", raw, err)
	}
	if s, err := LoadState(dir); err != nil || s != (State{}) {
		t.Errorf("second load: %+v, %v", s, err)
	}
}

func TestLoadStateIOErrorStaysHard(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, stateFile), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := LoadState(dir)
	if err == nil || errors.Is(err, ErrCorruptState) {
		t.Errorf("err = %v, want a plain I/O error", err)
	}
}

func TestExpired(t *testing.T) {
	names := []string{
		"The Daily Brief 2026-11-01.epub",
		"The Daily Brief 2026-11-01 (1).epub",
		"The Daily Brief 2026-11-15.epub",
		"The Daily Brief 2026-11-16.epub",
		"The Daily Brief 2026-11-25.epub",
		"Other 2026-01-01.epub",
		"The Daily Brief 2026-01-01.pdf",
		"The Daily Brief 2026-13-45.epub",
		"My Book.epub",
	}
	today := time.Date(2026, 11, 25, 15, 0, 0, 0, time.Local)
	got := Expired(names, "The Daily Brief", today, 10)
	want := []string{
		"The Daily Brief 2026-11-01.epub",
		"The Daily Brief 2026-11-01 (1).epub",
	} // 2026-11-15 is exactly 10 days old: kept
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Expired = %v, want %v", got, want)
	}
	if got := Expired(names, "The Daily Brief", today, 0); len(got) != 0 {
		t.Errorf("0 days must expire nothing: %v", got)
	}
}
