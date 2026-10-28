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

	if l, _, _ := rs.pickLogin("claude", "", t.TempDir(), false, false); l.Key() != "claude" {
		t.Fatalf("with room on both, the machine's own login goes first, got %s", l.Key())
	}
	soon, later := time.Now().Add(time.Hour), time.Now().Add(2*time.Hour)
	park("claude", later)
	l, env, err := rs.pickLogin("claude", "", t.TempDir(), false, false)
	if err != nil || l.Key() != "claude:work" || env[0] != "TOKEN=claude:work" {
		t.Fatalf("a parked login is passed over: %v %v %v", l, env, err)
	}
	if time.Until(rs.freeAt("claude", "", false)) > time.Second {
		t.Fatal("a run limited on one login moves to the other at once")
	}
	park("claude:work", soon)
	if _, _, err := rs.pickLogin("claude", "", t.TempDir(), false, false); err == nil {
		t.Fatal("with every login parked, no login is handed out")
	}
	if at := rs.freeAt("claude", "", false); at.Before(soon) || at.After(soon.Add(time.Minute)) {
		t.Fatalf("runs wait for the first reset, got %v want ~%v", at, soon)
	}
	park("claude", time.Now().Add(-time.Minute))
	if l, _, _ := rs.pickLogin("claude", "", t.TempDir(), false, false); l.Key() != "claude" {
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
	if l, env, err := rs.pickLogin("claude", "", "", true, false); err != nil || l.Key() != "claude" || len(env) != 0 {
		t.Fatalf("bare: %v %v %v", l, env, err)
	}
	if _, _, err := rs.pickLogin("claude", "", "", false, false); err == nil {
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
	if l, _, _ := rs.pickLogin("claude", "", t.TempDir(), false, false); l.Key() != "claude:work" {
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
	if l, _, _ := rs.pickLogin("claude", "", t.TempDir(), false, false); l.Key() != "claude:work" {
		t.Fatalf("the less-used added login goes first, got %s", l.Key())
	}
	work.Limit.Windows["seven_day"] = harness.Window{Utilization: 0.50, ResetsAt: time.Now().Add(48 * time.Hour)}
	rs.append("plan.usage", work)
	if l, _, _ := rs.pickLogin("claude", "", t.TempDir(), false, false); l.Key() != "claude" {
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
	used("claude", "allowed", 0.88)
	used("claude:work", "allowed", 0.6)
	if h := rs.Headroom(pick); h < 0.39 || h > 0.41 {
		t.Fatalf("one login at 60%%: %v, want 0.4", h)
	}
	used("claude:work", "rejected", 1)
	if h := rs.Headroom(pick); h < 0.11 || h > 0.13 {
		t.Fatalf("one parked, one at 88%%: %v, want 0.12", h)
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

// A login parked at WeeklyStop of its week stays parked through a late
// reading from a turn that started before the park, even one whose five-hour
// window is later than the parking reading's (#875's review); the next week
// lifts it.
func TestStaleReadingKeepsTheWeeklyStop(t *testing.T) {
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	rs := NewRuns(Workspace{Root: t.TempDir()}, j, nil, noOwnLogin{}, func() []string { return nil })
	week := time.Now().Add(3 * 24 * time.Hour)
	read := func(five time.Time, weekly float64, weekReset time.Time) {
		rs.append("plan.usage", Plan{Harness: "claude", Login: "claude", Limit: harness.RateLimit{Status: "allowed", ResetsAt: five, Window: "five_hour",
			Windows: map[string]harness.Window{"five_hour": {Utilization: 0.2, ResetsAt: five}, "seven_day": {Utilization: weekly, ResetsAt: weekReset}}}})
	}
	parked := func() bool { return rs.Plans()[0].Parked(time.Now()) }
	read(time.Now().Add(time.Hour), 0.96, week)
	if !parked() {
		t.Fatal("96% of the week didn't park the login")
	}
	read(time.Now().Add(2*time.Hour), 0.94, week)
	if !parked() {
		t.Fatal("a stale reading with a later five-hour reset lifted the weekly stop")
	}
	read(time.Now().Add(2*time.Hour), 0.01, week.Add(7*24*time.Hour))
	if parked() {
		t.Fatal("a reading from the next week keeps the login parked")
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
	_, _, err = rs.pickLogin("claude", "", t.TempDir(), false, false)
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

// At 95% of its weekly window a login takes no turns until the week resets
// (the person, 10-25), and every run but the checks stops CheckReserve
// before, at 90% (issue #983): the other login takes them, and with both
// there a turn waits for the first week's reset, not the five-hour one's.
// The checks spend the reserve between, so a written change is still read,
// judged and merged, and stop at 95% like the rest.
func TestWeeklyStopParksALogin(t *testing.T) {
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	rs := NewRuns(Workspace{Root: t.TempDir()}, j, nil, twoLogins{}, func() []string { return nil })
	now := time.Now()
	five, week := now.Add(time.Hour), now.Add(5*time.Hour+24*time.Hour)
	read := func(login string, weekly float64, resets time.Time) {
		rs.append("plan.usage", Plan{Harness: "claude", Login: login, Limit: harness.RateLimit{Status: "allowed_warning", ResetsAt: resets, Window: "seven_day", Utilization: weekly,
			Windows: map[string]harness.Window{"five_hour": {Utilization: 0.2, ResetsAt: five}, "seven_day": {Utilization: weekly, ResetsAt: resets}}}})
	}
	if !Reserved(JudgeRole) || !Reserved("reviewer") || !Reserved("lead") || Reserved("worker") || Reserved("sweep") {
		t.Fatal("the reserve is the checks' and the leads'")
	}
	read("claude", 0.89, week)
	read("claude:work", 0.5, week.Add(time.Hour))
	if rs.Plans()[0].Parked(now) {
		t.Fatal("a login at 89% of its week is parked")
	}
	read("claude", 0.92, week)
	if l, _, err := rs.pickLogin("claude", "", t.TempDir(), false, false); err != nil || l.Key() != "claude:work" {
		t.Fatalf("with the machine's login at 92%% of its week, picked %s (%v)", l.Key(), err)
	}
	if h := rs.Headroom(Pick{Harness: "claude", Model: "opus"}); h < 0.49 || h > 0.51 {
		t.Fatalf("headroom %v, want the other login's 0.5", h)
	}
	read("claude:work", 0.93, week.Add(time.Hour))
	if _, _, err := rs.pickLogin("claude", "", t.TempDir(), false, false); err == nil || !strings.Contains(err.Error(), "90% of its weekly window") {
		t.Fatalf("with every login past 90%% of its week: %v", err)
	}
	if at := rs.freeAt("claude", "opus", false); at.Before(week) || at.After(week.Add(time.Minute)) {
		t.Fatalf("free at %v, want the week's reset %v", at, week)
	}
	if s := rs.Plans()[0].Standing(now); !strings.Contains(s, "paused at 90%") || !strings.Contains(s, "checks and leads run on to 95%") {
		t.Fatalf("standing %q", s)
	}
	if l, _, err := rs.pickLogin("claude", "", t.TempDir(), false, true); err != nil || l.Key() != "claude" {
		t.Fatalf("a check with every login between 90%% and 95%% of its week picked %s (%v), want the least used", l.Key(), err)
	}
	if at := rs.freeAt("claude", "opus", true); time.Until(at) > time.Second {
		t.Fatalf("a check is free at %v, want now", at)
	}
	read("claude", 0.95, week)
	if l, _, err := rs.pickLogin("claude", "", t.TempDir(), false, true); err != nil || l.Key() != "claude:work" {
		t.Fatalf("a check with the machine's login at 95%% picked %s (%v)", l.Key(), err)
	}
	read("claude:work", 0.97, week.Add(time.Hour))
	if _, _, err := rs.pickLogin("claude", "", t.TempDir(), false, true); err == nil || !strings.Contains(err.Error(), "95% of its weekly window") {
		t.Fatalf("a check with every login at 95%% of its week: %v", err)
	}
	if at := rs.freeAt("claude", "opus", true); at.Before(week) || at.After(week.Add(time.Minute)) {
		t.Fatalf("a check at 95%% is free at %v, want the week's reset %v", at, week)
	}
	if s := rs.Plans()[0].Standing(now); !strings.Contains(s, "paused at 95%") {
		t.Fatalf("standing %q", s)
	}
	if rs.Plans()[0].Parked(week.Add(time.Second)) {
		t.Fatal("the login stays parked after its week resets")
	}
}

// At 90% of its five-hour window one login of a pick, the one with the
// most of it left, takes no turns until that window resets, while the
// others run to their limit (the person, 10-26: "90% of the only 5h limit
// avail", the rest theirs, to work with the plan themselves); with every
// other at its limit a turn waits for the first reset, and a late reading
// from before the stop doesn't lift it.
func TestFiveHourStopParksALogin(t *testing.T) {
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	rs := NewRuns(Workspace{Root: t.TempDir()}, j, nil, twoLogins{}, func() []string { return nil })
	now := time.Now()
	five, week := now.Add(time.Hour), now.Add(3*24*time.Hour)
	read := func(login string, used float64, resets time.Time) {
		status := "allowed"
		if used >= 1 {
			status = "rejected"
		}
		rs.append("plan.usage", Plan{Harness: "claude", Login: login, Limit: harness.RateLimit{Status: status, ResetsAt: resets, Window: "five_hour",
			Windows: map[string]harness.Window{"five_hour": {Utilization: used, ResetsAt: resets}, "seven_day": {Utilization: 0.11, ResetsAt: week}}}})
	}
	plan := func(key string) Plan {
		for _, p := range rs.Plans() {
			if p.Key() == key {
				return p
			}
		}
		t.Fatalf("no plan %s", key)
		return Plan{}
	}
	read("claude", 0.89, five)
	read("claude:work", 0.5, five.Add(time.Hour))
	if plan("claude").Parked(now) {
		t.Fatal("a login at 89% of its five hours is parked")
	}
	read("claude", 0.92, five)
	if plan("claude").Parked(now) || strings.Contains(plan("claude").Standing(now), "paused") {
		t.Fatalf("a login at 92%% of its five hours is parked beside one with more left: %q", plan("claude").Standing(now))
	}
	if h := rs.Headroom(Pick{Harness: "claude", Model: "opus"}); h < 0.49 || h > 0.51 {
		t.Fatalf("headroom %v, want the other login's 0.5", h)
	}
	read("claude:work", 0.93, five.Add(time.Hour))
	if s := plan("claude").Standing(now); s != "paused at 90% of its five-hour window until it resets" {
		t.Fatalf("standing %q, want the login with more of its five hours left kept back", s)
	}
	if l, _, err := rs.pickLogin("claude", "", t.TempDir(), false, false); err != nil || l.Key() != "claude:work" {
		t.Fatalf("with the machine's login kept back, picked %s (%v), want the other run on past 90%%", l.Key(), err)
	}
	read("claude", 0.85, five)
	if !plan("claude").Parked(now) {
		t.Fatal("a stale reading from before the stop lifted it")
	}
	read("claude:work", 1, five.Add(time.Hour))
	if _, _, err := rs.pickLogin("claude", "", t.TempDir(), false, false); err == nil || !strings.Contains(err.Error(), "90% of its five-hour one") {
		t.Fatalf("with the other login at its limit: %v", err)
	}
	// The five hours' last tenth is the person's: the checks' reserve is the week's alone.
	if _, _, err := rs.pickLogin("claude", "", t.TempDir(), false, true); err == nil {
		t.Fatal("a check took a login at 90% of its five hours")
	}
	if h := rs.Headroom(Pick{Harness: "claude", Model: "opus"}); h != 0 {
		t.Fatalf("headroom %v with the last login at its stop", h)
	}
	if at := rs.freeAt("claude", "opus", false); at.Before(five) || at.After(five.Add(time.Minute)) {
		t.Fatalf("free at %v, want the five-hour reset %v", at, five)
	}
	if plan("claude").Parked(five.Add(time.Second)) {
		t.Fatal("the login stays parked after its five hours reset")
	}
}

// Turn by turn, logins picked least used first climb their five hours
// together; past 90% all but one run on to their limit, and that one stops
// at 90% with its tenth left for the person.
func TestFiveHourStopKeepsOneLoginsTenth(t *testing.T) {
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	rs := NewRuns(Workspace{Root: t.TempDir()}, j, nil, twoLogins{}, func() []string { return nil })
	five := time.Now().Add(time.Hour)
	used := map[string]float64{"claude": 0.80, "claude:work": 0.81}
	read := func(login string) {
		status := "allowed"
		if used[login] >= 1 {
			status = "rejected"
		}
		rs.append("plan.usage", Plan{Harness: "claude", Login: login, Limit: harness.RateLimit{Status: status, ResetsAt: five, Window: "five_hour",
			Windows: map[string]harness.Window{"five_hour": {Utilization: used[login], ResetsAt: five}}}})
	}
	read("claude")
	read("claude:work")
	for range 100 {
		l, _, err := rs.pickLogin("claude", "", t.TempDir(), false, false)
		if err != nil {
			break
		}
		used[l.Key()] = min(used[l.Key()]+0.03, 1)
		read(l.Key())
	}
	full, kept := used["claude"], used["claude:work"]
	if full < kept {
		full, kept = kept, full
	}
	if full < 1 || kept < FiveHourStop || kept >= FiveHourStop+0.03 {
		t.Fatalf("five hours used %v: want one login at its limit and the other kept at 90%%", used)
	}
}

// A login lifted past 90% of its five hours shows as taking turns in the
// control center, which reads Plans through JSON (api.State), and one whose
// renewal was turned away keeps no tenth: Plans, pickLogin, Headroom and
// freeAt keep the stop on the other.
func TestPlansShowTheLoginKeptAtFiveHours(t *testing.T) {
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	tokens := &renewLimited{}
	rs := NewRuns(Workspace{Root: t.TempDir()}, j, nil, tokens, func() []string { return nil })
	now := time.Now()
	five := now.Add(time.Hour)
	read := func(login string, used float64) {
		rs.append("plan.usage", Plan{Harness: "claude", Login: login, Limit: harness.RateLimit{Status: "allowed", ResetsAt: five, Window: "five_hour",
			Windows: map[string]harness.Window{"five_hour": {Utilization: used, ResetsAt: five}}}})
	}
	shown := func() map[string]string {
		b, err := json.Marshal(rs.Plans())
		if err != nil {
			t.Fatal(err)
		}
		var ps []Plan
		if err := json.Unmarshal(b, &ps); err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, p := range ps {
			out[p.Key()] = p.Standing(now)
		}
		return out
	}
	read("claude", 0.92)
	read("claude:work", 0.5)
	if s := shown()["claude"]; strings.Contains(s, "paused") {
		t.Fatalf("a lifted login past 90%% shows %q", s)
	}
	tokens.until = now.Add(30 * time.Minute)
	if s := shown()["claude"]; !strings.Contains(s, "paused at 90%") {
		t.Fatalf("with the other login turned away on renewal, the machine's shows %q", s)
	}
	if _, _, err := rs.pickLogin("claude", "", t.TempDir(), false, false); err == nil {
		t.Fatal("a turn went on with one login turned away on renewal and the other at 90% of its five hours")
	}
	if h := rs.Headroom(Pick{Harness: "claude", Model: "opus"}); h != 0 {
		t.Fatalf("headroom %v, want none", h)
	}
	if at := rs.freeAt("claude", "opus", false); at.Before(tokens.until) || at.After(tokens.until.Add(time.Minute)) {
		t.Fatalf("free at %v, want the renewal's %v", at, tokens.until)
	}
}

// renewLimited is twoLogins whose second login's renewal was turned away
// until until.
type renewLimited struct {
	twoLogins
	until time.Time
}

func (r *renewLimited) LimitedUntil(key string) time.Time {
	if key == "claude:work" {
		return r.until
	}
	return time.Time{}
}
