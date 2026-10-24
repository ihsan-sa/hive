package projects

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// The status op shows agents only the allow-listed settings: a config with
// every field set to a secret shows none of it outside the list.
func TestStatusConfigIsAllowListed(t *testing.T) {
	allowed := []string{"autonomy", "harness", "model", "lead_model", "reviewer_model", "tribunal_models", "model_choices", "schedules", "review_rounds", "plateau_merges", "max_workers", "max_checks", "auto_update"}
	var c core.Config
	v := reflect.ValueOf(&c).Elem()
	for i := range v.NumField() {
		f := v.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString("SECRET")
		case reflect.Slice:
			if f.Type().Elem().Kind() == reflect.String {
				f.Set(reflect.ValueOf([]string{"SECRET"}))
			}
		}
	}
	c.Projects = map[string]core.ProjectSettings{"other": {Model: "SECRET"}}
	c.Egress.Default = []string{"SECRET"}
	c.Harness, c.Model, c.LeadModel, c.ReviewerModel, c.ModelChoices = "claude", "opus", "opus", "opus", nil
	raw, _ := json.Marshal(View(c))
	var got map[string]any
	json.Unmarshal(raw, &got)
	for k := range got {
		if !slices.Contains(allowed, k) {
			t.Errorf("status shows %q, which isn't on the allow-list", k)
		}
	}
	if strings.Contains(string(raw), "SECRET") {
		t.Errorf("a hidden setting leaked into status: %s", raw)
	}
}

// A level the Hive doesn't know is refused before anything is made on the forge.
func TestCreateRefusesAnUnknownSharing(t *testing.T) {
	m := &Module{C: &core.Core{}}
	if _, _, err := m.Create(Spec{Name: "x", Sharing: "public"}); err == nil || !strings.Contains(err.Error(), "sharing is one of") {
		t.Errorf("an unknown level: %v", err)
	}
}

// A hidden project's lead can't carry its text out as a new project's name,
// description or goals: nothing is made and nothing reaches the record.
func TestHiddenLeadCannotCreateProjects(t *testing.T) {
	dir := t.TempDir()
	ws := core.Workspace{Root: filepath.Join(dir, "hive")}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	forge := &core.Local{Dir: filepath.Join(dir, "forge")}
	c, err := core.Open(ws, forge)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	m := &Module{C: c}
	for _, level := range []core.Project{{Name: "walled", Repo: "me/walled", Isolated: true}, {Name: "quiet", Repo: "me/quiet", Private: true}} {
		if _, err := c.Projects.Add(level); err != nil {
			t.Fatal(err)
		}
		lead := core.Run{ID: "lead-" + level.Name, Role: "lead", Project: level.Name}
		args := `{"name":"secret-out","description":"SECRET TEXT","goals":"SECRET GOALS","sharing":"shared"}`
		if _, err := m.opCreate(lead, json.RawMessage(args)); err == nil {
			t.Fatalf("%s's lead created a project", level.Name)
		}
		if _, ok := c.Projects.Get("secret-out"); ok {
			t.Fatalf("%s's text reached the project list", level.Name)
		}
		for _, e := range c.J.Since(0) {
			if strings.Contains(string(e.Data), "SECRET") {
				t.Fatalf("%s's text reached the record: %s", level.Name, e.Data)
			}
		}
		if empty, err := forge.IsEmpty("me/secret-out"); err == nil && !empty {
			t.Fatalf("%s's text reached the forge", level.Name)
		}
	}
	// A caller whose project the Hive doesn't know can't show it has no wall.
	gone := core.Run{ID: "lead-gone", Role: "lead", Project: "gone"}
	if _, err := m.opCreate(gone, json.RawMessage(`{"name":"secret-out","goals":"SECRET GOALS"}`)); err == nil || !strings.Contains(err.Error(), "isn't a project") {
		t.Fatalf("an unknown project's lead created a project: %v", err)
	}
	if _, ok := c.Projects.Get("secret-out"); ok {
		t.Fatal("an unknown project's text reached the project list")
	}
}
