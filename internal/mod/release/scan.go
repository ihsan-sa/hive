package release

import (
	"errors"
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
