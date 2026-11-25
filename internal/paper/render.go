// Package paper turns the editor's Paper into the book's pages.
package paper

import (
	"embed"
	"fmt"
	"html/template"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RafaelXokito/daily-brief/internal/brief"
	"github.com/RafaelXokito/daily-brief/internal/editor"
	"github.com/RafaelXokito/daily-brief/internal/epub"
)

// Meta is what the colophon reports about the edition. Over is the
// tokens spent beyond the budget, 0 when within it.
type Meta struct {
	Title              string
	Issue              int
	Model              string
	Spent, Budget      int64
	Over               int64
	Summarised, Failed int
	Cut                []string
	// Pictures of PictureCandidates stories with a preview image got one:
	// front page leads, chapter leads and stories that need a picture.
	Pictures, PictureCandidates int
}

//go:embed templates/*.xhtml
var templateFS embed.FS

var tmpl = template.Must(template.New("").ParseFS(templateFS, "templates/*.xhtml"))

var ordinals = []string{"First", "Second", "Third", "Fourth", "Fifth", "Sixth",
	"Seventh", "Eighth", "Ninth", "Tenth", "Eleventh", "Twelfth"}

// frontEntry is a lead or an In Brief sentence. A lead also carries its
// story's standfirst, the title of the chapter it continues in and its
// picture, "img/<name>" or "", Small when the picture prints small.
type frontEntry struct {
	Href, Text, Standfirst, Continued, Image string
	Paragraphs                               []string
	Act, Small                               bool
}

// frontGroup is a topic's In Brief paragraph under its title, "Of
// <Topic>", or "" when neither its stories nor its topic name one.
type frontGroup struct {
	Title     string
	Sentences []frontEntry
	chapter   int // where the group sorts: its first placed story's chapter
}

// placed is what the front page needs from a story in its chapter.
type placed struct {
	chapter     int // index among the rendered chapters
	title, lead string
	act         bool
}

// storyView is a story ready for the template. A lead's first paragraph,
// when it starts with a letter or digit, is split into DropCap and
// DropRest and left out of Paragraphs. URL is the source link, "" unless
// http(s). Small means Image or Pic prints small (a logo).
type storyView struct {
	ID, Headline, Standfirst, Dateline, Host, URL string
	DropCap                                       string
	DropRest                                      []segment
	Image                                         string // the lead's picture, "img/chN.jpg", or ""
	Pic                                           string // the picture of a story that needs one, "img/chN-K.jpg", or ""
	Paragraphs                                    [][]segment
	Act, Small                                    bool
}

type chapterView struct {
	Heading, Title string
	Main, Briefs   []storyView
}

type colophonView struct {
	Title, Model, Date, Spent, Budget, Over, Cut string
	Issue, Summarised, Failed, Fallbacks         int
	Pictures, PictureCandidates                  int
}

// Render returns the paper's pages in spine order: front.xhtml, one
// chN.xhtml per topic with stories, then colophon.xhtml. All text is
// stripped of control and invisible characters and escaped.
func Render(p editor.Paper, m Meta) []epub.Chapter {
	reg := newIDs()
	stories := map[string]placed{} // raw story id -> its first placement
	var chapters []epub.Chapter
	for _, c := range p.Chapters {
		if len(c.Stories) == 0 {
			continue
		}
		n := len(chapters) + 1
		file := fmt.Sprintf("ch%d.xhtml", n)
		heading, short := ordinal(n)
		title := "Of " + titleCase(clean(c.Topic))
		v := chapterView{Heading: heading, Title: title}
		for _, s := range orderStories(c.Stories) {
			sv := storyView{
				ID: reg.add(s.ID, file), Headline: clean(s.Headline), Standfirst: clean(s.Standfirst),
				Dateline: clean(s.Dateline), Host: clean(s.Host), URL: sourceURL(s.URL), Paragraphs: paragraphViews(s.Paragraphs, s.Highlights),
				Act: s.Tag == "ACT", Small: s.SmallImage,
			}
			if _, seen := stories[s.ID]; !seen {
				stories[s.ID] = placed{chapter: n - 1, title: title, lead: sv.Standfirst, act: sv.Act}
			}
			switch {
			case s.Image == "":
			case s.Placement == "lead":
				sv.Image = "img/" + s.Image
			case s.NeedsPicture:
				sv.Pic = "img/" + s.Image
			}
			if s.Placement == "lead" && len(sv.Paragraphs) > 0 {
				if c, rest := dropCapSegments(sv.Paragraphs[0]); c != "" {
					sv.DropCap, sv.DropRest, sv.Paragraphs = c, rest, sv.Paragraphs[1:]
				}
			}
			if s.Placement == "brief" {
				v.Briefs = append(v.Briefs, sv)
			} else {
				v.Main = append(v.Main, sv)
			}
		}
		chapters = append(chapters, epub.Chapter{File: file, TOCTitle: short + ": " + title, Body: execute("chapter.xhtml", v)})
	}
	out := []epub.Chapter{{File: "front.xhtml", TOCTitle: "The Day in Brief", Body: execute("front.xhtml", frontView(p.Front, reg, stories, len(chapters)))}}
	out = append(out, chapters...)
	return append(out, epub.Chapter{File: "colophon.xhtml", TOCTitle: "Colophon", Body: execute("colophon.xhtml", colophon(p, m))})
}

type frontPageView struct {
	Leads   []frontEntry
	InBrief []frontGroup
}

// frontView lays out the front page: the leads in the editor's order,
// then the In Brief paragraphs in the order of their first placed story's
// chapter, keeping the editor's order otherwise, and last the paragraphs
// with no story in the book.
func frontView(fp editor.FrontPage, reg ids, stories map[string]placed, chapters int) frontPageView {
	var v frontPageView
	for _, l := range fp.Leads {
		s := stories[l.ID]
		e := frontEntry{Href: reg.href[l.ID], Text: clean(l.Headline), Standfirst: s.lead,
			Continued: s.title, Paragraphs: cleanAll(l.Paragraphs), Act: s.act}
		if l.Image != "" {
			e.Image, e.Small = "img/"+l.Image, l.SmallImage
		}
		v.Leads = append(v.Leads, e)
	}
	for _, p := range fp.InBrief {
		if g, ok := briefGroup(p, reg, stories, chapters); ok {
			v.InBrief = append(v.InBrief, g)
		}
	}
	slices.SortStableFunc(v.InBrief, func(a, b frontGroup) int { return a.chapter - b.chapter })
	return v
}

// briefGroup is p ready for the template, titled after its first placed
// story's chapter, else after its own topic; false when no sentence has text.
func briefGroup(p editor.BriefParagraph, reg ids, stories map[string]placed, chapters int) (frontGroup, bool) {
	g := frontGroup{chapter: chapters}
	placedOne := false
	for _, b := range p.Sentences {
		text := clean(b.Text)
		if text == "" {
			continue
		}
		if s, ok := stories[b.ID]; ok && !placedOne {
			g.Title, g.chapter, placedOne = s.title, s.chapter, true
		}
		g.Sentences = append(g.Sentences, frontEntry{Href: reg.href[b.ID], Text: text})
	}
	if topic := clean(p.Topic); !placedOne && topic != "" {
		g.Title = "Of " + titleCase(topic)
	}
	return g, len(g.Sentences) > 0
}

// sourceURL is raw as a link target when it is an http(s) URL, else "".
func sourceURL(raw string) string {
	u, ok := brief.SafeWebURL(raw)
	if !ok {
		return ""
	}
	return u
}

func colophon(p editor.Paper, m Meta) colophonView {
	v := colophonView{
		Title: clean(m.Title), Model: clean(m.Model), Date: p.Date.Format("Monday, 2 January 2006"),
		Spent: HumanTokens(m.Spent), Cut: strings.Join(cleanAll(m.Cut), ", "),
		Issue: m.Issue, Summarised: m.Summarised, Failed: m.Failed, Fallbacks: p.Fallbacks,
		Pictures: m.Pictures, PictureCandidates: m.PictureCandidates,
	}
	if m.Budget > 0 {
		v.Budget = HumanTokens(m.Budget)
	}
	if m.Over > 0 {
		v.Over = HumanTokens(m.Over)
	}
	return v
}

func execute(name string, data any) string {
	var sb strings.Builder
	if err := tmpl.ExecuteTemplate(&sb, name, data); err != nil {
		// The templates are fixed and the data plain strings: a failure
		// is a bug in this package, not bad input.
		panic(fmt.Sprintf("paper: template %s: %v", name, err))
	}
	return sb.String()
}

// orderStories puts the lead first, then the other stories, then the
// brief notes, each group in the editor's order.
func orderStories(ss []editor.Story) []editor.Story {
	rank := func(s editor.Story) int {
		switch s.Placement {
		case "lead":
			return 0
		case "brief":
			return 2
		}
		return 1
	}
	out := slices.Clone(ss)
	slices.SortStableFunc(out, func(a, b editor.Story) int { return rank(a) - rank(b) })
	return out
}

// ordinal is a chapter's heading and its short form for the contents:
// "Chapter the First" and "First", up to the twelfth, then "Chapter 13"
// and "13".
func ordinal(n int) (heading, short string) {
	if n >= 1 && n <= len(ordinals) {
		return "Chapter the " + ordinals[n-1], ordinals[n-1]
	}
	return fmt.Sprintf("Chapter %d", n), fmt.Sprint(n)
}

// titleCase capitalises each word except "and", "of", "the" and "&"
// after the first, and leaves words already in capitals alone.
func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		switch {
		case i > 0 && (w == "and" || w == "of" || w == "the" || w == "&"):
		case strings.ToUpper(w) == w:
		default:
			r, size := utf8.DecodeRuneInString(w)
			words[i] = string(unicode.ToUpper(r)) + w[size:]
		}
	}
	return strings.Join(words, " ")
}

// HumanTokens is brief.HumanTokens without a trailing ".0": "12k", "12.5k".
func HumanTokens(n int64) string {
	s := brief.HumanTokens(n)
	for _, unit := range []string{"k", "M"} {
		s = strings.Replace(s, ".0"+unit, unit, 1)
	}
	return s
}

// clean makes s safe to place in XHTML text and trims it (sanitize).
func clean(s string) string { return strings.TrimSpace(sanitize(s)) }

// sanitize makes s safe to place in XHTML text: invalid UTF-8 is replaced,
// control and invisible characters (brief.StripControl) and the XML
// non-characters U+FFFE and U+FFFF are removed.
func sanitize(s string) string {
	s = brief.StripControl(strings.ToValidUTF8(s, "�"))
	return strings.Map(func(r rune) rune {
		if r == '￾' || r == '￿' {
			return -1
		}
		return r
	}, s)
}

func cleanAll(ss []string) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if c := clean(s); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// safeID keeps the ASCII letters, digits, '-' and '_' of id, starting
// with a letter, so the same value works as an XML id and a URL fragment.
func safeID(id string) string {
	id = strings.Map(func(r rune) rune {
		if r < utf8.RuneSelf && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_') {
			return r
		}
		return -1
	}, id)
	if id == "" || !unicode.IsLetter(rune(id[0])) {
		id = "s" + id
	}
	return id
}

// ids hands out unique element ids and remembers where each raw story
// id was placed, so a link follows the raw id, never a sanitised one that
// another story's id may also reduce to.
type ids struct {
	used map[string]bool   // element ids taken
	href map[string]string // raw story id -> "chN.xhtml#elementid", first story wins
}

func newIDs() ids { return ids{used: map[string]bool{}, href: map[string]string{}} }

// add returns a unique element id for raw, safeID(raw) suffixed if taken,
// and records its link target in file.
func (r ids) add(raw, file string) string {
	base := safeID(raw)
	out := base
	for n := 2; r.used[out]; n++ {
		out = fmt.Sprintf("%s-%d", base, n)
	}
	r.used[out] = true
	if _, ok := r.href[raw]; !ok {
		r.href[raw] = file + "#" + out
	}
	return out
}

// dropCap splits a paragraph for a drop cap: any leading punctuation
// (an opening quote) with the first letter or digit, and the rest. It
// returns "" and p when p does not start that way.
func dropCap(p string) (initial, rest string) {
	for i, r := range p {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			end := i + utf8.RuneLen(r)
			return p[:end], p[end:]
		case !unicode.IsPunct(r) || i > 8:
			return "", p
		}
	}
	return "", p
}
