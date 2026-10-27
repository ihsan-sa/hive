package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	Mergeable string `json:"mergeable,omitempty"` // MERGEABLE, CONFLICTING, UNKNOWN (still being worked out)
	// MergeState is the forge's word on what else stands in the way; BEHIND
	// means its rules want the branch brought up to date with its base.
	MergeState string `json:"merge_state,omitempty"`
	// Draft is a PR that can't merge until marked ready: the Hive's merge
	// readies a draft once its own reads clear the head, unless the lead holds
	// it (pr_draft). Body is the PR's description.
	Draft bool   `json:"draft,omitempty"`
	Body  string `json:"-"`
}

// ErrDraft means the pull request is a draft, so it can't merge until it is
// marked ready. Its text is matched in stopped tasks' reasons; an older
// wording, "left for a read", is matched there too (work.stoppedOnDraft).
var ErrDraft = errors.New("the pull request is a draft, not yet marked ready")

// ErrHeadMoved means the pull request's head is no longer the commit the
// Hive read, so it isn't readied: the next look sees the new head.
var ErrHeadMoved = errors.New("its head moved past the commit read")

// Forge is where code, issues and pull requests live. GitHub through the gh
// CLI is the real one; Local is a folder-backed stand-in for tests.
type Forge interface {
	Name() string
	User() (string, error)
	// EnsureRepo makes repo, public or private, unless it exists; an
	// existing repository's visibility is never changed (that is the person's).
	EnsureRepo(repo, description string, public bool) (created bool, err error)
	// Fork is owner's fork of repo, made on first need; owner "" is the
	// person's own account, else an organization they can create repos in.
	Fork(repo, owner string) (string, error)
	Clone(repo, dir string) error
	IsEmpty(repo string) (bool, error)
	DefaultBranch(repo string) string
	CreateIssue(repo, title, body, marker string) (Issue, error)
	// EditIssue replaces an issue's title and body (a body keeps its marker
	// only if the caller writes it in again).
	EditIssue(repo string, number int, title, body string) error
	Comment(repo string, number int, body string) error
	// StatusComment keeps one comment per issue or PR that carries marker:
	// the first call posts it, later calls edit it in place.
	StatusComment(repo string, number int, marker, body string) error
	CreatePR(repo, head, base, title, body string) (PR, error)
	FindPR(repo, head string) (PR, bool, error)
	GetPR(repo string, number int) (PR, error)
	MergePR(repo string, number int, sha, subject string) (string, error)
	// ReadyPR takes a draft out of draft, only while its head is exactly
	// sha (else ErrHeadMoved); one already ready is left as it is, and
	// readied says whether this call changed anything.
	ReadyPR(repo string, number int, sha string) (readied bool, err error)
	ClosePR(repo string, number int, comment string) error
	// SetDraft marks an open pull request a draft or ready for review.
	SetDraft(repo string, number int, draft bool) error
	// DeleteBranch deletes a branch; one already gone counts as deleted.
	DeleteBranch(repo, branch string) error
	CloseIssue(repo string, number int) error // an issue already closed counts as closed
	// Checks are the forge's own checks (CI jobs) on exactly sha.
	Checks(repo, sha string) ([]CheckRun, error)
	// RerunFailed re-runs the failed jobs of every finished workflow run on
	// sha that failed, and says how many runs it started again.
	RerunFailed(repo, sha string) (int, error)
	// Releases (the Hive on itself). LatestRelease is the newest, a
	// pre-release included. CreateRelease cuts a pre-release, which installs
	// don't follow; PublishRelease makes it the latest once ReleaseRuns, the
	// workflow runs its publishing started at sha, pass. Tags are every
	// release's tag (pre-releases and drafts included) and every git tag, so
	// a new release can take a tag none of them has. TagCommit is the commit
	// a tag points at: a tag made between Tags and CreateRelease keeps its
	// own commit, so the release is published only if that is the one gated.
	LatestRelease(repo string) (Release, error)
	Tags(repo string) ([]string, error)
	CreateRelease(repo, tag, sha, notes string) error
	TagCommit(repo, tag string) (string, error)
	ReleaseRuns(repo, sha string) ([]WorkflowRun, error)
	PublishRelease(repo, tag string) error
	// Releases are the newest releases, pre-releases included and drafts
	// not, newest first; RunJobs are one workflow run's jobs. An install
	// following pre-releases reads them to update once its own system's
	// jobs of a pre-release's gates pass.
	Releases(repo string) ([]Release, error)
	RunJobs(repo string, run int64) ([]WorkflowJob, error)
	// File is one file on repo's default branch; ErrNotFound when it has
	// none (the release repository's steward record).
	File(repo, path string) (string, error)
}

// CheckRun is one of the forge's own checks on a commit, such as a CI job.
type CheckRun struct {
	Name   string `json:"name"`
	State  string `json:"state"` // pending | pass | fail | cantstart
	Detail string `json:"detail,omitempty"`
	URL    string `json:"url,omitempty"`
}

// Check states. CantStart is a check that never ran the commit (no runner, a
// startup failure, cancelled, stuck in the queue): the environment's fault,
// never a verdict on the commit.
const (
	CheckPending   = "pending"
	CheckPass      = "pass"
	CheckFail      = "fail"
	CheckCantStart = "cantstart"
	CheckSkipped   = "skipped" // ran nothing on the commit: neutral or skipped
)

// Release is a version on the forge.
type Release struct {
	Tag        string   `json:"tag"`
	Assets     []string `json:"assets"`
	Prerelease bool     `json:"prerelease,omitempty"`
}

// WorkflowRun is one run of a CI workflow, newest first in a list.
type WorkflowRun struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`     // queued, in_progress, completed
	Conclusion string `json:"conclusion"` // success, failure, cancelled... once completed
	URL        string `json:"html_url"`
}

// WorkflowJob is one job of a workflow run, such as "test (ubuntu-latest)".
type WorkflowJob struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	URL        string `json:"html_url"`
}

// GitHub is the real forge, driven through the person's own gh login.
type GitHub struct{}

func (GitHub) Name() string { return "github" }

// OwnerTokens maps a lower-cased repo owner to a file holding the GitHub token
// for that owner. An owner not listed (or whose file is unreadable, said once
// on stderr, which is daemon.log.err) uses gh's own login. Both tokens act as
// the same user, so gh cannot hold them as two logins; the effects process
// passes GH_TOKEN per call instead.
var OwnerTokens = map[string]string{
	"hive-rsi-lab": "/home/hive/h0-org.token",
}

// ForgeEnv is ghEnv for a module's git call to repo's remote in the host
// clone, which never reaches a run.
func ForgeEnv(repo string) []string { return ghEnv(repo) }

// ghEnv is the environment that makes gh and git's gh credential helper use
// the token configured for repo's owner, or nothing to keep gh's login. Under
// the system service install.sh writes, the token comes first from systemd's
// credential <owner>.token (root's file, which only the service can read), then
// from OwnerTokens; an empty one keeps gh's login.
func ghEnv(repo string) []string {
	owner, _, _ := strings.Cut(strings.ToLower(repo), "/")
	if owner == "" || strings.ContainsAny(owner, `/\.`) {
		return nil
	}
	for i, path := range []string{credential(owner + ".token"), OwnerTokens[owner]} {
		if path == "" {
			continue
		}
		b, err := os.ReadFile(path)
		if err == nil {
			if i == 1 { // readable again: a later break is said again
				unreadTokens.Delete(owner + "\x00" + path)
			}
			if tok := strings.TrimSpace(string(b)); tok != "" {
				return []string{"GH_TOKEN=" + tok}
			}
		} else if i == 1 { // a listed file, unlike a credential, is meant to be there
			if _, said := unreadTokens.LoadOrStore(owner+"\x00"+path, true); !said {
				fmt.Fprintf(tokenLog, "%s token file %s for %s can't be read, using gh's own login: %v\n", time.Now().Format(time.RFC3339), path, owner, err)
			}
		}
	}
	return nil
}

// unreadTokens holds each owner and listed token file already said to be
// unreadable, so the fallback is said once while it lasts, not on every gh call.
var unreadTokens sync.Map

// tokenLog is where that is said. Tests replace it.
var tokenLog io.Writer = os.Stderr

// credential is the path of a systemd credential the service was given, or
// "" outside one.
func credential(name string) string {
	dir := os.Getenv("CREDENTIALS_DIRECTORY")
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, name)
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
		} else if TimedOut(err.Error()) {
			stderr += ": " + err.Error()
		}
		return out, fmt.Errorf("gh %s: %s", ghVerb(args), stderr)
	}
	if write {
		ghPause.clear()
	}
	return out, nil
}

// ghVerb names a gh command by its leading words, such as "pr create" or
// "api repos/o/r/issues/3/comments". --paginate takes no value, so the path
// after it still names a list read.
func ghVerb(args []string) string {
	var w []string
	for _, a := range args {
		if a == "--paginate" {
			continue
		}
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
	return len(args) > 1 && slices.Contains([]string{"create", "comment", "merge", "close", "edit", "delete", "rerun", "ready"}, args[1])
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

var logins sync.Map // repo owner -> the login its token acts as

// ghLogin is the login the Hive acts as on repo, asked once per owner.
func ghLogin(repo string) (string, error) {
	owner, _, _ := strings.Cut(strings.ToLower(repo), "/")
	if l, ok := logins.Load(owner); ok {
		return l.(string), nil
	}
	l, err := gh(repo, "api", "user", "-q", ".login")
	if l = strings.TrimSpace(l); err != nil || l == "" {
		return "", fmt.Errorf("could not tell which login the Hive acts as on %s: %v", repo, err)
	}
	logins.Store(owner, l)
	return l, nil
}

// User is the login gh itself holds.
func (GitHub) User() (string, error) { return gh("", "api", "user", "-q", ".login") }

func (g GitHub) EnsureRepo(repo, description string, public bool) (bool, error) {
	if _, err := gh(repo, "repo", "view", repo, "--json", "name"); err == nil {
		return false, nil
	}
	visibility := "--private"
	if public {
		visibility = "--public"
	}
	if _, err := gh(repo, "repo", "create", repo, visibility, "--description", description); err != nil {
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

func (GitHub) EditIssue(repo string, number int, title, body string) error {
	_, err := gh(repo, "issue", "edit", strconv.Itoa(number), "-R", repo, "--title", title, "--body", body)
	return err
}

func (GitHub) Comment(repo string, number int, body string) error {
	_, err := gh(repo, "issue", "comment", strconv.Itoa(number), "-R", repo, "--body", body)
	return err
}

// StatusComment edits the Hive's own comment carrying marker, or posts one.
// Anyone can write the marker into a comment, so only one the Hive's login
// wrote is edited.
func (g GitHub) StatusComment(repo string, number int, marker, body string) error {
	body += "\n\n" + marker
	me, err := ghLogin(repo)
	if err != nil {
		return err
	}
	path := fmt.Sprintf("repos/%s/issues/%d/comments", repo, number)
	out, err := gh(repo, "api", "--paginate", path, "-q", fmt.Sprintf(`.[] | "\(.id) \(.user.login) \(.body | contains(%q))"`, marker))
	if err != nil {
		return err
	}
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) == 3 && f[1] == me && f[2] == "true" {
			_, err = gh(repo, "api", "-X", "PATCH", fmt.Sprintf("repos/%s/issues/comments/%s", repo, f[0]), "-f", "body="+body)
			return err
		}
	}
	return g.Comment(repo, number, body)
}

type ghPR struct {
	Number      int    `json:"number"`
	URL         string `json:"url"`
	State       string `json:"state"`
	HeadRefOid  string `json:"headRefOid"`
	Mergeable   string `json:"mergeable"`
	MergeState  string `json:"mergeStateStatus"`
	IsDraft     bool   `json:"isDraft"`
	Body        string `json:"body"`
	MergeCommit *struct {
		Oid string `json:"oid"`
	} `json:"mergeCommit"`
	HeadRepositoryOwner struct {
		Login string `json:"login"`
	} `json:"headRepositoryOwner"`
}

func (p ghPR) pr() PR {
	out := PR{Number: p.Number, URL: p.URL, State: p.State, HeadSHA: p.HeadRefOid, Mergeable: p.Mergeable, MergeState: p.MergeState, Draft: p.IsDraft, Body: p.Body}
	if p.MergeCommit != nil {
		out.MergeSHA = p.MergeCommit.Oid
	}
	return out
}

const prFields = "number,url,state,headRefOid,mergeable,mergeStateStatus,isDraft,body,mergeCommit,headRepositoryOwner"

// listFields is prFields less the description: a list brings every PR on the
// branch, and only GetPR's reader wants the body.
const listFields = "number,url,state,headRefOid,mergeable,mergeStateStatus,isDraft,mergeCommit,headRepositoryOwner"

func (g GitHub) FindPR(repo, head string) (PR, bool, error) {
	args := []string{"pr", "list", "-R", repo, "--head", head, "--state", "all", "--json", listFields}
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
	if p.State == "CLOSED" {
		return "", fmt.Errorf("PR #%d was closed without merging", number)
	}
	if p.HeadSHA != sha {
		return "", fmt.Errorf("PR #%d head is %s, not the checked commit %s", number, short(p.HeadSHA), short(sha))
	}
	if p.Draft {
		return "", fmt.Errorf("PR #%d: %w", number, ErrDraft)
	}
	if _, err := gh(repo, "pr", "merge", strconv.Itoa(number), "-R", repo, "--squash", "--match-head-commit", sha, "--subject", subject, "--body", g.squashBody(repo, number)); err != nil {
		if draftRefused.MatchString(err.Error()) { // made a draft since the look above
			return "", fmt.Errorf("PR #%d: %w (%v)", number, ErrDraft, err)
		}
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

// squashBody is the squash commit's body: the PR's commit messages, as
// GitHub's own default would give them, less every Co-authored-by trailer.
// Left to GitHub, the default turns each commit author and trailer into one,
// and an agent's "Co-Authored-By: Claude <noreply@anthropic.com>" lists the
// "claude" account as a contributor to the project.
func (GitHub) squashBody(repo string, number int) string {
	out, err := gh(repo, "pr", "view", strconv.Itoa(number), "-R", repo, "--json", "commits")
	if err != nil {
		return ""
	}
	var v struct {
		Commits []struct {
			Headline string `json:"messageHeadline"`
			Body     string `json:"messageBody"`
		} `json:"commits"`
	}
	if json.Unmarshal([]byte(out), &v) != nil {
		return ""
	}
	if len(v.Commits) == 1 {
		return SquashBody([]string{v.Commits[0].Body})
	}
	var parts []string
	for _, c := range v.Commits {
		parts = append(parts, "* "+c.Headline+"\n\n"+c.Body)
	}
	return SquashBody(parts)
}

var coAuthor = regexp.MustCompile(`(?im)^co-authored-by:.*$\n?`)

// SquashBody joins commit messages into a squash body with no
// Co-authored-by trailer, so nobody but the Hive is credited.
func SquashBody(messages []string) string {
	var parts []string
	for _, m := range messages {
		if m = strings.TrimSpace(coAuthor.ReplaceAllString(m, "")); m != "" {
			parts = append(parts, m)
		}
	}
	return strings.Join(parts, "\n\n")
}

// ReadyPR marks a draft ready for review with the token for repo's owner,
// refusing once its head moved past sha. gh can't bind `pr ready` to a head,
// so the head is looked at again after: one that moved in between is made a
// draft again and refused. The merge after it is bound to sha by
// --match-head-commit either way, so a push in that gap can't merge what
// wasn't read.
func (g GitHub) ReadyPR(repo string, number int, sha string) (bool, error) {
	p, err := g.GetPR(repo, number)
	if err != nil {
		return false, err
	}
	if p.State != "OPEN" {
		return false, fmt.Errorf("PR #%d is %s, not open", number, strings.ToLower(p.State))
	}
	if p.HeadSHA != sha {
		return false, fmt.Errorf("PR #%d head is %s, not the checked commit %s: %w", number, short(p.HeadSHA), short(sha), ErrHeadMoved)
	}
	if !p.Draft {
		return false, nil
	}
	if _, err := gh(repo, "pr", "ready", strconv.Itoa(number), "-R", repo); err != nil {
		return false, err
	}
	if p, err = g.GetPR(repo, number); err == nil && p.HeadSHA != sha {
		if err := g.SetDraft(repo, number, true); err != nil {
			return true, fmt.Errorf("PR #%d head moved to %s while it was readied at %s, and making it a draft again failed (%v): %w", number, short(p.HeadSHA), short(sha), err, ErrHeadMoved)
		}
		return false, fmt.Errorf("PR #%d head moved to %s while it was readied at %s, so it is a draft again: %w", number, short(p.HeadSHA), short(sha), ErrHeadMoved)
	}
	return true, nil
}

func (GitHub) ClosePR(repo string, number int, comment string) error {
	_, err := gh(repo, "pr", "close", strconv.Itoa(number), "-R", repo, "--comment", comment)
	return err
}

// draftRefused is how GitHub refuses to merge a draft ("Pull Request is
// still a draft (mergePullRequest)").
var draftRefused = regexp.MustCompile(`(?i)is still a draft`)

func (GitHub) SetDraft(repo string, number int, draft bool) error {
	args := []string{"pr", "ready", strconv.Itoa(number), "-R", repo}
	if draft {
		args = append(args, "--undo")
	}
	_, err := gh(repo, args...)
	return err
}

func (GitHub) DeleteBranch(repo, branch string) error {
	if err := HiveBranch(branch); err != nil {
		return err
	}
	_, err := gh(repo, "api", "-X", "DELETE", "repos/"+repo+"/git/refs/heads/"+branch)
	if err != nil && strings.Contains(err.Error(), "Reference does not exist") {
		return nil
	}
	return err
}

// hiveBranchName is what a branch the Hive made looks like: hive/ and then
// plain path segments.
var hiveBranchName = regexp.MustCompile(`^hive/[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)

// HiveBranch refuses a branch name that isn't plainly one of the Hive's own,
// so a forge never deletes another branch or builds an odd API path from it.
func HiveBranch(branch string) error {
	if !hiveBranchName.MatchString(branch) || strings.Contains(branch, "..") || strings.HasSuffix(branch, ".lock") {
		return fmt.Errorf("branch %q isn't one of the Hive's own (hive/...)", branch)
	}
	return nil
}

func (GitHub) CloseIssue(repo string, number int) error {
	_, err := gh(repo, "issue", "close", strconv.Itoa(number), "-R", repo)
	return err
}

// queuedTooLong is how long a check may wait for a runner before it counts as
// one that can't start.
const queuedTooLong = time.Hour

// Checks lists GitHub's check runs and commit statuses on sha. A check suite
// with no runs yet counts as pending, so a just-pushed head whose jobs aren't
// listed yet doesn't read as having none.
//
// A token that may not read checks (a 403) on a head with no workflows reads
// as no checks: there is no CI there to wait for, so the change goes on by
// the Hive's own tests, as on a project with no forge checks at all.
func (GitHub) Checks(repo, sha string) ([]CheckRun, error) {
	none := ciless(repo, sha)
	runs, err := ghPages[ghCheckRun](repo, "repos/"+repo+"/commits/"+sha+"/check-runs", "check_runs")
	if err = none(err); err != nil {
		return nil, err
	}
	// A check run again is listed once per attempt; the newest one counts. A
	// re-run still queued may have no start yet, so its larger id decides.
	newest := map[string]ghCheckRun{}
	var order []string
	for _, r := range runs {
		old, seen := newest[r.Name]
		if !seen {
			order = append(order, r.Name)
		}
		if !seen || r.ID > old.ID || r.ID == old.ID && r.StartedAt.After(old.StartedAt) {
			newest[r.Name] = r
		}
	}
	var list []CheckRun
	for _, name := range order {
		list = append(list, newest[name].check(time.Now()))
	}
	statuses, err := ghPages[struct {
		Context     string `json:"context"`
		State       string `json:"state"`
		Description string `json:"description"`
		TargetURL   string `json:"target_url"`
	}](repo, "repos/"+repo+"/commits/"+sha+"/status", "statuses")
	if err = none(err); err != nil {
		return nil, err
	}
	for _, s := range statuses {
		state := map[string]string{"success": CheckPass, "failure": CheckFail, "error": CheckCantStart}[s.State]
		if state == "" {
			state = CheckPending
		}
		list = append(list, CheckRun{Name: s.Context, State: state, Detail: s.Description, URL: s.TargetURL})
	}
	suites, err := ghPages[ghSuite](repo, "repos/"+repo+"/commits/"+sha+"/check-suites", "check_suites")
	if err = none(err); err != nil {
		return nil, err
	}
	for _, s := range suites {
		if s.pending(time.Now()) {
			list = append(list, CheckRun{Name: s.App.Slug, State: CheckPending, Detail: "suite " + s.Status + ", no jobs listed yet"})
		}
	}
	return list, nil
}

// checksRefused is a forge refusing the token a read of a head's checks, as
// gh words it: "gh api repos/o/r/commits/<sha>/check-runs?per_page=100: gh:
// Resource not accessible by personal access token (HTTP 403)".
var checksRefused = regexp.MustCompile(`^gh api repos/\S+/commits/[0-9a-f]+/(check-runs|check-suites|status)\S*: .*\(HTTP 403\)$`)

// ChecksRefused reports whether err's text is GitHub.Checks refused the
// token's read of a head's checks.
func ChecksRefused(text string) bool { return checksRefused.MatchString(text) }

// ciless returns a filter for the errors of reading sha's checks: a refusal
// (HTTP 403) is dropped when sha has no .github/workflows (GitHub answers 404
// for it), asked only on a refusal and remembered per head, since a commit's
// tree never changes; any other error stands. A rate limit's 403 is no
// refusal. A 404 can also mean a private repo the token can't see, but the
// 403 on its checks already says the token sees the repo.
//
// A head with no workflows can still carry statuses or check runs from an
// outside app (CircleCI, a GitHub App); a refusal hides those, so such a repo
// reads as having no checks (docs/risk.md).
func ciless(repo, sha string) func(error) error {
	return func(err error) error {
		var rl *RateLimited
		if err == nil || errors.As(err, &rl) || !strings.Contains(err.Error(), "HTTP 403") {
			return err
		}
		if noWorkflows(repo, sha) {
			return nil
		}
		return err
	}
}

// workflowless remembers, by repo@sha, whether a head was found to have no
// .github/workflows; only a definite answer (a 404 or a listing) is kept.
var workflowless = struct {
	sync.Mutex
	m map[string]bool
}{m: map[string]bool{}}

func noWorkflows(repo, sha string) bool {
	key := repo + "@" + sha
	workflowless.Lock()
	none, known := workflowless.m[key]
	workflowless.Unlock()
	if known {
		return none
	}
	_, err := gh(repo, "api", "repos/"+repo+"/contents/.github/workflows?ref="+sha)
	none = err != nil && strings.Contains(err.Error(), "HTTP 404")
	if err == nil || none {
		workflowless.Lock()
		if len(workflowless.m) >= 1024 {
			clear(workflowless.m)
		}
		workflowless.m[key] = none
		workflowless.Unlock()
	}
	return none
}

// ghPages reads every page of a GitHub list held under field of each page's
// object (check-runs, statuses and check-suites come 100 a page at most), so
// a head with more checks than one page holds still shows them all. gh prints
// one item per jq result, compact or indented; the decoder takes either.
func ghPages[T any](repo, path, field string) ([]T, error) {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	out, err := gh(repo, "api", "--paginate", path+sep+"per_page=100", "-q", "."+field+"[]")
	if err != nil {
		return nil, err
	}
	var list []T
	for dec := json.NewDecoder(strings.NewReader(out)); ; {
		var item T
		if err := dec.Decode(&item); err == io.EOF {
			return list, nil
		} else if err != nil {
			return nil, fmt.Errorf("reading %s: %w", field, err)
		}
		list = append(list, item)
	}
}

// CheckLogs is a forge that can read a check's log, so a check red on both a
// head and main can be told apart by the tests failing in it.
type CheckLogs interface {
	CheckLog(repo string, r CheckRun) (string, error)
}

// JobStep is one step of a CI job and how it ended: success, failure,
// skipped, cancelled.
type JobStep struct {
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
}

// CheckSteps is a forge that can list a check's steps, so a step a failure
// kept from running can be told from one that never runs on that job.
type CheckSteps interface {
	CheckSteps(repo string, r CheckRun) ([]JobStep, error)
}

// CheckSteps lists a GitHub Actions job's steps.
func (GitHub) CheckSteps(repo string, r CheckRun) ([]JobStep, error) {
	m := ghJobURL.FindStringSubmatch(r.URL)
	if m == nil {
		return nil, fmt.Errorf("%s is not a GitHub Actions job", r.Name)
	}
	out, err := gh(repo, "api", "repos/"+repo+"/actions/jobs/"+m[1], "-q", ".steps")
	if err != nil {
		return nil, err
	}
	var steps []JobStep
	return steps, json.Unmarshal([]byte(out), &steps)
}

// APIReader is a forge that answers read-only API calls on the person's
// login (5,000 an hour), so neither the daemon nor agents spend the anonymous
// 60 an hour this machine's address shares. RateReset is when the login's
// quota is next full again, or the zero time while it isn't spent.
type APIReader interface {
	APIGet(repo, path string) (string, error)
	RateReset(repo string) (time.Time, error)
}

// APIGet reads one API path with gh: a GET, never a write.
func (GitHub) APIGet(repo, path string) (string, error) {
	if strings.HasPrefix(path, "-") {
		return "", fmt.Errorf("bad API path %q", path)
	}
	return gh(repo, "api", path)
}

// RateReset reads the login's core quota; GitHub doesn't count that read.
// While calls are left (a secondary limit, not the hour's quota) it is the
// zero time: nothing to wait for past what GitHub itself asked.
func (GitHub) RateReset(repo string) (time.Time, error) {
	out, err := gh(repo, "api", "rate_limit", "-q", `.resources.core | "\(.remaining) \(.reset)"`)
	if err != nil {
		return time.Time{}, err
	}
	var left, n int64
	if _, err := fmt.Sscan(out, &left, &n); err != nil {
		return time.Time{}, fmt.Errorf("reading the rate limit's reset: %w", err)
	}
	if left > 0 {
		return time.Time{}, nil
	}
	return time.Unix(n, 0), nil
}

var (
	ghJobURL   = regexp.MustCompile(`/actions/runs/\d+/job/(\d+)`)
	ghLogStamp = regexp.MustCompile(`(?m)^\d{4}-\d\d-\d\dT[0-9:.]+Z `)
)

// CheckLog reads a GitHub Actions job's log, the timestamp GitHub puts on
// each line taken off. Only an Actions job has one.
func (GitHub) CheckLog(repo string, r CheckRun) (string, error) {
	m := ghJobURL.FindStringSubmatch(r.URL)
	if m == nil {
		return "", fmt.Errorf("%s is not a GitHub Actions job", r.Name)
	}
	out, err := gh(repo, "api", "repos/"+repo+"/actions/jobs/"+m[1]+"/logs")
	if err != nil {
		return "", err
	}
	return ghLogStamp.ReplaceAllString(out, ""), nil
}

type ghSuite struct {
	Status    string    `json:"status"`
	Runs      int       `json:"latest_check_runs_count"`
	CreatedAt time.Time `json:"created_at"`
	App       struct {
		Slug string `json:"slug"`
	} `json:"app"`
}

// suiteGrace is how long a just-pushed head's Actions suite with no jobs
// listed yet holds the merge.
const suiteGrace = 10 * time.Minute

// pending reports whether a suite stands for jobs about to be listed: only
// GitHub Actions' own, and only while young. GitHub makes a suite on every
// push for each app that may write checks, and one no workflow matches (or
// an app that never posts runs) stays queued with no runs for good, so an
// old or foreign one holds nothing; required_checks names the bar.
func (s ghSuite) pending(now time.Time) bool {
	return s.Runs == 0 && s.Status != "completed" && s.App.Slug == "github-actions" &&
		!s.CreatedAt.IsZero() && now.Sub(s.CreatedAt) < suiteGrace
}

type ghCheckRun struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	StartedAt  time.Time `json:"started_at"`
	HTMLURL    string    `json:"html_url"`
}

// check maps a GitHub check run to a state: a failure or timeout is a verdict
// on the commit; a startup failure, a cancelled or stale run, one awaiting an
// action, or one queued past queuedTooLong never ran it.
func (r ghCheckRun) check(now time.Time) CheckRun {
	c := CheckRun{Name: r.Name, URL: r.HTMLURL, Detail: r.Status}
	if r.Status != "completed" {
		c.State = CheckPending
		if r.Status != "in_progress" && !r.StartedAt.IsZero() && now.Sub(r.StartedAt) > queuedTooLong {
			c.State, c.Detail = CheckCantStart, r.Status+" for over "+queuedTooLong.String()
		}
		return c
	}
	c.Detail = r.Conclusion
	switch r.Conclusion {
	case "success":
		c.State = CheckPass
	case "neutral", "skipped":
		c.State = CheckSkipped
	case "failure", "timed_out":
		c.State = CheckFail
	default: // startup_failure, cancelled, action_required, stale
		c.State = CheckCantStart
	}
	return c
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

func (GitHub) Tags(repo string) ([]string, error) {
	rels, err := gh(repo, "release", "list", "-R", repo, "--limit", "1000", "--json", "tagName", "-q", ".[].tagName")
	if err != nil {
		return nil, err
	}
	tags, err := gh(repo, "api", "repos/"+repo+"/tags?per_page=100", "--paginate", "-q", ".[].name")
	if err != nil {
		return nil, err
	}
	return strings.Fields(rels + "\n" + tags), nil
}

func (GitHub) CreateRelease(repo, tag, sha, notes string) error {
	_, err := gh(repo, "release", "create", tag, "-R", repo, "--target", sha, "--title", tag, "--notes", notes, "--prerelease")
	return err
}

// TagCommit peels an annotated tag to its commit. The ref is spelled in full:
// GitHub answers commits/tags/<tag> with a 422 (checked live on git/git's
// annotated v2.0.0, which commits/refs/tags/ peels to e156455).
func (GitHub) TagCommit(repo, tag string) (string, error) {
	return gh(repo, "api", "repos/"+repo+"/commits/refs/tags/"+tag, "-q", ".sha")
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

func (GitHub) File(repo, path string) (string, error) {
	out, err := gh(repo, "api", "repos/"+repo+"/contents/"+path, "-H", "Accept: application/vnd.github.raw")
	if err != nil && strings.Contains(err.Error(), "HTTP 404") {
		return "", ErrNotFound
	}
	return out, err
}

func (GitHub) Releases(repo string) ([]Release, error) {
	out, err := gh(repo, "release", "list", "-R", repo, "--exclude-drafts", "--limit", "30", "--json", "tagName,isPrerelease")
	if err != nil {
		return nil, err
	}
	var rs []struct {
		TagName      string `json:"tagName"`
		IsPrerelease bool   `json:"isPrerelease"`
	}
	if err := json.Unmarshal([]byte(out), &rs); err != nil {
		return nil, err
	}
	var rels []Release
	for _, r := range rs {
		rels = append(rels, Release{Tag: r.TagName, Prerelease: r.IsPrerelease})
	}
	return rels, nil
}

func (GitHub) RunJobs(repo string, run int64) ([]WorkflowJob, error) {
	out, err := gh(repo, "api", fmt.Sprintf("repos/%s/actions/runs/%d/jobs?per_page=100", repo, run), "-q", ".jobs")
	if err != nil {
		return nil, err
	}
	var jobs []WorkflowJob
	return jobs, json.Unmarshal([]byte(out), &jobs)
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

// RerunFailed re-runs the failed jobs (gh run rerun --failed) of each
// finished workflow run on sha that failed, timed out or was cancelled. A run
// still going, or one going again, is left alone, and so is one that never
// started (startup_failure, action_required): GitHub won't re-run its jobs,
// since there are none, and a broken workflow file fails the same way again.
func (GitHub) RerunFailed(repo, sha string) (int, error) {
	out, err := gh(repo, "api", "repos/"+repo+"/actions/runs?head_sha="+sha+"&per_page=100", "-q", ".workflow_runs")
	if err != nil {
		return 0, err
	}
	var runs []WorkflowRun
	if err := json.Unmarshal([]byte(out), &runs); err != nil {
		return 0, err
	}
	n := 0
	for _, r := range runs {
		if r.Status != "completed" || !slices.Contains([]string{"failure", "timed_out", "cancelled"}, r.Conclusion) {
			continue
		}
		if _, err := gh(repo, "run", "rerun", strconv.FormatInt(r.ID, 10), "--failed", "-R", repo); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
