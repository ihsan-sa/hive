package gate

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Access is what a token can do to one repository.
type Access struct {
	Repo        string // owner/name
	Push, Admin bool
}

// ghAPI runs `gh api` with token. It is a variable so tests can replay
// recorded GitHub responses.
var ghAPI = func(token string, args ...string) (string, error) {
	return GitHub{Token: token}.gh(append([]string{"api"}, args...)...)
}

// listReach lists the repositories token can see: its own, its
// collaborations, its orgs', and g.Owner's, with the owner's role on each.
// For a fine-grained token that role is the owner's, not the token's: a
// token on one org still lists every public repository of its owner, marked
// admin. So the role only says what the token can't do (it never does more
// than its owner); what it can do is probed by probeReach. A repository in
// both listings (an org's, with organization_member) is kept once.
func listReach(g GitHub, token string) ([]Access, error) {
	var all []Access
	seen := map[string]bool{}
	for _, path := range []string{"/user/repos?per_page=100&affiliation=owner,collaborator,organization_member", "/orgs/" + g.Owner + "/repos?per_page=100&type=all"} {
		out, err := ghAPI(token, "--paginate", path, "--jq", `.[] | [.full_name, (.permissions.push|tostring), (.permissions.admin|tostring)] | @tsv`)
		if err != nil {
			return nil, fmt.Errorf("%w: listing what it reaches (%s): %v: %s", ErrReachUnknown, path, err, tail(out, 200))
		}
		for _, line := range strings.Split(out, "\n") {
			f := strings.Split(strings.TrimSpace(line), "\t")
			if len(f) == 3 && !seen[strings.ToLower(f[0])] {
				seen[strings.ToLower(f[0])] = true
				all = append(all, Access{Repo: f[0], Push: f[1] == "true", Admin: f[2] == "true"})
			}
		}
	}
	return all, nil
}

// probeReach asks GitHub what token itself can do to each repository its
// owner could write, the gate's own excepted. Contents write: creating a
// blob with no content, which GitHub refuses with 403 or 404 when the token
// lacks the permission and with 422 when it has it, so nothing is ever
// written. Administration: listing deploy keys, a read only a token with
// administration access may make. It stops at the first repository the
// token can write. An owner with hundreds of public repositories makes
// hundreds of probes, so they run one at a time, paced under GitHub's
// secondary limit (probeBudget points a minute, a POST costing 5 and a GET
// 1), and a secondary rate limit is waited out (its Retry-After, else a
// minute) and retried. Any other answer, or a limit that doesn't lift, is an
// ErrReachUnknown: the check never passes on what it couldn't learn.
func probeReach(org, token string, acc []Access) ([]Access, error) {
	out := make([]Access, 0, len(acc))
	p := &pacer{}
	for _, a := range acc {
		if !a.Push && !a.Admin { // the owner can't write it, so neither can the token
			out = append(out, Access{Repo: a.Repo})
			continue
		}
		if isGateRepo(org, a.Repo) {
			out = append(out, a)
			continue
		}
		push, err := p.probe(token, 5, "-X", "POST", "/repos/"+a.Repo+"/git/blobs", "-f", "encoding=utf-8")
		admin := false
		if err == nil && !push {
			admin, err = p.probe(token, 1, "/repos/"+a.Repo+"/keys?per_page=1")
		}
		if err != nil {
			return nil, fmt.Errorf("the probe couldn't run on %s, so the check refuses rather than guess (an empty repository answers 409: push a commit to it or move it out of the org): %w", a.Repo, err)
		}
		out = append(out, Access{Repo: a.Repo, Push: push, Admin: admin})
		if push || admin {
			break // refused already; no need to ask about the rest
		}
	}
	return out, nil
}

// ErrReachUnknown marks a reach check that couldn't learn the answer (a rate
// limit, GitHub unreachable), as against a token found too wide. A caller
// may ask again soon; it must not run on the org meanwhile.
var ErrReachUnknown = errors.New("the children's token's reach is unknown")

// probeBudget is the probes' points a minute: under GitHub's 900 for REST.
// reachSleep and reachNow are variables for tests.
var (
	probeBudget = 600
	reachSleep  = time.Sleep
	reachNow    = time.Now
)

// pacer spends probeBudget points a minute at most.
type pacer struct{ next time.Time }

var (
	httpStatus = regexp.MustCompile(`(?m)^HTTP/[0-9.]+ ([0-9]{3})|\(HTTP ([0-9]{3})\)`)
	retryAfter = regexp.MustCompile(`(?mi)^retry-after:\s*([0-9]+)`)
)

// probe is whether the token may make the call: yes on 2xx or 422 (allowed,
// then refused for its body), no on 403 or 404.
func (p *pacer) probe(token string, points int, args ...string) (bool, error) {
	args = append([]string{"--include"}, args...)
	for try := 0; ; try++ {
		if d := p.next.Sub(reachNow()); d > 0 {
			reachSleep(d)
		}
		p.next = reachNow().Add(time.Duration(points) * time.Minute / time.Duration(probeBudget))
		out, err := ghAPI(token, args...)
		code := ""
		if m := httpStatus.FindStringSubmatch(out); m != nil {
			code = m[1] + m[2]
		}
		limited := (code == "403" || code == "429") && strings.Contains(strings.ToLower(out), "rate limit")
		switch {
		case limited && try < 3:
			wait := time.Minute
			if m := retryAfter.FindStringSubmatch(out); m != nil {
				n, _ := strconv.Atoi(m[1])
				wait = time.Duration(n) * time.Second
			}
			reachSleep(wait)
			continue
		case limited:
		case err == nil && code != "" && code[0] == '2', code == "422":
			return true, nil
		case code == "403", code == "404":
			return false, nil
		}
		if code == "" {
			code = "no status"
		}
		return false, fmt.Errorf("%w: GitHub answered %s to gh api %s: %v: %s", ErrReachUnknown, code, strings.Join(args[1:], " "), err, tail(out, 200))
	}
}

// CheckReach refuses a children's token that can write more than the gate's
// throwaway repositories. It must be fine-grained (a classic token reaches
// all its user's repositories), the test org must not be the owner of the
// repository being gated, and every repository the token itself can push to
// or administer, as probed, must be a gate-* repository in the test org.
// Repositories it can only read (the owner's public ones) don't count. An
// org-wide token therefore passes only in an org that holds nothing but the
// gate's repositories: a test-only org, which is the person's to make. What
// a fine-grained token may do to the org itself (members, settings) or with
// repository permissions other than contents and administration the check
// does not probe; mint it with Contents read/write and the read permissions
// the checks need only.
func CheckReach(g GitHub, token, gated string) error {
	if !strings.HasPrefix(token, "github_pat_") {
		return fmt.Errorf("the children's token must be a fine-grained token (github_pat_…) on the test org alone")
	}
	if gatedOwner := ownerOf(gated); gatedOwner != "" && strings.EqualFold(g.Owner, gatedOwner) {
		return reachPolicy(g.Owner, gatedOwner, nil)
	}
	acc, err := listReach(g, token)
	if err != nil {
		return err
	}
	if acc, err = probeReach(g.Owner, token, acc); err != nil {
		return err
	}
	return reachPolicy(g.Owner, ownerOf(gated), acc)
}

var gateRepo = regexp.MustCompile(`^gate-[0-9]+-[0-9a-f]{8}-(candidate|control)$`)

func reachPolicy(org, gatedOwner string, acc []Access) error {
	if gatedOwner != "" && strings.EqualFold(org, gatedOwner) {
		return fmt.Errorf("the test org %s owns the repository being gated; use an org that holds only the gate's repositories", org)
	}
	var wide []string
	for _, a := range acc {
		if !a.Push && !a.Admin {
			continue
		}
		if !isGateRepo(org, a.Repo) {
			wide = append(wide, a.Repo)
		}
	}
	if len(wide) > 0 {
		if len(wide) > 5 {
			wide = append(wide[:5], fmt.Sprintf("and %d more", len(wide)-5))
		}
		return fmt.Errorf("the children's token can write to more than the gate's repositories in %s: %s. The owner must move those out of %s (transfer them to another owner, or delete them), or mint the children's token without write access to them", org, strings.Join(wide, ", "), org)
	}
	return nil
}

func isGateRepo(org, repo string) bool {
	owner, name, _ := strings.Cut(repo, "/")
	return strings.EqualFold(owner, org) && gateRepo.MatchString(name)
}

// ownerOf is the GitHub owner in a repository URL or owner/name, if any.
func ownerOf(repo string) string {
	for _, p := range []string{"github.com/", "github.com:"} {
		if i := strings.Index(repo, p); i >= 0 {
			owner, _, _ := strings.Cut(repo[i+len(p):], "/")
			return owner
		}
	}
	return ""
}
