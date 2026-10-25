package projects

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// stewardHive is a Hive whose release repository local/hive is the person's
// own (the local forge's user is "local"), with files on its main.
func stewardHive(t *testing.T, files map[string]string) (*Module, *core.Local) {
	t.Helper()
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "gc.auto")
	t.Setenv("GIT_CONFIG_VALUE_0", "0")
	dir := t.TempDir()
	ws := core.Workspace{Root: filepath.Join(dir, "hive")}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	cfg := ws.LoadConfig()
	cfg.ReleaseRepo = "local/hive"
	ws.SaveConfig(cfg)
	forge := &core.Local{Dir: filepath.Join(dir, "forge")}
	c, err := core.Open(ws, forge)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if _, err := forge.EnsureRepo("local/hive", "", false); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	git := func(dir string, args ...string) {
		if _, err := core.Git(dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	git("", "clone", "-q", forge.RepoPath("local/hive"), tmp)
	files[".hive/goals.md"] = "# Goals\n"
	for p, body := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(tmp, p)), 0o755)
		os.WriteFile(filepath.Join(tmp, p), []byte(body), 0o644)
	}
	git(tmp, "add", "-A")
	git(tmp, "commit", "-q", "-m", "seed")
	git(tmp, "push", "-q", "origin", "HEAD:main")
	a := &agents.Agents{C: c}
	ch := chat.New(c, a)
	return &Module{C: c, Chat: ch, Work: work.New(c, a, ch), retry: map[string]time.Time{}, lastErr: map[string]string{}, claimed: map[string]bool{}}, forge
}

// The steward the record names works in the release repository itself.
func TestStewardWorksInTheReleaseRepo(t *testing.T) {
	m2, _ := stewardHive(t, map[string]string{core.StewardFile: core.StewardRecord("H0")})
	os.WriteFile(filepath.Join(m2.C.WS.Hive(), "hive-id"), []byte("H0\n"), 0o644)
	p, _, err := m2.Create(Spec{Self: true})
	if err != nil || p.Repo != "local/hive" {
		t.Fatalf("the steward should work in its release repo: %+v %v", p, err)
	}
	if why := m2.C.Effects.Stewardship(p, "refs/remotes/origin/main"); why != "" {
		t.Fatalf("the steward is refused: %s", why)
	}
	m2.Tick()
	if n := len(m2.Work.List(func(w work.Task) bool { return strings.Contains(w.Title, "steward") })); n != 0 {
		t.Fatalf("a recorded steward claims again: %d briefs", n)
	}
}

// Another Hive of the same person is refused the release repository, with
// what to do; once the person names a fork owner it works in that fork; and
// its merges in the release repository are refused even when it has a
// project there.
func TestSecondHiveIsRefusedOrForked(t *testing.T) {
	m, forge := stewardHive(t, map[string]string{core.StewardFile: core.StewardRecord("H0")})
	_, _, err := m.Create(Spec{Self: true})
	if err == nil || !strings.Contains(err.Error(), "steward is the Hive H0") || !strings.Contains(err.Error(), "self_fork") {
		t.Fatalf("a second Hive should be refused the upstream with what to do: %v", err)
	}
	// The person's word on an empty record doesn't override one naming H0.
	if _, _, err := m.Create(Spec{Self: true, Steward: true}); err == nil {
		t.Fatal("a second Hive took over a recorded steward")
	}
	if _, _, err := m.Create(Spec{Self: true, Repo: "local/hive"}); err == nil {
		t.Fatal("naming the upstream itself skipped the steward record")
	}
	if len(m.C.Projects.List()) != 0 {
		t.Fatalf("a refused create left a project: %+v", m.C.Projects.List())
	}
	cfg := m.C.WS.LoadConfig()
	cfg.SelfFork = "Hive-RSI-Lab"
	m.C.WS.SaveConfig(cfg)
	p, _, err := m.Create(Spec{Self: true})
	if err != nil || p.Repo != "Hive-RSI-Lab/hive" {
		t.Fatalf("with a fork owner named, the Hive works in that fork: %+v %v", p, err)
	}
	if empty, _ := forge.IsEmpty("Hive-RSI-Lab/hive"); empty {
		t.Fatal("no fork was made")
	}
	if why := m.C.Effects.Stewardship(p, "refs/remotes/origin/main"); why != "" {
		t.Fatalf("a fork is refused: %s", why)
	}

	// A project in the upstream from before the record (as H0's was) is
	// refused once the record names another Hive.
	up, err := m.C.Projects.Add(core.Project{Name: "up", Repo: "local/hive", Self: true, Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	main, err := m.C.Effects.Sync(up)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.C.Effects.Merge(core.MergeRequest{Project: up, PR: 1, SHA: main, Main: main}); err == nil || !strings.Contains(err.Error(), "steward is the Hive H0") {
		t.Fatalf("a non-steward's merge in the upstream: %v", err)
	}
}

// With no steward recorded, a Hive works in the upstream only by the
// person's word, and then files the change that records it, once.
func TestUnclaimedUpstreamNeedsThePersonsWord(t *testing.T) {
	m, _ := stewardHive(t, map[string]string{})
	if _, _, err := m.Create(Spec{Self: true}); err == nil || !strings.Contains(err.Error(), "records no steward yet") {
		t.Fatalf("an unclaimed upstream without the person's word: %v", err)
	}
	p, _, err := m.Create(Spec{Self: true, Steward: true})
	if err != nil || p.Repo != "local/hive" {
		t.Fatalf("the person's word: %+v %v", p, err)
	}
	if again, _, err := m.Create(Spec{Self: true}); err != nil || again.Name != p.Name {
		t.Fatalf("asking again for the project already in the upstream: %+v %v", again, err)
	}
	m.Tick()
	m.claimed = map[string]bool{} // a restart
	m.Tick()
	briefs := m.Work.List(func(w work.Task) bool { return strings.Contains(w.Title, "steward") })
	if len(briefs) != 1 || !strings.Contains(briefs[0].Done, m.C.WS.HiveID()) {
		t.Fatalf("one brief recording this Hive as steward: %+v", briefs)
	}
}

// The steward works in its release repository even with a fork owner named,
// and a record it can't read at a commit refuses rather than allows.
func TestStewardWinsOverSelfForkAndUnreadableRefuses(t *testing.T) {
	m, _ := stewardHive(t, map[string]string{core.StewardFile: core.StewardRecord("H0")})
	os.WriteFile(filepath.Join(m.C.WS.Hive(), "hive-id"), []byte("H0\n"), 0o644)
	cfg := m.C.WS.LoadConfig()
	cfg.SelfFork = "Hive-RSI-Lab"
	m.C.WS.SaveConfig(cfg)
	p, _, err := m.Create(Spec{Self: true})
	if err != nil || p.Repo != "local/hive" {
		t.Fatalf("the steward forked instead of working in its release repo: %+v %v", p, err)
	}
	if why := m.C.Effects.Stewardship(core.Project{Name: "no-clone", Repo: p.Repo, Branch: "main"}, "refs/remotes/origin/main"); !strings.Contains(why, "can't read") {
		t.Fatalf("an unreadable record should refuse: %q", why)
	}
	// Any project in the release repository is checked, not only the self one.
	other := core.Project{Name: p.Name, Repo: p.Repo}
	os.WriteFile(filepath.Join(m.C.WS.Hive(), "hive-id"), []byte("H1\n"), 0o644)
	if why := m.C.Effects.Stewardship(other, "refs/remotes/origin/main"); !strings.Contains(why, "steward is the Hive H0") {
		t.Fatalf("a non-self project in the release repo skipped the record: %q", why)
	}
}

// A claim filed before the person renamed .hive/hive-id isn't filed again
// under the new name while it is open: the person is told to stop it.
func TestRenamedHiveDoesNotClaimTwice(t *testing.T) {
	m, _ := stewardHive(t, map[string]string{})
	if _, _, err := m.Create(Spec{Self: true, Steward: true}); err != nil {
		t.Fatal(err)
	}
	m.Tick()
	os.WriteFile(filepath.Join(m.C.WS.Hive(), "hive-id"), []byte("H0\n"), 0o644)
	m.claimed = map[string]bool{}
	m.Tick()
	briefs := m.Work.List(func(w work.Task) bool { return strings.Contains(w.Title, "steward") })
	if len(briefs) != 1 {
		t.Fatalf("a rename filed a second claim: %+v", briefs)
	}
	if err := m.Work.Stop(briefs[0].ID, "renamed"); err != nil {
		t.Fatal(err)
	}
	m.claimed = map[string]bool{}
	m.Tick()
	briefs = m.Work.List(func(w work.Task) bool { return strings.Contains(w.Title, "(H0)") })
	if len(briefs) != 1 {
		t.Fatalf("once the old claim is stopped, the claim under the new name: %+v", briefs)
	}
}
