// Package kb is the knowledge base: the Hive's memory across projects and
// runs. Notes are plain markdown files in .hive/kb, kept in a local git
// repository so a bad consolidation can be reverted. Agents read it directly
// (read-only) and write through `hive agent remember`; a scheduled filer
// consolidates it through the same take-commits-out path as any worker.
package kb

import (
	"crypto/sha256"
	"encoding/hex"
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
	m.filePractice()
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
	fresh := !core.Exists(filepath.Join(dir, ".git"))
	if fresh {
		core.Git(dir, "init", "-q", "-b", "main")
		if !core.Exists(filepath.Join(dir, "README.md")) {
			os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Knowledge base\n\nOne note per file. Written by agents with `hive agent remember`; consolidated by the filer.\n"), 0o644)
		}
	}
	// An isolated project's notes stay out of git, where history would keep them.
	os.MkdirAll(filepath.Join(dir, ".git", "info"), 0o755)
	os.WriteFile(filepath.Join(dir, ".git", "info", "exclude"), []byte("/isolated/\n"), 0o644)
	if fresh {
		core.Git(dir, "add", "-A")
		core.Git(dir, "commit", "-q", "-m", "Start the knowledge base")
	}
}

// moveIsolated moves notes an isolated project left in the shared knowledge
// base, from before they went in its own folder, into that folder. Earlier
// commits and the filer's old clones still hold them, so it then starts the
// knowledge base's history afresh from what it holds now; a clone made before
// shares no history with it after.
// It runs until every such note has moved, behind a marker in the Hive's own
// state where the filer can't write. A note whose name is taken in the folder
// moves under a free one, as Write picks it.
func (m *Module) moveIsolated() {
	marker := filepath.Join(m.C.WS.Hive(), "kb-isolated-moved")
	if core.Exists(marker) {
		return
	}
	kb := m.C.WS.KB()
	var moved []string
	left := false
	for _, name := range m.Notes() {
		b, _ := os.ReadFile(filepath.Join(kb, name))
		p, ok := m.isolatedIn(string(b))
		if !ok {
			continue
		}
		dir, err := m.C.WS.IsolatedKB(p)
		dst := filepath.Join(dir, name)
		for i := 2; core.Exists(dst); i++ {
			dst = filepath.Join(dir, fmt.Sprintf("%s-%d.md", strings.TrimSuffix(name, ".md"), i))
		}
		if err == nil && os.Rename(filepath.Join(kb, name), dst) == nil {
			moved = append(moved, name)
		} else {
			left = true
		}
	}
	if len(moved) > 0 {
		os.RemoveAll(filepath.Join(kb, ".git"))
		m.ensureRepo()
		old, _ := filepath.Glob(filepath.Join(m.C.WS.Hive(), "scratch", "filer-*"))
		for _, d := range old {
			os.RemoveAll(d)
		}
	}
	if !left {
		os.WriteFile(marker, nil, 0o644)
	}
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

// filePractice copies the kb module's practice docs (how a Hive works) into
// the store's practice/ folder, so a fresh install starts knowing them, and
// refreshes them when an update ships new text. practice/.filed.json keeps
// the hash of what was last filed, so a doc a person has edited since (or
// wrote before any filing) is never overwritten. Notes live at the top level,
// so a note never shares a path with a practice doc.
func (m *Module) filePractice() {
	src := filepath.Join(m.C.WS.Modules(), "kb", "practice")
	dst := filepath.Join(m.C.WS.KB(), "practice")
	ledger := filepath.Join(dst, ".filed.json")
	filed := map[string]string{}
	if raw, err := os.ReadFile(ledger); err == nil {
		_ = json.Unmarshal(raw, &filed)
	}
	entries, _ := os.ReadDir(src)
	changed := false
	var kept []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") || e.Name() == "README.md" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil || os.MkdirAll(dst, 0o755) != nil {
			continue
		}
		want := hashOf(raw)
		path := filepath.Join(dst, e.Name())
		have, err := os.ReadFile(path)
		switch {
		case err == nil && hashOf(have) == want:
			if filed[e.Name()] != want {
				filed[e.Name()], changed = want, true
			}
			continue
		case err == nil && hashOf(have) != filed[e.Name()]:
			kept = append(kept, e.Name()) // a person's text: leave it
			continue
		case err != nil && !os.IsNotExist(err):
			continue
		}
		if os.WriteFile(path, raw, 0o644) == nil {
			filed[e.Name()], changed = want, true
		}
	}
	if len(kept) > 0 {
		m.C.J.MustAppend("kb.practice", map[string]string{"result": "kept edited practice docs, not refreshed: " + strings.Join(kept, ", ")})
	}
	if !changed {
		return
	}
	if raw, err := json.MarshalIndent(filed, "", "  "); err == nil {
		_ = os.WriteFile(ledger, append(raw, '\n'), 0o644)
	}
	_, err := core.Git(m.C.WS.KB(), "add", "--", "practice")
	if err == nil {
		_, err = core.Git(m.C.WS.KB(), "commit", "-q", "-m", "File how a Hive works", "--", "practice")
	}
	if err != nil {
		m.C.J.MustAppend("kb.practice", map[string]string{"result": "practice docs not committed: " + err.Error()})
	}
}

func hashOf(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
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

// Notes lists note files, newest first; the practice/ folder isn't notes.
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
