//go:build unix

package database

import (
	"golang.org/x/sys/unix"
	"os"
)

func lockLease(f *os.File, exclusive bool) error {
	flags := unix.LOCK_SH | unix.LOCK_NB
	if exclusive {
		flags = unix.LOCK_EX | unix.LOCK_NB
	}
	return unix.Flock(int(f.Fd()), flags)
}
