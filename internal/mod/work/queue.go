package work

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// The queue: a filed brief waits as queued until a worker can take it. Briefs
// start in the order they were filed, or the order the lead set, and only as
// many work at once as the pace allows (pace.go, under max_workers) while the
// machine isn't loaded: the brake is the machine. A brief that names a file
// another brief names (one a worker is on, or ahead of it in line), or one
// that worker has changed, waits until that worker is done, so the two don't
// redo each other's work. A package or directory alone holds nothing back.
// Once a worker has pushed, its task holds nothing back: a conflict it meets
// later goes back to its worker with main to merge. Nothing here stops a
// worker that has started.
//
// A brief that only changes module prompts (every path it names is a .md
// file under modules/) is quick: it goes first in line, after those the lead
// started now, and may start past the pace while no other quick brief is
// working, since a prompt edit takes a worker little time and plan. It still
// waits on a brief on the same prompt, and on a strained machine.

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
// least one path, and every one is a .md file under modules/.
func (m *Module) quick(t Task) bool {
	areas := m.areas(t)
	for _, a := range areas {
		if !strings.HasPrefix(a, "modules/") || path.Ext(a) != ".md" {
			return false
		}
	}
	return len(areas) > 0
}

// admit decides which waiting tasks start a worker now, and why each other
// one waits. tasks is every task Tick looks at.
func (m *Module) admit(tasks []Task) (start map[string]bool, wait map[string]string) {
	cfg := m.C.WS.LoadConfig()
	start, wait = map[string]bool{}, map[string]string{}
	working, quickWorking := 0, 0
	var ahead, line []Task // ahead: being worked on, or earlier in line
	m.mu.Lock()
	for _, t := range tasks {
		switch {
		case t.Queued() && !m.busy[t.ID]:
			line = append(line, t)
		case t.Stage == StageWorking && m.quiet(t):
			working++ // a quiet worker holds a slot but no one back
		case t.Stage == StageWorking || t.Queued(): // or a queued one being started right now
			ahead = append(ahead, t)
			working++
		}
		// The rest hold no one back: new ones join the line once issued, and
		// pushed ones (landing, testing, reviewing, merging) merge main on
		// a conflict, with their worker if need be. Nor does a working task
		// whose worker has gone quiet: it may never finish, and briefs that
		// touch its files waited on one for hours (task 135947 held about 60).
	}
	m.mu.Unlock()
	for _, t := range ahead { // areas takes m.mu
		if m.quick(t) {
			quickWorking++
		}
	}
	m.inLine(line)
	strain, _ := machine(cfg)
	limit := m.paceWorkers(cfg, working, slices.ContainsFunc(line, func(t Task) bool { return !t.Held }))
	full := fmt.Sprintf("waiting for a worker (max_workers %d)", cfg.MaxWorkers)
	if cfg.MaxWorkers == 0 || limit < cfg.MaxWorkers {
		full = "waiting for a worker (pace " + m.workers.String() + ")"
	}
	for _, t := range line {
		quick := m.quick(t)
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
		case working >= limit && (!quick || quickWorking > 0):
			wait[t.ID] = full
		case strain != "":
			wait[t.ID] = "waiting: " + strain
		default:
			start[t.ID] = true
			working++
			if quick {
				quickWorking++
			}
		}
		ahead = append(ahead, t)
	}
	return start, wait
}

// quiet reports whether t's worker keeps ending turns without finishing.
func (m *Module) quiet(t Task) bool {
	r, ok := m.C.Runs.Get(t.Worker)
	return ok && r.Status == core.StatusQuiet
}

// overlap returns the first task in others on t's project whose brief names
// a file t's brief names too, or whose worker has changed one in its
// checkout, and that file. A package or directory named in a brief holds
// nothing by itself: two briefs in one package start together, and the
// second merges main if they meet.
func (m *Module) overlap(t Task, others []Task) (*Task, string) {
	mine := m.areas(t)
	if len(mine) == 0 {
		return nil, ""
	}
	for i, o := range others {
		if o.Project != t.Project || o.ID == t.ID {
			continue
		}
		theirs := map[string]bool{}
		for _, b := range m.areas(o) {
			theirs[b] = true
		}
		for _, a := range mine {
			if theirs[a] || m.edited(o, a) {
				return &others[i], a
			}
		}
	}
	return nil, ""
}

// edited reports whether o's worker has changed the clone file p in its
// checkout, committed or not, from the main it started on. The checkout is
// read as files (host git never runs there); a path that leaves it through a
// symlink counts as unchanged. Each answer is kept until the file or the base
// moves; none is given or kept while the base's tree can't be read.
func (m *Module) edited(o Task, p string) bool {
	if o.Stage != StageWorking || o.Worker == "" || o.Base == "" {
		return false
	}
	r, ok := m.C.Runs.Get(o.Worker)
	if !ok || r.Checkout == "" {
		return false
	}
	file := filepath.Join(r.Checkout, filepath.FromSlash(p))
	cacheKey := o.Worker + "\x00" + p
	key := o.Base + "\x00" + stamp(file)
	m.mu.Lock()
	got, ok := m.editCache[cacheKey]
	m.mu.Unlock()
	if ok && got.key == key {
		return got.edited
	}
	tree := m.baseTree(o.Project, o.Base)
	if tree == nil {
		return false // unknown for now: held by nothing, and asked again next tick
	}
	changed := differs(tree, r.Checkout, p)
	m.mu.Lock()
	m.editCache[cacheKey] = editEntry{key, changed}
	m.mu.Unlock()
	return changed
}

// differs reports whether the file p in checkout is not what the base's tree
// holds: changed, or added or removed.
func differs(tree map[string]string, checkout, p string) bool {
	blob, inBase := tree[p]
	file := filepath.Join(checkout, filepath.FromSlash(p))
	if _, err := os.Lstat(file); errors.Is(err, os.ErrNotExist) {
		return inBase // removed, or never there
	}
	realRoot, err := filepath.EvalSymlinks(checkout)
	if err != nil {
		return false
	}
	if real, err := filepath.EvalSymlinks(file); err != nil || !inside(realRoot, real) {
		return false
	}
	const limit = 32 << 20
	b, err := core.SafeRead(file, limit+1)
	switch {
	case err != nil:
		return false
	case !inBase:
		return true // added
	case len(b) > limit:
		return true
	}
	// A checkout made with CRLF line endings (Windows, text=auto) holds the
	// same blob as LF.
	return blobID(b, len(blob)) != blob && blobID(bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")), len(blob)) != blob
}

// baseTree is the blob of every file in base, read from the project's clone
// once per base: a commit's tree never changes. Only the newest few bases
// are kept; an unreadable one is nil and read again next time.
func (m *Module) baseTree(project, base string) map[string]string {
	key := project + "\x00" + base
	m.mu.Lock()
	tree, ok := m.baseTrees[key]
	m.mu.Unlock()
	if ok {
		return tree
	}
	out, err := core.Git(m.C.WS.Project(project), "ls-tree", "-r", "-z", "--full-tree", base)
	if err != nil {
		return nil
	}
	tree = map[string]string{}
	for _, e := range strings.Split(out, "\x00") {
		meta, name, ok := strings.Cut(e, "\t")
		if f := strings.Fields(meta); ok && len(f) == 3 && f[1] == "blob" {
			tree[name] = f[2]
		}
	}
	m.mu.Lock()
	if len(m.baseTrees) >= 16 {
		clear(m.baseTrees)
	}
	m.baseTrees[key] = tree
	m.mu.Unlock()
	return tree
}

// blobID is git's object id of content as a blob, sha256 when the repo's ids
// are that long.
func blobID(content []byte, idLen int) string {
	head := fmt.Sprintf("blob %d\x00", len(content))
	if idLen == 64 {
		h := sha256.New()
		h.Write([]byte(head))
		h.Write(content)
		return hex.EncodeToString(h.Sum(nil))
	}
	h := sha1.New()
	h.Write([]byte(head))
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

// editEntry is whether a worker changed a file, and the base and file stamp
// that was worked out from.
type editEntry struct {
	key    string
	edited bool
}

var pathLike = regexp.MustCompile(`[A-Za-z0-9_.\-]+(?:/[A-Za-z0-9_.\-]+)+`)

// areas are the files t's brief names that exist in its project's clone. A
// file is only itself, so two briefs on different files of one package start
// together, and a directory ("internal/mod", "internal/mod/work") is no area:
// a brief names its package to say where it works, not that it edits every
// file there, and holding on it queued most of the line behind one worker
// (issues #399, #406). A file that merges as union (.gitattributes
// merge=union, like docs/log.md) isn't an area: both sides' lines are kept.
// They are worked out once per brief text and .gitattributes, so a doc main
// newly marks union frees the briefs that name it.
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
		fi, err := os.Stat(real) // resolved, so a symlink to a directory is a directory
		if err != nil {
			continue
		}
		// Keyed on where it resolves, so two names for one file are one area.
		rel, err := filepath.Rel(realRoot, real)
		if err != nil {
			continue
		}
		if rel = filepath.ToSlash(rel); !fi.IsDir() && !isUnion(union, rel) {
			add(rel)
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
