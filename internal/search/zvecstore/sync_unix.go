//go:build !windows

package zvecstore

import (
	"errors"
	"os"
)

func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}
