package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// StewardFile, on the release repository's default branch, names the one
// Hive that merges and releases there: its first word is that Hive's id. It
// is protected, so it changes only by the person's word. Another Hive of the
// same person works in a fork and offers its changes upstream.
const StewardFile = ".hive/steward"

// HiveID is this Hive's own id, made on first need and kept in .hive/hive-id.
// The person may write a name of their own there (H0).
func (w Workspace) HiveID() string {
	path := filepath.Join(w.Hive(), "hive-id")
	if raw, err := os.ReadFile(path); err == nil {
		if id := ParseSteward(string(raw)); id != "" {
			return id
		}
	}
	id := "hive-" + NewUUID()[:13]
	os.MkdirAll(w.Hive(), 0o755)
	if f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644); err == nil {
		f.WriteString(id + "\n")
		f.Close()
	} else if errors.Is(err, os.ErrExist) {
		raw, _ := os.ReadFile(path) // another caller made it first
		if got := ParseSteward(string(raw)); got != "" {
			return got
		}
	}
	return id
}

// ParseSteward is the id a steward record names: its first word outside a
// "#" comment, "" if none.
func ParseSteward(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		line, _, _ = strings.Cut(line, "#")
		if f := strings.Fields(line); len(f) > 0 {
			return f[0]
		}
	}
	return ""
}

// StewardRecord is the record for this Hive's id: the file's content.
func StewardRecord(id string) string {
	return id + "\n# The Hive above is the steward of this repository: only it merges and releases here.\n" +
		"# Every other Hive works in a fork and offers its changes upstream as a pull request.\n" +
		"# Changing this file is the person's word (it is protected).\n"
}

// Stewardship says whether this Hive may merge and release in p's repository
// at sha: "" when it may (p isn't the release repository, the record names
// this Hive, or no Hive is recorded yet), else why not.
func (e *Effects) Stewardship(p Project, sha string) string {
	up := e.ws.LoadConfig().ReleaseRepo
	if !p.Self || up == "" || p.Repo != up {
		return ""
	}
	return StewardRefusal(up, ParseSteward(e.ReadFile(p, sha, StewardFile)), e.ws.HiveID())
}

// StewardRefusal says why Hive id may not work in up, whose record names
// steward ("" when it may).
func StewardRefusal(up, steward, id string) string {
	if steward == "" || steward == id {
		return ""
	}
	return up + "'s steward is the Hive " + steward + ", not this one (" + id + "): only the steward merges and releases there. " +
		"This Hive works on itself in a fork in another owner and offers its changes upstream as a pull request: " +
		"the person names that owner as self_fork in the settings (e.g. Hive-RSI-Lab). It becomes the steward only by the person's word, " +
		"a change to " + StewardFile + " in " + up + " naming " + id + "."
}
