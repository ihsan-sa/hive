package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// The lead runs on the strongest model by default. A config from before the
// lead had its own setting keeps a model the person chose for the lead too,
// and moves the old default to the new one.
func TestLeadModel(t *testing.T) {
	for _, c := range []struct{ name, raw, want string }{
		{"fresh install", "", "opus"},
		{"old default", `{"model": "sonnet"}`, "opus"},
		{"chosen model", `{"model": "haiku"}`, "haiku"},
		{"set its own", `{"model": "haiku", "lead_model": "sonnet"}`, "sonnet"},
	} {
		ws := Workspace{Root: t.TempDir()}
		if c.raw != "" {
			if err := WriteFileAtomic(ws.ConfigPath(), []byte(c.raw), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if got := ws.LoadConfig().LeadModel; got != c.want {
			t.Errorf("%s: lead model %q, want %q", c.name, got, c.want)
		}
	}
}

// Writers racing on one file each get their own temp file: one named by the
// date alone was shared by every write that day, so a rename found it gone
// (or, on Windows, open in the other writer).
func TestWriteFileAtomicConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for n := 0; n < 20; n++ {
				if err := WriteFileAtomic(path, []byte(fmt.Sprintf("writer %d\n", i)), 0o600); err != nil {
					errs <- err
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(path); err != nil || len(raw) == 0 {
		t.Fatalf("read %q, %v", raw, err)
	}
	if left, _ := filepath.Glob(path + ".tmp-*"); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}
