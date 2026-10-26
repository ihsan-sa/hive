package work

import (
	"regexp"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// A brief to the Hive itself that says no aim is read for a failure its
// title names: something broken is a fix that is known to be needed, and
// wins; only a brief that names nothing broken is unclear, and is taken as a
// gain (issue #894). Its worker, or the lead, can still say otherwise with
// `hive agent aim`. The known isn't read: on the record of 533 briefs (issue
// #927) a failure word there was as often background (a fact about the host,
// the history of an ask) as the change's own failure, and it took tree
// search, soft-layer experiments and goals updates for fixes. Another
// project's brief is never inferred: its score decides, as the person asked
// ("when it's unclear whether a change is a fix or a gain, lean on the score").
var (
	brokenWords = regexp.MustCompile(`(?i)\b(fail(s|ed|ing|ures?)?|broken|breaks|bugs?|crash(es|ed|ing)?|panic(s|ked)?|refused|refuses|stuck|hangs?|flak(y|es?)|wrong|regress(ed|ion|es)?|deadlocks?|races?|lost|loses|leaks?|errors?|red main|main is red|times? out|timed out|again)\b`)
	// notWords say what should hold and doesn't now ("ends done, not blocked").
	notWords = regexp.MustCompile(`(?i)(\b(not|never|no longer|instead of)\b|\b\w+n't\b)`)
)

// aim is the brief's aim and why: as it says, or, when it says none, a fix
// if it is to the Hive itself and names something broken, else unclear
// (empty).
func (b Brief) aim(self bool) (string, string) {
	aim, why := strings.ToLower(strings.TrimSpace(b.Aim)), strings.TrimSpace(b.Why)
	if aim != "" || !self {
		return aim, why
	}
	if s := broken(b.Title); s != "" {
		return AimFix, core.Clip("the brief names something broken: "+s, 300)
	}
	return "", ""
}

// broken returns the title when it names something broken, else empty.
func broken(title string) string {
	title = strings.ReplaceAll(strings.TrimSpace(title), "’", "'")
	if brokenWords.MatchString(title) || notWords.MatchString(title) {
		return title
	}
	return ""
}

// selfProject reports whether name is the Hive's own project.
func (m *Module) selfProject(name string) bool {
	p, ok := m.C.Projects.Get(name)
	return ok && p.Self
}
