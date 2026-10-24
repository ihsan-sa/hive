package improve

import (
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

func TestMissingTool(t *testing.T) {
	got := MissingTool("textkit-2", "test", "tests red: /bin/sh: 1: -m: not found")
	if !strings.Contains(got, "no program") || strings.Contains(got, "-m") || !strings.Contains(got, "ask the lead") {
		t.Errorf("empty interpreter: %q", got)
	}
	if got := MissingTool("p", "test", "sh: 1: python3: not found"); got != "python3 isn't installed, so p's tests can't run. Install python3 on this machine, or ask the lead in chat: an agent may be able to install it for the Hive's checks, or ask a warden to run the install for you." {
		t.Errorf("named tool: %q", got)
	}
	if got := MissingTool("p", "test", "bash: line 1: node: command not found"); !strings.HasPrefix(got, "node isn't installed") {
		t.Errorf("bash form: %q", got)
	}
	if got := MissingTool("p", "test", "tests red: hive: missing program: python or python3\n"); !strings.HasPrefix(got, "python or python3 isn't installed, so p's tests can't run") {
		t.Errorf("guard line: %q", got)
	}
	// Tests passed and the score command's tool is missing: the score can't be measured.
	if got := MissingTool("p", "score", "score did not produce a number: sh: 1: python3: not found"); !strings.HasPrefix(got, "python3 isn't installed, so p's score can't be measured") {
		t.Errorf("score tool: %q", got)
	}
	for _, out := range []string{"FAILED test_x", "AssertionError: key: not found", "lookup: not found in cache"} {
		if got := MissingTool("p", "test", out); got != "" {
			t.Errorf("not a missing program %q: %q", out, got)
		}
	}
}

// Different plain failures get different keys, even under a long shared
// header; a repeat with fresh timings keeps its key; a missing program is
// keyed on its cause and reads as one sentence. Notes are built the way
// measure builds them: clipped to one line.
func TestBaselineAlertKey(t *testing.T) {
	red := func(out string) string { return "tests red: " + core.Clip(out, 600) }
	_, a := baselineAlert("p", false, red("AssertionError: expected 1\nrun took 0.31s"))
	_, a2 := baselineAlert("p", false, red("AssertionError: expected 1\nrun took 0.47s"))
	_, b := baselineAlert("p", false, red("Segmentation fault"))
	if a != a2 || a == b || a == "test" {
		t.Errorf("keys: %q %q %q", a, a2, b)
	}
	banner := "============================= test session starts ==============================\n" +
		"platform linux -- Python 3.12.3, pytest-8.3.2, pluggy-1.5.0\n" +
		"rootdir: /work/textkit-2\nconfigfile: pyproject.toml\nplugins: cov-5.0.0, xdist-3.6.1\ncollected 42 items\n\n"
	if len(banner) <= 200 {
		t.Fatalf("banner too short to test a shared header: %d", len(banner))
	}
	_, p1 := baselineAlert("p", false, red(banner+"tests/test_a.py F\nE AssertionError: expected 1 (0.12s)"))
	_, p1b := baselineAlert("p", false, red(banner+"tests/test_a.py F\nE AssertionError: expected 1 (0.98s)"))
	_, p2 := baselineAlert("p", false, red(banner+"tests/test_b.py F\nE KeyError: 'name'"))
	if p1 != p1b || p1 == p2 {
		t.Errorf("pytest keys: %q %q %q", p1, p1b, p2)
	}
	text, key := baselineAlert("p", false, "tests red: sh: 1: python3: not found")
	if !strings.HasPrefix(key, "python3 isn't installed") || strings.Contains(text, ".;") || !strings.HasSuffix(text, "Measuring again in 10 minutes.") {
		t.Errorf("missing tool: %q %q", text, key)
	}
	if _, key := baselineAlert("p", true, "score did not produce a number: sh: 1: python3: not found"); !strings.Contains(key, "score can't be measured") {
		t.Errorf("score kind: %q", key)
	}
}
