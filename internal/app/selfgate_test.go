package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/gate"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

// The daemon's gate runner reads a keep only from a gate that said keep by
// its exit code and its report, on this head and main, under bubblewrap;
// every other answer is a rollback (exit 1) or can't run.
func TestRunGateReadsTheVerdict(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a shell stand-in for hive gate")
	}
	dir := t.TempDir()
	ws := core.Workspace{Root: filepath.Join(dir, "hive")}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	cfg := ws.LoadConfig()
	cfg.Harness = "claude"
	ws.SaveConfig(cfg)
	script := filepath.Join(dir, "hive-gate.sh")
	os.WriteFile(script, []byte("#!/bin/sh\necho \"$@\" > \"$GATE_ARGS\"\nps -o nice= -p $$ > \"$GATE_ARGS.nice\" 2>/dev/null\n[ -n \"$GATE_REPORT\" ] && cp \"$GATE_REPORT\" report.json\nexit $GATE_EXIT\n"), 0o755)
	old, oldOrg := gateExe, core.OwnerTokens["hive-rsi-lab"]
	gateExe = func() (string, error) { return script, nil }
	core.OwnerTokens["hive-rsi-lab"] = filepath.Join(dir, "no-such.token")
	t.Cleanup(func() { gateExe, core.OwnerTokens["hive-rsi-lab"] = old, oldOrg })
	args := filepath.Join(dir, "args")
	t.Setenv("GATE_ARGS", args)
	run := runGate(ws)
	// The test process stands for the daemon: the gate must run under it.
	own, nice := ownNice(t)
	for i, tc := range []struct {
		exit         string
		rep          *gate.Report
		tie          bool
		want, reason string
	}{
		{"0", &gate.Report{Head: "h", Base: "m", Sandbox: "bwrap", Verdict: gate.Verdict{Keep: true, Reason: "better"}}, false, core.GateKeep, "better"},
		{"0", &gate.Report{Head: "h", Base: "m", Sandbox: "bwrap", Verdict: gate.Verdict{Keep: true, Reason: "a tie"}}, true, core.GateKeep, "a tie"},
		{"1", &gate.Report{Head: "h", Base: "m", Sandbox: "bwrap", Measured: true, Verdict: gate.Verdict{Reason: "not better"}}, false, core.GateRollback, "not better"},
		{"1", &gate.Report{Head: "h", Base: "m", Sandbox: "bwrap", Error: "x", Verdict: gate.Verdict{Reason: "the candidate fails its own vet or short tests"}}, false, core.GateRollback, "own vet"},
		{"3", &gate.Report{Head: "h", Base: "m", CantRun: true, Verdict: gate.Verdict{Reason: "the gate could not run: no bubblewrap"}}, false, core.GateCantRun, "no bubblewrap"},
		{"0", &gate.Report{Head: "h", Base: "m", Sandbox: "bare", Verdict: gate.Verdict{Keep: true}}, false, core.GateCantRun, "sandbox \"bare\""},
		{"0", &gate.Report{Head: "other", Base: "m", Sandbox: "bwrap", Verdict: gate.Verdict{Keep: true}}, false, core.GateCantRun, "not this head"},
		{"2", nil, false, core.GateCantRun, "no report"},
	} {
		rep := ""
		if tc.rep != nil {
			rep = filepath.Join(dir, "rep.json")
			raw, _ := json.Marshal(tc.rep)
			os.WriteFile(rep, raw, 0o644)
		}
		t.Setenv("GATE_REPORT", rep)
		t.Setenv("GATE_EXIT", tc.exit)
		j := work.GateJob{Project: core.Project{Name: "hive", Repo: "ihsan-sa/hive", Self: true}, Repo: "/repo", Head: "h", Main: "m", AllowTie: tc.tie,
			Out: filepath.Join(dir, "runs", string(rune('a'+i))), Timeout: time.Hour}
		g := run(context.Background(), j)
		if g.Outcome != tc.want || !strings.Contains(g.Reason, tc.reason) || g.Measured != (tc.rep != nil && tc.rep.Measured) {
			t.Fatalf("case %d: %s %q, want %s with %q", i, g.Outcome, g.Reason, tc.want, tc.reason)
		}
		raw, _ := os.ReadFile(args)
		a := string(raw)
		if !strings.Contains(a, "--head h --base m") || !strings.Contains(a, "--local-org") || strings.Contains(a, "--allow-bare") || strings.Contains(a, "--allow-tie") != tc.tie {
			t.Fatalf("case %d: gate called with %q", i, a)
		}
		if nice {
			checkGateNice(t, strconv.Itoa(i), args+".nice", own)
		}
	}
	// Under a daemon already niced (the Hive runs its own test checks at
	// CheckNice, and the gate's child runs too) the gate goes lower still,
	// to the kernel's cap. Linux nices a thread, not a process, and Go's
	// goroutines move between threads, so the test can't nice itself in
	// place: it runs itself once more as a process under nice -n CheckNice,
	// where own reads CheckNice and the gate must read 19.
	// At the cap already (19: the Hive's own check inside a niced gate) the
	// child would read the same level, so it is skipped.
	if nice && runtime.GOOS == "linux" && own < 19 && os.Getenv("HIVE_TEST_NICED") == "" {
		cmd := exec.Command("nice", "-n", strconv.Itoa(core.CheckNice), os.Args[0], "-test.run=^TestRunGateReadsTheVerdict$", "-test.count=1")
		cmd.Env = append(os.Environ(), "HIVE_TEST_NICED=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("this test under nice -n %d: %v\n%s", core.CheckNice, err, out)
		}
	}

	cfg.Harness = "fake"
	ws.SaveConfig(cfg)
	if g := run(context.Background(), work.GateJob{Out: filepath.Join(dir, "fake")}); g.Outcome != core.GateKeep || g.Sandbox != "fake" {
		t.Fatalf("the fake harness: %+v", g)
	}
}

// ownNice is the nice level the test process runs at, read as the gate's
// stand-in reads its own, and whether there is a nice here to check at all.
func ownNice(t *testing.T) (int, bool) {
	t.Helper()
	// Without either tool (a minimal image, the gate's own sandbox) the
	// level isn't checked, rather than failing the test on the machine.
	for _, tool := range []string{"nice", "ps"} {
		if _, err := exec.LookPath(tool); err != nil {
			return 0, false
		}
	}
	raw, err := exec.Command("ps", "-o", "nice=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		t.Fatalf("reading the test process's own nice level: %v", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("the test process's own nice level read as %q", raw)
	}
	return n, true
}

// checkGateNice reads the level the gate's stand-in ran at from path and
// holds it under the daemon's (the test process's) own: `nice -n` adds
// CheckNice to it, capped at 19 (sandbox.Lowered). macOS reports a level
// on its own scale (15 asked read back as 5 on the forge's runner), so
// there it is only held under the daemon's own.
func checkGateNice(t *testing.T, label, path string, own int) {
	t.Helper()
	raw, _ := os.ReadFile(path)
	got, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("case %s: the gate's nice read as %q", label, raw)
	}
	if runtime.GOOS == "darwin" {
		if got <= own {
			t.Fatalf("case %s: the gate ran at nice %d, the daemon's own is %d; want it under", label, got, own)
		}
		return
	}
	if want := sandbox.Lowered(own, core.CheckNice); got != want {
		t.Fatalf("case %s: the gate ran at nice %d under a daemon at %d, want %d", label, got, own, want)
	}
}

// With the org's token here the gate runs on the test org, holding no second
// token (each child gets a deploy key on its own repository); without it, on
// a local org.
func TestGateOrgArgs(t *testing.T) {
	dir := t.TempDir()
	org := filepath.Join(dir, "org.token")
	old := core.OwnerTokens["hive-rsi-lab"]
	core.OwnerTokens["hive-rsi-lab"] = org
	t.Cleanup(func() { core.OwnerTokens["hive-rsi-lab"] = old })
	if args := orgArgs(work.GateJob{Out: dir}); args[0] != "--local-org" {
		t.Fatalf("no org token: args %q", args)
	}
	os.WriteFile(org, []byte("org-secret\n"), 0o600)
	args := orgArgs(work.GateJob{Out: dir})
	if strings.Join(args, " ") != "--org Hive-RSI-Lab --org-token-file "+org {
		t.Fatalf("args %q", args)
	}
}

// The gate's own fallback from the org to a local one reaches the PR as a note.
func TestGateOrgNoteReachesTheResult(t *testing.T) {
	dir := t.TempDir()
	rep := gate.Report{Head: "h", Base: "m", OrgNote: "The gate ran on a local org instead of Hive-RSI-Lab: no"}
	raw, _ := json.Marshal(rep)
	os.WriteFile(filepath.Join(dir, "report.json"), raw, 0o644)
	if g := readReport(work.GateJob{Out: dir, Head: "h", Main: "m"}, nil, nil); g.Note != rep.OrgNote {
		t.Fatalf("note %q", g.Note)
	}
}

// The gate weighs one of hive-eval's dev tasks, the same one for every run on
// a head, and none where this machine has no hive-eval project.
func TestGateTaskIsADevTask(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	if got := gateTask(ws, "abc"); got != "" {
		t.Fatalf("no hive-eval project, yet task %q", got)
	}
	dev := filepath.Join(ws.Project(evalProject), "tasks", "dev")
	for _, n := range []string{"a", "b", "c"} {
		os.MkdirAll(filepath.Join(dev, n), 0o755)
		os.WriteFile(filepath.Join(dev, n, "task.json"), []byte("{}"), 0o644)
	}
	os.MkdirAll(filepath.Join(dev, "no-task-json"), 0o755)
	seen := map[string]bool{}
	for i := 0; i < 40; i++ {
		head := strconv.Itoa(i)
		got := gateTask(ws, head)
		if got != gateTask(ws, head) {
			t.Fatal("one head, two tasks")
		}
		if filepath.Dir(got) != dev || filepath.Base(got) == "no-task-json" {
			t.Fatalf("task %q", got)
		}
		seen[filepath.Base(got)] = true
	}
	if len(seen) != 3 {
		t.Errorf("heads took only %v of the three tasks", seen)
	}
}
