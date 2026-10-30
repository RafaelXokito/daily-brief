package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGeneralDefaultsOff(t *testing.T) {
	cfg := Default()
	if cfg.General {
		t.Fatal("general is on by default")
	}
	if got := cfg.EffectiveTopics(); !reflect.DeepEqual(got, cfg.Topics) {
		t.Errorf("effective topics %v with general off", got)
	}
	data, err := marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(data); !strings.Contains(s, "# general: true adds a \"World & hot topics\" section") || !strings.Contains(s, "\ngeneral: false\n") {
		t.Errorf("saved config does not document general:\n%s", s)
	}
	if Hints["general"] == "" {
		t.Error("no hint for general")
	}
}

func TestEffectiveTopicsAddsWorld(t *testing.T) {
	cfg := Default()
	cfg.General = true
	cfg.Topics = []Topic{{Name: "Security"}, {Name: "AI", Weight: 2}}
	got := cfg.EffectiveTopics()
	if len(got) != 3 || len(cfg.Topics) != 2 {
		t.Fatalf("effective topics %v, configured %v", got, cfg.Topics)
	}
	w := got[2]
	want := "The most significant world events and the stories trending today across politics, economy, science and society. " +
		"Skip anything the reader's other topics already cover: Security, AI. Prefer primary or reputable sources; no rumours."
	if w.Name != WorldTopicName || w.Weight != 1 || w.Focus != want {
		t.Errorf("World topic %+v", w)
	}
	// Applying it again changes nothing.
	again := cfg
	again.Topics = got
	if n := len(again.EffectiveTopics()); n != 3 {
		t.Errorf("applied twice: %d topics", n)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("valid config refused: %v", err)
	}
}

// No configured topic may take the World section's name, in any case,
// whether general is on or off.
func TestWorldNameReserved(t *testing.T) {
	for _, general := range []bool{false, true} {
		for _, name := range []string{"World & hot topics", "world & HOT topics", " World & Hot Topics "} {
			cfg := Default()
			cfg.General = general
			cfg.Topics = append(cfg.Topics, Topic{Name: name})
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "reserved") {
				t.Errorf("general %v, topic %q: err %v", general, name, err)
			}
		}
	}
}

func TestFilterTopicsWorld(t *testing.T) {
	cfg := Default()
	cfg.General = true
	n := len(cfg.Topics)
	if err := cfg.FilterTopics([]string{"WORLD & hot topics", "ai"}); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Topics) != 2 || cfg.Topics[0].Name != "AI" || cfg.Topics[1].Name != WorldTopicName || len(cfg.AllTopics) != n+1 {
		t.Fatalf("kept %v of %v", cfg.Topics, cfg.AllTopics)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("filtered config refused: %v", err)
	}
	if got := cfg.EffectiveTopics(); len(got) != 2 {
		t.Errorf("filtered effective topics %v", got)
	}

	off := Default()
	if err := off.FilterTopics([]string{WorldTopicName}); err == nil {
		t.Error("the World section was chosen with general off")
	}
}

func TestSaveEditsGeneral(t *testing.T) {
	path, cfg, st := stamped(t, userConfig)
	cfg.General = true
	st2, err := SaveEdits(path, st, cfg)
	if err != nil {
		t.Fatal(err)
	}
	file := readFile(t, path)
	if !strings.Contains(file, "general: true") || !strings.Contains(file, "# My daily-brief config") || !strings.Contains(file, "my_note: keep me") {
		t.Errorf("saved file:\n%s", file)
	}
	back, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !back.General || !reflect.DeepEqual(back, cfg) {
		t.Errorf("loaded %+v, saved %+v", back, cfg)
	}
	cfg.General = false
	if _, err := SaveEdits(path, st2, cfg); err != nil {
		t.Fatal(err)
	}
	if back, _, _ := Load(path); back.General {
		t.Error("general not turned off")
	}

	// A fresh config file written by Load holds general: false, which a
	// round-trip keeps.
	fresh := filepath.Join(t.TempDir(), "config.yaml")
	if _, _, err := Load(fresh); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(fresh)
	if !strings.Contains(string(data), "general: false") {
		t.Errorf("default file lacks general:\n%s", data)
	}
}

// The reserved name is refused whatever ran first: FilterTopics may not
// hide a configured topic that takes it behind the World section.
func TestWorldNameReservedAfterFilter(t *testing.T) {
	for _, pick := range [][]string{{"AI"}, {WorldTopicName}, {"AI", "world & hot topics"}} {
		cfg := Default()
		cfg.General = true
		cfg.Topics = append(cfg.Topics, Topic{Name: "world & HOT topics"})
		if err := cfg.FilterTopics(pick); err != nil {
			t.Fatal(err)
		}
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("-topics %v: err %v", pick, err)
		}
	}
}

// The reading-cap error counts the World section and says so.
func TestReadingCapErrorNamesWorld(t *testing.T) {
	cfg := Default()
	cfg.Topics = nil
	for i := range 36 {
		cfg.Topics = append(cfg.Topics, Topic{Name: fmt.Sprintf("T%02d", i)})
	}
	cfg.MaxReadMinutes = 1
	if err := cfg.Validate(); err != nil {
		t.Fatalf("36 topics in 1 minute: %v", err)
	}
	cfg.General = true
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "across 37 (36 + World & hot topics) topics") {
		t.Errorf("err %v", err)
	}
}
