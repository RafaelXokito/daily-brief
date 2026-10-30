package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// userConfig is a hand-written config: comments everywhere, its own key
// order, two-space indents, a flow-style mute list, the short provider
// form and keys daily-brief doesn't know.
const userConfig = `# My daily-brief config
# (hand-written)

provider: claude-code   # my subscription
theme: dark
token_budget: 400000    # about a dollar
my_note: keep me        # not a daily-brief key
mute: [crypto, "celebrity gossip"]
profile: "Java 21, Quarkus"
topics:
  # the important one
  - name: Security
    focus: CVEs
    weight: 2
    colour: red         # unknown topic key
  - name: AI
    focus: models
# trailing comment
`

// stamped writes body to a temporary config and loads it stamped.
func stamped(t *testing.T, body string) (string, Config, Stamp) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, st, err := LoadStamped(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	return path, cfg, st
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// mustLoad loads path as daily-brief does at start.
func mustLoad(t *testing.T, path string) Config {
	t.Helper()
	cfg, created, err := Load(path)
	if err != nil || created {
		t.Fatalf("load: created %v, %v", created, err)
	}
	return cfg
}

// Saving unchanged writes the file back byte for byte.
func TestSaveEditsUnchangedKeepsTheFile(t *testing.T) {
	path, cfg, st := stamped(t, userConfig)
	if _, err := SaveEdits(path, st, cfg); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != userConfig {
		t.Errorf("unchanged save rewrote the file:\n%s", got)
	}
}

// Edits change only their values: comments, order, unknown keys, the
// flow list's style and the short provider form all stay, and the file
// loads as the edited config.
func TestSaveEditsKeepsCommentsOrderAndUnknownKeys(t *testing.T) {
	path, cfg, st := stamped(t, userConfig)
	cfg.TokenBudget = 0
	cfg.Theme = "light"
	cfg.HistoryDays = 7
	cfg.Mute = append(cfg.Mute, "sports")
	cfg.Profile = "Go, Postgres"
	cfg.Provider.Type = "aws"
	if _, err := SaveEdits(path, st, cfg); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	for _, want := range []string{"# My daily-brief config", "# my subscription", "# about a dollar", "my_note: keep me",
		"# not a daily-brief key", "# the important one", "colour: red", "# unknown topic key", "# trailing comment",
		"provider: aws", "theme: light", "token_budget: 0", "history_days: 7", `profile: "Go, Postgres"`,
		"mute: [crypto, \"celebrity gossip\", sports]", "  - name: Security"} {
		if !strings.Contains(got, want) {
			t.Errorf("saved file lacks %q:\n%s", want, got)
		}
	}
	// Order: the user's keys in their order, new ones at the end.
	order := []string{"provider:", "theme:", "token_budget:", "my_note:", "mute:", "profile:", "topics:", "history_days:"}
	last := -1
	for _, k := range order {
		i := strings.Index(got, "\n"+k)
		if i < last {
			t.Errorf("%s is out of order:\n%s", k, got)
		}
		last = i
	}
	// Keys holding their default aren't added.
	for _, k := range []string{"concurrency:", "max_read_minutes:", "price_input_per_mtok:", "model:"} {
		if strings.Contains(got, k) {
			t.Errorf("default %s was added:\n%s", k, got)
		}
	}
	if back := mustLoad(t, path); !reflect.DeepEqual(back, cfg) {
		t.Errorf("round trip:\n got %+v\nwant %+v", back, cfg)
	}
}

// A file from Save (four-space indents, every key) round-trips each edit,
// and clearing an optional value removes its key.
func TestSaveEditsRoundTripsEveryField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if _, _, err := Load(path); err != nil {
		t.Fatal(err)
	}
	cfg, st, err := LoadStamped(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Provider = Provider{Type: "vertex", ProjectID: "proj-1", Region: "europe-west4"}
	cfg.TokenBudget = 250_000
	cfg.MaxSearchesPerTopic = 4
	cfg.MaxReadMinutes = 12
	cfg.PriceInputPerMTok = 3.5
	cfg.HistoryDays = 0
	cfg.Concurrency = 5
	cfg.Model = "claude-sonnet-5"
	cfg.Effort = "high"
	cfg.Theme = "dark"
	cfg.Country, cfg.City = "PT", "Lisbon"
	cfg.Profile = "about me: \"quoted\" # not a comment"
	cfg.Mute = []string{"crypto", "- dash", "yes"}
	if st, err = SaveEdits(path, st, cfg); err != nil {
		t.Fatal(err)
	}
	if back := mustLoad(t, path); !reflect.DeepEqual(back, cfg) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", back, cfg)
	}
	if got := readFile(t, path); !strings.HasPrefix(got, "# daily-brief configuration") || !strings.Contains(got, "\n    type: vertex") {
		t.Errorf("header or indent lost:\n%s", got)
	}

	cfg.Provider = Provider{Type: "anthropic", Auth: "env"}
	cfg.Mute, cfg.Profile, cfg.City = nil, "", ""
	if _, err = SaveEdits(path, st, cfg); err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(readFile(t, path), "\n") {
		for _, gone := range []string{"project_id:", "region:", "mute:", "profile:", "city:"} {
			if strings.HasPrefix(strings.TrimSpace(l), gone) {
				t.Errorf("cleared %s is still there: %q", gone, l)
			}
		}
	}
	if back := mustLoad(t, path); !reflect.DeepEqual(back, cfg) {
		t.Fatalf("second round trip:\n got %+v\nwant %+v", back, cfg)
	}
}

// The short provider form takes extra keys by becoming a mapping, keeping
// its comment.
func TestSaveEditsExpandsShortProvider(t *testing.T) {
	path, cfg, st := stamped(t, userConfig)
	cfg.Provider = Provider{Type: "anthropic", Auth: "profile", Profile: "work"}
	if _, err := SaveEdits(path, st, cfg); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	if !strings.Contains(got, "provider: # my subscription\n") || !strings.Contains(got, "auth: profile") || !strings.Contains(got, "theme: dark\n") {
		t.Errorf("provider not expanded with its comment:\n%s", got)
	}
	if back := mustLoad(t, path); !reflect.DeepEqual(back.Provider, cfg.Provider) {
		t.Errorf("provider %+v", back.Provider)
	}
}

// Any change to the file since it was loaded, its contents or only its
// modification time, makes SaveEdits refuse and leaves the file alone.
func TestSaveEditsRefusesAChangedFile(t *testing.T) {
	for name, change := range map[string]func(path string) error{
		"contents": func(path string) error {
			return os.WriteFile(path, []byte(strings.Replace(userConfig, "theme: dark", "theme: auto", 1)), 0o600)
		},
		"mtime": func(path string) error {
			later := time.Now().Add(time.Hour)
			return os.Chtimes(path, later, later)
		},
		"removed": os.Remove,
	} {
		path, cfg, st := stamped(t, userConfig)
		if err := change(path); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(path)
		cfg.HistoryDays = 9
		if _, err := SaveEdits(path, st, cfg); err == nil || name != "removed" && !errors.Is(err, ErrChanged) {
			t.Errorf("%s: save over a changed file: %v", name, err)
		}
		if after, _ := os.ReadFile(path); string(after) != string(before) {
			t.Errorf("%s: the changed file was written", name)
		}
	}
}

// An invalid config writes nothing.
func TestSaveEditsRejectsInvalidConfig(t *testing.T) {
	for name, mod := range map[string]func(*Config){
		"history_days": func(c *Config) { c.HistoryDays = 15 },
		"budget":       func(c *Config) { c.TokenBudget = 5_000 },
		"profile":      func(c *Config) { c.Profile = strings.Repeat("x", MaxProfileChars+1) },
		"mute": func(c *Config) {
			for len(c.Mute) <= MaxMuteEntries {
				c.Mute = append(c.Mute, "m"+string(rune('a'+len(c.Mute))))
			}
		},
		"provider": func(c *Config) { c.Provider.Type = "bedrock" },
	} {
		path, cfg, st := stamped(t, userConfig)
		mod(&cfg)
		if _, err := SaveEdits(path, st, cfg); err == nil {
			t.Errorf("%s: invalid config saved", name)
		}
		if got := readFile(t, path); got != userConfig {
			t.Errorf("%s: the file was written", name)
		}
	}
}

// The write is atomic: the file keeps its mode, a symlink stays a link to
// the edited file, and no temporary file is left behind.
func TestSaveEditsAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.yaml")
	if err := os.WriteFile(target, []byte(userConfig), 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("no symlinks:", err)
	}
	cfg, st, err := LoadStamped(link)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Theme = "auto"
	st2, err := SaveEdits(link, st, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the symlink was replaced: %v", err)
	}
	if fi, err := os.Stat(target); err != nil || fi.Mode().Perm() != 0o640 {
		t.Errorf("mode %v, %v", fi.Mode().Perm(), err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("left behind: %v", entries)
	}
	// The returned stamp is the new file's: a second save goes through.
	cfg.Theme = "light"
	if _, err := SaveEdits(link, st2, cfg); err != nil {
		t.Errorf("second save: %v", err)
	}
	if mustLoad(t, link).Theme != "light" {
		t.Error("second save lost")
	}
}

// LoadStamped neither creates a missing file nor validates.
func TestLoadStamped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if _, _, err := LoadStamped(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("LoadStamped created the file")
	}
	os.WriteFile(path, []byte("history_days: 20\ntopics:\n  - name: A\n"), 0o644)
	cfg, _, err := LoadStamped(path)
	if err != nil || cfg.HistoryDays != 20 || cfg.Validate() == nil {
		t.Errorf("invalid file: %v, %+v", err, cfg)
	}
	os.WriteFile(path, []byte("topics: [\n"), 0o644)
	if _, _, err := LoadStamped(path); err == nil {
		t.Error("unparsable file loaded")
	}
}

// Every key the settings edit has a hint.
func TestHintsCoverEditableKeys(t *testing.T) {
	for k, ft := range yamlKeys(reflect.TypeOf(Config{})) {
		if ft.Kind() == reflect.Struct {
			for pk := range yamlKeys(ft) {
				if Hints[k+"."+pk] == "" {
					t.Errorf("no hint for %s.%s", k, pk)
				}
			}
			continue
		}
		if Hints[k] == "" {
			t.Errorf("no hint for %s", k)
		}
	}
}

// Clearing a commented optional key keeps it, empty, with its comments;
// the file's first comment stays even when the first key is cleared.
func TestSaveEditsClearingKeepsComments(t *testing.T) {
	body := "# top of file\ncountry: PT # where I live\n# my city\ncity: Lisbon\nprofile: me\n" +
		"mute:\n  - a # first\n  - b # second\n  - c # third\ntopics:\n  - name: A\n"
	path, cfg, st := stamped(t, body)
	cfg.Country, cfg.City, cfg.Profile = "", "", ""
	cfg.Mute = []string{"a", "c"}
	if _, err := SaveEdits(path, st, cfg); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	for _, want := range []string{"# top of file", "# where I live", "# my city", "- a # first", "- c # third"} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "second") || strings.Contains(got, "profile:") {
		t.Errorf("deleted entry's comment or uncommented key kept:\n%s", got)
	}
	if back := mustLoad(t, path); !reflect.DeepEqual(back, cfg) {
		t.Errorf("round trip %+v", back)
	}
	// Editing an entry in place keeps its comment.
	path, cfg, st = stamped(t, body)
	cfg.Mute[1] = "bee"
	if _, err := SaveEdits(path, st, cfg); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); !strings.Contains(got, "- bee # second") {
		t.Errorf("edited entry lost its comment:\n%s", got)
	}
}

// Line endings, the byte order mark and characters above U+FFFF come out
// as they went in.
func TestSaveEditsKeepsEncoding(t *testing.T) {
	body := "\ufeff# 🦀 crab\r\nmy_note: 🦀 plain # 🏔\r\ntopics:\r\n  - name: A\r\n"
	path, cfg, st := stamped(t, body)
	cfg.City = "Zürich 🏔"
	if _, err := SaveEdits(path, st, cfg); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	if !strings.HasPrefix(got, "\ufeff# 🦀 crab\r\n") || !strings.Contains(got, "my_note: 🦀 plain # 🏔\r\n") ||
		!strings.Contains(got, "Zürich 🏔") || strings.Contains(got, `\U`) || strings.Count(got, "\n") != strings.Count(got, "\r\n") {
		t.Errorf("encoding changed: %q", got)
	}
}

// Files SaveEdits can't edit faithfully are refused with a clear error and
// left alone: several documents, a value shared through an anchor, a
// read-only file.
func TestSaveEditsRefusesUnsafeFiles(t *testing.T) {
	for name, c := range map[string]struct{ body, want string }{
		"documents": {"topics:\n  - name: A\n---\nother: doc\n", "more than one YAML document"},
		"anchor":    {"history_days: &d 3\nmy_days: *d\ntopics:\n  - name: A\n", "anchor"},
	} {
		path, cfg, st := stamped(t, c.body)
		cfg.HistoryDays = 5
		if _, err := SaveEdits(path, st, cfg); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
		if readFile(t, path) != c.body {
			t.Errorf("%s: written", name)
		}
	}
	if os.Geteuid() != 0 {
		path, cfg, st := stamped(t, userConfig)
		os.Chmod(path, 0o400)
		path2, cfg2, st2 := path, cfg, st
		cfg2.HistoryDays = 5
		if _, err := SaveEdits(path2, st2, cfg2); err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Errorf("read-only file: %v", err)
		}
	}
}

// Numbers have upper bounds, and text settings lose control characters.
func TestValidateBoundsAndCleanText(t *testing.T) {
	for name, mod := range map[string]func(*Config){
		"searches":    func(c *Config) { c.MaxSearchesPerTopic = 200_000 },
		"minutes":     func(c *Config) { c.MaxReadMinutes = MaxReadMinutes + 1 },
		"concurrency": func(c *Config) { c.Concurrency = MaxConcurrency + 1 },
		"price":       func(c *Config) { c.PricePer1KSearches = MaxPrice + 1 },
	} {
		c := Default()
		mod(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s over its bound accepted", name)
		}
	}
	c := Default()
	c.MaxSearchesPerTopic, c.MaxReadMinutes, c.Concurrency, c.PriceInputPerMTok = MaxSearchesPerTopic, MaxReadMinutes, MaxConcurrency, MaxPrice
	if err := c.Validate(); err != nil {
		t.Errorf("at the bounds: %v", err)
	}
	c.Model = " claude\x1b[31m-x\u202e "
	c.Provider.ClaudePath = "/My  Apps/claude\x07"
	c.Clean()
	if c.Model != "claude[31m-x" || c.Provider.ClaudePath != "/My  Apps/claude" {
		t.Errorf("cleaned model %q, path %q", c.Model, c.Provider.ClaudePath)
	}
}

// mouse round-trips through SaveEdits: false is added to a file without
// it, keeping its comments; on again is written over it; and a file
// without the key gets none for other edits, as on is the default.
func TestSaveEditsMouseRoundTrip(t *testing.T) {
	path, cfg, st := stamped(t, userConfig)
	if !cfg.MouseOn() {
		t.Fatal("mouse off without the key")
	}
	cfg.TokenBudget = 500_000
	st, err := SaveEdits(path, st, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); strings.Contains(got, "mouse") {
		t.Errorf("an unchanged default mouse was written:\n%s", got)
	}

	cfg.Mouse = new(false)
	if st, err = SaveEdits(path, st, cfg); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	if !strings.Contains(got, "\nmouse: false\n") || !strings.Contains(got, "my_note: keep me # not a daily-brief key") ||
		!strings.Contains(got, "# trailing comment") {
		t.Errorf("mouse: false not added with the file kept:\n%s", got)
	}
	if back := mustLoad(t, path); !reflect.DeepEqual(back, cfg) || back.MouseOn() {
		t.Fatalf("round trip:\n got %+v\nwant %+v", back, cfg)
	}

	cfg.Mouse = new(true)
	if _, err = SaveEdits(path, st, cfg); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); !strings.Contains(got, "\nmouse: true\n") || strings.Contains(got, "mouse: false") {
		t.Errorf("mouse on not written:\n%s", got)
	}
	if back := mustLoad(t, path); !reflect.DeepEqual(back, cfg) || !back.MouseOn() {
		t.Fatalf("second round trip:\n got %+v\nwant %+v", back, cfg)
	}

	// A nil mouse saves as on.
	path, cfg, st = stamped(t, "mouse: false # mine\ntopics:\n  - name: A\n")
	cfg.Mouse = nil
	if _, err = SaveEdits(path, st, cfg); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != "mouse: true # mine\ntopics:\n  - name: A\n" {
		t.Errorf("nil mouse saved as:\n%s", got)
	}
}
