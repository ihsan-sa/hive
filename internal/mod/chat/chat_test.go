package chat

import (
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// A remote that never answers holds a person's post for syncWait at most,
// and the failure lands in the journal instead of vanishing.
func TestSyncBeforePostIsBounded(t *testing.T) {
	dir := t.TempDir()
	forge := &core.Local{Dir: filepath.Join(dir, "forge")}
	c, err := core.Open(core.Workspace{Root: filepath.Join(dir, "hive")}, forge)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	forge.EnsureRepo("me/slow", "")
	p, err := c.Projects.Add(core.Project{Name: "slow", Repo: "me/slow"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Effects.Clone(p); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { // accepts and never answers
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
		}
	}()
	if _, err := core.Git(c.WS.Project("slow"), "remote", "set-url", "origin", fmt.Sprintf("git://127.0.0.1:%d/slow", ln.Addr().(*net.TCPAddr).Port)); err != nil {
		t.Fatal(err)
	}
	defer func(d time.Duration) { syncWait = d }(syncWait)
	syncWait = 500 * time.Millisecond
	m := &Module{C: c}
	done := make(chan struct{})
	go func() { m.syncFor(&Chat{ID: "project:slow", Kind: "project", Project: "slow"}); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the post waited on a remote that never answers")
	}
	failed := false
	for _, e := range c.J.Since(0) {
		failed = failed || e.Kind == "chat.sync_failed"
	}
	if !failed {
		t.Fatal("the failed sync wasn't journaled")
	}
	ln.Close() // the held fetch ends; the next sync waits for it to let go
	c.Effects.Sync(p)
}
