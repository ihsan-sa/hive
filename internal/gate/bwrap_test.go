package gate

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// needBwrap skips where bubblewrap cannot make namespaces, except where
// HIVE_GATE_BWRAP=1 says it must (ci's sandbox job), which fails instead.
func needBwrap(t *testing.T) {
	t.Helper()
	if BwrapWorks() {
		return
	}
	if os.Getenv("HIVE_GATE_BWRAP") == "1" {
		t.Fatal("HIVE_GATE_BWRAP=1, but bubblewrap cannot make namespaces here")
	}
	t.Skip("bubblewrap cannot make namespaces here")
}

var (
	hiveOnce sync.Once
	hiveBin  string
	hiveErr  error
)

// builtHive is this tree's hive, for the forwarder.
func builtHive(t *testing.T) string {
	hiveOnce.Do(func() {
		dir, _ := os.MkdirTemp("", "gate-hive-")
		hiveBin = filepath.Join(dir, "hive")
		out, err := exec.Command("go", "build", "-o", hiveBin, "../../cmd/hive").CombinedOutput()
		if err != nil {
			hiveErr = err
			t.Logf("%s", out)
		}
	})
	if hiveErr != nil {
		t.Fatal(hiveErr)
	}
	return hiveBin
}

func meteredChild(t *testing.T, name string) (*Child, *Meter) {
	t.Helper()
	g, err := FindGo()
	if err != nil {
		t.Skip(err)
	}
	sock, _ := os.MkdirTemp(shortTmp(), "gate-test-")
	t.Cleanup(func() { os.RemoveAll(sock) })
	m, err := StartMeter(filepath.Join(sock, "m.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	c := &Child{Name: name, Root: filepath.Join(t.TempDir(), name), Sandbox: "bwrap", Go: g, Meter: m, Fwd: builtHive(t)}
	c.Prepare()
	return c, m
}

// The meter is the only way out: no direct connection, nothing on this
// machine, and what goes out through it is counted.
func TestBwrapMeterIsTheOnlyWayOut(t *testing.T) {
	needBwrap(t)
	c, m := meteredChild(t, "candidate")
	host, _ := net.Listen("tcp", "127.0.0.1:0")
	defer host.Close()
	go func() {
		for {
			x, err := host.Accept()
			if err != nil {
				return
			}
			x.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(host.Addr().String())
	script := `
if bash -c 'exec 3<>/dev/tcp/1.1.1.1/443' 2>/dev/null; then echo "a direct connection out"; exit 1; fi
if bash -c "exec 3<>/dev/tcp/127.0.0.1/$1" 2>/dev/null; then echo "a port on this machine"; exit 1; fi
bash -c 'exec 3<>/dev/tcp/127.0.0.1/3128' || { echo "no forwarder"; exit 1; }
if [ -n "$CHECK_OUT" ]; then curl -sS -o /dev/null https://example.com || { echo "nothing through the meter"; exit 1; }; fi
`
	check := ""
	if os.Getenv("HIVE_GATE_BWRAP") == "1" { // ci reaches the internet
		check = "1"
	}
	c.Extra = []string{"CHECK_OUT=" + check}
	if out, err := c.Exec(2*time.Minute, c.Root, "sh", "-c", script, "sh", port); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if check != "" && m.Bytes() == 0 {
		t.Error("the meter counted nothing")
	}
}

// Code the side landed runs in a sealed sandbox the gate starts beside the
// side's, on the side's request: it gets the request's stdin, has no network
// and sees nothing of the side.
func TestBwrapSealedCheckIsASibling(t *testing.T) {
	needBwrap(t)
	side, _ := meteredChild(t, "candidate")
	os.WriteFile(filepath.Join(side.Root, "mark"), []byte("x"), 0o644)
	check := &Child{Name: "check", Root: filepath.Join(t.TempDir(), "check"), Sandbox: "bwrap"}
	check.Prepare()
	dir, _ := os.MkdirTemp(shortTmp(), "gate-test-")
	defer os.RemoveAll(dir)
	l, err := StartLauncher(filepath.Join(dir, "check.sock"), check)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	script := filepath.Join(dir, "check")
	checkScript(script, side.Fwd, l.Sock)
	side.RO, side.RW = []string{script}, []string{l.Sock}
	inner := `
got=$(printf hello | "$1" sh -c '
	cat
	if bash -c "exec 3<>/dev/tcp/127.0.0.1/3128" 2>/dev/null; then echo " sealed code has a network"; exit 1; fi
	test ! -e "$0/mark" || { echo " sealed code sees the side"; exit 1; }
	[ "$HIVE_GATE_SANDBOXED" = 1 ] || { echo " not marked as inside"; exit 1; }
' "$2") || { echo "the sealed run failed: $got"; exit 1; }
[ "$got" = hello ] || { echo "sealed run said: $got"; exit 1; }`
	if out, err := side.Exec(2*time.Minute, side.Root, "sh", "-c", inner, "sh", script, side.Root); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}

// A deadline on a child kills everything it started, not only its first
// process.
func TestDeadlineKillsTheTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("killing a child's tree is for Unix process groups and bubblewrap's pid namespace; the gate's children run on Linux")
	}
	c := &Child{Name: "candidate", Root: filepath.Join(t.TempDir(), "c"), Sandbox: testSandbox()}
	c.Prepare()
	if c.Sandbox == "bwrap" {
		c.Fwd = builtHive(t)
	}
	pidFile := filepath.Join(c.Root, "pid")
	start := time.Now()
	out, err := c.Exec(2*time.Second, c.Root, "sh", "-c", `sh -c 'sleep 300' & echo $! > "$0"; sleep 300`, pidFile)
	if err == nil || !strings.Contains(err.Error(), "timed out") || time.Since(start) > 30*time.Second {
		t.Fatalf("%v after %s: %s", err, time.Since(start), out)
	}
	if c.Sandbox == "bare" {
		raw, _ := os.ReadFile(pidFile)
		time.Sleep(200 * time.Millisecond)
		if pid := strings.TrimSpace(string(raw)); pid != "" && exec.Command("kill", "-0", pid).Run() == nil {
			t.Errorf("the grandchild %s outlived the deadline", pid)
		}
	}
}
