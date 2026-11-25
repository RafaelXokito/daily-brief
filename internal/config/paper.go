package config

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// DefaultPaperTitle names the paper, and its file, when paper.title is unset.
	DefaultPaperTitle = "The Daily Brief"
	// DefaultPaperFolder is the Dropbox folder Kobo syncs, when
	// paper.dropbox.folder is unset.
	DefaultPaperFolder = "/Apps/Rakuten Kobo"
	// MaxPaperTitleChars is the longest paper.title, in characters.
	MaxPaperTitleChars = 80
)

// Paper configures the Kobo edition: a daily EPUB delivered to Dropbox.
type Paper struct {
	// TokenBudget is the total tokens one paper aims to spend. Unlike the
	// journal's, it must be positive for `daily-brief paper`: 0 is refused
	// there, not treated as auto.
	TokenBudget int64 `yaml:"token_budget"`
	// Title names the paper and its file; empty means DefaultPaperTitle.
	Title   string       `yaml:"title,omitempty"`
	Dropbox PaperDropbox `yaml:"dropbox,omitempty"`
}

// PaperDropbox is where the paper is delivered. No secret lives here: the
// app key identifies the app, and the refresh token is a systemd credential
// or a file only its owner can read.
type PaperDropbox struct {
	AppKey string `yaml:"app_key,omitempty"`
	// Folder is an absolute Dropbox path; empty means DefaultPaperFolder.
	Folder string `yaml:"folder,omitempty"`
}

// TitleOrDefault is the paper title without surrounding spaces, or
// DefaultPaperTitle when it is unset or blank.
func (p Paper) TitleOrDefault() string {
	if t := strings.TrimSpace(p.Title); t != "" {
		return t
	}
	return DefaultPaperTitle
}

// FolderOrDefault is the Dropbox folder, or DefaultPaperFolder when unset.
func (p Paper) FolderOrDefault() string {
	if p.Dropbox.Folder == "" {
		return DefaultPaperFolder
	}
	return p.Dropbox.Folder
}

// Validate checks the paper section. The title becomes a file name, so it
// may not hold path separators, control or invisible characters.
func (p Paper) Validate() error {
	if p.TokenBudget < 0 {
		return errors.New("config: paper.token_budget must not be negative")
	}
	if n := utf8.RuneCountInString(p.Title); n > MaxPaperTitleChars {
		return fmt.Errorf("config: paper.title is %d characters; keep it to %d", n, MaxPaperTitleChars)
	}
	if strings.ContainsAny(p.Title, `/\`) {
		return errors.New("config: paper.title must not contain / or \\")
	}
	if strings.ContainsFunc(p.Title, func(r rune) bool { return unicode.IsControl(r) || IsInvisible(r) }) {
		return errors.New("config: paper.title must not contain control or invisible characters")
	}
	if f := p.Dropbox.Folder; f != "" && !strings.HasPrefix(f, "/") {
		return fmt.Errorf("config: paper.dropbox.folder must start with /, not %q", f)
	}
	return nil
}
