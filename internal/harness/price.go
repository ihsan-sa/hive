package harness

import "strings"

// Price is a model's list price, in USD per million tokens.
type Price struct {
	Input, Output, CacheRead, CacheWrite float64
}

// claudePrices are Anthropic's list prices (claude-api skill, cached
// 2026-10-06), most specific family first: a model id or alias matches the
// first whose key it contains. A 5-minute cache write is 1.25x input; a
// cache read is 0.1x input but on Fable 5.1 and Opus 5.5.
var claudePrices = []struct {
	key string
	p   Price
}{
	{"fable-5-1", Price{10, 50, 0.25, 12.5}},
	{"mythos-5-1", Price{10, 50, 0.25, 12.5}},
	{"fable", Price{10, 50, 0.25, 12.5}}, // the alias is the current Fable; claude-fable-5 is caught below
	{"opus-5-5", Price{4, 20, 0.2, 5}},
	{"opus-5", Price{5, 25, 0.5, 6.25}},
	{"opus-4", Price{5, 25, 0.5, 6.25}},
	{"opus", Price{4, 20, 0.2, 5}},
	{"sonnet-5", Price{2, 10, 0.2, 2.5}},
	{"sonnet-4", Price{3, 15, 0.3, 3.75}},
	{"sonnet", Price{2, 10, 0.2, 2.5}},
	{"haiku-4", Price{1, 5, 0.1, 1.25}},
	{"haiku", Price{0.1, 0.5, 0.01, 0.125}},
}

// ListPrice is a harness's list price for a model it runs, and false when it
// has none (it says none, or doesn't know the model). A harness says one with
// a ListPrice method.
func ListPrice(h Harness, model string) (Price, bool) {
	if p, ok := h.(interface{ ListPrice(string) (Price, bool) }); ok {
		return p.ListPrice(model)
	}
	return Price{}, false
}

// ListPrice is Claude's list price for a model id or alias.
func (Claude) ListPrice(model string) (Price, bool) { return claudePrice(model) }

// claudePrice is the list price of a Claude model id or alias ("opus",
// "claude-opus-5-5[1m]"), and false for one it doesn't know.
func claudePrice(model string) (Price, bool) {
	m := strings.ToLower(model)
	if strings.Contains(m, "fable-5") && !strings.Contains(m, "fable-5-1") || strings.Contains(m, "mythos-5") && !strings.Contains(m, "mythos-5-1") {
		return Price{10, 50, 1, 12.5}, true // Fable 5 and Mythos 5 read the cache at 0.1x
	}
	for _, c := range claudePrices {
		if strings.Contains(m, c.key) {
			return c.p, true
		}
	}
	return Price{}, false
}

// Cost is t at p.
func (p Price) Cost(t Tokens) float64 {
	return (float64(t.Input)*p.Input + float64(t.Output)*p.Output + float64(t.CacheRead)*p.CacheRead + float64(t.CacheWrite)*p.CacheWrite) / 1e6
}

// Usage is a turn's tokens and model as its lines go by: a usage event that
// names its reply replaces that reply's earlier one, one that names none adds.
type Usage struct {
	Model   string // the model the harness said it began on
	loose   Tokens
	replies map[string]Tokens
}

// Add folds one event in.
func (u *Usage) Add(ev Event) {
	switch {
	case ev.Kind == "init" && ev.Model != "":
		u.Model = ev.Model
	case ev.Kind != "usage" || ev.Usage == nil:
	case ev.Reply != "":
		if u.replies == nil {
			u.replies = map[string]Tokens{}
		}
		u.replies[ev.Reply] = *ev.Usage
	default:
		u.loose.Add(*ev.Usage)
	}
}

// Total is the tokens so far.
func (u *Usage) Total() Tokens {
	t := u.loose
	for _, r := range u.replies {
		t.Add(r)
	}
	return t
}
