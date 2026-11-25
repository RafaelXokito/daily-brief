package editor

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// decodeObject finds the first JSON object in reply that decodes into v,
// skipping any prose or code fence around it.
func decodeObject(reply string, v any) error {
	for i := 0; i < len(reply); i++ {
		if reply[i] != '{' {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(reply[i:]))
		dec.DisallowUnknownFields()
		if err := dec.Decode(v); err == nil {
			return nil
		}
	}
	return errors.New("no valid JSON object in reply")
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
// appear exactly once; Host and Tag come from the item, not the model.
func parseChapter(reply string, c ChapterIn) (Chapter, error) {
	var raw chapterJSON
	if err := decodeObject(reply, &raw); err != nil {
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
		seen[sj.ID] = true
		ch.Stories = append(ch.Stories, Story{
			ID: sj.ID, Headline: strings.TrimSpace(sj.Headline), Standfirst: strings.TrimSpace(sj.Standfirst),
			Dateline: strings.TrimSpace(sj.Dateline), Paragraphs: paras, Placement: sj.Placement,
			Host: it.Host, Tag: it.Tag,
		})
	}
	for _, it := range c.Items {
		if !seen[it.ID] {
			return Chapter{}, fmt.Errorf("item id %q missing", it.ID)
		}
	}
	return ch, nil
}

type frontJSON struct {
	Leads []struct {
		ID         string   `json:"id"`
		Headline   string   `json:"headline"`
		Paragraphs []string `json:"paragraphs"`
	} `json:"leads"`
	InBrief []struct {
		ID       string `json:"id"`
		Sentence string `json:"sentence"`
	} `json:"in_brief"`
	CoverLine string `json:"cover_line"`
}

// parseFront reads the front page reply. Every id must be a story in the
// given chapters and may be used only once.
func parseFront(reply string, chapters []Chapter) (FrontPage, error) {
	var raw frontJSON
	if err := decodeObject(reply, &raw); err != nil {
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
	for _, b := range raw.InBrief {
		if err := use(b.ID); err != nil {
			return FrontPage{}, err
		}
		if blank(b.Sentence) {
			return FrontPage{}, fmt.Errorf("in brief %q: empty sentence", b.ID)
		}
		fp.InBrief = append(fp.InBrief, FrontLine{ID: b.ID, Sentence: strings.TrimSpace(b.Sentence)})
	}
	fp.CoverLine = strings.TrimSpace(raw.CoverLine)
	return fp, nil
}
