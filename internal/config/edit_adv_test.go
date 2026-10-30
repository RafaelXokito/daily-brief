package config

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// advTopics is a valid topics block for the unusual files below.
const advTopics = "topics:\n  - name: A\n    focus: a\n"

// advEdits are edits the Settings pane can make.
var advEdits = map[string]func(*Config){
	"history": func(c *Config) { c.HistoryDays = 7 },
	"theme":   func(c *Config) { c.Theme = "dark" },
	"mute":    func(c *Config) { c.Mute = append(c.Mute, "sports") },
	"profile": func(c *Config) { c.Profile = "Go, Postgres" },
	"model":   func(c *Config) { c.Model = "claude-sonnet-x" },
	"aws": func(c *Config) {
		c.Provider = Provider{Type: "aws", Region: "eu-west-1", Profile: "work"}
	},
	"all": func(c *Config) {
		c.HistoryDays, c.Theme, c.Effort, c.Concurrency = 9, "light", "max", 5
		c.Mute = []string{"x", "y"}
		c.Profile = "p"
		c.Provider = Provider{Type: "anthropic", Auth: "profile", Profile: "me"}
	},
}

// trySave writes body, loads it stamped, applies edit and saves. Either the
// save succeeds, the file loads as the edited config and keeps each of
// keep, or it fails and the file is byte-identical. It returns the error.
func trySave(t *testing.T, path, body string, edit func(*Config), keep []string) error {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, st, err := LoadStamped(path)
	if err != nil {
		// The pane would not let the user edit a file it can't read.
		return err
	}
	edit(&cfg)
	want := cfg.Clone()
	want.Clean()
	_, err = SaveEdits(path, st, cfg)
	got, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatalf("read back: %v", rerr)
	}
	if err != nil {
		if string(got) != body {
			t.Errorf("failed save (%v) changed the file:\n%q\nwas\n%q", err, got, body)
		}
		return err
	}
	loaded, _, lerr := Load(path)
	if lerr != nil {
		t.Errorf("saved file does not load: %v\n%s", lerr, got)
		return nil
	}
	if !reflect.DeepEqual(loaded, want) {
		t.Errorf("saved file loads as\n%+v\nwant\n%+v\nfile:\n%s", loaded, want, got)
	}
	for _, k := range keep {
		if !strings.Contains(string(got), k) {
			t.Errorf("saved file lost %q:\n%s", k, got)
		}
	}
	return nil
}

func deadline(t *testing.T, d time.Duration) func() {
	t.Helper()
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-time.After(d):
			panic(fmt.Sprintf("%s: exceeded %v", t.Name(), d))
		}
	}()
	return func() { close(done) }
}

// Unusual files: each save succeeds and round-trips, keeping the user's
// comments and unknown keys, or refuses and leaves the file alone.
func TestSaveEditsAdvUnusualFiles(t *testing.T) {
	defer deadline(t, 30*time.Second)()
	bigMute := "mute:\n"
	for i := range MaxMuteEntries - 1 {
		bigMute += fmt.Sprintf("  - %s%d\n", strings.Repeat("m", MaxMuteChars-3), i)
	}
	cases := []struct {
		name, body string
		keep       []string
		mustSave   bool // a refusal is a bug
	}{
		{"empty", "", nil, false},
		{"only comments", "# just a comment\n# another\n", nil, false},
		{"only comments + topics", "# head\n" + advTopics + "# tail\n", []string{"# head", "# tail"}, true},
		{"crlf", "# c\r\nprovider: claude-code # sub\r\nmy_note: keep\r\n" + strings.ReplaceAll(advTopics, "\n", "\r\n"),
			[]string{"# c", "# sub", "my_note: keep"}, true},
		{"bom", "\ufeff# bom\nmy_note: keep\n" + advTopics, []string{"# bom", "my_note: keep"}, true},
		{"tab before comment", "theme: dark\t# tabbed\nmy_note:\tkeep\n" + advTopics, []string{"# tabbed", "my_note:"}, true},
		{"tabs in values and odd indent", "my_note: \"a\\tb\"   # tab\ntopics:\n   -   name: A\n       focus: a\n       extra: 1\n",
			[]string{"# tab", "extra: 1"}, true},
		{"anchors and aliases", "base: &b\n  name: A\n  focus: a\ntopics:\n  - *b\n  - name: B\nmy_mute: &m [one]\nmute: *m\n",
			[]string{"base: &b"}, false},
		{"multi-document", "# d1\n" + advTopics + "---\n# d2\nother: doc\n", []string{"# d1", "other: doc", "# d2"}, false},
		{"flow map and list", "{provider: {type: claude-code}, my_note: keep, mute: [a, b], topics: [{name: A, focus: a}]}\n",
			[]string{"my_note: keep"}, true},
		{"duplicate keys", "theme: dark\ntheme: light\n" + advTopics, nil, false},
		{"quoted keys", "\"theme\": dark\n'history_days': 2\n\"my note\": keep\n" + advTopics, []string{"my note"}, true},
		{"provider short", "provider: claude-code # short\n" + advTopics, []string{"# short"}, true},
		{"provider mapping", "provider:\n  type: claude-code\n  extra: keep # x\n" + advTopics, []string{"extra: keep", "# x"}, true},
		{"topics unknown fields", "topics:\n  - name: A\n    focus: a\n    colour: red # c\n    tags: [x, y]\n    nested: {k: v}\n",
			[]string{"colour: red", "# c", "tags: [x, y]", "nested: {k: v}"}, true},
		{"big mute and profile", bigMute + "profile: \"" + strings.Repeat("p", MaxProfileChars) + "\"\n" + advTopics, nil, true},
		{"unicode", "# ☃ café 日本語\nmy_note: \"🦀 Ünïcödé\"\nprofile: \"Ação, 東京, 👨\u200d💻\"\nmute: [\"ñ\", \"日本\"]\n" + advTopics,
			[]string{"# ☃ café 日本語", "🦀 Ünïcödé"}, true},
		{"unicode plain", "my_note: 🦀 plain crab\ncity: Zürich 🏔\n" + advTopics, []string{"my_note: 🦀 plain crab"}, true},
	}
	for _, c := range cases {
		for name, edit := range advEdits {
			t.Run(c.name+"/"+name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "config.yaml")
				err := trySave(t, path, c.body, edit, c.keep)
				if err != nil && c.mustSave {
					t.Errorf("save refused: %v", err)
				} else if err != nil {
					t.Logf("refused (allowed): %v", err)
				}
			})
		}
	}
}

// Edits to a file holding a value only via an alias must not silently
// write through to the anchor others share, or drop the alias' meaning.
func TestSaveEditsAdvAliasRoundTrip(t *testing.T) {
	defer deadline(t, 30*time.Second)()
	body := "shared: &s 3\nhistory_days: *s\nconcurrency: *s\n" + advTopics
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := trySave(t, path, body, func(c *Config) { c.HistoryDays = 5 }, []string{"shared:"}); err != nil {
		t.Logf("refused: %v", err)
	}
}

// A symlinked config is saved through the link, into the other directory:
// the link stays a link and its target holds the edit.
func TestSaveEditsAdvSymlink(t *testing.T) {
	defer deadline(t, 30*time.Second)()
	realDir, linkDir := t.TempDir(), t.TempDir()
	target := filepath.Join(realDir, "real.yaml")
	body := "# mine\nmy_note: keep\n" + advTopics
	if err := os.WriteFile(target, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(linkDir, "config.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Skip(err)
	}
	cfg, st, err := LoadStamped(link)
	if err != nil {
		t.Fatal(err)
	}
	cfg.HistoryDays = 6
	if _, err := SaveEdits(link, st, cfg); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the link was replaced: %v", err)
	}
	got := readFile(t, target)
	if !strings.Contains(got, "history_days: 6") || !strings.Contains(got, "# mine") {
		t.Errorf("target:\n%s", got)
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(linkDir)
	if len(entries) != 1 {
		t.Errorf("stray files beside the link: %v", entries)
	}
	if c := mustLoad(t, link); c.HistoryDays != 6 {
		t.Errorf("loads history_days %d", c.HistoryDays)
	}
}

// A symlink chain (link -> link -> file) and a dangling link.
func TestSaveEditsAdvSymlinkChainAndDangling(t *testing.T) {
	defer deadline(t, 30*time.Second)()
	d := t.TempDir()
	target := filepath.Join(d, "real.yaml")
	os.WriteFile(target, []byte(advTopics), 0o600)
	l1, l2 := filepath.Join(d, "l1"), filepath.Join(d, "l2")
	if os.Symlink(target, l1) != nil || os.Symlink(l1, l2) != nil {
		t.Skip("no symlinks")
	}
	cfg, st, err := LoadStamped(l2)
	if err != nil {
		t.Fatal(err)
	}
	cfg.HistoryDays = 4
	if _, err := SaveEdits(l2, st, cfg); err != nil {
		t.Fatal(err)
	}
	if mustLoad(t, target).HistoryDays != 4 {
		t.Error("chain not followed")
	}
	// Dangling: the target vanished after load.
	os.Remove(target)
	if _, err := SaveEdits(l2, st, cfg); err == nil {
		t.Error("saved through a dangling link")
	}
	if _, err := os.Lstat(target); err == nil {
		t.Error("a dangling link's target was created")
	}
}

// In a read-only directory the save fails cleanly: an error, the file
// byte-identical, no temporary files.
func TestSaveEditsAdvReadOnlyDir(t *testing.T) {
	defer deadline(t, 30*time.Second)()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permissions not enforced")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := "# ro\n" + advTopics
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, st, err := LoadStamped(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	cfg.HistoryDays = 8
	if _, err := SaveEdits(path, st, cfg); err == nil {
		t.Fatal("saved into a read-only directory")
	}
	if got := readFile(t, path); got != body {
		t.Errorf("file changed:\n%s", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("stray files: %v", entries)
	}
	// A read-only file in a writable dir: the rename would replace it. It
	// must at least keep its mode and not silently become writable.
	os.Chmod(dir, 0o700)
	os.Chmod(path, 0o400)
	cfg2, st2, _ := LoadStamped(path)
	cfg2.HistoryDays = 8
	if _, err := SaveEdits(path, st2, cfg2); err == nil {
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o400 {
			t.Errorf("read-only file became %v", fi.Mode().Perm())
		}
	}
}

// Random sequences of valid edits: each save loads as the edited config,
// and a second save without changes leaves the file byte-identical.
func TestSaveEditsAdvPropertyRoundTrip(t *testing.T) {
	defer deadline(t, 30*time.Second)()
	rng := rand.New(rand.NewSource(1))
	strs := []string{"", "x", "a b", "café", "日本", "yes", "no", "null", "~", "123", "1e3", "0x1F", "true", "#hash", "a: b",
		"- dash", "'q'", "\"dq\"", "tail ", " lead", "[x]", "{y}", "&anchor", "*alias", "!tag", "%pct", "@at", "`bt`", "multi word value", "€"}
	pick := func() string { return strs[rng.Intn(len(strs))] }
	nonEmpty := func() string {
		for {
			if s := strings.TrimSpace(pick()); s != "" {
				return s
			}
		}
	}
	type edit func(*Config)
	edits := []edit{
		func(c *Config) {
			c.Provider.Type = []string{"claude-code", "anthropic", "aws", "vertex"}[rng.Intn(4)]
			if c.Provider.Type == "vertex" {
				c.Provider.ProjectID, c.Provider.Region = nonEmpty(), nonEmpty()
			}
		},
		func(c *Config) { c.Provider.Auth = []string{"", "keychain", "profile", "env"}[rng.Intn(4)] },
		func(c *Config) { c.Provider.Profile = strings.TrimSpace(pick()) },
		func(c *Config) { c.Provider.Region = strings.TrimSpace(pick()) },
		func(c *Config) { c.Provider.WorkspaceID = strings.TrimSpace(pick()) },
		func(c *Config) { c.Provider.ClaudePath = strings.TrimSpace(pick()) },
		func(c *Config) { c.Model = strings.TrimSpace(pick()) },
		func(c *Config) { c.Effort = []string{"low", "medium", "high", "xhigh", "max"}[rng.Intn(5)] },
		func(c *Config) { c.Theme = []string{"auto", "dark", "light"}[rng.Intn(3)] },
		func(c *Config) { c.Mouse = new(rng.Intn(2) == 0) },
		func(c *Config) { c.UpdateCheck = new(rng.Intn(2) == 0) },
		func(c *Config) { c.TokenBudget = []int64{0, 10_000, 400_000, 1 << 40}[rng.Intn(4)] },
		func(c *Config) { c.MaxSearchesPerTopic = int64(rng.Intn(20)) },
		func(c *Config) { c.MaxReadMinutes = 5 + rng.Intn(30) },
		func(c *Config) { c.HistoryDays = rng.Intn(15) },
		func(c *Config) { c.Concurrency = 1 + rng.Intn(8) },
		func(c *Config) { c.Country = strings.TrimSpace(pick()) },
		func(c *Config) { c.City = strings.TrimSpace(pick()) },
		func(c *Config) { c.Profile = strings.TrimSpace(pick() + " " + pick()) },
		func(c *Config) { c.PriceInputPerMTok = float64(rng.Intn(1000)) / 8 },
		func(c *Config) { c.PriceOutputPerMTok = []float64{0, 0.1, 1e-7, 15, 3.333}[rng.Intn(5)] },
		func(c *Config) { c.PricePer1KSearches = float64(rng.Intn(50)) },
		func(c *Config) {
			n := rng.Intn(5)
			c.Mute = nil
			for range n {
				c.Mute = append(c.Mute, nonEmpty())
			}
		},
		func(c *Config) {
			if len(c.Mute) > 0 {
				i := rng.Intn(len(c.Mute))
				c.Mute = append(c.Mute[:i:i], c.Mute[i+1:]...)
			}
		},
		func(c *Config) { c.Mute = append(c.Mute, nonEmpty()) },
	}
	bases := []string{userConfig, advTopics, "provider:\n  type: aws\n  region: x\n" + advTopics,
		"{topics: [{name: A, focus: a}], mute: [], my: 1}\n"}
	failures := 0
	for iter := 0; iter < 150 && failures < 5; iter++ {
		base := bases[iter%len(bases)]
		path := filepath.Join(t.TempDir(), "config.yaml")
		os.WriteFile(path, []byte(base), 0o600)
		cfg, st, err := LoadStamped(path)
		if err != nil {
			t.Fatal(err)
		}
		var log []int
		for step := 0; step < 1+rng.Intn(6); step++ {
			for k := 0; k < 1+rng.Intn(4); k++ {
				i := rng.Intn(len(edits))
				log = append(log, i)
				edits[i](&cfg)
			}
			if cfg.Validate() != nil {
				continue
			}
			before := readFile(t, path)
			nst, err := SaveEdits(path, st, cfg)
			if err != nil {
				failures++
				if got := readFile(t, path); got != before {
					t.Errorf("iter %d: failed save changed the file", iter)
				}
				t.Errorf("iter %d base %d edits %v: save refused: %v\ncfg %+v\nfile:\n%s", iter, iter%len(bases), log, err, cfg, before)
				break
			}
			st = nst
			want := cfg.Clone()
			want.Clean()
			got, _, err := Load(path)
			if err != nil || !reflect.DeepEqual(got, want) {
				failures++
				t.Errorf("iter %d edits %v: loads as %+v (%v), want %+v\n%s", iter, log, got, err, want, readFile(t, path))
				break
			}
			// A second save with no changes is byte-identical.
			once := readFile(t, path)
			again, err := SaveEdits(path, st, cfg)
			if err != nil || readFile(t, path) != once {
				failures++
				t.Errorf("iter %d: second save changed the file (%v)", iter, err)
				break
			}
			st = again
			// Reload from disk as the pane would and keep editing.
			cfg, st, _ = LoadStamped(path)
		}
	}
}

// A file whose stamp matches but whose save produces a file that reads
// differently is refused: data that would decode differently must never
// reach disk. Here a mute entry that YAML would read as a non-string.
func TestSaveEditsAdvTrickyScalars(t *testing.T) {
	defer deadline(t, 30*time.Second)()
	for _, v := range []string{"yes", "no", "on", "off", "null", "~", "1", "0x10", "1e9", ".inf", "-", "?", ":", "#", "- a", "a #b", "'", "\"", "\\", "*a", "&a", "!x", "|", ">", "%", "@", "`"} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := trySave(t, path, "mute: [x]\nmodel: m\n"+advTopics, func(c *Config) {
			c.Mute = []string{v}
			c.Model = v
			c.City = v
		}, nil); err != nil {
			t.Errorf("%q: refused: %v", v, err)
		}
	}
}

// ErrChanged: a file touched (same bytes, new mtime) or replaced is not
// overwritten.
func TestSaveEditsAdvStaleStamp(t *testing.T) {
	defer deadline(t, 30*time.Second)()
	path, cfg, st := stamped(t, userConfig)
	later := time.Now().Add(time.Hour)
	os.Chtimes(path, later, later)
	cfg.HistoryDays = 9
	if _, err := SaveEdits(path, st, cfg); !errors.Is(err, ErrChanged) {
		t.Errorf("touched file: %v", err)
	}
	if !bytes.Equal([]byte(readFile(t, path)), []byte(userConfig)) {
		t.Error("touched file overwritten")
	}
}
