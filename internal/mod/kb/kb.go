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
// It runs once, behind a marker in the Hive's own state where the filer can't
// write, and never replaces a note already in the folder.
func (m *Module) moveIsolated() {
	marker := filepath.Join(m.C.WS.Hive(), "kb-isolated-moved")
	if core.Exists(marker) {
		return
	}
	kb := m.C.WS.KB()
	var moved []string
	for _, name := range m.Notes() {
		b, _ := os.ReadFile(filepath.Join(kb, name))
		if p, ok := m.isolatedIn(string(b)); ok {
			dir, err := m.C.WS.IsolatedKB(p)
			if dst := filepath.Join(dir, name); err == nil && !core.Exists(dst) && os.Rename(filepath.Join(kb, name), dst) == nil {
				moved = append(moved, name)
			}
		}
	}
	if len(moved) > 0 {
		core.Git(kb, append([]string{"rm", "-q", "--cached", "--ignore-unmatch", "--"}, moved...)...)
		core.Git(kb, "commit", "-q", "-m", "Move isolated projects' notes out of the shared knowledge base")
	}
	os.WriteFile(marker, nil, 0o644)
}

// isolatedIn returns the isolated project a note's header names, if any.
func (m *Module) isolatedIn(note string) (string, bool) {
	head, _, _ := strings.Cut(note, "\n---\n")
	for _, line := range strings.Split(head, "\n") {
		proj, ok := strings.CutPrefix(line, "project: ")
		if p, isProj := m.C.Projects.Get(proj); ok && isProj && p.Isolated {
			return p.Name, true
		}
	}
	return "", false
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
		if dir, err = m.C.WS.IsolatedKB(n.Project); err != nil {
			return "", err
		}
	}
	body := fmt.Sprintf("---\ntitle: %s\ntags: %s\nproject: %s\ndate: %s\nrun: %s\n---\n\n%s\n", oneLine(n.Title), oneLine(n.Tags), n.Project, time.Now().Format("2006-01-02"), n.Run, strings.TrimSpace(n.Text))
	// O_EXCL never follows a link, even a dangling one, so a link left at a
	// note's name is passed over rather than written through.
	path := filepath.Join(dir, slug+".md")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	for i := 2; os.IsExist(err); i++ {
		path = filepath.Join(dir, fmt.Sprintf("%s-%d.md", slug, i))
		f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	}
	if err != nil {
		return "", err
	}
	_, err = f.WriteString(body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
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

var noteRe = regexp.MustCompile(`^[A-Za-z0-9._-]+\.md$`)

// checkFiled takes a filer's commits only when all they change is top-level
// notes, as plain files with plain names. Anything else, such as a folder
// that a case-insensitive disk folds onto isolated/, where isolated projects'
// notes sit untracked, a link, or a note whose header names an isolated
// project, is refused.
func (m *Module) checkFiled(sha string) error {
	kb := m.C.WS.KB()
	diff, err := core.Git(kb, "diff-tree", "-r", "-z", "--no-renames", "HEAD", sha)
	if err != nil {
		return err
	}
	f := strings.Split(diff, "\x00")
	for i := 0; i+1 < len(f); i += 2 {
		// ":oldmode newmode oldsha newsha status", then the path; a
		// deleted note's new mode is 000000.
		meta, p := strings.Fields(f[i]), f[i+1]
		if len(meta) < 2 || (meta[1] != "100644" && meta[1] != "000000") || !noteRe.MatchString(p) {
			return fmt.Errorf("refused: the filer touched %s", p)
		}
		if meta[1] == "100644" {
			b, _ := core.Git(kb, "show", sha+":"+p)
			if proj, ok := m.isolatedIn(b); ok {
				return fmt.Errorf("refused: the filer wrote %s for isolated project %s", p, proj)
			}
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
	if err := m.checkFiled(sha); err != nil {
		m.C.J.MustAppend("kb.filed", map[string]string{"run": id, "result": err.Error()})
		return
	}
	if _, err := core.Git(m.C.WS.KB(), "merge", "-q", "--ff-only", sha); err != nil {
		m.C.J.MustAppend("kb.filed", map[string]string{"run": id, "result": "not a fast-forward; new notes arrived meanwhile: " + err.Error()})
		return
	}
	m.C.J.MustAppend("kb.filed", map[string]string{"run": id, "result": "consolidated at " + core.Short(sha)})
}
