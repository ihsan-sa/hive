// Package core is the Hive's small core: it runs agents in separate checkouts,
// holds every credential in the effects process, lets agents spawn and message
// one another, enforces the hard safety ceiling, and keeps state so work
// survives a restart. Everything else is a module.
package core

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Event is one append-only record. Kind is dotted ("run.created", "task.merged").
type Event struct {
	Seq  int64           `json:"seq"`
	At   time.Time       `json:"at"`
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Decode unmarshals the event payload into v.
func (e Event) Decode(v any) error { return json.Unmarshal(e.Data, v) }

// Journal is an append-only JSONL file. Every append is fsynced, so a crash
// loses at most the record being written; a torn last line is ignored on read.
type Journal struct {
	mu   sync.Mutex
	path string
	f    *os.File
	seq  int64
	subs []func(Event)
	// breaks counts events whose seq didn't rise past the one before: two
	// writers on one file. flagged is the count an earlier start reported.
	breaks, flagged int
}

// OpenJournal opens (or creates) the journal at path and reads its last seq.
func OpenJournal(path string) (*Journal, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	j := &Journal{path: path}
	if err := j.Replay(func(e Event) {
		if e.Seq <= j.seq {
			j.breaks++
		}
		j.seq = max(j.seq, e.Seq) // after two writers, the next seq is still new
		var a struct {
			Kind   string `json:"kind"`
			Breaks int    `json:"breaks"`
		}
		if e.Kind == "alert.raised" && e.Decode(&a) == nil && a.Kind == "journal" {
			j.flagged = max(j.flagged, a.Breaks)
		}
	}); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	j.f = f
	return j, nil
}

// Breaks returns how many events' seq didn't rise since an earlier start
// reported them (none if it did): a sign two daemons wrote the journal at
// once, and the total so far.
func (j *Journal) Breaks() (fresh, total int) {
	return max(j.breaks-j.flagged, 0), j.breaks
}

// Subscribe registers fn to be called (synchronously, after the write) for each new event.
func (j *Journal) Subscribe(fn func(Event)) {
	j.mu.Lock()
	j.subs = append(j.subs, fn)
	j.mu.Unlock()
}

// Append writes one event and returns it.
func (j *Journal) Append(kind string, data any) (Event, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return Event{}, err
	}
	j.mu.Lock()
	j.seq++
	e := Event{Seq: j.seq, At: time.Now().UTC(), Kind: kind, Data: raw}
	line, _ := json.Marshal(e)
	_, err = j.f.Write(append(line, '\n'))
	if err == nil {
		err = j.f.Sync()
	}
	subs := append([]func(Event){}, j.subs...)
	j.mu.Unlock()
	if err != nil {
		return Event{}, err
	}
	for _, fn := range subs {
		fn(e)
	}
	return e, nil
}

// MustAppend appends and panics only on a disk failure, which leaves the Hive
// unable to keep its guarantees anyway. A step still running when the Hive
// shuts down writes to a closed journal: that write is dropped, as it would be
// a moment later when the process exits, and the step resumes from the record.
func (j *Journal) MustAppend(kind string, data any) Event {
	e, err := j.Append(kind, data)
	if err != nil && !errors.Is(err, os.ErrClosed) {
		panic("journal: " + err.Error())
	}
	return e
}

// Replay calls fn for every complete event in the file, in order.
func (j *Journal) Replay(fn func(Event)) error {
	return ReadJSONL(j.path, func(raw []byte) {
		var e Event
		if json.Unmarshal(raw, &e) == nil && e.Kind != "" {
			fn(e)
		}
	})
}

// Since returns events with Seq > after.
func (j *Journal) Since(after int64) []Event {
	var out []Event
	_ = j.Replay(func(e Event) {
		if e.Seq > after {
			out = append(out, e)
		}
	})
	return out
}

// Seq returns the last written sequence number.
func (j *Journal) Seq() int64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.seq
}

// Close closes the file.
func (j *Journal) Close() error { return j.f.Close() }

// ReadJSONL calls fn for each non-empty line; a missing file is empty.
func ReadJSONL(path string, fn func([]byte)) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<16)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 1 && line[len(line)-1] == '\n' {
			fn(line[:len(line)-1])
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// AppendJSONL appends one JSON line to path (used for chats and scores).
func AppendJSONL(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return err
	}
	return f.Sync()
}
