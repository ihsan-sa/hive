package core

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Project is a repository the Hive works on.
type Project struct {
	Name        string    `json:"name"` // unique; also its folder under projects/
	Repo        string    `json:"repo"` // owner/name on the forge
	Branch      string    `json:"branch"`
	Description string    `json:"description,omitempty"`
	Isolated    bool      `json:"isolated,omitempty"` // walled both ways: sees no other project, and none sees it
	Private     bool      `json:"private,omitempty"`  // walled one way: reads the shared Hive, and none sees it
	Self        bool      `json:"self,omitempty"`     // the Hive's own source
	Demo        bool      `json:"demo,omitempty"`
	Goals       string    `json:"goals,omitempty"` // the person's words when they made it
	Ready       bool      `json:"ready,omitempty"` // cloned and set up; until then setup is retried
	Created     time.Time `json:"created"`
}

// The three levels of sharing, from most to least seen (docs/sharing.md).
const (
	SharingShared   = "shared"
	SharingPrivate  = "private"
	SharingIsolated = "isolated"
)

// Sharing names p's level. Isolated wins over private.
func (p Project) Sharing() string {
	switch {
	case p.Isolated:
		return SharingIsolated
	case p.Private:
		return SharingPrivate
	}
	return SharingShared
}

// Hidden says whether other projects' agents are kept from p: its records are
// covered in their sandboxes, and they can't reach its agents or file work in
// it, nor its agents theirs.
func (p Project) Hidden() bool { return p.Isolated || p.Private }

// SetSharing sets p's level from its name; "" is shared.
func (p *Project) SetSharing(level string) error {
	switch level {
	case "", SharingShared:
		p.Isolated, p.Private = false, false
	case SharingPrivate:
		p.Isolated, p.Private = false, true
	case SharingIsolated:
		p.Isolated, p.Private = true, false
	default:
		return fmt.Errorf("sharing is one of %s, %s or %s, not %q", SharingShared, SharingPrivate, SharingIsolated, level)
	}
	return nil
}

// ProjectConfig is .hive/project.json in the project's repository, read from main.
type ProjectConfig struct {
	Test      string   `json:"test,omitempty"`
	Score     *Score   `json:"score,omitempty"`
	Protected []string `json:"protected,omitempty"` // paths whose changes the person merges
	High      []string `json:"high,omitempty"`      // paths whose changes need two separate reads
	Root      []string `json:"root,omitempty"`      // paths that run with elevated rights: high, and the person says when they release
	SmallMax  int      `json:"small_max_lines,omitempty"`
	// UnreadMax is the size line under which a change that touches no
	// protected, high, root or security-listed path merges on its tests and
	// the checks with no reviewer seat (default DefaultUnreadMax; negative:
	// every change is read). SampleUnread is one in how many of those get one
	// read after merge (default DefaultSampleUnread; negative: none).
	// ReadTwoMax is the size line over which a read change gets a second,
	// separate read (default DefaultReadTwoMax); a root path or a reader's
	// unsure land asks for one at any size.
	UnreadMax    int    `json:"unread_max_lines,omitempty"`
	SampleUnread int    `json:"sample_unread,omitempty"`
	ReadTwoMax   int    `json:"second_read_lines,omitempty"`
	DryRun       string `json:"dryrun,omitempty"` // for self-releasing projects: one ask, end to end
}

// The read lines' defaults (ProjectConfig).
const (
	DefaultUnreadMax    = 30
	DefaultSampleUnread = 5
	DefaultReadTwoMax   = 300
)

// SelfRoot and SelfHigh are the paths of the Hive's own source that a self
// project guards whatever its project.json says; the gate's binary holds them,
// so no change on the branch can drop them. Root is what acts on the person's
// machine outside the box: the box itself, the approved scripts the risk
// module runs, the install and autostart, and the workflows that build every
// install's binary. High is what holds or hands out credentials, what
// replaces the running Hive, and the dependencies compiled into it.
var (
	SelfRoot = []string{"internal/sandbox/", "internal/mod/risk/", "internal/setup/", "install/", ".github/"}
	SelfHigh = []string{"internal/core/effects.go", "internal/core/forge.go", "internal/harness/", "internal/mod/release/", "internal/daemon/", "modules/agents/warden.md", "modules/agents/tribunal.md", "go.mod", "go.sum", "go.work", "go.work.sum", "vendor/"}
)

// SelfUnread is the allow-list of a self project's unread path: a change is
// unread only when every file is a doc (by type, as Small judges it) under
// one of these. Everything else (code, prompts, tests, root instruction files
// such as CLAUDE.md, AGENTS.md and LEARNINGS.md, and all of .hive/, whose
// lessons every reviewer reads as a rule) keeps a reader, whether or not a
// list names it, so no small edit moves its own bar unread.
var SelfUnread = []string{"docs/"}

// SelfSpec are the owner's spec under docs/: in a self project they keep a
// reader like docs/REVIEW.md and .hive/goals.md, outside SelfUnread.
var SelfSpec = []string{"docs/spec.md", "docs/the-hive-essay.md"}

// Sampled picks one task in n by its id for a read after an unread merge, so
// the pick survives a restart and needs no counter; none at n <= 0.
func Sampled(task string, n int) bool {
	if n <= 0 || task == "" {
		return false
	}
	sum := sha256.Sum256([]byte("sample " + task))
	return binary.BigEndian.Uint32(sum[:4])%uint32(n) == 0
}

// Guarded returns cfg with a self project's built-in classes added to its own.
func (cfg ProjectConfig) Guarded(p Project) ProjectConfig {
	if p.Self {
		cfg.Root = append(slices.Clone(SelfRoot), cfg.Root...)
		cfg.High = append(slices.Clone(SelfHigh), cfg.High...)
	}
	return cfg
}

// Score is one number, which way is better, and how much noise to tolerate.
type Score struct {
	Command   string   `json:"command"`
	Direction string   `json:"direction"` // higher | lower
	Tolerance float64  `json:"tolerance"`
	Paths     []string `json:"paths,omitempty"` // what else the score reads, beyond a ./ file in its command
}

// Evaluator returns the paths in the repository that make up the score: its
// paths and, as a best guess, any ./ file its command names. A change to one
// is the person's to merge, and changes the score's version, so it starts a
// new baseline. A path outside the repository can't be either, so it's left out.
func (s Score) Evaluator() []string {
	var out []string
	add := func(f string) {
		if f = path.Clean(f); f != "." && !path.IsAbs(f) && f != ".." && !strings.HasPrefix(f, "../") {
			out = append(out, f)
		}
	}
	for _, f := range s.Paths {
		add(f)
	}
	for _, f := range strings.Fields(s.Command) {
		if f = strings.Trim(f, `"'();&|<>`); strings.HasPrefix(f, "./") {
			add(f)
		}
	}
	return out
}

// Better reports whether a is at least as good as b within tolerance.
func (s Score) WithinTolerance(value, best float64) bool {
	if s.Direction == "lower" {
		return value <= best+s.Tolerance
	}
	return value >= best-s.Tolerance
}

// Beats reports whether value is better than best by more than the tolerance.
func (s Score) Beats(value, best float64) bool { return !s.WithinTolerance(best, value) }

// Best returns the better of a and b.
func (s Score) Best(a, b float64) float64 {
	if s.Direction == "lower" {
		return min(a, b)
	}
	return max(a, b)
}

// Projects is the registry, rebuilt from the journal.
type Projects struct {
	j  *Journal
	mu sync.Mutex
	m  map[string]Project
}

// NewProjects replays project records.
func NewProjects(j *Journal) *Projects {
	p := &Projects{j: j, m: map[string]Project{}}
	apply := func(e Event) {
		if e.Kind != "project.created" && e.Kind != "project.updated" {
			return
		}
		var pr Project
		e.Decode(&pr)
		p.mu.Lock()
		p.m[pr.Name] = pr
		p.mu.Unlock()
	}
	j.Replay(apply)
	j.Subscribe(apply)
	return p
}

var nameRe = regexp.MustCompile(`[^a-z0-9-]+`)

// UniqueName derives a unique project name; two repos with the same last
// path segment never collide.
func (p *Projects) UniqueName(want, repo string) string {
	base := strings.Trim(nameRe.ReplaceAllString(strings.ToLower(want), "-"), "-")
	if base == "" {
		parts := strings.Split(repo, "/")
		base = strings.Trim(nameRe.ReplaceAllString(strings.ToLower(parts[len(parts)-1]), "-"), "-")
	}
	if base == "" {
		base = "project"
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	name := base
	for i := 2; ; i++ {
		existing, taken := p.m[name]
		if !taken || existing.Repo == repo {
			return name
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
}

// Add records a project.
func (p *Projects) Add(pr Project) (Project, error) {
	if pr.Name == "" || pr.Repo == "" {
		return pr, fmt.Errorf("project needs a name and a repo")
	}
	if existing, ok := p.Get(pr.Name); ok {
		if existing.Repo != pr.Repo {
			return pr, fmt.Errorf("project %q already exists for %s", pr.Name, existing.Repo)
		}
		return existing, nil
	}
	if pr.Branch == "" {
		pr.Branch = "main"
	}
	pr.Created = time.Now().UTC()
	p.j.MustAppend("project.created", pr)
	return pr, nil
}

// Update rewrites a project record.
func (p *Projects) Update(pr Project) { p.j.MustAppend("project.updated", pr) }

// Get returns a project by name.
func (p *Projects) Get(name string) (Project, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pr, ok := p.m[name]
	return pr, ok
}

// List returns projects by name.
func (p *Projects) List() []Project {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []Project
	for _, pr := range p.m {
		out = append(out, pr)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ParseProjectConfig reads .hive/project.json content.
func ParseProjectConfig(raw string) ProjectConfig {
	var c ProjectConfig
	json.Unmarshal([]byte(raw), &c)
	if c.SmallMax == 0 {
		c.SmallMax = 40
	}
	if c.UnreadMax == 0 {
		c.UnreadMax = DefaultUnreadMax
	}
	if c.SampleUnread == 0 {
		c.SampleUnread = DefaultSampleUnread
	}
	if c.ReadTwoMax == 0 {
		c.ReadTwoMax = DefaultReadTwoMax
	}
	return c
}
