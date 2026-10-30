package brief

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/aws"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
	"github.com/anthropics/anthropic-sdk-go/v2/vertex"

	"github.com/RafaelXokito/daily-brief/internal/config"
	"github.com/RafaelXokito/daily-brief/internal/keychain"
)

// Backend is one way of reaching Claude.
type Backend interface {
	// Research runs one research pass: it searches the web for the task's
	// topic and writes (or, with Prior set, revises) its digest, spending at
	// most the task's budget.
	Research(ctx context.Context, task Task) Result
	// Summarize runs a single prompt without tools.
	Summarize(ctx context.Context, system, input string) Result
	// Name describes the connection for display.
	Name() string
}

// Task is one research pass over a topic, planned by the Generator.
type Task struct {
	Topic    config.Topic
	Budget   int64
	Searches int64
	Items    int
	// Prior is the digest a top-up pass deepens or widens; empty for the
	// first pass.
	Prior string
	// History is what earlier days' journals reported on the topic, which
	// the pass is told not to repeat without a material new development.
	History []Reported
	// Profile and Mute are the reader's profile and the subjects to leave
	// out, from the config (see readerBlocks).
	Profile string
	Mute    []string

	// meter, if set, ties the pass to its run's pool: the API backend
	// charges each response to it as it goes and stops when the run has no
	// room left. cal, if set, is the run's calibration of search requests,
	// shared by its passes.
	meter *claim
	cal   *calibration
}

type Result struct {
	Markdown  string
	Sources   []Source
	Tokens    int64
	Searches  int64
	Truncated bool
	Err       error
}

// NewBackend builds the backend selected by cfg.Provider.
func NewBackend(ctx context.Context, cfg config.Config) (Backend, error) {
	p := cfg.Provider
	if err := p.Validate(); err != nil {
		return nil, err
	}
	switch p.Type {
	case "claude-code":
		path := p.ClaudePath
		if path == "" {
			var err error
			if path, err = exec.LookPath("claude"); err != nil {
				return nil, fmt.Errorf("provider claude-code: the `claude` CLI was not found on PATH (install Claude Code or set provider.claude_path)")
			}
		}
		return &claudeCodeBackend{cfg: cfg, path: path}, nil

	case "anthropic":
		var opts []option.RequestOption
		name := "Claude API"
		switch p.Auth {
		case "", "keychain":
			key, err := keychain.Get()
			if err != nil {
				return nil, fmt.Errorf("provider anthropic: %w", err)
			}
			opts = append(opts, option.WithAPIKey(key))
			name += " (Keychain key)"
		case "profile":
			if p.Profile != "" {
				opts = append(opts, option.WithProfile(p.Profile))
			}
			name += " (ant profile)"
		case "env":
			name += " (ANTHROPIC_API_KEY)"
		}
		client := anthropic.NewClient(opts...)
		return &apiBackend{cfg: cfg, messages: client.Beta.Messages, name: name, dynamicSearch: true, fallbacks: true, taskBudget: true}, nil

	case "aws":
		client, err := aws.NewClient(ctx, aws.ClientConfig{AWSProfile: p.Profile, AWSRegion: p.Region, WorkspaceID: p.WorkspaceID})
		if err != nil {
			return nil, fmt.Errorf("provider aws: %w", err)
		}
		return &apiBackend{cfg: cfg, messages: client.Beta.Messages, name: "Claude Platform on AWS", dynamicSearch: true, fallbacks: true, taskBudget: true}, nil

	case "vertex":
		client := anthropic.NewClient(vertex.WithGoogleAuth(ctx, p.Region, p.ProjectID))
		// Vertex AI offers only the basic web search tool, and no server-side
		// fallbacks or task budgets.
		return &apiBackend{cfg: cfg, messages: client.Beta.Messages, name: "Vertex AI"}, nil
	}
	return nil, fmt.Errorf("unknown provider %q", p.Type)
}
