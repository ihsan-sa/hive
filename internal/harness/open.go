package harness

import "fmt"

// Open is the Hive's own open harness: `hive open-agent`, an agent loop that
// drives any OpenAI-compatible chat endpoint (a model server on this machine
// such as Ollama, or a hosted tier such as OpenRouter) with shell and file
// tools, and speaks Claude's stream-json. The endpoint, the model id and any
// key come in the turn's environment from the named model's settings, so
// the model a turn names is that name.
type Open struct{}

func (Open) Name() string           { return "open" }
func (Open) CapsSpend()             {} // the models it runs are free, so a budget is never spent
func (Open) Program() string        { return "hive" }
func (Open) LoginCommand() []string { return nil }

func (Open) Args(t Turn) []string {
	a := []string{"open-agent", "--session", t.SessionID, "--model", t.Model}
	if t.Resume {
		a = append(a, "--resume")
	}
	if t.SystemFile != "" {
		a = append(a, "--system-file", t.SystemFile)
	}
	if t.ReadOnly {
		a = append(a, "--readonly", "--add-dir", t.RunDir)
	}
	if t.SchemaFile != "" {
		a = append(a, "--schema-file", t.SchemaFile)
	}
	if t.MaxOutput > 0 {
		a = append(a, "--max-output", fmt.Sprint(t.MaxOutput))
	}
	return a
}

func (Open) Env(Turn) []string { return nil }

// Parse uses Claude's stream-json shape, which the open agent emits.
func (Open) Parse(line []byte) []Event { return Claude{}.Parse(line) }
