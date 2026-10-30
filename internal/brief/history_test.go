package brief

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rafaelpereira/daily-brief/internal/config"
)

// daysAgo is the date n days before today.
func daysAgo(n int) string {
	return time.Now().AddDate(0, 0, -n).Format("2006-01-02")
}

// saveDay saves a journal for date with a finished section per topic.
func saveDay(t *testing.T, date string, digests map[string]string) {
	t.Helper()
	j := &Journal{Date: date, TokenBudget: 100_000, Overview: Section{Topic: "Overview", Status: StatusDone, Markdown: "## Today's highlights\n\n- Overview item"}}
	for _, topic := range slices.Sorted(func(yield func(string) bool) {
		for k := range digests {
			if !yield(k) {
				return
			}
		}
	}) {
		j.Sections = append(j.Sections, &Section{Topic: topic, Status: StatusDone, Markdown: digests[topic]})
	}
	if err := SaveJournal(j); err != nil {
		t.Fatal(err)
	}
}

func goldenTasks(topic config.Topic) map[string]Task {
	prior := "### Old item\nSomething happened. [source](https://a.example/x)"
	return map[string]Task{
		"first":    {Topic: topic, Budget: 207_750, Searches: 4, Items: 6},
		"topup":    {Topic: topic, Budget: 300_000, Searches: 3, Items: 6, Prior: prior},
		"narrow":   {Topic: topic, Budget: 8_000, Searches: 0, Items: 6, Prior: prior},
		"nosearch": {Topic: topic, Budget: 8_000, Searches: 0, Items: 3},
	}
}

// Without history, every kind of pass gets exactly the prompt it got
// before history existed (testdata holds those, with the date replaced).
func TestTaskPromptWithoutHistoryIsUnchanged(t *testing.T) {
	today := time.Now().Format("Monday, 2 January 2006")
	for name, task := range goldenTasks(config.Topic{Name: "Security", Focus: "Critical CVEs."}) {
		want, err := os.ReadFile(filepath.Join("testdata", "prompt_"+name+".golden"))
		if err != nil {
			t.Fatal(err)
		}
		task.History = []Reported{}
		if got := strings.ReplaceAll(taskPrompt(task), today, "{{TODAY}}"); got != string(want) {
			t.Errorf("%s prompt changed:\n%s\n--- want ---\n%s", name, got, want)
		}
	}
}

// With history, every kind of pass lists it compactly, with the rule for
// updates, before any prior digest.
func TestTaskPromptListsHistory(t *testing.T) {
	hist := []Reported{
		{Date: "2026-10-27", Headline: "Widget 2.0 released", URL: "https://www.Widget.dev/blog/2-0/?utm_source=x#top"},
		{Date: "2026-10-26", Headline: "No link here"},
	}
	block := "<previously_reported>\nTue 27 Oct · Widget 2.0 released · widget.dev/blog/2-0\nMon 26 Oct · No link here\n</previously_reported>\n\n" + historyInstruction
	for name, task := range goldenTasks(config.Topic{Name: "Security"}) {
		task.History = hist
		p := taskPrompt(task)
		if !strings.Contains(p, block) {
			t.Errorf("%s prompt lacks the history block:\n%s", name, p)
		}
		if task.Prior != "" && strings.Index(p, "<digest>") < strings.Index(p, "<previously_reported>") {
			t.Errorf("%s: history after the prior digest", name)
		}
	}
	for _, want := range []string{"Do not repeat them", "material new development", "### Update: <headline>", "Since Mon: …", "count toward the item limit"} {
		if !strings.Contains(historyInstruction, want) {
			t.Errorf("instruction lacks %q", want)
		}
	}
}

func TestNormalizeURL(t *testing.T) {
	for raw, want := range map[string]string{
		"https://www.Example.COM/a/b/":                    "example.com/a/b",
		"http://example.com/a/b#frag":                     "example.com/a/b",
		"https://example.com/a?utm_source=x&utm_medium=y": "example.com/a",
		"https://example.com/a?id=7&utm_campaign=z":       "example.com/a?id=7",
		"https://example.com/":                            "example.com",
		"":                                                "",
		"not a url/":                                      "",
		"/relative/path":                                  "",
		"javascript:alert(1)":                             "",
		"ftp://example.com/a":                             "",
		"https://example.com:443/a":                       "example.com/a",
		"http://example.com:80/a":                         "example.com/a",
		"https://example.com:8443/a":                      "example.com:8443/a",
		"https://nvd.nist.gov/vuln/detail/CVE-2026-1234#ref": "nvd.nist.gov/vuln/detail/CVE-2026-1234",
	} {
		if got := normalizeURL(raw); got != want {
			t.Errorf("normalizeURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestItemHeadline(t *testing.T) {
	for item, want := range map[string]string{
		"### Widget 2.0 released\nWhy it matters.":                    "Widget 2.0 released",
		"### Update: Widget 2.0 patched\nSince Mon: a patch.":         "Widget 2.0 patched",
		"### **Update:** Widget breach grows":                         "Widget breach grows",
		"## ↻ Update · Widget":                                        "↻ Update · Widget",
		"- **Big CVE in Foo**: patch now [source](https://a.example)": "Big CVE in Foo",
		"1. Plain bullet with [a link](https://a.example) inside":     "Plain bullet with a link inside",
		"**Bold lead** and the rest":                                  "Bold lead",
	} {
		if got := itemHeadline(item); got != want {
			t.Errorf("itemHeadline(%q) = %q, want %q", item, got, want)
		}
	}
}

// History reads only the history_days before today, done topics asked
// for, no overview; it lists each item (link and headline) once, at its
// latest date, newest first, and skips missing and corrupt journals. An
// update sharing an earlier story's link under another headline is listed
// apart from it.
func TestLoadHistory(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	saveDay(t, Today(), map[string]string{"A": "### Today's item\nx [s](https://today.example)"})
	saveDay(t, daysAgo(1), map[string]string{
		"A": "### Update: Widget breach grows\nSince Mon: more. [s](https://www.widget.dev/breach/?utm_source=feed)\n\n### Fresh A\ny [s](https://fresh.example/a)",
		"B": "### B item\nz [s](https://b.example)",
	})
	// Day 2 is corrupt.
	if err := os.WriteFile(journalPath(daysAgo(2)), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	saveDay(t, daysAgo(3), map[string]string{
		"A": "### Widget breach\nOld. [s](http://widget.dev/breach#x)\n\n### No link item\nText only.",
	})
	saveDay(t, daysAgo(4), map[string]string{"A": "### Too old\nx [s](https://old.example)"})
	// A failed section on day 1 of another topic is skipped.
	j, _ := readJournal(journalPath(daysAgo(1)))
	j.Sections = append(j.Sections, &Section{Topic: "C", Status: StatusFailed, Markdown: "### Failed C"})
	data, _ := json.Marshal(j)
	if err := os.WriteFile(journalPath(daysAgo(1)), data, 0o644); err != nil {
		t.Fatal(err)
	}

	h := LoadHistory(Today(), 3, []string{"A", "C"})
	var got []string
	for _, r := range h["A"] {
		got = append(got, r.Date+"|"+r.Headline+"|"+normalizeURL(r.URL))
	}
	want := []string{
		daysAgo(1) + "|Widget breach grows|widget.dev/breach",
		daysAgo(1) + "|Fresh A|fresh.example/a",
		daysAgo(3) + "|Widget breach|widget.dev/breach",
		daysAgo(3) + "|No link item|",
	}
	if !slices.Equal(got, want) {
		t.Errorf("history of A:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if _, ok := h["B"]; ok {
		t.Errorf("history has topic B, which was not asked for")
	}
	if len(h["C"]) != 0 {
		t.Errorf("history has C's failed section: %v", h["C"])
	}
	if h := LoadHistory(Today(), 0, []string{"A"}); h != nil {
		t.Errorf("history_days 0 loaded %v", h)
	}
}

// A topic's history keeps at most 12 entries and about 1,500 characters,
// newest first.
func TestLoadHistoryIsCapped(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	var short, long strings.Builder
	for i := range 20 {
		fmt.Fprintf(&short, "### Item %d\nx [s](https://e.example/%d)\n\n", i, i)
		fmt.Fprintf(&long, "### %s %d\nx [s](https://e.example/a-rather-long-path/for-item/%d)\n\n", strings.Repeat("Long headline words ", 8), i, i)
	}
	saveDay(t, daysAgo(1), map[string]string{"S": short.String(), "L": long.String()})
	h := LoadHistory(Today(), 3, []string{"S", "L"})
	if n := len(h["S"]); n != maxHistoryItems || h["S"][0].Headline != "Item 0" {
		t.Errorf("short items: %d entries, first %+v; want 12 from Item 0", n, h["S"][0])
	}
	size := 0
	for _, r := range h["L"] {
		size += len(r.line()) + 1
		if n := len([]rune(r.Headline)); n > maxHistoryHeadline {
			t.Errorf("headline of %d runes", n)
		}
	}
	if size > maxHistoryChars || len(h["L"]) < 5 {
		t.Errorf("long items: %d entries in %d chars, cap %d", len(h["L"]), size, maxHistoryChars)
	}
}

// Every pass over a topic, first, top-up or narrow, carries its history;
// a run over some topics loads history for those only.
func TestRunPassesCarryHistory(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	saveDay(t, daysAgo(1), map[string]string{"A": "### Old A\nx [s](https://a.example)", "B": "### Old B\ny [s](https://b.example)", "C": "### Old C\nz"})

	cfg := testConfig("anthropic", 0, "A", "B", "C")
	b := &scriptBackend{research: spending(0.3)}
	_, _ = runWithin(t, context.Background(), cfg, b, 10*time.Second)
	kinds := map[string]bool{}
	for _, task := range b.snapshot() {
		kind := map[bool]string{true: "first", false: "top-up"}[task.Prior == ""]
		if task.Searches == 0 {
			kind = "narrow"
		}
		kinds[kind] = true
		if len(task.History) != 1 || task.History[0].Headline != "Old "+task.Topic.Name {
			t.Errorf("%s pass of %s has history %+v", kind, task.Topic.Name, task.History)
		}
		if !strings.Contains(taskPrompt(task), "<previously_reported>") {
			t.Errorf("%s pass of %s: prompt lacks the history", kind, task.Topic.Name)
		}
	}
	for _, k := range []string{"first", "top-up", "narrow"} {
		if !kinds[k] {
			t.Errorf("no %s pass ran (ran %v)", k, kinds)
		}
	}

	// A -topics run: only B is researched, with only B's history.
	sub := testConfig("anthropic", 0, "A", "B", "C")
	if err := sub.FilterTopics([]string{"B"}); err != nil {
		t.Fatal(err)
	}
	g := NewGenerator(sub, &fakeBackend{})
	if h := g.loadHistory(Today()); len(h) != 1 || len(h["B"]) != 1 {
		t.Errorf("-topics B history: %v", h)
	}

	// history_days 0 turns it off.
	cfg.HistoryDays = 0
	b = &scriptBackend{research: spending(0.3)}
	_, _ = runWithin(t, context.Background(), cfg, b, 10*time.Second)
	for _, task := range b.snapshot() {
		if task.History != nil {
			t.Fatalf("history_days 0: pass has history %v", task.History)
		}
	}
}

// fullHistory is a history block at the 1,500-character cap.
func fullHistory() []Reported {
	var h []Reported
	size := 0
	for i := 0; ; i++ {
		r := Reported{Date: "2026-10-27", Headline: clip(strings.Repeat("Critical flaw in Widget server lets attackers run code ", 3), maxHistoryHeadline),
			URL: fmt.Sprintf("https://www.example-vendor.com/security/advisories/2026/widget-%d", i)}
		if len(h) == maxHistoryItems || size+len(r.line())+1 > maxHistoryChars {
			return h
		}
		size += len(r.line()) + 1
		h = append(h, r)
	}
}

// On the API, what a pass is allocated at least (need) includes the
// history it sends, and planWindow covers a first pass with a full
// history for the default topics, so Assess, which reads no journals,
// plans no search the run cannot make.
func TestHistoryIsInTheWindowAndThePlan(t *testing.T) {
	cfg := config.Default()
	cfg.Provider.Type = "anthropic"
	g := NewGenerator(cfg, &fakeBackend{})
	cal := newCalibration()
	before := g.need(cal, 0, "")
	g.history = map[string][]Reported{cfg.Topics[0].Name: fullHistory()}
	if after := g.need(cal, 0, ""); after <= before && after != passCost("anthropic") {
		t.Errorf("need %d with history, %d without", after, before)
	}
	task := g.task(0, needBudget, "")
	if task.History == nil || cal.firstSearch(task) <= cal.firstSearch(Task{Topic: task.Topic, Budget: needBudget, Searches: 1, Items: task.Items}) {
		t.Errorf("first search reserve ignores the history")
	}
	for _, tp := range cfg.Topics {
		task := Task{Topic: tp, Budget: needBudget, Searches: 1, Items: maxTopicItems, History: fullHistory()}
		if w := firstWindow(task); w > planWindow {
			t.Errorf("%s: first window with a full history %d, over planWindow %d", tp.Name, w, planWindow)
		}
	}
}

// Assess's first-pass plan still matches the API loop when every topic
// sends a full history.
func TestAssessFirstPassMatchesAPILoopWithHistory(t *testing.T) {
	for _, budget := range []int64{365_000, 1_500_000, 3_000_000} {
		cfg := testConfig("anthropic", budget, "A", "B", "C", "D", "E")
		a := Assess(cfg)
		g := NewGenerator(cfg, &fakeBackend{})
		g.history = map[string][]Reported{}
		for _, tp := range cfg.Topics {
			g.history[tp.Name] = fullHistory()
		}
		for i, tp := range a.Topics {
			if tp.Budget < passCost("anthropic") {
				continue
			}
			sim := &realistic{results: []int64{typicalResult}}
			b, _ := scriptedAPI(t, apiBackend{}, sim.handle)
			task := g.task(i, tp.Budget, "")
			if res := researchWithin(t, context.Background(), b, task, 5*time.Second); res.Searches != tp.Searches {
				t.Errorf("budget %s, %s: Assess shows %d searches, the loop with history made %d", HumanTokens(budget), tp.Name, tp.Searches, res.Searches)
			}
		}
	}
}

// History leaves the default config's baseline and plan as they were
// (claude-code: 740k, ~663k planned spend, 13 searches; the API baseline
// was 1.489M), and Assess reads no journals. The default 9 minutes allow 41
// items beside the 180-word overview (8 minutes and 90 words allowed 38).
func TestDefaultAssessUnchangedByHistory(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := config.Default()
	if cfg.HistoryDays != 3 {
		t.Fatalf("default history_days %d", cfg.HistoryDays)
	}
	ResolveBudget(&cfg)
	a := Assess(cfg)
	near := func(got, want int64) bool { return got >= want*98/100 && got <= want*102/100 }
	if a.Baseline != 740_000 || !near(a.Spend, 663_228) || a.Searches != 13 || a.Items != 41 || a.Level != LevelOK {
		t.Errorf("default Assess: baseline %d, spend %d, %d searches, %d items, %s", a.Baseline, a.Spend, a.Searches, a.Items, a.Level)
	}
	api := config.Default()
	api.Provider.Type = "anthropic"
	if b := Baseline(api); !near(b, 1_488_750) {
		t.Errorf("default API baseline %d, was 1488750", b)
	}
	saveDay(t, daysAgo(1), map[string]string{cfg.Topics[0].Name: "### Old\nx [s](https://a.example)"})
	if again := Assess(cfg); again.Spend != a.Spend || again.Searches != a.Searches {
		t.Errorf("Assess changed with a journal on disk: %d → %d", a.Spend, again.Spend)
	}
}

func TestSectionUpdates(t *testing.T) {
	s := Section{Markdown: "### Update: Widget patched\nSince Mon: x.\n\n### New thing\ny\n\n### update: Vote passed\nz"}
	if n := s.Updates(); n != 2 {
		t.Errorf("updates %d, want 2", n)
	}
}

// failingBackend fails every call, counting a failed research pass as
// spending its whole budget, as claude-code does when the CLI's output is
// unreadable.
type failingBackend struct{}

func (failingBackend) Research(_ context.Context, t Task) Result {
	return Result{Err: errors.New("claude CLI failed: boom"), Tokens: t.Budget}
}

func (failingBackend) Summarize(context.Context, string, string) Result {
	return Result{Err: errors.New("claude CLI failed: boom"), Tokens: 9_000}
}

func (failingBackend) Name() string { return "failing" }

// A -topics re-run in which every call fails keeps the earlier digests
// with their own tokens, searches, passes and allocation; what it spent is
// in its own run record, as spent on failed topics, and the budget shown is
// its own, not a sum of budgets.
func TestFailedSubsetRerunKeepsEarlierAccounting(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	earlier := &Journal{Date: Today(), TokenBudget: 740_000,
		Overview: Section{Topic: "Overview", Status: StatusDone, Markdown: "## Today's highlights\n\n- old", TokensUsed: 4_000, Allocated: 15_000},
		Sections: []*Section{
			{Topic: "A", Status: StatusDone, Markdown: "### Old A", TokensUsed: 150_000, Searches: 3, Passes: 1, Allocated: 200_000},
			{Topic: "B", Status: StatusDone, Markdown: "### Old B", TokensUsed: 160_000, Searches: 4, Passes: 2, Allocated: 260_000},
			{Topic: "C", Status: StatusDone, Markdown: "### Old C", TokensUsed: 140_000, Searches: 3, Passes: 1, Allocated: 190_000},
		}}
	if err := SaveJournal(earlier); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig("claude-code", 0, "A", "B", "C")
	if err := cfg.FilterTopics([]string{"A", "B"}); err != nil {
		t.Fatal(err)
	}
	j, _ := runWithin(t, context.Background(), cfg, failingBackend{}, 10*time.Second)
	spent, budget := j.TokensUsed(), j.TokenBudget
	if spent == 0 || spent > budget {
		t.Fatalf("failed run spent %d of %d", spent, budget)
	}
	if err := SaveJournal(j); err != nil {
		t.Fatal(err)
	}
	saved, err := LoadJournal(Today())
	if err != nil || saved == nil {
		t.Fatalf("load: %v", err)
	}
	for i, s := range saved.Sections {
		e := earlier.Sections[i]
		if s.Markdown != e.Markdown || s.TokensUsed != e.TokensUsed || s.Searches != e.Searches || s.Passes != e.Passes || s.Allocated != e.Allocated {
			t.Errorf("%s: %+v; want the earlier figures %+v", s.Topic, s, e)
		}
		if (s.Topic != "C") != strings.Contains(s.Error, "showing the earlier digest") {
			t.Errorf("%s: error %q", s.Topic, s.Error)
		}
	}
	if ov := saved.Overview; ov.TokensUsed != 4_000 || ov.Markdown != "## Today's highlights\n\n- old" {
		t.Errorf("overview %+v; want the earlier one with its tokens", ov)
	}
	if saved.TokenBudget != budget || saved.TokensUsed() != 454_000 {
		t.Errorf("budget %d (run's %d), shown tokens %d", saved.TokenBudget, budget, saved.TokensUsed())
	}
	if len(saved.Runs) != 2 {
		t.Fatalf("runs %+v", saved.Runs)
	}
	run := saved.Runs[1]
	if run.Budget != budget || run.Used != spent || run.Failed != spent || !slices.Equal(run.Topics, []string{"A", "B"}) || saved.Runs[0].Used != 454_000 {
		t.Errorf("runs %+v; want the failed run's own record of %d of %d", saved.Runs, spent, budget)
	}
	if saved.RunUsed() != spent || saved.TodayUsed() != 454_000+spent || saved.Utilization() > 100 || saved.Over() > 0 {
		t.Errorf("run %d, today %d, %.0f%%, over %d", saved.RunUsed(), saved.TodayUsed(), saved.Utilization(), saved.Over())
	}
	sum := saved.SpendSummary()
	for _, want := range []string{HumanTokens(spent) + " / " + HumanTokens(budget), "on failed topics", "today in 2 runs"} {
		if !strings.Contains(sum, want) {
			t.Errorf("summary %q lacks %q", sum, want)
		}
	}
}

// Journals saved before "budget" was renamed "allocated" still load it.
func TestSectionReadsLegacyBudget(t *testing.T) {
	var s Section
	if err := json.Unmarshal([]byte(`{"topic":"A","status":"done","budget":123}`), &s); err != nil || s.Allocated != 123 || s.Topic != "A" {
		t.Fatalf("legacy section %+v, %v", s, err)
	}
	data, _ := json.Marshal(&Section{Topic: "A", Allocated: 5})
	if !strings.Contains(string(data), `"allocated":5`) || strings.Contains(string(data), `"budget"`) {
		t.Errorf("saved as %s", data)
	}
	if err := json.Unmarshal([]byte(`{"allocated":7,"budget":3}`), &s); err != nil || s.Allocated != 7 {
		t.Errorf("allocated %d, want 7", s.Allocated)
	}
}
