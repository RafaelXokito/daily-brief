package tui

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/glamour"
	gansi "github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/rafaelpereira/daily-brief/internal/brief"
)

// Themes: "dark" and "light" are derived from glamour's styles of that
// name; any other name, such as "notty", renders without colour.
const (
	themeDark  = "dark"
	themeLight = "light"
)

// docMargin is the rendered document's margin on each side.
const docMargin = 1

// styleConfig is glamour's dark or light style without the "#" prefixes
// of headings: H2 in the accent colour, H3 bold in the body colour with a
// blank line above it, H6 small and muted (the Related label), and a rule
// across the width.
func styleConfig(theme string, width int) gansi.StyleConfig {
	var c gansi.StyleConfig
	var accentHex, mutedHex string
	switch theme {
	case themeDark:
		c, accentHex, mutedHex = styles.DarkStyleConfig, accent.Dark, muted.Dark
	case themeLight:
		c, accentHex, mutedHex = styles.LightStyleConfig, accent.Light, muted.Light
	default:
		c = styles.NoTTYStyleConfig
		margin := uint(docMargin)
		c.Document.Margin = &margin
		for _, h := range []*gansi.StyleBlock{&c.H1, &c.H2, &c.H3, &c.H4, &c.H5, &c.H6} {
			h.Prefix = ""
		}
		c.H3.BlockPrefix = "\n"
		c.HorizontalRule.Format = "\n" + strings.Repeat("─", max(1, width-2*docMargin)) + "\n"
		return c
	}
	color := func(hex string) *string {
		if hex == "" {
			return nil
		}
		return &hex
	}
	yes, no, margin := true, false, uint(docMargin)
	c.Document.Margin = &margin
	for _, h := range []*gansi.StyleBlock{&c.H1, &c.H2, &c.H3, &c.H4, &c.H5, &c.H6} {
		if strings.Contains(h.Prefix, "#") {
			h.Prefix = ""
		}
	}
	c.H2.Color, c.H2.Bold = color(accentHex), &yes
	c.H3.Color, c.H3.Bold, c.H3.BlockPrefix = c.Document.Color, &yes, "\n"
	c.H6.Color, c.H6.Bold = color(mutedHex), &no
	c.HorizontalRule.Color = color(mutedHex)
	c.HorizontalRule.Format = "\n" + strings.Repeat("─", max(1, width-2*docMargin)) + "\n"
	return c
}

type rendererKey struct {
	width int
	theme string
}

// renderers caches a glamour renderer per width and theme; mu also
// serialises their use.
var renderers = struct {
	sync.Mutex
	m map[rendererKey]*glamour.TermRenderer
}{m: map[rendererKey]*glamour.TermRenderer{}}

// Placeholders are made of private-use runes, one cell wide, which
// sanitise removes from the Markdown, so its text can never be mistaken
// for one: a link is tokStart, its number, tokEnd and tokPad up to the
// width of its text; a blockquote paragraph is its number between two
// quoteMark; itemMark starts each item's first line.
const (
	tokStart  = '\uE000'
	tokPad    = '\uE001'
	tokEnd    = '\uE002'
	quoteMark = '\uE003'
	itemMark  = '\uE004'
)

// entityRE matches HTML character references: numeric, decimal or hex,
// and named.
var entityRE = regexp.MustCompile(`&(?:#[0-9]+|#[xX][0-9a-fA-F]+|[A-Za-z][A-Za-z0-9]*);`)

// sanitise strips what a terminal could act on (see brief.StripControl)
// and the runes placeholders are made of from Markdown to be rendered.
// Character references that would decode to either, such as "&#27;", are
// removed first: goldmark decodes them after this runs.
func sanitise(md string) string {
	md = entityRE.ReplaceAllStringFunc(md, func(ref string) string {
		if unsafeEntity(ref) {
			return ""
		}
		return ref
	})
	return strings.Map(func(r rune) rune {
		if r >= tokStart && r <= itemMark {
			return -1
		}
		return r
	}, brief.StripControl(md))
}

// unsafeEntity reports whether the character reference ref decodes to a
// control character other than newline and tab, an invisible one (see
// brief.IsInvisible), a placeholder rune, or no character at all.
func unsafeEntity(ref string) bool {
	var decoded string
	if ref[1] == '#' {
		var n uint64
		var err error
		if ref[2] == 'x' || ref[2] == 'X' {
			n, err = strconv.ParseUint(ref[3:len(ref)-1], 16, 32)
		} else {
			n, err = strconv.ParseUint(ref[2:len(ref)-1], 10, 32)
		}
		if err != nil || n > unicode.MaxRune {
			return true
		}
		decoded = string(rune(n))
	} else {
		decoded = html.UnescapeString(ref)
	}
	return strings.ContainsFunc(decoded, func(r rune) bool {
		return (r >= tokStart && r <= itemMark) || brief.StripControl(string(r)) == ""
	})
}

// termSafe is rendered output with no escape sequence but SGR ("ESC [ …
// m") and OSC 8 hyperlinks ("ESC ] 8 ; params ; URI ESC \"), and no
// other control character but newline and tab: the last guard between
// web content and the terminal. Other sequences are removed whole, as are
// BEL, BS, CR, DEL, C1, invisible characters (see brief.IsInvisible) and
// invalid UTF-8.
func termSafe(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '\x1b':
			n, keep := escape(s[i:])
			if keep {
				b.WriteString(s[i : i+n])
			}
			i += n
			continue
		case r == utf8.RuneError && size == 1, brief.StripControl(string(r)) == "":
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}

// escape is the length of the escape sequence s starts with, and whether
// termSafe keeps it. A sequence cut short by another control character
// ends before it, so a newline is never swallowed.
func escape(s string) (int, bool) {
	if len(s) < 2 {
		return len(s), false
	}
	switch s[1] {
	case '[': // CSI: parameters, intermediates, a final byte
		i := 2
		for i < len(s) && s[i] >= 0x30 && s[i] <= 0x3f {
			i++
		}
		params := s[2:i]
		for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
			i++
		}
		if i == len(s) || s[i] < 0x40 || s[i] > 0x7e {
			return i, false
		}
		return i + 1, s[i] == 'm' && i == 2+len(params) && strings.Trim(params, "0123456789;:") == ""
	case ']', 'P', 'X', '^', '_': // strings, ended by ST (ESC \) or BEL
		for i := 2; i < len(s); i++ {
			switch c := s[i]; {
			case c == '\x07':
				return i + 1, false
			case c == '\x1b':
				if i+1 < len(s) && s[i+1] == '\\' {
					return i + 2, s[1] == ']' && validOSC8(s[2:i])
				}
				return i, false
			case c < 0x20 || c == 0x7f:
				return i, false
			}
		}
		return len(s), false
	}
	// Any other: intermediates and a final byte, as "ESC ( B" or "ESC c".
	i := 1
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
		i++
	}
	if i < len(s) && s[i] >= 0x30 && s[i] <= 0x7e {
		i++
	}
	return i, false
}

// validOSC8 reports whether body, an OSC string's content, is a hyperlink
// in our form, "8;id=…;URI" or "8;;" ending one, of printable characters.
func validOSC8(body string) bool {
	rest, ok := strings.CutPrefix(body, "8;")
	if !ok {
		return false
	}
	params, uri, ok := strings.Cut(rest, ";")
	if !ok || (params != "" && !strings.HasPrefix(params, "id=")) {
		return false
	}
	return !strings.ContainsFunc(uri, func(r rune) bool {
		return r == utf8.RuneError || r <= ' ' || brief.StripControl(string(r)) == ""
	})
}

// Render renders Markdown for display in width cells: control characters
// are removed, digest markers are shown as such (see preRender), http(s)
// links become OSC 8 hyperlinks that never break when wrapped, and no line
// is wider than width. Markdown that cannot be rendered comes back as it
// is, without control characters. md is taken as one digest: its items'
// tags (see brief.TagLines) show as badges.
func Render(md string, width int, theme string) string {
	out, _ := renderItems(md, width, theme, nil, brief.TagLines(md))
	return out
}

// renderItems is Render that also returns the rendered line each of
// items, lines of md such as an item's heading, starts on. The lines tags
// lists hold impact tags, shown as badges (see tagLine).
func renderItems(md string, width int, theme string, items []int, tags map[int]bool) (string, []int) {
	width = max(1, width)
	md = sanitise(md)
	pre, links, quotes := preRender(md, width, theme, items, tags)
	renderers.Lock()
	defer renderers.Unlock()
	out, ok := renderWith(pre, width, theme)
	if !ok {
		return termSafe(fitLines(md, width)), nil
	}
	if len(quotes) > 0 {
		lines := strings.Split(out, "\n")
		for i, l := range lines {
			if k := quoteIndex(ansi.Strip(l)); k >= 0 {
				lines[i] = barred(quotes[k], width, theme)
			}
		}
		out = strings.Join(lines, "\n")
	}
	lines := strings.Split(fitLines(links.restore(out), width), "\n")
	var starts []int
	for i, l := range lines {
		if strings.ContainsRune(l, itemMark) {
			starts = append(starts, i)
			lines[i] = strings.ReplaceAll(l, string(itemMark), "")
		}
	}
	return termSafe(strings.Join(lines, "\n")), starts
}

// renderWith renders md with the cached renderer for width and theme; the
// caller holds renderers' lock.
func renderWith(md string, width int, theme string) (string, bool) {
	r := renderers.m[rendererKey{width, theme}]
	if r == nil {
		var err error
		if r, err = glamour.NewTermRenderer(glamour.WithStyles(styleConfig(theme, width)), glamour.WithWordWrap(width)); err != nil {
			return "", false
		}
		renderers.m[rendererKey{width, theme}] = r
	}
	out, err := r.Render(md)
	return out, err == nil
}

// quoteToken stands for the kth blockquote paragraph until it is rendered.
func quoteToken(k int) string { return string(quoteMark) + strconv.Itoa(k) + string(quoteMark) }

var quoteTokenRE = regexp.MustCompile(`^\x{E003}(\d+)\x{E003}$`)

// quoteIndex is the number of the quote whose token is all of line, or -1.
func quoteIndex(line string) int {
	m := quoteTokenRE.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return -1
	}
	k, _ := strconv.Atoi(m[1])
	return k
}

// barred renders a blockquote paragraph two cells narrower than width and
// puts a muted "│" before each line, within the document margin. Glamour's
// own quotes count the "│ " indent as one cell, so wrapping overflows the
// line by one, and cutting it would lose the bar.
func barred(md string, width int, theme string) string {
	inner := max(1, width-2)
	out, ok := renderWith(md, inner, theme)
	if !ok {
		out = ansi.Wordwrap(md, inner, "")
	}
	lines := strings.Split(out, "\n")
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[0])) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	bar := strings.Repeat(" ", docMargin) + mutedStyle.Render("│")
	for i, l := range lines {
		lines[i] = bar + l
	}
	return strings.Join(lines, "\n")
}

// fitLines hard-wraps any line wider than width, a last resort for words
// longer than a line, and closes a hyperlink open at the end of a line,
// reopening it on the next, so no line leaves one open.
func fitLines(s string, width int) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if ansi.StringWidth(l) > width {
			lines[i] = ansi.Hardwrap(l, width, true)
		}
	}
	lines = strings.Split(strings.Join(lines, "\n"), "\n")
	open := ""
	for i, l := range lines {
		l = open + l
		open = openLink(l)
		if open != "" {
			l += linkEnd
		}
		lines[i] = l
	}
	return strings.Join(lines, "\n")
}

const linkEnd = "\x1b]8;;\x1b\\"

var osc8RE = regexp.MustCompile(`\x1b\]8;[^;\x1b]*;[^\x1b]*\x1b\\`)

// openLink is the OSC 8 sequence of the hyperlink line leaves open, or "".
func openLink(line string) string {
	open := ""
	for _, seq := range osc8RE.FindAllString(line, -1) {
		if seq == linkEnd || strings.HasSuffix(seq, ";\x1b\\") {
			open = ""
		} else {
			open = seq
		}
	}
	return open
}

// link is a hyperlink, or text in a style of its own such as a tag's
// badge, replaced by a placeholder until glamour is done.
type link struct {
	token, url, text string
	id               int
	style            *lipgloss.Style // for styled text, which has no url
}

type links []link

// sgrRE matches an SGR sequence at the start of a string.
var sgrRE = regexp.MustCompile(`^\x1b\[[0-9;:]*m`)

// restore puts each link's hyperlink, underlined, in place of its token,
// and styled text in its style. Styled text ends by resetting the
// terminal's style, so the SGR sequence that preceded it on its line is
// repeated after it, for the rest of a heading to stay bold.
func (ls links) restore(s string) string {
	if len(ls) == 0 {
		return s
	}
	var b strings.Builder
	sgr := ""
	for i := 0; i < len(s); {
		switch {
		case s[i] == '\n':
			sgr = ""
		case s[i] == '\x1b':
			if m := sgrRE.FindString(s[i:]); m != "" {
				sgr = m
				b.WriteString(m)
				i += len(m)
				continue
			}
		case strings.HasPrefix(s[i:], string(tokStart)):
			if l, ok := ls.at(s[i:]); ok {
				if l.style != nil {
					b.WriteString(l.style.Render(l.text))
					if sgr != "" && sgr != "\x1b[0m" && sgr != "\x1b[m" {
						b.WriteString(sgr)
					}
				} else {
					b.WriteString(hyperlink(l.url, l.text, l.id))
				}
				i += len(l.token)
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// at is the link whose token s starts with.
func (ls links) at(s string) (link, bool) {
	rest := s[len(string(tokStart)):]
	digits := len(rest) - len(strings.TrimLeft(rest, "0123456789"))
	k, err := strconv.Atoi(rest[:digits])
	if err != nil || k >= len(ls) || !strings.HasPrefix(s, ls[k].token) {
		return link{}, false
	}
	return ls[k], true
}

// hyperlink is text linked to url, a URL from brief.SafeWebURL, with OSC
// 8; parts of one link share an id so terminals highlight them together.
func hyperlink(url, text string, id int) string {
	return fmt.Sprintf("\x1b]8;id=db%d;%s\x1b\\\x1b[4m%s\x1b[24m"+linkEnd, id, url, text)
}

var (
	// mdLinkRE matches [text](url) links; the URL may hold balanced
	// parentheses, as Wikipedia's do.
	mdLinkRE = regexp.MustCompile(`\[([^\]]*)\]\(((?:[^()\s]|\([^()\s]*\))+)\)`)
	// autoLinkRE matches <url> autolinks and bare http(s) URLs, which end
	// at a backtick so a code span keeps its closing one.
	autoLinkRE   = regexp.MustCompile("<(https?://[^>\\s`]+)>|https?://[^\\s<>()\\[\\]`]+")
	sourceLineRE = regexp.MustCompile(`^([-*+] +)\[([^\]]*)\]\(((?:[^()\s]|\([^()\s]*\))+)\)\s*$`)
	// itemPrefixRE is the heading or list marker before an item's text.
	itemPrefixRE = regexp.MustCompile(`^\s*(?:#{1,6}\s+|(?:[-*+]|\d+[.)])\s+)?`)
)

// preRender rewrites sanitised Markdown for display; what is stored is
// untouched. Updates show as "↻ Update · …", a Related label as a rule
// and a small heading, and the impact tag on each line tags lists as a
// badge (see tagLine): no other line, so never one in a code block or a
// tag brief does not count; the lines items lists are marked with itemMark. Each http(s)
// link becomes placeholder tokens of the width of its text: "↗ domain"
// for [source](url) links and bare URLs, its title for others, and
// "title · domain" in the Sources list; other links show as their title
// alone, and never as a URL. Tokens hold no spaces, so glamour never
// breaks one; a title gets a token per word (short words joined), so it
// still wraps. Every token is cut to fit the width.
func preRender(md string, width int, theme string, items []int, tags map[int]bool) (string, links, []string) {
	var ls links
	limit := max(6, width-2*docMargin-6) // list and quote indents
	token := func(l link) string {
		l.text = truncate(l.text, limit)
		l.token = string(tokStart) + strconv.Itoa(len(ls)) + string(tokEnd)
		if w, n := lipgloss.Width(l.text), lipgloss.Width(l.token); w > n {
			l.token += strings.Repeat(string(tokPad), w-n)
		}
		ls = append(ls, l)
		return l.token
	}
	add := func(url, text string, id int) string {
		return token(link{url: url, text: text, id: id})
	}
	styled := func(text string, st *lipgloss.Style) string {
		return token(link{text: text, style: st})
	}
	linkText := func(raw, title string) string {
		id := len(ls)
		title = strings.TrimSpace(strings.NewReplacer("**", "", "__", "", "`", "").Replace(title))
		bare := title == "" || strings.EqualFold(title, "source") || strings.Contains(title, "://")
		url, ok := brief.SafeWebURL(raw)
		switch {
		case !ok && bare:
			return ""
		case !ok:
			return title
		case bare:
			d := brief.Domain(url)
			if d == "" {
				d = "link"
			}
			return add(url, "↗ "+d, id)
		}
		var toks []string
		var chunk string
		for _, w := range strings.Fields(title) {
			if chunk != "" {
				chunk += " "
			}
			if chunk += w; lipgloss.Width(chunk) >= 7 {
				toks, chunk = append(toks, add(url, chunk, id)), ""
			}
		}
		if chunk != "" {
			toks = append(toks, add(url, chunk, id))
		}
		return strings.Join(toks, " ")
	}

	lines := strings.Split(markUpdates(md), "\n")
	for i, head := range tags {
		if i >= 0 && i < len(lines) {
			next := ""
			if i+1 < len(lines) {
				next = lines[i+1]
			}
			lines[i] = tagLine(lines[i], next, theme, head, styled)
		}
	}
	for _, i := range items {
		if i >= 0 && i < len(lines) {
			n := len(itemPrefixRE.FindString(lines[i]))
			lines[i] = lines[i][:n] + string(itemMark) + lines[i][n:]
		}
	}
	inSources := false
	for i, l := range lines {
		switch t := strings.TrimSpace(l); {
		case t == "**Sources**":
			inSources = true
			continue
		case brief.IsRelated(t):
			lines[i] = "\n---\n\n###### Related\n"
			continue
		case t == "":
		case inSources && sourceLineRE.MatchString(t):
			m := sourceLineRE.FindStringSubmatch(t)
			title := m[2]
			if title == m[3] {
				title = ""
			}
			l = m[1] + linkText(m[3], title)
			if u, ok := brief.SafeWebURL(m[3]); ok && title != "" && brief.Domain(u) != "" {
				l += " · " + brief.Domain(u)
			}
			lines[i] = l
			continue
		default:
			inSources = inSources && sourceLineRE.MatchString(t)
		}
		l = mdLinkRE.ReplaceAllStringFunc(l, func(s string) string {
			m := mdLinkRE.FindStringSubmatch(s)
			return linkText(m[2], m[1])
		})
		lines[i] = autoLinkRE.ReplaceAllStringFunc(l, func(s string) string {
			u := strings.Trim(s, "<>")
			trail := ""
			if s[0] != '<' {
				trimmed := strings.TrimRight(u, ".,;:!?'\"")
				u, trail = trimmed, u[len(trimmed):]
			}
			return linkText(u, "") + trail
		})
	}
	text, quotes := extractQuotes(strings.Join(lines, "\n"))
	return text, ls, quotes
}

// tagLine shows a line that starts with an impact tag (see
// brief.SplitTag) with a badge in its place: ACT in the accent of urgency,
// WATCH in amber, FYI muted. On a body line (not head, the item's
// headline), a line of facts after the tag, such as
// "CVE-2026-1234 · CVSS 9.8 · fixed in 3.2.1", followed by the item's
// text, is muted and kept to a paragraph of its own. Without a colour
// theme the badge is "[ACT]" and the like. styled turns text into a
// placeholder shown in a style.
func tagLine(line, next, theme string, head bool, styled func(string, *lipgloss.Style) string) string {
	lead, imp, rest, ok := brief.SplitTag(line)
	if !ok {
		return line
	}
	plain := lipgloss.NewStyle()
	badge, facts := " "+imp.Label()+" ", &mutedStyle
	switch {
	case theme != themeDark && theme != themeLight:
		badge, facts = "["+imp.Label()+"]", &plain
	case imp == brief.ImpactAct:
		badge = styled(badge, &actBadge)
	case imp == brief.ImpactWatch:
		badge = styled(badge, &watchBadge)
	default:
		badge = styled(badge, &fyiBadge)
	}
	rest = strings.TrimSpace(rest)
	if head || lead != "" || !strings.Contains(rest, " · ") || strings.ContainsAny(rest, "[]<>`*_") || strings.Contains(rest, "://") || strings.TrimSpace(next) == "" {
		return lead + badge + " " + rest
	}
	// Muted facts, a placeholder per word or few, so they still wrap.
	var toks []string
	chunk := ""
	for _, w := range strings.Fields(rest) {
		if chunk != "" {
			chunk += " "
		}
		if chunk += w; lipgloss.Width(chunk) >= 7 {
			toks, chunk = append(toks, styled(chunk, facts)), ""
		}
	}
	if chunk != "" {
		toks = append(toks, styled(chunk, facts))
	}
	return badge + " " + strings.Join(toks, " ") + "\n"
}

// extractQuotes replaces each blockquote paragraph with a placeholder
// paragraph, to be rendered by barred, and returns their Markdown.
func extractQuotes(md string) (string, []string) {
	var out, para, quotes []string
	flush := func() {
		if len(para) > 0 {
			out = append(out, "", quoteToken(len(quotes)), "")
			quotes = append(quotes, strings.Join(para, "\n"))
			para = nil
		}
	}
	for _, l := range strings.Split(md, "\n") {
		t := strings.TrimSpace(l)
		switch {
		case !strings.HasPrefix(t, ">"):
			flush()
			out = append(out, l)
		case strings.TrimSpace(strings.TrimPrefix(t, ">")) == "":
			flush()
		default:
			para = append(para, strings.TrimSpace(strings.TrimPrefix(t, ">")))
		}
	}
	flush()
	return strings.Join(out, "\n"), quotes
}
