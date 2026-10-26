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
	go loop(ctx, j, make(chan struct{}, 1), func() { ticks <- time.Now() })
	<-ticks // the first tick is at once
	time.Sleep(100 * time.Millisecond)
	at := time.Now()
	j.MustAppend("task.saved", map[string]string{})
	if got := (<-ticks).Sub(at); got > 500*time.Millisecond {
		t.Fatalf("the tick after an event came %s later; want well under the second", got)
	}
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
	loop(ctx, j, make(chan struct{}, 1), func() { n.Add(1); j.MustAppend("noisy", map[string]string{}) })
	// 2.5s: the first tick, two of the second's, and wakeBurst early ones after each.
	if got, most := int(n.Load()), 3*(wakeBurst+1); got > most {
		t.Fatalf("%d ticks in 2.5s from a tick that always journals; want at most %d", got, most)
	}
}

// TestEventsFromElsewhereArentCapped: a tick that doesn't journal is woken by
// every nudge from elsewhere, past wakeBurst in a second, so a stream of
// verdicts and requests doesn't wait out the second. The nudges go on the wake
// channel, not the journal, so a slow fsync on a busy machine can't starve it.
func TestEventsFromElsewhereArentCapped(t *testing.T) {
	j, err := core.OpenJournal(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 950*time.Millisecond)
	defer cancel()
	wake := make(chan struct{}, 1)
	go func() {
		for ctx.Err() == nil {
			nudge(wake)
			time.Sleep(10 * time.Millisecond)
		}
	}()
	var n atomic.Int32
	loop(ctx, j, wake, func() { n.Add(1) })
	if got := int(n.Load()); got <= wakeBurst+1 {
		t.Fatalf("%d ticks in 0.95s with a nudge from elsewhere every 10ms; want more than %d", got, wakeBurst+1)
	}
}
