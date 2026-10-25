package work

import (
	"bytes"
	"cmp"
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
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// The queue: a filed brief waits as queued until a worker can take it. Briefs
// start in the order they were filed, or the order the lead set, and only as
// many work at once as the pace allows (pace.go, under max_workers) while the
// machine isn't loaded: the brake is the machine. A brief that names a file
// another brief names (one a worker is on, or ahead of it in line), or one
// that worker has changed, waits until that worker is done, so the two don't
// redo each other's work. A package or directory alone holds nothing back,
// nor does a file when each brief names its own functions in it (apart).
// A brief that itself waits on another's file holds no one back: a later
// brief on its other files starts, so waits never chain and two briefs never
// wait on each other (task 163142 waited 6h behind 140258, itself held by a
// stream of workers on its other files).
// Once a worker has pushed, its task holds nothing back, even when its worker
// is sent back to it (findings, a red check, a reopen): a conflict it meets
// later goes back to its worker with main to merge. While it works it still
// holds a slot. Nothing here stops a worker that has started.
//
// A brief that only changes module prompts (every path it names is a .md
// file under modules/) is quick: it goes first in line, after those the lead
// started now, and may start past the pace while no other quick brief is
// working, since a prompt edit takes a worker little time and plan. It still
// waits on a brief on the same prompt, on a strained machine and on a plan
// near its end.
//
// A worker that has gone quiet (it keeps ending turns without finishing)
// counts toward neither max_workers nor any brief's wait until it resumes.

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
	m.areas(t) // clones the project when no worker has yet
	text := t.Title + "\n" + t.Done + "\n" + t.Known
	root := m.C.WS.Project(t.Project)
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
	var ahead, line []Task         // ahead: being worked on, or earlier in line
	var sent []Task                // working on a pushed pull request: a slot, but no hold
	starting := map[string]bool{}  // queued ones whose worker is being started right now
	workingBy := map[string]bool{} // the workers of working tasks
	m.mu.Lock()
	for _, t := range tasks {
		if t.Stage == StageWorking && t.Worker != "" {
			workingBy[t.Worker] = true
		}
		switch {
		case t.Queued() && !m.busy[t.ID]:
			line = append(line, t)
		case t.Stage == StageWorking && m.quiet(t):
			// A quiet worker holds neither a slot nor anyone back: three
			// held 3 of 8 slots for 10-12h while ~39 briefs waited. A
			// message resumes it (core.Runs), and then it counts again,
			// even if that puts the workers past the limit (by as many as
			// resume at once) until slots free; none start meanwhile.
		case t.Stage == StageWorking && pushed(t):
			// Its worker holds a slot, but it holds no one back: its pull
			// request is open, so a conflict goes back to it with main to
			// merge, as for one in testing. A reopened fix round (task
			// 61fa1fd766, PR #601) held 34 briefs the second it woke.
			working++
			sent = append(sent, t)
		case t.Stage == StageWorking || t.Queued(): // or a queued one being started right now
			ahead = append(ahead, t)
			working++
			starting[t.ID] = t.Queued()
		}
		// The rest hold no one back: new ones join the line once issued, and
		// pushed ones (landing, testing, reviewing, merging) merge main on
		// a conflict, with their worker if need be. Nor does a working task
		// whose worker has gone quiet: it may never finish, and briefs that
		// touch its files waited on one for hours (task 135947 held about 60).
	}
	// Main is read afresh each pass, and the answers kept for a worker go
	// once its task leaves working, so the cache doesn't grow with the
	// daemon's life.
	clear(m.mainTips)
	for k := range m.editCache {
		if w, _, _ := strings.Cut(k, "\x00"); !workingBy[w] {
			delete(m.editCache, k)
		}
	}
	m.mu.Unlock()
	for _, t := range append(slices.Clone(ahead), sent...) { // areas takes m.mu
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
	// limit itself: only then may the pace grow, as a brief held, or waiting
	// on another's files, would leave a new slot empty.
	decide := func(limit int) (start map[string]bool, wait map[string]string, capped bool) {
		start, wait = map[string]bool{}, map[string]string{}
		working, quickWorking, ahead := working, quickWorking, slices.Clone(ahead)
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
			switch o, area := m.overlap(t, ahead, holder(t.Reason)); {
			case t.Held:
				wait[t.ID] = "held by " + cmp.Or(t.HeldBy, "the lead")
				continue // a held brief keeps no place that holds others back
			case o != nil && !t.Now: // one the lead started now goes past the overlap
				verb := "to land"
				if o.Queued() && !start[o.ID] && !starting[o.ID] {
					verb = "to go first"
				}
				wait[t.ID] = fmt.Sprintf("waiting for %q (task %s) %s%s%s", o.Label(), o.ID, verb, bothTouch, area)
				continue // one that waits on another holds no one back, so waits never chain
			case working >= limit && (!quick || quickWorking > 0 || !plan()):
				wait[t.ID], capped = full, true
			case strain != "":
				wait[t.ID] = "waiting: " + steady(strain)
			default:
				start[t.ID] = true
				working++
				if quick {
					quickWorking++
				}
			}
			ahead = append(ahead, t)
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

// bothTouch marks a wait on another brief's file (onFiles).
const bothTouch = ": both touch "

// filesTell is how long a brief waits on other briefs' files before the lead
// hears of it: since one that waits holds no one back, a broad brief can be
// passed again and again by later briefs on its other files.
var filesTell = 4 * time.Hour

// personal are the files workers don't change unless their brief asks: the
// person's to merge (the worker role prompt). Two briefs citing them still
// start together.
var personal = map[string]bool{".hive/goals.md": true, ".hive/project.json": true}

// onFiles reports whether why is a wait on another brief's file.
func onFiles(why string) bool {
	return strings.HasPrefix(why, "waiting for ") && strings.Contains(why, bothTouch)
}

// FilesWait is how long t has waited on other briefs' files, tick after
// tick; 0 when it doesn't.
func (t Task) FilesWait() time.Duration {
	if !t.Queued() || t.FilesSince.IsZero() {
		return 0
	}
	return time.Since(t.FilesSince)
}

// pushed reports whether t's own pull request is open: its worker was sent
// back on findings, a red check or a conflict, or the task reopened. A
// checkpoint's pull request doesn't count: the worker is still on the brief.
func pushed(t Task) bool {
	return t.PR.Number != 0 && t.Checkpoint == "" && t.PR.State != "CLOSED" && t.PR.State != "MERGED"
}

// quiet reports whether t's worker keeps ending turns without finishing.
func (m *Module) quiet(t Task) bool {
	r, ok := m.C.Runs.Get(t.Worker)
	return ok && r.Status == core.StatusQuiet
}

// waitsOn finds the task a queued one's reason says it waits for. It reads
// from the start, past the holder's label as %q wrote it, so a label that
// itself reads "(task X) to land: both touch " never names X.
var waitsOn = regexp.MustCompile(`^waiting for "(?:[^"\\]|\\.)*" \(task (\S+)\) to (?:land|go first): both touch `)

// holder returns the task a wait reason names, or "".
func holder(reason string) string {
	if s := waitsOn.FindStringSubmatch(reason); s != nil {
		return s[1]
	}
	return ""
}

// overlap returns the first task in others on t's project whose brief names
// a file t's brief names too, or whose worker has changed one in its
// checkout, and that file. The task prefer (the one t already waits for) is
// asked first, so a brief that overlaps several keeps one reason until that
// one lets go, rather than being saved anew each time another starts ahead of
// it (task 232db4fc42 swapped between holders every few minutes for an hour).
// A package or directory named in a brief holds nothing by itself: two
// briefs in one package start together, and the second merges main if they
// meet. Nor does a file both name when each names functions defined in it
// and none is the other's (apart), or a file only the person changes
// (personal): briefs cite the goals as evidence; it holds only once a worker
// changed it.
func (m *Module) overlap(t Task, others []Task, prefer string) (*Task, string) {
	mine := m.areas(t)
	if len(mine) == 0 {
		return nil, ""
	}
	order := make([]int, 0, len(others))
	for i, o := range others {
		if o.ID == prefer {
			order = append([]int{i}, order...)
		} else {
			order = append(order, i)
		}
	}
	for _, i := range order {
		o := others[i]
		if o.Project != t.Project || o.ID == t.ID {
			continue
		}
		theirs := map[string]bool{}
		for _, b := range m.areas(o) {
			theirs[b] = true
		}
		for _, a := range mine {
			if theirs[a] && m.apart(t, o, a) {
				continue // each names its own functions in the file: they start side by side
			}
			if (theirs[a] && !personal[a]) || m.edited(o, a) {
				return &others[i], a
			}
		}
	}
	return nil, ""
}

// apart reports whether briefs t and o, which both name the file p, each name
// at least one function, type or class defined in it and share none, so
// their edits sit in separate parts of the file and merge cleanly: the four
// at-once benchmark asks each implement one function of textkit/__init__.py
// and ran one at a time behind each other (issue #727). A brief that names
// no definition in p, or one the other names too, still waits, so two
// workers don't redo each other's work (#399, #406); one whose edits do meet
// merges main when it pushes, with its worker if need be.
func (m *Module) apart(t, o Task, p string) bool {
	defs := m.defs(t.Project, p)
	if len(defs) == 0 {
		return false
	}
	mine, theirs := named(t, defs), named(o, defs)
	if len(mine) == 0 || len(theirs) == 0 {
		return false
	}
	for d := range mine {
		if theirs[d] {
			return false
		}
	}
	return true
}

// named is the definitions in defs that t's brief names as words, outside
// the paths it names (textkit/__init__.py names no __init__).
func named(t Task, defs map[string]bool) map[string]bool {
	text := pathLike.ReplaceAllString(t.Title+"\n"+t.Done+"\n"+t.Known, " ")
	out := map[string]bool{}
	for _, w := range word.FindAllString(text, -1) {
		if defs[w] {
			out[w] = true
		}
	}
	return out
}

var (
	word = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	// definition is a line that defines a name in the common languages:
	// def, class, func (with a Go receiver), function, fn, type and the like.
	definition = regexp.MustCompile(`(?m)^[ \t]*(?:(?:export|pub(?:\([^)]*\))?|async|static|public|private|protected)[ \t]+)*(?:def|class|func|function|fn|type|struct|enum|trait|interface)[ \t]+(?:\([^)]*\)[ \t]*)?([A-Za-z_][A-Za-z0-9_]*)`)
)

// defs is the names defined in the clone file p, read once per version of
// the file; nil when it can't be read.
func (m *Module) defs(project, p string) map[string]bool {
	file := filepath.Join(m.C.WS.Project(project), filepath.FromSlash(p))
	key := stamp(file)
	m.mu.Lock()
	got, ok := m.defCache[file]
	m.mu.Unlock()
	if ok && got.key == key {
		return got.defs
	}
	b, err := core.SafeRead(file, 4<<20)
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, s := range definition.FindAllSubmatch(b, -1) {
		out[string(s[1])] = true
	}
	m.mu.Lock()
	if len(m.defCache) >= 256 {
		clear(m.defCache)
	}
	m.defCache[file] = defEntry{key, out}
	m.mu.Unlock()
	return out
}

// defEntry is the names a file defines and the file stamp they were read at.
type defEntry struct {
	key  string
	defs map[string]bool
}

// edited reports whether o's worker has changed the clone file p in its
// checkout, committed or not, from the main it started on. A file that is as
// main's tip holds it is not the worker's change either: a worker that has
// merged main carries main's later changes, and those held briefs on files
// it never touched. The checkout is read as files (host git never runs
// there); a symlink is its link text, as git keeps it, and a path that
// leaves the checkout through a symlinked directory counts as unchanged.
// Each answer is kept until the file, the base or main
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
	tip := m.mainTip(o.Project)
	key := o.Base + "\x00" + tip + "\x00" + stamp(file)
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
	if changed && tip != "" && tip != o.Base {
		if main := m.baseTree(o.Project, tip); main != nil {
			changed = differs(main, r.Checkout, p)
		}
	}
	m.mu.Lock()
	m.editCache[cacheKey] = editEntry{key, changed}
	m.mu.Unlock()
	return changed
}

// mainTip is the commit main is at in the project's host clone, the main a
// worker merges (Effects.MainBundle), read once per admit pass; "" when it
// can't be read.
func (m *Module) mainTip(project string) string {
	m.mu.Lock()
	tip, ok := m.mainTips[project]
	m.mu.Unlock()
	if ok {
		return tip
	}
	branch := "main"
	if p, ok := m.C.Projects.Get(project); ok && p.Branch != "" {
		branch = p.Branch
	}
	tip, _ = core.Git(m.C.WS.Project(project), "rev-parse", "--verify", "-q", branch)
	tip = strings.TrimSpace(tip)
	m.mu.Lock()
	if m.mainTips == nil {
		m.mainTips = map[string]string{}
	}
	m.mainTips[project] = tip
	m.mu.Unlock()
	return tip
}

// differs reports whether the file p in checkout is not what the base's tree
// holds: changed, or added or removed.
func differs(tree map[string]string, checkout, p string) bool {
	blob, inBase := tree[p]
	file := filepath.Join(checkout, filepath.FromSlash(p))
	fi, err := os.Lstat(file)
	if errors.Is(err, os.ErrNotExist) {
		return inBase // removed, or never there
	}
	realRoot, err := filepath.EvalSymlinks(checkout)
	if err != nil {
		return false
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		// Git keeps a symlink as its link text, read without following it,
		// so the link itself must sit inside the checkout.
		dir, err := filepath.EvalSymlinks(filepath.Dir(file))
		if err != nil || !inside(realRoot, dir) {
			return false
		}
		link, err := os.Readlink(file)
		if err != nil {
			return false
		}
		return !inBase || blobID([]byte(filepath.ToSlash(link)), len(blob)) != blob
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
			// Its wait was weighed in the old line (a "to go first" may no
			// longer hold): the next tick says anew, rather than this reply
			// showing a wait that looks mutual (issue #701).
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
		why := t.Reason
		if d := t.FilesWait(); d >= time.Hour {
			why = fmt.Sprintf("on other briefs' files for %s, %s", d.Round(time.Minute), why)
		}
		out = append(out, fmt.Sprintf("%d. %s %q (%s)%s", i+1, t.ID, t.Label(), t.Project, suffixed(why)))
	}
	return out
}

func suffixed(s string) string {
	if s == "" {
		return ""
	}
	return ": " + s
}
