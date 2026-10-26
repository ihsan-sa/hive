package work

import (
	"regexp"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// A brief that says no aim is read for a failure it names: something broken
// is a fix that is known to be needed, and wins; only a brief that names
// nothing broken is unclear, and is taken as a gain (issue #894). Its worker,
// or the lead, can still say otherwise with `hive agent aim`.
var (
	// brokenWords name a failure wherever they appear: the title or the known.
	brokenWords = regexp.MustCompile(`(?i)\b(fail(s|ed|ing|ures?)?|broken|breaks|bugs?|crash(es|ed|ing)?|panic(s|ked)?|refused|refuses|stuck|hangs?|flak(y|es?)|wrong|regress(ed|ion|es)?|deadlocks?|races?|lost|loses|leaks?|errors?|red main|main is red|times? out|timed out)\b`)
	// notWords name one only in the title, where the ask says what should
	// hold and doesn't now ("ends done, not blocked"); in the known they are
	// as often plain facts.
	notWords = regexp.MustCompile(`(?i)(\b(not|never|no longer|instead of)\b|\b\w+n't\b)`)
)

// aim is the brief's aim and why: as it says, or, when it says none, a fix
// if it names something broken, else unclear (empty).
func (b Brief) aim() (string, string) {
	aim, why := strings.ToLower(strings.TrimSpace(b.Aim)), strings.TrimSpace(b.Why)
	if aim != "" {
		return aim, why
	}
	if s := broken(b.Title, b.Known); s != "" {
		return AimFix, core.Clip("the brief names something broken: "+s, 300)
	}
	return "", ""
}

// broken returns the part of a brief that names something broken: the title,
// or the known's sentence that does; empty when neither does.
func broken(title, known string) string {
	title = strings.ReplaceAll(strings.TrimSpace(title), "’", "'")
	if brokenWords.MatchString(title) || notWords.MatchString(title) {
		return title
	}
	known = strings.ReplaceAll(known, "’", "'")
	for _, s := range sentences.Split(known, -1) {
		if s = strings.TrimSpace(s); brokenWords.MatchString(s) {
			return s
		}
	}
	return ""
}

var sentences = regexp.MustCompile(`[.!?]\s+|\n+`)
