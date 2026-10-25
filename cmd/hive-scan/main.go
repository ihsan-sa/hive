// Command hive-scan is the release workflow's identity and secret scan:
//
//	HIVE_SCAN_HANDLES=<handles> [HIVE_SCAN_DENY=<words>] go run ./cmd/hive-scan [dir]
//
// scans the files of the git checkout at dir (default .) that its
// .hive/public.txt names, and fails on a key, a deny word or a handle (see
// internal/mod/scan). Both lists are repository secrets, never files in the
// repository; with no handle list it fails.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/mod/scan"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "hive-scan:", err)
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			// An annotation, which the forge serves to anyone, unlike the job's log:
			// the Hive reads why its release scan failed. A hit names file, line and
			// kind only, so this prints no key, deny word or handle.
			fmt.Println(annotation(err))
		}
		os.Exit(1)
	}
	fmt.Println("hive-scan: nothing to keep from publishing")
}

func run(args []string) error {
	dir := "."
	if len(args) > 1 {
		return fmt.Errorf("usage: hive-scan [dir]")
	} else if len(args) == 1 {
		dir = args[0]
	}
	deny, handles, err := scan.RunTime(os.Getenv, "")
	if err != nil {
		return err
	}
	public, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(scan.PublicList)))
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("can't list the checkout's files: %v", err)
	}
	names := strings.Split(strings.TrimRight(string(out), "\x00"), "\x00")
	read := func(name string) ([]byte, error) { return os.ReadFile(filepath.Join(dir, filepath.FromSlash(name))) }
	return scan.Run(names, read, scan.Lists{Public: scan.ParseList(string(public), false), Deny: deny, Handles: handles})
}

// annotation is err as a GitHub Actions error annotation, its lines kept.
func annotation(err error) string {
	r := strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A")
	return "::error title=hive-scan::" + r.Replace(err.Error())
}
