package core

import (
	"fmt"
	"regexp"
	"strings"
)

// The hard ceiling. Everything inside it is judgement; these are limits, and
// each is enforced by structure rather than by an agent's good behaviour:
//
//   - Agents hold no credentials. Logins stay with the effects process and the
//     harnesses' own stores; an agent gets only a short-lived token (Tokens).
//   - Agents run sandboxed: they write only their checkout and scratch home and
//     reach the network outbound only (package sandbox).
//   - Main changes only through the gate: a merge pinned to a commit with a
//     host-run passing test and a bound verdict (Effects.Merge). Only hive/*
//     branches are ever pushed.
//   - Goals, the score and the project's settings are the person's to merge
//     (Effects.Classify marks them protected).
//   - The core has no path that runs an agent's request outside the sandbox.
//     Only the risk module does, and only as a warden's or a panel's exact,
//     digest-pinned script, after CeilingGuard on that script.
//   - The records are append-only: the journal, chats, scores and run output
//     live where agents can't write.
//
// AlwaysPerson names what is always the person's, whatever the autonomy
// setting says.
var AlwaysPerson = map[string]string{
	"money":      "spends real money",
	"credential": "mints or rotates a credential",
	"open":       "opens this machine to the network",
	"outward":    "sends something outward in your name",
}

// alwaysPersonRe recognises an AlwaysPerson step in what an agent asks for or
// in the script that would run. It errs towards the person: a false match
// costs one question, a miss would let a panel take the person's step.
var alwaysPersonRe = map[string]*regexp.Regexp{
	"money":      regexp.MustCompile(`(?i)\b(pay|payments?|purchases?|buy|spend|charges?|subscribe|subscriptions?|invoices?|billing|credit card|stripe|usd|eur)\b|\$\s?\d`),
	"credential": regexp.MustCompile(`(?i)\b(api[ -]?key|token|secret|credential|password|ssh[ -]?key|ssh-keygen|oauth|rotate|mint|private key|keychain|gh auth)\b`),
	"open":       regexp.MustCompile(`(?i)\b(open (a |the )?port|listen on|0\.0\.0\.0|expose|port[ -]?forward|firewall|ufw|iptables|nft|public url|tunnel|ngrok|cloudflared|inbound)\b`),
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

// Route says who weighs a step: "person" (the ceiling), "self" (T1),
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
// would run: a step the ceiling names runs only on the person's own yes,
// whatever a warden or panel decided.
func CeilingGuard(script string, personApproved bool) error {
	if k := CeilingHit(script); k != "" && !personApproved {
		return fmt.Errorf("refused: this step %s, which is always the person's to decide", AlwaysPerson[k])
	}
	return nil
}
