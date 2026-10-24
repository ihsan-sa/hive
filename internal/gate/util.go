package gate

import (
	"encoding/base64"
	"regexp"
	"strings"
)

// basic is the Authorization value git over HTTPS takes for a GitHub token.
func basic(token string) string {
	return base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
}

func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

// tokenShape is what in a child's output could be a credential: a known
// GitHub or API key prefix, a bearer or basic value, or a JWT. Commit and
// binary hashes are hex without such a prefix, so they stay.
var tokenShape = regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{16,}|sk-[A-Za-z0-9_-]{16,}|eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,})|(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`)

// redact takes the gate's tokens, in the forms git would show them, and
// anything token-shaped out of text a child's run produced.
func redact(s string, tokens []string) string {
	for _, t := range tokens {
		if len(t) < 8 {
			continue
		}
		for _, f := range []string{t, basic(t), basicAuth("x-access-token", t)} {
			s = strings.ReplaceAll(s, f, "[secret]")
		}
	}
	return tokenShape.ReplaceAllString(s, "[secret]")
}

// redact strips secrets from every part of the report a child's output can
// reach (its probes' details, errors and reasons), before it is signed and
// written: report.md is posted on the pull request, which may be public, and
// copied to the worker.
func (r *Report) redact(tokens ...string) {
	f := func(s string) string { return redact(s, tokens) }
	r.Error, r.Verdict.Reason = f(r.Error), f(r.Verdict.Reason)
	for _, l := range []*[]string{&r.Verdict.Gains, &r.Verdict.Regressions, &r.Verdict.Unmeasured} {
		for i := range *l {
			(*l)[i] = f((*l)[i])
		}
	}
	for _, rs := range [][]Result{r.Candidate, r.Control} {
		for i := range rs {
			rs[i].Detail = f(rs[i].Detail)
		}
	}
}
