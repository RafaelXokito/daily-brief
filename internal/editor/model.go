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
	// Image is the book-internal file name of the story's picture, such as
	// "ch1.jpg" or "ch1-2.jpg", or "". paper.Run sets it on chapter leads and
	// on stories that need a picture; the model never does.
	Image string
	// SmallImage means Image looks like a site's logo rather than a photo
	// of the story (picture.Picture.Small): it prints small. paper.Run sets it.
	SmallImage bool
	// NeedsPicture is the editor's judgement that a picture helps the reader
	// understand the story (a device, a place, a route, a chart).
	NeedsPicture bool
	// Highlights are the story's key phrases, in paragraph and text order.
	// Paragraphs hold plain text; the model's == marks never reach them.
	Highlights []Highlight
}

// Highlight is a key phrase: bytes [Start, End) of a story's
// Paragraphs[Para]. The parser makes them; Render ignores any that do not
// fit their paragraph.
type Highlight struct{ Para, Start, End int }

// Chapter is a topic's stories.
type Chapter struct {
	Topic   string
	Stories []Story
}

// FrontLead is a front page lead.
type FrontLead struct {
	ID, Headline string
	Paragraphs   []string
	// Image is the book-internal file name of the lead's picture, or "".
	// paper.Run sets it; the model never does.
	Image string
	// SmallImage means Image looks like a logo: it prints small, even for
	// the first lead. paper.Run sets it.
	SmallImage bool
}

// BriefSentence is one sentence of an In Brief paragraph, about the story ID.
type BriefSentence struct{ ID, Text string }

// BriefParagraph is a topic's In Brief paragraph on the front page.
type BriefParagraph struct {
	Topic     string
	Sentences []BriefSentence
}

// FrontPage is the paper's front page.
type FrontPage struct {
	Leads     []FrontLead
	InBrief   []BriefParagraph
	CoverLine string
}

// Paper is the finished edition.
type Paper struct {
	Date     time.Time
	Front    FrontPage
	Chapters []Chapter
	// Fallbacks is the stories that fell back, plus one if the whole front
	// page did, else one per In Brief topic that got its mechanical
	// paragraph. The colophon prints it as "Stories set by the editor's
	// fallback".
	Fallbacks int
	Tokens    int64
}
