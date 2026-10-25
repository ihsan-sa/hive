package hive

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// tests/core-size.sh reads the core's packages from docs/core.md and counts
// them; a package renamed or moved without docs/core.md fails here, not
// silently on the next merge's measure.
func TestCoreSize(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the size script runs where checks run, under a POSIX sh")
	}
	out, err := exec.Command("sh", "tests/core-size.sh", "--lines").CombinedOutput()
	if err != nil {
		t.Fatalf("tests/core-size.sh: %v\n%s", err, out)
	}
	got := map[string]int{}
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(l)
		if len(f) != 2 {
			t.Fatalf("not a \"name value\" line: %q", l)
		}
		n, err := strconv.Atoi(f[1])
		if err != nil {
			t.Fatalf("not a count: %q", l)
		}
		got[f[0]] = n
	}
	if got["core_lines"] <= 0 || got["go_lines"] <= got["core_lines"] {
		t.Fatalf("core %d of %d lines: the core should be some of the Go, not none or all", got["core_lines"], got["go_lines"])
	}
	// Every package docs/core.md names is a Go package, named once.
	doc, err := os.ReadFile("docs/core.md")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^<!-- core: (.*) -->$`).FindSubmatch(doc)
	if m == nil {
		t.Fatal("docs/core.md has no <!-- core: ... --> line")
	}
	wd, _ := os.Getwd()
	list, err := exec.Command("go", "list", "-f", "{{.Dir}}", "./...").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	pkgs := map[string]bool{}
	for _, d := range strings.Split(strings.TrimSpace(string(list)), "\n") {
		if rel, err := filepath.Rel(wd, d); err == nil {
			pkgs[filepath.ToSlash(rel)] = true
		}
	}
	seen := map[string]bool{}
	for _, p := range strings.Fields(string(m[1])) {
		if seen[p] {
			t.Errorf("docs/core.md names %s twice", p)
		}
		seen[p] = true
		if !pkgs[p] {
			t.Errorf("docs/core.md names %s, which isn't a package under go list", p)
		}
	}
}
