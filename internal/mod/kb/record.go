package kb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// subjectFields name what an event is about, in order of preference.
var subjectFields = []string{"chat", "id", "project", "name", "module", "job"}

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
	// only events past the cursor are written, which is the catch-up.
	m.C.J.Replay(func(e core.Event) { rec.file(e, e.Seq == resumed+1) })
	m.C.J.Subscribe(func(e core.Event) { rec.file(e, false) })
}

// file writes one event; recheck guards the one event a crash could have
// written without moving the cursor.
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
	for _, k := range subjectFields {
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
		os.WriteFile(filepath.Join(r.dir, ".filed"), []byte(strconv.FormatInt(seq, 10)), 0o644)
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
	score int
}

// Search returns the pieces of the knowledge base under dir (notes whole,
// record files entry by entry) that match the most words of query, newest
// first among equals, and at most limit of them.
func Search(dir, query string, limit int) []Hit {
	var terms []string
	for _, w := range strings.Fields(strings.ToLower(query)) {
		if len(w) > 1 {
			terms = append(terms, w)
		}
	}
	var hits []Hit
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		raw, _ := os.ReadFile(path)
		whole := strings.ToLower(rel + " " + string(raw))
		pieces := []string{string(raw)}
		if strings.HasPrefix(filepath.ToSlash(rel), "record/") {
			pieces = strings.Split(string(raw), "\n### ")[1:]
			slices.Reverse(pieces) // newest first among equals
		}
		for _, p := range pieces {
			low := strings.ToLower(p)
			h := Hit{File: filepath.ToSlash(rel), Text: strings.TrimSpace(p)}
			// A word in the entry counts double; one elsewhere in its file
			// (an earlier state of the same task) still counts.
			inEntry := false
			for _, t := range terms {
				if strings.Contains(low, t) {
					h.score += 2
					inEntry = true
				}
				if strings.Contains(whole, t) {
					h.score++
				}
			}
			if !inEntry {
				continue
			}
			if i := strings.Index(p, "date: "); i >= 0 && !strings.HasPrefix(h.File, "record/") {
				h.When, _, _ = strings.Cut(p[i+6:], "\n")
			} else if len(p) >= 16 {
				h.When = p[:16]
			}
			hits = append(hits, h)
		}
		return nil
	})
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].When > hits[j].When
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// FormatHits renders hits for an agent or a person to read.
func FormatHits(hits []Hit) string {
	if len(hits) == 0 {
		return "nothing in the knowledge base matches; the record doesn't cover it"
	}
	var b strings.Builder
	for _, h := range hits {
		fmt.Fprintf(&b, "== %s\n%s\n\n", h.File, core.Clip(h.Text, 1500))
	}
	return strings.TrimSpace(b.String())
}
