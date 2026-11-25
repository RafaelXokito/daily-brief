package paper

import (
	"os"
	"path/filepath"
	"testing"
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

func TestLoadStateCorrupt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, stateFile), []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(dir); err == nil {
		t.Error("corrupt state must be an error")
	}
}
