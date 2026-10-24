package daemon

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// The workflows that drive a released binary (update.yml waits for the
// updated daemon in its daemon.json) read the file where this build writes
// it: v0.1.4 moved it under .hive/private and update.yml went red on every
// system looking for it in .hive. Another path is a fallback for an older
// release's daemon, on a line that names this one first.
func TestWorkflowsReadTheDaemonFileWhereItIs(t *testing.T) {
	want := filepath.ToSlash(core.Workspace{}.DaemonFile())
	files, _ := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.yml"))
	if len(files) == 0 {
		t.Fatal("no workflows found")
	}
	path := regexp.MustCompile(`\.hive/(?:[\w.-]+/)*daemon\.json`)
	seen := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			for i, got := range path.FindAllString(line, -1) {
				seen++
				if got != want && (i == 0 || !strings.Contains(line, want)) {
					t.Errorf("%s reads %s; the daemon writes %s:\n%s", filepath.Base(f), got, want, strings.TrimSpace(line))
				}
			}
		}
	}
	if seen == 0 {
		t.Error("no workflow reads daemon.json; update.yml should wait for the updated daemon in it")
	}
}

// A jq program (gh's -q or jq itself) in a workflow is single-quoted, its
// values passed with --arg: a release tag spliced into a double-quoted
// program is code jq runs.
func TestWorkflowsPassValuesToJqAsArgs(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.yml"))
	spliced := regexp.MustCompile(`(?:(?:--json\s+\S+\s+|^\s*)(?:-q|--jq)|\bjq(?:\s+-[a-zA-Z]+)*)\s+"(?:[^"\\]|\\.)*\$[\w{]`)
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		for _, line := range strings.Split(string(raw), "\n") {
			if spliced.MatchString(line) {
				t.Errorf("%s splices a shell value into a jq program:\n%s", filepath.Base(f), strings.TrimSpace(line))
			}
		}
	}
	if !spliced.MatchString(`--json tagName -q "(map(.tagName) | index(\"$to\"))"`) || !spliced.MatchString(`| jq -r "index(\"$to\")"`) {
		t.Error("the check misses the splice it is for")
	}
}

// The release workflow signs the checksums in a job of its own, in a
// protected environment, since it runs the tagged commit's code with the key;
// no other job sees the key, and the signature is uploaded after it is made.
func TestReleaseWorkflowSigns(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	signed := 0
	for name, job := range jobs(string(raw)) {
		if !strings.Contains(job, "secrets.HIVE_RELEASE_KEY") {
			continue
		}
		signed++
		sign, upload := strings.Index(job, "go run ./cmd/hive-sign"), strings.Index(job, "gh release upload")
		if !regexp.MustCompile(`(?m)^    environment: \S`).MatchString(job) || sign < 0 || upload < sign {
			t.Errorf("release.yml's %s job holds HIVE_RELEASE_KEY outside a protected environment, or doesn't sign before its upload:\n%s", name, job)
		}
	}
	if signed != 1 {
		t.Errorf("%d jobs in release.yml hold HIVE_RELEASE_KEY; one, in its environment, signs", signed)
	}
}

// jobs splits a workflow into its jobs' text, by name.
func jobs(wf string) map[string]string {
	out, name, in := map[string]string{}, "", false
	for _, line := range strings.Split(wf, "\n") {
		switch {
		case line == "jobs:":
			in = true
		case in && strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.HasSuffix(line, ":"):
			name = strings.TrimSuffix(strings.TrimSpace(line), ":")
		case in && line != "" && !strings.HasPrefix(line, " "):
			in, name = false, ""
		case name != "":
			out[name] += line + "\n"
		}
	}
	return out
}

// No workflow splices an expression into a run script: ${{ }} is replaced
// before the shell reads it, so a crafted release tag or input would be code.
// Values go through env and are read as "$NAME".
func TestWorkflowsKeepExpressionsOutOfScripts(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.yml"))
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		for _, line := range spliced(string(raw)) {
			t.Errorf("%s splices an expression into a run script:\n%s", filepath.Base(f), line)
		}
	}
	if got := spliced("    steps:\n      - run: |\n          echo ok\n          gh release upload \"${{ github.event.release.tag_name }}\"\n        env:\n          TAG: ${{ x }}\n      - run: echo ${{ y }}\n"); len(got) != 2 {
		t.Errorf("the check finds %q; it should find the two spliced lines", got)
	}
}

// spliced is each line of a run script in wf that holds ${{.
func spliced(wf string) (out []string) {
	runKey := regexp.MustCompile(`^(\s*)(?:- )?run:\s*(.*)$`)
	depth := -1 // the run key's indent while inside its script
	for _, line := range strings.Split(wf, "\n") {
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if depth >= 0 && (strings.TrimSpace(line) == "" || indent > depth) {
			if strings.Contains(line, "${{") {
				out = append(out, strings.TrimSpace(line))
			}
			continue
		}
		depth = -1
		if m := runKey.FindStringSubmatch(line); m != nil {
			depth = len(m[1])
			if strings.HasPrefix(strings.TrimSpace(line), "- ") {
				depth += 2 // a script under "- run:" is indented past the dash
			}
			if strings.Contains(m[2], "${{") {
				out = append(out, strings.TrimSpace(line))
			}
		}
	}
	return out
}
