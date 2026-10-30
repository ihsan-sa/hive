package improve

import (
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// readingKeep is how long a reading taken for a merge's measure stands for
// the measure going on after a restart: well past the four readings a pair
// takes under load (22 minutes each on 2026-10-30), short of a day's drift.
const readingKeep = 6 * time.Hour

// Reading is one score a merge's measure read beyond the merge's own score
// check: its parent's or its own again beside it (paired), or a kept
// commit's measured now (scoreNow). Role names which, so a measure a daemon
// restart cut short takes the ones it had from the journal and reads only
// the rest: hifi-receiver-2's cb7bbae0, its score changed and its parent to
// be read beside it, started the pair over at each of four restarts in an
// hour on 2026-10-30 and never got a result.
type Reading struct {
	Project string    `json:"project"`
	SHA     string    `json:"sha"`  // the merge measured
	Role    string    `json:"role"` // "parent 1", "parent 2", "merge 2", "now <sha>"
	At      string    `json:"at"`   // the commit read
	Score   float64   `json:"score"`
	Taken   time.Time `json:"taken"`
}

// applyReading keeps a fresh reading (m.mu held).
func (m *Module) applyReading(e core.Event) {
	var r Reading
	if e.Decode(&r) != nil || r.SHA == "" || time.Since(r.Taken) >= readingKeep {
		return
	}
	k := r.Project + "@" + r.SHA
	if m.readings[k] == nil {
		m.readings[k] = map[string]Reading{}
	}
	m.readings[k][r.Role] = r
}

// reading is the score role of merge's measure: the one taken already, if
// fresh, or read now, and recorded when it gave a number. A merge of ""
// (no merge's measure) reads every time.
func (m *Module) reading(p core.Project, merge, role, at string, read func() (float64, bool)) (float64, bool) {
	if merge != "" {
		m.mu.Lock()
		r, ok := m.readings[p.Name+"@"+merge][role]
		m.mu.Unlock()
		if ok && time.Since(r.Taken) < readingKeep {
			return r.Score, true
		}
	}
	v, ok := read()
	if ok && merge != "" {
		m.C.J.MustAppend("score.reading", Reading{Project: p.Name, SHA: merge, Role: role, At: at, Score: v, Taken: time.Now().UTC()})
	}
	return v, ok
}
