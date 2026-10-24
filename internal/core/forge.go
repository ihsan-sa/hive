package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Issue and PR are the forge's records.
type Issue struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

type PR struct {
	Number    int    `json:"number"`
	URL       string `json:"url"`
	State     string `json:"state"` // OPEN, MERGED, CLOSED
	HeadSHA   string `json:"head_sha"`
	MergeSHA  string `json:"merge_sha,omitempty"`
	Mergeable string `json:"mergeable,omitempty"`
}

// Forge is where code, issues and pull requests live. GitHub through the gh
// CLI is the real one; Local is a folder-backed stand-in for tests.
type Forge interface {
	Name() string
	User() (string, error)
	EnsureRepo(repo, description string) (created bool, err error)
	// Fork is owner's fork of repo, made on first need; owner "" is the
	// person's own account, else an organization they can create repos in.
	Fork(repo, owner string) (string, error)
	Clone(repo, dir string) error
	IsEmpty(repo string) (bool, error)
	DefaultBranch(repo string) string
	CreateIssue(repo, title, body, marker string) (Issue, error)
	Comment(repo string, number int, body string) error
	CreatePR(repo, head, base, title, body string) (PR, error)
	FindPR(repo, head string) (PR, bool, error)
	GetPR(repo string, number int) (PR, error)
	MergePR(repo string, number int, sha, subject string) (string, error)
	ClosePR(repo string, number int, comment string) error
	CloseIssue(repo string, number int) error // an issue already closed counts as closed
	// Releases (the Hive on itself).
	LatestRelease(repo string) (Release, error)
	CreateRelease(repo, tag, sha, notes string) error
}

// Release is a published version.
type Release struct {
	Tag    string   `json:"tag"`
	Assets []string `json:"assets"`
}

// GitHub is the real forge, driven through the person's own gh login.
type GitHub struct{}

func (GitHub) Name() string { return "github" }

// OwnerTokens maps a lower-cased repo owner to a file holding the GitHub token
// for that owner. An owner not listed (or whose file is unreadable) uses gh's
// own login. Both tokens act as the same user, so gh cannot hold them as two
// logins; the effects process passes GH_TOKEN per call instead.
var OwnerTokens = map[string]string{
	"hive-rsi-lab": "/home/hive/h0-org.token",
}

// ghEnv is the environment that makes gh and git's gh credential helper use
// the token configured for repo's owner, or nothing to keep gh's login.
func ghEnv(repo string) []string {
	owner, _, _ := strings.Cut(repo, "/")
	path := OwnerTokens[strings.ToLower(owner)]
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return nil
	}
	return []string{"GH_TOKEN=" + tok}
}

func gh(repo string, args ...string) (string, error) {
	return RunCmdEnv(2*time.Minute, "", ghEnv(repo), "gh", args...)
}

// User is the login gh itself holds.
func (GitHub) User() (string, error) { return gh("", "api", "user", "-q", ".login") }

func (g GitHub) EnsureRepo(repo, description string) (bool, error) {
	if _, err := gh(repo, "repo", "view", repo, "--json", "name"); err == nil {
		return false, nil
	}
	if _, err := gh(repo, "repo", "create", repo, "--private", "--description", description); err != nil {
		return false, err
	}
	return true, nil
}

// Fork returns owner's fork of repo (the person's when owner is ""),
// creating it if there is none. GitHub answers before a new fork's branches
// exist, so it waits for them, up to a minute, and the first clone finds them.
func (GitHub) Fork(repo, owner string) (string, error) {
	args := []string{"api", "-X", "POST", "repos/" + repo + "/forks", "-q", ".full_name"}
	if owner != "" {
		args = append(args, "-f", "organization="+owner)
	}
	tokenRepo := repo
	if owner != "" {
		tokenRepo = owner + "/"
	}
	fork, err := gh(tokenRepo, args...)
	for i := 0; err == nil && i < 20 && !forkReady(fork); i++ {
		time.Sleep(3 * time.Second)
	}
	return fork, err
}

// forkReady reports whether a fork's branches exist yet. Tests replace it.
var forkReady = func(fork string) bool {
	out, err := GitEnvT(time.Minute, "", ghEnv(fork), "-c", "credential.helper=", "-c", "credential.helper="+ghHelper(), "ls-remote", "--heads", "https://github.com/"+fork+".git")
	return err == nil && strings.TrimSpace(out) != ""
}

// ghHelper is git's credential helper backed by the person's gh login, so
// git talks HTTPS whatever protocol gh is set to and whether or not SSH keys
// work in this shell. The Hive never sees or stores the credential.
func ghHelper() string {
	p, err := exec.LookPath("gh")
	if err != nil {
		p = "gh"
	}
	return `!"` + filepath.ToSlash(p) + `" auth git-credential`
}

// Clone clones over HTTPS and leaves the clone set to authenticate through gh.
func (GitHub) Clone(repo, dir string) error {
	url := "https://github.com/" + repo + ".git"
	if _, err := GitEnvT(10*time.Minute, "", ghEnv(repo), "-c", "credential.helper=", "-c", "credential.helper="+ghHelper(), "clone", "-q", url, dir); err != nil {
		return err
	}
	Git(dir, "config", "--local", "--unset-all", "credential.helper")
	if _, err := Git(dir, "config", "--local", "--add", "credential.helper", ""); err != nil {
		return err
	}
	_, err := Git(dir, "config", "--local", "--add", "credential.helper", ghHelper())
	return err
}

// IsEmpty reports whether a repository has no commits.
func (GitHub) IsEmpty(repo string) (bool, error) {
	out, err := gh(repo, "repo", "view", repo, "--json", "isEmpty", "-q", ".isEmpty")
	return out == "true", err
}

func (GitHub) DefaultBranch(repo string) string {
	out, err := gh(repo, "repo", "view", repo, "--json", "defaultBranchRef", "-q", ".defaultBranchRef.name")
	if err != nil || out == "" {
		return "main"
	}
	return out
}

var numRe = regexp.MustCompile(`/(\d+)\s*$`)

func numberFromURL(u string) int {
	m := numRe.FindStringSubmatch(strings.TrimSpace(u))
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// CreateIssue is idempotent: an issue whose body carries marker is reused.
func (GitHub) CreateIssue(repo, title, body, marker string) (Issue, error) {
	out, err := gh(repo, "issue", "list", "-R", repo, "--state", "all", "--limit", "100", "--json", "number,url,body")
	if err == nil {
		var list []struct {
			Number int    `json:"number"`
			URL    string `json:"url"`
			Body   string `json:"body"`
		}
		if json.Unmarshal([]byte(out), &list) == nil {
			for _, i := range list {
				if strings.Contains(i.Body, marker) {
					return Issue{i.Number, i.URL}, nil
				}
			}
		}
	}
	url, err := gh(repo, "issue", "create", "-R", repo, "--title", title, "--body", body+"\n\n"+marker)
	if err != nil {
		return Issue{}, err
	}
	return Issue{numberFromURL(url), url}, nil
}

func (GitHub) Comment(repo string, number int, body string) error {
	_, err := gh(repo, "issue", "comment", strconv.Itoa(number), "-R", repo, "--body", body)
	return err
}

type ghPR struct {
	Number      int    `json:"number"`
	URL         string `json:"url"`
	State       string `json:"state"`
	HeadRefOid  string `json:"headRefOid"`
	Mergeable   string `json:"mergeable"`
	MergeCommit *struct {
		Oid string `json:"oid"`
	} `json:"mergeCommit"`
	HeadRepositoryOwner struct {
		Login string `json:"login"`
	} `json:"headRepositoryOwner"`
}

func (p ghPR) pr() PR {
	out := PR{Number: p.Number, URL: p.URL, State: p.State, HeadSHA: p.HeadRefOid, Mergeable: p.Mergeable}
	if p.MergeCommit != nil {
		out.MergeSHA = p.MergeCommit.Oid
	}
	return out
}

const prFields = "number,url,state,headRefOid,mergeable,mergeCommit,headRepositoryOwner"

func (g GitHub) FindPR(repo, head string) (PR, bool, error) {
	args := []string{"pr", "list", "-R", repo, "--head", head, "--state", "all", "--json", prFields}
	// A fork's "owner:branch": gh matches the branch name alone, and once
	// merged the next change needs a new pull request.
	owner, branch, fork := strings.Cut(head, ":")
	if fork {
		args[5], args[7] = branch, "open"
	}
	out, err := gh(repo, args...)
	if err != nil {
		return PR{}, false, err
	}
	var list []ghPR
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return PR{}, false, err
	}
	for _, p := range list {
		if p.State != "CLOSED" && (!fork || p.HeadRepositoryOwner.Login == owner) {
			return p.pr(), true, nil
		}
	}
	return PR{}, false, nil
}

func (g GitHub) CreatePR(repo, head, base, title, body string) (PR, error) {
	if p, ok, err := g.FindPR(repo, head); err == nil && ok {
		return p, nil
	}
	url, err := gh(repo, "pr", "create", "-R", repo, "--head", head, "--base", base, "--title", title, "--body", body)
	if err != nil {
		return PR{}, err
	}
	return g.GetPR(repo, numberFromURL(url))
}

func (GitHub) GetPR(repo string, number int) (PR, error) {
	out, err := gh(repo, "pr", "view", strconv.Itoa(number), "-R", repo, "--json", prFields)
	if err != nil {
		return PR{}, err
	}
	var p ghPR
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		return PR{}, err
	}
	return p.pr(), nil
}

// MergePR squash-merges only if the head is still exactly sha.
func (g GitHub) MergePR(repo string, number int, sha, subject string) (string, error) {
	p, err := g.GetPR(repo, number)
	if err != nil {
		return "", err
	}
	if p.State == "MERGED" {
		if p.HeadSHA != sha {
			return "", fmt.Errorf("PR #%d was merged at %s, not the checked commit %s", number, short(p.HeadSHA), short(sha))
		}
		return p.MergeSHA, nil
	}
	if p.HeadSHA != sha {
		return "", fmt.Errorf("PR #%d head is %s, not the checked commit %s", number, short(p.HeadSHA), short(sha))
	}
	if _, err := gh(repo, "pr", "merge", strconv.Itoa(number), "-R", repo, "--squash", "--match-head-commit", sha, "--subject", subject); err != nil {
		return "", err
	}
	for i := 0; i < 10; i++ {
		p, err = g.GetPR(repo, number)
		if err == nil && p.State == "MERGED" && p.MergeSHA != "" {
			return p.MergeSHA, nil
		}
		time.Sleep(2 * time.Second)
	}
	return "", errors.New("merge reported success but the PR is not merged yet")
}

func (GitHub) ClosePR(repo string, number int, comment string) error {
	_, err := gh(repo, "pr", "close", strconv.Itoa(number), "-R", repo, "--comment", comment)
	return err
}

func (GitHub) CloseIssue(repo string, number int) error {
	_, err := gh(repo, "issue", "close", strconv.Itoa(number), "-R", repo)
	return err
}

func (GitHub) LatestRelease(repo string) (Release, error) {
	out, err := gh(repo, "release", "view", "-R", repo, "--json", "tagName,assets")
	if err != nil {
		return Release{}, err
	}
	var r struct {
		TagName string `json:"tagName"`
		Assets  []struct {
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		return Release{}, err
	}
	rel := Release{Tag: r.TagName}
	for _, a := range r.Assets {
		rel.Assets = append(rel.Assets, a.Name)
	}
	return rel, nil
}

func (GitHub) CreateRelease(repo, tag, sha, notes string) error {
	_, err := gh(repo, "release", "create", tag, "-R", repo, "--target", sha, "--title", tag, "--notes", notes)
	return err
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// Short abbreviates a commit id.
func Short(sha string) string { return short(sha) }
