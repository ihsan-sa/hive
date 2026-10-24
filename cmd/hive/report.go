package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/report"
	"github.com/ihsan-sa/hive-standalone-build/internal/setup"
)

// shareQuestion is report.Question, the installers' words, and where to see
// what was sent.
const shareQuestion = report.Question + " `%s report` shows what was sent. (y/N)"

// setShare turns sharing on or off.
func setShare(ws core.Workspace, on bool) error {
	cfg := ws.LoadConfig()
	cfg.SetShare(on)
	return ws.SaveConfig(cfg)
}

// askShare is first run's question. The installer's answer, or one already in
// the config, stands; otherwise it asks, and Enter is no.
func askShare(ws core.Workspace) error {
	if raw, err := os.ReadFile(core.ShareAnswer()); err == nil {
		os.Remove(core.ShareAnswer())
		return setShare(ws, strings.TrimSpace(string(raw)) == "on")
	}
	if ws.LoadConfig().Share != "" {
		return nil
	}
	return setShare(ws, strings.HasPrefix(strings.ToLower(setup.Prompt("\n"+fmt.Sprintf(shareQuestion, core.Cmd), "n")), "y"))
}

// cmdReport shows what's shared, turns it on or off, prints the last report
// sent, or sends one now. `asked` exits 0 once the person has answered; the
// installers use it so a reinstall doesn't ask again.
func cmdReport(args []string) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	ws, ok := core.FindWorkspace()
	switch sub {
	case "asked":
		if _, err := os.Stat(core.ShareAnswer()); err == nil || ok && ws.LoadConfig().Share != "" {
			return 0
		}
		return 1
	case "on", "off":
		if !ok {
			if err := core.WriteFileAtomic(core.ShareAnswer(), []byte(sub+"\n"), 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			fmt.Printf("Sharing failures with the developer: %s. `%s report on|off` changes it.\n", sub, core.Cmd)
			return 0
		}
		if err := setShare(ws, sub == "on"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	if !ok {
		fmt.Fprintln(os.Stderr, "no Hive here yet; run `"+core.Cmd+"` to set one up")
		return 1
	}
	switch sub {
	case "show":
		sent := report.Sent(ws)
		if len(sent) == 0 {
			fmt.Println("Nothing has been sent.")
			return 0
		}
		raw, _ := os.ReadFile(sent[len(sent)-1])
		fmt.Println(string(raw))
		return 0
	case "send":
		return withClient(func(c *api.Client) error {
			return c.Do("POST", "/api/job", map[string]string{"name": "report.send"}, nil)
		})
	case "", "on", "off":
	default:
		fmt.Fprintln(os.Stderr, "usage: "+core.Cmd+" report [on|off|show|send]")
		return 2
	}
	cfg := ws.LoadConfig()
	if since, on := cfg.Sharing(); !on {
		fmt.Println("Sharing failures with the developer is off. `" + core.Cmd + " report on` turns it on.")
	} else {
		fmt.Printf("Sending failure and usage reports to %s since %s (install %s).\n", report.URL(cfg), since.Format("2006-01-02 15:04"), cfg.InstallID)
	}
	if sent := report.Sent(ws); len(sent) > 0 {
		fmt.Printf("%d reports sent; copies in %s, the last is `%s report show`.\n", len(sent), filepath.Dir(sent[0]), core.Cmd)
	}
	return 0
}
