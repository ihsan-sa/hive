package kb

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/doc"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// SelfDir is where the Hive's own source sits in the knowledge base.
const SelfDir = "hive"

// SeedSelf writes the Hive's source and design into the knowledge base at the
// running version, with a source map, so every agent can read the code it
// runs. The daemon calls it at start, beside serving (on a loaded Windows
// runner it took over ten seconds), with new turns held until it ends; it
// rewrites and commits only when the source differs from what it last wrote,
// so an update (a new binary) reseeds. It writes a copy aside and moves it in
// whole, so a reader never sees half a version.
func (m *Module) SeedSelf(src fs.FS) {
	began := time.Now()
	defer func() { m.Seeded = time.Since(began) }()
	dir := filepath.Join(m.C.WS.KB(), SelfDir)
	h := sha256.New()
	fmt.Fprintln(h, core.Version)
	fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			raw, _ := fs.ReadFile(src, p)
			fmt.Fprintf(h, "%s %d\n", p, len(raw))
			h.Write(raw)
		}
		return err
	})
	sum := hex.EncodeToString(h.Sum(nil))
	if old, _ := os.ReadFile(filepath.Join(dir, ".source-hash")); string(old) == sum {
		return
	}
	stage := filepath.Join(m.C.WS.Hive(), "scratch", "kb-self")
	os.RemoveAll(stage) // a seed cut short
	defer os.RemoveAll(stage)
	fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := fs.ReadFile(src, p)
		if err == nil {
			os.MkdirAll(filepath.Join(stage, filepath.Dir(p)), 0o755)
			os.WriteFile(filepath.Join(stage, p), raw, 0o644)
		}
		return nil
	})
	os.WriteFile(filepath.Join(stage, "VERSION"), []byte(core.Version+"\n"), 0o644)
	os.WriteFile(filepath.Join(stage, "SOURCE-MAP.md"), []byte(sourceMap(src)), 0o644)
	os.WriteFile(filepath.Join(stage, ".source-hash"), []byte(sum), 0o644)
	m.mu.Lock()
	defer m.mu.Unlock()
	os.RemoveAll(dir)
	if err := os.Rename(stage, dir); err != nil {
		return // the next start seeds again: no hash was left
	}
	core.Git(m.C.WS.KB(), "add", "-A", SelfDir)
	core.Git(m.C.WS.KB(), "commit", "-q", "-m", "The Hive's own source at "+core.Version, "--", SelfDir)
}

// sourceMap lists the design docs and each Go package with its doc comment.
func sourceMap(src fs.FS) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# The Hive's own source, at %s\n\n", core.Version)
	b.WriteString("This folder is the Hive's repository at the version this install runs, rewritten when the Hive is updated. Read the code here to answer how the Hive works, and the live settings in `hive agent status` (`config`). The design: `docs/spec.md`, `docs/architecture.md`, `docs/risk.md`, `docs/status.md`. The default modules (role prompts) are in `modules/`.\n\n## Packages\n\n")
	synopsis := map[string]string{}
	fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		raw, _ := fs.ReadFile(src, p)
		f, err := parser.ParseFile(token.NewFileSet(), p, raw, parser.PackageClauseOnly|parser.ParseComments)
		if dirName := path.Dir(p); err == nil && f.Doc != nil {
			synopsis[dirName] = new(doc.Package).Synopsis(f.Doc.Text())
		} else if _, ok := synopsis[dirName]; !ok {
			synopsis[dirName] = ""
		}
		return nil
	})
	var dirs []string
	for d := range synopsis {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		fmt.Fprintf(&b, "- `%s`: %s\n", d, synopsis[d])
	}
	return b.String()
}
