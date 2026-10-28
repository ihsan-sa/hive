package gate

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func connect(t *testing.T, proxy, dest string) (net.Conn, *bufio.Reader, int) {
	t.Helper()
	c, err := net.Dial("tcp", proxy)
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(c, "CONNECT "+dest+" HTTP/1.1\r\nHost: "+dest+"\r\n\r\n")
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	return c, br, resp.StatusCode
}

// The meter counts what goes through it, and refuses this machine.
func TestMeter(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "")
	echo, _ := net.Listen("tcp", "127.0.0.1:0")
	defer echo.Close()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()
	// Not t.TempDir: under a gate's side TMPDIR is deep in the run's folder,
	// and a unix socket's path must fit in 108 bytes.
	dir, err := os.MkdirTemp(shortTmp(), "gate-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	m, err := StartMeter(filepath.Join(dir, "m.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if c, _, code := connect(t, m.Addr, echo.Addr().String()); code != http.StatusForbidden {
		t.Errorf("reached this machine: %d", code)
	} else {
		c.Close()
	}
	for _, dest := range []string{"localhost:22", "10.0.0.1:443", "169.254.169.254:80"} {
		if c, _, code := connect(t, m.Addr, dest); code != http.StatusForbidden {
			t.Errorf("%s: %d", dest, code)
		} else {
			c.Close()
		}
	}
	m.local = true
	c, br, code := connect(t, m.Addr, echo.Addr().String())
	if code != http.StatusOK {
		t.Fatalf("tunnel: %d", code)
	}
	io.WriteString(c, "hello")
	got := make([]byte, 5)
	io.ReadFull(br, got)
	c.(*net.TCPConn).CloseWrite()
	io.Copy(io.Discard, br)
	c.Close()
	if string(got) != "hello" {
		t.Errorf("echo %q", got)
	}
	for i := 0; i < 100 && m.Bytes() < 10; i++ {
		time.Sleep(10 * time.Millisecond) // the copiers finish after the close
	}
	if m.Bytes() != 10 {
		t.Errorf("counted %d bytes, want 10", m.Bytes())
	}
	if env := strings.Join(m.Env(true), " "); !strings.Contains(env, "HTTPS_PROXY=http://127.0.0.1:3128") {
		t.Errorf("sandboxed env: %s", env)
	}
}

// The two sides' TMPDIRs are the same length, and shorter than their roots'
// own folders: a socket a test binds there fits on both sides or on neither.
func TestSideTmpEqualAndShort(t *testing.T) {
	out := "/x/runs/gate-1/out"
	cand := &Child{Name: "candidate", Root: filepath.Join(out, "candidate")}
	ctrl := &Child{Name: "control", Root: filepath.Join(out, "control")}
	if len(cand.Tmp()) != len(ctrl.Tmp()) || len(cand.Scratch()) != len(ctrl.Scratch()) {
		t.Fatalf("unequal: %s %s, %s %s", cand.Tmp(), ctrl.Tmp(), cand.Scratch(), ctrl.Scratch())
	}
	if cand.Tmp() == ctrl.Tmp() || cand.Tmp() == cand.Scratch() || len(cand.Tmp()) >= len(filepath.Join(ctrl.Root, "tmp")) {
		t.Fatalf("not distinct and short: %s %s %s", cand.Tmp(), ctrl.Tmp(), cand.Scratch())
	}
	for _, p := range []string{cand.Tmp(), cand.Scratch()} {
		if !sideDir(filepath.Base(p)) {
			t.Fatalf("%s is not tidied as a side folder", p)
		}
	}
}

// The meter refuses local names and this machine by any of its addresses,
// and a name it can't resolve goes to the gate's proxy only once public DNS
// vouches for it.
func TestMeterRefusesLocalNamesAndUnvouchedNames(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "")
	var mu sync.Mutex
	var asked []string
	up, _ := net.Listen("tcp", "127.0.0.1:0") // the gate's proxy: says yes to all
	defer up.Close()
	go func() {
		for {
			c, err := up.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				if _, err := http.ReadRequest(bufio.NewReader(c)); err == nil {
					io.WriteString(c, "HTTP/1.1 200 OK\r\n\r\n")
				}
			}()
		}
	}()
	dir, err := os.MkdirTemp(shortTmp(), "gate-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	m, err := StartMeter(filepath.Join(dir, "m.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	m.upstream = &url.URL{Scheme: "http", Host: up.Addr().String()}
	mine := net.ParseIP("203.0.114.7")
	m.own = func() []net.IP { return []net.IP{mine} }
	m.lookup = func(host string) ([]net.IP, error) {
		if host == "me.example.com" {
			return []net.IP{mine}, nil
		}
		return nil, fmt.Errorf("no DNS here")
	}
	public := map[string][]net.IP{
		"api.example.com":     {net.ParseIP("8.8.8.8")},
		"tail.example.com":    {net.ParseIP("100.101.102.103")},
		"mine.example.com":    {mine},
		"nowhere.example.com": {},
	}
	m.public = func(_ context.Context, host string) ([]net.IP, error) {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, host)
		if ips, ok := public[host]; ok {
			return ips, nil
		}
		return nil, fmt.Errorf("no public DNS answered")
	}
	for dest, want := range map[string]int{
		"203.0.114.7:443": 403, "me.example.com:443": 403, "box.tail1234.ts.net:443": 403, "nas.lan:443": 403,
		"nas:443": 403, "[64:ff9b::cb00:7207]:443": 403, "tail.example.com:443": 403, "mine.example.com:443": 403,
		"nowhere.example.com:443": 403, "unanswered.example.com:443": 403, "api.example.com:443": 200,
	} {
		c, _, code := connect(t, m.Addr, dest)
		c.Close()
		if code != want {
			t.Errorf("%s: %d, want %d", dest, code, want)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, h := range asked {
		if strings.HasSuffix(h, ".lan") || strings.HasSuffix(h, ".ts.net") || h == "nas" {
			t.Errorf("asked public DNS for local name %s", h)
		}
	}
}
