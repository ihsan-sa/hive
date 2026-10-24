package release

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
)

// The channels auto-update follows.
const (
	Latest     = "latest"
	Prerelease = "pre-release"
)

// Channel is the releases this install follows: the config's channel, or by
// default pre-releases on the Hive that releases the Hive (its own project is
// release_repo, as on H0) and the latest release everywhere else.
func (m *Module) Channel() string {
	cfg := m.C.WS.LoadConfig()
	if cfg.Channel != "" {
		return cfg.Channel
	}
	for _, p := range m.C.Projects.List() {
		if p.Self && strings.EqualFold(p.Repo, cfg.ReleaseRepo) {
			return Prerelease
		}
	}
	return Latest
}

// followEvery is how often a Hive following pre-releases looks for one: each
// look is a few calls on GitHub's API, which allows 60 an hour without a login
// (an answer unchanged since the last look, by its ETag, doesn't count).
const followEvery = 5 * time.Minute

// recheckRed is how long a pre-release whose own checks failed is passed over
// before they are read again, so a re-run that passes is still taken.
const recheckRed = time.Hour

// runners names this system in the gates' job names, as their matrix does:
// "test (ubuntu-latest)", "update (ubuntu-latest, previous)".
var runners = map[string]string{"linux": "ubuntu", "darwin": "macos", "windows": "windows"}

// ownJobs are, for each gate, the jobs that test one system: ci's test job and
// the update test's legs.
var ownJobs = map[string]string{"ci": "test", "update": "update"}

// follow runs AutoUpdate every followEvery while the Hive follows pre-releases,
// so one that is safe here installs within minutes, not at the next schedule.
func (m *Module) follow() {
	m.pmu.Lock()
	due := time.Since(m.followed) >= followEvery
	if due {
		m.followed = time.Now()
	}
	m.pmu.Unlock()
	if !due {
		return
	}
	if cfg := m.C.WS.LoadConfig(); !cfg.AutoUpdate || cfg.ReleasePin != "" || m.Channel() != Prerelease {
		return
	}
	go m.AutoUpdate()
}

// prerelease is the newest pre-release of repo newer than after whose checks
// for this system passed, or "" with what each newer one waits for.
func (m *Module) prerelease(repo, after string) (tag string, why []string, err error) {
	var rels []struct {
		Tag        string `json:"tag_name"`
		Prerelease bool
		Draft      bool
	}
	if err := apiJSON("/repos/"+repo+"/releases?per_page=30", &rels); err != nil {
		return "", nil, err
	}
	var tags []string
	for _, r := range rels {
		if r.Prerelease && !r.Draft && modules.Newer(after, r.Tag) {
			tags = append(tags, r.Tag)
		}
	}
	slices.SortFunc(tags, func(a, b string) int {
		if modules.Newer(a, b) {
			return 1
		}
		return -1
	})
	for i, t := range tags {
		if i == 3 {
			break // older ones wait for the newer to settle
		}
		state, w := m.ownGates(repo, t)
		if state == "passed" {
			if e := hasBinary(repo, t); e != nil {
				why = append(why, t+": "+e.Error())
				continue
			}
			return t, why, nil
		}
		why = append(why, t+": "+w)
	}
	return "", why, nil
}

// ownGates says "passed" once every gate's jobs for this system passed in
// the release's own runs, at its tag's commit, else "failed" or "waiting",
// and why. A red verdict is kept for recheckRed.
func (m *Module) ownGates(repo, tag string) (state, why string) {
	key := repo + "@" + tag
	m.pmu.Lock()
	red, ok := m.red[key]
	m.pmu.Unlock()
	if ok && time.Since(red.at) < recheckRed {
		return "failed", red.why
	}
	state, why = ownGates(repo, tag)
	if state == "failed" {
		m.pmu.Lock()
		if m.red == nil {
			m.red = map[string]verdict{}
		}
		m.red[key] = verdict{time.Now(), why}
		m.pmu.Unlock()
	}
	return state, why
}

type verdict struct {
	at  time.Time
	why string
}

func ownGates(repo, tag string) (state, why string) {
	sha, err := apiGet("/repos/"+repo+"/commits/"+tag, "application/vnd.github.sha")
	if err != nil {
		return "waiting", "can't read its commit (" + core.Clip(err.Error(), 200) + ")"
	}
	at := strings.TrimSpace(string(sha))
	var runs struct {
		Runs []run `json:"workflow_runs"`
	}
	if err := apiJSON("/repos/"+repo+"/actions/runs?event=release&per_page=100&head_sha="+at, &runs); err != nil {
		return "waiting", "can't read its runs (" + core.Clip(err.Error(), 200) + ")"
	}
	runner := runners[runtime.GOOS]
	var bad, wait []string
	for _, g := range Gates {
		i := slices.IndexFunc(runs.Runs, func(r run) bool { return r.Name == g && r.HeadSHA == at })
		if i < 0 {
			wait = append(wait, g+" (no run yet)")
			continue
		}
		var jobs struct {
			Jobs []struct {
				Name, Status, Conclusion string
				URL                      string `json:"html_url"`
			}
		}
		if err := apiJSON(fmt.Sprintf("/repos/%s/actions/runs/%d/jobs?per_page=100", repo, runs.Runs[i].ID), &jobs); err != nil {
			wait = append(wait, g+" (can't read its jobs)")
			continue
		}
		prefix, n := ownJobs[g]+" ("+runner, 0
		for _, j := range jobs.Jobs {
			if !strings.HasPrefix(j.Name, prefix) {
				continue
			}
			n++
			switch {
			case j.Status != "completed":
				wait = append(wait, j.Name)
			case j.Conclusion != "success":
				bad = append(bad, j.Name+" "+j.Conclusion+" ("+j.URL+")")
			}
		}
		if n == 0 {
			wait = append(wait, g+" (no "+runner+" job yet)")
		}
	}
	switch {
	case len(bad) > 0:
		return "failed", strings.Join(bad, ", ")
	case len(wait) > 0:
		return "waiting", "waiting for " + strings.Join(wait, ", ")
	}
	return "passed", ""
}

// run is a workflow run in the API's list, newest first.
type run struct {
	ID      int64
	Name    string
	HeadSHA string `json:"head_sha"`
}

var apiClient = &http.Client{Timeout: 30 * time.Second}

// cached holds each API answer by its ETag: asking again with it costs
// nothing of the hourly allowance when the answer hasn't changed.
var (
	cacheMu sync.Mutex
	cached  = map[string]etagged{}
)

type etagged struct {
	etag string
	body []byte
}

// apiGet reads a path of GitHub's API, without a login.
func apiGet(path, accept string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, API+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	key := accept + " " + req.URL.String()
	cacheMu.Lock()
	c, ok := cached[key]
	cacheMu.Unlock()
	if ok {
		req.Header.Set("If-None-Match", c.etag)
	}
	resp, err := apiClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if ok && resp.StatusCode == http.StatusNotModified {
		return c.body, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", path, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if etag := resp.Header.Get("ETag"); err == nil && etag != "" {
		cacheMu.Lock()
		if len(cached) > 500 {
			clear(cached)
		}
		cached[key] = etagged{etag, body}
		cacheMu.Unlock()
	}
	return body, err
}

func apiJSON(path string, v any) error {
	raw, err := apiGet(path, "application/vnd.github+json")
	if err == nil {
		err = json.Unmarshal(raw, v)
	}
	return err
}
