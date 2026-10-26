package kb

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
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
	// An open journal blocks TempDir's cleanup on Windows; closing twice is harmless.
	t.Cleanup(func() { c.Close() })
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

// Search returns each file once, its best entry first and the entries that
// hold words the best one lacks with it, the newest state first when two
// match as well. Find shows the best limit of them and says how many more
// matched, and cuts a long entry to the lines that hold the words.
func TestSearch(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "record", "task"), 0o755)
	os.WriteFile(filepath.Join(dir, "record", "task", "t1.md"), []byte("# task t1\n\n### 2026-10-01 09:00 · task.saved · seq 1\n- title: Add login\n- stage: working\n\n### 2026-10-02 09:00 · task.saved · seq 9\n- stage: merged\n\n### 2026-10-03 09:00 · task.saved · seq 12\n- stage: merged\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "billing.md"), []byte("---\ntitle: Billing\ndate: 2026-09-01\n---\n\nInvoices go out monthly.\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "login-notes.md"), []byte("---\ntitle: Login\ndate: 2026-09-01\n---\n\nThe login page.\n"), 0o644)
	hits := Search(dir, "login merged")
	if len(hits) != 2 || hits[0].File != "record/task/t1.md" || !strings.Contains(hits[0].Text, "seq 12") || len(hits[0].More) != 1 || !strings.Contains(hits[0].More[0], "seq 1\n") || hits[0].Others != 1 {
		t.Fatalf("hits: %+v", hits)
	}
	if hits[0].Label != "title: Add login" || !slices.Contains(hits[0].Why, "matched login, merged") {
		t.Errorf("label %q, why %q", hits[0].Label, hits[0].Why)
	}
	if hits := Search(dir, "invoices"); len(hits) != 1 || hits[0].File != "billing.md" {
		t.Fatalf("hits: %+v", hits)
	}
	if hits := Search(dir, "invoice"); len(hits) != 1 || hits[0].File != "billing.md" {
		t.Fatalf("a word's other form: %+v", hits)
	}
	if got := Find(dir, "zebra", ""); !strings.Contains(got, "doesn't cover") {
		t.Fatal(got)
	}
	if got := Find(dir, "login merged", "1"); !strings.Contains(got, "seq 12") || !strings.Contains(got, "seq 1\n") || strings.Count(got, "== ") != 1 || !strings.Contains(got, "1 more files match") || !strings.Contains(got, "1 more entries here match") {
		t.Fatal(got)
	}
	if got := Find(dir, "login merged", "all"); strings.Count(got, "== ") != 2 || strings.Contains(got, "more files match") {
		t.Fatal(got)
	}
	os.WriteFile(filepath.Join(dir, "long.md"), []byte("intro\n"+strings.Repeat("x\n", 400)+"the walrus is here\n"+strings.Repeat("y\n", 400)), 0o644)
	if got := Find(dir, "walrus", ""); !strings.Contains(got, "the walrus is here") || strings.Contains(got, "x\nx") || !strings.Contains(got, "clipped; the rest is in long.md") {
		t.Fatal(got)
	}
	// One unreadable folder or file doesn't hide the rest of the knowledge base.
	// Root reads anything, and Windows can't make a folder unreadable with Chmod.
	if os.Geteuid() != 0 && runtime.GOOS != "windows" {
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

// The search op runs on the host, so it skips what the asking run's sandbox
// covers: another isolated project's notes and, while one is isolated, the
// record; the isolated project's own run still finds its notes.
func TestSearchOpSkipsWhatIsolationCovers(t *testing.T) {
	c, _ := open(t, t.TempDir())
	for _, p := range []core.Project{{Name: "open", Repo: "me/open"}, {Name: "secret", Repo: "me/secret", Isolated: true}} {
		if _, err := c.Projects.Add(p); err != nil {
			t.Fatal(err)
		}
	}
	kb := c.WS.KB()
	os.MkdirAll(filepath.Join(kb, "isolated", "secret"), 0o755)
	os.WriteFile(filepath.Join(kb, "isolated", "secret", "plan.md"), []byte("zebra plan"), 0o644)
	os.MkdirAll(filepath.Join(kb, "record", "task"), 0o755)
	os.WriteFile(filepath.Join(kb, "record", "task", "t1.md"), []byte("# task t1\n\n### 2026-10-01 09:00 · task.saved · seq 1\n- title: zebra task\n"), 0o644)
	os.WriteFile(filepath.Join(kb, "shared.md"), []byte("zebra shared"), 0o644)
	search := func(project string) string {
		out, err := c.Runs.Ops().Call(core.Run{RunSpec: core.RunSpec{ID: "r", Project: project}}, "search", []byte(`{"query":"zebra","limit":"all"}`))
		if err != nil {
			t.Fatal(err)
		}
		return out.(string)
	}
	if got := search("open"); strings.Contains(got, "isolated/") || strings.Contains(got, "record/") || !strings.Contains(got, "shared.md") {
		t.Errorf("a normal run's search: %s", got)
	}
	if got := search("secret"); !strings.Contains(got, "isolated/secret/plan.md") {
		t.Errorf("the isolated project's own search lost its notes: %s", got)
	}
}

// A private project's notes are kept as an isolated one's are: in its own
// folder, out of git and out of every other project's search.
func TestPrivateNotesStayInTheProject(t *testing.T) {
	c, m := newKB(t)
	c.Projects.Add(core.Project{Name: "mine", Repo: "me/mine", Private: true})
	c.Projects.Add(core.Project{Name: "open", Repo: "me/open"})
	path, err := m.Write(Note{Title: "zebra plan", Text: "zebra", Project: "mine"})
	if err != nil {
		t.Fatal(err)
	}
	if rel, _ := filepath.Rel(c.WS.KB(), path); !strings.HasPrefix(rel, filepath.Join("isolated", "mine")+string(filepath.Separator)) {
		t.Errorf("a private note went to %s", rel)
	}
	search := func(project string) string {
		out, err := c.Runs.Ops().Call(core.Run{RunSpec: core.RunSpec{ID: "r", Project: project}}, "search", []byte(`{"query":"zebra","limit":"all"}`))
		if err != nil {
			t.Fatal(err)
		}
		return out.(string)
	}
	if got := search("open"); strings.Contains(got, "isolated/") {
		t.Errorf("another project's search finds a private note: %s", got)
	}
	if got := search("mine"); !strings.Contains(got, "isolated/mine/") {
		t.Errorf("the private project's own search lost its note: %s", got)
	}
}

// Search follows the connections between files: the query's PR number
// picks its task, a task names its run and the task it reverts, a note its
// [[links]], and a file named by a hit comes up with it, saying why.
func TestSearchFollowsConnections(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"task", "run"} {
		os.MkdirAll(filepath.Join(dir, "record", d), 0o755)
	}
	task, revert, run := "261024-015726-45d2d27ed7", "261024-020043-5f804b138f", "w-261024-015726-45d2d27ed7"
	os.WriteFile(filepath.Join(dir, "record", "task", task+".md"), []byte("# task "+task+"\n\n### 2026-10-01 09:00 · task.saved · seq 1\n- id: "+task+"\n- title: Add login\n\n### 2026-10-01 10:00 · task.saved · seq 5\n- pr.number: 42\n- worker: "+run+"\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "record", "task", revert+".md"), []byte("# task "+revert+"\n\n### 2026-10-02 09:00 · task.saved · seq 9\n- id: "+revert+"\n- title: Revert: Add login\n- known: the tests went red after "+task+" merged, so it is reverted\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "record", "run", run+".md"), []byte("# run "+run+"\n\n### 2026-10-01 09:30 · run.created · seq 3\n- id: "+run+"\n- role: worker\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "gate.md"), []byte("---\ntitle: Gate\n---\n\nThe gate holds.\n\n## Related\n\n- [[login-notes]] · Login\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "login-notes.md"), []byte("---\ntitle: Login\n---\n\nThe login page.\n"), 0o644)
	files := func(hits []Hit) map[string]Hit {
		out := map[string]Hit{}
		for _, h := range hits {
			out[h.File] = h
		}
		return out
	}
	hits := Search(dir, "why was #42 reverted")
	got := files(hits)
	if hits[0].File != "record/task/"+task+".md" || !slices.Contains(hits[0].Why, "about #42") {
		t.Fatalf("the PR's task isn't first: %+v", hits)
	}
	if h, ok := got["record/task/"+revert+".md"]; !ok || !slices.Contains(h.Links, "record/task/"+task+".md") {
		t.Errorf("the revert doesn't name the task it reverts: %+v", h)
	}
	if h, ok := got["record/run/"+run+".md"]; !ok || !slices.Contains(h.Why, "named by record/task/"+task+".md") || h.Label != "role: worker" {
		t.Errorf("the task's run didn't come up with it: %+v", hits)
	}
	if h, ok := files(Search(dir, run))["record/task/"+task+".md"]; !ok {
		t.Errorf("a run's id doesn't find its task: %+v", h)
	}
	if h, ok := files(Search(dir, "gate holds"))["login-notes.md"]; !ok || !slices.Contains(h.Why, "named by gate.md") {
		t.Errorf("a note's [[link]] isn't followed: %+v", h)
	}
	if out := Find(dir, "why was #42 reverted", ""); !strings.Contains(out, "names: record/run/"+run+".md") || !strings.Contains(out, "(about #42") {
		t.Errorf("Find doesn't show the connections: %s", out)
	}
}
