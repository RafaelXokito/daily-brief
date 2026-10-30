//go:build !windows

package update

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Only root replacing another user's binary restores its owner.
func TestOwnerToRestore(t *testing.T) {
	p := filepath.Join(t.TempDir(), "daily-brief")
	if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	st := info.Sys().(*syscall.Stat_t)
	if _, _, ok := ownerToRestore(info, 1000); ok {
		t.Error("a non-root update changes the owner")
	}
	uid, gid, ok := ownerToRestore(info, 0)
	if os.Getuid() == 0 {
		if ok {
			t.Error("root's own binary given an owner")
		}
	} else if !ok || uid != int(st.Uid) || gid != int(st.Gid) {
		t.Errorf("root updating a user's binary: %d:%d %v, want %d:%d", uid, gid, ok, st.Uid, st.Gid)
	}
	// A root-owned binary stays root's.
	if root, err := os.Stat("/bin/sh"); err == nil && root.Sys().(*syscall.Stat_t).Uid == 0 {
		if _, _, ok := ownerToRestore(root, 0); ok {
			t.Error("a root-owned binary given an owner")
		}
	}
}
