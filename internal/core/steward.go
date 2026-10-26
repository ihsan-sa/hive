package core

import (
	"errors"
	"fmt"
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
// this Hive, or no Hive is recorded yet), else why not. A record it can't
// read refuses, so a broken clone never skips the check.
func (e *Effects) Stewardship(p Project, sha string) string {
	up := e.ws.LoadConfig().ReleaseRepo
	if up == "" || p.Repo != up {
		return ""
	}
	raw, err := e.stewardAt(p, sha)
	if err != nil {
		return "can't read " + up + "'s steward record (" + StewardFile + ") at " + Short(sha) + ", so this Hive won't merge or release there: " + Clip(err.Error(), 200)
	}
	return StewardRefusal(up, ParseSteward(raw), e.ws.HiveID())
}

// stewardAt is the record in p's clone at sha, or at main as last fetched
// when the clone hasn't fetched sha yet: "" with no error when that commit
// has no such file, an error when git can't read either.
func (e *Effects) stewardAt(p Project, sha string) (string, error) {
	dir := e.ws.Project(p.Name)
	if _, err := Git(dir, "rev-parse", "--verify", "-q", sha+"^{commit}"); err != nil {
		sha = "refs/remotes/origin/" + p.Branch
		if _, err2 := Git(dir, "rev-parse", "--verify", "-q", sha+"^{commit}"); p.Branch == "" || err2 != nil {
			return "", fmt.Errorf("neither that commit nor main is in the clone: %v", err)
		}
	}
	out, err := Git(dir, "ls-tree", "--name-only", sha, "--", StewardFile)
	if err != nil || strings.TrimSpace(out) == "" {
		return "", err
	}
	return Git(dir, "show", sha+":"+StewardFile)
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
