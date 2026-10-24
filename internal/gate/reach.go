package gate

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
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
// than its owner); what it can do is probed by probeReach.
func listReach(g GitHub, token string) ([]Access, error) {
	var all []Access
	for _, path := range []string{"/user/repos?per_page=100&affiliation=owner,collaborator,organization_member", "/orgs/" + g.Owner + "/repos?per_page=100&type=all"} {
		out, err := ghAPI(token, "--paginate", path, "--jq", `.[] | [.full_name, (.permissions.push|tostring), (.permissions.admin|tostring)] | @tsv`)
		if err != nil {
			return nil, fmt.Errorf("listing what the children's token reaches (%s): %v: %s", path, err, tail(out, 200))
		}
		for _, line := range strings.Split(out, "\n") {
			f := strings.Split(strings.TrimSpace(line), "\t")
			if len(f) == 3 {
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
// administration access may make. Any other answer (a rate limit, a 5xx)
// is an error: the check never passes on what it couldn't learn.
func probeReach(org, token string, acc []Access) ([]Access, error) {
	var todo []int
	for i, a := range acc {
		if (a.Push || a.Admin) && !isGateRepo(org, a.Repo) {
			todo = append(todo, i)
		}
	}
	out := append([]Access(nil), acc...)
	errs := make([]error, len(acc))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(8, len(todo)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				r := acc[i].Repo
				push, err := probe(token, "-X", "POST", "/repos/"+r+"/git/blobs", "-f", "encoding=utf-8")
				admin := false
				if err == nil {
					admin, err = probe(token, "/repos/"+r+"/keys?per_page=1")
				}
				out[i] = Access{Repo: r, Push: push, Admin: admin}
				errs[i] = err
			}
		}()
	}
	for _, i := range todo {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	for _, i := range todo {
		if errs[i] != nil {
			return nil, errs[i]
		}
	}
	for i, a := range acc {
		if !a.Push && !a.Admin { // the owner can't write it, so neither can the token
			out[i] = Access{Repo: a.Repo}
		}
	}
	return out, nil
}

var httpStatus = regexp.MustCompile(`\(HTTP ([0-9]{3})\)`)

// probe is whether the token may make the call: yes on 2xx or 422 (allowed,
// then refused for its body), no on 403 or 404.
func probe(token string, args ...string) (bool, error) {
	out, err := ghAPI(token, args...)
	if err == nil {
		return true, nil
	}
	m := httpStatus.FindStringSubmatch(out)
	switch {
	case m == nil:
	case m[1] == "422":
		return true, nil
	case m[1] == "404", m[1] == "403" && !strings.Contains(strings.ToLower(out), "rate limit"):
		return false, nil
	}
	return false, fmt.Errorf("probing what the children's token can do (%s): %v: %s", strings.Join(args, " "), err, tail(out, 200))
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
