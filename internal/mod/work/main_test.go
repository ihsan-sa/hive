package work

import (
	"os"
	"testing"
)

// TestMain gives every git these tests run an empty template directory, so a
// git init or clone (some 1,700 a run) doesn't copy a dozen sample hooks and a
// description into each repository: they were a fifth of the files the
// package's tests wrote, each at least a page on disk. Nor does a fetch or a
// clone write a reverse index beside each pack it keeps.
func TestMain(m *testing.M) {
	tmpl, err := os.MkdirTemp("", "git-template-")
	if err != nil {
		panic(err)
	}
	os.Setenv("GIT_TEMPLATE_DIR", tmpl)
	os.Setenv("GIT_CONFIG_COUNT", "1")
	os.Setenv("GIT_CONFIG_KEY_0", "pack.writeReverseIndex")
	os.Setenv("GIT_CONFIG_VALUE_0", "false")
	code := m.Run()
	os.RemoveAll(tmpl)
	os.Exit(code)
}
