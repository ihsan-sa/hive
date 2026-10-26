package harness

import (
	"testing"
	"time"
)

func TestLimitResets(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tz data")
	}
	now := time.Date(2026, 10, 25, 3, 12, 0, 0, ny)
	for _, c := range []struct {
		s    string
		want time.Time
	}{
		{"You've hit your weekly limit · resets Oct 26, 1pm (America/New_York)", time.Date(2026, 10, 26, 13, 0, 0, 0, ny)},
		{"You've hit your weekly limit · resets Oct 28, 12am (America/New_York)", time.Date(2026, 10, 28, 0, 0, 0, 0, ny)},
		{"5-hour limit reached ∙ resets 3:30pm (America/New_York)", time.Date(2026, 10, 25, 15, 30, 0, 0, ny)},
		{"limit reached, resets 2am (America/New_York)", time.Date(2026, 10, 26, 2, 0, 0, 0, ny)},
		{"limit · resets Jan 2, 9am (America/New_York)", time.Date(2027, 1, 2, 9, 0, 0, 0, ny)},
		{"Claude AI usage limit reached", time.Time{}},
	} {
		if got := LimitResets(c.s, now); !got.Equal(c.want) {
			t.Errorf("%q: got %v, want %v", c.s, got, c.want)
		}
		if !c.want.IsZero() && !LooksLimited(c.s) {
			t.Errorf("%q isn't recognized as a limit", c.s)
		}
	}
}

// On the renewal path a false match hides a failure from the person, so only
// a named limit, or a limit with its reset time, counts.
func TestSaysLimited(t *testing.T) {
	for _, s := range []string{
		"You've hit your weekly limit · resets Oct 28, 12am (America/New_York)",
		"5-hour limit reached ∙ resets 3:30pm",
		"Claude AI usage limit reached",
		"You've hit your limit · resets 1pm",
		"limit reached, resets 2am (America/New_York)",
	} {
		if !SaysLimited(s) {
			t.Errorf("%q is a limit", s)
		}
	}
	for _, s := range []string{
		"OAuth error: refresh token reset; retry limit exceeded",
		"rate limit on the token endpoint; connection reset by peer",
		"refresh token revoked",
	} {
		if SaysLimited(s) {
			t.Errorf("%q isn't a usage limit", s)
		}
	}
}
