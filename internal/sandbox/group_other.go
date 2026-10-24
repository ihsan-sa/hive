//go:build !windows

package sandbox

// group is Windows' alone; on Linux bwrap's --die-with-parent ends a
// sandboxed command's children with it.
func group(int) func(kill bool) { return func(bool) {} }
