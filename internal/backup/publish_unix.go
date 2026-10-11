//go:build unix

package backup

import "os"

func publishArchive(source, destination string) error { return os.Link(source, destination) }
