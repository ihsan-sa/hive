package improve

import (
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

	// Drafted and readied on the forge, then merged there, and a login
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
