package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
)

// TestKBSearchIsTheModules: the knowledge base's search is the kb module's
// own program, run by the daemon for each call (here the person's, as the
// Knowledge tab makes it): it returns the file a PR number is about, saying
// why. A deep search starts the module's research agent, which searches in
// its turn and answers with its sources.
func TestKBSearchIsTheModules(t *testing.T) {
	t.Parallel()
	h := start(t)
	os.WriteFile(filepath.Join(h.ws.KB(), "zebra-gate.md"), []byte("---\ntitle: The zebra gate\ndate: 2026-10-24\n---\n\nThe zebra gate rolled back #4242.\n"), 0o644)
	var fast struct{ Text string }
	h.wait("the kb module's search", 3*minute, func(api.State) bool {
		// The module's op is registered on the daemon's first tick; a first
		// `go run` builds the program.
		h.c.Do("POST", "/api/op", map[string]any{"op": "search", "args": map[string]string{"query": "#4242"}}, &fast)
		return strings.HasPrefix(fast.Text, "1. zebra-gate.md")
	})
	if !strings.Contains(fast.Text, "why: names #4242") {
		t.Fatalf("the search doesn't say why:\n%s", fast.Text)
	}

	var s struct{ Run string }
	h.post("/api/op", map[string]any{"op": "search", "question": "what did the zebra gate roll back?"}, &s)
	if !strings.HasPrefix(s.Run, "kb-researcher-") {
		t.Fatalf("the deep search started %q", s.Run)
	}
	var a struct {
		Text string
		Done bool
	}
	h.wait("the deep search's answer", 3*minute, func(api.State) bool {
		h.c.Do("GET", "/api/op?run="+s.Run, nil, &a)
		return a.Done
	})
	if !strings.Contains(a.Text, "FAKE: the best match is zebra-gate.md") || !strings.Contains(a.Text, "Sources:\n- zebra-gate.md") {
		t.Fatalf("the deep search's answer:\n%s", a.Text)
	}
}
