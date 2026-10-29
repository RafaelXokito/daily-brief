package config

import (
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
	if cfg.TokenBudget != 400_000 || cfg.MaxReadMinutes != 8 || cfg.Concurrency != 3 || cfg.Provider.Type != "claude-code" ||
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
