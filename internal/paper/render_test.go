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
			InBrief: []editor.BriefParagraph{
				{Topic: "Java & Quarkus", Sentences: []editor.BriefSentence{{ID: "t0i1", Text: "A build tool gained a faster cache."}}},
				{Topic: "security", Sentences: []editor.BriefSentence{{ID: "t1i1", Text: "A password manager changed its pricing."}}},
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
		`<p><span class="drop">T</span>he maintainers published a minor release on Tuesday.</p>`,
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
	if n, want := strings.Count(ch1, `<p class="source">From example.com</p>`), 2; n != want {
		t.Errorf("chapter one has %d 'From example.com' lines, want %d", n, want)
	}
	if !strings.Contains(ch1, `<p class="source">From example.org</p>`) {
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
	if links != 6 {
		t.Errorf("found %d internal links, want 6 (two leads, their two continued lines, two In Brief lines)", links)
	}
}

func TestRenderUnknownFrontIDIsUnlinked(t *testing.T) {
	p := samplePaper()
	p.Front.InBrief[1].Sentences = append(p.Front.InBrief[1].Sentences, editor.BriefSentence{ID: "t9i9", Text: "An orphan line."})
	p.Front.InBrief = append(p.Front.InBrief, editor.BriefParagraph{Topic: "elsewhere", Sentences: []editor.BriefSentence{{ID: "t8i8", Text: "Another orphan."}}})
	front := Render(p, sampleMeta())[0].Body
	if !strings.Contains(front, "An orphan line.") || !strings.Contains(front, "Another orphan.") {
		t.Fatalf("orphan line dropped\n%s", front)
	}
	if strings.Contains(front, "t9i9") || strings.Contains(front, "t8i8") {
		t.Errorf("orphan line is linked:\n%s", front)
	}
	if !strings.Contains(front, `<h2 class="brief-topic">Of Elsewhere</h2>`) {
		t.Errorf("a paragraph whose stories are not in the book lacks its topic heading:\n%s", front)
	}
	assertXML(t, "front", front)
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
		if got := HumanTokens(n); got != want {
			t.Errorf("HumanTokens(%d) = %q, want %q", n, got, want)
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

func TestDropCap(t *testing.T) {
	tests := []struct{ in, cap, rest string }{
		{"The day", "T", "he day"},
		{"\u201cQuoted,\u201d she said", "\u201cQ", "uoted,\u201d she said"},
		{"\"Plain quote\"", "\"P", "lain quote\""},
		{"\u00c9t\u00e9 arrives", "\u00c9", "t\u00e9 arrives"},
		{"42 servers", "4", "2 servers"},
		{"\u2026", "", "\u2026"},
		{"", "", ""},
	}
	for _, tt := range tests {
		c, r := dropCap(tt.in)
		if c != tt.cap || r != tt.rest {
			t.Errorf("dropCap(%q) = %q, %q; want %q, %q", tt.in, c, r, tt.cap, tt.rest)
		}
	}
}

func TestRenderDropCapFallsBackWithoutALetter(t *testing.T) {
	p := samplePaper()
	p.Chapters[0].Stories[0].Paragraphs = []string{"\u2026 and so on.", "Next."}
	ch1 := Render(p, sampleMeta())[1].Body
	if strings.Contains(ch1, `class="drop"`) || !strings.Contains(ch1, "<p>\u2026 and so on.</p>") {
		t.Errorf("paragraph without a letter should render plainly:\n%s", ch1)
	}
}

func TestRenderSanitisedIDCollisionLinksTheRightStory(t *testing.T) {
	p := samplePaper()
	p.Chapters[0].Stories[1].ID = "a b"
	p.Chapters[1].Stories[1].ID = "ab"
	p.Front.InBrief = []editor.BriefParagraph{{Sentences: []editor.BriefSentence{{ID: "ab", Text: "Second."}, {ID: "a b", Text: "First."}}}}
	chs := Render(p, sampleMeta())
	if !strings.Contains(chs[1].Body, `id="ab"`) || !strings.Contains(chs[2].Body, `id="ab-2"`) {
		t.Fatalf("ids not deduplicated:\n%s\n%s", chs[1].Body, chs[2].Body)
	}
	front := chs[0].Body
	for _, want := range []string{`<a href="ch2.xhtml#ab-2">Second.</a>`, `<a href="ch1.xhtml#ab">First.</a>`} {
		if !strings.Contains(front, want) {
			t.Errorf("front lacks %s\n%s", want, front)
		}
	}
}

func TestRenderSourceLine(t *testing.T) {
	p := samplePaper()
	p.Chapters[0].Stories[0].URL = "https://example.com/a?x=1&y=2"
	p.Chapters[0].Stories[2].Host = ""
	chs := Render(p, sampleMeta())
	ch1 := chs[1].Body

	want := `<p class="source"><a href="https://example.com/a?x=1&amp;y=2">[source]</a> From example.com</p>`
	if !strings.Contains(ch1, want) {
		t.Errorf("lead lacks %s\n%s", want, ch1)
	}
	lead := ch1[strings.Index(ch1, `id="t0i0"`):strings.Index(ch1, `<p class="sep">`)]
	if !strings.HasSuffix(strings.TrimSpace(lead), want) {
		t.Errorf("the source line does not end the story:\n%s", lead)
	}
	if !strings.Contains(ch1, `<p class="source">From example.com</p>`) {
		t.Errorf("a story without a URL lacks its plain source line:\n%s", ch1)
	}
	if n := strings.Count(ch1, `class="source"`); n != 2 {
		t.Errorf("chapter one has %d source lines, want 2 (the brief note has neither URL nor host)", n)
	}
	if n := strings.Count(ch1, "[source]"); n != 1 {
		t.Errorf("chapter one has %d [source] links, want 1", n)
	}
	if strings.Contains(chs[0].Body, "[source]") {
		t.Errorf("the front page carries a [source] link:\n%s", chs[0].Body)
	}
	assertXML(t, "ch1", ch1)
}

func TestRenderFrontLayout(t *testing.T) {
	p := samplePaper()
	// The second chapter's paragraph listed before the first's.
	p.Front.InBrief = []editor.BriefParagraph{
		{Topic: "security", Sentences: []editor.BriefSentence{{ID: "t1i1", Text: "A password manager changed its pricing."}}},
		{Topic: "Java & Quarkus", Sentences: []editor.BriefSentence{
			{ID: "t0i2", Text: "Conference dates were announced."},
			{ID: "t0i1", Text: "A build tool gained a faster cache."},
		}},
	}
	chs := Render(p, sampleMeta())
	front := chs[0].Body

	for _, want := range []string{
		`<p class="standfirst">Fewer features, more fixes</p>`,
		`<p class="continued"><a href="ch1.xhtml#t0i0">continued in Of Java &amp; Quarkus</a></p>`,
		`<p class="continued"><a href="ch2.xhtml#t1i0">continued in Of Security</a></p>`,
		`<h2 class="tier">In Brief</h2>`,
		`<h2 class="brief-topic">Of Java &amp; Quarkus</h2>`,
		`<h2 class="brief-topic">Of Security</h2>`,
		`<p class="brief"><a href="ch1.xhtml#t0i2">Conference dates were announced.</a> <a href="ch1.xhtml#t0i1">A build tool gained a faster cache.</a></p>`,
		`<p class="brief"><a href="ch2.xhtml#t1i1">A password manager changed its pricing.</a></p>`,
	} {
		if !strings.Contains(front, want) {
			t.Errorf("front page lacks %s\n%s", want, front)
		}
	}
	if n := strings.Count(front, "⁂"); n != 1 {
		t.Errorf("front page has %d ⁂, want 1 between its two leads", n)
	}
	if n := strings.Count(front, `<p class="brief">`); n != 2 {
		t.Errorf("front page has %d In Brief paragraphs, want 2 (one per topic)", n)
	}
	if n := strings.Count(front, `<p class="brief-fl">`); n != 2 {
		t.Errorf("front page has %d topic fleurons, want 2 (one per topic heading)", n)
	}
	for _, gone := range []string{"topic-head", "topic-rule", `class="hl"`, "Act now."} {
		if strings.Contains(front, gone) {
			t.Errorf("front page still has %q\n%s", gone, front)
		}
	}
	order := []string{
		"Framework ships a quieter release", "continued in Of Java", "⁂", "Patch the gateway today", "continued in Of Security",
		"In Brief", `brief-topic">Of Java`, "brief-fl", "Conference dates", "A build tool gained", `brief-topic">Of Security`, "A password manager",
	}
	last := -1
	for _, s := range order {
		i := strings.Index(front, s)
		if i <= last {
			t.Fatalf("%q is out of place on the front page:\n%s", s, front)
		}
		last = i
	}
	assertXML(t, "front", front)
}

// A front lead's picture shows right under its headline, full width for
// the first lead and smaller for the others; a lead without one shows
// nothing in its place.
func TestRenderFrontLeadPictures(t *testing.T) {
	p := samplePaper()
	p.Front.Leads[0].Image = "front1.jpg"
	p.Front.Leads[1].Image = "ch2.jpg"

	front := Render(p, sampleMeta())[0].Body

	for _, tc := range []struct{ img, head, next string }{
		{`<img class="lead-main" src="img/front1.jpg" alt="Framework ships a quieter release"/>`,
			`Framework ships a quieter release</a></h2>`, `<p class="standfirst">Fewer features`},
		{`<img class="lead-pic" src="img/ch2.jpg" alt="Patch the gateway today"/>`,
			`Patch the gateway today</a></h2>`, `<p class="tag act">`},
	} {
		h, i, n := strings.Index(front, tc.head), strings.Index(front, tc.img), strings.Index(front, tc.next)
		if i < 0 || h < 0 || !(h < i && i < n) {
			t.Errorf("%s not right under %s\n%s", tc.img, tc.head, front)
		}
	}
	if n := strings.Count(front, "<img"); n != 2 {
		t.Errorf("front page has %d pictures, want 2", n)
	}
	assertXML(t, "front", front)
}

// A small (logo-like) picture prints floated at a fifth of the width
// wherever it appears: as the front page's main picture, as another
// front lead's, as a chapter lead's and as a story's that needs one.
func TestRenderSmallPictures(t *testing.T) {
	p := samplePaper()
	p.Front.Leads[0].Image, p.Front.Leads[0].SmallImage = "ch1.jpg", true
	p.Front.Leads[1].Image, p.Front.Leads[1].SmallImage = "ch2.jpg", true
	lead := &p.Chapters[0].Stories[0]
	lead.Image, lead.SmallImage = "ch1.jpg", true
	s := &p.Chapters[0].Stories[1]
	s.NeedsPicture, s.Image, s.SmallImage = true, "ch1-1.jpg", true
	chs := Render(p, sampleMeta())
	front, ch1 := chs[0].Body, chs[1].Body
	for _, want := range []string{
		`<img class="small-pic" src="img/ch1.jpg" alt="Framework ships a quieter release"/>`,
		`<img class="small-pic" src="img/ch2.jpg" alt="Patch the gateway today"/>`,
	} {
		if !strings.Contains(front, want) {
			t.Errorf("front page lacks %s\n%s", want, front)
		}
	}
	if strings.Contains(front, "lead-main") || strings.Contains(front, "lead-pic") {
		t.Errorf("a small picture printed large on the front page\n%s", front)
	}
	for _, want := range []string{
		`<img class="small-pic" src="img/ch1.jpg" alt="Framework ships a quieter release"/>`,
		`<img class="small-pic" src="img/ch1-1.jpg" alt="Build tool gains a faster cache"/>`,
	} {
		if !strings.Contains(ch1, want) {
			t.Errorf("chapter one lacks %s\n%s", want, ch1)
		}
	}
	if strings.Contains(ch1, `class="pic"`) || strings.Contains(ch1, "story-pic") {
		t.Errorf("a small picture printed large in chapter one\n%s", ch1)
	}
	assertXML(t, "front", front)
	assertXML(t, "ch1.xhtml", ch1)
}

// Review Focus 5: no lead with a picture leaves no picture element, empty
// wrapper or picture class behind.
func TestRenderFrontLeadsWithoutPictures(t *testing.T) {
	p := samplePaper()
	p.Front.Leads[1].Image = "ch2.jpg"
	front := Render(p, sampleMeta())[0].Body
	if strings.Contains(front, "lead-main") || strings.Count(front, "<img") != 1 {
		t.Errorf("only the second lead has a picture, want one lead-pic and no lead-main\n%s", front)
	}

	front = Render(samplePaper(), sampleMeta())[0].Body

	for _, gone := range []string{"<img", "lead-main", "lead-pic", "<figure", `class="pic"`, `src=""`} {
		if strings.Contains(front, gone) {
			t.Errorf("front page without pictures has %q\n%s", gone, front)
		}
	}
	assertXML(t, "front", front)
}

func TestRenderLeadImageOnly(t *testing.T) {
	p := samplePaper()
	p.Chapters[0].Stories[0].Image = "ch1.jpg"
	p.Chapters[0].Stories[1].Image = "stray.jpg" // not the lead: never shown
	chs := Render(p, sampleMeta())
	ch1 := chs[1].Body
	if n := strings.Count(ch1, "<img"); n != 1 {
		t.Fatalf("chapter one has %d pictures, want 1\n%s", n, ch1)
	}
	img := `<img src="img/ch1.jpg" alt="Framework ships a quieter release"/>`
	head := `<h2 class="story" id="t0i0">Framework ships a quieter release</h2>`
	i, h, s := strings.Index(ch1, img), strings.Index(ch1, head), strings.Index(ch1, `<p class="standfirst">`)
	if i < 0 || h < 0 || !(h < i && i < s) {
		t.Errorf("picture not right after the lead's headline\n%s", ch1)
	}
	assertXML(t, "ch1.xhtml", ch1)
	for _, c := range chs {
		if c.File != "ch1.xhtml" && strings.Contains(c.Body, "<img") {
			t.Errorf("%s has a picture\n%s", c.File, c.Body)
		}
	}
}

func TestRenderColophonPictures(t *testing.T) {
	m := sampleMeta()
	if body := Render(samplePaper(), m)[3].Body; strings.Contains(body, "ictures") {
		t.Errorf("colophon mentions pictures when there were none to fetch\n%s", body)
	}
	m.Pictures, m.PictureCandidates = 1, 3
	if body := Render(samplePaper(), m)[3].Body; !strings.Contains(body, "Pictures: 1 of 3 with a preview image.") {
		t.Errorf("colophon lacks the picture count\n%s", body)
	}
}
