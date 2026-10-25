package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Remote reaches a Hive on another machine over SSH, so a control center on
// the person's own computer (whose clipboard it can read) drives a Hive that
// runs elsewhere. One ssh session does it all: it runs `hive remote-info
// --follow` there, which prints the daemon's daemon.json and again whenever a
// restart changes it, and it opens a SOCKS proxy (-D) on this machine's
// loopback through which the client reaches the daemon's 127.0.0.1 port.
// One login, and a restart on a new port needs no new tunnel.
type Remote struct {
	Dest string   // what ssh connects to: host, user@host or a Host from ~/.ssh/config
	Hive string   // the hive command there; "hive" by default, found in ~/.local/bin too
	SSH  []string // the ssh command; "ssh" by default

	mu     sync.Mutex
	info   DaemonInfo
	err    error // why the session ended
	stderr tailWriter
	cmd    *exec.Cmd
	stdin  io.Closer
	got    chan struct{} // closed at the first daemon.json
	done   chan struct{} // closed when ssh exits
}

// RemoteCommand is what ssh runs there: the installer puts hive in
// ~/.local/bin, which a non-interactive SSH session often leaves off PATH.
func RemoteCommand(hive string) string {
	if hive == "" {
		hive = "hive"
	}
	return `PATH="$HOME/.local/bin:$PATH" exec ` + shellQuote(hive) + ` remote-info --follow`
}

func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./~") == "" && !strings.HasPrefix(s, "~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// DialRemote starts the ssh session and returns a client once the Hive there
// has answered with its daemon.json. ssh asks for a password or passphrase
// on the terminal as usual; its messages show on stderr until then.
func DialRemote(r *Remote) (*Client, error) {
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	argv := r.SSH
	if len(argv) == 0 {
		argv = []string{"ssh"}
	}
	argv = append(append([]string{}, argv...), "-D", "127.0.0.1:"+strconv.Itoa(port), "-o", "ExitOnForwardFailure=yes", "--", r.Dest, RemoteCommand(r.Hive))
	cmd := exec.Command(argv[0], argv[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	r.stderr.live = os.Stderr
	cmd.Stderr = &r.stderr
	r.cmd, r.stdin, r.got, r.done = cmd, stdin, make(chan struct{}), make(chan struct{})
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("can't run ssh (OpenSSH's client; on Windows, the OpenSSH Client optional feature): %w", err)
	}
	go r.read(stdout)
	select {
	case <-r.got:
	case <-r.done:
		r.mu.Lock()
		defer r.mu.Unlock()
		return nil, r.err
	}
	r.stderr.quiet() // the control center owns the screen from here
	proxy := &url.URL{Scheme: "socks5", Host: "127.0.0.1:" + strconv.Itoa(port)}
	// Wait for the proxy: ssh may print daemon.json before it listens.
	for end := time.Now().Add(10 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if c, err := net.Dial("tcp", proxy.Host); err == nil {
			c.Close()
			break
		} else if time.Now().After(end) {
			r.Close()
			return nil, fmt.Errorf("ssh's proxy on %s didn't open: %w", proxy.Host, err)
		}
	}
	info, _ := r.Info()
	return &Client{Info: info, cur: info, info: r.Info, remote: true,
		http: &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}}, nil
}

// read takes each daemon.json the Hive there prints, one per line.
func (r *Remote) read(out io.Reader) {
	sc := bufio.NewScanner(out)
	first := true
	for sc.Scan() {
		var info DaemonInfo
		if json.Unmarshal(sc.Bytes(), &info) != nil || info.Port == 0 {
			continue
		}
		r.mu.Lock()
		r.info = info
		r.mu.Unlock()
		if first {
			close(r.got)
			first = false
		}
	}
	err := r.cmd.Wait()
	r.mu.Lock()
	r.err = r.ended(err)
	r.mu.Unlock()
	close(r.done)
}

// ended says why the session closed, in ssh's or the Hive's last words.
func (r *Remote) ended(err error) error {
	all, why := r.stderr.all()
	switch {
	case strings.Contains(all, `unknown command "remote-info"`):
		return fmt.Errorf("the Hive on %s is older than `remote`; run its `hive update` first", r.Dest)
	case strings.Contains(all, "not found"):
		for _, l := range strings.Split(all, "\n") {
			if strings.Contains(l, "not found") {
				why = strings.TrimSpace(l)
			}
		}
		return fmt.Errorf("no hive command on %s (%s); name it with --hive <path>", r.Dest, why)
	case why != "":
		return fmt.Errorf("ssh %s: %s", r.Dest, why)
	case err != nil:
		return fmt.Errorf("ssh %s: %w", r.Dest, err)
	}
	return fmt.Errorf("ssh %s ended", r.Dest)
}

// Info is the daemon.json the Hive there last printed, or why the session ended.
func (r *Remote) Info() (DaemonInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	select {
	case <-r.done:
		return r.info, r.err
	default:
	}
	return r.info, nil
}

// Close ends the session: the Hive there stops following when its input closes.
func (r *Remote) Close() {
	if r.stdin != nil {
		r.stdin.Close()
	}
	select {
	case <-r.done:
	case <-time.After(3 * time.Second):
		if r.cmd != nil && r.cmd.Process != nil {
			r.cmd.Process.Kill()
		}
	}
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// tailWriter passes ssh's stderr through until the control center takes the
// screen, and keeps its last lines for the error when the session ends.
type tailWriter struct {
	mu   sync.Mutex
	live io.Writer
	buf  bytes.Buffer
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.live != nil {
		w.live.Write(p)
	}
	w.buf.Write(p)
	if w.buf.Len() > 4096 {
		b := w.buf.Bytes()[w.buf.Len()-2048:]
		w.buf.Reset()
		w.buf.Write(b)
	}
	return len(p), nil
}

func (w *tailWriter) quiet() {
	w.mu.Lock()
	w.live = nil
	w.mu.Unlock()
}

// all is what was kept, and its last non-empty line.
func (w *tailWriter) all() (string, string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := strings.TrimSpace(w.buf.String())
	lines := strings.Split(s, "\n")
	return s, strings.TrimSpace(lines[len(lines)-1])
}
