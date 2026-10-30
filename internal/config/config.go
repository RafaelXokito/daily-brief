// Package config loads the user's topics and token budget from a YAML file.
package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

type Topic struct {
	Name string `yaml:"name"`
	// Focus tells the model what matters inside this topic.
	Focus string `yaml:"focus"`
	// Weight scales this topic's share of the token budget (default 1).
	Weight float64 `yaml:"weight,omitempty"`
	// world marks the section general adds (see WorldTopic), never a
	// configured topic, even one that takes its name.
	world bool
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
	// Theme is the TUI's colours: auto (GLAMOUR_STYLE if set, else the
	// terminal's background), dark or light.
	Theme string `yaml:"theme"`
	// Mouse lets the TUI take the mouse: a click opens a topic or highlight
	// and the wheel scrolls, while native selection needs Shift. Unset (nil)
	// means on; use MouseOn to read it.
	Mouse *bool `yaml:"mouse,omitempty"`
	// UpdateCheck lets the TUI ask GitHub once a day whether a newer
	// release exists, and show it in the header. Unset (nil) means on; use
	// UpdateCheckOn to read it.
	UpdateCheck *bool `yaml:"update_check,omitempty"`
	// Location biases search results (e.g. "Lisbon, Portugal"). Optional.
	Country string `yaml:"country,omitempty"`
	City    string `yaml:"city,omitempty"`
	// Profile describes the reader, such as their stack or investments, so
	// research passes rank and explain items for them. Optional.
	Profile string `yaml:"profile,omitempty"`
	// Mute lists subjects or keywords research passes leave out. Optional.
	Mute []string `yaml:"mute,omitempty"`
	// General adds a "World & hot topics" section (see EffectiveTopics):
	// the day's most significant world events and trending stories beside
	// the configured topics. Off by default.
	General bool    `yaml:"general"`
	Topics  []Topic `yaml:"topics"`
	// AllTopics are the effective topics (see EffectiveTopics: the
	// configured ones, then the World section when general is on) when
	// FilterTopics kept only some of them; a run over the subset is merged
	// with the rest.
	AllTopics []Topic `yaml:"-"`
}

func Default() Config {
	return Config{
		Provider:           Provider{Type: "claude-code"},
		Concurrency:        3,
		MaxReadMinutes:     9,
		HistoryDays:        3,
		PriceInputPerMTok:  4,
		PriceOutputPerMTok: 20,
		PricePer1KSearches: 10,
		Model:              "claude-opus-5-5",
		Effort:             "medium",
		Theme:              "auto",
		Mouse:              new(true),
		UpdateCheck:        new(true),
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
	if cfg, err = parse(path, data); err != nil {
		return cfg, false, err
	}
	return cfg, false, cfg.Validate()
}

func Save(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// marshal is cfg as Save writes it: a header documenting the keys, then
// the YAML.
func marshal(cfg Config) ([]byte, error) {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
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
		"# theme: auto (GLAMOUR_STYLE if set, else the terminal's background), dark or light.\n" +
		"# mouse: true (click a topic or highlight to open it, the wheel scrolls; Shift-drag selects text,\n" +
		"#        Shift-click opens links, Cmd in Ghostty) or false (the terminal keeps the mouse).\n" +
		"# update_check: true (once a day, ask api.github.com whether a newer release exists and show it in the\n" +
		"#               header; `daily-brief update` installs it) or false (never check).\n" +
		"# price_*: USD prices used only to estimate the cost of API runs.\n" +
		"# weight: optional per-topic multiplier for its share of the budget.\n" +
		"# profile: optional, about you (at most 500 characters): items that affect it rank higher and say how, e.g.\n" +
		"#          profile: \"Java 21, Quarkus 3.x, Hibernate, Kafka; EU ETF investor\"\n" +
		"# mute: optional subjects or keywords to leave out (at most 30, 80 characters each), e.g.\n" +
		"#          mute: [crypto, celebrity tech gossip]\n" +
		"# general: true adds a \"World & hot topics\" section (the day's major world events and trending stories,\n" +
		"#          skipping what your topics cover), researched and budgeted like a topic of weight 1 within\n" +
		"#          token_budget; false (default) = only your topics.\n"
	return append([]byte(header), data...), nil
}

// Topic weights may differ by at most this ratio.
const maxWeightRatio = 1000

// Upper bounds of the numeric settings. A pass on the API makes at most a
// dozen search requests (brief's maxRequests), so a higher search cap only
// matters on claude-code, where 50 is already far past any budget.
const (
	MaxSearchesPerTopic = 50
	MaxReadMinutes      = 120
	MaxConcurrency      = 16
	MaxPrice            = 1000
)

// Limits of the reader profile and mute list, in characters.
const (
	MaxProfileChars = 500
	MaxMuteEntries  = 30
	MaxMuteChars    = 80
)

// Clean strips control and invisible characters (see IsInvisible) from
// the model, location and provider settings, and from the profile and mute
// list, as brief.StripControl does from what the
// model writes, puts each on one line and drops empty mute entries. The
// joiners emoji and some scripts need stay.
func (c *Config) Clean() {
	for _, s := range []*string{&c.Model, &c.Country, &c.City, &c.Provider.Type, &c.Provider.Auth, &c.Provider.Profile,
		&c.Provider.Region, &c.Provider.WorkspaceID, &c.Provider.ProjectID, &c.Provider.ClaudePath} {
		*s = stripControls(*s)
	}
	c.Profile = cleanText(c.Profile)
	var mute []string
	for _, m := range c.Mute {
		if m = cleanText(m); m != "" {
			mute = append(mute, m)
		}
	}
	c.Mute = mute
	if c.Mouse == nil {
		// An unset (or null) mouse is on.
		c.Mouse = new(true)
	}
	if c.UpdateCheck == nil {
		// An unset (or null) update_check is on.
		c.UpdateCheck = new(true)
	}
}

// MouseOn reports whether the TUI takes the mouse: true unless mouse is
// set to false.
func (c Config) MouseOn() bool {
	return c.Mouse == nil || *c.Mouse
}

// UpdateCheckOn reports whether the TUI checks for a newer release: true
// unless update_check is set to false.
func (c Config) UpdateCheckOn() bool {
	return c.UpdateCheck == nil || *c.UpdateCheck
}

// stripControls removes control and invisible characters from a one-line
// value and trims it; unlike cleanText it leaves inner spaces, which a
// path may need.
func stripControls(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if IsInvisible(r) || unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
}

func cleanText(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case IsInvisible(r):
			return -1
		case unicode.IsControl(r):
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if strings.Trim(s, "\u200c\u200d") == "" {
		// Joiners alone show nothing.
		return ""
	}
	return s
}

// IsInvisible reports whether r is a bidirectional control, which can
// make text read differently from what it is, or a zero-width space or
// word joiner, which can hide in it. The zero-width joiner and non-joiner,
// which emoji sequences and some scripts need, are not.
func IsInvisible(r rune) bool {
	switch {
	case r >= '\u202A' && r <= '\u202E', r >= '\u2066' && r <= '\u2069':
		return true
	}
	switch r {
	case '\u200E', '\u200F', '\u061C', '\u200B', '\u2060':
		return true
	}
	return false
}

func (c Config) Validate() error {
	if len(c.Topics) == 0 {
		return errors.New("config: at least one topic is required")
	}
	if c.TokenBudget != 0 && c.TokenBudget < 10_000 {
		return errors.New("config: token_budget must be 0 (auto) or at least 10000")
	}
	if c.MaxSearchesPerTopic < 0 || c.MaxSearchesPerTopic > MaxSearchesPerTopic {
		return fmt.Errorf("config: max_searches_per_topic must be 0 (auto) or 1 to %d", MaxSearchesPerTopic)
	}
	if c.MaxReadMinutes < 1 || c.MaxReadMinutes > MaxReadMinutes {
		return fmt.Errorf("config: max_read_minutes must be between 1 and %d", MaxReadMinutes)
	}
	if n := max(len(c.EffectiveTopics()), len(c.AllTopics)); ReadableItems(c.MaxReadMinutes, n) == 0 {
		across := fmt.Sprint(n)
		if c.General {
			across = fmt.Sprintf("%d (%d + %s)", n, n-1, WorldTopicName)
		}
		return fmt.Errorf("config: max_read_minutes %d leaves no room for a single item across %s topics; raise it or remove topics", c.MaxReadMinutes, across)
	}
	if c.HistoryDays < 0 || c.HistoryDays > 14 {
		return errors.New("config: history_days must be between 0 (off) and 14")
	}
	for _, p := range []float64{c.PriceInputPerMTok, c.PriceOutputPerMTok, c.PricePer1KSearches} {
		if p < 0 || p > MaxPrice || math.IsNaN(p) {
			return fmt.Errorf("config: prices must be between 0 and %d USD", MaxPrice)
		}
	}
	switch c.Effort {
	case "low", "medium", "high", "xhigh", "max":
	default:
		return fmt.Errorf("config: unknown effort %q", c.Effort)
	}
	switch c.Theme {
	case "", "auto", "dark", "light":
	default:
		return fmt.Errorf("config: theme must be auto, dark or light, not %q", c.Theme)
	}
	if err := c.Provider.Validate(); err != nil {
		return err
	}
	// Only the section general adds may take its name, whether or not
	// FilterTopics ran.
	if slices.ContainsFunc(slices.Concat(c.Topics, c.AllTopics), reservedName) {
		return fmt.Errorf("config: the topic name %q is reserved for the general section; rename that topic", WorldTopicName)
	}
	lo, hi := math.Inf(1), 0.0
	for _, t := range c.EffectiveTopics() {
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
		if c.General {
			return fmt.Errorf("config: topic weights differ by more than %d:1 (the %s section has weight 1)", maxWeightRatio, WorldTopicName)
		}
		return fmt.Errorf("config: topic weights differ by more than %d:1", maxWeightRatio)
	}
	if c.Concurrency < 1 || c.Concurrency > MaxConcurrency {
		return fmt.Errorf("config: concurrency must be between 1 and %d", MaxConcurrency)
	}
	if n := utf8.RuneCountInString(c.Profile); n > MaxProfileChars {
		return fmt.Errorf("config: profile is %d characters; keep it to %d", n, MaxProfileChars)
	}
	if len(c.Mute) > MaxMuteEntries {
		return fmt.Errorf("config: mute has %d entries; keep it to %d", len(c.Mute), MaxMuteEntries)
	}
	for _, m := range c.Mute {
		if utf8.RuneCountInString(m) > MaxMuteChars {
			return fmt.Errorf("config: mute entry %q is longer than %d characters", m, MaxMuteChars)
		}
	}
	return nil
}

// The reading-time model behind max_read_minutes, which brief's item caps
// follow.
const (
	ReadingWPM   = 230
	WordsPerItem = 45 // headline plus at most two tight sentences
	// OverviewWords is the most the overview's highlights, numbers and
	// status lines take; shorter reading caps give it a third of them.
	OverviewWords = 180
	// Words a Related label may add to a topic beside its items.
	RelatedAllowance = 3
)

// OverviewAllowance is the overview's share of a reading cap of minutes.
func OverviewAllowance(minutes int) int {
	return min(OverviewWords, minutes*ReadingWPM/3)
}

// ReadableItems is how many items fit in minutes beside the overview and
// each of topics' Related label.
func ReadableItems(minutes, topics int) int {
	return max(0, (minutes*ReadingWPM-OverviewAllowance(minutes)-topics*RelatedAllowance)/WordsPerItem)
}

// WorldTopicName names the section general adds; no configured topic may
// take it (case-insensitive).
const WorldTopicName = "World & hot topics"

func isWorld(t Topic) bool { return t.world }

// reservedName reports whether t is a configured topic that takes the
// World section's name (case-insensitive).
func reservedName(t Topic) bool {
	return !t.world && strings.EqualFold(strings.TrimSpace(t.Name), WorldTopicName)
}

// WorldTopic is the section general adds beside topics, told to skip
// what they cover.
func WorldTopic(topics []Topic) Topic {
	names := make([]string, 0, len(topics))
	for _, t := range topics {
		names = append(names, t.Name)
	}
	return Topic{
		Name:   WorldTopicName,
		Weight: 1,
		Focus: "The most significant world events and the stories trending today across politics, economy, science and society. " +
			"Skip anything the reader's other topics already cover: " + strings.Join(names, ", ") + ". " +
			"Prefer primary or reputable sources; no rumours.",
		world: true,
	}
}

// EffectiveTopics are the topics a run researches and budgets for: the
// configured ones, then the World section when general is on. Topics that
// already include that section, and those FilterTopics kept (chosen among
// the effective ones), are returned as they are, so applying it again
// changes nothing. A configured topic merely named like it does not count:
// Validate rejects it.
func (c Config) EffectiveTopics() []Topic {
	if !c.General || c.AllTopics != nil || slices.ContainsFunc(c.Topics, isWorld) {
		return c.Topics
	}
	return append(slices.Clip(c.Topics), WorldTopic(c.Topics))
}

// FilterTopics keeps only topics whose names match one of names
// (case-insensitive), among the effective ones, so the World section can
// be chosen too. An empty list keeps everything.
func (c *Config) FilterTopics(names []string) error {
	if len(names) == 0 {
		return nil
	}
	all := c.EffectiveTopics()
	var kept []Topic
	for _, t := range all {
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
		c.AllTopics = all
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
