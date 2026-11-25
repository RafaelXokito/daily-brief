package editor

import (
	"reflect"
	"strings"
	"testing"

	"github.com/RafaelXokito/daily-brief/internal/brief"
)

func TestFallbackStory(t *testing.T) {
	s := FallbackStory(ItemIn{ID: "t0i0", Headline: "H", Tag: "ACT", Facts: "a · b", Prose: "One. Two.", Host: "x.org"}, "story")
	want := Story{ID: "t0i0", Headline: "H", Standfirst: "a; b", Paragraphs: []string{"One. Two."},
		Placement: "story", Host: "x.org", Tag: "ACT", Fallback: true}
	if !reflect.DeepEqual(s, want) {
		t.Errorf("got %+v\nwant %+v", s, want)
	}
	all := s.Headline + s.Standfirst + strings.Join(s.Paragraphs, "")
	if strings.ContainsAny(all, "`·") {
		t.Errorf("markup left in %q", all)
	}
}

func TestFallbackStoryNoProse(t *testing.T) {
	if s := FallbackStory(ItemIn{Headline: "H"}, "brief"); len(s.Paragraphs) != 0 || s.Standfirst != "" {
		t.Errorf("got %+v", s)
	}
}

func fixtureInput() Input {
	return Input{
		Chapters: []ChapterIn{{Topic: "A", FrontPage: true, Items: []ItemIn{
			{ID: "t0i0", Headline: "First flaw patched", Prose: "p0"},
			{ID: "t0i1", Headline: "Second thing", Prose: "p1"},
			{ID: "t0i2", Headline: "Third thing", Prose: "p2"},
		}}, {Topic: "B", FrontPage: true, Items: []ItemIn{{ID: "t1i0", Headline: "Fourth thing"}}}},
	}
}

func TestFallbackFrontUsesActHighlights(t *testing.T) {
	in := fixtureInput()
	in.Highlights = []brief.Highlight{
		{Tier: brief.TierKnow, Headline: "Second thing", Text: "ignored"},
		{Tier: brief.TierAct, Headline: "Third thing", Text: "Patch now.", Why: "Exploited."},
		{Tier: brief.TierAct, Headline: "First flaw patched", Text: "Update."},
		{Tier: brief.TierAct, Headline: "Fourth thing", Text: "Extra."},
	}
	f := FallbackFront(in)
	if len(f.Leads) != 2 || f.Leads[0].ID != "t0i2" || f.Leads[1].ID != "t0i0" {
		t.Fatalf("leads = %+v", f.Leads)
	}
	if !reflect.DeepEqual(f.Leads[0].Paragraphs, []string{"Patch now.", "Exploited."}) {
		t.Errorf("paragraphs = %v", f.Leads[0].Paragraphs)
	}
	if f.CoverLine != "Third thing" {
		t.Errorf("cover = %q", f.CoverLine)
	}
	want := []FrontLine{{"t0i1", "Second thing"}, {"t1i0", "Fourth thing"}}
	if !reflect.DeepEqual(f.InBrief, want) {
		t.Errorf("in brief = %+v", f.InBrief)
	}
}

func TestFallbackFrontWithoutActUsesFirstItems(t *testing.T) {
	f := FallbackFront(fixtureInput())
	if len(f.Leads) != 2 || f.Leads[0].ID != "t0i0" || f.Leads[1].ID != "t0i1" {
		t.Fatalf("leads = %+v", f.Leads)
	}
	if f.CoverLine != "First flaw patched" || len(f.InBrief) != 2 {
		t.Errorf("cover = %q, in brief = %+v", f.CoverLine, f.InBrief)
	}
}

func TestFallbackFrontEmpty(t *testing.T) {
	if f := FallbackFront(Input{}); len(f.Leads) != 0 || len(f.InBrief) != 0 || f.CoverLine != "" {
		t.Errorf("got %+v", f)
	}
}

func TestFallbackFrontDropsUnmatchedActHighlight(t *testing.T) {
	in := fixtureInput()
	in.Highlights = []brief.Highlight{
		{Tier: brief.TierAct, Headline: "Nothing like any item", Text: "Orphan."},
		{Tier: brief.TierAct, Headline: "Third thing", Text: "Patch now."},
	}
	f := FallbackFront(in)
	if len(f.Leads) != 2 || f.Leads[0].ID != "t0i2" || f.Leads[1].ID != "t0i0" {
		t.Fatalf("leads = %+v", f.Leads)
	}
	for _, l := range f.Leads {
		if l.ID == "" {
			t.Errorf("lead without ID: %+v", l)
		}
	}
	if f.CoverLine != "Third thing" {
		t.Errorf("cover = %q", f.CoverLine)
	}
	for _, l := range f.InBrief {
		if l.ID == "t0i2" || l.ID == "t0i0" {
			t.Errorf("lead repeated in brief: %+v", l)
		}
	}
}

func TestFallbackFrontItemLeadUsesFirstParagraph(t *testing.T) {
	in := Input{Chapters: []ChapterIn{{FrontPage: true, Items: []ItemIn{{ID: "t0i0", Headline: "H", Prose: "One.\n\nTwo."}}}}}
	if f := FallbackFront(in); !reflect.DeepEqual(f.Leads[0].Paragraphs, []string{"One."}) {
		t.Errorf("paragraphs = %v", f.Leads[0].Paragraphs)
	}
}
