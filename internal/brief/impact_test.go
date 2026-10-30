package brief

import (
	"strings"
	"testing"
)

// Tags are recognised in one form: an uppercase code span, `ACT`, `WATCH`
// or `FYI`, at the start of the line or after a heading or list marker,
// followed by whitespace or the end of the line.
func TestSplitTag(t *testing.T) {
	cases := []struct {
		line, lead, rest string
		imp              Impact
	}{
		{"`ACT` CVE-2026-1 · CVSS 9.8", "", "CVE-2026-1 · CVSS 9.8", ImpactAct},
		{"`WATCH` Rates may rise.", "", "Rates may rise.", ImpactWatch},
		{"`FYI` Worth knowing.", "", "Worth knowing.", ImpactFYI},
		{"`ACT`", "", "", ImpactAct},
		{"`ACT`\tTab", "", "Tab", ImpactAct},
		{"`ACT` — dash", "", "— dash", ImpactAct},
		{"`WATCH` `ACT` second tag", "", "`ACT` second tag", ImpactWatch},
		{"### `ACT` In a heading", "### ", "In a heading", ImpactAct},
		{"- `WATCH` In a bullet", "- ", "In a bullet", ImpactWatch},
		{"2. `FYI` Numbered", "2. ", "Numbered", ImpactFYI},
	}
	for _, c := range cases {
		lead, imp, rest, ok := SplitTag(c.line)
		if !ok || lead != c.lead || imp != c.imp || rest != c.rest {
			t.Errorf("%q: got %q %v %q %v, want %q %v %q", c.line, lead, imp, rest, ok, c.lead, c.imp, c.rest)
		}
	}
	for _, line := range []string{
		"Plain sentence about ACT.",
		"`code` span that is not a tag",
		"[ACT](https://evil.example) is a link",
		"**Widget** bold headline",
		"`URGENT` not a tag",
		"`ACT\x1b[31m` hostile",
		"`A\u202ECT` hostile",
		"",
		"ACT: bare words are not tags",
		// Variants an earlier version accepted: never tags now.
		"`act` lower, as nektos/act or React's act",
		"`Act today` spelled out",
		"`act now`: colon",
		"[ACT] bracket",
		"[INFO] bracket",
		"`[FYI]` both",
		"**ACT** bold word",
		"**`WATCH`** bold code",
		"**Action:** bold colon",
		"__fyi__ underscores",
		"`info` alias",
		"**`ACT` Bold headline**",
		"FYI, the cluster was upgraded",
		"`ACT`: colon after",
		"`ACT`, comma after",
		"`ACT`s plural",
	} {
		if _, _, _, ok := SplitTag(line); ok {
			t.Errorf("%q: recognised as a tag", line)
		}
	}
}

// An item's tag is on its headline line or its first body line; without
// one an item counts as FYI, untagged.
func TestItemImpact(t *testing.T) {
	cases := []struct {
		item   string
		imp    Impact
		tagged bool
	}{
		{"### Flaw\n`ACT` CVE-1 · CVSS 9.8\nPatch. [source](https://a.example)", ImpactAct, true},
		{"### Flaw\n\n`WATCH` Blank line first.", ImpactWatch, true},
		{"### `FYI` Flaw\nBody.", ImpactFYI, true},
		{"### Update: `ACT` Flaw\nSince Mon: patched.", ImpactAct, true},
		{"### `ACT` Update: Flaw\nSince Mon: patched.", ImpactAct, true},
		{"### **Update:** `WATCH` Flaw\nSince Mon.", ImpactWatch, true},
		{"- `ACT` Bullet item", ImpactAct, true},
		{"### `fyi` Flaw\nBody.", ImpactFYI, false},
		{"- **[ACT]** Bullet item", ImpactFYI, false},
		{"**Widget** `ACT` bold-led, tag mid-line", ImpactFYI, false},
		{"**Widget**\n`ACT` Bold-led, tag on the body line", ImpactAct, true},
		{"### Old item\nNo tag. [source](https://a.example)", ImpactFYI, false},
		{"### Item\nBody `ACT` later in the line.", ImpactFYI, false},
		{"### Item\nBody.\n`ACT` on the third line", ImpactFYI, false},
	}
	for _, c := range cases {
		if imp, tagged := itemImpact(c.item); imp != c.imp || tagged != c.tagged {
			t.Errorf("%q: got %v %v, want %v %v", c.item, imp, tagged, c.imp, c.tagged)
		}
	}
}

func headlines(md string) []string {
	var out []string
	for _, it := range (&Section{Markdown: md}).Items() {
		out = append(out, it.Headline)
	}
	return out
}

// Items are reordered ACT, WATCH, FYI (untagged as FYI), keeping the
// model's order within a tag, with Related items still after the label.
func TestRankItems(t *testing.T) {
	md := "Intro line.\n\n### F1\n`FYI` a\n\n### A1\n`ACT` b\n\n### U1\nuntagged\n\n### W1\n`WATCH` c\n\n### A2\n`ACT` d\n\n**Related**\n\n### RF\n`FYI` e\n\n### RA\n`ACT` f"
	got := rankItems(md)
	if want := "A1 A2 W1 F1 U1 RA RF"; strings.Join(headlines(got), " ") != want {
		t.Errorf("order %q, want %q:\n%s", headlines(got), want, got)
	}
	if !strings.HasPrefix(got, "Intro line.") || !strings.Contains(got, "### U1\nuntagged\n\n**Related**\n\n### RA") {
		t.Errorf("preamble or Related block moved:\n%s", got)
	}
	// Already in order, or untagged: unchanged byte for byte.
	for _, md := range []string{got, "### A\nx\n\n### B\ny", "- one\n- two\n- three"} {
		if r := rankItems(md); r != md {
			t.Errorf("in-order digest changed:\n%q\n%q", md, r)
		}
	}
	// A bullet list without blank lines stays tight.
	if r := rankItems("- `FYI` **B** x\n- `ACT` **A** y"); r != "- `ACT` **A** y\n- `FYI` **B** x" {
		t.Errorf("bullets: %q", r)
	}
}

// capItems ranks before it cuts, so FYI items are dropped first and a
// Related block stays last.
func TestCapItemsTrimsFYIFirst(t *testing.T) {
	md := "### F1\n`FYI` one.\n\n### W1\n`WATCH` two.\n\n### A1\n`ACT` three.\n\n**Related**\n\n### R1\n`ACT` four."
	got := capItems(md, 2)
	if h := strings.Join(headlines(got), " "); h != "A1 W1" {
		t.Errorf("kept %q:\n%s", h, got)
	}
	got = capItems(md, 4)
	if h := strings.Join(headlines(got), " "); h != "A1 W1 F1 R1" || !strings.Contains(got, "**Related**\n\n### R1") {
		t.Errorf("kept %q:\n%s", h, got)
	}
}

// A typical security item, with its tag and facts line, fits the ~45-word
// item cap whole: nothing is cut or marked.
func TestSecurityFactsSurviveTheWordCap(t *testing.T) {
	item := "### Widget server flaw exploited in the wild\n" +
		"`ACT` CVE-2026-1234 · CVSS 9.8 · KEV due Nov 10 · fixed in 3.2.1\n" +
		"Unauthenticated attackers run code on exposed servers; affects your Quarkus 3.x apps behind it. Upgrade now. [source](https://widget.example/advisory)"
	if n := countWords(item); n > wordsPerItem {
		t.Fatalf("typical item is %d words, over %d", n, wordsPerItem)
	}
	md := item + "\n\n### Other\n`FYI` Filler words here."
	got := capItems(md, 1)
	if got != item {
		t.Errorf("capped:\n%s", got)
	}
	if it := (&Section{Markdown: got}).Items()[0]; it.Headline != "Widget server flaw exploited in the wild" || it.Impact != ImpactAct || !it.Tagged {
		t.Errorf("item %+v", it)
	}
}

// History headlines never include a tag, wherever the model put it, and
// journals written before tags still give their headlines.
func TestHistoryIgnoresTags(t *testing.T) {
	// Its own data dir, as every other test that saves journals: without
	// it, this journal stays in the package's shared dir for later tests
	// (and later -count iterations) to read as history.
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	day := daysAgo(1)
	saveDay(t, day, map[string]string{"Tags": "### `ACT` Tagged heading\nx\n\n### Body tag\n`WATCH` CVE-1 · CVSS 7\ny\n\n- `FYI` Bullet tag\n\n### Update: `ACT` Update tag\nSince Mon.",
		"Old": "### Plain old item\nNo tags. [source](https://old.example/a)"})
	h := LoadHistory(Today(), 1, []string{"Tags", "Old"})
	var got []string
	for _, r := range h["Tags"] {
		got = append(got, r.Headline)
	}
	if strings.Join(got, "|") != "Tagged heading|Body tag|Update tag" {
		t.Errorf("headlines %q", got)
	}
	if len(h["Old"]) != 1 || h["Old"][0].Headline != "Plain old item" || h["Old"][0].URL != "https://old.example/a" {
		t.Errorf("old journal: %+v", h["Old"])
	}
	// Other item formats, checked directly.
	for item, want := range map[string]string{
		"- `FYI` Bullet tag":                "Bullet tag",
		"### Update: `ACT` Update tag\nx":   "Update tag",
		"### `ACT` Update: Tag first\nx":    "Tag first",
		"**Update:** `WATCH` Bold update":   "Bold update",
		"**Update: `FYI` Bold whole**\nx":   "Bold whole",
		"### `FYI` Tagged heading\n`ACT` x": "Tagged heading",
	} {
		if h := itemHeadline(item); h != want {
			t.Errorf("%q: headline %q, want %q", item, h, want)
		}
	}
}

// A tagged item's highlight is "act" exactly when the item is tagged ACT;
// an untagged item keeps the model's tier.
func TestHighlightTierFollowsTags(t *testing.T) {
	sec := &Section{Topic: "Security", Status: StatusDone, Markdown: "### Patch Widget\n`ACT` CVE-1\nPatch.\n\n### Watch Gadget\n`WATCH` Soon.\n\n### Fyi thing\n`FYI` Meh.\n\n### Untagged thing\nOld style."}
	reply := `{"highlights":[` +
		`{"tier":"know","topic":"Security","headline":"Patch Widget","text":"` + "`ACT`" + ` Patch it"},` +
		`{"tier":"act","topic":"Security","headline":"Watch Gadget","text":"w"},` +
		`{"tier":"act","topic":"Security","headline":"Fyi thing","text":"f"},` +
		`{"tier":"act","topic":"Security","headline":"Untagged thing","text":"u"}]}`
	_, hl := capOverview(reply, []*Section{sec})
	tiers := map[string]string{}
	for _, h := range hl {
		tiers[h.Headline] = h.Tier
	}
	want := map[string]string{"Patch Widget": TierAct, "Watch Gadget": TierKnow, "Fyi thing": TierKnow, "Untagged thing": TierAct}
	for k, v := range want {
		if tiers[k] != v {
			t.Errorf("%s: tier %q, want %q", k, tiers[k], v)
		}
	}
	if hl[0].Headline != "Patch Widget" || hl[0].Text != "Patch it" {
		t.Errorf("first highlight %+v", hl[0])
	}
}

// The numbers line counts the items tagged ACT, and leaves the count out
// for journals without tags.
func TestNumbersLineCountsActItems(t *testing.T) {
	tagged := []*Section{
		{Topic: "A", Status: StatusDone, Markdown: "### One\n`ACT` x\n\n### Two\n`FYI` y\n\n### Three\nuntagged"},
		{Topic: "B", Status: StatusDone, Markdown: "### Four\n`ACT` z"},
		{Topic: "C", Status: StatusFailed, Markdown: "### Five\n`ACT` not counted"},
	}
	if got, want := NumbersLine(tagged, 3), "4 items · 2 to act · 0 updates · 2 topics · ~3 min"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	quiet := []*Section{{Topic: "A", Status: StatusDone, Markdown: "### One\n`FYI` x"}}
	if got, want := NumbersLine(quiet, 1), "1 item · 0 to act · 0 updates · 1 topic · ~1 min"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	old := []*Section{{Topic: "A", Status: StatusDone, Markdown: "### One\nx"}}
	if got, want := NumbersLine(old, 1), "1 item · 0 updates · 1 topic · ~1 min"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	md, _ := capOverview(`{"highlights":[]}`, tagged)
	if !strings.Contains(md, "4 items · 2 to act ·") {
		t.Errorf("overview lacks the act count:\n%s", md)
	}
	if n := tagged[0].ActCount(); n != 1 {
		t.Errorf("ActCount %d", n)
	}
}

// A tag before "Update:" or after it marks an update all the same: it
// counts toward ↻ and history drops both from the headline. A body line
// starting "`ACT` Update:" is not an item.
func TestTagAroundUpdate(t *testing.T) {
	sec := &Section{Markdown: "### `ACT` Update: Foo\nSince Mon.\n\n### Update: `WATCH` Bar\nx\n`ACT` Update: not an item"}
	if n := sec.Updates(); n != 2 {
		t.Errorf("Updates %d, want 2", n)
	}
	for line, want := range map[string]string{"### `ACT` Update: Foo": "`ACT` Foo", "### Update: `WATCH` Bar": "`WATCH` Bar"} {
		if lead, h, ok := SplitUpdate(line); !ok || lead != "### " || h != want {
			t.Errorf("%q: %q %q %v, want %q", line, lead, h, ok, want)
		}
	}
	if h := strings.Join(headlines(sec.Markdown), "|"); h != "Foo|Bar" {
		t.Errorf("headlines %q", h)
	}
	if _, update := itemLine("### `ACT` Update: Foo"); !update {
		t.Error("not an update")
	}
}
