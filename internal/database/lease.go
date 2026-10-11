package database

import (
	"fmt"
	"os"
	"path/filepath"
)

// AcquireLease coordinates API processes and administrative restore. The lock
// file stays in place so all processes lock the same inode after crashes.
func AcquireLease(path string, exclusive bool) (*os.File, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return nil, err
	}
	if resolved, e := filepath.EvalSymlinks(abs); e == nil {
		abs = resolved
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return nil, err
	}
	lockPath := filepath.Join(parent, filepath.Base(abs)) + ".lease"
	if info, e := os.Lstat(lockPath); e == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("lease must be a regular file: %s", lockPath)
	} else if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = lockLease(f, exclusive); err != nil {
		f.Close()
		return nil, fmt.Errorf("data is in use; stop the API before restoring: %w", err)
	}
	return f, nil
}
