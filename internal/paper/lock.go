package paper

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const lockFile = ".lock"

// lockRun takes the papers directory's run lock without waiting, so two
// runs never build or deliver at once. unlock releases it; the kernel also
// releases it if the process dies.
func lockRun(dir string) (unlock func(), err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("paper: lock: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, lockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("paper: lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.New("paper: another paper run is in progress")
		}
		return nil, fmt.Errorf("paper: lock: %w", err)
	}
	return func() { f.Close() }, nil
}
