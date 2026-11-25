package brief

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A prompt larger than Linux's 128 KiB per-argument limit reaches the CLI
// whole on stdin, and no argument carries it.
func TestClaudeCodeSendsPromptOnStdin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(t.TempDir(), "claude")
	script := `#!/bin/sh
cat > "` + dir + `/stdin"
for a in "$@"; do printf '%s\n' "$a"; done > "` + dir + `/args"
echo '{"result":"ok","is_error":false,"stop_reason":"end_turn","modelUsage":{"m":{"inputTokens":10,"outputTokens":5}}}'
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig("claude-code", 0, "A")
	b := &claudeCodeBackend{cfg: cfg, path: path}
	prompt := "Invented article text. " + strings.Repeat("The widget team shipped a release. ", 6_000) + "THE-END"
	if len(prompt) <= 200*1024 {
		t.Fatalf("prompt is only %d bytes", len(prompt))
	}

	res := b.Compose(context.Background(), "system", prompt, 1_000)

	if res.Err != nil {
		t.Fatalf("compose: %v", res.Err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "stdin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != prompt {
		t.Errorf("stdin carried %d bytes, want the %d-byte prompt", len(got), len(prompt))
	}
	args, err := os.ReadFile(filepath.Join(dir, "args"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(args), "THE-END") {
		t.Error("the prompt is also passed as an argument")
	}
	for _, want := range []string{"-p\n", "--output-format\njson\n", "--system-prompt\nsystem\n", "--no-session-persistence\n"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("args lack %q:\n%s", want, args)
		}
	}
}
