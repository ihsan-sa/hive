package softlayer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
)

// Report is what one Hive did, read from its journal: who ran, who talked to
// whom, what they asked for and what landed. It is the observation; it
// judges nothing.
type Report struct {
	Variant  string         `json:"variant,omitempty"`
	From     time.Time      `json:"from"`
	To       time.Time      `json:"to"`
	Events   int            `json:"events"`
	Runs     map[string]int `json:"runs"`  // by role
	Turns    int            `json:"turns"` // agent turns ended
	Messages int            `json:"messages"`
	Pairs    map[string]int `json:"pairs"` // "worker→lead": count
	// Peer is a message from one worker run to another: the lateral talk the
	// person asked to see ("2 of them worker to worker"). A sweep's or
	// reviewer's message to a worker is in Pairs, not here.
	Peer int `json:"peer"`
	// Room is what agents said to the room (hive agent message --to room),
	// once per post however many heard it; each hearer's copy is also in
	// Messages, Pairs and Peer.
	Room     int            `json:"room"`
	Briefs   int            `json:"briefs"`  // work an agent filed
	Helpers  int            `json:"helpers"` // helpers an agent spawned
	Asks     int            `json:"asks"`    // requests past an agent's reach (warden, tribunal, person)
	Finished map[string]int `json:"finished"`
	Notes    int            `json:"notes"`    // knowledge-base notes
	Feedback int            `json:"feedback"` // feedback about the Hive itself
	Posts    int            `json:"posts"`    // an agent's posts in a chat
	Merged   int            `json:"merged"`
	Reverted int            `json:"reverted"`
	// Outcome is filled in by the experiment: landed, open or stalled, and why.
	Outcome string `json:"outcome,omitempty"`
	// PeakMB and PeakLoad are filled in by the experiment too: the most
	// memory the run's processes held at once (their summed RSS, sampled)
	// and the highest 1-minute load the machine had while the arm ran.
	PeakMB   int     `json:"peak_mb,omitempty"`
	PeakLoad float64 `json:"peak_load,omitempty"`
}

type event struct {
	At   time.Time       `json:"at"`
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

// Observe reads a journal (events.jsonl) and reports the events at or after
// since (the zero time: all of it) and before until (zero: no end). A line it
// can't read is skipped: a journal being written may end mid-line.
func Observe(r io.Reader, since, until time.Time) (Report, error) {
	rep := Report{Runs: map[string]int{}, Pairs: map[string]int{}, Finished: map[string]int{}}
	roles := map[string]string{} // run id → role, from every run.created, even before since
	seen := map[string]bool{}    // tasks, requests and feedback already filed
	said := map[string]bool{}    // room posts already counted, by sender and text
	reverts := map[string]bool{} // project and PR of each revert task, which merges as an undo
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Bytes()
		var e event
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		var d map[string]any
		json.Unmarshal(e.Data, &d)
		str := func(k string) string { s, _ := d[k].(string); return s }
		if e.Kind == "run.created" {
			roles[str("id")] = str("role")
		}
		if e.Kind == "task.saved" && str("kind") == "revert" {
			if pr, _ := d["pr"].(map[string]any); pr != nil {
				if n, _ := pr["number"].(float64); n > 0 {
					reverts[fmt.Sprint(str("project"), "#", n)] = true
				}
			}
		}
		// The first time the journal names a task, request or feedback is its
		// filing; later saves of it are not new, even inside the window.
		first := false
		if key := firstKey(e.Kind, str("id")); key != "" && !seen[key] {
			seen[key], first = true, true
		}
		if (!since.IsZero() && e.At.Before(since)) || (!until.IsZero() && !e.At.Before(until)) {
			continue
		}
		rep.Events++
		if rep.From.IsZero() || e.At.Before(rep.From) {
			rep.From = e.At
		}
		if e.At.After(rep.To) {
			rep.To = e.At
		}
		switch e.Kind {
		case "run.created":
			rep.Runs[str("role")]++
		case "run.turn_ended":
			rep.Turns++
		case "msg.sent":
			from, to := endpoint(roles, str("from")), endpoint(roles, str("to"))
			rep.Messages++
			rep.Pairs[from+"→"+to]++
			if from == "worker" && to == "worker" {
				rep.Peer++
			}
			if text := str("text"); strings.HasPrefix(text, chat.RoomMark) && !said[str("from")+"\x00"+text] {
				said[str("from")+"\x00"+text] = true
				rep.Room++
			}
		case "task.saved":
			if _, ok := roles[str("from")]; !first || !ok {
				continue // the person's, or a chat's: not an agent's filing
			}
			if str("kind") == "helper" {
				rep.Helpers++
			} else {
				rep.Briefs++
			}
		case "risk.saved":
			if first && str("kind") == "request" {
				rep.Asks++
			}
		case "run.finish":
			f, _ := d["finish"].(map[string]any)
			s, _ := f["status"].(string)
			rep.Finished[s]++
		case "kb.note":
			rep.Notes++
		case "feedback.saved":
			if first {
				rep.Feedback++
			}
		case "chat.posted":
			if _, ok := roles[str("from")]; ok {
				rep.Posts++
			}
		case "effect.merged":
			// A revert lands as a merge of its own task's PR: an undo, not work.
			if n, _ := d["pr"].(float64); reverts[fmt.Sprint(str("project"), "#", n)] {
				rep.Reverted++
			} else {
				rep.Merged++
			}
		}
	}
	return rep, sc.Err()
}

func firstKey(kind, id string) string {
	switch kind {
	case "task.saved", "risk.saved", "feedback.saved":
		return kind + ":" + id
	}
	return ""
}

// endpoint names one end of a message by its role: a run id the journal
// created, or the names hive agent message takes (lead, parent, person).
func endpoint(roles map[string]string, id string) string {
	if r, ok := roles[id]; ok && r != "" {
		return r
	}
	switch {
	case id == "lead" || strings.HasPrefix(id, "lead-"):
		return "lead"
	case id == "person" || id == "parent":
		return id
	}
	return "other"
}

func sorted(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	var b []string
	for _, k := range keys {
		b = append(b, fmt.Sprintf("%s %d", k, m[k]))
	}
	if len(b) == 0 {
		return "-"
	}
	return strings.Join(b, ", ")
}

// Table is the reports side by side, one row per arm, in Markdown.
func Table(reps []Report) string {
	var b strings.Builder
	b.WriteString("| arm | outcome | minutes | runs | turns | messages | peer | room | briefs | helpers | asks | finished | notes | feedback | merged | reverted | peak MB | peak load |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range reps {
		runs := 0
		for _, n := range r.Runs {
			runs += n
		}
		name := r.Variant
		if name == "" {
			name = "-"
		}
		fmt.Fprintf(&b, "| %s | %s | %.0f | %d | %d | %d | %d | %d | %d | %d | %d | %s | %d | %d | %d | %d | %s | %s |\n", name, orDash(r.Outcome),
			r.To.Sub(r.From).Minutes(), runs, r.Turns, r.Messages, r.Peer, r.Room, r.Briefs, r.Helpers, r.Asks, sorted(r.Finished), r.Notes, r.Feedback, r.Merged, r.Reverted,
			peak(r.PeakMB > 0, "%d", r.PeakMB), peak(r.PeakLoad > 0, "%.1f", r.PeakLoad))
	}
	b.WriteString("\nWho talked to whom, and which roles ran:\n\n")
	for _, r := range reps {
		fmt.Fprintf(&b, "- **%s**: messages %s; runs %s\n", orDash(r.Variant), sorted(r.Pairs), sorted(r.Runs))
	}
	return b.String()
}

// peak is a measure, or "-" for an arm nobody measured (hive-soft observe).
func peak(measured bool, format string, v any) string {
	if !measured {
		return "-"
	}
	return fmt.Sprintf(format, v)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
