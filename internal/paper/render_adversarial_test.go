package paper

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
	"unicode"

	"github.com/RafaelXokito/daily-brief/internal/config"
	"github.com/RafaelXokito/daily-brief/internal/editor"
	"github.com/RafaelXokito/daily-brief/internal/epub"
)

const hostile = "A & B <script>\x07\u202e</script>"

// hostilePaper puts markup, control, bidi and invalid characters everywhere.
func hostilePaper() (editor.Paper, Meta) {
	p := samplePaper()
	p.Front.Leads[0].Headline = hostile
	p.Front.Leads[0].Paragraphs = []string{hostile, "bad utf8 \xff\xfe here￿"}
	p.Front.InBrief[0].Sentence = hostile
	p.Front.InBrief[1].ID = `t1i1" onclick="x`
	s := &p.Chapters[0].Stories[0]
	s.Headline, s.Standfirst, s.Dateline, s.Host = hostile, hostile, hostile, "evil.example‮<b>"
	s.Paragraphs = []string{hostile, "]]> <![CDATA[ x"}
	p.Chapters[0].Topic = hostile
	m := sampleMeta()
	m.Title, m.Model, m.Cut = hostile, hostile, []string{hostile}
	return p, m
}

func TestRenderEscapes(t *testing.T) {
	chs := Render(hostilePaper())
	for _, c := range chs {
		assertXML(t, c.File, c.Body)
		for _, field := range []string{c.Body, c.TOCTitle} {
			for _, r := range field {
				if (unicode.IsControl(r) && r != '\n' && r != '\t') || config.IsInvisible(r) || r == '￿' || r == '￾' {
					t.Errorf("%s contains %U", c.File, r)
				}
			}
		}
		if strings.Contains(c.Body, "<script>") || strings.Contains(c.Body, "<b>") || strings.Contains(c.Body, `onclick="`) {
			t.Errorf("%s contains raw markup from the input:\n%s", c.File, c.Body)
		}
	}
	for _, c := range chs[:2] {
		if !strings.Contains(c.Body, "A &amp; B &lt;script&gt;") {
			t.Errorf("%s lacks the escaped headline:\n%s", c.File, c.Body)
		}
	}
	// TOC titles are plain text; Build escapes them.
	if !strings.Contains(chs[1].TOCTitle, "A & B <script>") {
		t.Errorf("TOC title %q lost its text", chs[1].TOCTitle)
	}
	if !strings.Contains(chs[3].Body, "A &amp; B &lt;script&gt;") {
		t.Errorf("colophon lacks the escaped model or cut topic:\n%s", chs[3].Body)
	}
}

func TestRenderHostileIDsStillResolve(t *testing.T) {
	p := samplePaper()
	p.Chapters[0].Stories[0].ID = `t0 i0"><x`
	p.Front.Leads[0].ID = `t0 i0"><x`
	chs := Render(p, sampleMeta())
	for _, c := range chs {
		assertXML(t, c.File, c.Body)
	}
	ids := map[string]bool{}
	for _, c := range chs {
		for _, m := range idRE.FindAllStringSubmatch(c.Body, -1) {
			ids[c.File+"#"+m[1]] = true
		}
	}
	for _, c := range chs {
		for _, m := range hrefRE.FindAllStringSubmatch(c.Body, -1) {
			if !ids[m[1]+"#"+m[2]] {
				t.Errorf("%s links to %s#%s, which does not exist", c.File, m[1], m[2])
			}
		}
	}
}

func TestRenderHostileBookRoundTrip(t *testing.T) {
	p, m := hostilePaper()
	data, err := epub.Build(epub.Book{Title: hostile, Creator: hostile, Identifier: "urn:test:" + hostile,
		Date: p.Date, Cover: []byte("\x89PNG"), Chapters: Render(p, m)})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	checked := 0
	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, ".xhtml") && !strings.HasSuffix(f.Name, ".opf") &&
			!strings.HasSuffix(f.Name, ".ncx") && !strings.HasSuffix(f.Name, ".xml") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		d := xml.NewDecoder(rc)
		d.Strict = true
		for {
			_, err := d.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Errorf("%s does not parse: %v", f.Name, err)
				break
			}
		}
		rc.Close()
		checked++
	}
	// container, OPF, NCX, nav, cover, front, two chapters, colophon.
	if checked != 9 {
		t.Errorf("checked %d XML files, want 9", checked)
	}
}
