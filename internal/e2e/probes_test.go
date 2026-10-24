package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/daemon"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/release"
	"github.com/ihsan-sa/hive-standalone-build/internal/proc"
	"github.com/ihsan-sa/hive-standalone-build/internal/setup"
)

// The probes the person asked for by name: a source build stays dev, an
// update restarts without losing a message, and every kind of lead sees a
// push made outside the Hive. Each runs the real binary; the Live twins run
// real agents (HIVE_E2E_HARNESS=claude). Their t.Logf lines are the evidence.

const modPath = "github.com/ihsan-sa/hive-standalone-build/internal/"

// buildHive builds cmd/hive into dir/hive as a release build does, with each
// -X setting (a name under internal/, its value).
func buildHive(t *testing.T, dir string, x map[string]string) string {
	t.Helper()
	out := filepath.Join(dir, "hive")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	flags := ""
	for k, v := range x {
		flags += " -X " + modPath + k + "=" + v
	}
	root, _ := exec.Command("go", "env", "GOMOD").Output()
	cmd := exec.Command("go", "build", "-ldflags", strings.TrimSpace(flags), "-o", out, "./cmd/hive")
	cmd.Dir = filepath.Dir(strings.TrimSpace(string(root)))
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, raw)
	}
	return out
}

// releaseSite serves test/hive's releases as GitHub does: releases/latest
// redirects to the newest tag's page, and each release has checksums.txt and
// this platform's binary. fetched counts downloads of the binary.
type releaseSite struct {
	*httptest.Server
	mu      sync.Mutex
	tag     string
	binary  []byte
	fetched atomic.Int32
}

func newReleaseSite(t *testing.T) *releaseSite {
	s := &releaseSite{}
	asset := release.AssetName(runtime.GOOS, runtime.GOARCH)
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		tag, bin := s.tag, s.binary
		s.mu.Unlock()
		sum := sha256.Sum256(bin)
		switch r.URL.Path {
		case "/test/hive/releases/latest":
			http.Redirect(w, r, "/test/hive/releases/tag/"+tag, http.StatusFound)
		case "/test/hive/releases/tag/" + tag:
			fmt.Fprintln(w, "release", tag)
		case "/test/hive/releases/download/" + tag + "/checksums.txt":
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), asset)
		case "/test/hive/releases/download/" + tag + "/" + asset:
			s.fetched.Add(1)
			w.Write(bin)
		case "/repos/test/hive/releases":
			json.NewEncoder(w).Encode([]map[string]string{{"tag_name": tag, "body": "the probe's release", "html_url": s.URL + "/test/hive/releases/tag/" + tag}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *releaseSite) publish(tag string, bin []byte) {
	s.mu.Lock()
	s.tag, s.binary = tag, bin
	s.mu.Unlock()
}

// cli runs the person's command line against h's workspace.
func (h *hive) cli(exe string, args ...string) string {
	h.t.Helper()
	cmd := exec.Command(exe, args...)
	cmd.Dir = h.ws.Root
	cmd.Env = append(append(os.Environ(), "HIVE_HOME="+h.ws.Root, "HIVE_FORGE=local:"+h.forge.Dir), h.env...)
	out, err := cmd.CombinedOutput()
	h.t.Logf("$ hive %s\n%s", strings.Join(args, " "), out)
	if err != nil {
		h.t.Fatalf("hive %s: %v", strings.Join(args, " "), err)
	}
	return string(out)
}

func fileSum(t *testing.T, path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// TestSourceBuildStaysDev: a build from source, with no release version set,
// says it is a dev build everywhere it reports a version, and stays that build
// when a newer release is out: neither `hive update` nor the scheduled
// auto-update at start fetches it.
func TestSourceBuildStaysDev(t *testing.T) {
	t.Parallel()
	site := newReleaseSite(t)
	site.publish("v9.9.9", []byte("a release a source build must never install"))
	exe := buildHive(t, t.TempDir(), map[string]string{"mod/release.Site": site.URL, "mod/release.API": site.URL})
	before := fileSum(t, exe)

	h := startWith(t, exe, func(c *core.Config) {
		c.ReleaseRepo, c.AutoUpdate = "test/hive", true
		c.Schedules["hive.update"] = "every 6h" // and once at start
	})
	if out := h.cli(exe, "--version"); !strings.Contains(out, "-dev") {
		t.Fatalf("--version: %q", out)
	}
	if out := h.cli(exe, "status"); !strings.Contains(out, "Hive 0.0.0-dev") {
		t.Fatalf("status: %q", out)
	}
	h.wait("the auto-update at start", minute, func(api.State) bool { return h.journaled("system.ran", "hive.update") })
	if !h.journaled("system.ran", "source build") {
		t.Fatal("the auto-update at start didn't say this is a source build")
	}
	if out := h.cli(exe, "update"); !strings.Contains(out, "source build") {
		t.Fatalf("update: %q", out)
	}
	if out := h.cli(exe, "update", "--release", "v9.9.9"); !strings.Contains(out, "source build") {
		t.Fatalf("update --release: %q", out)
	}
	st := h.state()
	if !strings.HasSuffix(st.Version, "-dev") || h.count("hive.updated") != 0 || site.fetched.Load() != 0 || fileSum(t, exe) != before {
		t.Fatalf("the source build moved: version %s, %d hive.updated, %d downloads, binary changed %v",
			st.Version, h.count("hive.updated"), site.fetched.Load(), fileSum(t, exe) != before)
	}
	t.Logf("source build: version %s, auto-update and `hive update` both declined, %d downloads of v9.9.9, binary unchanged", st.Version, site.fetched.Load())
}

// TestUpdateKeepsAMessage: a release build updates to a newer release and
// restarts; a message the person sends with `hive ask` while the old daemon
// hands over, and one sent once it is gone, each reach the lead exactly once,
// and the CLI doesn't start a daemon of its own in the gap.
func TestUpdateKeepsAMessage(t *testing.T) { t.Parallel(); updateKeepsAMessage(t) }

func TestLiveUpdateKeepsAMessage(t *testing.T) { live(t); updateKeepsAMessage(t) }

func updateKeepsAMessage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("an update on Windows also ships the WSL binary; this probe serves one")
	}
	if setup.InUnit() {
		t.Skip("inside the Hive's systemd unit a restart goes to systemd, which would restart the real Hive")
	}
	site := newReleaseSite(t)
	x := func(v string) map[string]string {
		return map[string]string{"core.Version": v, "mod/release.Site": site.URL, "mod/release.API": site.URL}
	}
	var old, next string
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); old = buildHive(t, t.TempDir(), x("v0.0.1")) }()
	go func() { defer wg.Done(); next = buildHive(t, t.TempDir(), x("v0.0.2")) }()
	wg.Wait()
	raw, err := os.ReadFile(next)
	if err != nil {
		t.Fatal(err)
	}
	site.publish("v0.0.2", raw)

	h := startWith(t, old, func(c *core.Config) { c.ReleaseRepo = "test/hive" })
	t.Cleanup(func() { // the daemons after the update aren't h.cmd
		var evs []core.Event
		h.c.Do("GET", "/api/events", nil, &evs)
		h.quiesce()
		daemon.Stop(h.ws)
		for _, e := range evs {
			var d struct{ PID string }
			if e.Kind == "hive.started" && e.Decode(&d) == nil {
				if pid, _ := strconv.Atoi(d.PID); pid > 0 && proc.Alive(proc.Handle{PID: pid}) {
					if p, err := os.FindProcess(pid); err == nil {
						p.Kill() // a second daemon, as the gap once started
					}
				}
			}
		}
	})
	wait := patience(t)
	h.ask("lead", "hello", wait)
	first := h.c.Info.PID

	if out := h.cli(old, "update"); !strings.Contains(out, "updated to v0.0.2") {
		t.Fatalf("update: %q", out)
	}
	// Wait for the handover to begin, on a state that lasts: daemon.json says
	// the old daemon is restarting from then until the new one writes its
	// own, so polling for the restarting state alone could miss that window
	// (it's short on a fast run). Seen past it, the message below goes after
	// the handover instead of in it, and must still arrive once.
	handing, gap := false, false
	for end := time.Now().Add(30 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if c, err := api.Dial(h.ws); err == nil && (c.Info.PID != first || c.Info.Restarting) {
			handing, gap = true, c.Info.PID == first
			break
		}
	}
	if !handing {
		t.Fatal("daemon.json never said the old daemon was restarting, nor named a new one")
	}
	if !gap {
		t.Log("the handover was over before it was seen; the first message goes after it")
	}
	h.cli(old, "ask", "Sent while you update: reply with the word MARMALADE and nothing else.")
	for end := time.Now().Add(30 * time.Second); proc.Alive(proc.Handle{PID: first}) && time.Now().Before(end); {
		time.Sleep(20 * time.Millisecond)
	}
	if proc.Alive(proc.Handle{PID: first}) {
		t.Fatal("the old daemon didn't exit")
	}
	h.cli(old, "ask", "Sent after the old daemon left: reply with the word QUINCE and nothing else.")

	st := h.wait("the updated daemon", minute, func(st api.State) bool { return st.Version == "v0.0.2" })
	// The update announced itself before it began, and the new daemon has
	// none under way.
	var evs []core.Event
	h.c.Do("GET", "/api/events", nil, &evs)
	var order []string
	for _, e := range evs {
		if e.Kind == "hive.updating" || e.Kind == "hive.updated" {
			order = append(order, e.Kind)
		}
	}
	if !slices.Equal(order, []string{"hive.updating", "hive.updated"}) || st.Updating != nil {
		t.Fatalf("the update's events: %v; under way after the restart: %+v", order, st.Updating)
	}
	h.wait("the lead to answer the message sent during the update", wait, h.replied("lead", "MARMALADE"))
	h.wait("the lead to answer the message sent after the old daemon left", wait, h.replied("lead", "QUINCE"))
	for _, word := range []string{"MARMALADE", "QUINCE"} {
		n := 0
		for _, m := range h.messages("lead") {
			if m.From == "person" && strings.Contains(m.Text, word) {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("%s is in the chat %d times", word, n)
		}
	}
	if started := h.count("hive.started"); started != 2 {
		t.Fatalf("%d daemons started; want the first and the updated one", started)
	}
	if fileSum(t, old) != fileSum(t, next) {
		t.Fatal("the binary on disk isn't the release")
	}
	now, _ := api.Dial(h.ws)
	t.Logf("update: v0.0.1 (pid %d) -> %s (pid %d); both messages answered once; %d daemon starts", first, st.Version, now.Info.PID, h.count("hive.started"))
}

// TestLeadsSeePushes: the workspace lead, a project's lead and an isolated
// project's lead, each already running, see a file the person pushed to main
// outside the Hive when they are next asked.
func TestLeadsSeePushes(t *testing.T) { t.Parallel(); leadsSeePushes(t) }

func TestLiveLeadsSeePushes(t *testing.T) { live(t); leadsSeePushes(t) }

func leadsSeePushes(t *testing.T) {
	h := start(t)
	wait := patience(t)
	var p, iso core.Project
	h.post("/api/project", map[string]any{"demo": true}, &p)
	h.post("/api/project", map[string]any{"name": "vault", "isolated": true, "description": "secrets, kept apart", "goals": "Keep secrets."}, &iso)
	if !iso.Isolated {
		t.Fatalf("vault: %+v", iso)
	}
	asks := []struct{ chat, repo, ask string }{
		{"lead", p.Repo, "What's in the " + p.Name + " repo now? List the files at the top level of its main branch."},
		{"project:" + p.Name, p.Repo, "What's in the repo now? List the files at the top level of main."},
		{"project:vault", iso.Repo, "What's in the repo now? List the files at the top level of main."},
	}
	for i, a := range asks {
		// Live first, so the push lands on a running lead's view.
		h.ask(a.chat, a.ask, wait)
		file := fmt.Sprintf("PUSHED-OUTSIDE-%d.md", i)
		h.seed(a.repo, map[string]string{file: "pushed by the person, not the Hive\n"})
		h.post("/api/chat", map[string]string{"chat": a.chat, "text": a.ask}, nil)
		h.wait(a.chat+" to see the push", wait, h.replied(a.chat, file))
		msgs := h.messages(a.chat)
		t.Logf("%s after a push of %s to %s:\n%s", a.chat, file, a.repo, msgs[len(msgs)-1].Text)
	}
}

// ask posts text to a chat and waits for someone other than the person to say
// something after it.
func (h *hive) ask(chat, text string, timeout time.Duration) {
	h.t.Helper()
	others := func() int {
		n := 0
		for _, m := range h.messages(chat) {
			if m.From != "person" {
				n++
			}
		}
		return n
	}
	before := others()
	h.post("/api/chat", map[string]string{"chat": chat, "text": text}, nil)
	h.wait(chat+" to answer", timeout, func(api.State) bool { return others() > before })
}
