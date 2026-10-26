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
// Get finds them here first; an installed module's spec can't take a name.
var builtins = []Harness{Claude{}, Codex{}, Open{}, Fake{}}

// aliases are the other names a built-in harness answers to.
var aliases = map[string]string{"": "claude", "claude-code": "claude"}

func builtinGet(name string) (Harness, bool) {
	if n, ok := aliases[name]; ok {
		name = n
	}
	for _, h := range builtins {
		if h.Name() == name {
			return h, true
		}
	}
	return nil, false
}

func builtin(name string) bool {
	_, ok := builtinGet(name)
	return ok
}

func builtinNames() []string {
	out := make([]string, len(builtins))
	for i, h := range builtins {
		out[i] = h.Name()
	}
	return out
}

// Registry is the harnesses one workspace can run: the built-in ones and
// those its installed modules declare. It reads the modules again only when
// the folder, a module's folder, its module.json or a spec has changed
// (stamp), so a module installed while the daemon runs is found without a
// restart, and the control center redrawing its settings costs a few stats
// rather than reading every module.
type Registry struct {
	dir       string
	mu        sync.Mutex
	declared  map[string]*Spec
	loadErrs  []string
	read      []string // the paths the last reload read, stamped
	readStamp string
}

var (
	regsMu sync.Mutex
	regs   = map[string]*Registry{}
	def    = For("")
)

// For is the registry of the modules installed in dir ("" is the built-in
// harnesses only). One registry per folder is kept for the process, so
// workspaces side by side (tests in parallel) each see their own.
func For(dir string) *Registry {
	regsMu.Lock()
	defer regsMu.Unlock()
	r, ok := regs[dir]
	if !ok {
		r = &Registry{dir: dir}
		regs[dir] = r
	}
	return r
}

// UseModules points the package's own Get, Names and Declared at a
// workspace's installed modules folder; a process with one workspace (the
// daemon, the control center, the CLI) says which once.
func UseModules(dir string) {
	r := For(dir)
	regsMu.Lock()
	def = r
	regsMu.Unlock()
}

// Default is the registry UseModules last named.
func Default() *Registry {
	regsMu.Lock()
	defer regsMu.Unlock()
	return def
}

// Get, Names, Declared and Models ask the default registry.
func Get(name string) (Harness, error) { return Default().Get(name) }
func Names() []string                  { return Default().Names() }
func Declared() ([]*Spec, []string)    { return Default().Declared() }
func Models(harness string) []string   { return Default().Models(harness) }

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
func (r *Registry) reload() {
	r.declared, r.loadErrs, r.read = map[string]*Spec{}, nil, nil
	if r.dir == "" {
		return
	}
	r.read = append(r.read, r.dir)
	defer func() { r.readStamp = stamp(r.read) }()
	ents, _ := os.ReadDir(r.dir)
	var dirs []string
	for _, e := range ents {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			dirs = append(dirs, filepath.Join(r.dir, e.Name()))
		}
	}
	sort.SliceStable(dirs, func(i, j int) bool { return builtinModule(dirs[i]) && !builtinModule(dirs[j]) })
	for _, dir := range dirs {
		name := filepath.Base(dir)
		r.read = append(r.read, dir, filepath.Join(dir, "module.json"))
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
				r.loadErrs = append(r.loadErrs, fmt.Sprintf("%s: %s is outside the module", name, f))
				continue
			}
			r.read = append(r.read, filepath.Join(dir, f))
			s, err := LoadSpec(filepath.Join(dir, f))
			if err != nil {
				r.loadErrs = append(r.loadErrs, name+": "+err.Error())
				continue
			}
			if _, dup := r.declared[s.ID]; dup {
				r.loadErrs = append(r.loadErrs, fmt.Sprintf("%s: harness %s is already declared by another module", name, s.ID))
				continue
			}
			r.declared[s.ID] = s
		}
	}
}

// load reads the specs again when what was read has changed. The caller
// holds r.mu.
func (r *Registry) load() {
	if r.declared == nil || stamp(r.read) != r.readStamp {
		r.reload()
	}
}

func (r *Registry) lookup(name string) (*Spec, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.load()
	s, ok := r.declared[name]
	return s, ok
}

// Get returns a harness by name: a built-in one, or one an installed module
// declares.
func (r *Registry) Get(name string) (Harness, error) {
	if h, ok := builtinGet(name); ok {
		return h, nil
	}
	if s, ok := r.lookup(name); ok {
		return s, nil
	}
	return nil, fmt.Errorf("unknown harness %q (built in: %s; a module can add one, see docs/harnesses.md)", name, strings.Join(builtinNames(), ", "))
}

// Names are the harnesses a person can pick: the built-in ones, then those
// the installed modules declare.
func (r *Registry) Names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.load()
	mods := make([]string, 0, len(r.declared))
	for n := range r.declared {
		mods = append(mods, n)
	}
	sort.Strings(mods)
	return append(builtinNames(), mods...)
}

// Declared are the installed modules' harnesses, and what kept any from
// loading.
func (r *Registry) Declared() ([]*Spec, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.load()
	var out []*Spec
	for _, s := range r.declared {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, slices.Clone(r.loadErrs)
}

// Models are the models a harness knows, the default first: those its
// Models method lists (none for the open harness, which knows only the
// models the config names), or a module's spec's list.
func (r *Registry) Models(harness string) []string {
	h, err := r.Get(harness)
	if err != nil {
		return nil
	}
	if m, ok := h.(interface{ Models() []string }); ok {
		return m.Models()
	}
	return nil
}
