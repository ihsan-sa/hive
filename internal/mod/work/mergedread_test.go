package work

import (
	"strconv"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

// mergedRig is a change to a root path that merged on the forge with no
// security read, as #601 and #1082 did under the early-majority panel: its
// merge commit, own diff and root paths as effect.merged records them.
func mergedRig(t *testing.T) (m *Module, p core.Project, task *Task, mergeSHA, diff string, root []string) {
	t.Helper()
	list := map[string]string{core.SecurityList: "paths = [\"secure/**\"]\nroot = [\"ops/**\"]\n"}
	m, task, p, f := gateRigSeeded(t, false, list, map[string]string{"ops/deploy.sh": "echo hi\n"})
	main, _ := m.C.Effects.Sync(p)
	ch, err := m.C.Effects.Classify(p, main, task.SHA)
	if err != nil {
		t.Fatal(err)
	}
	mergeSHA, err = f.MergePR(p.Repo, task.PR.Number, task.SHA, "work")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.C.Effects.Sync(p); err != nil {
		t.Fatal(err)
	}
	return m, p, task, mergeSHA, ch.DiffID, ch.SecurityRoot
}

// mergedSays ends a seat's run on a merged change with a verdict.
func mergedSays(t *testing.T, m *Module, id, verdict string) {
	t.Helper()
	if r, ok := m.C.Runs.Get(id); !ok || !r.ReadOnly || r.Role != "reviewer" {
		t.Fatalf("no read-only reviewer run %s: %+v", id, r)
	}
	m.C.J.MustAppend("run.turn_ended", map[string]any{"id": id, "turn": 1, "status": core.StatusDone,
		"result": harness.Result{Structured: []byte(`{"verdict":"` + verdict + `","summary":"` + id + ` says ` + verdict + `","token":"` + reviewToken(id) + `"}`)}})
}

// A merged root change with no security read gets one from the Hive's own
// seats, on the merge's exact own diff: the security seat first, then the
// whole panel, and it passes once they land, with no word from the person.
func TestReadMergedRootPasses(t *testing.T) {
	m, p, task, mergeSHA, diff, root := mergedRig(t)
	if len(root) == 0 {
		t.Fatal("the rig's change touches no root path")
	}
	passed, why := m.ReadMerged(p, task.PR.Number, task.SHA, mergeSHA, diff, root)
	if passed || why != "" {
		t.Fatalf("passed %v, why %q before any seat read", passed, why)
	}
	id := func(seat string) string {
		return "m-p-" + strconv.Itoa(task.PR.Number) + "-" + core.Short(diff) + "-" + seat + "-0"
	}
	r, _ := m.C.Runs.Get(id(core.SeatSecurity))
	if r.Inputs["seat"] != core.SeatSecurity || r.Inputs["sha"] != task.SHA || core.DiffID(r.Inputs["diff.patch"]) != diff || !strings.Contains(r.Inputs["brief.md"], "ops/deploy.sh") {
		t.Fatalf("the security seat doesn't read the merge's own diff: %+v", r.Inputs)
	}
	mergedSays(t, m, id(core.SeatSecurity), "land")
	if passed, why = m.ReadMerged(p, task.PR.Number, task.SHA, mergeSHA, diff, root); passed || why != "" {
		t.Fatalf("passed %v, why %q on the seat alone", passed, why)
	}
	for i := 0; i < core.PanelSeats; i++ {
		mergedSays(t, m, id(core.PanelSeat(i)), "land")
	}
	if passed, why = m.ReadMerged(p, task.PR.Number, task.SHA, mergeSHA, diff, root); !passed || why != "" {
		t.Fatalf("passed %v, why %q once the seat and panel landed", passed, why)
	}
	if !m.C.Effects.SecurityOK(p.Name, core.Change{Security: root, SecurityRoot: root, DiffID: diff}) {
		t.Fatal("the seats' reads aren't bound to the merge's diff")
	}
}

// A seat's fix leaves the release to the person, and says why; so does a
// diff the merge record names but the clone can't rebuild.
func TestReadMergedFixOrWrongDiff(t *testing.T) {
	m, p, task, mergeSHA, diff, root := mergedRig(t)
	m.ReadMerged(p, task.PR.Number, task.SHA, mergeSHA, diff, root)
	mergedSays(t, m, "m-p-"+strconv.Itoa(task.PR.Number)+"-"+core.Short(diff)+"-security-0", "fix")
	if passed, why := m.ReadMerged(p, task.PR.Number, task.SHA, mergeSHA, diff, root); passed || !strings.Contains(why, "asked for fixes") {
		t.Fatalf("passed %v, why %q after the seat's fix", passed, why)
	}
	if passed, why := m.ReadMerged(p, task.PR.Number, task.SHA, mergeSHA, "0123456789abcdef", root); passed || !strings.Contains(why, "rebuilt") {
		t.Fatalf("passed %v, why %q on a diff the merge doesn't have", passed, why)
	}
}

// A merge with no ordinary read gets one reviewer's read of its own diff.
func TestReadMergedUnread(t *testing.T) {
	m, p, task, mergeSHA, diff, _ := mergedRig(t)
	if passed, why := m.ReadMerged(p, task.PR.Number, task.SHA, mergeSHA, diff, nil); passed || why != "" {
		t.Fatalf("passed %v, why %q before the read", passed, why)
	}
	id := "m-p-" + strconv.Itoa(task.PR.Number) + "-" + core.Short(diff) + "-read-0"
	if r, _ := m.C.Runs.Get(id); r.Inputs["seat"] != "" {
		t.Fatalf("an ordinary read sits on a security seat: %+v", r.Inputs)
	}
	mergedSays(t, m, id, "land")
	if passed, why := m.ReadMerged(p, task.PR.Number, task.SHA, mergeSHA, diff, nil); !passed || why != "" {
		t.Fatalf("passed %v, why %q after the reviewer's land", passed, why)
	}
}

// A panel seat's fix is one vote of three: two lands and a fix pass the
// read, whichever seat ends last; a majority of fixes holds it.
func TestReadMergedPanelMajority(t *testing.T) {
	for name, votes := range map[string][]string{"fix last": {"land", "land", "fix"}, "fix first": {"fix", "land", "land"}, "two fixes": {"fix", "land", "fix"}} {
		t.Run(name, func(t *testing.T) {
			m, p, task, mergeSHA, diff, root := mergedRig(t)
			id := func(seat string) string {
				return "m-p-" + strconv.Itoa(task.PR.Number) + "-" + core.Short(diff) + "-" + seat + "-0"
			}
			m.ReadMerged(p, task.PR.Number, task.SHA, mergeSHA, diff, root)
			mergedSays(t, m, id(core.SeatSecurity), "land")
			m.ReadMerged(p, task.PR.Number, task.SHA, mergeSHA, diff, root)
			for i, v := range votes {
				mergedSays(t, m, id(core.PanelSeat(i)), v)
			}
			passed, why := m.ReadMerged(p, task.PR.Number, task.SHA, mergeSHA, diff, root)
			if name == "two fixes" {
				if passed || !strings.Contains(why, "panel asked for fixes (2 of 3)") {
					t.Fatalf("passed %v, why %q on two fixes of three", passed, why)
				}
				return
			}
			if !passed || why != "" {
				t.Fatalf("passed %v, why %q on two lands of three", passed, why)
			}
			n := 0
			m.C.J.Replay(func(e core.Event) {
				if e.Kind == "effect.verdict" {
					n++
				}
			})
			m.ReadMerged(p, task.PR.Number, task.SHA, mergeSHA, diff, root)
			m.C.J.Replay(func(e core.Event) {
				if e.Kind == "effect.verdict" {
					n--
				}
			})
			if n != 0 {
				t.Fatalf("a later call recorded %d verdicts again", -n)
			}
		})
	}
}
