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
// grandchildren, the daemons they detached, a nested gate's) and removes the
// folders the run made there, keeping its report, the verified binary and the
// log. Run calls it as it ends; the daemon calls it again once the gate has
// exited, which covers a gate that was killed or crashed. It returns how many
// processes it ended.
func Reap(out string) int {
	abs, err := filepath.Abs(out)
	if err != nil || abs == "/" {
		return 0
	}
	n := reap(abs)
	tidy(abs)
	return n
}

// tidy removes what a run made in out: only folders it names itself, never
// anything else a person keeps there.
func tidy(out string) {
	ents, _ := os.ReadDir(out)
	for _, e := range ents {
		name := e.Name()
		switch {
		case name == "org":
			os.Remove(filepath.Join(out, name)) // a local org, once its repositories are deleted
		case name == "candidate" || name == "control" || name == "evaluator" || name == "prefetch" ||
			strings.HasPrefix(name, "src-") || strings.HasPrefix(name, "built-") || strings.HasPrefix(name, "check-"):
			removeTree(filepath.Join(out, name))
		}
	}
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
