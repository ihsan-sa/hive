package sandbox

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPublic(t *testing.T) {
	for ip, want := range map[string]bool{
		"8.8.8.8": true, "1.1.1.1": true, "2606:4700::1111": true,
		"127.0.0.1": false, "10.1.2.3": false, "192.168.1.1": false, "172.20.0.1": false,
		"169.254.169.254": false, "100.100.1.1": false, "::1": false, "fe80::1": false, "fd00::1": false,
		"::ffff:127.0.0.1": false, "0.0.0.0": false,
	} {
		if got := Public(net.ParseIP(ip)); got != want {
			t.Errorf("Public(%s) = %v, want %v", ip, got, want)
		}
	}
}

// The proxy refuses to reach a service on this machine.
func TestEgressRefusesLocal(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	e := ServeEgress(ln, nil, nil, nil)
	defer e.Close()
	for _, dest := range []string{target.Addr().String(), "localhost:" + strings.Split(target.Addr().String(), ":")[1]} {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", dest, dest)
		resp, err := http.ReadResponse(bufio.NewReader(c), nil)
		c.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("CONNECT %s: %s, want 403", dest, resp.Status)
		}
	}
}

// The proxy reaches public hosts (skipped without network).
func TestEgressReachesPublic(t *testing.T) {
	if _, err := net.LookupHost("github.com"); err != nil {
		t.Skip("no network")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	e := ServeEgress(ln, nil, nil, nil)
	defer e.Close()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintf(c, "CONNECT github.com:443 HTTP/1.1\r\nHost: github.com:443\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("CONNECT github.com: %s", resp.Status)
	}
}

func TestAllowed(t *testing.T) {
	allow := []string{"github.com", "*.pypi.org", "API.Anthropic.com"}
	for host, want := range map[string]bool{
		"github.com": true, "codeload.github.com": true, "files.pypi.org": true, "pypi.org": true,
		"api.anthropic.com.": true, "evilgithub.com": false, "github.com.evil.net": false, "example.com": false,
	} {
		if got := Allowed(allow, host); got != want {
			t.Errorf("Allowed(%s) = %v, want %v", host, got, want)
		}
	}
	if !Allowed(nil, "example.com") {
		t.Error("no list should allow every host")
	}
	if Allowed([]string{}, "example.com") {
		t.Error("an empty list should allow no host")
	}
}

// A host off the run's list is refused before it is resolved, and recorded.
func TestEgressHoldsToItsList(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var refused bytes.Buffer
	e := ServeEgress(ln, []string{"github.com"}, nil, &refused)
	defer e.Close()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintf(c, "CONNECT paste.example:443 HTTP/1.1\r\nHost: paste.example:443\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusForbidden || refused.String() != "paste.example:443 unlisted\n" {
		t.Fatalf("CONNECT paste.example: %s, recorded %q", resp.Status, refused.String())
	}
}

// echo serves one line back per connection on a loopback port.
func echo(t *testing.T) (net.Listener, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				line, _ := bufio.NewReader(c).ReadString('\n')
				io.WriteString(c, "echo "+line)
			}()
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return ln, port
}

// connect asks the proxy at addr (tcp, or unix when it starts with /) for
// dest and, when let through, sends a line and returns the reply.
func connect(t *testing.T, network, addr, dest string) (int, string) {
	t.Helper()
	c, err := net.Dial(network, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", dest, dest)
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	io.WriteString(c, "hi\n")
	line, _ := br.ReadString('\n')
	return resp.StatusCode, line
}

// A granted loopback port is reached, by its address or as localhost, even
// past an egress list; an ungranted one is refused, recorded as local, and
// the refusal says how it would open.
func TestEgressLocalGrant(t *testing.T) {
	_, ollama := echo(t)
	_, other := echo(t)
	local, err := LocalGrant("127.0.0.1:" + ollama)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var refused bytes.Buffer
	e := ServeEgress(ln, []string{"github.com"}, local, &refused)
	defer e.Close()
	for _, dest := range []string{"127.0.0.1:" + ollama, "localhost:" + ollama} {
		if code, got := connect(t, "tcp", ln.Addr().String(), dest); code != 200 || got != "echo hi\n" {
			t.Errorf("granted %s: %d %q", dest, code, got)
		}
	}
	code, body := connect(t, "tcp", ln.Addr().String(), "127.0.0.1:"+other)
	if code != http.StatusForbidden || !strings.Contains(body, "egress grant 127.0.0.1:"+other) {
		t.Errorf("ungranted port: %d %q, want 403 naming the grant", code, body)
	}
	if want := "127.0.0.1:" + other + " local\n"; refused.String() != want {
		t.Errorf("recorded %q, want %q", refused.String(), want)
	}
}

func TestLocalGrant(t *testing.T) {
	for entry, want := range map[string][]string{
		"127.0.0.1:11434": {"127.0.0.1:11434"},
		"[::1]:11434":     {"[::1]:11434"},
		"LocalHost:11434": {"127.0.0.1:11434", "[::1]:11434"},
	} {
		if got, err := LocalGrant(entry); err != nil || !slices.Equal(got, want) {
			t.Errorf("LocalGrant(%s) = %v, %v; want %v", entry, got, err, want)
		}
	}
	for _, bad := range []string{"127.0.0.1", "192.168.1.5:11434", "ollama.local:11434", "8.8.8.8:53", "127.0.0.1:0", "127.0.0.1:x"} {
		if _, err := LocalGrant(bad); err == nil {
			t.Errorf("LocalGrant(%s) was accepted", bad)
		}
	}
}

// Inside a Linux sandbox a granted address is a loopback listener relayed
// over the egress socket, so a client that never proxies loopback reaches it.
func TestRelayConnect(t *testing.T) {
	_, port := echo(t)
	dest := "127.0.0.1:" + port
	sock := filepath.Join(t.TempDir(), "egress.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip("no unix sockets:", err)
	}
	e := ServeEgress(ln, []string{}, []string{dest}, nil)
	defer e.Close()
	in, out := net.Pipe()
	go relayConnect(out, sock, dest)
	io.WriteString(in, "hi\n")
	if line, _ := bufio.NewReader(in).ReadString('\n'); line != "echo hi\n" {
		t.Fatalf("relayed %q", line)
	}
	in.Close()
}

// A granted address the sandbox can't bind (::1 with IPv6 off, or one in
// use) is skipped with a warning; the others are still forwarded.
func TestForwardLocalSkipsWhatItCantBind(t *testing.T) {
	_, port := echo(t) // 127.0.0.1:port is taken, so binding it fails
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ok := free.Addr().String()
	free.Close()
	sock := filepath.Join(t.TempDir(), "egress.sock")
	var warn bytes.Buffer
	held := forwardLocal([]string{"127.0.0.1:" + port, ok}, sock, &warn)
	for _, ln := range held {
		defer ln.Close()
	}
	if len(held) != 1 || held[0].Addr().String() != ok {
		t.Fatalf("held %v, want only %s", held, ok)
	}
	if !strings.Contains(warn.String(), "127.0.0.1:"+port+" not forwarded") {
		t.Fatalf("warned %q", warn.String())
	}
}
