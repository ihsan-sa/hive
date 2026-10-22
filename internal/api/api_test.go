package api

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// A daemon that restarted on a new port still gets the message: the client
// re-reads daemon.json and sends it there instead of reporting the old port.
func TestClientFollowsRestartedDaemon(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	write := func(info DaemonInfo) {
		raw, _ := json.Marshal(info)
		os.MkdirAll(filepath.Dir(ws.DaemonFile()), 0o755)
		if err := os.WriteFile(ws.DaemonFile(), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	write(DaemonInfo{PID: 1, Port: dead, Token: "old"})
	c, err := Dial(ws)
	if err != nil {
		t.Fatal(err)
	}
	got := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	go func() {
		time.Sleep(time.Second)
		write(DaemonInfo{PID: 2, Port: srv.Listener.Addr().(*net.TCPAddr).Port, Token: "new"})
	}()
	if err := c.Do("POST", "/api/chat", map[string]string{"text": "hi"}, nil); err != nil {
		t.Fatal(err)
	}
	if got != "Bearer new" {
		t.Fatalf("daemon got %q", got)
	}
}
