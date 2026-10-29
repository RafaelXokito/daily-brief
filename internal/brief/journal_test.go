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
	// A keeps the earlier digest's figures; the 500 tokens its refresh
	// spent are in that run's record, as spent on a failed topic.
	if a.Status != StatusDone || a.Markdown != "### Old A" || len(a.Sources) != 1 || a.TokensUsed != 0 || a.Error == "" {
		t.Errorf("A after failed refresh: %+v", a)
	}
	if len(j.Runs) != 2 || j.Runs[1].Used != 500 || j.Runs[1].Failed != 500 {
		t.Errorf("runs %+v; want the second spending 500 on failed topics", j.Runs)
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

// A run over some topics merged into today's journal reports its own
// overshoot against its own budget; the earlier runs' overshoot stays in
// their run records and is shown apart, next to today's total.
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
	if err != nil || j == nil || j.Overshoot != 2_000 || j.Over() != 2_000 || j.EarlierOver() != 7_000 {
		t.Fatalf("saved %+v, %v; want overshoot 2000, 7000 earlier", j, err)
	}
	if note := j.OvershootNote(); !strings.Contains(note, "by 2.0k tokens (1.0%)") {
		t.Errorf("note %q", note)
	}
	if s := j.SpendSummary(); !strings.Contains(s, "7.0k over earlier today") {
		t.Errorf("summary %q", s)
	}
}

// A full run that went 30k over, then a run over some topics that spent
// 250k of its 300k: the second run is not reported as over, against any
// budget; the first run's overshoot is shown apart, next to today's total.
// A later full run reports only its own overshoot.
func TestOvershootIsPerRun(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	full := &Journal{Date: Today(), TokenBudget: 600_000, Overshoot: 30_000,
		Sections: []*Section{
			{Topic: "A", Status: StatusDone, Markdown: "### A", TokensUsed: 400_000},
			{Topic: "B", Status: StatusDone, Markdown: "### B", TokensUsed: 230_000},
		}}
	if err := SaveJournal(full); err != nil {
		t.Fatal(err)
	}
	if full.Over() != 30_000 || !strings.Contains(full.OvershootNote(), "by 30.0k tokens (5.0%)") {
		t.Errorf("full run: over %d, note %q", full.Over(), full.OvershootNote())
	}
	run := &Journal{Date: Today(), TokenBudget: 300_000, Subset: true,
		Sections: []*Section{{Topic: "B", Status: StatusDone, Markdown: "### B2", TokensUsed: 250_000}}}
	if err := SaveJournal(run); err != nil {
		t.Fatal(err)
	}
	saved, err := LoadJournal(Today())
	if err != nil || saved == nil {
		t.Fatal(saved, err)
	}
	for name, j := range map[string]*Journal{"run": run, "saved": saved} {
		if j.Over() != 0 || j.OvershootNote() != "" || strings.Contains(j.Markdown(), "went over") {
			t.Errorf("%s: over %d, note %q", name, j.Over(), j.OvershootNote())
		}
		s := j.SpendSummary()
		for _, want := range []string{"250.0k / 300.0k tokens (83%)", "880.0k today in 2 runs · 30.0k over earlier today"} {
			if !strings.Contains(s, want) {
				t.Errorf("%s: summary %q lacks %q", name, s, want)
			}
		}
	}
	again := &Journal{Date: Today(), TokenBudget: 600_000, Overshoot: 6_000,
		Sections: []*Section{
			{Topic: "A", Status: StatusDone, Markdown: "### A3", TokensUsed: 300_000},
			{Topic: "B", Status: StatusDone, Markdown: "### B3", TokensUsed: 306_000},
		}}
	if err := SaveJournal(again); err != nil {
		t.Fatal(err)
	}
	if again.Over() != 6_000 || again.EarlierOver() != 30_000 || !strings.Contains(again.OvershootNote(), "by 6.0k tokens (1.0%)") {
		t.Errorf("later full run: over %d, earlier %d, note %q", again.Over(), again.EarlierOver(), again.OvershootNote())
	}
	if md := again.Markdown(); strings.Contains(md, "⚠") || !strings.Contains(md, "> **Warning:** This run went over") {
		t.Errorf("overshoot note in the journal:\n%s", md)
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
