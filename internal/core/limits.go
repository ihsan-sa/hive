package core

import "strings"

// LimitsRead is the approval kind a warden's read of a change to the code
// that enforces the hard limits records (ApproveDiff), and the kind of the ask
// that settles it.
//
// Everything else an agent changes, the core included, lands with nothing in
// between (the person, 10-30: "freely edit everything including the core
// with no process"). The one exception is the code that holds the hard
// limits (money, credentials, private details going out, anything outward in
// the person's name, deleting a repository or its history): the person,
// 10-30, "maybe a change to the warden code needs a warden". Its paths are
// SecurityList's limits array, read from main, so a change can't move its
// own bar; the check is here, a file the list names, so loosening it needs
// the read it loosens.
const LimitsRead = "limits"

// LimitsHeld says why a merge of ch at sha must wait for a warden's read, or
// "" when it needn't: it changes none of the limits paths, a warden (or the
// person) said land on its exact diff, or it is a revert, which puts back
// what main held before.
func (e *Effects) LimitsHeld(p Project, ch Change, sha string, revert bool) string {
	if len(ch.Limits) == 0 || revert || e.ApprovedDiff(LimitsRead, p.Name, sha, ch.DiffID) {
		return ""
	}
	return "it changes " + strings.Join(ch.Limits, ", ") + ", code that enforces the hard limits (limits in " + SecurityList + "), and no warden has read its diff yet"
}
