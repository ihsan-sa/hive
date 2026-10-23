package sandbox

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Egress is the host-side proxy that is a sandbox's only way out. It accepts
// HTTP CONNECT and plain HTTP proxy requests, resolves the destination itself,
// refuses anything that resolves to this machine or a private network, and
// dials the exact address it vetted (so DNS rebinding can't swap it). With an
// allow list it also refuses every host not on it.
type Egress struct {
	ln       net.Listener
	resolver *net.Resolver
	allow    []string
	mu       sync.Mutex
	refused  io.Writer // one line per refused host; nil keeps none
}

// ServeEgress serves the proxy on ln until it is closed. An empty allow list
// lets every public host through; refused, if set, gets each refused host.
func ServeEgress(ln net.Listener, allow []string, refused io.Writer) *Egress {
	e := &Egress{ln: ln, resolver: net.DefaultResolver, allow: allow, refused: refused}
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
	if !Allowed(e.allow, host) {
		e.refuse(host)
		return nil, fmt.Errorf("%s is not on this run's egress list", host)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ips, err := e.resolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("cannot resolve %s", host)
	}
	for _, ip := range ips {
		if !Public(ip.IP) {
			e.refuse(host)
			return nil, fmt.Errorf("destination %s (%s) is local or private; sandboxes reach the network outbound only", host, ip.IP)
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

func (e *Egress) refuse(host string) {
	if e.refused == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	fmt.Fprintln(e.refused, host)
}

// Allowed reports whether host is on allow: an entry matches itself and its
// subdomains. An empty list allows every host.
func Allowed(allow []string, host string) bool {
	if len(allow) == 0 {
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
