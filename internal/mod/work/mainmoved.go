package work

import (
	"errors"
	"os/exec"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// mainMoved looks, once for each main a waiting head meets, whether t's head
// still merges with it, and sends a head that doesn't back to its worker at
// once (catchUp, then conflict) instead of after its test, its reads and the
// forge's say at the merge: conflicts found only there cost a median 2.6 h
// from push (issue #1996). The look is git merge-tree in the project's own
// clone, with main's attributes as UpdateBranch takes them; a head that
// merges cleanly is left as it is, so main moving never restarts its checks.
// Anything but a conflict (an older git, a missing object) is no verdict, and
// the merge step's own look still stands behind it.
func (m *Module) mainMoved(t *Task, p core.Project, main string) (bool, error) {
	if t.SHA == "" || main == "" {
		return false, nil
	}
	key := t.SHA + "@" + main
	m.mu.Lock()
	seen := m.mergeLooked[t.ID] == key
	m.mergeLooked[t.ID] = key
	m.mu.Unlock()
	if seen {
		return false, nil
	}
	dir := m.C.WS.Project(p.Name)
	if _, err := core.Git(dir, "merge-base", "--is-ancestor", main, t.SHA); err == nil {
		return false, nil
	}
	_, err := core.Git(dir, "-c", "attr.tree="+main, "merge-tree", "--write-tree", "--name-only", t.SHA, main)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		return false, nil
	}
	return m.catchUp(t, p, main, "main moved to "+core.Short(main)+" and the branch conflicts with it")
}
