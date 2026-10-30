package config

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Stamp identifies the contents of a config file as it was read, so that
// SaveEdits can tell whether something else has written it since.
type Stamp struct {
	ModTime time.Time
	Size    int64
	Sum     [sha256.Size]byte
}

// ErrChanged is SaveEdits refusing to overwrite a file that changed since
// it was loaded.
var ErrChanged = errors.New("config: the file changed on disk since it was loaded")

// Hints describe each key the TUI's settings edit in a line, worded as
// Save's header.
var Hints = map[string]string{
	"provider.type":          "claude-code (your Claude login, no key), anthropic, aws or vertex",
	"provider.auth":          "keychain (`daily-brief auth set-key`), profile (`ant auth login`) or env (ANTHROPIC_API_KEY)",
	"provider.profile":       "the `ant` profile (anthropic, auth profile) or AWS profile (aws) to use",
	"provider.region":        "the cloud region (aws, vertex)",
	"provider.workspace_id":  "the Claude Platform on AWS workspace",
	"provider.project_id":    "the Google Cloud project (vertex)",
	"provider.claude_path":   "the claude binary to use; empty = `claude` on PATH",
	"model":                  "the Claude model research passes use",
	"effort":                 "low, medium, high, xhigh or max",
	"theme":                  "auto (GLAMOUR_STYLE if set, else the terminal's background), dark or light",
	"token_budget":           "tokens (input + output + search results) each journal aims to spend; 0 = auto",
	"max_searches_per_topic": "optional cap per research pass; 0 = derived from the budget",
	"max_read_minutes":       "the journal is kept short enough to read in this many minutes",
	"history_days":           "earlier days' journals (0-14) whose items are not repeated; 0 = off",
	"concurrency":            "how many topics are researched in parallel",
	"country":                "optional, biases search results, e.g. PT",
	"city":                   "optional, biases search results, e.g. Lisbon",
	"price_input_per_mtok":   "USD per million input tokens, only to estimate the cost of API runs",
	"price_output_per_mtok":  "USD per million output tokens, only to estimate the cost of API runs",
	"price_per_1k_searches":  "USD per 1000 web searches, only to estimate the cost of API runs",
	"profile":                "optional, about you (at most 500 characters): items that affect it rank higher",
	"mute":                   "optional subjects or keywords to leave out (at most 30, 80 characters each)",
	"topics":                 "what to research; edit them in the file",
}

// LoadStamped reads the config at path as Load does, without creating it
// or validating it, and stamps what it read. The error is about reading or
// parsing; the caller validates.
func LoadStamped(path string) (Config, Stamp, error) {
	data, info, err := readStat(path)
	if err != nil {
		return Config{}, Stamp{}, err
	}
	cfg, err := parse(path, data)
	return cfg, stamp(data, info), err
}

// parse decodes a config file's contents over the defaults and cleans it.
func parse(path string, data []byte) (Config, error) {
	cfg := Default()
	cfg.Topics = nil
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.Clean()
	return cfg, nil
}

func readStat(path string) ([]byte, os.FileInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	var b bytes.Buffer
	if _, err := b.ReadFrom(f); err != nil {
		return nil, nil, err
	}
	return b.Bytes(), info, nil
}

func stamp(data []byte, info os.FileInfo) Stamp {
	return Stamp{ModTime: info.ModTime(), Size: info.Size(), Sum: sha256.Sum256(data)}
}

// SaveEdits writes cfg to the config at path, which was loaded as loaded,
// keeping the file's comments, key order and keys it doesn't know: only
// the values that changed are rewritten, and keys it lacks are added at
// the end unless they hold the default. It refuses with ErrChanged if the
// file changed since it was loaded, and writes nothing if cfg is invalid.
// The file is replaced atomically, keeping its mode; a symlink is followed.
// It returns the new file's stamp.
func SaveEdits(path string, loaded Stamp, cfg Config) (Stamp, error) {
	cfg.Clean()
	if err := cfg.Validate(); err != nil {
		return Stamp{}, err
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return Stamp{}, err
	}
	old, info, err := readStat(real)
	if err != nil {
		return Stamp{}, err
	}
	if info.Mode().Perm()&0o200 == 0 {
		return Stamp{}, fmt.Errorf("config: %s is read-only", real)
	}
	if now := stamp(old, info); now.Sum != loaded.Sum || !now.ModTime.Equal(loaded.ModTime) || now.Size != loaded.Size {
		return Stamp{}, ErrChanged
	}
	if was, err := parse(path, old); err == nil && reflect.DeepEqual(was, withoutAllTopics(cfg)) {
		// Nothing to change: the file stays exactly as it is.
		return stamp(old, info), nil
	}
	data, err := applyEdits(old, cfg)
	if err != nil {
		return Stamp{}, err
	}
	// The file must read back as cfg; anything else is a bug here, and
	// the user's file is left alone.
	if back, err := parse(path, data); err != nil || !reflect.DeepEqual(back, withoutAllTopics(cfg)) {
		return Stamp{}, errors.New("config: could not write the changes without altering other settings; edit the file instead")
	}
	if err := writeAtomic(real, data, info.Mode().Perm()); err != nil {
		return Stamp{}, err
	}
	data, info, err = readStat(real)
	if err != nil {
		return Stamp{}, err
	}
	return stamp(data, info), nil
}

// Clone is a copy of c that shares no slices with it.
func (c Config) Clone() Config {
	c.Mute = slices.Clone(c.Mute)
	c.Topics = slices.Clone(c.Topics)
	c.AllTopics = slices.Clone(c.AllTopics)
	return c
}

func withoutAllTopics(cfg Config) Config {
	cfg.AllTopics = nil
	return cfg
}

// writeAtomic replaces path with data: a temporary file in the same
// directory, synced, then renamed over it.
func writeAtomic(path string, data []byte, mode os.FileMode) (err error) {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(f.Name())
		}
	}()
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	// The rename itself is durable once the directory is synced; not every
	// system allows that, and the file is already in place.
	if d, derr := os.Open(dir); derr == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

// applyEdits is the file data with cfg's values: its YAML tree edited in
// place where they differ. A file that is not a mapping is written afresh
// as Save writes it. A file of several documents is refused, as the
// encoder would write only the first.
func applyEdits(data []byte, cfg Config) ([]byte, error) {
	if n, err := documents(data); err != nil {
		return nil, err
	} else if n > 1 {
		return nil, errors.New("config: the file holds more than one YAML document; edit it in $EDITOR instead")
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	var want yaml.Node
	if err := want.Encode(cfg); err != nil {
		return nil, err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return marshal(cfg)
	}
	def := Default()
	def.Topics = nil
	var defNode yaml.Node
	if err := defNode.Encode(def); err != nil {
		return nil, err
	}
	if err := mergeMapping(doc.Content[0], &want, &defNode, reflect.TypeOf(cfg), false); err != nil {
		return nil, err
	}
	restore, err := protectAstral(&doc, data)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(indentOf(data))
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	out := restore.Replace(b.String())
	// Keep the file's line endings and byte order mark.
	if bytes.Contains(data, []byte("\r\n")) {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	if bytes.HasPrefix(data, []byte("\ufeff")) {
		out = "\ufeff" + out
	}
	return []byte(out), nil
}

// documents counts the YAML documents in data.
func documents(data []byte) (int, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	n := 0
	for {
		var d yaml.Node
		if err := dec.Decode(&d); errors.Is(err, io.EOF) {
			return n, nil
		} else if err != nil {
			return n, err
		}
		n++
	}
}

// protectAstral swaps each character above U+FFFF in doc's text (which
// yaml.v3's encoder would write as a \U escape) for a private-use
// character found nowhere in data or doc, which it writes as it is. The
// replacer it returns swaps them back in the encoded text: as the stand-ins
// occur nowhere else, only they are replaced.
func protectAstral(doc *yaml.Node, data []byte) (*strings.Replacer, error) {
	var texts []*string
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		texts = append(texts, &n.Value, &n.HeadComment, &n.LineComment, &n.FootComment)
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(doc)
	used := map[rune]bool{}
	astral := map[rune]rune{}
	for _, t := range append([]*string{new(string(data))}, texts...) {
		for _, r := range *t {
			used[r] = true
		}
	}
	next := rune(0xE000)
	var pairs []string
	for _, t := range texts {
		for _, r := range *t {
			if r <= 0xFFFF || astral[r] != 0 {
				continue
			}
			for used[next] {
				next++
			}
			if next > 0xF8FF {
				return nil, errors.New("config: too many distinct characters above U+FFFF to save")
			}
			astral[r], used[next] = next, true
			pairs = append(pairs, string(next), string(r))
		}
	}
	if len(astral) > 0 {
		for _, t := range texts {
			*t = strings.Map(func(r rune) rune {
				if s, ok := astral[r]; ok {
					return s
				}
				return r
			}, *t)
		}
	}
	return strings.NewReplacer(pairs...), nil
}

// indentOf guesses the file's indentation: its smallest indent of a line
// that is not blank or a comment, 4 (yaml.Marshal's) if there is none.
func indentOf(data []byte) int {
	n := 0
	for _, l := range strings.Split(string(data), "\n") {
		t := strings.TrimLeft(l, " ")
		if d := len(l) - len(t); d > 0 && t != "" && !strings.HasPrefix(t, "#") && (n == 0 || d < n) {
			n = d
		}
	}
	if n < 2 || n > 8 {
		return 4
	}
	return n
}

// errAnchored refuses an edit that would also change what an alias
// elsewhere in the file refers to.
var errAnchored = errors.New("config: the change would alter a value the file shares through a YAML anchor; edit it in $EDITOR instead")

func hasComments(n *yaml.Node) bool {
	return n.HeadComment != "" || n.LineComment != "" || n.FootComment != ""
}

// mergeMapping edits old, a mapping of type t, to hold new's values: keys
// in both are merged, keys only in new are added unless def (the default
// mapping, or nil) holds the same, and keys t knows but new lacks (an
// omitted empty value) are removed, or set empty if they carry comments,
// which then stay. Keys t doesn't know are kept. anchored says that old
// is, or is inside, a node with an anchor, which must not change.
func mergeMapping(old, new, def *yaml.Node, t reflect.Type, anchored bool) error {
	anchored = anchored || old.Anchor != ""
	known := yamlKeys(t)
	for i := 0; i+1 < len(new.Content); i += 2 {
		k, v := new.Content[i], new.Content[i+1]
		ft := known[k.Value]
		if j := mapIndex(old, k.Value); j >= 0 {
			key, val := old.Content[j], old.Content[j+1]
			if err := mergeValue(val, v, ft, anchored); err != nil {
				return err
			}
			if val.Kind != yaml.ScalarNode && val.Style&yaml.FlowStyle == 0 && key.LineComment == "" {
				// A scalar's comment that became a block's goes beside its
				// key, where it was; under the block it would land on the
				// next key.
				key.LineComment, val.LineComment = val.LineComment, ""
			}
			continue
		}
		if d := mapValue(def, k.Value); d != nil && equalAs(d, v, ft) {
			continue
		}
		if anchored {
			return errAnchored
		}
		old.Content = append(old.Content, k, v)
	}
	for i := 0; i+1 < len(old.Content); {
		k, v := old.Content[i], old.Content[i+1]
		ft := known[k.Value]
		if ft == nil || mapIndex(new, k.Value) >= 0 {
			i += 2
			continue
		}
		var zero yaml.Node
		if err := zero.Encode(reflect.Zero(ft).Interface()); err != nil {
			return err
		}
		if equalAs(v, &zero, ft) {
			i += 2
			continue
		}
		if anchored || v.Anchor != "" {
			return errAnchored
		}
		if hasComments(k) || hasComments(v) {
			head, line, foot := v.HeadComment, v.LineComment, v.FootComment
			*v = zero
			v.HeadComment, v.LineComment, v.FootComment = head, line, foot
			i += 2
			continue
		}
		old.Content = append(old.Content[:i], old.Content[i+2:]...)
	}
	return nil
}

// mergeValue edits old to hold new's value, of type t, keeping old's
// comments and, for strings, its quoting. Sequence items are matched by
// value, so items that stay keep their comments.
func mergeValue(old, new *yaml.Node, t reflect.Type, anchored bool) error {
	if t != nil && equalAs(old, new, t) {
		return nil
	}
	if old.Kind == yaml.AliasNode {
		// The alias is replaced by the value; the anchor stays as it was.
		head, line, foot := old.HeadComment, old.LineComment, old.FootComment
		*old = *new
		old.HeadComment, old.LineComment, old.FootComment = head, line, foot
		return nil
	}
	anchored = anchored || old.Anchor != ""
	switch {
	case old.Kind == yaml.MappingNode && new.Kind == yaml.MappingNode:
		return mergeMapping(old, new, nil, t, anchored)
	case old.Kind == yaml.SequenceNode && new.Kind == yaml.SequenceNode:
		if anchored {
			return errAnchored
		}
		var et reflect.Type
		if t != nil && t.Kind() == reflect.Slice {
			et = t.Elem()
		}
		return mergeSequence(old, new, et)
	case old.Kind == yaml.ScalarNode && new.Kind == yaml.MappingNode && len(new.Content) == 2 && new.Content[0].Value == "type":
		// `provider: claude-code` stays in that short form.
		new = new.Content[1]
	}
	if anchored {
		return errAnchored
	}
	if old.Kind == yaml.ScalarNode && new.Kind == yaml.ScalarNode {
		quoted := old.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0
		old.Value, old.Tag = new.Value, new.Tag
		if !quoted || new.Tag != "!!str" {
			old.Style = new.Style
		}
		return nil
	}
	head, line, foot := old.HeadComment, old.LineComment, old.FootComment
	*old = *new
	old.HeadComment, old.LineComment, old.FootComment = head, line, foot
	return nil
}

// mergeSequence edits old's items, of type et, to new's: an item equal to
// one of old's keeps that node and its comments; the rest are paired in
// order with old's unmatched items of the same kind and merged, or added.
// Old items left over are dropped.
func mergeSequence(old, new *yaml.Node, et reflect.Type) error {
	used := make([]bool, len(old.Content))
	out := make([]*yaml.Node, len(new.Content))
	for i, n := range new.Content {
		for j, o := range old.Content {
			if !used[j] && equalAs(o, n, et) {
				used[j], out[i] = true, o
				break
			}
		}
	}
	j := 0
	for i, n := range new.Content {
		if out[i] != nil {
			continue
		}
		for j < len(old.Content) && used[j] {
			j++
		}
		if j < len(old.Content) && old.Content[j].Kind == n.Kind {
			if err := mergeValue(old.Content[j], n, et, false); err != nil {
				return err
			}
			used[j], out[i] = true, old.Content[j]
			j++
			continue
		}
		out[i] = n
	}
	old.Content = out
	return nil
}

// equalAs reports whether a and b decode to the same value of type t (any
// value if t is nil).
func equalAs(a, b *yaml.Node, t reflect.Type) bool {
	if t == nil {
		t = reflect.TypeOf((*any)(nil)).Elem()
	}
	va, vb := reflect.New(t), reflect.New(t)
	if a.Decode(va.Interface()) != nil || b.Decode(vb.Interface()) != nil {
		return false
	}
	return reflect.DeepEqual(va.Elem().Interface(), vb.Elem().Interface())
}

// yamlKeys maps each YAML key of struct type t to its field's type.
func yamlKeys(t reflect.Type) map[string]reflect.Type {
	keys := map[string]reflect.Type{}
	if t == nil || t.Kind() != reflect.Struct {
		return keys
	}
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name != "" && name != "-" {
			keys[name] = f.Type
		}
	}
	return keys
}

func mapIndex(m *yaml.Node, key string) int {
	if m == nil || m.Kind != yaml.MappingNode {
		return -1
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return i
		}
	}
	return -1
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
	if i := mapIndex(m, key); i >= 0 {
		return m.Content[i+1]
	}
	return nil
}
