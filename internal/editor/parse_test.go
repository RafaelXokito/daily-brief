package editor

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func testChapter() ChapterIn {
	return ChapterIn{Topic: "Security", Items: []ItemIn{
		{ID: "t0i0", Headline: "One", Tag: "ACT", Host: "a.example"},
		{ID: "t0i1", Headline: "Two", Tag: "", Host: "b.example"},
	}}
}

const goodChapter = `{"stories":[
{"id":"t0i0","headline":"H1","standfirst":"S1","dateline":"","paragraphs":["P1"],"placement":"lead"},
{"id":"t0i1","headline":"H2","standfirst":"S2","dateline":"","paragraphs":["P2"],"placement":"brief"}]}`

func TestParseChapterFenced(t *testing.T) {
	for name, reply := range map[string]string{
		"fenced":  "Here it is:\n```json\n" + goodChapter + "\n```",
		"bare":    goodChapter,
		"trailer": "Sure.\n" + goodChapter + "\nHope that helps {really}.",
		"braces":  "Note {x} first.\n" + goodChapter,
	} {
		ch, err := parseChapter(reply, testChapter())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if ch.Topic != "Security" || len(ch.Stories) != 2 {
			t.Fatalf("%s: %+v", name, ch)
		}
		s := ch.Stories[0]
		if s.Host != "a.example" || s.Tag != "ACT" || s.Placement != "lead" || s.Fallback {
			t.Fatalf("%s: %+v", name, s)
		}
		if ch.Stories[1].Host != "b.example" {
			t.Fatalf("%s: %+v", name, ch.Stories[1])
		}
	}
}

func TestParseChapterErrors(t *testing.T) {
	story := func(id, placement string) string {
		return `{"id":"` + id + `","headline":"H","standfirst":"","dateline":"","paragraphs":["P"],"placement":"` + placement + `"}`
	}
	wrap := func(ss ...string) string { return `{"stories":[` + strings.Join(ss, ",") + `]}` }
	for name, reply := range map[string]string{
		"missing":    wrap(story("t0i0", "lead")),
		"duplicate":  wrap(story("t0i0", "lead"), story("t0i0", "story"), story("t0i1", "brief")),
		"unknown":    wrap(story("t0i0", "lead"), story("t0i1", "brief"), story("t9i9", "story")),
		"placement":  wrap(story("t0i0", "sidebar"), story("t0i1", "brief")),
		"nojson":     "I cannot do that.",
		"emptybody":  wrap(story("t0i0", "lead"), `{"id":"t0i1","headline":"H","paragraphs":[],"placement":"brief"}`),
		"noheadline": wrap(story("t0i0", "lead"), `{"id":"t0i1","headline":" ","paragraphs":["P"],"placement":"brief"}`),
	} {
		if _, err := parseChapter(reply, testChapter()); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func testChapters() []Chapter {
	return []Chapter{{Topic: "Security", Stories: []Story{{ID: "t0i0"}, {ID: "t0i1"}, {ID: "t0i2"}}}}
}

func TestParseFront(t *testing.T) {
	reply := "```json\n" + `{"leads":[{"id":"t0i0","headline":"L","paragraphs":["p"]},{"id":"t0i2","headline":"L2","paragraphs":["p"]}],"in_brief":[{"topic":"Security","sentences":[{"id":"t0i1","text":"s"}]}],"cover_line":"C"}` + "\n```"
	fp, err := parseFront(reply, testChapters())
	if err != nil {
		t.Fatal(err)
	}
	if len(fp.Leads) != 2 || fp.Leads[0].ID != "t0i0" || len(fp.InBrief) != 1 || fp.CoverLine != "C" {
		t.Fatalf("%+v", fp)
	}
}

func TestParseFrontErrors(t *testing.T) {
	for name, reply := range map[string]string{
		"unknown lead": `{"leads":[{"id":"t9i9","headline":"L","paragraphs":["p"]}],"in_brief":[],"cover_line":"C"}`,
		"one lead":     `{"leads":[{"id":"t0i0","headline":"L","paragraphs":["p"]}],"in_brief":[],"cover_line":"C"}`,
		"empty lead":   `{"leads":[{"id":"t0i0","headline":"","paragraphs":["p"]},{"id":"t0i2","headline":"L","paragraphs":["p"]}],"in_brief":[{"topic":"Security","sentences":[{"id":"t0i1","text":"s"}]}],"cover_line":"C"}`,
		"old shape":    `{"leads":[{"id":"t0i0","headline":"L","paragraphs":["p"]},{"id":"t0i2","headline":"L","paragraphs":["p"]}],"in_brief":[{"id":"t0i1","sentence":"s"}],"cover_line":"C"}`,
		"no json":      "nope",
	} {
		if _, err := parseFront(reply, testChapters()); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

// briefChapters has a topic with six stories after its lead and a topic
// with two.
func briefChapters() []Chapter {
	var sec, world []Story
	for i := range 7 {
		sec = append(sec, Story{ID: fmt.Sprintf("t0i%d", i)})
	}
	for i := range 3 {
		world = append(world, Story{ID: fmt.Sprintf("t1i%d", i)})
	}
	return []Chapter{{Topic: "Dev & Security", Stories: sec}, {Topic: "World", Stories: world}}
}

func briefReply(inBrief string) string {
	return `{"leads":[{"id":"t0i0","headline":"L","paragraphs":["p"]},{"id":"t1i0","headline":"L2","paragraphs":["p"]}],"in_brief":[` + inBrief + `],"cover_line":"C"}`
}

func para(topic string, ids ...string) string {
	var ss []string
	for _, id := range ids {
		ss = append(ss, `{"id":"`+id+`","text":"About `+id+`."}`)
	}
	return `{"topic":"` + topic + `","sentences":[` + strings.Join(ss, ",") + `]}`
}

func TestParseFrontInBriefParagraphs(t *testing.T) {
	reply := briefReply(para("dev &  security", "t0i3", "t0i1", "t0i6") + "," + para("World", "t1i2", "t1i1"))

	fp, err := parseFront(reply, briefChapters())

	if err != nil {
		t.Fatal(err)
	}
	want := []BriefParagraph{
		{Topic: "Dev & Security", Sentences: []BriefSentence{{"t0i3", "About t0i3."}, {"t0i1", "About t0i1."}, {"t0i6", "About t0i6."}}},
		{Topic: "World", Sentences: []BriefSentence{{"t1i2", "About t1i2."}, {"t1i1", "About t1i1."}}},
	}
	if !reflect.DeepEqual(fp.InBrief, want) {
		t.Errorf("in brief = %+v\nwant %+v", fp.InBrief, want)
	}
}

// A topic with only one story after the front page leads has a
// one-sentence paragraph; a paragraph for a topic with none is dropped.
func TestParseFrontInBriefShortTopics(t *testing.T) {
	chapters := []Chapter{
		{Topic: "Alpha", Stories: []Story{{ID: "t0i0"}, {ID: "t0i1"}}},
		{Topic: "Beta", Stories: []Story{{ID: "t1i0"}}},
	}
	for name, reply := range map[string]string{
		"alpha only":     briefReply(para("Alpha", "t0i1")),
		"beta with none": briefReply(para("Alpha", "t0i1") + "," + para("Beta", "t1i0")),
	} {
		fp, err := parseFront(reply, chapters)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(fp.InBrief) != 1 || fp.InBrief[0].Topic != "Alpha" {
			t.Errorf("%s: in brief = %+v, want Alpha's paragraph only", name, fp.InBrief)
		}
	}
}

// A paragraph that breaks a rule is left out, not the whole reply; the
// others are kept, in chapter order.
func TestParseFrontDropsBadParagraphs(t *testing.T) {
	world := para("World", "t1i1", "t1i2")
	for name, tc := range map[string]struct {
		inBrief string
		kept    []string
	}{
		"unknown id":          {para("Dev & Security", "t0i1", "t9i9") + "," + world, []string{"World"}},
		"id from other topic": {para("Dev & Security", "t0i1", "t1i1") + "," + world, []string{"World"}},
		"five sentences":      {para("Dev & Security", "t0i1", "t0i2", "t0i3", "t0i4", "t0i5") + "," + world, []string{"World"}},
		"id reused":           {para("Dev & Security", "t0i1", "t0i1") + "," + world, []string{"World"}},
		"lead id reused":      {para("Dev & Security", "t0i1", "t0i0") + "," + world, []string{"World"}},
		"one sentence":        {para("Dev & Security", "t0i1") + "," + world, []string{"World"}},
		"empty text":          {strings.Replace(para("Dev & Security", "t0i1", "t0i2"), "About t0i2.", " ", 1) + "," + world, []string{"World"}},
		"missing topic":       {para("Dev & Security", "t0i1", "t0i2"), []string{"Dev & Security"}},
		"out of order":        {world + "," + para("Dev & Security", "t0i1", "t0i2"), []string{"Dev & Security", "World"}},
		"topic twice":         {para("Dev & Security", "t0i1", "t0i2") + "," + para("Dev & Security", "t0i3", "t0i4") + "," + world, []string{"Dev & Security", "World"}},
		"unknown topic":       {para("Sport", "t0i1", "t0i2") + "," + world, []string{"World"}},
	} {
		fp, err := parseFront(briefReply(tc.inBrief), briefChapters())
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		var got []string
		for _, p := range fp.InBrief {
			got = append(got, p.Topic)
		}
		if !reflect.DeepEqual(got, tc.kept) {
			t.Errorf("%s: kept %v, want %v", name, got, tc.kept)
		}
	}
	fp, _ := parseFront(briefReply(para("Dev & Security", "t0i1", "t0i2")+","+para("Dev & Security", "t0i3", "t0i4")+","+world), briefChapters())
	if len(fp.InBrief) > 0 && fp.InBrief[0].Sentences[0].ID != "t0i1" {
		t.Errorf("topic twice: kept %+v, want the first paragraph", fp.InBrief[0])
	}
}

func TestParseChapterLeadCount(t *testing.T) {
	story := func(id, placement string) string {
		return `{"id":"` + id + `","headline":"H","paragraphs":["P"],"placement":"` + placement + `"}`
	}
	for name, reply := range map[string]string{
		"zero leads": `{"stories":[` + story("t0i0", "story") + "," + story("t0i1", "brief") + `]}`,
		"two leads":  `{"stories":[` + story("t0i0", "lead") + "," + story("t0i1", "lead") + `]}`,
	} {
		if _, err := parseChapter(reply, testChapter()); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestParseChapterBriefOneParagraph(t *testing.T) {
	reply := `{"stories":[{"id":"t0i0","headline":"H","paragraphs":["P"],"placement":"lead"},{"id":"t0i1","headline":"H","paragraphs":["P","Q"],"placement":"brief"}]}`
	if _, err := parseChapter(reply, testChapter()); err == nil {
		t.Fatal("two-paragraph brief should fail")
	}
}

func TestParseChapterObjectSelection(t *testing.T) {
	draft := strings.Replace(goodChapter, `"headline":"H1"`, `"headline":"DRAFT"`, 1)
	for name, tc := range map[string]struct {
		reply, headline string
		ok              bool
	}{
		"trailing {}":         {goodChapter + " trailing {}", "H1", true},
		"lone {}":             {"I would put {} here.", "", false},
		"unknown key then {}": {strings.Replace(goodChapter, `]}`, `],"extra":1}`, 1) + " trailing {}", "", false},
		"draft then final":    {draft + "\nCorrected:\n" + goodChapter, "H1", true},
		"fence wins":          {"```json\n" + goodChapter + "\n```\nAlso " + draft, "H1", true},
	} {
		ch, err := parseChapter(tc.reply, testChapter())
		if (err == nil) != tc.ok {
			t.Errorf("%s: err=%v", name, err)
			continue
		}
		if tc.ok && ch.Stories[0].Headline != tc.headline {
			t.Errorf("%s: got %q", name, ch.Stories[0].Headline)
		}
	}
}

func TestParseFrontEmptyObject(t *testing.T) {
	if _, err := parseFront("I would put {} here.", testChapters()); err == nil {
		t.Fatal("empty object must fail")
	}
}
