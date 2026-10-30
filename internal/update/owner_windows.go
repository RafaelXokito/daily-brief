package update

import "io/fs"

// ownerToRestore is a no-op on Windows, which has no uid or gid.
func ownerToRestore(fs.FileInfo, int) (uid, gid int, ok bool) { return 0, 0, false }
