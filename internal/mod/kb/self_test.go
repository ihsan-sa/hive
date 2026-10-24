package kb

import (
	"context"
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
	m.SeedSelf(context.Background(), src)
	first := commits()
	m.SeedSelf(context.Background(), src)
	if commits() != first {
		t.Fatal("an unchanged source was committed again")
	}
	src["internal/x/x.go"] = &fstest.MapFile{Data: []byte("// Package x is y.\npackage x\n")}
	m.SeedSelf(context.Background(), src)
	raw, _ := os.ReadFile(filepath.Join(c.WS.KB(), SelfDir, "internal/x/x.go"))
	if commits() != first+1 || !strings.Contains(string(raw), "is y") {
		t.Fatalf("a changed source was not reseeded (%d commits after %d)", commits(), first)
	}
}

// A swap that can't go through (on Windows, a reader holding a file in the
// old copy) leaves the old copy whole and says so in the journal, and the
// next seed puts the new one in.
func TestSeedSelfKeepsTheOldCopyWhenTheSwapFails(t *testing.T) {
	dir := t.TempDir()
	c, err := core.Open(core.Workspace{Root: filepath.Join(dir, "hive")}, &core.Local{Dir: filepath.Join(dir, "forge")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	m := New(c, &agents.Agents{C: c})
	src := fstest.MapFS{"internal/x/x.go": {Data: []byte("// Package x is x.\npackage x\n")}}
	m.SeedSelf(context.Background(), src)
	// A folder that won't delete stands where the old copy is set aside.
	held := filepath.Join(c.WS.KB(), selfAside, "held")
	os.MkdirAll(held, 0o755)
	os.WriteFile(filepath.Join(held, "f"), []byte("x"), 0o644)
	os.Chmod(held, 0o555)
	t.Cleanup(func() { os.Chmod(held, 0o755) })
	if os.Remove(filepath.Join(held, "f")) == nil {
		t.Skip("this platform or user deletes from a read-only folder")
	}
	src["internal/x/x.go"] = &fstest.MapFile{Data: []byte("// Package x is y.\npackage x\n")}
	m.SeedSelf(context.Background(), src)
	if raw, _ := os.ReadFile(filepath.Join(c.WS.KB(), SelfDir, "internal/x/x.go")); !strings.Contains(string(raw), "is x") {
		t.Fatalf("the old copy wasn't kept whole: %q", raw)
	}
	failed := 0
	c.J.Replay(func(e core.Event) {
		if e.Kind == "kb.self_failed" {
			failed++
		}
	})
	if failed != 1 {
		t.Fatalf("%d kb.self_failed events, want 1", failed)
	}
	if st, _ := core.Git(c.WS.KB(), "status", "--porcelain"); st != "" {
		t.Fatalf("the failed swap left the knowledge base changed: %s", st)
	}
	os.Chmod(held, 0o755)
	m.SeedSelf(context.Background(), src)
	if raw, _ := os.ReadFile(filepath.Join(c.WS.KB(), SelfDir, "internal/x/x.go")); !strings.Contains(string(raw), "is y") {
		t.Fatalf("the next seed didn't put the new copy in: %q", raw)
	}
	if core.Exists(filepath.Join(c.WS.KB(), selfAside)) || core.Exists(filepath.Join(c.WS.KB(), selfStage)) {
		t.Fatal("a staged or set-aside copy was left in the knowledge base")
	}
}

// A copy SeedSelf is swapping in or out is not searched as notes.
func TestSearchSkipsTheSeedsCopies(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{selfStage, selfAside, "notes"} {
		os.MkdirAll(filepath.Join(dir, d), 0o755)
		os.WriteFile(filepath.Join(dir, d, "n.md"), []byte("zebrafish\n"), 0o644)
	}
	hits := Search(dir, "zebrafish")
	if len(hits) != 1 || !strings.HasPrefix(hits[0].File, "notes") {
		t.Fatalf("hits %+v, want only notes/n.md", hits)
	}
}
