// Package modules manages installed modules: the defaults written on first
// run, modules installed from a git repository, where each came from and
// which release it is on, and which projects may use it.
package modules

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	defaults "github.com/ihsan-sa/hive-standalone-build/modules"
)

// Manifest is a module's own module.json.
type Manifest struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Provides    map[string][]string `json:"provides,omitempty"`
	Projects    []string            `json:"projects,omitempty"` // default access
	// Exec is a program the daemon starts and keeps running (argv; a first
	// word starting with "." is relative to the module's folder), and Hooks
	// are what it serves, each a fixed slice of the control API (see runner).
	Exec  []string `json:"exec,omitempty"`
	Hooks []string `json:"hooks,omitempty"`
}

// Source is the host-written record of where a module came from.
type Source struct {
	Source    string    `json:"source"` // builtin | git URL
	Release   string    `json:"release"`
	Hash      string    `json:"hash,omitempty"` // builtin content hash at install, to spot local edits
	Installed time.Time `json:"installed"`
	Updated   time.Time `json:"updated,omitempty"`
	Projects  []string  `json:"projects"`         // access: "*" or project names
	Commit    string    `json:"commit,omitempty"` // git modules: the commit checked out
	Review    *Review   `json:"review,omitempty"` // git modules: the last read of its code
}

// Review is a read-only review run's verdict on a module at one commit.
type Review struct {
	Commit string `json:"commit"`
	Vote   string `json:"vote"` // approve | decline
	Reason string `json:"reason"`
	Run    string `json:"run"`
}

// Module is one installed module.
type Module struct {
	Manifest
	Source Source `json:"source"`
	Dir    string `json:"dir"`
}

// Registry reads and writes .modules/.
type Registry struct {
	ws    core.Workspace
	j     *core.Journal
	ticks []func()
}

// Builtin adds an in-process module's step to the tick list.
func (r *Registry) Builtin(tick func()) { r.ticks = append(r.ticks, tick) }

// Tick steps every in-process module in the order they were added.
func (r *Registry) Tick() {
	for _, t := range r.ticks {
		t()
	}
}

// New returns the registry.
func New(ws core.Workspace, j *core.Journal) *Registry { return &Registry{ws: ws, j: j} }

func sourcePath(dir string) string { return filepath.Join(dir, ".hive-source.json") }

// InstallDefaults writes the default modules that aren't installed yet, and
// refreshes ones still identical to an older builtin release.
func (r *Registry) InstallDefaults() error {
	entries, err := fs.ReadDir(defaults.FS, ".")
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		dir := filepath.Join(r.ws.Modules(), name)
		src, hasSrc := readSource(dir)
		if hasSrc && src.Source != "builtin" {
			continue
		}
		if hasSrc && (src.Release == core.Version || hashDir(dir) != src.Hash) {
			continue // current, or edited locally: the Hive's own changes win
		}
		if err := writeDefault(name, dir); err != nil {
			return err
		}
		s := Source{Source: "builtin", Release: core.Version, Installed: time.Now().UTC(), Projects: []string{"*"}, Hash: hashDir(dir)}
		if hasSrc {
			s.Installed, s.Projects, s.Updated = src.Installed, src.Projects, time.Now().UTC()
		}
		if err := writeSource(dir, s); err != nil {
			return err
		}
	}
	return nil
}

func writeDefault(name, dir string) error {
	return fs.WalkDir(defaults.FS, name, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(name, filepath.FromSlash(p))
		target := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := defaults.FS.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o644)
	})
}

func readSource(dir string) (Source, bool) {
	raw, err := os.ReadFile(sourcePath(dir))
	if err != nil {
		return Source{}, false
	}
	var s Source
	return s, json.Unmarshal(raw, &s) == nil
}

func writeSource(dir string, s Source) error {
	raw, _ := json.MarshalIndent(s, "", "  ")
	return core.WriteFileAtomic(sourcePath(dir), raw, 0o644)
}

// hashDir hashes a module's files except the source record.
func hashDir(dir string) string {
	h := sha256.New()
	var files []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Base(p) != ".hive-source.json" && !strings.Contains(p, string(filepath.Separator)+".git"+string(filepath.Separator)) {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	for _, f := range files {
		rel, _ := filepath.Rel(dir, f)
		raw, _ := os.ReadFile(f)
		h.Write([]byte(filepath.ToSlash(rel)))
		h.Write(raw)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// List returns installed modules.
func (r *Registry) List() []Module {
	entries, _ := os.ReadDir(r.ws.Modules())
	var out []Module
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(r.ws.Modules(), e.Name())
		m := Module{Dir: dir}
		raw, err := os.ReadFile(filepath.Join(dir, "module.json"))
		if err == nil {
			json.Unmarshal(raw, &m.Manifest)
		}
		if m.Name == "" {
			m.Name = e.Name()
		}
		m.Source, _ = readSource(dir)
		if len(m.Source.Projects) == 0 {
			m.Source.Projects = m.Manifest.Projects
			if len(m.Source.Projects) == 0 {
				m.Source.Projects = []string{"*"}
			}
		}
		out = append(out, m)
	}
	return out
}

// Get returns one module.
func (r *Registry) Get(name string) (Module, bool) {
	for _, m := range r.List() {
		if m.Name == name || filepath.Base(m.Dir) == name {
			return m, true
		}
	}
	return Module{}, false
}

// Granted lists the modules a project may use.
func (r *Registry) Granted(project string) []string {
	var out []string
	for _, m := range r.List() {
		for _, p := range m.Source.Projects {
			if p == "*" || p == project {
				out = append(out, filepath.Base(m.Dir))
				break
			}
		}
	}
	return out
}

// Reaches says whether a module was granted project; "" (the whole
// workspace) is reached only by a grant of "*".
func (r *Registry) Reaches(name, project string) bool {
	m, ok := r.Get(name)
	if !ok {
		return false
	}
	for _, p := range m.Source.Projects {
		if p == "*" || (project != "" && p == project) {
			return true
		}
	}
	return false
}

// SetAccess grants or removes a project's access to a module.
func (r *Registry) SetAccess(name, project string, allow bool) error {
	m, ok := r.Get(name)
	if !ok {
		return core.ErrNotFound
	}
	s := m.Source
	var next []string
	for _, p := range s.Projects {
		if p != project {
			next = append(next, p)
		}
	}
	if allow {
		next = append(next, project)
	}
	if !allow && contains(s.Projects, "*") && project != "*" {
		return fmt.Errorf("%s is granted to every project; remove \"*\" first", name)
	}
	s.Projects = next
	if err := writeSource(m.Dir, s); err != nil {
		return err
	}
	r.j.MustAppend("module.access", map[string]any{"module": name, "project": project, "allow": allow})
	return nil
}

// SetReview records a review run's verdict on a module's commit.
func (r *Registry) SetReview(name string, v Review) error {
	m, ok := r.Get(name)
	if !ok {
		return core.ErrNotFound
	}
	s := m.Source
	s.Review = &v
	if err := writeSource(m.Dir, s); err != nil {
		return err
	}
	r.j.MustAppend("module.reviewed", map[string]string{"module": name, "commit": v.Commit, "vote": v.Vote, "reason": v.Reason, "run": v.Run})
	return nil
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

var safeName = regexp.MustCompile(`[^a-z0-9-]+`)

// NameFromURL derives a module folder name from a git URL.
func NameFromURL(url string) string {
	base := strings.TrimSuffix(filepath.Base(strings.TrimRight(url, "/")), ".git")
	return strings.Trim(safeName.ReplaceAllString(strings.ToLower(base), "-"), "-")
}

// Install clones a module the person approved and records where it came from.
func (r *Registry) Install(url, name string) (Module, error) {
	dir := filepath.Join(r.ws.Modules(), name)
	if core.Exists(dir) {
		return Module{}, fmt.Errorf("%s already exists", dir)
	}
	if _, err := core.GitT(10*time.Minute, "", "clone", "-q", url, dir); err != nil {
		os.RemoveAll(dir)
		return Module{}, err
	}
	return r.record(url, name)
}

func (r *Registry) record(url, name string) (Module, error) {
	dir := filepath.Join(r.ws.Modules(), name)
	if !core.Exists(filepath.Join(dir, ".git")) {
		return Module{}, fmt.Errorf("module %s did not clone", name)
	}
	core.Git(dir, "fetch", "-q", "--tags")
	rel := latestTag(dir)
	if rel != "" {
		core.Git(dir, "checkout", "-q", rel)
	} else {
		rel, _ = core.Git(dir, "rev-parse", "--short", "HEAD")
	}
	commit, _ := core.Git(dir, "rev-parse", "HEAD")
	s := Source{Source: url, Release: rel, Commit: commit, Installed: time.Now().UTC(), Projects: []string{"*"}}
	if err := writeSource(dir, s); err != nil {
		return Module{}, err
	}
	r.j.MustAppend("module.installed", map[string]string{"module": name, "source": url, "release": rel})
	m, _ := r.Get(name)
	return m, nil
}

// Update moves git-sourced modules to their source's newest release.
func (r *Registry) Update() []string {
	var changed []string
	for _, m := range r.List() {
		if m.Source.Source == "" || m.Source.Source == "builtin" || !core.Exists(filepath.Join(m.Dir, ".git")) {
			continue
		}
		if _, err := core.Git(m.Dir, "fetch", "-q", "--tags", "--force"); err != nil {
			continue
		}
		tag := latestTag(m.Dir)
		if tag == "" || tag == m.Source.Release {
			continue
		}
		if _, err := core.Git(m.Dir, "checkout", "-q", tag); err != nil {
			continue
		}
		s := m.Source
		s.Release, s.Updated = tag, time.Now().UTC()
		s.Commit, _ = core.Git(m.Dir, "rev-parse", "HEAD")
		writeSource(m.Dir, s)
		r.j.MustAppend("module.updated", map[string]string{"module": m.Name, "from": m.Source.Release, "to": tag})
		changed = append(changed, m.Name+" "+tag)
	}
	return changed
}

// latestTag returns the highest vX.Y.Z tag.
func latestTag(dir string) string {
	out, err := core.Git(dir, "tag", "--list")
	if err != nil {
		return ""
	}
	var best string
	var bestV [3]int
	for _, t := range strings.Fields(out) {
		v, ok := parseSemver(t)
		if ok && (best == "" || less(bestV, v)) {
			best, bestV = t, v
		}
	}
	return best
}

func parseSemver(t string) ([3]int, bool) {
	var v [3]int
	parts := strings.SplitN(strings.TrimPrefix(t, "v"), ".", 3)
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		p = strings.SplitN(p, "-", 2)[0]
		n, err := strconv.Atoi(p)
		if err != nil {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func less(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// Newer reports whether release tag b is newer than a.
func Newer(a, b string) bool {
	va, oka := parseSemver(a)
	vb, okb := parseSemver(b)
	if !okb {
		return false
	}
	if !oka {
		return true
	}
	return less(va, vb)
}

// Prompt reads a prompt file from the agents module (falling back to the default).
func (r *Registry) Prompt(file string) string {
	if raw, err := os.ReadFile(filepath.Join(r.ws.Modules(), "agents", file)); err == nil {
		return string(raw)
	}
	raw, _ := defaults.FS.ReadFile("agents/" + file)
	return string(raw)
}
