package e2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
)

// hiveQuestion is the owner's own question from his first run, where the lead
// answered from memory.
const hiveQuestion = "does the hive have wardens and tribunals"

// guessed is how the lead answered then: a guess, or a file it could read but didn't.
var guessed = regexp.MustCompile(`(?i)presumably|haven't read|have not read|can't see how many`)

// askAboutTheHive asks the lead the question and returns its answer.
func askAboutTheHive(t *testing.T, h *hive, wait time.Duration) string {
	t.Helper()
	h.post("/api/chat", map[string]string{"chat": "lead", "text": hiveQuestion}, nil)
	var answer string
	h.wait("the lead to answer", wait, func(api.State) bool {
		for _, m := range h.messages("lead") {
			if m.From != "person" && strings.Contains(strings.ToLower(m.Text), "tribunal") {
				answer = m.Text
				return true
			}
		}
		return false
	})
	if guessed.MatchString(answer) {
		t.Fatalf("the lead guessed instead of reading: %q", answer)
	}
	return answer
}

// Asked about the Hive itself, the lead reads its installed modules and the
// live state before it answers. A module installed just before the question
// shows the answer came from a read, not from what the lead was told earlier.
func TestAskAboutTheHive(t *testing.T) {
	t.Parallel()
	h := start(t)
	probe := filepath.Join(h.ws.Modules(), "probe")
	os.MkdirAll(probe, 0o755)
	os.WriteFile(filepath.Join(probe, "module.json"), []byte(`{"name": "probe", "description": "a module only this test installs", "provides": {"prompts": ["auditor.md"]}, "projects": ["*"]}`), 0o644)
	// The fake lead reads the panel size from here; pinned so a rewording of
	// the shipped tribunal prompt can't fail this test.
	if err := os.WriteFile(filepath.Join(h.ws.Modules(), "agents", "tribunal.md"), []byte("A warden convened a panel of three.\n"), 0o644); err != nil {
		t.Fatalf("pin the tribunal prompt (is the agents module installed?): %v", err)
	}
	answer := askAboutTheHive(t, h, minute)
	for _, want := range []string{"probe", "auditor", "warden", "panel of three", "waiting on you"} {
		if !strings.Contains(answer, want) {
			t.Errorf("the answer lacks %q, so the lead didn't read it: %q", want, answer)
		}
	}
}

// TestLiveAskAboutTheHive is the host probe of the same question: run it with
// HIVE_E2E_HARNESS=claude. A real lead should name the warden and the
// three-seat tribunal from its modules, without guessing.
func TestLiveAskAboutTheHive(t *testing.T) {
	if os.Getenv("HIVE_E2E_HARNESS") == "" {
		t.Skip("set HIVE_E2E_HARNESS=claude to run real agents")
	}
	h := start(t)
	answer := askAboutTheHive(t, h, 10*minute)
	t.Logf("the lead: %s", answer)
	if !regexp.MustCompile(`(?i)warden`).MatchString(answer) || !regexp.MustCompile(`(?i)three|\b3\b`).MatchString(answer) {
		t.Errorf("the answer doesn't name the warden and the panel of three: %q", answer)
	}
}
