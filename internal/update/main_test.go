package update

import (
	"fmt"
	"os"
	"testing"
)

// TestMain keeps every test away from the user's data directory, where
// the check cache lives.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "daily-brief-update-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Setenv("XDG_DATA_HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
