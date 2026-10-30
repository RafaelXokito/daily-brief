package brief

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rafaelpereira/daily-brief/internal/config"
)

func overviewSections() []*Section {
	return []*Section{
		{Topic: "Java & Quarkus", Status: StatusDone, Markdown: "### Quarkus 3.20 released with faster native builds\nStartup is down. [source](https://quarkus.io/blog/3-20)\n\n" +
			"### Update: Spring Boot 4 milestone slips\nSince Mon: delayed. [source](https://spring.io/blog/m3)"},
		{Topic: "Security", Status: StatusDone, Markdown: "### Critical Log4j flaw exploited in the wild\nPatch to 2.25.1 now. [source](https://logging.apache.org/security)\n\n" +
			"### npm token theft campaign\nRotate tokens. [source](https://github.blog/npm)"},
		{Topic: "AI", Status: StatusDone, Markdown: "Nothing notable today."},
		{Topic: "Tech", Status: StatusFailed, Error: "rate limited"},
	}
}

var highlightsReply = "Here you go:\n```json\n" + `{"highlights":[
 {"tier":"know","topic":"Java & Quarkus","headline":"Quarkus 3.20 released with faster native builds","text":"Quarkus 3.20 cuts native build times and startup.","why":"Faster CI and cold starts for our services."},
 {"tier":"act","topic":"security","headline":"### Critical Log4j flaw exploited","text":"Log4j RCE is exploited in the wild; patch to 2.25.1.","why":"Many of our services ship Log4j."},
 {"tier":"know","topic":"Security","headline":"A story nobody wrote","text":"Invented.","why":"Invented."},
 {"tier":"act","topic":"Gardening","headline":"npm token theft campaign","text":"Wrong topic.","why":"x"},
 {"tier":"ACT","topic":"Security","headline":"npm token theft campaign","text":"` + strings.Repeat("word ", 40) + `","why":"` + strings.Repeat("why ", 40) + `"},
 {"tier":"know","topic":"Java & Quarkus","headline":"Update: Spring Boot 4 milestone slips","text":"Spring Boot 4 M3 slips a month.","why":"Plan the upgrade later."}
]}` + "\n```\nThat's all."

func TestCapOverviewParsesAndValidatesHighlights(t *testing.T) {
	secs := overviewSections()
	md, hl := capOverview(highlightsReply, secs)
	want := []struct{ tier, topic, headline string }{
		{TierAct, "Security", "Critical Log4j flaw exploited in the wild"},
		{TierAct, "Security", "npm token theft campaign"},
		{TierKnow, "Java & Quarkus", "Quarkus 3.20 released with faster native builds"},
		{TierKnow, "Java & Quarkus", "Spring Boot 4 milestone slips"},
	}
	if len(hl) != len(want) {
		t.Fatalf("got %d highlights, want %d: %+v", len(hl), len(want), hl)
	}
	for i, w := range want {
		if hl[i].Tier != w.tier || hl[i].Topic != w.topic || hl[i].Headline != w.headline {
			t.Errorf("highlight %d: %+v, want %+v", i, hl[i], w)
		}
		if countWords(hl[i].Text) > highlightWords || countWords(hl[i].Why) > whyWords {
			t.Errorf("highlight %d not trimmed: %+v", i, hl[i])
		}
	}
	if strings.Contains(md, "Invented") || strings.Contains(md, "Wrong topic") {
		t.Errorf("unresolvable highlights kept:\n%s", md)
	}
	for _, s := range []string{"## Today's highlights", "4 items · 1 update · 3 topics · ~1 min", "**Act now**", "**Worth knowing**",
		"[source](https://logging.apache.org/security)", "Java & Quarkus — 2 items, 1 update", "AI — quiet day", "Tech — not summarized"} {
		if !strings.Contains(md, s) {
			t.Errorf("overview lacks %q:\n%s", s, md)
		}
	}
	if strings.Index(md, "Act now") > strings.Index(md, "Worth knowing") {
		t.Errorf("act is not first:\n%s", md)
	}
	if n := countWords(md); n > overviewWords {
		t.Errorf("overview is %d words, cap %d", n, overviewWords)
	}
}

// At most six highlights, act first, and the overview stays within its
// words however long the reply's texts are.
func TestCapOverviewCapsHighlightsAndWords(t *testing.T) {
	var secs []*Section
	var entries []string
	for i := range 4 {
		var md strings.Builder
		for k := range 4 {
			h := fmt.Sprintf("Story %d about topic %d here", k, i)
			fmt.Fprintf(&md, "### %s\nBody. [source](https://example.com/%d/%d)\n\n", h, i, k)
			tier := "know"
			if k == 3 {
				tier = "act"
			}
			entries = append(entries, fmt.Sprintf(`{"tier":%q,"topic":"T%d","headline":%q,"text":%q,"why":%q}`,
				tier, i, h, strings.Repeat("long ", 30), strings.Repeat("why ", 30)))
		}
		secs = append(secs, &Section{Topic: fmt.Sprintf("T%d", i), Status: StatusDone, Markdown: md.String()})
	}
	md, hl := capOverview(`{"highlights":[`+strings.Join(entries, ",")+`]}`, secs)
	if len(hl) == 0 || len(hl) > maxHighlights {
		t.Fatalf("%d highlights", len(hl))
	}
	if n := countWords(md); n > overviewWords {
		t.Errorf("overview is %d words, cap %d:\n%s", n, overviewWords, md)
	}
	for i := 1; i < len(hl); i++ {
		if hl[i-1].Tier == TierKnow && hl[i].Tier == TierAct {
			t.Errorf("act after know: %+v", hl)
		}
	}
	// With short texts all six fit.
	short := strings.NewReplacer(strings.Repeat("long ", 30), "short text", strings.Repeat("why ", 30), "short why").Replace(`{"highlights":[` + strings.Join(entries, ",") + `]}`)
	if _, hl := capOverview(short, secs); len(hl) != maxHighlights || hl[0].Tier != TierAct {
		t.Errorf("short texts: %d highlights, first %+v", len(hl), hl[0])
	}
}

// A reply that is not JSON keeps its bullets, without links, and gets no
// highlights.
func TestCapOverviewFallsBackToBullets(t *testing.T) {
	reply := "## Today's highlights\n\n- one [source](https://a.example)\n- two\n- three\n- four\n- five\n- six\n- seven"
	md, hl := capOverview(reply, overviewSections())
	if hl != nil {
		t.Errorf("fallback has highlights %+v", hl)
	}
	if !strings.Contains(md, "- one\n- two\n- three\n- four\n- five") || strings.Contains(md, "six") || strings.Contains(md, "http") {
		t.Errorf("fallback bullets:\n%s", md)
	}
	if strings.Count(md, "## Today's highlights") != 1 || !strings.Contains(md, "4 items") || !strings.Contains(md, "AI — quiet day") {
		t.Errorf("fallback lacks its heading or computed lines:\n%s", md)
	}
	long := strings.Repeat("- "+strings.Repeat("word ", 30)+"\n", 5)
	if md, _ := capOverview(long, overviewSections()); countWords(md) > overviewWords || !strings.Contains(md, "…") {
		t.Errorf("%d words, want at most %d and a cut mark", countWords(md), overviewWords)
	}
	// Invalid JSON falls back too.
	if _, hl := capOverview(`{"highlights":[{"tier":`, overviewSections()); hl != nil {
		t.Errorf("broken JSON gave highlights %+v", hl)
	}
}

// Many topics leave out the status lines rather than the highlights.
func TestCapOverviewDropsStatusLinesForManyTopics(t *testing.T) {
	secs := overviewSections()
	for i := range 30 {
		secs = append(secs, &Section{Topic: fmt.Sprintf("Topic %d", i), Status: StatusDone, Markdown: "### A\nb."})
	}
	md, hl := capOverview(highlightsReply, secs)
	if countWords(md) > overviewWords || len(hl) != 4 || strings.Contains(md, "By topic") {
		t.Errorf("%d words, %d highlights:\n%s", countWords(md), len(hl), md)
	}
}

// The generator stores the parsed highlights in the journal.
func TestRunStoresHighlights(t *testing.T) {
	cfg := testConfig("anthropic", 0, "Security")
	f := &scriptBackend{
		research: func(_ context.Context, t Task, _ int) Result {
			return Result{Markdown: "### npm token theft campaign\nRotate tokens. [source](https://github.blog/npm)", Tokens: t.Budget / 4, Searches: 1}
		},
		summarize: func(context.Context) Result {
			return Result{Markdown: `{"highlights":[{"tier":"act","topic":"Security","headline":"npm token theft campaign","text":"Rotate npm tokens.","why":"Stolen tokens publish malware."}]}`, Tokens: 2_000}
		},
	}
	j, _ := runWithin(t, context.Background(), cfg, f, 10*time.Second)
	if ov := j.Overview; ov.Status != StatusDone || len(ov.Highlights) != 1 || ov.Highlights[0].Tier != TierAct ||
		!strings.Contains(ov.Markdown, "Rotate npm tokens.") || !strings.Contains(j.Markdown(), "https://github.blog/npm") {
		t.Errorf("overview %+v", ov)
	}
}

// The default reading cap still holds with the larger overview: the items
// it allows, the overview and the Related labels read within 9 minutes.
func TestTotalItemsKeepsDefaultReadingCap(t *testing.T) {
	cfg := config.Default()
	if cfg.MaxReadMinutes != 9 {
		t.Fatalf("default max_read_minutes %d, want 9", cfg.MaxReadMinutes)
	}
	for _, n := range []int{1, 5, 12} {
		cfg.Topics = nil
		for i := range n {
			cfg.Topics = append(cfg.Topics, config.Topic{Name: fmt.Sprint(i)})
		}
		words := totalItems(cfg)*wordsPerItem + overviewWords + n*relatedAllowance
		if words > cfg.MaxReadMinutes*readingWPM {
			t.Errorf("%d topics: %d items need %d words, over %d", n, totalItems(cfg), words, cfg.MaxReadMinutes*readingWPM)
		}
		caps := 0
		for _, c := range itemCaps(cfg) {
			caps += c
		}
		if caps > totalItems(cfg) {
			t.Errorf("%d topics: caps add up to %d, over %d", n, caps, totalItems(cfg))
		}
	}
}

func TestPlainTextAndMatchHeadline(t *testing.T) {
	if got := PlainText("### Update: **Log4j** flaw\nPatch `2.25.1` now, see [advisory](https://a.example). [source](https://b.example)"); got != "Update: Log4j flaw\nPatch 2.25.1 now, see advisory." {
		t.Errorf("plain text %q", got)
	}
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"Critical Log4j flaw exploited in the wild", "critical log4j flaw exploited", true},
		{"### Update: Spring Boot 4 slips", "Spring Boot 4 slips", true},
		{"Critical Log4j flaw", "Critical", false},
		{"Go 1.26", "Go 1.26", true},
		{"Critical Log4j flaw", "Minor Log4j flaw", false},
	} {
		if got := MatchHeadline(c.a, c.b); got != c.want {
			t.Errorf("MatchHeadline(%q, %q) = %v", c.a, c.b, got)
		}
	}
	if d := Domain("https://www.TheRegister.com/x"); d != "theregister.com" {
		t.Errorf("domain %q", d)
	}
}
