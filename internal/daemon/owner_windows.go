package daemon

// fileOwner is empty on Windows, where the lock file's ACL is what matters.
func fileOwner(string) string { return "" }

func fileOwnerOf(int) string { return "" }
