package brief

import (
	"errors"
	"strings"
	"testing"
	"unicode"
)

// hasUnsafe reports whether s holds a control character other than
// newline and tab, or an invisible one.
func hasUnsafe(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return (unicode.IsControl(r) && r != '\n' && r != '\t') || IsInvisible(r)
	})
}

// StripControl removes bidi controls and zero-width spaces and joiners,
// but keeps the zero-width joiner emoji need.
func TestStripControlInvisible(t *testing.T) {
	in := "a‮b‪c⁦d⁩e‎f‏g؜h​i⁠j 👩‍💻\x1b[2J\u009b\n\tk"
	if got, want := StripControl(in), "abcdefghij 👩‍💻[2J\n\tk"; got != want {
		t.Errorf("StripControl = %+q, want %+q", got, want)
	}
}

// SafeWebURL drops user information and refuses hosts with control,
// invisible or space characters.
func TestSafeWebURLHostAndUserinfo(t *testing.T) {
	for raw, want := range map[string]string{
		"https://good.com@evil.com/x":    "https://evil.com/x",
		"https://user:pw@evil.com/":      "https://evil.com/",
		"https://good.com%40x@evil.com/": "https://evil.com/",
		"https://ex.com/a@b":             "https://ex.com/a@b",
		"https://ex.com‮/moc.live":       "",
		"https://evil.com‮good.com":      "",
		"https://ex​.com/":               "",
		"https://ex.com%1b/":             "",
		"https://ex.com%E2%80%AE/":       "",
		"https://ex.com%20x/":            "",
		"https://[fe80::1%25%1b]/":       "",
		"https://xn--pple-43d.com/":      "https://xn--pple-43d.com/",
		"https://ex.com/%1b%5d8":         "https://ex.com/%1b%5d8",
		"https://ex.com/path‮reverse":    "https://ex.com/path%E2%80%AEreverse",
	} {
		got, ok := SafeWebURL(raw)
		if got != want || ok != (want != "") {
			t.Errorf("SafeWebURL(%+q) = %+q, %v; want %+q", raw, got, ok, want)
		}
	}
}

// Domain shows non-ASCII hosts as punycode, so lookalikes stand out.
func TestDomainPunycode(t *testing.T) {
	for raw, want := range map[string]string{
		"https://аpple.com/x":             "xn--pple-43d.com",
		"https://www.аpple.com/":          "xn--pple-43d.com",
		"https://bücher.example.de/x":     "xn--bcher-kva.example.de",
		"https://WWW.Example.COM/":        "example.com",
		"https://xn--pple-43d.com/":       "xn--pple-43d.com",
		"https://%D0%B0pple.com/":         "xn--pple-43d.com",
		"https://[::1]:8080/":             "::1",
		"https://user@example.org:8443/p": "example.org",
	} {
		if got := Domain(raw); got != want {
			t.Errorf("Domain(%q) = %q, want %q", raw, got, want)
		}
	}
}

// Control characters in a backend's digest, sources and error, and in
// the overview's reply, never reach the journal, so its Markdown, saved
// or piped, is clean.
func TestModelOutputEntersJournalClean(t *testing.T) {
	dirty := "\x1b]0;pwned\x07\x1b[2J\u009b31m\r\b\x7f‮"
	sec := &Section{Topic: "Security"}
	apply(sec, Result{
		Markdown: "### Patch now" + dirty + "\nBody " + dirty + " [source](https://a.example/x)",
		Sources:  []Source{{Title: "Advisory" + dirty, URL: "https://a.example/x" + dirty}},
	})
	failed := &Section{Topic: "AI"}
	apply(failed, Result{Err: errors.New("upstream said " + dirty)})
	pass := &Section{Topic: "Java", Status: StatusDone, Markdown: "### Old\nBody."}
	applyPass(pass, Result{Markdown: "### New" + dirty + "\nBody.", Sources: []Source{{Title: dirty, URL: "https://j.example"}}})
	capped := capItems("### Capped"+dirty+"\nBody.", 3)

	ov := Section{Topic: "Overview", Status: StatusDone}
	reply := `{"highlights":[{"tier":"act","topic":"Security","headline":"Patch now","text":"Do it ` +
		`\u001b]0;x\u0007 \u009b2J","why":"Because ‮"}]}`
	clean := &Section{Topic: "Security", Status: StatusDone, Markdown: "### Patch now\nBody."}
	ov.Markdown, ov.Highlights = capOverview(reply, []*Section{clean})
	if len(ov.Highlights) != 1 {
		t.Fatalf("highlights: %+q", ov.Highlights)
	}
	prose, _ := capOverview("- Something "+dirty+" happened.\n- Else.", []*Section{clean})

	j := &Journal{Date: "2026-10-29", Overview: ov, Sections: []*Section{sec, failed, pass}}
	for label, s := range map[string]string{
		"markdown": j.Markdown(), "error": failed.Error, "capItems": capped, "prose": prose,
		"highlight": ov.Highlights[0].Text + ov.Highlights[0].Why,
	} {
		if hasUnsafe(s) {
			t.Errorf("%s has control characters: %+q", label, s)
		}
	}
	if !strings.Contains(j.Markdown(), "### Patch now]0;pwned[2J31m") {
		t.Errorf("text around the controls was lost:\n%s", j.Markdown())
	}
	if sec.Sources[0].URL != "https://a.example/x]0;pwned[2J31m" || len(pass.Sources) != 1 {
		t.Errorf("sources: %+q %+q", sec.Sources, pass.Sources)
	}
	empty := &Section{}
	if apply(empty, Result{Markdown: "x"}); empty.Sources != nil {
		t.Errorf("nil sources became %#v", empty.Sources)
	}
}
