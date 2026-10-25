// Command dream is the kb module's consolidation pass: the knowledge base
// "dreams". It runs as the module's program (module.json "exec"), reads the
// journal through the control API like any module, folds it into a small
// state in its HOME, and at its schedule (module.scheduled kb.dream, "every
// 4h" by default) or at a quiet moment (hive.idle, at most hourly) works over
// the whole knowledge base: it merges duplicate notes into one living note,
// retires notes the record has since contradicted, distils the record into
// per-project summaries and a practice note, links related notes both ways,
// and writes a digest of what the Hive learned since the last pass under
// dreams/. The kb module checks and commits each pass (POST /api/kb/dream)
// and journals it as kb.dreamed.
//
// It uses only the standard library and the HTTP API, so it can move to a
// repository of its own unchanged. It runs as the person with no module
// review, so here every change to it gets a security read
// (tests/LANDING-security.toml lists modules/kb/dream/**); a repository it
// moves to needs the same rule.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	api   = os.Getenv("HIVE_API")
	token = os.Getenv("HIVE_MODULE_TOKEN")
	me    = os.Getenv("HIVE_MODULE")
	home  = os.Getenv("HOME")
)

// quietGap is the least time between passes started at a quiet moment.
const quietGap = time.Hour

// recent is how old a trigger may be and still start a pass, so catching up
// on old events doesn't dream once per old hive.idle.
const recent = 10 * time.Minute

func call(method, path string, body, out any) (int, error) {
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, api+path, bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct{ Error string }
		json.NewDecoder(resp.Body).Decode(&e)
		return resp.StatusCode, fmt.Errorf("%s: %d %s", path, resp.StatusCode, e.Error)
	}
	if out != nil {
		return resp.StatusCode, json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode, nil
}

func statePath() string { return filepath.Join(home, "dream.json") }

func load() *State {
	s := newState()
	if raw, err := os.ReadFile(statePath()); err == nil {
		json.Unmarshal(raw, s)
	}
	if s.Tasks == nil || s.Runs == nil || s.Steps == nil || s.Alerts == nil {
		f := newState()
		s.Tasks, s.Runs, s.Steps, s.Alerts = or(s.Tasks, f.Tasks), or(s.Runs, f.Runs), or(s.Steps, f.Steps), or(s.Alerts, f.Alerts)
	}
	return s
}

func or[M ~map[K]V, K comparable, V any](m, d M) M {
	if m == nil {
		return d
	}
	return m
}

func save(s *State) {
	raw, _ := json.Marshal(s)
	tmp := statePath() + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		os.Rename(tmp, statePath())
	}
}

func report(text string) { call("POST", "/api/module/report", map[string]string{"text": text}, nil) }

func main() {
	st := load()
	if st.Born.IsZero() {
		st.Born = time.Now().UTC()
		save(st)
	}
	for misses := 0; misses < 10; {
		var evs []Event
		code, err := call("GET", fmt.Sprintf("/api/events?after=%d", st.Cursor), nil, &evs)
		if code == http.StatusUnauthorized || code == http.StatusForbidden {
			return // a token from an earlier start; the daemon starts a fresh one
		}
		if err != nil {
			misses++ // the daemon is gone; it starts this program again when it is back
			time.Sleep(2 * time.Second)
			continue
		}
		misses = 0
		trigger := ""
		for _, e := range evs {
			job := st.fold(e, me)
			switch {
			case time.Since(e.At) > recent:
			case job == "kb.dream":
				trigger = "scheduled"
			case job == "idle" && trigger == "" && time.Since(st.Last) >= quietGap && time.Since(st.Born) >= quietGap:
				trigger = "quiet"
			}
		}
		if len(evs) > 0 {
			save(st)
		}
		if trigger != "" {
			dream(st, trigger)
		}
		if len(evs) < 2000 { // caught up
			time.Sleep(2 * time.Second)
		}
	}
}

// dream runs one pass. A pass refused because a note changed since it was
// read is worked out again once over the new notes.
func dream(st *State, trigger string) {
	for try := 0; try < 2; try++ {
		var notes struct {
			Notes   []Shared
			Shipped []string
			Hidden  []string
		}
		if _, err := call("GET", "/api/kb/notes", nil, &notes); err != nil {
			report("dream: could not read the notes: " + err.Error())
			return
		}
		now := time.Now().UTC()
		p := Plan(Input{Notes: notes.Notes, Shipped: notes.Shipped, Hidden: notes.Hidden, State: st, Now: now, Trigger: trigger})
		var result string
		_, err := call("POST", "/api/kb/dream", p, &result)
		if err != nil && strings.Contains(err.Error(), "stale:") && try == 0 {
			continue
		}
		if err != nil {
			report("dream: the pass was refused: " + err.Error())
			return
		}
		st.Last = now
		save(st)
		return
	}
}
