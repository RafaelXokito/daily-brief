package brief

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func savedFull(t *testing.T) *Journal {
	t.Helper()
	full := &Journal{Date: Today(), TokenBudget: 600_000,
		Overview: Section{Topic: "Overview", Status: StatusDone, Markdown: "## Today's highlights\n\n- old", TokensUsed: 5_000},
		Sections: []*Section{
			{Topic: "A", Status: StatusDone, Markdown: "### Old A", TokensUsed: 100_000},
			{Topic: "B", Status: StatusDone, Markdown: "### Old B", TokensUsed: 100_000},
			{Topic: "C", Status: StatusDone, Markdown: "### Old C", TokensUsed: 100_000},
		}}
	if err := SaveJournal(full); err != nil {
		t.Fatal(err)
	}
	return full
}

// A run over some topics replaces only those sections of today's journal
// and keeps the rest; without budget for a new overview, the old one stays,
// marked as possibly stale.
func TestSubsetRunMergesIntoTodaysJournal(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	savedFull(t)
	run := &Journal{Date: Today(), TokenBudget: 150_000, Subset: true,
		Overview: Section{Topic: "Overview", Status: StatusFailed, Error: "skipped: not enough token budget left for the overview"},
		Sections: []*Section{{Topic: "B", Status: StatusDone, Markdown: "### New B", TokensUsed: 120_000}}}
	if err := SaveJournal(run); err != nil {
		t.Fatal(err)
	}
	j, err := LoadJournal(Today())
	if err != nil || j == nil {
		t.Fatalf("load: %v", err)
	}
	var got []string
	for _, s := range j.Sections {
		got = append(got, s.Topic+":"+s.Markdown)
	}
	if want := "A:### Old A|B:### New B|C:### Old C"; strings.Join(got, "|") != want {
		t.Errorf("sections %q, want %q", strings.Join(got, "|"), want)
	}
	if ov := j.Overview; ov.Markdown != "## Today's highlights\n\n- old" || !ov.Stale || !strings.Contains(ov.Render(), "may be out of date") {
		t.Errorf("overview %+v, want the old one marked stale", ov)
	}
	// Kept sections keep their tokens; the budget stays this run's, and
	// each run's spend is recorded.
	if j.TokensUsed() != 325_000 || j.TokenBudget != 150_000 || j.RunUsed() != 120_000 || j.TodayUsed() != 305_000+120_000 || j.Utilization() > 100 {
		t.Errorf("tokens %d, run %d of %d, today %d", j.TokensUsed(), j.RunUsed(), j.TokenBudget, j.TodayUsed())
	}
	// The run's journal is updated to what was saved, as the TUI shows it.
	if len(run.Sections) != 3 || !run.Overview.Stale {
		t.Errorf("run journal not merged in place: %d sections", len(run.Sections))
	}
}

// A full regeneration whose overview fails keeps the earlier overview too.
func TestFullRunKeepsEarlierOverviewWhenItsOwnFails(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	savedFull(t)
	run := &Journal{Date: Today(), TokenBudget: 600_000,
		Overview: Section{Topic: "Overview", Status: StatusFailed, Error: "cancelled"},
		Sections: []*Section{
			{Topic: "A", Status: StatusDone, Markdown: "### New A"},
			{Topic: "B", Status: StatusFailed, Error: "boom"},
		}}
	if err := SaveJournal(run); err != nil {
		t.Fatal(err)
	}
	j, _ := LoadJournal(Today())
	if len(j.Sections) != 2 || j.Sections[1].Markdown != "### Old B" || !j.Overview.Stale || j.Overview.Markdown == "" {
		t.Errorf("sections %d, B %q, overview stale %v %q", len(j.Sections), j.Sections[1].Markdown, j.Overview.Stale, j.Overview.Markdown)
	}
}

// summaryBackend records the overview's input.
type summaryBackend struct {
	scriptBackend
	mu    sync.Mutex
	input string
}

func (b *summaryBackend) Summarize(_ context.Context, _, input string) Result {
	b.mu.Lock()
	b.input = input
	b.mu.Unlock()
	return Result{Markdown: "## Today's highlights\n\n- new", Tokens: 3_000}
}

// Through the generator: a run over one topic, given budget, writes a new
// overview from the merged sections, keeps the reading caps of the whole
// config and saves a journal with every topic.
func TestFilteredRunRegeneratesOverviewFromMergedSections(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	savedFull(t)
	cfg := testConfig("anthropic", 700_000, "A", "B", "C")
	full := itemCaps(cfg)
	if err := cfg.FilterTopics([]string{"b"}); err != nil {
		t.Fatal(err)
	}
	if caps := itemCaps(cfg); len(caps) != 1 || caps[0] != full[1] {
		t.Errorf("filtered caps %v, want B's cap in the full config %d", caps, full[1])
	}
	b := &summaryBackend{scriptBackend: scriptBackend{research: func(context.Context, Task, int) Result {
		return Result{Markdown: "### New B", Tokens: 50_000, Searches: 1}
	}}}
	j, _ := runWithin(t, context.Background(), cfg, b, 30*time.Second)
	for _, want := range []string{"### Old A", "### New B", "### Old C"} {
		if !strings.Contains(b.input, want) {
			t.Errorf("overview input lacks %q:\n%s", want, b.input)
		}
	}
	if strings.Contains(b.input, "### Old B") {
		t.Errorf("overview input has the replaced digest")
	}
	if err := SaveJournal(j); err != nil {
		t.Fatal(err)
	}
	saved, _ := LoadJournal(Today())
	if len(saved.Sections) != 3 || saved.Sections[1].Markdown != "### New B" || saved.Overview.Stale ||
		!strings.Contains(saved.Overview.Markdown, "- new") || !strings.Contains(saved.Overview.Markdown, "3 items · 0 updates · 3 topics") ||
		!strings.Contains(saved.Overview.Markdown, "- A — 1 item\n- B — 1 item\n- C — 1 item") {
		t.Errorf("saved %d sections, B %q, overview %q stale %v", len(saved.Sections), saved.Sections[1].Markdown, saved.Overview.Markdown, saved.Overview.Stale)
	}
}

// digestOf is a digest of n items of about 44 words each.
func digestOf(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "### Headline %d\n%s[source](https://e%d.example)\n\n", i, strings.Repeat("word ", 40), i)
	}
	return b.String()
}

// After max_read_minutes is lowered, saving over an earlier, longer journal
// keeps within the new cap: topics the cap now skips stay empty, a topic
// that failed gets the earlier digest cut to its cap, and a subset run
// drops kept topics that are no longer configured and cuts the rest.
func TestMergeKeepsJournalWithinLoweredReadCap(t *testing.T) {
	names := []string{"A", "B", "C", "D", "E"}
	saveEarlier := func(t *testing.T, topics ...string) {
		t.Helper()
		prev := &Journal{Date: Today(), TokenBudget: 600_000, Overview: Section{Topic: "Overview", Status: StatusDone, Markdown: "## Today's highlights\n\n- old"}}
		for _, n := range topics {
			prev.Sections = append(prev.Sections, &Section{Topic: n, Status: StatusDone, Markdown: digestOf(7), TokensUsed: 100_000})
		}
		if err := SaveJournal(prev); err != nil {
			t.Fatal(err)
		}
	}
	cfg := testConfig("anthropic", 600_000, names...)
	cfg.MaxReadMinutes = 1
	caps := itemCaps(cfg)

	t.Run("full run", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", t.TempDir())
		saveEarlier(t, names...)
		run := &Journal{Date: Today(), TokenBudget: 600_000, Caps: topicCaps(cfg),
			Overview: Section{Topic: "Overview", Status: StatusDone, Markdown: "## Today's highlights\n\n- new"}}
		for i, n := range names {
			s := &Section{Topic: n, Status: StatusDone, Markdown: capItems(digestOf(7), caps[i]), TokensUsed: 50_000}
			switch {
			case caps[i] == 0:
				s.Status, s.Error, s.Markdown = StatusFailed, skippedRead, ""
			case i == 1:
				s.Status, s.Error, s.Markdown = StatusFailed, "boom", ""
			}
			run.Sections = append(run.Sections, s)
		}
		if err := SaveJournal(run); err != nil {
			t.Fatal(err)
		}
		if got := run.ReadMinutes(); got > float64(cfg.MaxReadMinutes) {
			t.Errorf("saved journal reads in %.1f min, cap %d min (caps %v)", got, cfg.MaxReadMinutes, caps)
		}
		for i, s := range run.Sections {
			if n := countItems(s.Markdown); n > caps[i] {
				t.Errorf("%s: %d items, cap %d", s.Topic, n, caps[i])
			}
		}
		if b := run.Sections[1]; b.Status != StatusDone || !strings.Contains(b.Error, "earlier digest") {
			t.Errorf("failed topic B not filled from the earlier digest: %s %q", b.Status, b.Error)
		}
	})

	t.Run("subset run", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", t.TempDir())
		saveEarlier(t, append([]string{"Gone"}, names...)...)
		sub := cfg
		if err := sub.FilterTopics([]string{"A"}); err != nil {
			t.Fatal(err)
		}
		run := &Journal{Date: Today(), TokenBudget: 100_000, Subset: true, Caps: topicCaps(sub),
			Overview: Section{Topic: "Overview", Status: StatusDone, Markdown: "## Today's highlights\n\n- new"},
			Sections: []*Section{{Topic: "A", Status: StatusDone, Markdown: capItems(digestOf(7), caps[0]), TokensUsed: 50_000}}}
		if err := SaveJournal(run); err != nil {
			t.Fatal(err)
		}
		if got := run.ReadMinutes(); got > float64(cfg.MaxReadMinutes) {
			t.Errorf("saved journal reads in %.1f min, cap %d min", got, cfg.MaxReadMinutes)
		}
		if len(run.Sections) != len(names) || slices.ContainsFunc(run.Sections, func(s *Section) bool { return s.Topic == "Gone" }) {
			t.Errorf("saved %d sections; want the %d configured topics only", len(run.Sections), len(names))
		}
	})
}
