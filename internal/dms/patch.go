package dms

// patchValues changes only the supplied keys. JSON null removes a key.
// Neither map is mutated, so callers and failed transactions keep their input.
func patchValues(current, changes map[string]any) map[string]any {
	result := make(map[string]any, len(current)+len(changes))
	for key, value := range current {
		result[key] = value
	}
	for key, value := range changes {
		if value == nil {
			delete(result, key)
		} else {
			result[key] = value
		}
	}
	return result
}
