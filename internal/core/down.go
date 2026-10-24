package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

// Down is a pick that answered with an error other than a usage limit, so
// runs skip it as they skip a login at its limit: a 404 (no endpoint, a
// guardrail), a 401 or 403 on a named model's key, a 400 naming the model, or
// the same error three turns running. A turn tries it again when its config
// changes or once Retry has passed, and an answer brings it back.
type Down struct {
	Pick   Pick      `json:"pick"`
	Status int       `json:"status,omitempty"` // the API status it answered; 0 when it said none
	Text   string    `json:"text"`             // its last answer, as it gave it
	Since  time.Time `json:"since"`            // when this spell began
	At     time.Time `json:"at"`               // its last such answer
	Retry  time.Time `json:"retry"`            // when a turn tries it again
	Run    string    `json:"run,omitempty"`    // the run whose turn got the answer
	Config string    `json:"config,omitempty"` // its config then (downConfig), so a change brings it back
}

// downRetry is how long a pick that can't answer is skipped before a turn
// tries it again.
const downRetry = time.Hour

// downStrikes is how many straight turns with the same error mark a pick
// down when the error alone doesn't say it can't answer.
const downStrikes = 3

// modelUp is the event that brings a pick back.
type modelUp struct {
	Pick Pick   `json:"pick"`
	Why  string `json:"why"`
}

// strike is a pick's run of turns ending in the same error, and the runs
// they came from: one run failing its own way again and again is that run's
// trouble (it backs off), not the model's.
type strike struct {
	text string
	n    int
	runs map[string]bool
}

// noModel is the wording of an answer that says the endpoint has no such
// model; a 400 saying anything else (a context too long) is one run's.
var noModel = regexp.MustCompile(`(?i)(model[^.]{0,80}(not found|does not exist|doesn't exist|not exist|is not a valid|not available|unknown|not supported))|((unknown|invalid|unsupported|no such) model)|(no endpoints found)`)

// Cause names what a Down is about, so the person hears once per cause.
func (d Down) Cause() string {
	sum := sha256.Sum256([]byte(fmt.Sprint(d.Status, " ", d.Text)))
	return hex.EncodeToString(sum[:4])
}

// ToldID is the id of the one notice for this spell and cause.
func (d Down) ToldID() string {
	return fmt.Sprintf("model-down-%s-%d-%s", strings.ReplaceAll(d.Pick.String(), ":", "-"), d.Since.Unix(), d.Cause())
}

// Line is the Down as the person reads it on one line.
func (d Down) Line() string {
	s := d.Pick.String() + " can't answer"
	if d.Status > 0 {
		s += fmt.Sprintf(" (%d)", d.Status)
	}
	return s + ": " + Clip(d.Text, 300)
}

var openRouterLink = regexp.MustCompile(`https://openrouter\.ai/[^\s"'<>)]+`)

// Fix says what would let the pick answer, read from its error.
func (d Down) Fix(cfg Config) string {
	name := d.Pick.Model
	m, named := cfg.Models[name]
	t := strings.ToLower(d.Text)
	switch {
	case strings.Contains(t, "guardrail") || strings.Contains(t, "data policy"):
		link := strings.TrimRight(openRouterLink.FindString(d.Text), ".,;:")
		if link == "" {
			link = "https://openrouter.ai/settings/privacy"
		}
		return fmt.Sprintf("Change the OpenRouter guardrail at %s so this model's endpoints are allowed, or name another free model that doesn't train on your data (`%s model add <name> https://openrouter.ai/api/v1 <model id> --key`) and route to it.", link, Cmd)
	case named && (d.Status == 401 || d.Status == 403):
		return fmt.Sprintf("Store a working key for it: `%s model key %s`.", Cmd, name)
	case named && (d.Status == 404 || d.Status == 400):
		return fmt.Sprintf("Check that %s serves the model id %q, or name another model (`%s model add`) and route to it.", m.Endpoint, m.Model, Cmd)
	case d.Status == 404 || d.Status == 400:
		return "Check the model's name in Settings, or route to another model."
	}
	return "Check that the model's endpoint is up and its settings are right."
}

// cantAnswer reports whether a turn's error means the pick it ran on can't
// answer at all, given the straight turns with the same error (this one
// included) and how many runs they came from. Only a named model's 404,
// 401/403 or repeated error counts: a Claude or Codex pick is the Hive's main
// harness, whose 401/403 is its login's and whose other errors (often with
// no text) are each run's own, so it is marked only when it says it has no
// such model. A 400 counts only when it says so too, and an unreachable
// server is retried as offline.
func cantAnswer(p Pick, res *harness.Result, offline string, strikes, runs int) bool {
	if res == nil || !res.IsError || res.Limited || res.Subtype == "error_max_budget_usd" {
		return false
	}
	text := strings.TrimSpace(res.Text)
	if (res.APIStatus == 404 || res.APIStatus == 400) && noModel.MatchString(text) {
		return true
	}
	if p.Harness != "open" {
		return false
	}
	switch res.APIStatus {
	case 404, 401, 403:
		return true
	}
	return offline == "" && text != "" && strikes >= downStrikes && runs >= 2
}

// downConfig is what a pick's answer depends on in the config: a named
// model's settings and its stored key. A change to it brings the pick back.
func (rs *Runs) downConfig(cfg Config, p Pick) string {
	if p.Harness != "open" {
		return ""
	}
	m, ok := cfg.Models[p.Model]
	if !ok {
		return "gone"
	}
	raw, _ := json.Marshal(m)
	s := string(raw)
	if fi, err := os.Stat(rs.ws.OpenKey(p.Model)); err == nil {
		s += fmt.Sprintf(" key %d %d", fi.Size(), fi.ModTime().UnixNano())
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// applyDown folds model.down and model.up into the state. rs.mu is held.
func (rs *Runs) applyDown(e Event) {
	switch e.Kind {
	case "model.down":
		var d Down
		e.Decode(&d)
		rs.down[d.Pick.String()] = d
	case "model.up":
		var u modelUp
		e.Decode(&u)
		delete(rs.down, u.Pick.String())
		delete(rs.strikes, u.Pick.String())
	}
}

// sameError is the text a strike compares, "" for an error with none.
func sameError(res *harness.Result) string { return Clip(strings.TrimSpace(res.Text), 200) }

// strikeTurn counts a pick's straight turns ending in the same error, from
// the turn's end. rs.mu is held.
func (rs *Runs) strikeTurn(p Pick, run string, res *harness.Result) {
	if res == nil {
		return
	}
	k := p.String()
	text := sameError(res)
	if !res.IsError || res.Limited || text == "" {
		delete(rs.strikes, k)
		return
	}
	s := rs.strikes[k]
	if s.text != text {
		s = strike{text: text, runs: map[string]bool{}}
	}
	s.n++
	s.runs[run] = true
	rs.strikes[k] = s
}

// strikesWith is how many straight turns on p, and from how many runs,
// run's result would make.
func (rs *Runs) strikesWith(p Pick, run string, res *harness.Result) (turns, runs int) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	s := rs.strikes[p.String()]
	if text := sameError(res); text == "" || s.text != text {
		return 1, 1
	}
	runs = len(s.runs)
	if !s.runs[run] {
		runs++
	}
	return s.n + 1, runs
}

// markDown records that p can't answer, from a turn of run r; a pick already
// down keeps the start of its spell.
func (rs *Runs) markDown(r Run, p Pick, res *harness.Result) Down {
	now := time.Now().UTC()
	d := Down{Pick: p, Status: res.APIStatus, Text: Clip(res.Text, 2000), Since: now, At: now, Retry: now.Add(downRetry), Run: r.ID}
	rs.mu.Lock()
	if old, ok := rs.down[p.String()]; ok {
		d.Since = old.Since
	}
	d.Config = rs.downConfig(rs.cfg, p)
	rs.mu.Unlock()
	rs.append("model.down", d)
	return d
}

// downFor is p's Down, if it is down.
func (rs *Runs) downFor(p Pick) (Down, bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	d, ok := rs.down[p.String()]
	return d, ok
}

// Down lists the picks that can't answer, by pick.
func (rs *Runs) Down() []Down {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	out := make([]Down, 0, len(rs.down))
	for _, d := range rs.down {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pick.String() < out[j].Pick.String() })
	return out
}

// Up brings a pick back, saying why; nothing when it isn't down.
// Runs parked waiting on a pick that couldn't answer wake to look again,
// so a fixed config is tried at once rather than at the retry.
func (rs *Runs) Up(p Pick, why string) {
	if _, ok := rs.downFor(p); !ok {
		return
	}
	rs.append("model.up", modelUp{Pick: p, Why: why})
	now := time.Now()
	for _, r := range rs.List(func(r *Run) bool {
		return r.Status == StatusParked && r.ParkedUntil.After(now) && strings.Contains(r.Reason, cantAnswerWhy)
	}) {
		if next, until := rs.next(r); until.Before(r.ParkedUntil) {
			rs.append("run.status", runNote{ID: r.ID, Status: StatusParked, Reason: next.String() + " has room again (" + p.String() + " " + why + ")", Until: until})
		}
	}
}

// cantAnswerWhy marks the reason of a run parked because its pick can't answer.
const cantAnswerWhy = " can't answer; "

// tickDown keeps the config the next Down is fingerprinted against, and
// brings back each pick whose config changed since it was marked.
func (rs *Runs) tickDown(cfg Config) {
	rs.mu.Lock()
	rs.cfg = cfg
	var changed []Pick
	for _, d := range rs.down {
		if rs.downConfig(cfg, d.Pick) != d.Config {
			changed = append(changed, d.Pick)
		}
	}
	rs.mu.Unlock()
	for _, p := range changed {
		rs.Up(p, "its config changed")
	}
}
