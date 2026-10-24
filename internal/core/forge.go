package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
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
	// StatusComment keeps one comment per issue or PR that carries marker:
	// the first call posts it, later calls edit it in place.
	StatusComment(repo string, number int, marker, body string) error
	CreatePR(repo, head, base, title, body string) (PR, error)
	FindPR(repo, head string) (PR, bool, error)
	GetPR(repo string, number int) (PR, error)
	MergePR(repo string, number int, sha, subject string) (string, error)
	ClosePR(repo string, number int, comment string) error
	CloseIssue(repo string, number int) error // an issue already closed counts as closed
	// Releases (the Hive on itself). LatestRelease is the newest, a
	// pre-release included. CreateRelease cuts a pre-release, which installs
	// don't follow; PublishRelease makes it the latest once ReleaseRuns, the
	// workflow runs its publishing started at sha, pass.
	LatestRelease(repo string) (Release, error)
	CreateRelease(repo, tag, sha, notes string) error
	ReleaseRuns(repo, sha string) ([]WorkflowRun, error)
	PublishRelease(repo, tag string) error
}

// Release is a version on the forge.
type Release struct {
	Tag        string   `json:"tag"`
	Assets     []string `json:"assets"`
	Prerelease bool     `json:"prerelease,omitempty"`
}

// WorkflowRun is one run of a CI workflow, newest first in a list.
type WorkflowRun struct {
	Name       string `json:"name"`
	Status     string `json:"status"`     // queued, in_progress, completed
	Conclusion string `json:"conclusion"` // success, failure, cancelled... once completed
	URL        string `json:"html_url"`
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

// ghRun runs gh and returns its stdout and stderr. Tests replace it.
var ghRun = func(repo string, args []string) (string, string, error) {
	return runCmd(2*time.Minute, "", ghEnv(repo), "gh", args...)
}

// gh runs one gh command. Its error carries gh's stderr, the actual reason,
// under a short name for the command, never the arguments (which hold bodies
// long enough to push the reason out of any log line). While GitHub has asked
// for a pause, writes wait it out without being sent.
func gh(repo string, args ...string) (string, error) {
	write := ghWrites(args)
	if write {
		if err := ghPause.wait(); err != nil {
			return "", err
		}
	}
	out, stderr, err := ghRun(repo, args)
	if err != nil {
		if rl := ghPause.hit(stderr); rl != nil {
			return out, rl
		}
		if stderr == "" {
			stderr = err.Error()
		}
		return out, fmt.Errorf("gh %s: %s", ghVerb(args), stderr)
	}
	if write {
		ghPause.clear()
	}
	return out, nil
}

// ghVerb names a gh command by its leading words, such as "pr create" or
// "api repos/o/r/issues/3/comments".
func ghVerb(args []string) string {
	var w []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") || len(w) == 3 {
			break
		}
		w = append(w, a)
	}
	return strings.Join(w, " ")
}

// ghWrites reports whether a gh command writes to GitHub.
func ghWrites(args []string) bool {
	if len(args) == 0 {
		return false
	}
	if args[0] == "api" {
		for i, a := range args {
			switch a {
			case "-f", "-F", "--field", "--raw-field", "--input":
				return true
			case "-X", "--method":
				return i+1 < len(args) && !strings.EqualFold(args[i+1], "GET")
			}
		}
		return false
	}
	return len(args) > 1 && slices.Contains([]string{"create", "comment", "merge", "close", "edit", "delete"}, args[1])
}

// RateLimited is GitHub asking the Hive to slow down: a secondary rate limit
// ("was submitted too quickly"), or a 403 or 429 that names a rate limit or a
// retry-after. Writes wait until Until; it isn't any task's fault.
type RateLimited struct {
	Until time.Time
	Text  string // gh's own words
}

func (r *RateLimited) Error() string {
	return fmt.Sprintf("GitHub asked the Hive to slow down, so its writes wait until %s: %s", r.Until.UTC().Format("15:04:05Z"), r.Text)
}

// IsRateLimit reports whether an error's text is GitHub's rate limit.
func IsRateLimit(text string) bool {
	_, ok := rateLimit(text)
	return ok
}

var retryAfterRe = regexp.MustCompile(`(?i)retry[- ]after:?\s*(\d+)`)

// rateLimit reads gh's stderr for a rate limit and the wait GitHub asked for
// (0 when it named none).
func rateLimit(stderr string) (time.Duration, bool) {
	t := strings.ToLower(stderr)
	has := func(words ...string) bool {
		return slices.ContainsFunc(words, func(w string) bool { return strings.Contains(t, w) })
	}
	limited := has("submitted too quickly", "secondary rate limit", "api rate limit exceeded", "abuse detection") ||
		has("http 403", "http 429", " 403 ", " 429 ") && has("rate limit", "retry-after", "retry after")
	if !limited {
		return 0, false
	}
	var d time.Duration
	if m := retryAfterRe.FindStringSubmatch(stderr); m != nil {
		n, _ := strconv.Atoi(m[1])
		d = time.Duration(n) * time.Second
	}
	return d, true
}

// pause is the one wait every write to GitHub shares, since GitHub limits the
// login, not a task: a minute after the first hit, doubling to 15 minutes
// while hits keep coming, or the retry-after GitHub names.
type pause struct {
	mu    sync.Mutex
	until time.Time
	hits  int
	text  string
}

var ghPause = &pause{}

func (p *pause) wait() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if time.Now().Before(p.until) {
		return &RateLimited{Until: p.until, Text: p.text}
	}
	return nil
}

func (p *pause) hit(stderr string) *RateLimited {
	d, ok := rateLimit(stderr)
	if !ok {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if d <= 0 {
		d = min(time.Minute<<min(p.hits, 4), 15*time.Minute)
	}
	if u := time.Now().Add(d); u.After(p.until) {
		p.until = u
	}
	p.hits++
	p.text = Clip(stderr, 300)
	return &RateLimited{Until: p.until, Text: p.text}
}

func (p *pause) clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hits = 0
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

func (g GitHub) StatusComment(repo string, number int, marker, body string) error {
	body += "\n\n" + marker
	path := fmt.Sprintf("repos/%s/issues/%d/comments", repo, number)
	out, err := gh(repo, "api", "--paginate", path, "-q", fmt.Sprintf(".[] | select(.body | contains(%q)) | .id", marker))
	if err != nil {
		return err
	}
	if id, _, _ := strings.Cut(strings.TrimSpace(out), "\n"); id != "" {
		_, err = gh(repo, "api", "-X", "PATCH", fmt.Sprintf("repos/%s/issues/comments/%s", repo, id), "-f", "body="+body)
		return err
	}
	return g.Comment(repo, number, body)
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
	out, err := gh(repo, "release", "list", "-R", repo, "--exclude-drafts", "--limit", "1", "--json", "tagName,isPrerelease")
	if err != nil {
		return Release{}, err
	}
	var rs []struct {
		TagName      string `json:"tagName"`
		IsPrerelease bool   `json:"isPrerelease"`
	}
	if err := json.Unmarshal([]byte(out), &rs); err != nil {
		return Release{}, err
	}
	if len(rs) == 0 {
		return Release{}, ErrNotFound
	}
	rel := Release{Tag: rs[0].TagName, Prerelease: rs[0].IsPrerelease}
	if out, err = gh(repo, "release", "view", rel.Tag, "-R", repo, "--json", "assets", "-q", ".assets[].name"); err == nil && out != "" {
		rel.Assets = strings.Split(out, "\n")
	}
	return rel, nil
}

func (GitHub) CreateRelease(repo, tag, sha, notes string) error {
	_, err := gh(repo, "release", "create", tag, "-R", repo, "--target", sha, "--title", tag, "--notes", notes, "--prerelease")
	return err
}

// ReleaseRuns are the runs a release event started at sha (a release's runs
// check out its tag, whose commit is sha), newest first.
func (GitHub) ReleaseRuns(repo, sha string) ([]WorkflowRun, error) {
	out, err := gh(repo, "api", "repos/"+repo+"/actions/runs?event=release&per_page=100&head_sha="+sha, "-q", ".workflow_runs")
	if err != nil {
		return nil, err
	}
	var runs []WorkflowRun
	return runs, json.Unmarshal([]byte(out), &runs)
}

func (GitHub) PublishRelease(repo, tag string) error {
	_, err := gh(repo, "release", "edit", tag, "-R", repo, "--prerelease=false", "--latest")
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
