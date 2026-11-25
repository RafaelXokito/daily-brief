package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/RafaelXokito/daily-brief/internal/brief"
	"github.com/RafaelXokito/daily-brief/internal/config"
	"github.com/RafaelXokito/daily-brief/internal/fetch"
	"github.com/RafaelXokito/daily-brief/internal/paper"
)

// cmdBackend researches every topic into one invented item, or fails each
// pass after spending tokens when researchErr is set. Its other replies
// are unusable, so the editor falls back mechanically.
type cmdBackend struct {
	mu          sync.Mutex
	topics      []string
	researchErr error
}

func (b *cmdBackend) Research(_ context.Context, t brief.Task) brief.Result {
	b.mu.Lock()
	b.topics = append(b.topics, t.Topic.Name)
	b.mu.Unlock()
	if b.researchErr != nil {
		return brief.Result{Err: b.researchErr, Tokens: 7_000}
	}
	md := fmt.Sprintf("### %s widget released\nThe widget team shipped a release. [source](https://example.com/%s)\n", t.Topic.Name, t.Topic.Name)
	return brief.Result{Markdown: md, Tokens: 7_000, Searches: t.Searches}
}

func (b *cmdBackend) Summarize(context.Context, string, string) brief.Result {
	return brief.Result{Markdown: "not json", Tokens: 100}
}

func (b *cmdBackend) Name() string { return "fake" }

type failingDeliverer struct{}

func (failingDeliverer) Deliver(context.Context, string, []byte) ([]string, error) {
	return nil, errors.New("dropbox is down")
}

// fakePaperDeps makes paperCommand talk to b and d, with no network.
func fakePaperDeps(t *testing.T, b brief.Backend, d paper.Deliverer) {
	t.Helper()
	old := paperDeps
	t.Cleanup(func() { paperDeps = old })
	paperDeps = func(_ context.Context, _ config.Config, f paperFlags) (paper.Deps, error) {
		deps := paper.Deps{Backend: b, Fetch: func(context.Context, []string) map[string]fetch.Article { return nil }}
		if !f.noDeliver {
			deps.Deliverer = d
		}
		return deps, nil
	}
}

func paperTestConfig() config.Config {
	cfg := config.Default()
	cfg.HistoryDays = 0
	cfg.Topics = []config.Topic{{Name: "Alpha"}, {Name: "Beta"}}
	cfg.Paper.TokenBudget = 2_000_000
	return cfg
}

func TestPaperCommandPrintsSummaryWhenDeliveryFails(t *testing.T) {
	isolate(t)
	fakePaperDeps(t, &cmdBackend{}, failingDeliverer{})
	var out, errb bytes.Buffer

	err := paperCommand(context.Background(), paperTestConfig(), paperFlags{}, &out, &errb)

	if got := exitCode(err); got != 4 {
		t.Fatalf("exit code = %d (%v), want 4", got, err)
	}
	line := out.String()
	if !strings.HasPrefix(line, "paper ") || !strings.Contains(line, "delivery failed, saved to ") {
		t.Errorf("stdout = %q, want the summary line naming the failed delivery", line)
	}
}

func TestPaperCommandPrintsSummaryWhenNoStoriesAfterResearch(t *testing.T) {
	isolate(t)
	fakePaperDeps(t, &cmdBackend{researchErr: errors.New("search failed")}, nil)
	var out, errb bytes.Buffer

	err := paperCommand(context.Background(), paperTestConfig(), paperFlags{noDeliver: true}, &out, &errb)

	if got := exitCode(err); got != 3 {
		t.Fatalf("exit code = %d (%v), want 3", got, err)
	}
	line := out.String()
	if !strings.HasPrefix(line, "paper ") || !strings.Contains(line, "0 stories") || !strings.Contains(line, "nothing built") {
		t.Errorf("stdout = %q, want the summary line of a run with no stories", line)
	}
	if strings.Contains(line, ": 0 of ") {
		t.Errorf("stdout = %q, want the research spend counted", line)
	}
}

// runMain runs the program's run() with args as its command line.
func runMain(t *testing.T, args ...string) error {
	t.Helper()
	oldArgs, oldFlags := os.Args, flag.CommandLine
	t.Cleanup(func() { os.Args, flag.CommandLine = oldArgs, oldFlags })
	os.Args = append([]string{"daily-brief"}, args...)
	flag.CommandLine = flag.NewFlagSet("daily-brief", flag.ContinueOnError)
	return run()
}

// -topics is ignored by the paper: it researches every configured topic.
func TestPaperIgnoresTopicsFlag(t *testing.T) {
	isolate(t)
	b := &cmdBackend{}
	fakePaperDeps(t, b, nil)
	cfg := paperTestConfig()
	cfg.General = false
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}

	if err := runMain(t, "-config", path, "-topics", "Alpha", "-no-deliver", "paper"); err != nil {
		t.Fatalf("paper: %v", err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if !slices.Contains(b.topics, "Alpha") || !slices.Contains(b.topics, "Beta") {
		t.Errorf("researched %v, want both Alpha and Beta", b.topics)
	}
}
