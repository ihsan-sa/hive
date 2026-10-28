package sandbox

import (
	"context"
	"net"
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
	at         time.Time
	refreshing bool
	named      []net.IP // what the hostname resolves to
}

// OwnAddrs is every address this machine holds: each interface's (the LAN,
// tailscale0, a public one, IPv4 and IPv6), read at every call, and what its
// hostname resolves to, looked up again in the background every few seconds
// so no dial waits on it.
func OwnAddrs() []net.IP {
	var out []net.IP
	if as, err := net.InterfaceAddrs(); err == nil {
		for _, a := range as {
			if n, ok := a.(*net.IPNet); ok {
				out = append(out, n.IP)
			}
		}
	}
	own.Lock()
	defer own.Unlock()
	if !own.refreshing && time.Since(own.at) >= 5*time.Second {
		own.refreshing = true
		go refreshNamed()
	}
	return append(out, own.named...)
}

func refreshNamed() {
	var named []net.IP
	if h, err := os.Hostname(); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if ips, err := net.DefaultResolver.LookupIPAddr(ctx, h); err == nil {
			for _, ip := range ips {
				named = append(named, ip.IP)
			}
		}
		cancel()
	}
	own.Lock()
	defer own.Unlock()
	own.at, own.named, own.refreshing = time.Now(), named, false
}
