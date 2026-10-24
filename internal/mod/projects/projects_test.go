package projects

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// The status op shows agents only the allow-listed settings: a config with
// every field set to a secret shows none of it outside the list.
func TestStatusConfigIsAllowListed(t *testing.T) {
	allowed := []string{"autonomy", "harness", "model", "lead_model", "reviewer_model", "tribunal_models", "model_choices", "schedules", "review_rounds", "plateau_merges", "auto_update"}
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
