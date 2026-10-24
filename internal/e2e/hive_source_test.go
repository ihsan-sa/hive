package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// A fresh Hive's knowledge base holds its own source and design, and the lead
// answers how a score is computed from that code and when the sweep runs from
// the live config. The owner's lead couldn't: its modules held only prompts.
// A restart on a new binary (an update) rewrites the source at its version.
func TestHiveKnowsItself(t *testing.T) {
	t.Parallel()
	h := start(t)
	self := filepath.Join(h.ws.KB(), "hive")
	seeded := func() {
		t.Helper()
		// It is written beside the start, and moved in whole once it's there.
		h.wait("the Hive's source in the knowledge base", minute, func(api.State) bool {
			v, _ := os.ReadFile(filepath.Join(self, "VERSION"))
			st, _ := core.Git(h.ws.KB(), "status", "--porcelain")
			return strings.TrimSpace(string(v)) == binVersion(t) && st == "" // the gate stamps the binary it builds
		})
	}
	seeded()
	for _, f := range []string{"docs/spec.md", "docs/architecture.md", "docs/risk.md", "docs/status.md", "internal/mod/improve/improve.go", "modules/agents/lead.md", "SOURCE-MAP.md"} {
		if !core.Exists(filepath.Join(self, f)) {
			t.Errorf("the knowledge base lacks the Hive's %s", f)
		}
	}
	if m, _ := os.ReadFile(filepath.Join(self, "SOURCE-MAP.md")); !strings.Contains(string(m), "`internal/mod/kb`: Package kb is the knowledge base") {
		t.Errorf("the source map doesn't describe the packages: %s", m)
	}

	cfg := h.ws.LoadConfig()
	cfg.Schedules["improve.sweep"] = "every 7h"
	h.ws.SaveConfig(cfg)
	h.post("/api/chat", map[string]string{"chat": "lead", "text": "how is a score computed, and when does the sweep run?"}, nil)
	var answer string
	h.wait("the lead to answer", minute, func(api.State) bool {
		for _, m := range h.messages("lead") {
			if m.From != "person" && strings.Contains(m.Text, "score") {
				answer = m.Text
				return true
			}
		}
		return false
	})
	for _, want := range []string{"value >= best-s.Tolerance", "every 7h", binVersion(t)} {
		if !strings.Contains(answer, want) {
			t.Errorf("the answer lacks %q, so the lead didn't read it: %q", want, answer)
		}
	}

	// An older version's source, with a file the new one dropped.
	h.quiesce()
	h.kill()
	os.WriteFile(filepath.Join(self, "VERSION"), []byte("v0.0.1\n"), 0o644)
	os.WriteFile(filepath.Join(self, ".source-hash"), []byte("the hash of v0.0.1's source"), 0o644)
	os.WriteFile(filepath.Join(self, "internal", "gone.go"), []byte("package gone\n"), 0o644)
	core.Git(h.ws.KB(), "add", "-A")
	core.Git(h.ws.KB(), "commit", "-q", "-m", "the Hive's source at v0.0.1")
	h.launch()
	seeded() // at the new version, committed
	if core.Exists(filepath.Join(self, "internal", "gone.go")) {
		t.Error("a file the new version dropped is still in the knowledge base")
	}
}

// Every role sees the same Hive: a worker asked the owner's question gives
// the lead's answer, fact for fact.
func TestWorkerKnowsTheHive(t *testing.T) {
	t.Parallel()
	h := start(t)
	h.post("/api/project", map[string]any{"name": "facts"}, nil)
	const q = "tell me everything you can find out about the hive"
	h.post("/api/chat", map[string]string{"chat": "lead", "text": q}, nil)
	var lead string
	h.wait("the lead to answer", minute, func(api.State) bool {
		for _, m := range h.messages("lead") {
			if m.From != "person" && strings.Contains(m.Text, "tribunal") {
				lead = m.Text
				return true
			}
		}
		return false
	})
	h.post("/api/chat", map[string]string{"chat": "lead", "text": "ask a worker: " + q}, nil)
	note := filepath.Join(h.ws.KB(), "everything-about-the-hive.md")
	h.wait("the worker's answer in the knowledge base", 2*minute, func(api.State) bool { return core.Exists(note) })
	worker, _ := os.ReadFile(note)
	for _, fact := range []string{"Installed modules: agents, chat, improve, kb, system", "panel of three", "value >= best-s.Tolerance", "the sweep runs off"} {
		if !strings.Contains(lead, fact) || !strings.Contains(string(worker), fact) {
			t.Errorf("the lead and the worker don't both say %q:\nlead: %s\nworker: %s", fact, lead, worker)
		}
	}
}
