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
}

//go:embed templates/*.xhtml
var templateFS embed.FS

var tmpl = template.Must(template.New("").ParseFS(templateFS, "templates/*.xhtml"))

var ordinals = []string{"First", "Second", "Third", "Fourth", "Fifth", "Sixth",
	"Seventh", "Eighth", "Ninth", "Tenth", "Eleventh", "Twelfth"}

type frontEntry struct {
	Href, Text string
	Paragraphs []string
	Act        bool
}

// storyView is a story ready for the template. A lead's first paragraph,
// when it starts with a letter or digit, is split into DropCap and
// DropRest and left out of Paragraphs.
type storyView struct {
	ID, Headline, Standfirst, Dateline, Host string
	DropCap, DropRest                        string
	Paragraphs                               []string
	Act                                      bool
}

type chapterView struct {
	Heading, Title string
	Main, Briefs   []storyView
}

type colophonView struct {
	Title, Model, Date, Spent, Budget, Over, Cut string
	Issue, Summarised, Failed, Fallbacks         int
}

// Render returns the paper's pages in spine order: front.xhtml, one
// chN.xhtml per topic with stories, then colophon.xhtml. All text is
// stripped of control and invisible characters and escaped.
func Render(p editor.Paper, m Meta) []epub.Chapter {
	reg := newIDs()
	acts := map[string]bool{} // raw story id -> tagged ACT
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
				Dateline: clean(s.Dateline), Host: clean(s.Host), Paragraphs: cleanAll(s.Paragraphs),
				Act: s.Tag == "ACT",
			}
			if _, seen := acts[s.ID]; !seen {
				acts[s.ID] = sv.Act
			}
			if s.Placement == "lead" && len(sv.Paragraphs) > 0 {
				if c, rest := dropCap(sv.Paragraphs[0]); c != "" {
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
	front := struct{ Leads, InBrief []frontEntry }{}
	for _, l := range p.Front.Leads {
		front.Leads = append(front.Leads, frontEntry{Href: reg.href[l.ID], Text: clean(l.Headline), Paragraphs: cleanAll(l.Paragraphs), Act: acts[l.ID]})
	}
	for _, l := range p.Front.InBrief {
		front.InBrief = append(front.InBrief, frontEntry{Href: reg.href[l.ID], Text: clean(l.Sentence), Act: acts[l.ID]})
	}
	out := []epub.Chapter{{File: "front.xhtml", TOCTitle: "The Day in Brief", Body: execute("front.xhtml", front)}}
	out = append(out, chapters...)
	return append(out, epub.Chapter{File: "colophon.xhtml", TOCTitle: "Colophon", Body: execute("colophon.xhtml", colophon(p, m))})
}

func colophon(p editor.Paper, m Meta) colophonView {
	v := colophonView{
		Title: clean(m.Title), Model: clean(m.Model), Date: p.Date.Format("Monday, 2 January 2006"),
		Spent: HumanTokens(m.Spent), Cut: strings.Join(cleanAll(m.Cut), ", "),
		Issue: m.Issue, Summarised: m.Summarised, Failed: m.Failed, Fallbacks: p.Fallbacks,
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

// clean makes s safe to place in XHTML text: invalid UTF-8 is replaced,
// control and invisible characters (brief.StripControl) and the XML
// non-characters U+FFFE and U+FFFF are removed.
func clean(s string) string {
	s = brief.StripControl(strings.ToValidUTF8(s, "�"))
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '￾' || r == '￿' {
			return -1
		}
		return r
	}, s))
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
