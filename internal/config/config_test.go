package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadYAML(t *testing.T, body string) (Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, created, err := Load(path)
	if created {
		t.Fatal("Load overwrote an existing file with defaults")
	}
	return cfg, err
}

// A config written before the budget feature: no max_read_minutes, prices,
// concurrency or provider, plus keys that no longer exist.
func TestLoadLegacyConfigFillsDefaults(t *testing.T) {
	cfg, err := loadYAML(t, `
token_budget: 400000
max_tokens_per_topic: 50000
max_items: 6
searches_per_topic: 5
model: claude-opus-5-5
effort: high
topics:
  - name: Java
    focus: JVM news
  - name: Security
    weight: 0
`)
	if err != nil {
		t.Fatalf("legacy config rejected: %v", err)
	}
	if cfg.TokenBudget != 400_000 || cfg.MaxReadMinutes != 9 || cfg.Theme != "auto" || cfg.Concurrency != 3 || cfg.Provider.Type != "claude-code" ||
		cfg.PriceInputPerMTok != 4 || cfg.MaxSearchesPerTopic != 0 || len(cfg.Topics) != 2 || cfg.Topics[1].Weight != 0 {
		t.Errorf("legacy config loaded as %+v", cfg)
	}
}

func TestLoadRejectsInvalidLegacyValues(t *testing.T) {
	base := "topics:\n  - name: A\n"
	cases := map[string]string{
		"token_budget: 5000\n":                           "token_budget",
		"token_budget: -1\n":                             "token_budget",
		"max_read_minutes: 0\n":                          "max_read_minutes",
		"max_searches_per_topic: -2\n":                   "max_searches_per_topic",
		"concurrency: 0\n":                               "concurrency",
		"effort: extreme\n":                              "effort",
		"price_input_per_mtok: -1\n":                     "prices",
		"provider:\n  type: bedrock\n":                   "Bedrock",
		"provider:\n  type: vertex\n":                    "project_id",
		"provider:\n  type: openai\n":                    "unknown provider",
		"provider:\n  type: anthropic\n  auth: apikey\n": "provider.auth",
		"provider: openai\n":                             "unknown provider",
		"  - name: B\n    weight: .nan\n":                "not a number",
		"  - name: B\n    weight: .inf\n":                "not a number",
		"  - name: B\n    weight: 1001\n":                "1000:1",
		"theme: solarized\n":                             "theme",
	}
	for extra, want := range cases {
		_, err := loadYAML(t, base+extra)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err %v, want one mentioning %q", strings.TrimSpace(extra), err, want)
		}
	}
	if _, err := loadYAML(t, "token_budget: 0\ntopics: []\n"); err == nil {
		t.Error("empty topics accepted")
	}
	if _, err := loadYAML(t, "topics:\n  - name: A\n    weight: -1\n"); err == nil {
		t.Error("negative weight accepted")
	}
	if _, err := loadYAML(t, "topics:\n  - name: '  '\n"); err == nil {
		t.Error("blank topic name accepted")
	}
}

func TestProviderAsPlainString(t *testing.T) {
	cfg, err := loadYAML(t, "provider: anthropic\ntopics:\n  - name: A\n")
	if err != nil || cfg.Provider != (Provider{Type: "anthropic"}) {
		t.Errorf("provider %+v, err %v", cfg.Provider, err)
	}
	cfg, err = loadYAML(t, "provider:\n  type: aws\n  region: eu-west-1\ntopics:\n  - name: A\n")
	if err != nil || cfg.Provider.Type != "aws" || cfg.Provider.Region != "eu-west-1" {
		t.Errorf("mapping provider %+v, err %v", cfg.Provider, err)
	}
}

func TestWeightRatioBoundary(t *testing.T) {
	cfg := Default()
	cfg.Topics = []Topic{{Name: "A", Weight: 0.5}, {Name: "B", Weight: 500}}
	if err := cfg.Validate(); err != nil {
		t.Errorf("1000:1 rejected: %v", err)
	}
	cfg.Topics[1].Weight = 500.5
	if err := cfg.Validate(); err == nil {
		t.Error("over 1000:1 accepted")
	}
}

func TestBudgetBoundaryValues(t *testing.T) {
	for budget, ok := range map[int64]bool{0: true, 9_999: false, 10_000: true, 50_000_000: true} {
		cfg := Default()
		cfg.TokenBudget = budget
		if err := cfg.Validate(); (err == nil) != ok {
			t.Errorf("token_budget %d: err %v", budget, err)
		}
	}
}

func TestLoadCreatesDefaultsAndRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	cfg, created, err := Load(path)
	if err != nil || !created || cfg.TokenBudget != 0 {
		t.Fatalf("first load: created %v, budget %d, err %v", created, cfg.TokenBudget, err)
	}
	cfg.TokenBudget = 123_456
	cfg.Topics[0].Weight = 2.5
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	again, created, err := Load(path)
	if err != nil || created || again.TokenBudget != 123_456 || again.Topics[0].Weight != 2.5 || len(again.Topics) != len(cfg.Topics) {
		t.Fatalf("round trip: %+v created %v err %v", again, created, err)
	}
}

// history_days defaults to 3 when absent, 0 turns it off, and it must be
// within 0-14.
func TestHistoryDays(t *testing.T) {
	cfg, err := loadYAML(t, "topics:\n  - name: A\n")
	if err != nil || cfg.HistoryDays != 3 {
		t.Fatalf("default history_days %d, %v", cfg.HistoryDays, err)
	}
	if cfg, err := loadYAML(t, "history_days: 0\ntopics:\n  - name: A\n"); err != nil || cfg.HistoryDays != 0 {
		t.Errorf("history_days 0: %d, %v", cfg.HistoryDays, err)
	}
	for _, bad := range []string{"-1", "15"} {
		if _, err := loadYAML(t, "history_days: "+bad+"\ntopics:\n  - name: A\n"); err == nil || !strings.Contains(err.Error(), "history_days") {
			t.Errorf("history_days %s accepted: %v", bad, err)
		}
	}
}

func TestThemeValues(t *testing.T) {
	for _, theme := range []string{"auto", "dark", "light"} {
		if _, err := loadYAML(t, "theme: "+theme+"\ntopics:\n  - name: A\n"); err != nil {
			t.Errorf("theme %q rejected: %v", theme, err)
		}
	}
	cfg := Default()
	cfg.Theme = "Dark"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "theme") {
		t.Errorf("theme Dark: err %v", err)
	}
}

// max_read_minutes must leave room for at least one item: one minute does
// for a few topics, not for many.
func TestReadingCapLeavesAnItem(t *testing.T) {
	cfg := Default()
	cfg.MaxReadMinutes = 1
	if err := cfg.Validate(); err != nil || ReadableItems(1, len(cfg.Topics)) < 1 {
		t.Errorf("1 minute, %d topics: err %v, %d items", len(cfg.Topics), err, ReadableItems(1, len(cfg.Topics)))
	}
	cfg.Topics = nil
	for i := range 60 {
		cfg.Topics = append(cfg.Topics, Topic{Name: fmt.Sprint("T", i)})
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "max_read_minutes") {
		t.Errorf("1 minute, 60 topics: err %v", err)
	}
	cfg.MaxReadMinutes = 2
	if err := cfg.Validate(); err != nil {
		t.Errorf("2 minutes, 60 topics: err %v", err)
	}
}

// The profile and mute list load cleaned: control and invisible
// characters gone, one line each, blank mute entries dropped. Provider's
// own "profile" is a different key.
func TestLoadProfileAndMute(t *testing.T) {
	cfg, err := loadYAML(t, "provider:\n  type: anthropic\n  auth: profile\n  profile: work\n"+
		"profile: \"Java 21,\\nQuarkus 3.x\\u001b[31m \\u202Eand Kafka\"\n"+
		"mute:\n  - crypto\n  - \"  \"\n  - \"celebrity\\ttech\\u200Bgossip\"\n"+
		"topics:\n  - name: A\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Profile != "Java 21, Quarkus 3.x [31m and Kafka" || cfg.Provider.Profile != "work" {
		t.Errorf("profile %q, provider profile %q", cfg.Profile, cfg.Provider.Profile)
	}
	// A zero-width space is removed, as brief.StripControl does, not
	// turned into a space.
	if strings.Join(cfg.Mute, "|") != "crypto|celebrity techgossip" {
		t.Errorf("mute %q", cfg.Mute)
	}
	cfg, err = loadYAML(t, "topics:\n  - name: A\n")
	if err != nil || cfg.Profile != "" || cfg.Mute != nil {
		t.Errorf("default profile %q, mute %q, %v", cfg.Profile, cfg.Mute, err)
	}
}

// The profile is at most 500 characters, the mute list at most 30 entries
// of 80 characters, counted in characters, not bytes.
func TestProfileAndMuteLimits(t *testing.T) {
	ok := Default()
	ok.Profile = strings.Repeat("é", MaxProfileChars)
	for range MaxMuteEntries {
		ok.Mute = append(ok.Mute, strings.Repeat("ü", MaxMuteChars))
	}
	if err := ok.Validate(); err != nil {
		t.Errorf("at the limits: %v", err)
	}
	for name, mod := range map[string]func(*Config){
		"profile":     func(c *Config) { c.Profile += "x" },
		"entries":     func(c *Config) { c.Mute = append(c.Mute, "x") },
		"entry chars": func(c *Config) { c.Mute[3] += "x" },
	} {
		c := ok
		c.Mute = append([]string(nil), ok.Mute...)
		mod(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s over the limit accepted", name)
		}
	}
}

// The default config file documents profile and mute with examples.
func TestDefaultConfigDocumentsProfileAndMute(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if _, _, err := Load(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# profile:", "Quarkus 3.x", "# mute:", "celebrity tech gossip"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("default config lacks %q", want)
		}
	}
	if cfg, _, err := Load(path); err != nil || cfg.Profile != "" || len(cfg.Mute) != 0 {
		t.Errorf("default config reloads with profile %q, mute %q, %v", cfg.Profile, cfg.Mute, err)
	}
}
