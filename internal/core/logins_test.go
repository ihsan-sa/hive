package core

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

// twoLogins is a token source with the machine's own login and one added.
type twoLogins struct{}

func (twoLogins) Logins(h string) []Login {
	return []Login{{Harness: "claude"}, {Harness: "claude", Name: "work"}}
}
func (twoLogins) AgentEnv(l Login) ([]string, error) { return []string{"TOKEN=" + l.Key()}, nil }

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

	if l, _, _ := rs.pickLogin("claude"); l.Key() != "claude" {
		t.Fatalf("with room on both, the machine's own login goes first, got %s", l.Key())
	}
	soon, later := time.Now().Add(time.Hour), time.Now().Add(2*time.Hour)
	park("claude", later)
	l, env, err := rs.pickLogin("claude")
	if err != nil || l.Key() != "claude:work" || env[0] != "TOKEN=claude:work" {
		t.Fatalf("a parked login is passed over: %v %v %v", l, env, err)
	}
	if time.Until(rs.freeAt("claude")) > time.Second {
		t.Fatal("a run limited on one login moves to the other at once")
	}
	park("claude:work", soon)
	if _, _, err := rs.pickLogin("claude"); err == nil {
		t.Fatal("with every login parked, no login is handed out")
	}
	if at := rs.freeAt("claude"); at.Before(soon) || at.After(soon.Add(time.Minute)) {
		t.Fatalf("runs wait for the first reset, got %v want ~%v", at, soon)
	}
	park("claude", time.Now().Add(-time.Minute))
	if l, _, _ := rs.pickLogin("claude"); l.Key() != "claude" {
		t.Fatalf("a login whose reset has passed takes runs again, got %s", l.Key())
	}
}
