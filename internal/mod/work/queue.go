package work

import (
	"encoding/json"
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
// many work at once as max_workers allows (the plan's limit, until its usage
// reading is trusted) while the machine isn't loaded. A brief that names the
// same files or area as one already in flight (or ahead of it in line) waits
// until that one lands, so the two don't conflict and redo each other's work.
// Nothing here stops a worker that has started.

// Queued reports whether t is waiting for a worker.
func (t Task) Queued() bool { return t.Stage == StageQueued || t.Stage == StageIssued }

// inLine sorts waiting tasks: those the lead ordered first, in its order, then
// the rest as filed.
func inLine(q []Task) {
	sort.SliceStable(q, func(i, j int) bool {
		a, b := q[i], q[j]
		if (a.Order > 0) != (b.Order > 0) {
			return a.Order > 0
		}
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		return a.Created.Before(b.Created)
	})
}

// admit decides which waiting tasks start a worker now, and why each other
// one waits. tasks is every task Tick looks at.
func (m *Module) admit(tasks []Task) (start map[string]bool, wait map[string]string) {
	cfg := m.C.WS.LoadConfig()
	start, wait = map[string]bool{}, map[string]string{}
	working := 0
	var ahead, line []Task // ahead: in flight, or earlier in line
	m.mu.Lock()
	for _, t := range tasks {
		switch {
		case t.Terminal(), t.Stage == StageNew: // a new one joins the line once issued
		case t.Queued() && !m.busy[t.ID]:
			line = append(line, t)
		default: // past the queue, or a queued one being started right now
			ahead = append(ahead, t)
			if t.Stage == StageWorking || t.Queued() {
				working++
			}
		}
	}
	m.mu.Unlock()
	inLine(line)
	machine := core.Strained(cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax)
	if machine == "" {
		machine = core.Loaded(cfg.LoadMax)
	}
	for _, t := range line {
		switch o, area := m.overlap(t, ahead); {
		case t.Held:
			wait[t.ID] = "held by the lead"
			continue // a held brief keeps no place that holds others back
		case o != nil:
			verb := "to land"
			if o.Queued() && !start[o.ID] {
				verb = "to go first"
			}
			wait[t.ID] = fmt.Sprintf("waiting for %q (task %s) %s: both touch %s", o.Label(), o.ID, verb, area)
		case cfg.MaxWorkers > 0 && working >= cfg.MaxWorkers:
			wait[t.ID] = fmt.Sprintf("waiting for a worker (max_workers %d)", cfg.MaxWorkers)
		case machine != "":
			wait[t.ID] = "waiting: " + machine
		default:
			start[t.ID] = true
			working++
		}
		ahead = append(ahead, t)
	}
	return start, wait
}

// overlap returns the first task in others on t's project whose brief names
// a file or area t's brief names too, and that area.
func (m *Module) overlap(t Task, others []Task) (*Task, string) {
	mine := m.areas(t)
	if len(mine) == 0 {
		return nil, ""
	}
	for i, o := range others {
		if o.Project != t.Project || o.ID == t.ID {
			continue
		}
		for _, a := range mine {
			for _, b := range m.areas(o) {
				if a == b || strings.HasPrefix(b, a+"/") || strings.HasPrefix(a, b+"/") {
					if len(b) < len(a) {
						a = b
					}
					return &others[i], a
				}
			}
		}
	}
	return nil, ""
}

var pathLike = regexp.MustCompile(`[A-Za-z0-9_.\-]+(?:/[A-Za-z0-9_.\-]+)+`)

// areas are the paths t's brief names that exist in its project's clone: each
// file, and the directory it is in when that is deeper than the top level, so
// two briefs on one package meet. They are worked out once per brief text.
func (m *Module) areas(t Task) []string {
	text := t.Title + "\n" + t.Done + "\n" + t.Known
	key := t.ID + "\x00" + text
	m.mu.Lock()
	got, ok := m.areaCache[key]
	m.mu.Unlock()
	if ok {
		return got
	}
	root := m.C.WS.Project(t.Project)
	if !core.Exists(root) { // the queue reads the clone before any worker has synced it
		if p, ok := m.C.Projects.Get(t.Project); !ok || m.C.Effects.Clone(p) != nil {
			return nil
		}
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, p := range pathLike.FindAllString(text, -1) {
		p = path.Clean(strings.TrimRight(strings.TrimPrefix(p, "./"), "."))
		if p == "." || strings.HasPrefix(p, "..") || strings.HasPrefix(p, ".git/") {
			continue
		}
		// A symlink in the clone may point outside it: the path must resolve
		// inside, or the brief could test what exists on the machine.
		real, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil || !inside(realRoot, real) {
			continue
		}
		fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			continue
		}
		if dir := path.Dir(p); !fi.IsDir() && strings.Contains(dir, "/") {
			add(dir)
		}
		add(p)
	}
	m.mu.Lock()
	m.areaCache[key] = out
	m.mu.Unlock()
	return out
}

// inside reports whether path is root or under it.
func inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// opQueue shows the queue, and with arguments sets its order or holds briefs.
func (m *Module) opQueue(r core.Run, args json.RawMessage) (any, error) {
	var a struct {
		Order   ids `json:"order"`
		Hold    ids `json:"hold"`
		Release ids `json:"release"`
	}
	if len(args) > 0 {
		if err := core.Bind(args, &a); err != nil {
			return nil, err
		}
	}
	type change struct {
		order int
		held  *bool
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
	var notes []string
	if len(a.Order) > 0 { // a new order replaces the old one: the rest go after, as filed
		for _, t := range m.List(func(t Task) bool { return t.Queued() && t.Order > 0 }) {
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
		if c.order >= 0 && c.order != t.Order {
			t.Order, note = c.order, fmt.Sprintf("queue place set by %s", r.ID)
		}
		if c.held != nil && *c.held != t.Held {
			t.Held, note = *c.held, map[bool]string{true: "held by " + r.ID, false: "released by " + r.ID}[*c.held]
			if !t.Held && t.Reason == "held by the lead" {
				t.Reason = ""
			}
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
	inLine(line)
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
