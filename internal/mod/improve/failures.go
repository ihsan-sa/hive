package improve

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// failure is one failing package in a red test run, with the tests that
// failed in it and the first line each one said.
type failure struct {
	Pkg   string
	Tests []string
	Says  []string // the assertion under each test, or a panic or build error
	Build bool     // the package didn't build, so a rerun can't help
}

var (
	pkgLine  = regexp.MustCompile(`^(ok|FAIL|\?)\s+(\S+)(\s+\[([^\]]+)\])?`)
	testLine = regexp.MustCompile(`^\s*--- FAIL: (\S+)`)
	// The same marks in a check's stored output, whose whitespace is folded.
	foldedPkg  = regexp.MustCompile(`FAIL\s+(\S+/\S+)(\s+\[(build|setup) failed\]|\s+[\d.]+s)`)
	foldedTest = regexp.MustCompile(`--- FAIL: (\S+) \([\d.]+s\)\s*([^-]{0,160})`)
)

// failures reads go test's output for the packages and tests that failed.
// It reads the folded one-line output too, less precisely.
func failures(out string) []failure {
	if !strings.Contains(out, "\n") {
		return foldedFailures(out)
	}
	var fs []failure
	var tests, says []string
	var block []string // lines since the last package line
	for _, l := range strings.Split(out, "\n") {
		if m := pkgLine.FindStringSubmatch(l); m != nil && strings.ContainsAny(m[2], "/.") {
			if m[1] == "FAIL" {
				f := failure{Pkg: m[2], Tests: tests, Says: says, Build: strings.Contains(m[4], "failed")}
				if len(f.Tests) == 0 {
					f.Says = firstSaid(block)
				}
				fs = append(fs, f)
			}
			tests, says, block = nil, nil, nil
			continue
		}
		block = append(block, l)
		if m := testLine.FindStringSubmatch(l); m != nil {
			tests = append(tests, m[1])
			says = append(says, "")
			continue
		}
		if t := strings.TrimSpace(l); len(says) > 0 && says[len(says)-1] == "" && t != "" && t != "FAIL" && !strings.HasPrefix(t, "=== ") {
			says[len(says)-1] = t
		}
	}
	return fs
}

func foldedFailures(out string) []failure {
	var fs []failure
	start := 0
	for _, m := range foldedPkg.FindAllStringSubmatchIndex(out, -1) {
		f := failure{Pkg: out[m[2]:m[3]], Build: m[6] >= 0}
		for _, t := range foldedTest.FindAllStringSubmatch(out[start:m[0]], -1) {
			f.Tests = append(f.Tests, t[1])
			f.Says = append(f.Says, strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(t[2]), "FAIL")))
		}
		fs = append(fs, f)
		start = m[1]
	}
	return fs
}

// firstSaid is the first telling line of a package's output with no failing
// test: a panic, a build error or a timeout.
func firstSaid(block []string) []string {
	for _, l := range block {
		t := strings.TrimSpace(l)
		if t != "" && t != "FAIL" && !strings.HasPrefix(t, "=== ") && !strings.HasPrefix(t, "# ") {
			return []string{t}
		}
	}
	return nil
}

// summary names what failed in one line: each package, its tests and what
// they said. With no go test failures in it, it is the end of the output,
// where a vet or build error is.
func summary(fs []failure, out string) string {
	if len(fs) == 0 {
		lines := strings.Split(strings.TrimSpace(out), "\n")
		return core.Clip(strings.Join(lines[max(0, len(lines)-8):], " | "), 600)
	}
	var parts []string
	for _, f := range fs {
		var ts []string
		for i, t := range f.Tests {
			if i < len(f.Says) && f.Says[i] != "" {
				t += " (" + core.Clip(f.Says[i], 160) + ")"
			}
			ts = append(ts, t)
		}
		s := "FAIL " + f.Pkg
		switch {
		case len(ts) > 0:
			s += ": " + strings.Join(ts, ", ")
		case len(f.Says) > 0:
			s += ": " + core.Clip(f.Says[0], 160)
		}
		parts = append(parts, s)
	}
	return core.Clip(strings.Join(parts, "; "), 900)
}

// safeName is a package or test name fit for a shell line. The names come
// from what the candidate's code printed, so anything else (a quote, a $, a
// ;) makes the rerun the whole command again. A leading - would be read as
// a flag: "-test.run" swallows the next name and the rerun runs no tests.
var safeName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_/.-]*$`)

// rerunCommand is a command that runs only what failed, once more: the
// failing packages and tests under the go test flags of the project's own
// test command. Short of that (not a plain go test, a build failure, a cd,
// a name that isn't plainly a name) it is the whole command again.
func rerunCommand(test string, fs []failure) string {
	i := strings.LastIndex(test, "go test")
	if i < 0 || len(fs) == 0 || strings.Contains(test, "cd ") {
		return test
	}
	seg := test[i+len("go test"):]
	if j := strings.IndexAny(seg, "&|;"); j >= 0 {
		seg = seg[:j]
	}
	valued := []string{"-timeout", "-tags", "-p", "-parallel", "-cpu", "-run", "-skip", "-count"}
	var flags []string
	fields := strings.Fields(seg)
	for k := 0; k < len(fields); k++ {
		f := fields[k]
		if !strings.HasPrefix(f, "-") {
			continue
		}
		name, _, hasVal := strings.Cut(strings.TrimPrefix(f, "-"), "=")
		name = "-" + strings.TrimPrefix(name, "-")
		takes := !hasVal && slices.Contains(valued, name)
		if name != "-run" && name != "-count" && name != "-skip" {
			flags = append(flags, f)
			if takes && k+1 < len(fields) {
				flags = append(flags, fields[k+1])
			}
		}
		if takes {
			k++
		}
	}
	var pkgs, tests []string
	whole := false
	for _, f := range fs {
		if f.Build || !safeName.MatchString(f.Pkg) {
			return test
		}
		pkgs = append(pkgs, f.Pkg)
		if len(f.Tests) == 0 {
			whole = true // a panic or timeout outside a named test
		}
		for _, t := range f.Tests {
			if !safeName.MatchString(t) {
				return test
			}
			top, _, _ := strings.Cut(t, "/")
			if !slices.Contains(tests, top) {
				tests = append(tests, top)
			}
		}
	}
	cmd := "go test " + strings.Join(append(flags, "-count=1"), " ")
	if !whole && len(tests) > 0 {
		cmd += " -run '^(" + strings.Join(tests, "|") + ")$'"
	}
	return cmd + " '" + strings.Join(pkgs, "' '") + "'"
}

// redTests reads a red check: what failed, and whether it passes when only
// that runs again. A pass is a flaky test, not a broken merge. cant is the
// rerun when it couldn't start for want of a tool: no answer either way.
func (m *Module) redTests(p core.Project, sha, test string, tc core.Check) (sum string, flaky bool, cant core.Check) {
	out := tc.Output
	if tc.Log != "" {
		if b, err := core.SafeRead(tc.Log, 8<<20); err == nil && len(b) > 0 {
			out = string(b)
		}
	}
	fs := failures(out)
	sum = summary(fs, out)
	if strings.TrimSpace(test) == "" {
		return sum, false, cant // no command to run again: its removal is the red
	}
	rc := m.C.Effects.RunCheck(p, sha, "retest", rerunCommand(test, fs))
	if rc.CantRun != "" {
		return sum, false, rc // the rerun couldn't start: no answer either way
	}
	if rc.OK {
		m.fileFlaky(p, sha, fs, sum, out)
		return sum, true, cant
	}
	return sum, false, cant
}

// fileFlaky files the flaky tests as work, once for as long as that work is open.
func (m *Module) fileFlaky(p core.Project, sha string, fs []failure, sum, out string) {
	var names []string
	for _, f := range fs {
		names = append(names, f.Tests...)
		if len(f.Tests) == 0 {
			names = append(names, f.Pkg)
		}
	}
	if len(names) == 0 {
		names = core.FailedTests(out) // not go test's output: what its own runner named
	}
	what := "the tests"
	if len(names) > 0 {
		what = strings.Join(names, ", ")
	}
	title := core.Clip("Fix the flaky "+what, 120)
	if len(m.Work.List(func(t work.Task) bool { return t.Project == p.Name && t.Title == title && !t.Terminal() })) > 0 {
		return
	}
	m.C.J.MustAppend("alert.raised", map[string]string{"kind": "flake", "project": p.Name, "sha": sha, "text": "a flaky test in " + p.Name + " failed at " + core.Short(sha) + " and passed on rerun: " + core.Clip(sum, 300)})
	m.Work.File(work.Brief{Project: p.Name, From: "improve", Title: title, Aim: work.AimFix, Why: "flaky test: " + core.Clip(what, 200) + " failed at " + core.Short(sha) + " and passed on rerun",
		Done:  "The test passes every time: it no longer depends on timing, order or shared state, and running it many times (go test -count=50 -run on it) never fails.",
		Known: fmt.Sprintf("After a merge at %s the tests went red and passed when only what failed ran again, so the merge was kept. What failed: %s", core.Short(sha), sum)})
}
