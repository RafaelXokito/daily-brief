package editor

import (
	"strings"

	"github.com/RafaelXokito/daily-brief/internal/brief"
)

const maxLeads = 2

var plainer = strings.NewReplacer("`", "", " · ", "; ", "·", ";")

func plain(s string) string { return strings.TrimSpace(plainer.Replace(s)) }

// FallbackStory is the item as a story without the model: its facts as the
// standfirst and its prose as the body.
func FallbackStory(it ItemIn, placement string) Story {
	var paras []string
	for _, p := range strings.Split(it.Prose, "\n\n") {
		if p = plain(p); p != "" {
			paras = append(paras, p)
		}
	}
	return Story{
		ID: it.ID, Headline: plain(it.Headline), Standfirst: plain(it.Facts),
		Paragraphs: paras, Placement: placement, Host: it.Host, Tag: it.Tag, URL: it.URL, Fallback: true,
	}
}

// FallbackFront builds the front page without the model. Leads are the
// first two "act" highlights, else the first two items; In Brief is one
// paragraph per topic of every other item's headline as a sentence; the
// cover line is the first "act" headline, else the first lead's. Chapters
// kept off the front page, and the highlights about their stories, are
// left out.
func FallbackFront(in Input) FrontPage {
	in = frontOnly(in)
	var all []ItemIn
	for _, c := range in.Chapters {
		all = append(all, c.Items...)
	}
	var fp FrontPage
	used := map[string]bool{}
	for _, h := range in.Highlights {
		if h.Tier != brief.TierAct || len(fp.Leads) == maxLeads {
			continue
		}
		for _, it := range all {
			if !used[it.ID] && brief.MatchHeadline(it.Headline, h.Headline) {
				used[it.ID] = true
				fp.Leads = append(fp.Leads, FrontLead{ID: it.ID, Headline: plain(h.Headline), Paragraphs: nonEmpty(plain(h.Text), plain(h.Why))})
				if fp.CoverLine == "" {
					fp.CoverLine = plain(h.Headline)
				}
				break
			}
		}
	}
	for _, it := range all {
		if len(fp.Leads) == maxLeads {
			break
		}
		if !used[it.ID] {
			used[it.ID] = true
			first, _, _ := strings.Cut(it.Prose, "\n\n")
			fp.Leads = append(fp.Leads, FrontLead{ID: it.ID, Headline: plain(it.Headline), Paragraphs: nonEmpty(plain(first))})
		}
	}
	if fp.CoverLine == "" && len(fp.Leads) > 0 {
		fp.CoverLine = fp.Leads[0].Headline
	}
	for _, c := range in.Chapters {
		p := BriefParagraph{Topic: c.Topic}
		for _, it := range c.Items {
			if !used[it.ID] {
				p.Sentences = append(p.Sentences, BriefSentence{ID: it.ID, Text: sentence(plain(it.Headline))})
			}
		}
		if len(p.Sentences) > 0 {
			fp.InBrief = append(fp.InBrief, p)
		}
	}
	return fp
}

// sentence is s ending with a full stop, unless it already ends with one,
// a question mark or an exclamation mark.
func sentence(s string) string {
	if s == "" || strings.HasSuffix(s, ".") || strings.HasSuffix(s, "?") || strings.HasSuffix(s, "!") {
		return s
	}
	return s + "."
}

func nonEmpty(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
