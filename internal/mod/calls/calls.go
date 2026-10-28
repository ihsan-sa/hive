// Package calls turns the Hive's fixed cadences, caps and counts into calls
// agents make (issue #1940). Each one a module once held as a constant (how
// often the release train leaves, how many reruns a head gets, how long a
// step may take) is declared here by name with its old value as the default;
// the code reads the call where it read the constant, and any agent that
// steers changes it at once with `hive agent call`, on the record with why.
// So a rule that stops fitting the work is changed in a turn, not in a PR, a
// merge and a release. The person's own limits (the pace's load and pressure,
// the plan's weekly and five-hour stops) stay settings, not calls.
package calls

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// Kind is the journal event an agent's call is.
const Kind = "call.made"

// Made is one call an agent made: Value "" puts it back to its default.
type Made struct {
	Name  string    `json:"name"`
	Value string    `json:"value"`
	Was   string    `json:"was"` // what it read before
	Why   string    `json:"why"`
	By    string    `json:"by"`
	Role  string    `json:"role,omitempty"`
	At    time.Time `json:"at"`
	Event int64     `json:"-"` // its journal seq: the latest wins
}

// spec is one declared call: its name, what it decides and what 0 means
// (Help), its default as text (read each time, so a test's override of the
// variable behind it holds) and how its value is checked.
type spec struct {
	Name, Help string
	def        func() string
	check      func(string) (string, error) // the value as kept, or why not
	bar        bool                         // it decides whether a change counts (see Bar)
}

var (
	smu   sync.Mutex
	specs = map[string]*spec{}
)

func declare(s *spec) *spec {
	smu.Lock()
	defer smu.Unlock()
	if _, dup := specs[s.Name]; dup {
		panic("calls: " + s.Name + " declared twice")
	}
	specs[s.Name] = s
	return s
}

func lookup(name string) *spec {
	smu.Lock()
	defer smu.Unlock()
	return specs[name]
}

// Dur is a call that is a length of time.
type Dur struct{ s *spec }

// Duration declares a call named name whose default is *def.
func Duration(name, help string, def *time.Duration) Dur {
	return Dur{declare(&spec{Name: name, Help: help, def: func() string { return fmtDur(*def) }, check: checkDur})}
}

// Of is the call's value on the Hive whose journal is j: the latest an agent
// made, else its default.
func (d Dur) Of(j *core.Journal) time.Duration {
	if v, ok := of(j).value(d.s.Name); ok {
		if x, err := parseDur(v); err == nil {
			return x
		}
	}
	x, _ := parseDur(d.s.def())
	return x
}

// Name is the call's name.
func (d Dur) Name() string { return d.s.Name }

// Bar marks the call as one that decides whether a change counts (how often
// its failed checks run again, how long before tests that can't start count
// as red): a worker, whose own change it would be, doesn't make it.
func (d Dur) Bar() Dur { d.s.bar = true; return d }

// Int is a call that is a count.
type Int struct{ s *spec }

// Count declares a call named name whose default is *def.
func Count(name, help string, def *int) Int {
	return Int{declare(&spec{Name: name, Help: help, def: func() string { return strconv.Itoa(*def) }, check: checkInt})}
}

// Of is the call's value on the Hive whose journal is j.
func (n Int) Of(j *core.Journal) int {
	if v, ok := of(j).value(n.s.Name); ok {
		if x, err := strconv.Atoi(v); err == nil {
			return x
		}
	}
	x, _ := strconv.Atoi(n.s.def())
	return x
}

// Name is the call's name.
func (n Int) Name() string { return n.s.Name }

// Bar marks the call as one that decides whether a change counts (Dur.Bar).
func (n Int) Bar() Int { n.s.bar = true; return n }

func checkInt(v string) (string, error) {
	x, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || x < 0 {
		return "", fmt.Errorf("%q isn't a count: a whole number, 0 or more", v)
	}
	return strconv.Itoa(x), nil
}

func checkDur(v string) (string, error) {
	x, err := parseDur(v)
	if err != nil || x < 0 {
		return "", fmt.Errorf("%q isn't a length of time: say 90m, 6h or 2d, 0 or more", v)
	}
	return fmtDur(x), nil
}

// parseDur reads a Go duration, with d for days.
func parseDur(v string) (time.Duration, error) {
	v = strings.TrimSpace(v)
	if n, ok := strings.CutSuffix(v, "d"); ok {
		f, err := strconv.ParseFloat(n, 64)
		if err != nil {
			return 0, err
		}
		return time.Duration(f * float64(24*time.Hour)), nil
	}
	return time.ParseDuration(v)
}

func fmtDur(d time.Duration) string {
	if d != 0 && d%(24*time.Hour) == 0 {
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	}
	s := d.String()
	if t, ok := strings.CutSuffix(s, "m0s"); ok {
		s = t + "m"
	}
	if t, ok := strings.CutSuffix(s, "h0m"); ok {
		s = t + "h"
	}
	return s
}

// book is one Hive's calls as made: the latest per name, kept from its
// journal's replay and every call journaled since.
type book struct {
	mu   sync.Mutex
	made map[string]Made
}

var (
	bmu   sync.Mutex
	books = map[*core.Journal]*book{}
)

// of is the book of the Hive whose journal is j, read from the journal once
// and kept current by subscription.
func of(j *core.Journal) *book {
	if j == nil {
		return &book{}
	}
	bmu.Lock()
	defer bmu.Unlock()
	if b, ok := books[j]; ok {
		return b
	}
	b := &book{made: map[string]Made{}}
	// Subscribed before the replay, so a call made during it isn't missed;
	// the higher Seq wins either way.
	j.Subscribe(b.take)
	_ = j.Replay(b.take)
	books[j] = b
	return b
}

func (b *book) take(e core.Event) {
	if e.Kind != Kind {
		return
	}
	var m Made
	if e.Decode(&m) != nil || m.Name == "" {
		return
	}
	m.Event, m.At = e.Seq, e.At
	b.mu.Lock()
	if old, ok := b.made[m.Name]; !ok || old.Event < m.Event {
		b.made[m.Name] = m
	}
	b.mu.Unlock()
}

func (b *book) value(name string) (string, bool) {
	if b.made == nil {
		return "", false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	m, ok := b.made[name]
	if !ok || m.Value == "" {
		return "", false
	}
	return m.Value, true
}

// Entry is one call as listed: its value now, its default and the latest
// call made on it, if any.
type Entry struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	Default string `json:"default"`
	Help    string `json:"help"`
	Bar     bool   `json:"bar,omitempty"` // a worker doesn't make it: it decides whether a change counts
	Made    *Made  `json:"made,omitempty"`
}

// List is every declared call on the Hive whose journal is j, by name.
func List(j *core.Journal) []Entry {
	smu.Lock()
	all := make([]*spec, 0, len(specs))
	for _, s := range specs {
		all = append(all, s)
	}
	smu.Unlock()
	sort.Slice(all, func(a, b int) bool { return all[a].Name < all[b].Name })
	bk := of(j)
	out := make([]Entry, 0, len(all))
	for _, s := range all {
		e := Entry{Name: s.Name, Default: s.def(), Help: s.Help, Bar: s.bar}
		e.Value = e.Default
		if v, ok := bk.value(s.Name); ok {
			e.Value = v
		}
		bk.mu.Lock()
		if m, ok := bk.made[s.Name]; ok {
			e.Made = &m
		}
		bk.mu.Unlock()
		out = append(out, e)
	}
	return out
}

// Make journals a call by run r: name to value ("" or "default" for its
// default), with why.
func Make(j *core.Journal, r core.Run, name, value, why string) (Entry, error) {
	s := lookup(strings.TrimSpace(name))
	if s == nil {
		return Entry{}, fmt.Errorf("no call is named %q: `hive agent call` with no name lists them", name)
	}
	if s.bar && r.Role == "worker" {
		return Entry{}, fmt.Errorf("%s decides whether a change counts, so a worker doesn't make it, its own change being one: ask the lead, with why", s.Name)
	}
	if strings.TrimSpace(why) == "" {
		return Entry{}, errors.New("say why: --why \"...\" goes on the record with the call")
	}
	v := strings.TrimSpace(value)
	if v == "default" {
		v = ""
	}
	if v != "" {
		var err error
		if v, err = s.check(v); err != nil {
			return Entry{}, fmt.Errorf("%s: %v", s.Name, err)
		}
	}
	was := s.def()
	if old, ok := of(j).value(s.Name); ok {
		was = old
	}
	if _, err := j.Append(Kind, Made{Name: s.Name, Value: v, Was: was, Why: why, By: r.ID, Role: r.Role}); err != nil {
		return Entry{}, err
	}
	for _, e := range List(j) {
		if e.Name == s.Name {
			return e, nil
		}
	}
	return Entry{}, nil
}

// steerers are the roles that make calls: those that steer the loop
// (reviewers, wardens, tribunals and judges weigh one thing and don't).
var steerers = []string{"lead", "worker", "sweep", "stepback"}

// New registers the call op on c and reads c's calls from its journal.
func New(c *core.Core) {
	of(c.J)
	c.Runs.Ops().Register(core.Op{Name: "call", Roles: steerers,
		Help: `{"name","value","why"} -> the Hive's cadences, caps and counts are calls, not rules: with no name, every call with its value, its default, what it decides and the last call made on it; with a name, value takes effect at once for the whole Hive (a length like 90m, 6h or 2d, or a count; "default" goes back to the default), and why goes on the record. Make the call the work needs now; the person's own limits (the pace, the plan's stops) are settings, not calls`,
		Fn: func(r core.Run, args json.RawMessage) (any, error) {
			var a struct{ Name, Value, Why string }
			if err := core.Bind(args, &a); err != nil {
				return nil, err
			}
			if strings.TrimSpace(a.Name) == "" {
				return List(c.J), nil
			}
			return Make(c.J, r, a.Name, a.Value, a.Why)
		}})
}
