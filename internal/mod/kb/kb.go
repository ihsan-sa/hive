// Package kb is the knowledge base: the Hive's memory across projects and
// runs. Notes are plain markdown files in .hive/kb, kept in a local git
// repository so a bad consolidation can be reverted. Agents read it directly
// (read-only), search it with `hive agent search` and write through `hive
// agent remember`; a scheduled filer consolidates the notes through the same
// take-commits-out path as any worker. Every journal event is also filed
// under kb/record on its own (record.go).
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
	c.Runs.Ops().Register(core.Op{Name: "search", Help: `{"query", "limit": "n or all"}`, Fn: func(r core.Run, args json.RawMessage) (any, error) {
		var q struct {
			Query string
			Limit json.RawMessage
		}
		if err := core.Bind(args, &q); err != nil {
			return nil, err
		}
		return Find(c.WS.KB(), q.Query, strings.Trim(string(q.Limit), `"`)), nil
	}})
	m.startRecord()
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
		os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Knowledge base\n\nOne note per file, written by agents with `hive agent remember` and consolidated by the filer. `record/` holds every event the Hive journaled, one file per task, request, run, chat or project, filed on its own.\n"), 0o644)
		core.Git(dir, "add", "-A")
		core.Git(dir, "commit", "-q", "-m", "Start the knowledge base")
	}
	if ignore := filepath.Join(dir, ".gitignore"); !core.Exists(ignore) {
		os.WriteFile(ignore, []byte("record/\n"), 0o644)
		core.Git(dir, "add", ".gitignore")
		core.Git(dir, "commit", "-q", "-m", "Keep the record, rebuilt from the journal, out of the notes' history")
	}
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
	path := filepath.Join(m.C.WS.KB(), slug+".md")
	for i := 2; core.Exists(path); i++ {
		path = filepath.Join(m.C.WS.KB(), fmt.Sprintf("%s-%d.md", slug, i))
	}
	body := fmt.Sprintf("---\ntitle: %s\ntags: %s\nproject: %s\ndate: %s\nrun: %s\n---\n\n%s\n", oneLine(n.Title), oneLine(n.Tags), n.Project, time.Now().Format("2006-01-02"), n.Run, strings.TrimSpace(n.Text))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", err
	}
	core.Git(m.C.WS.KB(), "add", filepath.Base(path))
	core.Git(m.C.WS.KB(), "commit", "-q", "-m", "Note: "+oneLine(n.Title))
	return path, nil
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

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
	if _, err := core.Git(m.C.WS.KB(), "merge", "-q", "--ff-only", sha); err != nil {
		m.C.J.MustAppend("kb.filed", map[string]string{"run": id, "result": "not a fast-forward; new notes arrived meanwhile: " + err.Error()})
		return
	}
	m.C.J.MustAppend("kb.filed", map[string]string{"run": id, "result": "consolidated at " + core.Short(sha)})
}
