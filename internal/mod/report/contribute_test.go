package report

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/scan"
)

// upstream is a stand-in forge recording what is written to it.
type upstream struct {
	core.Forge
	mu       sync.Mutex
	issues   []string          // title + body
	comments map[string]string // repo#n marker -> body
	prs      []string          // repo head base
	// github: named as the real forge, whose CreateIssue checks only the
	// newest issues, so this one finds none: dedup is the module's lookup.
	github bool
}

func (u *upstream) Name() string {
	if u.github {
		return "github"
	}
	return "local"
}

func (u *upstream) CreateIssue(repo, title, body, marker string) (core.Issue, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for i, x := range u.issues {
		if strings.Contains(x, marker) && !u.github {
			return core.Issue{Number: i + 1}, nil
		}
	}
	u.issues = append(u.issues, repo+"\n"+title+"\n"+body+"\n"+marker)
	return core.Issue{Number: len(u.issues)}, nil
}

func (u *upstream) StatusComment(repo string, n int, marker, body string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.comments[fmt.Sprintf("%s#%d %s", repo, n, marker)] = body
	return nil
}

func (u *upstream) CreatePR(repo, head, base, title, body string) (core.PR, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.prs = append(u.prs, repo+" "+head+" "+base)
	return core.PR{Number: 40 + len(u.prs)}, nil
}

func (u *upstream) DefaultBranch(string) string { return "main" }

func openForge(t *testing.T, f core.Forge, edit func(*core.Config)) *core.Core {
	t.Helper()
	c := open(t, edit)
	c.Close()
	c, err := core.Open(c.WS, f)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// Contributing is its own yes: with it, each kind of failure becomes one
// scrubbed issue upstream however often it happens, with one comment per
// install counting it, and the fixes the Hive made to its own code on the
// person's fork are offered as a pull request; without it, nothing goes.
func TestContribute(t *testing.T) {
	up := &upstream{comments: map[string]string{}}
	c := openForge(t, up, func(cfg *core.Config) {})
	c.Projects.Add(core.Project{Name: "hive", Repo: "me/hive", Branch: "main", Self: true})
	c.J.MustAppend("task.saved", map[string]any{"id": "t0", "project": "hive", "stage": "merged", "title": "before the yes"})
	c.J.MustAppend("task.saved", map[string]any{"id": "t1", "stage": "blocked", "reason": "no"})
	if res, _ := (&Module{C: c}).Send(); res != "sharing is off" || len(up.issues)+len(up.prs) != 0 {
		t.Fatalf("without the yes: %q, %d issues, %d prs", res, len(up.issues), len(up.prs))
	}

	if _, err := Save(c.WS, "", "on"); err != nil {
		t.Fatal(err)
	}
	c.J.MustAppend("task.saved", map[string]any{"id": "t2", "stage": "stopped", "reason": "tests failed after 3 rounds in run w-261024-1234, key sk-abcdefghijklmnopqrstu"})
	c.J.MustAppend("task.saved", map[string]any{"id": "t3", "stage": "stopped", "reason": "tests failed after 4 rounds in run w-261025-9876, key sk-abcdefghijklmnopqrstu"})
	c.Projects.Add(core.Project{Name: "app", Repo: "someperson/secret-app", Branch: "main"})
	c.J.MustAppend("alert.raised", map[string]string{"kind": "quiet", "text": "nobody moved on someperson/secret-app, ask SomePerson"})
	c.J.MustAppend("task.saved", map[string]any{"id": "t4", "project": "hive", "stage": "merged", "title": "Fix the gate"})
	res, err := (&Module{C: c}).Send()
	if err != nil || len(up.issues) != 2 {
		t.Fatalf("%q %v: %d issues\n%s", res, err, len(up.issues), strings.Join(up.issues, "\n---\n"))
	}
	if all := strings.Join(up.issues, "\n"); strings.Contains(strings.ToLower(all), "someperson") || strings.Contains(all, "secret-app") || !strings.Contains(all, "[repo]") {
		t.Fatalf("an issue names the person's repo or account:\n%s", all)
	}
	if strings.Contains(up.issues[0]+up.issues[1], "sk-abc") || !strings.HasPrefix(up.issues[0], core.DefaultReleaseRepo+"\n") {
		t.Fatalf("an issue unscrubbed or elsewhere:\n%s", up.issues[0])
	}
	if strings.Contains(up.issues[0]+up.issues[1], "t1") || strings.Contains(fmt.Sprint(up.issues), "\"no\"") {
		t.Fatal("a failure from before the yes was filed")
	}
	counted := ""
	for k, v := range up.comments {
		if strings.Contains(k, installMarker) {
			counted += v + "\n"
		}
	}
	if !strings.Contains(counted, "2 times") || !strings.Contains(counted, "once") || strings.Contains(counted, c.WS.LoadConfig().InstallID) {
		t.Fatalf("the install comments: %s", counted)
	}
	if len(up.prs) != 1 || up.prs[0] != core.DefaultReleaseRepo+" me:main main" {
		t.Fatalf("the fixes offered: %v", up.prs)
	}
	offer := ""
	for k, v := range up.comments {
		if strings.Contains(k, "hive-offer:") {
			offer = v
		}
	}
	if !strings.Contains(offer, "Fix the gate") || strings.Contains(offer, "before the yes") {
		t.Fatalf("the offer lists: %q", offer)
	}

	// Again with nothing new: nothing written, nothing offered again.
	n, prs := len(up.comments), len(up.prs)
	(&Module{C: c}).Send()
	if len(up.comments) != n || len(up.prs) != prs || len(up.issues) != 2 {
		t.Fatal("a run with nothing new wrote upstream")
	}
	// The same failure again: no new issue, its count goes up.
	c.J.MustAppend("task.saved", map[string]any{"id": "t5", "stage": "stopped", "reason": "tests failed after 5 rounds in run w-261026-5555, key sk-abcdefghijklmnopqrstu"})
	(&Module{C: c}).Send()
	if len(up.issues) != 2 || !strings.Contains(fmt.Sprint(up.comments), "3 times") {
		t.Fatalf("a repeat: %d issues, %v", len(up.issues), up.comments)
	}
	// No: nothing more goes.
	Save(c.WS, "", "off")
	c.J.MustAppend("task.saved", map[string]any{"id": "t6", "stage": "blocked", "reason": "something else"})
	c.J.MustAppend("task.saved", map[string]any{"id": "t7", "project": "hive", "stage": "merged", "title": "Another fix"})
	(&Module{C: c}).Send()
	if len(up.issues) != 2 || len(up.prs) != prs {
		t.Fatal("contribute off still wrote upstream")
	}
}

// A Hive working in the upstream itself (the steward) offers no pull request.
func TestStewardOffersNothing(t *testing.T) {
	up := &upstream{comments: map[string]string{}}
	c := openForge(t, up, func(cfg *core.Config) { SetContribute(cfg, true) })
	c.Projects.Add(core.Project{Name: "hive", Repo: core.DefaultReleaseRepo, Branch: "main", Self: true})
	c.J.MustAppend("task.saved", map[string]any{"id": "t1", "project": "hive", "stage": "merged", "title": "x"})
	(&Module{C: c}).Send()
	if len(up.prs) != 0 {
		t.Fatalf("the steward offered %v", up.prs)
	}
}

// H0, the Hive whose own project is the upstream, reads what installs sent:
// the endpoint's store and the contribute issues, each kind with its
// installs and count, for the improve sweep.
func TestReceived(t *testing.T) {
	store := []Report{
		{Install: "aaaaaaaa-1", Version: "0.1.0", Type: "failure", Kind: "crash", Message: "the Hive stopped without shutting down"},
		{Install: "bbbbbbbb-2", Version: "0.1.1", Type: "failure", Kind: "crash", Message: "the Hive stopped without shutting down"},
		{Install: "bbbbbbbb-2", Version: "0.1.1", Type: "failure", Kind: "task-stopped", Message: "stopped after 3 rounds"},
		{Install: "bbbbbbbb-2", Version: "0.1.1", Type: "usage", Usage: map[string]float64{"runs": 4, "merges": 1}},
	}
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/reports":
			auth = r.Header.Get("Authorization")
			for _, x := range store {
				raw, _ := json.Marshal(x)
				fmt.Fprintf(w, "%s\n", raw)
			}
		case "/search/issues":
			if q := r.URL.Query().Get("q"); !strings.Contains(q, "repo:"+core.DefaultReleaseRepo) || !strings.Contains(q, issueMarker) {
				http.Error(w, "bad query "+q, 422)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{
				{"number": 7, "title": "Install failure (crash): the Hive stopped", "body": "...\n\n<!-- hive-report:0123456789ab -->"},
				{"number": 8, "title": "Someone's own issue", "body": "not a report"},
			}})
		case "/repos/" + core.DefaultReleaseRepo + "/issues/7/comments":
			json.NewEncoder(w).Encode([]map[string]any{
				{"body": "Seen on install 0a0a0a0a0a 2 times, the last at x\n\n<!-- hive-install:0a0a0a0a0a -->"},
				{"body": "Seen on install 1b1b1b1b1b once, the last at x\n\n<!-- hive-install:1b1b1b1b1b -->"},
				{"body": "me too"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := githubAPI
	githubAPI = srv.URL
	defer func() { githubAPI = old }()

	c := open(t, func(cfg *core.Config) {})
	if got := (&Module{C: c}).Received(); got != "" {
		t.Fatalf("an install's Hive read what installs sent: %q", got)
	}
	c.Projects.Add(core.Project{Name: "hive", Repo: core.DefaultReleaseRepo, Self: true})
	cfg := c.WS.LoadConfig()
	cfg.ReportsReadURL = srv.URL + "/reports"
	c.WS.SaveConfig(cfg)
	write(t, c.WS, "logins/report/read.key", "the-owners-key\n")
	got := (&Module{C: c}).Received()
	for _, want := range []string{
		"4 from 2 installs", "4 runs", "1 merges",
		"`crash`: 2 reports from 2 installs (aaaaaaaa, bbbbbbbb), versions 0.1.0, 0.1.1",
		"`task-stopped`: 1 reports from 1 installs",
		core.DefaultReleaseRepo + "#7 (0123456789ab)", "2 installs, 3 times",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the digest doesn't say %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "#8") {
		t.Error("an issue that isn't a report was counted")
	}
	if auth != "Bearer the-owners-key" {
		t.Errorf("the store was read with %q", auth)
	}
}

// The lead sets less at once, and more only with the person's yes.
func TestLeadShareOp(t *testing.T) {
	c := open(t, func(cfg *core.Config) { SetLevel(cfg, Failures) })
	m := New(c)
	lead := core.Run{RunSpec: core.RunSpec{ID: "lead-1", Role: "lead"}}
	if out, err := m.opShare(lead, json.RawMessage(`{"level":"off"}`)); err != nil || Level(c.WS.LoadConfig()) != Off {
		t.Fatalf("off: %v %v", out, err)
	}
	out, err := m.opShare(lead, json.RawMessage(`{"level":"everything","contribute":"on"}`))
	if err != nil || Level(c.WS.LoadConfig()) != Off {
		t.Fatalf("more was set without a yes: %v %v", out, err)
	}
	asks := c.Person.Open()
	if len(asks) != 1 || asks[0].Kind != "share" || !strings.Contains(asks[0].Detail, WhatIsSent(Everything)) || !strings.Contains(asks[0].Detail, ContributeQuestion) {
		t.Fatalf("the ask: %+v", asks)
	}
	a := asks[0]
	a.Answer = "yes"
	m.onPerson(a)
	cfg := c.WS.LoadConfig()
	if _, yes := Contributing(cfg); Level(cfg) != Everything || !yes {
		t.Fatalf("after the yes: %s, contribute %q", Level(cfg), cfg.Contribute)
	}
}

// On the real forge, whose own marker check sees only the newest issues, a
// failure another install filed long ago is found by searching for its
// fingerprint, and counted there instead of filed again; a search that
// fails files nothing.
func TestContributeFindsAnOldIssue(t *testing.T) {
	var queries []string
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		queries = append(queries, q)
		if r.URL.Path != "/search/issues" || fail {
			w.WriteHeader(403)
			return
		}
		fp := strings.TrimSuffix(strings.Fields(strings.SplitN(q, issueMarker, 2)[1])[0], `"`)
		json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{
			{"number": 12, "body": "a near miss <!-- " + issueMarker + fp + "0 -->"},
			{"number": 300, "body": "filed by another install\n\n<!-- " + issueMarker + fp + " -->"},
		}})
	}))
	defer srv.Close()
	old := githubAPI
	githubAPI = srv.URL
	defer func() { githubAPI = old }()

	up := &upstream{comments: map[string]string{}, github: true}
	c := openForge(t, up, func(cfg *core.Config) { SetContribute(cfg, true) })
	c.J.MustAppend("task.saved", map[string]any{"id": "t1", "stage": "stopped", "reason": "tests failed"})
	fail = true
	(&Module{C: c}).Send()
	if len(up.issues) != 0 || len(up.comments) != 0 {
		t.Fatalf("a failed search filed %d issues", len(up.issues))
	}
	fail = false
	c.J.MustAppend("task.saved", map[string]any{"id": "t2", "stage": "stopped", "reason": "tests failed"})
	(&Module{C: c}).Send()
	if len(up.issues) != 0 {
		t.Fatalf("an issue another install filed was filed again:\n%s", up.issues)
	}
	if !strings.Contains(fmt.Sprint(up.comments), core.DefaultReleaseRepo+"#300 ") || !strings.Contains(fmt.Sprint(up.comments), "2 times") {
		t.Fatalf("not counted on #300: %v", up.comments)
	}
	if q := queries[len(queries)-1]; !strings.Contains(q, "repo:"+core.DefaultReleaseRepo) || !strings.Contains(q, "in:body") {
		t.Fatalf("searched %q", q)
	}
}

// A word of hex letters is part of a failure's message, not an id.
func TestFingerprintKeepsWords(t *testing.T) {
	a := Report{Kind: "alert", Message: "page defaced in run 3fa9c2e1"}
	b := Report{Kind: "alert", Message: "page defaced in run 77ab01ff"}
	cc := Report{Kind: "alert", Message: "page acceded in run 77ab01ff"}
	if Fingerprint(a) != Fingerprint(b) || Fingerprint(a) == Fingerprint(cc) {
		t.Fatal("ids don't collapse, or words do")
	}
}

// Both outward paths pass the publish scan (issue #370), as `hive share`
// does: with no handle list nothing goes, and a part or an issue with a key
// Scrub doesn't know, a deny word or a handle is held, never sent.
func TestPublishScanGatesWhatLeaves(t *testing.T) {
	b := newBundles(t)
	up := &upstream{comments: map[string]string{}}
	c := openForge(t, up, func(cfg *core.Config) { cfg.ShareBundleURL = b.URL; SetContribute(cfg, true) })
	Save(c.WS, Everything, "")
	webhook := "ya29.a0AfH6SMBx1y2z3w4v5u6t7s" // a Google OAuth token, a shape Scrub doesn't know
	if Scrub(webhook, "") != webhook {
		t.Fatal("Scrub knows this shape now: pick one only the scan knows")
	}
	write(t, c.WS, "runs/w1/host/turn-001.jsonl", `{"text":"posting to `+webhook+`"}`+"\n")
	write(t, c.WS, "runs/w1/box/notes.md", "fine notes")
	write(t, c.WS, "runs/w2/box/notes.md", "ask Zed-Handle about it")
	write(t, c.WS, "runs/w3/box/notes.md", "the codename-bluebird plan")
	c.J.MustAppend("task.saved", map[string]any{"id": "t1", "stage": "blocked", "reason": "send it to " + webhook})
	c.J.MustAppend("task.saved", map[string]any{"id": "t2", "stage": "blocked", "reason": "Zed-Handle should look"})
	c.J.MustAppend("task.saved", map[string]any{"id": "t3", "stage": "blocked", "reason": "a plain reason"})

	// No handle list: nothing at all goes, and the failures wait.
	t.Setenv(scan.HandlesEnv, "")
	res, _ := (&Module{C: c}).Send()
	if len(b.batches()) != 0 || len(up.issues)+len(up.comments) != 0 || !strings.Contains(res, "publish scan can't run") {
		t.Fatalf("without a handle list: %q, %d batches, %d issues", res, len(b.batches()), len(up.issues))
	}

	t.Setenv(scan.HandlesEnv, "zed-handle")
	res, _ = (&Module{C: c}).Send()
	if len(b.batches()) != 1 {
		t.Fatalf("%q: %d batches", res, len(b.batches()))
	}
	got := fmt.Sprint(b.batches()[0])
	if strings.Contains(got, "ya29.") || strings.Contains(strings.ToLower(got), "zed-handle") || strings.Contains(got, "bluebird") || !strings.Contains(got, "fine notes") {
		t.Fatalf("the batch carried what the scan stops, or left out what it passes: %s", got)
	}
	if !strings.Contains(res, "publish scan held 4 parts") || strings.Contains(res, "a0AfH6") || strings.Contains(strings.ToLower(res), "zed-handle") {
		t.Fatalf("the result doesn't say what was held, or says the thing itself: %q", res)
	}
	all := strings.Join(up.issues, "\n")
	if len(up.issues) != 1 || !strings.Contains(all, "a plain reason") || strings.Contains(all, "ya29.") || strings.Contains(strings.ToLower(all), "zed-handle") {
		t.Fatalf("%q\nissues filed (%d): %s", res, len(up.issues), all)
	}
	if !strings.Contains(res, "the publish scan held 2") {
		t.Fatalf("the held issues aren't said: %q", res)
	}
	// The held parts are tried again, not marked sent.
	(&Module{C: c}).Send()
	if bs := b.batches(); len(bs) != 1 {
		t.Fatalf("a held part went later: %v", bs[len(bs)-1])
	}
}
