package model

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// UTF16Length counts the units used by the API's Unicode length limits.
func UTF16Length(value string) int {
	n := 0
	for _, r := range value {
		n++
		if r > 0xffff {
			n++
		}
	}
	return n
}

// ValidateTitle applies to item and folder titles and uploaded filenames.
func ValidateTitle(value string) error {
	if !utf8.ValidString(value) || strings.TrimSpace(value) == "" || UTF16Length(value) > 1024 {
		return fmt.Errorf("title must contain 1 to 1024 UTF-16 units")
	}
	return nil
}
