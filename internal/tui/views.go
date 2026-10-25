package tui

import (
	"cmp"
	"fmt"
	"github.com/ihsan-sa/hive-standalone-build/internal/text"
	"maps"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/improve"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/report"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/system"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

func visibleWidth(s string) int { return ansi.StringWidth(s) }

// ---- dashboard -------------------------------------------------------------

type row struct {
	key    string
	depth  int
	text   string
	expand bool // has children
	def    bool // open by default
	open   *detail
	jump   int // section to jump to, -1 none
}

func (m *model) nodeOpen(key string, def bool) bool {
	if v, ok := m.open[key]; ok {
		return v
	}
	return def
}

func (m *model) dashRows() []row {
	var rows []row
	add := func(r row) bool {
		if r.jump == 0 {
			r.jump = -1
		}
		rows = append(rows, r)
		return r.expand && m.nodeOpen(r.key, r.def)
	}
	m.statRows(add)
	for _, p := range m.st.Projects {
		best := "no score yet"
		if p.Best != nil {
			best = "best " + fmtNum(*p.Best)
		}
		if !add(row{key: "proj:" + p.Name, text: fmt.Sprintf("%s  %s  %s  %s", stTitle.Render(p.Name), stDim.Render(p.Repo), best, spark(p.Scores)), expand: true, def: true}) {
			continue
		}
		if p.Hold != nil {
			add(row{key: "hold:" + p.Name, depth: 1, text: stWarn.Render(text.Visible(p.Hold.Text()))})
		}
		if add(row{key: "goals:" + p.Name, depth: 1, text: "goals", expand: true}) {
			for _, l := range nonEmpty(strings.Split(p.Goals, "\n")) {
				add(row{key: "goal:" + p.Name + l, depth: 2, text: stDim.Render(l)})
			}
		}
		var live, done []work.Task
		for _, t := range p.Tasks {
			if t.Terminal() {
				done = append(done, t)
			} else {
				live = append(live, t)
			}
		}
		if add(row{key: "live:" + p.Name, depth: 1, text: fmt.Sprintf("work in flight (%d)", len(live)), expand: len(live) > 0, def: true}) {
			for _, t := range live {
				d := detail{kind: "task", id: t.ID, title: core.Clip(t.Label(), 40)}
				if add(row{key: "task:" + t.ID, depth: 2, text: m.taskLine(t), expand: true, open: &d}) {
					if r, ok := m.run(t.Worker); ok {
						rd := detail{kind: "run", id: r.ID, title: runLabel(r)}
						add(row{key: "run:" + r.ID, depth: 3, text: m.runLine(r), open: &rd})
					}
					if r, ok := m.run(t.Reviewer); ok && !r.Terminal() {
						rd := detail{kind: "run", id: r.ID, title: runLabel(r)}
						add(row{key: "run:" + r.ID, depth: 3, text: m.runLine(r), open: &rd})
					}
					if t.PR.URL != "" {
						add(row{key: "pr:" + t.ID, depth: 3, text: stDim.Render(fmt.Sprintf("PR #%d %s", t.PR.Number, t.PR.URL))})
					}
				}
			}
		}
		if add(row{key: "score:" + p.Name, depth: 1, text: "score over the last merges", expand: len(p.Scores) > 0}) {
			sc := p.Scores
			if len(sc) > 8 {
				sc = sc[len(sc)-8:]
			}
			for i := len(sc) - 1; i >= 0; i-- {
				x := sc[i]
				v := "-"
				if x.Score != nil {
					v = fmtNum(*x.Score)
				}
				mark := stGood.Render("kept")
				if x.NoResult {
					mark = stDim.Render("can't run")
				} else if !x.Kept {
					mark = stBad.Render("not kept")
				}
				add(row{key: "sc:" + x.SHA + x.At.String(), depth: 2, text: fmt.Sprintf("%s  %s  %s  %s  %s", x.At.Local().Format("Mon 15:04"), core.Short(x.SHA), v, mark, stDim.Render(core.Clip(x.Note, 80)))})
			}
		}
		if len(p.Flakes) > 0 && add(row{key: "flakes:" + p.Name, depth: 1, text: fmt.Sprintf("known flakes, quarantined (%d)", len(p.Flakes)), expand: true}) {
			for _, f := range p.Flakes {
				fix := stWarn.Render("no fix task yet")
				var open *detail
				if f.Task != "" {
					fix = "fix: " + f.Task + " " + stDim.Render(f.TaskStage)
					open = &detail{kind: "task", id: f.Task, title: core.Clip(text.Visible(f.Test), 40)}
				}
				add(row{key: "flake:" + p.Name + f.Test, depth: 2, text: fmt.Sprintf("%s  %s  %s", text.Visible(f.Test), fix, stDim.Render(text.Visible(core.Clip(f.Why, 100)))), open: open})
			}
		}
		if add(row{key: "done:" + p.Name, depth: 1, text: fmt.Sprintf("recent work (%d)", len(done)), expand: len(done) > 0}) {
			for _, t := range done {
				d := detail{kind: "task", id: t.ID, title: core.Clip(t.Label(), 40)}
				add(row{key: "task:" + t.ID, depth: 2, text: m.taskLine(t), open: &d})
			}
		}
	}
	var live []core.Run
	for _, r := range m.st.Runs {
		if !r.Terminal() {
			live = append(live, r)
		}
	}
	if add(row{key: "agents", text: fmt.Sprintf("agents (%d live)", len(live)), expand: len(live) > 0, def: true}) {
		for _, r := range live {
			d := detail{kind: "run", id: r.ID, title: runLabel(r)}
			add(row{key: "run:" + r.ID, depth: 1, text: m.runLine(r), open: &d})
		}
	}
	ck := m.st.Checks
	if add(row{key: "checks", text: fmt.Sprintf("checks (%d running, %d waiting)", len(ck.Running), len(ck.Waiting)), expand: len(ck.Running)+len(ck.Waiting) > 0, def: true}) {
		for _, u := range ck.Running {
			add(row{key: fmt.Sprintf("check:%s:%s:%s", u.Project, u.SHA, u.Kind), depth: 1, text: fmt.Sprintf("%s  %s %s  %s  %s", stGood.Render("running"), u.Project, core.Short(u.SHA), u.Kind, stDim.Render(ago(u.Since)))})
		}
		for i, u := range ck.Waiting {
			add(row{key: fmt.Sprintf("check:%s:%s:%s:w", u.Project, u.SHA, u.Kind), depth: 1, text: fmt.Sprintf("%s  %s %s  %s  %s", stWarn.Render(fmt.Sprintf("waiting #%d", i+1)), u.Project, core.Short(u.SHA), u.Kind, stDim.Render(ago(u.Since)))})
		}
	}
	if add(row{key: "sweeps", text: fmt.Sprintf("improvement sweeps (%d)", len(m.st.Sweeps)), expand: len(m.st.Sweeps) > 0}) {
		for _, s := range m.st.Sweeps {
			text := fmt.Sprintf("%s  %s  asks filed: %d", s.Started.Local().Format("Mon 15:04"), s.Status, len(s.Asks))
			d := detail{kind: "text", id: s.ID, title: "sweep", text: sweepText(s.Report, s.Asks)}
			add(row{key: "sweep:" + s.ID, depth: 1, text: text, open: &d})
		}
	}
	if add(row{key: "plans", text: "plan usage", expand: len(m.st.Plans) > 0, def: true}) {
		for _, p := range m.st.Plans {
			status := text.Visible(p.Limit.Status)
			if status == "rejected" {
				status = stBad.Render("limited") + ", resets " + p.Limit.ResetsAt.Local().Format("Mon 15:04")
			}
			line := fmt.Sprintf("%s  %s", text.Visible(p.Key()), status)
			for _, k := range sortedKeys(p.Limit.Windows) {
				wnd := p.Limit.Windows[k]
				line += fmt.Sprintf("  %s %.0f%%", text.Visible(k), wnd.Utilization*100)
			}
			add(row{key: "plan:" + p.Key(), depth: 1, text: line})
		}
	}
	if len(m.st.Down) > 0 && add(row{key: "down", text: fmt.Sprintf("models that can't answer (%d)", len(m.st.Down)), expand: true, def: true}) {
		for _, d := range m.st.Down {
			line := stBad.Render(text.Visible(d.Pick.String())) + "  tried again " + d.Retry.Local().Format("Mon 15:04") + "  " + text.Visible(core.Clip(d.Text, 160))
			dt := detail{kind: "text", id: d.Pick.String(), title: d.Pick.String() + " can't answer", text: text.Visible(d.Line()) + "\n\n" + text.Visible(d.Fix(m.st.Config))}
			add(row{key: "down:" + d.Pick.String(), depth: 1, text: line, open: &dt})
		}
	}
	if add(row{key: "logins", text: "logins", expand: len(m.st.Logins) > 0, def: true}) {
		for _, l := range m.st.Logins {
			add(row{key: "login:" + l.Login, depth: 1, text: loginLine(l)})
		}
	}
	add(row{key: "waiting", text: fmt.Sprintf("waiting on you (%d)", len(m.st.Asks)), jump: secWaiting})
	if add(row{key: "told", text: toldHead(m.st.Told, time.Now()), expand: len(m.st.Told) > 0}) {
		for _, a := range m.st.Told {
			d := toldDetail(a)
			title := text.Visible(core.Clip(a.Title, 120))
			if strings.HasPrefix(a.ID, "spend-") { // money is the person's: the notice stands out
				title = stWarn.Render(title)
			}
			add(row{key: "told:" + a.ID, depth: 1, text: a.Asked.Local().Format("Mon 15:04") + "  " + title, open: &d})
		}
	}
	if add(row{key: "requests", text: fmt.Sprintf("requests past the sandbox (%d)", len(m.st.Requests)), expand: len(m.st.Requests) > 0}) {
		for _, r := range m.st.Requests {
			out := r.Outcome
			if out == "" {
				out = r.State // a warden or panel may still be weighing it
			}
			if out == "asked" {
				out = "waiting on you"
			}
			add(row{key: "req:" + r.ID, depth: 1, text: fmt.Sprintf("%s  %s  %s", r.Created.Local().Format("Mon 15:04"), core.Clip(r.Want, 60), stDim.Render(out))})
		}
	}
	if add(row{key: "alerts", text: fmt.Sprintf("alerts (%d)", len(m.st.Alerts)), expand: len(m.st.Alerts) > 0}) {
		for i := len(m.st.Alerts) - 1; i >= 0; i-- {
			a := m.st.Alerts[i]
			n := ""
			if a.Count > 1 {
				n = fmt.Sprintf(" (x%d)", a.Count)
			}
			add(row{key: fmt.Sprintf("alert:%d", i), depth: 1, text: fmt.Sprintf("%s  %s  %s", a.At.Local().Format("Mon 15:04"), stWarn.Render(a.Kind), a.Text+n)})
		}
	}
	if add(row{key: "jobs", text: "schedules", expand: len(m.st.Jobs) > 0}) {
		for _, j := range m.st.Jobs {
			add(row{key: "job:" + j.Name, depth: 1, text: fmt.Sprintf("%s  %s  last %s  %s", j.Name, j.Schedule, ago(j.Last), stDim.Render(core.Clip(j.Result, 60)))})
		}
	}
	return rows
}

func sweepText(report string, asks []string) string {
	s := report
	if s == "" {
		s = "(no report yet)"
	}
	if len(asks) > 0 {
		s += "\n\nAsks filed: " + strings.Join(asks, ", ")
	}
	return s
}

func nonEmpty(lines []string) []string {
	var out []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func (m *model) run(id string) (core.Run, bool) {
	for _, r := range m.st.Runs {
		if r.ID == id {
			return r, true
		}
	}
	return core.Run{}, false
}

func (m *model) taskLine(t work.Task) string {
	stage := t.Stage
	switch t.Stage {
	case work.StageMerged, work.StageDone:
		stage = stGood.Render(stage)
	case work.StageBlocked, work.StageStopped:
		stage = stBad.Render(stage)
	case work.StagePerson, work.StageQueued, work.StageIssued:
		stage = stWarn.Render(stage)
	}
	progress := ""
	if r, ok := m.run(t.Worker); ok && r.Progress != "" && !t.Terminal() && !t.Queued() {
		progress = stDim.Render("  “" + core.Clip(r.Progress, 60) + "” " + ago(r.ProgressAt))
	}
	if (t.Queued() || t.Stage == work.StageMerging) && t.Reason != "" {
		progress = stDim.Render("  " + core.Clip(t.Reason, 70))
	}
	if t.Stage == work.StageTesting && slices.ContainsFunc(m.st.Checks.Waiting, func(u core.SlotUse) bool { return u.Project == t.Project && u.SHA == t.SHA }) {
		progress = stDim.Render("  waiting for a check slot")
	}
	pr := ""
	if t.PR.Number > 0 {
		pr = fmt.Sprintf("  #%d", t.PR.Number)
	}
	return fmt.Sprintf("[%s] %s%s%s  %s", stage, t.Label(), pr, progress, stDim.Render(ago(t.Updated)))
}

func (m *model) runLine(r core.Run) string {
	status := r.Status
	switch r.Status {
	case core.StatusRunning:
		status = stGood.Render(status)
	case core.StatusParked, core.StatusQuiet, core.StatusQueued:
		status = stWarn.Render(status)
	}
	extra := r.Progress
	if r.Reason != "" && r.Status != core.StatusRunning {
		extra = r.Reason
	}
	return fmt.Sprintf("%s  %s  %s  turn %d  $%.2f  %dk ctx  %s  %s", runLabel(r), status, text.Visible(core.HarnessName(r.Harness)+":"+r.Model), r.Turn, r.Cost, r.Context/1000, stDim.Render(core.Clip(extra, 70)), stDim.Render(ago(r.Updated)))
}

// spark draws the last scores as a one-line chart.
func spark(ms []improve.Measurement) string {
	var vals []float64
	for _, x := range ms {
		if x.Score != nil {
			vals = append(vals, *x.Score)
		}
	}
	if len(vals) < 2 {
		return ""
	}
	if len(vals) > 16 {
		vals = vals[len(vals)-16:]
	}
	lo, hi := vals[0], vals[0]
	for _, v := range vals {
		lo, hi = min(lo, v), max(hi, v)
	}
	bars := []rune("▁▂▃▄▅▆▇█")
	var b strings.Builder
	for _, v := range vals {
		i := 0
		if hi > lo {
			i = int((v - lo) / (hi - lo) * float64(len(bars)-1))
		}
		b.WriteRune(bars[i])
	}
	return stAccent.Render(b.String())
}

func (m *model) keyTree(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.dashRows()
	if len(rows) == 0 {
		return m, nil
	}
	r := rows[m.treeAt(rows)]
	defer func() { m.dashKey = m.treeKey(m.dashSel) }()
	switch k.String() {
	case "up", "k":
		if m.dashSel > 0 {
			m.dashSel--
		}
	case "down", "j":
		if m.dashSel < len(rows)-1 {
			m.dashSel++
		}
	case " ":
		if r.expand {
			m.open[r.key] = !m.nodeOpen(r.key, r.def)
		}
	case "right", "l", "enter":
		switch {
		case r.jump >= 0:
			m.section = r.jump
		case r.expand && !m.nodeOpen(r.key, r.def):
			m.open[r.key] = true
		case r.open != nil:
			return m, m.push(*r.open)
		case r.expand && k.String() == "enter":
			m.open[r.key] = false
		}
	case "left", "h", "esc":
		if r.expand && m.nodeOpen(r.key, r.def) {
			m.open[r.key] = false
			return m, nil
		}
		for i := m.dashSel - 1; i >= 0; i-- { // go to the parent
			if rows[i].depth < r.depth {
				m.dashSel = i
				break
			}
		}
	}
	return m, nil
}

// treeAt finds the status cursor's row by its key, as sideAt does: work
// that starts or ends above it doesn't move it. A run shows both under its
// task and under agents, so the nearer of the two is the cursor's.
func (m *model) treeAt(rows []row) int {
	best := -1
	for i, r := range rows {
		if m.dashKey != "" && r.key == m.dashKey && (best < 0 || abs(i-m.dashSel) < abs(best-m.dashSel)) {
			best = i
		}
	}
	if best < 0 {
		best = m.dashSel
	}
	m.dashSel = max(0, min(best, len(rows)-1))
	return m.dashSel
}

func abs(n int) int { return max(n, -n) }

// treeKey is the key of status row i, or "".
func (m *model) treeKey(i int) string {
	if rows := m.dashRows(); i < len(rows) {
		return rows[i].key
	}
	return ""
}

func (m *model) viewTree() []string {
	rows := m.dashRows()
	m.treeAt(rows)
	var lines []string
	for i, r := range rows {
		arrow := "  "
		if r.expand {
			arrow = "▸ "
			if m.nodeOpen(r.key, r.def) {
				arrow = "▾ "
			}
		}
		line := strings.Repeat("  ", r.depth) + arrow + r.text
		if i == m.dashSel && m.focus == 1 {
			line = stSel.Render(clip(line, m.w))
		}
		lines = append(lines, line)
		if r.depth == 0 && i+1 < len(rows) && rows[i+1].depth == 0 {
			lines = append(lines, "")
		}
	}
	sel := 0
	for i, r := range rows {
		if i == m.dashSel {
			break
		}
		sel++
		if r.depth == 0 && i+1 < len(rows) && rows[i+1].depth == 0 {
			sel++
		}
	}
	if len(m.st.Held) > 0 { // turns wait for a reason, not for nothing
		lines = append([]string{stDim.Render("New turns are " + strings.Join(m.st.Held, "; ") + "."), ""}, lines...)
		sel += 2
	}
	if rel := releaseText(m.st); m.st.Behind > 0 {
		lines = append([]string{stWarn.Render(rel), ""}, lines...)
		sel += 2
	} else if rel != "" {
		lines = append([]string{stDim.Render(rel), ""}, lines...)
		sel += 2
	}
	if d := m.st.Disk; !d.At.IsZero() {
		style := stDim
		if d.Low() {
			style = stWarn
		}
		lines = append([]string{style.Render("Disk: " + d.String() + "."), ""}, lines...)
		sel += 2
	}
	if m.st.Offline != "" {
		lines = append([]string{stWarn.Render(strings.ToUpper(m.st.Offline[:1]) + m.st.Offline[1:] + "; the work picks up again on its own."), ""}, lines...)
		sel += 2
	}
	if m.st.SandboxErr != "" {
		lines = append([]string{stBad.Render("Agents can't start: " + core.Clip(m.st.SandboxErr, 200)), ""}, lines...)
		sel += 2
	}
	return window(lines, sel, m.bodyHeight())
}

// releaseText says which release the Hive runs and how far behind the newest
// it is, as the last look for an update found; "" before any look.
func releaseText(st api.State) string {
	switch {
	case st.Newest == "":
		return ""
	case st.Behind == 0:
		return "On " + st.Version + ", the newest release"
	case st.Behind == 1:
		return "On " + st.Version + ", 1 release behind " + st.Newest
	}
	return fmt.Sprintf("On %s, %d releases behind %s", st.Version, st.Behind, st.Newest)
}

// ---- details ---------------------------------------------------------------

func (m *model) keyDetailAction(d *detail, key string) (tea.Model, tea.Cmd) {
	switch d.kind {
	case "task":
		switch key {
		case "s":
			return m, m.post("/api/task/stop", map[string]string{"id": d.id}, "stopping the task")
		case "R":
			return m, m.post("/api/task/retry", map[string]string{"id": d.id}, "handed to a fresh worker")
		case "w":
			if t, ok := m.tasks[d.id]; ok && t.Worker != "" {
				r, _ := m.run(t.Worker)
				return m, m.push(detail{kind: "run", id: t.Worker, title: runLabel(r)})
			}
		}
	case "run":
		switch key {
		case "s":
			return m, m.post("/api/run/stop", map[string]string{"id": d.id}, "stopping the agent")
		case "c":
			return m, tea.Batch(m.openChat("agent:"+d.id), m.post("/api/chat/agent", map[string]string{"run": d.id}, ""))
		}
	case "module":
		mod, ok := m.module(d.id)
		if !ok {
			return m, nil
		}
		projects := m.projectNames()
		switch key {
		case "down", "j":
			if d.sel < len(projects)-1 {
				d.sel++
			}
		case "up", "k":
			if d.sel > 0 {
				d.sel--
			}
		case " ", "enter":
			if d.sel < len(projects) {
				p := projects[d.sel]
				allow := !contains(mod.Projects, p)
				return m, m.post("/api/module/access", map[string]any{"module": mod.Name, "project": p, "allow": allow}, "access updated")
			}
		case "r":
			return m, m.post("/api/module/review", map[string]any{"module": mod.Name}, "its code will be read again")
		}
	}
	return m, nil
}

func (m *model) viewDetail(d *detail) []string {
	w := m.mainW() - 3
	var lines []string
	kv := func(k, v string) { lines = append(lines, stDim.Render(fmt.Sprintf("%-10s", k))+" "+v) }
	switch d.kind {
	case "run":
		rd, ok := m.runs[d.id]
		if !ok {
			return []string{"", "  loading…"}
		}
		r := rd.Run
		kv("agent", runLabel(r)+"  "+r.ID)
		kv("status", r.Status+"  "+r.Reason)
		kv("project", r.Project+"  task "+r.Task)
		kv("model", r.Harness+" "+r.Model)
		kv("turns", fmt.Sprintf("%d  cost $%.2f  started %s", r.Turn, r.Cost, r.Created.Local().Format("Mon 15:04")))
		kv("context", fmt.Sprintf("%dk tokens", r.Context/1000))
		if r.Progress != "" {
			kv("progress", r.Progress+"  "+ago(r.ProgressAt))
		}
		lines = append(lines, "", stTitle.Render("brief"))
		lines = append(lines, indent(wrap(core.Clip(r.Brief, 1200), w-2), "  ")...)
		lines = append(lines, "", stTitle.Render("steps"))
		for _, s := range rd.Steps {
			tag := fmt.Sprintf("t%d ", s.Turn)
			if s.Kind == "tool" {
				lines = append(lines, stDim.Render(tag)+stAccent.Render(s.Tool)+" "+oneLine(s.Text))
			} else {
				for i, l := range wrap(s.Text, w-4) {
					if i == 0 {
						lines = append(lines, stDim.Render(tag)+l)
					} else {
						lines = append(lines, "   "+l)
					}
				}
			}
		}
		lines = append(lines, "", stDim.Render("s stop this agent · c open its chat"))
	case "task":
		t, ok := m.tasks[d.id]
		if !ok {
			return []string{"", "  loading…"}
		}
		kv("task", t.Label())
		kv("stage", t.Stage+"  "+core.Clip(t.Reason, 200))
		kv("project", t.Project)
		if t.Issue.Number > 0 {
			kv("issue", fmt.Sprintf("#%d %s", t.Issue.Number, t.Issue.URL))
		}
		if t.PR.Number > 0 {
			kv("PR", fmt.Sprintf("#%d %s", t.PR.Number, t.PR.URL))
		}
		kv("commit", core.Short(t.SHA)+"  branch "+t.Branch)
		kv("rounds", fmt.Sprintf("%d fix rounds", t.Round))
		if t.MergeSHA != "" {
			kv("merged", core.Short(t.MergeSHA))
		}
		if g := t.Gate; g != nil {
			kv("gate", work.GateWord(g.Outcome)+" on "+core.Short(g.Head)+" vs main "+core.Short(g.Main)+": "+core.Clip(g.Reason, 160))
			if g.Report != "" {
				kv("report", g.Report)
			}
		}
		lines = append(lines, "", stTitle.Render("brief"))
		lines = append(lines, indent(wrap("Done looks like: "+t.Done, w-2), "  ")...)
		if t.Known != "" {
			lines = append(lines, indent(wrap("Known: "+core.Clip(t.Known, 800), w-2), "  ")...)
		}
		if t.Findings != "" {
			lines = append(lines, "", stTitle.Render("last findings"))
			lines = append(lines, indent(wrap(core.Clip(t.Findings, 1500), w-2), "  ")...)
		}
		lines = append(lines, "", stTitle.Render("timeline"))
		for _, e := range t.Log {
			lines = append(lines, stDim.Render(e.At.Local().Format("Mon 15:04"))+"  "+oneLine(e.Text))
		}
		lines = append(lines, "", stDim.Render("w worker's log · s stop · R hand to a fresh worker"))
	case "module":
		mod, ok := m.module(d.id)
		if !ok {
			return []string{"", "  not found"}
		}
		kv("module", mod.Name)
		kv("source", mod.Source)
		kv("release", mod.Release)
		lines = append(lines, "")
		lines = append(lines, indent(wrap(mod.Description, w-2), "  ")...)
		lines = append(lines, "", stTitle.Render("projects that may use it"))
		all := contains(mod.Projects, "*")
		top := len(lines)
		for i, p := range m.projectNames() {
			mark := "[ ]"
			if all || contains(mod.Projects, p) {
				mark = "[x]"
			}
			line := fmt.Sprintf("  %s %s", mark, p)
			if p == "*" {
				line = fmt.Sprintf("  %s every project", mark)
			}
			if i == d.sel {
				line = stSel.Render(line)
			}
			lines = append(lines, line)
		}
		lines = append(lines, "", stDim.Render("space grant or remove · r read its code again"))
		return window(lines, top+d.sel, m.bodyHeight())
	case "diff":
		lines = append(lines, diffView(d.text, w)...)
	default:
		lines = append(lines, wrap(d.text, w)...)
	}
	d.total = len(lines)
	d.scroll = max(0, min(d.scroll, len(lines)-m.bodyHeight())) // the last page stays full
	return lines[d.scroll:]
}

func (m *model) projectNames() []string {
	names := []string{"*"}
	for _, p := range m.st.Projects {
		names = append(names, p.Name)
	}
	return names
}

func (m *model) module(name string) (api.ModuleView, bool) {
	for _, x := range m.st.Modules {
		if x.Name == name {
			return x, true
		}
	}
	return api.ModuleView{}, false
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// ---- waiting on you --------------------------------------------------------

func (m *model) keyWaiting(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	asks := m.st.Asks
	if m.waitSel >= len(asks) {
		m.waitSel = max(0, len(asks)-1)
	}
	switch k.String() {
	case "up", "k":
		if m.waitSel > 0 {
			m.waitSel--
			m.optSel = 0
		}
	case "down", "j":
		if m.waitSel < len(asks)-1 {
			m.waitSel++
			m.optSel = 0
		}
	case "left", "h":
		if m.optSel > 0 {
			m.optSel--
		}
	case "right", "l":
		if m.waitSel < len(asks) && m.optSel < len(asks[m.waitSel].Options)-1 {
			m.optSel++
		}
	case "d":
		if m.waitSel < len(asks) && asks[m.waitSel].Diff != "" {
			a := asks[m.waitSel]
			return m, m.push(detail{kind: "diff", id: a.ID, title: "what it changes", text: a.Diff})
		}
	case "enter":
		if m.waitSel < len(asks) {
			a := asks[m.waitSel]
			opt := a.Options[min(m.optSel, len(a.Options)-1)]
			m.optSel = 0
			return m, m.post("/api/answer", map[string]string{"id": a.ID, "answer": opt}, "answered: "+opt)
		}
	}
	return m, nil
}

func (m *model) viewWaiting() []string {
	if len(m.st.Asks) == 0 {
		return []string{"", "  Nothing is waiting on you."}
	}
	w := m.mainW() - 5
	var lines []string
	sel, end := 0, 0
	for i, a := range m.st.Asks {
		gut := "  "
		if i == m.waitSel {
			gut, sel = stTitle.Render("▌")+" ", len(lines)
		}
		from := core.Clip(a.From, 20)
		if a.Project != "" {
			from = a.Project + " › " + from
		}
		age := ago(a.Asked)
		top := stTitle.Render(from) + "  " + stWarn.Render(a.Kind)
		ls := []string{pad(clip(top, w-len(age)-1), w-len(age)) + stDim.Render(age)}
		ls = append(ls, wrap(a.Title, w)...)
		if i == m.waitSel {
			if a.Detail != "" {
				for _, l := range wrap(a.Detail, w) {
					ls = append(ls, stDim.Render(l))
				}
			}
			if a.Diff != "" { // as much of the diff as leaves the options on screen
				diff := diffView(a.Diff, w)
				n := max(3, m.bodyHeight()-len(ls)-2)
				if len(diff) > n {
					diff = diff[:n-1]
				}
				ls = append(append(ls, diff...), stDim.Render("… d the whole diff"))
			}
			var opts []string
			for j, o := range a.Options {
				if j == m.optSel && m.focus == 1 {
					o = stSel.Render(o)
				}
				opts = append(opts, o)
			}
			ls = append(ls, optionRows(opts, stTitle.Render("←→")+" "+stDim.Render("pick")+"   "+stTitle.Render("enter")+" "+stDim.Render("answer"), w)...)
		}
		lines = append(append(lines, indent(ls, gut)...), "")
		if i == m.waitSel {
			end = len(lines) - 2
		}
	}
	return span(lines, sel, end, m.bodyHeight())
}

// optionRows lays an ask's options out in rows of at most w cells, with the
// key hint after them when it fits; the bottom row repeats it anyway.
func optionRows(opts []string, hint string, w int) []string {
	var rows []string
	row := ""
	for _, o := range opts {
		o = clip(o, w)
		if row != "" && visibleWidth(row)+4+visibleWidth(o) > w {
			rows, row = append(rows, row), ""
		}
		if row != "" {
			row += "    "
		}
		row += o
	}
	if visibleWidth(row)+4+visibleWidth(hint) <= w {
		row += "    " + hint
	}
	return append(rows, row)
}

// diffView colours a unified diff and breaks its lines at w cells. Control
// characters are shown as escapes, so a changed line can't restyle the
// terminal or hide itself.
func diffView(diff string, w int) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimRight(diff, "\n"), "\n") {
		st := stGood
		switch {
		case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"), strings.HasPrefix(l, "diff "), strings.HasPrefix(l, "index "):
			st = stTitle
		case strings.HasPrefix(l, "-"):
			st = stBad
		case strings.HasPrefix(l, "@@"):
			st = stAccent
		}
		for _, p := range strings.Split(ansi.Hardwrap(visible(l), max(10, w), true), "\n") {
			out = append(out, st.Render(p))
		}
	}
	return out
}

// visible spells out control characters (ESC as \x1b), and tabs as spaces.
func visible(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteString("    ")
		case unicode.IsControl(r):
			fmt.Fprintf(&b, "\\x%02x", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ---- modules ---------------------------------------------------------------

func (m *model) keyModules(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "up", "k":
		if m.modSel > 0 {
			m.modSel--
		}
	case "down", "j":
		if m.modSel < len(m.st.Modules)-1 {
			m.modSel++
		}
	case "enter", "right", "l":
		if m.modSel < len(m.st.Modules) {
			mod := m.st.Modules[m.modSel]
			return m, m.push(detail{kind: "module", id: mod.Name, title: mod.Name})
		}
	}
	return m, nil
}

func (m *model) viewModules() []string {
	w := m.mainW() - 5
	var lines []string
	for i, mod := range m.st.Modules {
		on, name := "[ ]", mod.Name
		if len(mod.Projects) > 0 {
			on, name = "[x]", stTitle.Render(mod.Name)
		}
		line := pad(pad(on+" "+name, 18)+applies(mod.Projects), 40) + stDim.Render(mod.Description)
		if i == m.modSel && m.focus == 1 {
			line = stSel.Render(fit(fmt.Sprintf("%-18s%-22s%s", on+" "+mod.Name, applies(mod.Projects), mod.Description), w))
		}
		lines = append(lines, line)
	}
	lines = append(lines, "", stDim.Render(strings.Repeat("─", w)), "")
	if mod, ok := m.module(m.modName()); ok {
		lines = append(lines, stTitle.Render(mod.Name))
		lines = append(lines, wrap(mod.Description, min(w, 72))...)
		lines = append(lines, "")
		for _, kv := range [][2]string{{"applies to", applies(mod.Projects)}, {"release", mod.Release}, {"source", mod.Source}} {
			lines = append(lines, stDim.Render(fmt.Sprintf("%-14s", kv[0]))+kv[1])
		}
	}
	lines = append(lines, "", stDim.Render("Install a module by asking the lead (it needs your approval). Modules live in .modules/."))
	return window(indent(lines, "  "), m.modSel, m.bodyHeight()) // the list's rows come first
}

func (m *model) modName() string {
	if m.modSel < len(m.st.Modules) {
		return m.st.Modules[m.modSel].Name
	}
	return ""
}

func applies(projects []string) string {
	switch {
	case len(projects) == 0:
		return "off"
	case contains(projects, "*"):
		return "all projects"
	}
	return strings.Join(projects, ", ")
}

// ---- settings --------------------------------------------------------------

type setting struct {
	group, label, value, key string
	choices                  []string // a pick-list; custom ends it when any value goes
	action                   func() tea.Cmd
}

const custom = "custom…"

// modelChoices is the harness's known models plus any the config adds, Opus
// first, then a custom entry.
func modelChoices(c core.Config) []string {
	var out []string
	for _, m := range append(append(harness.Models(c.Harness), c.ModelChoices...), sortedKeys(c.Models)...) {
		if !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	return append(out, custom)
}

// scheduleText shows a stored schedule the way a person sets it: hours.
func scheduleText(s string) string {
	if h, ok := strings.CutPrefix(s, "every "); ok {
		if d, err := time.ParseDuration(h); err == nil && d%time.Hour == 0 {
			return fmtNum(d.Hours()) + " hours"
		}
	}
	return s
}

// scheduleHours turns hours typed in Settings into the stored form; 0 is off.
// A form the scheduler already reads ("daily 04:00", "every 90m") is kept as typed.
func scheduleHours(v string) (string, error) {
	if _, err := system.Parse(v); err == nil {
		return strings.Join(strings.Fields(v), " "), nil
	}
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(v), " hours"))
	switch {
	case err == nil && n == 0:
		return "off", nil
	case err != nil || n < 0:
		return "", fmt.Errorf("a schedule is a number of hours, daily HH:MM, or 0 for off")
	}
	return fmt.Sprintf("every %dh", n), nil
}

func (m *model) settings() []setting {
	c := m.st.Config
	onOff := func(b bool) string {
		if b {
			return "on"
		}
		return "off"
	}
	share := "off  (scrubbed; `" + core.Cmd + " report` shows what was sent)"
	if since, on := c.Sharing(); on {
		share = "on since " + since.Format("2006-01-02 15:04") + "  (scrubbed; `" + core.Cmd + " report` shows what was sent)"
	} else if m.shareAsk {
		share = report.Question + "  Enter: yes; any other key: no"
	}
	sandboxVal := c.Sandbox
	if m.st.Sandbox != "" {
		sandboxVal += " (" + m.st.Sandbox + ")"
	}
	models := modelChoices(c)
	s := []setting{
		{group: "General", label: "autonomy", value: strconv.Itoa(c.Autonomy) + "  " + autonomyText(c.Autonomy), key: "autonomy"},
		{group: "General", label: "sandbox", value: sandboxVal, key: "sandbox"},
		{group: "General", label: "WSL distro", value: c.WSLDistro, key: "wsl_distro"},
		{group: "General", label: "auto-update", value: onOff(c.AutoUpdate), key: "auto_update"},
		{group: "General", label: "use ANTHROPIC_API_KEY", value: onOff(c.APIKeyOptIn), key: "api_key_opt_in"},
		{group: "General", label: "release repo", value: c.ReleaseRepo, key: "release_repo"},
		{group: "General", label: "release channel", value: channelText(m.st.Channel, c.Channel), key: "channel", choices: []string{"default", "latest", "pre-release"}},
		{group: "General", label: "release", value: m.st.Version + ", " + pinText(c.ReleasePin), action: func() tea.Cmd {
			return func() tea.Msg {
				return flashMsg("`" + core.Cmd + " update --release <tag>` installs and pins a release")
			}
		}},
		{group: "General", label: "share reports", value: share, key: "share"},
		{group: "Agents", label: "harness", value: c.Harness, key: "harness", choices: append(slices.Clone(harness.Names), custom)},
		{group: "Agents", label: "worker model", value: c.Model, key: "model", choices: models},
		{group: "Agents", label: "lead model", value: c.LeadModel, key: "lead_model", choices: models},
		{group: "Agents", label: "reviewer model", value: c.ReviewerModel, key: "reviewer_model", choices: models},
	}
	for i, t := range c.TribunalModels {
		s = append(s, setting{group: "Agents", label: fmt.Sprintf("tribunal seat %d model", i+1), value: t.Model, key: "tribunal:" + strconv.Itoa(i), choices: models})
	}
	for _, pv := range m.st.Projects {
		p := pv.Name
		v := c.Projects[p].Model
		if v == "" {
			v = "default"
		}
		s = append(s, setting{group: "Agents", label: "model for " + p, value: v, key: "project:" + p, choices: append([]string{"default"}, models...)})
		// The open harness runs a named model, which the model setting picks.
		hs := slices.DeleteFunc(append([]string{"default"}, harness.Names...), func(h string) bool { return h == "open" })
		s = append(s, setting{group: "Agents", label: "harness for " + p, value: cmp.Or(c.Projects[p].Harness, "default"), key: "project-harness:" + p, choices: hs})
	}
	s = append(s, setting{group: "Agents", label: "review rounds", value: strconv.Itoa(c.ReviewRounds), key: "review_rounds"})
	// Each role's route shows the models it runs on in order; its pick-list
	// adds a model at the end, removes one, or clears the role's own route.
	for _, role := range core.RouteRoles {
		picks := c.Shown(role, "")
		v := strings.Join(picks, ", ")
		if v == "" {
			v = "none"
		}
		s = append(s, setting{group: "Routes", label: role, value: v, key: "route:" + role, choices: routeChoices(c, role)})
	}
	for _, name := range sortedKeys(c.Models) {
		x := c.Models[name]
		s = append(s, setting{group: "Routes", label: "model " + name, value: x.Where(), action: func() tea.Cmd {
			return func() tea.Msg {
				return flashMsg("models are set in .hive/config.json; a key is stored with `" + core.Cmd + " model key " + name + "`")
			}
		}})
	}
	for _, name := range sortedKeys(c.Schedules) {
		s = append(s, setting{group: "Schedules", label: name, value: scheduleText(c.Schedules[name]), key: "schedule:" + name})
	}
	s = append(s,
		setting{group: "Log in", label: "Claude Code", value: "runs Claude Code's own login", action: func() tea.Cmd { return login("claude", "auth", "login") }},
		setting{group: "Log in", label: "Codex", value: "runs Codex's own login (sign in with ChatGPT)", action: func() tea.Cmd { return login("codex", "login", "--device-auth") }},
		setting{group: "Log in", label: "GitHub", value: "runs the GitHub CLI's own login", action: func() tea.Cmd { return login("gh", "auth", "login") }},
	)
	return s
}

func pinText(pin string) string {
	if pin == "" {
		return "following the newest"
	}
	return "pinned to " + pin + ", auto-update off (`" + core.Cmd + " update` goes back to the newest)"
}

// channelText is the channel the daemon follows, and whether the person chose it.
func channelText(follows, chosen string) string {
	t := "the latest release"
	if follows == "pre-release" {
		t = "pre-releases, once safe here"
	}
	if chosen == "" {
		t += " (default)"
	}
	return t
}

func autonomyText(n int) string {
	switch n {
	case 1:
		return "ask me before anything outside the project"
	case 3:
		return "tell me after"
	}
	return "default"
}

func login(name string, args ...string) tea.Cmd {
	return tea.ExecProcess(exec.Command(name, args...), func(err error) tea.Msg {
		if err != nil {
			return flashMsg(name + " login: " + err.Error())
		}
		return flashMsg(name + " logged in")
	})
}

func (m *model) keySettings(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := m.settings()
	if m.picking && m.setSel < len(s) && s[m.setSel].choices != nil {
		return m.keyPick(s[m.setSel], k)
	}
	ask := m.shareAsk // any key answers the question; only Enter on it is yes
	m.shareAsk = false
	if ask && k.String() != "enter" {
		return m, nil
	}
	switch k.String() {
	case "up", "k":
		if m.setSel > 0 {
			m.setSel--
		}
	case "down", "j":
		if m.setSel < len(s)-1 {
			m.setSel++
		}
	case "enter", "right", "l":
		if m.setSel >= len(s) {
			return m, nil
		}
		cur := s[m.setSel]
		if cur.action != nil {
			return m, cur.action()
		}
		c := m.st.Config
		switch cur.key {
		case "autonomy":
			c.Autonomy = c.Autonomy%3 + 1
			return m, m.saveConfig(c, "autonomy set")
		case "auto_update":
			c.AutoUpdate = !c.AutoUpdate
			return m, m.saveConfig(c, "auto-update changed")
		case "channel":
			m.picking, m.pickSel = true, max(0, slices.Index(cur.choices, c.Channel))
			return m, nil
		case "api_key_opt_in":
			c.APIKeyOptIn = !c.APIKeyOptIn
			return m, m.saveConfig(c, "API key use changed")
		case "share":
			// Turning it on asks the installers' question first, so this way
			// in shows the same words; the second Enter is the yes.
			_, on := m.st.Config.Sharing()
			if !on && !ask {
				m.shareAsk = true
				return m, nil
			}
			c.SetShare(!on)
			return m, m.saveConfig(c, "sharing changed")
		case "sandbox":
			if c.Sandbox == "bare" {
				c.Sandbox = "auto"
				return m, m.saveConfig(c, "sandbox back to auto")
			}
			m.setFlash("to run agents without a sandbox, read the warning in `" + core.Cmd + " doctor --bare`")
			return m, nil
		}
		if cur.choices != nil {
			m.picking, m.pickSel = true, max(0, slices.Index(cur.choices, cur.value))
			return m, nil
		}
		value := strings.SplitN(cur.value, "  ", 2)[0]
		if strings.HasPrefix(cur.key, "schedule:") {
			value = strings.TrimSuffix(value, " hours")
		}
		return m, m.edit(cur.label, value)
	}
	return m, nil
}

func (m *model) edit(label, value string) tea.Cmd {
	m.editing = true
	m.input.Placeholder = label
	m.input.SetValue(value)
	m.input.CursorEnd()
	m.input.SetHeight(1)
	_, vw := m.settingCols()
	m.input.SetWidth(max(10, vw-3))
	return m.input.Focus()
}

// keyPick moves through a setting's pick-list; custom opens a text field.
func (m *model) keyPick(cur setting, k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "up", "k":
		m.pickSel = max(0, m.pickSel-1)
	case "down", "j":
		m.pickSel = min(len(cur.choices)-1, m.pickSel+1)
	case "esc", "left", "h":
		m.picking = false
	case "enter", "right", "l":
		m.picking = false
		if v := cur.choices[min(m.pickSel, len(cur.choices)-1)]; v != custom {
			return m, m.saveSetting(v)
		}
		return m, m.edit(cur.label, "")
	}
	return m, nil
}

// routeModels are the models a route can name, as a route names them: a
// named model by its name, any other as harness:model. Besides the
// workspace harness's, each other harness's known models are there, so a
// role can run on Codex in a Claude Hive, or the other way round.
func routeModels(c core.Config) []string {
	var out []string
	add := func(e string) {
		if e != "" && !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	for _, m := range modelChoices(c) {
		if m != custom {
			add(routeEntry(c, m))
		}
	}
	for _, h := range harness.Names {
		if h == "open" || h == "fake" || h == core.HarnessName(c.Harness) {
			continue
		}
		for _, m := range harness.Models(h) {
			add(core.Pick{Harness: h, Model: m}.String())
		}
	}
	return out
}

func routeEntry(c core.Config, m string) string {
	p, err := c.Pick(m)
	switch {
	case err != nil:
		return ""
	case p.Harness == "open":
		return p.Model
	}
	return p.String()
}

// routeNow is the route a pick changes: the role's own, else what it runs on
// now (its model setting, or the tribunal's seats).
func routeNow(c core.Config, role string) []string {
	var out []string
	add := func(e string) {
		if e != "" && !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	switch own := c.Routes[role]; {
	case len(own) > 0:
		for _, e := range own {
			add(routeEntry(c, e))
		}
	case role == "tribunal":
		for _, t := range c.Seats("") {
			if t.Harness == "open" {
				add(t.Model)
			} else {
				add(core.Pick{Harness: core.HarnessName(cmp.Or(t.Harness, c.Harness)), Model: t.Model}.String())
			}
		}
	case role != "fallback" && role != "security":
		add(routeEntry(c, c.Route(role, "")[0].String()))
	}
	return out
}

// routeChoices are a route's pick-list: add each known model that isn't on
// it, remove each that is, and clear the role's own route.
func routeChoices(c core.Config, role string) []string {
	now := routeNow(c, role)
	var out []string
	for _, m := range routeModels(c) {
		if slices.Contains(now, m) {
			out = append(out, "remove "+m)
		} else {
			out = append(out, "add "+m)
		}
	}
	return append(out, "clear")
}

// routeEdit is c with one pick from a route's pick-list made.
func routeEdit(c core.Config, role, choice string) core.Config {
	now := routeNow(c, role)
	switch {
	case choice == "clear":
		now = nil
	case strings.HasPrefix(choice, "add "):
		now = append(now, strings.TrimPrefix(choice, "add "))
	case strings.HasPrefix(choice, "remove "):
		now = slices.DeleteFunc(now, func(e string) bool { return e == strings.TrimPrefix(choice, "remove ") })
	default:
		return c
	}
	c.Routes = maps.Clone(c.Routes)
	if c.Routes == nil {
		c.Routes = map[string][]string{}
	}
	if len(now) == 0 {
		delete(c.Routes, role)
	} else {
		c.Routes[role] = now
	}
	return c
}

func (m *model) saveSetting(v string) tea.Cmd {
	s := m.settings()
	if m.setSel >= len(s) {
		return nil
	}
	c := m.st.Config
	switch key := s[m.setSel].key; {
	case key == "harness":
		c.Harness = v
	case key == "model":
		c.Model = v
	case key == "lead_model":
		c.LeadModel = v
	case key == "reviewer_model":
		c.ReviewerModel = v
	case strings.HasPrefix(key, "tribunal:"):
		i, _ := strconv.Atoi(strings.TrimPrefix(key, "tribunal:"))
		c.TribunalModels = slices.Clone(c.TribunalModels)
		c.TribunalModels[i].Model = v // a seat keeps its own harness
	case strings.HasPrefix(key, "project:"):
		p := strings.TrimPrefix(key, "project:")
		c.Projects = maps.Clone(c.Projects)
		if c.Projects == nil {
			c.Projects = map[string]core.ProjectSettings{}
		}
		ps := c.Projects[p]
		if ps.Model = v; v == "default" {
			ps.Model = ""
		}
		c.Projects[p] = ps
	case strings.HasPrefix(key, "project-harness:"):
		p := strings.TrimPrefix(key, "project-harness:")
		c.Projects = maps.Clone(c.Projects)
		if c.Projects == nil {
			c.Projects = map[string]core.ProjectSettings{}
		}
		ps := c.Projects[p]
		if ps.Harness = v; v == "default" {
			ps.Harness = ""
		}
		c.Projects[p] = ps
	case strings.HasPrefix(key, "route:"):
		c = routeEdit(c, strings.TrimPrefix(key, "route:"), v)
	case key == "channel":
		if c.Channel = v; v == "default" {
			c.Channel = ""
		} else {
			m.st.Channel = v // shown at once; the default shows once the daemon says what it is
		}
	case key == "review_rounds":
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return func() tea.Msg { return flashMsg("review rounds must be a number ≥ 1") }
		}
		c.ReviewRounds = n
	case key == "wsl_distro":
		c.WSLDistro = v
	case key == "release_repo":
		c.ReleaseRepo = v
	case strings.HasPrefix(key, "schedule:"):
		sched, err := scheduleHours(v)
		if err != nil {
			return func() tea.Msg { return flashMsg(err.Error()) }
		}
		c.Schedules = maps.Clone(c.Schedules)
		if c.Schedules == nil {
			c.Schedules = map[string]string{}
		}
		c.Schedules[strings.TrimPrefix(key, "schedule:")] = sched
	default:
		return nil
	}
	return m.saveConfig(c, "saved")
}

// saveConfig shows the new settings at once and posts them. The flash that
// follows fetches state, which puts the daemon's back if it refused them.
func (m *model) saveConfig(c core.Config, ok string) tea.Cmd {
	m.st.Config = c
	return m.post("/api/config", c, ok)
}

// settingCols is the width of the settings' label column, 28 cells or less
// on a narrow pane, and of the value column, which takes the rest.
func (m *model) settingCols() (label, value int) {
	label = min(28, max(12, (m.mainW()-4)/2))
	return label, max(10, m.mainW()-5-label)
}

func (m *model) viewSettings() []string {
	var lines, banner []string
	if m.st.ConfigErr != "" { // pinned above the rows so a short window never scrolls it away
		banner = []string{"  " + stBad.Render(clip("config.json: "+m.st.ConfigErr, m.mainW()-3)), ""}
	}
	sel, end, group := 0, 0, ""
	lw, vw := m.settingCols()
	for i, st := range m.settings() {
		if st.group != group {
			if group != "" {
				lines = append(lines, "")
			}
			group = st.group
			lines = append(lines, "  "+stDim.Render(group))
		}
		gut, label, value := "  ", st.label, st.value
		if i == m.setSel {
			gut, label, sel = stTitle.Render("▌")+" ", stTitle.Render(label), len(lines)
			if m.editing {
				value = m.input.View()
			}
		}
		// A long value wraps under its own column; a label too long for its
		// column puts the value on the next line.
		vals := []string{value}
		if !(i == m.setSel && m.editing) {
			vals = wrap(value, vw)
		}
		if visibleWidth(st.label) >= lw-1 {
			for _, l := range wrap(st.label, m.mainW()-5) {
				if i == m.setSel {
					l = stTitle.Render(l)
				}
				lines = append(lines, gut+"  "+l)
			}
			label = ""
		}
		for j, v := range vals {
			if j > 0 {
				label = ""
			}
			lines = append(lines, gut+"  "+pad(label, lw)+v)
		}
		if i == m.setSel {
			end = len(lines) - 1
		}
		if i == m.setSel && m.picking {
			for j, ch := range st.choices {
				if j == m.pickSel {
					ch = stTitle.Render("▸ " + ch)
				} else {
					ch = "  " + ch
				}
				lines = append(lines, "    "+pad("", lw)+clip(ch, vw))
			}
			end = len(lines) - 1
		}
	}
	lines = append(lines, "", "  "+stDim.Render("Hive "+m.st.Version+" · "+m.st.Workspace))
	return append(banner, span(lines, sel, end, m.bodyHeight()-len(banner))...)
}

func fmtNum(f float64) string { return strconv.FormatFloat(f, 'g', 6, 64) }

// loginLine is a login's expiry and last renewal, and why it can't renew.
func loginLine(l core.LoginState) string {
	exp := "expires " + l.Expires.Local().Format("Mon 15:04")
	if l.Expires.IsZero() {
		exp = "expiry unknown"
	} else if !l.Expires.After(time.Now()) {
		exp = stBad.Render("expired") + " " + l.Expires.Local().Format("Mon 15:04")
	}
	renewed := "never renewed by the Hive"
	if !l.Renewed.IsZero() {
		renewed = "renewed " + l.Renewed.Local().Format("Mon 15:04")
	}
	line := fmt.Sprintf("%s  %s  %s", text.Visible(l.Login), exp, stDim.Render(renewed))
	if l.Error != "" {
		line += "  " + stBad.Render("can't renew") + ": " + text.Visible(core.Clip(l.Error, 120))
		if l.Run != "" {
			line += "  " + stWarn.Render("to fix: ") + text.Visible(l.Run)
		}
	}
	return line
}

// toldHead heads the "done for you" list: how many in the last day, so a
// quiet notice isn't missed among the recent ones.
func toldHead(told []core.Ask, now time.Time) string {
	day := 0
	for _, a := range told {
		if now.Sub(a.Asked) < 24*time.Hour {
			day++
		}
	}
	if day == 0 {
		return fmt.Sprintf("done for you (%d recent)", len(told))
	}
	return fmt.Sprintf("done for you (%d in the last day, %d recent)", day, len(told))
}
