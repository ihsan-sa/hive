// Command hive is the Hive: a small team of AI agents that lives in a folder
// on your computer and works on your projects while you do other things.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/agentcli"
	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/daemon"
	"github.com/ihsan-sa/hive-standalone-build/internal/fakeagent"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/kb"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/tools"
	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
	"github.com/ihsan-sa/hive-standalone-build/internal/setup"
	"github.com/ihsan-sa/hive-standalone-build/internal/tui"
)

const usage = `hive: a small team of AI agents working on your projects.

  hive                     open the control center (starts the Hive if needed)
  hive ask "..."           ask the lead [--project name] [--wait]
  hive status              what the Hive is doing
  hive improve             run an improvement sweep now
  hive kb [words]          search the knowledge base: notes and everything the Hive did [--limit n|all]
  hive answer <id> <opt>   answer something waiting on you
  hive approve <id> [note] say yes to it (its first option)
  hive refuse <id> [note]  say no to it (its last option)
  hive login               log in to Claude Code and GitHub with their own logins
  hive login add <name>    add another Claude login to spread runs over; add "codex" for Codex
  hive doctor [--bare]     check the install; --bare opts into unsandboxed agents
  hive egress              the services on this machine agents may reach
  hive egress grant <host:port>   open one to every sandboxed run (loopback only, e.g. 127.0.0.1:11434)
  hive egress revoke <host:port>  close it again
  hive share [--yes]       send the developer your Hive's chats, runs, notes and logs in one bundle
  hive update              install the newest release [--release <tag>: that one, and stay on it]
  hive shutdown            stop the Hive (running agents finish their turn)
  hive uninstall           remove autostart and the binary's record; leaves your folder
  hive --version
`

func main() {
	// Messages name the command the person actually runs (hive, hive-v0, ...).
	if name := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe"); strings.HasPrefix(name, "hive") && !strings.Contains(name, "linux") {
		core.Cmd = name
	}
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	cmd := ""
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	// Internal commands, used inside sandboxes and by the Hive itself.
	case "agent":
		return agentcli.Main(args)
	case "fake-agent":
		return fakeagent.Main(args)
	case "sandbox-exec":
		fs := flag.NewFlagSet("sandbox-exec", flag.ContinueOnError)
		spec := fs.String("spec", "", "")
		fs.Parse(args)
		return sandbox.Exec(*spec)
	case "sandbox-init":
		if len(args) != 1 {
			return 2
		}
		return sandbox.Init(args[0])
	case "tools-fetch":
		if len(args) < 2 || len(args) > 3 {
			return 2
		}
		v, err := tools.Fetch(args[0], args[1], strings.Join(args[2:], ""))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println(v)
		return 0
	case "sandbox-inner":
		return sandbox.Inner(args)
	case "sandbox-probe":
		sb, err := sandbox.Select(sandbox.Options{Mode: "auto"})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("ok", sb.Name())
		return 0
	case "daemon":
		return cmdDaemon(args)

	case "", "open":
		return cmdOpen()
	case "init":
		return firstRun()
	case "-v", "--version", "version":
		fmt.Println(core.Cmd, core.Version)
		return 0
	case "-h", "--help", "help":
		fmt.Print(usageText())
		return 0
	case "ask":
		return cmdAsk(args)
	case "status":
		return cmdStatus()
	case "kb":
		ws, err := workspace()
		if err != nil {
			fmt.Fprintln(os.Stderr, core.Cmd+":", err)
			return 1
		}
		query, limit := kbArgs(args)
		if query == "" {
			fmt.Println("The knowledge base is the folder " + ws.KB() + "; its record/ has a file per task, request, run, chat and project.")
			return 0
		}
		fmt.Println(kb.Find(ws.KB(), query, limit))
		return 0
	case "improve":
		return withClient(func(c *api.Client) error {
			var s map[string]any
			if err := c.Do("POST", "/api/improve", map[string]string{}, &s); err != nil {
				return err
			}
			fmt.Println("sweep started:", s["id"])
			return nil
		})
	case "answer":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: hive answer <id> <option> [note]")
			return 2
		}
		return withClient(func(c *api.Client) error {
			note := strings.Join(args[2:], " ")
			return c.Do("POST", "/api/answer", map[string]string{"id": args[0], "answer": args[1], "note": note}, nil)
		})
	case "approve", "refuse":
		if len(args) < 1 {
			fmt.Fprintf(os.Stderr, "usage: hive %s <id> [note]\n", cmd)
			return 2
		}
		return withClient(func(c *api.Client) error {
			st, err := c.State()
			if err != nil {
				return err
			}
			for _, a := range st.Asks {
				if a.ID == args[0] {
					opt, err := approveOption(cmd, a)
					if err != nil {
						return err
					}
					fmt.Println(cmd+"d:", a.Title, "->", opt)
					return c.Do("POST", "/api/answer", map[string]string{"id": a.ID, "answer": opt, "note": strings.Join(args[1:], " ")}, nil)
				}
			}
			return fmt.Errorf("nothing waiting on you has id %s (%s status lists them)", args[0], core.Cmd)
		})
	case "login":
		if len(args) > 0 && args[0] == "add" {
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "usage: hive login add <name> [claude|codex]")
				return 2
			}
			ws, err := workspace()
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			h := "claude"
			if len(args) > 2 {
				h = args[2]
			}
			if err := setup.AddLogin(ws, h, args[1]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			fmt.Printf("Added login %s; runs now spread over it too.\n", args[1])
			return 0
		}
		if err := setup.Login(len(args) > 0 && args[0] == "--all"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("Logged in.")
		return 0
	case "doctor":
		return cmdDoctor(args)
	case "egress":
		return cmdEgress(args)
	case "share":
		return cmdShare(args)
	case "update":
		tag, ok := releaseArg(args)
		if !ok {
			fmt.Fprintln(os.Stderr, "usage: "+core.Cmd+" update [--release <tag>|latest]")
			return 2
		}
		return withClient(func(c *api.Client) error {
			var msg string
			if err := c.Do("POST", "/api/update", map[string]string{"release": tag}, &msg); err != nil {
				return err
			}
			fmt.Println(msg)
			return nil
		})
	case "shutdown":
		ws, _ := core.FindWorkspace()
		if err := daemon.Stop(ws); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("The Hive is stopped. Agents mid-turn finish on their own; `" + core.Cmd + "` starts it again.")
		return 0
	case "uninstall":
		return cmdUninstall()
	}
	fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usageText())
	return 2
}

func usageText() string {
	return strings.ReplaceAll(strings.ReplaceAll(usage, "\n  hive", "\n  "+core.Cmd), "hive: a small", core.Cmd+": a small")
}

func workspace() (core.Workspace, error) {
	ws, ok := core.FindWorkspace()
	if !ok {
		return ws, errors.New("no Hive here yet; run `" + core.Cmd + "` to set one up")
	}
	return ws, nil
}

func withClient(fn func(*api.Client) error) int {
	ws, err := workspace()
	if err == nil {
		var c *api.Client
		if c, err = daemon.Start(ws); err == nil {
			err = fn(c)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, core.Cmd+":", err)
		return 1
	}
	return 0
}

func cmdDaemon(args []string) int {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	waitFor := fs.Int("wait-for", 0, "")
	detach := fs.Bool("detach", false, "")
	fs.Parse(args)
	ws, err := workspace()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if *detach {
		if _, err := daemon.Start(ws); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	if err := daemon.Run(ws, *waitFor); err != nil {
		fmt.Fprintln(os.Stderr, core.Cmd+" daemon:", err)
		return 1
	}
	return 0
}

func cmdOpen() int {
	ws, ok := core.FindWorkspace()
	if !ok {
		return firstRun()
	}
	c, err := daemon.Start(ws)
	if err != nil {
		fmt.Fprintln(os.Stderr, core.Cmd+":", err)
		return 1
	}
	if !setup.AutostartEnabled() {
		setup.EnableAutostart(ws.Root)
	}
	if err := tui.Run(c, ws); err != nil {
		fmt.Fprintln(os.Stderr, core.Cmd+":", err)
		return 1
	}
	return 0
}

// releaseArg is the tag `update` was asked for, "" for the newest; ok is
// false for any other arguments, so a misspelt flag never installs the newest.
func releaseArg(args []string) (tag string, ok bool) {
	switch {
	case len(args) == 0:
		return "", true
	case len(args) == 2 && args[0] == "--release" && args[1] != "":
		return args[1], true
	case len(args) == 1 && strings.HasPrefix(args[0], "--release=") && args[0] != "--release=":
		return strings.TrimPrefix(args[0], "--release="), true
	}
	return "", false
}

func cmdAsk(args []string) int {
	// Flags may come before or after the text.
	var words []string
	project, wait := new(string), new(bool)
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--wait" || a == "-wait":
			*wait = true
		case (a == "--project" || a == "-project") && i+1 < len(args):
			i++
			*project = args[i]
		case strings.HasPrefix(a, "--project="):
			*project = strings.TrimPrefix(a, "--project=")
		default:
			words = append(words, a)
		}
	}
	text := strings.Join(words, " ")
	if strings.TrimSpace(text) == "" {
		fmt.Fprintln(os.Stderr, `usage: hive ask "what you want" [--project name] [--wait]`)
		return 2
	}
	chatID := "lead"
	if *project != "" {
		chatID = "project:" + *project
	}
	return withClient(func(c *api.Client) error {
		var msg chat.Message
		if err := c.Do("POST", "/api/chat", map[string]string{"chat": chatID, "text": text}, &msg); err != nil {
			return err
		}
		if !*wait {
			fmt.Println("Sent to the lead. Its reply shows in the control center (`" + core.Cmd + "`), or use --wait.")
			return nil
		}
		fmt.Println("Sent. Waiting for the lead's reply...")
		deadline := time.Now().Add(10 * time.Minute)
		for time.Now().Before(deadline) {
			var msgs []chat.Message
			if err := c.Do("GET", "/api/chat?id="+chatID, nil, &msgs); err == nil {
				for _, m := range msgs {
					if m.Thread == msg.Thread && m.ID != msg.ID && m.From != "person" {
						fmt.Printf("\n%s: %s\n", m.Name, m.Text)
						return nil
					}
				}
			}
			time.Sleep(2 * time.Second)
		}
		return errors.New("no reply within 10 minutes; it will show in the control center")
	})
}

func cmdStatus() int {
	return withClient(func(c *api.Client) error {
		st, err := c.State()
		if err != nil {
			return err
		}
		fmt.Printf("Hive %s  ·  %s\n", st.Version, st.Workspace)
		if st.Config.ReleasePin != "" {
			fmt.Printf("pinned to %s, auto-update off: `%s update` goes back to the newest\n", st.Config.ReleasePin, core.Cmd)
		}
		if st.SandboxErr != "" {
			fmt.Printf("sandbox: NOT AVAILABLE (%s)\n", core.Clip(st.SandboxErr, 200))
		} else {
			fmt.Printf("sandbox: %s\n", st.Sandbox)
		}
		for _, p := range st.Projects {
			best := "-"
			if p.Best != nil {
				best = strconv.FormatFloat(*p.Best, 'g', -1, 64)
			}
			fmt.Printf("\n%s (%s)  best score: %s\n", p.Name, p.Repo, best)
			for _, t := range p.Tasks {
				if !t.Terminal() {
					fmt.Printf("  [%s] %s  %s\n", t.Stage, t.Label(), t.PR.URL)
				}
			}
		}
		live := 0
		for _, r := range st.Runs {
			if !r.Terminal() {
				live++
			}
		}
		fmt.Printf("\nagents: %d live\n", live)
		for _, p := range st.Plans {
			fmt.Println(planLine(p, time.Now()))
		}
		if len(st.Asks) > 0 {
			fmt.Println("\nwaiting on you:")
			for _, a := range st.Asks {
				fmt.Printf("  %s  %s  (hive answer %s %s)\n", a.ID, a.Title, a.ID, strings.Join(a.Options, "|"))
			}
		}
		return nil
	})
}

func cmdDoctor(args []string) int {
	bare := len(args) > 0 && args[0] == "--bare"
	ok := true
	for _, t := range setup.Check() {
		switch {
		case !t.Found:
			ok = false
			fmt.Printf("✗ %s missing. %s\n", t.Name, t.Get)
		case t.NeedsLogin && !t.LoggedIn:
			ok = false
			fmt.Printf("✗ %s is not logged in. Run `%s login`.\n", t.Name, core.Cmd)
		default:
			fmt.Printf("✓ %s\n", t.Name)
		}
	}
	ws, found := core.FindWorkspace()
	if !found {
		fmt.Println("✗ no workspace yet; run `" + core.Cmd + "`")
		return 1
	}
	fmt.Printf("✓ workspace %s\n", ws.Root)
	c := &core.Core{WS: ws}
	cfg, err := ws.ReadConfig()
	if err != nil {
		ok = false
		fmt.Printf("✗ settings: %v\n", err)
	}
	if cfg.Sandbox == "bare" && cfg.BareAccepted != "" {
		fmt.Printf("! agents run WITHOUT a sandbox (opted in %s); set \"sandbox\": \"auto\" in Settings to go back\n", cfg.BareAccepted)
	}
	if msg, err := setup.SandboxReport(ws, c.HiveLinux()); err != nil {
		ok = false
		fmt.Printf("✗ sandbox: %v\n", err)
		if bare {
			fmt.Println("\n" + sandbox.BareWarning)
			if setup.Prompt("Type `bare` to run agents without a sandbox", "") == "bare" {
				setup.AcceptBare(ws)
				fmt.Println("Bare runs are on. Change it back in Settings, or set \"sandbox\": \"auto\" in .hive/config.json.")
			}
		}
	} else {
		fmt.Println("✓ " + msg)
		if !setup.WSLClaude(ws.LoadConfig().WSLDistro) {
			ok = false
			fmt.Println("✗ Claude Code is not installed inside WSL. Install it there:\n    wsl -- bash -lc \"curl -fsSL https://claude.ai/install.sh | bash\"")
		}
	}
	if msg, err := setup.CodexReport(ws, c.HiveLinux()); err != nil {
		ok = false
		fmt.Printf("✗ %v\n", err)
	} else if msg != "" {
		fmt.Println("✓ " + msg)
	}
	if got := tools.Installed(ws.Tools()); len(got) > 0 {
		fmt.Println("✓ tools agents installed: " + strings.Join(got, ", "))
	} else {
		fmt.Println("· no tools installed yet (agents install python, uv, node and go themselves)")
	}
	if setup.AutostartEnabled() {
		fmt.Println("✓ starts with the machine")
	} else {
		fmt.Println("· doesn't start with the machine (run `" + core.Cmd + "` once to set it up)")
	}
	if _, running := daemon.Running(ws); running {
		fmt.Println("✓ running")
	} else {
		fmt.Println("· not running")
	}
	if !ok {
		return 1
	}
	return 0
}

// cmdEgress lists, grants or revokes the services on this machine sandboxed
// runs may reach (egress.local). Each run reads the config as it starts, so a
// grant holds from the next turn on.
func cmdEgress(args []string) int {
	ws, err := workspace()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	cfg := ws.LoadConfig()
	if len(args) == 0 {
		if len(cfg.Egress.Local) == 0 {
			fmt.Println("No service on this machine is open to agents.")
		}
		for _, l := range cfg.Egress.Local {
			fmt.Println(l)
		}
		return 0
	}
	if len(args) != 2 || (args[0] != "grant" && args[0] != "revoke") {
		fmt.Fprintln(os.Stderr, "usage: "+core.Cmd+" egress [grant|revoke <host:port>]")
		return 2
	}
	want, err := sandbox.LocalGrant(args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// Entries match by what they open, so revoking 127.0.0.1:11434 also
	// closes a localhost:11434 grant that covers it.
	var keep []string
	for _, l := range cfg.Egress.Local {
		has, err := sandbox.LocalGrant(l)
		if err == nil && slices.ContainsFunc(has, func(a string) bool { return slices.Contains(want, a) }) {
			continue
		}
		keep = append(keep, l)
	}
	if args[0] == "grant" {
		keep = append(keep, args[1])
	}
	cfg.Egress.Local = keep
	if err := ws.SaveConfig(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if args[0] == "grant" {
		fmt.Printf("Agents can reach %s from their next turn.\n", args[1])
	} else {
		fmt.Printf("Agents no longer reach %s from their next turn.\n", args[1])
	}
	return 0
}

func cmdUninstall() int {
	ws, _ := core.FindWorkspace()
	daemon.Stop(ws)
	setup.DisableAutostart()
	core.ForgetWorkspace()
	fmt.Printf("Autostart removed. Your folder %s is untouched.\nDelete the binary to finish: %s\n", ws.Root, setup.Executable())
	return 0
}

func pretty(v any) string {
	raw, _ := json.MarshalIndent(v, "", "  ")
	return string(raw)
}

// kbArgs splits `hive kb` arguments into the search words and a --limit
// value, which may come before, between or after the words.
func kbArgs(args []string) (query, limit string) {
	var words []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--limit" && i+1 < len(args) {
			limit = args[i+1]
			i++
			continue
		}
		words = append(words, args[i])
	}
	return strings.Join(words, " "), limit
}

// planLine is a plan's usage for hive status. The reset carries its zone and
// how far off it is, and a reading whose window has since reset says so
// rather than passing its old number off as current.
func planLine(p core.Plan, now time.Time) string {
	l := p.Limit
	at := l.ResetsAt.Local().Format("Mon 15:04 MST")
	if !l.ResetsAt.After(now) {
		return fmt.Sprintf("plan %s: the %s window reset at %s, no reading since (last %.0f%%, read %s ago)", p.Key(), l.Window, at, l.Utilization*100, now.Sub(p.At).Round(time.Minute))
	}
	s := fmt.Sprintf("plan %s: %s, %.0f%% of the %s window, resets in %s (%s)", p.Key(), l.Status, l.Utilization*100, l.Window, l.ResetsAt.Sub(now).Round(time.Minute), at)
	for _, k := range slices.Sorted(maps.Keys(l.Windows)) {
		if k != l.Window {
			s += fmt.Sprintf("; %s %.0f%%", k, l.Windows[k].Utilization*100)
		}
	}
	return s
}

// approveOption is the option approve (the first) or refuse (the last) picks;
// an ask with no options takes the person's own words through hive answer.
func approveOption(cmd string, a core.Ask) (string, error) {
	if len(a.Options) == 0 {
		return "", fmt.Errorf("%s has no options to %s: answer it in your own words with %s answer %s <text>", a.ID, cmd, core.Cmd, a.ID)
	}
	if cmd == "refuse" {
		return a.Options[len(a.Options)-1], nil
	}
	return a.Options[0], nil
}
