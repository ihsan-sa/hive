// Command hive is the Hive: a small team of AI agents that lives in a folder
// on your computer and works on your projects while you do other things.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
	"github.com/ihsan-sa/hive-standalone-build/internal/text"
	"maps"
	"os"
	"os/exec"
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
	"github.com/ihsan-sa/hive-standalone-build/internal/gate"
	"github.com/ihsan-sa/hive-standalone-build/internal/gocache"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/kb"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/models"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/report"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/tools"
	"github.com/ihsan-sa/hive-standalone-build/internal/openagent"
	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
	"github.com/ihsan-sa/hive-standalone-build/internal/setup"
	"github.com/ihsan-sa/hive-standalone-build/internal/tui"
)

const usage = `hive: a small team of AI agents working on your projects.

  hive                     open the control center (starts the Hive if needed)
  hive ask "..."           ask the lead [--project name] [--wait]
  hive status              what the Hive is doing
  hive improve             run an improvement sweep now
  hive gate ...            try a candidate Hive against the current one in child Hives (docs/gate.md)
  hive kb [words]          search the knowledge base: notes and everything the Hive did [--limit n|all]
  hive answer <id> <opt>   answer something waiting on you
  hive approve <id> [note] say yes to it (its first option)
  hive refuse <id> [note]  say no to it (its last option)
  hive read <task|#pr> <sha> land|fix --by perms|architect [note]
                           record the box's perms seat's or architect's read of that head
  hive hold <project> [why]  hold every merge on it, the Hive's own included [--only 12,15] [--until release]
  hive hold <project> --lift [why]  lift the hold
  hive login               log in to Claude Code and GitHub with their own logins
  hive login add <name>    add another Claude login to spread runs over; add "codex" for Codex
  hive doctor [--bare]     check the install; --bare opts into unsandboxed agents
  hive report [on|off|show|send]  sharing failures with the developer: state, switch, last report, send now
  hive egress              the services on this machine agents may reach
  hive egress grant <host:port>   open one to every sandboxed run (loopback only, e.g. 127.0.0.1:11434)
  hive egress revoke <host:port>  close it again
  hive model               the named models (free ones too) and the routes that pick each role's model
  hive model add <name> <endpoint> <model id> [--key] [--no-probe]   a model on an OpenAI-compatible endpoint, e.g.
                           hive model add qwen http://127.0.0.1:11434/v1 qwen2.5-coder:14b
                           [--anthropic]: its API is Anthropic-compatible, so Claude Code runs it, e.g.
                           hive model add kimi https://api.moonshot.ai/anthropic kimi-k2-0905-preview --key --anthropic
  hive model key <name>    store its API key, read from stdin (kept where no agent can read it)
  hive model rm <name>     forget it and its key
  hive harness             the harnesses agents can run on: built in, and those installed modules add
  hive harness key <name>  store a module harness's API key, read from stdin (kept where no agent can read it)
  hive harness login <name>  run a module harness's own login on this machine (what bare runs use)
  hive share [--yes]       send the developer your Hive's chats, runs, notes and logs in one bundle
                           (scanned first: it needs a handle list in .hive/private/scan-handles.txt or HIVE_SCAN_HANDLES)
  hive update              install the newest release [--release <tag>: that one, and stay on it]
  hive remote <ssh host>   open the control center here for the Hive on another machine, over ssh [--hive <its hive command>]
                           pasted images and dropped files then go from this computer to that Hive
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
	if help := commandHelp(cmd); help != "" && wantsHelp(args) {
		fmt.Print(help)
		if cmd == "ask" && len(args) > 1 {
			fmt.Fprintln(os.Stderr, "Nothing was sent. To send -h or --help as text, quote the text or put it after --: "+core.Cmd+` ask -- "..."`)
		}
		return 0
	}
	switch cmd {
	// Internal commands, used inside sandboxes and by the Hive itself.
	case "agent":
		return agentcli.Main(args)
	case "fake-agent":
		return fakeagent.Main(args)
	case "fake-codex":
		return fakeagent.Codex(args)
	case "open-agent":
		return openagent.Main(args)
	case "open-key":
		return openagent.KeyMain(args)
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
	case "gocacheprog": // a check's GOCACHEPROG (core.goBuild)
		return gocache.Main(args)
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
		if len(args) > 0 && commandHelp(args[0]) != "" {
			fmt.Print(commandHelp(args[0]))
			return 0
		}
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
	case "gate-check": // inside a gate's sandbox: ask the gate for a sealed run
		if len(args) < 3 || args[1] != "--" {
			return 2
		}
		return gate.LaunchClient(args[0], args[2:])
	case "gate-fwd": // inside a gate's sandbox: relay to the meter, run the rest
		if len(args) < 3 || args[1] != "--" {
			return 2
		}
		return gate.Forward(args[0], args[2:])
	case "gate-connect": // inside a gate's sandbox: ssh's ProxyCommand, through the meter
		if len(args) != 2 {
			return 2
		}
		return gate.Connect(args[0], args[1])
	case "gate":
		return cmdGate(args)
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
					fmt.Printf("%sd: %s -> %s%s\n", cmd, text.Visible(a.Title), text.Visible(opt), meaning(opt))
					return c.Do("POST", "/api/answer", map[string]string{"id": a.ID, "answer": opt, "note": strings.Join(args[1:], " ")}, nil)
				}
			}
			return fmt.Errorf("nothing waiting on you has id %s (%s status lists them)", args[0], core.Cmd)
		})
	case "read":
		by, rest := readArgs(args)
		if len(rest) < 3 || by == "" {
			fmt.Fprintln(os.Stderr, "usage: "+core.Cmd+" read <task|#pr> <sha> land|fix --by perms|architect [note]")
			return 2
		}
		return withClient(func(c *api.Client) error {
			body := map[string]string{"id": rest[0], "sha": rest[1], "verdict": rest[2], "by": by, "note": strings.Join(rest[3:], " ")}
			if err := c.Do("POST", "/api/read", body, nil); err != nil {
				return err
			}
			fmt.Printf("recorded: %s's read of %s at %s: %s\n", by, rest[0], rest[1], rest[2])
			return nil
		})
	case "hold":
		body, err := holdArgs(args)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			fmt.Fprint(os.Stderr, commandHelp("hold"))
			return 2
		}
		return withClient(func(c *api.Client) error {
			var msg string
			if err := c.Do("POST", "/api/hold", body, &msg); err != nil {
				return err
			}
			fmt.Printf("%s: %s\n", body["project"], msg)
			return nil
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
	case "report":
		return cmdReport(args)
	case "egress":
		return cmdEgress(args)
	case "model":
		return cmdModel(args)
	case "harness":
		return cmdHarness(args)
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
	case "remote":
		return cmdRemote(args)
	case "remote-info": // run over ssh by `hive remote`
		return cmdRemoteInfo(args, os.Stdin, os.Stdout)
	}
	fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usageText())
	return 2
}

// holdArgs reads hive hold's arguments into the body of /api/hold.
func holdArgs(args []string) (map[string]any, error) {
	body := map[string]any{}
	var words []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		flag, val, eq := strings.Cut(a, "=")
		switch flag {
		case "--lift":
			body["lift"] = true
			continue
		case "--only", "--until":
			if !eq {
				if i+1 >= len(args) {
					return nil, fmt.Errorf("%s needs a value", flag)
				}
				i++
				val = args[i]
			}
			if flag == "--until" {
				body["until"] = val
				continue
			}
			var prs []int
			for _, f := range strings.FieldsFunc(val, func(r rune) bool { return r == ',' || r == ' ' }) {
				n, err := strconv.Atoi(strings.TrimPrefix(f, "#"))
				if err != nil {
					return nil, fmt.Errorf("--only takes PR numbers, not %q", f)
				}
				prs = append(prs, n)
			}
			body["only"] = prs
			continue
		}
		words = append(words, a)
	}
	if len(words) == 0 {
		return nil, errors.New("say which project")
	}
	body["project"], body["why"] = words[0], strings.Join(words[1:], " ")
	return body, nil
}

// readArgs takes --by <reader> (or --by=<reader>) out of args.
func readArgs(args []string) (by string, rest []string) {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--by" && i+1 < len(args):
			by = args[i+1]
			i++
		case strings.HasPrefix(a, "--by="):
			by = strings.TrimPrefix(a, "--by=")
		default:
			rest = append(rest, a)
		}
	}
	return by, rest
}

func usageText() string {
	return strings.ReplaceAll(strings.ReplaceAll(usage, "\n  hive", "\n  "+core.Cmd), "hive: a small", core.Cmd+": a small")
}

// wantsHelp is true when a command's arguments carry -h or --help (before
// any --), so `hive update --help` prints help instead of installing and
// `hive ask --help` isn't sent to the lead. Only commands with help lines ask
// it; the internal ones (agent, sandbox-exec, ...) keep their own arguments.
func wantsHelp(args []string) bool {
	for _, a := range args {
		switch a {
		case "--":
			return false
		case "-h", "-help", "--help":
			return true
		}
	}
	return false
}

// unlisted is help for the commands the usage doesn't list.
var unlisted = map[string]string{
	"open":   "  hive open                open the control center (as plain `hive` does)\n",
	"init":   "  hive init                set up a Hive in a folder\n",
	"daemon": "  hive daemon              run the Hive in this terminal [--detach: in the background]\n",
}

// commandHelp is the usage lines for one command, "" for one it doesn't know.
func commandHelp(cmd string) string {
	var b strings.Builder
	for _, l := range strings.SplitAfter(usage, "\n") {
		if f := strings.Fields(l); len(f) > 1 && f[0] == "hive" && f[1] == cmd {
			b.WriteString(l)
		}
	}
	if b.Len() == 0 {
		b.WriteString(unlisted[cmd])
	}
	if b.Len() == 0 {
		return ""
	}
	return strings.ReplaceAll("\n"+b.String(), "\n  hive", "\n  "+core.Cmd)[1:]
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
		if _, err := daemon.StartAfter(ws, *waitFor); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	os.Unsetenv(sandbox.OwnerEnv) // a check's tag, if a check started this daemon
	if err := daemon.Run(ws, *waitFor); err != nil {
		fmt.Fprintln(os.Stderr, core.Cmd+" daemon:", err)
		if code, ok := err.(daemon.ExitCode); ok {
			return int(code)
		}
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
	// Flags may come before or after the text; everything after a -- is text.
	var words []string
	project, wait := new(string), new(bool)
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--":
			words = append(words, args[i+1:]...)
			i = len(args)
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
			if p.Hold != nil {
				fmt.Printf("  %s; `%s hold %s --lift` lifts it\n", text.Visible(p.Hold.Text()), core.Cmd, p.Name)
			}
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
		for _, why := range st.Held {
			fmt.Printf("  new turns %s\n", text.Visible(why))
		}
		for _, p := range st.Plans {
			fmt.Println(planLine(p, time.Now()))
		}
		for _, d := range st.Down {
			fmt.Printf("model %s, so runs skip it; tried again %s\n", text.Visible(d.Line()), d.Retry.Local().Format("Mon 15:04"))
		}
		if len(st.Asks) > 0 {
			fmt.Println("\nwaiting on you:")
			for _, a := range st.Asks {
				fmt.Printf("  %s  %s  (%s)\n", text.Visible(a.ID), text.Visible(a.Title), text.Visible(answerHint(a)))
			}
		}
		return nil
	})
}

// meaning says what an answer tells the Hive, where that isn't plain from
// its word.
func meaning(opt string) string {
	switch opt {
	case "run":
		return " (the Hive runs exactly the script you were shown)"
	case "done":
		return " (you say you did the step yourself; nothing runs)"
	case "declined", "decline":
		return " (nothing runs; the agent that asked is told no)"
	}
	return ""
}

// answerHint is how to answer an ask from a shell.
func answerHint(a core.Ask) string {
	h := fmt.Sprintf("%s answer %s %s", core.Cmd, a.ID, strings.Join(a.Options, "|"))
	if len(a.Options) >= 2 {
		h = fmt.Sprintf("%s approve|refuse %s, or %s", core.Cmd, a.ID, h)
	}
	return h
}

func cmdDoctor(args []string) int {
	bare := len(args) > 0 && args[0] == "--bare"
	ok := true
	var failed []string
	fail := func(format string, a ...any) {
		ok = false
		failed = append(failed, fmt.Sprintf(format, a...))
		fmt.Print(failed[len(failed)-1])
	}
	for _, t := range setup.Check() {
		switch {
		case !t.Found:
			fail("✗ %s missing. %s\n", t.Name, t.Get)
		case t.NeedsLogin && !t.LoggedIn:
			fail("✗ %s is not logged in. Run `%s login`.\n", t.Name, core.Cmd)
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
		fail("✗ sandbox: %v\n", err)
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
			fail("✗ Claude Code is not installed inside WSL. Install it there:\n    wsl -- bash -lc \"curl -fsSL https://claude.ai/install.sh | bash\"\n")
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
	switch twins, err := twinTurns(ws.Events(), time.Now().Add(-24*time.Hour)); {
	case err != nil:
		fail("✗ journal: %v\n", err)
	case len(twins) > 0:
		fail("✗ %d turns started in the last day while the same run's last turn still ran (issue #1022), e.g. %s\n", len(twins), twins[len(twins)-1])
	default:
		fmt.Println("✓ no turn started in the last day while its run's last turn still ran")
	}
	if _, running := daemon.Running(ws); running {
		fmt.Println("✓ running")
	} else {
		fmt.Println("· not running")
	}
	if !ok {
		report.Note(ws, report.Failure{Kind: "doctor", Text: strings.Join(failed, "")})
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

func cmdModel(args []string) int {
	ws, err := workspace()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	cfg, err := ws.ReadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	usage := func() int {
		fmt.Fprintln(os.Stderr, "usage: "+core.Cmd+" model [add <name> <endpoint> <model id> [--key] [--anthropic] [--no-probe] | key <name> | rm <name>]")
		return 2
	}
	if len(args) == 0 {
		if len(cfg.Models) == 0 {
			fmt.Println("No named models yet; add one with `" + core.Cmd + " model add`.")
		}
		for _, name := range slices.Sorted(maps.Keys(cfg.Models)) {
			x := cfg.Models[name]
			key := ""
			if _, err := os.Stat(ws.OpenKey(name)); err == nil {
				key = ", key stored"
			} else if x.Key {
				key = ", needs a key: " + core.Cmd + " model key " + name
			}
			fmt.Printf("%s: %s%s\n", name, x.Where(), key)
		}
		for _, role := range core.RouteRoles {
			if r := cfg.Shown(role, ""); len(r) > 0 {
				fmt.Printf("%s: %s\n", role, strings.Join(r, ", "))
			}
		}
		return 0
	}
	switch {
	case args[0] == "add" && len(args) >= 4:
		m, noProbe := core.OpenModel{Endpoint: args[2], Model: args[3]}, false
		for _, f := range args[4:] {
			switch f {
			case "--key":
				m.Key = true
			case "--anthropic":
				m.API = "anthropic"
			case "--no-probe":
				noProbe = true
			default:
				return usage()
			}
		}
		if cfg.Models == nil {
			cfg.Models = map[string]core.OpenModel{}
		}
		was, replaced := cfg.Models[args[1]]
		cfg.Models[args[1]] = m
		if err := cfg.Check(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if m.Key && cfg.Plaintext(args[1]) {
			fmt.Fprintf(os.Stderr, "%s is plain http on a public host, so its key would cross the network unencrypted; use its https:// endpoint\n", m.Endpoint)
			return 1
		}
		if replaced {
			fmt.Printf("Replacing %s (was %s).\n", args[1], was.Where())
		}
		// One real request first: a model that can't answer isn't added.
		var err error
		if !noProbe {
			err = models.Probe(ws, args[1], m)
		}
		switch {
		case noProbe:
			fmt.Println("Not tried (--no-probe); runs mark it down if it can't answer.")
		case errors.Is(err, models.ErrNoKey):
			fmt.Printf("Not tried yet: %v; storing it tries it.\n", err)
		case err != nil:
			fmt.Fprintf(os.Stderr, "%s can't answer, so it wasn't added: %v\n%s\n", args[1], err, probeFix(cfg, args[1], err))
			return 1
		default:
			fmt.Printf("%s answered.\n", args[1])
		}
	case args[0] == "rm" && len(args) == 2:
		if _, ok := cfg.Models[args[1]]; !ok {
			fmt.Fprintf(os.Stderr, "no model %q\n", args[1])
			return 1
		}
		// A route naming it would read the bare name as a model of the
		// harness once it's gone, so both stay until no route does.
		if on := routesNaming(cfg, args[1]); len(on) > 0 {
			fmt.Fprintf(os.Stderr, "%s is still named by %s; take it off there first (Settings, or ask the lead), so nothing was removed.\n", args[1], strings.Join(on, ", "))
			return 1
		}
		delete(cfg.Models, args[1])
		// The key goes only once the config without the model is saved.
		if err := cfg.Check(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := ws.SaveConfig(cfg); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		os.Remove(ws.OpenKey(args[1]))
		fmt.Println("Removed " + args[1] + " and its key.")
		return 0
	case args[0] == "key" && len(args) == 2:
		if _, ok := cfg.Models[args[1]]; !ok {
			fmt.Fprintf(os.Stderr, "no model %q; add it first\n", args[1])
			return 1
		}
		if cfg.Plaintext(args[1]) {
			fmt.Fprintf(os.Stderr, "%s is plain http on a public host, so its key would cross the network unencrypted; point it at its https:// endpoint first (%s model add)\n", cfg.Models[args[1]].Endpoint, core.Cmd)
			return 1
		}
		fmt.Fprintln(os.Stderr, "Paste the key and press Enter:")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if err := ws.SaveOpenKey(args[1], line); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("Stored; runs on " + args[1] + " use it from their next turn.")
		if err := models.Probe(ws, args[1], cfg.Models[args[1]]); err != nil {
			fmt.Fprintf(os.Stderr, "But %s can't answer with it: %v\n%s\n", args[1], err, probeFix(cfg, args[1], err))
			return 1
		}
		fmt.Printf("%s answered.\n", args[1])
		return 0
	default:
		return usage()
	}
	if err := cfg.Check(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := ws.SaveConfig(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println("Saved. Route a role to it in Settings, or ask the lead.")
	return 0
}

// routesNaming are the routes and model settings that name model name, as
// "worker", "worker in lab" or "the lead model".
func routesNaming(cfg core.Config, name string) []string {
	var out []string
	for what, m := range map[string]string{"the worker model": cfg.Model, "the lead model": cfg.LeadModel, "the reviewer model": cfg.ReviewerModel} {
		if m == name {
			out = append(out, what)
		}
	}
	slices.Sort(out)
	for _, t := range cfg.TribunalModels {
		if t.Model == name && (t.Harness == "" || t.Harness == "open") {
			out = append(out, "a tribunal seat")
		}
	}
	for _, p := range slices.Sorted(maps.Keys(cfg.Projects)) {
		if cfg.Projects[p].Model == name {
			out = append(out, "the model for "+p)
		}
	}
	for _, role := range slices.Sorted(maps.Keys(cfg.Routes)) {
		if slices.Contains(cfg.Routes[role], name) || slices.Contains(cfg.Routes[role], "open:"+name) {
			out = append(out, role)
		}
	}
	for _, p := range slices.Sorted(maps.Keys(cfg.Projects)) {
		routes := cfg.Projects[p].Routes
		for _, role := range slices.Sorted(maps.Keys(routes)) {
			if slices.Contains(routes[role], name) || slices.Contains(routes[role], "open:"+name) {
				out = append(out, role+" in "+p)
			}
		}
	}
	return out
}

func cmdHarness(args []string) int {
	ws, err := workspace()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	harness.UseModules(ws.Modules())
	spec := func(name string) (*harness.Spec, bool) {
		h, err := harness.Get(name)
		s, ok := h.(*harness.Spec)
		if err != nil || !ok {
			fmt.Fprintf(os.Stderr, "%q is not a module's harness; `%s harness` lists them\n", name, core.Cmd)
		}
		return s, ok
	}
	switch {
	case len(args) == 0:
		specs, bad := harness.Declared()
		fmt.Println("Built in: claude, codex, open, fake.")
		for _, s := range specs {
			found := "not installed: `" + s.Program() + "` isn't on PATH"
			if _, err := exec.LookPath(s.Program()); err == nil {
				found = "`" + s.Program() + "` found"
			}
			key := ""
			if s.KeyEnv != "" {
				if _, err := os.Stat(ws.HarnessKey(s.Name())); err == nil {
					key = ", key stored"
				} else if !s.KeyOptional {
					key = ", sandboxed runs need a key: " + core.Cmd + " harness key " + s.Name()
				}
			}
			fmt.Printf("%s (%s, from %s): %s%s\n", s.Name(), s.Display(), filepath.Base(filepath.Dir(s.File())), found, key)
		}
		for _, b := range bad {
			fmt.Println("not loaded:", b)
		}
		fmt.Println("Pick one in Settings (harness), per project, or per role in routes.")
		return 0
	case len(args) == 2 && args[0] == "key":
		s, ok := spec(args[1])
		if !ok {
			return 1
		}
		if s.KeyEnv == "" {
			fmt.Fprintf(os.Stderr, "%s takes no API key; log in with `%s harness login %s`\n", s.Name(), core.Cmd, s.Name())
			return 1
		}
		fmt.Fprintln(os.Stderr, "Paste the key and press Enter:")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if err := ws.SaveHarnessKey(s.Name(), line); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("Stored; runs on " + s.Name() + " use it from their next turn.")
		return 0
	case len(args) == 2 && args[0] == "login":
		s, ok := spec(args[1])
		if !ok {
			return 1
		}
		argv := s.LoginCommand()
		if len(argv) == 0 {
			fmt.Fprintf(os.Stderr, "%s has no login command of its own\n", s.Name())
			return 1
		}
		// The spec comes from a module: say what it runs as the person, here.
		fmt.Printf("Running %s's login from the %s module: %s\n", s.Name(), filepath.Base(filepath.Dir(s.File())), strings.Join(argv, " "))
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "%s login: %v\n", s.Name(), err)
			return 1
		}
		return 0
	}
	fmt.Fprint(os.Stderr, commandHelp("harness"))
	return 2
}

// probeFix is what would let named model name answer, read from its probe's error.
func probeFix(cfg core.Config, name string, err error) string {
	return core.Down{Pick: core.Pick{Harness: "open", Model: name}, Status: openagent.Status(err), Text: err.Error()}.Fix(cfg)
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

// planLine is a plan's usage for hive status, in plain words: how near its
// limit the login is, then each window's share and when it resets, with the
// zone and how far off. A window that has reset since the reading says so
// rather than passing its old number off as current. Names from the usage
// feed are escaped, so none can rewrite the screen.
func planLine(p core.Plan, now time.Time) string {
	s := fmt.Sprintf("plan %s: %s", text.Visible(p.Key()), text.Visible(p.Standing(now)))
	for _, w := range p.Limit.WindowUses(now) {
		name, at := text.Visible(w.Name), w.ResetsAt.Local().Format("Mon 15:04 MST")
		switch {
		case w.ResetsAt.IsZero():
			s += fmt.Sprintf("; %s window %.0f%% used", name, w.Used*100)
		case w.Stale:
			s += fmt.Sprintf("; %s window reset at %s, no reading since (was %.0f%%)", name, at, w.Used*100)
		default:
			s += fmt.Sprintf("; %s window %.0f%% used, resets in %s (%s)", name, w.Used*100, w.ResetsIn(now), at)
		}
	}
	if !p.At.IsZero() {
		s += " (read " + harness.Until(now, p.At) + " ago)"
	}
	return s
}

// approveOption is the option approve (the first) or refuse (the last) picks;
// an ask with no options takes the person's own words through hive answer,
// and one with a single option (a told "seen") has no yes and no to pick.
func approveOption(cmd string, a core.Ask) (string, error) {
	if len(a.Options) == 0 {
		return "", fmt.Errorf("%s has no options to %s: answer it in your own words with %s answer %s <text>", a.ID, cmd, core.Cmd, a.ID)
	}
	if len(a.Options) == 1 { // a told "seen" has no yes and no to tell apart
		return "", fmt.Errorf("%s has no yes and no to %s between: answer it with %s answer %s %s", a.ID, cmd, core.Cmd, a.ID, a.Options[0])
	}
	if cmd == "refuse" {
		return a.Options[len(a.Options)-1], nil
	}
	return a.Options[0], nil
}
