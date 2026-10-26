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
