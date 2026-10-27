package docs

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// status.md merges as union, so a merge of main can bring back a paragraph a
// later change rewrote, naming tests since renamed or never written. Every
// test it names in backticks exists.
func TestStatusNamesRealTests(t *testing.T) {
	b, err := os.ReadFile("status.md")
	if err != nil {
		t.Fatal(err)
	}
	funcs := map[string]bool{}
	decl := regexp.MustCompile(`(?m)^func (Test\w+)\(`)
	filepath.WalkDir("..", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() && strings.HasPrefix(d.Name(), ".") && p != ".." {
			return fs.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(p, "_test.go") {
			src, _ := os.ReadFile(p)
			for _, m := range decl.FindAllStringSubmatch(string(src), -1) {
				funcs[m[1]] = true
			}
		}
		return nil
	})
	for i, line := range strings.Split(string(b), "\n") {
		for _, m := range regexp.MustCompile("`(Test\\w+)`").FindAllStringSubmatch(line, -1) {
			if !funcs[m[1]] {
				t.Errorf("status.md:%d names %s, which no test file declares (a union merge may have kept a stale paragraph)", i+1, m[1])
			}
		}
	}
}
