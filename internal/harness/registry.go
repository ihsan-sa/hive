package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
)

// builtins are the harnesses compiled in, in the order a person sees them.
var builtins = []string{"claude", "codex", "open", "fake"}

func builtin(name string) bool {
	return slices.Contains(builtins, name) || name == "" || name == "claude-code"
}

// The harnesses installed modules declare, read from modulesDir. They are
// read again only when the folder, a module's folder, its module.json or a
// spec has changed (stamp), so a module installed while the
// daemon runs is found without a restart, and the control center redrawing
// its settings costs a few stats rather than reading every module.
var (
	regMu      sync.Mutex
	modulesDir string
	declared   map[string]*Spec
	loadErrs   []string
	read       []string // the paths the last reload read, stamped
	readStamp  string
)

// UseModules points the registry at a workspace's installed modules folder.
// It is one per process, as the daemon, CLI and control center each serve
// one workspace; tests that use two at once must not run in parallel.
func UseModules(dir string) {
	regMu.Lock()
	defer regMu.Unlock()
	if dir != modulesDir {
		modulesDir, declared = dir, nil
	}
}

// stamp is the size and modification time of each path, so a change to any
// of them (a module added or removed changes the folder's own) reads again.
func stamp(paths []string) string {
	var b strings.Builder
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil {
			fmt.Fprintf(&b, "%d.%d;", fi.ModTime().UnixNano(), fi.Size())
		} else {
			b.WriteString("-;")
		}
	}
	return b.String()
}

// builtinModule says the Hive itself installed the module in dir (a default
// one), from its .hive-source.json.
func builtinModule(dir string) bool {
	raw, err := os.ReadFile(filepath.Join(dir, ".hive-source.json"))
	var src struct {
		Source string `json:"source"`
	}
	return err == nil && json.Unmarshal(raw, &src) == nil && src.Source == "builtin"
}

// reload reads every installed module's harness specs. When two modules
// declare one name, a default module's wins, then the first by folder name,
// so a module installed later can't take over a name (and the key stored for
// it) that a default one ships; a spec that doesn't parse is skipped and kept
// in loadErrs for `hive harness`.
func reload() {
	declared, loadErrs, read = map[string]*Spec{}, nil, nil
	if modulesDir == "" {
		return
	}
	read = append(read, modulesDir)
	defer func() { readStamp = stamp(read) }()
	ents, _ := os.ReadDir(modulesDir)
	var dirs []string
	for _, e := range ents {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			dirs = append(dirs, filepath.Join(modulesDir, e.Name()))
		}
	}
	sort.SliceStable(dirs, func(i, j int) bool { return builtinModule(dirs[i]) && !builtinModule(dirs[j]) })
	for _, dir := range dirs {
		name := filepath.Base(dir)
		read = append(read, dir, filepath.Join(dir, "module.json"))
		raw, err := os.ReadFile(filepath.Join(dir, "module.json"))
		if err != nil {
			continue
		}
		var m struct {
			Provides map[string][]string `json:"provides"`
		}
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		for _, f := range m.Provides["harnesses"] {
			if !filepath.IsLocal(f) {
				loadErrs = append(loadErrs, fmt.Sprintf("%s: %s is outside the module", name, f))
				continue
			}
			read = append(read, filepath.Join(dir, f))
			s, err := LoadSpec(filepath.Join(dir, f))
			if err != nil {
				loadErrs = append(loadErrs, name+": "+err.Error())
				continue
			}
			if _, dup := declared[s.ID]; dup {
				loadErrs = append(loadErrs, fmt.Sprintf("%s: harness %s is already declared by another module", name, s.ID))
				continue
			}
			declared[s.ID] = s
		}
	}
}

// load reads the specs again when what was read has changed.
func load() {
	if declared == nil || stamp(read) != readStamp {
		reload()
	}
}

func lookup(name string) (*Spec, bool) {
	regMu.Lock()
	defer regMu.Unlock()
	load()
	s, ok := declared[name]
	return s, ok
}

// Get returns a harness by name: a built-in one, or one an installed module
// declares.
func Get(name string) (Harness, error) {
	switch name {
	case "", "claude", "claude-code":
		return Claude{}, nil
	case "fake":
		return Fake{}, nil
	case "codex":
		return Codex{}, nil
	case "open":
		return Open{}, nil
	}
	if s, ok := lookup(name); ok {
		return s, nil
	}
	return nil, fmt.Errorf("unknown harness %q (built in: %s; a module can add one, see docs/harnesses.md)", name, strings.Join(builtins, ", "))
}

// Names are the harnesses a person can pick: the built-in ones, then those
// the installed modules declare.
func Names() []string {
	regMu.Lock()
	defer regMu.Unlock()
	load()
	out := slices.Clone(builtins)
	var mods []string
	for n := range declared {
		mods = append(mods, n)
	}
	sort.Strings(mods)
	return append(out, mods...)
}

// Declared are the installed modules' harnesses, and what kept any from
// loading.
func Declared() ([]*Spec, []string) {
	regMu.Lock()
	defer regMu.Unlock()
	load()
	var out []*Spec
	for _, s := range declared {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, slices.Clone(loadErrs)
}
