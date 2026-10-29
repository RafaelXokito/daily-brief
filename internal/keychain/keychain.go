// Package keychain stores the Anthropic API key in the macOS Keychain, so it
// never has to live in an environment variable or a config file.
package keychain

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

const (
	service = "daily-brief"
	account = "anthropic-api-key"
)

var ErrNotFound = errors.New("no API key in the Keychain; run `daily-brief auth set-key`")

func supported() error {
	if runtime.GOOS != "darwin" {
		return errors.New("the Keychain is only available on macOS; use auth: env or auth: profile")
	}
	return nil
}

// Prompt asks for the key on the terminal and stores it. The `security` tool
// reads the secret itself, so it never passes through this process or argv.
func Prompt() error {
	if err := supported(); err != nil {
		return err
	}
	cmd := exec.Command("security", "add-generic-password", "-U", "-s", service, "-a", account, "-w")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func Get() (string, error) {
	if err := supported(); err != nil {
		return "", err
	}
	out, err := exec.Command("security", "find-generic-password", "-s", service, "-a", account, "-w").Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", ErrNotFound
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func Delete() error {
	if err := supported(); err != nil {
		return err
	}
	if err := exec.Command("security", "delete-generic-password", "-s", service, "-a", account).Run(); err != nil {
		return ErrNotFound
	}
	return nil
}
