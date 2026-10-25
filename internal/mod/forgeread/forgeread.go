// Package forgeread serves agents' reads of the forge's API (a pull request,
// its check runs, a check's annotations) on the person's login, cached and
// held to a budget, so agents stop spending the anonymous 60 calls an hour
// this machine's address shares with the daemon's own update checks.
package forgeread

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
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
}

// New registers the github op.
func New(c *core.Core) *Module {
	m := &Module{C: c, Now: time.Now, cache: map[string]answer{}}
	c.Runs.Ops().Register(core.Op{Name: "github", Help: `{"pr": N} the pull request, {"checks": sha} the check runs on a commit, {"annotations": check run id} a check's annotations, or {"path": "repos/owner/name/..."} any read under a project's repo; "repo" is your project's by default -> GitHub's JSON, read on the person's login and cached for a minute, so it spends nothing of the anonymous 60 calls an hour (don't curl api.github.com)`, Fn: m.op})
	return m
}

// path is safe characters only, under repos/owner/name, with no "..".
var pathRe = regexp.MustCompile(`^repos/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)(/[A-Za-z0-9_./-]*)?(\?[A-Za-z0-9_.=&,-]*)?$`)

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
	arg := func(v any) string { return strings.TrimSpace(fmt.Sprint(v)) }
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
	if g == nil || strings.Contains(path, "..") {
		return "", fmt.Errorf("%q is not a read under repos/owner/name/", path)
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
		return a.body, nil
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
		return "", err
	}
	if len(body) > most {
		body = body[:most] + "\n[cut at " + fmt.Sprint(most) + " bytes; ask for less with ?per_page=]"
	}
	m.mu.Lock()
	if len(m.cache) >= kept {
		for k, a := range m.cache {
			if now.Sub(a.at) >= fresh {
				delete(m.cache, k)
			}
		}
		if len(m.cache) >= kept {
			clear(m.cache)
		}
	}
	m.cache[path] = answer{now, body}
	m.mu.Unlock()
	return body, nil
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
