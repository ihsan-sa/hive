package core

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

// OpenModel is a model on an OpenAI-compatible endpoint (a model server on
// this machine such as Ollama, or a hosted tier such as OpenRouter's free
// models), which the open harness drives, or on an Anthropic-compatible one
// (API "anthropic"), which Claude Code drives. Its name in Config.Models is
// how routes, tribunal seats and the lead's route op refer to it.
type OpenModel struct {
	Endpoint string `json:"endpoint"`          // base URL, e.g. http://127.0.0.1:11434/v1
	Model    string `json:"model"`             // the id the endpoint knows, e.g. qwen2.5-coder:14b
	API      string `json:"api,omitempty"`     // "openai" (the default) or "anthropic"
	Key      bool   `json:"key,omitempty"`     // it needs an API key, which the person stores with `hive model key <name>`
	Context  int    `json:"context,omitempty"` // tokens it holds; the agent trims its history to fit (0: 32000)
	Price    *Price `json:"price,omitempty"`   // what a paid tier charges; none is free, or not known
}

// Price is what a model charges, in USD per million tokens. A turn's cost is
// its tokens at this price (the cache's reads and writes as input).
type Price struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

// Cost is t at p.
func (p Price) Cost(t harness.Tokens) float64 {
	return (float64(t.Input+t.CacheRead+t.CacheWrite)*p.Input + float64(t.Output)*p.Output) / 1e6
}

// Holds is nil when a run on m can be held to a budget: one that costs
// nothing, or one with a price the open agent counts against it as it goes.
// A model with a key and no price may be a paid tier whose spend nobody
// counts, and Claude Code (an Anthropic API) prices a turn as Claude's.
func (m OpenModel) Holds(budget float64) error {
	free := m.Price == nil && !m.Key || m.Price != nil && *m.Price == Price{}
	switch {
	case budget <= 0 || free:
		return nil
	case m.Price == nil:
		return errors.New("it needs a key and has no price, so what it spends isn't known; set its price in config.json (0 for a free tier)")
	case m.Anthropic():
		return errors.New("Claude Code runs it and can't hold a turn to the vendor's price")
	}
	return nil
}

// APIs are the kinds of endpoint a named model can be on.
var APIs = []string{"openai", "anthropic"}

// Anthropic reports whether the model's endpoint speaks Anthropic's
// Messages API, so Claude Code runs it instead of the open agent.
func (m OpenModel) Anthropic() bool { return m.API == "anthropic" }

// Where is the model as the person reads it: its id, endpoint, and who
// runs it when that isn't the open agent.
func (m OpenModel) Where() string {
	s := m.Model + " at " + m.Endpoint
	if m.Anthropic() {
		s += " (Anthropic API, on Claude Code)"
	}
	if m.Price != nil {
		s += fmt.Sprintf(", $%g/$%g per million tokens in/out", m.Price.Input, m.Price.Output)
	}
	return s
}

// Harness is the adapter that runs a turn on the model.
func (m OpenModel) Harness() harness.Harness {
	if m.Anthropic() {
		return harness.OpenClaude{}
	}
	return harness.Open{}
}

// Pick is one harness and model a run can go on.
type Pick struct {
	Harness string `json:"harness"`
	Model   string `json:"model,omitempty"`
}

func (p Pick) String() string { return HarnessName(p.Harness) + ":" + p.Model }

// Same reports whether two picks are the same harness and model.
func (p Pick) Same(q Pick) bool {
	return HarnessName(p.Harness) == HarnessName(q.Harness) && p.Model == q.Model
}

// CheckRoles are the seats the Hive's checks sit in. The general fallback
// list never moves them: a weaker model needs the checks more, so they go on
// another model only where the person routes them themselves.
var CheckRoles = []string{"reviewer", "security", "warden", "tribunal", JudgeRole}

// RouteRoles are the roles a route can name; "fallback" is the list every
// other role moves along at a limit.
var RouteRoles = []string{"lead", "worker", "reviewer", "security", "warden", "tribunal", JudgeRole, "sweep", "fallback"}

// Pick reads a route entry: a model named in Models (the open harness), a
// "harness:model" pair, or a model on the workspace's harness.
func (c Config) Pick(entry string) (Pick, error) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return Pick{}, errors.New("an empty model name")
	}
	if _, ok := c.Models[entry]; ok {
		return Pick{Harness: "open", Model: entry}, nil
	}
	if h, m, ok := strings.Cut(entry, ":"); ok {
		if _, err := c.Harnesses().Get(h); err == nil {
			if HarnessName(h) == "open" {
				if _, ok := c.Models[m]; !ok {
					return Pick{}, fmt.Errorf("%q: no model %q in models", entry, m)
				}
			}
			return Pick{Harness: HarnessName(h), Model: m}, nil
		}
		return Pick{}, fmt.Errorf("%q: no harness %q, and no model of that name in models (a model on an endpoint is added with `%s model add`)", entry, h, Cmd)
	}
	if HarnessName(c.Harness) == "open" {
		return Pick{}, fmt.Errorf("%q: no model of that name in models", entry)
	}
	return Pick{Harness: HarnessName(c.Harness), Model: entry}, nil
}

// Routed is the route the person (or the lead, for them) set for role, the
// project's own before the workspace's; nil when none is set.
func (c Config) Routed(role, project string) []string {
	if ps, ok := c.Projects[project]; ok && len(ps.Routes[role]) > 0 {
		return ps.Routes[role]
	}
	return c.Routes[role]
}

// Route is the picks a run of role in project goes on, in the order it moves
// along them when one is at its usage limit: the role's route, or the
// harness and model settings when it has none; then, for a role that isn't
// a check, the fallback list.
func (c Config) Route(role, project string) []Pick {
	var out []Pick
	add := func(entries []string) {
		for _, e := range entries {
			if p, err := c.Pick(e); err == nil && !slices.ContainsFunc(out, p.Same) {
				out = append(out, p)
			}
		}
	}
	add(c.Routed(role, project))
	if len(out) == 0 {
		out = append(out, c.base(role, project))
	}
	if !slices.Contains(CheckRoles, role) {
		add(c.Routed("fallback", project))
	}
	return out
}

// base is the harness and model settings for role: the workspace's harness
// and model, the lead's and reviewer's own models, a project's own.
func (c Config) base(role, project string) Pick {
	h, model := c.Harness, c.Model
	if role == "lead" && c.LeadModel != "" {
		model = c.LeadModel // a project's own model, below, is more specific
	}
	if ps, ok := c.Projects[project]; ok {
		if ps.Harness != "" {
			h = ps.Harness
		}
		if ps.Model != "" {
			model = ps.Model
		}
	}
	if role == "reviewer" && c.ReviewerModel != "" {
		model = c.ReviewerModel
	}
	if p, err := c.Pick(model); err == nil && p.Harness == "open" {
		return p // a named model set as a plain model setting
	}
	return Pick{Harness: HarnessName(h), Model: model}
}

// checkRoutes refuses a model or route entry the Hive can't run.
func (c Config) checkRoutes() error {
	for name, m := range c.Models {
		if name == "" || strings.ContainsAny(name, ":/ \t") {
			return fmt.Errorf("model name %q: use letters, digits, dots and dashes", name)
		}
		u, err := url.Parse(m.Endpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("model %s: endpoint %q isn't an http(s) URL", name, m.Endpoint)
		}
		if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			// Agents and the person see endpoints (the models op, `hive
			// model`), so a key put in one would leak.
			return fmt.Errorf("model %s: the endpoint holds no user, password or query; a key is stored with `%s model key %s`", name, Cmd, name)
		}
		if m.Model == "" {
			return fmt.Errorf("model %s: say which model the endpoint runs (model)", name)
		}
		if m.API != "" && !slices.Contains(APIs, m.API) {
			return fmt.Errorf("model %s: api %q is one of %s", name, m.API, strings.Join(APIs, ", "))
		}
	}
	check := func(where string, routes map[string][]string) error {
		for role, entries := range routes {
			if !slices.Contains(RouteRoles, role) {
				return fmt.Errorf("%sroute for %q: a route is for one of %s", where, role, strings.Join(RouteRoles, ", "))
			}
			for _, e := range entries {
				if _, err := c.Pick(e); err != nil {
					return fmt.Errorf("%sroute for %s: %v", where, role, err)
				}
			}
		}
		return nil
	}
	if err := check("", c.Routes); err != nil {
		return err
	}
	for name, ps := range c.Projects {
		if err := check("project "+name+": ", ps.Routes); err != nil {
			return err
		}
	}
	return nil
}

// openHost is the host a sandboxed run reaches model name's endpoint at, for
// its egress list; "" for one on this machine or a private network (an IP
// that isn't public), which only the person's local grant opens. A name that
// resolves to such an address is still refused by the egress proxy.
func (c Config) openHost(name string) string {
	u, err := url.Parse(c.Models[name].Endpoint)
	if err != nil {
		return ""
	}
	h := u.Hostname()
	if strings.EqualFold(h, "localhost") || strings.HasSuffix(strings.ToLower(h), ".localhost") {
		return ""
	}
	if ip, err := netip.ParseAddr(h); err == nil {
		ip = ip.Unmap()
		if !ip.IsGlobalUnicast() || ip.IsPrivate() || cgnat.Contains(ip) {
			return ""
		}
	}
	return h
}

// Plaintext reports whether model name's key would cross a public network
// unencrypted: a plain http:// endpoint on a host that isn't this machine or
// a private network.
func (c Config) Plaintext(name string) bool {
	u, err := url.Parse(c.Models[name].Endpoint)
	return err == nil && u.Scheme == "http" && c.openHost(name) != ""
}

// cgnat is the shared address space (RFC 6598), private in practice.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// Seats are the tribunal's seats: the tribunal route when one is set, else
// tribunal_models. A seat on a model named in Models sits on the open harness.
func (c Config) Seats(project string) []TribunalSeat {
	var out []TribunalSeat
	if r := c.Routed("tribunal", project); len(r) > 0 {
		for _, e := range r {
			if p, err := c.Pick(e); err == nil {
				out = append(out, TribunalSeat{Harness: p.Harness, Model: p.Model})
			}
		}
	} else {
		for _, t := range c.TribunalModels {
			if _, ok := c.Models[t.Model]; ok && t.Harness == "" {
				t.Harness = "open"
			}
			out = append(out, t)
		}
	}
	var seats []TribunalSeat
	for _, t := range out {
		if t.Model != "" && !slices.Contains(seats, t) {
			seats = append(seats, t)
		}
	}
	return seats
}

// Uses reports whether any role of any project runs on harness h: its
// route, its harness setting, a tribunal seat or the fallback list.
func (c Config) Uses(h string) bool {
	h = HarnessName(h)
	projects := []string{""}
	for name := range c.Projects {
		projects = append(projects, name)
	}
	for _, project := range projects {
		for _, role := range RouteRoles {
			if role == "fallback" {
				continue // in every other role's route
			}
			for _, p := range c.Route(role, project) {
				if HarnessName(p.Harness) == h {
					return true
				}
			}
		}
		for _, t := range c.Seats(project) {
			if t.Harness == "" && HarnessName(c.base("tribunal", project).Harness) == h || t.Harness != "" && HarnessName(t.Harness) == h {
				return true
			}
		}
	}
	return false
}

// Shown is role's route as the person reads it: each pick, the tribunal's
// seats, and for an unrouted security seat how it is chosen.
func (c Config) Shown(role, project string) []string {
	var out []string
	switch {
	case role == "fallback":
		return c.Routed(role, project)
	case role == "tribunal":
		for _, t := range c.Seats(project) {
			h := t.Harness
			if h == "" {
				h = c.base("tribunal", project).Harness
			}
			out = append(out, Pick{Harness: HarnessName(h), Model: t.Model}.String())
		}
		return out
	case role == "security" && len(c.Routed(role, project)) == 0:
		return []string{"a tribunal seat unlike the reviewer's"}
	}
	for _, p := range c.Route(role, project) {
		out = append(out, p.String())
	}
	return out
}
