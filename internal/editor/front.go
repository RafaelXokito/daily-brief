package editor

import (
	"strings"

	"github.com/RafaelXokito/daily-brief/internal/brief"
)

// frontOnly is in with only the chapters whose stories may reach the front
// page, and without the highlights about the other chapters' stories: those
// named for their topic or matching one of their items' headlines.
func frontOnly(in Input) Input {
	out := Input{Date: in.Date}
	var off []ChapterIn
	for _, c := range in.Chapters {
		if c.FrontPage {
			out.Chapters = append(out.Chapters, c)
		} else {
			off = append(off, c)
		}
	}
	for _, h := range in.Highlights {
		if !aboutAny(h, off) {
			out.Highlights = append(out.Highlights, h)
		}
	}
	return out
}

func aboutAny(h brief.Highlight, chapters []ChapterIn) bool {
	for _, c := range chapters {
		if strings.EqualFold(strings.TrimSpace(h.Topic), strings.TrimSpace(c.Topic)) {
			return true
		}
		for _, it := range c.Items {
			if brief.MatchHeadline(it.Headline, h.Headline) {
				return true
			}
		}
	}
	return false
}

// frontChapters is the edited chapters whose stories may reach the front
// page; chapters[i] is the edit of in.Chapters[i].
func frontChapters(in Input, chapters []Chapter) []Chapter {
	var out []Chapter
	for i, c := range chapters {
		if i < len(in.Chapters) && in.Chapters[i].FrontPage {
			out = append(out, c)
		}
	}
	return out
}
