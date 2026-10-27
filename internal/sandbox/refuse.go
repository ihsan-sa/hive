package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// localSuffixes are names that only mean something on the person's own
// networks: Tailscale's MagicDNS, mDNS, and the usual home and corporate ones.
var localSuffixes = []string{".localhost", ".localdomain", ".local", ".lan", ".home", ".home.arpa", ".internal",
	".intranet", ".corp", ".private", ".ts.net", ".beta.tailscale.net", ".in-addr.arpa", ".ip6.arpa"}

// LocalName is why host, a name, is refused before it is resolved, or "": a
// local name, or a single label (a search-domain or MagicDNS host).
func LocalName(host string) string {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if net.ParseIP(h) != nil {
		return ""
	}
	if h == "localhost" {
		return "this machine"
	}
	for _, s := range localSuffixes {
		if strings.HasSuffix(h, s) {
			return "a local name"
		}
	}
	if !strings.Contains(h, ".") {
		return "a single-label name (a local or MagicDNS host)"
	}
	return ""
}

// Refused is why ip is out of reach, or "": anything but the public internet
// (Public), or one of this machine's own addresses (own), a public or LAN one
// included.
func Refused(ip net.IP, own []net.IP) string {
	if !Public(ip) {
		return "local or private"
	}
	for _, a := range append([]net.IP{ip}, embedded(ip)...) {
		for _, o := range own {
			if o.Equal(a) {
				return "this machine"
			}
		}
	}
	return ""
}

var own struct {
	sync.Mutex
	at    time.Time
	addrs []net.IP
}

// OwnAddrs is every address this machine holds: each interface's (the LAN,
// tailscale0, a public one, IPv4 and IPv6) and what its hostname resolves to.
// It is read again after a few seconds, so an address that comes up mid-run
// counts almost at once.
func OwnAddrs() []net.IP {
	own.Lock()
	defer own.Unlock()
	if !own.at.IsZero() && time.Since(own.at) < 5*time.Second {
		return own.addrs
	}
	var out []net.IP
	if as, err := net.InterfaceAddrs(); err == nil {
		for _, a := range as {
			if n, ok := a.(*net.IPNet); ok {
				out = append(out, n.IP)
			}
		}
	}
	if h, err := os.Hostname(); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if ips, err := net.DefaultResolver.LookupIPAddr(ctx, h); err == nil {
			for _, ip := range ips {
				out = append(out, ip.IP)
			}
		}
		cancel()
	}
	own.at, own.addrs = time.Now(), out
	return out
}

// doh is public DNS over HTTPS (the JSON API), asked when this machine can't
// resolve a name itself, so a name isn't handed on unchecked.
var doh = []string{"https://dns.google/resolve", "https://cloudflare-dns.com/dns-query"}

// PublicDNS is host's A and AAAA addresses in public DNS, asked over HTTPS
// through proxy when it is set. It fails when no resolver answers; a name
// public DNS doesn't know comes back empty.
func PublicDNS(ctx context.Context, host string, proxy *url.URL) ([]net.IP, error) {
	tr := &http.Transport{}
	if proxy != nil {
		tr.Proxy = http.ProxyURL(proxy)
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 10 * time.Second}
	var last error
	for _, base := range doh {
		var out []net.IP
		var err error
		for _, qt := range []struct {
			name string
			code int
		}{{"A", 1}, {"AAAA", 28}} {
			var ips []net.IP
			if ips, err = dohAsk(ctx, client, base, host, qt.name, qt.code); err != nil {
				break
			}
			out = append(out, ips...)
		}
		if err == nil {
			return out, nil
		}
		last = err
	}
	return nil, fmt.Errorf("no public DNS answered: %v", last)
}

func dohAsk(ctx context.Context, client *http.Client, base, host, qtype string, code int) ([]net.IP, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"?"+url.Values{"name": {host}, "type": {qtype}}.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/dns-json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s said %s", base, resp.Status)
	}
	var ans struct {
		Answer []struct {
			Type int    `json:"type"`
			Data string `json:"data"`
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&ans); err != nil {
		return nil, err
	}
	var out []net.IP
	for _, a := range ans.Answer {
		if ip := net.ParseIP(a.Data); a.Type == code && ip != nil {
			out = append(out, ip)
		}
	}
	return out, nil
}
