package improve

import (
	"errors"
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

func handsModule(t *testing.T) (*core.Core, *Module) {
	t.Helper()
	dir := t.TempDir()
	c, err := core.Open(core.Workspace{Root: filepath.Join(dir, "hive")}, &core.Local{Dir: filepath.Join(dir, "forge")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if _, err := c.Projects.Add(core.Project{Name: "hive", Repo: "me/hive", Self: true}); err != nil {
		t.Fatal(err)
	}
	a := &agents.Agents{C: c}
	ch := chat.New(c, a)
	return c, New(c, a, work.New(c, a, ch), ch)
}

func say(c *core.Core, chat, from, text string) {
	id := core.NewID()
	c.J.MustAppend("chat.posted", map[string]string{"chat": chat, "id": id, "thread": id, "from": from, "name": from, "text": text})
}

func kinds(h Hands) string {
	var k []string
	for _, x := range h.Hands {
		k = append(k, x.Kind)
	}
	return strings.Join(k, ",")
}

// Every kind of outside hand is counted once against the merge it took, each
// linked to its journal seq; what the Hive did itself, or what names another
// change, is not.
func TestHandsCountEachKind(t *testing.T) {
	c, m := handsModule(t)
	task := work.Task{ID: "t-one", Project: "hive", Title: "one", From: "lead-1", Stage: work.StageWorking, Worker: "w-1", PR: core.PR{Number: 7, URL: "https://x/me/hive/pull/7"}, Issue: core.Issue{Number: 3}}
	c.J.MustAppend("task.saved", task)
	other := work.Task{ID: "t-two", Project: "hive", Title: "two", From: "lead-1", Stage: work.StageWorking, Worker: "w-2", PR: core.PR{Number: 8}}
	c.J.MustAppend("task.saved", other)

	// The person answers a merge ask about it, and a step its worker asked.
	merge := c.Person.Ask(core.Ask{Kind: "merge", Ref: "t-one@abc", Project: "hive", Title: "Merge one?", Options: []string{"merge", "leave open"}})
	if _, err := c.Person.AnswerAs("person", merge.ID, "merge", ""); err != nil {
		t.Fatal(err)
	}
	step := c.Person.Ask(core.Ask{Kind: "request", Ref: "req-1", From: "w-1", Project: "hive", Title: "Always yours: install a thing", Options: []string{"done", "declined"}})
	c.Person.AnswerAs("person", step.ID, "done", "")
	// Not its hands: the Hive's own answer, an old answer whose note says the
	// Hive's, the other task's step, a login ask (the renewal counts instead).
	own := c.Person.Ask(core.Ask{Kind: "question", Ref: "t-one", Project: "hive", Title: "ok?", Options: []string{"ok"}})
	c.Person.Answer(own.ID, "ok", "the Hive: moved")
	old := c.Person.Ask(core.Ask{Kind: "question", Ref: "t-one-x", Project: "hive", Title: "old", Options: []string{"ok"}})
	c.J.MustAppend("person.answered", core.Ask{ID: old.ID, Answer: "ok", Note: "the Hive, not the person: superseded"})
	theirs := c.Person.Ask(core.Ask{Kind: "request", Ref: "req-2", From: "w-2", Project: "hive", Title: "step two", Options: []string{"done", "declined"}})
	c.Person.AnswerAs("person", theirs.ID, "done", "")
	login := c.Person.Ask(core.Ask{Kind: "login", Ref: "claude", Title: "login", Options: []string{"done"}})
	c.Person.AnswerAs("person", login.ID, "done", "")

	// The person (or autobox, as them) names its PR; other posts don't count.
	say(c, "project:hive", "person", "autobox: perms read on #7 is land")
	say(c, "project:other", "person", "#7 over there is another repo's")
	say(c, "project:hive", "person", "#70 and #8 are not it")
	say(c, "project:hive", "hive", "Merged: one https://x/me/hive/pull/7")
	say(c, "project:hive", "improve", "about t-one")

	// Drafted and readied on the forge, then ended as landed (the lead's
	// landed op: on main with no merge of the Hive's), and a login
	// renewed by hand meanwhile.
	task.DraftSince = time.Now()
	c.J.MustAppend("task.saved", task)
	task.DraftSince = time.Time{}
	c.J.MustAppend("task.saved", task)
	c.J.MustAppend("login.renewed", map[string]any{"login": "claude", "by": "hand"})
	c.J.MustAppend("login.renewed", map[string]any{"login": "claude"}) // renewed itself
	task.Stage, task.MergeSHA = work.StageMerged, "m-one"
	c.J.MustAppend("task.saved", task)

	h := m.HandsFor("t-one", "m-one", c.J.Seq())
	if got, want := kinds(h), "answer,step,chat,draft,ready,login,merge"; got != want || h.Count != 7 || h.Unassisted {
		t.Fatalf("kinds %q, want %q (count %d): %+v", got, want, h.Count, h)
	}
	seen := map[int64]bool{}
	for _, x := range h.Hands {
		if x.Seq == 0 || seen[x.Seq] || x.What == "" {
			t.Fatalf("each hand names its own journal seq: %+v", h.Hands)
		}
		seen[x.Seq] = true
	}

	// Closed on the forge.
	other.Stage, other.ByPerson, other.Reason = work.StageStopped, true, "closed on the forge without merging (PR #8)"
	c.J.MustAppend("task.saved", other)
	if h := m.HandsFor("t-two", "", c.J.Seq()); !strings.Contains(kinds(h), "close") {
		t.Fatalf("a PR closed outside the Hive isn't counted: %+v", h)
	}

	// A change filed after all that, merged by the Hive with nobody in
	// between, counts none.
	three := work.Task{ID: "t-three", Project: "hive", Title: "three", From: "sweep-1", Stage: work.StageWorking, PR: core.PR{Number: 9}}
	c.J.MustAppend("task.saved", three)
	c.J.MustAppend("effect.merged", map[string]any{"project": "hive", "pr": 9, "sha": "h-three", "merge_sha": "m-three"})
	three.Stage, three.MergeSHA = work.StageMerged, "m-three"
	c.J.MustAppend("task.saved", three)
	if h := m.HandsFor("t-three", "m-three", c.J.Seq()); !h.Unassisted || h.Count != 0 {
		t.Fatalf("an unassisted merge counts %+v", h)
	}
}

// A release counts the release asks the person answered since the last one,
// once; a release H0 updated to that the Hive didn't cut is one by hand. The
// milestone reads all five steps from the record.
func TestReleaseHandsAndMilestone(t *testing.T) {
	c, m := handsModule(t)
	ask := c.Person.Ask(core.Ask{Kind: "release", Ref: "hive@s1", Project: "hive", Title: "Release?", Options: []string{"release", "hold"}})
	c.Person.AnswerAs("person", ask.ID, "release", "")
	c.J.MustAppend("hive.released", map[string]any{"project": "hive", "tag": "v0.1.1", "sha": "s1"})
	c.J.MustAppend("hive.updated", map[string]any{"from": "v0.1.1", "to": "v0.1.2"}) // never cut by the Hive

	// A change the Hive proposed, kept, merged with no hand, then released
	// and installed by the Hive alone.
	task := work.Task{ID: "t-sw", Project: "hive", Title: "from a sweep", From: "sweep-1", Stage: work.StageMerged, MergeSHA: "s2"}
	c.J.MustAppend("task.saved", task)
	c.J.MustAppend("effect.merged", map[string]any{"project": "hive", "merge_sha": "s2"})
	hs := m.HandsFor(task.ID, "s2", c.J.Seq())
	best := 1.0
	c.J.MustAppend("score.measured", Measurement{Project: "hive", SHA: "s2", Task: task.ID, TestsOK: true, Kept: true, Best: &best, Outside: &hs, At: time.Now().UTC()})
	if ml := m.Milestone("hive"); ml.Steps != 3 || ml.Met() || !strings.HasPrefix(ml.Line, "milestone 3/5") {
		t.Fatalf("before its release: %+v", ml)
	}
	time.Sleep(10 * time.Millisecond)
	c.J.MustAppend("hive.released", map[string]any{"project": "hive", "tag": "v0.1.3", "sha": "s2"})
	c.J.MustAppend("hive.updated", map[string]any{"from": "v0.1.2", "to": "v0.1.3"})

	m.CountReleases()
	m.CountReleases()
	n := 0
	for _, e := range c.J.Since(0) {
		if e.Kind == "release.counted" {
			n++
		}
	}
	rs := m.Releases("hive")
	if n != 3 || len(rs) != 3 {
		t.Fatalf("each release is counted once: %d events, %+v", n, rs)
	}
	if r := rs[0]; r.Tag != "v0.1.1" || r.Outside.Count != 1 || r.Outside.Hands[0].Kind != "answer" || r.Outside.Unassisted {
		t.Fatalf("the person released v0.1.1: %+v", r)
	}
	if r := rs[1]; r.Tag != "v0.1.2" || !r.ByHand || r.Outside.Count != 1 || r.Outside.Hands[0].Kind != "release" {
		t.Fatalf("v0.1.2 was released by hand: %+v", r)
	}
	if r := rs[2]; r.Tag != "v0.1.3" || !r.Outside.Unassisted {
		t.Fatalf("v0.1.3 was released unassisted: %+v", r)
	}
	ml := m.Milestone("hive")
	if !ml.Met() || ml.Release != "v0.1.3" || !strings.HasPrefix(ml.Line, "milestone 5/5") {
		t.Fatalf("milestone: %+v", ml)
	}
	d := m.Digest(0, "")
	if !strings.Contains(d, "Outside hands ('nobody in between')") || !strings.Contains(d, "1 merges, 0 outside actions (0.0 per merge), 1 unassisted") || !strings.Contains(d, "milestone 5/5") {
		t.Fatalf("digest lacks the trend:\n%s", d)
	}
}

// A brief the lead filed in a thread the person started is the person's
// proposal, not the Hive's; an update by hand doesn't count as H0 updated.
func TestMilestoneProposedAndUpdatedByHand(t *testing.T) {
	c, m := handsModule(t)
	c.J.MustAppend("chat.posted", map[string]string{"chat": "lead", "id": "th1", "thread": "th1", "from": "person", "name": "you", "text": "please do x"})
	task := work.Task{ID: "t-asked", Project: "hive", Title: "x", From: "lead-1", Thread: "th1", Stage: work.StageMerged, MergeSHA: "s1"}
	c.J.MustAppend("task.saved", task)
	c.J.MustAppend("effect.merged", map[string]any{"project": "hive", "merge_sha": "s1"})
	hs := m.HandsFor(task.ID, "s1", c.J.Seq())
	c.J.MustAppend("score.measured", Measurement{Project: "hive", SHA: "s1", Task: task.ID, TestsOK: true, Kept: true, Outside: &hs, At: time.Now().UTC()})
	if ml := m.Milestone("hive"); ml.Proposed || ml.Steps != 0 {
		t.Fatalf("the person's ask isn't the Hive's proposal: %+v", ml)
	}
	m.mu.Lock()
	lead := m.proposed(work.Task{From: "lead-1"})
	m.mu.Unlock()
	if !lead {
		t.Fatal("a brief the lead filed on its own is the Hive's")
	}
	c.J.MustAppend("hive.released", map[string]any{"project": "hive", "tag": "v0.2.0", "sha": "s9"})
	c.J.MustAppend("hive.updated", map[string]any{"to": "v0.2.0", "by": "hand"})
	m.CountReleases()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.hands.updates) != 1 || m.hands.updates[0].by != "hand" {
		t.Fatalf("update by hand: %+v", m.hands.updates)
	}
}

// The real merge path: a PR the person merged on the forge before the Hive's
// merge ran is recorded by Effects.Merge as already merged, and counts as a
// merge by hand; one the Hive merges itself doesn't.
func TestMergeOnTheForgeIsAHand(t *testing.T) {
	for k, v := range map[string]string{"GIT_CONFIG_COUNT": "2", "GIT_CONFIG_KEY_0": "gc.auto", "GIT_CONFIG_VALUE_0": "0", "GIT_CONFIG_KEY_1": "maintenance.auto", "GIT_CONFIG_VALUE_1": "false"} {
		t.Setenv(k, v)
	}
	dir := t.TempDir()
	ws := core.Workspace{Root: filepath.Join(dir, "hive")}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	cfg := ws.LoadConfig()
	cfg.Sandbox, cfg.BareAccepted, cfg.Harness = "bare", time.Now().Format(time.RFC3339), "fake"
	cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax, cfg.LoadMax = 0, 0, 0, 0
	ws.SaveConfig(cfg)
	f := &core.Local{Dir: filepath.Join(dir, "forge")}
	c, err := core.Open(ws, f)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	a := &agents.Agents{C: c}
	ch := chat.New(c, a)
	m := New(c, a, work.New(c, a, ch), ch)

	git := func(dir string, args ...string) {
		t.Helper()
		if _, err := core.Git(dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.EnsureRepo("me/demo", ""); err != nil {
		t.Fatal(err)
	}
	seed := t.TempDir()
	git("", "clone", "-q", f.RepoPath("me/demo"), seed)
	os.MkdirAll(filepath.Join(seed, ".hive"), 0o755)
	os.WriteFile(filepath.Join(seed, ".hive", "project.json"), []byte(`{"test": "true"}`), 0o644)
	os.WriteFile(filepath.Join(seed, "README.md"), []byte("# demo\n"), 0o644)
	git(seed, "add", "-A")
	git(seed, "commit", "-q", "-m", "seed")
	git(seed, "push", "-q", "origin", "HEAD:main")
	p, err := c.Projects.Add(core.Project{Name: "demo", Repo: "me/demo"})
	if err != nil {
		t.Fatal(err)
	}

	// land opens a PR for a one-file change and merges it through
	// Effects.Merge, after the person merged it on the forge when byHand.
	land := func(id string, n int, before func(pr int, sha string)) string {
		t.Helper()
		main, err := c.Effects.Sync(p)
		if err != nil {
			t.Fatal(err)
		}
		co, err := c.Effects.NewCheckout(p, id, main)
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(co, id+".md"), []byte("# "+id+"\n"), 0o644)
		git(co, "add", "-A")
		git(co, "commit", "-q", "-m", id)
		r, err := c.Runs.Start(core.RunSpec{ID: "w-" + id, Role: "worker", Project: p.Name, Checkout: co, Harness: "fake"})
		if err != nil {
			t.Fatal(err)
		}
		sha, err := c.Effects.Extract(p, r, main)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Effects.Push(p, "hive/"+id, sha); err != nil {
			t.Fatal(err)
		}
		pr, err := f.CreatePR(p.Repo, "hive/"+id, "main", id, "body")
		if err != nil || pr.Number != n {
			t.Fatalf("pr %+v: %v", pr, err)
		}
		task := work.Task{ID: id, Project: p.Name, Title: id, From: "lead-1", Stage: work.StageMerging, Worker: r.ID, SHA: sha, PR: pr}
		c.J.MustAppend("task.saved", task)
		if ck := c.Effects.RunCheck(p, sha, "test", "true"); !ck.OK {
			t.Fatalf("test: %+v", ck)
		}
		f.SetChecks(p.Repo, sha, core.CheckRun{Name: "test", State: core.CheckPass}, core.CheckRun{Name: "sandboxed-e2e", State: core.CheckPass})
		if before != nil {
			before(pr.Number, sha)
		}
		merged, err := c.Effects.Merge(core.MergeRequest{Project: p, PR: pr.Number, SHA: sha, Main: main, Subject: id})
		if err != nil {
			t.Fatal(err)
		}
		task.Stage, task.MergeSHA = work.StageMerged, merged
		c.J.MustAppend("task.saved", task)
		return merged
	}
	byHand := func(pr int, sha string) {
		if _, err := f.MergePR(p.Repo, pr, sha, "by hand"); err != nil {
			t.Fatal(err)
		}
	}
	own := land("t-own", 1, nil)
	if h := m.HandsFor("t-own", own, c.J.Seq()); !h.Unassisted {
		t.Fatalf("the Hive's own merge counts %+v", h)
	}
	hand := land("t-hand", 2, byHand)
	if h := m.HandsFor("t-hand", hand, c.J.Seq()); kinds(h) != "merge" || h.Unassisted {
		t.Fatalf("a merge on the forge counts %+v", h)
	}
	// The Hive's merge is refused (twice, journalled once), the person merges
	// it on the forge, and the Hive's next try finds it merged: by hand.
	refused := land("t-refused", 3, func(pr int, sha string) {
		main, _ := c.Effects.Sync(p)
		c.Effects.Forge = refusing{f}
		for i := 0; i < 2; i++ {
			if _, err := c.Effects.Merge(core.MergeRequest{Project: p, PR: pr, SHA: sha, Main: main, Subject: "x"}); err == nil {
				t.Fatal("the refusing forge merged")
			}
		}
		c.Effects.Forge = f
		byHand(pr, sha)
	})
	if h := m.HandsFor("t-refused", refused, c.J.Seq()); kinds(h) != "merge" {
		t.Fatalf("a hand merge after a refused try counts %+v", h)
	}
	n := 0
	for _, e := range c.J.Since(0) {
		if e.Kind == "effect.merge_failed" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("a retry that fails the same way is journalled again: %d failures", n)
	}
	// The Hive's own try got through on the forge and the daemon stopped
	// before it recorded the merge: the next try finds it merged, its own.
	restarted := land("t-restart", 4, func(pr int, sha string) {
		c.J.MustAppend("effect.merging", map[string]any{"project": p.Name, "pr": pr, "sha": sha})
		byHand(pr, sha)
	})
	if h := m.HandsFor("t-restart", restarted, c.J.Seq()); !h.Unassisted {
		t.Fatalf("the Hive's own merge cut short by a restart counts %+v", h)
	}
}

// A hand that names no change (a login renewed by hand) counts once, for the
// next merge after it, not for every change in flight.
func TestUnlinkedHandCountsOnce(t *testing.T) {
	c, m := handsModule(t)
	a := work.Task{ID: "t-a", Project: "hive", Title: "a", From: "lead-1", Stage: work.StageWorking}
	b := work.Task{ID: "t-b", Project: "hive", Title: "b", From: "lead-1", Stage: work.StageWorking}
	c.J.MustAppend("task.saved", a)
	c.J.MustAppend("task.saved", b)
	c.J.MustAppend("login.renewed", map[string]any{"login": "claude", "by": "hand"})
	for _, x := range []*work.Task{&a, &b} {
		c.J.MustAppend("effect.merged", map[string]any{"project": "hive", "merge_sha": "m-" + x.ID})
		x.Stage, x.MergeSHA = work.StageMerged, "m-"+x.ID
		c.J.MustAppend("task.saved", *x)
	}
	ha, hb := m.HandsFor("t-a", "m-t-a", c.J.Seq()), m.HandsFor("t-b", "m-t-b", c.J.Seq())
	if kinds(ha) != "login" || !hb.Unassisted {
		t.Fatalf("the login counts for the next merge only: a %+v, b %+v", ha, hb)
	}
}

// refusing is a forge whose branch rules refuse every merge.
type refusing struct{ *core.Local }

func (refusing) MergePR(repo string, number int, sha, subject string) (string, error) {
	return "", errors.New(`gh pr merge: GraphQL: Required status check "test" is failing. (mergePullRequest)`)
}
