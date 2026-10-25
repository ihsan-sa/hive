package e2e

import (
	"runtime"

	"golang.org/x/sys/unix"
)

// A child inherits the CPU affinity of the thread that forks it, and passes
// it on: the sandbox, a check's go build and its test binaries all start
// from it, and Go sizes GOMAXPROCS (so go build's -p and go test's
// parallelism) to it. So the start runs on a locked thread narrowed to cpus
// of the CPUs this process may use, which gets its own mask back after.
func init() {
	pin = func(cpus int, start func() error) error {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		var all unix.CPUSet
		if unix.SchedGetaffinity(0, &all) != nil || all.Count() <= cpus {
			return start()
		}
		var some unix.CPUSet
		for c := 0; some.Count() < cpus; c++ {
			if all.IsSet(c) {
				some.Set(c)
			}
		}
		if unix.SchedSetaffinity(0, &some) != nil {
			return start()
		}
		defer unix.SchedSetaffinity(0, &all)
		return start()
	}
}
