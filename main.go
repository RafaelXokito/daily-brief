// Command daily-brief opens a terminal journal summarizing today's news on
// the topics you care about, researched by Claude within a token budget.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"regexp"
	"runtime/debug"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"

	"github.com/RafaelXokito/daily-brief/internal/brief"
	"github.com/RafaelXokito/daily-brief/internal/config"
	"github.com/RafaelXokito/daily-brief/internal/keychain"
	"github.com/RafaelXokito/daily-brief/internal/tui"
)

// version is the release tag, set at build time by
// -ldflags "-X main.version=v1.2.3". Builds without it fall back to the
// module version Go records (see buildVersion).
var version = ""

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "daily-brief:", err)
		os.Exit(1)
	}
}

const usage = `Usage:
  daily-brief [flags]          open today's journal
  daily-brief budget           show the recommended budget and what the configured one buys
  daily-brief auth set-key     store an Anthropic API key in the macOS Keychain
  daily-brief auth delete-key  remove it
  daily-brief auth status      show which provider and credentials are configured
  daily-brief version          print the version (same as -version)

Flags:
`

func run() error {
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, usage)
		flag.PrintDefaults()
	}
	var (
		configPath = flag.String("config", config.DefaultPath(), "path to the config file")
		budget     = flag.Int64("budget", 0, "token budget for this run (overrides config; 0 keeps the config's)")
		topics     = flag.String("topics", "", "comma-separated subset of configured topics to research")
		refresh    = flag.Bool("refresh", false, "regenerate today's journal even if one exists")
		print      = flag.Bool("print", false, "print the journal instead of opening the TUI: rendered on a terminal, Markdown when piped")
		edit       = flag.Bool("edit", false, "open the config file in $EDITOR")
		provider   = flag.String("provider", "", "connection to use for this run: claude-code, anthropic, aws or vertex (overrides config)")
		showVer    = flag.Bool("version", false, "print the version and exit")
	)
	args := parseArgs()
	if *showVer || args[0] == "version" {
		fmt.Println(versionString())
		return nil
	}

	cfg, created, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *provider != "" {
		cfg.Provider.Type = *provider
	}
	if created {
		fmt.Fprintf(os.Stderr, "Created default config at %s — edit it to choose your topics and budget.\n"+
			"token_budget is 0 (auto): the recommended baseline for %s is %s tokens. Run `daily-brief budget` for details.\n",
			*configPath, cfg.Provider.Type, brief.HumanTokens(brief.Baseline(cfg)))
	}
	if *edit {
		return openEditor(*configPath)
	}
	if args[0] == "auth" {
		return authCommand(args[1], cfg, *configPath)
	}
	ov := overrides{provider: *provider, budget: *budget, topics: *topics}
	if err := ov.apply(&cfg); err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	auto := brief.ResolveBudget(&cfg)
	if args[0] == "budget" {
		return budgetCommand(cfg, auto)
	}

	// Overrides produce a different journal, so don't reuse the saved one.
	// A run over some topics is merged into it when saved.
	var journal *brief.Journal
	if !*refresh && *budget == 0 && *topics == "" {
		if journal, err = brief.LoadJournal(brief.Today()); err != nil {
			return err
		}
	}

	// A saved journal can be viewed without credentials, so a backend
	// error only matters once we need to generate.
	gen, backendErr := newGenerator(cfg)
	if backendErr != nil && journal == nil {
		return backendErr
	}
	if journal == nil {
		if note := autoNote(cfg, auto); note != "" {
			fmt.Fprintln(os.Stderr, "note:", note)
		}
		for _, msg := range brief.Assess(cfg).Messages {
			fmt.Fprintln(os.Stderr, "warning:", msg)
		}
	}
	if *print {
		return printJournal(gen, journal, cfg.Theme)
	}

	// The terminal is asked for its background before the TUI owns it, in
	// case the Settings pane switches the theme to auto.
	autoTheme := resolveTheme("auto")
	theme := resolveTheme(cfg.Theme)
	m := tui.New(gen, backendErr, journal, theme).WithSettings(tui.Settings{
		Path:      *configPath,
		Overrides: ov.describe(),
		// The pane saves the file's config; this session's overrides still
		// apply to its runs.
		Rebuild: func(saved config.Config) (*brief.Generator, error) {
			if err := ov.apply(&saved); err != nil {
				return nil, err
			}
			if err := saved.Validate(); err != nil {
				return nil, err
			}
			return newGenerator(saved)
		},
		Theme: func(name string) string {
			if name != "dark" && name != "light" {
				name = autoTheme
			}
			lipgloss.SetHasDarkBackground(name == "dark")
			return name
		},
	})
	m.WithMouse(cfg.MouseOn())
	_, err = tea.NewProgram(m, programOptions(cfg.MouseOn())...).Run()
	return err
}

// newGenerator connects to the configured provider and plans runs of cfg.
func newGenerator(cfg config.Config) (*brief.Generator, error) {
	backend, err := brief.NewBackend(context.Background(), cfg)
	if err != nil {
		return nil, err
	}
	return brief.NewGenerator(cfg, backend), nil
}

// overrides are this session's -provider, -budget and -topics flags,
// which apply to its runs but are never saved to the config.
type overrides struct {
	provider string
	budget   int64
	topics   string
}

func (o overrides) apply(cfg *config.Config) error {
	if o.provider != "" {
		cfg.Provider.Type = o.provider
	}
	if o.budget > 0 {
		cfg.TokenBudget = o.budget
	}
	if o.topics != "" {
		return cfg.FilterTopics(strings.Split(o.topics, ","))
	}
	return nil
}

// describe names the overrides in effect, e.g. "provider=aws".
func (o overrides) describe() []string {
	var out []string
	if o.provider != "" {
		out = append(out, "provider="+o.provider)
	}
	if o.budget > 0 {
		out = append(out, fmt.Sprintf("budget=%d", o.budget))
	}
	if o.topics != "" {
		out = append(out, "topics="+o.topics)
	}
	return out
}

// resolveTheme turns the configured theme into dark or light: auto takes
// GLAMOUR_STYLE if it names a light or dark style, else asks the terminal
// for its background. The TUI's own colours follow it.
func resolveTheme(theme string) string {
	if theme != "dark" && theme != "light" {
		switch os.Getenv("GLAMOUR_STYLE") {
		case "light":
			theme = "light"
		case "dark", "dracula", "tokyo-night", "pink":
			theme = "dark"
		default:
			theme = "light"
			if lipgloss.HasDarkBackground() {
				theme = "dark"
			}
		}
	}
	lipgloss.SetHasDarkBackground(theme == "dark")
	return theme
}

// programOptions takes the mouse if mouse is set, with cell-motion
// tracking (modes 1002 and 1006: clicks, the wheel and drags, never bare
// motion), so a click opens a topic and the wheel scrolls; native
// selection then needs Shift. Without it the terminal keeps the mouse, and
// in the alternate screen sends the wheel as arrow keys, which scroll the
// focused Summary pane. Output goes through tui.Output, which the copy
// key's OSC 52 shares.
func programOptions(mouse bool) []tea.ProgramOption {
	opts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithOutput(tui.Output)}
	if mouse {
		opts = append(opts, tea.WithMouseCellMotion())
	}
	return opts
}

// parseArgs parses flags anywhere on the command line, so they may follow
// a subcommand (`daily-brief budget -budget 1000000`). It returns the
// positional arguments, padded to at least two.
func parseArgs() []string {
	flag.Parse()
	var args []string
	for flag.NArg() > 0 {
		args = append(args, flag.Arg(0))
		_ = flag.CommandLine.Parse(flag.Args()[1:])
	}
	return append(args, "", "")
}

func printJournal(gen *brief.Generator, journal *brief.Journal, theme string) error {
	tty, width := stdoutTerminal()
	if tty {
		theme = resolveTheme(theme)
	}
	if journal == nil {
		journal = gen.NewJournal()
		updates := make(chan brief.Update)
		go gen.Run(context.Background(), journal, updates)
		for u := range updates {
			switch {
			case u.Section.Status != brief.StatusRunning:
			case u.Section.Markdown != "":
				fmt.Fprintf(os.Stderr, "… %s (deepening)\n", u.Section.Topic)
			default:
				fmt.Fprintf(os.Stderr, "… %s\n", u.Section.Topic)
			}
		}
		// Saving merges the run into the journal saved earlier today.
		saveErr := brief.SaveJournal(journal)
		fmt.Fprintf(os.Stderr, "This run: %s, ~%.0f min read.\n", journal.SpendSummary(), max(1, journal.ReadMinutes()))
		if note := journal.OvershootNote(); note != "" {
			fmt.Fprintln(os.Stderr, "Warning:", note)
		}
		fmt.Print(printOutput(journal, tty, width, theme))
		return saveErr
	}
	fmt.Print(printOutput(journal, tty, width, theme))
	return nil
}

// printOutput is what -print writes: on a terminal the journal rendered
// as the TUI renders it, links as OSC 8 hyperlinks, in width cells;
// piped, its Markdown exactly.
func printOutput(j *brief.Journal, tty bool, width int, theme string) string {
	if !tty {
		return j.Markdown()
	}
	return tui.Render(j.Markdown(), width, theme)
}

// stdoutTerminal reports whether stdout is a terminal, and its width.
func stdoutTerminal() (bool, int) {
	fd := int(os.Stdout.Fd())
	if !term.IsTerminal(fd) {
		return false, 0
	}
	w, _, err := term.GetSize(fd)
	if err != nil || w <= 0 {
		w = 80
	}
	return true, w
}

// autoNote explains an auto budget for a run over some of the topics, or
// is "".
func autoNote(cfg config.Config, auto bool) string {
	if !auto || len(cfg.AllTopics) <= len(cfg.EffectiveTopics()) {
		return ""
	}
	subset, full := brief.SubsetBaseline(cfg)
	return fmt.Sprintf("token_budget is auto: this run over %d of %d topics gets %s tokens, their weighted share of the %s baseline for all of them (at least one search pass each)",
		len(cfg.EffectiveTopics()), len(cfg.AllTopics), brief.HumanTokens(subset), brief.HumanTokens(full))
}

func budgetCommand(cfg config.Config, auto bool) error {
	a := brief.Assess(cfg)
	source := "configured"
	if auto {
		source = "auto: the baseline"
	}
	fmt.Printf("Provider:  %s\n", cfg.Provider.Type)
	fmt.Printf("Budget:    %s tokens (%s)\n", brief.HumanTokens(a.Budget), source)
	if a.FullBaseline > 0 {
		fmt.Printf("Baseline:  %s tokens (recommended for %d of %d topics: their weighted share of the %s\n"+
			"           baseline for all of them, at least one search pass each)\n",
			brief.HumanTokens(a.Baseline), len(cfg.EffectiveTopics()), len(cfg.AllTopics), brief.HumanTokens(a.FullBaseline))
	} else {
		fmt.Printf("Baseline:  %s tokens (recommended)\n", brief.HumanTokens(a.Baseline))
	}
	fmt.Printf("Overview:  %s tokens reserved\n\n", brief.HumanTokens(a.Reserve))

	fmt.Printf("%-24s %9s %9s %6s\n", "Topic", "Share", "Searches", "Items")
	for _, t := range a.Topics {
		fmt.Printf("%-24s %9s %9d %6d\n", t.Name, brief.HumanTokens(t.Share), t.Searches, t.Items)
	}
	fmt.Println("Share is each topic's weighted share of the budget after the overview; topics draw")
	fmt.Println("from one pool, so what one leaves funds the others. Searches and items are the first pass.")
	fmt.Printf("\nEstimated: %d searches on the first pass, about %d in all with deepen/widen passes,\n", a.Searches, a.RunSearches)
	fmt.Printf("           spending ~%s (%.0f%%); up to %d items, ~%.0f min read (cap %d min)\n",
		brief.HumanTokens(a.Spend), 100*float64(a.Spend)/float64(max(1, a.Budget)), a.Items, a.ReadMinutes, cfg.MaxReadMinutes)
	fmt.Println("The journal stays within the item caps however much is spent.")
	if a.Subscription {
		fmt.Println("Cost:      uses your Claude subscription quota, not dollars")
	} else {
		fmt.Printf("Cost:      ~$%.2f per run\n", a.CostUSD)
	}
	if len(a.Messages) > 0 {
		fmt.Println("\nWarnings:")
		for _, m := range a.Messages {
			fmt.Println("  -", m)
		}
	}
	if j, err := brief.LoadJournal(brief.Today()); err == nil && j != nil {
		if note := j.OvershootNote(); note != "" {
			fmt.Println("\nToday's journal:", note)
		}
	}
	return nil
}

func openEditor(path string) error {
	cmd := tui.EditorCommand(path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func authCommand(sub string, cfg config.Config, configPath string) error {
	switch sub {
	case "set-key":
		fmt.Println("Paste your Anthropic API key when prompted (input is hidden).")
		if err := keychain.Prompt(); err != nil {
			return err
		}
		fmt.Println("Saved. Set `provider: {type: anthropic, auth: keychain}` in", configPath, "to use it.")
		return nil
	case "delete-key":
		if err := keychain.Delete(); err != nil {
			return err
		}
		fmt.Println("API key removed from the Keychain.")
		return nil
	case "status", "":
		p := cfg.Provider
		fmt.Printf("Config:   %s\nProvider: %s\n", configPath, p.Type)
		if p.Type == "anthropic" {
			fmt.Printf("Auth:     %s\n", firstNonEmpty(p.Auth, "keychain"))
		}
		backend, err := brief.NewBackend(context.Background(), cfg)
		if err != nil {
			fmt.Println("Status:   not ready:", err)
			return nil
		}
		fmt.Println("Status:   ready —", backend.Name())
		return nil
	}
	return fmt.Errorf("unknown auth command %q (want set-key, delete-key or status)", sub)
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// buildVersion is the -X main.version value, or else the module version from
// the build info: a tag for `go install …@v1.2.3`, a pseudo-version for
// other module builds, or "(devel)" for a plain `go build`.
func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}

var releaseRE = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// isRelease reports whether v is a plain release tag like v1.2.3, not a
// pre-release, pseudo-version, dirty build or "(devel)".
func isRelease(v string) bool { return releaseRE.MatchString(v) }

// releaseVersion returns this binary's version and whether it is a release
// build, which self-update can compare against the latest release.
func releaseVersion() (string, bool) {
	v := buildVersion()
	return v, isRelease(v)
}

// versionString is the `daily-brief version` output: "daily-brief v1.2.3"
// for a release, "daily-brief dev (<version>)" otherwise, or just
// "daily-brief dev" when Go recorded no version.
func versionString() string {
	return formatVersion(releaseVersion())
}

func formatVersion(v string, release bool) string {
	if release {
		return "daily-brief " + v
	}
	if v == "(devel)" {
		return "daily-brief dev"
	}
	return "daily-brief dev (" + v + ")"
}
