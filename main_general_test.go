package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/RafaelXokito/daily-brief/internal/brief"
	"github.com/RafaelXokito/daily-brief/internal/config"
)

// budgetOutput is what `daily-brief budget` prints for cfg.
func budgetOutput(t *testing.T, cfg config.Config) string {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	auto := brief.ResolveBudget(&cfg)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	err = budgetCommand(cfg, auto)
	os.Stdout = stdout
	w.Close()
	out, _ := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// The budget table lists the World section when general is on, and a
// -topics subset names it among the topics it counts.
func TestBudgetCommandListsWorld(t *testing.T) {
	cfg := config.Default()
	if out := budgetOutput(t, cfg); strings.Contains(out, config.WorldTopicName) {
		t.Errorf("general off lists the World section:\n%s", out)
	}
	cfg.General = true
	out := budgetOutput(t, cfg)
	if !strings.Contains(out, "\n"+config.WorldTopicName+" ") {
		t.Errorf("budget table lacks the World section:\n%s", out)
	}

	sub := cfg
	if err := (overrides{topics: "World & hot topics,AI"}).apply(&sub); err != nil {
		t.Fatal(err)
	}
	if err := sub.Validate(); err != nil {
		t.Fatal(err)
	}
	out = budgetOutput(t, sub)
	if !strings.Contains(out, "recommended for 2 of 6 topics") || !strings.Contains(out, config.WorldTopicName) {
		t.Errorf("subset budget:\n%s", out)
	}
	if note := autoNote(sub, true); !strings.Contains(note, "over 2 of 6 topics") {
		t.Errorf("auto note %q", note)
	}
}
