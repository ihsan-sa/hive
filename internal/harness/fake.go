package harness

// Fake is a scripted stand-in for a coding harness. It runs `hive fake-agent`,
// which speaks the same stream-json protocol and uses the same mailbox as a
// real agent, so tests and the release dry run exercise the real path without
// spending any plan usage.
type Fake struct{}

func (Fake) Name() string           { return "fake" }
func (Fake) Program() string        { return "hive" }
func (Fake) LoginCommand() []string { return nil }

func (Fake) Args(t Turn) []string {
	a := []string{"fake-agent", "--role", t.Role, "--session", t.SessionID}
	if t.Resume {
		a = append(a, "--resume")
	}
	if t.ReadOnly {
		a = append(a, "--readonly", "--add-dir", t.RunDir)
	}
	if t.Schema != "" {
		a = append(a, "--schema")
	}
	return a
}

func (Fake) Env(Turn) []string { return nil }

// Parse uses Claude's stream-json shape, which the fake emits.
func (Fake) Parse(line []byte) []Event { return Claude{}.Parse(line) }
