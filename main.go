// Command daily-brief opens a terminal journal summarizing today's news on
// the topics you care about, researched by Claude within a token budget.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/rafaelpereira/daily-brief/internal/brief"
	"github.com/rafaelpereira/daily-brief/internal/config"
	"github.com/rafaelpereira/daily-brief/internal/keychain"
	"github.com/rafaelpereira/daily-brief/internal/tui"
)

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
		print      = flag.Bool("print", false, "print the journal as Markdown instead of opening the TUI")
		edit       = flag.Bool("edit", false, "open the config file in $EDITOR")
		provider   = flag.String("provider", "", "connection to use for this run: claude-code, anthropic, aws or vertex (overrides config)")
	)
	args := parseArgs()

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
	if *budget > 0 {
		cfg.TokenBudget = *budget
	}
	if *topics != "" {
		if err := cfg.FilterTopics(strings.Split(*topics, ",")); err != nil {
			return err
		}
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
	backend, backendErr := brief.NewBackend(context.Background(), cfg)
	var gen *brief.Generator
	if backendErr == nil {
		gen = brief.NewGenerator(cfg, backend)
	} else if journal == nil {
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
		return printJournal(gen, journal)
	}

	style := "light"
	if lipgloss.HasDarkBackground() {
		style = "dark"
	}
	_, err = tea.NewProgram(tui.New(gen, backendErr, journal, style), programOptions()...).Run()
	return err
}

// programOptions leaves the mouse to the terminal, so text can always be
// selected and copied natively; in the alternate screen, terminals send
// the wheel as arrow keys, which scroll the focused Summary pane.
func programOptions() []tea.ProgramOption {
	return []tea.ProgramOption{tea.WithAltScreen()}
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

func printJournal(gen *brief.Generator, journal *brief.Journal) error {
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
		fmt.Print(journal.Markdown())
		return saveErr
	}
	fmt.Print(journal.Markdown())
	return nil
}

// autoNote explains an auto budget for a run over some of the topics, or
// is "".
func autoNote(cfg config.Config, auto bool) string {
	if !auto || len(cfg.AllTopics) <= len(cfg.Topics) {
		return ""
	}
	subset, full := brief.SubsetBaseline(cfg)
	return fmt.Sprintf("token_budget is auto: this run over %d of %d topics gets %s tokens, their weighted share of the %s baseline for all of them (at least one search pass each)",
		len(cfg.Topics), len(cfg.AllTopics), brief.HumanTokens(subset), brief.HumanTokens(full))
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
			brief.HumanTokens(a.Baseline), len(cfg.Topics), len(cfg.AllTopics), brief.HumanTokens(a.FullBaseline))
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
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	cmd := exec.Command(editor, path)
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
