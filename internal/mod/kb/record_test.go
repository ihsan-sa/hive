package kb

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
)

func open(t *testing.T, dir string) (*core.Core, *Module) {
	c, err := core.Open(core.Workspace{Root: filepath.Join(dir, "hive")}, &core.Local{Dir: filepath.Join(dir, "forge")})
	if err != nil {
		t.Fatal(err)
	}
	return c, New(c, &agents.Agents{C: c})
}

func read(t *testing.T, c *core.Core, rel string) string {
	raw, err := os.ReadFile(filepath.Join(c.WS.KB(), "record", rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// Events are filed by subject as they happen; a snapshot shows only what
// changed; a restart catches up without filing anything twice.
func TestRecordFilesEveryEvent(t *testing.T) {
	dir := t.TempDir()
	c, _ := open(t, dir)
	c.J.MustAppend("task.saved", map[string]string{"id": "t1", "title": "Add login", "stage": "working"})
	c.J.MustAppend("task.saved", map[string]string{"id": "t1", "title": "Add login", "stage": "merged"})
	c.J.MustAppend("chat.posted", map[string]string{"chat": "project:demo", "from": "person", "text": "why did login stall?"})
	task := read(t, c, "task/t1.md")
	if strings.Count(task, "Add login") != 1 || !strings.Contains(task, "- stage: merged") {
		t.Fatalf("task record:\n%s", task)
	}
	if !strings.Contains(read(t, c, "chat/project-demo.md"), "why did login stall?") {
		t.Fatal("chat message not filed")
	}
	c.Close()

	// While the daemon is down the journal grows; the next start catches up.
	j, err := core.OpenJournal(c.WS.Events())
	if err != nil {
		t.Fatal(err)
	}
	e := j.MustAppend("risk.saved", map[string]string{"id": "r1", "want": "open port", "decision": "decline", "reason": "reaches outside"})
	j.Close()
	// A crash after writing but before moving the cursor.
	c, _ = open(t, dir)
	c.Close()
	os.WriteFile(filepath.Join(c.WS.KB(), "record", ".filed"), []byte(strconv.FormatInt(e.Seq-1, 10)), 0o644)
	c, _ = open(t, dir)
	defer c.Close()
	if risk := read(t, c, "risk/r1.md"); strings.Count(risk, "reaches outside") != 1 {
		t.Fatalf("risk record:\n%s", risk)
	}

}

// Search returns the matching entry, not its whole file, and the newest
// state first when two match as well.
func TestSearch(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "record", "task"), 0o755)
	os.WriteFile(filepath.Join(dir, "record", "task", "t1.md"), []byte("# task t1\n\n### 2026-10-01 09:00 · task.saved · seq 1\n- title: Add login\n- stage: working\n\n### 2026-10-02 09:00 · task.saved · seq 9\n- stage: merged\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "billing.md"), []byte("---\ntitle: Billing\ndate: 2026-09-01\n---\n\nInvoices go out monthly.\n"), 0o644)
	hits := Search(dir, "login merged", 20)
	if len(hits) != 2 || !strings.Contains(hits[0].Text, "seq 9") {
		t.Fatalf("hits: %+v", hits)
	}
	if hits := Search(dir, "invoices", 20); len(hits) != 1 || hits[0].File != "billing.md" {
		t.Fatalf("hits: %+v", hits)
	}
	if got := FormatHits(Search(dir, "zebra", 20)); !strings.Contains(got, "doesn't cover") {
		t.Fatal(got)
	}
}
