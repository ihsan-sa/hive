package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// digest is the pass's "what the Hive learned since last time": what moved in
// the record since the last pass and what the pass did to the notes, short
// enough to read in the Knowledge tab. It is nil when there is nothing to
// say, so a quiet Hive makes no digests.
func digest(in Input, p Pass, notes map[string]*Note, hidden map[string]bool, day string) *Note {
	st, since := in.State, in.State.Last
	after := func(t time.Time) bool { return !t.IsZero() && t.After(since) }
	var learned []string
	type item struct {
		at   time.Time
		line string
	}
	byProject := map[string]map[string][]item{}
	note := func(project, what string, at time.Time, line string) {
		if hidden[project] {
			return
		}
		if byProject[project] == nil {
			byProject[project] = map[string][]item{}
		}
		byProject[project][what] = append(byProject[project][what], item{at, line})
	}
	for _, t := range st.Tasks {
		if after(t.Merged) {
			note(t.Project, "merged", t.Merged, clip(t.Title, 100))
		}
		if after(t.Reverted) {
			note(t.Project, "reverted", t.Reverted, clip(t.Title, 100))
		}
		if after(t.Blocked) && t.Stage == "blocked" {
			note(t.Project, "blocked", t.Blocked, clip(t.Title, 80)+": "+clip(t.Reason, 120))
		}
	}
	for _, g := range st.Gates {
		if after(g.At) && g.Outcome == "rollback" {
			note(g.Project, "rolled back by the gate", g.At, short(g.Head)+": "+clip(g.Reason, 120))
		}
	}
	var projects []string
	for pr := range byProject {
		projects = append(projects, pr)
	}
	sort.Strings(projects)
	for _, pr := range projects {
		for _, what := range []string{"merged", "reverted", "rolled back by the gate", "blocked"} {
			items := byProject[pr][what]
			if len(items) == 0 {
				continue
			}
			sort.Slice(items, func(i, j int) bool { return items[i].at.After(items[j].at) })
			var lines []string
			for i, it := range items {
				if i == 3 {
					lines = append(lines, fmt.Sprintf("and %d more", len(items)-3))
					break
				}
				lines = append(lines, it.line)
			}
			learned = append(learned, fmt.Sprintf("- **%s, %d %s:** %s", firstOf(pr, "the Hive"), len(items), what, strings.Join(lines, "; ")))
		}
	}
	for _, s := range st.Sweeps {
		if after(s.At) {
			learned = append(learned, "- **Sweep** ("+s.At.UTC().Format("Jan 2 15:04")+"): "+clip(s.Text, 240))
		}
	}
	for pr, s := range st.Steps {
		if after(s.At) && !hidden[pr] {
			learned = append(learned, fmt.Sprintf("- **Step-back, %s** (%s): %s", firstOf(pr, "the Hive"), s.Status, clip(s.Text, 200)))
		}
	}
	var said []string
	for _, s := range st.Said {
		if p, ok := st.chatProject(s.Chat); after(s.At) && ok && !hidden[p] {
			said = append(said, "- "+s.At.UTC().Format("Jan 2 15:04")+" in "+s.Chat+": “"+clip(s.Text, 160)+"”")
		}
	}
	if len(said) > 3 {
		said = said[len(said)-3:]
	}
	var kb []string
	for _, m := range p.Merged {
		kb = append(kb, fmt.Sprintf("- Merged %d notes into [[%s]]", len(m.From)+1, strings.TrimSuffix(m.Into, ".md")))
	}
	if len(p.Linked) > 0 {
		var ls []string
		for i, l := range p.Linked {
			if i == 5 {
				ls = append(ls, fmt.Sprintf("and %d more", len(p.Linked)-5))
				break
			}
			ls = append(ls, "[["+l[0]+"]] ↔ [["+l[1]+"]]")
		}
		kb = append(kb, fmt.Sprintf("- Linked %d pairs: %s", len(p.Linked), strings.Join(ls, ", ")))
	}
	for _, r := range p.Retired {
		kb = append(kb, "- Retired [["+strings.TrimSuffix(r.Path, ".md")+"]]: "+r.Why)
	}
	if len(p.Distilled) > 0 {
		kb = append(kb, "- Distilled from the record: "+strings.Join(p.Distilled, ", "))
	}
	if len(learned)+len(said)+len(kb) == 0 {
		return nil
	}
	from := "the start"
	if !since.IsZero() {
		from = since.UTC().Format("Mon Jan 2 15:04 UTC")
	}
	n := &Note{Path: "dreams/" + in.Now.UTC().Format("2006-01-02-1504") + ".md"}
	n.set("title", "What the Hive learned since "+from)
	n.set("date", day)
	n.set("trigger", firstOf(in.Trigger, "asked"))
	var b strings.Builder
	fmt.Fprintf(&b, "The kb module's dream pass at %s (%s), over %d notes and the record to event %d.\n", in.Now.UTC().Format("2006-01-02 15:04 UTC"), firstOf(in.Trigger, "asked"), len(in.Notes), st.Cursor)
	for _, s := range []struct {
		title string
		lines []string
	}{{"What happened", learned}, {"What the person said", said}, {"What the pass did to the knowledge base", kb}} {
		if len(s.lines) > 0 {
			fmt.Fprintf(&b, "\n## %s\n\n%s\n", s.title, strings.Join(s.lines, "\n"))
		}
	}
	n.Body = strings.TrimSpace(b.String())
	return n
}
