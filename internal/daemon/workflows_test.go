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

// The release workflow signs the checksums before it uploads them.
func TestReleaseWorkflowSigns(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	sign, upload := strings.Index(s, "go run ./cmd/hive-sign"), strings.Index(s, "gh release upload")
	if sign < 0 || upload < sign || !strings.Contains(s, "secrets.HIVE_RELEASE_KEY") {
		t.Fatal("release.yml doesn't sign checksums.txt with HIVE_RELEASE_KEY before the upload")
	}
}

// No run script has a ${{ }} expression in it: the runner pastes its value
// into the script before the shell sees it, so a release tag or branch name
// is code. Values go in through env and are read as "$NAME".
func TestWorkflowsKeepExpressionsOutOfScripts(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.yml"))
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		for _, s := range runScripts(string(raw)) {
			if strings.Contains(s, "${{") {
				t.Errorf("%s has an expression in a run script:\n%s", filepath.Base(f), s)
			}
		}
	}
	if got := runScripts("    - run: a ${{ x }}\n      - name: b\n        run: |\n          b\n          c ${{ y }}\n        env: {}\n"); len(got) != 2 || !strings.Contains(got[1], "${{ y }}") {
		t.Errorf("the check misses the scripts it is for: %q", got)
	}
}

// runScripts are a workflow's run: values, inline or block, one per step.
func runScripts(yml string) []string {
	var out []string
	lines := strings.Split(yml, "\n")
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		trimmed := strings.TrimLeft(strings.TrimPrefix(strings.TrimLeft(l, " "), "- "), " ")
		v, ok := strings.CutPrefix(trimmed, "run:")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if v != "|" && v != ">" && !strings.HasPrefix(v, "|") && !strings.HasPrefix(v, ">") {
			out = append(out, v)
			continue
		}
		indent := len(l) - len(strings.TrimLeft(l, " "))
		var block []string
		for i+1 < len(lines) {
			n := lines[i+1]
			if strings.TrimSpace(n) != "" && len(n)-len(strings.TrimLeft(n, " ")) <= indent {
				break
			}
			block = append(block, n)
			i++
		}
		out = append(out, strings.Join(block, "\n"))
	}
	return out
}

// The release key is read only in the sign job, which waits behind the
// protected release environment; the job that can write releases never sees it.
func TestReleaseKeyOnlyBehindTheEnvironment(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	jobs := map[string]string{}
	name := ""
	for _, l := range strings.Split(string(raw), "\n") {
		if m := regexp.MustCompile(`^  ([\w-]+):\s*$`).FindStringSubmatch(l); m != nil {
			name = m[1]
		}
		jobs[name] += l + "\n"
	}
	sign := jobs["sign"]
	if !strings.Contains(sign, "environment: release") || !strings.Contains(sign, "secrets.HIVE_RELEASE_KEY") {
		t.Fatalf("the sign job doesn't read the key behind the release environment:\n%s", sign)
	}
	for n, j := range jobs {
		if n != "sign" && strings.Contains(j, "HIVE_RELEASE_KEY") {
			t.Errorf("job %q reads the release key outside the sign job", n)
		}
		if n == "sign" && strings.Contains(j, "contents: write") {
			t.Error("the sign job can write releases")
		}
	}
}
