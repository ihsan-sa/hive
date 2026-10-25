package work

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// Reads are proportionate to the change (issue #374): a small change no guard
// names merges on its tests and checks with no reviewer seat, and one in
// SampleUnread of those gets one read after merge, whose fix files a
// follow-up. This file holds that sampled read and the reads' own figures.

// markUnread records that t merged with no read, and whether it is sampled.
// A docs-only small change was never read and isn't sampled either.
func (m *Module) markUnread(t *Task, p core.Project, main string) {
	ch, err := m.C.Effects.Classify(p, main, t.SHA)
	if err != nil || !(ch.Small || ch.Unread) || m.C.Effects.Reviewed(p, ch, t.SHA) {
		return
	}
	t.Unread = true
	// The same pick Effects.Merge recorded in effect.merged.
	t.Sample = !ch.Small && core.Sampled(t.ID, m.C.Effects.Config(p, main).SampleUnread)
}

// sampleRead runs the one read a sampled unread merge gets: wait says the
// read is still to come. A land lets the task finish as usual (its advice
// filed as the follow-up); a fix files one follow-up brief with the findings,
// for a worker to fix or revert through the usual path. A reader that gives
// no usable verdict three times is let go: the change has merged on its
// tests and checks already.
func (m *Module) sampleRead(t *Task, p core.Project) (wait bool, err error) {
	if !t.Sample || t.Kind != "work" {
		return false, nil
	}
	id := fmt.Sprintf("p-%s-%s-%d", t.ID, core.Short(t.SHA), t.Reviews)
	r, ok := m.C.Runs.Get(id)
	if !ok {
		intro := fmt.Sprintf("Sampled read of task %s: %q. It merged on its tests and the forge's checks with no read, being small and touching no guarded path; one such merge in a few gets this read after the fact, to keep that path honest. Say fix for what should be fixed or reverted on main now; the Hive files it as a follow-up.", t.ID, t.Label())
		if err := m.startRead(t, p, id, intro, false); err != nil {
			return true, err
		}
		m.save(t, "sampled read "+id+" reading the merged "+core.Short(t.SHA))
		return true, nil
	}
	if !r.Terminal() {
		return true, nil
	}
	v, ok := verdictOf(r)
	if !ok {
		if t.Reviews++; t.Reviews >= 3 {
			t.Sample = false
			m.save(t, "the sampled read gave no usable verdict three times; let go")
			return false, nil
		}
		m.save(t, "the sampled read gave no usable verdict; asking again")
		return true, nil
	}
	v.Project, v.SHA, v.Run = p.Name, t.SHA, r.ID
	if err := m.C.Effects.RecordVerdict(v); err != nil {
		return true, err
	}
	fb := verdictText(v)
	m.comment(p.Repo, t.PR.Number, fmt.Sprintf("**Sampled read after merge, of %s: %s**\n\n%s", core.Short(t.SHA), v.Verdict, fb))
	if v.Verdict == "land" {
		keepAdvice(t, v.Advice)
		t.Sample = false
		m.save(t, "sampled read: land")
		return false, nil
	}
	if len(m.List(func(x Task) bool { return x.FollowUp == t.ID })) == 0 {
		known := fmt.Sprintf("%s merged as %s with no read (small, no guarded path). The sampled read after merge, %s, asked for fixes:\n\n%s", orNone(t.PR.URL), core.Short(t.MergeSHA), r.ID, fb)
		if len(t.Advice) > 0 {
			known += "\n\nAdvice from its reads:\n\n- " + strings.Join(t.Advice, "\n- ")
		}
		if _, err := m.File(Brief{Project: p.Name, From: r.ID, Chat: t.Chat, Thread: t.Thread, FollowUp: t.ID,
			Title: "Fix what the sampled read found in " + core.Clip(t.Label(), 100),
			Done:  "Each finding below is fixed on main, or the merge reverted, or judged wrong with the reason in your account.",
			Known: known}); err != nil {
			return true, err
		}
	}
	t.Sample, t.Advice = false, nil // the follow-up carries the advice too
	m.save(t, "sampled read: fix; filed a follow-up")
	return false, nil
}

// verdictOf reads a reviewer run's structured verdict: ok only for land or
// fix, and a land only with the token from its brief.md.
func verdictOf(r core.Run) (core.Verdict, bool) {
	var v core.Verdict
	var tok struct{ Token string }
	json.Unmarshal(r.Structured, &tok)
	if err := json.Unmarshal(r.Structured, &v); err != nil || (v.Verdict != "land" && v.Verdict != "fix") || (v.Verdict == "land" && tok.Token != reviewToken(r.ID)) {
		return v, false
	}
	return v, true
}

// statsWindow is how many merges, and each seat's verdicts, the figures cover.
const statsWindow = 50

// A SeatStat is one reader seat's verdicts over its last statsWindow.
type SeatStat struct {
	Seat  string `json:"seat"` // ordinary, read2, security, panel, sampled
	Reads int    `json:"reads"`
	Lands int    `json:"lands"`
}

// LandRate is the share of its reads that said land.
func (s SeatStat) LandRate() float64 {
	if s.Reads == 0 {
		return 0
	}
	return float64(s.Lands) / float64(s.Reads)
}

// ReadStats are a project's reads per merge over its last statsWindow merges
// and each seat's land rate, so a seat that lands nearly everything shows.
type ReadStats struct {
	Merges int        `json:"merges"`
	Unread int        `json:"unread"` // of those merges, the ones that merged with no read
	Reads  int        `json:"reads"`  // verdicts from every seat on the tasks of those merges
	Seats  []SeatStat `json:"seats,omitempty"`
}

// PerMerge is reads per merge.
func (s ReadStats) PerMerge() float64 {
	if s.Merges == 0 {
		return 0
	}
	return float64(s.Reads) / float64(s.Merges)
}

// Line says the figures in one line for status, flagging a seat whose land
// rate is 98% or more over 20 reads or more: a read that never sends back
// may not be earning its cost.
func (s ReadStats) Line() string {
	if s.Merges == 0 && len(s.Seats) == 0 {
		return ""
	}
	out := fmt.Sprintf("reads: %.1f per merge over the last %d merges (%d with no read)", s.PerMerge(), s.Merges, s.Unread)
	var seats []string
	for _, st := range s.Seats {
		x := fmt.Sprintf("%s %d/%d land (%.0f%%)", st.Seat, st.Lands, st.Reads, 100*st.LandRate())
		if st.Reads >= 20 && st.LandRate() >= 0.98 {
			x += " !"
		}
		seats = append(seats, x)
	}
	if len(seats) > 0 {
		out += "; seats, last " + fmt.Sprint(statsWindow) + " each: " + strings.Join(seats, ", ")
	}
	return out
}

var furtherRead = regexp.MustCompile(`-read\d+$`)

// seatOf names the seat a verdict came from.
func seatOf(v core.Verdict) string {
	switch {
	case strings.HasPrefix(v.Seat, "panel"):
		return "panel"
	case v.Seat != "":
		return v.Seat
	case strings.HasPrefix(v.Run, "p-"):
		return "sampled"
	case furtherRead.MatchString(v.Run):
		return "read2"
	}
	return "ordinary"
}

type statsAt struct {
	at time.Time
	st ReadStats
}

// ReadStats are a project's figures, worked out at most once a minute: the
// state they go in is asked for often, and they read the whole journal.
func (m *Module) ReadStats(project string) ReadStats {
	m.statsMu.Lock()
	defer m.statsMu.Unlock()
	if c, ok := m.stats[project]; ok && time.Since(c.at) < time.Minute {
		return c.st
	}
	st := m.readStats(project)
	if m.stats == nil {
		m.stats = map[string]statsAt{}
	}
	m.stats[project] = statsAt{time.Now(), st}
	return st
}

// readStats works out a project's figures from the journal's verdicts and
// merges, and the tasks their runs read for.
func (m *Module) readStats(project string) ReadStats {
	var st ReadStats
	type merged struct {
		pr     int
		unread bool
	}
	var merges []merged
	var verdicts []core.Verdict
	m.C.J.Replay(func(e core.Event) {
		switch e.Kind {
		case "effect.merged":
			var x struct {
				Project string
				PR      int
				Unread  bool
			}
			if e.Decode(&x) == nil && x.Project == project {
				merges = append(merges, merged{x.PR, x.Unread})
			}
		case "effect.verdict":
			var v core.Verdict
			if e.Decode(&v) == nil && v.Project == project {
				verdicts = append(verdicts, v)
			}
		}
	})
	if len(merges) > statsWindow {
		merges = merges[len(merges)-statsWindow:]
	}
	st.Merges = len(merges)
	prs := map[int]bool{}
	for _, mg := range merges {
		prs[mg.pr] = true
		if mg.unread {
			st.Unread++
		}
	}
	// A task's reads count toward the merges of its pull requests (its own,
	// and each checkpoint's) in the window.
	tasks := map[string]bool{}
	for _, t := range m.List(func(t Task) bool { return t.Project == project }) {
		in := t.MergeSHA != "" && prs[t.PR.Number]
		for _, b := range t.Banks {
			in = in || prs[b.PR.Number]
		}
		if in {
			tasks[t.ID] = true
		}
	}
	bySeat := map[string][]bool{}
	for _, v := range verdicts {
		if r, ok := m.C.Runs.Get(v.Run); ok && tasks[r.Task] {
			st.Reads++
		}
		s := seatOf(v)
		bySeat[s] = append(bySeat[s], v.Verdict == "land")
	}
	for seat, lands := range bySeat {
		if len(lands) > statsWindow {
			lands = lands[len(lands)-statsWindow:]
		}
		ss := SeatStat{Seat: seat, Reads: len(lands)}
		for _, l := range lands {
			if l {
				ss.Lands++
			}
		}
		st.Seats = append(st.Seats, ss)
	}
	order := []string{"ordinary", "read2", "security", "panel", "sampled"}
	sort.Slice(st.Seats, func(i, j int) bool {
		a, b := slices.Index(order, st.Seats[i].Seat), slices.Index(order, st.Seats[j].Seat)
		return a < b || a == b && st.Seats[i].Seat < st.Seats[j].Seat
	})
	return st
}
