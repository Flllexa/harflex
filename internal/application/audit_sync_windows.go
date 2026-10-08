package application

// Windows does not expose directory fsync through os.File.
func syncAuditDirectory(string) error { return nil }
