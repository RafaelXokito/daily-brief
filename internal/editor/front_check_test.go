package editor

import (
	"context"
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
		`"in_brief":[{"id":"t0i1","sentence":"` + brief + `"}],"cover_line":"` + cover + `"}`
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

// A front page that falls back counts as one editor fallback.
func TestRunCountsFrontFallback(t *testing.T) {
	f := newFake(1)
	f.front = "nope"

	p := Run(context.Background(), f, runInput(), bigBudget(), 1)

	if p.Fallbacks != 1 {
		t.Errorf("fallbacks = %d, want 1 for the front page", p.Fallbacks)
	}
}
