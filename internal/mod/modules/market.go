package modules

// The marketplace. An index is a repository (or a folder) with an index.json
// listing modules by name, each pinned to one commit of its own repository,
// with what it needs and the read behind its trust. A Hive reads the indexes
// in its config, its own first. Installing fetches the pinned commit, checks it
// against the listing, and refuses a module whose module.json asks for more
// than its listing declares; who says yes (a warden, or the person) is the
// risk module's call. docs/modules.md is the guide for module authors.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// DefaultIndex is the Hive's own index. Only its listings may say core-team.
const DefaultIndex = "https://github.com/Hive-RSI-Lab/marketplace"

// Needs is a module's reach: what its manifest declares, and what a listing
// or the person's yes grants.
type Needs struct {
	Program     bool     `json:"program,omitempty"`     // it carries a program the daemon runs
	Hooks       []string `json:"hooks,omitempty"`       // the slices of the control API it calls
	Hosts       []string `json:"hosts,omitempty"`       // network hosts its program reaches
	Schedules   []string `json:"schedules,omitempty"`   // schedules it adds
	Ops         []string `json:"ops,omitempty"`         // agent ops it adds
	Credentials []string `json:"credentials,omitempty"` // credentials it uses, only ever through the Hive
}

// Needs is what a module's own manifest asks for.
func (m Manifest) Needs() Needs {
	return Needs{Program: len(m.Exec) > 0, Hooks: m.Hooks, Hosts: m.Hosts, Schedules: m.Provides["schedules"], Ops: m.Provides["ops"], Credentials: m.Credentials}
}

// Beyond lists what n asks for that granted does not cover.
func (n Needs) Beyond(granted Needs) []string {
	var out []string
	if n.Program && !granted.Program {
		out = append(out, "a program")
	}
	for _, k := range []struct {
		kind      string
		ask, have []string
	}{{"hook", n.Hooks, granted.Hooks}, {"host", n.Hosts, granted.Hosts}, {"schedule", n.Schedules, granted.Schedules}, {"op", n.Ops, granted.Ops}, {"credential", n.Credentials, granted.Credentials}} {
		for _, x := range k.ask {
			if !slices.Contains(k.have, x) {
				out = append(out, k.kind+" "+x)
			}
		}
	}
	return out
}

func (n Needs) String() string {
	var parts []string
	if n.Program {
		parts = append(parts, "runs a program")
	}
	for _, k := range []struct {
		name string
		l    []string
	}{{"hooks", n.Hooks}, {"hosts", n.Hosts}, {"schedules", n.Schedules}, {"ops", n.Ops}, {"credentials", n.Credentials}} {
		if len(k.l) > 0 {
			parts = append(parts, k.name+": "+strings.Join(k.l, ", "))
		}
	}
	if len(parts) == 0 {
		return "files only (prompts, procedures, skills): no program, no reach"
	}
	return strings.Join(parts, "; ")
}

// Listing is one module in an index.
type Listing struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Repo        string `json:"repo"`           // a git URL
	Path        string `json:"path,omitempty"` // the module's folder in the repo; "" for its root
	Sha         string `json:"sha"`            // the full commit installs take
	Trust       string `json:"trust"`          // core-team | reviewed
	Read        string `json:"read"`           // who read it at that commit, and what they found
	Needs       Needs  `json:"needs"`
	Pulled      string `json:"pulled,omitempty"` // taken down, and why: it no longer installs
	Index       string `json:"index,omitempty"`  // the index it came from (filled on read)
}

// Indexes are the indexes a Hive reads, in order: the Hive's own unless the
// person's config lists others.
func Indexes(cfg core.Config) []string {
	if cfg.Indexes == nil {
		return []string{DefaultIndex}
	}
	return cfg.Indexes
}

var (
	nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	shaRe  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	hexRe  = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	refRe  = regexp.MustCompile(`^[0-9a-f]{7,40}$|^v[0-9][0-9A-Za-z.\-]*$`)
)

type indexCache struct {
	mu   sync.Mutex
	key  string
	at   time.Time
	list []Listing
	errs []string
}

// Listings reads every index (from a cache up to five minutes old); an index
// that can't be read is named in errs and the others still list.
func (r *Registry) Listings() ([]Listing, []string) { return r.listings(false) }

func (r *Registry) listings(fresh bool) ([]Listing, []string) {
	idx := Indexes(r.ws.LoadConfig())
	key := strings.Join(idx, "\n")
	c := &r.index
	c.mu.Lock()
	defer c.mu.Unlock()
	if !fresh && c.key == key && time.Since(c.at) < 5*time.Minute {
		return c.list, c.errs
	}
	var list []Listing
	var errs []string
	for _, src := range idx {
		ls, err := readIndex(src)
		if err != nil {
			errs = append(errs, src+": "+err.Error())
		}
		list = append(list, ls...)
	}
	c.key, c.at, c.list, c.errs = key, time.Now(), list, errs
	return list, errs
}

// readIndex reads an index: a local folder or file, or a git repository with
// index.json at its root. Listings that can't be installed safely are dropped
// with the reason.
func readIndex(src string) ([]Listing, error) {
	file := src
	if fi, err := os.Stat(src); err != nil || fi.IsDir() {
		dir := src
		if err != nil {
			tmp, err := os.MkdirTemp("", "hive-index-")
			if err != nil {
				return nil, err
			}
			defer os.RemoveAll(tmp)
			if _, err := core.GitT(2*time.Minute, "", "clone", "-q", "--depth", "1", src, tmp); err != nil {
				return nil, err
			}
			dir = tmp
		}
		file = filepath.Join(dir, "index.json")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var idx struct {
		Modules []Listing `json:"modules"`
	}
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil, fmt.Errorf("index.json: %w", err)
	}
	var bad []string
	var list []Listing
	for _, l := range idx.Modules {
		switch {
		case !nameRe.MatchString(l.Name):
			bad = append(bad, fmt.Sprintf("%q: not a module name", l.Name))
			continue
		case l.Repo == "" || !shaRe.MatchString(l.Sha):
			bad = append(bad, l.Name+": a listing needs a repo and a full 40-character sha")
			continue
		case safePath(l.Path) != nil:
			bad = append(bad, l.Name+": "+safePath(l.Path).Error())
			continue
		}
		if l.Trust == "core-team" && src != DefaultIndex {
			l.Trust = "reviewed" // only the Hive's own index speaks for the core team
		}
		if l.Trust != "core-team" && l.Trust != "reviewed" {
			l.Trust = "unlisted"
		}
		l.Index = src
		list = append(list, l)
	}
	if len(bad) > 0 {
		return list, errors.New("skipped " + strings.Join(bad, "; "))
	}
	return list, nil
}

func safePath(p string) error {
	if p == "" {
		return nil
	}
	if strings.Contains(p, "\\") || path.IsAbs(p) || slices.Contains(strings.Split(path.Clean(p), "/"), "..") {
		return fmt.Errorf("path %q must be a folder inside the repository", p)
	}
	return nil
}

// Plan is a module to install or update: where it comes from, the commit, and
// the reach it is granted. Fetch fills Sha (when it was a release or none),
// Release, Manifest and Diff.
type Plan struct {
	Name     string   `json:"name"`
	Repo     string   `json:"repo"`
	Path     string   `json:"path,omitempty"`
	Sha      string   `json:"sha,omitempty"`
	Release  string   `json:"release,omitempty"`
	Trust    string   `json:"trust"` // core-team | reviewed | unlisted | project (one of the Hive's own projects)
	Read     string   `json:"read,omitempty"`
	Index    string   `json:"index,omitempty"`
	Project  string   `json:"project,omitempty"`
	Needs    Needs    `json:"needs"`
	Manifest Manifest `json:"manifest"`
	Diff     string   `json:"diff,omitempty"` // an update: what changed since the installed commit
}

// Listed says whether an index vouches for it.
func (p Plan) Listed() bool { return p.Trust == "core-team" || p.Trust == "reviewed" }

// Ref names the plan so that PlanFor reads it back to the same commit: an
// ask's Ref.
func (p Plan) Ref() string {
	switch {
	case p.Trust == "project":
		return "project:" + p.Project + "@" + p.Sha
	case p.Listed():
		return p.Name + "@" + p.Sha
	}
	src := p.Repo
	if p.Path != "" {
		src += "//" + p.Path
	}
	return p.Name + "=" + src + "@" + p.Sha
}

// Describe is what the person or a warden reads before saying yes.
func (p Plan) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Module %s: %s\n", p.Name, p.Manifest.Description)
	src := p.Repo
	if p.Path != "" {
		src += " (folder " + p.Path + ")"
	}
	fmt.Fprintf(&b, "From %s at %s", src, p.Sha)
	if p.Release != "" && !strings.HasPrefix(p.Sha, p.Release) {
		fmt.Fprintf(&b, " (%s)", p.Release)
	}
	b.WriteString(".\n")
	switch p.Trust {
	case "project":
		fmt.Fprintf(&b, "Trust: the Hive's own project %s, built and reviewed through its pull requests.\n", p.Project)
	case "unlisted":
		b.WriteString("Trust: unlisted. No index vouches for this commit, so it installs only with the person's yes.\n")
	default:
		fmt.Fprintf(&b, "Trust: %s, listed by %s.\n", p.Trust, p.Index)
	}
	if p.Read != "" {
		fmt.Fprintf(&b, "Read: %s\n", p.Read)
	}
	fmt.Fprintf(&b, "It declares: %s.\n", p.Needs)
	if p.Needs.Program {
		fmt.Fprintf(&b, "Its program (%s) starts only after a read-only review of its code at this commit.\n", strings.Join(p.Manifest.Exec, " "))
	}
	return b.String()
}

// PlanFor reads what to install: an index's name, name@sha, a git URL or
// local repository (repo, repo//folder, either @ref), project:<name>, or a
// Ref. It does not fetch; a name@sha other than the listing's pin is unlisted.
func (r *Registry) PlanFor(spec string) (Plan, error) {
	spec = strings.TrimSpace(spec)
	name := ""
	if n, rest, ok := strings.Cut(spec, "="); ok && nameRe.MatchString(n) {
		name, spec = n, rest
	}
	base, ref := spec, ""
	if i := strings.LastIndex(spec, "@"); i > 0 && refRe.MatchString(spec[i+1:]) {
		base, ref = spec[:i], spec[i+1:]
	}
	if p, ok := strings.CutPrefix(base, "project:"); ok {
		if !nameRe.MatchString(p) {
			return Plan{}, fmt.Errorf("no project %q", p)
		}
		return Plan{Name: p, Repo: r.ws.Project(p), Sha: ref, Trust: "project", Project: p}, nil
	}
	if !nameRe.MatchString(base) { // a source, not a name
		repo, dir, _ := strings.Cut(base, "//")
		if strings.Contains(repo, "://") { // https://host/x//y: the scheme's // is not the folder's
			scheme, rest, _ := strings.Cut(base, "://")
			r2, d2, _ := strings.Cut(rest, "//")
			repo, dir = scheme+"://"+r2, d2
		}
		if err := safePath(dir); err != nil {
			return Plan{}, err
		}
		return Plan{Name: name, Repo: repo, Path: dir, Sha: ref, Trust: "unlisted"}, nil
	}
	list, errs := r.listings(false)
	for _, l := range list {
		if l.Name != base {
			continue
		}
		if l.Pulled != "" {
			return Plan{}, fmt.Errorf("%s was pulled from %s: %s", l.Name, l.Index, l.Pulled)
		}
		p := Plan{Name: l.Name, Repo: l.Repo, Path: l.Path, Sha: l.Sha, Trust: l.Trust, Read: l.Read, Index: l.Index, Needs: l.Needs}
		if ref != "" && !strings.HasPrefix(l.Sha, ref) {
			p.Sha, p.Trust, p.Index, p.Needs = ref, "unlisted", "", Needs{}
			p.Read = fmt.Sprintf("%s pins %s at %s; %s is another commit, which no index vouches for.", l.Index, l.Name, l.Sha[:12], ref)
		}
		return p, nil
	}
	why := ""
	if len(errs) > 0 {
		why = " (unread: " + strings.Join(errs, "; ") + ")"
	}
	return Plan{}, fmt.Errorf("no index lists a module named %q%s; give its git URL to install it unlisted", base, why)
}

// Resolve plans spec and fetches it, so the plan carries its commit, manifest
// and what it declares, checked against its listing.
func (r *Registry) Resolve(spec string) (Plan, error) {
	p, err := r.PlanFor(spec)
	if err != nil {
		return p, err
	}
	return r.fetch(p, "", nil)
}

// fetch clones p's repository out of sight, checks out its commit (a pinned
// one must match exactly; none means its newest release, else its HEAD), reads
// its module.json and refuses reach its listing doesn't declare. from, if set,
// is an installed commit to diff against; keep, if set, gets the module's
// folder before the clone goes.
func (r *Registry) fetch(p Plan, from string, keep func(tree string, p Plan) error) (Plan, error) {
	if err := safePath(p.Path); err != nil {
		return p, err
	}
	tmp, err := os.MkdirTemp("", "hive-module-")
	if err != nil {
		return p, err
	}
	defer os.RemoveAll(tmp)
	if _, err := core.GitT(10*time.Minute, "", "clone", "-q", "--no-checkout", p.Repo, tmp); err != nil {
		return p, fmt.Errorf("fetching %s: %w", p.Repo, err)
	}
	want := p.Sha
	if want == "" {
		if want = latestTag(tmp); want == "" {
			want = "HEAD"
		}
	}
	if _, err := core.Git(tmp, "checkout", "-q", want+"^{commit}"); err != nil {
		return p, fmt.Errorf("%s has no commit %s", p.Repo, want)
	}
	sha, err := core.Git(tmp, "rev-parse", "HEAD")
	if err != nil {
		return p, err
	}
	if hexRe.MatchString(p.Sha) && !strings.HasPrefix(sha, p.Sha) {
		return p, fmt.Errorf("%s at %s is commit %s, not the one pinned", p.Repo, p.Sha, sha)
	}
	p.Sha, p.Release = sha, releaseAt(tmp, sha)
	tree := filepath.Join(tmp, filepath.FromSlash(p.Path))
	raw, err := os.ReadFile(filepath.Join(tree, "module.json"))
	if err != nil {
		return p, fmt.Errorf("%s has no module.json in %q at %s", p.Repo, p.Path, sha[:12])
	}
	if err := json.Unmarshal(raw, &p.Manifest); err != nil {
		return p, fmt.Errorf("%s: module.json: %w", p.Repo, err)
	}
	if p.Name == "" {
		p.Name = p.Manifest.Name
		if !nameRe.MatchString(p.Name) {
			p.Name = NameFromURL(strings.TrimSuffix(p.Repo+"/"+p.Path, "/"))
		}
	}
	if !nameRe.MatchString(p.Name) {
		return p, fmt.Errorf("%q is not a module name", p.Name)
	}
	asks := p.Manifest.Needs()
	if p.Listed() {
		if extra := asks.Beyond(p.Needs); len(extra) > 0 {
			return p, fmt.Errorf("refused: %s at %s asks for reach its listing doesn't declare: %s", p.Name, sha[:12], strings.Join(extra, ", "))
		}
	} else {
		p.Needs = asks // unlisted: what it declares is what the person is shown and grants
	}
	if from != "" && from != sha {
		if _, err := core.Git(tmp, "cat-file", "-e", from+"^{commit}"); err != nil {
			p.Diff = fmt.Sprintf("The installed commit %s is no longer in %s: read the whole module.", from, p.Repo)
		} else {
			args := []string{"diff", from, sha}
			if p.Path != "" {
				args = append(args, "--", p.Path)
			}
			stat, _ := core.Git(tmp, append([]string{"diff", "--stat", from, sha}, args[3:]...)...)
			full, _ := core.Git(tmp, args...)
			p.Diff = core.Clip(stat+"\n\n"+full, 40000)
		}
	}
	if keep != nil {
		if err := keep(tree, p); err != nil {
			return p, err
		}
	}
	return p, nil
}

// releaseAt names a commit by its highest release tag, else its short sha.
func releaseAt(dir, sha string) string {
	out, _ := core.Git(dir, "tag", "--points-at", sha)
	best := ""
	for _, t := range strings.Fields(out) {
		if _, ok := parseSemver(t); ok && (best == "" || Newer(best, t)) {
			best = t
		}
	}
	if best == "" {
		best = sha[:min(7, len(sha))]
	}
	return best
}

// Install fetches a plan and moves it into .modules/ whole, with where it
// came from and the reach it was granted.
func (r *Registry) Install(p Plan) (Module, error) {
	if _, ok := r.Get(p.Name); ok && p.Name != "" {
		return Module{}, fmt.Errorf("a module named %s is already installed", p.Name)
	}
	return r.put(p, nil)
}

// Upgrade replaces an installed module with a plan's commit, keeping its grants.
func (r *Registry) Upgrade(p Plan) (Module, error) {
	m, ok := r.Get(p.Name)
	if !ok {
		return Module{}, core.ErrNotFound
	}
	return r.put(p, &m)
}

func (r *Registry) put(p Plan, prev *Module) (Module, error) {
	var tmp string
	from := ""
	if prev != nil {
		from = prev.Source.Commit
	}
	// The module is copied to a hidden folder and moves into place whole: List
	// reads any folder as a module, and a half-written one would show (and be
	// read by agents) without its files.
	got, err := r.fetch(p, from, func(tree string, p Plan) error {
		if prev == nil && core.Exists(filepath.Join(r.ws.Modules(), p.Name)) {
			return fmt.Errorf("a module named %s is already installed", p.Name)
		}
		tmp = filepath.Join(r.ws.Modules(), "."+p.Name+"-"+core.NewID())
		if err := CopyTree(tree, tmp); err != nil {
			return err
		}
		now := time.Now().UTC()
		needs := p.Needs
		s := Source{Source: p.Repo, Path: p.Path, Release: p.Release, Commit: p.Sha, Trust: p.Trust, Index: p.Index, Read: p.Read,
			Project: p.Project, Needs: &needs, Installed: now, Projects: []string{"*"}}
		if prev != nil {
			s.Installed, s.Updated, s.Projects = prev.Source.Installed, now, prev.Source.Projects
		}
		return writeSource(tmp, s)
	})
	if err != nil {
		if tmp != "" {
			os.RemoveAll(tmp)
		}
		return Module{}, fmt.Errorf("module %s: %w", orName(p), err)
	}
	dir := filepath.Join(r.ws.Modules(), got.Name)
	if prev != nil {
		old := filepath.Join(r.ws.Modules(), "."+got.Name+"-old-"+core.NewID())
		if err := os.Rename(prev.Dir, old); err != nil {
			os.RemoveAll(tmp)
			return Module{}, err
		}
		if err := os.Rename(tmp, dir); err != nil {
			os.Rename(old, prev.Dir)
			os.RemoveAll(tmp)
			return Module{}, err
		}
		os.RemoveAll(old)
		r.j.MustAppend("module.updated", map[string]string{"module": got.Name, "from": prev.Source.Release, "to": got.Release, "commit": got.Sha, "trust": got.Trust})
	} else {
		if err := os.Rename(tmp, dir); err != nil {
			os.RemoveAll(tmp)
			return Module{}, err
		}
		r.j.MustAppend("module.installed", map[string]string{"module": got.Name, "source": got.Repo, "path": got.Path, "release": got.Release, "commit": got.Sha, "trust": got.Trust})
	}
	m, _ := r.Get(got.Name)
	return m, nil
}

func orName(p Plan) string {
	if p.Name != "" {
		return p.Name
	}
	return p.Repo
}

// Remove uninstalls a module: its folder and its program's home. A standard
// module ships with the Hive and stays.
func (r *Registry) Remove(name string) error {
	m, ok := r.Get(name)
	if !ok {
		return core.ErrNotFound
	}
	if m.Source.Source == "builtin" {
		return fmt.Errorf("%s is a standard module: it ships with the Hive; take it from every project instead", name)
	}
	base := filepath.Base(m.Dir)
	gone := filepath.Join(r.ws.Modules(), "."+base+"-removed-"+core.NewID())
	if err := os.Rename(m.Dir, gone); err != nil {
		return err
	}
	os.RemoveAll(gone)
	os.RemoveAll(filepath.Join(r.ws.Hive(), "modules", base))
	r.j.MustAppend("module.removed", map[string]string{"module": base, "source": m.Source.Source, "commit": m.Source.Commit})
	return nil
}

// Skip records that an update to sha was declined, so it isn't asked again.
func (r *Registry) Skip(name, sha string) error {
	m, ok := r.Get(name)
	if !ok {
		return core.ErrNotFound
	}
	s := m.Source
	s.Skipped = sha
	return writeSource(m.Dir, s)
}

// Outdated fetches each installed module's newer commit, if it has one: the
// index's new pin for a listed module, the newest release (else HEAD) of an
// unlisted module's or project's repository. Each comes with the diff from the
// installed commit. A commit already declined is left out; notes say what
// could not be checked, and listings that were pulled.
func (r *Registry) Outdated() ([]Plan, []string) {
	var out []Plan
	var notes []string
	r.listings(true)
	for _, m := range r.List() {
		s := m.Source
		if s.Source == "" || s.Source == "builtin" {
			continue
		}
		name := filepath.Base(m.Dir)
		var p Plan
		switch {
		case s.Trust == "project":
			p = Plan{Name: name, Repo: s.Source, Trust: "project", Project: s.Project}
		case s.Trust == "core-team" || s.Trust == "reviewed":
			var err error
			if p, err = r.PlanFor(name); err != nil {
				notes = append(notes, name+": "+err.Error())
				continue
			}
			if !p.Listed() { // no longer listed by name: nothing vouches for a newer one
				continue
			}
		default: // unlisted, and modules cloned before indexes
			p = Plan{Name: name, Repo: s.Source, Path: s.Path, Trust: "unlisted"}
		}
		if p.Sha != "" && (p.Sha == s.Commit || p.Sha == s.Skipped) {
			continue
		}
		got, err := r.fetch(p, s.Commit, nil)
		if err != nil {
			notes = append(notes, name+": "+err.Error())
			continue
		}
		if got.Sha == s.Commit || got.Sha == s.Skipped {
			continue
		}
		if s.Needs != nil {
			if more := got.Needs.Beyond(*s.Needs); len(more) > 0 {
				got.Read = strings.TrimSpace(got.Read + " It asks for more reach than the installed commit: " + strings.Join(more, ", ") + ".")
			}
		}
		out = append(out, got)
	}
	return out, notes
}

// Undeclared lists what a module's own manifest asks for beyond the reach it
// was installed with; its program doesn't start while there is any. Modules
// installed before reach was recorded, and the person's own, have none on
// record and are not checked.
func (m Module) Undeclared() []string {
	if m.Source.Needs == nil {
		return nil
	}
	return m.Manifest.Needs().Beyond(*m.Source.Needs)
}

// CopyTree copies a module's regular files, without .git or its source
// record, keeping whether each is executable.
func CopyTree(from, to string) error {
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		if d.Name() == ".git" || d.Name() == ".hive-source.json" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(to, rel), 0o755)
		}
		if !d.Type().IsRegular() {
			return nil // a link could point anywhere on the machine
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if info, err := d.Info(); err == nil && info.Mode()&0o111 != 0 {
			mode = 0o755
		}
		return os.WriteFile(filepath.Join(to, rel), raw, mode)
	})
}
