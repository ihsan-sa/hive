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

// The harnesses installed modules declare, read from modulesDir. Get reads
// the folder again when it doesn't know a name, so a module installed while
// the daemon runs is found without a restart.
var (
	regMu      sync.Mutex
	modulesDir string
	declared   map[string]*Spec
	loadErrs   []string
)

// UseModules points the registry at a workspace's installed modules folder.
func UseModules(dir string) {
	regMu.Lock()
	defer regMu.Unlock()
	if dir != modulesDir {
		modulesDir, declared = dir, nil
	}
}

// reload reads every installed module's harness specs. Two modules declaring
// one name keep the first by folder name; a spec that doesn't parse is
// skipped and kept in loadErrs for `hive harness`.
func reload() {
	declared, loadErrs = map[string]*Spec{}, nil
	if modulesDir == "" {
		return
	}
	ents, _ := os.ReadDir(modulesDir)
	for _, e := range ents {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(modulesDir, e.Name())
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
				loadErrs = append(loadErrs, fmt.Sprintf("%s: %s is outside the module", e.Name(), f))
				continue
			}
			s, err := LoadSpec(filepath.Join(dir, f))
			if err != nil {
				loadErrs = append(loadErrs, e.Name()+": "+err.Error())
				continue
			}
			if _, dup := declared[s.ID]; dup {
				loadErrs = append(loadErrs, fmt.Sprintf("%s: harness %s is already declared by another module", e.Name(), s.ID))
				continue
			}
			declared[s.ID] = s
		}
	}
}

func lookup(name string, fresh bool) (*Spec, bool) {
	regMu.Lock()
	defer regMu.Unlock()
	if declared == nil || fresh {
		reload()
	}
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
	if s, ok := lookup(name, false); ok {
		return s, nil
	}
	if s, ok := lookup(name, true); ok {
		return s, nil
	}
	return nil, fmt.Errorf("unknown harness %q (built in: %s; a module can add one, see docs/harnesses.md)", name, strings.Join(builtins, ", "))
}

// Names are the harnesses a person can pick: the built-in ones, then those
// the installed modules declare.
func Names() []string {
	regMu.Lock()
	defer regMu.Unlock()
	reload()
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
	reload()
	var out []*Spec
	for _, s := range declared {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, slices.Clone(loadErrs)
}
