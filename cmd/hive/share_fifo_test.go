//go:build unix

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/scan"
)

// A run that keeps swapping a file of its box for a FIFO, which nothing
// writes to, never hangs share: the open can't wait for a writer.
func TestShareNeverHangsOnASwappedFIFO(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	box := ws.RunBox("r1")
	os.MkdirAll(box, 0o755)
	file, fifo, real := filepath.Join(box, "out.log"), filepath.Join(box, "pipe"), filepath.Join(box, "real")
	os.WriteFile(file, []byte("plain"), 0o644)
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skip("no FIFOs here:", err)
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			os.Rename(file, real)
			os.Rename(fifo, file) // the FIFO takes the file's place
			os.Rename(file, fifo)
			os.Rename(real, file)
		}
	}()
	defer func() { close(stop); <-done }()
	finished := make(chan error, 1)
	go func() {
		for i := 0; i < 2000; i++ {
			if _, err := writeBundle(&bytes.Buffer{}, ws, shareParts(ws), scan.Lists{Handles: []string{"nobody-at-all"}}); err != nil {
				finished <- err
				return
			}
		}
		finished <- nil
	}()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		// unblock the hung open so the test can end
		if f, err := os.OpenFile(file, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			f.Close()
		}
		t.Fatal("share hung opening a FIFO")
	}
}
