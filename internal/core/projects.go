package core

import (
	"encoding/json"
	"fmt"
	"regexp"
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
	Isolated    bool      `json:"isolated,omitempty"`
	Self        bool      `json:"self,omitempty"` // the Hive's own source
	Demo        bool      `json:"demo,omitempty"`
	Goals       string    `json:"goals,omitempty"` // the person's words when they made it
	Ready       bool      `json:"ready,omitempty"` // cloned and set up; until then setup is retried
	Created     time.Time `json:"created"`
}

// ProjectConfig is .hive/project.json in the project's repository, read from main.
type ProjectConfig struct {
	Test      string   `json:"test,omitempty"`
	Score     *Score   `json:"score,omitempty"`
	Protected []string `json:"protected,omitempty"` // paths whose changes the person merges
	SmallMax  int      `json:"small_max_lines,omitempty"`
	DryRun    string   `json:"dryrun,omitempty"` // for self-releasing projects: one ask, end to end
}

// Score is one number, which way is better, and how much noise to tolerate.
type Score struct {
	Command   string  `json:"command"`
	Direction string  `json:"direction"` // higher | lower
	Tolerance float64 `json:"tolerance"`
}

// Better reports whether a is at least as good as b within tolerance.
func (s Score) WithinTolerance(value, best float64) bool {
	if s.Direction == "lower" {
		return value <= best+s.Tolerance
	}
	return value >= best-s.Tolerance
}

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
	return c
}
