package gate

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// TestFailure is one thing a side's vet or short tests failed on: a test, or
// a package that failed without naming one (its build, a panic, a timeout,
// a vet report).
type TestFailure struct {
	Step    string `json:"step"` // "go vet" or "go test -short"
	Package string `json:"package"`
	Test    string `json:"test,omitempty"`
	Excerpt string `json:"excerpt,omitempty"` // the end of its output: the side's own words, data only
}

// Name is the failure as a report names it: the package and the test.
func (f TestFailure) Name() string {
	n := f.Package
	if f.Test != "" {
		n += " " + f.Test
	}
	if f.Step == "go vet" {
		n += " (vet)"
	}
	return n
}

func (f TestFailure) key() string { return f.Step + "\x00" + f.Package + "\x00" + f.Test }

// UnitReport is the vet and short-test step of a run, both sides.
type UnitReport struct {
	Candidate []TestFailure `json:"candidate,omitempty"` // what failed on the candidate's first run
	Control   []TestFailure `json:"control,omitempty"`
	Shared    []TestFailure `json:"shared,omitempty"` // failed on both sides: the environment's, never the candidate's
	Flaky     []TestFailure `json:"flaky,omitempty"`  // failed on the candidate alone, then passed when run again
	Own       []TestFailure `json:"own,omitempty"`    // failed on the candidate alone, again: its own
}

// unitFailure is a side's vet or short tests failing, with what failed.
type unitFailure struct {
	failures []TestFailure
	out      string // the end of the output, when nothing could be named
}

func (u *unitFailure) Error() string {
	if len(u.failures) == 0 {
		return "vet or short tests failed, naming no test: " + tail(u.out, 600)
	}
	var b strings.Builder
	for i, f := range u.failures {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(f.Name())
		if f.Excerpt != "" {
			b.WriteString(": " + tail(f.Excerpt, 300))
		}
	}
	return b.String()
}

// unitTest runs vet and the short tests in the side's sandbox, its checkout
// and binary read-only. The side's own code (its tests, its init functions)
// runs only like this, with the child's empty HOME and no host login; never
// through gitRun. Both run whatever the other gives, so a vet failure the
// control shares still leaves the tests measured.
func unitTest(c *Child) error {
	defer c.with(offline...)()
	u := &unitFailure{}
	vet, verr := c.Exec(30*time.Minute, c.Src, "go", "vet", "./...")
	if verr != nil {
		u.failures = append(u.failures, vetFailures(vet, modulePath(c.Src))...)
		u.out = vet
	}
	test, terr := c.Exec(30*time.Minute, c.Src, "go", "test", "-short", "-json", "./...")
	if terr != nil {
		fs, stray := testFailures(test)
		u.failures = append(u.failures, fs...)
		if len(fs) == 0 {
			u.out = strings.TrimSpace(u.out + "\n" + stray + "\n" + terr.Error())
		}
	}
	if verr == nil && terr == nil {
		return nil
	}
	return u
}

// rerun runs the short tests of fs again on the side, once each and
// uncached: the failures that remain. A run that fails naming nothing keeps
// what it was asked for, and says so (unnamed).
func rerun(c *Child, fs []TestFailure) (left []TestFailure, unnamed bool) {
	defer c.with(offline...)()
	var named, whole []string
	byPkg := map[string]bool{}
	var vetPkgs []string
	for _, f := range fs {
		switch {
		case f.Step == "go vet":
			vetPkgs = append(vetPkgs, f.Package)
		case f.Test == "":
			whole = append(whole, f.Package)
		default:
			top, _, _ := strings.Cut(f.Test, "/")
			if !slices.Contains(named, top) {
				named = append(named, top)
			}
			byPkg[f.Package] = true
		}
	}
	if len(vetPkgs) > 0 {
		// vet reads code, not the machine: run once more only to be sure.
		args := append([]string{"go", "vet"}, vetPkgs...)
		if out, err := c.Exec(30*time.Minute, c.Src, args...); err != nil {
			got := vetFailures(out, modulePath(c.Src))
			if len(got) == 0 { // it failed and named nothing: the vet failures stand
				unnamed = true
				got = slices.DeleteFunc(slices.Clone(fs), func(f TestFailure) bool { return f.Step != "go vet" })
			}
			left = append(left, got...)
		}
	}
	run := func(pkgs []string, pattern string) {
		if len(pkgs) == 0 {
			return
		}
		args := []string{"go", "test", "-short", "-count=1", "-json"}
		if pattern != "" {
			args = append(args, "-run", pattern)
		}
		args = append(args, pkgs...)
		out, err := c.Exec(30*time.Minute, c.Src, args...)
		if err == nil {
			return
		}
		got, _ := testFailures(out)
		if len(got) == 0 {
			// It failed and named nothing: everything asked for still stands.
			unnamed = true
			for _, f := range fs {
				if f.Step != "go vet" && slices.Contains(pkgs, f.Package) {
					got = append(got, f)
				}
			}
		}
		left = append(left, got...)
	}
	var pkgs []string
	for p := range byPkg {
		if !slices.Contains(whole, p) {
			pkgs = append(pkgs, p)
		}
	}
	slices.Sort(pkgs)
	if len(named) > 0 {
		var q []string
		for _, n := range named {
			q = append(q, regexp.QuoteMeta(n))
		}
		run(pkgs, "^("+strings.Join(q, "|")+")$")
	}
	run(whole, "")
	return left, unnamed
}

// testEvent is a line of go test -json.
type testEvent struct {
	Action      string
	Package     string
	ImportPath  string // build-output, build-fail
	Test        string
	Output      string
	FailedBuild string
}

// testFailures reads go test -json output: the tests that failed, each with
// the end of its output, and the packages that failed with no test failing
// (their build, a panic outside a test, a timeout). Lines that aren't events
// (the go command's own errors) come back as stray.
func testFailures(out string) (fs []TestFailure, stray string) {
	logs := map[string][]string{} // package \x00 test, and the build's ImportPath
	failed := map[string]bool{}   // packages with a failing test
	var pkgFails []TestFailure
	var strayLines []string
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		var e testEvent
		if !strings.HasPrefix(line, "{") || json.Unmarshal([]byte(line), &e) != nil {
			if strings.TrimSpace(line) != "" {
				strayLines = append(strayLines, line)
			}
			continue
		}
		switch e.Action {
		case "output":
			k := e.Package + "\x00" + e.Test
			logs[k] = keepLast(append(logs[k], strings.TrimRight(e.Output, "\n")), 40)
		case "build-output":
			k := "build\x00" + e.ImportPath
			logs[k] = keepLast(append(logs[k], strings.TrimRight(e.Output, "\n")), 40)
		case "fail":
			if e.Test != "" {
				fs = append(fs, TestFailure{Step: "go test -short", Package: e.Package, Test: e.Test, Excerpt: excerpt(logs[e.Package+"\x00"+e.Test])})
				failed[e.Package] = true
				continue
			}
			lines := logs[e.Package+"\x00"]
			if e.FailedBuild != "" {
				lines = logs["build\x00"+e.FailedBuild]
			}
			pkgFails = append(pkgFails, TestFailure{Step: "go test -short", Package: e.Package, Excerpt: excerpt(lines)})
		}
	}
	for _, p := range pkgFails {
		if !failed[p.Package] {
			fs = append(fs, p)
		}
	}
	// A parent test fails with its subtest: the subtest names it.
	fs = slices.DeleteFunc(fs, func(f TestFailure) bool {
		return f.Test != "" && slices.ContainsFunc(fs, func(g TestFailure) bool { return g.Package == f.Package && strings.HasPrefix(g.Test, f.Test+"/") })
	})
	return fs, strings.Join(keepLast(strayLines, 20), "\n")
}

// vetHeader is the line go vet prints above a package's reports; a package's
// tests are bracketed ("# [p]").
var vetHeader = regexp.MustCompile(`^# \[?([^\s\]]+)\]?`)

// vetFile is a report line naming its file, relative to the module's root.
var vetFile = regexp.MustCompile(`^(?:vet: )?(\S+\.go):\d+`)

// vetFailures reads go vet's output: each package it reported on, with its
// reports. A run that vets one package prints no header: a report there is
// put on the package of its file, in module mod.
func vetFailures(out, mod string) []TestFailure {
	var fs []TestFailure
	headed := false
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		pkg := ""
		if m := vetHeader.FindStringSubmatch(line); m != nil {
			pkg, headed = m[1], true
		} else if m := vetFile.FindStringSubmatch(line); m != nil && !headed && mod != "" && !filepath.IsAbs(m[1]) {
			pkg = path.Join(mod, path.Dir(filepath.ToSlash(m[1])))
		}
		if pkg != "" && (len(fs) == 0 || fs[len(fs)-1].Package != pkg) {
			fs = append(fs, TestFailure{Step: "go vet", Package: pkg})
		}
		if vetHeader.MatchString(line) {
			continue
		}
		if len(fs) > 0 && strings.TrimSpace(line) != "" {
			last := &fs[len(fs)-1]
			last.Excerpt = strings.TrimSpace(last.Excerpt + "\n" + line)
		}
	}
	for i := range fs {
		fs[i].Excerpt = tail(fs[i].Excerpt, 1500)
	}
	return fs
}

// fenced is text in a code fence no line of it closes.
func fenced(text string) string {
	fence := "```"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	return fence + "\n" + text + "\n" + fence
}

func keepLast(lines []string, n int) []string {
	if len(lines) > n {
		return lines[len(lines)-n:]
	}
	return lines
}

func excerpt(lines []string) string {
	return tail(strings.Join(keepLast(lines, 20), "\n"), 1500)
}

// judgeUnit decides whose the failures of the vet and short-test step are.
// A failure the control shares, by package and test, is the environment's:
// it is reported and the run goes on. One on the candidate alone is run
// again on both sides: passing then on the candidate, it was a flake;
// failing then on the control too, it is shared; failing on the candidate
// alone again, it is the candidate's own (a rollback, naming it). A step
// that fails naming nothing can't be compared: on the candidate alone, run
// again; on both, the gate could not run.
func judgeUnit(cand, ctrl *Child, errs map[*Child]error, mod string) (UnitReport, error) {
	var u UnitReport
	ce, ke := errs[cand], errs[ctrl]
	cu, _ := ce.(*unitFailure)
	ku, _ := ke.(*unitFailure)
	if cu != nil {
		u.Candidate = cu.failures
	}
	if ku != nil {
		u.Control = ku.failures
	}
	if ce == nil {
		return u, nil // the control failing alone measures nothing of the candidate's
	}
	if cu == nil || (ke != nil && ku == nil) {
		return u, whose(cand, ctrl, errs, "fails its own vet or short tests")
	}
	if len(cu.failures) == 0 {
		if ku != nil && len(ku.failures) == 0 {
			return u, base{fmt.Errorf("both sides fail their vet or short tests naming no test, so the step itself cannot run here: the candidate: %v; the control: %v", cu, ku)}
		}
		// Nothing named: the whole step once more.
		if again := unitTest(cand); again != nil {
			ag, _ := again.(*unitFailure)
			if ag == nil || len(ag.failures) == 0 {
				return u, &candidateFault{"fails its own vet or short tests, naming no test", fmt.Errorf("the candidate fails its own vet or short tests, twice: %v", again)}
			}
			cu = ag
			u.Candidate = ag.failures
		} else {
			return u, nil
		}
	}
	inCtrl := map[string]bool{}
	if ku != nil {
		for _, f := range ku.failures {
			inCtrl[f.key()] = true
		}
	}
	var only []TestFailure
	for _, f := range cu.failures {
		if inCtrl[f.key()] {
			u.Shared = append(u.Shared, f)
		} else {
			only = append(only, f)
		}
	}
	if len(only) > 0 {
		still, _ := rerun(cand, only)
		stillKey := map[string]bool{}
		for _, f := range still {
			stillKey[f.key()] = true
		}
		var again []TestFailure
		for _, f := range only {
			if stillKey[f.key()] || (f.Test == "" && slices.ContainsFunc(still, func(g TestFailure) bool { return g.Package == f.Package })) {
				again = append(again, f)
			} else {
				u.Flaky = append(u.Flaky, f)
			}
		}
		// Failures that appeared only on the rerun are named too.
		for _, g := range still {
			if !slices.ContainsFunc(again, func(f TestFailure) bool { return f.key() == g.key() || (f.Test == "" && f.Package == g.Package) }) {
				again = append(again, g)
			}
		}
		// A package the control doesn't have can't fail there: its failures
		// are the candidate's own, and asking the control would only fail to
		// load it, naming nothing.
		again = slices.DeleteFunc(again, func(f TestFailure) bool {
			if hasPackage(ctrl.Src, mod, f.Package) {
				return false
			}
			u.Own = append(u.Own, f)
			return true
		})
		if len(again) > 0 {
			// Shared only as the same failure: a package the candidate breaks
			// whole is not excused by one test the control fails in it.
			onCtrl, unnamed := rerun(ctrl, again)
			if unnamed {
				// The control failed naming nothing: that shows no failure
				// shared, and none of the candidate's own either.
				return u, base{fmt.Errorf("the control fails the candidate's failures run again on it naming no test, so the step cannot be judged here: %s", (&unitFailure{failures: again}).Error())}
			}
			for _, f := range again {
				if slices.ContainsFunc(onCtrl, func(g TestFailure) bool { return g.key() == f.key() }) {
					u.Shared = append(u.Shared, f)
				} else {
					u.Own = append(u.Own, f)
				}
			}
		}
	}
	if len(u.Own) > 0 {
		names := failureNames(u.Own, mod)
		return u, &candidateFault{"fails its own vet or short tests: " + names, fmt.Errorf("the candidate fails its own vet or short tests where the control passes them, twice: %s", (&unitFailure{failures: u.Own}).Error())}
	}
	return u, nil
}

// unsafeName is what failureNames keeps out of a package or test name: only the
// characters Go allows in them, so the candidate's output carries no prose
// into the verdict a worker reads.
var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_./#()\-]`)

// failureNames is a short list of failures, safe for a verdict: package (the
// module's prefix cut) and test, at most five.
func failureNames(fs []TestFailure, mod string) string {
	var names []string
	for i, f := range fs {
		if i == 5 {
			names = append(names, fmt.Sprintf("and %d more", len(fs)-5))
			break
		}
		pkg := strings.TrimPrefix(strings.TrimPrefix(f.Package, mod), "/")
		if pkg == "" {
			pkg = "."
		}
		n := clip(unsafeName.ReplaceAllString(pkg, "_"), 80)
		if f.Test != "" {
			n += " " + clip(unsafeName.ReplaceAllString(f.Test, "_"), 80)
		}
		if f.Step == "go vet" {
			n += " (vet)"
		}
		names = append(names, n)
	}
	return strings.Join(names, ", ")
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// hasPackage says whether src, the checkout of module mod, holds the Go
// package pkg: a folder with a .go file in it. A package outside mod (or a
// side with no module path) is taken to be there.
func hasPackage(src, mod, pkg string) bool {
	if src == "" || mod == "" || (pkg != mod && !strings.HasPrefix(pkg, mod+"/")) {
		return true
	}
	files, _ := filepath.Glob(filepath.Join(src, filepath.FromSlash(strings.TrimPrefix(pkg, mod)), "*.go"))
	return len(files) > 0
}

// modulePath is the module go.mod in src names, or "".
func modulePath(src string) string {
	raw, err := os.ReadFile(filepath.Join(src, "go.mod"))
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(raw), "\n") {
		if f := strings.Fields(l); len(f) == 2 && f[0] == "module" {
			return f[1]
		}
	}
	return ""
}

// markdown is the step as a report section: each side's failures, named,
// with the end of each one's output.
func (u UnitReport) markdown() string {
	if len(u.Candidate) == 0 && len(u.Control) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## Vet and short tests\n")
	list := func(title string, fs []TestFailure, excerpts bool) {
		if len(fs) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n%s:\n\n", title)
		for _, f := range fs {
			fmt.Fprintf(&b, "- %s\n", f.Name())
			if excerpts && f.Excerpt != "" {
				fmt.Fprintf(&b, "\n%s\n", fenced(tail(f.Excerpt, 1200)))
			}
		}
	}
	list("Failed on the candidate", u.Candidate, true)
	list("Failed on the control", u.Control, true)
	list("Failed on both sides (the environment's, not the candidate's; the run went on without them)", u.Shared, false)
	list("Failed once on the candidate alone and passed when run again (a flake, not counted)", u.Flaky, false)
	list("Failed on the candidate alone, twice, and the control passes them: the candidate's own", u.Own, false)
	return b.String()
}
