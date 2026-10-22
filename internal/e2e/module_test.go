package e2e

import (
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
// started again when it dies.
func TestModuleSeam(t *testing.T) {
	t.Parallel()
	h := start(t)
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
	st := h.wait("the install to reach the person", 2*minute, func(st api.State) bool { return len(st.Asks) > 0 })
	for _, a := range st.Asks {
		if a.Kind == "module" {
			h.post("/api/answer", map[string]string{"id": a.ID, "answer": "install"}, nil)
		}
	}
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
	os.WriteFile(filepath.Join(home, "inbox"), []byte("ask p echo ask\nsay hello from the door\nforbidden\n"), 0o600)
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
	if !h.journaled("module.reported", `"text":"started"`) {
		t.Fatal("the module's report is not in the journal")
	}

	f, _ := os.OpenFile(filepath.Join(home, "inbox"), os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("exit\n")
	f.Close()
	h.wait("the module to be started again", minute, func(st api.State) bool {
		return h.count("module.started") >= 2 && running(st)
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
		return h.count("module.reviewed") >= 2 && h.count("module.started") >= 3 && running(st)
	})

	// Granted only another project, it reaches neither this one nor the lead.
	h.post("/api/module/access", map[string]any{"module": "echo", "project": "other", "allow": true}, nil)
	h.post("/api/module/access", map[string]any{"module": "echo", "project": "*", "allow": false}, nil)
	f, _ = os.OpenFile(filepath.Join(home, "inbox"), os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("ask p not granted\nsay hello again\n")
	f.Close()
	h.wait("asks outside its grant refused", minute, outbox("ask 400\nsay 400"))
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

func (h *hive) count(kind string) int {
	var evs []core.Event
	h.c.Do("GET", "/api/events", nil, &evs)
	n := 0
	for _, e := range evs {
		if e.Kind == kind {
			n++
		}
	}
	return n
}
