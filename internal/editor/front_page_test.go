package editor

import (
	"strings"
	"testing"

	"github.com/RafaelXokito/daily-brief/internal/brief"
)

// workInput has a chapter kept off the front page holding the day's only
// ACT highlight, between two front page chapters.
func workInput() Input {
	return Input{
		Chapters: []ChapterIn{
			{Topic: "World", FrontPage: true, Items: []ItemIn{
				{ID: "t0i0", Headline: "Harbour reopens after storm", Prose: "Ships return."},
				{ID: "t0i1", Headline: "Rail strike called off", Prose: "Trains run."},
			}},
			{Topic: "Dev & Security", Items: []ItemIn{
				{ID: "t1i0", Headline: "Gateway flaw exploited", Prose: "Patch now."},
				{ID: "t1i1", Headline: "Build tool release", Prose: "Faster."},
			}},
			{Topic: "Money", FrontPage: true, Items: []ItemIn{
				{ID: "t2i0", Headline: "Rates held steady", Prose: "No change."},
			}},
		},
		Highlights: []brief.Highlight{
			{Tier: brief.TierAct, Topic: "Dev & Security", Headline: "Gateway flaw exploited", Text: "Patch today."},
			{Tier: brief.TierKnow, Topic: "Dev & Security", Headline: "Build tool release", Text: "Faster builds."},
		},
	}
}

func TestFrontPageExcludesChapter(t *testing.T) {
	in := workInput()

	f := FallbackFront(in)

	if len(f.Leads) != maxLeads {
		t.Fatalf("leads = %+v, want %d from the other chapters", f.Leads, maxLeads)
	}
	ids := []string{}
	for _, l := range f.Leads {
		ids = append(ids, l.ID)
	}
	for _, l := range f.InBrief {
		ids = append(ids, l.ID)
	}
	for _, id := range ids {
		if strings.HasPrefix(id, "t1") {
			t.Errorf("front page uses %s from a chapter kept off it: %+v", id, f)
		}
	}
	if f.Leads[0].ID != "t0i0" || f.Leads[1].ID != "t0i1" || len(f.InBrief) != 1 || f.InBrief[0].ID != "t2i0" {
		t.Errorf("front = %+v", f)
	}
	if strings.Contains(f.CoverLine, "Gateway") {
		t.Errorf("cover line %q comes from a chapter kept off the front page", f.CoverLine)
	}

	chapters := []Chapter{fallbackChapter(in.Chapters[0]), fallbackChapter(in.Chapters[1]), fallbackChapter(in.Chapters[2])}
	reply := `{"leads":[{"id":"t1i0","headline":"L","paragraphs":["p"]},{"id":"t0i0","headline":"L2","paragraphs":["p"]}],"in_brief":[],"cover_line":"C"}`
	if _, err := parseFront(reply, frontChapters(in, chapters)); err == nil {
		t.Errorf("parseFront accepted a lead from a chapter kept off the front page")
	}
}

func TestFrontPromptLeavesOutChapterAndItsHighlights(t *testing.T) {
	in := workInput()
	chapters := []Chapter{fallbackChapter(in.Chapters[0]), fallbackChapter(in.Chapters[1]), fallbackChapter(in.Chapters[2])}

	_, input := frontPrompt(frontOnly(in), frontChapters(in, chapters))

	for _, bad := range []string{"t1i0", "t1i1", "Gateway", "Build tool", "Dev & Security"} {
		if strings.Contains(input, bad) {
			t.Errorf("front prompt contains %q:\n%s", bad, input)
		}
	}
	for _, want := range []string{"t0i0", "t2i0", "Harbour reopens"} {
		if !strings.Contains(input, want) {
			t.Errorf("front prompt lacks %q:\n%s", want, input)
		}
	}
}

func TestStoryURLFromItem(t *testing.T) {
	c := testChapter()
	c.Items[0].URL = "https://a.example/one"

	ch, err := parseChapter(goodChapter, c)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Stories[0].URL != "https://a.example/one" || ch.Stories[1].URL != "" {
		t.Errorf("urls = %q, %q", ch.Stories[0].URL, ch.Stories[1].URL)
	}
	if s := FallbackStory(c.Items[0], "lead"); s.URL != "https://a.example/one" {
		t.Errorf("fallback url = %q", s.URL)
	}
	withURL := `{"stories":[
{"id":"t0i0","headline":"H1","standfirst":"","dateline":"","paragraphs":["P1"],"placement":"lead","url":"https://evil.example"},
{"id":"t0i1","headline":"H2","standfirst":"","dateline":"","paragraphs":["P2"],"placement":"brief"}]}`
	if _, err := parseChapter(withURL, c); err == nil {
		t.Errorf("a reply that sets a url was accepted")
	}
}

// A front reply that leads with a story from a chapter kept off the front
// page is rejected; the fallback still has leads from the other chapters.
func TestRunFrontPageRejectsExcludedChapter(t *testing.T) {
	in := workInput()
	f := newFake(1)
	f.front = `{"leads":[{"id":"t1i0","headline":"Patch the gateway","paragraphs":["Patch now."]},{"id":"t0i0","headline":"Harbour","paragraphs":["Ships return."]}],"in_brief":[],"cover_line":"Patch the gateway"}`

	p := Run(t.Context(), f, in, bigBudget(), 1)

	if p.Front.CoverLine == "Patch the gateway" || len(p.Front.Leads) != maxLeads {
		t.Fatalf("front = %+v, want the fallback with %d leads", p.Front, maxLeads)
	}
	for _, l := range p.Front.Leads {
		if strings.HasPrefix(l.ID, "t1") {
			t.Errorf("lead %s from a chapter kept off the front page", l.ID)
		}
	}
	if len(p.Chapters) != 3 || len(p.Chapters[1].Stories) != 2 {
		t.Errorf("the chapter kept off the front page lost its stories: %+v", p.Chapters)
	}
}

func TestBuildInputFrontPage(t *testing.T) {
	in := BuildInput(fixtureJournal(), nil, func(topic string) bool { return topic != "Security" })
	if in.Chapters[0].FrontPage || !in.Chapters[1].FrontPage {
		t.Errorf("front page = %v, %v; want false for Security, true for World", in.Chapters[0].FrontPage, in.Chapters[1].FrontPage)
	}
	for _, c := range BuildInput(fixtureJournal(), nil, nil).Chapters {
		if !c.FrontPage {
			t.Errorf("nil onFront left %s off the front page", c.Topic)
		}
	}
}
