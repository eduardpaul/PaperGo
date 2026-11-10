//go:build windows

package config

import "io/fs"

// describeFile has nothing to add on Windows, whose access lists a mode
// does not describe.
func describeFile(fs.FileInfo) string { return "" }

// runningAs is not a number on Windows.
func runningAs() (user, group int, known bool) { return 0, 0, false }
