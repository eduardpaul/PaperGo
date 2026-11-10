//go:build !windows

package config

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// describeFile is a file's owner and mode, for permission errors.
func describeFile(info fs.FileInfo) string {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf(", owned by %d:%d with mode %04o", st.Uid, st.Gid, info.Mode().Perm())
	}
	return fmt.Sprintf(", mode %04o", info.Mode().Perm())
}

// runningAs is the user and group the process runs as.
func runningAs() (user, group int, known bool) {
	return os.Getuid(), os.Getgid(), true
}
