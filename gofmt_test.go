package hive

import (
	"bytes"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGofmt keeps every Go file in the repository gofmt-clean, so `gofmt -l .`
// stays empty and a stray edit fails the project's test command.
func TestGofmt(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != "." && (strings.HasPrefix(d.Name(), ".") || d.Name() == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out, err := format.Source(src)
		if err != nil {
			t.Errorf("%s: %v", path, err)
		} else if !bytes.Equal(src, out) {
			t.Errorf("%s is not gofmt-clean; run gofmt -w %s", path, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
