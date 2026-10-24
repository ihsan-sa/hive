package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/gate"
)

const gateUsage = `usage: hive gate --repo <git> --head <sha> --base <sha> [--org Hive-RSI-Lab | --local-org <dir>]
                 [--eval <sha, default base>] [--reps 3] [--bench 4] [--probes daemon,org,e2e,benchmark,recurse] [--depth 1]
                 [--lessons .hive/lessons.md] [--allow-tie] [--install <path>] [--out <dir>]
       hive gate --self-test [--out <dir>]      the gate on a trivial change, inside a child
       hive gate install --bin <new> --target <path> [--version <v>]
Exit 0 keeps the candidate, 1 rolls it back, 2 is a usage error. The report is JSON on stdout.`

func cmdGate(args []string) int {
	if len(args) > 0 && args[0] == "install" {
		fs := flag.NewFlagSet("gate install", flag.ContinueOnError)
		bin, target, ver := fs.String("bin", "", ""), fs.String("target", "", ""), fs.String("version", "", "")
		if fs.Parse(args[1:]) != nil || *bin == "" || *target == "" {
			fmt.Fprintln(os.Stderr, gateUsage)
			return 2
		}
		if err := gate.Install(*bin, *target, *ver); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("installed", *target, "(previous kept as "+*target+".prev)")
		return 0
	}
	fs := flag.NewFlagSet("gate", flag.ContinueOnError)
	repo, head, base, eval := fs.String("repo", "", ""), fs.String("head", "", ""), fs.String("base", "", ""), fs.String("eval", "", "")
	org, localOrg, tokenFile := fs.String("org", "", ""), fs.String("local-org", "", ""), fs.String("org-token-file", "", "")
	reps, bench, depth := fs.Int("reps", 3, ""), fs.Int("bench", 4, ""), fs.Int("depth", 1, "")
	probes, lessons, out, install := fs.String("probes", "", ""), fs.String("lessons", "", ""), fs.String("out", "", ""), fs.String("install", "", "")
	tie, self := fs.Bool("allow-tie", false, ""), fs.Bool("self-test", false, "")
	if fs.Parse(args) != nil {
		fmt.Fprintln(os.Stderr, gateUsage)
		return 2
	}
	cfg := gate.Config{Repo: *repo, Head: *head, Base: *base, Eval: *eval, Out: *out, Reps: *reps, Bench: *bench, Depth: *depth, Lessons: *lessons,
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
		cfg.CandBin, cfg.CtrlBin, cfg.Reps, cfg.Depth = exe, exe, 1, d-1
		cfg.Probes = []string{"daemon", "org"}
		if d > 1 {
			cfg.Probes = append(cfg.Probes, "recurse")
		}
		cfg.Org = gate.Local{Dir: filepath.Join(cfg.Out, "org")}
		cfg.MaxRepos = 2
	} else {
		if *repo == "" || *head == "" || *base == "" {
			fmt.Fprintln(os.Stderr, gateUsage)
			return 2
		}
		switch {
		case *localOrg != "":
			cfg.Org = gate.Local{Dir: *localOrg}
		case *org != "":
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
			cfg.Org, cfg.OrgToken = gate.GitHub{Owner: *org, Token: tok}, tok
		}
	}
	rep, err := gate.Run(cfg)
	raw, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Println(string(raw))
	fmt.Fprintf(os.Stderr, "gate: %s (%s); report in %s\n", map[bool]string{true: "KEPT", false: "ROLLED BACK"}[rep.Verdict.Keep], rep.Verdict.Reason, cfg.Out)
	if err != nil || !rep.Verdict.Keep {
		return 1
	}
	if *install != "" {
		bin := cfg.CandBin
		if bin == "" {
			bin = filepath.Join(cfg.Out, "bin-candidate")
		}
		if err := gate.Install(bin, *install, ""); err != nil {
			fmt.Fprintln(os.Stderr, "gate:", err)
			return 1
		}
		fmt.Fprintln(os.Stderr, "gate: installed", *install, "at", time.Now().Format(time.RFC3339))
	}
	return 0
}
