package dropbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, s string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestLoadPrefersCredential(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, CredentialName), "from-cred\n", 0o600)
	fb := filepath.Join(t.TempDir(), "dropbox.token")
	write(t, fb, "from-file", 0o600)
	got, err := LoadRefreshToken(dir, fb)
	if err != nil || got != "from-cred" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestLoadFallback(t *testing.T) {
	fb := filepath.Join(t.TempDir(), "dropbox.token")
	write(t, fb, " from-file \n", 0o600)
	for _, credDir := range []string{"", t.TempDir()} {
		got, err := LoadRefreshToken(credDir, fb)
		if err != nil || got != "from-file" {
			t.Errorf("credDir %q: got %q, %v", credDir, got, err)
		}
	}
}

func TestLoadMissingAndEmpty(t *testing.T) {
	if _, err := LoadRefreshToken("", filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("expected error for missing token")
	}
	fb := filepath.Join(t.TempDir(), "t")
	write(t, fb, "\n", 0o600)
	if _, err := LoadRefreshToken("", fb); err == nil {
		t.Error("expected error for empty token")
	}
}

func TestLoadRefusesOpenFile(t *testing.T) {
	for _, mode := range []os.FileMode{0o644, 0o640, 0o604} {
		fb := filepath.Join(t.TempDir(), "dropbox.token")
		write(t, fb, "tok", mode)
		_, err := LoadRefreshToken("", fb)
		if err == nil || !strings.Contains(err.Error(), fb) || !strings.Contains(err.Error(), "chmod 600") {
			t.Errorf("mode %o: err = %v", mode, err)
		}
	}
}

func TestSaveRefreshToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg", "daily-brief", "dropbox.token")
	if err := SaveRefreshToken(path, "tok"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file: %v %v", fi, err)
	}
	di, _ := os.Stat(filepath.Dir(path))
	if di.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %o", di.Mode().Perm())
	}
	if err := SaveRefreshToken(path, "tok2"); err != nil {
		t.Fatal(err)
	}
	got, err := LoadRefreshToken("", path)
	if err != nil || got != "tok2" {
		t.Errorf("got %q, %v", got, err)
	}
	ents, _ := os.ReadDir(filepath.Dir(path))
	if len(ents) != 1 {
		t.Errorf("leftover files: %v", ents)
	}
}

func TestDefaultTokenPath(t *testing.T) {
	t.Setenv("HOME", "/home/x")
	if got := DefaultTokenPath(); got != "/home/x/.config/daily-brief/dropbox.token" {
		t.Errorf("got %q", got)
	}
}
