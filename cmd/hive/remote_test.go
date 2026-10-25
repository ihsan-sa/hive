package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// TestRemoteInfoFollows: what `hive remote` runs over ssh prints daemon.json,
// again when a restart rewrites it, and stops when the session's input closes.
func TestRemoteInfoFollows(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	os.MkdirAll(filepath.Dir(ws.DaemonFile()), 0o700)
	write := func(d api.DaemonInfo) {
		raw, _ := json.Marshal(d)
		core.WriteFileAtomic(ws.DaemonFile(), raw, 0o600)
	}
	first := api.DaemonInfo{PID: 1, Port: 1001, Token: "one"}
	write(first)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan int)
	go func() { done <- followInfo(ws, first, true, inR, outW, 10*time.Millisecond) }()
	lines := bufio.NewScanner(outR)
	read := func() (d api.DaemonInfo) {
		if !lines.Scan() {
			t.Fatal("no line")
		}
		json.Unmarshal(lines.Bytes(), &d)
		return d
	}
	if d := read(); d.Port != 1001 || d.Token != "one" {
		t.Fatalf("first: %+v", d)
	}
	write(api.DaemonInfo{PID: 2, Port: 1002, Token: "two"})
	if d := read(); d.PID != 2 || d.Port != 1002 || d.Token != "two" {
		t.Fatalf("after the restart: %+v", d)
	}
	inW.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("didn't stop when its input closed")
	}
}

// TestRemoteWorkspace: each remote host gets its own folder here, named safely.
func TestRemoteWorkspace(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("LocalAppData", os.Getenv("XDG_CACHE_HOME"))
	t.Setenv("HOME", os.Getenv("XDG_CACHE_HOME")) // macOS: ~/Library/Caches
	a, err := remoteWorkspace("me@box:22")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := remoteWorkspace("other")
	if a.Root == b.Root || filepath.Base(a.Root) != "me_box_22" {
		t.Fatalf("%s %s", a.Root, b.Root)
	}
	if _, err := os.Stat(a.Hive()); err != nil {
		t.Fatal(err)
	}
}
