package release

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// gateAPI is GitHub's API for me/hive: its releases (pre is the
// pre-releases), each tag at a commit named for it, and the conclusion of
// each job of each tag's gate runs, "" while it runs.
type gateAPI struct {
	mu   sync.Mutex
	rels []string
	pre  map[string]bool
	jobs map[string]map[string]map[string]string // tag -> gate -> job -> conclusion
	runs []string                                // run id -> "tag gate"
}

// serveGates serves api in place of GitHub's API.
func serveGates(t *testing.T, api *gateAPI) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.mu.Lock()
		defer api.mu.Unlock()
		p := r.URL.Path
		switch {
		case p == "/repos/me/hive/releases":
			var out []map[string]any
			for _, tag := range api.rels {
				out = append(out, map[string]any{"tag_name": tag, "prerelease": api.pre[tag]})
			}
			json.NewEncoder(w).Encode(out)
		case strings.HasPrefix(p, "/repos/me/hive/commits/"):
			if r.Header.Get("Accept") != "application/vnd.github.sha" {
				http.Error(w, "want the sha", http.StatusBadRequest)
			}
			w.Write([]byte("sha-" + strings.TrimPrefix(p, "/repos/me/hive/commits/") + "\n"))
		case p == "/repos/me/hive/actions/runs":
			tag := strings.TrimPrefix(r.URL.Query().Get("head_sha"), "sha-")
			var runs []map[string]any
			for _, g := range Gates {
				if _, ok := api.jobs[tag][g]; ok && r.URL.Query().Get("event") == "release" {
					api.runs = append(api.runs, tag+" "+g)
					runs = append(runs, map[string]any{"id": len(api.runs) - 1, "name": g, "head_sha": "sha-" + tag})
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"workflow_runs": runs})
		case strings.HasPrefix(p, "/repos/me/hive/actions/runs/"):
			var id int
			fmt.Sscanf(strings.TrimPrefix(p, "/repos/me/hive/actions/runs/"), "%d/jobs", &id)
			tag, g, _ := strings.Cut(api.runs[id], " ")
			var out []map[string]any
			for name, c := range api.jobs[tag][g] {
				st := "completed"
				if c == "" {
					st = "in_progress"
				}
				out = append(out, map[string]any{"name": name, "status": st, "conclusion": c, "html_url": "https://example.com/" + name})
			}
			json.NewEncoder(w).Encode(map[string]any{"jobs": out})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	api0 := API
	t.Cleanup(func() { API = api0 })
	API = srv.URL
}

// gatesFor are a release's gate jobs, this system's concluding here and the
// others' there.
func gatesFor(here, there string) map[string]map[string]string {
	own, other := runners[runtime.GOOS], "plan9"
	return map[string]map[string]string{
		"ci": {"test (" + own + "-latest)": here, "test (" + other + "-latest)": there, "sandboxed-e2e": there},
		"update": {"update (" + own + "-latest, previous)": here, "update (" + own + "-latest, v0.1.2)": here,
			"update (" + other + "-latest, previous)": there},
	}
}

// preSite is v0.1.12 as the latest release and v0.1.13 to v0.1.15 as
// pre-releases: v0.1.13's checks passed here though red elsewhere, v0.1.14's
// failed here, and v0.1.15's still run here.
func preSite(t *testing.T) *gateAPI {
	serveAll(t, "v0.1.12", map[string]map[string]string{"v0.1.12": build("v12"), "v0.1.13": build("v13"),
		"v0.1.14": build("v14"), "v0.1.15": build("v15")}, false)
	api := &gateAPI{rels: []string{"v0.1.15", "v0.1.14", "v0.1.13", "v0.1.12"},
		pre: map[string]bool{"v0.1.13": true, "v0.1.14": true, "v0.1.15": true},
		jobs: map[string]map[string]map[string]string{
			"v0.1.13": gatesFor("success", "failure"),
			"v0.1.14": gatesFor("failure", "success"),
			"v0.1.15": gatesFor("", "success"),
		}}
	api.jobs["v0.1.14"]["update"]["update ("+runners[runtime.GOOS]+"-latest, v0.1.2)"] = "success" // one red leg is enough
	serveGates(t, api)
	return api
}

// The Hive that releases the Hive follows pre-releases by default: it takes
// the newest whose checks for its own system passed, passing over one red
// here and one still running here, and waits out a merge; once the newer one
// passes here it takes that too.
func TestFollowsTheNewestPreReleaseSafeHere(t *testing.T) {
	api := preSite(t)
	m, exe := releaseAt(t, "v0.1.12", "me/hive")
	if _, err := m.C.Projects.Add(core.Project{Name: "hive", Repo: "me/hive", Self: true}); err != nil {
		t.Fatal(err)
	}
	if m.Channel() != Prerelease {
		t.Fatalf("the Hive that releases the Hive follows %q", m.Channel())
	}
	merging := true
	m.Busy = func() bool { return merging }
	if msg, _ := m.AutoUpdate(); !strings.Contains(msg, "mid-merge") {
		t.Fatalf("updated mid-merge: %q", msg)
	}
	merging = false
	msg, err := m.AutoUpdate()
	if raw, _ := os.ReadFile(exe); err != nil || !strings.HasPrefix(msg, "updated to v0.1.13\n") || string(raw) != "v13" {
		t.Fatalf("update: %q, %v, binary %q", msg, err, raw)
	}
	core.Version = "v0.1.13"
	msg, err = m.AutoUpdate()
	if err != nil || !strings.HasPrefix(msg, "already on v0.1.13") || !strings.Contains(msg, "v0.1.15: waiting for") || !strings.Contains(msg, "v0.1.14: test (") {
		t.Fatalf("with nothing safe newer: %q, %v", msg, err)
	}
	api.mu.Lock()
	api.jobs["v0.1.15"] = gatesFor("success", "")
	api.mu.Unlock()
	if msg, err := m.AutoUpdate(); err != nil || !strings.HasPrefix(msg, "updated to v0.1.15\n") {
		t.Fatalf("once v0.1.15 passed here: %q, %v", msg, err)
	}
}

// A pre-release with no runs yet, or with no job for this system, is not safe.
func TestPreReleaseWithoutItsChecksWaits(t *testing.T) {
	api := preSite(t)
	api.jobs = map[string]map[string]map[string]string{
		"v0.1.13": {"ci": gatesFor("success", "success")["ci"]},
		"v0.1.15": {"ci": {"test (plan9-latest)": "success"}, "update": {"update (plan9-latest, previous)": "success"}},
	}
	m, exe := releaseAt(t, "v0.1.12", "me/hive")
	cfg := m.C.WS.LoadConfig()
	cfg.Channel = Prerelease
	m.C.WS.SaveConfig(cfg)
	msg, err := m.AutoUpdate()
	if raw, _ := os.ReadFile(exe); err != nil || !strings.HasPrefix(msg, "already on v0.1.12") || string(raw) != "old" {
		t.Fatalf("took an unchecked pre-release: %q, %v, binary %q", msg, err, raw)
	}
	for _, want := range []string{"v0.1.13: waiting for update (no run yet)", "v0.1.15: waiting for ci (no " + runners[runtime.GOOS] + " job yet)"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("%q doesn't say %q", msg, want)
		}
	}
}

// Every other install follows the latest release by default; a person can
// choose either channel.
func TestPreReleasesOffByDefault(t *testing.T) {
	preSite(t)
	m, exe := releaseAt(t, "v0.1.12", "me/hive")
	if _, err := m.C.Projects.Add(core.Project{Name: "fork", Repo: "you/hive", Self: true}); err != nil {
		t.Fatal(err)
	}
	if m.Channel() != Latest {
		t.Fatalf("a fresh install follows %q", m.Channel())
	}
	if msg, err := m.AutoUpdate(); err != nil || msg != "already on v0.1.12" {
		t.Fatalf("off by default: %q, %v", msg, err)
	}
	cfg := m.C.WS.LoadConfig()
	cfg.Channel = Prerelease
	m.C.WS.SaveConfig(cfg)
	if msg, err := m.AutoUpdate(); err != nil || !strings.HasPrefix(msg, "updated to v0.1.13\n") {
		t.Fatalf("chosen: %q, %v", msg, err)
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "v13" {
		t.Fatalf("binary %q", raw)
	}
	m2, _ := releaseAt(t, "v0.1.12", "me/hive")
	m2.C.Projects.Add(core.Project{Name: "hive", Repo: "me/hive", Self: true})
	cfg = m2.C.WS.LoadConfig()
	cfg.Channel = Latest
	m2.C.WS.SaveConfig(cfg)
	if m2.Channel() != Latest {
		t.Fatalf("the person chose latest, it follows %q", m2.Channel())
	}
}

// A channel config.json can't name is refused.
func TestUnknownChannelRefused(t *testing.T) {
	c := core.DefaultConfig()
	c.Channel = "nightly"
	if c.Check() == nil {
		t.Fatal("channel nightly accepted")
	}
}

// An API answer that hasn't changed is asked for by its ETag, which costs
// nothing of the hourly allowance, and read from the cache.
func TestAPIAsksAgainByETag(t *testing.T) {
	asked := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Write([]byte("body"))
	}))
	defer srv.Close()
	api0 := API
	defer func() { API = api0 }()
	API = srv.URL
	for range 2 {
		if got, err := apiGet("/etag", "text/plain"); err != nil || string(got) != "body" {
			t.Fatalf("got %q, %v", got, err)
		}
	}
	if asked != 2 {
		t.Fatalf("asked %d times", asked)
	}
}
