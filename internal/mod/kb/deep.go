package kb

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/modules"
)

// The deep search: `hive agent search --deep "question"` hands the question
// to a researcher, a run on a small model with a budget that reads the
// knowledge base and the Hive's records in its own sandbox, follows the
// connections the search shows, and answers with its sources. It has the
// read tools only (Read, Grep, Glob: no shell, so no ops and no writes) and
// gives a structured answer, so it ends with its one turn. Its answer comes
// to the asker as a message; Tick sends it, once, even across a restart.

// Researcher is the deep search's role.
const Researcher = "researcher"

// Deep search's bounds: the model, the most one may spend, and how many may
// run at once across the Hive (each asker has one at a time).
var (
	DeepModel  = "haiku"
	DeepBudget = 0.40
	DeepAtOnce = 3
)

// researchSchema is the researcher's answer.
const researchSchema = `{"type":"object","properties":{"answer":{"type":"string"},"sources":{"type":"array","items":{"type":"string"}}},"required":["answer","sources"]}`

// Deep starts a researcher on question for run r and says where its answer goes.
func (m *Module) Deep(r core.Run, question, hits string) (string, error) {
	question = strings.TrimSpace(question)
	switch {
	case question == "":
		return "", fmt.Errorf("a deep search needs a question: --deep \"why was #111 reverted\"")
	case r.Role == Researcher:
		return "", fmt.Errorf("you are the deep search: use the search's results and read the files")
	}
	live := m.C.Runs.List(func(x *core.Run) bool { return x.Role == Researcher && !x.Terminal() })
	for _, x := range live {
		if x.Inputs["asker"] == r.ID {
			return "", fmt.Errorf("your deep search %s is still at work; its answer comes to you as a message", x.ID)
		}
	}
	if len(live) >= DeepAtOnce {
		return "", fmt.Errorf("%d deep searches are at work already; search without --deep, or ask again later", len(live))
	}
	id := "kbq-" + core.NewID()
	dir, err := m.Agents.Scratch(id)
	if err != nil {
		return "", err
	}
	spec := m.Agents.Spec(id, Researcher, r.Project, dir, fmt.Sprintf("%s\n\n## The question\n\n%s\n\nYour budget is $%.2f on %s; what the search found for the question's words is in $HIVE_RUN/search.md.", researcherPrompt(m.C.WS), question, DeepBudget, DeepModel))
	// On Claude Code, the small model; any other harness keeps its own,
	// and Start refuses one that can't hold a run to its budget.
	if spec.Harness == "claude" {
		spec.Model = DeepModel
	}
	spec.Route = nil
	spec.Budget, spec.Schema = DeepBudget, researchSchema
	spec.Tools = []string{"Read", "Grep", "Glob"}
	spec.Inputs = map[string]string{"asker": r.ID, "question.md": question + "\n", "search.md": hits + "\n"}
	if _, err := m.C.Runs.Start(spec); err != nil {
		return "", err
	}
	m.C.J.MustAppend("kb.deep", map[string]string{"run": id, "asker": r.ID, "question": core.Clip(question, 300)})
	if r.ID != "" {
		m.C.Runs.Wait(r.ID, "its deep search "+id)
	}
	return fmt.Sprintf("deep search %s started on %s (up to $%.2f); its answer comes to you as a message: end your turn to wait for it. Meanwhile, what the plain search found:\n\n%s", id, DeepModel, DeepBudget, hits), nil
}

// researcherPrompt is the kb module's researcher.md, as installed, else as shipped.
func researcherPrompt(ws core.Workspace) string {
	if raw, err := os.ReadFile(filepath.Join(ws.Modules(), "kb", "researcher.md")); err == nil {
		return string(raw)
	}
	raw, _ := modules.FS.ReadFile("kb/researcher.md")
	return string(raw)
}

// answerDeep sends each finished researcher's answer to the run that asked,
// once: kb.answered records it, so a restart doesn't send it again.
func (m *Module) answerDeep() {
	for _, x := range m.C.Runs.List(func(x *core.Run) bool { return x.Role == Researcher && x.Terminal() }) {
		m.mu.Lock()
		done := m.answered[x.ID]
		m.mu.Unlock()
		if done {
			continue
		}
		var a struct {
			Answer  string
			Sources []string
		}
		text := ""
		switch {
		case json.Unmarshal(x.Structured, &a) == nil && strings.TrimSpace(a.Answer) != "":
			text = strings.TrimSpace(a.Answer)
			if len(a.Sources) > 0 {
				text += "\n\nSources:\n- " + strings.Join(a.Sources, "\n- ")
			}
		case x.Status == core.StatusDone && strings.TrimSpace(x.Result) != "":
			text = strings.TrimSpace(x.Result)
		default:
			text = "it gave no answer (" + x.Status + core.Clip(": "+x.Reason, 300) + "); search without --deep and read the files it names"
		}
		msg := fmt.Sprintf("[deep search %s answered, $%.2f: %s]\n%s", x.ID, x.Cost, strings.TrimSpace(x.Inputs["question.md"]), text)
		err := m.C.Runs.Send(x.Inputs["asker"], msg)
		result := "sent"
		if err != nil {
			result = "not sent: " + err.Error()
		}
		m.mu.Lock()
		m.answered[x.ID] = true
		m.mu.Unlock()
		m.C.J.MustAppend("kb.answered", map[string]string{"run": x.ID, "asker": x.Inputs["asker"], "result": result})
	}
}
