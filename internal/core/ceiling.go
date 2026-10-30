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
	"open":       regexp.MustCompile(`(?i)\b(open (a |the )?port|listen on|0\.0\.0\.0|expose|port[ -]?forward|firewall(d|-cmd)?|ufw|iptables|nft|public url|tunnel|ngrok|cloudflared|inbound|egress grant|egress\.local|egress\.projects)\b`),
	"outward":    regexp.MustCompile(`(?i)\b(send (an? )?(e-?mail|mail|message|sms|text|dm)|email to|sendmail|tweet|post (to|on|as)|publish (as|in my name)|on my behalf|in my name)\b`),
}

// CeilingHit names the AlwaysPerson category the texts fall in, or "".
func CeilingHit(texts ...string) string {
	k, _ := ceilingMatch(false, texts...)
	return k
}

// CeilingMatch is CeilingHit with the words that matched, for telling the
// person why a step is theirs.
func CeilingMatch(texts ...string) (kind, phrase string) {
	return ceilingMatch(false, texts...)
}

// AskCeilingMatch is CeilingMatch for what an agent asks in words: a mention
// the ask itself rules out ("do not set OLLAMA_HOST=0.0.0.0", "no
// credentials needed", "listen on 127.0.0.1 only") doesn't count, any other
// does: a money mention, a negated protection ("no firewall") and a negated
// cap ("don't expose more than one port") always count. It only
// routes; the script that would run still meets CeilingGuard,
// which counts every mention outside a comment.
func AskCeilingMatch(texts ...string) (kind, phrase string) {
	return ceilingMatch(true, texts...)
}

func ceilingMatch(skipRuledOut bool, texts ...string) (string, string) {
	if ms := askMatches(skipRuledOut, true, texts...); len(ms) > 0 {
		return ms[0].Kind, ms[0].Phrase
	}
	return "", ""
}

// A Mention is one AlwaysPerson match: its category and the words.
type Mention struct{ Kind, Phrase string }

func askMatches(skipRuledOut, first bool, texts ...string) []Mention {
	t := strings.Join(texts, "\n")
	var out []Mention
	for _, k := range []string{"money", "credential", "open", "outward"} {
		for _, m := range alwaysPersonRe[k].FindAllStringIndex(t, -1) {
			if !skipRuledOut || !ruledOut(k, t[:m[0]], t[m[0]:m[1]], t[m[1]:]) {
				if out = append(out, Mention{k, t[m[0]:m[1]]}); first {
					return out
				}
			}
		}
	}
	return out
}

// AskMentions is every mention AskCeilingMatch counts, not just the first.
func AskMentions(texts ...string) []Mention { return askMatches(true, false, texts...) }

// ScriptMentions is every mention ScriptCeilingMatch counts.
func ScriptMentions(texts ...string) []Mention { return scriptMatches(false, texts...) }

// ScriptCeilingMatch is CeilingMatch for the script that would run: every
// mention counts, except one on a whole-line shell comment that the comment
// itself rules out ("# Scrubbed environment: no tokens"), since a comment
// never runs. A mention on a line with code, or one a comment asks for
// ("# rotate the token"), still counts.
func ScriptCeilingMatch(texts ...string) (string, string) {
	if ms := scriptMatches(true, texts...); len(ms) > 0 {
		return ms[0].Kind, ms[0].Phrase
	}
	return "", ""
}

func scriptMatches(first bool, texts ...string) []Mention {
	t := strings.Join(texts, "\n")
	var out []Mention
	for _, k := range []string{"money", "credential", "open", "outward"} {
		for _, m := range alwaysPersonRe[k].FindAllStringIndex(t, -1) {
			start := strings.LastIndexByte(t[:m[0]], '\n') + 1
			end := len(t)
			if i := strings.IndexByte(t[m[1]:], '\n'); i >= 0 {
				end = m[1] + i
			}
			body, ok := strings.CutPrefix(strings.TrimLeft(t[start:m[0]], " \t"), "#")
			if !ok || !ruledOut(k, body, t[m[0]:m[1]], t[m[1]:end]) {
				if out = append(out, Mention{k, t[m[0]:m[1]]}); first {
					return out
				}
			}
		}
	}
	return out
}

var (
	// clauseEnd ends the stretch a negation reaches back over.
	clauseEnd = regexp.MustCompile(`(?i)[.;:!?,\n]|\b(and|or|but|then|so|also|plus|while|unless|except)\b`)
	// negatedRe is a negation at most three words before a mention, with
	// nothing between that turns it back ("don't forget to open a port").
	negatedRe = regexp.MustCompile(`(?i)\b(not|no|never|don'?t|doesn'?t|won'?t|shouldn'?t|mustn'?t|needn'?t|without|nor|none)\b((\s+\S+){0,3})\s*$`)
	// anyNegRe is any negation, for counting them in a clause.
	anyNegRe  = regexp.MustCompile(`(?i)\b(not|no|never|nor|none|nothing|cannot|without|(do|does|did|wo|ca|is|are|was|were|should|must|need|would|could|have|has|ai)n'?t)\b`)
	turnsBack = regexp.MustCompile(`(?i)\b(forget|fail|skip|neglect|omit|hesitate|stop|only|just|but|more|over|above|beyond|up|most|less|under|fewer|except)\b`)
	// limitRe is a cap right after a mention: the negation limits the step
	// rather than ruling it out.
	limitRe = regexp.MustCompile(`(?i)^\W{0,3}(\S+\s+){0,2}(more than|over|above|beyond|up to|at most|less than|under|fewer than|except)\b`)
	// protectionRe is an 'open' word never ruled out: a protection, which
	// negated asks to open the machine, or a local egress grant, whose every
	// ask is the person's.
	protectionRe = regexp.MustCompile(`(?i)firewall|ufw|iptables|nft|inbound|egress`)
	// loopbackRe is a listen address that stays on this machine.
	loopbackRe = regexp.MustCompile(`(?i)^\s*(127\.\d+\.\d+\.\d+|localhost|loopback|::1|\[::1\])\b`)
)

// ruledOut says whether a mention is one the text excludes rather than asks
// for. It errs towards the person: only a negation close before the mention
// in its own clause, or listening on loopback, rules it out.
func ruledOut(kind, before, match, after string) bool {
	if kind == "open" && strings.EqualFold(match, "listen on") && loopbackRe.MatchString(after) {
		return true
	}
	if kind == "money" {
		return false // "don't spend more than $50" still spends
	}
	if limitRe.MatchString(after) {
		return false // "don't expose more than one port" still exposes one
	}
	if protectionRe.MatchString(match) {
		return false // "no firewall", "do not block inbound" open the machine
	}
	if ix := clauseEnd.FindAllStringIndex(before, -1); len(ix) > 0 {
		before = before[ix[len(ix)-1][1]:]
	}
	m := negatedRe.FindStringSubmatch(before)
	// A second negation turns the first back: "won't work without your API
	// key" and "no reason not to expose it" ask for the thing.
	return m != nil && !turnsBack.MatchString(m[2]) && len(anyNegRe.FindAllString(before, 2)) < 2
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
	return ceiling(CeilingMatch, floor, texts...)
}

// AskCeiling is Ceiling for routing what an agent asks in words: a mention
// the ask rules out doesn't count (AskCeilingMatch).
func AskCeiling(floor []string, texts ...string) string {
	return ceiling(AskCeilingMatch, floor, texts...)
}

// ScriptCeiling is Ceiling for the exact script that would run: a mention a
// comment rules out doesn't count (ScriptCeilingMatch).
func ScriptCeiling(floor []string, script string) string {
	return ceiling(ScriptCeilingMatch, floor, script)
}

func ceiling(match func(...string) (string, string), floor []string, texts ...string) string {
	if k, p := match(texts...); k != "" {
		return fmt.Sprintf("%s (%s: it says %q)", AlwaysPerson[k], k, p)
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
	if why := ScriptCeiling(floor, script); why != "" && !personApproved {
		return fmt.Errorf("refused: this step %s, which is always the person's to decide", why)
	}
	return nil
}
