package work

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/scan"
)

// publishScan runs the release's own scan (internal/mod/scan) over a self
// project's change before it merges: a key, a deny word or a handle in a
// published file the change touches goes back to its worker, as a red
// check would. Without it a goals or docs line blocked every release for
// hours, seen only once a release was tried. Only what the change adds
// counts: a hit already at its merge base is main's, not the worker's. With
// no handle list the release asks the person; here it is not a reason to
// hold a merge.
func (m *Module) publishScan(t *Task, p core.Project, main string) (bool, error) {
	if !p.Self {
		return false, nil
	}
	deny, handles, err := scan.RunTime(os.Getenv, m.C.WS.Private())
	if err != nil {
		return false, nil
	}
	hits, err := addedHits(m.C.WS.Project(p.Name), main, t.SHA, scan.Lists{Deny: deny, Handles: handles})
	if err != nil || len(hits) == 0 {
		return false, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "The release scan (internal/mod/scan, the one every release runs) blocks %s: it adds %d thing(s) that must never be published, and a release of main with it would be refused:\n", core.Short(t.SHA), len(hits))
	for _, h := range hits {
		b.WriteString("\n  " + h.String())
	}
	b.WriteString("\n\nThe scan names the file, line and kind, never the thing itself; reword those lines so they say the same without it, and don't repeat what you remove in a commit message, note or account.")
	t.Reason = ""
	return true, m.sendBack(t, p, b.String())
}

// addedHits are the scan's hits in the published files (the head's public
// list) that head changes since its merge base with main, of each file and
// kind beyond as many as the base had there. l's Public is read at head.
func addedHits(dir, main, head string, l scan.Lists) ([]scan.Hit, error) {
	base, err := core.Git(dir, "merge-base", main, head)
	if err != nil {
		return nil, errors.New("can't find the merge base of " + core.Short(head) + " and main: " + core.Clip(err.Error(), 200))
	}
	base = strings.TrimSpace(base)
	public, _ := core.Git(dir, "show", head+":"+scan.PublicList)
	if l.Public = scan.ParseList(public, false); len(l.Public) == 0 {
		return nil, nil
	}
	out, err := core.Git(dir, "diff", "--name-only", "--no-renames", "--diff-filter=d", "-z", base, head)
	if err != nil {
		return nil, errors.New("can't list what " + core.Short(head) + " changes: " + core.Clip(err.Error(), 200))
	}
	var hits []scan.Hit
	for _, f := range scan.Files(strings.Split(strings.TrimRight(out, "\x00"), "\x00"), l.Public) {
		now, _ := core.Git(dir, "show", head+":"+f)
		at := scan.Text(f, []byte(now), l)
		if len(at) == 0 {
			continue
		}
		was := map[string]int{}
		if old, err := core.Git(dir, "show", base+":"+f); err == nil {
			for _, h := range scan.Text(f, []byte(old), l) {
				was[h.What]++
			}
		}
		n := map[string]int{}
		for _, h := range at {
			n[h.What]++
		}
		for _, h := range at {
			if n[h.What] > was[h.What] {
				hits = append(hits, h)
			}
		}
	}
	return hits, nil
}
