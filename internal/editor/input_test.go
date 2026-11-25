package editor

import (
	"testing"

	"github.com/RafaelXokito/daily-brief/internal/brief"
	"github.com/RafaelXokito/daily-brief/internal/fetch"
)

func fixtureJournal() *brief.Journal {
	return &brief.Journal{
		Date: "2026-11-25",
		Sections: []*brief.Section{
			{Topic: "Security", Status: brief.StatusDone, Markdown: "### Widget server flaw exploited\n" +
				"`WATCH` facts · more facts\n" +
				"Attackers run code on unpatched servers. [source](https://example.com/a)\n\n" +
				"### Quiet release\n" +
				"A minor release, see [the notes](https://example.org/n) too. [source](https://example.net/b)\n"},
			{Topic: "Broken", Status: brief.StatusFailed, Markdown: "### Skipped\nNope. [source](https://example.com/z)\n"},
			{Topic: "World", Status: brief.StatusDone, Markdown: "### Harbour reopens\nShips return. [source](https://example.com/c)\n"},
		},
	}
}

func TestBuildInputSplitsItem(t *testing.T) {
	in := BuildInput(fixtureJournal(), map[string]fetch.Article{"https://example.com/a": {Text: "Body.", Words: 400}})
	it := in.Chapters[0].Items[0]
	want := ItemIn{
		ID: "t0i0", Headline: "Widget server flaw exploited", Tag: "WATCH", Facts: "facts · more facts",
		Prose: "Attackers run code on unpatched servers.",
		Host:  "example.com", URL: "https://example.com/a", Article: "Body.",
	}
	if it != want {
		t.Errorf("got %+v\nwant %+v", it, want)
	}
	if in.Date.Format("2006-01-02") != "2026-11-25" {
		t.Errorf("date = %v", in.Date)
	}
	if q := in.Chapters[0].Items[1]; q.Tag != "" || q.Facts != "" || q.ID != "t0i1" || q.Prose != "A minor release, see the notes too." {
		t.Errorf("untagged item = %+v", q)
	}
}

func TestBuildInputSkipsNonDoneSections(t *testing.T) {
	in := BuildInput(fixtureJournal(), nil)
	if len(in.Chapters) != 2 || in.Chapters[0].Topic != "Security" || in.Chapters[1].Topic != "World" {
		t.Fatalf("chapters = %+v", in.Chapters)
	}
	if id := in.Chapters[1].Items[0].ID; id != "t1i0" {
		t.Errorf("id = %q, want t1i0", id)
	}
}

func TestBuildInputFailedArticleIsEmpty(t *testing.T) {
	arts := map[string]fetch.Article{
		"https://example.com/a": {Err: fetch.ErrTooShort, Text: "leftover"},
		"https://example.net/b": {},
	}
	in := BuildInput(fixtureJournal(), arts)
	for _, it := range in.Chapters[0].Items {
		if it.Article != "" {
			t.Errorf("%s article = %q, want empty", it.ID, it.Article)
		}
	}
}

func TestBuildInputTagPlacements(t *testing.T) {
	tests := []struct {
		name, md, tag, facts, prose string
	}{
		{"tag on headline", "### `ACT` Patch now\nFirst prose line.\nSecond line. [source](https://example.com/a)\n", "ACT", "", "First prose line. Second line."},
		{"tag without facts", "### Headline\n`WATCH`\nProse here. [source](https://example.com/a)\n", "WATCH", "", "Prose here."},
		{"tag line without prose", "### Headline\n`FYI` only facts · here [source](https://example.com/a)\n", "FYI", "only facts · here", ""},
		{"no tag", "### Headline\nProse. [source](https://example.com/a)\n", "", "", "Prose."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			j := &brief.Journal{Date: "2026-11-25", Sections: []*brief.Section{{Topic: "T", Status: brief.StatusDone, Markdown: tt.md}}}
			in := BuildInput(j, nil)
			if len(in.Chapters) != 1 || len(in.Chapters[0].Items) != 1 {
				t.Fatalf("chapters = %+v", in.Chapters)
			}
			it := in.Chapters[0].Items[0]
			if it.Tag != tt.tag || it.Facts != tt.facts || it.Prose != tt.prose {
				t.Errorf("got tag %q facts %q prose %q", it.Tag, it.Facts, it.Prose)
			}
		})
	}
}
