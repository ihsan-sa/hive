package gate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// A change the benchmark cannot see (a bug fix, a hardening) still does
// better when it demonstrably fixes something: a test of its own, new or
// changed in its diff, that passes on the candidate and fails on the control.
// The test is the candidate's on both sides, so all that differs between the
// two runs is the code outside the tests. See docs/gate.md, "Fixes".

// fixRounds is how often each side runs each test; every round must agree.
const fixRounds = 2

// maxFixTests is the most of the candidate's tests the step tries.
const maxFixTests = 20

// Fix is one of the candidate's own tests that passes on it and fails on the
// control, running code its diff changed.
type Fix struct {
	Package string `json:"package"`
	Test    string `json:"test"`
	Covers  string `json:"covers"` // a changed line it runs: file:line
}

func (f Fix) String() string {
	return fmt.Sprintf("fixes: the candidate's test %s %s passes on it and fails on the control (%d/%d each), running the change at %s", f.Package, f.Test, fixRounds, fixRounds, f.Covers)
}

// FixReport is the step: which of the candidate's tests were tried, which
// count, and why the others don't.
type FixReport struct {
	Tests []string `json:"tests,omitempty"`
	Fixes []Fix    `json:"fixes,omitempty"`
	Notes []string `json:"notes,omitempty"`
}

// fixTest is a test the candidate's diff adds or changes.
type fixTest struct{ dir, name string }

func (t fixTest) String() string { return safeName(t.dir) + " " + t.name }

func safeName(s string) string { return clip(unsafeName.ReplaceAllString(s, "_"), 80) }

// findFixes runs the candidate's new and changed tests on both sides. Both
// runs happen in one sandbox of their own, at one path, with one fresh home
// each time: once on the candidate's tree, once on the control's tree with
// the candidate's test files of the test's package (and its testdata) in
// place of the control's. A test counts only if, every round, it passes on
// the candidate and fails, by name, on the control, and on the candidate it
// runs a line the diff changed outside the tests. A test that does not build
// or run on the control (it calls a new function) is no evidence either way.
func findFixes(cfg *Config, cand, ctrl *Child) (fr FixReport) {
	note := func(f string, a ...any) { fr.Notes = append(fr.Notes, fmt.Sprintf(f, a...)) }
	if cand.Src == "" || ctrl.Src == "" || cfg.Base == "" || cfg.Head == "" {
		note("prebuilt binaries: no diff to read")
		return
	}
	mod := modulePath(cand.Src)
	if mod == "" {
		note("the candidate's go.mod names no module")
		return
	}
	head := cand.Commit // the tree the candidate was built from: its head with the base merged in
	if head == "" {
		head = cfg.Head
	}
	from := cfg.Base
	if out, err := gitRun(cand.Src, "git", "merge-base", cfg.Base, head); err == nil && strings.TrimSpace(out) != "" {
		from = strings.TrimSpace(out) // the candidate's own changes, not main's since it branched
	}
	code, tests, err := changedGo(cand.Src, from, head)
	switch {
	case err != nil:
		note("reading the diff: %v", err)
		return
	case len(code) == 0:
		note("the change touches no Go code outside its tests")
		return
	case len(tests) == 0:
		note("the change adds or changes no test")
		return
	}
	touched, err := touchedLines(cand.Src, from, head, code)
	if err != nil {
		note("reading the diff: %v", err)
		return
	}
	var tried []fixTest
	for _, f := range tests {
		for _, name := range changedTests(cand.Src, from, f) {
			t := fixTest{path.Dir(f), name}
			if !slices.Contains(tried, t) {
				tried = append(tried, t)
			}
		}
	}
	if len(tried) > maxFixTests {
		note("%d tests changed; the first %d were tried", len(tried), maxFixTests)
		tried = tried[:maxFixTests]
	}
	if len(tried) == 0 {
		note("the change adds or changes no test function")
		return
	}
	for _, t := range tried {
		fr.Tests = append(fr.Tests, t.String())
	}
	var cover []string
	for _, f := range code {
		if p := importPath(mod, path.Dir(f)); !slices.Contains(cover, p) {
			cover = append(cover, p)
		}
	}
	overlay := map[string]bool{}
	for _, t := range tried {
		overlay[t.dir] = true
	}

	lab := &Child{Name: "fixes", Root: filepath.Join(cfg.Out, "fixes"), Sandbox: cand.Sandbox, Go: cand.Go, Meter: cand.Meter, Fwd: cand.Fwd}
	defer os.RemoveAll(lab.Root)
	defer os.RemoveAll(lab.Tmp())
	type tally struct {
		pass, fail int
		norun      bool // did not build or run: no evidence
		covers     string
	}
	cands, ctrls := map[fixTest]*tally{}, map[fixTest]*tally{}
	for _, t := range tried {
		cands[t], ctrls[t] = &tally{}, &tally{}
	}
	deadline := time.Now().Add(45 * time.Minute)
	first := time.Now().UnixNano()%2 == 0 // which side starts: neither may count on going first
	for round := 0; round < fixRounds; round++ {
		for _, onCand := range []bool{first, !first} {
			if time.Now().After(deadline) {
				note("out of time after %d rounds", round)
				return
			}
			src, ov := ctrl.Src, overlay
			if onCand {
				src, ov = cand.Src, nil
			}
			if err := lab.reset(src, cand.Src, ov); err != nil {
				note("preparing the tests' tree: %v", err)
				return
			}
			for _, t := range tried {
				pkg := importPath(mod, t.dir)
				args := []string{"go", "test", "-short", "-count=1", "-json", "-run", "^" + regexp.QuoteMeta(t.name) + "$"}
				profile := filepath.Join(lab.Root, "cover", "cover.out")
				if onCand {
					args = append(args, "-coverpkg="+strings.Join(cover, ","), "-coverprofile="+profile)
				}
				args = append(args, "./"+t.dir)
				out := lab.test(args)
				reap(cfg.Out, true)
				got := testOutcome(out, pkg, t.name)
				if onCand {
					if got == "pass" {
						cands[t].pass++
					}
					if raw, err := core.SafeRead(profile, 64<<20); err == nil && cands[t].covers == "" {
						cands[t].covers = coversChange(raw, mod, touched)
					}
					os.Remove(profile)
				} else if got == "fail" {
					ctrls[t].fail++
				} else if got == "" {
					ctrls[t].norun = true
				} else {
					ctrls[t].pass++
				}
			}
		}
	}
	for _, t := range tried {
		c, k := cands[t], ctrls[t]
		switch {
		case c.pass < fixRounds:
			note("%s: does not pass on the candidate every time (%d/%d)", t, c.pass, fixRounds)
		case k.norun:
			note("%s: does not build or run on the control: no evidence", t)
		case k.fail < fixRounds:
			note("%s: passes on the control too", t)
		case c.covers == "":
			note("%s: fails on the control but runs none of the lines the change touches: not counted", t)
		default:
			fr.Fixes = append(fr.Fixes, Fix{Package: safeName(t.dir), Test: t.name, Covers: c.covers})
		}
	}
	return fr
}

// reset puts a fresh tree of src at the lab's one path, with the test files
// and testdata of the dirs in overlay taken from tests, and empties the
// lab's home and scratch; the build cache stays.
func (c *Child) reset(src, tests string, overlay map[string]bool) error {
	for _, d := range []string{"tree", "cover", "home", "tmp", "gopath", "hive"} {
		p := filepath.Join(c.Root, d)
		if err := os.RemoveAll(p); err != nil {
			makeWritable(p)
			if err := os.RemoveAll(p); err != nil {
				return err
			}
		}
	}
	if err := c.Prepare(); err != nil {
		return err
	}
	tree := filepath.Join(c.Root, "tree")
	if err := copyTree(src, tree); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(c.Root, "cover"), 0o755); err != nil {
		return err
	}
	for dir := range overlay {
		to, from := filepath.Join(tree, filepath.FromSlash(dir)), filepath.Join(tests, filepath.FromSlash(dir))
		if fi, err := os.Stat(to); err != nil || !fi.IsDir() {
			continue // no such package on this side: the test can't run here
		}
		old, _ := filepath.Glob(filepath.Join(to, "*_test.go"))
		for _, f := range old {
			os.Remove(f)
		}
		os.RemoveAll(filepath.Join(to, "testdata"))
		files, _ := filepath.Glob(filepath.Join(from, "*_test.go"))
		for _, f := range files {
			if err := copyFile(f, filepath.Join(to, filepath.Base(f))); err != nil {
				return err
			}
		}
		if exists(filepath.Join(from, "testdata")) {
			if err := copyTree(filepath.Join(from, "testdata"), filepath.Join(to, "testdata")); err != nil {
				return err
			}
		}
	}
	return nil
}

// test runs go test in the lab's tree, offline, and returns its output.
func (c *Child) test(args []string) string {
	defer c.with(offline...)()
	out, _ := c.Exec(10*time.Minute, filepath.Join(c.Root, "tree"), args...)
	return out
}

// copyTree copies a checkout without its .git: dirs, regular files and
// symlinks as they are.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() && rel == ".git" {
			return filepath.SkipDir
		}
		to := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(to, 0o755)
		case d.Type()&os.ModeSymlink != 0:
			l, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(l, to)
		case d.Type().IsRegular():
			return copyFile(p, to)
		}
		return nil
	})
}

// makeWritable gives the owner write on every dir under p, so a tree a test
// left read-only can go.
func makeWritable(p string) {
	filepath.WalkDir(p, func(q string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			os.Chmod(q, 0o755)
		}
		return nil
	})
}

func importPath(mod, dir string) string {
	if dir == "." || dir == "" {
		return mod
	}
	return mod + "/" + dir
}

// changedGo is the Go files from..to added or changed: code, and tests.
// Vendored code and testdata are neither.
func changedGo(src, from, to string) (code, tests []string, err error) {
	out, err := gitRun(src, "git", "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "-z", "--name-status", from, to)
	if err != nil {
		return nil, nil, fmt.Errorf("%v: %s", err, tail(out, 300))
	}
	f := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
	for i := 0; i+1 < len(f); i += 2 {
		st, p := f[i], f[i+1]
		if (st != "A" && st != "M") || !strings.HasSuffix(p, ".go") || !utf8.ValidString(p) {
			continue
		}
		parts := strings.Split(p, "/")
		if slices.Contains(parts, "testdata") || slices.Contains(parts, "vendor") || slices.ContainsFunc(parts, func(s string) bool { return strings.HasPrefix(s, ".") || strings.HasPrefix(s, "_") }) {
			continue // the go command ignores these
		}
		if strings.HasSuffix(p, "_test.go") {
			tests = append(tests, p)
		} else {
			code = append(code, p)
		}
	}
	return code, tests, nil
}

// hunk is the lines of a file, in the candidate's numbering, its diff touched.
type hunk struct{ from, to int }

var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// touchedLines is, per file, the lines from..to added or changed; where lines
// were only removed, the two lines around the cut.
func touchedLines(src, from, to string, files []string) (map[string][]hunk, error) {
	args := append([]string{"diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--no-color", "-U0", "--src-prefix=a/", "--dst-prefix=b/", from, to, "--"}, files...)
	out, err := gitRun(src, "git", args...)
	if err != nil {
		return nil, fmt.Errorf("%v: %s", err, tail(out, 300))
	}
	t := map[string][]hunk{}
	file := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "+++ ") {
			file = strings.TrimPrefix(strings.TrimPrefix(l, "+++ "), "b/")
			continue
		}
		m := hunkHeader.FindStringSubmatch(l)
		if m == nil || file == "" {
			continue
		}
		start, _ := strconv.Atoi(m[1])
		n := 1
		if m[2] != "" {
			n, _ = strconv.Atoi(m[2])
		}
		h := hunk{start, start + n - 1}
		if n == 0 {
			h = hunk{start, start + 1}
		}
		t[file] = append(t[file], h)
	}
	return t, nil
}

// changedTests is the test functions of a test file that from did not have,
// or had with another text.
func changedTests(src, from, file string) []string {
	now, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(file)))
	if err != nil {
		return nil
	}
	before := map[string]string{}
	if old, err := gitRun(src, "git", "show", from+":"+file); err == nil {
		before = testFuncs([]byte(old))
	}
	var names []string
	for name, body := range testFuncs(now) {
		if b, ok := before[name]; !ok || b != body {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// testFuncs is the source of each test function of a file, by name: read,
// never run.
func testFuncs(src []byte) map[string]string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	fs := map[string]string{}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Type.Params == nil || len(fn.Type.Params.List) != 1 || !isTestName(fn.Name.Name) {
			continue
		}
		var b bytes.Buffer
		printer.Fprint(&b, fset, fn)
		fs[fn.Name.Name] = b.String()
	}
	return fs
}

func isTestName(n string) bool {
	if !strings.HasPrefix(n, "Test") || n == "TestMain" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(n[4:])
	return len(n) == 4 || !unicode.IsLower(r)
}

// testOutcome is how test ended in go test -json output: "pass", "fail",
// "skip", or "" when it never ran (its package did not build).
func testOutcome(out, pkg, test string) string {
	got := ""
	for _, l := range strings.Split(out, "\n") {
		var e testEvent
		if !strings.HasPrefix(l, "{") || json.Unmarshal([]byte(l), &e) != nil || e.Package != pkg || e.Test != test {
			continue
		}
		if e.Action == "pass" || e.Action == "fail" || e.Action == "skip" {
			got = e.Action
		}
	}
	return got
}

var coverLine = regexp.MustCompile(`^(.+\.go):(\d+)\.\d+,(\d+)\.\d+ \d+ (\d+)$`)

// coversChange is the first changed line a coverage profile shows run, as
// file:line, or "".
func coversChange(profile []byte, mod string, touched map[string][]hunk) string {
	for _, l := range strings.Split(string(profile), "\n") {
		m := coverLine.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil || m[4] == "0" || !strings.HasPrefix(m[1], mod+"/") {
			continue
		}
		file := strings.TrimPrefix(m[1], mod+"/")
		a, _ := strconv.Atoi(m[2])
		b, _ := strconv.Atoi(m[3])
		for _, h := range touched[file] {
			if a <= h.to && h.from <= b {
				return fmt.Sprintf("%s:%d", safeName(file), max(a, h.from))
			}
		}
	}
	return ""
}

// markdown is the step as a report section.
func (fr FixReport) markdown() string {
	if len(fr.Tests) == 0 && len(fr.Notes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## The candidate's own tests on both sides\n\n")
	for _, f := range fr.Fixes {
		fmt.Fprintf(&b, "- counted: %s\n", f)
	}
	for _, n := range fr.Notes {
		fmt.Fprintf(&b, "- %s\n", n)
	}
	return b.String()
}
