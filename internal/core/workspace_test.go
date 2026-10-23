package core

import "testing"

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
