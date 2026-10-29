package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/gate"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/release"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

// The test org a self project's gate makes its fresh private repositories
// in, when the org's own token (which makes and deletes them,
// core.OwnerTokens) is on this machine. Each child reaches only its own
// repository, through a deploy key the gate adds to it.
var gateOrg = "Hive-RSI-Lab"

// gateExe is the binary that runs `hive gate`: the daemon's own.
var gateExe = os.Executable

// runGate runs the recursive gate for the work module: `hive gate` from the
// daemon itself, on this host, so its sibling bubblewrap sandboxes start one
// level deep (a sandbox inside an agent's would be refused). It never passes
// --allow-bare: without bubblewrap the gate can't run, and the change waits.
// Under the fake harness (dev runs and the e2e) nothing real is built, so the
// change is kept as judged by a stand-in, and the report says so.
func runGate(ws core.Workspace) func(context.Context, work.GateJob) core.GateResult {
	return func(ctx context.Context, j work.GateJob) core.GateResult {
		if ws.LoadConfig().Harness == "fake" {
			return core.GateResult{Outcome: core.GateKeep, Reason: "the fake harness does no real work, so a stand-in keeps it", Sandbox: "fake"}
		}
		exe, err := gateExe()
		if err != nil {
			return core.GateResult{Outcome: core.GateCantRun, Reason: "can't find the Hive's own binary: " + err.Error()}
		}
		if err := os.MkdirAll(j.Out, 0o755); err != nil {
			return core.GateResult{Outcome: core.GateCantRun, Reason: err.Error()}
		}
		args := []string{"gate", "--repo", j.Repo, "--head", j.Head, "--base", j.Main, "--out", j.Out, "--timeout", j.Timeout.String()}
		if j.AllowTie {
			args = append(args, "--allow-tie")
		}
		if j.CIPassed {
			args = append(args, "--ci-passed")
		}
		args = append(args, orgArgs(j)...)
		// The gate and all it starts (builds, child Hives, the e2e suite) run
		// under every lead's priority, as a check does.
		argv := sandbox.Niced(append([]string{exe}, args...), core.CheckNice)
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir = j.Out
		cmd.WaitDelay = time.Minute
		dieWithDaemon(cmd)
		if log, err := os.Create(filepath.Join(j.Out, "gate.log")); err == nil {
			defer log.Close()
			cmd.Stdout, cmd.Stderr = log, log
		}
		runErr := cmd.Run()
		// A gate killed or crashed mid-run left its children. Reap runs here
		// as the person: only on a run folder of the Hive's own.
		if rel, err := filepath.Rel(ws.Gates(), j.Out); err == nil && filepath.IsLocal(rel) {
			gate.Reap(j.Out)
		}
		return readReport(j, runErr, ctx.Err())
	}
}

// runCanary runs the release train's canary as a gate run: the candidate
// release's commit against the running release's, both child Hives on the
// same real work. Ties are kept, since a release must break nothing rather
// than show a gain; only a keep moves the Hive. Both are releases whose CI
// passed vet and the short tests, so the canary goes straight to the e2e,
// probes and benchmark.
func runCanary(ws core.Workspace, run func(context.Context, work.GateJob) core.GateResult) func(context.Context, release.CanaryJob) core.GateResult {
	return func(ctx context.Context, j release.CanaryJob) core.GateResult {
		return run(ctx, work.GateJob{Project: j.Project, Repo: ws.Project(j.Project.Name), Head: j.Head, Main: j.Base, AllowTie: true, CIPassed: true, Out: j.Out, Timeout: j.Timeout})
	}
}

// orgArgs are the gate's org flags: the test org when its token is here,
// else a local org under the run. The gate itself falls back to a local org,
// and says why in its report, when the org leg can't be set up.
func orgArgs(j work.GateJob) []string {
	orgFile := core.OwnerTokens[strings.ToLower(gateOrg)]
	if readToken(orgFile) == "" {
		return []string{"--local-org", filepath.Join(j.Out, "org")}
	}
	return []string{"--org", gateOrg, "--org-token-file", orgFile}
}

func readToken(path string) string {
	if path == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// readReport turns the run's signed report into a verdict. It is a keep only
// if the gate said so by both its exit code and its report, on exactly this
// head and main, under bubblewrap; anything the gate didn't decide is
// "can't run", never a keep.
func readReport(j work.GateJob, runErr, ctxErr error) core.GateResult {
	g := core.GateResult{Outcome: core.GateCantRun, Report: filepath.Join(j.Out, "report.md")}
	if ctxErr != nil {
		g.Reason = "the gate ran out of time (" + ctxErr.Error() + ")"
		return g
	}
	raw, err := os.ReadFile(filepath.Join(j.Out, "report.json"))
	var rep gate.Report
	if err == nil {
		err = json.Unmarshal(raw, &rep)
	}
	if err != nil {
		g.Report = filepath.Join(j.Out, "gate.log")
		g.Reason = "the gate left no report: " + fmt.Sprint(runErr)
		return g
	}
	g.Note = rep.OrgNote
	g.Reason, g.Sandbox, g.Transient, g.Alone, g.Conflicts, g.Measured, g.MainFails = rep.Verdict.Reason, rep.Sandbox, rep.Transient, rep.Alone, rep.Conflicts, rep.Measured && !rep.CantRun, rep.BaseFails
	if g.Reason == "" {
		g.Reason = rep.Error
	}
	var exit *exec.ExitError
	code := 0
	if errors.As(runErr, &exit) {
		code = exit.ExitCode()
	} else if runErr != nil {
		g.Reason = "the gate didn't run: " + runErr.Error()
		return g
	}
	switch {
	case rep.Head != j.Head || rep.Base != j.Main:
		g.Reason = fmt.Sprintf("the report is on %s against %s, not this head and main", core.Short(rep.Head), core.Short(rep.Base))
	case rep.CantRun || code == 3:
	case rep.Verdict.Keep && code == 0 && rep.Sandbox == "bwrap":
		g.Outcome = core.GateKeep
	case rep.Verdict.Keep:
		g.Reason = fmt.Sprintf("the report keeps it but the gate exited %d in sandbox %q", code, rep.Sandbox)
	case code == 1:
		g.Outcome = core.GateRollback
	default:
		g.Reason = fmt.Sprintf("the gate exited %d: %s", code, g.Reason)
	}
	return g
}
