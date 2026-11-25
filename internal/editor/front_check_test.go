package editor

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// frontInput is runInput with a number in each Alpha item.
func frontInput() Input {
	in := runInput()
	in.Chapters[0].Items[0].Facts = "fact a, 42 users affected"
	in.Chapters[0].Items[1].Article = "The release fixed 7 bugs."
	return in
}

func frontReply(lead, brief, cover string) string {
	return `{"leads":[{"id":"t0i0","headline":"Front A","paragraphs":["` + lead + `"]},{"id":"t1i0","headline":"Front B","paragraphs":["Opening."]}],` +
		`"in_brief":[{"topic":"Alpha","sentences":[{"id":"t0i1","text":"` + brief + `"}]}],"cover_line":"` + cover + `"}`
}

func TestRunFrontFactCheck(t *testing.T) {
	for _, tc := range []struct {
		name, lead, brief, cover string
		keep                     bool
	}{
		{"facts from own items", "It hit 42 users.", "It fixed 7 bugs.", "42 users hit", true},
		{"cover line from any story", "Opening.", "Short.", "7 bugs fixed", true},
		{"lead invents a number", "It hit 43 users.", "Short.", "Cover", false},
		{"lead borrows another story's number", "It fixed 7 bugs.", "Short.", "Cover", false},
		{"in brief invents a CVE", "Opening.", "It fixed CVE-2026-1234.", "Cover", false},
		{"in brief borrows another story's number", "Opening.", "It hit 42 users.", "Cover", false},
		{"cover line invents money", "Opening.", "Short.", "A $5 fix", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(1)
			f.front = frontReply(tc.lead, tc.brief, tc.cover)

			p := Run(context.Background(), f, frontInput(), bigBudget(), 1)

			kept := p.Front.CoverLine == tc.cover
			if kept != tc.keep {
				t.Fatalf("front kept = %v, want %v: %+v", kept, tc.keep, p.Front)
			}
			if !tc.keep {
				want := FallbackFront(frontInput())
				if p.Front.CoverLine != want.CoverLine || len(p.Front.Leads) != len(want.Leads) || !strings.HasPrefix(p.Front.Leads[0].Headline, "Alpha") {
					t.Fatalf("front = %+v, want the fallback %+v", p.Front, want)
				}
			}
		})
	}
}

// briefInput is frontInput with a third Alpha item, so Alpha's In Brief
// paragraph has two sentences: one about the 42 users, one about the 7 bugs.
func briefInput() Input {
	in := frontInput()
	in.Chapters[0].Items[1].Article = ""
	in.Chapters[0].Items[1].Facts = "42 users affected"
	in.Chapters[0].Items = append(in.Chapters[0].Items, ItemIn{ID: "t0i2", Headline: "Alpha three", Prose: "Prose.", Article: "The release fixed 7 bugs."})
	return in
}

func briefFront(second string) string {
	return `{"leads":[{"id":"t0i0","headline":"Front A","paragraphs":["Opening."]},{"id":"t1i0","headline":"Front B","paragraphs":["Opening."]}],` +
		`"in_brief":[{"topic":"Alpha","sentences":[{"id":"t0i1","text":"It hit 42 users."},{"id":"t0i2","text":"` + second + `"}]}],"cover_line":"Cover"}`
}

// Every sentence of an In Brief paragraph is checked against its own
// story: a number from another sentence's story, or from nowhere, sends
// the front page to the fallback.
func TestCheckFrontBriefSentence(t *testing.T) {
	for _, tc := range []struct {
		name, second string
		keep         bool
	}{
		{"number from its own story", "It fixed 7 bugs.", true},
		{"number from the other sentence's story", "It fixed 42 bugs.", false},
		{"number in no story", "It fixed 8 bugs.", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(1)
			f.front = briefFront(tc.second)

			p := Run(context.Background(), f, briefInput(), bigBudget(), 1)

			if kept := p.Front.CoverLine == "Cover"; kept != tc.keep {
				t.Fatalf("front kept = %v, want %v: %+v", kept, tc.keep, p.Front)
			}
			if !tc.keep && !reflect.DeepEqual(p.Front, FallbackFront(briefInput())) {
				t.Errorf("front = %+v, want the fallback", p.Front)
			}
			if !tc.keep && p.Fallbacks != 1 {
				t.Errorf("fallbacks = %d, want 1 for the front page", p.Fallbacks)
			}
		})
	}
}

// A front page that falls back counts as one editor fallback.
func TestRunCountsFrontFallback(t *testing.T) {
	f := newFake(1)
	f.front = "nope"

	p := Run(context.Background(), f, runInput(), bigBudget(), 1)

	if p.Fallbacks != 1 {
		t.Errorf("fallbacks = %d, want 1 for the front page", p.Fallbacks)
	}
}
