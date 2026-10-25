package app

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// TestAnEventBringsTheNextTickForward: a step that ends journals, and the next
// tick comes at once, not at the end of the second.
func TestAnEventBringsTheNextTickForward(t *testing.T) {
	j, err := core.OpenJournal(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time, 16)
	go loop(ctx, j, func() { ticks <- time.Now() })
	<-ticks // the first tick is at once
	// A loaded machine (checks running beside the test) can hold the loop
	// back past the second once; the tick never coming early three times
	// running is the bug.
	var got time.Duration
	for try := 0; try < 3; try++ {
		time.Sleep(100 * time.Millisecond)
		for len(ticks) > 0 {
			<-ticks
		}
		at := time.Now()
		j.MustAppend("task.saved", map[string]string{})
		if got = (<-ticks).Sub(at); got <= 500*time.Millisecond {
			return
		}
	}
	t.Fatalf("the tick after an event came %s later; want well under the second", got)
}

// TestATickThatAlwaysJournalsCantSpin: a step that journals on every tick
// gets at most wakeBurst early ticks a second.
func TestATickThatAlwaysJournalsCantSpin(t *testing.T) {
	j, err := core.OpenJournal(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	var n atomic.Int32
	loop(ctx, j, func() { n.Add(1); j.MustAppend("noisy", map[string]string{}) })
	// 2.5s: the first tick, two of the second's, and wakeBurst early ones after each.
	if got, most := int(n.Load()), 3*(wakeBurst+1); got > most {
		t.Fatalf("%d ticks in 2.5s from a tick that always journals; want at most %d", got, most)
	}
}
