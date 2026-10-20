package zvecstore

// Windows does not support fsync on directory handles opened by os.Open.
func syncDirectory(string) error { return nil }
