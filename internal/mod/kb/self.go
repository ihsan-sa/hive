package kb

import (
	"context"
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
// whole, so a reader never sees half a version. A cancelled ctx stops it
// before the swap, leaving the old copy. A swap that fails (Windows refuses
// to move a folder a reader has a file open in) puts the old copy back and
// is journaled as kb.self_failed; the next start tries again.
func (m *Module) SeedSelf(ctx context.Context, src fs.FS) {
	began := time.Now()
	defer func() { m.Seeded = time.Since(began) }()
	kb := m.C.WS.KB()
	dir := filepath.Join(kb, SelfDir)
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
	// Both sit in the knowledge base, so the swap is a rename on one
	// filesystem; git, and search, leave them out. What a seed cut short, or
	// a reader holding a file, left goes first.
	stage, aside := filepath.Join(kb, selfStage), filepath.Join(kb, selfAside)
	os.RemoveAll(stage)
	os.RemoveAll(aside)
	if old, _ := os.ReadFile(filepath.Join(dir, ".source-hash")); string(old) == sum {
		return
	}
	defer os.RemoveAll(stage)
	write := func(p string, raw []byte) error {
		if err := os.MkdirAll(filepath.Join(stage, filepath.Dir(p)), 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(stage, p), raw, 0o644)
	}
	err := fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		raw, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		return write(p, raw)
	})
	for _, f := range []struct{ name, text string }{{"VERSION", core.Version + "\n"}, {"SOURCE-MAP.md", sourceMap(src)}, {".source-hash", sum}} {
		if err == nil {
			err = write(f.name, []byte(f.text))
		}
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		if ctx.Err() == nil {
			m.C.J.MustAppend("kb.self_failed", map[string]string{"version": core.Version, "error": "writing the copy: " + err.Error()})
		}
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// The old copy is set aside in one rename and deleted after the new one
	// is in, so a failure leaves a whole copy, old or new, never half of one.
	had := core.Exists(dir)
	if had {
		if err := core.RenameOver(dir, aside); err != nil {
			m.C.J.MustAppend("kb.self_failed", map[string]string{"version": core.Version, "error": "setting the old copy aside: " + err.Error()})
			return
		}
	}
	if err := core.RenameOver(stage, dir); err != nil {
		if had {
			core.RenameOver(aside, dir)
		}
		m.C.J.MustAppend("kb.self_failed", map[string]string{"version": core.Version, "error": "moving the new copy in: " + err.Error()})
		return
	}
	os.RemoveAll(aside) // what a reader still holds goes at the next start
	core.Git(kb, "add", "-A", SelfDir)
	core.Git(kb, "commit", "-q", "-m", "The Hive's own source at "+core.Version, "--", SelfDir)
}

// Where SeedSelf writes its new copy and sets the old one aside, inside the
// knowledge base; git excludes both (ensureRepo) and Search skips them.
const (
	selfStage = ".hive-new"
	selfAside = ".hive-old"
)

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
