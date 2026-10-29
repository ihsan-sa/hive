package kb

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// The journal (events.jsonl) grows without end: 981 MB on 2026-10-29, and an
// agent that grepped or tailed it pulled the whole file through the disk
// once it fell out of the page cache. So the record also keeps the last
// hoursKept hours of it as they were journaled, one JSONL file per UTC hour
// (record/hours/2026-10-29T09.jsonl): what happened lately is a few MB to
// read. Search reads only .md, so these never enter its results.

const hoursKept = 48

type hours struct {
	dir  string
	mu   sync.Mutex
	hour string // the hour f is open on
	f    *os.File
	made map[string]bool // hours this start wrote afresh
}

func hourOf(t time.Time) string { return t.UTC().Format("2006-01-02T15") }

// write adds one event to its hour's file. The first event of an hour this
// start sees truncates the file, so the catch-up at start rewrites the window
// whole and never doubles a line.
func (h *hours) write(e core.Event) {
	if time.Since(e.At) > hoursKept*time.Hour {
		return
	}
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if hr := hourOf(e.At); hr != h.hour || h.f == nil {
		if h.f != nil {
			h.f.Close()
		}
		flag := os.O_CREATE | os.O_WRONLY | os.O_APPEND
		if !h.made[hr] {
			flag |= os.O_TRUNC
			h.made[hr] = true
		}
		h.f, h.hour = nil, hr
		f, err := os.OpenFile(filepath.Join(h.dir, hr+".jsonl"), flag, 0o644)
		if err != nil {
			return
		}
		h.f = f
		h.prune()
	}
	h.f.Write(append(line, '\n'))
}

// prune removes the hours past the window.
func (h *hours) prune() {
	ents, _ := os.ReadDir(h.dir)
	cut := hourOf(time.Now().Add(-hoursKept * time.Hour))
	for _, e := range ents {
		if hr, ok := strings.CutSuffix(e.Name(), ".jsonl"); ok && hr < cut {
			os.Remove(filepath.Join(h.dir, e.Name()))
		}
	}
}
