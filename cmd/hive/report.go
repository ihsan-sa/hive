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
const shareQuestion = report.Question + " `%s report` shows what was sent. (off/failures/everything)"

// askShare is first run's question. The installer's answer, or one already in
// the config, stands; otherwise it asks, and Enter is off. Then it asks the
// separate yes or no on contributing, unless that was answered.
func askShare(ws core.Workspace) error {
	if raw, err := os.ReadFile(core.ShareAnswer()); err == nil {
		os.Remove(core.ShareAnswer())
		level, err := report.ParseLevel(string(raw))
		if err != nil {
			level = report.Off
		}
		if _, err := report.Save(ws, level, ""); err != nil {
			return err
		}
	} else if ws.LoadConfig().Share == "" {
		level, err := report.ParseLevel(setup.Prompt("\n"+fmt.Sprintf(shareQuestion, core.Cmd), "off"))
		if err != nil {
			level = report.Off
		}
		if _, err := report.Save(ws, level, ""); err != nil {
			return err
		}
	}
	if ws.LoadConfig().Contribute != "" {
		return nil
	}
	yes := strings.HasPrefix(strings.ToLower(setup.Prompt("\n"+report.ContributeQuestion+" (y/N)", "n")), "y")
	_, err := report.Save(ws, "", map[bool]string{true: "on", false: "off"}[yes])
	return err
}

// cmdReport shows what's shared, sets the level or the contribute choice,
// prints the last report sent, or sends now. `asked` exits 0 once the person
// has answered; the installers use it so a reinstall doesn't ask again.
func cmdReport(args []string) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	ws, ok := core.FindWorkspace()
	level, isLevel := "", false
	if l, err := report.ParseLevel(sub); err == nil && sub != "" {
		level, isLevel = l, true
	}
	switch {
	case sub == "asked":
		if _, err := os.Stat(core.ShareAnswer()); err == nil || ok && ws.LoadConfig().Share != "" {
			return 0
		}
		return 1
	case isLevel && !ok:
		if err := core.WriteFileAtomic(core.ShareAnswer(), []byte(level+"\n"), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println(report.WhatIsSent(level))
		fmt.Printf("Sharing with the Hive developer: %s. `%s report off|failures|everything` changes it.\n", level, core.Cmd)
		return 0
	case !ok:
		fmt.Fprintln(os.Stderr, "no Hive here yet; run `"+core.Cmd+"` to set one up")
		return 1
	case isLevel:
		if _, err := report.Save(ws, level, ""); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println(report.WhatIsSent(level))
	case sub == "contribute":
		if len(args) < 2 {
			fmt.Println(report.ContributeQuestion)
		} else if _, err := report.Save(ws, "", args[1]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
	case sub == "show":
		sent := report.Sent(ws)
		if len(sent) == 0 {
			fmt.Println("Nothing has been sent.")
			return 0
		}
		raw, _ := os.ReadFile(sent[len(sent)-1])
		fmt.Println(string(raw))
		return 0
	case sub == "send":
		return withClient(func(c *api.Client) error {
			return c.Do("POST", "/api/job", map[string]string{"name": "report.send"}, nil)
		})
	case sub != "":
		fmt.Fprintln(os.Stderr, "usage: "+core.Cmd+" report [off|failures|everything|contribute on|off|show|send]")
		return 2
	}
	cfg := ws.LoadConfig()
	since, _ := cfg.Sharing()
	switch report.Level(cfg) {
	case report.Off:
		fmt.Println("Sharing with the Hive developer is off. `" + core.Cmd + " report failures` or `" + core.Cmd + " report everything` turns it on.")
	case report.Failures:
		fmt.Printf("Sending failure and usage reports to %s since %s (install %s).\n", report.URL(cfg), since.Format("2006-01-02 15:04"), cfg.InstallID)
	case report.Everything:
		fmt.Printf("Sending everything: the reports to %s since %s, and the records in batches to %s (install %s).\n", report.URL(cfg), since.Format("2006-01-02 15:04"), report.BundleURL(cfg), cfg.InstallID)
	}
	if t, yes := report.Contributing(cfg); yes {
		fmt.Printf("Contributing to %s since %s: failures as issues, fixes as a pull request. `%s report contribute off` stops it.\n", report.Upstream(cfg), t.Format("2006-01-02 15:04"), core.Cmd)
	} else {
		fmt.Printf("Not contributing to %s. `%s report contribute on` says what it would do and turns it on.\n", report.Upstream(cfg), core.Cmd)
	}
	if sent := report.Sent(ws); len(sent) > 0 {
		fmt.Printf("%d reports sent; copies in %s, the last is `%s report show`.\n", len(sent), filepath.Dir(sent[0]), core.Cmd)
	}
	return 0
}
