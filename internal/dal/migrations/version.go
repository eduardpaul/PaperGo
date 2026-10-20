package migrations

import (
	"fmt"
	"regexp"
	"strconv"
)

var versionPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-rc\.(0|[1-9][0-9]*))?$`)

// CompareVersions orders stable releases after their release candidates.
func CompareVersions(a, b string) (int, error) {
	parse := func(s string) ([5]uint64, error) {
		var key [5]uint64
		match := versionPattern.FindStringSubmatch(s)
		if match == nil {
			return key, fmt.Errorf("invalid release version %q", s)
		}
		for i, index := range []int{1, 2, 3, 5} {
			if match[index] == "" {
				continue
			}
			n, err := strconv.ParseUint(match[index], 10, 64)
			if err != nil {
				return key, fmt.Errorf("invalid release version %q: %w", s, err)
			}
			if i == 3 {
				key[4] = n
			} else {
				key[i] = n
			}
		}
		if match[4] == "" {
			key[3] = 1
		}
		return key, nil
	}
	x, err := parse(a)
	if err != nil {
		return 0, err
	}
	y, err := parse(b)
	if err != nil {
		return 0, err
	}
	for i := range x {
		if x[i] < y[i] {
			return -1, nil
		}
		if x[i] > y[i] {
			return 1, nil
		}
	}
	return 0, nil
}
