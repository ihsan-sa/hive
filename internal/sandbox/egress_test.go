package sandbox

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"net/http"
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
	e := ServeEgress(ln, nil, nil)
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
	e := ServeEgress(ln, nil, nil)
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
		t.Error("an empty list should allow every host")
	}
}

// A host off the run's list is refused before it is resolved, and recorded.
func TestEgressHoldsToItsList(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var refused bytes.Buffer
	e := ServeEgress(ln, []string{"github.com"}, &refused)
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
	if resp.StatusCode != http.StatusForbidden || refused.String() != "paste.example\n" {
		t.Fatalf("CONNECT paste.example: %s, recorded %q", resp.Status, refused.String())
	}
}
