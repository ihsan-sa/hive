package core

import (
	"encoding/json"
	"errors"
	"fmt"
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
	// Releases (the Hive on itself).
	LatestRelease(repo string) (Release, error)
	CreateRelease(repo, tag, sha, notes string) error
	DownloadAsset(repo, tag, pattern, dir string) error
}

// Release is a published version.
type Release struct {
	Tag    string   `json:"tag"`
	Assets []string `json:"assets"`
}

// GitHub is the real forge, driven through the person's own gh login.
type GitHub struct{}

func (GitHub) Name() string { return "github" }

func gh(args ...string) (string, error) { return RunCmd(2*time.Minute, "", "gh", args...) }

func (GitHub) User() (string, error) { return gh("api", "user", "-q", ".login") }

func (g GitHub) EnsureRepo(repo, description string) (bool, error) {
	if _, err := gh("repo", "view", repo, "--json", "name"); err == nil {
		return false, nil
	}
	if _, err := gh("repo", "create", repo, "--private", "--description", description); err != nil {
		return false, err
	}
	return true, nil
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
	if _, err := GitT(10*time.Minute, "", "-c", "credential.helper=", "-c", "credential.helper="+ghHelper(), "clone", "-q", url, dir); err != nil {
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
	out, err := gh("repo", "view", repo, "--json", "isEmpty", "-q", ".isEmpty")
	return out == "true", err
}

func (GitHub) DefaultBranch(repo string) string {
	out, err := gh("repo", "view", repo, "--json", "defaultBranchRef", "-q", ".defaultBranchRef.name")
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
	out, err := gh("issue", "list", "-R", repo, "--state", "all", "--limit", "100", "--json", "number,url,body")
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
	url, err := gh("issue", "create", "-R", repo, "--title", title, "--body", body+"\n\n"+marker)
	if err != nil {
		return Issue{}, err
	}
	return Issue{numberFromURL(url), url}, nil
}

func (GitHub) Comment(repo string, number int, body string) error {
	_, err := gh("issue", "comment", strconv.Itoa(number), "-R", repo, "--body", body)
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
}

func (p ghPR) pr() PR {
	out := PR{Number: p.Number, URL: p.URL, State: p.State, HeadSHA: p.HeadRefOid, Mergeable: p.Mergeable}
	if p.MergeCommit != nil {
		out.MergeSHA = p.MergeCommit.Oid
	}
	return out
}

const prFields = "number,url,state,headRefOid,mergeable,mergeCommit"

func (g GitHub) FindPR(repo, head string) (PR, bool, error) {
	out, err := gh("pr", "list", "-R", repo, "--head", head, "--state", "all", "--json", prFields)
	if err != nil {
		return PR{}, false, err
	}
	var list []ghPR
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return PR{}, false, err
	}
	for _, p := range list {
		if p.State != "CLOSED" {
			return p.pr(), true, nil
		}
	}
	return PR{}, false, nil
}

func (g GitHub) CreatePR(repo, head, base, title, body string) (PR, error) {
	if p, ok, err := g.FindPR(repo, head); err == nil && ok {
		return p, nil
	}
	url, err := gh("pr", "create", "-R", repo, "--head", head, "--base", base, "--title", title, "--body", body)
	if err != nil {
		return PR{}, err
	}
	return g.GetPR(repo, numberFromURL(url))
}

func (GitHub) GetPR(repo string, number int) (PR, error) {
	out, err := gh("pr", "view", strconv.Itoa(number), "-R", repo, "--json", prFields)
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
	if _, err := gh("pr", "merge", strconv.Itoa(number), "-R", repo, "--squash", "--match-head-commit", sha, "--subject", subject); err != nil {
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
	_, err := gh("pr", "close", strconv.Itoa(number), "-R", repo, "--comment", comment)
	return err
}

func (GitHub) LatestRelease(repo string) (Release, error) {
	out, err := gh("release", "view", "-R", repo, "--json", "tagName,assets")
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
	_, err := gh("release", "create", tag, "-R", repo, "--target", sha, "--title", tag, "--notes", notes)
	return err
}

func (GitHub) DownloadAsset(repo, tag, pattern, dir string) error {
	_, err := RunCmd(10*time.Minute, "", "gh", "release", "download", tag, "-R", repo, "--pattern", pattern, "--dir", dir, "--clobber")
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
