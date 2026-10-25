// Package softlayer runs the Hive's soft layer as an experiment: the same
// Hive, the same task, with very different agent prompts (the agents module),
// and an observer that reads what each sub-Hive did from its journal. The
// hard layer (core, sandbox, merge path) is the same in every arm; only the
// words the agents start with change. See docs/soft-layer.md.
package softlayer

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/modules"
)

// Roles are the prompt files of the agents module, orientation first.
var Roles = []string{"orientation", "lead", "worker", "reviewer", "sweep", "filer", "warden", "tribunal", "module-reviewer"}

// Variant is one soft layer: a full set of agents-module prompt files.
type Variant struct {
	Name  string
	About string
	// Files builds every prompt file from the standard ones and the essay.
	Files func(std map[string]string, essay string) (map[string]string, error)
}

// oneLine is the role in one line, for the arms that give only that.
var oneLine = map[string]string{
	"lead":            "You are the lead.",
	"worker":          "You are a worker.",
	"reviewer":        "You are a reviewer.",
	"sweep":           "You are the sweep.",
	"filer":           "You are the filer.",
	"warden":          "You are a warden.",
	"tribunal":        "You are a panelist of a tribunal.",
	"module-reviewer": "You are a module reviewer.",
}

func lines(orientation string) map[string]string {
	out := map[string]string{"orientation": orientation}
	for r, l := range oneLine {
		out[r] = l + "\n"
	}
	return out
}

// tools is the standard orientation's "Talking to the Hive" list, the commands
// alone: the arms that add it learn what they can call, not how to behave.
func tools(std map[string]string) (string, error) {
	o := std["orientation"]
	i := strings.Index(o, "## Talking to the Hive")
	if i < 0 {
		return "", fmt.Errorf("the standard orientation has no \"## Talking to the Hive\" section")
	}
	var keep []string
	for _, l := range strings.Split(o[i:], "\n") {
		if strings.HasPrefix(l, "## ") || strings.HasPrefix(l, "Everything goes through") || strings.HasPrefix(l, "- `hive agent") {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n") + "\n", nil
}

// drop removes the paragraphs that start with any of prefixes, and fails if
// one isn't there, so a reworded prompt can't silently leave an arm unchanged.
func drop(text string, prefixes ...string) (string, error) {
	paras := strings.Split(text, "\n\n")
	for _, p := range prefixes {
		n := len(paras)
		paras = slices.DeleteFunc(paras, func(s string) bool { return strings.HasPrefix(strings.TrimSpace(s), p) })
		if len(paras) == n {
			return "", fmt.Errorf("no paragraph starts with %q", p)
		}
	}
	return strings.Join(paras, "\n\n"), nil
}

const philosophy = `# The Hive

You are one agent in a hive: many agents, working at once on one person's projects, on their machine. A small, hard core holds you: your sandbox, the journal, the merge path, the safety ceiling. Everything else is soft and yours to improve.

- Simplicity. Do the least that does the job; a light core, the rest in modules.
- Talk. You can find, message and spawn other agents. A hive works because its members tell each other what they find.
- Measure. A change is kept only if it measurably does not make things worse; a worse one is reverted, and that is fine.
- Remember. What you learn goes in the knowledge base for whoever comes next.
- Ask before acting outside. Inside the Hive, act. Past your reach, ask a warden; past theirs, a tribunal; money, credentials and anything in the person's name are the person's.
- Improve the Hive itself. You are part of a system that improves itself recursively; leave it better than you found it.

` + "`hive agent help`" + ` lists what you can do.
`

const comms = `## Talk, a lot

This arm of the Hive talks. Before you start, run ` + "`hive agent status`" + ` and message every run whose work touches yours: say what you are about to do and ask what they know. Each time you find something (a cause, a flake, a file others edit, a test that lies), message the runs it helps at once, by run id. When you are unsure, ask a peer before you guess. When you finish, tell the lead and each run you worked beside what you did and what is left. Reply to every message you get.
`

const quiet = `## Work alone

Work alone. Don't message other agents, don't read ` + "`hive agent status`" + ` for peers, and don't file briefs or spawn helpers. Message the lead only if you are blocked and cannot finish.
`

// Variants are the arms, from the standard prompts to almost nothing.
var Variants = []Variant{
	{"standard", "the Hive's own agents module, unchanged: the control", func(std map[string]string, _ string) (map[string]string, error) {
		return std, nil
	}},
	{"chatty", "the standard prompts, plus: talk to every related run, at every step", func(std map[string]string, _ string) (map[string]string, error) {
		out := clone(std)
		out["orientation"] = std["orientation"] + "\n" + comms
		return out, nil
	}},
	{"quiet", "the standard prompts with the talk-to-each-other paragraphs removed, plus: work alone", func(std map[string]string, _ string) (map[string]string, error) {
		out := clone(std)
		o, err := drop(std["orientation"], "Talk to each other like a hive.")
		if err != nil {
			return nil, err
		}
		w, err := drop(std["worker"], "Others are working beside you.")
		if err != nil {
			return nil, err
		}
		out["orientation"], out["worker"] = o+"\n"+quiet, w
		return out, nil
	}},
	{"essay", "the person's essay as the whole orientation, and one line per role", func(_ map[string]string, essay string) (map[string]string, error) {
		return lines(essay), nil
	}},
	{"essay-tools", "the essay, the bare list of hive agent commands, and one line per role", func(std map[string]string, essay string) (map[string]string, error) {
		t, err := tools(std)
		if err != nil {
			return nil, err
		}
		return lines(essay + "\n\n" + t), nil
	}},
	{"philosophy", "a page of principles, no procedure, and one line per role", func(_ map[string]string, _ string) (map[string]string, error) {
		return lines(philosophy), nil
	}},
	{"vague", "one sentence, and one line per role", func(_ map[string]string, _ string) (map[string]string, error) {
		return lines("You are an agent in a team of agents working on a software project. Do good work.\n"), nil
	}},
}

func clone(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Find returns the named variant.
func Find(name string) (Variant, bool) {
	for _, v := range Variants {
		if v.Name == name {
			return v, true
		}
	}
	return Variant{}, false
}

// Pick resolves a comma list of names; "all" (or empty) is every variant.
func Pick(list string) ([]Variant, error) {
	if list == "" || list == "all" || list == "1" {
		return Variants, nil
	}
	var out []Variant
	for _, n := range strings.Split(list, ",") {
		v, ok := Find(strings.TrimSpace(n))
		if !ok {
			return nil, fmt.Errorf("no soft-layer variant %q", n)
		}
		out = append(out, v)
	}
	return out, nil
}

// Standard is the shipped agents module's prompts, by role.
func Standard() (map[string]string, error) {
	out := map[string]string{}
	for _, r := range Roles {
		raw, err := fs.ReadFile(modules.FS, "agents/"+r+".md")
		if err != nil {
			return nil, err
		}
		out[r] = string(raw)
	}
	return out, nil
}

// Build returns the variant's prompt files, every role present.
func (v Variant) Build(essay string) (map[string]string, error) {
	std, err := Standard()
	if err != nil {
		return nil, err
	}
	files, err := v.Files(std, essay)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", v.Name, err)
	}
	for _, r := range Roles {
		if _, ok := files[r]; !ok {
			return nil, fmt.Errorf("%s: no %s prompt", v.Name, r)
		}
	}
	return files, nil
}

// Install writes the variant into a workspace's modules folder (core.Workspace.Modules()),
// over the agents module, with a source record that is not "builtin" so the
// daemon's default install never puts the standard prompts back. Every role
// file is written, empty or not, since a missing one falls back to the default.
func (v Variant) Install(modulesDir, essay string) error {
	files, err := v.Build(essay)
	if err != nil {
		return err
	}
	dir := filepath.Join(modulesDir, "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	manifest, _ := fs.ReadFile(modules.FS, "agents/module.json")
	if err := os.WriteFile(filepath.Join(dir, "module.json"), manifest, 0o644); err != nil {
		return err
	}
	for r, text := range files {
		if err := os.WriteFile(filepath.Join(dir, r+".md"), []byte(text), 0o644); err != nil {
			return err
		}
	}
	src, _ := json.Marshal(map[string]any{"source": "soft-layer:" + v.Name, "projects": []string{"*"}})
	return os.WriteFile(filepath.Join(dir, ".hive-source.json"), src, 0o644)
}
