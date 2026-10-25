package core

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// Local is a folder-backed forge: bare repositories plus a JSON file of
// issues and pull requests. Tests and the release dry run use it so the full
// path (issue, branch, PR, pinned squash merge) runs without the network.
type Local struct {
	Dir string
	// UpToDate makes it want a branch up to date with its base before it
	// merges, as GitHub's branch rule of that name does (MergeState BEHIND).
	UpToDate bool
	mu       sync.Mutex
}

type localState struct {
	Issues   map[string][]localIssue  `json:"issues"`
	PRs      map[string][]localPR     `json:"prs"`
	Comments map[string][]string      `json:"comments"`
	Releases map[string][]Release     `json:"releases"`
	Runs     map[string][]WorkflowRun `json:"runs"`             // repo@sha -> release runs, newest first
	TagSHAs  map[string]string        `json:"tags"`             // repo@tag -> the commit it points at
	Checks   map[string][]CheckRun    `json:"checks,omitempty"` // repo@sha
	Jobs     map[string][]WorkflowJob `json:"jobs,omitempty"`   // repo#run -> its jobs
	Logs     map[string]string        `json:"logs,omitempty"`   // repo url -> a check's log
	Steps    map[string][]JobStep     `json:"steps,omitempty"`  // repo url -> a check's steps
	Reruns   map[string]int           `json:"reruns,omitempty"` // repo@sha -> re-runs of its failed checks asked
	Public   map[string]bool          `json:"public,omitempty"` // repos made public
}

type localIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"`
}

type localPR struct {
	PR
	Head  string `json:"head"`
	Base  string `json:"base"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

func (l *Local) Name() string          { return "local" }
func (l *Local) User() (string, error) { return "local", nil }

// RepoPath is the bare repository for repo.
func (l *Local) RepoPath(repo string) string {
	return filepath.Join(l.Dir, "repos", strings.ReplaceAll(repo, "/", "__")+".git")
}

func (l *Local) load() localState {
	s := localState{Issues: map[string][]localIssue{}, PRs: map[string][]localPR{}, Comments: map[string][]string{}, Releases: map[string][]Release{}, Runs: map[string][]WorkflowRun{}, TagSHAs: map[string]string{}}
	raw, err := os.ReadFile(filepath.Join(l.Dir, "state.json"))
	if err == nil {
		json.Unmarshal(raw, &s)
	}
	return s
}

func (l *Local) save(s localState) {
	raw, _ := json.MarshalIndent(s, "", " ")
	WriteFileAtomic(filepath.Join(l.Dir, "state.json"), raw, 0o644)
}

func (l *Local) EnsureRepo(repo, _ string, public bool) (bool, error) {
	p := l.RepoPath(repo)
	if Exists(p) {
		return false, nil
	}
	if err := os.MkdirAll(p, 0o755); err != nil {
		return false, err
	}
	if public {
		l.mu.Lock()
		s := l.load()
		if s.Public == nil {
			s.Public = map[string]bool{}
		}
		s.Public[repo] = true
		l.save(s)
		l.mu.Unlock()
	}
	if _, err := Git(p, "init", "--bare", "-b", "main"); err != nil {
		return true, err
	}
	return true, foreground(p)
}

// IsPublic says whether repo was made public.
func (l *Local) IsPublic(repo string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.load().Public[repo]
}

// foreground keeps a bare repo's auto maintenance after a push in the
// receiving git rather than a detached one: -c settings don't cross the
// local transport, and a detached gc outlives the push that started it.
func foreground(bare string) error {
	for _, key := range []string{"maintenance.autoDetach", "gc.autoDetach"} {
		if _, err := Git(bare, "config", key, "false"); err != nil {
			return err
		}
	}
	return nil
}

// Fork copies repo to owner's name, the local user's when owner is "".
func (l *Local) Fork(repo, owner string) (string, error) {
	if owner == "" {
		owner = "local"
	}
	fork := owner + "/" + repo[strings.LastIndex(repo, "/")+1:]
	if Exists(l.RepoPath(fork)) {
		return fork, nil
	}
	if _, err := Git("", "clone", "-q", "--bare", l.RepoPath(repo), l.RepoPath(fork)); err != nil {
		return fork, err
	}
	return fork, foreground(l.RepoPath(fork))
}

func (l *Local) Clone(repo, dir string) error {
	_, err := Git("", "clone", l.RepoPath(repo), dir)
	return err
}

func (l *Local) DefaultBranch(string) string { return "main" }

// IsEmpty reports whether the bare repository has no refs.
func (l *Local) IsEmpty(repo string) (bool, error) {
	out, err := Git(l.RepoPath(repo), "for-each-ref", "--count=1")
	return strings.TrimSpace(out) == "", err
}

func (l *Local) CreateIssue(repo, title, body, marker string) (Issue, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.load()
	for _, i := range s.Issues[repo] {
		if strings.Contains(i.Body, marker) {
			return Issue{i.Number, fmt.Sprintf("local://%s/issues/%d", repo, i.Number)}, nil
		}
	}
	n := len(s.Issues[repo]) + len(s.PRs[repo]) + 1
	s.Issues[repo] = append(s.Issues[repo], localIssue{Number: n, Title: title, Body: body + "\n\n" + marker, State: "OPEN"})
	l.save(s)
	return Issue{n, fmt.Sprintf("local://%s/issues/%d", repo, n)}, nil
}

func (l *Local) Comment(repo string, number int, body string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.load()
	k := fmt.Sprintf("%s#%d", repo, number)
	s.Comments[k] = append(s.Comments[k], body)
	l.save(s)
	return nil
}

func (l *Local) StatusComment(repo string, number int, marker, body string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.load()
	k := fmt.Sprintf("%s#%d", repo, number)
	body += "\n\n" + marker
	if i := slices.IndexFunc(s.Comments[k], func(c string) bool { return strings.Contains(c, marker) }); i >= 0 {
		s.Comments[k][i] = body
	} else {
		s.Comments[k] = append(s.Comments[k], body)
	}
	l.save(s)
	return nil
}

// Comments returns comments on an issue or PR (tests read them).
func (l *Local) Comments(repo string, number int) []string {
	return l.load().Comments[fmt.Sprintf("%s#%d", repo, number)]
}

func (l *Local) head(repo, branch string) string {
	sha, _ := Git(l.RepoPath(repo), "rev-parse", "refs/heads/"+branch)
	return sha
}

func (l *Local) refresh(repo string, p *localPR) {
	if p.State != "OPEN" {
		return
	}
	p.HeadSHA = l.head(repo, p.Head)
	dir, base := l.RepoPath(repo), "refs/heads/"+cmp.Or(p.Base, "main")
	p.Mergeable, p.MergeState = "MERGEABLE", "CLEAN"
	// merge-tree exits 1 on a conflict; any other failure (an older git) leaves
	// it to the merge itself to refuse.
	if _, err := Git(dir, "merge-tree", "--write-tree", base, p.HeadSHA); err != nil && strings.Contains(err.Error(), "exit status 1:") {
		p.Mergeable, p.MergeState = "CONFLICTING", "DIRTY"
	} else if _, err := Git(dir, "merge-base", "--is-ancestor", base, p.HeadSHA); err != nil && l.UpToDate {
		p.MergeState = "BEHIND"
	}
}

func (l *Local) FindPR(repo, head string) (PR, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.load()
	for i := range s.PRs[repo] {
		p := &s.PRs[repo][i]
		if p.Head == head && p.State != "CLOSED" {
			l.refresh(repo, p)
			return p.PR, true, nil
		}
	}
	return PR{}, false, nil
}

func (l *Local) CreatePR(repo, head, base, title, body string) (PR, error) {
	if p, ok, _ := l.FindPR(repo, head); ok {
		return p, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.load()
	n := len(s.Issues[repo]) + len(s.PRs[repo]) + 1
	p := localPR{PR: PR{Number: n, URL: fmt.Sprintf("local://%s/pull/%d", repo, n), State: "OPEN", HeadSHA: l.head(repo, head)}, Head: head, Base: base, Title: title, Body: body}
	s.PRs[repo] = append(s.PRs[repo], p)
	l.save(s)
	return p.PR, nil
}

func (l *Local) GetPR(repo string, number int) (PR, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.load()
	for i := range s.PRs[repo] {
		p := &s.PRs[repo][i]
		if p.Number == number {
			l.refresh(repo, p)
			out := p.PR
			out.Body = p.Body
			return out, nil
		}
	}
	return PR{}, ErrNotFound
}

// SetDraft marks a PR a draft or ready, as a reader outside the Hive would.
func (l *Local) SetDraft(repo string, number int, draft bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.load()
	for i := range s.PRs[repo] {
		if p := &s.PRs[repo][i]; p.Number == number {
			p.Draft = draft
			l.save(s)
			return nil
		}
	}
	return ErrNotFound
}

// ReadyPR takes a draft out of draft while its head is still sha.
func (l *Local) ReadyPR(repo string, number int, sha string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.load()
	for i := range s.PRs[repo] {
		p := &s.PRs[repo][i]
		if p.Number != number {
			continue
		}
		if p.State != "OPEN" {
			return fmt.Errorf("PR #%d is %s, not open", number, strings.ToLower(p.State))
		}
		l.refresh(repo, p)
		if p.HeadSHA != sha {
			return fmt.Errorf("PR #%d head is %s, not the checked commit %s", number, short(p.HeadSHA), short(sha))
		}
		if p.Draft {
			p.Draft = false
			l.save(s)
		}
		return nil
	}
	return ErrNotFound
}

// MergePR squashes the PR's head onto main, refusing if the head moved.
func (l *Local) MergePR(repo string, number int, sha, subject string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.load()
	for i := range s.PRs[repo] {
		p := &s.PRs[repo][i]
		if p.Number != number {
			continue
		}
		if p.State == "MERGED" {
			return p.MergeSHA, nil
		}
		if p.State == "CLOSED" {
			return "", fmt.Errorf("PR #%d was closed without merging", number)
		}
		l.refresh(repo, p)
		if p.HeadSHA != sha {
			return "", fmt.Errorf("PR #%d head is %s, not the checked commit %s", number, short(p.HeadSHA), short(sha))
		}
		if p.Draft {
			return "", fmt.Errorf("PR #%d: %w", number, ErrDraft)
		}
		tmp, err := os.MkdirTemp("", "hive-localmerge-")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(tmp)
		if _, err := Git("", "clone", "-q", l.RepoPath(repo), tmp); err != nil {
			return "", err
		}
		if _, err := Git(tmp, "fetch", "-q", "origin", sha); err != nil {
			return "", err
		}
		if _, err := Git(tmp, "merge", "--squash", sha); err != nil {
			return "", fmt.Errorf("conflict: %v", err)
		}
		if _, err := Git(tmp, "commit", "-q", "--allow-empty", "-m", fmt.Sprintf("%s (#%d)", subject, number)); err != nil {
			return "", err
		}
		if _, err := Git(tmp, "push", "-q", "origin", "HEAD:main"); err != nil {
			return "", err
		}
		m, _ := Git(tmp, "rev-parse", "HEAD")
		p.State, p.MergeSHA = "MERGED", m
		l.save(s)
		return m, nil
	}
	return "", ErrNotFound
}

func (l *Local) ClosePR(repo string, number int, comment string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.load()
	for i := range s.PRs[repo] {
		if s.PRs[repo][i].Number == number {
			s.PRs[repo][i].State = "CLOSED"
		}
	}
	k := fmt.Sprintf("%s#%d", repo, number)
	s.Comments[k] = append(s.Comments[k], comment)
	l.save(s)
	return nil
}

func (l *Local) DeleteBranch(repo, branch string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err := Git(l.RepoPath(repo), "update-ref", "-d", "refs/heads/"+branch)
	return err
}

func (l *Local) CloseIssue(repo string, number int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.load()
	for i := range s.Issues[repo] {
		if s.Issues[repo][i].Number == number {
			s.Issues[repo][i].State = "CLOSED"
		}
	}
	l.save(s)
	return nil
}

// PRs lists a repo's pull requests (tests read them).
func (l *Local) PRs(repo string) []PR {
	var out []PR
	for _, p := range l.load().PRs[repo] {
		out = append(out, p.PR)
	}
	return out
}

// Issues counts a repo's issues (tests read them).
func (l *Local) Issues(repo string) int { return len(l.load().Issues[repo]) }

func (l *Local) LatestRelease(repo string) (Release, error) {
	rs := l.load().Releases[repo]
	if len(rs) == 0 {
		return Release{}, ErrNotFound
	}
	return rs[len(rs)-1], nil
}

func (l *Local) Tags(repo string) ([]string, error) {
	var tags []string
	s := l.load()
	for _, r := range s.Releases[repo] {
		tags = append(tags, r.Tag)
	}
	for k := range s.TagSHAs {
		if t, ok := strings.CutPrefix(k, repo+"@"); ok && !slices.Contains(tags, t) {
			tags = append(tags, t)
		}
	}
	return tags, nil
}

// AddTag makes a bare tag at sha, as a push outside the Hive would (tests add them).
func (l *Local) AddTag(repo, tag, sha string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.load()
	s.TagSHAs[repo+"@"+tag] = sha
	l.save(s)
}

func (l *Local) TagCommit(repo, tag string) (string, error) {
	if sha, ok := l.load().TagSHAs[repo+"@"+tag]; ok {
		return sha, nil
	}
	return "", ErrNotFound
}

// AddRelease records a release as made outside the Hive, such as the person
// marking an older one the latest (tests add them); it becomes the latest.
func (l *Local) AddRelease(repo string, r Release) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.load()
	s.Releases[repo] = append(s.Releases[repo], r)
	l.save(s)
}

func (l *Local) CreateRelease(repo, tag, sha, notes string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.load()
	s.Releases[repo] = append(s.Releases[repo], Release{Tag: tag, Prerelease: true})
	if _, ok := s.TagSHAs[repo+"@"+tag]; !ok { // as on GitHub, a tag already there keeps its commit
		s.TagSHAs[repo+"@"+tag] = sha
	}
	l.save(s)
	return nil
}

// Checks are the checks set on repo@sha; a local forge runs none of its own.
func (l *Local) Checks(repo, sha string) ([]CheckRun, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.load().Checks[repo+"@"+sha], nil
}

// CheckLog is the log set on a check's URL.
func (l *Local) CheckLog(repo string, r CheckRun) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	log, ok := l.load().Logs[repo+" "+r.URL]
	if !ok {
		return "", fmt.Errorf("no log for %s", r.Name)
	}
	return log, nil
}

// SetCheckLog sets the log of the check at url, as a CI would keep it.
func (l *Local) SetCheckLog(repo, url, log string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.load()
	if s.Logs == nil {
		s.Logs = map[string]string{}
	}
	s.Logs[repo+" "+url] = log
	l.save(s)
}

// CheckSteps are the steps set on a check's URL.
func (l *Local) CheckSteps(repo string, r CheckRun) ([]JobStep, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	steps, ok := l.load().Steps[repo+" "+r.URL]
	if !ok {
		return nil, fmt.Errorf("no steps for %s", r.Name)
	}
	return steps, nil
}

// SetCheckSteps sets the steps of the check at url, as a CI would list them.
func (l *Local) SetCheckSteps(repo, url string, steps ...JobStep) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.load()
	if s.Steps == nil {
		s.Steps = map[string][]JobStep{}
	}
	s.Steps[repo+" "+url] = steps
	l.save(s)
}

// RerunFailed sets each failed or cancelled check on repo@sha back to
// pending, as a CI starting them again would, and counts it in Reruns. Like
// GitHub, it leaves a check that never started (a startup failure, one
// awaiting approval) alone.
func (l *Local) RerunFailed(repo, sha string) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.load()
	n := 0
	for i, c := range s.Checks[repo+"@"+sha] {
		if c.State == CheckFail || c.State == CheckCantStart && c.Detail == "cancelled" {
			s.Checks[repo+"@"+sha][i].State, s.Checks[repo+"@"+sha][i].Detail = CheckPending, "re-run"
			n++
		}
	}
	if n > 0 {
		if s.Reruns == nil {
			s.Reruns = map[string]int{}
		}
		s.Reruns[repo+"@"+sha]++
		l.save(s)
	}
	return n, nil
}

// Reruns counts the re-runs asked on repo@sha.
func (l *Local) Reruns(repo, sha string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.load().Reruns[repo+"@"+sha]
}

// SetChecks sets the forge's checks on repo@sha, as a CI would report them.
func (l *Local) SetChecks(repo, sha string, runs ...CheckRun) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.load()
	if s.Checks == nil {
		s.Checks = map[string][]CheckRun{}
	}
	s.Checks[repo+"@"+sha] = runs
	l.save(s)
}

func (l *Local) ReleaseRuns(repo, sha string) ([]WorkflowRun, error) {
	return l.load().Runs[repo+"@"+sha], nil
}

// SetReleaseRuns sets the runs a release at sha started (tests set them).
func (l *Local) SetReleaseRuns(repo, sha string, runs ...WorkflowRun) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.load()
	s.Runs[repo+"@"+sha] = runs
	l.save(s)
}

func (l *Local) Releases(repo string) ([]Release, error) {
	rels := slices.Clone(l.load().Releases[repo])
	slices.Reverse(rels)
	return rels, nil
}

func (l *Local) RunJobs(repo string, run int64) ([]WorkflowJob, error) {
	return l.load().Jobs[fmt.Sprintf("%s#%d", repo, run)], nil
}

// SetRunJobs sets one run's jobs (tests set them).
func (l *Local) SetRunJobs(repo string, run int64, jobs ...WorkflowJob) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.load()
	if s.Jobs == nil {
		s.Jobs = map[string][]WorkflowJob{}
	}
	s.Jobs[fmt.Sprintf("%s#%d", repo, run)] = jobs
	l.save(s)
}

func (l *Local) PublishRelease(repo, tag string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.load()
	for i, r := range s.Releases[repo] {
		if r.Tag == tag {
			s.Releases[repo][i].Prerelease = false
			l.save(s)
			return nil
		}
	}
	return ErrNotFound
}
