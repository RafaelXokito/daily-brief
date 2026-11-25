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
		Paragraphs: paras, Placement: placement, Host: it.Host, Tag: it.Tag, Fallback: true,
	}
}

// FallbackFront builds the front page without the model. Leads are the
// first two "act" highlights, else the first two items; In Brief is every
// other item's headline; the cover line is the first "act" headline, else
// the first lead's.
func FallbackFront(in Input) FrontPage {
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
		lead := FrontLead{Headline: plain(h.Headline), Paragraphs: nonEmpty(plain(h.Text), plain(h.Why))}
		for _, it := range all {
			if !used[it.ID] && brief.MatchHeadline(it.Headline, h.Headline) {
				lead.ID, used[it.ID] = it.ID, true
				break
			}
		}
		fp.Leads = append(fp.Leads, lead)
	}
	if len(fp.Leads) > 0 {
		fp.CoverLine = fp.Leads[0].Headline
	}
	if len(fp.Leads) == 0 {
		for _, it := range all[:min(maxLeads, len(all))] {
			fp.Leads = append(fp.Leads, FrontLead{ID: it.ID, Headline: plain(it.Headline), Paragraphs: nonEmpty(plain(it.Prose))})
			used[it.ID] = true
		}
	}
	if fp.CoverLine == "" && len(fp.Leads) > 0 {
		fp.CoverLine = fp.Leads[0].Headline
	}
	for _, it := range all {
		if !used[it.ID] {
			fp.InBrief = append(fp.InBrief, FrontLine{ID: it.ID, Sentence: plain(it.Headline)})
		}
	}
	return fp
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
