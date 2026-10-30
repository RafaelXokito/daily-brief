package brief

import (
	"regexp"
	"slices"
	"strings"
)

// Impact is the tag a digest item carries: what the reader should do
// about it. Items are written with it as a code span at the start of their
// first body line, as in
//
//	### Widget server flaw exploited
//	`ACT` CVE-2026-1234 · CVSS 9.8 · KEV due Nov 10 · fixed in 3.2.1
//	Attackers run code on unpatched servers. [source](https://…)
//
// An item without a tag ranks, and shows, as FYI.
type Impact int

// Impacts rank by value, most urgent highest; the zero value is FYI.
const (
	ImpactFYI   Impact = iota // worth knowing
	ImpactWatch               // likely to matter soon
	ImpactAct                 // act today: patch, upgrade, rotate credentials, a deadline
)

// Label is the tag as written in a digest, without its code ticks.
func (i Impact) Label() string {
	switch i {
	case ImpactAct:
		return "ACT"
	case ImpactWatch:
		return "WATCH"
	}
	return "FYI"
}

// tagRE matches a tag at the start of a line, after an optional heading
// or list marker: an uppercase code span, "`ACT`", "`WATCH`" or "`FYI`",
// followed by whitespace or the end of the line. Nothing else is a tag:
// not "`act`" (as in React's act()), "**Action:**", "[INFO]" or "FYI, …".
var tagRE = regexp.MustCompile("^(\\s*(?:#{1,6}[ \\t]+|(?:[-*+]|\\d+[.)])[ \\t]+)?)`(ACT|WATCH|FYI)`(?:[ \\t]+|$)")

var tagNames = map[string]Impact{"ACT": ImpactAct, "WATCH": ImpactWatch, "FYI": ImpactFYI}

// SplitTag splits a line that starts with an impact tag, after any heading
// or list marker, into that marker, the tag's impact and the rest of the
// line, and reports whether it does. Only the first tag counts: a second
// one stays in the rest, as text.
func SplitTag(line string) (lead string, imp Impact, rest string, ok bool) {
	m := tagRE.FindStringSubmatch(line)
	if m == nil {
		return "", 0, "", false
	}
	return m[1], tagNames[m[2]], line[len(m[0]):], true
}

// itemTag finds a digest item's tag: on its headline line, after the
// heading or list marker or an "Update:" prefix (see splitHead), else at
// the start of its first non-blank body line. It returns the line of item
// that holds it, its impact, and whether there is one. This is the one
// place a tag is looked for: the TUI shows badges on these lines alone.
func itemTag(item string) (line int, imp Impact, ok bool) {
	lines := strings.Split(item, "\n")
	if _, imp, tagged, _ := splitHead(lines[0]); tagged {
		return 0, imp, true
	}
	for i, l := range lines[1:] {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if lead, imp, _, ok := SplitTag(l); ok && strings.TrimSpace(lead) == "" {
			return i + 1, imp, true
		}
		break
	}
	return -1, ImpactFYI, false
}

// itemImpact is the tag of a digest item (see itemTag) and whether it has
// one: an item without one is FYI.
func itemImpact(item string) (Impact, bool) {
	_, imp, ok := itemTag(strings.TrimLeft(item, " \t\n"))
	return imp, ok
}

// TagLines are the lines of md that hold its items' impact tags (see
// itemTag), as line numbers in md, each mapped to whether it is the
// item's headline line rather than a body line.
func TagLines(md string) map[int]bool {
	lead := leadingLines(md)
	tags := map[int]bool{}
	for _, b := range splitDigest(md) {
		if k, _, ok := itemTag(b.text); b.item && ok {
			tags[lead+b.line+k] = k == 0
		}
	}
	return tags
}

// rankItems orders a digest's items by impact, ACT first, then WATCH, then
// FYI (and untagged), keeping the model's order within each tag. Items
// after a Related label stay after it, ranked among themselves, and a
// preamble stays first. A digest already in that order is returned as it
// is, and so is one ranking could garble: with a code block left open at
// its end, which would take in the items moved after it, or with an item
// whose headline is not recognised, likely a body line that only looked
// like a headline. Items never start inside a code block (see
// splitDigest), so fences move whole, with their item.
func rankItems(md string) string {
	blocks := splitDigest(md)
	if _, open := fenceLines(strings.Split(strings.TrimSpace(md), "\n")); open ||
		slices.ContainsFunc(blocks, func(b block) bool { return b.item && fullHeadline(b.text) == "" }) {
		return md
	}
	ranked := slices.Clone(blocks)
	for i := 0; i < len(ranked); {
		j := i
		for j < len(ranked) && ranked[j].item {
			j++
		}
		slices.SortStableFunc(ranked[i:j], func(a, b block) int {
			x, _ := itemImpact(a.text)
			y, _ := itemImpact(b.text)
			return int(y) - int(x)
		})
		i = j + 1
	}
	if slices.EqualFunc(blocks, ranked, func(a, b block) bool { return a.line == b.line }) {
		return md
	}
	// Items keep the blank line between them the digest had.
	sep := "\n"
	for _, b := range blocks {
		if b.item && strings.HasSuffix(b.text, "\n") {
			sep = "\n\n"
			break
		}
	}
	parts := make([]string, len(ranked))
	for k, b := range ranked {
		parts[k] = strings.TrimRight(b.text, " \t\n")
	}
	return strings.Join(parts, sep)
}

// stripTag is s without a tag it starts with.
func stripTag(s string) string {
	if lead, _, rest, ok := SplitTag(s); ok {
		return lead + rest
	}
	return s
}
