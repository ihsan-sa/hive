package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/daemon"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/projects"
	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
	"github.com/ihsan-sa/hive-standalone-build/internal/setup"
	"github.com/ihsan-sa/hive-standalone-build/internal/tui"
)

// firstRun takes a fresh install from nothing to a lead that answers: check
// the tools, offer their logins, pick the folder, check the sandbox, set up
// the first project, start with the machine, open the control center.
func firstRun() int {
	fmt.Print("Welcome to the Hive.\n\n")
	missing := false
	for _, t := range setup.Check() {
		if !t.Found {
			missing = true
			fmt.Printf("  missing: %s\n    %s\n", t.Name, t.Get)
		}
	}
	if missing {
		fmt.Println("\nInstall what's missing, then run `" + core.Cmd + "` again.")
		return 1
	}
	for _, t := range setup.Check() {
		if t.NeedsLogin && !t.LoggedIn {
			if strings.HasPrefix(strings.ToLower(setup.Prompt(t.Name+" isn't logged in. Log in now with its own login? (Y/n)", "y")), "y") {
				if err := setup.Login(false); err != nil {
					fmt.Fprintln(os.Stderr, err)
					return 1
				}
			} else {
				fmt.Println("The Hive needs both logins. Run `" + core.Cmd + " login` when ready.")
				return 1
			}
			break
		}
	}

	root := setup.Prompt("\nWhere should the Hive live?", core.DefaultRoot())
	if strings.HasPrefix(root, "~") {
		root = filepath.Join(core.HomeDir(), strings.TrimPrefix(root, "~"))
	}
	root, _ = filepath.Abs(root)
	ws := core.Workspace{Root: root}
	if err := ws.Init(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := core.RememberWorkspace(root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	c := &core.Core{WS: ws}
	if msg, err := setup.SandboxReport(ws, c.HiveLinux()); err != nil {
		fmt.Printf("\nAgents need a sandbox, and this machine doesn't have one ready:\n  %v\n\n%s\n", err, sandbox.BareWarning)
		if setup.Prompt("Type `bare` to run agents without a sandbox anyway, or press Enter to fix it first", "") == "bare" {
			setup.AcceptBare(ws)
		} else {
			fmt.Println("Agents won't start until the sandbox works (`" + core.Cmd + " doctor` checks it). Continuing setup.")
		}
	} else {
		fmt.Println("\n✓ " + msg)
		if !setup.WSLClaude(ws.LoadConfig().WSLDistro) {
			fmt.Println("  Claude Code isn't installed inside WSL yet. Install it there:\n    wsl -- bash -lc \"curl -fsSL https://claude.ai/install.sh | bash\"")
		}
	}

	repo := setup.Prompt("\nGitHub repository for your first project (owner/name; a new name is created; blank for a small demo)", "")
	client, err := daemon.Start(ws)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	spec := projects.Spec{Demo: repo == ""}
	if repo != "" {
		spec.Repo = repo
		parts := strings.Split(repo, "/")
		spec.Name = parts[len(parts)-1]
		spec.Description = setup.Prompt("In a sentence, what is it for?", "")
		spec.Goals = spec.Description
	} else {
		spec.Name, spec.Description = "textkit", "A small text library; the demo the Hive improves on its first run."
	}
	fmt.Println("Setting up the project...")
	var p core.Project
	if err := client.Do("POST", "/api/project", spec, &p); err != nil {
		fmt.Printf("  project setup didn't finish: %s\n  The Hive keeps retrying in the background; the Dashboard shows how it's going.\n", core.Clip(err.Error(), 400))
	} else {
		fmt.Printf("✓ project %s (%s)\n", p.Name, p.Repo)
	}

	if err := setup.EnableAutostart(root); err != nil {
		fmt.Println("  couldn't register autostart:", err)
	} else {
		fmt.Println("✓ starts with the machine")
	}
	fmt.Println("\nOpening the control center. Type to the lead; Tab switches views, ? shows keys.")
	if err := tui.Run(client, ws); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
