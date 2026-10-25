package work

import (
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
// many work at once as max_workers allows (the plan's limit, until its usage
// reading is trusted) while the machine isn't loaded. A brief that names the
// same files or area as one a worker is on (or ahead of it in line) waits
// until that worker is done, so the two don't conflict and redo each other's
// work. Once a worker has pushed, its task holds nothing back: a conflict it
// meets later goes back to its worker with main to merge. Nothing here stops a
// worker that has started.

// Queued reports whether t is waiting for a worker.
func (t Task) Queued() bool { return t.Stage == StageQueued || t.Stage == StageIssued }

// inLine sorts waiting tasks: those the lead started now first, then those it
// ordered, in its order, then the rest as filed.
func inLine(q []Task) {
	sort.SliceStable(q, func(i, j int) bool {
		a, b := q[i], q[j]
		if a.Now != b.Now {
			return a.Now
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

// admit decides which waiting tasks start a worker now, and why each other
// one waits. tasks is every task Tick looks at.
func (m *Module) admit(tasks []Task) (start map[string]bool, wait map[string]string) {
	cfg := m.C.WS.LoadConfig()
	start, wait = map[string]bool{}, map[string]string{}
	working := 0
	var ahead, line []Task // ahead: being worked on, or earlier in line
	m.mu.Lock()
	for _, t := range tasks {
		switch {
		case t.Queued() && !m.busy[t.ID]:
			line = append(line, t)
		case t.Stage == StageWorking || t.Queued(): // or a queued one being started right now
			ahead = append(ahead, t)
			working++
		}
		// The rest hold no one back: new ones join the line once issued, and
		// pushed ones (landing, testing, reviewing, merging) merge main on
		// a conflict, with their worker if need be.
	}
	m.mu.Unlock()
	inLine(line)
	machine := machineHold(core.Strained(cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax), core.Loaded(cfg.LoadMax), m.C.Runs.Elsewhere)
	for _, t := range line {
		held := "" // asked only of a task nothing else holds back
		switch o, area := m.overlap(t, ahead); {
		case t.Held:
			wait[t.ID] = "held by the lead"
			continue // a held brief keeps no place that holds others back
		case o != nil && !t.Now: // one the lead started now goes past the overlap
			verb := "to land"
			if o.Queued() && !start[o.ID] {
				verb = "to go first"
			}
			wait[t.ID] = fmt.Sprintf("waiting for %q (task %s) %s: both touch %s", o.Label(), o.ID, verb, area)
		case cfg.MaxWorkers > 0 && working >= cfg.MaxWorkers:
			wait[t.ID] = fmt.Sprintf("waiting for a worker (max_workers %d)", cfg.MaxWorkers)
		case func() bool { held = machine(); return held != "" }():
			wait[t.ID] = "waiting: " + held
		default:
			start[t.ID] = true
			working++
		}
		ahead = append(ahead, t)
	}
	return start, wait
}

// machineHold says, for each task in line in turn, why the machine holds it
// back, or "". A machine over load_max alone (loaded) holds every one, since
// launch would run it here; one task a pass goes past a strained machine
// while an executor module has room, since launch hands a strained turn to it.
func machineHold(strained, loaded string, elsewhere func() bool) func() string {
	asked := false
	return func() string {
		if strained == "" {
			return loaded
		}
		if !asked {
			asked = true
			if elsewhere() {
				return ""
			}
		}
		return strained
	}
}

// overlap returns the first task in others on t's project whose brief names
// a file t's brief names too, or a directory one of them names on its own
// that holds the other's file or directory, and that area.
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

// areas are the paths t's brief names that exist in its project's clone. A
// file is only itself, so two briefs on different files of one package start
// together; a directory counts when the brief names it, unless it is at the
// top level ("internal", "docs"), which would hold everything under it. A
// file that merges as union (.gitattributes merge=union, like docs/log.md)
// isn't an area: both sides' lines are kept. They are worked out once per
// brief text and .gitattributes, so a doc main newly marks union frees the
// briefs that name it.
func (m *Module) areas(t Task) []string {
	text := t.Title + "\n" + t.Done + "\n" + t.Known
	root := m.C.WS.Project(t.Project)
	key := text + "\x00" + stamp(filepath.Join(root, ".gitattributes"))
	m.mu.Lock()
	got, ok := m.areaCache[t.ID]
	m.mu.Unlock()
	if ok && got.key == key {
		return got.areas
	}
	if !core.Exists(root) { // the queue reads the clone before any worker has synced it
		if p, ok := m.C.Projects.Get(t.Project); !ok || m.C.Effects.Clone(p) != nil {
			return nil
		}
		key = text + "\x00" + stamp(filepath.Join(root, ".gitattributes"))
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil
	}
	union := unionRules(root)
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
		switch {
		case fi.IsDir() && !strings.Contains(p, "/"): // a top-level directory, as in "internal/."
		case !fi.IsDir() && isUnion(union, p):
		default:
			add(p)
		}
	}
	m.mu.Lock()
	m.areaCache[t.ID] = areaEntry{key, out}
	m.mu.Unlock()
	return out
}

// areaEntry is one task's areas and the brief text and .gitattributes they
// were worked out from.
type areaEntry struct {
	key   string
	areas []string
}

// stamp is a file's size and modification time, "" when it is missing.
func stamp(p string) string {
	fi, err := os.Lstat(p)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d %d", fi.Size(), fi.ModTime().UnixNano())
}

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
		Now     ids `json:"now"`
		Why     string
	}
	if len(args) > 0 {
		if err := core.Bind(args, &a); err != nil {
			return nil, err
		}
	}
	if len(a.Now) > 0 && strings.TrimSpace(a.Why) == "" {
		return nil, errors.New("now starts a brief past the one it waits on: say why with why (it goes on the record)")
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
		if c.now && !t.Now {
			t.Now, note = true, fmt.Sprintf("started now by %s, past any brief on the same files: %s", r.ID, core.Clip(a.Why, 300))
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
