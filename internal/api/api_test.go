package api

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

func workspace(t *testing.T) (core.Workspace, func(DaemonInfo) error) {
	ws := core.Workspace{Root: t.TempDir()}
	if err := os.MkdirAll(filepath.Dir(ws.DaemonFile()), 0o755); err != nil {
		t.Fatal(err)
	}
	return ws, func(info DaemonInfo) error {
		raw, _ := json.Marshal(info)
		return core.WriteFileAtomic(ws.DaemonFile(), raw, 0o600)
	}
}

func deadPort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// A daemon that restarted on a new port still gets the message: the client
// keeps dialling while the old one is restarting, re-reads daemon.json and
// sends to the new daemon. The new port is held from the start (a freed port
// can be taken by another test's server before the new daemon listens), and
// it answers only once it serves.
func TestClientFollowsRestartedDaemon(t *testing.T) {
	ws, write := workspace(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	next := ln.Addr().(*net.TCPAddr).Port
	if err := write(DaemonInfo{PID: 1, Port: deadPort(t), Token: "old", Restarting: true}); err != nil {
		t.Fatal(err)
	}
	c, err := Dial(ws)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case got <- r.Header.Get("Authorization"):
		default:
		}
		w.Write([]byte(`{}`))
	})}
	defer srv.Close()
	go func() {
		// The new daemon is named a while before it listens.
		time.Sleep(500 * time.Millisecond)
		if err := write(DaemonInfo{PID: 2, Port: next, Token: "new"}); err != nil {
			t.Error(err)
			return
		}
		time.Sleep(time.Second)
		srv.Serve(ln)
	}()
	if err := c.Do("POST", "/api/chat", map[string]string{"text": "hi"}, nil); err != nil {
		t.Fatal(err)
	}
	if a := <-got; a != "Bearer new" {
		t.Fatalf("daemon got %q", a)
	}
}

// With no restart under way (a dead port under a live-looking daemon.json, or
// a daemon the person stopped), the client fails at once instead of waiting.
func TestClientFailsFastWithoutRestart(t *testing.T) {
	ws, write := workspace(t)
	if err := write(DaemonInfo{PID: 1, Port: deadPort(t), Token: "t"}); err != nil {
		t.Fatal(err)
	}
	c, err := Dial(ws)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if c.Ping() {
		t.Fatal("ping answered on a dead port")
	}
	if c.Do("GET", "/api/state", nil, nil) == nil {
		t.Fatal("call answered on a dead port")
	}
	os.Remove(ws.DaemonFile())
	if c.Do("GET", "/api/state", nil, nil) == nil {
		t.Fatal("call answered after the daemon stopped")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("failing took %v", d)
	}
}

// An update's new daemon reads the version of the one it replaces, which
// before v0.1.4 wrote .hive/daemon.json: a v0.1.2 hive-v0 is renamed by it.
func TestDialReadsADaemonFromBeforePrivate(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	os.MkdirAll(ws.Hive(), 0o755)
	os.WriteFile(filepath.Join(ws.Hive(), "daemon.json"), []byte(`{"pid": 7, "version": "v0.1.2", "restarting": true}`), 0o600)
	c, err := Dial(ws)
	if err != nil || c.Info.Version != "v0.1.2" || !c.Info.Restarting {
		t.Fatalf("Dial = %+v, %v", c, err)
	}
	os.MkdirAll(ws.Private(), 0o700)
	os.WriteFile(ws.DaemonFile(), []byte(`{"pid": 8, "version": "v0.1.7"}`), 0o600)
	if c, err := Dial(ws); err != nil || c.Info.Version != "v0.1.7" {
		t.Fatalf("Dial preferred the old file: %+v, %v", c, err)
	}
}
