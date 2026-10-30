package brief

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// ------------------------------------------------------ tag parsing

// Prose, code and links that only look like tags are not tags; tags carrying
// escapes are refused; a tag past the first body line is ignored; of two
// tags the first wins.
func TestAdvTagFalsePositives(t *testing.T) {
	cases := []struct {
		name, item string
		imp        Impact
		tagged     bool
	}{
		{"fyi-prose", "### Cluster\nFYI, the cluster was upgraded overnight.", ImpactFYI, false},
		{"act-prose", "### Cluster\nAct now, vendors say, before Friday.", ImpactFYI, false},
		{"watch-prose", "### Cluster\nWatch: the vendor's next release.", ImpactFYI, false},
		{"mid-sentence", "### Cluster\nTeams should `ACT` today on this.", ImpactFYI, false},
		{"mid-bracket", "### Cluster\nTeams should [ACT] today on this.", ImpactFYI, false},
		{"third-line", "### Cluster\nBody line.\n`ACT` on the third line", ImpactFYI, false},
		{"fenced-act", "### React 19\n```js\nact(() => render(<App />))\n```", ImpactFYI, false},
		{"fenced-act-tick", "### React 19\n```\n`act` x\n```", ImpactFYI, false},
		{"call-act", "### React 19\n`act()` now warns outside tests.", ImpactFYI, false},
		{"link", "### Cluster\n[ACT](https://evil.example/x) more.", ImpactFYI, false},
		{"code-link", "### Cluster\n[`ACT`](https://evil.example/x) more.", ImpactFYI, false},
		{"bold-link", "### Cluster\n**[ACT](https://evil.example/x)** more.", ImpactFYI, false},
		{"spaced-link", "### Cluster\n[ACT] (https://evil.example/x) more.", ImpactFYI, false},
		{"esc-in", "### X\n`ACT\x1b[31m` hostile", ImpactFYI, false},
		{"esc-before", "### X\n`\x1b[31mACT` hostile", ImpactFYI, false},
		{"nul-in", "### X\n`AC\x00T` hostile", ImpactFYI, false},
		{"bidi", "### X\n`‮TCA` hostile", ImpactFYI, false},
		{"zwsp", "### X\n`​ACT` hostile", ImpactFYI, false},
		{"fullwidth", "### X\n`ＡＣＴ` hostile", ImpactFYI, false},
		{"cyrillic", "### X\n`АСТ` lookalike", ImpactFYI, false},
		{"two-tags", "### X\n`WATCH` `ACT` two tags", ImpactWatch, true},
		{"two-tags-heading", "### `FYI` X\n`ACT` body tag", ImpactFYI, true},
		{"tag-after-blank", "### X\n\n\n`ACT` after blanks", ImpactAct, true},
		// Only the canonical form is a tag.
		{"lower-act", "### nektos/act 0.3\n`act` now runs reusable workflows.", ImpactFYI, false},
		{"bold-action", "### Cluster\n**Action:** upgrade before Friday.", ImpactFYI, false},
		{"bracket-info", "### Cluster\n[INFO] the cluster was upgraded.", ImpactFYI, false},
		{"bracket-act", "### Cluster\n[ACT] upgrade now.", ImpactFYI, false},
		{"fyi-comma", "### Cluster\nFYI, the cluster was upgraded.", ImpactFYI, false},
		{"colon", "### Cluster\n`ACT`: upgrade now.", ImpactFYI, false},
		{"bold-heading-tag", "### **`ACT`** Cluster\nx", ImpactFYI, false},
		{"tag-in-fence", "### Cluster\n```\n`ACT` x\n```", ImpactFYI, false},
	}
	for _, c := range cases {
		if imp, tagged := itemImpact(c.item); imp != c.imp || tagged != c.tagged {
			t.Errorf("%s: %q: got %v %v, want %v %v", c.name, c.item, imp, tagged, c.imp, c.tagged)
		}
	}
}

// A code span that is just a tag's name in lower case, as React's `act`
// or the nektos/act tool, at the start of an item's first body line is
// not a tag: only `ACT`, `WATCH` and `FYI`, upper case, are.
func TestAdvBareCodeSpanActIsNotATag(t *testing.T) {
	if imp, tagged := itemImpact("### React 19 testing\n`act` now warns outside a test environment."); imp != ImpactFYI || tagged {
		t.Errorf("got %v %v", imp, tagged)
	}
}

// Headlines lose their tag wherever it is, for history and for matching
// highlights; a tag the model put in the overview's headline still matches.
func TestAdvHeadlineWithoutTag(t *testing.T) {
	for item, want := range map[string]string{
		"### `ACT` Patch Widget\nx":         "Patch Widget",
		"1. `FYI` Numbered\nx":              "Numbered",
		"### Update: `ACT` Widget again\nx": "Widget again",
		"### `ACT` Update: Widget again\nx": "Widget again",
		"### `WATCH` `ACT` Two tags\nx":     "`ACT` Two tags",
		"### [WATCH] Rates\nx":              "[WATCH] Rates",
		"### FYI, plain headline\nx":        "FYI, plain headline",
		"### [ACT](https://e.example) x\nx": "ACT x",
	} {
		if got := itemHeadline(item); got != want {
			t.Errorf("%q: headline %q, want %q", item, got, want)
		}
	}
	items := (&Section{Markdown: "### `ACT` Patch Widget\n`ACT` CVE-1\nx\n\n### Other\ny"}).Items()
	for _, h := range []string{"Patch Widget", "`ACT` Patch Widget", "### `ACT` Patch Widget"} {
		if k := ItemIndex(items, h); k != 0 {
			t.Errorf("highlight headline %q matched item %d", h, k)
		}
	}
}

// --------------------------------------------------------- rankItems

// lineBag is a digest's non-blank lines, trimmed, sorted: what ranking
// must preserve.
func lineBag(md string) []string {
	var out []string
	for _, l := range strings.Split(md, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	slices.Sort(out)
	return out
}

// afterRelated is the headlines after the digest's Related label.
func afterRelated(md string) []string {
	i := slices.IndexFunc(strings.Split(md, "\n"), isRelated)
	if i < 0 {
		return nil
	}
	return headlines(strings.Join(strings.Split(md, "\n")[i+1:], "\n"))
}

var trickyDigests = map[string]string{
	"preamble":           "Quiet week; one notable item.\n\nAnother preamble line.\n\n### F\n`FYI` f\n\n### A\n`ACT` a",
	"related-tagged":     "### F\n`FYI` f\n\n### W\n`WATCH` w\n\n**Related**\n\n### RF\n`FYI` rf\n\n### RA\n`ACT` ra\n\n### RW\n`WATCH` rw",
	"related-only":       "**Related**\n\n### RF\n`FYI` rf\n\n### RA\n`ACT` ra",
	"updates":            "### Update: Widget breach grows\nSince Mon: more.\n\n### **Update:** `ACT` Widget patch\n`ACT` x\n\n### Update: Gadget\n`WATCH` y\n\n### `FYI` Update: Tag first\nz\n\n### New\n`FYI` z",
	"dup-headlines":      "### Same\n`FYI` one\n\n### Same\n`ACT` two\n\n### Same\n`WATCH` three\n\n### Same\n`ACT` four",
	"bullets-mixed":      "- `FYI` a\n- `ACT` b\n\n- `WATCH` c\n- untagged d",
	"bold-items":         "**Widget** `FYI` a\n\n**Gadget** x\n`ACT` b\n\n**Related**\n\n**Other** `WATCH` c",
	"numbered":           "1. `FYI` one\n2. `ACT` two\n3. `WATCH` three",
	"fence-in-item":      "### F\n`FYI` f\n```\ncode line\n[ACT] in code\n```\n\n### A\n`ACT` a",
	"fence-with-heading": "### A\n`FYI` a\n```md\n### Inside fence\nact\n```\n\n### B\n`ACT` b",
	"tilde-fence":        "### A\n`FYI` x\n~~~\n### B\n`ACT` in fence\n~~~\n\n### C\n`ACT` c",
	"bold-tag-lines":     "**Widget**\n`FYI` a\n\n**Gadget**\n`ACT` b",
	"fence-spans-items":  "### A\n`FYI` x\n```go\nfunc f() {}\n\n### B\n`ACT` in fence\n```\n\n### C\n`WATCH` c",
	"crlf":               "### F\r\n`FYI` f\r\n\r\n### A\r\n`ACT` a",
	"trailing-spaces":    "### F  \n`FYI` f   \n\n### A\n`ACT` a  ",
	"many":               strings.Repeat("### F\n`FYI` f\n\n### W\n`WATCH` w\n\n### A\n`ACT` a\n\n", 10),
}

// Ranking never loses, duplicates or alters a non-blank line, keeps the
// preamble first and Related items after the label, puts ACT before WATCH
// before FYI within each block, and is idempotent.
func TestAdvRankItemsKeepsContent(t *testing.T) {
	for name, md := range trickyDigests {
		got := rankItems(md)
		if !slices.Equal(lineBag(md), lineBag(got)) {
			t.Errorf("%s: lines changed:\n%s\n--- got ---\n%s", name, md, got)
		}
		if again := rankItems(got); again != got {
			t.Errorf("%s: not idempotent:\n%s\n--- again ---\n%s", name, got, again)
		}
		if first := strings.SplitN(strings.TrimSpace(md), "\n", 2)[0]; !strings.HasPrefix(md, "#") && !isBullet(first) && !strings.HasPrefix(first, "**") && !strings.HasPrefix(got, first) {
			t.Errorf("%s: preamble moved:\n%s", name, got)
		}
		in, out := afterRelated(md), afterRelated(got)
		slices.Sort(in)
		slices.Sort(out)
		if !slices.Equal(in, out) {
			t.Errorf("%s: Related block changed from %q to %q", name, in, out)
		}
		// Within each run of items, impact never rises.
		for _, b := range splitBlocks(got) {
			prev := ImpactAct
			for _, it := range b {
				imp, _ := itemImpact(it)
				if imp > prev {
					t.Errorf("%s: %q ranks after a lower item:\n%s", name, firstLine(it), got)
				}
				prev = imp
			}
		}
	}
	// Duplicate headlines keep their own bodies, in tag then model order.
	got := rankItems(trickyDigests["dup-headlines"])
	var bodies []string
	for _, it := range (&Section{Markdown: got}).Items() {
		bodies = append(bodies, strings.TrimSpace(strings.SplitN(it.Markdown, "\n", 2)[1]))
	}
	if want := "`ACT` two|`ACT` four|`WATCH` three|`FYI` one"; strings.Join(bodies, "|") != want {
		t.Errorf("duplicates in order %q, want %q", strings.Join(bodies, "|"), want)
	}
	// Updates rank by their tag like any other item.
	if h := strings.Join(headlines(rankItems(trickyDigests["updates"])), "|"); h != "Widget patch|Gadget|Widget breach grows|Tag first|New" {
		t.Errorf("updates ranked %q", h)
	}
}

// splitBlocks is a digest's item texts, in runs split at a Related label.
func splitBlocks(md string) [][]string {
	var out [][]string
	var cur []string
	for _, b := range splitDigest(md) {
		switch {
		case b.label:
			out, cur = append(out, cur), nil
		case b.item:
			cur = append(cur, b.text)
		}
	}
	return append(out, cur)
}

func firstLine(s string) string { l, _, _ := strings.Cut(s, "\n"); return l }

// fenced reports, for each line of md, whether it is inside a fenced code
// block (fence lines included).
func fenced(md string) map[string]bool {
	in := false
	out := map[string]bool{}
	for _, l := range strings.Split(md, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			out[l] = true
			in = !in
			continue
		}
		out[l] = in || out[l]
	}
	return out
}

// A "### " line inside a fenced code block does not start an item, so
// ranking moves the whole fence with the item that opened it, and real
// headings never end up inside a code block.
func TestAdvRankItemsKeepsCodeFenceSpanningItems(t *testing.T) {
	md := trickyDigests["fence-spans-items"]
	got := rankItems(md)
	if !strings.Contains(got, "```go\nfunc f() {}\n\n### B\n`ACT` in fence\n```") {
		t.Errorf("the code block was split:\n%s", got)
	}
	if f := fenced(got); f["### C"] || f["### A"] {
		t.Errorf("real headings ended up inside a code block:\n%s", got)
	}
	if h := strings.Join(headlines(got), "|"); h != "C|A" || countItems(md) != 2 {
		t.Errorf("items %q, want C|A", h)
	}
	// capItems, which splits the same way, keeps the fence whole too.
	if got := capItems(md, 1); got != "### C\n`WATCH` c" {
		t.Errorf("capped:\n%s", got)
	}
	// Tilde fences too.
	if got := rankItems(trickyDigests["tilde-fence"]); !strings.HasPrefix(got, "### C\n`ACT` c\n\n### A\n`FYI` x\n~~~\n### B\n`ACT` in fence\n~~~") {
		t.Errorf("tilde fence:\n%s", got)
	}
	// A fence left open at the end would swallow what ranking moved after
	// it: such a digest is left as it is.
	if md := "### A\n`FYI` x\n\n### C\n`WATCH` c\n```\n### D\n`ACT` in fence"; rankItems(md) != md {
		t.Errorf("open fence reordered:\n%s", rankItems(md))
	}
}

// Ranking never puts a separator inside a code block, even one holding a
// "### " line.
func TestAdvRankItemsKeepsCodeBlockContents(t *testing.T) {
	got := rankItems(trickyDigests["fence-with-heading"])
	if !strings.Contains(got, "```md\n### Inside fence\nact\n```") {
		t.Errorf("code block contents changed:\n%s", got)
	}
}

// In a bold-headline digest, a tag line is the body of the bold line
// before it: ranking moves headline and body together. Ranking leaves a
// digest alone if any item's headline is not recognised.
func TestAdvRankItemsBoldHeadlines(t *testing.T) {
	if got, want := rankItems(trickyDigests["bold-tag-lines"]), "**Gadget**\n`ACT` b\n\n**Widget**\n`FYI` a"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if md := "### \n`FYI` a\n\n### B\n`ACT` b"; rankItems(md) != md {
		t.Errorf("digest with an empty headline reordered:\n%s", rankItems(md))
	}
}

// ---------------------------------------------------------- capItems

// mixedDigest is n items cycling FYI, WATCH, untagged, ACT, each about
// words words long.
func mixedDigest(n, words int, related bool) string {
	tags := []string{"`FYI` ", "`WATCH` ", "", "`ACT` "}
	var b strings.Builder
	for i := range n {
		if related && i == n-2 {
			b.WriteString("**Related**\n\n")
		}
		fmt.Fprintf(&b, "### Item %d\n%s%s[source](https://s.example/%d)\n\n", i, tags[i%4], strings.Repeat("word ", words), i)
	}
	return b.String()
}

// After ranking, capItems still holds the item and word caps, and drops an
// item only once every lower-ranked main item is gone.
func TestAdvCapItemsAfterRanking(t *testing.T) {
	for _, related := range []bool{false, true} {
		for _, words := range []int{5, 30, 44, 80} {
			for _, total := range []int{1, 3, 8, 13} {
				md := mixedDigest(total, words, related)
				for n := 1; n <= maxTopicItems+1; n++ {
					got := capItems(md, n)
					label := fmt.Sprintf("related %v, %d items of %d words, cap %d", related, total, words, n)
					if c := countItems(got); c > n || c == 0 {
						t.Errorf("%s: %d items", label, c)
					}
					if w := countWords(got); w > n*wordsPerItem+1 {
						t.Errorf("%s: %d words, over %d", label, w, n*wordsPerItem+1)
					}
					kept := map[string]bool{}
					for _, h := range headlines(got) {
						kept[h] = true
					}
					// Main items (before any Related label) drop lowest first.
					var main []Item
					for _, it := range (&Section{Markdown: md}).Items() {
						if related && slices.Contains(afterRelated(md), it.Headline) {
							continue
						}
						main = append(main, it)
					}
					for _, a := range main {
						for _, b := range main {
							if a.Impact > b.Impact && kept[b.Headline] && !kept[a.Headline] && n >= 2 {
								t.Errorf("%s: kept %s (%v) but dropped %s (%v):\n%s", label, b.Headline, b.Impact, a.Headline, a.Impact, got)
							}
						}
					}
				}
			}
		}
	}
}

// ---------------------------------------------------- overview tiers

// With an older journal's untagged sections merged in, untagged items keep
// the model's tier, tagged ones follow their tag, and the numbers line
// counts only tagged ACT items.
func TestAdvOverviewTiersWithMergedUntagged(t *testing.T) {
	own := []*Section{{Topic: "Security", Status: StatusDone, Markdown: "### Patch Widget\n`ACT` CVE-1\nx\n\n### Gadget\n`FYI` y"}}
	old := []*Section{
		{Topic: "Finance", Status: StatusDone, Markdown: "### Rates rise\nOld untagged.\n\n### Deadline Friday\nOld untagged too."},
		{Topic: "Security", Status: StatusDone, Markdown: "### Stale\nreplaced"},
	}
	secs := combine(own, old)
	reply := `{"highlights":[` +
		`{"tier":"know","topic":"Security","headline":"Patch Widget","text":"p","why":"w"},` +
		`{"tier":"act","topic":"Security","headline":"Gadget","text":"g","why":"w"},` +
		`{"tier":"act","topic":"Finance","headline":"Deadline Friday","text":"d","why":"w"},` +
		`{"tier":"know","topic":"Finance","headline":"Rates rise","text":"r","why":"w"},` +
		`{"tier":"act","topic":"Security","headline":"Stale","text":"s","why":"w"}]}`
	md, hl := capOverview(reply, secs)
	tiers := map[string]string{}
	for _, h := range hl {
		tiers[h.Headline] = h.Tier
	}
	want := map[string]string{"Patch Widget": TierAct, "Gadget": TierKnow, "Deadline Friday": TierAct, "Rates rise": TierKnow}
	if len(tiers) != len(want) {
		t.Errorf("highlights %v, want %v", tiers, want)
	}
	for k, v := range want {
		if tiers[k] != v {
			t.Errorf("%s: tier %q, want %q", k, tiers[k], v)
		}
	}
	if !strings.Contains(md, "4 items · 1 to act ·") {
		t.Errorf("numbers line:\n%s", md)
	}
	// "act" highlights come first.
	for i := 1; i < len(hl); i++ {
		if hl[i].Tier == TierAct && hl[i-1].Tier != TierAct {
			t.Errorf("act highlight after know: %+v", hl)
		}
	}
}

// Highlights naming items capItems trimmed are dropped; the rest keep the
// tier of their tag.
func TestAdvOverviewHighlightOfTrimmedItem(t *testing.T) {
	md := "### Fyi one\n`FYI` a\n\n### Act one\n`ACT` b\n\n### Watch one\n`WATCH` c\n\n### Fyi two\n`FYI` d"
	sec := &Section{Topic: "T", Status: StatusDone, Markdown: capItems(md, 2)}
	if h := strings.Join(headlines(sec.Markdown), "|"); h != "Act one|Watch one" {
		t.Fatalf("capped to %q", h)
	}
	reply := `{"highlights":[` +
		`{"tier":"act","topic":"T","headline":"Fyi one","text":"x"},` +
		`{"tier":"know","topic":"T","headline":"Act one","text":"y"},` +
		`{"tier":"act","topic":"T","headline":"Fyi two","text":"z"},` +
		`{"tier":"act","topic":"T","headline":"Watch one","text":"w"}]}`
	_, hl := capOverview(reply, []*Section{sec})
	var got []string
	for _, h := range hl {
		got = append(got, h.Headline+"="+h.Tier)
	}
	if want := "Act one=act|Watch one=know"; strings.Join(got, "|") != want {
		t.Errorf("highlights %q, want %q", strings.Join(got, "|"), want)
	}
}
