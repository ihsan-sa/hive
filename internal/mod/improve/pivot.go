package improve

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// A pivot is a change of direction any agent can propose, not only a
// step-back: a short case with the evidence for it. It is filed as a fix,
// first in line, and its worker weighs the case before it acts, so it is
// weighed and acted on in one run. Nothing bounds what it may throw away;
// the lead and the person hear it at once and turn it down with withdraw.
// The person, 2026-10-27: "we should be ready to do radical changes and
// pivots ... We cant set any bounds".

// pivotCase is the longest case a pivot carries: a case, not an essay.
const pivotCase = 2000

func (m *Module) opPivot(r core.Run, args json.RawMessage) (any, error) {
	var a struct {
		Case     string `json:"case"`
		Evidence string `json:"evidence"`
		Project  string `json:"project"`
	}
	if err := core.Bind(args, &a); err != nil {
		return nil, err
	}
	a.Case, a.Evidence = strings.TrimSpace(a.Case), strings.TrimSpace(a.Evidence)
	switch {
	case a.Case == "":
		return nil, errors.New(`say the pivot with --case "...": what to stop, throw away or change, and what instead`)
	case a.Evidence == "":
		return nil, errors.New(`a pivot carries its evidence (--evidence "..."): what in the record, the scores or the code shows the case, by task, run, PR or file`)
	case len([]rune(a.Case)) > pivotCase:
		return nil, fmt.Errorf("a pivot's case is short (at most %d characters, this is %d): the evidence carries the detail", pivotCase, len([]rune(a.Case)))
	}
	if a.Project == "" {
		a.Project = r.Project
	}
	if a.Project == "" {
		return nil, errors.New("say which project the pivot is on with --project")
	}
	if err := m.C.Runs.Walled(r, a.Project); err != nil {
		return nil, err
	}
	head, _, _ := strings.Cut(a.Case, "\n")
	who := r.ID
	if r.Role != "" {
		who += " (" + r.Role + ")"
	}
	known := fmt.Sprintf(`A pivot, proposed by %s on %s.

The case:

%s

The evidence:

%s

The person, 2026-10-27: "we should be ready to do radical changes and pivots where current stuff may be rendered completely obsolete or the hive could change fully its architecture. Thats the point of the hive. We cant set any bounds." So weigh it on the case and its evidence, not on how much it changes or throws away: no part of the project is off limits, the core and the Hive's own loop included (a pivot that grows the core says why in docs/core.md, as any change there does). Only .hive/goals.md stays the person's: a pivot of the goals themselves goes to them (hive agent message --to person).`,
		who, time.Now().UTC().Format("2006-01-02 15:04Z"), a.Case, a.Evidence)
	t, err := m.Work.File(work.Brief{
		Project: a.Project,
		Title:   "Pivot: " + core.Clip(strings.TrimSpace(head), 120),
		From:    r.ID,
		Aim:     work.AimFix,
		Why:     "a pivot proposed by " + r.ID + ": " + core.Clip(a.Case, 200),
		Known:   known,
		Done:    `First weigh the case, fast: check its evidence against the record, the scores and the code. If it doesn't hold, change nothing and finish done saying why, and tell the proposer. If it holds, make the pivot in full on this branch: delete or replace what it makes obsolete (code, modules, prompts, tests, docs) rather than keeping it beside the new way, and tell the lead which waiting briefs it makes obsolete so they are withdrawn. A pivot too big for one change lands its first cut here and files the rest as briefs.`,
		Now:     true,
	})
	if err != nil {
		return nil, err
	}
	m.C.J.MustAppend("pivot.proposed", map[string]string{"task": t.ID, "project": a.Project, "run": r.ID, "case": a.Case, "evidence": core.Clip(a.Evidence, 4000)})
	m.Chat.EnsureProject(a.Project)
	m.Chat.Notify("project:"+a.Project, "", fmt.Sprintf("%s proposes a pivot on %s, filed first in line as task %s:\n\n%s\n\nEvidence: %s\n\nIts worker weighs the case before acting. To turn it down: hive agent withdraw --task %s --why \"...\"", who, a.Project, t.ID, core.Clip(a.Case, 1000), core.Clip(a.Evidence, 600), t.ID))
	return map[string]string{"task": t.ID, "note": "filed first in line as a fix; its worker weighs your case before acting, and the project's lead and the person have heard it. Add to it with amend, or take it back with withdraw, until a worker starts"}, nil
}
