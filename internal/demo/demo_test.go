package demo

import (
	"encoding/json"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// With no interpreter on PATH the demo's checks say so, instead of running
// an empty $PY ("/bin/sh: 1: -m: not found").
func TestChecksNameAMissingPython(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh")
	}
	var p struct {
		Test  string `json:"test"`
		Score struct {
			Command string `json:"command"`
		} `json:"score"`
	}
	if err := json.Unmarshal([]byte(Files()[".hive/project.json"]), &p); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{p.Test, p.Score.Command} {
		cmd := exec.Command("/bin/sh", "-c", c)
		cmd.Env = []string{"PATH=" + t.TempDir()}
		out, err := cmd.CombinedOutput()
		if err == nil || strings.TrimSpace(string(out)) != "no python or python3 on PATH" {
			t.Fatalf("%q: err %v, output %q", c, err, out)
		}
	}
}
