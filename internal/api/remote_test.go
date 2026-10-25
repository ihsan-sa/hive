package api

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestMain lets the test binary stand in for ssh (HIVE_FAKE_SSH=1): it
// opens the -D SOCKS proxy, mapping the daemon ports "there" to real ones
// here, and prints the info file's daemon.json whenever it changes, as
// `hive remote-info --follow` does, until its input closes.
func TestMain(m *testing.M) {
	if os.Getenv("HIVE_FAKE_SSH") == "1" {
		os.Exit(fakeSSH(os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakeSSH(args []string) int {
	if why := os.Getenv("HIVE_FAKE_SSH_FAIL"); why != "" {
		fmt.Fprintln(os.Stderr, why)
		fmt.Fprintln(os.Stderr, "\nhive: a small team of AI agents working on your projects.")
		return 2
	}
	if len(args) != 7 || args[0] != "-D" || args[4] != "--" || args[5] != "box" || args[6] != RemoteCommand("") {
		fmt.Fprintf(os.Stderr, "fake ssh: unexpected args %q\n", args)
		return 255
	}
	ports := map[string]string{} // there -> here
	for _, kv := range strings.Split(os.Getenv("HIVE_FAKE_SSH_PORTS"), ",") {
		k, v, _ := strings.Cut(kv, "=")
		ports[k] = v
	}
	ln, err := net.Listen("tcp", args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 255
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go socks(c, ports)
		}
	}()
	closed := make(chan struct{})
	go func() { io.Copy(io.Discard, os.Stdin); close(closed) }()
	last := ""
	for {
		if raw, err := os.ReadFile(os.Getenv("HIVE_FAKE_SSH_INFO")); err == nil && string(raw) != last {
			last = string(raw)
			fmt.Println(strings.TrimSpace(last))
		}
		select {
		case <-closed:
			return 0
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// socks serves one SOCKS5 CONNECT to 127.0.0.1, as ssh -D does.
func socks(c net.Conn, ports map[string]string) {
	defer c.Close()
	r := bufio.NewReader(c)
	head := make([]byte, 2)
	if _, err := io.ReadFull(r, head); err != nil {
		return
	}
	io.ReadFull(r, make([]byte, head[1]))
	c.Write([]byte{5, 0})
	req := make([]byte, 4)
	if _, err := io.ReadFull(r, req); err != nil || req[3] != 1 {
		return
	}
	addr := make([]byte, 6)
	io.ReadFull(r, addr)
	port := strconv.Itoa(int(binary.BigEndian.Uint16(addr[4:])))
	up, err := net.Dial("tcp", "127.0.0.1:"+ports[port])
	if err != nil {
		c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0}) // connection refused
		return
	}
	defer up.Close()
	c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	go io.Copy(up, r)
	io.Copy(c, up)
}

// daemonAt serves /api/ping for one token on a port of its own.
func daemonAt(t *testing.T, token string) (*http.Server, int) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprintf(w, `{"token":%q}`, token)
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return srv, ln.Addr().(*net.TCPAddr).Port
}

// TestRemoteFollowsTheHiveThere: a control center here reaches a Hive over
// ssh's proxy at the port it has there (not one here), and follows it when a
// restart moves it to a new port and token.
func TestRemoteFollowsTheHiveThere(t *testing.T) {
	one, p1 := daemonAt(t, "one")
	_, p2 := daemonAt(t, "two")
	info := filepath.Join(t.TempDir(), "daemon.json")
	write := func(d DaemonInfo) {
		raw, _ := json.Marshal(d)
		os.WriteFile(info+".tmp", raw, 0o600)
		os.Rename(info+".tmp", info)
	}
	write(DaemonInfo{PID: 1, Port: 41001, Token: "one"})
	t.Setenv("HIVE_FAKE_SSH", "1")
	t.Setenv("HIVE_FAKE_SSH_INFO", info)
	t.Setenv("HIVE_FAKE_SSH_PORTS", fmt.Sprintf("41001=%d,41002=%d", p1, p2))
	r := &Remote{Dest: "box", SSH: []string{os.Args[0]}}
	c, err := DialRemote(r)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var got struct{ Token string }
	if err := c.Do("GET", "/api/ping", nil, &got); err != nil || got.Token != "one" {
		t.Fatalf("first daemon: %+v %v", got, err)
	}

	one.Close()
	write(DaemonInfo{PID: 2, Port: 41002, Token: "two"})
	if err := c.Do("GET", "/api/ping", nil, &got); err != nil || got.Token != "two" {
		t.Fatalf("after the restart: %+v %v", got, err)
	}

	r.Close()
	if _, err := r.Info(); err == nil {
		t.Fatal("a closed session still reads as live")
	}
	if err := c.Do("GET", "/api/ping", nil, nil); err == nil || !strings.Contains(err.Error(), "remote` again") {
		t.Fatalf("after the session ended: %v", err)
	}
}

// TestRemoteSendsOnce: a daemon that took a post and dropped the connection
// before answering gets it once; only a request that never went out (the
// proxy's connect refused) is sent again.
func TestRemoteSendsOnce(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var posts atomic.Int32
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		posts.Add(1)
		c, _, _ := w.(http.Hijacker).Hijack()
		c.Close() // taken, never answered: a daemon going down mid-request
	})}
	go srv.Serve(ln)
	defer srv.Close()
	info := filepath.Join(t.TempDir(), "daemon.json")
	os.WriteFile(info, []byte(`{"pid":1,"port":41001,"token":"one"}`), 0o600)
	t.Setenv("HIVE_FAKE_SSH", "1")
	t.Setenv("HIVE_FAKE_SSH_INFO", info)
	t.Setenv("HIVE_FAKE_SSH_PORTS", fmt.Sprintf("41001=%d", ln.Addr().(*net.TCPAddr).Port))
	r := &Remote{Dest: "box", SSH: []string{os.Args[0]}}
	c, err := DialRemote(r)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := c.Do("POST", "/api/chat", map[string]string{"text": "hi"}, nil); err == nil {
		t.Fatal("a dropped answer read as sent")
	}
	if n := posts.Load(); n != 1 {
		t.Fatalf("the post went %d times", n)
	}
}

// TestRemoteSaysWhy: a Hive there too old for remote-info, or no hive on its
// PATH, is named, not left as ssh's exit status.
func TestRemoteSaysWhy(t *testing.T) {
	t.Setenv("HIVE_FAKE_SSH", "1")
	for why, want := range map[string]string{
		`unknown command "remote-info"`:         "older than `remote`",
		`bash: line 1: hive: command not found`: "--hive <path>",
	} {
		t.Setenv("HIVE_FAKE_SSH_FAIL", why)
		_, err := DialRemote(&Remote{Dest: "box", SSH: []string{os.Args[0]}})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", why, err)
		}
	}
}
