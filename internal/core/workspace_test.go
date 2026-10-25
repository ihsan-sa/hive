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

// A config from before the pace saved the then defaults as max_workers and
// max_checks; reading it clears them, so the pace can grow. Other values, and
// any in a config that has the ceilings marker, are the person's.
func TestOldDefaultCeilingsClear(t *testing.T) {
	old := DefaultMaxChecks()
	for _, c := range []struct {
		name            string
		raw             string
		workers, checks int
	}{
		{"fresh install", "", 0, 0},
		{"old defaults", fmt.Sprintf(`{"max_workers": 4, "max_checks": %d}`, old), 0, 0},
		{"chosen before the pace", fmt.Sprintf(`{"max_workers": 8, "max_checks": %d}`, old+1), 8, old + 1},
		{"chosen since", fmt.Sprintf(`{"max_workers": 4, "max_checks": %d, "ceilings": true}`, old), 4, old},
	} {
		ws := Workspace{Root: t.TempDir()}
		if c.raw != "" {
			if err := WriteFileAtomic(ws.ConfigPath(), []byte(c.raw), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		cfg := ws.LoadConfig()
		if cfg.MaxWorkers != c.workers || cfg.MaxChecks != c.checks || !cfg.Ceilings {
			t.Errorf("%s: max_workers %d max_checks %d ceilings %v, want %d %d true", c.name, cfg.MaxWorkers, cfg.MaxChecks, cfg.Ceilings, c.workers, c.checks)
		}
	}
}

// A config saved before public_owners keeps the default owner, so a public
// repository there isn't quietly turned into an ask; one that lists its own,
// even none, keeps that.
func TestPublicOwnersFromAnOlderConfig(t *testing.T) {
	for _, c := range []struct{ name, raw, want string }{
		{"older config", `{"model": "opus"}`, "[Hive-RSI-Lab]"},
		{"its own", `{"public_owners": ["me"]}`, "[me]"},
		{"none", `{"public_owners": []}`, "[]"},
	} {
		ws := Workspace{Root: t.TempDir()}
		if err := WriteFileAtomic(ws.ConfigPath(), []byte(c.raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprint(ws.LoadConfig().PublicOwners); got != c.want {
			t.Errorf("%s: public_owners %s, want %s", c.name, got, c.want)
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

// A workspace given by a relative path still guards absolute paths, so a
// host interpreter's install check never skips them.
func TestGuardedIsAbsolute(t *testing.T) {
	t.Chdir(t.TempDir())
	w := Workspace{Root: "rel"}
	for _, g := range append(w.Guarded(), w.Covered()...) {
		if !filepath.IsAbs(g) {
			t.Errorf("guarded %q is relative", g)
		}
	}
}
