package work

// Agents meet where their work overlaps. Every worker-to-worker message the
// Hive's record holds came from a shared object in flight: the same file,
// function or test, or a scarce slot (docs/research/collaboration.md). Telling
// agents to talk sent none. So the Hive puts the other party in front of the
// agent in what it already reads: the brief, a message when a neighbour
// starts, the conflict that main moving made, and the reply to finish.

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// neighbour is a working task whose brief names files another's names too.
type neighbour struct {
	Task  Task
	Files []string
}

// briefFiles are the files t's brief names that are in its project's clone.
// The person's files (briefs cite the goals as evidence) and union-merged
// docs (each side's lines are kept) are left out: two runs naming them
// share nothing to settle.
func (m *Module) briefFiles(t Task) []string {
	root := m.C.WS.Project(t.Project)
	union := unionRules(root)
	var out []string
	for _, p := range pathLike.FindAllString(t.Title+"\n"+t.Done+"\n"+t.Known, -1) {
		p = path.Clean(strings.TrimRight(strings.TrimPrefix(p, "./"), "."))
		if p == ".." || strings.HasPrefix(p, "../") || p == ".hive/goals.md" || p == ".hive/project.json" || isUnion(union, p) || slices.Contains(out, p) {
			continue
		}
		if fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p))); err == nil && fi.Mode().IsRegular() {
			out = append(out, p)
		}
	}
	return out
}

// neighbours are the tasks working now on t's project, quiet ones aside,
// whose briefs name a file t's brief names.
func (m *Module) neighbours(t Task) []neighbour {
	mine := m.briefFiles(t)
	if len(mine) == 0 {
		return nil
	}
	var out []neighbour
	for _, o := range m.List(func(o Task) bool {
		return o.ID != t.ID && o.Project == t.Project && o.Stage == StageWorking && o.Worker != "" && o.Worker != t.Worker
	}) {
		if m.quiet(o) {
			continue // a message would wake it for nothing
		}
		theirs := m.briefFiles(o)
		var both []string
		for _, f := range mine {
			if slices.Contains(theirs, f) {
				both = append(both, f)
			}
		}
		if len(both) > 0 {
			out = append(out, neighbour{o, both})
		}
	}
	slices.SortFunc(out, func(a, b neighbour) int { return strings.Compare(a.Task.Worker, b.Task.Worker) })
	return out
}

// codeList is a list of paths as code: "`a.go`, `b.go`".
func codeList(fs []string) string {
	if len(fs) > 6 {
		fs = append(fs[:6:6], fmt.Sprintf("%d more", len(fs)-6))
	}
	return "`" + strings.Join(fs, "`, `") + "`"
}

// besideText is the brief's section naming the runs on its files.
func besideText(nb []neighbour) string {
	if len(nb) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Working beside you\n\n")
	for _, n := range nb {
		fmt.Fprintf(&b, "- %s is on %q (task %s) and its brief names %s too.\n", n.Task.Worker, n.Task.Label(), n.Task.ID, codeList(n.Files))
	}
	b.WriteString("\nYou start side by side, and whoever merges second merges main. Tell them what you change in those files (`hive agent message --to <run> --text \"...\"`), and ask what they change.\n\n")
	return b.String()
}

// meet tells the runs on t's files that its worker started, and tells t's
// worker of any its brief didn't name (one started in the same moment),
// once the task is saved as working. meeting makes two that start together
// meet: the later one sees the earlier as working. The meeting is journaled
// (work.met) so peer talk can be counted per pair that met (stats).
func (m *Module) meet(t *Task, brief string) {
	m.meeting.Lock()
	defer m.meeting.Unlock()
	nb := m.neighbours(*t)
	if len(nb) == 0 {
		return
	}
	type side struct {
		Task  string   `json:"task"`
		Run   string   `json:"run"`
		Files []string `json:"files"`
	}
	met := struct {
		Task string `json:"task"`
		Run  string `json:"run"`
		With []side `json:"with"`
	}{Task: t.ID, Run: t.Worker}
	var late []neighbour
	for _, n := range nb {
		met.With = append(met.With, side{n.Task.ID, n.Task.Worker, n.Files})
		m.C.Runs.Send(n.Task.Worker, fmt.Sprintf("[from the Hive] %s started on %q (task %s), and its brief names %s, which yours names too. Tell it what you change there, and ask what it changes: `hive agent message --to %s --text \"...\"`.",
			t.Worker, t.Label(), t.ID, codeList(n.Files), t.Worker))
		if !strings.Contains(brief, n.Task.Worker) {
			late = append(late, n)
		}
	}
	if len(late) > 0 {
		m.C.Runs.Send(t.Worker, "[from the Hive] Started beside you:\n\n"+strings.TrimPrefix(besideText(late), "## Working beside you\n\n"))
	}
	m.C.J.Append("work.met", met)
}

// movedBy names what moved main into the files a conflicting branch
// changed: the tasks whose merges since the branch's base touched them, and
// how to reach their workers. "" when nothing can be named.
func (m *Module) movedBy(p core.Project, head, main string) string {
	if head == "" || main == "" {
		return ""
	}
	dir := m.C.WS.Project(p.Name)
	git := func(args ...string) []string {
		out, err := core.Git(dir, args...)
		if err != nil {
			return nil
		}
		return strings.Fields(out)
	}
	base := git("merge-base", head, main)
	if len(base) != 1 {
		return ""
	}
	mine := git("diff", "--name-only", "--no-renames", base[0], head)
	if len(mine) == 0 || len(mine) > 200 {
		return ""
	}
	shas := git(append([]string{"log", "--format=%H", "-n", "20", base[0] + ".." + main, "--"}, mine...)...)
	byMerge := map[string]Task{}
	for _, o := range m.List(func(o Task) bool { return o.Project == p.Name && (o.MergeSHA != "" || len(o.Banks) > 0) }) {
		byMerge[o.MergeSHA] = o
		for _, b := range o.Banks {
			byMerge[b.MergeSHA] = o
		}
	}
	var lines []string
	for _, sha := range shas {
		o, ok := byMerge[sha]
		if !ok || len(lines) == 3 {
			continue
		}
		var both []string
		for _, f := range git("diff-tree", "--no-commit-id", "--name-only", "--no-renames", "-r", sha+"^", sha) {
			if slices.Contains(mine, f) {
				both = append(both, f)
			}
		}
		reach := ""
		if r, ok := m.C.Runs.Get(o.Worker); ok && !r.Terminal() {
			reach = fmt.Sprintf("; if its change and yours disagree, message it: `hive agent message --to %s --text \"...\"`", o.Worker)
		} else if pr := o.PROf(sha); pr.Number > 0 {
			reach = fmt.Sprintf("; its worker has ended, and PR #%d says what it changed and why", pr.Number)
		}
		lines = append(lines, fmt.Sprintf("- %q (task %s, run %s) merged as %s and changed %s%s.", o.Label(), o.ID, o.Worker, core.Short(sha), codeList(both), reach))
	}
	if len(lines) == 0 {
		return ""
	}
	return "\n\nWhat moved main in the files you changed:\n\n" + strings.Join(lines, "\n")
}

// finishNeighbours is what the reply to a worker's finish adds: the runs on
// its brief's files now, so what it did reaches them while it can still say.
func (m *Module) finishNeighbours(r core.Run) string {
	t, ok := m.Get(r.Task)
	if !ok || r.Role != "worker" {
		return ""
	}
	t.Worker = r.ID
	nb := m.neighbours(t)
	if len(nb) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nWorking on the same files now:\n")
	for _, n := range nb {
		fmt.Fprintf(&b, "- %s on %q (%s)\n", n.Task.Worker, n.Task.Label(), codeList(n.Files))
	}
	b.WriteString("If what you did changes theirs, message them before you end your turn (`hive agent message --to <run> --text \"...\"`). If one helped you, say \"Helped-by: <run>\" in your account.")
	return b.String()
}

// wrapFinish has the reply to finish name the finishing worker's neighbours.
func (m *Module) wrapFinish() {
	ops := m.C.Runs.Ops()
	for _, op := range ops.List() {
		if op.Name != "finish" {
			continue
		}
		inner := op.Fn
		op.Fn = func(r core.Run, a json.RawMessage) (any, error) {
			out, err := inner(r, a)
			if s, ok := out.(string); ok && err == nil {
				out = s + m.finishNeighbours(r)
			}
			return out, err
		}
		ops.Register(op)
	}
}
