package core

import (
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

	if l, _, _ := rs.pickLogin("claude", t.TempDir(), false); l.Key() != "claude" {
		t.Fatalf("with room on both, the machine's own login goes first, got %s", l.Key())
	}
	soon, later := time.Now().Add(time.Hour), time.Now().Add(2*time.Hour)
	park("claude", later)
	l, env, err := rs.pickLogin("claude", t.TempDir(), false)
	if err != nil || l.Key() != "claude:work" || env[0] != "TOKEN=claude:work" {
		t.Fatalf("a parked login is passed over: %v %v %v", l, env, err)
	}
	if time.Until(rs.freeAt("claude")) > time.Second {
		t.Fatal("a run limited on one login moves to the other at once")
	}
	park("claude:work", soon)
	if _, _, err := rs.pickLogin("claude", t.TempDir(), false); err == nil {
		t.Fatal("with every login parked, no login is handed out")
	}
	if at := rs.freeAt("claude"); at.Before(soon) || at.After(soon.Add(time.Minute)) {
		t.Fatalf("runs wait for the first reset, got %v want ~%v", at, soon)
	}
	park("claude", time.Now().Add(-time.Minute))
	if l, _, _ := rs.pickLogin("claude", t.TempDir(), false); l.Key() != "claude" {
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
	if l, env, err := rs.pickLogin("claude", "", true); err != nil || l.Key() != "claude" || len(env) != 0 {
		t.Fatalf("bare: %v %v %v", l, env, err)
	}
	if _, _, err := rs.pickLogin("claude", "", false); err == nil {
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
	if l, _, _ := rs.pickLogin("claude", t.TempDir(), false); l.Key() != "claude:work" {
		t.Fatalf("the less used login takes the run, got %s", l.Key())
	}
	for _, p := range rs.Plans() {
		if p.Key() == "claude" && p.Limit.Utilization != 0.28 {
			t.Fatalf("plan claude reads %v, want 0.28", p.Limit.Utilization)
		}
	}
}
