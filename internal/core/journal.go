// Package core is the Hive's small core: it runs agents in separate checkouts,
// holds every credential in the effects process, lets agents spawn and message
// one another, enforces the hard safety ceiling, and keeps state so work
// survives a restart. Everything else is a module.
package core

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// durable says whether writes are fsynced. A test binary's journals are
// throwaway, and on a loaded disk an fsync took half a second: with them
// internal/mod/work's tests took 8.5 minutes on disk against 70s on a tmpfs,
// and ran into the checks' 25-minute limit. Its git skips them too
// (GIT_TEST_FSYNC, which every git it starts inherits).
var durable = !testing.Testing()

func init() {
	if !durable {
		os.Setenv("GIT_TEST_FSYNC", "0")
	}
}

// Event is one append-only record. Kind is dotted ("run.created", "task.merged").
type Event struct {
	Seq  int64           `json:"seq"`
	At   time.Time       `json:"at"`
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data,omitempty"`
}

// parseEvent reads one journal line. Append writes every line as
// {"seq":N,"at":"T","kind":"K","data":D}, and a start replays the journal
// once per module, so that layout is read directly, leaving D for Decode to
// check: json.Unmarshal of every line spent most of a start's CPU (13 s of
// it on 300 MB). Any other line goes through json.Unmarshal. raw may be
// reused once it returns, so what the Event keeps is copied; an event whose
// kind want refuses is passed over (false) before that.
func parseEvent(raw []byte, want func(string) bool) (Event, bool) {
	var e Event
	if ok, fast := fastEvent(raw, &e, want); fast {
		return e, ok
	}
	e = Event{}
	return e, json.Unmarshal(raw, &e) == nil && e.Kind != "" && (want == nil || want(e.Kind))
}

// fastEvent reads raw in Append's layout; fast is false for any other line.
func fastEvent(raw []byte, e *Event, want func(string) bool) (ok, fast bool) {
	rest, ok := bytes.CutPrefix(raw, []byte(`{"seq":`))
	if !ok {
		return false, false
	}
	i := 0
	for i < len(rest) && i < 19 && rest[i] >= '0' && rest[i] <= '9' {
		e.Seq = e.Seq*10 + int64(rest[i]-'0')
		i++
	}
	if rest, ok = bytes.CutPrefix(rest[i:], []byte(`,"at":"`)); i == 0 || !ok {
		return false, false
	}
	i = bytes.IndexByte(rest, '"')
	if i < 0 || bytes.IndexByte(rest[:i], '\\') >= 0 {
		return false, false
	}
	at, err := time.Parse(time.RFC3339Nano, string(rest[:i]))
	if err != nil {
		return false, false
	}
	e.At = at
	if rest, ok = bytes.CutPrefix(rest[i+1:], []byte(`,"kind":"`)); !ok {
		return false, false
	}
	i = bytes.IndexByte(rest, '"')
	if i <= 0 || bytes.IndexByte(rest[:i], '\\') >= 0 {
		return false, false
	}
	e.Kind = string(rest[:i])
	if want != nil && !want(e.Kind) {
		return false, true
	}
	switch rest = rest[i+1:]; {
	case string(rest) == "}":
	case bytes.HasPrefix(rest, []byte(`,"data":`)) && bytes.HasSuffix(rest, []byte("}")):
		e.Data = json.RawMessage(bytes.Clone(rest[len(`,"data":`) : len(rest)-1]))
	default:
		return false, false
	}
	return true, true
}

// Decode unmarshals the event payload into v.
func (e Event) Decode(v any) error { return json.Unmarshal(e.Data, v) }

// Journal is an append-only JSONL file. Every append is fsynced before it
// returns, so a crash loses at most the records being written; appends at
// once share one fsync. A torn last line is ignored on read.
type Journal struct {
	mu   sync.Mutex
	path string
	f    *os.File
	seq  int64
	subs []func(Event)
	// breaks counts events whose seq didn't rise past the one before: two
	// writers on one file. flagged is the count an earlier start reported.
	breaks, flagged int
	// size is where the next line goes; marks index the file for Since.
	size  int64
	marks []mark
	// spans index each kind's lines, so a Replay of a few kinds reads those
	// lines, not the whole journal: on 2026-10-30 the release module's
	// replays read the 1.09 GB file ~20 times every 5 minutes, ~40 MB/s of
	// the disk once it fell out of the page cache.
	spans map[string][]span
	// syncMu orders the fsyncs; synced is how much of the file the last one
	// covered. An append waits for an fsync that covers its line, one an
	// append beside it may already be making: on a saturated disk an fsync
	// took ~0.8 s, and one each under mu held every other append behind it.
	syncMu sync.Mutex
	synced int64
}

// A mark is where a line starts and the highest seq of every line before
// it, taken every markEvery bytes. Since starts at the last mark whose seq is
// at most the cursor, so a module polling /api/events every second reads the
// tail it wants, not the whole journal (981 MB on 2026-10-29, read from disk
// once it fell out of the page cache). The highest seq, not the last, keeps
// that right after two writers left seqs out of order.
type mark struct{ off, seq int64 }

const markEvery = 1 << 20

// A span is where one line starts and how long it is, without its newline.
type span struct{ off, n int64 }

// index notes a line of kind at off.
func (j *Journal) index(kind string, off int64, n int) {
	if j.spans == nil {
		j.spans = map[string][]span{}
	}
	j.spans[kind] = append(j.spans[kind], span{off, int64(n)})
}

// mark notes a line starting at off, before the event it holds is counted.
func (j *Journal) mark(off int64) {
	if last := int64(0); len(j.marks) > 0 {
		last = j.marks[len(j.marks)-1].off
		if off-last < markEvery {
			return
		}
	} else if off < markEvery {
		return
	}
	j.marks = append(j.marks, mark{off, j.seq})
}

// OpenJournal opens (or creates) the journal at path and reads its last seq.
func OpenJournal(path string) (*Journal, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	j := &Journal{path: path}
	whole, err := scan(path, 0, func(off int64, raw []byte) {
		e, ok := parseEvent(raw, nil)
		if !ok {
			return
		}
		j.mark(off)
		j.index(e.Kind, off, len(raw))
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
	})
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	if fi, err := f.Stat(); err == nil {
		j.size = fi.Size()
	}
	if j.size > whole {
		// A torn last line is ended, so the next event starts a line of its
		// own: on 2026-10-30 a start's hive.started was glued onto a torn
		// task.saved, the pair read as one task.saved with no task in it,
		// and the start's own event was lost.
		n, err := f.Write([]byte("\n"))
		j.size += int64(n)
		if err != nil {
			f.Close()
			return nil, err
		}
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
	j.mark(j.size)
	j.seq++
	e := Event{Seq: j.seq, At: time.Now().UTC(), Kind: kind, Data: raw}
	line, _ := json.Marshal(e)
	n, err := j.f.Write(append(line, '\n'))
	if err == nil {
		j.index(kind, j.size, len(line))
	}
	j.size += int64(n)
	end := j.size
	subs := append([]func(Event){}, j.subs...)
	j.mu.Unlock()
	if err == nil && durable {
		err = j.syncTo(end)
	}
	if err != nil {
		return Event{}, err
	}
	for _, fn := range subs {
		fn(e)
	}
	return e, nil
}

// syncTo returns once the file is fsynced up to end at least: at once if an
// fsync since covered it, else after one that covers every line written so
// far, the appends beside this one's too.
func (j *Journal) syncTo(end int64) error {
	j.syncMu.Lock()
	defer j.syncMu.Unlock()
	if j.synced >= end {
		return nil
	}
	j.mu.Lock()
	upto := j.size
	j.mu.Unlock()
	if err := j.f.Sync(); err != nil {
		return err
	}
	j.synced = upto
	return nil
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

// Replay calls fn for every complete event in the file, in order; with
// kinds, only for the events of those kinds ("release.ci_*" is every kind
// with that start), read from the index rather than the whole file. If
// another writer grew the file, the index doesn't cover it and the whole
// file is read.
func (j *Journal) Replay(fn func(Event), kinds ...string) error {
	if len(kinds) == 0 {
		return j.replay(fn, nil)
	}
	return j.replay(fn, func(kind string) bool {
		for _, k := range kinds {
			if pre, ok := strings.CutSuffix(k, "*"); ok && strings.HasPrefix(kind, pre) || kind == k {
				return true
			}
		}
		return false
	})
}

// ReplayExcept is Replay of every kind but those named, which are not read
// at all while the index holds. Most of the journal is two kinds most
// replays don't want (task.saved 51% of its bytes, run.created 41% on
// 2026-10-30), and naming those is shorter, and safer as a replay's switch
// grows, than naming every kind it does want.
func (j *Journal) ReplayExcept(fn func(Event), skip ...string) error {
	return j.replay(fn, func(kind string) bool { return !slices.Contains(skip, kind) })
}

// replay calls fn for the events whose kind want takes (all of them with
// nil), from the index when it covers the file.
func (j *Journal) replay(fn func(Event), want func(string) bool) error {
	j.mu.Lock()
	var at []span
	if want != nil {
		for k, s := range j.spans {
			if want(k) {
				at = append(at, s...)
			}
		}
	}
	size := j.size
	j.mu.Unlock()
	f, err := os.Open(j.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	if fi, err := f.Stat(); want == nil || j.f == nil || err != nil || fi.Size() != size {
		_, err := scan(j.path, 0, func(_ int64, raw []byte) {
			if e, ok := parseEvent(raw, want); ok {
				fn(e)
			}
		})
		return err
	}
	sort.Slice(at, func(a, b int) bool { return at[a].off < at[b].off })
	var buf []byte
	for _, s := range at {
		buf = slices.Grow(buf[:0], int(s.n))[:s.n]
		if _, err := f.ReadAt(buf, s.off); err != nil {
			return err
		}
		if e, ok := parseEvent(buf, nil); ok {
			fn(e)
		}
	}
	return nil
}

// Since returns events with Seq > after, reading from the last mark before
// them rather than from the start.
func (j *Journal) Since(after int64) []Event {
	j.mu.Lock()
	from := int64(0)
	if i := sort.Search(len(j.marks), func(i int) bool { return j.marks[i].seq > after }); i > 0 {
		from = j.marks[i-1].off
	}
	j.mu.Unlock()
	var out []Event
	_, _ = scan(j.path, from, func(_ int64, raw []byte) {
		if e, ok := parseEvent(raw, nil); ok && e.Seq > after {
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
	_, err := readFrom(path, 0, func(_ int64, raw []byte) { fn(raw) })
	return err
}

// readFrom calls fn with the offset and text of each complete non-empty line
// from byte off on, and returns where the last complete line ended.
func readFrom(path string, off int64, fn func(int64, []byte)) (int64, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return off, err
	}
	r := bufio.NewReaderSize(f, 1<<16)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			if len(line) > 1 {
				fn(off, line[:len(line)-1])
			}
			off += int64(len(line))
		}
		if err == io.EOF {
			return off, nil
		}
		if err != nil {
			return off, err
		}
	}
}

// scan is readFrom without a copy of each line: fn's line is a slice of a
// reused buffer, good only until fn returns. The journal's own reads use it,
// copying out only what an Event keeps.
func scan(path string, off int64, fn func(int64, []byte)) (int64, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return off, err
	}
	r := bufio.NewReaderSize(f, 1<<20)
	var long []byte // a line longer than r's buffer, gathered
	for {
		line, err := r.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			long = append(long, line...)
			continue
		}
		if len(long) > 0 {
			long = append(long, line...)
			line, long = long, long[:0]
		}
		if len(line) > 0 && line[len(line)-1] == '\n' {
			if len(line) > 1 {
				fn(off, line[:len(line)-1])
			}
			off += int64(len(line))
		}
		if err == io.EOF {
			return off, nil
		}
		if err != nil {
			return off, err
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
	if !durable {
		return nil
	}
	return f.Sync()
}
