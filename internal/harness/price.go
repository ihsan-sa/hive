package harness

import "strings"

// ClaudePrice is a Claude model's list price, in USD per million tokens.
type ClaudePrice struct {
	Input, Output, CacheRead, CacheWrite float64
}

// claudePrices are Anthropic's list prices (claude-api skill, cached
// 2026-10-06), most specific family first: a model id or alias matches the
// first whose key it contains. A 5-minute cache write is 1.25x input; a
// cache read is 0.1x input but on Fable 5.1 and Opus 5.5.
var claudePrices = []struct {
	key string
	p   ClaudePrice
}{
	{"fable-5-1", ClaudePrice{10, 50, 0.25, 12.5}},
	{"mythos-5-1", ClaudePrice{10, 50, 0.25, 12.5}},
	{"fable", ClaudePrice{10, 50, 0.25, 12.5}}, // the alias is the current Fable; claude-fable-5 is caught below
	{"opus-5-5", ClaudePrice{4, 20, 0.2, 5}},
	{"opus-5", ClaudePrice{5, 25, 0.5, 6.25}},
	{"opus-4", ClaudePrice{5, 25, 0.5, 6.25}},
	{"opus", ClaudePrice{4, 20, 0.2, 5}},
	{"sonnet-5", ClaudePrice{2, 10, 0.2, 2.5}},
	{"sonnet-4", ClaudePrice{3, 15, 0.3, 3.75}},
	{"sonnet", ClaudePrice{2, 10, 0.2, 2.5}},
	{"haiku-4", ClaudePrice{1, 5, 0.1, 1.25}},
	{"haiku", ClaudePrice{0.1, 0.5, 0.01, 0.125}},
}

// PriceOf is the list price of a Claude model id or alias ("opus",
// "claude-opus-5-5[1m]"), and false for one it doesn't know.
func PriceOf(model string) (ClaudePrice, bool) {
	m := strings.ToLower(model)
	if strings.Contains(m, "fable-5") && !strings.Contains(m, "fable-5-1") || strings.Contains(m, "mythos-5") && !strings.Contains(m, "mythos-5-1") {
		return ClaudePrice{10, 50, 1, 12.5}, true // Fable 5 and Mythos 5 read the cache at 0.1x
	}
	for _, c := range claudePrices {
		if strings.Contains(m, c.key) {
			return c.p, true
		}
	}
	return ClaudePrice{}, false
}

// Cost is t at p.
func (p ClaudePrice) Cost(t Tokens) float64 {
	return (float64(t.Input)*p.Input + float64(t.Output)*p.Output + float64(t.CacheRead)*p.CacheRead + float64(t.CacheWrite)*p.CacheWrite) / 1e6
}
