package gate

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	// Short, as gate.go's own: a socket path has a length limit (108 bytes on
	// Linux), and t.TempDir() under a gate child's long TMPDIR passes it.
	dir, err := os.MkdirTemp(shortTmp(), "hive-meter-test-")
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
