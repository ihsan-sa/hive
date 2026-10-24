// Package contracttest is a local stand-in for the report endpoint, for
// tests. It checks every request against the endpoint's contract
// (docs/report.md) and fails the test on any drift, so a client change that
// the real endpoint would refuse can't pass.
package contracttest

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"
	"unicode/utf8"
)

// Path is the report address's path. Its /v1/ is the contract's version: a
// change a receiver of v1 would refuse goes to /v2/.
const Path = "/v1/report"

// Endpoint records the reports it took; its URL is the full report address. Answer, if set, gives the status for
// the nth request (from 0); otherwise every valid report gets 202.
type Endpoint struct {
	*httptest.Server
	Answer func(n int) int
	mu     sync.Mutex
	n      int
	raw    [][]byte
	got    []Report
}

// Report is a request body as the endpoint reads it.
type Report struct {
	Install, Version, Type, Kind, OS, At, Message, Log string
	Usage                                              map[string]float64
}

// New starts an endpoint that closes with the test.
func New(t testing.TB) *Endpoint {
	e := &Endpoint{}
	e.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		rep, err := check(r, raw)
		if err != nil {
			t.Errorf("a report breaks the contract: %v\n%s", err, raw)
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		e.mu.Lock()
		code := 202
		if e.Answer != nil {
			code = e.Answer(e.n)
		}
		e.n++
		e.raw = append(e.raw, raw)
		e.got = append(e.got, rep)
		e.mu.Unlock()
		w.WriteHeader(code)
	}))
	e.URL += Path
	t.Cleanup(e.Close)
	return e
}

// Got returns the bodies and reports received so far, whatever was answered.
func (e *Endpoint) Got() ([][]byte, []Report) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([][]byte(nil), e.raw...), append([]Report(nil), e.got...)
}

var (
	install = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)
	version = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,32}$`)
	kind    = regexp.MustCompile(`^[a-z0-9_.:-]{1,64}$`)
	key     = regexp.MustCompile(`^[a-z0-9_.-]{1,64}$`)
	longest = map[string]int{"os": 64, "at": 40, "message": 4000, "log": 48000}
)

// check applies the contract. Unknown keys fail too: the endpoint drops them,
// but a client that sends them has drifted.
func check(r *http.Request, raw []byte) (Report, error) {
	var rep Report
	if r.URL.Path != Path {
		return rep, fmt.Errorf("sent to %s, not the contract's %s", r.URL.Path, Path)
	}
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); r.Method != "POST" || mt != "application/json" {
		return rep, fmt.Errorf("%s with %q", r.Method, r.Header.Get("Content-Type"))
	}
	if len(raw) > 64<<10 {
		return rep, fmt.Errorf("%d bytes", len(raw))
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return rep, err
	}
	str := func(k string) (string, error) {
		var s string
		if v, ok := m[k]; ok {
			if err := json.Unmarshal(v, &s); err != nil {
				return "", fmt.Errorf("%s: %v", k, err)
			}
			if n, ok := longest[k]; ok && utf8.RuneCountInString(s) > n {
				return "", fmt.Errorf("%s: %d characters", k, utf8.RuneCountInString(s))
			}
		}
		return s, nil
	}
	for k := range m {
		if !map[string]bool{"install": true, "version": true, "type": true, "kind": true, "os": true, "at": true, "message": true, "log": true, "usage": true}[k] {
			return rep, fmt.Errorf("unknown key %q", k)
		}
	}
	var err error
	for k, p := range map[string]*string{"install": &rep.Install, "version": &rep.Version, "type": &rep.Type, "kind": &rep.Kind, "os": &rep.OS, "at": &rep.At, "message": &rep.Message, "log": &rep.Log} {
		if *p, err = str(k); err != nil {
			return rep, err
		}
	}
	switch {
	case !install.MatchString(rep.Install):
		return rep, fmt.Errorf("install %q", rep.Install)
	case !version.MatchString(rep.Version):
		return rep, fmt.Errorf("version %q", rep.Version)
	case rep.Type != "failure" && rep.Type != "usage":
		return rep, fmt.Errorf("type %q", rep.Type)
	case rep.Type == "failure" && !kind.MatchString(rep.Kind):
		return rep, fmt.Errorf("kind %q", rep.Kind)
	}
	if v, ok := m["usage"]; ok {
		if err := json.Unmarshal(v, &rep.Usage); err != nil || len(rep.Usage) > 50 {
			return rep, fmt.Errorf("usage: %v (%d keys)", err, len(rep.Usage))
		}
		for k := range rep.Usage {
			if !key.MatchString(k) {
				return rep, fmt.Errorf("usage key %q", k)
			}
		}
	}
	return rep, nil
}
