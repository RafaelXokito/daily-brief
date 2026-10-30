package brief

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RafaelXokito/daily-brief/internal/config"
)

// withGeneral is cfg with the World section turned on.
func withGeneral(cfg config.Config) config.Config {
	cfg.General = true
	return cfg
}

// The World section counts as one more topic of weight 1 when the
// baseline is worked out: it always matches a config with a plain extra
// topic. Beyond the floors that grows it by exactly one topic's share;
// within them it may not (the API floor holds for 1-7 topics, and
// claude-code's step-up gives 1 topic more than 2 get).
func TestGeneralBaselineCountsWorldAsOneTopic(t *testing.T) {
	for _, tt := range []struct {
		name    string
		cfg     config.Config
		off, on int64
	}{
		{"api floor", testConfig("anthropic", 0, "A"), 1_513_750, 1_513_750},
		{"api floor, 6 topics", testConfig("anthropic", 0, topicNames(6)...), 1_513_750, 1_513_750},
		{"claude-code, 1 topic", testConfig("claude-code", 0, "A"), Baseline(testConfig("claude-code", 0, "A")), Baseline(testConfig("claude-code", 0, "A", "B"))},
	} {
		off, on := Baseline(tt.cfg), Baseline(withGeneral(tt.cfg))
		if off != tt.off || on != tt.on {
			t.Errorf("%s: baseline %d off, %d on; want %d, %d", tt.name, off, on, tt.off, tt.on)
		}
		extra := tt.cfg
		extra.Topics = append(slices.Clone(extra.Topics), config.Topic{Name: "X"})
		if b := Baseline(extra); b != on {
			t.Errorf("%s: baseline with general %d, with a plain extra topic %d", tt.name, on, b)
		}
	}
	// Beyond the floors: exactly one topic's share more.
	for _, tt := range []struct {
		name     string
		cfg      config.Config
		off, on  int64
		perTopic int64
	}{
		{"claude-code", testConfig("claude-code", 0, "A", "B", "C", "D", "E"), 5*145_000 + 15_000, 6*145_000 + 15_000, 145_000},
		{"api many topics", testConfig("anthropic", 0, topicNames(8)...), 8*212_750 + 15_000, 9*212_750 + 15_000, 212_750},
	} {
		off, on := Baseline(tt.cfg), Baseline(withGeneral(tt.cfg))
		if off != tt.off || on != tt.on || on-off != tt.perTopic {
			t.Errorf("%s: baseline %d off, %d on; want %d, %d (one topic's %d more)", tt.name, off, on, tt.off, tt.on, tt.perTopic)
		}
		extra := tt.cfg
		extra.Topics = append(slices.Clone(extra.Topics), config.Topic{Name: "X"})
		if b := Baseline(extra); b != on {
			t.Errorf("%s: baseline with general %d, with a plain extra topic %d", tt.name, on, b)
		}
		auto := withGeneral(tt.cfg)
		if !ResolveBudget(&auto) || auto.TokenBudget != on {
			t.Errorf("%s: auto budget %d, want %d", tt.name, auto.TokenBudget, on)
		}
	}
}

// Off, nothing changes: the topics are the configured ones.
func TestGeneralOffChangesNothing(t *testing.T) {
	cfg := testConfig("anthropic", 400_000, "A", "B")
	if got := cfg.EffectiveTopics(); !slices.Equal(got, cfg.Topics) {
		t.Fatalf("effective topics %v, want %v", got, cfg.Topics)
	}
	a := Assess(cfg)
	if len(a.Topics) != 2 || a.Topics[1].Name != "B" {
		t.Errorf("assessed topics %+v", a.Topics)
	}
}

// The World section counts toward max_read_minutes: the caps of n topics
// and it add up to what fits for n+1, and it shows in the budget table
// and the journal's split, which stays within the budget.
func TestGeneralCapsAndAssess(t *testing.T) {
	cfg := withGeneral(testConfig("anthropic", 2_000_000, "A", "B", "C"))
	cfg.MaxReadMinutes = 4
	caps := itemCaps(cfg)
	sum := 0
	for _, c := range caps {
		sum += c
	}
	if len(caps) != 4 || sum != config.ReadableItems(4, 4) {
		t.Errorf("caps %v sum to %d, want 4 caps summing to %d", caps, sum, config.ReadableItems(4, 4))
	}
	if totalItems(cfg) != config.ReadableItems(4, 4) {
		t.Errorf("total items %d", totalItems(cfg))
	}
	a := Assess(cfg)
	if len(a.Topics) != 4 || a.Topics[3].Name != config.WorldTopicName || a.Topics[3].Items == 0 || a.Topics[3].Share == 0 {
		t.Errorf("budget table %+v lacks the World section", a.Topics)
	}
	reserve, shares := split(cfg)
	total := reserve
	for _, s := range shares {
		total += s
	}
	if len(shares) != 4 || total > cfg.TokenBudget {
		t.Errorf("split %d + %v exceeds %d", reserve, shares, cfg.TokenBudget)
	}
	if shares[3] != shares[0] {
		t.Errorf("World's share %d, want a weight-1 topic's %d", shares[3], shares[0])
	}
}

// A run researches the World section like any topic, with the reader's
// profile and mute list, tells it to skip the other topics, and stays
// within the budget.
func TestGeneralGeneratorSchedulesWorld(t *testing.T) {
	cfg := withGeneral(testConfig("claude-code", 1_000_000, "Security", "AI"))
	cfg.Profile, cfg.Mute = "Java developer", []string{"crypto"}
	f := &fakeBackend{spend: spendFraction(0.5)}
	j := runFake(cfg, f)
	if len(j.Sections) != 3 || j.Sections[2].Topic != config.WorldTopicName {
		t.Fatalf("sections %v", sectionNames(j))
	}
	k := slices.IndexFunc(f.tasks, func(task Task) bool { return task.Topic.Name == config.WorldTopicName })
	if k < 0 {
		t.Fatalf("World never researched: %d tasks", len(f.tasks))
	}
	task := f.tasks[k]
	if task.Profile != cfg.Profile || !slices.Equal(task.Mute, cfg.Mute) || task.Topic.Weight != 1 {
		t.Errorf("World task %+v", task)
	}
	prompt := taskPrompt(task)
	for _, want := range []string{"Skip anything the reader's other topics already cover: Security, AI.", "no rumours", "Java developer", "crypto"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("World prompt lacks %q:\n%s", want, prompt)
		}
	}
	if j.TokensUsed() > cfg.TokenBudget {
		t.Errorf("spent %d of %d", j.TokensUsed(), cfg.TokenBudget)
	}
	if j.Caps[config.WorldTopicName] == 0 {
		t.Errorf("caps %v lack the World section", j.Caps)
	}
}

// -topics may pick the World section alone, or leave it out of a subset
// whose caps and baseline still count it.
func TestGeneralTopicsSubset(t *testing.T) {
	all := withGeneral(testConfig("claude-code", 0, "A", "B"))
	full := Baseline(all)

	world := all
	if err := world.FilterTopics([]string{" world & HOT topics "}); err != nil {
		t.Fatal(err)
	}
	if err := world.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(world.Topics) != 1 || world.Topics[0].Name != config.WorldTopicName || len(world.AllTopics) != 3 {
		t.Fatalf("filtered to %v of %v", world.Topics, world.AllTopics)
	}
	if !strings.Contains(world.Topics[0].Focus, "cover: A, B.") {
		t.Errorf("subset World focus %q lacks the skip-list", world.Topics[0].Focus)
	}
	if _, gotFull := SubsetBaseline(world); gotFull != full {
		t.Errorf("full baseline %d, want %d", gotFull, full)
	}
	f := &fakeBackend{spend: spendFraction(0.5)}
	j := runFake(world, f)
	if !j.Subset || len(j.Sections) != 1 || j.Sections[0].Topic != config.WorldTopicName {
		t.Errorf("subset run sections %v (subset %v)", sectionNames(j), j.Subset)
	}
	for _, task := range f.tasks {
		if task.Topic.Name != config.WorldTopicName {
			t.Errorf("subset run researched %q", task.Topic.Name)
		}
	}

	some := all
	if err := some.FilterTopics([]string{"A"}); err != nil {
		t.Fatal(err)
	}
	if err := some.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := some.EffectiveTopics(); len(got) != 1 || got[0].Name != "A" {
		t.Errorf("-topics A researches %v", got)
	}
	if caps, want := itemCaps(some), itemCaps(all)[0]; len(caps) != 1 || caps[0] != want {
		t.Errorf("subset caps %v, want [%d]", caps, want)
	}
	if a := Assess(some); len(a.Topics) != 1 || a.FullBaseline != full {
		t.Errorf("subset assessed %+v, full baseline %d want %d", a.Topics, a.FullBaseline, full)
	}
}

func sectionNames(j *Journal) []string {
	var out []string
	for _, s := range j.Sections {
		out = append(out, s.Topic)
	}
	return out
}

// A subset run's overview reads the saved journal's other sections fitted
// to the current caps, as saving fits them: a World section saved while
// general was on is not offered to the overview once it is off, nor kept.
func TestSubsetOverviewSkipsDroppedSections(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	saved := &Journal{Date: Today(), TokenBudget: 600_000,
		Overview: Section{Topic: "Overview", Status: StatusDone, Markdown: "## Today's highlights\n\n- old", TokensUsed: 5_000},
		Sections: []*Section{
			{Topic: "A", Status: StatusDone, Markdown: "### Old A", TokensUsed: 100_000},
			{Topic: "B", Status: StatusDone, Markdown: "### Old B", TokensUsed: 100_000},
			{Topic: config.WorldTopicName, Status: StatusDone, Markdown: "### Old world news", TokensUsed: 100_000},
		}}
	if err := SaveJournal(saved); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig("anthropic", 700_000, "A", "B") // general now off
	if err := cfg.FilterTopics([]string{"b"}); err != nil {
		t.Fatal(err)
	}
	b := &summaryBackend{scriptBackend: scriptBackend{research: func(context.Context, Task, int) Result {
		return Result{Markdown: "### New B", Tokens: 50_000, Searches: 1}
	}}}
	j, _ := runWithin(t, context.Background(), cfg, b, 30*time.Second)
	if !strings.Contains(b.input, "### Old A") || !strings.Contains(b.input, "### New B") {
		t.Errorf("overview input lacks the kept sections:\n%s", b.input)
	}
	if strings.Contains(b.input, "Old world news") {
		t.Errorf("overview input has a section saving drops:\n%s", b.input)
	}
	if err := SaveJournal(j); err != nil {
		t.Fatal(err)
	}
	got, _ := LoadJournal(Today())
	if names := sectionNames(got); !slices.Equal(names, []string{"A", "B"}) {
		t.Errorf("saved sections %v", names)
	}
}
