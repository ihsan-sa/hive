package main

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// Input is what one pass works over: the shared notes as the kb module hands
// them, and the record folded into State.
type Input struct {
	Notes   []Shared
	Shipped []string // practice docs the module files; never edited
	Hidden  []string // hidden projects, kept out of every note
	State   *State
	Now     time.Time
	Trigger string
}

// Shared is a note as GET /api/kb/notes returns it.
type Shared struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

// Pass is what POST /api/kb/dream takes.
type Pass struct {
	Trigger   string       `json:"trigger"`
	Changes   []FileChange `json:"changes"`
	Merged    []Merge      `json:"merged"`
	Linked    [][2]string  `json:"linked"`
	Distilled []string     `json:"distilled"`
	Retired   []Retire     `json:"retired"`
	Digest    string       `json:"digest"`
	Seq       int64        `json:"seq"`
}

type FileChange struct {
	Path   string `json:"path"`
	Text   string `json:"text,omitempty"`
	Was    string `json:"was,omitempty"`
	Remove bool   `json:"remove,omitempty"`
}

type Merge struct {
	Into string   `json:"into"`
	From []string `json:"from"`
}

type Retire struct {
	Path string `json:"path"`
	Why  string `json:"why"`
}

// keepDigests is how many past digests stay under dreams/.
const keepDigests = 60

// Plan works out one pass: merge duplicates into living notes, retire what
// the record has since contradicted, distil the record into project summaries
// and a practice note, link related notes both ways, and write the digest.
// It is pure: the same notes and state give the same pass, and a second pass
// over what the first wrote changes nothing but a new digest, and only when
// there is news.
func Plan(in Input) Pass {
	p := Pass{Trigger: in.Trigger, Seq: in.State.Cursor}
	day := in.Now.UTC().Format("2006-01-02")
	shipped := map[string]bool{}
	for _, s := range in.Shipped {
		shipped[s] = true
	}
	hidden := map[string]bool{}
	for _, h := range in.Hidden {
		hidden[h] = true
	}
	orig := map[string]string{}
	notes := map[string]*Note{}
	for _, s := range in.Notes {
		n := parse(s.Path, s.Text)
		if hidden[n.get("project")] {
			continue // written before its project was hidden: the kb module refuses a pass that rewrites it
		}
		orig[s.Path] = s.Text
		notes[s.Path] = n
	}

	p.Merged = merge(notes, day)
	p.Retired = retire(notes, in.State, day)
	p.Distilled = distil(notes, in.State, hidden, day)
	p.Linked = link(notes, shipped)

	titles := map[string]string{}
	for _, n := range notes {
		titles[n.name()] = n.title()
	}
	var paths []string
	for path := range orig {
		paths = append(paths, path)
	}
	for path := range notes {
		if _, ok := orig[path]; !ok {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		n, keep := notes[path]
		was, had := orig[path]
		switch {
		case !keep:
			p.Changes = append(p.Changes, FileChange{Path: path, Remove: true, Was: hashOf(was)})
		case shipped[path]:
		default:
			text := n.render(titles)
			if !had || text != was {
				c := FileChange{Path: path, Text: text}
				if had {
					c.Was = hashOf(was)
				}
				p.Changes = append(p.Changes, c)
			}
		}
	}
	if d := digest(in, p, notes, hidden, day); d != nil {
		p.Digest = d.Path
		c := FileChange{Path: d.Path, Text: d.render(titles)}
		if was, ok := orig[d.Path]; ok { // a second pass in the same minute
			c.Was = hashOf(was)
		}
		p.Changes = append(p.Changes, c)
		var old []string
		for path := range orig {
			if strings.HasPrefix(path, "dreams/") {
				old = append(old, path)
			}
		}
		sort.Strings(old)
		for len(old) >= keepDigests {
			p.Changes = append(p.Changes, FileChange{Path: old[0], Remove: true, Was: hashOf(orig[old[0]])})
			old = old[1:]
		}
	}
	return p
}

// entry is one dated piece of a living note.
type entry struct {
	Date, Run, From, Text string
}

// merge folds notes with the same title (as remember names them, so
// "x.md", "x-2.md" ...) into one living note: the newest text first, then
// every older one under History with its date, run and file, less the
// paragraphs a newer one already says. The header keeps every run and
// source. A living note from an earlier pass takes new duplicates in, and
// what it held is older than them.
func merge(notes map[string]*Note, day string) []Merge {
	groups := map[string][]*Note{}
	for _, n := range notes {
		if !n.topLevel() || n.distilled() || n.get("retired") != "" {
			continue
		}
		key := slug(n.title())
		if key == "" {
			key = suffixRe.ReplaceAllString(n.name(), "")
		}
		// Only one project's notes merge: one title in two projects may be
		// two different things.
		groups[n.get("project")+"\x00"+key] = append(groups[n.get("project")+"\x00"+key], n)
	}
	var keys []string
	for k, g := range groups {
		if len(g) > 1 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var out []Merge
	for _, key := range keys {
		g := groups[key]
		var living *Note
		var fresh []*Note
		for _, n := range g {
			if n.get("consolidated") != "" && (living == nil || n.Path < living.Path) {
				if living != nil {
					fresh = append(fresh, living)
				}
				living = n
			} else {
				fresh = append(fresh, n)
			}
		}
		sort.Slice(fresh, func(i, j int) bool { // oldest first
			a, b := fresh[i], fresh[j]
			if a.get("date") != b.get("date") {
				return a.get("date") < b.get("date")
			}
			return suffix(a.name()) < suffix(b.name())
		})
		var entries []entry // newest first
		for i := len(fresh) - 1; i >= 0; i-- {
			n := fresh[i]
			entries = append(entries, entryList(n)...)
		}
		if living != nil {
			entries = append(entries, entryList(living)...)
		}
		into := living
		if into == nil {
			_, base, _ := strings.Cut(key, "\x00")
			into = notes[base+".md"]
			if into == nil || !contains(g, into) {
				into = fresh[0]
			}
		}
		newest := fresh[len(fresh)-1]
		m := Merge{Into: into.Path}
		runs, sources, tags, projects := []string{}, []string{}, []string{}, []string{}
		for _, n := range g {
			runs = append(runs, splitList(n.get("runs"))...)
			runs = append(runs, n.get("run"))
			sources = append(sources, splitList(n.get("sources"))...)
			sources = append(sources, n.Path)
			tags = append(tags, splitList(n.get("tags"))...)
			projects = append(projects, splitList(n.get("projects"))...)
			projects = append(projects, n.get("project"))
			if n != into {
				m.From = append(m.From, n.Path)
				delete(notes, n.Path)
			}
		}
		sort.Strings(m.From)
		ln := &Note{Path: into.Path, was: into.was, Related: into.Related}
		ln.set("title", newest.title())
		ln.set("tags", strings.Join(uniq(tags), ", "))
		if ps := uniq(projects); len(ps) == 1 {
			ln.set("project", ps[0])
		} else {
			ln.set("project", "")
			ln.set("projects", strings.Join(ps, ", "))
		}
		ln.set("date", entries[0].Date)
		ln.set("run", entries[0].Run)
		ln.set("from", entries[0].From)
		ln.set("runs", strings.Join(uniq(runs), ", "))
		ln.set("sources", strings.Join(uniq(sources), ", "))
		ln.set("consolidated", day)
		ln.Body = livingBody(entries)
		notes[into.Path] = ln
		out = append(out, m)
	}
	return out
}

func contains(g []*Note, n *Note) bool {
	for _, x := range g {
		if x == n {
			return true
		}
	}
	return false
}

const historyHeading = "\n## History\n"

// entryList is a note's entries: a living note's current text and history,
// or a plain note's whole text.
func entryList(n *Note) []entry {
	if n.get("consolidated") == "" {
		return []entry{{Date: n.get("date"), Run: n.get("run"), From: n.Path, Text: n.Body}}
	}
	cur, hist, _ := strings.Cut("\n"+n.Body, historyHeading)
	out := []entry{{Date: n.get("date"), Run: n.get("run"), From: n.get("from"), Text: strings.TrimSpace(cur)}}
	for _, part := range strings.Split(hist, "\n### ")[1:] {
		line, text, _ := strings.Cut(part, "\n")
		e := entry{Text: strings.TrimSpace(text)}
		for _, f := range strings.Split(line, " · ") {
			switch {
			case strings.HasPrefix(f, "run "):
				e.Run = strings.TrimPrefix(f, "run ")
			case strings.HasPrefix(f, "was "):
				e.From = strings.TrimPrefix(f, "was ")
			case e.Date == "":
				e.Date = f
			}
		}
		if e.Text == sameAbove {
			e.Text = ""
		}
		out = append(out, e)
	}
	return out
}

const sameAbove = "(says nothing the newer text above doesn't)"

// livingBody renders entries, newest first, dropping paragraphs a newer
// entry already holds.
func livingBody(entries []entry) string {
	seen := map[string]bool{}
	var b strings.Builder
	for i, e := range entries {
		var keep []string
		for _, para := range paragraphs(e.Text) {
			if !seen[norm(para)] {
				keep = append(keep, para)
			}
			seen[norm(para)] = true
		}
		text := strings.Join(keep, "\n\n")
		if i == 0 {
			b.WriteString(text)
			if len(entries) > 1 {
				b.WriteString("\n" + historyHeading)
			}
			continue
		}
		head := []string{firstOf(e.Date, "undated")}
		if e.Run != "" {
			head = append(head, "run "+e.Run)
		}
		if e.From != "" {
			head = append(head, "was "+e.From)
		}
		if text == "" {
			text = sameAbove
		}
		fmt.Fprintf(&b, "\n### %s\n\n%s\n", strings.Join(head, " · "), text)
	}
	return strings.TrimSpace(b.String())
}

func splitList(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

func uniq(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range s {
		if v = strings.TrimSpace(v); v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

var supersedeRe = regexp.MustCompile(`(?i)\b(?:supersedes|revises|replaces)\s+['"‘“]([^'"’”\n]+)['"’”]`)

// retire marks notes later events contradict, keeping their text: a
// "Blocked: ..." note whose run has since finished done or whose task has
// merged, finished or been stopped, and a note another note says it
// supersedes, revises or replaces (by title).
func retire(notes map[string]*Note, st *State, day string) []Retire {
	byTitle := map[string]*Note{}
	for _, n := range notes {
		if n.topLevel() {
			byTitle[strings.ToLower(n.title())] = n
		}
	}
	why := map[string]string{}
	for _, n := range notes {
		if !n.topLevel() || n.get("retired") != "" {
			continue
		}
		if rest, ok := strings.CutPrefix(n.title(), "Blocked: "); ok {
			run := n.get("run")
			if r := st.Runs[run]; r != nil && r.Status == "done" && r.At.UTC().Format("2006-01-02") >= n.get("date") {
				why[n.Path] = "its run " + run + " has since finished done"
			} else if id, t := st.taskOf(run, rest); t != nil {
				switch t.Stage {
				case "merged", "done", "stopped":
					why[n.Path] = fmt.Sprintf("its task %s has since %s", id, map[string]string{"merged": "merged", "done": "finished", "stopped": "been stopped"}[t.Stage])
				}
			}
		}
		for _, m := range supersedeRe.FindAllStringSubmatch(n.Body, -1) {
			if old := byTitle[strings.ToLower(strings.TrimSpace(m[1]))]; old != nil && old != n && old.get("retired") == "" {
				why[old.Path] = "superseded by [[" + n.name() + "]]"
			}
		}
	}
	var out []Retire
	for path, w := range why {
		n := notes[path]
		n.set("retired", day+" · "+w)
		n.Body = fmt.Sprintf("> Retired %s: %s. Kept for history.\n\n%s", day, w, n.Body)
		out = append(out, Retire{Path: path, Why: w})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// unnamed drops the lines of a body the pass writes from the record that
// name a hidden project (a sweep's report, a person's line in the lead chat,
// a shared task's title that mentions one). The kb module refuses a pass
// whose text names a hidden project it didn't before, the same way: as a
// whole word, case and all.
func unnamed(body string, hidden map[string]bool) string {
	var res []*regexp.Regexp
	for h := range hidden {
		res = append(res, regexp.MustCompile(`\b`+regexp.QuoteMeta(h)+`\b`))
	}
	if len(res) == 0 {
		return body
	}
	lines := strings.Split(body, "\n")
	out := lines[:0]
	for _, l := range lines {
		if !slices.ContainsFunc(res, func(re *regexp.Regexp) bool { return re.MatchString(l) }) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}
