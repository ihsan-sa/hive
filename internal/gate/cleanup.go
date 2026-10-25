package gate

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Every process a gate's child starts carries these, whatever it detaches
// from: rootEnv, the child's root, so reap finds it by the run it belongs to;
// ownerEnv, the gate that started it, so a later gate ends what one that died
// left behind (reapOrphans).
const (
	rootEnv  = "HIVE_GATE_ROOT"
	ownerEnv = "HIVE_GATE_OWNER"
)

// owner is this gate as ownerEnv names it.
var owner = ownerID()

// Reap ends every process the gate run in out started (children,
// grandchildren, the daemons they detached, a nested gate's) and removes
// the bulky parts of the run (checkouts, binaries, Go caches), keeping the
// report, the verified binary and every log: the children's workspaces, with
// their daemon.log and journal, and their *.log files stay in the Hive's
// record. Run calls it as it ends; the daemon calls it again once the gate
// has exited, which covers a gate that was killed or crashed. It returns how
// many processes it ended.
func Reap(out string) int {
	abs, err := filepath.Abs(out)
	if err != nil || abs == "/" {
		return 0
	}
	if fi, err := os.Lstat(abs); err != nil || !fi.IsDir() {
		return 0 // a symlink in out's place is not a run's folder
	}
	n := reap(abs)
	tidy(abs)
	return n
}

// Untidy reports whether the run in out still holds what Reap removes, so
// a sweep over every past run reaps only those a dead gate left.
func Untidy(out string) bool {
	for _, dir := range runDirs(out) {
		if !childRoot(filepath.Base(dir)) {
			return true
		}
		for _, b := range bulk {
			if _, err := os.Lstat(filepath.Join(dir, b)); err == nil {
				return true
			}
		}
	}
	return false
}

// childRoot is a side's or a sealed check's root in a run folder.
func childRoot(name string) bool {
	return name == "candidate" || name == "control" || strings.HasPrefix(name, "check-")
}

// runDirs are the folders a run makes in out: the only places a process
// counts as the run's by its program or working directory. Anything else in
// out (a shell or an editor a person has open there) is matched only by
// rootEnv, which the gate alone sets. A symlink is never one of them.
func runDirs(out string) []string {
	var dirs []string
	ents, _ := os.ReadDir(out)
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() && (childRoot(name) || name == "evaluator" || name == "prefetch" ||
			strings.HasPrefix(name, "src-") || strings.HasPrefix(name, "built-")) {
			dirs = append(dirs, filepath.Join(out, name))
		}
	}
	return dirs
}

// bulk is what a child root holds besides its logs: its home, scratch,
// caches and clone, and in its workspace ("hive") the project clones and
// the binaries and tools the child's daemon fetched. The workspace's daemon
// log, journal and runs stay, and so does the nested run ("grandchild",
// tidied by its own gate). The gate's children run the fake harness, so
// home holds no harness's session logs; one that ran a real harness would
// keep those first.
var bulk = []string{"home", "tmp", "gocache", "gopath", "org-clone", "evaluator-home", "evaluator-tmp",
	"hive/projects", "hive/.hive/bin", "hive/.hive/tools"}

// tidy removes the run's checkouts, binaries and caches: only folders it
// names itself, never anything else a person keeps in out.
func tidy(out string) {
	root, err := os.OpenRoot(out)
	if err != nil {
		return
	}
	defer root.Close()
	for _, dir := range runDirs(out) {
		name := filepath.Base(dir)
		if !childRoot(name) {
			removeIn(root, name)
			continue
		}
		for _, b := range bulk {
			removeIn(root, filepath.Join(name, b))
		}
	}
	root.Remove("org") // a local org, once its repositories are deleted
}

// removeIn removes rel under root, making its folders writable first: a
// module cache is read-only. Every step stays inside root, so a symlink a
// bare candidate swaps in for a folder mid-walk can't turn the chmod or the
// removal onto the host's files.
func removeIn(root *os.Root, rel string) {
	fs.WalkDir(root.FS(), filepath.ToSlash(rel), func(path string, d fs.DirEntry, err error) error {
		if d != nil && d.IsDir() {
			root.Chmod(path, 0o755)
		}
		return nil
	})
	root.RemoveAll(rel)
}
