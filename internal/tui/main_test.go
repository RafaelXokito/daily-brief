package tui

import (
	"fmt"
	"os"
	"testing"
)

// TestMain keeps every test away from the user's journals: runs read
// earlier days' journals for history, and save into today's.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "daily-brief-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Setenv("XDG_DATA_HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
