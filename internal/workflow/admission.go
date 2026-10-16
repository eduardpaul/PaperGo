package workflow

import (
	"fmt"
	"sort"
)

// AdmissionPolicy selects batch sizes from ready work. Executors own grouping
// compatibility and payload limits; policies own when work becomes eligible.
type AdmissionPolicy func(ready, size int, due bool) []int

var admissionPolicies = map[string]AdmissionPolicy{}

func RegisterAdmissionPolicy(name string, p AdmissionPolicy) {
	if name == "" || p == nil || admissionPolicies[name] != nil {
		panic("invalid or duplicate admission policy: " + name)
	}
	admissionPolicies[name] = p
}
func AdmissionPolicies() []string {
	var names []string
	for name := range admissionPolicies {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
func Admit(policy string, ready, size int, due bool) ([]int, error) {
	p, ok := admissionPolicies[policy]
	if !ok {
		return nil, fmt.Errorf("unknown admission policy %s", policy)
	}
	if ready < 0 || size < 1 || size > 100 {
		return nil, fmt.Errorf("batch size must be from 1 to 100")
	}
	groups := p(ready, size, due)
	remaining := ready
	for _, n := range groups {
		if n < 1 || n > size || n > remaining {
			return nil, fmt.Errorf("admission policy %s returned invalid groups", policy)
		}
		remaining -= n
	}
	return groups, nil
}
func init() {
	groups := func(ready, size int, partial bool) []int {
		var out []int
		for ready >= size {
			out = append(out, size)
			ready -= size
		}
		if partial && ready > 0 {
			out = append(out, ready)
		}
		return out
	}
	RegisterAdmissionPolicy("threshold", func(n, size int, _ bool) []int { return groups(n, size, false) })
	RegisterAdmissionPolicy("schedule", func(n, size int, due bool) []int {
		if !due {
			return nil
		}
		return groups(n, size, true)
	})
	RegisterAdmissionPolicy("both", func(n, size int, due bool) []int { return groups(n, size, due) })
}
