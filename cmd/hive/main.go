// Command hive is the Hive: a small team of AI agents that lives in a folder
// on your computer and works on your projects while you do other things.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/agentcli"
	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/daemon"
	"github.com/ihsan-sa/hive-standalone-build/internal/fakeagent"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
	"github.com/ihsan-sa/hive-standalone-build/internal/setup"
	"github.com/ihsan-sa/hive-standalone-build/internal/tui"
)

const usage = `hive: a small team of AI agents working on your projects.

  hive                     open the control center (starts the Hive if needed)
  hive ask "..."           ask the lead [--project name] [--wait]
  hive status              what the Hive is doing
  hive improve             run an improvement sweep now
  hive answer <id> <opt>   answer something waiting on you
  hive login               log in to Claude Code and GitHub with their own logins
  hive doctor [--bare]     check the install; --bare opts into unsandboxed agents
  hive update              install the newest release
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
	case "login":
		if err := setup.Login(len(args) > 0 && args[0] == "--all"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("Logged in.")
		return 0
	case "doctor":
		return cmdDoctor(args)
	case "update":
		return withClient(func(c *api.Client) error {
			var msg string
			if err := c.Do("POST", "/api/update", map[string]string{}, &msg); err != nil {
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
			fmt.Printf("plan %s: %s, %.0f%% of the %s window, resets %s\n", p.Harness, p.Limit.Status, p.Limit.Utilization*100, p.Limit.Window, p.Limit.ResetsAt.Local().Format("Mon 15:04"))
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
