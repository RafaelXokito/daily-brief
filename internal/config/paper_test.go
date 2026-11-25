package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPaperDefaults(t *testing.T) {
	var p Paper
	if p.TitleOrDefault() != "The Daily Brief" {
		t.Fatal(p.TitleOrDefault())
	}
	if p.FolderOrDefault() != "/Apps/Rakuten Kobo" {
		t.Fatal(p.FolderOrDefault())
	}
	if p.ArchiveFolderOrDefault() != "/Daily Brief Archive" {
		t.Fatal(p.ArchiveFolderOrDefault())
	}
	if got := (Paper{Dropbox: PaperDropbox{ArchiveFolder: "/Old"}}).ArchiveFolderOrDefault(); got != "/Old" {
		t.Fatal(got)
	}
	set := Paper{Title: "Mine", Dropbox: PaperDropbox{Folder: "/Books"}}
	if set.TitleOrDefault() != "Mine" || set.FolderOrDefault() != "/Books" {
		t.Fatalf("set values not kept: %q %q", set.TitleOrDefault(), set.FolderOrDefault())
	}
}

func TestPaperValidate(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    Paper
		ok   bool
	}{
		{"empty is fine", Paper{}, true},
		{"budget", Paper{TokenBudget: 400_000}, true},
		{"negative budget", Paper{TokenBudget: -1}, false},
		{"slash in title", Paper{Title: "a/b"}, false},
		{"backslash in title", Paper{Title: "a\\b"}, false},
		{"control in title", Paper{Title: "a\x07b"}, false},
		{"bidi in title", Paper{Title: "a‮b"}, false},
		{"zero-width space in title", Paper{Title: "a​b"}, false},
		{"title of 80 runes", Paper{Title: strings.Repeat("é", 80)}, true},
		{"title of 81 runes", Paper{Title: strings.Repeat("é", 81)}, false},
		{"relative folder", Paper{Dropbox: PaperDropbox{Folder: "Apps/Kobo"}}, false},
		{"absolute folder", Paper{Dropbox: PaperDropbox{Folder: "/Apps/Rakuten Kobo"}}, true},
		{"relative archive", Paper{Dropbox: PaperDropbox{ArchiveFolder: "Archive"}}, false},
		{"absolute archive", Paper{Dropbox: PaperDropbox{ArchiveFolder: "/Elsewhere"}}, true},
		{"archive is kobo folder", Paper{Dropbox: PaperDropbox{ArchiveFolder: "/apps/rakuten kobo/"}}, false},
		{"archive inside kobo folder", Paper{Dropbox: PaperDropbox{ArchiveFolder: "/Apps/Rakuten Kobo/Old"}}, false},
		{"archive beside kobo folder", Paper{Dropbox: PaperDropbox{ArchiveFolder: "/Apps/Rakuten Kobo Old"}}, true},
		{"default archive vs kobo folder set to it", Paper{Dropbox: PaperDropbox{Folder: "/daily brief archive"}}, false},
		{"archive days", Paper{Dropbox: PaperDropbox{ArchiveDays: 30}}, true},
		{"negative archive days", Paper{Dropbox: PaperDropbox{ArchiveDays: -1}}, false},
		{"non-ascii title", Paper{Title: "Le Brief Quotidien · été"}, true},
	} {
		cfg := Default()
		cfg.Paper = tc.p
		if err := cfg.Validate(); (err == nil) != tc.ok {
			t.Errorf("%s: err=%v", tc.name, err)
		}
	}
}

func TestPaperRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := Default()
	cfg.Paper = Paper{
		TokenBudget: 400_000,
		Title:       "Le Brief Quotidien · été",
		Dropbox:     PaperDropbox{AppKey: "abc123", Folder: "/Books", ArchiveFolder: "/Shelf", ArchiveDays: 90},
	}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Paper, cfg.Paper) {
		t.Fatalf("Paper = %+v, want %+v", got.Paper, cfg.Paper)
	}

	// A config without a paper section loads as the zero value and
	// saves without a paper key.
	plain := filepath.Join(t.TempDir(), "plain.yaml")
	if err := Save(plain, Default()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(plain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "paper:") {
		t.Fatalf("default config saved a paper key:\n%s", data)
	}
	loaded, _, err := Load(plain)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Paper != (Paper{}) {
		t.Fatalf("Paper = %+v, want zero", loaded.Paper)
	}
}

func TestPaperTitleOrDefaultTrims(t *testing.T) {
	if got := (Paper{Title: "  Mine  "}).TitleOrDefault(); got != "Mine" {
		t.Errorf("title = %q, want Mine", got)
	}
	if got := (Paper{Title: " \t "}).TitleOrDefault(); got != DefaultPaperTitle {
		t.Errorf("blank title = %q, want the default", got)
	}
}
