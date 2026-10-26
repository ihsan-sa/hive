package harness

import (
	"math"
	"testing"
)

func TestPriceOf(t *testing.T) {
	for model, in := range map[string]float64{"opus": 4, "claude-opus-5-5[1m]": 4, "claude-opus-4-8": 5, "sonnet": 2, "claude-sonnet-4-6": 3, "haiku": 0.1, "claude-haiku-4-5": 1, "fable": 10, "claude-fable-5": 10} {
		if p, ok := PriceOf(model); !ok || p.Input != in {
			t.Errorf("%s: %+v %v", model, p, ok)
		}
	}
	if p, _ := PriceOf("claude-fable-5"); p.CacheRead != 1 {
		t.Errorf("fable 5 reads the cache at 0.1x: %+v", p)
	}
	if _, ok := PriceOf("gpt-5"); ok {
		t.Error("a model not Claude's has no list price")
	}
	p, _ := PriceOf("claude-opus-5-5")
	if got := p.Cost(Tokens{Input: 1e6, Output: 1e6, CacheRead: 1e6, CacheWrite: 1e6}); math.Abs(got-29.2) > 1e-9 {
		t.Errorf("cost %v", got)
	}
}

// Claude Code prints a reply once per content block, each with the reply's
// usage; the usage events name the reply so it counts once.
func TestParseReplyUsage(t *testing.T) {
	evs := Claude{}.Parse([]byte(`{"type":"assistant","message":{"id":"msg_1","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":2,"cache_creation_input_tokens":100,"cache_read_input_tokens":300,"output_tokens":87}}}`))
	if len(evs) != 3 || evs[1].Kind != "usage" || evs[1].Reply != "msg_1" || *evs[1].Usage != (Tokens{Input: 2, Output: 87, CacheRead: 300, CacheWrite: 100}) {
		t.Fatalf("got %+v", evs)
	}
	if evs := (Open{}).Parse([]byte(`{"type":"assistant","message":{"content":[],"usage":{"input_tokens":5000}}}`)); len(evs) != 1 {
		t.Fatalf("the open agent's reply names none and its usage is the context: %+v", evs)
	}
}
