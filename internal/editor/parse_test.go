package editor

import (
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
	reply := "```json\n" + `{"leads":[{"id":"t0i0","headline":"L","paragraphs":["p"]},{"id":"t0i2","headline":"L2","paragraphs":["p"]}],"in_brief":[{"id":"t0i1","sentence":"s"}],"cover_line":"C"}` + "\n```"
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
		"unknown lead":  `{"leads":[{"id":"t9i9","headline":"L","paragraphs":["p"]}],"in_brief":[],"cover_line":"C"}`,
		"unknown brief": `{"leads":[{"id":"t0i0","headline":"L","paragraphs":["p"]},{"id":"t0i2","headline":"L","paragraphs":["p"]}],"in_brief":[{"id":"zz","sentence":"s"}],"cover_line":"C"}`,
		"one lead":      `{"leads":[{"id":"t0i0","headline":"L","paragraphs":["p"]}],"in_brief":[],"cover_line":"C"}`,
		"duplicate":     `{"leads":[{"id":"t0i0","headline":"L","paragraphs":["p"]},{"id":"t0i2","headline":"L","paragraphs":["p"]}],"in_brief":[{"id":"t0i0","sentence":"s"}],"cover_line":"C"}`,
		"empty lead":    `{"leads":[{"id":"t0i0","headline":"","paragraphs":["p"]},{"id":"t0i2","headline":"L","paragraphs":["p"]}],"in_brief":[],"cover_line":"C"}`,
		"no json":       "nope",
	} {
		if _, err := parseFront(reply, testChapters()); err == nil {
			t.Errorf("%s: expected error", name)
		}
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
