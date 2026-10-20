package database

import (
	"golang.org/x/sys/windows"
	"os"
)

func lockLease(f *os.File, exclusive bool) error {
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	return windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, &windows.Overlapped{})
}

// ShareLease permits online backups after exclusive release initialization.
// Callers close all SQLite handles before unlocking this lease.
func ShareLease(f *os.File) error {
	if err := windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{}); err != nil {
		return err
	}
	return lockLease(f, false)
}
