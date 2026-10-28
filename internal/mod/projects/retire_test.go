package projects

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// A bare name goes in repo_owner, else self_fork, before the forge login.
func TestBareNameGoesInTheOwnerNamed(t *testing.T) {
	m, _ := stewardHive(t, map[string]string{})
	if p, _, err := m.Create(Spec{Name: "plain"}); err != nil || p.Repo != "local/plain" {
		t.Fatalf("no owner named: %+v %v", p, err)
	}
	cfg := m.C.WS.LoadConfig()
	cfg.SelfFork = "lab"
	m.C.WS.SaveConfig(cfg)
	if p, _, err := m.Create(Spec{Name: "forked"}); err != nil || p.Repo != "lab/forked" {
		t.Fatalf("self_fork: %+v %v", p, err)
	}
	cfg.RepoOwner = "org"
	m.C.WS.SaveConfig(cfg)
	if p, _, err := m.Create(Spec{Name: "owned"}); err != nil || p.Repo != "org/owned" {
		t.Fatalf("repo_owner: %+v %v", p, err)
	}
}

// A lead retires a project whose setup never finished: it leaves the
// registry for good, its name is free again, and its setup alert is cleared.
func TestLeadRetiresAProjectMadeByMistake(t *testing.T) {
	m, _ := stewardHive(t, map[string]string{})
	if _, err := m.C.Projects.Add(core.Project{Name: "broken", Repo: "nobody/broken"}); err != nil {
		t.Fatal(err)
	}
	lead := core.Run{ID: "lead-x", Role: "lead"}
	if _, err := m.opRetire(lead, json.RawMessage(`{"name":"broken"}`)); err == nil {
		t.Fatal("retired without a why")
	}
	if _, err := m.opRetire(lead, json.RawMessage(`{"name":"broken","why":"made in an owner the Hive can't write"}`)); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.C.Projects.Get("broken"); ok {
		t.Fatal("still in the registry")
	}
	if _, ok := core.NewProjects(m.C.J).Get("broken"); ok {
		t.Fatal("back after a replay")
	}
	if n := m.C.Projects.UniqueName("broken", "lab/broken"); n != "broken" {
		t.Fatalf("name not free: %s", n)
	}
	cleared := false
	for _, e := range m.C.J.Since(0) {
		cleared = cleared || (e.Kind == "alert.cleared" && strings.Contains(string(e.Data), "setup:broken"))
	}
	if !cleared {
		t.Fatal("setup alert not cleared")
	}
	if _, err := m.opRetire(lead, json.RawMessage(`{"name":"broken","why":"again"}`)); err == nil {
		t.Fatal("retired a project that isn't there")
	}
}

// Work in flight, and the Hive's own project, stay.
func TestRetireRefusesProjectsWithWork(t *testing.T) {
	m, _ := stewardHive(t, map[string]string{})
	if _, _, err := m.Create(Spec{Name: "busy"}); err != nil {
		t.Fatal(err)
	}
	m.Work.File(work.Brief{Project: "busy", From: "lead-x", Title: "something"})
	lead := core.Run{ID: "lead-x", Role: "lead"}
	if _, err := m.opRetire(lead, json.RawMessage(`{"name":"busy","why":"x"}`)); err == nil || !strings.Contains(err.Error(), "in flight") {
		t.Fatalf("retired a project with work in flight: %v", err)
	}
	if _, err := m.C.Projects.Add(core.Project{Name: "hive", Repo: "local/hive", Self: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.opRetire(lead, json.RawMessage(`{"name":"hive","why":"x"}`)); err == nil {
		t.Fatal("retired the Hive's own project")
	}
}

// The forge refusing the login is told apart from a fault that passes.
func TestNoAccess(t *testing.T) {
	if !noAccess(errors.New("clone: exit status 128: remote: Write access to repository not granted. fatal: unable to access: The requested URL returned error: 403")) {
		t.Error("a 403 isn't no access")
	}
	if noAccess(errors.New("Could not resolve host: github.com")) {
		t.Error("the network down is no access")
	}
}
