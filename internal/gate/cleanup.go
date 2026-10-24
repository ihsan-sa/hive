package gate

import (
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
	n := reap(abs)
	tidy(abs)
	return n
}

// childRoot is a side's or a sealed check's root in a run folder.
func childRoot(name string) bool {
	return name == "candidate" || name == "control" || strings.HasPrefix(name, "check-")
}

// runDirs are the folders a run makes in out: the only places a process
// counts as the run's by its program or working directory. Anything else in
// out (a shell or an editor a person has open there) is matched only by
// rootEnv, which the gate alone sets.
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
// caches and clone. Its workspace ("hive", with the daemon's log and
// journal) and the nested run ("grandchild", tidied by its own gate) stay.
var bulk = []string{"home", "tmp", "gocache", "gopath", "org-clone", "evaluator-home", "evaluator-tmp"}

// tidy removes the run's checkouts, binaries and caches: only folders it
// names itself, never anything else a person keeps in out.
func tidy(out string) {
	for _, dir := range runDirs(out) {
		if !childRoot(filepath.Base(dir)) {
			removeTree(dir)
			continue
		}
		for _, b := range bulk {
			removeTree(filepath.Join(dir, b))
		}
	}
	os.Remove(filepath.Join(out, "org")) // a local org, once its repositories are deleted
}

// removeTree removes p, making its folders writable first: a module cache is
// read-only.
func removeTree(p string) {
	filepath.WalkDir(p, func(path string, d os.DirEntry, err error) error {
		if d != nil && d.IsDir() {
			os.Chmod(path, 0o755)
		}
		return nil
	})
	os.RemoveAll(p)
}
