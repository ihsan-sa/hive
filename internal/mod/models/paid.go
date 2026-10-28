package models

import (
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

// Paid holds paid OpenRouter models to the month's cap, the person's word
// of 10-24: "cheap paid models are fine too, as long as you stay inside it.
// Free ones first". The config check lets one in only with a cap
// (openrouter_monthly_usd) and a price (core.Config.PaidRefused); Paid puts
// it after the rest of a route (Last), counts what its turns cost from the
// journal, holds it until the next month (UTC) once that reaches the cap
// (Held), and gives each turn on it no more than is left, less what the
// turns already running on paid models may spend (TurnBudget).
type Paid struct {
	cfg func() core.Config

	mu      sync.Mutex
	pick    map[string]core.Pick         // each run's pick, from its latest turn's start
	current map[string]string            // each run's session
	session map[string]core.SessionSpend // each run's session spend, as the run manager counts it
	month   map[string]float64           // "2006-01" (UTC) -> what paid OpenRouter turns cost
	running map[string]float64           // the budget each paid turn still running was given, by run
}

// NewPaid counts the journal's paid OpenRouter turns, and keeps counting.
func NewPaid(j *core.Journal, cfg func() core.Config) *Paid {
	p := &Paid{cfg: cfg, pick: map[string]core.Pick{}, current: map[string]string{}, session: map[string]core.SessionSpend{}, month: map[string]float64{}, running: map[string]float64{}}
	c := cfg()
	j.Replay(func(e core.Event) { p.apply(e, c) })
	j.Subscribe(func(e core.Event) { p.apply(e, p.cfg()) })
	return p
}

func (p *Paid) apply(e core.Event, cfg core.Config) {
	switch e.Kind {
	case "run.turn_started":
		var t struct {
			ID, Harness, Model, Session string
		}
		if e.Decode(&t) != nil {
			return
		}
		p.mu.Lock()
		if t.Harness != "" {
			p.pick[t.ID] = core.Pick{Harness: core.HarnessName(t.Harness), Model: t.Model}
		}
		p.current[t.ID] = t.Session
		p.mu.Unlock()
	case "run.turn_ended":
		var t struct {
			ID      string          `json:"id"`
			Session string          `json:"session"`
			Result  *harness.Result `json:"result"`
		}
		if e.Decode(&t) != nil {
			return
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		delete(p.running, t.ID)
		if t.Result == nil {
			return
		}
		if t.Session != "" {
			p.current[t.ID] = t.Session
		}
		s := p.session[t.ID]
		spent := s.Turn(p.current[t.ID], t.Result.CostUSD)
		p.session[t.ID] = s
		if paid(cfg, p.pick[t.ID]) {
			p.month[e.At.UTC().Format("2006-01")] += spent
		}
	}
}

// paid reports whether pk is a paid model on OpenRouter.
func paid(cfg core.Config, pk core.Pick) bool {
	m, ok := cfg.Models[pk.Model]
	return core.HarnessName(pk.Harness) == "open" && ok && harness.OpenRouterPaid(m.Endpoint, m.Model) != nil
}

// Last reports whether pk goes after the rest of a route: a paid OpenRouter
// model, which a run reaches once the others are at their limits.
func (p *Paid) Last(pk core.Pick) bool { return paid(p.cfg(), pk) }

// Spent is what paid OpenRouter turns cost in month ("2006-01", UTC).
func (p *Paid) Spent(month string) float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.month[month]
}

// Held is when pk next has room under the cap: the start of next month once
// this month's spend has reached it, else zero.
func (p *Paid) Held(pk core.Pick) time.Time {
	cfg := p.cfg()
	if !paid(cfg, pk) {
		return time.Time{}
	}
	now := time.Now().UTC()
	if p.Spent(now.Format("2006-01")) < cfg.OpenRouterUSD {
		return time.Time{}
	}
	return time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
}

// TurnBudget is the most r's next turn may spend: on a paid OpenRouter
// model, what is left of the month's cap less what the other paid turns
// running were given, so turns at once can't pass it together; 0 (no
// limit) on any other model.
func (p *Paid) TurnBudget(r core.Run) float64 {
	cfg := p.cfg()
	if !paid(cfg, core.Pick{Harness: r.Harness, Model: r.Model}) {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	left := cfg.OpenRouterUSD - p.month[time.Now().UTC().Format("2006-01")]
	for id, b := range p.running {
		if id != r.ID {
			left -= b
		}
	}
	left = max(left, 0.01) // the open agent's smallest budget; Held keeps a run off once nothing is left
	p.running[r.ID] = left
	return left
}
