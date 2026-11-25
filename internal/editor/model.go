// Package editor turns a day's journal into newspaper stories.
package editor

import (
	"time"

	"github.com/RafaelXokito/daily-brief/internal/brief"
)

// ItemIn is one journal item as the editor sees it.
type ItemIn struct {
	ID, Headline, Tag, Facts, Prose, Host, URL, Article string
}

// ChapterIn is one topic's items. FrontPage false keeps the chapter's
// stories off the front page.
type ChapterIn struct {
	Topic     string
	Items     []ItemIn
	FrontPage bool
}

// Input is everything the editor works from.
type Input struct {
	Date       time.Time
	Chapters   []ChapterIn
	Highlights []brief.Highlight
}

// Story is one article of the paper.
type Story struct {
	ID, Headline, Standfirst, Dateline string
	Paragraphs                         []string
	Placement                          string // "lead" | "story" | "brief"
	Host                               string
	// Tag is the item's tag: "ACT", "WATCH", "FYI" or "" when untagged.
	Tag      string
	Fallback bool
	// URL is the item's source, never the model's.
	URL string
}

// Chapter is a topic's stories.
type Chapter struct {
	Topic   string
	Stories []Story
}

// FrontLead is a front page lead.
type FrontLead struct {
	ID, Headline string
	Paragraphs   []string
}

// FrontLine is one "In Brief" line on the front page.
type FrontLine struct{ ID, Sentence string }

// FrontPage is the paper's front page.
type FrontPage struct {
	Leads     []FrontLead
	InBrief   []FrontLine
	CoverLine string
}

// Paper is the finished edition.
type Paper struct {
	Date      time.Time
	Front     FrontPage
	Chapters  []Chapter
	Fallbacks int // stories that fell back, plus one if the front page did
	Tokens    int64
}
