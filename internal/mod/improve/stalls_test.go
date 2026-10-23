package improve

import (
	"testing"
	"time"
)

// The limit is three times the 90th percentile of the stage's own history,
// never under the floor, which is all there is with no history.
func TestStallLimit(t *testing.T) {
	floor := time.Minute
	for _, c := range []struct {
		took []float64
		want time.Duration
	}{
		{nil, time.Minute},
		{[]float64{5, 10}, time.Minute},
		{[]float64{100, 30, 60}, 300 * time.Second},
		{[]float64{100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 9000}, 300 * time.Second}, // one outlier in twenty doesn't move it
	} {
		if got := StallLimit(c.took, floor); got != c.want {
			t.Errorf("StallLimit(%v) = %s, want %s", c.took, got, c.want)
		}
	}
}
