package core

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// The hard ceiling. Everything inside it is judgement; these are limits, and
// each but the last check on a step is enforced by structure rather than by
// an agent's good behaviour:
//
//   - Agents hold no credentials. Logins stay with the effects process and the
//     harnesses' own stores; an agent gets only a short-lived token (Tokens).
//   - Agents run sandboxed: they write only their checkout and scratch home and
//     reach the network outbound only (package sandbox).
//   - Main changes only through the gate: a merge pinned to a commit with a
//     host-run passing test and a bound verdict (Effects.Merge). Only hive/*
//     branches are ever pushed.
//   - Goals, the score and the project's settings merge only on a recorded
//     approval (Effects.Classify marks them protected): the person's at
//     autonomy 1, otherwise a warden's, never the worker's that wrote them,
//     and the person is told after (risk.Weigh).
//   - The core has no path that runs an agent's request outside the sandbox.
//     Only the risk module does, and only as a warden's or a panel's exact,
//     digest-pinned script, after CeilingGuard on that script. That guard is
//     a best-effort text check that errs towards the person, not structure:
//     a script that hides its words (a variable, base64) gets past it.
//   - The records are append-only: the journal, chats, scores and run output
//     live where agents can't write.
//
// AlwaysPerson names what is always the person's, whatever the autonomy
// setting says.
var AlwaysPerson = map[string]string{
	"money":      "spends real money",
	"credential": "reads, mints or rotates a credential",
	"open":       "opens this machine to the network",
	"outward":    "sends something outward in your name",
}

// alwaysPersonRe recognises an AlwaysPerson step in what an agent asks for or
// in the script that would run. It errs towards the person: a false match
// costs one question, a miss would let a panel take the person's step.
var alwaysPersonRe = map[string]*regexp.Regexp{
	"money":      regexp.MustCompile(`(?i)\b(pay|payments?|purchases?|buy|spend|charges?|subscribe|subscriptions?|invoices?|billing|credit card|stripe|usd|eur|dollars?)\b|\$\s?\d+(\.\d\d|,\d{3})|\$\d{2,}`), // not $1: a shell's positional parameter
	"credential": regexp.MustCompile(`(?i)\b(api[ -]?keys?|tokens?|secrets?|credentials?|passwords?|ssh[ -]?keys?|ssh-keygen|oauth|rotate|mint|private keys?|keychain|gh auth)\b|\.ssh/|\.netrc|\.git-credentials|\.config/gh\b|\.aws/|\.docker/config|\.kube/config|\.gnupg|\.npmrc|\.pypirc`),
	"open":       regexp.MustCompile(`(?i)\b(open (a |the )?port|listen on|0\.0\.0\.0|expose|port[ -]?forward|firewall|ufw|iptables|nft|public url|tunnel|ngrok|cloudflared|inbound|egress grant|egress\.local)\b`),
	"outward":    regexp.MustCompile(`(?i)\b(send (an? )?(e-?mail|mail|message|sms|text|dm)|email to|sendmail|tweet|post (to|on|as)|publish (as|in my name)|on my behalf|in my name)\b`),
}

// CeilingHit names the AlwaysPerson category the texts fall in, or "".
func CeilingHit(texts ...string) string {
	t := strings.Join(texts, "\n")
	for _, k := range []string{"money", "credential", "open", "outward"} {
		if alwaysPersonRe[k].MatchString(t) {
			return k
		}
	}
	return ""
}

// FloorHit returns the first entry of the person's floor list the texts
// touch, or "". An entry with a glob character (*?[) matches a word of the
// texts or its last path element; any other entry matches as written.
func FloorHit(floor []string, texts ...string) string {
	t := strings.Join(texts, "\n")
	words := strings.FieldsFunc(t, func(r rune) bool { return strings.ContainsRune(" \t\n\"'`=;&|()<>", r) })
	for _, f := range floor {
		if f == "" {
			continue
		}
		if !strings.ContainsAny(f, "*?[") {
			if strings.Contains(t, f) {
				return f
			}
			continue
		}
		for _, w := range words {
			a, _ := path.Match(f, w)
			b, _ := path.Match(f, path.Base(w))
			if a || b {
				return f
			}
		}
	}
	return ""
}

// Ceiling says why the texts are always the person's: what an AlwaysPerson
// step does, or the floor entry they touch. "" means neither.
func Ceiling(floor []string, texts ...string) string {
	if k := CeilingHit(texts...); k != "" {
		return AlwaysPerson[k]
	}
	if f := FloorHit(floor, texts...); f != "" {
		return fmt.Sprintf("touches %q, which your floor list names", f)
	}
	return ""
}

// Tier normalises a risk tier (spec 7): T1 inside the Hive, the worker weighs
// it; T2 outside but recoverable, or load-bearing inside, a warden weighs it;
// T3 outside and hard to undo, a tribunal weighs it. Unknown is T2.
func Tier(s string) string {
	switch s = strings.ToUpper(strings.TrimSpace(s)); s {
	case "T1", "T3":
		return s
	}
	return "T2"
}

// Route says who weighs a step: "person" (the ceiling or the floor), "self" (T1),
// "person-first" (autonomy 1 asks before anything outside the project),
// "warden" (T2) or "tribunal" (T3). Autonomy 3 routes like 2 and tells the
// person after. The setting moves lines; it never lifts the ceiling.
func Route(tier string, autonomy int, ceiling string) string {
	switch {
	case ceiling != "":
		return "person"
	case Tier(tier) == "T1":
		return "self"
	case autonomy == 1:
		return "person-first"
	case Tier(tier) == "T2":
		return "warden"
	}
	return "tribunal"
}

// CeilingGuard is checked at the point of effect, on the exact script that
// would run: a step the ceiling or the person's floor names runs only on the
// person's own yes, whatever a warden or panel decided.
func CeilingGuard(script string, floor []string, personApproved bool) error {
	if why := Ceiling(floor, script); why != "" && !personApproved {
		return fmt.Errorf("refused: this step %s, which is always the person's to decide", why)
	}
	return nil
}
