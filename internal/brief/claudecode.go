package brief

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/RafaelXokito/daily-brief/internal/config"
)

// claudeCodeBackend runs the local Claude Code CLI in print mode, so requests
// use whatever account Claude Code is logged into (including Team and
// Enterprise subscriptions) with no API key.
//
// Claude Code runs the whole search loop itself, so the token budget cannot
// be enforced mid-pass: it is passed to Claude as a search limit, and the
// Generator holds a safety margin beside each pass. The CLI (checked on
// claude 2.1.312) has no turn or token limit flag: there is no --max-turns,
// only --max-budget-usd, which caps dollars, not tokens.
type claudeCodeBackend struct {
	cfg  config.Config
	path string
}

func (b *claudeCodeBackend) Name() string { return "Claude Code (your Claude login)" }

// cliResult is the subset of `claude -p --output-format json` we use.
type cliResult struct {
	Result     string `json:"result"`
	IsError    bool   `json:"is_error"`
	Subtype    string `json:"subtype"`
	StopReason string `json:"stop_reason"`
	ModelUsage map[string]struct {
		InputTokens              int64 `json:"inputTokens"`
		OutputTokens             int64 `json:"outputTokens"`
		CacheReadInputTokens     int64 `json:"cacheReadInputTokens"`
		CacheCreationInputTokens int64 `json:"cacheCreationInputTokens"`
		WebSearchRequests        int64 `json:"webSearchRequests"`
	} `json:"modelUsage"`
}

func (b *claudeCodeBackend) Research(ctx context.Context, task Task) Result {
	prompt, tools := taskPrompt(task), ""
	if task.Searches > 0 {
		prompt += fmt.Sprintf("\n\nUse at most %d web searches.", task.Searches)
		tools = "WebSearch"
	}
	return b.run(ctx, researchSystemPrompt, prompt, tools, task.Budget)
}

func (b *claudeCodeBackend) Summarize(ctx context.Context, system, input string) Result {
	return b.run(ctx, system, input, "", int64(len(system)+len(input))/3+4_000)
}

// run runs one prompt; budget is what the pass planned to spend, counted as
// spent when the CLI's usage cannot be read.
func (b *claudeCodeBackend) run(ctx context.Context, system, prompt, tools string, budget int64) Result {
	// Run from an empty directory with no setting sources so no project
	// CLAUDE.md, hooks or MCP servers leak into the brief.
	dir, err := os.MkdirTemp("", "daily-brief-")
	if err != nil {
		return Result{Err: err}
	}
	defer os.RemoveAll(dir)

	args := []string{
		"-p", prompt,
		"--output-format", "json",
		"--model", b.cfg.Model,
		"--effort", b.cfg.Effort,
		"--system-prompt", system,
		"--tools", tools,
		"--setting-sources", "",
		"--strict-mcp-config",
		"--no-session-persistence",
	}
	if tools != "" {
		args = append(args, "--allowedTools", tools)
	}
	cmd := exec.CommandContext(ctx, b.path, args...)
	cmd.Dir = dir
	// On cancellation, stop waiting for output a child process may still
	// hold open, so the run cannot hang.
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()

	var out cliResult
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		if ctx.Err() != nil {
			return Result{Err: errors.New("cancelled"), Tokens: budget}
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" && runErr != nil {
			msg = runErr.Error()
		}
		return Result{Err: fmt.Errorf("claude CLI failed: %s", msg), Tokens: budget}
	}

	var res Result
	for _, u := range out.ModelUsage {
		res.Tokens += u.InputTokens + u.OutputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
		res.Searches += u.WebSearchRequests
	}
	switch {
	case out.IsError:
		res.Err = fmt.Errorf("claude CLI: %s", firstNonEmpty(out.Result, out.Subtype))
	case out.StopReason == "refusal":
		res.Err = errors.New("the model declined to cover this topic")
	default:
		res.Markdown = strings.TrimSpace(out.Result)
		res.Truncated = out.StopReason == "max_tokens"
	}
	return res
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
