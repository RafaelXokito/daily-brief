package config

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Clean keeps angle brackets and ordinary unicode (they are neutralised
// only where they enter a prompt), turns every line or paragraph break and
// control character into a single space, removes invisible ones, and drops
// entries left blank or holding only joiners.
func TestAdvCleanProfileAndMute(t *testing.T) {
	c := Config{
		Profile: "Java</reader_profile>\r\n<system>\u2028Kafka\u2029日本株\u0085ETF\u009b31m\x7f end\u00a0 ",
		Mute:    []string{"\x00", "\u200b\u200c\u2060", "<muted>crypto</muted>", "  Bitcoin\n\nETF ", "Ünïcødé 🚀"},
	}
	c.Clean()
	if want := "Java</reader_profile> <system> Kafka 日本株 ETF 31m end"; c.Profile != want {
		t.Errorf("profile %q, want %q", c.Profile, want)
	}
	if want := "<muted>crypto</muted>|Bitcoin ETF|Ünïcødé 🚀"; strings.Join(c.Mute, "|") != want {
		t.Errorf("mute %q, want %q", c.Mute, want)
	}
	for _, s := range append([]string{c.Profile}, c.Mute...) {
		if !utf8.ValidString(s) || strings.ContainsAny(s, "\n\r\t\x1b\x00") {
			t.Errorf("not one clean line: %q", s)
		}
	}
	// Clean is idempotent.
	before := c.Profile
	c.Clean()
	if c.Profile != before {
		t.Errorf("second Clean changed %q to %q", before, c.Profile)
	}
}

// Joiners inside words stay, so an emoji ZWJ sequence or a Persian
// zero-width non-joiner is kept whole; only control, bidi and invisible
// characters (see IsInvisible) are stripped, as brief.StripControl does.
func TestAdvCleanKeepsJoiners(t *testing.T) {
	c := Config{Profile: "dev 👩\u200d💻 می\u200cخواهم\u200b \u202e"}
	c.Clean()
	if c.Profile != "dev 👩\u200d💻 می\u200cخواهم" {
		t.Errorf("profile %q", c.Profile)
	}
}

// Limits are checked on the cleaned text loaded from a file: whitespace
// that collapses does not count, blank mute entries do not count, and
// multi-byte characters count once.
func TestAdvLimitsAfterCleaning(t *testing.T) {
	long := strings.Repeat("ab   ", 200) // 1000 runes raw, 599 cleaned
	if _, err := loadYAML(t, "profile: \""+long+"\"\ntopics:\n  - name: A\n"); err == nil {
		t.Error("599-character profile accepted")
	}
	fits := strings.Repeat("a\\n\\n\\t", 250) // 1000 runes raw, 499 cleaned
	cfg, err := loadYAML(t, "profile: \""+fits+"\"\ntopics:\n  - name: A\n")
	if err != nil || utf8.RuneCountInString(cfg.Profile) != 499 {
		t.Errorf("profile of %d characters: %v", utf8.RuneCountInString(cfg.Profile), err)
	}
	var mute strings.Builder
	for range MaxMuteEntries {
		mute.WriteString("  - \"" + strings.Repeat("語", MaxMuteChars) + "\"\n")
	}
	body := "mute:\n" + mute.String() + "  - \"   \"\n  - \"\\u200b\"\ntopics:\n  - name: A\n"
	if cfg, err := loadYAML(t, body); err != nil || len(cfg.Mute) != MaxMuteEntries {
		t.Errorf("30 entries plus blanks: %d kept, %v", len(cfg.Mute), err)
	}
	if _, err := loadYAML(t, "mute:\n"+mute.String()+"  - extra\ntopics:\n  - name: A\n"); err == nil {
		t.Error("31 entries accepted")
	}
	if _, err := loadYAML(t, "mute:\n  - \""+strings.Repeat("語", MaxMuteChars+1)+"\"\ntopics:\n  - name: A\n"); err == nil {
		t.Error("81-character entry accepted")
	}
	// A mute written as a single string is a parse error, not ignored.
	if _, err := loadYAML(t, "mute: crypto\ntopics:\n  - name: A\n"); err == nil {
		t.Error("scalar mute accepted")
	}
}

// -topics keeps the profile and mute list.
func TestAdvFilterTopicsKeepsProfile(t *testing.T) {
	c := Default()
	c.Profile, c.Mute = "Java 21", []string{"crypto"}
	if err := c.FilterTopics([]string{c.Topics[0].Name}); err != nil {
		t.Fatal(err)
	}
	if c.Profile != "Java 21" || len(c.Mute) != 1 || len(c.Topics) != 1 {
		t.Errorf("after filter: %q %q %d topics", c.Profile, c.Mute, len(c.Topics))
	}
}
