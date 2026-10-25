package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

// twoLogins is a token source with the machine's own login and one added.
type twoLogins struct{}

func (twoLogins) Logins(h string) []Login {
	return []Login{{Harness: "claude"}, {Harness: "claude", Name: "work"}}
}
func (twoLogins) AgentEnv(l Login, home string) ([]string, error) {
	return []string{"TOKEN=" + l.Key()}, nil
}

// A login at its usage limit is parked until its reset while the others take
// the runs; with every login parked, runs wait for the first reset.
func TestLoginPoolParksALimitedLogin(t *testing.T) {
	ws := Workspace{Root: t.TempDir()}
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	rs := NewRuns(ws, j, nil, twoLogins{}, func() []string { return nil })
	park := func(login string, reset time.Time) {
		rs.append("plan.usage", Plan{Harness: "claude", Login: login, Limit: harness.RateLimit{Status: "rejected", ResetsAt: reset}})
	}

	if l, _, _ := rs.pickLogin("claude", "", t.TempDir(), false); l.Key() != "claude" {
		t.Fatalf("with room on both, the machine's own login goes first, got %s", l.Key())
	}
	soon, later := time.Now().Add(time.Hour), time.Now().Add(2*time.Hour)
	park("claude", later)
	l, env, err := rs.pickLogin("claude", "", t.TempDir(), false)
	if err != nil || l.Key() != "claude:work" || env[0] != "TOKEN=claude:work" {
		t.Fatalf("a parked login is passed over: %v %v %v", l, env, err)
	}
	if time.Until(rs.freeAt("claude", "")) > time.Second {
		t.Fatal("a run limited on one login moves to the other at once")
	}
	park("claude:work", soon)
	if _, _, err := rs.pickLogin("claude", "", t.TempDir(), false); err == nil {
		t.Fatal("with every login parked, no login is handed out")
	}
	if at := rs.freeAt("claude", ""); at.Before(soon) || at.After(soon.Add(time.Minute)) {
		t.Fatalf("runs wait for the first reset, got %v want ~%v", at, soon)
	}
	park("claude", time.Now().Add(-time.Minute))
	if l, _, _ := rs.pickLogin("claude", "", t.TempDir(), false); l.Key() != "claude" {
		t.Fatalf("a login whose reset has passed takes runs again, got %s", l.Key())
	}
}

// noOwnLogin is a token source whose machine login has no credential file.
type noOwnLogin struct{}

func (noOwnLogin) Logins(h string) []Login { return []Login{{Harness: "claude"}} }
func (noOwnLogin) AgentEnv(l Login, home string) ([]string, error) {
	return nil, ErrNoLogin
}

// A bare run with no credential file to read runs on the host's own login,
// with no token set; a sandboxed one has nothing to run on.
func TestBareRunKeepsTheHostLogin(t *testing.T) {
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	rs := NewRuns(Workspace{Root: t.TempDir()}, j, nil, noOwnLogin{}, func() []string { return nil })
	if l, env, err := rs.pickLogin("claude", "", "", true); err != nil || l.Key() != "claude" || len(env) != 0 {
		t.Fatalf("bare: %v %v %v", l, env, err)
	}
	if _, _, err := rs.pickLogin("claude", "", "", false); err == nil {
		t.Fatal("a sandboxed run started with no credential")
	}
}

// A turn limited on one login while another has room is told its turn moved,
// not that the limit has reset.
func TestLimitedTurnMovesToAnotherLogin(t *testing.T) {
	ws := Workspace{Root: t.TempDir()}
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	rs := NewRuns(ws, j, nil, twoLogins{}, func() []string { return nil })
	r := Run{RunSpec: RunSpec{ID: "r1", Harness: "claude"}, Turn: 1, Login: "claude"}
	out := ws.TurnOut(r.ID, 1)
	os.MkdirAll(filepath.Dir(out), 0o755)
	os.WriteFile(out, []byte(`{"type":"result","subtype":"success","is_error":true,"result":"You've hit your usage limit"}`+"\n"), 0o600)
	rs.endTurn(r)
	var te turnEnded
	j.Replay(func(e Event) {
		if e.Kind == "run.turn_ended" {
			e.Decode(&te)
		}
	})
	if te.Status != StatusParked || len(te.Queue) != 1 || !strings.Contains(te.Queue[0], "another login") {
		t.Fatalf("turn ended %+v", te)
	}
}

// Least-used routing reads the binding window's utilization, including from a
// reading journaled before the parse fix with 0 at the top level.
func TestLoginPoolRoutesByWindowUsage(t *testing.T) {
	ws := Workspace{Root: t.TempDir()}
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	rs := NewRuns(ws, j, nil, twoLogins{}, func() []string { return nil })
	reset := time.Now().Add(3 * time.Hour)
	used := func(login string, u float64) {
		rs.append("plan.usage", Plan{Harness: "claude", Login: login, Limit: harness.RateLimit{Status: "allowed", ResetsAt: reset, Window: "five_hour",
			Windows: map[string]harness.Window{"five_hour": {Utilization: u, ResetsAt: reset}}}})
	}
	used("claude", 0.28)
	used("claude:work", 0.05)
	if l, _, _ := rs.pickLogin("claude", "", t.TempDir(), false); l.Key() != "claude:work" {
		t.Fatalf("the less used login takes the run, got %s", l.Key())
	}
	for _, p := range rs.Plans() {
		if p.Key() == "claude" && p.Limit.Utilization != 0.28 {
			t.Fatalf("plan claude reads %v, want 0.28", p.Limit.Utilization)
		}
	}
}

// h0Usage is a plan.usage record as H0's journal holds it (seq 183,
// 2026-10-24): Claude leaves the top-level utilization at 0 and reports the
// real figures per window.
const h0Usage = `{"harness":"claude","login":"claude","limit":{"status":"allowed","resets_at":"2026-10-24T05:40:00Z","window":"five_hour","utilization":0,"windows":{"five_hour":{"utilization":0.29,"resets_at":"2026-10-24T05:40:00Z"},"seven_day":{"utilization":0.36,"resets_at":"2026-10-24T17:00:00Z"}}},"at":"2026-10-24T02:10:29.06537972Z"}`

// Runs go on the least-used login by its windows' figures, so the machine's
// own login at 36% of its week yields to an added one at 10%; once those
// windows reset, it counts as unused again.
func TestLoginPoolPicksTheLeastUsedByItsWindows(t *testing.T) {
	var own Plan
	if err := json.Unmarshal([]byte(h0Usage), &own); err != nil {
		t.Fatal(err)
	}
	recorded := time.Date(2026, 10, 24, 2, 10, 29, 0, time.UTC)
	if u := own.Used(recorded); u != 0.36 {
		t.Fatalf("H0's record reads as %v used, want 0.36", u)
	}
	if u := own.Used(time.Date(2026, 10, 24, 6, 0, 0, 0, time.UTC)); u != 0.36 {
		t.Fatalf("after the five-hour reset the week still counts: %v", u)
	}
	if u := own.Used(time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)); u != 0 {
		t.Fatalf("after every reset nothing counts: %v", u)
	}

	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	rs := NewRuns(Workspace{Root: t.TempDir()}, j, nil, twoLogins{}, func() []string { return nil })
	shift := time.Since(recorded) // replay the record as if it were just taken: its resets move forward by its age
	own.Limit.ResetsAt = own.Limit.ResetsAt.Add(shift)
	for k, w := range own.Limit.Windows {
		w.ResetsAt = w.ResetsAt.Add(shift)
		own.Limit.Windows[k] = w
	}
	rs.append("plan.usage", own)
	work := Plan{Harness: "claude", Login: "claude:work", Limit: harness.RateLimit{Status: "allowed", Windows: map[string]harness.Window{
		"five_hour": {Utilization: 0.05, ResetsAt: time.Now().Add(time.Hour)},
		"seven_day": {Utilization: 0.10, ResetsAt: time.Now().Add(48 * time.Hour)},
	}}}
	rs.append("plan.usage", work)
	if l, _, _ := rs.pickLogin("claude", "", t.TempDir(), false); l.Key() != "claude:work" {
		t.Fatalf("the less-used added login goes first, got %s", l.Key())
	}
	work.Limit.Windows["seven_day"] = harness.Window{Utilization: 0.50, ResetsAt: time.Now().Add(48 * time.Hour)}
	rs.append("plan.usage", work)
	if l, _, _ := rs.pickLogin("claude", "", t.TempDir(), false); l.Key() != "claude" {
		t.Fatalf("once the added login is fuller, the machine's own goes first, got %s", l.Key())
	}
}

// Headroom is the least used login's share left, all logins together: one
// login near its limit leaves the other's room, and with both spent or
// parked there is next to none.
func TestHeadroomCountsEveryLogin(t *testing.T) {
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	rs := NewRuns(Workspace{Root: t.TempDir()}, j, nil, twoLogins{}, func() []string { return nil })
	pick := Pick{Harness: "claude", Model: "opus"}
	if h := rs.Headroom(pick); h != 1 {
		t.Fatalf("with no readings: %v, want 1", h)
	}
	reset := time.Now().Add(time.Hour)
	used := func(login, status string, u float64) {
		rs.append("plan.usage", Plan{Harness: "claude", Login: login, Limit: harness.RateLimit{Status: status, ResetsAt: reset,
			Windows: map[string]harness.Window{"five_hour": {Utilization: u, ResetsAt: reset}}}})
	}
	used("claude", "allowed", 0.95)
	used("claude:work", "allowed", 0.6)
	if h := rs.Headroom(pick); h < 0.39 || h > 0.41 {
		t.Fatalf("one login at 60%%: %v, want 0.4", h)
	}
	used("claude:work", "rejected", 1)
	if h := rs.Headroom(pick); h < 0.04 || h > 0.06 {
		t.Fatalf("one parked, one at 95%%: %v, want 0.05", h)
	}
}

// A reading taken before the limit, from a turn that ends after it, doesn't
// lift the park (10-25 10:41: a 0.97 reading after a rejected one started a
// turn only for it to be turned away); a reset or a later window does.
func TestStaleReadingKeepsThePark(t *testing.T) {
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	rs := NewRuns(Workspace{Root: t.TempDir()}, j, nil, noOwnLogin{}, func() []string { return nil })
	reset := time.Now().Add(time.Hour)
	read := func(status string, u float64, at time.Time) {
		rs.append("plan.usage", Plan{Harness: "claude", Login: "claude", Limit: harness.RateLimit{Status: status, ResetsAt: at, Window: "five_hour",
			Utilization: u, Windows: map[string]harness.Window{"five_hour": {Utilization: u, ResetsAt: at}}}})
	}
	parked := func() bool { return rs.Plans()[0].Parked(time.Now()) }
	read("rejected", 1, reset)
	read("allowed_warning", 0.97, reset)
	if !parked() {
		t.Fatal("a stale reading in the same window lifted the park")
	}
	read("allowed", 0.1, reset.Add(5*time.Hour))
	if parked() {
		t.Fatal("a reading from the next window keeps the login parked")
	}
}

// With the machine's login at its limit and an added one with no
// credential, the run is parked for the limit, not for "not logged in".
func TestLimitIsTheReasonOverAMissingLogin(t *testing.T) {
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	rs := NewRuns(Workspace{Root: t.TempDir()}, j, nil, ownAndEmpty{}, func() []string { return nil })
	rs.append("plan.usage", Plan{Harness: "claude", Login: "claude", Limit: harness.RateLimit{Status: "rejected", ResetsAt: time.Now().Add(time.Hour)}})
	_, _, err = rs.pickLogin("claude", "", t.TempDir(), false)
	if err == nil || !strings.HasPrefix(err.Error(), "every claude login is at its usage limit") || !strings.Contains(err.Error(), "claude:empty") {
		t.Fatalf("parked for %v", err)
	}
}

// ownAndEmpty is the machine's login and an added one with no credential.
type ownAndEmpty struct{}

func (ownAndEmpty) Logins(h string) []Login {
	return []Login{{Harness: "claude"}, {Harness: "claude", Name: "empty"}}
}
func (ownAndEmpty) AgentEnv(l Login, home string) ([]string, error) {
	if l.Name == "empty" {
		return nil, ErrNoLogin
	}
	return []string{"TOKEN=x"}, nil
}

// Ahead is a five-hour window's use beyond the share of it passed: 99% used
// with 64 minutes left (10-25 10:36) is 20% ahead. The week is paced the same
// way, against its own length: 75% used 23.5 hours into it (10-25 16:33) is
// 61% ahead.
func TestPlanAheadOfItsWindow(t *testing.T) {
	now := time.Now()
	p := Plan{Limit: harness.RateLimit{Windows: map[string]harness.Window{
		"five_hour": {Utilization: 0.99, ResetsAt: now.Add(64 * time.Minute)},
		"seven_day": {Utilization: 0.1, ResetsAt: now.Add(6 * 24 * time.Hour)},
	}}}
	if a := p.Ahead(now); a < 0.19 || a > 0.21 {
		t.Fatalf("ahead %v, want 0.2", a)
	}
	p.Limit.Windows["five_hour"] = harness.Window{Utilization: 0.5, ResetsAt: now.Add(4 * time.Hour)}
	if a := p.Ahead(now); a < 0.29 || a > 0.31 {
		t.Fatalf("half spent in the first hour: %v, want 0.3", a)
	}
	p.Limit.Windows["five_hour"] = harness.Window{Utilization: 0.3, ResetsAt: now.Add(2 * time.Hour)}
	if a := p.Ahead(now); a != 0 {
		t.Fatalf("behind the clock: %v, want 0", a)
	}
	p.Limit.Windows["seven_day"] = harness.Window{Utilization: 0.75, ResetsAt: now.Add(7*24*time.Hour - 23*time.Hour - 30*time.Minute)}
	if a := p.Ahead(now); a < 0.60 || a > 0.62 {
		t.Fatalf("a week three quarters spent in its first day: %v, want 0.61", a)
	}
	p.Limit.Windows["seven_day_opus"] = harness.Window{Utilization: 0.95, ResetsAt: now.Add(12 * time.Hour)}
	if a := p.Ahead(now); a < 0.61 || a > 0.62 {
		t.Fatalf("a week's per-model window is paced as a week: %v, want 0.61", a)
	}
}
