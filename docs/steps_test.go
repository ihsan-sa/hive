package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// A hand-resolved merge of docs/architecture.md can keep both sides' copy of
// a numbered step. No number repeats under one heading.
func TestNoRepeatedNumberedStep(t *testing.T) {
	raw, err := os.ReadFile("architecture.md")
	if err != nil {
		t.Fatal(err)
	}
	step := regexp.MustCompile(`^(\d+)\. `)
	seen := map[string]int{}
	for i, l := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(l, "#") {
			clear(seen)
			continue
		}
		m := step.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		if at, ok := seen[m[1]]; ok {
			t.Errorf("architecture.md:%d repeats step %s from line %d (a union merge kept both copies; keep one)", i+1, m[1], at)
		}
		seen[m[1]] = i + 1
	}
}
