package brief

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// The reading cap holds whatever format a digest comes in: headings of
// either level, bullets, bold-led lines, prose without items, or a single
// item far too long.
func TestCapItemsHoldsWordBudgetInAnyFormat(t *testing.T) {
	var bullets, h2, bold, numbered strings.Builder
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&bullets, "- **Headline %d**: something happened that matters a lot today [source](https://x%d.example)\n", i, i)
		fmt.Fprintf(&h2, "## Headline %d\nSomething happened that matters a lot today [source](https://x%d.example)\n\n", i, i)
		fmt.Fprintf(&bold, "**Headline %d**\nSomething happened that matters a lot today [source](https://x%d.example)\n\n", i, i)
		fmt.Fprintf(&numbered, "%d. Headline: something happened that matters a lot today [source](https://x%d.example)\n", i+1, i)
	}
	prose := strings.Repeat("Something big happened in the ecosystem today and it matters a lot. ", 60)
	cases := map[string]struct {
		md    string
		items int // items expected at cap 4; -1 for prose
	}{
		"bullets":                  {bullets.String(), 4},
		"h2":                       {h2.String(), 4},
		"bold":                     {bold.String(), 4},
		"numbered":                 {numbered.String(), 4},
		"prose":                    {prose, -1},
		"one long item":            {"### One item\n" + strings.Repeat("word ", 2000), 1},
		"2000-word item then more": {"### One item\n" + strings.Repeat("Long sentence here. ", 500) + "\n\n### Two\nx.", 1},
	}
	for name, c := range cases {
		out := capItems(c.md, 4)
		if w := countWords(out); w > 4*wordsPerItem {
			t.Errorf("%s: capItems(…, 4) kept %d words (cap %d), %.1f min", name, w, 4*wordsPerItem, readMinutes(out))
		}
		if c.items >= 0 && countItems(out) != c.items {
			t.Errorf("%s: kept %d items, want %d:\n%s", name, countItems(out), c.items, out)
		}
		if strings.TrimSpace(out) == "" {
			t.Errorf("%s: nothing kept", name)
		}
	}
	// A cut is marked, and made at a sentence end when one keeps enough.
	if out := capItems(prose, 1); !strings.HasSuffix(out, "a lot. …") {
		t.Errorf("prose cut %q, want a sentence end and a mark", out)
	}
	if out := capItems("### One item\n"+strings.Repeat("word ", 2000), 1); !strings.HasSuffix(out, "word …") || countWords(out) != wordsPerItem {
		t.Errorf("long item cut to %d words, ending %q", countWords(out), out[len(out)-12:])
	}
}

// A digest is never emptied by the cap: a heading alone longer than the
// whole budget, or an item whose cut would split a link or open a code
// block, keeps its heading, cut and marked.
func TestCapItemsNeverEmptiesADigest(t *testing.T) {
	longHeading := "### " + strings.Repeat("headline ", 3*wordsPerItem)
	for name, md := range map[string]string{
		"heading longer than the budget":           longHeading + "\nBody [source](https://x.example).",
		"heading alone":                            longHeading,
		"heading longer than the budget, as items": longHeading + "\nBody.\n\n### Two\nMore.",
		"bold heading":                             "**" + strings.Repeat("headline ", 3*wordsPerItem) + "**\nBody.",
		"item that is one long link":               "### Short\n[" + strings.Repeat("word ", 3*wordsPerItem) + "](https://x.example)",
		"item that is one long code block":         "### Short\n```\n" + strings.Repeat("code ", 3*wordsPerItem) + "\n```",
	} {
		for _, n := range []int{1, 2} {
			out := capItems(md, n)
			if strings.TrimSpace(out) == "" {
				t.Errorf("%s, cap %d: emptied", name, n)
				continue
			}
			if w := countWords(out); w > n*wordsPerItem {
				t.Errorf("%s, cap %d: kept %d words, over %d", name, n, w, n*wordsPerItem)
			}
			if !strings.HasSuffix(out, "…") {
				t.Errorf("%s, cap %d: cut not marked: %q", name, n, out)
			}
		}
	}
}

// Whole items are kept, and a preamble gets little room beside them.
func TestCapItemsKeepsWholeItemsAndShortensPreamble(t *testing.T) {
	md := strings.Repeat("Preamble sentence goes on. ", 30) + "\n\n### A\nFirst item.\n\n### B\nSecond item."
	out := capItems(md, 2)
	if !strings.Contains(out, "### A\nFirst item.\n\n### B\nSecond item.") || !strings.Contains(out, "on. …") {
		t.Errorf("preamble not shortened or items not whole:\n%s", out)
	}
	if countWords(out) > 2*wordsPerItem {
		t.Errorf("%d words over %d", countWords(out), 2*wordsPerItem)
	}
}

// A Related label left with no items after it is dropped, however spelled.
func TestCapItemsDropsDanglingRelatedLabel(t *testing.T) {
	for _, label := range []string{"**Related**", "**Related:**", "### Related", "## Related", "Related:", "**Related**:", "_Related_"} {
		md := "### A\na.\n\n### B\nb.\n\n" + label + "\n\n### C\nc."
		if got := capItems(md, 2); got != "### A\na.\n\n### B\nb." {
			t.Errorf("%q: %q", label, got)
		}
		if got := capItems(md, 3); !strings.Contains(got, label) || countItems(got) != 3 {
			t.Errorf("%q: label lost before a kept item: %q", label, got)
		}
		if got := capItems("### A\na.\n\n"+label, 3); got != "### A\na." {
			t.Errorf("%q at the end: %q", label, got)
		}
	}
	bullets := "- **A**: one\n- **B**: two\n\nRelated:\n- **C**: three"
	if got := capItems(bullets, 2); got != "- **A**: one\n- **B**: two" {
		t.Errorf("bullets: %q", got)
	}
}

func TestPruneSourcesKeepsNoMoreThanItems(t *testing.T) {
	src := []Source{{URL: "https://a"}, {URL: "https://b"}, {URL: "https://c"}}
	if got := pruneSources("### A\nx\n\n### B\ny", src); len(got) != 2 {
		t.Errorf("two items kept %d sources", len(got))
	}
	if got := pruneSources("Nothing notable today.", src); len(got) != 1 {
		t.Errorf("prose kept %d sources", len(got))
	}
}

func TestCapOverviewLimitsBulletsAndWords(t *testing.T) {
	md := "## Today's highlights\n\n- one\n- two\n- three\n- four\n- five\n- six\n- seven\n\nThat's all."
	if got := capOverview(md); got != "## Today's highlights\n\n- one\n- two\n- three\n- four\n- five" {
		t.Errorf("bullets: %q", got)
	}
	long := "## Today's highlights\n\n" + strings.Repeat("- "+strings.Repeat("word ", 30)+"\n", 5)
	if got := capOverview(long); countWords(got) > overviewWords || !strings.HasSuffix(got, "…") {
		t.Errorf("%d words, want at most %d and a cut mark", countWords(got), overviewWords)
	}
}

// A pass's digest is capped, Sources included, before it is shown.
func TestPassCapsDigestAndSources(t *testing.T) {
	cfg := testConfig("anthropic", 0, "A")
	cfg.MaxReadMinutes = 1 // 2 items
	var md strings.Builder
	var src []Source
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&md, "## Item %d\nSomething happened.\n\n", i)
		src = append(src, Source{URL: fmt.Sprintf("https://%d.example", i)})
	}
	g := NewGenerator(cfg, &scriptBackend{research: func(context.Context, Task, int) Result {
		return Result{Markdown: md.String(), Sources: src, Tokens: 1_000, Searches: 1}
	}})
	sec := &Section{}
	g.pass(context.Background(), (&pool{total: 1_000_000}).hold(0), 0, sec, Task{})
	if countItems(sec.Markdown) != g.caps[0] || len(sec.Sources) != g.caps[0] {
		t.Errorf("cap %d: kept %d items and %d sources", g.caps[0], countItems(sec.Markdown), len(sec.Sources))
	}
}

// A cut never splits a link, leaves a code fence open or ends on a heading
// with no body.
func TestTrimWordsKeepsMarkdownWhole(t *testing.T) {
	link := "Intro words here " + "[a long link text that goes on and on](https://example.com/x) " + strings.Repeat("tail ", 20)
	for limit := 2; limit < 20; limit++ {
		out := trimWords(link, limit)
		if strings.Count(out, "[") != strings.Count(out, "](") || strings.Count(out, "](") != strings.Count(out, ")") {
			t.Errorf("limit %d: split link %q", limit, out)
		}
	}
	code := "Some words first.\n\n```go\n" + strings.Repeat("x := 1\n", 30) + "```\n\nAfter."
	for limit := 2; limit < 40; limit++ {
		if out := trimWords(code, limit); strings.Count(out, "```")%2 != 0 {
			t.Errorf("limit %d: open fence %q", limit, out)
		}
	}
	heading := "First paragraph that is long enough to keep.\n\n## A heading that has a body\n" + strings.Repeat("body ", 40)
	for limit := 2; limit < 30; limit++ {
		out := strings.TrimSuffix(trimWords(heading, limit), " …")
		if isHeading(lastLine(out)) {
			t.Errorf("limit %d: ends on a heading %q", limit, out)
		}
	}
	// A single item is cut inside its body, not to its bare heading.
	item := "### " + strings.Repeat("head ", 30) + "\n" + strings.Repeat("Body sentence. ", 40)
	if out := capItems(item, 1); isHeading(lastLine(out)) || countWords(out) > wordsPerItem {
		t.Errorf("item cut to %q", out)
	}
}
