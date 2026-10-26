package work

import (
	"fmt"
	"strings"
	"time"
)

// Filing slows to what the loop can merge (issue #899). While a project's
// open work would take longer than inflowWindow to merge at the rate it
// merged over the last inflowWindow, a new gain an agent files (a lead's,
// a sweep's, a worker's) is not filed: it would only wait behind the rest,
// lengthen every task's lead time and add check and catch-up churn. Fixes,
// briefs that join an open task, the person's asks (and a lead's brief
// carrying the person's words in asked), and the Hive's own briefs are filed
// as they come.
const (
	inflowWindow = 6 * time.Hour
	inflowFloor  = 10 // open tasks a project may always have, so a quiet one isn't shut
)

// open reports whether t is work still waiting to merge.
func (t Task) open() bool {
	return (t.Kind == "work" || t.Kind == "revert") && !t.Terminal() && t.Stage != StageMerged
}

// mergedSince counts the merges (checkpoints too) in t's log since from.
func (t Task) mergedSince(from time.Time) int {
	n := 0
	for _, e := range t.Log {
		if e.At.After(from) && (strings.HasPrefix(e.Text, "merged as ") || strings.HasPrefix(e.Text, "checkpoint merged as ") || strings.HasPrefix(e.Text, "merged on the forge")) {
			n++
		}
	}
	return n
}

// backlog is a project's open tasks and its merges over the last
// inflowWindow.
func (m *Module) backlog(project string, now time.Time) (open, merged int) {
	from := now.Add(-inflowWindow)
	for _, t := range m.List(func(t Task) bool { return t.Project == project }) {
		if t.open() {
			open++
		}
		merged += t.mergedSince(from)
	}
	return open, merged
}

// slowed says why a new gain on project isn't filed now, or "" when it may be.
func (m *Module) slowed(project string, now time.Time) string {
	open, merged := m.backlog(project, now)
	if open < max(inflowFloor, merged) {
		return ""
	}
	return fmt.Sprintf("not filed: %s has %d tasks open and merged %d in the last %d hours, so a new gain would only wait behind them and add churn. "+
		"Filing slows to what the loop can merge: put the idea in your notes or the knowledge base (hive agent remember) and file it once the backlog is below what merges in %[4]d hours; "+
		"meanwhile help what is open move (queue --hold or --order, recheck, landed). A fix files now: say --aim fix --why naming the failure it fixes. "+
		"The person's ask files now too: give their words with --asked \"...\"",
		project, open, merged, int(inflowWindow.Hours()))
}
