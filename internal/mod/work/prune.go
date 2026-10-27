package work

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// Agents' checkouts under a project's .checkouts are removed once nothing can
// take a turn in them again (they had piled up to 641, 3.1 GB, in a day):
//
//   - a task's worker checkout when the task is merged or done, or has sat
//     stopped or blocked for staleTask (Recheck and a warden's answer bring a
//     worker back in its own checkout until then);
//   - a reviewer's or seat's checkout, and an earlier worker's after Retry,
//     once its run has ended and its task is over or the run is staleRun old;
//   - a run with no task (a spawned reviewer, a step-back), once ended and
//     staleRun old; a lead's (an isolated project's) after staleTask;
//   - a folder no run names, once staleRun old.
//
// A run that hasn't ended keeps its checkout, whatever its task says.
//
// A run's private folder (its home and CODEX_HOME, which nothing else
// removed) goes by the same rule. Before that, the caches in its home go once
// it has ended and sat idle for staleCache: a Go build cache filled by one
// `go test ./...` is a gigabyte or more, and every run kept its own, so the
// Hive grew by ~50 GB in eight hours that hive.clean never named. A run
// brought back after that rebuilds what it needs.
const (
	staleTask  = 7 * 24 * time.Hour
	staleRun   = 24 * time.Hour
	staleCache = time.Hour
)

// homeCaches are what of a run's home is a cache: the XDG cache (Go's build
// cache, pip's, uv's), GOPATH's module cache and npm's.
var homeCaches = []string{".cache", filepath.Join("go", "pkg"), ".npm"}

// over reports whether a task can no longer bring its worker back.
func over(t Task, now time.Time) bool {
	return t.Stage == StageMerged || t.Stage == StageDone || t.Terminal() && now.Sub(t.Updated) > staleTask
}

// spent reports whether the checkout named id may go.
func (m *Module) spent(id string, now time.Time, dir string) bool {
	r, ok := m.C.Runs.Get(id)
	if !ok {
		fi, err := os.Stat(dir)
		return err == nil && now.Sub(fi.ModTime()) > staleRun
	}
	if !r.Terminal() {
		return false
	}
	old := now.Sub(r.Updated) > staleRun
	if r.Role == "lead" {
		return now.Sub(r.Updated) > staleTask
	}
	if r.Task == "" {
		return old
	}
	t, ok := m.Get(r.Task)
	if !ok {
		return old
	}
	if t.Worker == id {
		return over(t, now)
	}
	return over(t, now) || old
}

// PruneCheckouts removes every spent checkout of every project, for the
// hive.clean job.
func (m *Module) PruneCheckouts() string {
	now := time.Now()
	n, freed := 0, int64(0)
	for _, p := range m.C.Projects.List() {
		root := m.C.WS.Checkouts(p.Name)
		entries, _ := os.ReadDir(root)
		for _, e := range entries {
			dir := filepath.Join(root, e.Name())
			if !e.IsDir() || !m.spent(e.Name(), now, dir) {
				continue
			}
			size := core.Size(dir)
			if m.C.Effects.RemoveCheckout(p.Name, e.Name()) == nil {
				n, freed = n+1, freed+size
			}
		}
	}
	var did []string
	if n > 0 {
		did = append(did, fmt.Sprintf("removed %d checkout(s), %s", n, core.Bytes(freed)))
	}
	if s := m.pruneRunHomes(now); s != "" {
		did = append(did, s)
	}
	return strings.Join(did, ", ")
}

// pruneRunHomes removes the private folders of spent runs and the caches in
// the homes of runs that ended staleCache ago.
func (m *Module) pruneRunHomes(now time.Time) string {
	root := filepath.Join(m.C.WS.Private(), "runs")
	entries, _ := os.ReadDir(root)
	homes, caches := 0, 0
	var freed int64
	for _, e := range entries {
		id, dir := e.Name(), filepath.Join(root, e.Name())
		if !e.IsDir() {
			continue
		}
		if m.spent(id, now, dir) {
			size := core.Size(dir)
			if core.RemoveTree(dir) == nil {
				homes, freed = homes+1, freed+size
			}
			continue
		}
		if r, ok := m.C.Runs.Get(id); !ok || !r.Terminal() || now.Sub(r.Updated) < staleCache {
			continue
		}
		for _, c := range homeCaches {
			p, ok := realDirUnder(m.C.WS.RunHome(id), c)
			if !ok {
				continue
			}
			// A run reopened since the check above keeps its caches.
			if r, ok := m.C.Runs.Get(id); !ok || !r.Terminal() {
				break
			}
			size := core.Size(p)
			if core.RemoveTree(p) == nil {
				caches, freed = caches+1, freed+size
			}
		}
	}
	if homes == 0 && caches == 0 {
		return ""
	}
	return fmt.Sprintf("removed %d spent run(s)' homes and %d cache folder(s) of ended runs, %s", homes, caches, core.Bytes(freed))
}

// ended removes a task's spent checkouts as it ends (merged or done).
func (m *Module) ended(t Task) {
	now := time.Now()
	for _, r := range m.C.Runs.List(func(r *core.Run) bool { return r.Task == t.ID }) {
		if dir := filepath.Join(m.C.WS.Checkouts(t.Project), r.ID); core.Exists(dir) && m.spent(r.ID, now, dir) {
			m.C.Effects.RemoveCheckout(t.Project, r.ID)
		}
	}
}

// realDirUnder is home/rel when it and every folder from home down to it is
// a real folder, not a symlink: the home is the agent's to write, and the
// daemon removing a cache there must not follow a link it planted (`~/go`
// pointing at a project would take that project's pkg/).
func realDirUnder(home, rel string) (string, bool) {
	p := home
	for _, part := range append([]string{""}, strings.Split(filepath.ToSlash(rel), "/")...) {
		p = filepath.Join(p, part)
		if fi, err := os.Lstat(p); err != nil || !fi.IsDir() {
			return "", false
		}
	}
	return p, true
}
