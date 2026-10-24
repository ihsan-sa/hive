//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// Pid 1 of the sandbox reaps the orphans reparented to it while its command
// runs, so none is left a zombie, and still returns the command's own exit
// code. The test stands in for pid 1 as a child subreaper, in a process of its own.
func TestInitReapsOrphans(t *testing.T) {
	if os.Getenv("HIVE_REAP_HELPER") == "1" {
		if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
			fmt.Print("prctl: ", err)
			os.Exit(0)
		}
		// The inner sh exits at once, orphaning its sleep, which exits while
		// the command still runs.
		cmd := exec.Command("/bin/sh", "-c", "sh -c 'sleep 0.05 &'; sleep 0.5; exit 3")
		if err := cmd.Start(); err != nil {
			fmt.Print("start: ", err)
			os.Exit(0)
		}
		code, err := reap(cmd.Process.Pid)
		var ws syscall.WaitStatus
		left, werr := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
		if err != nil || code != 3 || werr != syscall.ECHILD {
			fmt.Printf("code %d, err %v; then wait gave pid %d, %v (a zombie was left)", code, err, left, werr)
			os.Exit(0)
		}
		fmt.Print("ok")
		os.Exit(0)
	}
	cmd := exec.Command(os.Args[0], "-test.run", "^TestInitReapsOrphans$")
	cmd.Env = append(os.Environ(), "HIVE_REAP_HELPER=1")
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Fatalf("%v: %s", err, out)
	}
}
