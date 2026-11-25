package editor

import (
	"fmt"
	"strings"
	"time"

	"github.com/RafaelXokito/daily-brief/internal/brief"
	"github.com/RafaelXokito/daily-brief/internal/fetch"
)

// BuildInput reads the journal's done sections into editor input. articles
// is keyed by item URL; a failed or empty article leaves Article empty.
// onFront reports whether a topic's stories may reach the front page; nil
// means every topic's may.
func BuildInput(j *brief.Journal, articles map[string]fetch.Article, onFront func(topic string) bool) Input {
	in := Input{Highlights: j.Overview.Highlights}
	in.Date, _ = time.Parse("2006-01-02", j.Date)
	for _, s := range j.Sections {
		if s == nil || s.Status != brief.StatusDone {
			continue
		}
		c := ChapterIn{Topic: s.Topic, FrontPage: onFront == nil || onFront(s.Topic)}
		ci := len(in.Chapters)
		for _, it := range s.Items() {
			c.Items = append(c.Items, itemIn(fmt.Sprintf("t%di%d", ci, len(c.Items)), it, articles[it.URL]))
		}
		if len(c.Items) > 0 {
			in.Chapters = append(in.Chapters, c)
		}
	}
	return in
}

func itemIn(id string, it brief.Item, a fetch.Article) ItemIn {
	out := ItemIn{ID: id, Headline: it.Headline, URL: it.URL, Host: brief.Domain(it.URL)}
	if a.Err == nil {
		out.Article = strings.TrimSpace(a.Text)
	}
	lines := strings.Split(brief.PlainText(it.Markdown), "\n")[1:]
	if it.Tagged {
		out.Tag = it.Impact.Label()
		out.Facts, lines = splitFacts(lines, out.Tag)
	}
	out.Prose = paragraphs(lines)
	return out
}

// splitFacts takes the facts off the first non-blank line, if it starts
// with the tag as PlainText writes it, and returns the lines after it.
func splitFacts(lines []string, tag string) (string, []string) {
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(l, "["+tag+"]"); ok {
			return strings.TrimSpace(rest), lines[i+1:]
		}
		break
	}
	return "", lines
}

// paragraphs joins lines into text: blank lines separate paragraphs,
// line breaks within one become spaces.
func paragraphs(lines []string) string {
	var out, cur []string
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.Join(cur, " "))
			cur = nil
		}
	}
	for _, l := range lines {
		if l = strings.TrimSpace(l); l == "" {
			flush()
		} else {
			cur = append(cur, l)
		}
	}
	flush()
	return strings.Join(out, "\n\n")
}
