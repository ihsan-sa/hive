// Package scan is the gate on everything the Hive publishes: a release and
// the export `hive share` sends. It reads only the files on a public list,
// and blocks on a key or token of any common format, on a word from a deny
// list, and on a person's handle. The deny list and the handle list are given
// at run time, from the environment or the Hive's private folder, so neither
// is ever in what is published; were one shipped, its own words would block
// it. With no handle list the scan fails: a scan that can't look for people
// says nothing about them.
//
// What it says on a hit is a file, a line and what kind of thing is there,
// never the thing: not the key, not the deny word, and not the handle, so a
// failure message (which may itself land in a public CI log) never prints a
// member's handle, the one found or the others on the list. A file whose
// name holds one is named by its place among the published files.
package scan

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// PublicList is the repository's list of the files its releases publish:
// one glob per line, `*` inside one path segment and `**` across segments,
// `!glob` leaving files out, `#` comments.
const PublicList = ".hive/public.txt"

// The run-time lists: each env var holds the list itself (a CI secret, never
// set in the daemon's environment), and else the file of the same role in
// the Hive's private folder, which no agent reads and no repository holds.
// The handle list never names the owner's own login, which the module path
// carries; an email or a real name goes on the deny list.
const (
	HandlesEnv  = "HIVE_SCAN_HANDLES"
	DenyEnv     = "HIVE_SCAN_DENY"
	HandlesFile = "scan-handles.txt"
	DenyFile    = "scan-deny.txt"
)

// Lists is what one scan reads.
type Lists struct {
	Public  []string // globs of the published files; "!glob" leaves some out
	Deny    []string // words never to publish, matched in any case
	Handles []string // people's handles, matched as whole names in any case
}

// A Hit is one thing that must not be published. What names its kind only.
type Hit struct {
	File string
	Line int
	What string
}

func (h Hit) String() string { return fmt.Sprintf("%s:%d: %s", h.File, h.Line, h.What) }

// ParseList reads a list: one entry per line, # comments, blanks skipped.
// Handles may also be split by commas or spaces, since none holds either.
func ParseList(raw string, words bool) []string {
	var out []string
	for _, l := range strings.Split(raw, "\n") {
		if i := strings.IndexByte(l, '#'); i >= 0 {
			l = l[:i]
		}
		if words {
			out = append(out, strings.FieldsFunc(l, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\r' })...)
		} else if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// RunTime reads the deny and handle lists given at run time: from env, else
// from the files in private. The handle list must be there and name someone;
// the deny list may be empty.
func RunTime(env func(string) string, private string) (deny, handles []string, err error) {
	read := func(key, file string) (string, bool) {
		if v := env(key); strings.TrimSpace(v) != "" {
			return v, true
		}
		if private == "" {
			return "", false
		}
		raw, err := os.ReadFile(filepath.Join(private, file))
		return string(raw), err == nil
	}
	if raw, ok := read(DenyEnv, DenyFile); ok {
		deny = ParseList(raw, false)
	}
	raw, ok := read(HandlesEnv, HandlesFile)
	if handles = ParseList(raw, true); !ok || len(handles) == 0 {
		where := "the " + HandlesEnv + " variable"
		if private != "" {
			where += " or " + filepath.Join(private, HandlesFile)
		}
		return nil, nil, errors.New("no handle list was given, so the scan can't look for people's handles and refuses to pass: put one handle per line in " + where)
	}
	return deny, handles, nil
}

// Files keeps the names (slash paths) the public list names, in their order.
func Files(names, public []string) []string {
	var out []string
	for _, n := range names {
		in := false
		for _, g := range public {
			if ex, ok := strings.CutPrefix(g, "!"); ok {
				if match(ex, n) {
					in = false
				}
			} else if match(g, n) {
				in = true
			}
		}
		if in {
			out = append(out, n)
		}
	}
	return out
}

func match(glob, name string) bool {
	return segs(strings.Split(strings.Trim(glob, "/"), "/"), strings.Split(name, "/"))
}

func segs(pat, name []string) bool {
	if len(pat) == 0 {
		return len(name) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(name); i++ {
			if segs(pat[1:], name[i:]) {
				return true
			}
		}
		return false
	}
	if len(name) == 0 {
		return false
	}
	if ok, _ := path.Match(pat[0], name[0]); !ok {
		return false
	}
	return segs(pat[1:], name[1:])
}

// keys are the formats of keys, tokens and credentials the scan knows. A
// label on the line ("fake", "example", "test") never excuses a match: only
// the value itself can, when it is a placeholder (see placeholder).
var keys = []struct {
	what string
	re   *regexp.Regexp
}{
	{"a private key", regexp.MustCompile(`-----BEGIN ([A-Z0-9]+ )*PRIVATE KEY( BLOCK)?-----`)},
	{"an age secret key", regexp.MustCompile(`AGE-SECRET-KEY-1[0-9A-Z]{58}`)},
	{"a GitHub token", regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{22,})`)},
	{"an Anthropic key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}`)},
	{"an OpenAI-style key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`)}, // OpenAI, OpenRouter (sk-or-v1-), DeepSeek and the like
	{"a Stripe key", regexp.MustCompile(`\b[sr]k_(live|test)_[A-Za-z0-9]{20,}`)},
	{"a Slack token", regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`)},
	{"a Slack webhook", regexp.MustCompile(`hooks\.slack\.com/services/T[A-Za-z0-9]+/B[A-Za-z0-9]+/[A-Za-z0-9]{16,}`)},
	{"a Discord webhook", regexp.MustCompile(`discord(app)?\.com/api/webhooks/[0-9]+/[A-Za-z0-9_-]{30,}`)},
	{"an AWS access key", regexp.MustCompile(`\b(AKIA|ASIA|ABIA|ACCA|AROA|AIDA)[0-9A-Z]{16}\b`)},
	{"an AWS secret key", regexp.MustCompile(`(?i)aws.{0,20}secret.{0,20}[:=]\s*["']?[A-Za-z0-9/+=]{40}\b`)},
	{"a Google API key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`)},
	{"a Google OAuth token", regexp.MustCompile(`\bya29\.[0-9A-Za-z_-]{20,}`)},
	{"a GitLab token", regexp.MustCompile(`\bgl(pat|dt|rt|ptt|cbt)-[A-Za-z0-9_-]{20,}`)},
	{"an npm token", regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36}`)},
	{"a PyPI token", regexp.MustCompile(`\bpypi-AgEIcHlwaS5vcmc[A-Za-z0-9_-]{50,}`)},
	{"a Hugging Face token", regexp.MustCompile(`\bhf_[A-Za-z0-9]{34,}`)},
	{"a SendGrid key", regexp.MustCompile(`\bSG\.[A-Za-z0-9_-]{22}\.[A-Za-z0-9_-]{43}`)},
	{"a Twilio key", regexp.MustCompile(`\bSK[0-9a-f]{32}\b`)},
	{"a Telegram bot token", regexp.MustCompile(`(^|[^0-9])[0-9]{8,10}:AA[A-Za-z0-9_-]{33}\b`)},
	{"a Discord bot token", regexp.MustCompile(`\b[MN][A-Za-z0-9]{23,25}\.[A-Za-z0-9_-]{6}\.[A-Za-z0-9_-]{27,}`)},
	{"a DigitalOcean token", regexp.MustCompile(`\bdo[opr]_v1_[a-f0-9]{64}`)},
	{"a Shopify token", regexp.MustCompile(`\bshp(at|ca|pa|ss)_[a-fA-F0-9]{32}`)},
	{"an Azure storage key", regexp.MustCompile(`AccountKey=[A-Za-z0-9+/=]{80,}`)},
	{"a JSON web token", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
	{"a password in a URL", regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^/\s:@"'$]+:[^/\s:@"'$]{3,}@[a-z0-9]`)},
	{"a bearer token", regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{20,}`)},
	{"a secret assigned to a name", regexp.MustCompile(`(?i)[a-z0-9_.-]*(token|secret|passw(or)?d|api[_-]?key|access[_-]?key|client[_-]?secret)[a-z0-9_.-]*["']?\s*[:=]\s*["']?([A-Za-z0-9+/_.=-]{20,})`)},
}

// hiveID is the shape of the Hive's own task and run ids (261024-225610-163a3bc0d0,
// r-261025-081832-f0ffb403d4-43295337-0), which prose names after a word
// like "token:" ("a brokered org leg with no child token: 261024-..."). Only
// that exact shape: anything more after an id is still read as a value.
var hiveID = regexp.MustCompile(`^([a-z]+-)?[0-9]{6}-[0-9]{6}-[0-9a-f]{10}(-[0-9a-f]{8}(-[a-z]{1,12})?-[0-9]{1,3})?\.?$`)

// placeholder says a match is no key: a value with few distinct characters
// (ghp_xxxx..., sk-0000...) or, for a value assigned to a name, one without
// both letters and digits (a variable or a word, not a key) or one of the
// Hive's own ids.
func placeholder(what, m string, sub []string) bool {
	if what == "a secret assigned to a name" {
		v := sub[len(sub)-1]
		return !strings.ContainsAny(v, "0123456789") || !strings.ContainsFunc(v, func(r rune) bool { return r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' }) || distinct(v) < 10 || hiveID.MatchString(v)
	}
	return what != "a private key" && distinct(m) < 10
}

func distinct(s string) int {
	seen := map[rune]bool{}
	for _, r := range s {
		seen[r] = true
	}
	return len(seen)
}

// handleREs match each handle as a whole name, in any case, so "ann" never
// hits "annotate". The handle is the second group.
func handleREs(l Lists) []*regexp.Regexp {
	var res []*regexp.Regexp
	for _, h := range l.Handles {
		res = append(res, regexp.MustCompile(`(?i)(^|[^A-Za-z0-9_-])(`+regexp.QuoteMeta(h)+`)($|[^A-Za-z0-9_-])`))
	}
	return res
}

// Text checks one file. Handles and deny words are matched in any case; a
// handle only as a whole name.
func Text(name string, data []byte, l Lists) []Hit {
	var hits []Hit
	handles := handleREs(l)
	for i, line := range bytes.Split(data, []byte("\n")) {
		s, low := string(line), strings.ToLower(string(line))
		at := func(what string) { hits = append(hits, Hit{name, i + 1, what}) }
		for _, k := range keys {
			for _, sub := range k.re.FindAllStringSubmatch(s, -1) {
				if !placeholder(k.what, sub[0], sub) {
					at(k.what)
					break
				}
			}
		}
		for j, w := range l.Deny {
			if strings.Contains(low, strings.ToLower(w)) {
				at(fmt.Sprintf("word %d of the deny list", j+1))
			}
		}
		for j, re := range handles {
			if re.MatchString(s) {
				at(fmt.Sprintf("handle %d of the handle list", j+1))
			}
		}
	}
	return hits
}

// keyArmour starts a private key and keyEnd ends one.
var (
	keyArmour = regexp.MustCompile(`-----BEGIN ([A-Z0-9]+ )*PRIVATE KEY( BLOCK)?-----`)
	keyEnd    = regexp.MustCompile(`-----END ([A-Z0-9]+ )*PRIVATE KEY( BLOCK)?-----`)
)

// maxKeyBlock is the most text from a private key's armour to its end that
// scrubKeyBlocks takes out: a 4096-bit RSA key is about 3.3 KB, and a turn
// log's line numbers and escapes can double that.
const maxKeyBlock = 32 << 10

// scrubKeyBlocks replaces each private key whose end it finds, armour, body
// and whatever each line carries before it (line numbers, quoting,
// indentation), with its kind. An armour with no end within maxKeyBlock is
// left as it is, so the scan, which flags the armour, still blocks it: the
// body could be anywhere after it, cut off or prefixed past recognising.
func scrubKeyBlocks(s string) string {
	var b strings.Builder
	for {
		ix := keyArmour.FindStringIndex(s)
		if ix == nil {
			return b.String() + s
		}
		rest := s[ix[1]:]
		end := keyEnd.FindStringIndex(rest[:min(len(rest), maxKeyBlock)])
		if end == nil || keyArmour.MatchString(rest[:end[0]]) {
			b.WriteString(s[:ix[1]]) // no end, or another key starts first: leave it to block
			s = rest
			continue
		}
		b.WriteString(s[:ix[0]] + "[a private key]")
		s = rest[end[1]:]
	}
}

// Scrub is data with what Text would flag replaced by its kind in brackets:
// every key (a private key through its end line, and of a secret assigned to a
// name only the value), every deny word and every handle. It is for what is
// sent to be read, such as `hive share`'s bundle of run logs that quote the
// tests' made-up keys; what it returns is still scanned before it goes.
func Scrub(data []byte, l Lists) []byte {
	s := scrubKeyBlocks(string(data))
	for _, k := range keys {
		if k.what == "a private key" {
			continue // scrubKeyBlocks took out every whole one; what is left must block
		}
		var b strings.Builder
		last := 0
		for _, ix := range k.re.FindAllStringSubmatchIndex(s, -1) {
			sub := make([]string, len(ix)/2)
			for i := range sub {
				if ix[2*i] >= 0 {
					sub[i] = s[ix[2*i]:ix[2*i+1]]
				}
			}
			if placeholder(k.what, sub[0], sub) {
				continue
			}
			from, to := ix[0], ix[1]
			if k.what == "a secret assigned to a name" {
				from, to = ix[len(ix)-2], ix[len(ix)-1]
			} else if k.what == "a Telegram bot token" && ix[2] >= 0 {
				from = ix[3] // keep what came before the token
			}
			b.WriteString(s[last:from] + "[" + k.what + "]")
			last = to
		}
		s = b.String() + s[last:]
	}
	for _, w := range l.Deny {
		if w != "" {
			s = regexp.MustCompile(`(?i)`+regexp.QuoteMeta(w)).ReplaceAllLiteralString(s, "[a deny word]")
		}
	}
	for _, re := range handleREs(l) {
		// A name's end is the next one's start ("carol,carol"), so a few passes.
		for i := 0; i < 3 && re.MatchString(s); i++ {
			s = re.ReplaceAllString(s, "${1}[a handle]${3}")
		}
	}
	return []byte(s)
}

// maxShown is how many hits a failure lists before it counts the rest.
const maxShown = 30

// Run scans the files among names that the public list names, reading each
// with read, and fails on any hit, on no public list, on a public list that
// names nothing, and on no handle list.
func Run(names []string, read func(string) ([]byte, error), l Lists) error {
	if len(l.Public) == 0 {
		return errors.New("no public list, so the scan can't tell what is published and refuses to pass")
	}
	if len(l.Handles) == 0 {
		return errors.New("no handle list was given, so the scan can't look for people's handles and refuses to pass")
	}
	files := Files(names, l.Public)
	if len(files) == 0 {
		return errors.New("the public list names no file, so the scan refuses to pass")
	}
	var hits []Hit
	for i, f := range files {
		data, err := read(f)
		// A file's name is published too. One that holds a key, a deny word or a
		// handle is named by its place among the published files instead, here
		// and in what its contents hit.
		shown := f
		if named := Text(f, []byte(f), l); len(named) > 0 {
			shown = fmt.Sprintf("published file %d", i+1)
			for _, h := range named {
				hits = append(hits, Hit{shown, 0, h.What + " in its name"})
			}
		}
		if err != nil {
			return fmt.Errorf("can't read %s, which the public list names: %v", shown, err)
		}
		hits = append(hits, Text(shown, data, l)...)
	}
	if len(hits) == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "the scan found %d thing(s) in %d published file(s) that must never be published:", len(hits), len(files))
	for i, h := range hits {
		if i == maxShown {
			fmt.Fprintf(&b, "\n  and %d more", len(hits)-maxShown)
			break
		}
		b.WriteString("\n  " + h.String())
	}
	return errors.New(b.String())
}
