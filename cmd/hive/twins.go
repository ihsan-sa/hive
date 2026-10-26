package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// twinTurns lists the turns the journal at path shows starting since since
// while the same run's last turn hadn't ended: a twin agent in one checkout
// (issue #1022). It is the sweep's query that found them, kept so the fix
// can be seen holding.
func twinTurns(path string, since time.Time) ([]string, error) {
	type start struct {
		turn int
		at   time.Time
	}
	open := map[string]start{} // each run's turn started and not yet ended
	var twins []string
	err := core.ReadJSONL(path, func(raw []byte) {
		var e core.Event
		if json.Unmarshal(raw, &e) != nil {
			return
		}
		var t struct {
			ID   string `json:"id"`
			Turn int    `json:"turn"`
		}
		switch e.Kind {
		case "run.turn_started":
			e.Decode(&t)
			if prev, ok := open[t.ID]; ok && !e.At.Before(since) {
				twins = append(twins, fmt.Sprintf("%s turn %d started %s after turn %d, which hadn't ended (%s)", t.ID, t.Turn, e.At.Sub(prev.at).Round(100*time.Millisecond), prev.turn, e.At.Format(time.RFC3339)))
			}
			open[t.ID] = start{t.Turn, e.At}
		case "run.turn_ended":
			e.Decode(&t)
			delete(open, t.ID)
		}
	})
	return twins, err
}
