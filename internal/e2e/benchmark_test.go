package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// benchAsks is the benchmark's fixed set of asks on the demo textkit project:
// one per function the demo leaves unwritten.
var benchAsks = []string{
	"In textkit, implement capitalize_words in textkit/__init__.py.",
	"In textkit, implement is_palindrome in textkit/__init__.py.",
	"In textkit, implement char_frequency in textkit/__init__.py.",
	"In textkit, implement truncate in textkit/__init__.py.",
}

// TestBenchmark is what `hive gate` measures: the fixed asks on the scored demo
// project, one after another. It prints one BENCH line of JSON. It runs only
// when HIVE_BENCH is set (to the number of asks), because on the real harness
// it spends plan usage.
//
// The Hive being measured is not taken at its word. What it says (a task is
// done, its score, its cost) only decides when to stop waiting; what is judged
// is measured here, from what landed on its forge: the commits on textkit's
// main, and textkit's functions checked on inputs drawn fresh for this run
// against answers worked out here. Its cost is reported (cost_usd_reported)
// but it is the Hive's own count, so the gate does not judge it.
func TestBenchmark(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("HIVE_BENCH"))
	if n <= 0 {
		t.Skip("set HIVE_BENCH=<asks> (4 runs the whole set) to run the benchmark")
	}
	n = min(n, len(benchAsks))
	h := start(t)
	h.post("/api/project", map[string]any{"demo": true}, nil)
	st := h.wait("the demo project", 5*minute, func(st api.State) bool {
		for _, p := range st.Projects {
			if p.Name == "textkit" && p.Repo != "" && len(p.Scores) > 0 {
				return true
			}
		}
		return false
	})
	repo := ""
	for _, p := range st.Projects {
		if p.Name == "textkit" {
			repo = p.Repo
		}
	}
	base := onForge(t, h, repo)
	start := time.Now()
	done := 0
	for _, ask := range benchAsks[:n] {
		h.post("/api/ask", map[string]string{"project": "textkit", "title": ask, "known": ask}, nil)
		st := h.wait("the ask to finish", patience(t), func(st api.State) bool {
			tk, ok := task(st, "textkit", ask)
			return ok && (tk.Stage == work.StageDone || tk.Stage == work.StageBlocked || tk.Stage == work.StageStopped)
		})
		if tk, _ := task(st, "textkit", ask); tk.Stage == work.StageDone {
			done++
		}
	}
	seconds := time.Since(start).Seconds()
	cost := 0.0
	for _, r := range h.state().Runs {
		cost += r.Cost
	}
	after := onForge(t, h, repo)
	commits := len(after.commits) - len(base.commits)
	finished := 0
	for _, f := range benchFuncs[:n] {
		if after.pass[f] {
			finished++
		}
	}
	score := 0
	for _, ok := range after.pass {
		if ok {
			score++
		}
	}
	out, _ := json.Marshal(map[string]any{"score": score, "asks_finished": finished, "commits_landed": commits, "asks": n, "seconds": seconds,
		"asks_done_reported": done, "cost_usd_reported": cost})
	fmt.Println("BENCH " + string(out))
	if commits < n {
		t.Errorf("%d commits landed on %s's main for %d asks (the Hive said %d were done)", commits, repo, n, done)
	}
}

// benchFuncs are the functions the asks are about, in the asks' order; the
// rest of benchChecks is what the demo starts with.
var benchFuncs = []string{"capitalize_words", "is_palindrome", "char_frequency", "truncate"}

type benchState struct {
	commits []string
	pass    map[string]bool
}

// onForge reads repo's main straight from the forge's bare repository and
// checks each textkit function on fresh inputs.
func onForge(t *testing.T, h *hive, repo string) benchState {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "landed")
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("clone", "-q", "--no-local", "--branch", "main", h.forge.RepoPath(repo), dir)
	s := benchState{commits: strings.Fields(git("-C", dir, "rev-list", "HEAD")), pass: map[string]bool{}}
	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 1))
	var calls []benchCall
	for _, name := range benchNames() {
		for range 6 {
			calls = append(calls, benchChecks[name](rng))
		}
	}
	got := runTextkit(t, dir, calls)
	for _, name := range benchNames() {
		s.pass[name] = true
	}
	for i, c := range calls {
		if i >= len(got) || !got[i].OK || !c.Same(got[i].V) {
			s.pass[c.Func] = false
		}
	}
	return s
}

func benchNames() []string {
	var ns []string
	for n := range benchChecks {
		ns = append(ns, n)
	}
	sort.Strings(ns)
	return ns
}

// benchCall is one call into textkit and the answer it must give.
type benchCall struct {
	Func string `json:"f"`
	Args []any  `json:"a"`
	Want any    `json:"-"`
}

// Same compares an answer that came back as JSON with the one worked out here.
func (c benchCall) Same(got json.RawMessage) bool {
	want, _ := json.Marshal(c.Want)
	var a, b any
	if json.Unmarshal(want, &a) != nil || json.Unmarshal(got, &b) != nil {
		return false
	}
	return reflect.DeepEqual(a, b)
}

func benchWords(rng *rand.Rand, gaps bool) (text string, words []string) {
	for i := range 1 + rng.IntN(5) {
		w := make([]byte, 1+rng.IntN(7))
		for j := range w {
			w[j] = byte('a' + rng.IntN(26))
		}
		words = append(words, string(w))
		if i > 0 {
			text += " "
			if gaps && rng.IntN(2) == 0 {
				text += " "
			}
		}
		text += string(w)
	}
	return text, words
}

// benchChecks draws a fresh call for each function, with its answer as the
// demo's docstring says.
var benchChecks = map[string]func(*rand.Rand) benchCall{
	"word_count": func(r *rand.Rand) benchCall {
		text, ws := benchWords(r, true)
		return benchCall{"word_count", []any{text}, len(ws)}
	},
	"reverse_words": func(r *rand.Rand) benchCall {
		text, ws := benchWords(r, true)
		slices.Reverse(ws)
		return benchCall{"reverse_words", []any{text}, strings.Join(ws, " ")}
	},
	"capitalize_words": func(r *rand.Rand) benchCall {
		text, ws := benchWords(r, false)
		for i, w := range ws {
			ws[i] = strings.ToUpper(w[:1]) + w[1:]
		}
		return benchCall{"capitalize_words", []any{text}, strings.Join(ws, " ")}
	},
	"is_palindrome": func(r *rand.Rand) benchCall {
		_, ws := benchWords(r, false)
		core := strings.Join(ws, "")
		rev := []byte(core)
		slices.Reverse(rev)
		letters := core + string(rev)
		if r.IntN(2) == 0 {
			letters = core + "q" + string(rev[:len(rev)-1]) + "z"
		}
		back := []byte(letters)
		slices.Reverse(back)
		pal := letters == string(back)
		var b strings.Builder
		for i, ch := range letters {
			if r.IntN(3) == 0 {
				ch = ch - 'a' + 'A'
			}
			b.WriteRune(ch)
			if i < len(letters)-1 && r.IntN(4) == 0 {
				b.WriteString([]string{" ", ", ", ": ", "!"}[r.IntN(4)])
			}
		}
		return benchCall{"is_palindrome", []any{b.String()}, pal}
	},
	"char_frequency": func(r *rand.Rand) benchCall {
		text, _ := benchWords(r, false)
		want := map[string]int{}
		for _, ch := range strings.ReplaceAll(text, " ", "") {
			want[string(ch)]++
		}
		return benchCall{"char_frequency", []any{text}, want}
	},
	"truncate": func(r *rand.Rand) benchCall {
		b := make([]byte, r.IntN(16))
		for i := range b {
			b[i] = byte('a' + r.IntN(26))
		}
		text, width := string(b), 3+r.IntN(10)
		want := text
		if len(text) > width {
			want = text[:width-3] + "..."
		}
		return benchCall{"truncate", []any{text, width}, want}
	},
}

// textkitDriver calls textkit with the calls on stdin and prints the answers.
const textkitDriver = `import json, sys
calls = json.load(sys.stdin)
sys.path.insert(0, ".")
out = []
try:
    import textkit
except Exception:
    textkit = None
for c in calls:
    try:
        out.append({"ok": True, "v": getattr(textkit, c["f"])(*c["a"])})
    except Exception:
        out.append({"ok": False})
sys.stdout.write("\nRESULT " + json.dumps(out) + "\n")
`

type textkitAnswer struct {
	OK bool            `json:"ok"`
	V  json.RawMessage `json:"v"`
}

// runTextkit runs the driver in dir. The code is the measured Hive's, so under
// the gate it runs in that Hive's sandbox (HIVE_E2E_RUN), not here.
func runTextkit(t *testing.T, dir string, calls []benchCall) []textkitAnswer {
	argv := []string{"python3", "-I", "-c", textkitDriver}
	if run := os.Getenv("HIVE_E2E_RUN"); run != "" {
		argv = append([]string{run}, argv...)
	}
	in, _ := json.Marshal(calls)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir, cmd.Stdin = dir, bytes.NewReader(in)
	out, _ := cmd.Output()
	i := bytes.LastIndex(out, []byte("\nRESULT "))
	var got []textkitAnswer
	if i < 0 || json.Unmarshal(bytes.TrimSpace(out[i+len("\nRESULT "):]), &got) != nil {
		t.Logf("textkit gave no answers: %s", out)
	}
	return got
}
