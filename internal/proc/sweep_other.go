//go:build !linux

package proc

// OwnerID, OwnerAlive and Sweep read /proc: elsewhere a process group is all
// that ends what a process left.
func OwnerID() string                                    { return "" }
func OwnerAlive(string) bool                             { return true }
func Sweep(func(env [][]byte, exe, cwd string) bool) int { return 0 }
