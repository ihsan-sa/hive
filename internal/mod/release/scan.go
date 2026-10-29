package release

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/scan"
)

// scan is the identity and secret scan of a release at sha: the files of the
// commit that its public list (scan.PublicList, read at sha) names, against
// the deny and handle lists in the Hive's private folder. Any hit, or no
// list, blocks the release.
func (m *Module) scan(p core.Project, sha string) error {
	deny, handles, err := scan.RunTime(os.Getenv, m.C.WS.Private())
	if err != nil {
		return err
	}
	dir := m.C.WS.Project(p.Name)
	out, err := core.Git(dir, "ls-tree", "-r", "-z", "--name-only", sha)
	if err != nil {
		return errors.New("can't list the files at " + core.Short(sha) + ": " + core.Clip(err.Error(), 200))
	}
	public := scan.ParseList(m.C.Effects.ReadFile(p, sha, scan.PublicList), false)
	read := func(name string) ([]byte, error) {
		s, err := core.Git(dir, "show", sha+":"+name)
		return []byte(s), err
	}
	return scan.Run(strings.Split(strings.TrimRight(out, "\x00"), "\x00"), read, scan.Lists{Public: public, Deny: deny, Handles: handles})
}

// scanFix is the title of the brief that mends a scan blocking main.
const scanFix = "Main's release scan blocks every release: reword what it finds"

// fileScanFix files the fix for hits that block a release of main: one brief
// per project, naming each hit's file, line and the commit that last wrote
// that line. Work's publishScan sends back a change that adds a hit before it
// merges, but a merge outside that path (a warden's script, the person on
// GitHub) is first scanned here, hours later: the alert alone filed nothing,
// and every release was refused until someone read it (10-28 to 10-29).
func (m *Module) fileScanFix(p core.Project, sha string, err error) {
	var f *scan.Found
	if m.FileFix == nil || !errors.As(err, &f) {
		return
	}
	dir := m.C.WS.Project(p.Name)
	var hits strings.Builder
	for i, h := range f.Hits {
		if i == 30 {
			fmt.Fprintf(&hits, "\n  and %d more", len(f.Hits)-i)
			break
		}
		by := "a commit git blame can't name"
		if out, err := core.Git(dir, "blame", "--porcelain", "-L", fmt.Sprintf("%d,%d", h.Line, h.Line), sha, "--", h.File); err == nil {
			if w := strings.Fields(out); len(w) > 0 {
				by = core.Short(w[0])
			}
		}
		fmt.Fprintf(&hits, "\n  %s (the line as %s wrote it)", h, by)
	}
	m.FileFix(p.Name, scanFix,
		"the release scan blocks every release of main ("+core.Short(sha)+")",
		"The release scan passes at main: each line below says the same without what the scan found, or is gone if it is stale. "+
			"The scan names the file, line and kind, never the thing; find it by running the scan (cmd/hive-scan, internal/mod/scan) on your tree, "+
			"and never write it in a commit, PR, note or account. If the commit that wrote a line merged outside work's merge path, say how it got past publishScan.",
		// No commit here, so the same hits on a later main are no news.
		"The release scan of main found these in published files:"+hits.String())
}
