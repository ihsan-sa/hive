package sandbox

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Egress is the host-side proxy that is a sandbox's only way out. It accepts
// HTTP CONNECT and plain HTTP proxy requests, resolves the destination itself,
// refuses anything that resolves to this machine or a private network, and
// dials the exact address it vetted (so DNS rebinding can't swap it). With an
// allow list it also refuses every host not on it. The one way to a service on
// this machine is a local grant: a loopback ip:port the person named
// (LocalGrant), dialled as that literal address and nothing else.
type Egress struct {
	ln      net.Listener
	lookup  func(ctx context.Context, host string) ([]net.IPAddr, error) // net.DefaultResolver's; a test's own
	allow   []string
	local   []string // granted loopback ip:port, canonical
	mu      sync.Mutex
	refused io.Writer // one line per refusal, "host:port why"; nil keeps none
}

// Why a destination was refused, as recorded after it.
const (
	RefusedUnlisted = "unlisted" // not on the run's egress list
	RefusedLocal    = "local"    // this machine or a private network, with no grant
)

// ServeEgress serves the proxy on ln until it is closed. A nil allow list
// lets every public host through; local is the granted loopback ip:port
// (LocalGrant's); refused, if set, gets each refusal and its reason.
func ServeEgress(ln net.Listener, allow, local []string, refused io.Writer) *Egress {
	e := &Egress{ln: ln, lookup: net.DefaultResolver.LookupIPAddr, allow: allow, local: local, refused: refused}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go e.handle(c)
		}
	}()
	return e
}

// Close stops the proxy and closes where refusals go.
func (e *Egress) Close() error {
	if c, ok := e.refused.(io.Closer); ok {
		c.Close()
	}
	return e.ln.Close()
}

func (e *Egress) handle(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	if req.Method == http.MethodConnect {
		up, err := e.dial(req.Host)
		if err != nil {
			fmt.Fprintf(c, "HTTP/1.1 403 Forbidden\r\nContent-Length: %d\r\n\r\n%s", len(err.Error()), err.Error())
			return
		}
		defer up.Close()
		_ = c.SetDeadline(time.Time{})
		io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
		pipe(c, br, up)
		return
	}
	// Plain HTTP proxying: absolute-form request.
	if req.URL == nil || req.URL.Host == "" {
		io.WriteString(c, "HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n")
		return
	}
	host := req.URL.Host
	if !strings.Contains(host, ":") {
		host += ":80"
	}
	up, err := e.dial(host)
	if err != nil {
		fmt.Fprintf(c, "HTTP/1.1 403 Forbidden\r\nContent-Length: %d\r\n\r\n%s", len(err.Error()), err.Error())
		return
	}
	defer up.Close()
	_ = c.SetDeadline(time.Time{})
	req.RequestURI = ""
	req.Header.Del("Proxy-Connection")
	req.Header.Del("Proxy-Authorization")
	if err := req.Write(up); err != nil {
		return
	}
	pipe(c, br, up)
}

func pipe(c net.Conn, br *bufio.Reader, up net.Conn) {
	done := make(chan struct{}, 2)
	go func() { io.Copy(up, br); closeWrite(up); done <- struct{}{} }()
	go func() { io.Copy(c, up); closeWrite(c); done <- struct{}{} }()
	<-done
	<-done
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		cw.CloseWrite()
	}
}

// dial resolves hostport, rejects local and private destinations, and connects.
func (e *Egress) dial(hostport string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return nil, fmt.Errorf("bad destination %q", hostport)
	}
	if addrs := granted(e.local, host, port); len(addrs) > 0 {
		var lastErr error
		for _, a := range addrs {
			c, err := net.DialTimeout("tcp", a, 15*time.Second)
			if err == nil {
				return c, nil
			}
			lastErr = err
		}
		return nil, fmt.Errorf("%s is granted but nothing answers there: %v", hostport, lastErr)
	}
	// A local address named outright is refused as local, list or not, so
	// the record says what would open it.
	if ip := net.ParseIP(host); strings.EqualFold(host, "localhost") || ip != nil && !Public(ip) {
		e.refuse(hostport, RefusedLocal)
		return nil, localErr(host, hostport)
	}
	if !Allowed(e.allow, host) {
		e.refuse(hostport, RefusedUnlisted)
		return nil, fmt.Errorf("%s is not on this run's egress list", host)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ips, err := e.lookup(ctx, host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("cannot resolve %s", host)
	}
	for _, ip := range ips {
		if !Public(ip.IP) {
			e.refuse(hostport, RefusedLocal)
			return nil, localErr(fmt.Sprintf("%s (%s)", host, ip.IP), net.JoinHostPort(ip.IP.String(), port))
		}
	}
	var lastErr error
	for _, ip := range ips {
		c, err := net.DialTimeout("tcp", net.JoinHostPort(ip.IP.String(), port), 15*time.Second)
		if err == nil {
			return c, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// localErr is the refusal of a local or private destination, with the way a
// service on this machine opens.
func localErr(dest, grant string) error {
	return fmt.Errorf("destination %s is local or private; sandboxes reach the network outbound only. "+
		"A service on this machine opens to agents only on the person's grant: ask a warden for `hive egress grant %s`", dest, grant)
}

func (e *Egress) refuse(hostport, why string) {
	if e.refused == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	fmt.Fprintln(e.refused, hostport, why)
}

// LocalGrant reads a grant of a service on this machine: "127.0.0.1:11434",
// "[::1]:11434" or "localhost:11434" (both loopback addresses). It returns
// the canonical ip:port it opens, and refuses anything but a loopback address
// with a port, so a grant can't name a host whose address could change.
func LocalGrant(entry string) ([]string, error) {
	host, port, err := net.SplitHostPort(strings.TrimSpace(entry))
	if err != nil {
		return nil, fmt.Errorf("local grant %q isn't host:port", entry)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return nil, fmt.Errorf("local grant %q has no port", entry)
	}
	if strings.EqualFold(host, "localhost") {
		return []string{net.JoinHostPort("127.0.0.1", port), net.JoinHostPort("::1", port)}, nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("local grant %q isn't a loopback address (127.0.0.1, ::1 or localhost)", entry)
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	return []string{net.JoinHostPort(ip.String(), port)}, nil
}

// granted is the granted addresses host:port asks for: host as a loopback
// literal or "localhost", never a name that merely resolves to one.
func granted(local []string, host, port string) []string {
	if len(local) == 0 {
		return nil
	}
	want, err := LocalGrant(net.JoinHostPort(host, port))
	if err != nil {
		return nil
	}
	var out []string
	for _, w := range want {
		if slices.Contains(local, w) {
			out = append(out, w)
		}
	}
	return out
}

// Allowed reports whether host is on allow: an entry matches itself and its
// subdomains. A nil list allows every host; an empty one, none.
func Allowed(allow []string, host string) bool {
	if allow == nil {
		return true
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, a := range allow {
		a = strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(a, "*."), "."))
		if host == a || strings.HasSuffix(host, "."+a) {
			return true
		}
	}
	return false
}

var blocked = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.0.0.0/24", "192.168.0.0/16", "198.18.0.0/15", "224.0.0.0/4", "240.0.0.0/4",
		"::/128", "::1/128", "fc00::/7", "fe80::/10", "ff00::/8", "64:ff9b:1::/48",
	} {
		_, n, _ := net.ParseCIDR(c)
		out = append(out, n)
	}
	return out
}()

// Public reports whether ip is a routable public address.
func Public(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, n := range blocked {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

// forwardLocal listens inside the sandbox on each granted local address and
// relays it through the egress proxy, so a client that never proxies loopback
// (Go's, for one) reaches the host's service at the address it already knows.
// An address it can't bind (::1 where loopback has no IPv6) is skipped with a
// line to warn, never fatal: the run still starts, and the proxy still serves
// the grant. It returns the listeners it holds.
func forwardLocal(local []string, sock string, warn io.Writer) []net.Listener {
	var held []net.Listener
	for _, a := range local {
		ln, err := net.Listen("tcp", a)
		if err != nil {
			fmt.Fprintf(warn, "sandbox: granted local %s not forwarded inside (reach it through the proxy): %v\n", a, err)
			continue
		}
		held = append(held, ln)
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				go relayConnect(c, sock, a)
			}
		}()
	}
	return held
}

// relayConnect opens a CONNECT to dest through the proxy at sock and pipes c over it.
func relayConnect(c net.Conn, sock, dest string) {
	defer c.Close()
	u, err := net.Dial("unix", sock)
	if err != nil {
		return
	}
	defer u.Close()
	fmt.Fprintf(u, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", dest, dest)
	br := bufio.NewReader(u)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != http.StatusOK {
		return
	}
	done := make(chan struct{}, 2)
	go func() { io.Copy(u, c); closeWrite(u); done <- struct{}{} }()
	go func() { io.Copy(c, br); closeWrite(c); done <- struct{}{} }()
	<-done
	<-done
}
