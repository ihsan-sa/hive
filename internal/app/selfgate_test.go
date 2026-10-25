package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/gate"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
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
	os.WriteFile(script, []byte("#!/bin/sh\necho \"$@\" > \"$GATE_ARGS\"\n[ -n \"$GATE_REPORT\" ] && cp \"$GATE_REPORT\" report.json\nexit $GATE_EXIT\n"), 0o755)
	old, oldChild := gateExe, gateChildFile
	gateExe = func() (string, error) { return script, nil }
	gateChildFile = filepath.Join(dir, "no-such.token")
	t.Cleanup(func() { gateExe, gateChildFile = old, oldChild })
	args := filepath.Join(dir, "args")
	t.Setenv("GATE_ARGS", args)
	run := runGate(ws)
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
			Out: filepath.Join(dir, "runs", string(rune('a'+i))), Lessons: filepath.Join(dir, "lessons.md"), Timeout: time.Hour}
		g := run(context.Background(), j)
		if g.Outcome != tc.want || !strings.Contains(g.Reason, tc.reason) || g.Measured != (tc.rep != nil && tc.rep.Measured) {
			t.Fatalf("case %d: %s %q, want %s with %q", i, g.Outcome, g.Reason, tc.want, tc.reason)
		}
		raw, _ := os.ReadFile(args)
		a := string(raw)
		if !strings.Contains(a, "--head h --base m") || !strings.Contains(a, "--local-org") || !strings.Contains(a, "--judged") || strings.Contains(a, "--allow-bare") || strings.Contains(a, "--allow-tie") != tc.tie {
			t.Fatalf("case %d: gate called with %q", i, a)
		}
	}
	cfg.Harness = "fake"
	ws.SaveConfig(cfg)
	if g := run(context.Background(), work.GateJob{Out: filepath.Join(dir, "fake")}); g.Outcome != core.GateKeep || g.Sandbox != "fake" {
		t.Fatalf("the fake harness: %+v", g)
	}
}

// With both tokens here, a children's token the gate refuses runs the gate on
// a local org and says why, rather than leave the change unjudged.
func TestGateOrgFallsBackToLocal(t *testing.T) {
	dir := t.TempDir()
	org, child := filepath.Join(dir, "org.token"), filepath.Join(dir, "child.token")
	os.WriteFile(org, []byte("org-secret\n"), 0o600)
	os.WriteFile(child, []byte("classic-token\n"), 0o600)
	oldOrg, oldChild := core.OwnerTokens["hive-rsi-lab"], gateChildFile
	core.OwnerTokens["hive-rsi-lab"], gateChildFile = org, child
	t.Cleanup(func() { core.OwnerTokens["hive-rsi-lab"], gateChildFile = oldOrg, oldChild; reachOK.at = time.Time{} })
	reachOK.at = time.Time{}
	args, note := orgArgs(work.GateJob{Project: core.Project{Repo: "ihsan-sa/hive"}, Out: dir})
	if args[0] != "--local-org" || !strings.Contains(note, "fine-grained") {
		t.Fatalf("args %q, note %q", args, note)
	}
	os.WriteFile(child, []byte("org-secret\n"), 0o600)
	reachOK.at = time.Time{}
	if args, note = orgArgs(work.GateJob{Out: dir}); args[0] != "--local-org" || !strings.Contains(note, "same token") {
		t.Fatalf("args %q, note %q", args, note)
	}
}
