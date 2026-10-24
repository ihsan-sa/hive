package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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
	// The gate's key, as the gate keeps it in the person's config folder.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("HOME", dir)
	key := []byte(strings.Repeat("k", 32))
	cfgDir, _ := os.UserConfigDir()
	os.MkdirAll(filepath.Join(cfgDir, "hive"), 0o700)
	os.WriteFile(filepath.Join(cfgDir, "hive", "gate.key"), key, 0o600)
	sign := func(r gate.Report) *gate.Report {
		r.Sig = ""
		raw, _ := json.Marshal(r)
		h := hmac.New(sha256.New, key)
		h.Write(raw)
		r.Sig = hex.EncodeToString(h.Sum(nil))
		return &r
	}
	run := runGate(ws)
	for i, tc := range []struct {
		exit         string
		rep          *gate.Report
		tie          bool
		want, reason string
	}{
		{"0", sign(gate.Report{Head: "h", Base: "m", Sandbox: "bwrap", Verdict: gate.Verdict{Keep: true, Reason: "better"}}), false, core.GateKeep, "better"},
		{"0", sign(gate.Report{Head: "h", Base: "m", Sandbox: "bwrap", Verdict: gate.Verdict{Keep: true, Reason: "a tie"}}), true, core.GateKeep, "a tie"},
		{"1", sign(gate.Report{Head: "h", Base: "m", Sandbox: "bwrap", Verdict: gate.Verdict{Reason: "not better"}}), false, core.GateRollback, "not better"},
		{"3", sign(gate.Report{Head: "h", Base: "m", CantRun: true, Verdict: gate.Verdict{Reason: "the gate could not run: no bubblewrap"}}), false, core.GateCantRun, "no bubblewrap"},
		{"0", sign(gate.Report{Head: "h", Base: "m", Sandbox: "bare", Verdict: gate.Verdict{Keep: true}}), false, core.GateCantRun, "sandbox \"bare\""},
		{"0", sign(gate.Report{Head: "other", Base: "m", Sandbox: "bwrap", Verdict: gate.Verdict{Keep: true}}), false, core.GateCantRun, "not this head"},
		{"2", nil, false, core.GateCantRun, "no report"},
		// A keep this machine's gate didn't sign, or one changed after.
		{"0", &gate.Report{Head: "h", Base: "m", Sandbox: "bwrap", Verdict: gate.Verdict{Keep: true}}, false, core.GateCantRun, "not trusted"},
		{"0", func() *gate.Report {
			r := sign(gate.Report{Head: "h", Base: "m", Sandbox: "bwrap", Verdict: gate.Verdict{Reason: "x"}})
			r.Verdict.Keep = true
			return r
		}(), false, core.GateCantRun, "not trusted"},
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
		if g.Outcome != tc.want || !strings.Contains(g.Reason, tc.reason) {
			t.Fatalf("case %d: %s %q, want %s with %q", i, g.Outcome, g.Reason, tc.want, tc.reason)
		}
		raw, _ := os.ReadFile(args)
		a := string(raw)
		if !strings.Contains(a, "--head h --base m") || !strings.Contains(a, "--local-org") || strings.Contains(a, "--allow-bare") || strings.Contains(a, "--allow-tie") != tc.tie {
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
	// The check is cached on both tokens: a children's token swapped for
	// another is checked again at once, not trusted for the cached minutes.
	asked := 0
	oldCheck := checkReach
	checkReach = func(gate.GitHub, string, string) error { asked++; return nil }
	t.Cleanup(func() { checkReach = oldCheck })
	reachOK.at = time.Time{}
	for i, tok := range []string{"github_pat_one", "github_pat_one", "github_pat_two"} {
		os.WriteFile(child, []byte(tok+"\n"), 0o600)
		if args, note = orgArgs(work.GateJob{Project: core.Project{Repo: "ihsan-sa/hive"}, Out: dir}); args[0] != "--org" || note != "" {
			t.Fatalf("token %d: args %q, note %q", i, args, note)
		}
	}
	if asked != 2 {
		t.Fatalf("CheckReach asked %d times for two tokens, want 2", asked)
	}
}
