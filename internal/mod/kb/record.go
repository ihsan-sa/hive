package kb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// The record is every journal event filed as markdown under kb/record, one
// file per subject (a task, a request, a run, a chat, a project), so the
// knowledge base holds what happened without any agent choosing to remember
// it. It is derived from the journal and kept out of the notes' git history.

// skipKinds carry nothing a reader needs (process ids, plan meters).
var skipKinds = map[string]bool{"plan.usage": true, "run.turn_started": true, "run.delivered": true}

// skipFields are long or machine-only parts of an event.
var skipFields = map[string]bool{"system": true, "inputs": true, "tools": true, "modules": true, "queue": true, "structured": true, "session_live": true}

// subjectFields name what an event is about, in order of preference. A chat
// event is about its chat, never its message's id; anything else is about its
// own id, so a task that names its chat still files under the task.
var subjectFields = []string{"id", "project", "name", "module", "job"}

type recorder struct {
	dir    string
	mu     sync.Mutex
	cursor int64                        // last seq filed
	last   map[string]map[string]string // area/subject kind -> fields, for *.saved snapshots
}

func (m *Module) startRecord() {
	rec := &recorder{dir: filepath.Join(m.C.WS.KB(), "record"), last: map[string]map[string]string{}}
	os.MkdirAll(rec.dir, 0o755)
	raw, _ := os.ReadFile(filepath.Join(rec.dir, ".filed"))
	rec.cursor, _ = strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	resumed := rec.cursor
	// Replay all of it so snapshots diff against their true previous state;
	// only events past the cursor are written, which is the catch-up. Each of
	// those is rechecked, since the cursor moves only when an entry is written.
	m.C.J.Replay(func(e core.Event) { rec.file(e, e.Seq > resumed) })
	m.C.J.Subscribe(func(e core.Event) { rec.file(e, false) })
}

// file writes one event; recheck guards an event a crash could have written
// without moving the cursor.
func (r *recorder) file(e core.Event, recheck bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	area, _, _ := strings.Cut(e.Kind, ".")
	fields := map[string]string{}
	var data map[string]any
	if json.Unmarshal(e.Data, &data) == nil {
		flatten("", data, fields)
	}
	subject := ""
	order := subjectFields
	if area == "chat" {
		order = append([]string{"chat"}, order...)
	}
	for _, k := range order {
		if v := fields[k]; v != "" {
			subject = v
			break
		}
	}
	shown := fields
	if strings.HasSuffix(e.Kind, ".saved") || strings.HasSuffix(e.Kind, ".updated") {
		key := area + "/" + subject + " " + e.Kind
		prev := r.last[key]
		r.last[key] = fields
		if prev != nil {
			shown = map[string]string{}
			for k, v := range fields {
				if prev[k] != v {
					shown[k] = v
				}
			}
			for k := range prev {
				if _, ok := fields[k]; !ok {
					shown[k] = "(cleared)"
				}
			}
		}
	}
	if e.Seq <= r.cursor || skipKinds[e.Kind] || len(shown) == 0 {
		return
	}
	path := filepath.Join(r.dir, area+".md")
	if s := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(subject), "-"), "-"); s != "" {
		path = filepath.Join(r.dir, area, s+".md")
	}
	mark := fmt.Sprintf("· seq %d\n", e.Seq)
	if recheck {
		if old, _ := os.ReadFile(path); bytes.Contains(old, []byte(mark)) {
			r.advance(e.Seq)
			return
		}
	}
	var b strings.Builder
	if !core.Exists(path) {
		os.MkdirAll(filepath.Dir(path), 0o755)
		fmt.Fprintf(&b, "# %s %s\n", area, subject)
	}
	fmt.Fprintf(&b, "\n### %s · %s %s", e.At.Format("2006-01-02 15:04"), e.Kind, mark)
	keys := make([]string, 0, len(shown))
	for k := range shown {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "- %s: %s\n", k, strings.ReplaceAll(strings.TrimSpace(shown[k]), "\n", "\n  "))
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	f.WriteString(b.String())
	f.Close()
	r.advance(e.Seq)
}

func (r *recorder) advance(seq int64) {
	if seq > r.cursor {
		r.cursor = seq
		core.WriteFileAtomic(filepath.Join(r.dir, ".filed"), []byte(strconv.FormatInt(seq, 10)), 0o644)
	}
}

// flatten turns nested JSON into dotted keys with string values, dropping
// empty ones.
func flatten(prefix string, v any, out map[string]string) {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if !skipFields[k] {
				flatten(prefix+k+".", x, out)
			}
		}
	case nil:
	default:
		s := fmt.Sprint(t)
		if _, ok := t.(string); !ok {
			raw, _ := json.Marshal(t)
			s = string(raw)
		}
		if s != "" && s != "false" && s != "0" && s != "[]" && s != "0001-01-01T00:00:00Z" {
			out[strings.TrimSuffix(prefix, ".")] = s
		}
	}
}

// Hit is one piece of the knowledge base that matches a search.
type Hit struct {
	File  string `json:"file"` // relative to the knowledge base
	When  string `json:"when"`
	Text  string `json:"text"`
	score float64
	tier  int // 0 a note, 1 a record entry, 2 a retired note
	lived bool
	marks []float64 // a note's points for each word of the query
}

// Search returns the pieces of the knowledge base under dir that match
// query, skipping the folders in hide (relative to dir). Notes come first,
// best match first: a word in a note's title or file name counts most, then
// one in its tags, then one in its text, and the whole query in the title
// most of all, each word weighed by how few notes hold it; a living note the dream consolidated goes ahead of one that
// matches as well. Then the record, one entry per file (the one that matches
// best, the newest among equals), so one task's many states don't crowd out
// everything else; and a retired note last. The record's files are read in
// parallel and skipped whole when no word is in them.
func Search(dir, query string, hide ...string) []Hit {
	var terms []string
	for _, w := range strings.Fields(strings.ToLower(query)) {
		if len(w) > 1 && !slices.Contains(terms, w) {
			terms = append(terms, w)
		}
	}
	if len(terms) == 0 {
		return nil
	}
	phrase := strings.Join(terms, " ")
	var notes, record []string
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable folder has nothing to give; go on with the rest
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		// A dot folder is git's or a copy SeedSelf is swapping in, never notes.
		if d.IsDir() && path != dir && (strings.HasPrefix(d.Name(), ".") || slices.Contains(hide, rel)) {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		if strings.HasPrefix(rel, "record/") {
			record = append(record, rel)
		} else {
			notes = append(notes, rel)
		}
		return nil
	})
	found := make([]*Hit, len(notes)+len(record))
	work := make(chan int)
	var wg sync.WaitGroup
	for range max(4, 2*runtime.GOMAXPROCS(0)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				if i < len(notes) {
					found[i] = matchNote(dir, notes[i], terms, phrase)
				} else {
					found[i] = matchRecord(dir, record[i-len(notes)], terms)
				}
			}
		}()
	}
	for i := range found {
		work <- i
	}
	close(work)
	wg.Wait()
	// A word few notes hold says more about the note than one most hold.
	held := make([]int, len(terms))
	for _, h := range found[:len(notes)] {
		for k := range terms {
			if h != nil && h.marks[k] > 0 {
				held[k]++
			}
		}
	}
	for _, h := range found[:len(notes)] {
		if h == nil {
			continue
		}
		for k, m := range h.marks {
			h.score += m * (1 + math.Log(float64(len(notes)+1)/float64(held[k]+1)))
		}
	}
	var hits []Hit
	seen := map[string]bool{}
	for _, h := range found {
		// The same text under two names (a note copied, an event filed
		// under two subjects) is shown once.
		if h != nil && !seen[h.Text] {
			seen[h.Text] = true
			hits = append(hits, *h)
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.tier != b.tier {
			return a.tier < b.tier
		}
		if a.score != b.score {
			return a.score > b.score
		}
		if a.lived != b.lived {
			return a.lived
		}
		if a.When != b.When {
			return a.When > b.When
		}
		return a.File < b.File
	})
	return hits
}

// matchNote scores the note at rel, or returns nil when no word is in it.
func matchNote(dir, rel string, terms []string, phrase string) *Hit {
	raw, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		return nil
	}
	text := string(raw)
	low := strings.ToLower(text)
	head := header(text)
	title := strings.ToLower(head["title"])
	name := strings.NewReplacer("-", " ", "_", " ").Replace(strings.ToLower(rel))
	tags := strings.ToLower(head["tags"])
	h := Hit{File: rel, Text: strings.TrimSpace(text), When: head["date"], marks: make([]float64, len(terms))}
	any := false
	for k, t := range terms {
		switch {
		case hasWord(title, t) || hasWord(name, t):
			h.marks[k] = 6
		case hasWord(tags, t):
			h.marks[k] = 4
		case hasWord(low, t):
			h.marks[k] = 2
		default:
			continue
		}
		any = true
	}
	if !any {
		return nil
	}
	if len(terms) > 1 {
		if hasWord(title, phrase) || hasWord(name, phrase) {
			h.score += 12
		} else if hasWord(low, phrase) {
			h.score += 4
		}
	}
	switch {
	case head["retired"] != "":
		h.tier = 2
	case head["consolidated"] != "":
		h.lived = true
	}
	return &h
}

// matchRecord returns the entry of the record file at rel that matches best,
// the newest among equals, or nil when no word is in the file.
func matchRecord(dir, rel string, terms []string) *Hit {
	raw, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		return nil
	}
	whole := bytes.ToLower(raw)
	inFile := 0
	for _, t := range terms {
		if bytes.Contains(whole, []byte(t)) {
			inFile++
		}
	}
	if inFile == 0 {
		return nil
	}
	var best *Hit
	pieces := strings.Split(string(raw), "\n### ")[1:]
	for i := len(pieces) - 1; i >= 0; i-- { // newest first
		p := pieces[i]
		low := strings.ToLower(p)
		// A word in the entry counts double; one elsewhere in its file
		// (an earlier state of the same task) still counts.
		score := 0.0
		for _, t := range terms {
			if hasWord(low, t) {
				score += 2
			}
		}
		if score == 0 || best != nil && score <= best.score {
			continue
		}
		h := Hit{File: rel, Text: strings.TrimSpace(p), score: score, tier: 1}
		if len(p) >= 16 {
			h.When = p[:16]
		}
		best = &h
	}
	if best != nil {
		best.score += float64(inFile)
	}
	return best
}

// hasWord says whether t starts a word in s, so "merge" finds "merged" but
// "stall" doesn't find "installer".
func hasWord(s, t string) bool {
	for i := 0; ; {
		j := strings.Index(s[i:], t)
		if j < 0 {
			return false
		}
		if i += j; i == 0 || !isWordByte(s[i-1]) {
			return true
		}
		i++
	}
}

func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c >= 0x80
}

// Find searches the knowledge base under dir and renders the best limit hits
// ("all", or a number; 20 when empty) for an agent or a person to read,
// saying how many more matched; hide is as for Search.
func Find(dir, query, limit string, hide ...string) string {
	hits := Search(dir, query, hide...)
	if len(hits) == 0 {
		return "nothing in the knowledge base matches; the record doesn't cover it"
	}
	n, err := strconv.Atoi(limit)
	if limit == "all" || n > len(hits) {
		n = len(hits)
	} else if err != nil || n <= 0 {
		n = min(20, len(hits))
	}
	var b strings.Builder
	for _, h := range hits[:n] {
		text := h.Text
		if r := []rune(text); len(r) > 1500 {
			text = string(r[:1500]) + " … (clipped; the rest is in " + h.File + ")"
		}
		fmt.Fprintf(&b, "== %s\n%s\n\n", h.File, text)
	}
	if more := len(hits) - n; more > 0 {
		fmt.Fprintf(&b, "%d more match; ask again with --limit all (or a bigger number) to see them.", more)
	}
	return strings.TrimSpace(b.String())
}

// header reads a note's "---" header into its fields.
func header(note string) map[string]string {
	out := map[string]string{}
	rest, ok := strings.CutPrefix(note, "---\n")
	if !ok {
		return out
	}
	head, _, _ := strings.Cut(rest, "\n---\n")
	for _, l := range strings.Split(head, "\n") {
		if k, v, ok := strings.Cut(l, ":"); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}
