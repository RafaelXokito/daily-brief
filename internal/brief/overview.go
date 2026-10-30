package brief

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/idna"

	"github.com/rafaelpereira/daily-brief/internal/config"
)

var (
	bareURLRE = regexp.MustCompile(`https?://[^\s<>()\[\]]+`)
	// textURLRE matches URLs in free text, bare or in angle brackets.
	textURLRE = regexp.MustCompile(`(?i)<?\b[a-z][a-z0-9+.-]*://[^\s>]*>?`)
	// jsonReplyRE marks where a reply's highlights object starts.
	jsonReplyRE = regexp.MustCompile(`\{\s*"highlights"`)
)

// StripControl removes the control characters in s, newline and tab
// aside: C0, DEL and C1, which a terminal could act on. It also removes
// the invisible characters that reorder or hide text (see IsInvisible).
// Digests, sources and the overview come from web pages through the
// model's output.
func StripControl(s string) string {
	if !strings.ContainsFunc(s, isUnsafe) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if isUnsafe(r) {
			return -1
		}
		return r
	}, s)
}

func isControl(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }

func isUnsafe(r rune) bool { return isControl(r) || IsInvisible(r) }

// IsInvisible reports whether r is a bidirectional control, which can
// make text read differently from what it is, or a zero-width space or
// word joiner, which can hide in it (see config.IsInvisible).
func IsInvisible(r rune) bool { return config.IsInvisible(r) }

// SafeWebURL is raw as a normalised http(s) URL with a host, or false:
// the only links shown as hyperlinks or handed to the system's opener. A
// host with control, invisible or space characters is refused, and any
// user information dropped, so "https://good.com@evil.com" shows and
// opens as https://evil.com.
func SafeWebURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "-") {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", false
	}
	if strings.ContainsFunc(u.Host, func(r rune) bool { return isUnsafe(r) || unicode.IsSpace(r) }) {
		return "", false
	}
	u.User = nil
	return u.String(), true
}

// CleanText is free text the model wrote, such as a highlight's, as one
// plain line: links as their text, without URLs, control characters or
// leading heading, quote or code markers.
func CleanText(s string) string {
	s = mdLinkRE.ReplaceAllString(StripControl(s), "$1")
	s = textURLRE.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(strings.TrimLeft(strings.TrimSpace(s), "#>` \t\n")), " ")
}

// Highlight is an overview entry: one item of a topic's digest, worth
// acting on (TierAct) or knowing (TierKnow).
type Highlight struct {
	Tier     string `json:"tier"`
	Topic    string `json:"topic"`
	Headline string `json:"headline"`
	Text     string `json:"text"`
	Why      string `json:"why,omitempty"`
}

const (
	TierAct  = "act"  // security actions and deadlines
	TierKnow = "know" // worth knowing

	maxHighlights  = 6
	highlightWords = 18
	whyWords       = 14
	// highlightRunes caps a highlight's text and why however long their
	// words.
	highlightRunes = 160
)

// Item is one item of a digest, as capItems recognises them.
type Item struct {
	// Headline is the item's first line without heading, bullet or bold
	// markers, an "Update:" prefix or the source links that end it.
	Headline string
	// URL is the item's first link, else a source matching its headline,
	// else a bare URL in it, if that is an http(s) URL (see SafeWebURL).
	URL      string
	Markdown string
	// Impact is the item's tag, FYI if it has none; Tagged reports whether
	// it has one.
	Impact Impact
	Tagged bool
}

// Items lists the section's items in order.
func (s *Section) Items() []Item {
	var items []Item
	for _, b := range splitDigest(s.Markdown) {
		if b.item {
			u, ok := SafeWebURL(itemURL(b.text, s.Sources))
			if !ok {
				u, _ = SafeWebURL(strings.TrimRight(bareURLRE.FindString(b.text), ".,;:!?'\""))
			}
			imp, tagged := itemImpact(b.text)
			items = append(items, Item{Headline: fullHeadline(b.text), URL: u, Markdown: b.text, Impact: imp, Tagged: tagged})
		}
	}
	return items
}

// MatchHeadline reports whether two headlines name the same item: compared
// as words, without markers or an "Update:" prefix, one starts with the
// other, which has at least three words (or all of the longer one's).
func MatchHeadline(a, b string) bool {
	x, y := words(fullHeadline(a)), words(fullHeadline(b))
	if len(x) > len(y) {
		x, y = y, x
	}
	return len(x) > 0 && len(x) >= min(3, len(y)) && slices.Equal(x, y[:len(x)])
}

// ItemLines are the lines of the section's Markdown that start its
// items, in the order Items lists them.
func (s *Section) ItemLines() []int {
	lead := leadingLines(s.Markdown)
	var lines []int
	for _, b := range splitDigest(s.Markdown) {
		if b.item {
			lines = append(lines, lead+b.line)
		}
	}
	return lines
}

// leadingLines is how many lines the blank start of md takes: what
// splitDigest's line numbers are offset by in md.
func leadingLines(md string) int {
	return strings.Count(md[:len(md)-len(strings.TrimLeftFunc(md, unicode.IsSpace))], "\n")
}

// Headline is the headline of an item whose first line is line (see
// Item.Headline).
func Headline(line string) string { return fullHeadline(line) }

// ItemIndex is the index of the item headline names, or -1: the item
// with that very headline, else the first whose words are the same, else
// the first MatchHeadline accepts.
func ItemIndex(items []Item, headline string) int {
	if k := slices.IndexFunc(items, func(it Item) bool { return it.Headline == headline }); k >= 0 {
		return k
	}
	w := words(fullHeadline(headline))
	if k := slices.IndexFunc(items, func(it Item) bool { return slices.Equal(words(fullHeadline(it.Headline)), w) }); k >= 0 {
		return k
	}
	return slices.IndexFunc(items, func(it Item) bool { return MatchHeadline(it.Headline, headline) })
}

// PlainText is an item's Markdown as plain text: without heading or bold
// markers, code ticks or the source links that end its lines; other links
// keep their text. Its impact tag reads "[ACT]" and the like.
func PlainText(md string) string {
	md = strings.TrimSpace(md)
	k, imp, tagged := itemTag(md)
	var out []string
	for i, l := range strings.Split(md, "\n") {
		if tagged && i == k {
			l = strings.Replace(l, "`"+imp.Label()+"`", "["+imp.Label()+"]", 1)
		}
		l = strings.TrimSpace(l)
		if isHeading(l) {
			l = strings.TrimSpace(strings.TrimLeft(l, "#"))
		}
		if loc := trailingLinksRE.FindStringIndex(l); loc != nil && loc[0] > 0 {
			l = strings.TrimSpace(l[:loc[0]])
		}
		l = mdLinkRE.ReplaceAllString(l, "$1")
		out = append(out, strings.NewReplacer("**", "", "__", "", "`", "").Replace(l))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// Domain is a URL's host without "www.", or "" if it has none. A host
// with non-ASCII characters is shown as punycode ("xn--…"), so a
// lookalike such as "аpple.com", with a Cyrillic "а", cannot pass for the
// real one.
func Domain(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if strings.ContainsFunc(host, func(r rune) bool { return r >= utf8.RuneSelf }) {
		if host, err = idna.ToASCII(host); err != nil || strings.ContainsFunc(host, func(r rune) bool { return r >= utf8.RuneSelf }) {
			return ""
		}
	}
	return strings.TrimPrefix(host, "www.")
}

// IsRelated reports whether a digest line is a Related label.
func IsRelated(l string) bool { return isRelated(l) }

// NumbersLine sums up the finished sections: "12 items · 2 to act ·
// 2 updates · 5 topics · ~7 min", with minutes the journal's reading time.
// Items to act on are those tagged ACT; journals written before items
// were tagged leave that count out.
func NumbersLine(sections []*Section, minutes float64) string {
	items, act, updates, topics := 0, 0, 0, 0
	tagged := false
	for _, s := range sections {
		if s.Status == StatusDone {
			for _, it := range s.Items() {
				items++
				tagged = tagged || it.Tagged
				if it.Tagged && it.Impact == ImpactAct {
					act++
				}
			}
			updates += s.Updates()
			topics++
		}
	}
	parts := []string{plural(items, "item")}
	if tagged {
		parts = append(parts, fmt.Sprintf("%d to act", act))
	}
	parts = append(parts, plural(updates, "update"), plural(topics, "topic"), fmt.Sprintf("~%.0f min", max(1, minutes)))
	return strings.Join(parts, " · ")
}

// ActCount is how many of the section's items are tagged ACT.
func (s *Section) ActCount() int {
	n := 0
	for _, it := range s.Items() {
		if it.Tagged && it.Impact == ImpactAct {
			n++
		}
	}
	return n
}

// StatusLines say how each topic fared: "Security — 3 items, 1 update",
// "AI — quiet day".
func StatusLines(sections []*Section) []string {
	var lines []string
	for _, s := range sections {
		var st string
		switch s.Status {
		case StatusDone:
			n, u := countItems(s.Markdown), s.Updates()
			switch {
			case n == 0:
				st = "quiet day"
			case u > 0:
				st = plural(n, "item") + ", " + plural(u, "update")
			default:
				st = plural(n, "item")
			}
		case StatusFailed:
			st = "not summarized"
		default:
			st = "in progress"
		}
		lines = append(lines, s.Topic+" — "+st)
	}
	return lines
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// capOverview is capOverviewWithin overviewWords.
func capOverview(reply string, sections []*Section) (string, []Highlight) {
	return capOverviewWithin(reply, sections, overviewWords)
}

// capOverviewWithin turns the overview reply into the overview's Markdown
// and highlights. The reply's first JSON object with highlights, code
// fences tolerated, gives them: those whose topic or headline does not
// name an item of sections are dropped, the rest cleaned and trimmed,
// "act" first, at most six. The Markdown lists them with the numbers and
// status lines built here. A reply in prose keeps its bullets (at most
// five, cleaned) and gets no highlights; one that looks like JSON but does
// not decode (cut off, mistyped) gets neither. Highlights are dropped from
// the end, and status lines left out if they would take over a third, to
// keep the overview within about limit words.
func capOverviewWithin(reply string, sections []*Section, limit int) (string, []Highlight) {
	reply = StripControl(reply)
	var status []string
	if s := StatusLines(sections); countWords(strings.Join(s, "\n")) <= limit/3 {
		status = s
	}
	hl, ok := parseHighlights(reply)
	switch {
	case !ok && looksLikeJSON(reply):
		return overviewMarkdown(nil, sections, status), nil
	case !ok:
		return fallbackOverview(reply, sections, status, limit), nil
	}
	hl = resolveHighlights(hl, sections)
	for {
		md := overviewMarkdown(hl, sections, status)
		if countWords(md) <= limit || len(hl) == 0 {
			return md, hl
		}
		hl = hl[:len(hl)-1]
	}
}

// parseHighlights decodes the first JSON object in reply with
// highlights: the one at its first "{", else one starting {"highlights".
func parseHighlights(reply string) ([]Highlight, bool) {
	var starts []int
	if i := strings.Index(reply, "{"); i >= 0 {
		starts = append(starts, i)
	}
	for _, loc := range jsonReplyRE.FindAllStringIndex(reply, -1) {
		starts = append(starts, loc[0])
	}
	for _, i := range starts {
		var v struct {
			Highlights []Highlight `json:"highlights"`
		}
		if err := json.NewDecoder(strings.NewReader(reply[i:])).Decode(&v); err == nil && v.Highlights != nil {
			return v.Highlights, true
		}
	}
	return nil, false
}

// looksLikeJSON reports whether reply was meant as JSON: it names the
// highlights object or starts with a brace, bracket or code fence.
func looksLikeJSON(reply string) bool {
	t := strings.TrimSpace(reply)
	return jsonReplyRE.MatchString(reply) || strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") || strings.HasPrefix(t, "```")
}

// resolveHighlights keeps the highlights that name an item of a finished
// section, once each, with that section's topic and item's headline as
// written, "act" first and at most maxHighlights. A tagged item's
// highlight is "act" if and only if the item is tagged ACT; an untagged
// one keeps the tier the model chose. Their text and why are
// cleaned (see CleanText) and trimmed.
func resolveHighlights(in []Highlight, sections []*Section) []Highlight {
	var out []Highlight
	seen := map[string]bool{}
	for _, h := range in {
		k := slices.IndexFunc(sections, func(s *Section) bool {
			return s.Status == StatusDone && strings.EqualFold(strings.TrimSpace(s.Topic), strings.TrimSpace(h.Topic))
		})
		if k < 0 {
			continue
		}
		items := sections[k].Items()
		n := ItemIndex(items, h.Headline)
		if n < 0 || seen[sections[k].Topic+"\x00"+items[n].Headline] {
			continue
		}
		seen[sections[k].Topic+"\x00"+items[n].Headline] = true
		h.Topic, h.Headline = sections[k].Topic, items[n].Headline
		act := strings.EqualFold(strings.TrimSpace(h.Tier), TierAct)
		if items[n].Tagged {
			// The item's tag decides: "act" is for items tagged ACT.
			act = items[n].Impact == ImpactAct
		}
		h.Tier = TierKnow
		if act {
			h.Tier = TierAct
		}
		h.Text = clip(firstWords(CleanText(stripTag(StripControl(h.Text))), highlightWords), highlightRunes)
		if h.Text == "" {
			h.Text = clip(firstWords(CleanText(h.Headline), highlightWords), highlightRunes)
		}
		h.Why = clip(firstWords(CleanText(h.Why), whyWords), highlightRunes)
		out = append(out, h)
	}
	slices.SortStableFunc(out, func(a, b Highlight) int {
		if a.Tier == b.Tier {
			return 0
		}
		if a.Tier == TierAct {
			return -1
		}
		return 1
	})
	return out[:min(len(out), maxHighlights)]
}

// firstWords is s on one line, without bold markers, cut to n words and
// marked "…" if cut.
func firstWords(s string, n int) string {
	w := strings.Fields(strings.ReplaceAll(s, "**", ""))
	if len(w) <= n {
		return strings.Join(w, " ")
	}
	return strings.Join(w[:n], " ") + "…"
}

// HighlightURL is the source of the item a highlight names, or "".
func HighlightURL(h Highlight, sections []*Section) string {
	for _, s := range sections {
		if s.Topic != h.Topic {
			continue
		}
		items := s.Items()
		if k := ItemIndex(items, h.Headline); k >= 0 {
			return items[k].URL
		}
	}
	return ""
}

// overviewMarkdown lists the highlights by tier between the numbers line
// and the status lines.
func overviewMarkdown(hl []Highlight, sections []*Section, status []string) string {
	build := func(minutes float64) string {
		var b strings.Builder
		b.WriteString("## Today's highlights\n\n" + NumbersLine(sections, minutes) + "\n")
		for _, tier := range []struct{ tier, title string }{{TierAct, "Act now"}, {TierKnow, "Worth knowing"}} {
			title := "\n**" + tier.title + "**\n\n"
			for _, h := range hl {
				if h.Tier != tier.tier {
					continue
				}
				b.WriteString(title + "- " + h.Text)
				title = ""
				if h.Why != "" {
					b.WriteString(" — " + h.Why)
				}
				b.WriteString(" (" + h.Topic + ")")
				if u := HighlightURL(h, sections); u != "" {
					b.WriteString(" [source](" + u + ")")
				}
				b.WriteString("\n")
			}
		}
		writeStatus(&b, status)
		return strings.TrimSpace(b.String())
	}
	return build(journalMinutes(build(0), sections))
}

// fallbackOverview is the overview of a reply in prose: its bullets, at
// most maxOverviewBullets, cleaned as highlights are (see CleanText) and
// without headings, between the numbers line and the status lines, within
// about limit words.
func fallbackOverview(reply string, sections []*Section, status []string, limit int) string {
	var lines []string
	bullets := 0
	for _, l := range strings.Split(strings.TrimSpace(reply), "\n") {
		if isHeading(l) {
			continue
		}
		if isBullet(l) {
			if bullets++; bullets > maxOverviewBullets {
				break
			}
		}
		if loc := trailingLinksRE.FindStringIndex(l); loc != nil && strings.Trim(l[:loc[0]], "-*+ \t") != "" {
			l = l[:loc[0]]
		}
		if t := strings.TrimSpace(l); isBullet(t) {
			marker, rest, _ := strings.Cut(t, " ")
			l = marker + " " + CleanText(rest)
		} else {
			l = CleanText(l)
		}
		lines = append(lines, l)
	}
	body := strings.TrimSpace(strings.Join(lines, "\n"))
	build := func(body string, minutes float64) string {
		var b strings.Builder
		b.WriteString("## Today's highlights\n\n" + NumbersLine(sections, minutes) + "\n")
		if body != "" {
			b.WriteString("\n" + body + "\n")
		}
		writeStatus(&b, status)
		return strings.TrimSpace(b.String())
	}
	body = strings.TrimSpace(trimWords(body, limit-countWords(build("", 0))))
	return build(body, journalMinutes(build(body, 0), sections))
}

func writeStatus(b *strings.Builder, status []string) {
	if len(status) == 0 {
		return
	}
	b.WriteString("\n**By topic**\n\n")
	for _, s := range status {
		b.WriteString("- " + s + "\n")
	}
}

// journalMinutes is the reading time of a journal with this overview and
// sections, as Journal.ReadMinutes counts it.
func journalMinutes(overview string, sections []*Section) float64 {
	j := Journal{Overview: Section{Markdown: overview}, Sections: sections}
	return j.ReadMinutes()
}
