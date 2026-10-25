package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fixture reads the fixture knowledge base under _fixture/kb (a folder go and
// the module's embed both leave out).
func fixture(t *testing.T) []Shared {
	t.Helper()
	root := filepath.Join("_fixture", "kb")
	var out []Shared
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, Shared{Path: filepath.ToSlash(rel), Text: string(raw)})
		return nil
	})
	return out
}

// apply does what the kb module does with a pass: checks each file is as the
// pass read it, then writes and removes.
func apply(t *testing.T, notes []Shared, p Pass) []Shared {
	t.Helper()
	have := map[string]string{}
	for _, n := range notes {
		have[n.Path] = n.Text
	}
	for _, c := range p.Changes {
		old, ok := have[c.Path]
		if ok && hashOf(old) != c.Was || !ok && c.Was != "" {
			t.Fatalf("%s: stale or unknown was", c.Path)
		}
		if c.Remove {
			delete(have, c.Path)
		} else {
			have[c.Path] = c.Text
		}
	}
	var out []Shared
	for p, text := range have {
		out = append(out, Shared{Path: p, Text: text})
	}
	slices.SortFunc(out, func(a, b Shared) int { return strings.Compare(a.Path, b.Path) })
	return out
}

func text(notes []Shared, path string) string {
	for _, n := range notes {
		if n.Path == path {
			return n.Text
		}
	}
	return ""
}

var t0 = time.Date(2026, 10, 25, 4, 0, 0, 0, time.UTC)

// record folds a small record: the banner's run finished done after its
// note was written, the gate's task is still blocked, one change was rolled
// back and one merged, the person said something, and an isolated project's
// task must stay out of every note.
func record(t *testing.T) *State {
	st := newState()
	ev := func(seq int64, at time.Time, kind string, data map[string]any) {
		raw, _ := json.Marshal(data)
		if job := st.fold(Event{Seq: seq, At: at, Kind: kind, Data: raw}, "kb"); kind == "module.scheduled" && job != "kb.dream" {
			t.Fatalf("module.scheduled for kb read as %q", job)
		}
	}
	at := t0.Add(-3 * time.Hour)
	ev(1, at, "run.created", map[string]any{"id": "w-banner", "task": "t-banner", "project": "hive"})
	ev(2, at, "task.saved", map[string]any{"id": "t-banner", "title": "Ship the release banner", "project": "hive", "stage": "working"})
	ev(3, at, "run.created", map[string]any{"id": "w-gate", "task": "t-gate", "project": "hive"})
	ev(4, at, "task.saved", map[string]any{"id": "t-gate", "title": "The recursive gate: a candidate Hive must beat the current one", "project": "hive", "stage": "blocked", "reason": "waiting on the noise band"})
	ev(5, at.Add(time.Hour), "run.finish", map[string]any{"id": "w-banner", "finish": map[string]any{"status": "done"}})
	ev(6, at.Add(time.Hour), "task.saved", map[string]any{"id": "t-banner", "title": "Ship the release banner", "project": "hive", "stage": "merged"})
	ev(7, at.Add(2*time.Hour), "gate.judged", map[string]any{"project": "hive", "outcome": "rollback", "head": "fe55aed96bfb", "reason": "not better than the current Hive: nothing moved beyond the noise"})
	ev(8, at.Add(2*time.Hour), "chat.posted", map[string]any{"chat": "lead", "from": "person", "text": "the kb should dream"})
	ev(9, at, "task.saved", map[string]any{"id": "t-secret", "title": "Secret thing", "project": "vault", "stage": "merged"})
	ev(9, at, "run.created", map[string]any{"id": "w-secret", "task": "t-secret", "project": "vault"})
	ev(9, at.Add(2*time.Hour), "chat.posted", map[string]any{"chat": "agent:w-secret", "from": "person", "text": "the vault password is hunter2"})
	ev(9, at.Add(2*time.Hour), "chat.posted", map[string]any{"chat": "agent:w-unknown", "from": "person", "text": "a run never seen created"})
	ev(9, at.Add(2*time.Hour), "chat.posted", map[string]any{"chat": "agent:w-banner", "from": "person", "text": "the banner looks right"})
	ev(10, t0, "module.scheduled", map[string]any{"module": "kb", "job": "kb.dream"})
	if st.fold(Event{Seq: 11, At: t0, Kind: "module.scheduled", Data: json.RawMessage(`{"module":"other","job":"kb.dream"}`)}, "kb") != "" {
		t.Fatal("another module's schedule read as this one's")
	}
	if st.Cursor != 11 {
		t.Fatalf("cursor %d, want 11", st.Cursor)
	}
	return st
}

func plan(t *testing.T, notes []Shared, st *State) Pass {
	return Plan(Input{Notes: notes, Shipped: []string{"practice/risk.md"}, Hidden: []string{"vault"}, State: st, Now: t0, Trigger: "scheduled"})
}

const gate = "blocked-the-recursive-gate-a-candidate-hive-must-beat-the-cu"

// The three gate notes become one living note: the newest text first, every
// older fact under History with its date and file, nothing said twice, every
// source and run in the header; the two others are removed.
func TestMergeKeepsEveryFactAndSource(t *testing.T) {
	notes := fixture(t)
	p := plan(t, notes, record(t))
	if len(p.Merged) != 1 || p.Merged[0].Into != gate+".md" || !slices.Equal(p.Merged[0].From, []string{gate + "-2.md", gate + "-3.md"}) {
		t.Fatalf("merged %+v", p.Merged)
	}
	after := apply(t, notes, p)
	living := text(after, gate+".md")
	if text(after, gate+"-2.md") != "" || text(after, gate+"-3.md") != "" {
		t.Fatal("merged notes left behind")
	}
	for _, want := range []string{
		"consolidated: 2026-10-25",
		"sources: " + gate + "-2.md, " + gate + "-3.md, " + gate + ".md",
		"tags: gate, sandbox",
		"TestMeter is fixed, and it waits on the noise band", // the newest, first
		"both sides fail TestMeter",                          // older facts kept
		"bubblewrap is missing on the host",
		"was " + gate + "-2.md",
		"## History",
	} {
		if !strings.Contains(living, want) {
			t.Errorf("living note lacks %q:\n%s", want, living)
		}
	}
	body := living[strings.Index(living, "\n---\n"):]
	if strings.Index(body, "waits on the noise band") > strings.Index(body, "## History") {
		t.Error("the newest text is not first")
	}
	if n := strings.Count(living, "**Tested:** go vet passes."); n != 1 {
		t.Errorf("a shared paragraph appears %d times", n)
	}

	// A new duplicate later is folded into the living note, newest first.
	after = append(after, Shared{Path: gate + "-2.md", Text: "---\ntitle: Blocked: The recursive gate: a candidate Hive must beat the current one\ntags: gate\nproject: hive\ndate: 2026-10-25\nrun: w-gate\n---\n\nThe gate kept its first change.\n"})
	p = plan(t, after, record(t))
	again := text(apply(t, after, p), gate+".md")
	if !strings.Contains(again, "bubblewrap is missing on the host") || strings.Index(again, "kept its first change") > strings.Index(again, "waits on the noise band") {
		t.Errorf("re-merge lost history or order:\n%s", again)
	}
}

// Notes that share their subject link both ways across projects; an
// unrelated one gets no link.
func TestLinksBothWays(t *testing.T) {
	notes := fixture(t)
	after := apply(t, notes, plan(t, notes, record(t)))
	a, b := text(after, "sandbox-mounts-hide-the-private-folder.md"), text(after, "bubblewrap-needs-user-namespaces.md")
	if !strings.Contains(a, "[[bubblewrap-needs-user-namespaces]]") || !strings.Contains(b, "[[sandbox-mounts-hide-the-private-folder]]") {
		t.Errorf("related notes not linked both ways:\n%s\n%s", a, b)
	}
	if !strings.Contains(a, "## Related\n\n- [[bubblewrap-needs-user-namespaces]] · Bubblewrap needs user namespaces") {
		t.Errorf("the Related list lacks the title:\n%s", a)
	}
	if strings.Contains(text(after, "releases-stay-unsigned.md"), "## Related") {
		t.Error("an unrelated note got links")
	}
	if strings.Contains(text(after, "practice/risk.md"), "Related") {
		t.Error("a shipped practice doc was edited")
	}
}

// A blocked note whose run has since finished done, and a decision another
// note revises, are retired with why, their text kept; a note whose task is
// still blocked stays.
func TestRetireContradicted(t *testing.T) {
	notes := fixture(t)
	p := plan(t, notes, record(t))
	got := map[string]string{}
	for _, r := range p.Retired {
		got[r.Path] = r.Why
	}
	if !strings.Contains(got["blocked-ship-the-release-banner.md"], "finished done") || !strings.Contains(got["module-layout-decision-hybrid.md"], "superseded by [[module-layout-revised]]") || len(got) != 2 {
		t.Fatalf("retired %v", got)
	}
	after := apply(t, notes, p)
	banner := text(after, "blocked-ship-the-release-banner.md")
	if !strings.Contains(banner, "retired: 2026-10-25 · its run w-banner") || !strings.Contains(banner, "Waiting for the forge's checks") {
		t.Errorf("retired note lost its mark or its text:\n%s", banner)
	}
	if strings.Contains(text(after, gate+".md"), "retired:") {
		t.Error("a note whose task is still blocked was retired")
	}
	// The decision links to what superseded it, and back.
	if !strings.Contains(text(after, "module-layout-revised.md"), "[[module-layout-decision-hybrid]]") {
		t.Error("the superseding note doesn't link back")
	}
}

// The record is distilled into a project summary and the practice note; the
// digest says what moved and what the pass did; an isolated project appears
// nowhere; a second pass with no news changes nothing and writes no digest.
func TestDistilAndDigest(t *testing.T) {
	notes := fixture(t)
	st := record(t)
	p := plan(t, notes, st)
	after := apply(t, notes, p)
	sum := text(after, "project-hive-what-s-true-now.md")
	for _, want := range []string{"consolidated: 2026-10-25", "## Blocked now", "The recursive gate", "waiting on the noise band", "## Merged lately", "Ship the release banner", "the gate rolled back fe55aed9"} {
		if !strings.Contains(sum, want) {
			t.Errorf("summary lacks %q:\n%s", want, sum)
		}
	}
	if learned := text(after, learnedPath); !strings.Contains(learned, "1× not better than the current Hive: nothing moved beyond the noise") {
		t.Errorf("practice note:\n%s", learned)
	}
	if p.Digest != "dreams/2026-10-25-0400.md" {
		t.Fatalf("digest %q", p.Digest)
	}
	d := text(after, p.Digest)
	for _, want := range []string{"title: What the Hive learned since the start", "**hive, 1 merged:** Ship the release banner", "rolled back by the gate", "the kb should dream", "Merged 3 notes into [[" + gate + "]]", "Retired [[blocked-ship-the-release-banner]]", "Linked", "project-hive-what-s-true-now.md"} {
		if !strings.Contains(d, want) {
			t.Errorf("digest lacks %q:\n%s", want, d)
		}
	}
	if !strings.Contains(d, "the banner looks right") || strings.Contains(d, "never seen created") {
		t.Errorf("agent chat lines: want the shared run's, not the unknown run's:\n%s", d)
	}
	for _, n := range after {
		if strings.Contains(n.Text, "Secret thing") || strings.Contains(n.Text, "vault") || strings.Contains(n.Text, "hunter2") {
			t.Errorf("%s names the isolated project", n.Path)
		}
	}

	st.Last = t0
	again := Plan(Input{Notes: after, Shipped: []string{"practice/risk.md"}, Hidden: []string{"vault"}, State: st, Now: t0.Add(4 * time.Hour), Trigger: "scheduled"})
	if len(again.Changes) != 0 || again.Digest != "" {
		t.Errorf("a pass with no news changed %+v", again.Changes)
	}
}

// The manifest runs the program with `go run` over its files, since the
// module's folder has no go.mod; it must name every one of them.
func TestManifestNamesEveryFile(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "module.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Exec     []string
		Hooks    []string
		Schedule map[string]string
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob("*.go")
	var want []string
	for _, f := range files {
		if !strings.HasSuffix(f, "_test.go") {
			want = append(want, "./dream/"+f)
		}
	}
	got := slices.Clone(m.Exec[2:])
	slices.Sort(got)
	slices.Sort(want)
	if len(m.Exec) < 3 || m.Exec[0] != "go" || m.Exec[1] != "run" || !slices.Equal(got, want) {
		t.Errorf("exec %v, want go run over %v", m.Exec, want)
	}
	if m.Schedule["kb.dream"] == "" || !slices.Contains(m.Hooks, "kb") {
		t.Errorf("manifest lacks the kb.dream schedule or the kb hook: %+v", m)
	}
}

// One title in two projects stays two notes.
func TestMergeKeepsProjectsApart(t *testing.T) {
	notes := []Shared{
		{Path: "flaky-test.md", Text: "---\ntitle: Flaky test\nproject: hive\ndate: 2026-10-24\n---\n\nhive's flake\n"},
		{Path: "flaky-test-2.md", Text: "---\ntitle: Flaky test\nproject: deck\ndate: 2026-10-24\n---\n\ndeck's flake\n"},
	}
	if p := plan(t, notes, newState()); len(p.Merged) != 0 {
		t.Fatalf("merged across projects: %+v", p.Merged)
	}
}
