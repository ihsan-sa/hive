package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// The module seam: a module from git that carries a program (testdata/echomod)
// starts only after the person approves it and a read-only review run clears
// its commit; it files an ask with where it came from, carries the lead's
// conversation out and a steer line in, is refused outside its hooks, and is
// started again when it dies. One the review declines stays stopped; one
// granted a single project never reads the lead's chat or the person's asks;
// and a module catching up past more events than one read holds misses none.
func TestModuleSeam(t *testing.T) {
	t.Parallel()
	h := start(t)
	cfg := h.ws.LoadConfig()
	cfg.Autonomy = 1 // installs ask the person, whose asks a module must not read
	h.ws.SaveConfig(cfg)
	t.Cleanup(func() { // stop the daemon, and with it the module, before its folder goes
		h.c.Do("POST", "/api/shutdown", nil, nil)
		for i := 0; i < 50 && h.c.Ping(); i++ {
			time.Sleep(200 * time.Millisecond)
		}
		time.Sleep(time.Second)
	})
	h.seed("me/p", map[string]string{".hive/project.json": `{"test": "true"}`, ".hive/goals.md": "# Goals\n"})
	h.post("/api/project", map[string]any{"name": "p", "repo": "me/p"}, nil)

	src := filepath.Join(t.TempDir(), "echo")
	exe := "echomod"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", filepath.Join(src, exe), "./testdata/echomod").CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	os.WriteFile(filepath.Join(src, "module.json"), []byte(`{"name":"echo","description":"a door: an inbox and an outbox file","exec":["./`+exe+`"],"hooks":["source","channel"]}`), 0o644)
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-q", "-m", "v1"}, {"tag", "v1.0.0"}} {
		if _, err := core.Git(src, args...); err != nil {
			t.Fatal(err)
		}
	}
	h.post("/api/chat", map[string]string{"chat": "lead", "text": "install module " + src}, nil)
	// The install goes ahead at once: no ask reaches the person.
	running := func(st api.State) bool {
		for _, m := range st.Modules {
			if m.Name == "echo" {
				return m.Status == "running"
			}
		}
		return false
	}
	h.wait("the module to be reviewed and started", 2*minute, running)
	if !h.journaled("module.reviewed", `"vote":"approve"`) {
		t.Fatal("the module started without a review on record")
	}

	home := filepath.Join(h.ws.Hive(), "modules", "echo")
	os.WriteFile(filepath.Join(home, "inbox"), []byte("ask p echo ask\nsay hello from the door\nforbidden\nnull\nraw\n"), 0o600)
	h.wait("the ask to be filed from the inbox", minute, func(st api.State) bool {
		tk, ok := task(st, "p", "echo ask")
		return ok && tk.From == "echo/inbox"
	})
	outbox := func(line string) func(api.State) bool {
		return func(api.State) bool {
			raw, _ := os.ReadFile(filepath.Join(home, "outbox"))
			return strings.Contains(string(raw), line)
		}
	}
	h.wait("the lead's reply in the outbox", minute, outbox("lead: Hello. I'm the lead."))
	h.wait("a call outside its hooks refused", minute, outbox("forbidden 403"))
	h.wait("an empty report refused", minute, outbox("null 400"))
	h.wait("a token without Bearer refused", minute, outbox("raw 401"))
	tagged := false
	for _, m := range h.messages("lead") {
		tagged = tagged || (m.Text == "hello from the door" && m.From == "echo")
	}
	if !tagged {
		t.Fatal("the module's line in the lead's chat is not tagged as the module")
	}
	if !h.journaled("module.reported", `"text":"started"`) {
		t.Fatal("the module's report is not in the journal")
	}

	f, _ := os.OpenFile(filepath.Join(home, "inbox"), os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("exit\n")
	f.Close()
	h.wait("the module to be started again", minute, func(st api.State) bool {
		return h.countWith("module.started", `"module":"echo"`) >= 2 && running(st)
	})

	// An update is read again before it runs.
	os.WriteFile(filepath.Join(src, "README.md"), []byte("an inbox and an outbox\n"), 0o644)
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "v1.1"}, {"tag", "v1.1.0"}} {
		if _, err := core.Git(src, args...); err != nil {
			t.Fatal(err)
		}
	}
	h.post("/api/job", map[string]string{"name": "modules.update"}, nil)
	h.wait("the update to be reviewed and started", 2*minute, func(st api.State) bool {
		return h.count("module.reviewed") >= 2 && h.countWith("module.started", `"module":"echo"`) >= 3 && running(st)
	})

	// Granted only another project, it reaches neither this one nor the lead.
	h.post("/api/module/access", map[string]any{"module": "echo", "project": "other", "allow": true}, nil)
	h.post("/api/module/access", map[string]any{"module": "echo", "project": "*", "allow": false}, nil)
	f, _ = os.OpenFile(filepath.Join(home, "inbox"), os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("ask p not granted\nsay hello again\n")
	f.Close()
	h.wait("asks outside its grant refused", minute, outbox("ask 400\nsay 400"))

	// A module its review declines stays stopped, with an alert. Installing it
	// talks with the lead, which echo must not read.
	cut := h.state().Seq
	nope := filepath.Join(t.TempDir(), "nope")
	os.MkdirAll(nope, 0o755)
	os.WriteFile(filepath.Join(nope, "module.json"), []byte(`{"name":"nope","description":"FAKE:paneldecline","exec":["./nope"],"hooks":["source"]}`), 0o644)
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-q", "-m", "v1"}, {"tag", "v1.0.0"}} {
		if _, err := core.Git(nope, args...); err != nil {
			t.Fatal(err)
		}
	}
	h.post("/api/chat", map[string]string{"chat": "lead", "text": "install module " + nope}, nil)
	h.wait("the review to decline it", 2*minute, func(st api.State) bool {
		for _, m := range st.Modules {
			if m.Name == "nope" {
				return strings.HasPrefix(m.Status, "declined by its review")
			}
		}
		return false
	})
	if !h.journaled("alert.raised", "module nope was not started") || h.journaled("module.started", `"module":"nope"`) {
		t.Fatal("a declined module: want an alert and no start")
	}
	events := func() map[string]int64 { // the last seq echo read of each kind
		raw, _ := os.ReadFile(filepath.Join(home, "events"))
		last := map[string]int64{}
		for _, l := range strings.Split(string(raw), "\n") {
			var seq int64
			var kind string
			if n, _ := fmt.Sscan(l, &seq, &kind); n == 2 {
				last[kind] = seq
			}
		}
		return last
	}
	h.wait("echo to read the alert", minute, func(api.State) bool { return events()["alert.raised"] > cut })
	for kind, seq := range events() {
		if seq > cut && (strings.HasPrefix(kind, "chat.") || strings.HasPrefix(kind, "person.") || strings.HasPrefix(kind, "run.")) {
			t.Fatalf("echo, granted one project, read %s", kind)
		}
	}

	// Held while more events land than one read returns, it reads the first
	// one after its cursor.
	f, _ = os.OpenFile(filepath.Join(home, "inbox"), os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("hold\n")
	f.Close()
	h.wait("echo to hold", minute, outbox("holding"))
	h.post("/api/module/report", map[string]string{"text": "marker"}, nil)
	for range 2001 {
		h.post("/api/module/report", map[string]string{"text": "filler"}, nil)
	}
	os.WriteFile(filepath.Join(home, "release"), nil, 0o600)
	h.wait("echo to read past the gap from its cursor", minute, outbox("report marker"))
}

// journaled says some event of kind carries text in its data.
func (h *hive) journaled(kind, text string) bool {
	var evs []core.Event
	h.c.Do("GET", "/api/events", nil, &evs)
	for _, e := range evs {
		if e.Kind == kind && strings.Contains(string(e.Data), text) {
			return true
		}
	}
	return false
}

func (h *hive) count(kind string) int { return h.countWith(kind, "") }

// countWith counts events of kind that carry text in their data.
func (h *hive) countWith(kind, text string) int {
	var evs []core.Event
	h.c.Do("GET", "/api/events", nil, &evs)
	n := 0
	for _, e := range evs {
		if e.Kind == kind && strings.Contains(string(e.Data), text) {
			n++
		}
	}
	return n
}

// The kb module's dream program is a module program like any other: the
// daemon starts it from the module's folder, its schedule reaches it as
// module.scheduled (here run at once, as the control center's run-now does),
// and the pass it sends back merges two notes with one title into one living
// note, committed in the knowledge base and journaled as kb.dreamed.
func TestKBDreams(t *testing.T) {
	t.Parallel()
	h := start(t)
	t.Cleanup(func() {
		h.c.Do("POST", "/api/shutdown", nil, nil)
		for i := 0; i < 50 && h.c.Ping(); i++ {
			time.Sleep(200 * time.Millisecond)
		}
		time.Sleep(time.Second)
	})
	kb := h.ws.KB()
	for name, body := range map[string]string{
		"sandbox-gate.md":   "---\ntitle: Sandbox gate\ntags: gate\nproject: \ndate: 2026-10-23\nrun: w-1\n---\n\nThe gate needs bubblewrap.\n",
		"sandbox-gate-2.md": "---\ntitle: Sandbox gate\ntags: gate\nproject: \ndate: 2026-10-24\nrun: w-2\n---\n\nThe gate runs under bubblewrap now.\n",
	} {
		os.WriteFile(filepath.Join(kb, name), []byte(body), 0o644)
	}
	core.Git(kb, "add", "sandbox-gate.md", "sandbox-gate-2.md")
	core.Git(kb, "commit", "-q", "-m", "two notes")
	h.wait("the dream program to run", 3*minute, func(st api.State) bool {
		for _, m := range st.Modules {
			if m.Name == "kb" {
				return m.Status == "running"
			}
		}
		return false
	})
	h.post("/api/job", map[string]string{"name": "kb.dream"}, nil)
	h.wait("a dream pass", 3*minute, func(api.State) bool { return h.count("kb.dreamed") > 0 })
	if !h.journaled("kb.dreamed", `"into":"sandbox-gate.md"`) {
		t.Fatal("the pass didn't merge the two notes")
	}
	living, _ := os.ReadFile(filepath.Join(kb, "sandbox-gate.md"))
	if core.Exists(filepath.Join(kb, "sandbox-gate-2.md")) || !strings.Contains(string(living), "runs under bubblewrap now") || !strings.Contains(string(living), "needs bubblewrap") {
		t.Fatalf("living note:\n%s", living)
	}
	if out, _ := core.Git(kb, "log", "-1", "--format=%s"); !strings.HasPrefix(out, "Dream (scheduled)") {
		t.Fatalf("last commit %q", out)
	}
}
