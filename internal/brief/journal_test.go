package brief

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveJournalSkipsEmptyAndKeepsEarlierDigests(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	failed := func() *Journal {
		return &Journal{Date: "2026-10-29", Sections: []*Section{
			{Topic: "A", Status: StatusFailed, Error: "rate limited", TokensUsed: 500},
			{Topic: "B", Status: StatusFailed, Error: "boom"},
		}}
	}
	if err := SaveJournal(failed()); !errors.Is(err, ErrNothingDone) {
		t.Fatalf("saving a journal with nothing done: %v", err)
	}
	if _, err := os.Stat(journalPath("2026-10-29")); !os.IsNotExist(err) {
		t.Fatalf("an empty journal was written: %v", err)
	}

	good := &Journal{Date: "2026-10-29", Sections: []*Section{
		{Topic: "A", Status: StatusDone, Markdown: "### Old A", Sources: []Source{{URL: "https://a.example"}}},
		{Topic: "B", Status: StatusFailed, Error: "boom"},
	}}
	if err := SaveJournal(good); err != nil {
		t.Fatal(err)
	}
	// A regeneration where A fails keeps the earlier A; B stays failed.
	again := failed()
	again.Sections[1] = &Section{Topic: "B", Status: StatusDone, Markdown: "### New B"}
	if err := SaveJournal(again); err != nil {
		t.Fatal(err)
	}
	j, err := LoadJournal("2026-10-29")
	if err != nil || j == nil {
		t.Fatalf("load: %v %v", j, err)
	}
	a, b := j.Sections[0], j.Sections[1]
	if a.Status != StatusDone || a.Markdown != "### Old A" || len(a.Sources) != 1 || a.TokensUsed != 500 || a.Error == "" {
		t.Errorf("A after failed refresh: %+v", a)
	}
	if b.Markdown != "### New B" {
		t.Errorf("B: %+v", b)
	}
}

func TestLoadJournalTreatsNothingDoneAsMissing(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	path := journalPath("2026-10-28")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"date":"2026-10-28","sections":[{"topic":"A","status":"failed","error":"boom"}]}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if j, err := LoadJournal("2026-10-28"); j != nil || err != nil {
		t.Errorf("journal with nothing done loaded as %+v, %v", j, err)
	}
}

// Markdown output puts each topic under its own heading, so an error
// section says which topic it is.
func TestMarkdownHeadsEachTopic(t *testing.T) {
	j := &Journal{Date: "2026-10-29", Sections: []*Section{
		{Topic: "Go", Status: StatusDone, Markdown: "### A\na."},
		{Topic: "Rust", Status: StatusFailed, Error: "boom"},
	}}
	md := j.Markdown()
	if !strings.Contains(md, "## Go\n\n### A") || !strings.Contains(md, "## Rust\n\n\n\n> **Error:** boom") {
		t.Errorf("topics not headed:\n%s", md)
	}
}

// A run over some topics merged into today's journal keeps the earlier
// runs' overshoot, added to its own.
func TestSubsetMergeKeepsEarlierOvershoot(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	prev := &Journal{Date: Today(), TokenBudget: 600_000, Overshoot: 7_000,
		Sections: []*Section{{Topic: "A", Status: StatusDone, Markdown: "### A", TokensUsed: 300_000}}}
	if err := SaveJournal(prev); err != nil {
		t.Fatal(err)
	}
	run := &Journal{Date: Today(), TokenBudget: 200_000, Subset: true, Overshoot: 2_000,
		Sections: []*Section{{Topic: "B", Status: StatusDone, Markdown: "### B", TokensUsed: 202_000}}}
	if err := SaveJournal(run); err != nil {
		t.Fatal(err)
	}
	j, err := LoadJournal(Today())
	if err != nil || j == nil || j.Overshoot != 9_000 || j.OvershootNote() == "" {
		t.Fatalf("saved %+v, %v; want overshoot 9000", j, err)
	}
}

// Saving replaces the journal atomically, leaving no temporary file, and
// moves an unparseable journal aside to <date>.json.bak (never over an
// earlier backup) instead of overwriting it.
func TestSaveJournalBacksUpUnreadableJournal(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	path := journalPath(Today())
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	fresh := func() *Journal {
		return &Journal{Date: Today(), TokenBudget: 100, Sections: []*Section{{Topic: "A", Status: StatusDone, Markdown: "### A"}}}
	}
	for n, bak := range []string{path + ".bak", path + ".bak.2"} {
		corrupt := []byte(fmt.Sprintf("{not json %d", n))
		if err := os.WriteFile(path, corrupt, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadJournal(Today()); !errors.Is(err, errUnreadable) {
			t.Fatalf("load of a corrupt journal: %v", err)
		}
		// Nothing to save: the corrupt journal stays where it is.
		if err := SaveJournal(&Journal{Date: Today()}); !errors.Is(err, ErrNothingDone) {
			t.Fatalf("save with nothing done: %v", err)
		}
		if got, _ := os.ReadFile(path); string(got) != string(corrupt) {
			t.Fatalf("corrupt journal changed to %q", got)
		}
		if err := SaveJournal(fresh()); err != nil {
			t.Fatal(err)
		}
		if got, err := os.ReadFile(bak); err != nil || string(got) != string(corrupt) {
			t.Fatalf("backup %s: %q, %v; want the corrupt journal", bak, got, err)
		}
		if j, err := LoadJournal(Today()); err != nil || j == nil || j.Sections[0].Markdown != "### A" {
			t.Fatalf("new journal %+v, %v", j, err)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("temporary file %s left behind", e.Name())
		}
	}
}
