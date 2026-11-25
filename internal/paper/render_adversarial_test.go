package paper

import (
	"strings"
	"testing"
	"unicode"

	"github.com/RafaelXokito/daily-brief/internal/config"
)

func TestRenderEscapes(t *testing.T) {
	const hostile = "A & B <script>\x07‮</script>"
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

	chs := Render(p, m)
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
