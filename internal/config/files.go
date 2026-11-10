package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ExplainPermission adds to a permission error on a file or directory who
// owns it, its mode and the account PaperGo runs as, so that a deployment
// can grant access. In the container image PaperGo runs as user and group
// 65532, and Compose file-backed secrets and bind mounts keep their host
// owner and mode. Other errors are returned as they are.
func ExplainPermission(err error) error {
	var path *fs.PathError
	if !errors.As(err, &path) {
		return err
	}
	return explainPermission(path.Path, err)
}

func explainPermission(path string, err error) error {
	if !errors.Is(err, fs.ErrPermission) {
		return err
	}
	// The path itself, or the nearest folder above it that can be read: a
	// file in a folder PaperGo cannot open cannot be looked at either.
	owner := ""
	for at := path; ; at = filepath.Dir(at) {
		if info, e := os.Stat(at); e == nil {
			owner = describeFile(info)
			if at != path {
				owner = ", in " + at + owner
			}
			break
		}
		if filepath.Dir(at) == at {
			break
		}
	}
	user, group, known := runningAs()
	if !known {
		return fmt.Errorf("%w (%s%s): give the account PaperGo runs as access to it", err, path, owner)
	}
	return fmt.Errorf("%w (%s%s): PaperGo runs as user %d, group %d; give that user or group access, for example chown <owner>:%d and chmod g+r for a file, or chown -R %d:%d for a data directory",
		err, path, owner, user, group, group, user, group)
}
