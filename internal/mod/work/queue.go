package work

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// The queue: a filed brief waits as queued until a worker can take it. Briefs
// start in the order they were filed, or the order the lead set, and only as
// many work at once as the pace allows (pace.go, under max_workers) while the
// machine isn't loaded: the brake is the machine. Two briefs on the same
// files start side by side: a conflict is settled at merge, where the one
// that meets it goes back to its worker with main to merge. A hold on shared
// files took two thirds of all queue time and what it caught was never
// measured (docs/research/forced.md). Nothing here stops a worker that has
// started.
//
// A brief that only changes module prompts (every path it names is a .md
// file under modules/) is quick: it goes first in line, after those the lead
// started now, and may start past the pace while no other quick brief is
// working, since a prompt edit takes a worker little time and plan. It still
// waits on a strained machine and on a plan near its end.
//
// A worker that has gone quiet (it keeps ending turns without finishing)
// counts toward no one's max_workers until it resumes.

// Queued reports whether t is waiting for a worker.
func (t Task) Queued() bool { return t.Stage == StageQueued || t.Stage == StageIssued }

// inLine sorts waiting tasks: those the lead started now first, then quick
// ones, then those the lead ordered, in its order, then the rest as filed.
func (m *Module) inLine(q []Task) {
	quick := map[string]bool{}
	for _, t := range q {
		quick[t.ID] = m.quick(t)
	}
	sort.SliceStable(q, func(i, j int) bool {
		a, b := q[i], q[j]
		if a.Now != b.Now {
			return a.Now
		}
		if quick[a.ID] != quick[b.ID] {
			return quick[a.ID]
		}
		if (a.Order > 0) != (b.Order > 0) {
			return a.Order > 0
		}
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		return a.Created.Before(b.Created)
	})
}

// quick reports whether t's brief only changes module prompts: it names at
// least one path, and every path-like word in it is a .md file under modules/
// (one that doesn't exist yet counts) or a merge=union file in the clone. A
// package, a directory, a code file or a file still to be made, named
// alongside a prompt, makes it an ordinary brief.
func (m *Module) quick(t Task) bool {
	root := m.C.WS.Project(t.Project)
	if !core.Exists(root) { // the queue reads the clone before any worker has synced it
		if p, ok := m.C.Projects.Get(t.Project); !ok || m.C.Effects.Clone(p) != nil {
			return false
		}
	}
	text := t.Title + "\n" + t.Done + "\n" + t.Known
	var union []attrRule
	read, n := false, 0
	for _, p := range pathLike.FindAllString(text, -1) {
		p = path.Clean(strings.TrimRight(strings.TrimPrefix(p, "./"), "."))
		switch {
		case p == ".." || strings.HasPrefix(p, "../"): // never looked up outside the clone
			return false
		case strings.HasPrefix(p, "modules/") && path.Ext(p) == ".md":
			n++
		default:
			if !read {
				union, read = unionRules(root), true
			}
			fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p)))
			if err != nil || fi.IsDir() || !isUnion(union, p) {
				return false
			}
		}
	}
	return n > 0
}

// admit decides which waiting tasks start a worker now, and why each other
// one waits. tasks is every task Tick looks at.
func (m *Module) admit(tasks []Task) (start map[string]bool, wait map[string]string) {
	cfg := m.C.WS.LoadConfig()
	working, quickWorking := 0, 0
	var ahead, line []Task // ahead: being worked on, or being started right now
	m.mu.Lock()
	for _, t := range tasks {
		switch {
		case t.Queued() && !m.busy[t.ID]:
			line = append(line, t)
		case t.Stage == StageWorking && m.quiet(t):
			// A quiet worker holds no slot: three held 3 of 8 slots for
			// 10-12h while ~39 briefs waited. A message resumes it
			// (core.Runs), and then it counts again, even if that puts the
			// workers past the limit (by as many as resume at once) until
			// slots free; none start meanwhile.
		case t.Stage == StageWorking || t.Queued(): // or a queued one being started right now
			ahead = append(ahead, t)
			working++
		}
		// The rest hold no slot: new ones join the line once issued, and
		// pushed ones (landing, testing, reviewing, merging) merge main on
		// a conflict, with their worker if need be.
	}
	m.mu.Unlock()
	for _, t := range ahead {
		if m.quick(t) {
			quickWorking++
		}
	}
	m.inLine(line)
	strain, _ := machine(cfg)
	// planOK says the plan has room for a quick brief past the pace: read
	// once, on need.
	var planOK *bool
	plan := func() bool {
		if planOK == nil {
			ok := m.planSpent(cfg) == ""
			planOK = &ok
		}
		return *planOK
	}
	// decide sorts the line at limit, and says whether a brief waits on the
	// limit itself: only then may the pace grow, as a held brief would leave
	// a new slot empty.
	decide := func(limit int) (start map[string]bool, wait map[string]string, capped bool) {
		start, wait = map[string]bool{}, map[string]string{}
		working, quickWorking := working, quickWorking
		// The reason is saved with the task, so it names the limit but not
		// its live value: the pace moved about every minute under load, and
		// each move re-saved every waiting task with its whole brief (one
		// 36 times in 40 minutes). The pace itself shows in Pace.
		full := fmt.Sprintf("waiting for a worker (max_workers %d)", cfg.MaxWorkers)
		if cfg.MaxWorkers == 0 || limit < cfg.MaxWorkers {
			full = "waiting for a worker (at the pace)"
		}
		for _, t := range line {
			quick := m.quick(t)
			switch {
			case t.Held:
				wait[t.ID] = "held by " + cmp.Or(t.HeldBy, "the lead")
			case working >= limit && (!quick || quickWorking > 0 || !plan()):
				wait[t.ID], capped = full, true
			case loadHolds(strain, working, cmp.Or(cfg.MaxWorkers, defaultWorkers)):
				wait[t.ID] = "waiting: " + steady(strain)
			default:
				start[t.ID] = true
				working++
				if quick {
					quickWorking++
				}
			}
		}
		return start, wait, capped
	}
	// The pace moves on what waits at its limit now; if that moves it, the
	// line is sorted again at the new one.
	limit := m.paceWorkers(cfg, working, false)
	start, wait, capped := decide(limit)
	if capped {
		if l := m.paceWorkers(cfg, working, true); l != limit {
			start, wait, _ = decide(l)
		}
	}
	return start, wait
}

// quiet reports whether t's worker keeps ending turns without finishing.
func (m *Module) quiet(t Task) bool {
	r, ok := m.C.Runs.Get(t.Worker)
	return ok && r.Status == core.StatusQuiet
}

// pathLike is a word that reads as a path in the project.
var pathLike = regexp.MustCompile(`[A-Za-z0-9_.\-]+(?:/[A-Za-z0-9_.\-]+)+`)

// attrRule is one .gitattributes line that sets or clears merge: union is
// whether it makes the files it matches merge as union.
type attrRule struct {
	pattern string
	union   bool
}

// unionRules are the .gitattributes lines in the clone root that touch the
// merge attribute, in file order. Only the root file is read, and macros
// ([attr]) and quoted patterns are skipped.
func unionRules(root string) []attrRule {
	b, err := core.SafeRead(filepath.Join(root, ".gitattributes"), 1<<20)
	if err != nil {
		return nil
	}
	var out []attrRule
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || strings.HasPrefix(f[0], "#") || strings.HasPrefix(f[0], "!") || strings.HasPrefix(f[0], "[attr]") || strings.HasPrefix(f[0], `"`) {
			continue // git ignores negative patterns
		}
		for _, attr := range f[1:] {
			switch {
			case attr == "merge=union":
				out = append(out, attrRule{f[0], true})
			case attr == "merge" || attr == "-merge" || attr == "!merge" || strings.HasPrefix(attr, "merge="):
				out = append(out, attrRule{f[0], false})
			}
		}
	}
	return out
}

// isUnion reports whether the clone path p merges as union: the last rule
// that matches it decides, as in git.
func isUnion(rules []attrRule, p string) bool {
	union := false
	for _, r := range rules {
		if attrMatch(r.pattern, p) {
			union = r.union
		}
	}
	return union
}

// attrMatch reports whether the clone path p matches a .gitattributes
// pattern: one without a slash matches the file's name at any depth, one with
// a slash the path from the root, where "**" stands for any number of
// directories.
func attrMatch(pat, p string) bool {
	if strings.HasSuffix(pat, "/") {
		return false // matches only directories, and attributes don't recurse
	}
	if !strings.Contains(pat, "/") {
		ok, _ := path.Match(pat, path.Base(p))
		return ok
	}
	return matchSegs(strings.Split(strings.TrimPrefix(pat, "/"), "/"), strings.Split(p, "/"))
}

func matchSegs(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if matchSegs(pat[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	ok, _ := path.Match(pat[0], segs[0])
	return ok && matchSegs(pat[1:], segs[1:])
}

// opQueue shows the queue, and with arguments sets its order or holds briefs.
func (m *Module) opQueue(r core.Run, args json.RawMessage) (any, error) {
	var a struct {
		Order   ids `json:"order"`
		Hold    ids `json:"hold"`
		Release ids `json:"release"`
		Now     ids `json:"now"`
		Why     string
	}
	if len(args) > 0 {
		if err := core.Bind(args, &a); err != nil {
			return nil, err
		}
	}
	if len(a.Now) > 0 && strings.TrimSpace(a.Why) == "" {
		return nil, errors.New("now puts a brief first in line: say why with why (it goes on the record)")
	}
	type change struct {
		order int
		held  *bool
		now   bool
	}
	changes := map[string]*change{}
	at := func(id string) *change {
		if changes[id] == nil {
			changes[id] = &change{order: -1}
		}
		return changes[id]
	}
	yes, no := true, false
	for i, id := range a.Order {
		at(id).order = i + 1
	}
	for _, id := range a.Hold {
		at(id).held = &yes
	}
	for _, id := range a.Release {
		at(id).held = &no
	}
	for _, id := range a.Now {
		at(id).now, at(id).held = true, &no
	}
	var notes []string
	if len(a.Order) > 0 { // a new order replaces the old one: the rest go after, as filed
		// Only in the caller's reach: another project's order is kept, and
		// not one note each for the briefs it never named.
		for _, t := range m.List(func(t Task) bool { return t.Queued() && t.Order > 0 && m.reach(r, t.Project) == nil }) {
			if changes[t.ID] == nil {
				at(t.ID).order = 0
			}
		}
	}
	for id, c := range changes {
		t, ok := m.Get(id)
		switch {
		case !ok:
			notes = append(notes, "no task "+id)
			continue
		case m.C.Runs.Walled(r, t.Project) != nil:
			notes = append(notes, "no task "+id)
			continue
		case m.reach(r, t.Project) != nil:
			notes = append(notes, fmt.Sprintf("%s is on project %s; you steer only your own", id, t.Project))
			continue
		case !t.Queued():
			notes = append(notes, fmt.Sprintf("%s has left the queue (%s); a started worker isn't stopped by the queue", id, t.Stage))
			continue
		}
		m.mu.Lock()
		busy := m.busy[id]
		if !busy {
			m.busy[id] = true
		}
		m.mu.Unlock()
		if busy {
			notes = append(notes, id+" is starting its worker right now")
			continue
		}
		note := ""
		held := t.Held
		if c.held != nil {
			held = *c.held
		}
		if moved := c.order >= 0 && c.order != t.Order || c.now && !t.Now; moved && !held {
			// Its wait was weighed in the old line: the next tick says anew
			// (issue #701).
			t.Reason = "moved in line by " + r.ID + "; the next tick weighs it"
		}
		if c.order >= 0 && c.order != t.Order {
			t.Order, note = c.order, fmt.Sprintf("queue place set by %s", r.ID)
		}
		if c.held != nil && *c.held != t.Held {
			t.Held, note = *c.held, map[bool]string{true: "held by " + r.ID, false: "released by " + r.ID}[*c.held]
			t.HeldBy = map[bool]string{true: r.ID}[t.Held]
			if !t.Held && strings.HasPrefix(t.Reason, "held by ") {
				t.Reason = ""
			}
		}
		if c.now && !t.Now {
			t.Now, note = true, fmt.Sprintf("started now by %s, first in line: %s", r.ID, core.Clip(a.Why, 300))
		}
		if note != "" {
			m.save(&t, note)
		}
		m.mu.Lock()
		delete(m.busy, id)
		m.mu.Unlock()
	}
	return map[string]any{"queue": m.Queue(r), "notes": notes}, nil
}

// ids are task ids as a list, or as one string split at commas or spaces (the CLI's --order a,b).
type ids []string

func (l *ids) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*l = strings.FieldsFunc(one, func(r rune) bool { return r == ',' || r == ' ' })
		return nil
	}
	return json.Unmarshal(b, (*[]string)(l))
}

// Queue lists the waiting briefs r may see, in the order they will start.
func (m *Module) Queue(r core.Run) []string {
	line := m.List(func(t Task) bool { return t.Queued() && m.C.Runs.Walled(r, t.Project) == nil })
	m.inLine(line)
	out := []string{}
	for i, t := range line {
		out = append(out, fmt.Sprintf("%d. %s %q (%s)%s", i+1, t.ID, t.Label(), t.Project, suffixed(t.Reason)))
	}
	return out
}

func suffixed(s string) string {
	if s == "" {
		return ""
	}
	return ": " + s
}
