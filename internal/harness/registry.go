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
	"time"
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
// a module's files change, so a module installed or updated while the daemon
// runs is found without a restart, and a picker that asks on every frame
// costs a few stats.
type Registry struct {
	dir      string
	mu       sync.Mutex
	stamp    string
	files    []string // the spec files the last read loaded, whose changes it watches
	declared map[string]*Spec
	loadErrs []string
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

// fingerprint is what the modules folder holds that a read depends on: each
// module's module.json and the spec files the last read loaded.
func (r *Registry) fingerprint() string {
	var b strings.Builder
	ents, _ := os.ReadDir(r.dir)
	for _, e := range ents {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		b.WriteString(e.Name())
		stamp(&b, filepath.Join(r.dir, e.Name(), "module.json"))
	}
	for _, f := range r.files {
		stamp(&b, f)
	}
	return b.String()
}

func stamp(b *strings.Builder, p string) {
	if fi, err := os.Stat(p); err == nil {
		fmt.Fprintf(b, "|%d.%d", fi.ModTime().UnixNano(), fi.Size())
	} else {
		b.WriteString("|-")
	}
	b.WriteByte('\n')
}

// fresh reads the modules again if their files changed since the last read.
// The caller holds r.mu.
func (r *Registry) fresh() {
	if r.dir == "" {
		r.declared = map[string]*Spec{}
		return
	}
	if fp := r.fingerprint(); r.declared == nil || fp != r.stamp {
		r.reload()
		// A file written within the clock's tick of this read may change
		// again with the same stamp; leave the stamp empty so the next ask
		// reads again.
		if r.stamp = r.fingerprint(); time.Since(r.newest()) < time.Second {
			r.stamp = ""
		}
	}
}

// newest is the latest change among the files the last read loaded.
func (r *Registry) newest() time.Time {
	var t time.Time
	for _, f := range r.files {
		if fi, err := os.Stat(f); err == nil && fi.ModTime().After(t) {
			t = fi.ModTime()
		}
	}
	return t
}

// reload reads every installed module's harness specs. Two modules declaring
// one name keep the first by folder name; a spec that doesn't parse is
// skipped and kept in loadErrs for `hive harness`.
func (r *Registry) reload() {
	r.declared, r.loadErrs, r.files = map[string]*Spec{}, nil, nil
	ents, _ := os.ReadDir(r.dir)
	for _, e := range ents {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(r.dir, e.Name())
		r.files = append(r.files, filepath.Join(dir, "module.json"))
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
				r.loadErrs = append(r.loadErrs, fmt.Sprintf("%s: %s is outside the module", e.Name(), f))
				continue
			}
			r.files = append(r.files, filepath.Join(dir, f))
			s, err := LoadSpec(filepath.Join(dir, f))
			if err != nil {
				r.loadErrs = append(r.loadErrs, e.Name()+": "+err.Error())
				continue
			}
			if _, dup := r.declared[s.ID]; dup {
				r.loadErrs = append(r.loadErrs, fmt.Sprintf("%s: harness %s is already declared by another module", e.Name(), s.ID))
				continue
			}
			r.declared[s.ID] = s
		}
	}
}

func (r *Registry) lookup(name string) (*Spec, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fresh()
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
	r.fresh()
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
	r.fresh()
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
