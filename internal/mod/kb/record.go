package kb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
