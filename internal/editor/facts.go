package editor

import (
	"regexp"
	"strings"
)

var (
	numberRe   = regexp.MustCompile(`\d[\d,.]*\d|\d`)
	cveRe      = regexp.MustCompile(`(?i)CVE-\d{4}-\d+`)
	currencyRe = regexp.MustCompile(`[$€£¥]\s?\d[\d,.]*\d|[$€£¥]\s?\d`)
	wordRe     = regexp.MustCompile(`\p{L}+`)
)

// normNumber drops thousands commas and a trailing dot.
func normNumber(s string) string {
	return strings.TrimRight(strings.ReplaceAll(s, ",", ""), ".")
}

func normCurrency(s string) string {
	return normNumber(strings.Join(strings.Fields(s), ""))
}

// factTokens returns the numbers, CVE ids and currency amounts in text.
func factTokens(text string) map[string]bool {
	out := map[string]bool{}
	for _, m := range numberRe.FindAllString(text, -1) {
		out[normNumber(m)] = true
	}
	for _, m := range cveRe.FindAllString(text, -1) {
		out[strings.ToUpper(m)] = true
	}
	for _, m := range currencyRe.FindAllString(text, -1) {
		out["cur:"+normCurrency(m)] = true
	}
	return out
}

func sourceText(it ItemIn) string {
	return strings.Join([]string{it.Headline, it.Facts, it.Prose, it.Host, it.Article}, "\n")
}

func storyText(s Story) string {
	return strings.Join(append([]string{s.Headline, s.Standfirst, s.Dateline}, s.Paragraphs...), "\n")
}

// checkStory reports whether every number, version, CVE id and money amount
// in the story appears in its item or article, and every word of the
// dateline does too.
func checkStory(s Story, it ItemIn) bool {
	src := sourceText(it)
	if !covered(storyText(s), factTokens(src)) {
		return false
	}
	words := map[string]bool{}
	for _, w := range wordRe.FindAllString(src, -1) {
		words[strings.ToLower(w)] = true
	}
	for _, w := range wordRe.FindAllString(s.Dateline, -1) {
		if !words[strings.ToLower(w)] {
			return false
		}
	}
	return true
}

// covered reports whether every fact token in text is among known.
func covered(text string, known map[string]bool) bool {
	for tok := range factTokens(text) {
		if !known[tok] {
			return false
		}
	}
	return true
}

// checkFront reports whether every fact on the front page has a source:
// each lead and In Brief line in its own story plus that story's item and
// article, and the cover line in any of them.
func checkFront(fp FrontPage, chapters []Chapter, items map[string]ItemIn) bool {
	own := map[string]string{}
	var all []string
	for _, c := range chapters {
		for _, s := range c.Stories {
			own[s.ID] = storyText(s) + "\n" + sourceText(items[s.ID])
			all = append(all, own[s.ID])
		}
	}
	for _, l := range fp.Leads {
		if !covered(strings.Join(append([]string{l.Headline}, l.Paragraphs...), "\n"), factTokens(own[l.ID])) {
			return false
		}
	}
	for _, b := range fp.InBrief {
		if !covered(b.Sentence, factTokens(own[b.ID])) {
			return false
		}
	}
	return covered(fp.CoverLine, factTokens(strings.Join(all, "\n")))
}
