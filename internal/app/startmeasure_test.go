package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// TestStartMeasure is hive-autoresearch's start-time metric, not a check: it
// runs only with HIVE_START_JOURNAL set (a journal to start from) and
// HIVE_START_WS (an empty folder, ideally on tmpfs). It copies the journal into
// a fresh workspace there and opens it once, so its records are caught up
// as a running Hive's are (kb's record/ the first time files every event);
// then, as a daemon's restart does, opens it again and prints the CPU (user+sys) and the bytes
// allocated by New, the part of a daemon's start it logs as "building the
// modules".
func TestStartMeasure(t *testing.T) {
	src, dir := os.Getenv("HIVE_START_JOURNAL"), os.Getenv("HIVE_START_WS")
	if src == "" || dir == "" {
		t.Skip("HIVE_START_JOURNAL and HIVE_START_WS unset")
	}
	os.RemoveAll(dir)
	defer os.RemoveAll(dir)
	ws := core.Workspace{Root: dir}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(filepath.Join(ws.Hive(), "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	in.Close()
	out.Close()
	first, err := New(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	first.Close()
	runtime.GC()
	var m0, m1 runtime.MemStats
	var r0, r1 syscall.Rusage
	runtime.ReadMemStats(&m0)
	syscall.Getrusage(syscall.RUSAGE_SELF, &r0)
	a, err := New(ws, nil)
	syscall.Getrusage(syscall.RUSAGE_SELF, &r1)
	runtime.ReadMemStats(&m1)
	if err != nil {
		t.Fatal(err)
	}
	a.Close()
	ms := func(r syscall.Rusage) int64 { return (r.Utime.Nano() + r.Stime.Nano()) / 1e6 }
	fmt.Printf("alloc_mb=%d\nmallocs_k=%d\ninblock=%d\ncpu_ms=%d\n", (m1.TotalAlloc-m0.TotalAlloc)>>20, (m1.Mallocs-m0.Mallocs)/1000, r1.Inblock-r0.Inblock, ms(r1)-ms(r0))
}
