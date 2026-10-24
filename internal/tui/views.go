package tui

import (
	"fmt"
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
	for _, p := range m.st.Projects {
		best := "no score yet"
		if p.Best != nil {
			best = "best " + fmtNum(*p.Best)
		}
		if !add(row{key: "proj:" + p.Name, text: fmt.Sprintf("%s  %s  %s  %s", stTitle.Render(p.Name), stDim.Render(p.Repo), best, spark(p.Scores)), expand: true, def: true}) {
			continue
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
				if !x.Kept {
					mark = stBad.Render("not kept")
				}
				add(row{key: "sc:" + x.SHA + x.At.String(), depth: 2, text: fmt.Sprintf("%s  %s  %s  %s  %s", x.At.Local().Format("Mon 15:04"), core.Short(x.SHA), v, mark, stDim.Render(core.Clip(x.Note, 80)))})
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
	if add(row{key: "sweeps", text: fmt.Sprintf("improvement sweeps (%d)", len(m.st.Sweeps)), expand: len(m.st.Sweeps) > 0}) {
		for _, s := range m.st.Sweeps {
			text := fmt.Sprintf("%s  %s  asks filed: %d", s.Started.Local().Format("Mon 15:04"), s.Status, len(s.Asks))
			d := detail{kind: "text", id: s.ID, title: "sweep", text: sweepText(s.Report, s.Asks)}
			add(row{key: "sweep:" + s.ID, depth: 1, text: text, open: &d})
		}
	}
	if add(row{key: "plans", text: "plan usage", expand: len(m.st.Plans) > 0, def: true}) {
		for _, p := range m.st.Plans {
			status := p.Limit.Status
			if status == "rejected" {
				status = stBad.Render("limited") + ", resets " + p.Limit.ResetsAt.Local().Format("Mon 15:04")
			}
			text := fmt.Sprintf("%s  %s", p.Key(), status)
			for _, k := range sortedKeys(p.Limit.Windows) {
				wnd := p.Limit.Windows[k]
				text += fmt.Sprintf("  %s %.0f%%", k, wnd.Utilization*100)
			}
			add(row{key: "plan:" + p.Key(), depth: 1, text: text})
		}
	}
	add(row{key: "waiting", text: fmt.Sprintf("waiting on you (%d)", len(m.st.Asks)), jump: secWaiting})
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
			add(row{key: fmt.Sprintf("alert:%d", i), depth: 1, text: fmt.Sprintf("%s  %s  %s", a.At.Local().Format("Mon 15:04"), stWarn.Render(a.Kind), a.Text)})
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
	case work.StagePerson:
		stage = stWarn.Render(stage)
	}
	progress := ""
	if r, ok := m.run(t.Worker); ok && r.Progress != "" && !t.Terminal() {
		progress = stDim.Render("  “" + core.Clip(r.Progress, 60) + "” " + ago(r.ProgressAt))
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
	return fmt.Sprintf("%s  %s  turn %d  $%.2f  %dk ctx  %s  %s", runLabel(r), status, r.Turn, r.Cost, r.Context/1000, stDim.Render(core.Clip(extra, 70)), stDim.Render(ago(r.Updated)))
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
	if m.dashSel >= len(rows) {
		m.dashSel = max(0, len(rows)-1)
	}
	if len(rows) == 0 {
		return m, nil
	}
	r := rows[m.dashSel]
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

func (m *model) viewTree() []string {
	rows := m.dashRows()
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
	if d.scroll > len(lines)-1 {
		d.scroll = max(0, len(lines)-1)
	}
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
	sel := 0
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
		top := stTitle.Render(from) + "  " + stDim.Render(a.Kind)
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
			ls = append(ls, strings.Join(opts, "    ")+"    "+stTitle.Render("←→")+" "+stDim.Render("pick")+"   "+stTitle.Render("enter")+" "+stDim.Render("answer"))
		}
		lines = append(append(lines, indent(ls, gut)...), "")
	}
	return window(lines, sel, m.bodyHeight())
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
	return indent(lines, "  ")
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
	for _, m := range append(harness.Models(c.Harness), c.ModelChoices...) {
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
	share := "off"
	if since, on := c.Sharing(); on {
		share = "on since " + since.Format("2006-01-02 15:04")
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
		{group: "General", label: "release", value: pinText(c.ReleasePin), action: func() tea.Cmd {
			return func() tea.Msg {
				return flashMsg("`" + core.Cmd + " update --release <tag>` installs and pins a release")
			}
		}},
		{group: "General", label: "share failures", value: share + "  (scrubbed; `" + core.Cmd + " report` shows what was sent)", key: "share"},
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
	}
	s = append(s, setting{group: "Agents", label: "review rounds", value: strconv.Itoa(c.ReviewRounds), key: "review_rounds"})
	for _, name := range sortedKeys(c.Schedules) {
		s = append(s, setting{group: "Schedules", label: name, value: scheduleText(c.Schedules[name]), key: "schedule:" + name})
	}
	s = append(s,
		setting{group: "Log in", label: "Claude Code", value: "runs Claude Code's own login", action: func() tea.Cmd { return login("claude", "auth", "login") }},
		setting{group: "Log in", label: "GitHub", value: "runs the GitHub CLI's own login", action: func() tea.Cmd { return login("gh", "auth", "login") }},
	)
	return s
}

func pinText(pin string) string {
	if pin == "" {
		return "the newest"
	}
	return "pinned to " + pin + ", auto-update off (`" + core.Cmd + " update` goes back to the newest)"
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
			return m, m.post("/api/config", c, "autonomy set")
		case "auto_update":
			c.AutoUpdate = !c.AutoUpdate
			return m, m.post("/api/config", c, "auto-update changed")
		case "api_key_opt_in":
			c.APIKeyOptIn = !c.APIKeyOptIn
			return m, m.post("/api/config", c, "API key use changed")
		case "share":
			_, on := m.st.Config.Sharing()
			c.SetShare(!on)
			return m, m.post("/api/config", c, "sharing changed")
		case "sandbox":
			if c.Sandbox == "bare" {
				c.Sandbox = "auto"
				return m, m.post("/api/config", c, "sandbox back to auto")
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
	m.input.SetWidth(max(10, m.mainW()-36))
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
	return m.post("/api/config", c, "saved")
}

func (m *model) viewSettings() []string {
	var lines, banner []string
	if m.st.ConfigErr != "" { // pinned above the rows so a short window never scrolls it away
		banner = []string{"  " + stBad.Render(clip("config.json: "+m.st.ConfigErr, m.mainW()-3)), ""}
	}
	sel, group := 0, ""
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
		lines = append(lines, gut+"  "+pad(label, 28)+value)
		if i == m.setSel && m.picking {
			for j, ch := range st.choices {
				if j == m.pickSel {
					ch = stTitle.Render("▸ " + ch)
				} else {
					ch = "  " + ch
				}
				lines = append(lines, "    "+pad("", 28)+ch)
			}
		}
	}
	lines = append(lines, "", "  "+stDim.Render("Hive "+m.st.Version+" · "+m.st.Workspace))
	return append(banner, window(lines, sel, m.bodyHeight()-len(banner))...)
}

func fmtNum(f float64) string { return strconv.FormatFloat(f, 'g', 6, 64) }
