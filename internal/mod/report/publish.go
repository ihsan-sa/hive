package report

import (
	"fmt"
	"os"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/mod/scan"
)

// publishLists are the publish scan's deny and handle lists, given at run
// time as `hive share` reads them (issue #370). With no handle list nothing
// may go: a scan that can't look for people says nothing about them.
func (m *Module) publishLists() (scan.Lists, error) {
	deny, handles, err := scan.RunTime(os.Getenv, m.C.WS.Private())
	if err != nil {
		return scan.Lists{}, err
	}
	return scan.Lists{Public: []string{"**"}, Deny: deny, Handles: handles}, nil
}

// held runs the publish scan on texts, already scrubbed, named name, and
// says what it found: each hit's place and kind, never the thing itself.
// Empty means they may go.
func held(name string, l scan.Lists, texts ...string) string {
	var hits []string
	for _, t := range texts {
		for _, h := range scan.Text(name, []byte(t), l) {
			hits = append(hits, h.String())
		}
	}
	if len(hits) > 5 {
		hits = append(hits[:5], fmt.Sprintf("and %d more", len(hits)-5))
	}
	return strings.Join(hits, "; ")
}
