package paper

import (
	"bytes"
	"encoding/xml"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/RafaelXokito/daily-brief/internal/editor"
)

// samplePaper is invented text, two chapters and a front page.
func samplePaper() editor.Paper {
	return editor.Paper{
		Date: time.Date(2026, 11, 25, 6, 0, 0, 0, time.UTC),
		Front: editor.FrontPage{
			Leads: []editor.FrontLead{
				{ID: "t0i0", Headline: "Framework ships a quieter release", Paragraphs: []string{"The maintainers published a minor release.", "Upgrades are optional."}},
				{ID: "t1i0", Headline: "Patch the gateway today", Paragraphs: []string{"A flaw in a popular gateway is being exploited."}},
			},
			InBrief: []editor.FrontLine{
				{ID: "t0i1", Sentence: "A build tool gained a faster cache."},
				{ID: "t1i1", Sentence: "A password manager changed its pricing."},
			},
			CoverLine: "Patch the gateway today",
		},
		Chapters: []editor.Chapter{
			{Topic: "Java & Quarkus", Stories: []editor.Story{
				{ID: "t0i0", Headline: "Framework ships a quieter release", Standfirst: "Fewer features, more fixes", Dateline: "AMSTERDAM",
					Paragraphs: []string{"The maintainers published a minor release on Tuesday.", "Upgrades are optional."}, Placement: "lead", Host: "example.com", Tag: "FYI"},
				{ID: "t0i1", Headline: "Build tool gains a faster cache", Paragraphs: []string{"The cache now survives restarts."}, Placement: "story", Host: "example.com"},
				{ID: "t0i2", Headline: "Conference dates announced", Paragraphs: []string{"The spring conference will be held in May."}, Placement: "brief", Host: "example.org", Tag: "WATCH"},
			}},
			{Topic: "security", Stories: []editor.Story{
				{ID: "t1i0", Headline: "Gateway flaw under attack", Standfirst: "Patch now", Paragraphs: []string{"Attackers are exploiting a flaw.", "A fix is available."}, Placement: "lead", Host: "example.net", Tag: "ACT"},
				{ID: "t1i1", Headline: "Password manager changes pricing", Paragraphs: []string{"Plans cost more from January."}, Placement: "brief", Host: "example.com"},
			}},
		},
		Fallbacks: 1,
	}
}

func sampleMeta() Meta {
	return Meta{Title: "The Daily Brief", Issue: 12, Model: "claude-test", Spent: 250000, Budget: 400000, Summarised: 4, Failed: 1}
}

func TestRenderStructure(t *testing.T) {
	chs := Render(samplePaper(), sampleMeta())
	var files, titles []string
	for _, c := range chs {
		files = append(files, c.File)
		titles = append(titles, c.TOCTitle)
	}
	if got, want := strings.Join(files, ","), "front.xhtml,ch1.xhtml,ch2.xhtml,colophon.xhtml"; got != want {
		t.Errorf("files %s, want %s", got, want)
	}
	if got, want := strings.Join(titles, "|"), "The Day in Brief|First: Of Java & Quarkus|Second: Of Security|Colophon"; got != want {
		t.Errorf("TOC titles %s, want %s", got, want)
	}
	ch1 := chs[1].Body
	for _, want := range []string{
		`<p class="chap">Chapter the First</p>`,
		`<h1>Of Java &amp; Quarkus</h1>`,
		`<p class="fl">❧</p>`,
		`<h2 class="story" id="t0i0">Framework ships a quieter release</h2>`,
		`<p class="standfirst">Fewer features, more fixes</p>`,
		`<p class="dateline">AMSTERDAM</p>`,
		`<p class="drop">The maintainers published a minor release on Tuesday.</p>`,
		`<h2 class="tier">In Brief</h2>`,
	} {
		if !strings.Contains(ch1, want) {
			t.Errorf("chapter one lacks %s\n%s", want, ch1)
		}
	}
	if n := strings.Count(ch1, `class="drop"`); n != 1 {
		t.Errorf("chapter one has %d drop caps, want 1 (the lead's first paragraph)", n)
	}
	// Two main stories, then In Brief with one note: one fleuron between the two main stories.
	if n := strings.Count(ch1, "⁂"); n != 1 {
		t.Errorf("chapter one has %d ⁂, want 1", n)
	}
	if strings.Index(ch1, "In Brief") > strings.Index(ch1, "Conference dates announced") ||
		strings.Index(ch1, "In Brief") < strings.Index(ch1, "Build tool gains") {
		t.Errorf("In Brief heading is not between the stories and the brief notes")
	}
	if n, want := strings.Count(ch1, `<p class="srcline">From example.com</p>`), 2; n != want {
		t.Errorf("chapter one has %d 'From example.com' lines, want %d", n, want)
	}
	if !strings.Contains(ch1, `<p class="srcline">From example.org</p>`) {
		t.Errorf("brief note lacks its source line")
	}
	if !strings.Contains(chs[2].Body, `<p class="chap">Chapter the Second</p>`) || !strings.Contains(chs[2].Body, "<h1>Of Security</h1>") {
		t.Errorf("chapter two heading wrong:\n%s", chs[2].Body)
	}
	front := chs[0].Body
	for _, want := range []string{"<h1>The Day in Brief</h1>", "The maintainers published a minor release.", "A build tool gained a faster cache."} {
		if !strings.Contains(front, want) {
			t.Errorf("front page lacks %s\n%s", want, front)
		}
	}
	col := chs[3].Body
	for _, want := range []string{"<h1>Of This Edition</h1>", "Wednesday, 25 November 2026", "claude-test", "250k", "400k",
		"IM Fell English", "UnifrakturMaguntia", "SIL Open Font License"} {
		if !strings.Contains(col, want) {
			t.Errorf("colophon lacks %s\n%s", want, col)
		}
	}
	if strings.Contains(col, "over by") {
		t.Errorf("colophon reports an overshoot that did not happen")
	}
	for _, c := range chs {
		if strings.ContainsRune(c.Body, '\u2014') {
			t.Errorf("%s contains an em dash", c.File)
		}
		assertXML(t, c.File, c.Body)
	}
}

func TestRenderOrdinals(t *testing.T) {
	tests := []struct {
		n            int
		heading, toc string
	}{
		{1, "Chapter the First", "First"},
		{3, "Chapter the Third", "Third"},
		{12, "Chapter the Twelfth", "Twelfth"},
		{13, "Chapter 13", "13"},
	}
	for _, tt := range tests {
		h, toc := ordinal(tt.n)
		if h != tt.heading || toc != tt.toc {
			t.Errorf("ordinal(%d) = %q, %q; want %q, %q", tt.n, h, toc, tt.heading, tt.toc)
		}
	}
}

func TestTitleCase(t *testing.T) {
	tests := map[string]string{
		"Java & Quarkus":     "Java & Quarkus",
		"world & hot topics": "World & Hot Topics",
		"state of the AI":    "State of the AI",
		"the internet":       "The Internet",
		"élections":          "Élections",
	}
	for in, want := range tests {
		if got := titleCase(in); got != want {
			t.Errorf("titleCase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderSkipsEmptyChapters(t *testing.T) {
	p := samplePaper()
	p.Chapters = append([]editor.Chapter{{Topic: "Empty"}}, p.Chapters...)
	chs := Render(p, sampleMeta())
	if len(chs) != 4 || chs[1].TOCTitle != "First: Of Java & Quarkus" {
		t.Errorf("empty chapter not skipped: %d chapters, first %q", len(chs), chs[1].TOCTitle)
	}
}

var (
	hrefRE = regexp.MustCompile(`href="([^"#]+)#([^"]+)"`)
	idRE   = regexp.MustCompile(`id="([^"]+)"`)
)

func TestRenderLinksResolve(t *testing.T) {
	chs := Render(samplePaper(), sampleMeta())
	ids := map[string]bool{}
	for _, c := range chs {
		for _, m := range idRE.FindAllStringSubmatch(c.Body, -1) {
			ids[c.File+"#"+m[1]] = true
		}
	}
	links := 0
	for _, c := range chs {
		for _, m := range hrefRE.FindAllStringSubmatch(c.Body, -1) {
			links++
			if !ids[m[1]+"#"+m[2]] {
				t.Errorf("%s links to %s#%s, which does not exist", c.File, m[1], m[2])
			}
		}
	}
	if links != 4 {
		t.Errorf("found %d internal links, want 4 (two leads, two In Brief lines)", links)
	}
}

func TestRenderUnknownFrontIDIsUnlinked(t *testing.T) {
	p := samplePaper()
	p.Front.InBrief = append(p.Front.InBrief, editor.FrontLine{ID: "t9i9", Sentence: "An orphan line."})
	front := Render(p, sampleMeta())[0].Body
	if !strings.Contains(front, "An orphan line.") {
		t.Fatalf("orphan line dropped")
	}
	if strings.Contains(front, "t9i9") {
		t.Errorf("orphan line is linked:\n%s", front)
	}
}

func TestRenderColophonOvershoot(t *testing.T) {
	m := sampleMeta()
	m.Spent, m.Budget, m.Over = 412000, 400000, 12000
	m.Cut = []string{"World & hot topics"}
	col := Render(samplePaper(), m)[3].Body
	for _, want := range []string{"412k", "400k", "over by 12k", "World &amp; hot topics"} {
		if !strings.Contains(col, want) {
			t.Errorf("colophon lacks %q\n%s", want, col)
		}
	}
}

func TestHumanTokens(t *testing.T) {
	tests := map[int64]string{0: "0", 950: "950", 12000: "12k", 12500: "12.5k", 412000: "412k", 2000000: "2M", 1250000: "1.2M"}
	for n, want := range tests {
		if got := humanTokens(n); got != want {
			t.Errorf("humanTokens(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestRenderActLabel(t *testing.T) {
	chs := Render(samplePaper(), sampleMeta())
	front := chs[0].Body
	act := `<p class="tag act">Act now</p>`
	i := strings.Index(front, "Patch the gateway today")
	j := strings.Index(front, act)
	if i < 0 || j < i {
		t.Errorf("front page lead from an ACT item lacks %s after its headline\n%s", act, front)
	}
	if strings.Count(front, "Act now") != 1 {
		t.Errorf("front page shows Act now %d times, want once (only the ACT lead)", strings.Count(front, "Act now"))
	}
	if !strings.Contains(chs[2].Body, act) {
		t.Errorf("ACT story in its chapter lacks the label")
	}
	if strings.Contains(chs[1].Body, "Act now") || strings.Contains(chs[1].Body, `class="tag`) {
		t.Errorf("FYI or WATCH story shows a label:\n%s", chs[1].Body)
	}
}

// assertXML fails unless body parses as well-formed XML inside a body element.
func assertXML(t *testing.T, name, body string) {
	t.Helper()
	d := xml.NewDecoder(bytes.NewReader([]byte("<body>" + body + "</body>")))
	d.Strict = true
	for {
		_, err := d.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Errorf("%s is not well-formed XML: %v\n%s", name, err, body)
			return
		}
	}
}
