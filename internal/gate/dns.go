package gate

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// doh is public DNS over HTTPS (the JSON API), asked when this machine can't
// resolve a name itself, so a name isn't handed on unchecked.
var doh = []string{"https://dns.google/resolve", "https://cloudflare-dns.com/dns-query"}

// publicDNS is host's A and AAAA addresses in public DNS, asked over HTTPS
// through proxy when it is set. It fails when no resolver answers; a name
// public DNS doesn't know comes back empty.
func publicDNS(ctx context.Context, host string, proxy *url.URL) ([]net.IP, error) {
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
