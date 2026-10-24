package gate

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

// Meter is a side's way out: an HTTP CONNECT proxy the gate runs, outside
// every sandbox, that counts the bytes each way. Under bubblewrap it is the
// side's only way out (its sandbox has no network of its own, and a
// forwarder inside relays 127.0.0.1:ProxyPort to the meter's socket), so what
// the side spends talking to a model is counted where the side cannot write,
// and cannot be routed around. It refuses this machine and private networks,
// so a side cannot reach a daemon's port, or anything else, here.
type Meter struct {
	Sock string // the unix socket a sandbox's forwarder dials
	Addr string // a loopback address, for bare runs (which it cannot enforce)

	ln, tcp  net.Listener
	upstream *url.URL // the gate's own proxy, if it has one
	bytes    atomic.Int64
	local    bool // tests only: reach this machine
	wg       sync.WaitGroup
}

// ProxyPort is where the forwarder listens inside a sandbox's own network.
const ProxyPort = 3128

// StartMeter serves a meter on a unix socket at sock and on a loopback port.
func StartMeter(sock string) (*Meter, error) {
	m := &Meter{Sock: sock}
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		if v := os.Getenv(k); v != "" {
			if u, err := url.Parse(v); err == nil && u.Host != "" {
				m.upstream = u
				break
			}
		}
	}
	os.Remove(sock)
	var err error
	if m.ln, err = net.Listen("unix", sock); err != nil {
		return nil, err
	}
	if m.tcp, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
		m.ln.Close()
		return nil, err
	}
	m.Addr = m.tcp.Addr().String()
	for _, ln := range []net.Listener{m.ln, m.tcp} {
		go func(ln net.Listener) {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				m.wg.Add(1)
				go func() { defer m.wg.Done(); m.handle(c) }()
			}
		}(ln)
	}
	return m, nil
}

// Bytes is how many bytes went through, both ways, so far.
func (m *Meter) Bytes() int64 { return m.bytes.Load() }

func (m *Meter) Close() {
	m.ln.Close()
	m.tcp.Close()
	os.Remove(m.Sock)
}

// Env points a process at the meter: through the in-sandbox forwarder under
// bubblewrap, at its loopback port bare.
func (m *Meter) Env(sandboxed bool) []string {
	addr := m.Addr
	if sandboxed {
		addr = fmt.Sprintf("127.0.0.1:%d", ProxyPort)
	}
	var env []string
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		env = append(env, k+"=http://"+addr)
	}
	return append(env, "NO_PROXY=localhost,127.0.0.1,::1", "no_proxy=localhost,127.0.0.1,::1")
}

func (m *Meter) handle(c net.Conn) {
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(30 * time.Second))
	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	if req.Method != http.MethodConnect {
		io.WriteString(c, "HTTP/1.1 405 Method Not Allowed\r\nContent-Length: 0\r\n\r\n")
		return
	}
	up, err := m.dial(req.Host)
	if err != nil {
		fmt.Fprintf(c, "HTTP/1.1 403 Forbidden\r\nContent-Length: %d\r\n\r\n%s", len(err.Error()), err.Error())
		return
	}
	defer up.Close()
	c.SetReadDeadline(time.Time{})
	io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n")
	done := make(chan struct{}, 2)
	count := func(dst io.Writer, src io.Reader) {
		n, _ := io.Copy(dst, src)
		m.bytes.Add(n)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		}
		done <- struct{}{}
	}
	if n := br.Buffered(); n > 0 {
		b, _ := br.Peek(n)
		up.Write(b)
		m.bytes.Add(int64(n))
		br.Discard(n)
	}
	go count(up, c)
	go count(c, up)
	<-done
	<-done
}

// dial reaches hostport, directly or through the gate's own proxy, refusing
// this machine and private networks. A name that resolves to any private
// address is refused; one that does not resolve here goes to the gate's proxy
// (which does its own resolving) as a name, never as an address.
func (m *Meter) dial(hostport string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return nil, err
	}
	if m.local {
		return net.DialTimeout("tcp", hostport, 30*time.Second)
	}
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return nil, fmt.Errorf("the meter does not reach this machine")
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else if addrs, err := net.LookupIP(host); err == nil {
		ips = addrs
	} else if m.upstream == nil {
		return nil, err
	}
	for _, ip := range ips {
		if !sandbox.Public(ip) {
			return nil, fmt.Errorf("the meter does not reach this machine or a private network (%s)", ip)
		}
	}
	if m.upstream == nil {
		return net.DialTimeout("tcp", net.JoinHostPort(ips[0].String(), port), 30*time.Second)
	}
	up, err := net.DialTimeout("tcp", m.upstream.Host, 30*time.Second)
	if err != nil {
		return nil, err
	}
	up.SetDeadline(time.Now().Add(30 * time.Second))
	auth := ""
	if u := m.upstream.User; u != nil {
		p, _ := u.Password()
		auth = "Proxy-Authorization: Basic " + basicAuth(u.Username(), p) + "\r\n"
	}
	fmt.Fprintf(up, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n%s\r\n", hostport, hostport, auth)
	br := bufio.NewReader(up)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil || resp.StatusCode != http.StatusOK {
		up.Close()
		if err == nil {
			err = fmt.Errorf("the gate's proxy said %s", resp.Status)
		}
		return nil, err
	}
	up.SetDeadline(time.Time{})
	if br.Buffered() > 0 {
		up.Close()
		return nil, fmt.Errorf("the gate's proxy sent data before the tunnel")
	}
	return up, nil
}

// Forward is `hive gate-fwd <sock> -- argv...`: inside a sandbox with no
// network of its own, it listens on 127.0.0.1:ProxyPort, relays each
// connection to the meter's socket, and runs argv. It is the gate's own
// binary, never a side's.
func Forward(sock string, argv []string) int {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", ProxyPort))
	if err != nil {
		fmt.Fprintln(os.Stderr, "gate-fwd:", err)
		return 125
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				up, err := net.Dial("unix", sock)
				if err != nil {
					return
				}
				defer up.Close()
				done := make(chan struct{}, 2)
				go func() { io.Copy(up, c); up.(*net.UnixConn).CloseWrite(); done <- struct{}{} }()
				go func() { io.Copy(c, up); c.(*net.TCPConn).CloseWrite(); done <- struct{}{} }()
				<-done
				<-done
			}()
		}
	}()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if x, ok := err.(*exec.ExitError); ok {
			return x.ExitCode()
		}
		fmt.Fprintln(os.Stderr, "gate-fwd:", err)
		return 127
	}
	return 0
}
