package editor

import (
	"slices"
	"strings"
	"testing"
)

// marked returns the text of each highlight, so tests read as phrases.
func marked(paras []string, hs []Highlight) []string {
	var out []string
	for _, h := range hs {
		out = append(out, paras[h.Para][h.Start:h.End])
	}
	return out
}

func TestParseMarks(t *testing.T) {
	twelve := strings.TrimSpace(strings.Repeat("word ", 12))
	thirteen := strings.TrimSpace(strings.Repeat("word ", 13))
	for _, tc := range []struct {
		name       string
		in         []string
		wantParas  []string
		wantMarked []string
	}{
		{"one mark", []string{"The board ==cut the price in half== on Monday."},
			[]string{"The board cut the price in half on Monday."}, []string{"cut the price in half"}},
		{"no marks", []string{"Plain text, a = b."}, []string{"Plain text, a = b."}, nil},
		{"unbalanced", []string{"The board ==cut the price on Monday."},
			[]string{"The board cut the price on Monday."}, nil},
		{"unbalanced keeps the other paragraph's marks", []string{"One ==two", "Three ==four== five."},
			[]string{"One two", "Three four five."}, []string{"four"}},
		{"nested", []string{"A ==outer ==inner== phrase== here."},
			[]string{"A outer inner phrase here."}, nil},
		{"four equals signs are text", []string{"A ==== b."}, []string{"A ==== b."}, nil},
		{"spaced == is text", []string{"if x == y then"}, []string{"if x == y then"}, nil},
		{"spaced == beside a mark", []string{"x == y and ==key phrase== here, == at the end =="},
			[]string{"x == y and key phrase here, == at the end =="}, []string{"key phrase"}},
		{"three equals signs are text", []string{"a ===b=== c"}, []string{"a ===b=== c"}, nil},
		{"three marks keep the first two", []string{"==One== and ==two==.", "Then ==three==."},
			[]string{"One and two.", "Then three."}, []string{"One", "two"}},
		{"twelve words kept", []string{"==" + twelve + "== end."}, []string{twelve + " end."}, []string{twelve}},
		{"thirteen words dropped", []string{"==" + thirteen + "== end."}, []string{thirteen + " end."}, nil},
		{"an over-long mark does not use up the limit", []string{"==" + thirteen + "== ==a== ==b==."},
			[]string{thirteen + " a b."}, []string{"a", "b"}},
		{"script inside", []string{"Note ==<script>alert(1)</script>== now."},
			[]string{"Note <script>alert(1)</script> now."}, nil},
		{"spaced opening is text, so the closing is unbalanced", []string{"== edge== text"}, []string{"== edge text"}, nil},
		{"spaced closing is text, so the opening is unbalanced", []string{"a ==edge == text"}, []string{"a edge == text"}, nil},
		{"blank paragraph", []string{"  "}, nil, nil},
		{"multibyte", []string{"Em ==Évora, açúcar== subiu."}, []string{"Em Évora, açúcar subiu."}, []string{"Évora, açúcar"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paras, hs := parseMarks(tc.in, maxHighlights)
			if !slices.Equal(paras, tc.wantParas) {
				t.Fatalf("paragraphs %q, want %q", paras, tc.wantParas)
			}
			if got := marked(paras, hs); !slices.Equal(got, tc.wantMarked) {
				t.Errorf("marked %q, want %q", got, tc.wantMarked)
			}
		})
	}
}

func TestParseChapterHighlightsAndPictures(t *testing.T) {
	reply := `{"stories":[
{"id":"t0i0","headline":"The ==board== acts","standfirst":"A ==short== note","dateline":"","paragraphs":["The board ==cut the price== today."],"placement":"lead","needs_picture":true},
{"id":"t0i1","headline":"H2","standfirst":"","dateline":"","paragraphs":["Plain."],"placement":"brief"}]}`
	ch, err := parseChapter(reply, testChapter())
	if err != nil {
		t.Fatal(err)
	}
	lead, brief := ch.Stories[0], ch.Stories[1]
	if lead.Headline != "The board acts" || lead.Standfirst != "A short note" {
		t.Errorf("marks kept in the headline or standfirst: %q, %q", lead.Headline, lead.Standfirst)
	}
	if got := marked(lead.Paragraphs, lead.Highlights); !slices.Equal(got, []string{"cut the price"}) {
		t.Errorf("highlights %q, want [cut the price]", got)
	}
	if lead.Paragraphs[0] != "The board cut the price today." {
		t.Errorf("paragraph %q keeps its marks", lead.Paragraphs[0])
	}
	if !lead.NeedsPicture || brief.NeedsPicture {
		t.Errorf("needs_picture = %v, %v; want true, then false by default", lead.NeedsPicture, brief.NeedsPicture)
	}
}

func TestCheckStoryIgnoresMarks(t *testing.T) {
	c := ChapterIn{Topic: "Markets", Items: []ItemIn{{ID: "t0i0", Headline: "Prices", Facts: "Prices rose 5 percent."}}}
	for _, tc := range []struct {
		para string
		want bool
	}{
		{"Prices ==rose 5 percent== this week.", true},
		{"Prices ==rose 9 percent== this week.", false}, // a mark does not hide a number
	} {
		reply := `{"stories":[{"id":"t0i0","headline":"Prices","paragraphs":["` + tc.para + `"],"placement":"lead"}]}`
		ch, err := parseChapter(reply, c)
		if err != nil {
			t.Fatal(err)
		}
		if got := checkStory(ch.Stories[0], c.Items[0]); got != tc.want {
			t.Errorf("%q: checkStory = %v, want %v", tc.para, got, tc.want)
		}
	}
}

func TestEditChapterFallbackHasNoHighlights(t *testing.T) {
	c := ChapterIn{Topic: "Markets", Items: []ItemIn{{ID: "t0i0", Headline: "Prices", Facts: "Prices rose 5 percent.", Prose: "Prices rose."}}}
	reply := `{"stories":[{"id":"t0i0","headline":"Prices","paragraphs":["Prices ==rose 9 percent==."],"placement":"lead","needs_picture":true}]}`
	ch := editChapter(t.Context(), &fakeBackend{chapter: func(string, string) string { return reply }}, &pool{remaining: 1 << 30}, c)
	s := ch.Stories[0]
	if !s.Fallback || len(s.Highlights) != 0 {
		t.Errorf("story %+v, want a fallback without highlights", s)
	}
}

func TestChapterPromptAsksForPicturesAndHighlights(t *testing.T) {
	for _, want := range []string{`"needs_picture":false`, "==", "at most two", "never in a headline or standfirst", "spacecraft"} {
		if !strings.Contains(chapterSystem, want) {
			t.Errorf("chapter prompt lacks %q", want)
		}
	}
}
