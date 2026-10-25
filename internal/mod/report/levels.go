package report

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// The levels of sharing with the developer, lowest first.
const (
	Off        = "off"
	Failures   = "failures"
	Everything = "everything"
)

// Levels are the choices, lowest first.
var Levels = []string{Off, Failures, Everything}

// The question's parts. Each level's sentences name what it sends;
// TestQuestionSaysWhatIsSent holds them to the reports and
// TestEverythingSaysWhatIsSent to the batches.
const (
	askLevel     = "How much should this Hive share with the Hive developer so problems get fixed: off, failures or everything?"
	saysOff      = "off sends nothing."
	saysFailures = "failures sends failure and usage reports. " +
		"Each failure report holds what failed, when and why (up to 2,000 characters), " +
		"and the agent reply or the last lines of the task log (up to 4,000 characters). " +
		"A usage report counts runs, turns, merges, reverts, cost and dropped reports, with the harness name and the time span it covers. " +
		"Every report has the Hive version, the OS and a random install id."
	saysEverything = "everything sends those reports and also the brief, notes and turn logs of each agent run, the chats, the Hive journal and the daemon log, " +
		"written from when it was chosen, in batches of up to 8 MB each time reports go."
	saysScrub = "Secrets, emails, your user and machine names and your home folder are scrubbed on this machine first."
)

// Question is the question the installers and first run ask, word for word:
// it names everything each level sends, so the person agrees to what is
// really sent. The installers quote it, so it has no ' " ` $ % or \.
const Question = askLevel + " " + saysOff + " " + saysFailures + " " + saysEverything + " " + saysScrub

// ContributeQuestion is the separate yes or no on contributing upstream.
var ContributeQuestion = "Also contribute to the open-source Hive at " + core.DefaultReleaseRepo + "? " +
	"Each new kind of failure becomes a public issue there, filed once and scrubbed as a report is, " +
	"and a fix this Hive makes to the Hive code is offered there as a pull request from your own repo. " +
	"Nothing goes there without this yes."

// WhatIsSent says what level sends.
func WhatIsSent(level string) string {
	switch level {
	case Failures:
		return saysFailures + " " + saysScrub
	case Everything:
		return saysFailures + " " + saysEverything + " " + saysScrub
	}
	return saysOff
}

// ParseLevel reads a level as the person may type it: on and yes are
// failures, as they were before there were levels.
func ParseLevel(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "off", "no", "n", "none", "nothing":
		return Off, nil
	case "failures", "failure", "f", "on", "yes", "y":
		return Failures, nil
	case "everything", "all", "e":
		return Everything, nil
	}
	return "", fmt.Errorf("sharing is off, failures or everything, not %q", s)
}

// Level is what this install shares.
func Level(cfg core.Config) string {
	if _, on := cfg.Sharing(); !on {
		return Off
	}
	if _, ok := everythingSince(cfg); ok {
		return Everything
	}
	return Failures
}

func everythingSince(cfg core.Config) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, cfg.ShareAll)
	return t, err == nil
}

// SetLevel records the person's choice. What a level adds is sent only from
// when it was chosen, and a lower level stops the rest at once.
func SetLevel(cfg *core.Config, level string) error {
	level, err := ParseLevel(level)
	if err != nil {
		return err
	}
	_, on := cfg.Sharing()
	if level == Off {
		cfg.SetShare(false)
	} else if !on {
		cfg.SetShare(true)
	}
	if level != Everything {
		cfg.ShareAll = ""
	} else if _, ok := everythingSince(*cfg); !ok {
		cfg.ShareAll = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return nil
}

// Contributing says whether the person said yes to contributing upstream,
// and since when.
func Contributing(cfg core.Config) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, cfg.Contribute)
	return t, err == nil
}

// SetContribute records the yes or no; a yes needs the install id its
// issues are counted by.
func SetContribute(cfg *core.Config, on bool) {
	cfg.Contribute = "off"
	if on {
		if _, was := Contributing(*cfg); was {
			return
		}
		cfg.Contribute = time.Now().UTC().Format(time.RFC3339Nano)
		if cfg.InstallID == "" {
			cfg.InstallID = core.NewUUID()
		}
	}
}

// Changed is called with the config before and after the person changed it
// (Settings, `hive report`, the lead): a lower level stops what the higher
// one would still send. Off drops the reports waiting, and leaving
// everything drops where the batches had got to, so choosing it again sends
// only what is written from then.
func Changed(ws core.Workspace, before, after core.Config) {
	if Level(after) == Off && Level(before) != Off {
		os.Remove(pending(ws))
	}
	if Level(after) != Everything {
		os.Remove(batchState(ws))
	} else if a, _ := everythingSince(after); before.ShareAll != after.ShareAll {
		baseline(ws, a)
	}
}

// Save changes the level and the contribute choice in ws's config; an empty
// level or contribute leaves it as it is.
func Save(ws core.Workspace, level, contribute string) (core.Config, error) {
	before, err := ws.ReadConfig()
	if err != nil {
		return before, fmt.Errorf("config.json has to be fixed first: %v", err)
	}
	cfg := before
	if level != "" {
		if err := SetLevel(&cfg, level); err != nil {
			return before, err
		}
	}
	if contribute != "" {
		yes, err := parseYes(contribute)
		if err != nil {
			return before, err
		}
		SetContribute(&cfg, yes)
	}
	if err := ws.SaveConfig(cfg); err != nil {
		return before, err
	}
	Changed(ws, before, cfg)
	return cfg, nil
}

func parseYes(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "on", "yes", "y", "true":
		return true, nil
	case "off", "no", "n", "false":
		return false, nil
	}
	return false, fmt.Errorf("contribute is on or off, not %q", s)
}

// New registers the lead's share op and the person's answer to it.
func New(c *core.Core) *Module {
	m := &Module{C: c}
	c.Runs.Ops().Register(core.Op{Name: "share", Roles: []string{"lead"}, Help: `{"level": "(optional) off|failures|everything", "contribute": "(optional) on|off"} -> what this Hive shares with the Hive developer and whether it contributes upstream, as the person asked; with neither it says what is set. Less is set at once; more waits for the person's yes in Waiting, which shows what it sends`, Fn: m.opShare})
	c.Person.Handle("share", m.onPerson)
	return m
}

type shareAsk struct {
	Level      string `json:"level,omitempty"`
	Contribute string `json:"contribute,omitempty"`
}

func (m *Module) opShare(r core.Run, args json.RawMessage) (any, error) {
	var a shareAsk
	if err := core.Bind(args, &a); err != nil {
		return nil, err
	}
	cfg := m.C.WS.LoadConfig()
	_, was := Contributing(cfg)
	now := map[string]any{"level": Level(cfg), "contribute": was, "level_sends": WhatIsSent(Level(cfg))}
	if a.Level == "" && a.Contribute == "" {
		return now, nil
	}
	var more []string
	if a.Level != "" {
		l, err := ParseLevel(a.Level)
		if err != nil {
			return nil, err
		}
		a.Level = l
		if slices.Index(Levels, l) > slices.Index(Levels, Level(cfg)) {
			more = append(more, "Share "+l+" with the Hive developer: "+WhatIsSent(l))
		}
	}
	if a.Contribute != "" {
		yes, err := parseYes(a.Contribute)
		if err != nil {
			return nil, err
		}
		if yes && !was {
			more = append(more, ContributeQuestion)
		}
	}
	if len(more) > 0 {
		raw, _ := json.Marshal(a)
		ask := m.C.Person.Ask(core.Ask{Kind: "share", Title: "Share more with the Hive developer, as the lead asks?", Detail: strings.Join(more, "\n\n"), Ref: string(raw), From: r.ID})
		return map[string]any{"ask": ask.ID, "now": now, "note": "sharing more waits for the person's yes in Waiting, which shows them what it sends; tell them so"}, nil
	}
	cfg, err := Save(m.C.WS, a.Level, a.Contribute)
	if err != nil {
		return nil, err
	}
	_, yes := Contributing(cfg)
	m.C.J.MustAppend("report.level", map[string]any{"level": Level(cfg), "contribute": yes, "by": r.ID})
	return map[string]any{"level": Level(cfg), "contribute": yes}, nil
}

func (m *Module) onPerson(a core.Ask) {
	var s shareAsk
	if a.Answer != "yes" || json.Unmarshal([]byte(a.Ref), &s) != nil {
		return
	}
	cfg, err := Save(m.C.WS, s.Level, s.Contribute)
	if err != nil {
		if a.From != "" {
			m.C.Runs.Send(a.From, "The person said yes to sharing more, but it couldn't be set: "+err.Error())
		}
		return
	}
	_, yes := Contributing(cfg)
	m.C.J.MustAppend("report.level", map[string]any{"level": Level(cfg), "contribute": yes, "by": "person (asked by " + a.From + ")"})
}
