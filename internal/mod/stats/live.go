package stats

import (
	"bytes"
	"io"
	"os"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

// Live estimates what each running turn has spent so far, so a turn's cost
// moves before its result line says it (issue #348). It reads each running
// turn's output from where it left off, at most every ten seconds, and prices
// the replies' tokens: at the named model's price when the person set one,
// else at the harness's list price (Claude's); a harness with neither (Codex,
// the open agent) shows none. The figure is the tally's to serve (View.Live)
// until the turn ends; it is never journalled or counted in spend.
type Live struct {
	C     *core.Core
	T     *Tally
	reads map[string]*reading // by run
}

type reading struct {
	turn  int
	off   int64     // bytes of the turn's output read
	at    time.Time // the last read
	usage harness.Usage
}

// Tick is Live's step on the module tick list.
func (l *Live) Tick() {
	if l.reads == nil {
		l.reads = map[string]*reading{}
	}
	cfg := l.C.WS.LoadConfig()
	seen := map[string]bool{}
	for _, r := range l.C.Runs.List(func(r *core.Run) bool { return r.Status == core.StatusRunning }) {
		seen[r.ID] = true
		rd := l.reads[r.ID]
		if rd == nil || rd.turn != r.Turn {
			rd = &reading{turn: r.Turn}
			l.reads[r.ID] = rd
		}
		if time.Since(rd.at) < 10*time.Second {
			continue
		}
		h, err := harness.Get(r.HarnessAt(r.Turn))
		if err != nil {
			continue
		}
		out := l.C.WS.TurnOut(r.ID, r.Turn)
		if fi, err := os.Stat(out); err != nil || fi.Size() == rd.off {
			continue
		}
		rd.at = time.Now()
		rd.off = readLines(out, rd.off, func(line []byte) {
			for _, ev := range h.Parse(line) {
				rd.usage.Add(ev)
			}
		})
		l.T.live(r.ID, r.Turn, estimate(h, r, rd.usage, cfg))
	}
	for id := range l.reads {
		if !seen[id] {
			delete(l.reads, id)
		}
	}
}

// estimate is what u's tokens cost on r's model.
func estimate(h harness.Harness, r core.Run, u harness.Usage, cfg core.Config) float64 {
	t := u.Total()
	if t == (harness.Tokens{}) {
		return 0
	}
	if p := cfg.Models[r.Model].Price; p != nil {
		return p.Cost(t)
	}
	if p, ok := harness.ListPrice(h, or(u.Model, r.Model)); ok {
		return p.Cost(t)
	}
	return 0
}

// readLines calls fn with each whole line of path past off and returns the
// offset after the last one; a line still being written waits for the next read.
func readLines(path string, off int64, fn func([]byte)) int64 {
	f, err := os.Open(path)
	if err != nil {
		return off
	}
	defer f.Close()
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return off
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return off
	}
	end := bytes.LastIndexByte(b, '\n') + 1
	for _, line := range bytes.Split(b[:end], []byte("\n")) {
		if len(line) > 0 {
			fn(line)
		}
	}
	return off + int64(end)
}
