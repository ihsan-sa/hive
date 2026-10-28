// Package goscope narrows a Go module's check to the packages a change can
// affect: those holding a changed file, those that import them, and those
// whose tests or embeds name a changed path ("../../cmd/hive",
// filepath.Join("..", "..", ".github"), "." for a test walking its own
// folder), each closed over what imports it.
// A package outside that set builds and tests exactly as it does on the
// change's base, so its result there stands.
//
// It reads the checkout's files and nothing else: no go command runs, so it
// is safe on a commit's own content.
package goscope

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

// Affected names, as "./dir" patterns ("." for the module root), the
// packages of the module at dir whose vet or tests the changed files (slash
// paths relative to dir) can affect. ok is false when it can't tell (no
// go.mod, a changed go.mod, go.sum or vendor folder, nothing to narrow to),
// and everything should run.
func Affected(dir string, changed []string) (pkgs []string, ok bool) {
	mod := modulePath(dir)
	if mod == "" || len(changed) == 0 {
		return nil, false
	}
	for _, f := range changed {
		if base := path.Base(f); f == base && strings.HasPrefix(base, "go.") && !strings.HasSuffix(base, ".go") ||
			slices.Contains(strings.Split(f, "/"), "vendor") {
			return nil, false
		}
	}
	g, err := load(dir, mod)
	if err != nil {
		return nil, false
	}
	// code: a package whose build changed, which every importer's build
	// shares. test: one whose vet or tests may differ: its build, its test
	// files, a file in its folder, a path its tests read or build.
	code, test := map[string]bool{}, map[string]bool{}
	for _, f := range changed {
		d := g.home(f)
		if strings.HasSuffix(f, ".go") && !strings.HasSuffix(f, "_test.go") {
			// Its folder's build changed even if no package is left there
			// (deleted or moved): what still imports it no longer builds.
			code[path.Dir(f)] = true
		}
		switch {
		case d == "":
		case strings.HasSuffix(f, ".go") && !strings.HasSuffix(f, "_test.go") && path.Dir(f) == d:
			code[d] = true
		default:
			test[d] = true
		}
		for p, ps := range g.embeds {
			if slices.ContainsFunc(ps, func(q string) bool { return under(f, q) }) {
				code[p] = true
			}
		}
	}
	for grew := true; grew; {
		grew = false
		for p, ims := range g.imports {
			if !code[p] && slices.ContainsFunc(ims, func(q string) bool { return code[q] }) {
				code[p], grew = true, true
			}
		}
	}
	built := slices.Collect(maps.Keys(code)) // a test that builds one reads it
	for p := range g.pkgs {
		switch {
		case code[p], slices.ContainsFunc(g.testImports[p], func(q string) bool { return code[q] }):
			test[p] = true
		case slices.ContainsFunc(g.reads[p], func(q string) bool {
			return slices.ContainsFunc(changed, func(f string) bool { return under(f, q) }) ||
				slices.ContainsFunc(built, func(c string) bool { return under(c, q) })
		}):
			test[p] = true
		}
	}
	for p := range test {
		if g.pkgs[p] {
			pkgs = append(pkgs, pattern(p))
		}
	}
	slices.Sort(pkgs)
	return pkgs, len(pkgs) > 0
}

// home is the package folder holding the slash path f: its own folder or the
// nearest above it with a package, or "" when none does.
func (g *graph) home(f string) string {
	for d := path.Dir(f); ; d = path.Dir(d) {
		if g.pkgs[d] {
			return d
		}
		if d == "." {
			return ""
		}
	}
}

func pattern(rel string) string {
	if rel == "." {
		return "."
	}
	return "./" + rel
}

// under says the slash path f is q or inside it; "." holds everything.
func under(f, q string) bool {
	return q == "." || f == q || strings.HasPrefix(f, q+"/")
}

var goStep = regexp.MustCompile(`^go (vet|test)((?: -[\w.=:,/-]+)*) \./\.\.\.$`)

// Narrow rewrites a check command made only of "go vet ./..." and "go test
// [flags] ./..." steps joined by && to run on pkgs instead. ok is false for
// any other command, which runs as it is.
func Narrow(command string, pkgs []string) (string, bool) {
	if len(pkgs) == 0 {
		return "", false
	}
	steps := strings.Split(command, "&&")
	for i, s := range steps {
		s = strings.Join(strings.Fields(s), " ")
		if !goStep.MatchString(s) {
			return "", false
		}
		steps[i] = strings.TrimSuffix(s, "./...") + strings.Join(pkgs, " ")
	}
	return strings.Join(steps, " && "), true
}

// Misfired says a narrowed command's failure is the narrowing's (a listed
// folder the go command found no package in), not the commit's.
func Misfired(out string) bool {
	return strings.Contains(out, "build constraints exclude all Go files") ||
		strings.Contains(out, "no Go files in") || strings.Contains(out, "matched no packages")
}

const maxGoMod = 1 << 20

func modulePath(dir string) string {
	// The commit's own file, read before any sandbox: a regular file only
	// (not a symlink out of the checkout, a FIFO or a device), and no more
	// than any real go.mod holds.
	p := filepath.Join(dir, "go.mod")
	if fi, err := os.Lstat(p); err != nil || !fi.Mode().IsRegular() || fi.Size() > maxGoMod {
		return ""
	}
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxGoMod+1))
	if err != nil || len(b) > maxGoMod {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Fields(l); len(f) >= 2 && f[0] == "module" {
			if m, err := strconv.Unquote(f[1]); err == nil {
				return m
			}
			return f[1]
		}
	}
	return ""
}

type graph struct {
	pkgs        map[string]bool     // folders (slash, relative) "./..." holds a package in
	imports     map[string][]string // a package's own imports inside the module
	testImports map[string][]string // its test files' imports inside the module
	embeds      map[string][]string // module paths it embeds
	reads       map[string][]string // module paths its tests name: read, or built
}

func load(dir, mod string) (*graph, error) {
	g := &graph{pkgs: map[string]bool{}, imports: map[string][]string{}, testImports: map[string][]string{},
		embeds: map[string][]string{}, reads: map[string][]string{}}
	ctx := build.Default
	if runtime.GOOS == "windows" {
		ctx.GOOS = "linux" // checks run in a Linux sandbox there
	}
	ctx.CgoEnabled = false
	fset := token.NewFileSet()
	consts := map[string]map[string]string{} // a package's string constants
	tests := map[string][]*ast.File{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			n := d.Name()
			if p != dir && (strings.HasPrefix(n, ".") || strings.HasPrefix(n, "_") || n == "testdata" || n == "vendor" || exists(filepath.Join(p, "go.mod"))) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || !d.Type().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(dir, filepath.Dir(p))
		rel = filepath.ToSlash(rel)
		if ok, err := ctx.MatchFile(filepath.Dir(p), d.Name()); err == nil && ok {
			g.pkgs[rel] = true
		}
		f, err := parser.ParseFile(fset, p, nil, parser.ParseComments)
		if err != nil {
			// A file that doesn't parse fails its package's build, which a
			// changed file there already tests; its edges are unknown.
			return nil
		}
		isTest := strings.HasSuffix(p, "_test.go")
		ims := g.imports
		if isTest {
			ims = g.testImports
		}
		for _, im := range f.Imports {
			ip, _ := strconv.Unquote(im.Path.Value)
			switch {
			case ip == mod:
				ims[rel] = append(ims[rel], ".")
			case strings.HasPrefix(ip, mod+"/"):
				ims[rel] = append(ims[rel], strings.TrimPrefix(ip, mod+"/"))
			}
		}
		for _, q := range embeds(f) {
			addPath(g.embeds, rel, q)
		}
		if consts[rel] == nil {
			consts[rel] = map[string]string{}
		}
		stringConsts(f, consts[rel])
		if isTest {
			tests[rel] = append(tests[rel], f)
		}
		return nil
	})
	for rel, fs := range tests {
		for _, f := range fs {
			named := func(q string) bool {
				full := path.Join(rel, filepath.ToSlash(q))
				return q != "" && !filepath.IsAbs(q) && full != ".." && !strings.HasPrefix(full, "../") &&
					exists(filepath.Join(dir, filepath.FromSlash(full)))
			}
			for _, q := range testPaths(f, consts[rel], named) {
				addPath(g.reads, rel, q)
			}
		}
	}
	return g, err
}

// addPath records under package rel the path q, relative to its folder, cut
// before its first glob element. Paths leaving the module are dropped.
func addPath(to map[string][]string, rel, q string) {
	var keep []string
	for _, e := range strings.Split(filepath.ToSlash(q), "/") {
		if strings.ContainsAny(e, "*?[") {
			break
		}
		keep = append(keep, e)
	}
	full := path.Join(rel, strings.Join(keep, "/"))
	if full == ".." || strings.HasPrefix(full, "../") {
		return
	}
	to[rel] = append(to[rel], full)
}

func embeds(f *ast.File) []string {
	var out []string
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			rest, ok := strings.CutPrefix(c.Text, "//go:embed ")
			if !ok {
				continue
			}
			for _, w := range strings.Fields(rest) {
				if u, err := strconv.Unquote(w); err == nil {
					w = u
				}
				out = append(out, strings.TrimPrefix(w, "all:"))
			}
		}
	}
	return out
}

func stringConsts(f *ast.File, into map[string]string) {
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, sp := range gd.Specs {
			vs := sp.(*ast.ValueSpec)
			for i, n := range vs.Names {
				if i < len(vs.Values) {
					if b, ok := vs.Values[i].(*ast.BasicLit); ok && b.Kind == token.STRING {
						if v, err := strconv.Unquote(b.Value); err == nil {
							into[n.Name] = v
						}
					}
				}
			}
		}
	}
}

// testPaths is every path a test file names: above its own folder (a string
// starting "../", or just ".."), or in it or below it where named says that
// path is there ("." for the whole folder, "docs/core.md"); a path.Join or
// filepath.Join counts as far as its arguments are known: literals, the
// package's string constants, FromSlash, ToSlash, Clean and Abs of known
// paths, and local names given one ("root := filepath.Join("..", "..")").
// Such a name counts where it is joined onto or passed to Rel; used any
// other way, the test reads what it names.
func testPaths(f *ast.File, consts map[string]string, named func(string) bool) []string {
	var out []string
	alias := map[string]string{}
	var known func(e ast.Expr) (string, bool)
	known = func(e ast.Expr) (string, bool) {
		switch e := e.(type) {
		case *ast.BasicLit:
			if e.Kind == token.STRING {
				s, err := strconv.Unquote(e.Value)
				return s, err == nil
			}
		case *ast.Ident:
			if v, ok := alias[e.Name]; ok {
				return v, true
			}
			v, ok := consts[e.Name]
			return v, ok
		case *ast.ParenExpr:
			return known(e.X)
		case *ast.CallExpr:
			name := callName(e)
			switch {
			case len(e.Args) == 1 && (name == "FromSlash" || name == "ToSlash" || name == "Clean" || name == "Abs"):
				return known(e.Args[0])
			case name == "Join" && len(e.Args) > 0:
				var parts []string
				for _, a := range e.Args {
					s, ok := known(a)
					if !ok {
						break
					}
					parts = append(parts, filepath.ToSlash(s))
				}
				if len(parts) == len(e.Args) {
					return path.Join(parts...), true
				}
			}
		}
		return "", false
	}
	above := func(s string) bool {
		if t := filepath.ToSlash(s); t == ".." || strings.HasPrefix(t, "../") {
			return true
		}
		return named(s) // a path in its own folder or below that is there
	}
	// Join's known head: as far as its arguments are known.
	head := func(c *ast.CallExpr) string {
		var parts []string
		for _, a := range c.Args {
			s, ok := known(a)
			if !ok {
				break
			}
			parts = append(parts, filepath.ToSlash(s))
		}
		if len(parts) == 0 {
			return ""
		}
		return path.Join(parts...)
	}
	// Names first, so a use before its line in the file still resolves.
	defs := map[ast.Node]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		if a, ok := n.(*ast.AssignStmt); ok && len(a.Rhs) == 1 {
			if id, ok := a.Lhs[0].(*ast.Ident); ok {
				if v, ok := known(a.Rhs[0]); ok && above(v) {
					alias[id.Name] = v
					defs[a.Rhs[0]], defs[id] = true, true
				}
			}
		}
		return true
	})
	var visit func(n ast.Node) bool
	visit = func(n ast.Node) bool {
		if defs[n] {
			return false
		}
		switch n := n.(type) {
		case *ast.CallExpr:
			switch callName(n) {
			case "Join":
				if h := head(n); above(h) {
					out = append(out, h)
				}
				for _, a := range n.Args {
					switch a.(type) {
					case *ast.BasicLit, *ast.Ident: // part of the head, or no path
					default:
						ast.Inspect(a, visit)
					}
				}
				return false
			case "Rel":
				return false
			}
		case *ast.Ident:
			if v, ok := alias[n.Name]; ok {
				out = append(out, v)
			}
		case *ast.BasicLit:
			if s, ok := known(n); ok && above(s) {
				out = append(out, s)
			}
		}
		return true
	}
	ast.Inspect(f, visit)
	return out
}

func callName(c *ast.CallExpr) string {
	switch f := c.Fun.(type) {
	case *ast.SelectorExpr:
		return f.Sel.Name
	case *ast.Ident:
		return f.Name
	}
	return ""
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}
