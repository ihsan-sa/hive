package kb

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
)

// The source is written and committed once per version of it: a restart on
// the same binary leaves the knowledge base alone, a changed source reseeds.
func TestSeedSelfOnlyWhenTheSourceChanged(t *testing.T) {
	dir := t.TempDir()
	c, err := core.Open(core.Workspace{Root: filepath.Join(dir, "hive")}, &core.Local{Dir: filepath.Join(dir, "forge")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	m := New(c, &agents.Agents{C: c})
	commits := func() int {
		out, _ := core.Git(c.WS.KB(), "rev-list", "--count", "HEAD")
		n, _ := strconv.Atoi(strings.TrimSpace(out))
		return n
	}
	src := fstest.MapFS{"internal/x/x.go": {Data: []byte("// Package x is x.\npackage x\n")}}
	m.SeedSelf(src)
	first := commits()
	m.SeedSelf(src)
	if commits() != first {
		t.Fatal("an unchanged source was committed again")
	}
	src["internal/x/x.go"] = &fstest.MapFile{Data: []byte("// Package x is y.\npackage x\n")}
	m.SeedSelf(src)
	raw, _ := os.ReadFile(filepath.Join(c.WS.KB(), SelfDir, "internal/x/x.go"))
	if commits() != first+1 || !strings.Contains(string(raw), "is y") {
		t.Fatalf("a changed source was not reseeded (%d commits after %d)", commits(), first)
	}
}
