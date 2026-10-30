package core

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

// A project's runs read its own secrets folder read-only at /hive/secrets;
// a read-only run, another project's run and a project with no folder get
// none, and every run that reads /hive/state finds .hive/secrets covered.
func TestProjectSecretsMountOnlyForItsOwnRuns(t *testing.T) {
	ws := Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ws.ProjectSecrets("hifi"), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(ws.ProjectSecrets("hifi"), "relay.json"), []byte("{}"), 0o600)
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	rs := NewRuns(ws, j, nil, NewTokens(ws), func() []string { return nil })
	h, _ := harness.Get("fake")
	spec := func(r Run) sandbox.Spec {
		s, err := rs.sandboxSpec(rs.ws.LoadConfig(), isolatedBox{}, h, r, 1, false, "", nil, "")
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	secrets := func(s sandbox.Spec) *sandbox.Mount {
		for _, m := range s.Mounts {
			if m.Inside == "/hive/secrets" {
				return &m
			}
		}
		return nil
	}
	own := spec(Run{RunSpec: RunSpec{ID: "r1", Project: "hifi", Role: "worker", Checkout: t.TempDir()}})
	m := secrets(own)
	if m == nil || m.Host != ws.ProjectSecrets("hifi") || m.RW || m.Hide {
		t.Fatalf("the project's own worker reads its secrets read-only: %+v", m)
	}
	if !slices.Contains(own.Env, "HIVE_SECRETS=/hive/secrets") {
		t.Fatalf("HIVE_SECRETS not set: %v", own.Env)
	}
	covered := false
	for _, m := range own.Mounts {
		covered = covered || (m.Hide && m.Inside == "/hive/state/secrets")
	}
	if !covered {
		t.Fatal(".hive/secrets is not covered in /hive/state")
	}
	for _, r := range []Run{
		{RunSpec: RunSpec{ID: "r2", Project: "other", Role: "worker", Checkout: t.TempDir()}},
		{RunSpec: RunSpec{ID: "r3", Project: "hifi", Role: "reviewer", ReadOnly: true, Checkout: t.TempDir()}},
		{RunSpec: RunSpec{ID: "r4", Project: "hifi", Role: "warden", Checkout: t.TempDir()}},
	} {
		s := spec(r)
		if m := secrets(s); m != nil {
			t.Fatalf("%s/%s reads secrets: %+v", r.Project, r.Role, m)
		}
		for _, e := range s.Env {
			if e == "HIVE_SECRETS=/hive/secrets" {
				t.Fatalf("%s/%s has HIVE_SECRETS", r.Project, r.Role)
			}
		}
	}
	if d := rs.secretsFor(Run{RunSpec: RunSpec{Project: "../hifi"}}); d != "" {
		t.Fatalf("a project name that leaves the folder reads %s", d)
	}
}
