package gate

import (
	"fmt"
	"regexp"
	"strings"
)

// Access is what a token can do to one repository.
type Access struct {
	Repo        string // owner/name
	Push, Admin bool
}

// listReach lists the repositories token can see: its own, its
// collaborations, its orgs', and g.Owner's. A fine-grained token sees only
// what it was granted. It is a variable for tests.
var listReach = func(g GitHub, token string) ([]Access, error) {
	var all []Access
	for _, path := range []string{"/user/repos?per_page=100&affiliation=owner,collaborator,organization_member", "/orgs/" + g.Owner + "/repos?per_page=100&type=all"} {
		out, err := GitHub{Owner: g.Owner, Token: token}.gh("api", "--paginate", path, "--jq", `.[] | [.full_name, (.permissions.push|tostring), (.permissions.admin|tostring)] | @tsv`)
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

// CheckReach refuses a children's token that reaches more than the gate's
// throwaway repositories. It must be fine-grained (a classic token reaches
// all its user's repositories), the test org must not be the owner of the
// repository being gated, and everything it can push to or administer must
// be a gate-* repository in the test org. An org-wide token therefore passes
// only in an org that holds nothing but the gate's repositories: a test-only
// org, which is the person's to make. What a fine-grained token may do to the
// org itself (members, settings) the API does not show; mint it with
// repository permissions only.
func CheckReach(g GitHub, token, gated string) error {
	if !strings.HasPrefix(token, "github_pat_") {
		return fmt.Errorf("the children's token must be a fine-grained token (github_pat_…) on the test org alone")
	}
	acc, err := listReach(g, token)
	if err != nil {
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
		owner, name, _ := strings.Cut(a.Repo, "/")
		if !strings.EqualFold(owner, org) || !gateRepo.MatchString(name) {
			wide = append(wide, a.Repo)
		}
	}
	if len(wide) > 0 {
		if len(wide) > 5 {
			wide = append(wide[:5], fmt.Sprintf("and %d more", len(wide)-5))
		}
		return fmt.Errorf("the children's token can write to more than the gate's repositories in %s: %s", org, strings.Join(wide, ", "))
	}
	return nil
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
