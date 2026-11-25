package paper

import (
	"strings"
	"testing"

	"github.com/RafaelXokito/daily-brief/internal/editor"
)

// span is the Highlight over the first occurrence of phrase in paragraph para.
func span(t *testing.T, s editor.Story, para int, phrase string) editor.Highlight {
	t.Helper()
	i := strings.Index(s.Paragraphs[para], phrase)
	if i < 0 {
		t.Fatalf("%q not in %q", phrase, s.Paragraphs[para])
	}
	return editor.Highlight{Para: para, Start: i, End: i + len(phrase)}
}

func TestRenderHighlights(t *testing.T) {
	p := samplePaper()
	story := &p.Chapters[0].Stories[1]
	story.Highlights = []editor.Highlight{span(t, *story, 0, "survives restarts")}
	lead := &p.Chapters[0].Stories[0]
	lead.Highlights = []editor.Highlight{span(t, *lead, 0, "The maintainers"), span(t, *lead, 1, "optional")}
	ch1 := Render(p, sampleMeta())[1].Body
	for _, want := range []string{
		`<p>The cache now <span class="key">survives restarts</span>.</p>`,
		`<p><span class="drop">T</span><span class="key">he maintainers</span> published a minor release on Tuesday.</p>`,
		`<p>Upgrades are <span class="key">optional</span>.</p>`,
	} {
		if !strings.Contains(ch1, want) {
			t.Errorf("chapter one lacks %s\n%s", want, ch1)
		}
	}
	if strings.Contains(ch1, "<mark") {
		t.Errorf("a highlight uses <mark>, which the Kobo does not show\n%s", ch1)
	}
	assertXML(t, "ch1.xhtml", ch1)
	if front := Render(p, sampleMeta())[0].Body; strings.Contains(front, `class="key"`) {
		t.Errorf("the front page has a highlight\n%s", front)
	}
}

func TestRenderHighlightEscapes(t *testing.T) {
	p := samplePaper()
	s := &p.Chapters[0].Stories[1]
	s.Paragraphs = []string{"Before <script>alert(\"x\")</script> & \x07after."}
	s.Highlights = []editor.Highlight{span(t, *s, 0, "<script>alert(\"x\")</script> & \x07after")}
	ch1 := Render(p, sampleMeta())[1].Body
	want := `<p>Before <span class="key">&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt; &amp; after</span>.</p>`
	if !strings.Contains(ch1, want) {
		t.Errorf("chapter one lacks %s\n%s", want, ch1)
	}
	if strings.Contains(ch1, "<script>") {
		t.Errorf("raw markup in the chapter\n%s", ch1)
	}
	assertXML(t, "ch1.xhtml", ch1)
}

// Highlights that do not fit their paragraph are ignored and the text is
// printed whole.
func TestRenderIgnoresBadHighlights(t *testing.T) {
	text := "Évora raised the fee."
	for name, h := range map[string]editor.Highlight{
		"past the end":       {Para: 0, Start: 3, End: 99},
		"negative":           {Para: 0, Start: -1, End: 3},
		"empty":              {Para: 0, Start: 4, End: 4},
		"reversed":           {Para: 0, Start: 6, End: 2},
		"inside a character": {Para: 0, Start: 1, End: 6},
		"no such paragraph":  {Para: 3, Start: 0, End: 2},
		"negative paragraph": {Para: -1, Start: 0, End: 2},
	} {
		p := samplePaper()
		s := &p.Chapters[0].Stories[1]
		s.Paragraphs = []string{text}
		s.Highlights = []editor.Highlight{h}
		ch1 := Render(p, sampleMeta())[1].Body
		if strings.Contains(ch1, `class="key"`) || !strings.Contains(ch1, "<p>"+text+"</p>") {
			t.Errorf("%s: want the paragraph whole and unmarked\n%s", name, ch1)
		}
	}
	p := samplePaper()
	s := &p.Chapters[0].Stories[1]
	s.Paragraphs = []string{text}
	s.Highlights = []editor.Highlight{{Para: 0, Start: 0, End: 9}, {Para: 0, Start: 4, End: 12}} // overlapping: the second goes
	ch1 := Render(p, sampleMeta())[1].Body
	if !strings.Contains(ch1, `<p><span class="key">Évora ra</span>ised the fee.</p>`) {
		t.Errorf("overlapping highlights: want only the first\n%s", ch1)
	}
}

func TestRenderStoryPicture(t *testing.T) {
	p := samplePaper()
	s := &p.Chapters[0].Stories[1]
	s.Standfirst, s.NeedsPicture, s.Image = "A cache that lasts", true, "ch1-1.jpg"
	brief := &p.Chapters[0].Stories[2]
	brief.NeedsPicture, brief.Image = true, "ch1-2.jpg"
	ch1 := Render(p, sampleMeta())[1].Body
	img := `<p class="pic"><img class="story-pic" src="img/ch1-1.jpg" alt="Build tool gains a faster cache"/></p>`
	stand := `<p class="standfirst">A cache that lasts</p>`
	body := `<p>The cache now survives restarts.</p>`
	i, sf, b := strings.Index(ch1, img), strings.Index(ch1, stand), strings.Index(ch1, body)
	if i < 0 || sf < 0 || !(sf < i && i < b) {
		t.Errorf("story picture not between the standfirst and the text\n%s", ch1)
	}
	if !strings.Contains(ch1, `<img class="story-pic" src="img/ch1-2.jpg" alt="Conference dates announced"/>`) {
		t.Errorf("brief story's picture missing\n%s", ch1)
	}
	if n := strings.Count(ch1, "<img"); n != 2 {
		t.Errorf("chapter one has %d pictures, want 2\n%s", n, ch1)
	}
	assertXML(t, "ch1.xhtml", ch1)
}
