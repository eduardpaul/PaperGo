package config

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

// PromptAdminPassword reads a password without echoing it. A non-interactive
// standard input fails instead of inventing a password.
func PromptAdminPassword(in *os.File, out io.Writer) (string, error) {
	if in == nil || !term.IsTerminal(int(in.Fd())) {
		return "", errors.New("admin password is required")
	}
	fmt.Fprint(out, "Admin password: ")
	raw, err := term.ReadPassword(int(in.Fd()))
	fmt.Fprintln(out)
	if err != nil {
		return "", err
	}
	if len(raw) == 0 {
		return "", errors.New("admin password is required")
	}
	return string(raw), nil
}
