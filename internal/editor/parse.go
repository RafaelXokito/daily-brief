package editor

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var fenceRe = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)```")

// decodeObject finds the JSON object in reply: the last object that decodes
// cleanly (no unknown fields, no type errors) and passes valid. A fenced
// block is preferred when it holds one. Each attempt decodes into a fresh
// value, so a failed attempt never leaks into the result.
func decodeObject[T any](reply string, valid func(T) bool) (T, error) {
	for _, m := range fenceRe.FindAllStringSubmatch(reply, -1) {
		if v, ok := lastObject(m[1], valid); ok {
			return v, nil
		}
	}
	if v, ok := lastObject(reply, valid); ok {
		return v, nil
	}
	var zero T
	return zero, errors.New("no valid JSON object in reply")
}

func lastObject[T any](s string, valid func(T) bool) (best T, found bool) {
	for i := 0; i < len(s); i++ {
		if s[i] != '{' {
			continue
		}
		var v T
		dec := json.NewDecoder(strings.NewReader(s[i:]))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&v); err != nil || !valid(v) {
			continue
		}
		best, found = v, true
		i += int(dec.InputOffset()) - 1
	}
	return best, found
}

type storyJSON struct {
	ID         string   `json:"id"`
	Headline   string   `json:"headline"`
	Standfirst string   `json:"standfirst"`
	Dateline   string   `json:"dateline"`
	Paragraphs []string `json:"paragraphs"`
	Placement  string   `json:"placement"`
}

type chapterJSON struct {
	Stories []storyJSON `json:"stories"`
}

const (
	minLeads      = 2
	maxFrontLeads = 3
	minBrief      = 2 // sentences in an In Brief paragraph, or fewer when its topic has fewer stories left
	maxBrief      = 4
)

func validPlacement(p string) bool { return p == "lead" || p == "story" || p == "brief" }

func blank(s string) bool { return strings.TrimSpace(s) == "" }

func cleanParagraphs(ps []string) []string {
	var out []string
	for _, p := range ps {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseChapter reads the editor's reply for one chapter. Every item must
// appear exactly once; Host, Tag and URL come from the item, not the model.
func parseChapter(reply string, c ChapterIn) (Chapter, error) {
	raw, err := decodeObject(reply, func(r chapterJSON) bool { return len(r.Stories) > 0 })
	if err != nil {
		return Chapter{}, err
	}
	items := map[string]ItemIn{}
	for _, it := range c.Items {
		items[it.ID] = it
	}
	seen := map[string]bool{}
	ch := Chapter{Topic: c.Topic}
	for _, sj := range raw.Stories {
		it, ok := items[sj.ID]
		switch {
		case !ok:
			return Chapter{}, fmt.Errorf("unknown item id %q", sj.ID)
		case seen[sj.ID]:
			return Chapter{}, fmt.Errorf("item id %q appears twice", sj.ID)
		case !validPlacement(sj.Placement):
			return Chapter{}, fmt.Errorf("item %q: bad placement %q", sj.ID, sj.Placement)
		case blank(sj.Headline):
			return Chapter{}, fmt.Errorf("item %q: empty headline", sj.ID)
		}
		paras := cleanParagraphs(sj.Paragraphs)
		if len(paras) == 0 {
			return Chapter{}, fmt.Errorf("item %q: no paragraphs", sj.ID)
		}
		if sj.Placement == "brief" && len(paras) != 1 {
			return Chapter{}, fmt.Errorf("item %q: a brief must be one paragraph", sj.ID)
		}
		seen[sj.ID] = true
		ch.Stories = append(ch.Stories, Story{
			ID: sj.ID, Headline: strings.TrimSpace(sj.Headline), Standfirst: strings.TrimSpace(sj.Standfirst),
			Dateline: strings.TrimSpace(sj.Dateline), Paragraphs: paras, Placement: sj.Placement,
			Host: it.Host, Tag: it.Tag, URL: it.URL,
		})
	}
	for _, it := range c.Items {
		if !seen[it.ID] {
			return Chapter{}, fmt.Errorf("item id %q missing", it.ID)
		}
	}
	leads := 0
	for _, s := range ch.Stories {
		if s.Placement == "lead" {
			leads++
		}
	}
	if len(ch.Stories) > 0 && leads != 1 {
		return Chapter{}, fmt.Errorf("want exactly one lead, got %d", leads)
	}
	return ch, nil
}

type frontJSON struct {
	Leads []struct {
		ID         string   `json:"id"`
		Headline   string   `json:"headline"`
		Paragraphs []string `json:"paragraphs"`
	} `json:"leads"`
	InBrief   []briefJSON `json:"in_brief"`
	CoverLine string      `json:"cover_line"`
}

type briefJSON struct {
	Topic     string `json:"topic"`
	Sentences []struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	} `json:"sentences"`
}

// parseFront reads the front page reply. Every id must be a story in the
// given chapters and may be used only once. A bad lead or cover line fails
// the whole reply; a bad In Brief paragraph is only left out (parseInBrief).
func parseFront(reply string, chapters []Chapter) (FrontPage, error) {
	raw, err := decodeObject(reply, func(r frontJSON) bool { return len(r.Leads) > 0 && !blank(r.CoverLine) })
	if err != nil {
		return FrontPage{}, err
	}
	known := map[string]bool{}
	for _, c := range chapters {
		for _, s := range c.Stories {
			known[s.ID] = true
		}
	}
	used := map[string]bool{}
	use := func(id string) error {
		if !known[id] {
			return fmt.Errorf("unknown story id %q", id)
		}
		if used[id] {
			return fmt.Errorf("story id %q used twice on the front page", id)
		}
		used[id] = true
		return nil
	}
	var fp FrontPage
	for _, l := range raw.Leads {
		if err := use(l.ID); err != nil {
			return FrontPage{}, err
		}
		paras := cleanParagraphs(l.Paragraphs)
		if blank(l.Headline) || len(paras) == 0 {
			return FrontPage{}, fmt.Errorf("lead %q: empty headline or paragraphs", l.ID)
		}
		fp.Leads = append(fp.Leads, FrontLead{ID: l.ID, Headline: strings.TrimSpace(l.Headline), Paragraphs: paras})
	}
	if n := len(fp.Leads); n < minLeads || n > maxFrontLeads {
		return FrontPage{}, fmt.Errorf("want %d to %d leads, got %d", minLeads, maxFrontLeads, n)
	}
	fp.InBrief = parseInBrief(raw.InBrief, chapters, used)
	fp.CoverLine = strings.TrimSpace(raw.CoverLine)
	return fp, nil
}

// parseInBrief keeps the In Brief paragraphs that follow the rules: for
// each chapter with stories that are not leads, in chapter order, the
// first paragraph naming its topic, when it has two to four sentences each
// about a different story of that chapter. A paragraph for an unknown
// topic, a second one for a topic and one that breaks a rule are left out;
// Run gives those topics their mechanical paragraph.
func parseInBrief(raw []briefJSON, chapters []Chapter, leads map[string]bool) []BriefParagraph {
	var out []BriefParagraph
	for _, c := range chapters {
		left := map[string]bool{}
		for _, s := range c.Stories {
			if !leads[s.ID] {
				left[s.ID] = true
			}
		}
		if len(left) == 0 {
			continue
		}
		for _, r := range raw {
			if !sameTopic(r.Topic, c.Topic) {
				continue
			}
			if p, err := briefParagraph(r, c.Topic, left); err == nil {
				out = append(out, p)
			}
			break
		}
	}
	return out
}

// briefParagraph checks one paragraph against the ids its chapter has
// left, taking each id it uses.
func briefParagraph(raw briefJSON, topic string, left map[string]bool) (BriefParagraph, error) {
	want := min(minBrief, len(left))
	if n := len(raw.Sentences); n < want || n > maxBrief {
		return BriefParagraph{}, fmt.Errorf("in brief %q: want %d to %d sentences, got %d", topic, want, maxBrief, n)
	}
	p := BriefParagraph{Topic: topic}
	for _, s := range raw.Sentences {
		if !left[s.ID] {
			return BriefParagraph{}, fmt.Errorf("in brief %q: story id %q is not a story of this topic left to use", topic, s.ID)
		}
		if blank(s.Text) {
			return BriefParagraph{}, fmt.Errorf("in brief %q: empty sentence for %q", topic, s.ID)
		}
		delete(left, s.ID)
		p.Sentences = append(p.Sentences, BriefSentence{ID: s.ID, Text: strings.TrimSpace(s.Text)})
	}
	return p, nil
}

// sameTopic compares topics ignoring case and spacing.
func sameTopic(a, b string) bool {
	return strings.EqualFold(strings.Join(strings.Fields(a), " "), strings.Join(strings.Fields(b), " "))
}
