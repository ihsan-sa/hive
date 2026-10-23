package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Local is a folder-backed forge: bare repositories plus a JSON file of
// issues and pull requests. Tests and the release dry run use it so the full
// path (issue, branch, PR, pinned squash merge) runs without the network.
type Local struct {
	Dir string
	mu  sync.Mutex
}

type localState struct {
	Issues   map[string][]localIssue `json:"issues"`
	PRs      map[string][]localPR    `json:"prs"`
	Comments map[string][]string     `json:"comments"`
	Releases map[string][]Release    `json:"releases"`
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
	s := localState{Issues: map[string][]localIssue{}, PRs: map[string][]localPR{}, Comments: map[string][]string{}, Releases: map[string][]Release{}}
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

func (l *Local) EnsureRepo(repo, _ string) (bool, error) {
	p := l.RepoPath(repo)
	if Exists(p) {
		return false, nil
	}
	if err := os.MkdirAll(p, 0o755); err != nil {
		return false, err
	}
	_, err := Git(p, "init", "--bare", "-b", "main")
	return true, err
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

// Comments returns comments on an issue or PR (tests read them).
func (l *Local) Comments(repo string, number int) []string {
	return l.load().Comments[fmt.Sprintf("%s#%d", repo, number)]
}

func (l *Local) head(repo, branch string) string {
	sha, _ := Git(l.RepoPath(repo), "rev-parse", "refs/heads/"+branch)
	return sha
}

func (l *Local) refresh(repo string, p *localPR) {
	if p.State == "OPEN" {
		p.HeadSHA = l.head(repo, p.Head)
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
			return p.PR, nil
		}
	}
	return PR{}, ErrNotFound
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
		l.refresh(repo, p)
		if p.HeadSHA != sha {
			return "", fmt.Errorf("PR #%d head is %s, not the checked commit %s", number, short(p.HeadSHA), short(sha))
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

func (l *Local) CreateRelease(repo, tag, sha, notes string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.load()
	s.Releases[repo] = append(s.Releases[repo], Release{Tag: tag})
	l.save(s)
	return nil
}
