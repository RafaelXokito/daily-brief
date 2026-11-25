package dropbox

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// CredentialName is the systemd credential holding the refresh token.
const CredentialName = "dropbox_refresh_token"

// LoadRefreshToken reads the token from credDir/CredentialName (the systemd
// $CREDENTIALS_DIRECTORY) if present, otherwise from the fallback file, which
// must not be readable by group or others.
func LoadRefreshToken(credDir, fallback string) (string, error) {
	if credDir != "" {
		b, err := os.ReadFile(filepath.Join(credDir, CredentialName))
		if err == nil {
			return nonEmpty(string(b), filepath.Join(credDir, CredentialName))
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("dropbox: read credential: %w", err)
		}
	}
	fi, err := os.Stat(fallback)
	if err != nil {
		return "", fmt.Errorf("dropbox: no refresh token (run `daily-brief dropbox login`): %w", err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("dropbox: %s is readable by group or others (mode %04o); run: chmod 600 %s", fallback, fi.Mode().Perm(), fallback)
	}
	b, err := os.ReadFile(fallback)
	if err != nil {
		return "", fmt.Errorf("dropbox: read %s: %w", fallback, err)
	}
	return nonEmpty(string(b), fallback)
}

func nonEmpty(s, from string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("dropbox: %s is empty", from)
	}
	return s, nil
}

// SaveRefreshToken writes token to path atomically (dir 0700, file 0600).
func SaveRefreshToken(path, token string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("dropbox: create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".dropbox.token-*")
	if err != nil {
		return fmt.Errorf("dropbox: save token: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(token + "\n"); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// DefaultTokenPath is ~/.config/daily-brief/dropbox.token.
func DefaultTokenPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "daily-brief", "dropbox.token")
}
