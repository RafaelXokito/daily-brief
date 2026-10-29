// Package config loads the user's topics and token budget from a YAML file.
package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Topic struct {
	Name string `yaml:"name"`
	// Focus tells the model what matters inside this topic.
	Focus string `yaml:"focus"`
	// Weight scales this topic's share of the token budget (default 1).
	Weight float64 `yaml:"weight,omitempty"`
}

// Provider selects how daily-brief connects to Claude.
type Provider struct {
	// Type is one of:
	//   claude-code  use the local Claude Code CLI and its login (Pro, Max,
	//                Team or Enterprise subscription); no API key needed
	//   anthropic    Claude API with an API key or an `ant auth login` profile
	//   aws          Claude Platform on AWS, with your AWS credentials
	//   vertex       Claude on Google Cloud Vertex AI, with gcloud credentials
	Type string `yaml:"type"`
	// Auth for type anthropic: keychain (key stored with `daily-brief auth
	// set-key`), profile (an `ant auth login` profile) or env (ANTHROPIC_API_KEY).
	Auth string `yaml:"auth,omitempty"`
	// Profile is the `ant` profile (anthropic) or AWS profile (aws) to use.
	Profile     string `yaml:"profile,omitempty"`
	Region      string `yaml:"region,omitempty"`       // aws, vertex
	WorkspaceID string `yaml:"workspace_id,omitempty"` // aws
	ProjectID   string `yaml:"project_id,omitempty"`   // vertex
	// ClaudePath overrides the claude binary for type claude-code.
	ClaudePath string `yaml:"claude_path,omitempty"`
}

// UnmarshalYAML also accepts the provider type alone, e.g.
// `provider: claude-code`.
func (p *Provider) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*p = Provider{Type: n.Value}
		return nil
	}
	type plain Provider
	return n.Decode((*plain)(p))
}

type Config struct {
	Provider Provider `yaml:"provider"`
	// TokenBudget is the total number of tokens (input + output, including
	// web search results) one journal aims to spend and may not exceed.
	// 0 means auto: the recommended baseline for the provider and topics.
	TokenBudget int64 `yaml:"token_budget"`
	// MaxSearchesPerTopic optionally caps web searches for a single topic
	// pass. 0 means auto: derived from the topic's share of the budget.
	MaxSearchesPerTopic int64 `yaml:"max_searches_per_topic"`
	// MaxReadMinutes caps how long the journal takes to read; it bounds the
	// number of items, not the research effort.
	MaxReadMinutes int `yaml:"max_read_minutes"`
	// Prices (USD) used only to estimate the cost of a run on API providers.
	PriceInputPerMTok  float64 `yaml:"price_input_per_mtok"`
	PriceOutputPerMTok float64 `yaml:"price_output_per_mtok"`
	PricePer1KSearches float64 `yaml:"price_per_1k_searches"`
	// HistoryDays is how many days before today's journals are read so
	// that research passes don't repeat what they reported, unless it has
	// developed. 0 turns this off.
	HistoryDays int `yaml:"history_days"`
	// Concurrency is how many topics are researched in parallel.
	Concurrency int    `yaml:"concurrency"`
	Model       string `yaml:"model"`
	// Effort is one of low, medium, high, xhigh, max.
	Effort string `yaml:"effort"`
	// Location biases search results (e.g. "Lisbon, Portugal"). Optional.
	Country string  `yaml:"country,omitempty"`
	City    string  `yaml:"city,omitempty"`
	Topics  []Topic `yaml:"topics"`
	// AllTopics are the configured topics when FilterTopics kept only
	// some of them; a run over the subset is merged with the rest.
	AllTopics []Topic `yaml:"-"`
}

func Default() Config {
	return Config{
		Provider:           Provider{Type: "claude-code"},
		Concurrency:        3,
		MaxReadMinutes:     8,
		HistoryDays:        3,
		PriceInputPerMTok:  4,
		PriceOutputPerMTok: 20,
		PricePer1KSearches: 10,
		Model:              "claude-opus-5-5",
		Effort:             "medium",
		Topics: []Topic{
			{Name: "Java & Quarkus", Focus: "OpenJDK releases and JEPs, Quarkus releases and features, Jakarta EE, Spring, GraalVM, notable JVM library updates and ecosystem news."},
			{Name: "Security", Focus: "Critical or actively exploited CVEs, supply-chain attacks on package registries (Maven Central, npm, PyPI, crates.io), vulnerabilities in programming languages, runtimes, frameworks and developer tools. Say who is affected and what to do."},
			{Name: "AI", Focus: "Major model releases, AI developer tooling, important research results, regulation and industry moves."},
			{Name: "Tech", Focus: "The most significant technology industry news: big tech, open source, cloud, hardware, developer platforms."},
			{Name: "Finance & Investing", Focus: "Market moves, macro data and central bank decisions, and news relevant to a long-term individual investor (indices, ETFs, rates). No price predictions."},
		},
	}
}

func DefaultPath() string {
	dir, err := os.UserConfigDir()
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		dir, err = xdg, nil
	} else if home, herr := os.UserHomeDir(); herr == nil {
		// Prefer ~/.config over ~/Library/Application Support on macOS.
		dir, err = filepath.Join(home, ".config"), nil
	}
	if err != nil {
		return "daily-brief.yaml"
	}
	return filepath.Join(dir, "daily-brief", "config.yaml")
}

// Load reads the config at path, writing the defaults there first if it
// does not exist. created reports whether that happened.
func Load(path string) (cfg Config, created bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg = Default()
		if err := Save(path, cfg); err != nil {
			return cfg, false, err
		}
		return cfg, true, nil
	}
	if err != nil {
		return cfg, false, err
	}
	cfg = Default()
	cfg.Topics = nil
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, false, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, false, cfg.Validate()
}

func Save(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	header := "# daily-brief configuration\n" +
		"# provider.type: claude-code (your Claude login, no key), anthropic (auth: keychain | profile | env),\n" +
		"#                aws (region, workspace_id, profile) or vertex (project_id, region).\n" +
		"# token_budget: tokens (input + output + search results) each journal aims to spend, and a cap kept by a\n" +
		"#               large safety margin (an overshoot is very unlikely, and shown if it happens);\n" +
		"#               0 = auto (the recommended baseline, see `daily-brief budget`).\n" +
		"# max_searches_per_topic: optional cap per research pass; 0 = derived from the budget.\n" +
		"# max_read_minutes: the journal is kept short enough to read in this many minutes.\n" +
		"# history_days: earlier days' journals (0-14) whose items are not repeated unless they have a material\n" +
		"#               new development, then shown as an update; 0 = off.\n" +
		"# price_*: USD prices used only to estimate the cost of API runs.\n" +
		"# weight: optional per-topic multiplier for its share of the budget.\n"
	return os.WriteFile(path, append([]byte(header), data...), 0o644)
}

// Topic weights may differ by at most this ratio.
const maxWeightRatio = 1000

func (c Config) Validate() error {
	if len(c.Topics) == 0 {
		return errors.New("config: at least one topic is required")
	}
	if c.TokenBudget != 0 && c.TokenBudget < 10_000 {
		return errors.New("config: token_budget must be 0 (auto) or at least 10000")
	}
	if c.MaxSearchesPerTopic < 0 {
		return errors.New("config: max_searches_per_topic must be 0 (auto) or positive")
	}
	if c.MaxReadMinutes < 1 {
		return errors.New("config: max_read_minutes must be at least 1")
	}
	if c.HistoryDays < 0 || c.HistoryDays > 14 {
		return errors.New("config: history_days must be between 0 (off) and 14")
	}
	if c.PriceInputPerMTok < 0 || c.PriceOutputPerMTok < 0 || c.PricePer1KSearches < 0 {
		return errors.New("config: prices must not be negative")
	}
	switch c.Effort {
	case "low", "medium", "high", "xhigh", "max":
	default:
		return fmt.Errorf("config: unknown effort %q", c.Effort)
	}
	if err := c.Provider.Validate(); err != nil {
		return err
	}
	lo, hi := math.Inf(1), 0.0
	for _, t := range c.Topics {
		if strings.TrimSpace(t.Name) == "" {
			return errors.New("config: every topic needs a name")
		}
		if math.IsNaN(t.Weight) || math.IsInf(t.Weight, 0) {
			return fmt.Errorf("config: topic %q has a weight that is not a number", t.Name)
		}
		if t.Weight < 0 {
			return fmt.Errorf("config: topic %q has a negative weight", t.Name)
		}
		w := t.Weight
		if w == 0 {
			w = 1
		}
		lo, hi = min(lo, w), max(hi, w)
	}
	if hi > maxWeightRatio*lo {
		return fmt.Errorf("config: topic weights differ by more than %d:1", maxWeightRatio)
	}
	if c.Concurrency < 1 {
		return errors.New("config: concurrency must be at least 1")
	}
	return nil
}

// FilterTopics keeps only topics whose names match one of names
// (case-insensitive). An empty list keeps everything.
func (c *Config) FilterTopics(names []string) error {
	if len(names) == 0 {
		return nil
	}
	var kept []Topic
	for _, t := range c.Topics {
		for _, n := range names {
			if strings.EqualFold(strings.TrimSpace(n), t.Name) {
				kept = append(kept, t)
				break
			}
		}
	}
	if len(kept) == 0 {
		return fmt.Errorf("no configured topic matches %v", names)
	}
	if c.AllTopics == nil {
		c.AllTopics = c.Topics
	}
	c.Topics = kept
	return nil
}

func (p Provider) Validate() error {
	switch p.Type {
	case "claude-code", "aws":
	case "anthropic":
		switch p.Auth {
		case "", "keychain", "profile", "env":
		default:
			return fmt.Errorf("config: provider.auth must be keychain, profile or env, not %q", p.Auth)
		}
	case "vertex":
		if p.ProjectID == "" || p.Region == "" {
			return errors.New("config: provider type vertex needs project_id and region")
		}
	case "bedrock":
		return errors.New("config: Amazon Bedrock does not offer the web search tool; use type aws (Claude Platform on AWS) instead")
	default:
		return fmt.Errorf("config: unknown provider type %q (want claude-code, anthropic, aws or vertex)", p.Type)
	}
	return nil
}
