package report

import (
	"regexp"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// scrubbers run in order: whole blocks first, then known token shapes, then
// anything a name says is secret, then emails and home folders.
var scrubbers = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*?(-----END [A-Z0-9 ]*PRIVATE KEY-----|$)`), "[private key]"},
	{regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^/\s:@]+:[^/\s@]+@`), "${1}[cred]@"},
	{regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{16,}|sk-[A-Za-z0-9_-]{16,}|[sr]k_(live|test)_[A-Za-z0-9]{16,}|xox[abposr]-[A-Za-z0-9-]{10,}|(AKIA|ASIA)[0-9A-Z]{16}|AIza[0-9A-Za-z_-]{30,}|glpat-[A-Za-z0-9_-]{16,}|npm_[A-Za-z0-9]{30,}|hf_[A-Za-z0-9]{30,}|eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,})`), "[secret]"},
	{regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`), "${1} [secret]"},
	{regexp.MustCompile(`(?im)\b((set-)?cookie\s*:\s*)[^\r\n]+`), "${1}[secret]"},
	{regexp.MustCompile(`(?i)([a-z0-9_.-]*(token|secret|passw(or)?d|pwd|api[_-]?key|access[_-]?key|private[_-]?key|credential|cookie|session)[a-z0-9_.-]*["']?\s*[:=]\s*["']?)[^\s"',;}]+`), "${1}[secret]"},
	// auth keys (auth, authorization, authorization-header, auth_token, authkey,
	// x-auth-token) but not author or authors.
	{regexp.MustCompile(`(?i)([a-z0-9_.-]*auth(orization[a-z0-9_.-]*|[_.-]?[a-np-z0-9_.-][a-z0-9_.-]*|o[a-qs-z0-9_.-][a-z0-9_.-]*)?["']?\s*[:=]\s*["']?)[^\s"',;}]+`), "${1}[secret]"},
	{regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`), "[email]"},
	{regexp.MustCompile(`(/home/|/Users/)[^/\s"']+|/root\b|(?i)\b[a-z]:\\users\\[^\\\s"']+`), "~"},
}

// longRun is the last pass: a long run with letters and digits is a key; one
// without digits is a word or a path.
var longRun = regexp.MustCompile(`[A-Za-z0-9+/_=-]{32,}`)

// Scrub removes what must never leave the machine: tokens, keys,
// credentials, emails and the person's home folder. root, if set, is the
// Hive's folder and becomes <hive>.
func Scrub(s, root string) string {
	if root != "" {
		s = strings.ReplaceAll(s, root, "<hive>")
	}
	if h := core.HomeDir(); len(h) > 1 {
		s = strings.ReplaceAll(s, h, "~")
	}
	for _, r := range scrubbers {
		s = r.re.ReplaceAllString(s, r.with)
	}
	return longRun.ReplaceAllStringFunc(s, func(m string) string {
		if mixed(m) {
			return "[secret]"
		}
		return m
	})
}

// mixed says a long run holds both letters and digits, as keys do.
func mixed(s string) bool {
	return strings.ContainsAny(s, "0123456789") && strings.IndexFunc(s, func(r rune) bool { return r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' }) >= 0
}
