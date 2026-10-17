//go:build !windows

package standalone

import (
	"os"
	"syscall"
)

func runProcess(path string, args, env []string) (int, error) {
	return 1, syscall.Exec(path, append([]string{os.Args[0]}, args...), env)
}
