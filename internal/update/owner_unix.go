//go:build !windows

package update

import (
	"io/fs"
	"syscall"
)

// ownerToRestore is the uid and gid to give the new binary: the old one's,
// when root (euid 0) replaces a binary another user owns, as sudo would
// otherwise leave it root's.
func ownerToRestore(old fs.FileInfo, euid int) (uid, gid int, ok bool) {
	st, isStat := old.Sys().(*syscall.Stat_t)
	if euid != 0 || !isStat || st.Uid == 0 {
		return 0, 0, false
	}
	return int(st.Uid), int(st.Gid), true
}
