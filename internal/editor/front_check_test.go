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

// topicsInput is briefInput with two more Beta items, so both topics
// have an In Brief paragraph after the leads t0i0 and t1i0.
func topicsInput() Input {
	in := briefInput()
	in.Chapters[1].Items = append(in.Chapters[1].Items,
		ItemIn{ID: "t1i1", Headline: "Beta two", Facts: "3 ships docked", Prose: "Prose."},
		ItemIn{ID: "t1i2", Headline: "Beta three", Prose: "Prose."})
	return in
}

const (
	alphaOK = `{"topic":"Alpha","sentences":[{"id":"t0i1","text":"It hit 42 users."},{"id":"t0i2","text":"It fixed 7 bugs."}]}`
	betaOK  = `{"topic":"Beta","sentences":[{"id":"t1i1","text":"Three ships docked."},{"id":"t1i2","text":"Beta happened."}]}`
)

func topicsFront(lead string, paragraphs ...string) string {
	return `{"leads":[{"id":"t0i0","headline":"Front A","paragraphs":["` + lead + `"]},{"id":"t1i0","headline":"Front B","paragraphs":["Opening."]}],` +
		`"in_brief":[` + strings.Join(paragraphs, ",") + `],"cover_line":"Cover"}`
}

func alphaWith(second string) string {
	return `{"topic":"Alpha","sentences":[{"id":"t0i1","text":"It hit 42 users."},{"id":"t0i2","text":"` + second + `"}]}`
}

var (
	alphaModel = BriefParagraph{Topic: "Alpha", Sentences: []BriefSentence{{"t0i1", "It hit 42 users."}, {"t0i2", "It fixed 7 bugs."}}}
	alphaMech  = BriefParagraph{Topic: "Alpha", Sentences: []BriefSentence{{"t0i1", "Alpha two."}, {"t0i2", "Alpha three."}}}
	betaModel  = BriefParagraph{Topic: "Beta", Sentences: []BriefSentence{{"t1i1", "Three ships docked."}, {"t1i2", "Beta happened."}}}
	betaMech   = BriefParagraph{Topic: "Beta", Sentences: []BriefSentence{{"t1i1", "Beta two."}, {"t1i2", "Beta three."}}}
)

// runTopics runs the editor on topicsInput with front as the front reply.
func runTopics(t *testing.T, front string) Paper {
	t.Helper()
	f := newFake(1)
	f.front = front
	return Run(context.Background(), f, topicsInput(), bigBudget(), 1)
}

// assertModelLeads fails unless the model's leads and cover line were kept.
func assertModelLeads(t *testing.T, p Paper) {
	t.Helper()
	if p.Front.CoverLine != "Cover" || len(p.Front.Leads) != 2 || p.Front.Leads[0].Headline != "Front A" {
		t.Fatalf("leads and cover line not kept: %+v", p.Front)
	}
}

// Review Focus 2: every sentence of an In Brief paragraph is checked
// against its own story. A number from another sentence's story, or from
// nowhere, sends that topic's paragraph to the mechanical one; the leads,
// the cover line and the other topic's paragraph stay the model's.
func TestCheckFrontBriefSentence(t *testing.T) {
	for _, tc := range []struct {
		name, second string
		keep         bool
	}{
		{"number from its own story", "It fixed 7 bugs.", true},
		{"number from the other sentence's story", "It fixed 42 bugs.", false},
		{"number in no story", "It fixed 8 bugs.", false},
		{"invented CVE", "It fixed CVE-2026-1234.", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := runTopics(t, topicsFront("Opening.", alphaWith(tc.second), betaOK))

			assertModelLeads(t, p)
			want, fallbacks := []BriefParagraph{alphaModel, betaModel}, 0
			if !tc.keep {
				want, fallbacks = []BriefParagraph{alphaMech, betaModel}, 1
			}
			if !reflect.DeepEqual(p.Front.InBrief, want) {
				t.Errorf("in brief = %+v\nwant %+v", p.Front.InBrief, want)
			}
			if p.Fallbacks != fallbacks {
				t.Errorf("fallbacks = %d, want %d (one per fallen-back topic)", p.Fallbacks, fallbacks)
			}
		})
	}
}

func TestRunInBriefPerTopic(t *testing.T) {
	sport := `{"topic":"Sport","sentences":[{"id":"t1i1","text":"Three ships docked."}]}`
	betaBadIDs := `{"topic":"Beta","sentences":[{"id":"t1i1","text":"A."},{"id":"t0i1","text":"B."}]}`
	for _, tc := range []struct {
		name      string
		reply     string
		want      []BriefParagraph
		fallbacks int
	}{
		{"missing topic is mechanical", topicsFront("Opening.", alphaOK), []BriefParagraph{alphaModel, betaMech}, 1},
		{"unknown topic is dropped", topicsFront("Opening.", alphaOK, sport, betaOK), []BriefParagraph{alphaModel, betaModel}, 0},
		{"order comes from the chapters", topicsFront("Opening.", betaOK, alphaOK), []BriefParagraph{alphaModel, betaModel}, 0},
		{"id from another topic is mechanical", topicsFront("Opening.", alphaOK, betaBadIDs), []BriefParagraph{alphaModel, betaMech}, 1},
		{"no paragraphs at all", topicsFront("Opening."), []BriefParagraph{alphaMech, betaMech}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := runTopics(t, tc.reply)

			assertModelLeads(t, p)
			if !reflect.DeepEqual(p.Front.InBrief, tc.want) {
				t.Errorf("in brief = %+v\nwant %+v", p.Front.InBrief, tc.want)
			}
			if p.Fallbacks != tc.fallbacks {
				t.Errorf("fallbacks = %d, want %d", p.Fallbacks, tc.fallbacks)
			}
		})
	}
}

// A lead that fails the fact check still sends the whole front page to
// the fallback, counted once, even with good paragraphs.
func TestRunBadLeadFallsWholeFrontBack(t *testing.T) {
	p := runTopics(t, topicsFront("It hit 99 users.", alphaOK, betaOK))

	if !reflect.DeepEqual(p.Front, FallbackFront(topicsInput())) {
		t.Errorf("front = %+v\nwant the fallback", p.Front)
	}
	if p.Fallbacks != 1 {
		t.Errorf("fallbacks = %d, want 1 for the front page", p.Fallbacks)
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
