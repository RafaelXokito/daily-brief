package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/RafaelXokito/daily-brief/internal/brief"
	"github.com/RafaelXokito/daily-brief/internal/config"
	"github.com/RafaelXokito/daily-brief/internal/dropbox"
	"github.com/RafaelXokito/daily-brief/internal/paper"
)

const (
	dropboxRedirect = "http://localhost:53682/"
	dropboxTokenURL = "https://api.dropboxapi.com/oauth2/token"
	loginTimeout    = 10 * time.Minute
	loginHint       = "run `daily-brief dropbox login`"
)

// exitError carries the process exit code of a failed command.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

// exitCode is the process exit code for err: the code an exitError
// carries, else 1 for any error and 0 for none.
func exitCode(err error) int {
	var e *exitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &e):
		return e.code
	}
	return 1
}

// paperFlags are the command line flags `daily-brief paper` reads.
type paperFlags struct {
	budget             int64
	refresh, noDeliver bool
}

// paperCommand is `daily-brief paper`: progress and warnings go to
// stderr, the report line to stdout. The line is printed whenever the run
// got as far as a build or delivery, even when it then failed. Its error
// carries paper.ExitCode.
func paperCommand(ctx context.Context, cfg config.Config, f paperFlags, stdout, stderr io.Writer) error {
	rep, err := runPaper(ctx, cfg, f, stderr)
	if rep.Date != "" {
		_, _ = fmt.Fprintln(stdout, rep.Line())
	}
	if err != nil {
		return &exitError{code: paper.ExitCode(err), err: err}
	}
	return nil
}

func runPaper(ctx context.Context, cfg config.Config, f paperFlags, stderr io.Writer) (paper.Report, error) {
	total := cfg.Paper.TokenBudget
	if f.budget > 0 {
		total = f.budget
	}
	if total <= 0 {
		return paper.Report{}, fmt.Errorf("%w: set paper.token_budget or -budget to a positive number of tokens (0, auto, is not allowed for the paper)", paper.ErrNoBudget)
	}
	deps, err := paperDeps(ctx, cfg, f)
	if err != nil {
		return paper.Report{}, err
	}
	return paper.Run(ctx, cfg,
		paper.Options{Budget: f.budget, Refresh: f.refresh, NoDeliver: f.noDeliver, Log: stderr}, deps)
}

// paperDeps builds the Claude backend and, unless -no-deliver, the Dropbox
// deliverer a paper run talks to; tests replace it. A missing app key or
// token, or a backend that cannot be built, is a configuration error.
var paperDeps = func(ctx context.Context, cfg config.Config, f paperFlags) (paper.Deps, error) {
	var deps paper.Deps
	if !f.noDeliver {
		client, err := dropboxClient(cfg)
		if err != nil {
			return deps, fmt.Errorf("%w: %w", paper.ErrConfig, err)
		}
		deps.Deliverer = paper.NewDropboxDeliverer(&client, cfg.Paper.FolderOrDefault(), cfg.Paper.TitleOrDefault())
	}
	backend, err := brief.NewBackend(ctx, cfg)
	if err != nil {
		return deps, fmt.Errorf("%w: %w", paper.ErrConfig, err)
	}
	deps.Backend = backend
	return deps, nil
}

// dropboxClient is a client with the configured app key and the stored
// refresh token.
func dropboxClient(cfg config.Config) (dropbox.Client, error) {
	if cfg.Paper.Dropbox.AppKey == "" {
		return dropbox.Client{}, fmt.Errorf("paper.dropbox.app_key is not set: create a Dropbox app, put its app key in the config, then %s", loginHint)
	}
	token, err := dropbox.LoadRefreshToken(os.Getenv("CREDENTIALS_DIRECTORY"), dropbox.DefaultTokenPath())
	if err != nil {
		return dropbox.Client{}, fmt.Errorf("%w (%s)", err, loginHint)
	}
	return dropbox.Client{AppKey: cfg.Paper.Dropbox.AppKey, RefreshToken: token}, nil
}

func dropboxCommand(ctx context.Context, sub string, cfg config.Config) error {
	switch sub {
	case "login":
		ctx, cancel := context.WithTimeout(ctx, loginTimeout)
		defer cancel()
		l := dropboxLogin{TokenURL: dropboxTokenURL, ListenAddr: dropbox.DefaultListenAddr, TokenPath: dropbox.DefaultTokenPath()}
		return l.run(ctx, cfg, os.Stdin, os.Stdout)
	case "status":
		return dropboxStatus(ctx, cfg, os.Stdout, &dropbox.Client{})
	}
	return fmt.Errorf("unknown dropbox command %q (want login or status)", sub)
}

// dropboxStatus lists the paper folder and counts what it holds. base
// supplies test seams (HTTP, URLs); its AppKey and RefreshToken are
// replaced. It changes nothing on Dropbox.
func dropboxStatus(ctx context.Context, cfg config.Config, out io.Writer, base *dropbox.Client) error {
	c, err := dropboxClient(cfg)
	if err != nil {
		return err
	}
	base.AppKey, base.RefreshToken = c.AppKey, c.RefreshToken
	folder := cfg.Paper.FolderOrDefault()
	names, err := base.List(ctx, folder)
	if err != nil {
		return err
	}
	editions := len(paper.Stale(names, cfg.Paper.TitleOrDefault(), ""))
	_, _ = fmt.Fprintf(out, "Dropbox is connected. %s holds %d %s, %d daily-brief %s.\n",
		folder, len(names), plural(len(names), "file"), editions, plural(editions, "edition"))
	return nil
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// dropboxLogin is `daily-brief dropbox login`; the fields are test seams.
type dropboxLogin struct {
	HTTP       *http.Client
	TokenURL   string
	ListenAddr string
	TokenPath  string
}

type loginResult struct {
	code string
	err  error
}

// run prints the authorisation URL and waits for the code, from the
// loopback redirect or a pasted redirect URL, whichever comes first.
func (l dropboxLogin) run(ctx context.Context, cfg config.Config, in io.Reader, out io.Writer) error {
	appKey := cfg.Paper.Dropbox.AppKey
	if appKey == "" {
		return errors.New("paper.dropbox.app_key is not set: create a Dropbox app and put its app key in the config first")
	}
	verifier, challenge := dropbox.NewPKCE()
	state := dropbox.NewState()
	_, _ = fmt.Fprintf(out, "Open this URL, approve the app, then either let the browser reach %s\nor paste the URL it ends up on here:\n\n%s\n\n> ",
		dropboxRedirect, dropbox.AuthURL(appKey, challenge, dropboxRedirect, state))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan loginResult, 2)
	go func() {
		code, err := dropbox.Listen(ctx, l.ListenAddr, state)
		results <- loginResult{code, err}
	}()
	// The stdin reader cannot be cancelled; it is left running on purpose,
	// as the process exits right after the login.
	go func() { results <- promptForCode(in, out, state) }()

	var errs []error
	for range 2 {
		select {
		case r := <-results:
			if r.err == nil {
				cancel()
				return l.finish(ctx, appKey, r.code, verifier, out)
			}
			errs = append(errs, r.err)
		case <-ctx.Done():
			return fmt.Errorf("dropbox login: timed out or cancelled: %w", errors.Join(append(errs, ctx.Err())...))
		}
	}
	return fmt.Errorf("dropbox login: %w", errors.Join(errs...))
}

// promptForCode reads lines until one holds a valid code or the input ends.
// A bad line is reported and the prompt carries on.
func promptForCode(in io.Reader, out io.Writer, state string) loginResult {
	sc := bufio.NewScanner(in)
	sc.Buffer(nil, 1<<16)
	for sc.Scan() {
		code, err := dropbox.ParseRedirect(sc.Text(), state)
		if err == nil {
			return loginResult{code: code}
		}
		_, _ = fmt.Fprintf(out, "%v\n> ", err)
	}
	if err := sc.Err(); err != nil {
		return loginResult{err: err}
	}
	return loginResult{err: errors.New("no code pasted")}
}

func (l dropboxLogin) finish(ctx context.Context, appKey, code, verifier string, out io.Writer) error {
	// run cancelled ctx to stop the other source; the exchange still needs
	// a live context of its own.
	xctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	token, err := dropbox.ExchangeCode(xctx, l.HTTP, l.TokenURL, appKey, code, verifier, dropboxRedirect)
	if err != nil {
		return err
	}
	if err := dropbox.SaveRefreshToken(l.TokenPath, token); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "\nConnected. The refresh token is saved to %s (mode 0600).\n\n"+
		"To run the paper on a server, move it into an encrypted credential:\n\n"+
		"  sudo systemd-creds encrypt --name=%s %s /etc/credstore.encrypted/%s\n"+
		"  shred -u %s\n",
		l.TokenPath, dropbox.CredentialName, l.TokenPath, dropbox.CredentialName, l.TokenPath)
	return nil
}

// paperBudgetLines is what `daily-brief budget` adds for the paper: its
// budget and how it splits, or nothing when paper.token_budget is unset.
func paperBudgetLines(cfg config.Config) string {
	total := cfg.Paper.TokenBudget
	if total <= 0 {
		return ""
	}
	split, err := paper.SplitBudget(total, cfg)
	if err != nil {
		return fmt.Sprintf("Paper: %s tokens, but %v\n", paper.HumanTokens(total), err)
	}
	return fmt.Sprintf("Paper: %s tokens (research %s, editor %s reserved first)\n",
		paper.HumanTokens(total), paper.HumanTokens(split.Research), paper.HumanTokens(split.Editor))
}
