package core

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Where the daemon's scratch files go. On many Linux machines the host's
// /tmp is a tmpfs: what is written there is memory, and what a killed test,
// gate or build leaves there stays held until a reboot. So a daemon whose
// temp folder is an in-memory /tmp moves its own (TMPDIR, which everything it
// starts on the host inherits) onto disk, into the workspace's private
// folder (Temp), and the hourly clean job removes what the Hive left in the
// shared /tmp and in its own once nothing has touched it for leftoverAge.

// Temp is the daemon's own temp folder, on disk. It sits in Private, covered
// wherever agents read .hive, so no agent reads or writes what is in it.
func (w Workspace) Temp() string { return filepath.Join(w.Private(), "tmp") }

// leftoverAge is how long nothing in a Hive temp folder must have changed
// before the clean job takes it for a leftover: a gate is bounded at two
// hours, a soft-layer arm at a few.
const leftoverAge = 24 * time.Hour

// sharedTmp is the host's shared temp folder, swapped in tests.
var sharedTmp = "/tmp"

// UseDiskTemp points this process's TMPDIR at ws.Temp when the temp folder
// it would use is the host's /tmp and that is in memory. A TMPDIR set to
// anything else is left as it is. It returns the folder it chose, "" when it
// left TMPDIR alone.
func UseDiskTemp(ws Workspace) (string, error) {
	if t := os.Getenv("TMPDIR"); t != "" && filepath.Clean(t) != sharedTmp {
		return "", nil
	}
	if !inMemory(sharedTmp) {
		return "", nil
	}
	dir, err := filepath.Abs(ws.Temp())
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if inMemory(dir) { // the workspace itself is in memory: nothing gained
		return "", nil
	}
	return dir, os.Setenv("TMPDIR", dir)
}

// hiveLeftovers are the names the Hive and its tests give what they make in
// the shared temp folder. The sandbox's hive-sbx- runtime folders are its own
// sweep's (sandbox.Sweep), held by their locks.
var hiveLeftovers = []string{"hive-*", "TestSoftLayer*"}

// pruneTemp removes the Hive's leftovers from the shared temp folder and
// everything in its own temp folder that nothing has changed in for
// leftoverAge, through drop.
func pruneTemp(ws Workspace, drop func(string)) {
	var ms []string
	for _, g := range hiveLeftovers {
		m, _ := filepath.Glob(filepath.Join(sharedTmp, g))
		ms = append(ms, m...)
	}
	if es, err := os.ReadDir(ws.Temp()); err == nil {
		for _, e := range es {
			ms = append(ms, filepath.Join(ws.Temp(), e.Name()))
		}
	}
	for _, m := range ms {
		if !strings.HasPrefix(filepath.Base(m), "hive-sbx-") && quiet(m, leftoverAge) {
			drop(m)
		}
	}
}

// quiet reports whether nothing in the tree at p has changed for d. It stops
// at the first thing that has.
func quiet(p string, d time.Duration) bool {
	since := time.Now().Add(-d)
	ok := true
	filepath.WalkDir(p, func(_ string, e fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if fi, err := e.Info(); err == nil && fi.ModTime().After(since) {
			ok = false
			return filepath.SkipAll
		}
		return nil
	})
	return ok
}
