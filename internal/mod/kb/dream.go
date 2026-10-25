package kb

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// The kb module's dream program (modules/kb/dream) consolidates the whole
// knowledge base on its own schedule. It runs as a module program, outside the
// daemon, so all the host keeps is this seam: it hands the program the shared
// notes (the "kb" hook's GET /api/kb/notes) and applies the pass the program
// worked out (POST /api/kb/dream), checked like a filer's commit and committed
// in one go. What to merge, link, distil and retire is the program's.

// Shared is a note the dream program may read, with its path from the
// knowledge base's root.
type Shared struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

// SharedNotes is what GET /api/kb/notes returns: the top-level notes, the
// practice docs, the dream digests, and the hidden projects, whose events a
// program granted every project still reads and must keep out of the notes.
type SharedNotes struct {
	Notes   []Shared `json:"notes"`
	Shipped []string `json:"shipped"` // practice docs the module files, never edited by a pass
	Hidden  []string `json:"hidden"`
}

// SharedNotes lists the shared notes for the dream program. Isolated notes and the
// record are never in it.
func (m *Module) SharedNotes() SharedNotes {
	kb := m.C.WS.KB()
	var out SharedNotes
	for _, dir := range []string{"", "practice", dreamDir} {
		entries, _ := os.ReadDir(filepath.Join(kb, dir))
		for _, e := range entries {
			if !e.Type().IsRegular() || !noteRe.MatchString(e.Name()) || e.Name() == "README.md" {
				continue
			}
			p := path.Join(dir, e.Name())
			raw, err := os.ReadFile(filepath.Join(kb, filepath.FromSlash(p)))
			if err != nil {
				continue
			}
			out.Notes = append(out.Notes, Shared{Path: p, Text: string(raw)})
		}
	}
	out.Shipped = m.shipped()
	for _, p := range m.C.Projects.List() {
		if p.Hidden() {
			out.Hidden = append(out.Hidden, p.Name)
		}
	}
	return out
}

// shipped names the practice docs filePractice keeps, as practice/<name>.
func (m *Module) shipped() []string {
	var out []string
	raw, _ := os.ReadFile(filepath.Join(m.C.WS.KB(), "practice", ".filed.json"))
	var filed map[string]string
	json.Unmarshal(raw, &filed)
	for name := range filed {
		if strings.HasSuffix(name, ".md") {
			out = append(out, "practice/"+name)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(m.C.WS.Modules(), "kb", "practice"))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			out = append(out, "practice/"+e.Name())
		}
	}
	sort.Strings(out)
	return compact(out)
}

func compact(s []string) []string {
	var out []string
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// dreamDir holds each pass's digest, "what the Hive learned since last time".
const dreamDir = "dreams"

// FileChange is one file a pass writes or removes. Was is the sha256 of the text
// the program read ("" for a new file), so a note that changed since it read
// it (a new remember, the filer) refuses the pass rather than losing that.
type FileChange struct {
	Path   string `json:"path"`
	Text   string `json:"text,omitempty"`
	Was    string `json:"was,omitempty"`
	Remove bool   `json:"remove,omitempty"`
}

// Pass is one consolidation pass, as the program sends it: the files and
// what it did, which the journal keeps as kb.dreamed.
type Pass struct {
	Trigger   string       `json:"trigger"` // scheduled | quiet | asked
	Changes   []FileChange `json:"changes"`
	Merged    []Merge      `json:"merged"`
	Linked    [][2]string  `json:"linked"`
	Distilled []string     `json:"distilled"`
	Retired   []Retire     `json:"retired"`
	Digest    string       `json:"digest"`
	Seq       int64        `json:"seq"` // the last event the pass read
}

// Merge is notes folded into one living note.
type Merge struct {
	Into string   `json:"into"`
	From []string `json:"from"`
}

// Retire is a note marked retired, and why.
type Retire struct {
	Path string `json:"path"`
	Why  string `json:"why"`
}

var dreamPathRe = regexp.MustCompile(`^(?:(practice|` + dreamDir + `)/)?[A-Za-z0-9._-]+\.md$`)

// Dream checks a pass and commits it. A pass may write top-level notes,
// practice docs other than the ones the module ships, and digests under
// dreams/; it may remove notes, which it does when it merges them into
// another, and old digests, but no practice doc. A note whose header names a hidden project, a path that
// is not a plain file, or a file changed since the program read it refuses
// the whole pass, and nothing is written.
func (m *Module) Dream(module string, p Pass) (string, error) {
	kb := m.C.WS.KB()
	shipped := map[string]bool{}
	for _, s := range m.shipped() {
		shipped[s] = true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[string]bool{}
	for _, c := range p.Changes {
		switch {
		case !dreamPathRe.MatchString(c.Path) || c.Path == "README.md" || seen[c.Path]:
			return "", fmt.Errorf("refused: a pass can't write %q", c.Path)
		case shipped[c.Path]:
			return "", fmt.Errorf("refused: %s is the module's own practice doc", c.Path)
		case c.Remove && strings.HasPrefix(c.Path, "practice/"):
			return "", fmt.Errorf("refused: a pass removes notes and digests only, not %s", c.Path)
		case !c.Remove && strings.TrimSpace(c.Text) == "":
			return "", fmt.Errorf("refused: %s would be empty", c.Path)
		}
		seen[c.Path] = true
		if proj, ok := m.isolatedIn(c.Text); ok && !c.Remove {
			return "", fmt.Errorf("refused: %s names hidden project %s", c.Path, proj)
		}
		file := filepath.Join(kb, filepath.FromSlash(c.Path))
		fi, err := os.Lstat(file)
		switch {
		case err == nil && !fi.Mode().IsRegular():
			return "", fmt.Errorf("refused: %s is not a plain file", c.Path)
		case err != nil && !os.IsNotExist(err):
			return "", err
		case err != nil && c.Was != "":
			return "", fmt.Errorf("stale: %s is gone since the pass read it", c.Path)
		case err == nil:
			raw, err := os.ReadFile(file)
			if err != nil {
				return "", err
			}
			if hashOf(raw) != c.Was {
				return "", fmt.Errorf("stale: %s changed since the pass read it", c.Path)
			}
		}
	}
	var wrote, removed []string
	for _, c := range p.Changes {
		file := filepath.Join(kb, filepath.FromSlash(c.Path))
		var err error
		if c.Remove {
			err = os.Remove(file)
			removed = append(removed, c.Path)
		} else if err = os.MkdirAll(filepath.Dir(file), 0o755); err == nil {
			err = os.WriteFile(file, []byte(c.Text), 0o644)
			wrote = append(wrote, c.Path)
		}
		if err != nil {
			return "", err // what was written stays uncommitted; git shows it
		}
	}
	result := "nothing to change"
	if len(wrote)+len(removed) > 0 {
		// Staged by hand, then committed as staged: a removed note that was
		// never committed has no path for git to match. Nothing else stages
		// in the knowledge base while m.mu is held.
		msg := fmt.Sprintf("Dream (%s): merged %d, linked %d, distilled %d, retired %d", orDash(p.Trigger), len(p.Merged), len(p.Linked), len(p.Distilled), len(p.Retired))
		var err error
		if len(wrote) > 0 {
			_, err = core.Git(kb, append([]string{"add", "--"}, wrote...)...)
		}
		if err == nil && len(removed) > 0 {
			_, err = core.Git(kb, append([]string{"rm", "-q", "--cached", "--ignore-unmatch", "--"}, removed...)...)
		}
		if err == nil {
			_, err = core.Git(kb, "commit", "-q", "--allow-empty", "-m", msg)
		}
		if err != nil {
			return "", err
		}
		sha, _ := core.Git(kb, "rev-parse", "HEAD")
		result = "committed " + core.Short(sha)
	}
	m.C.J.MustAppend("kb.dreamed", map[string]any{"module": module, "trigger": p.Trigger, "merged": p.Merged, "linked": p.Linked,
		"distilled": p.Distilled, "retired": p.Retired, "digest": p.Digest, "seq": p.Seq, "files": len(wrote) + len(removed), "result": result})
	return result, nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// LatestDigest is the newest pass's digest under dir, "what the Hive learned
// since last time", for the Knowledge tab to pin at its top. Digests are named
// by when their pass ran, so the last name is the newest.
func LatestDigest(dir string) (Change, bool) {
	entries, _ := os.ReadDir(filepath.Join(dir, dreamDir))
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if !e.Type().IsRegular() || !noteRe.MatchString(e.Name()) {
			continue
		}
		p := dreamDir + "/" + e.Name()
		raw, err := core.SafeRead(filepath.Join(dir, dreamDir, e.Name()), 8<<10)
		fi, ferr := e.Info()
		if err != nil || ferr != nil {
			continue
		}
		return Change{Path: p, Title: titleOf(string(raw), p), At: fi.ModTime()}, true
	}
	return Change{}, false
}
