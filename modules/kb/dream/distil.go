package main

import (
	"fmt"
	"sort"
	"strings"
)

// learnedPath is the practice note the pass distils from the record.
const learnedPath = "practice/learned-from-the-record.md"

// distil writes what the record says is true now: one summary per project
// (what is in flight, blocked, merged and reverted, the gate's and the last
// step-back's word) and one practice note of why work gets rolled back,
// blocked or alerted, counted. Hidden projects are left out. A note is only
// rewritten when what it says changed.
func distil(notes map[string]*Note, st *State, hidden map[string]bool, day string) []string {
	byProject := map[string][]string{}
	for id, t := range st.Tasks {
		if t.Project != "" && !hidden[t.Project] {
			byProject[t.Project] = append(byProject[t.Project], id)
		}
	}
	var projects []string
	for p := range byProject {
		projects = append(projects, p)
	}
	sort.Strings(projects)
	var out []string
	put := func(path, title, project, body string) {
		if n := notes[path]; n != nil && n.Body == body {
			return
		}
		n := &Note{Path: path}
		if old := notes[path]; old != nil {
			n.was, n.Related = old.was, old.Related
		}
		n.set("title", title)
		n.set("tags", "summary, record")
		n.set("project", project)
		n.set("date", day)
		n.set("consolidated", day)
		n.set("distilled", "the kb module's dream, from the record")
		n.Body = body
		notes[path] = n
		out = append(out, path)
	}
	for _, p := range projects {
		put("project-"+slug(p)+"-what-s-true-now.md", "Project "+p+": what's true now", p, projectBody(p, byProject[p], st))
	}
	if body := learnedBody(st, hidden); body != "" {
		put(learnedPath, "Learned from the record", "", body)
	}
	return out
}

func projectBody(p string, ids []string, st *State) string {
	var b strings.Builder
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	stages := map[string]int{}
	var blocked, merged, reverted []string
	type dated struct {
		at, line string
	}
	var m, r []dated
	for _, id := range ids {
		t := st.Tasks[id]
		stages[t.Stage]++
		if t.Stage == "blocked" {
			blocked = append(blocked, fmt.Sprintf("- %s (%s): %s", clip(t.Title, 120), id, firstOf(clip(t.Reason, 200), "no reason given")))
		}
		if !t.Merged.IsZero() {
			m = append(m, dated{t.Merged.UTC().Format("2006-01-02 15:04"), clip(t.Title, 140)})
		}
		if !t.Reverted.IsZero() {
			r = append(r, dated{t.Reverted.UTC().Format("2006-01-02 15:04"), clip(t.Title, 140) + " (reverted)"})
		}
	}
	for _, g := range st.Gates {
		if g.Project == p && g.Outcome == "rollback" {
			r = append(r, dated{g.At.UTC().Format("2006-01-02 15:04"), "the gate rolled back " + short(g.Head) + ": " + g.Reason})
		}
	}
	for _, list := range [][]dated{m, r} {
		sort.Slice(list, func(i, j int) bool { return list[i].at > list[j].at })
	}
	for i, d := range m {
		if i < 8 {
			merged = append(merged, "- "+d.at+" "+d.line)
		}
	}
	for i, d := range r {
		if i < 8 {
			reverted = append(reverted, "- "+d.at+" "+d.line)
		}
	}
	var flight []string
	for _, s := range []string{"working", "reviewing", "testing", "merging", "landing", "queued", "waiting-person"} {
		if stages[s] > 0 {
			flight = append(flight, fmt.Sprintf("%d %s", stages[s], strings.ReplaceAll(s, "-", " on the ")))
		}
	}
	fmt.Fprintf(&b, "What the record says about %s now, distilled by the kb module's dream. It is rewritten when that changes; the record under record/ has the detail.\n\n", p)
	fmt.Fprintf(&b, "**Tasks:** %d in all, %d merged or done, %d blocked, %d stopped.", len(ids), stages["merged"]+stages["done"], stages["blocked"], stages["stopped"])
	if len(flight) > 0 {
		b.WriteString(" In flight: " + strings.Join(flight, ", ") + ".")
	}
	b.WriteString("\n")
	section := func(title string, lines []string) {
		if len(lines) > 0 {
			fmt.Fprintf(&b, "\n## %s\n\n%s\n", title, strings.Join(lines, "\n"))
		}
	}
	section("Blocked now", blocked)
	section("Merged lately", merged)
	section("Reverted or rolled back lately", reverted)
	if s, ok := st.Steps[p]; ok {
		section("The last step-back", []string{fmt.Sprintf("%s, %s: %s", s.At.UTC().Format("2006-01-02"), s.Status, firstOf(s.Text, "no report"))})
	}
	return strings.TrimSpace(b.String())
}

func short(sha string) string { return sha[:min(8, len(sha))] }

// learnedBody counts why changes were rolled back, why work was blocked and
// what raised alerts, most common first.
func learnedBody(st *State, hidden map[string]bool) string {
	var b strings.Builder
	count := func(title, intro string, c map[string]int, last map[string]string) {
		if len(c) == 0 {
			return
		}
		var keys []string
		for k := range c {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if c[keys[i]] != c[keys[j]] {
				return c[keys[i]] > c[keys[j]]
			}
			return keys[i] < keys[j]
		})
		fmt.Fprintf(&b, "\n## %s\n\n%s\n\n", title, intro)
		for i, k := range keys {
			if i == 8 {
				break
			}
			line := fmt.Sprintf("- %d× %s", c[k], k)
			if l := last[k]; l != "" {
				line += " (latest: " + l + ")"
			}
			b.WriteString(line + "\n")
		}
	}
	gates, gateLast := map[string]int{}, map[string]string{}
	for _, g := range st.Gates {
		if g.Outcome != "rollback" || hidden[g.Project] {
			continue
		}
		k := reasonKey(g.Reason)
		gates[k]++
		gateLast[k] = g.At.UTC().Format("2006-01-02") + ", " + short(g.Head)
	}
	count("Why the gate rolled changes back", "A change the gate rolls back is reverted after merge; these are the reasons it gave, so a change can be built to clear them.", gates, gateLast)
	blocks, blockLast := map[string]int{}, map[string]string{}
	for _, t := range st.Tasks {
		if t.Blocked.IsZero() || hidden[t.Project] {
			continue
		}
		k := reasonKey(t.Reason)
		blocks[k]++
		if l := blockLast[k]; l < t.Blocked.UTC().Format("2006-01-02") {
			blockLast[k] = t.Blocked.UTC().Format("2006-01-02")
		}
	}
	count("Why work got blocked", "What tasks that were ever blocked last said; a reason that keeps coming back is worth a brief of its own.", blocks, blockLast)
	count("What raised alerts", "Alerts by kind, over the whole record.", st.Alerts, nil)
	steps := map[string]int{}
	for p, s := range st.Steps {
		if !hidden[p] {
			steps[s.Status+": "+reasonKey(s.Text)]++
		}
	}
	count("How the last step-backs ended", "One per project.", steps, nil)
	if b.Len() == 0 {
		return ""
	}
	return strings.TrimSpace("What the record keeps saying, counted by the kb module's dream. It is rewritten when the counts change.\n" + b.String())
}

// reasonKey is a reason without its particulars: up to its second colon,
// clipped, so "fails its own vet or short tests: internal/gate TestMeter"
// and the bare form count apart only by the test they name.
func reasonKey(r string) string {
	r = strings.TrimSpace(r)
	if r == "" {
		return "no reason given"
	}
	if first, rest, ok := strings.Cut(r, ": "); ok {
		if second, _, ok := strings.Cut(rest, ": "); ok {
			r = first + ": " + second
		}
	}
	return clip(r, 120)
}
