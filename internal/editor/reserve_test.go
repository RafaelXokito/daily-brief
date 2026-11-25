package editor

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/RafaelXokito/daily-brief/internal/brief"
	"github.com/RafaelXokito/daily-brief/internal/fetch"
)

// words is n words of invented English prose, about six bytes a word.
func words(n int) string {
	return strings.TrimSpace(strings.Repeat("widget ", n))
}

// worstInput is the largest input the editor meets: every item carries an
// article cut at fetch's default word limit, a full digest and a highlight.
func worstInput(chapters, perChapter int) Input {
	in := Input{Date: time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)}
	for c := range chapters {
		ch := ChapterIn{Topic: fmt.Sprintf("Topic %d", c)}
		for i := range perChapter {
			ch.Items = append(ch.Items, ItemIn{
				ID: fmt.Sprintf("t%di%d", c, i), Headline: words(14), Tag: "WATCH", Facts: words(30),
				Prose: words(130), Host: "news.example", URL: "https://news.example/a", Article: words(fetch.DefaultMaxWords),
			})
			in.Highlights = append(in.Highlights, brief.Highlight{Tier: brief.TierAct, Headline: words(14), Text: words(30), Why: words(15)})
		}
		in.Chapters = append(in.Chapters, ch)
	}
	return in
}

// claims is what Run would claim, margin included, for every pass of in.
func claims(in Input, marginPct int) int64 {
	var total int64
	var fb []Chapter
	for _, c := range in.Chapters {
		total += withMargin(chapterPlan(c).planned, marginPct)
		fb = append(fb, fallbackChapter(c))
	}
	fs, fi := frontPrompt(in, fb)
	return total + withMargin(newPlan(fs, fi, frontOutput).planned, marginPct)
}

// Reserve covers every claim Run makes on the worst-case input, margin
// included.
func TestReserveCoversRunClaims(t *testing.T) {
	for _, tc := range []struct{ chapters, perChapter, margin int }{
		{1, 1, 0}, {3, 10, 0}, {9, 4, 30}, {8, 10, 30},
	} {
		in := worstInput(tc.chapters, tc.perChapter)
		got := Reserve(tc.chapters, tc.chapters*tc.perChapter, tc.margin)
		if need := claims(in, tc.margin); got < need {
			t.Errorf("Reserve(%d, %d, %d) = %d, below the %d Run claims", tc.chapters, tc.chapters*tc.perChapter, tc.margin, got, need)
		}
	}
}

func TestReserveScalesWithMargin(t *testing.T) {
	base := Reserve(8, 30, 0)
	if got, want := Reserve(8, 30, 30), withMargin(base, 30); got != want {
		t.Errorf("Reserve with 30%% margin = %d, want %d", got, want)
	}
}
