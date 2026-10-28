//go:build !linux

package sandbox

import "os"

// diskUsed counts nothing off Linux: macOS's rusage counts block operations,
// not bytes, and Windows has none.
func diskUsed(*os.ProcessState) Disk { return Disk{} }
