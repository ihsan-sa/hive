// Package kb is the knowledge base: the Hive's memory across projects and
// runs. Notes are plain markdown files in .hive/kb, kept in a local git
// repository so a bad consolidation can be reverted. Agents read it directly
// (read-only) and write through `hive agent remember`; a scheduled filer
// consolidates it through the same take-commits-out path as any worker.
package kb

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
)

// Note is one knowledge base entry.
type Note struct {
	Title   string `json:"title"`
	Text    string `json:"text"`
	Tags    string `json:"tags,omitempty"`
	Project string `json:"project,omitempty"`
	Run     string `json:"run,omitempty"`
}

// Module is the knowledge base.
type Module struct {
	C      *core.Core
	Agents *agents.Agents
	mu     sync.Mutex
	filing string // running filer
}

// New registers ops and makes sure the store is a repository.
func New(c *core.Core, a *agents.Agents) *Module {
	m := &Module{C: c, Agents: a}
	m.ensureRepo()
	m.moveIsolated()
	c.J.Replay(func(e core.Event) {
		var f struct{ Run string }
		e.Decode(&f)
		switch e.Kind {
		case "kb.filing":
			m.filing = f.Run
		case "kb.filed":
			if m.filing == f.Run {
				m.filing = ""
			}
		}
	})
	c.Runs.Ops().Register(core.Op{Name: "remember", Help: `{"title","text","tags"}`, Fn: func(r core.Run, args json.RawMessage) (any, error) {
		var n Note
		if err := core.Bind(args, &n); err != nil {
			return nil, err
		}
		n.Run, n.Project = r.ID, r.Project
		path, err := m.Write(n)
		if err != nil {
			return nil, err
		}
		return "saved as " + filepath.Base(path), nil
	}})
	c.J.Subscribe(func(e core.Event) {
		if e.Kind == "kb.note" {
			var n Note
			e.Decode(&n)
			go m.Write(n)
		}
	})
	return m
}

func (m *Module) ensureRepo() {
	dir := m.C.WS.KB()
	os.MkdirAll(dir, 0o755)
	if !core.Exists(filepath.Join(dir, ".git")) {
		core.Git(dir, "init", "-q", "-b", "main")
		os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Knowledge base\n\nOne note per file. Written by agents with `hive agent remember`; consolidated by the filer.\n"), 0o644)
		core.Git(dir, "add", "-A")
		core.Git(dir, "commit", "-q", "-m", "Start the knowledge base")
	}
	// An isolated project's notes stay out of git, where history would keep them.
	os.WriteFile(filepath.Join(dir, ".git", "info", "exclude"), []byte("/isolated/\n"), 0o644)
}

// moveIsolated moves notes an isolated project left in the shared knowledge
// base, from before they went in its own folder, into that folder, and takes
// them out of the repository from here on. Earlier commits still hold them.
func (m *Module) moveIsolated() {
	kb := m.C.WS.KB()
	var moved []string
	for _, name := range m.Notes() {
		b, _ := os.ReadFile(filepath.Join(kb, name))
		head, _, _ := strings.Cut(string(b), "\n---\n")
		for _, line := range strings.Split(head, "\n") {
			proj, ok := strings.CutPrefix(line, "project: ")
			if p, isProj := m.C.Projects.Get(proj); ok && isProj && p.Isolated {
				dir, err := m.isolatedDir(p.Name)
				if err == nil && os.Rename(filepath.Join(kb, name), filepath.Join(dir, name)) == nil {
					moved = append(moved, name)
				}
			}
		}
	}
	if len(moved) > 0 {
		core.Git(kb, append([]string{"rm", "-q", "--cached", "--ignore-unmatch", "--"}, moved...)...)
		core.Git(kb, "commit", "-q", "-m", "Move isolated projects' notes out of the shared knowledge base")
	}
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// Write saves a note and commits it.
func (m *Module) Write(n Note) (string, error) {
	if strings.TrimSpace(n.Title) == "" || strings.TrimSpace(n.Text) == "" {
		return "", fmt.Errorf("a note needs a title and text")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	slug := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(n.Title), "-"), "-")
	if len(slug) > 60 {
		slug = slug[:60]
	}
	// An isolated project's notes go in its own folder, which only isolated
	// runs see, and the filer never reads.
	dir := m.C.WS.KB()
	if p, ok := m.C.Projects.Get(n.Project); ok && p.Isolated {
		var err error
		if dir, err = m.isolatedDir(n.Project); err != nil {
			return "", err
		}
	}
	path := filepath.Join(dir, slug+".md")
	for i := 2; core.Exists(path); i++ {
		path = filepath.Join(dir, fmt.Sprintf("%s-%d.md", slug, i))
	}
	body := fmt.Sprintf("---\ntitle: %s\ntags: %s\nproject: %s\ndate: %s\nrun: %s\n---\n\n%s\n", oneLine(n.Title), oneLine(n.Tags), n.Project, time.Now().Format("2006-01-02"), n.Run, strings.TrimSpace(n.Text))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", err
	}
	if dir != m.C.WS.KB() {
		return path, nil
	}
	core.Git(m.C.WS.KB(), "add", filepath.Base(path))
	core.Git(m.C.WS.KB(), "commit", "-q", "-m", "Note: "+oneLine(n.Title))
	return path, nil
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// isolatedDir makes and returns an isolated project's notes folder, refusing
// one that isn't a real folder, such as a link the filer planted.
func (m *Module) isolatedDir(project string) (string, error) {
	dir := m.C.WS.KB()
	for _, d := range []string{"isolated", project} {
		dir = filepath.Join(dir, d)
		os.Mkdir(dir, 0o755)
		if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
			return "", fmt.Errorf("%s is not a real folder", dir)
		}
	}
	return dir, nil
}

// checkFiled refuses a filer's commits that would reach isolated projects'
// notes, which sit untracked in isolated/, or add a link anywhere.
func checkFiled(kb, sha string) error {
	names, err := core.Git(kb, "diff-tree", "-r", "-z", "--no-renames", "--name-only", "HEAD", sha)
	if err != nil {
		return err
	}
	for _, p := range strings.Split(names, "\x00") {
		if p == "isolated" || strings.HasPrefix(p, "isolated/") {
			return fmt.Errorf("refused: the filer touched %s", p)
		}
	}
	tree, err := core.Git(kb, "ls-tree", "-r", "-z", sha)
	if err != nil {
		return err
	}
	for _, e := range strings.Split(tree, "\x00") {
		if strings.HasPrefix(e, "120000 ") {
			return fmt.Errorf("refused: the filer added a link: %s", e)
		}
	}
	return nil
}

// Notes lists note files, newest first.
func (m *Module) Notes() []string {
	entries, _ := os.ReadDir(m.C.WS.KB())
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") && e.Name() != "README.md" {
			out = append(out, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

// File starts the filer on a copy of the knowledge base.
func (m *Module) File() (string, error) {
	m.mu.Lock()
	running := m.filing
	m.mu.Unlock()
	if running != "" {
		if r, ok := m.C.Runs.Get(running); ok && !r.Terminal() {
			return running, nil
		}
	}
	if len(m.Notes()) < 2 {
		return "", fmt.Errorf("fewer than two notes; nothing to consolidate")
	}
	id := "filer-" + core.NewID()
	dir := filepath.Join(m.C.WS.Hive(), "scratch", id)
	if _, err := core.Git("", "clone", "-q", "--no-local", m.C.WS.KB(), dir); err != nil {
		return "", err
	}
	base, _ := core.Git(m.C.WS.KB(), "rev-parse", "HEAD")
	spec := m.Agents.Spec(id, "filer", "", dir, "Consolidate the knowledge base in your checkout, commit, and finish.")
	spec.Inputs = map[string]string{"base": base}
	if _, err := m.C.Runs.Start(spec); err != nil {
		return "", err
	}
	m.mu.Lock()
	m.filing = id
	m.mu.Unlock()
	m.C.J.MustAppend("kb.filing", map[string]string{"run": id, "base": base})
	return id, nil
}

// Tick applies a finished filer's commits by fast-forward.
func (m *Module) Tick() {
	m.mu.Lock()
	id := m.filing
	m.mu.Unlock()
	if id == "" {
		return
	}
	r, ok := m.C.Runs.Get(id)
	if !ok || !r.Terminal() {
		return
	}
	m.mu.Lock()
	m.filing = ""
	m.mu.Unlock()
	if r.Status != core.StatusDone {
		return
	}
	base := r.Inputs["base"]
	sha, err := m.C.Effects.ExtractInto(m.C.WS.KB(), "kb", r, base)
	if err != nil {
		m.C.J.MustAppend("kb.filed", map[string]string{"run": id, "result": err.Error()})
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := checkFiled(m.C.WS.KB(), sha); err != nil {
		m.C.J.MustAppend("kb.filed", map[string]string{"run": id, "result": err.Error()})
		return
	}
	if _, err := core.Git(m.C.WS.KB(), "merge", "-q", "--ff-only", sha); err != nil {
		m.C.J.MustAppend("kb.filed", map[string]string{"run": id, "result": "not a fast-forward; new notes arrived meanwhile: " + err.Error()})
		return
	}
	m.C.J.MustAppend("kb.filed", map[string]string{"run": id, "result": "consolidated at " + core.Short(sha)})
}
