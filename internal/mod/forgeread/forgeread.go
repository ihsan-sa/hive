// Package forgeread serves agents' reads of the forge's API (a pull request,
// its check runs, a check's annotations) on the person's login, cached and
// held to a budget, so agents stop spending the anonymous 60 calls an hour
// this machine's address shares with the daemon's own update checks.
//
// The cache and the budget are held in memory and start afresh when the
// daemon restarts. The cache is keyed by path alone, shared across runs, so
// a run's reach (mayRead) is checked before the cache is.
package forgeread

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// fresh is how long a read is served from the cache: agents polling a PR's
// checks read GitHub once a minute between them, however many ask.
const fresh = time.Minute

// budget is the reads agents together may send to GitHub an hour, of the
// login's 5,000, so their reads never crowd out the Hive's own work.
const budget = 300

// most is the longest answer returned; a longer one is cut there.
const most = 256 << 10

// kept is the most answers the cache holds.
const kept = 500

// Module serves the github op.
type Module struct {
	C   *core.Core
	Now func() time.Time

	mu    sync.Mutex
	cache map[string]answer
	sent  []time.Time // reads sent to GitHub in the last hour
}

type answer struct {
	at   time.Time
	body string
	err  error // GitHub's refusal (a 404, a 403), kept as long as an answer
}

// New registers the github op.
func New(c *core.Core) *Module {
	m := &Module{C: c, Now: time.Now, cache: map[string]answer{}}
	c.Runs.Ops().Register(core.Op{Name: "github", Help: `{"pr": N} the pull request, {"checks": sha} the check runs on a commit, {"annotations": check run id} a check's annotations, or {"path": "repos/owner/name/..."} a read under a project's repo of its pulls, issues, commits and their checks, check-runs, check-suites, actions runs and jobs, releases, contents or git objects; "repo" is your project's by default -> GitHub's JSON, read on the person's login and cached for a minute, so it spends nothing of the anonymous 60 calls an hour (don't curl api.github.com)`, Fn: m.op})
	return m
}

// path is safe characters only, under repos/owner/name; Read refuses "." and
// ".." segments.
var pathRe = regexp.MustCompile(`^repos/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)(/[A-Za-z0-9_./-]*)?(\?[A-Za-z0-9_.=&,-]*)?$`)

// readable are the reads under a repo an agent may ask for: pull requests,
// issues, commits and their checks, Actions runs and jobs, releases and the
// repo's contents. Anything else (hooks, whose URLs are credentials,
// variables, secrets, environments, keys, collaborators, invitations and
// every other admin read) is the person's token's alone.
var readable = regexp.MustCompile(`^(` +
	`|/pulls(/[0-9]+(/(files|reviews|comments|commits))?)?` +
	`|/issues(/[0-9]+(/comments)?)?` +
	`|/commits(/[A-Za-z0-9_.-]+(/(check-runs|check-suites|status|statuses))?)?` +
	`|/check-runs/[0-9]+(/annotations)?` +
	`|/check-suites/[0-9]+(/check-runs)?` +
	`|/actions/runs(/[0-9]+(/jobs)?)?` +
	`|/actions/jobs/[0-9]+(/logs)?` +
	`|/releases(/[0-9]+|/latest|/tags/[A-Za-z0-9_.-]+)?` +
	`|/contents(/[A-Za-z0-9_./-]*)?` +
	`|/git/(commits|trees|refs|tags)/[A-Za-z0-9_./-]+` +
	`)$`)

var (
	numRe = regexp.MustCompile(`^[0-9]{1,12}$`)
	shaRe = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)
)

func (m *Module) op(r core.Run, args json.RawMessage) (any, error) {
	var req struct {
		Repo, Path              string
		PR, Checks, Annotations any
	}
	if err := core.Bind(args, &req); err != nil {
		return nil, err
	}
	arg := func(v any) string {
		if f, ok := v.(float64); ok { // a JSON number: never "3e+10"
			return strconv.FormatFloat(f, 'f', -1, 64)
		}
		return strings.TrimSpace(fmt.Sprint(v))
	}
	repo := req.Repo
	if repo == "" {
		if p, ok := m.C.Projects.Get(r.Project); ok {
			repo = p.Repo
		}
	}
	path := strings.TrimPrefix(strings.TrimPrefix(req.Path, "https://api.github.com"), "/")
	if path == "" && repo == "" {
		return nil, errors.New(`name the "repo" (owner/name): you have no project of your own`)
	}
	switch {
	case path != "":
	case req.PR != nil && numRe.MatchString(arg(req.PR)):
		path = "repos/" + repo + "/pulls/" + arg(req.PR)
	case req.Checks != nil && shaRe.MatchString(arg(req.Checks)):
		path = "repos/" + repo + "/commits/" + arg(req.Checks) + "/check-runs?per_page=100"
	case req.Annotations != nil && numRe.MatchString(arg(req.Annotations)):
		path = "repos/" + repo + "/check-runs/" + arg(req.Annotations) + "/annotations?per_page=100"
	default:
		return nil, errors.New(`give "pr" (a number), "checks" (a commit sha), "annotations" (a check run id) or "path"`)
	}
	return m.Read(r, path)
}

// Read is one API path's answer for run r: from the cache while fresh, else
// read on the login, within the budget.
func (m *Module) Read(r core.Run, path string) (string, error) {
	g := pathRe.FindStringSubmatch(path)
	if g == nil || slices.ContainsFunc(strings.Split(strings.SplitN(path, "?", 2)[0], "/"), func(s string) bool { return s == "." || s == ".." }) {
		return "", fmt.Errorf("%q is not a read under repos/owner/name/", path)
	}
	if !readable.MatchString(g[2]) {
		return "", fmt.Errorf("%q is not a read agents may make: pull requests, issues, commits and their checks, Actions runs and jobs, releases and contents only", path)
	}
	repo := g[1]
	if !m.mayRead(r, repo) {
		return "", fmt.Errorf("%s is not a repository of a project you can see", repo)
	}
	reader, ok := m.C.Effects.Forge.(core.APIReader)
	if !ok {
		return "", fmt.Errorf("the %s forge has no API to read", m.C.Effects.Forge.Name())
	}
	now := m.Now()
	m.mu.Lock()
	if a, ok := m.cache[path]; ok && now.Sub(a.at) < fresh {
		m.mu.Unlock()
		return a.body, a.err
	}
	m.sent = slices.DeleteFunc(m.sent, func(t time.Time) bool { return now.Sub(t) >= time.Hour })
	if len(m.sent) >= budget {
		next := m.sent[0].Add(time.Hour)
		m.mu.Unlock()
		return "", fmt.Errorf("agents have read GitHub %d times this hour, the most they may; try again after %s", budget, next.UTC().Format("15:04Z"))
	}
	m.sent = append(m.sent, now)
	m.mu.Unlock()
	body, err := reader.APIGet(repo, path)
	if err != nil {
		var rl *core.RateLimited
		if errors.As(err, &rl) {
			return "", fmt.Errorf("GitHub's rate limit on the login: try again after %s (%s)", rl.Until.UTC().Format("15:04Z"), core.Clip(rl.Text, 200))
		}
		// A refusal (gh's "HTTP 404" and the like) is kept as an answer is,
		// so an agent asking again for a path that isn't there doesn't spend
		// the budget each time; a timeout or a fault on this side isn't.
		if strings.Contains(err.Error(), "HTTP 4") {
			m.keep(path, answer{now, "", err})
		}
		return "", err
	}
	if len(body) > most {
		body = body[:most] + "\n[cut at " + fmt.Sprint(most) + " bytes; ask for less with ?per_page=]"
	}
	m.keep(path, answer{now, body, nil})
	return body, nil
}

// keep caches one path's answer, making room when the cache is full.
func (m *Module) keep(path string, a answer) {
	now := a.at
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.cache) >= kept {
		for k, old := range m.cache {
			if now.Sub(old.at) >= fresh {
				delete(m.cache, k)
			}
		}
		if len(m.cache) >= kept {
			clear(m.cache)
		}
	}
	m.cache[path] = a
}

// mayRead says whether run r may read repo on the login: its own project's,
// or, from a project that isn't isolated, any shared project's or the Hive's
// releases'. A walled project's repo is its own runs' alone.
func (m *Module) mayRead(r core.Run, repo string) bool {
	own, _ := m.C.Projects.Get(r.Project)
	if own.Repo != "" && strings.EqualFold(own.Repo, repo) {
		return true
	}
	if own.Isolated {
		return false
	}
	for _, p := range m.C.Projects.List() {
		if strings.EqualFold(p.Repo, repo) {
			return p.Sharing() == core.SharingShared
		}
	}
	return strings.EqualFold(m.C.WS.LoadConfig().ReleaseRepo, repo)
}
