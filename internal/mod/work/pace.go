package work

import (
	"cmp"
	"fmt"
	"path"
	"strings"
	"sync"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// Check ranks: main's own measurement goes first, then the fix of a red
// main's failing test, then any other fix's, then the rest in the order they
// asked. Main's score and size of 282991d7 waited 2h16m behind PR checks,
// and keep/revert, the release and the next baseline with them (issue
// #1078); a red main's fix waited behind 29 other fixes' checks that failed
// on the same red, and every merge with it (issue #1877). Both go past the
// line's patience: there is one red main at a time, and few fixes of it.
const (
	rankFix    = 1
	rankRedFix = 2
	rankMain   = 3
)

// MeasuringMain marks sha as main's commit being measured on project until
// the returned done is called: its checks go ahead of the line.
func (m *Module) MeasuringMain(project, sha string) (done func()) {
	key := project + "@" + sha
	m.mu.Lock()
	m.measuring[key]++
	m.mu.Unlock()
	m.C.Effects.Slots.Fill()
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.measuring[key]--; m.measuring[key] <= 0 {
				delete(m.measuring, key)
			}
		})
	}
}

// RankWhy says why a waiting check of rank r goes ahead of the line, or ""
// for one that keeps its place, for the dashboard and the status op.
func RankWhy(r int) string {
	switch {
	case r >= rankMain:
		return "measures main"
	case r == rankRedFix:
		return "fixes red main"
	case r == rankFix:
		return "a fix"
	}
	return ""
}

// checkRank ranks a check of sha on project: main's being measured, or by
// the task whose head it is.
func (m *Module) checkRank(project, sha, _ string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.measuring[project+"@"+sha] > 0 {
		return rankMain
	}
	for _, t := range m.tasks {
		if t.Project == project && t.SHA == sha && !t.Terminal() {
			if t.Fix() && namesAny(t.Title+"\n"+t.Known+"\n"+t.AimWhy, m.mainFails[project]) {
				return rankRedFix
			}
			if t.Fix() {
				return rankFix
			}
			return 0
		}
	}
	return 0
}

// namesAny says text names one of tests as a whole word: a Go subtest by
// its top test, a pytest id (path::test[params]) by its test or its file.
func namesAny(text string, tests []string) bool {
	for _, id := range tests {
		for _, n := range testNames(id) {
			if namesWord(text, n) {
				return true
			}
		}
	}
	return false
}

// testNames are the words that name a failing test's id.
func testNames(id string) []string {
	where, _, found := strings.Cut(id, "::")
	if !found {
		n, _, _ := strings.Cut(id, "/")
		return []string{n}
	}
	n := id[strings.LastIndex(id, "::")+2:]
	n, _, _ = strings.Cut(n, "[")
	if strings.HasSuffix(where, ".py") {
		return []string{n, path.Base(where)}
	}
	n, _, _ = strings.Cut(n, "/")
	return []string{n}
}

// namesWord says text holds n as a whole word.
func namesWord(text, n string) bool {
	for rest, at := text, 0; n != ""; {
		i := strings.Index(rest, n)
		if i < 0 {
			break
		}
		start, end := at+i, at+i+len(n)
		if (start == 0 || !isWord(text[start-1])) && (end == len(text) || !isWord(text[end])) {
			return true
		}
		rest, at = text[start+1:], start+1
	}
	return false
}

func isWord(c byte) bool {
	return c == '_' || '0' <= c && c <= '9' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

// Pace says what paces the workers and checks, for the dashboard and the
// lead: one memory budget (core.MemBudget) for every turn and worker, and
// max_checks for the checks.
func (m *Module) Pace() map[string]string {
	cfg := m.C.WS.LoadConfig()
	budget := fmt.Sprintf("as many as memory holds at %d MB a turn (turn_mb)", cfg.TurnMB)
	if why := core.MemBudget(cfg.TurnMB, 0); why != "" {
		budget += "; now " + why
	}
	return map[string]string{"workers": budget, "checks": fmt.Sprint(cmp.Or(cfg.MaxChecks, core.DefaultMaxChecks()))}
}
