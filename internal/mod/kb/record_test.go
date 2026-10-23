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
// changed, a cleared field included; a restart catches up without filing
// anything twice, whatever state a crash left the cursor in.
func TestRecordFilesEveryEvent(t *testing.T) {
	dir := t.TempDir()
	c, _ := open(t, dir)
	c.J.MustAppend("task.saved", map[string]string{"id": "t1", "chat": "project:demo", "title": "Add login", "stage": "working", "reason": "flaky test"})
	c.J.MustAppend("task.saved", map[string]string{"id": "t1", "chat": "project:demo", "title": "Add login", "stage": "merged"})
	c.J.MustAppend("chat.posted", map[string]string{"chat": "project:demo", "id": "m1", "from": "person", "text": "why did login stall?"})
	task := read(t, c, "task/t1.md")
	if strings.Count(task, "Add login") != 1 || !strings.Contains(task, "- stage: merged") || !strings.Contains(task, "- reason: (cleared)") {
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
	j.MustAppend("plan.usage", map[string]string{"status": "allowed"})
	j.MustAppend("task.saved", map[string]string{"id": "t1", "title": "Add login", "stage": "reverted"})
	j.Close()
	filed := filepath.Join(c.WS.KB(), "record", ".filed")
	restart := func(cursor string) {
		t.Helper()
		c, _ = open(t, dir)
		c.Close()
		os.WriteFile(filed, []byte(cursor), 0o644)
		c, _ = open(t, dir)
		c.Close()
		if risk := read(t, c, "risk/r1.md"); strings.Count(risk, "reaches outside") != 1 {
			t.Fatalf("cursor %q, risk record:\n%s", cursor, risk)
		}
		task := read(t, c, "task/t1.md")
		if strings.Count(task, "Add login") != 1 || strings.Count(task, "- stage: reverted") != 1 {
			t.Fatalf("cursor %q, task record:\n%s", cursor, task)
		}
		if strings.Count(read(t, c, "chat/project-demo.md"), "why did login stall?") != 1 {
			t.Fatalf("cursor %q: chat message filed twice", cursor)
		}
	}
	// A crash after writing an entry but before moving the cursor, with a
	// skipped event between the cursor and the half-filed one.
	restart(strconv.FormatInt(e.Seq, 10))
	// A cursor lost or cut short is no cursor: everything is rechecked.
	restart("")
	restart("1") // what is left of a cut "12"
}

// Search returns the matching entry, not its whole file, and the newest
// state first when two match as well. Find shows the best limit of them and
// says how many more matched, and marks an entry it clips.
func TestSearch(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "record", "task"), 0o755)
	os.WriteFile(filepath.Join(dir, "record", "task", "t1.md"), []byte("# task t1\n\n### 2026-10-01 09:00 · task.saved · seq 1\n- title: Add login\n- stage: working\n\n### 2026-10-02 09:00 · task.saved · seq 9\n- stage: merged\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "billing.md"), []byte("---\ntitle: Billing\ndate: 2026-09-01\n---\n\nInvoices go out monthly.\n"), 0o644)
	hits := Search(dir, "login merged")
	if len(hits) != 2 || !strings.Contains(hits[0].Text, "seq 9") {
		t.Fatalf("hits: %+v", hits)
	}
	if hits := Search(dir, "invoices"); len(hits) != 1 || hits[0].File != "billing.md" {
		t.Fatalf("hits: %+v", hits)
	}
	if got := Find(dir, "zebra", ""); !strings.Contains(got, "doesn't cover") {
		t.Fatal(got)
	}
	if got := Find(dir, "login merged", "1"); !strings.Contains(got, "seq 9") || strings.Contains(got, "seq 1\n") || !strings.Contains(got, "1 more match") {
		t.Fatal(got)
	}
	if got := Find(dir, "login merged", "all"); !strings.Contains(got, "seq 1\n") || strings.Contains(got, "more match") {
		t.Fatal(got)
	}
	os.WriteFile(filepath.Join(dir, "long.md"), []byte("walrus "+strings.Repeat("x", 2000)), 0o644)
	if got := Find(dir, "walrus", ""); !strings.Contains(got, "clipped; the rest is in long.md") {
		t.Fatal(got)
	}
	// One unreadable folder or file doesn't hide the rest of the knowledge base.
	if os.Geteuid() != 0 {
		os.MkdirAll(filepath.Join(dir, "a-locked"), 0o755)
		os.WriteFile(filepath.Join(dir, "a-locked", "x.md"), []byte("invoices"), 0o644)
		os.WriteFile(filepath.Join(dir, "a-locked.md"), []byte("invoices"), 0o644)
		os.Chmod(filepath.Join(dir, "a-locked.md"), 0)
		os.Chmod(filepath.Join(dir, "a-locked"), 0)
		defer os.Chmod(filepath.Join(dir, "a-locked"), 0o755)
		if hits := Search(dir, "invoices"); len(hits) != 1 || hits[0].File != "billing.md" {
			t.Fatalf("hits past an unreadable folder: %+v", hits)
		}
	}
}
