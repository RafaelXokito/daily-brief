package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RafaelXokito/daily-brief/internal/config"
	"github.com/RafaelXokito/daily-brief/internal/dropbox"
	"github.com/RafaelXokito/daily-brief/internal/paper"
)

func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("CREDENTIALS_DIRECTORY", "")
}

func TestUsageListsPaper(t *testing.T) {
	for _, line := range []string{
		"  daily-brief paper            research, write and deliver today's Kobo paper (see -no-deliver)",
		"  daily-brief dropbox login    connect Dropbox for paper delivery",
		"  daily-brief dropbox status   check the Dropbox connection (read-only)",
	} {
		if !strings.Contains(usage, line+"\n") {
			t.Errorf("usage lacks %q", line)
		}
	}
}

func TestPaperCommandNoBudget(t *testing.T) {
	isolate(t)
	var out, errb bytes.Buffer
	err := paperCommand(context.Background(), config.Default(), paperFlags{}, &out, &errb)
	if err == nil || !strings.Contains(err.Error(), "paper.token_budget") {
		t.Fatalf("err = %v, want one naming paper.token_budget", err)
	}
	if got := exitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2", got)
	}
}

func TestPaperCommandNoDropbox(t *testing.T) {
	isolate(t)
	cfg := config.Default()
	cfg.Paper.TokenBudget = 400000
	var out, errb bytes.Buffer
	err := paperCommand(context.Background(), cfg, paperFlags{}, &out, &errb)
	if err == nil || !strings.Contains(err.Error(), "daily-brief dropbox login") {
		t.Fatalf("err = %v, want a pointer to `daily-brief dropbox login`", err)
	}
	if got := exitCode(err); got != 1 {
		t.Errorf("exit code = %d, want 1", got)
	}
}

func TestExitCode(t *testing.T) {
	if exitCode(nil) != 0 || exitCode(errors.New("x")) != 1 {
		t.Error("plain errors must map to 0 and 1")
	}
	if got := exitCode(&exitError{code: 4, err: errors.New("x")}); got != 4 {
		t.Errorf("exit code = %d, want 4", got)
	}
}

func TestDropboxStatusNoToken(t *testing.T) {
	isolate(t)
	cfg := config.Default()
	cfg.Paper.Dropbox.AppKey = "key"
	var out bytes.Buffer
	err := dropboxStatus(context.Background(), cfg, &out, &dropbox.Client{})
	if err == nil || !strings.Contains(err.Error(), "daily-brief dropbox login") {
		t.Fatalf("err = %v, want a pointer to `daily-brief dropbox login`", err)
	}
}

func TestDropboxStatusNoAppKey(t *testing.T) {
	isolate(t)
	err := dropboxStatus(context.Background(), config.Default(), &bytes.Buffer{}, &dropbox.Client{})
	if err == nil || !strings.Contains(err.Error(), "paper.dropbox.app_key") {
		t.Fatalf("err = %v, want one naming paper.dropbox.app_key", err)
	}
}

func TestDropboxStatusCountsEditions(t *testing.T) {
	isolate(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			_, _ = w.Write([]byte(`{"access_token":"a","expires_in":14400}`))
		case strings.HasSuffix(r.URL.Path, "/files/list_folder"):
			_, _ = w.Write([]byte(`{"entries":[{".tag":"file","name":"The Daily Brief 2026-11-24.epub"},{".tag":"file","name":"Other.epub"}],"has_more":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cfg := config.Default()
	cfg.Paper.Dropbox.AppKey = "key"
	if err := dropbox.SaveRefreshToken(dropbox.DefaultTokenPath(), "refresh"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	c := dropbox.Client{HTTP: srv.Client(), TokenURL: srv.URL + "/token", APIBase: srv.URL}
	if err := dropboxStatus(context.Background(), cfg, &out, &c); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "2 files") || !strings.Contains(got, "1 daily-brief edition") {
		t.Errorf("status output: %q", got)
	}
}

func TestDropboxLoginPastedRedirect(t *testing.T) {
	isolate(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("code") != "thecode" || r.Form.Get("client_id") != "key" {
			http.Error(w, `{"error":"bad"}`, 400)
			return
		}
		_, _ = w.Write([]byte(`{"refresh_token":"rt-123"}`))
	}))
	defer srv.Close()
	cfg := config.Default()
	cfg.Paper.Dropbox.AppKey = "key"
	var out bytes.Buffer
	tokenPath := filepath.Join(t.TempDir(), "sub", "dropbox.token")
	// The prompt sees a bare code on its second line; the first is junk
	// with a wrong state and must not end the login.
	in := strings.NewReader("http://localhost:53682/?code=x&state=wrong\nthecode\n")
	l := dropboxLogin{HTTP: srv.Client(), TokenURL: srv.URL, ListenAddr: "127.0.0.1:0", TokenPath: tokenPath}
	if err := l.run(context.Background(), cfg, in, &out); err != nil {
		t.Fatalf("login: %v\n%s", err, out.String())
	}
	b, err := os.ReadFile(tokenPath)
	if err != nil || strings.TrimSpace(string(b)) != "rt-123" {
		t.Fatalf("token file = %q, %v", b, err)
	}
	for _, want := range []string{"https://www.dropbox.com/oauth2/authorize", "http%3A%2F%2Flocalhost%3A53682%2F",
		"sudo systemd-creds encrypt --name=dropbox_refresh_token " + tokenPath + " /etc/credstore.encrypted/dropbox_refresh_token"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("login output lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "rt-123") {
		t.Error("login output leaks the refresh token")
	}
}

func TestDropboxLoginNeedsAppKey(t *testing.T) {
	isolate(t)
	l := dropboxLogin{ListenAddr: "127.0.0.1:0", TokenPath: filepath.Join(t.TempDir(), "t")}
	err := l.run(context.Background(), config.Default(), strings.NewReader(""), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "paper.dropbox.app_key") {
		t.Fatalf("err = %v", err)
	}
}

func TestBudgetShowsPaperSplit(t *testing.T) {
	cfg := config.Default()
	cfg.Paper.TokenBudget = 400000
	out := budgetOutput(t, cfg)
	split, err := paper.SplitBudget(400000, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Paper: 400k") {
		t.Fatalf("budget output lacks the paper line:\n%s", out)
	}
	for _, want := range []string{"research " + paper.HumanTokens(split.Research), "editor " + paper.HumanTokens(split.Editor)} {
		if !strings.Contains(out, want) {
			t.Errorf("budget output lacks %q:\n%s", want, out)
		}
	}
}

func TestBudgetOmitsPaperWhenUnset(t *testing.T) {
	if out := budgetOutput(t, config.Default()); strings.Contains(out, "Paper:") {
		t.Errorf("paper line without paper.token_budget:\n%s", out)
	}
}
