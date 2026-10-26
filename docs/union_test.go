package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// A hand-resolved merge of architecture.md can keep both sides' copy of a
// bullet both changed. Each bullet that opens with a name in backticks (a
// setting, a package) names it once.
func TestArchitectureBulletsOnce(t *testing.T) {
	b, err := os.ReadFile("architecture.md")
	if err != nil {
		t.Fatal(err)
	}
	lead := regexp.MustCompile("^- (`[^`]+`(?:, `[^`]+`)*)")
	seen := map[string]int{}
	for i, line := range strings.Split(string(b), "\n") {
		m := lead.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if at, ok := seen[m[1]]; ok {
			t.Errorf("architecture.md:%d repeats the bullet for %s from line %d, likely a union merge keeping both sides: keep one", i+1, m[1], at)
		}
		seen[m[1]] = i + 1
	}
}

// status.md is merge=union, so a merge keeps both sides' copy of a bullet a
// branch edited (#679 landed with two "Work starts in order" bullets, old and
// new, and a third "Worker and check limits"). No two top-level bullets
// open with the same words: their first 60 bytes differ.
func TestStatusBulletsOnce(t *testing.T) {
	b, err := os.ReadFile("status.md")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for i, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "- ") || len(line) < 60 {
			continue
		}
		head := line[:60]
		if at, ok := seen[head]; ok {
			t.Errorf("status.md:%d repeats the bullet %q from line %d, likely a union merge keeping both sides: keep the newer one", i+1, head, at)
		}
		seen[head] = i + 1
	}
}
