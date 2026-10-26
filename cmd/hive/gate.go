package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/gate"
)

const gateUsage = `usage: hive gate --repo <git> --head <sha> --base <sha> [--org Hive-RSI-Lab | --local-org <dir>] [--child-token-file f [--reach-checked]]
                 [--eval <sha, default base>] [--reps 3] [--max-reps 6] [--bench 4] [--probes daemon,org,e2e,benchmark,recurse] [--depth 1]
                 [--allow-tie] [--timeout 2h] [--install <path>]  (installs only after a keep from bubblewrap-isolated children) [--out <dir>]
                 [--allow-bare]  (where bubblewrap cannot make namespaces; never with --org, never installed)
       hive gate --self-test [--out <dir>]      the gate on a trivial change, inside a child
       hive gate install --report <run>/report.json --target <path>   install a kept run's verified binary later
Exit 0 keeps the candidate, 1 rolls it back, 3 means the gate could not run or measure it
(no sandbox, no Go, a probe broken on both sides...: not the candidate's failure, no lesson), 2 is a usage error. The report is JSON on stdout.`

func cmdGate(args []string) int {
	if len(args) > 0 && args[0] == "install" {
		fs := flag.NewFlagSet("gate install", flag.ContinueOnError)
		report, target := fs.String("report", "", ""), fs.String("target", "", "")
		if fs.Parse(args[1:]) != nil || *report == "" || *target == "" {
			fmt.Fprintln(os.Stderr, gateUsage)
			return 2
		}
		// Only a gate run's report installs, and only through InstallTo's
		// checks: kept, run under bubblewrap, the hashed binary, its stamped version.
		raw, err := os.ReadFile(*report)
		var rep gate.Report
		if err == nil {
			err = json.Unmarshal(raw, &rep)
		}
		if err == nil {
			err = rep.InstallTo(*target)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("installed", *target, "(previous kept as "+*target+".prev)")
		return 0
	}
	fs := flag.NewFlagSet("gate", flag.ContinueOnError)
	repo, head, base, eval := fs.String("repo", "", ""), fs.String("head", "", ""), fs.String("base", "", ""), fs.String("eval", "", "")
	org, localOrg, tokenFile, childFile := fs.String("org", "", ""), fs.String("local-org", "", ""), fs.String("org-token-file", "", ""), fs.String("child-token-file", "", "")
	reps, bench, depth := fs.Int("reps", 3, ""), fs.Int("bench", 4, ""), fs.Int("depth", 1, "")
	maxReps := fs.Int("max-reps", 0, "") // a close call is run again up to this many times a side (default twice --reps)
	probes, out, install := fs.String("probes", "", ""), fs.String("out", "", ""), fs.String("install", "", "")
	tie, self, bare := fs.Bool("allow-tie", false, ""), fs.Bool("self-test", false, ""), fs.Bool("allow-bare", false, "")
	reachChecked := fs.Bool("reach-checked", false, "") // the daemon passed CheckReach on these tokens within the hour
	timeout := fs.Duration("timeout", 0, "")
	if fs.Parse(args) != nil {
		fmt.Fprintln(os.Stderr, gateUsage)
		return 2
	}
	cfg := gate.Config{Repo: *repo, Head: *head, Base: *base, Eval: *eval, Out: *out, Reps: *reps, MaxReps: *maxReps, Bench: *bench, Depth: *depth, AllowBare: *bare, Timeout: *timeout,
		Options: gate.Options{AllowTie: *tie}, Log: func(s string) { fmt.Fprintln(os.Stderr, "gate:", s) }}
	if *probes != "" {
		cfg.Probes = strings.Split(*probes, ",")
	}
	if *out == "" {
		d, err := os.MkdirTemp("", "hive-gate-")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		cfg.Out = d
	}
	if *self {
		d, _ := strconv.Atoi(os.Getenv("HIVE_GATE_DEPTH"))
		if d <= 0 {
			fmt.Fprintln(os.Stderr, "gate: --self-test runs only inside a child that may gate (HIVE_GATE_DEPTH > 0)")
			return 2
		}
		exe, _ := os.Executable()
		cfg.CandBin, cfg.CtrlBin, cfg.Reps, cfg.MaxReps, cfg.Depth = exe, exe, 1, 1, d-1
		cfg.Probes = []string{"daemon", "org"}
		if d > 1 {
			cfg.Probes = append(cfg.Probes, "recurse")
		}
		cfg.Org = gate.Local{Dir: filepath.Join(cfg.Out, "org")}
		cfg.MaxRepos = 2
		// The self-test runs inside a child's sandbox already, on local repos
		// and no token: where bubblewrap cannot nest, it runs bare in there.
		cfg.AllowBare = true
	} else {
		if *repo == "" || *head == "" || *base == "" {
			fmt.Fprintln(os.Stderr, gateUsage)
			return 2
		}
		switch {
		case *localOrg != "":
			cfg.Org = gate.Local{Dir: *localOrg}
		case *org != "":
			if *bare {
				fmt.Fprintln(os.Stderr, "gate: --allow-bare is never allowed with --org")
				return 2
			}
			tok := os.Getenv("HIVE_GATE_ORG_TOKEN")
			if *tokenFile != "" {
				raw, err := os.ReadFile(*tokenFile)
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					return 2
				}
				tok = strings.TrimSpace(string(raw))
			}
			if tok == "" {
				fmt.Fprintln(os.Stderr, "gate: --org needs HIVE_GATE_ORG_TOKEN or --org-token-file: the org's own token, never the repository owner's login")
				return 2
			}
			// The token that makes and deletes repositories stays with the gate.
			// Children get a different, contents-only token or none.
			child := os.Getenv("HIVE_GATE_CHILD_TOKEN")
			if *childFile != "" {
				raw, err := os.ReadFile(*childFile)
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					return 2
				}
				child = strings.TrimSpace(string(raw))
			}
			if child == tok {
				fmt.Fprintln(os.Stderr, "gate: the children's token must not be the token that makes and deletes repositories")
				return 2
			}
			cfg.Org, cfg.OrgToken, cfg.ChildToken, cfg.ReachOK = gate.GitHub{Owner: *org, Token: tok}, tok, child, *reachChecked
		}
	}
	// A gate a person interrupts or kills (but for SIGKILL) ends what it
	// started and tidies its run now, not when the next gate runs.
	sig := make(chan os.Signal, 1)
	var stopping atomic.Bool
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		s := <-sig
		stopping.Store(true)
		fmt.Fprintf(os.Stderr, "gate: %v: ending what the run started\n", s)
		n := gate.Reap(cfg.Out)
		fmt.Fprintf(os.Stderr, "gate: ended %d processes; logs kept in %s\n", n, cfg.Out)
		os.Exit(3)
	}()
	rep, err := gate.Run(cfg)
	signal.Stop(sig)
	if stopping.Load() {
		select {} // the handler exits once it has reaped
	}
	raw, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Println(string(raw))
	fmt.Fprintf(os.Stderr, "gate: %s (%s); report in %s\n", outcome(rep), rep.Verdict.Reason, cfg.Out)
	if rep.CantRun {
		return 3
	}
	if err != nil || !rep.Verdict.Keep {
		return 1
	}
	if *install != "" {
		if err := rep.InstallTo(*install); err != nil {
			fmt.Fprintln(os.Stderr, "gate: not installing:", err)
			return 1
		}
		fmt.Fprintln(os.Stderr, "gate: installed", *install, "at", time.Now().Format(time.RFC3339))
	}
	return 0
}

func outcome(rep gate.Report) string {
	switch {
	case rep.Verdict.Keep:
		return "KEPT"
	case rep.CantRun:
		return "COULD NOT RUN"
	}
	return "ROLLED BACK"
}
