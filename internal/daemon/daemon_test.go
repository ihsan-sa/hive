package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/app"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// After an update renames the running binary to hive.old, the restart runs
// the new hive, not the old code under its new name, and daemon.json tells
// the control center to wait for it.
func TestRestartRunsNewBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in binaries are shell scripts")
	}
	dir := t.TempDir()
	ws := core.Workspace{Root: filepath.Join(dir, "ws")}
	if err := os.MkdirAll(ws.Hive(), 0o755); err != nil {
		t.Fatal(err)
	}
	ran := filepath.Join(dir, "ran")
	for name, says := range map[string]string{"hive": `"$*"`, "hive.old": "old"} {
		script := fmt.Sprintf("#!/bin/sh\necho %s > '%s.tmp' && mv '%s.tmp' '%s'\n", says, ran, ran, ran)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	defer func(f func() (string, error)) { executable = f }(executable)
	executable = func() (string, error) { return filepath.Join(dir, "hive.old"), nil }
	stopped := false
	s := &server{a: &app.App{C: &core.Core{WS: ws}}, info: api.DaemonInfo{PID: os.Getpid(), Token: "t"}, cancel: func() { stopped = true }}
	s.restart()
	if !stopped {
		t.Fatal("the old daemon didn't stop")
	}
	if c, err := api.Dial(ws); err != nil || !c.Info.Restarting {
		t.Fatalf("daemon.json doesn't say a restart is under way: %v", err)
	}
	want := fmt.Sprintf("daemon --wait-for %d", os.Getpid())
	got := ""
	for i := 0; i < 50 && got != want; i++ {
		time.Sleep(100 * time.Millisecond)
		raw, _ := os.ReadFile(ran)
		got = strings.TrimSpace(string(raw))
	}
	if got != want {
		t.Fatalf("the restart ran %q, want the new binary with %q", got, want)
	}
}
