package kb

import (
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
)

// Searching ranks the files of the knowledge base, not its words: each file
// once, by the entries that match the question best (BM25 over the notes and
// the record's entries, so a rare word counts for more than a common one),
// with the files it is connected to. Two files are connected when one names
// the other: a task's record names its runs and its PR, a run names its task,
// a note links others ([[name]], as the dream's Related lists do). The query's
// own ids and PR numbers pick the files they are about, and a file the best
// hits name gets a share of their score, so the task behind a run that matched
// comes up even when its own words don't. Each hit says why it matched.

// Hit is one file of the knowledge base that matches a search.
type Hit struct {
	File   string   `json:"file"` // relative to the knowledge base, with slashes
	Label  string   `json:"label,omitempty"`
	When   string   `json:"when"`
	Text   string   `json:"text"`             // its best entry: a whole note, or one entry of the record
	More   []string `json:"more,omitempty"`   // other entries holding words the best one lacks
	Others int      `json:"others,omitempty"` // further entries that match
	Why    []string `json:"why,omitempty"`    // the words it matched, the ids it is about, the hits that name it
	Links  []string `json:"links,omitempty"`  // the knowledge base files it names
	score  float64
	tier   int // 0 a living note the dream made, 1 any other, 2 a retired note
}

const (
	bm25K = 1.2
	bm25B = 0.75
	// spread is the share of a hit's score that goes to each file it names,
	// divided by the root of how many it names, so a chat that names every
	// task lends each little.
	spread = 0.5
	// noteWeight lifts notes and practice over the record's entries: an
	// agent wrote them to be found.
	noteWeight = 1.3
)

var stopWords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`a an and are as at be been but by did do does done for from had has have how i if in into is it its me my no not of on or our so that the their them then there these they this to was we were what when where which who whom why will with you your has get got any all can could should would`) {
		stopWords[w] = true
	}
}

// tokens splits lowered text into its runs of letters and digits.
func tokens(s string, fn func(tok string)) {
	start := -1
	for i, r := range s {
		word := unicode.IsLetter(r) || unicode.IsDigit(r)
		if word && start < 0 {
			start = i
		} else if !word && start >= 0 {
			fn(s[start:i])
			start = -1
		}
	}
	if start >= 0 {
		fn(s[start:])
	}
}

// stem cuts a common ending, so "reverted" finds "revert" and "reverts".
func stem(w string) string {
	for _, suf := range []string{"ing", "ed", "es", "s"} {
		if len(w)-len(suf) >= 4 && strings.HasSuffix(w, suf) {
			return w[:len(w)-len(suf)]
		}
	}
	return w
}

// matches says whether token tok is an instance of term t (a stem): the
// same word, or one that begins with it and runs at most a few letters on.
func matches(tok, t string) bool {
	if len(t) < 4 || unicode.IsDigit(rune(t[0])) {
		return tok == t
	}
	return strings.HasPrefix(tok, t) && len(tok) <= len(t)+4
}

var (
	idRe   = regexp.MustCompile(`[a-z0-9][a-z0-9._-]{6,}[a-z0-9]`)
	prRe   = regexp.MustCompile(`(?:#|\bpr\s*#?|\bpull/|\bissue\s*#?|\.number: )(\d{1,6})\b`)
	linkRe = regexp.MustCompile(`\[\[([^\]\n]+)\]\]`)
	taskRe = regexp.MustCompile(`\b\d{6}-\d{6}-[0-9a-f]{10}\b`)
)

// graph is how the knowledge base's files name each other: a record file
// by its name (a task, a run, a request), a note by its name in [[links]],
// and a task by its PR's and issue's numbers.
type graph struct {
	ids   map[string]string   // a record file's name (one with a digit in it) -> its file
	notes map[string]string   // a note's name -> its file
	prs   map[string][]string // a PR or issue number -> the task files that have it
}

func (g *graph) add(rel string, prs []string) {
	name := strings.ToLower(strings.TrimSuffix(path.Base(rel), ".md"))
	if strings.HasPrefix(rel, "record/") {
		if strings.IndexFunc(name, unicode.IsDigit) >= 0 && len(name) >= 8 {
			g.ids[name] = rel
		}
		for _, n := range prs {
			if !slices.Contains(g.prs[n], rel) {
				g.prs[n] = append(g.prs[n], rel)
			}
		}
		return
	}
	if _, taken := g.notes[name]; !taken || !strings.Contains(rel, "/") {
		g.notes[name] = rel
	}
}

// prNumbers are the PR and issue numbers a task's record gives it.
func prNumbers(rel, raw string) []string {
	if !strings.HasPrefix(rel, "record/task/") {
		return nil
	}
	var out []string
	for _, key := range []string{"\n- pr.number: ", "\n- issue.number: "} {
		for rest := raw; ; {
			i := strings.Index(rest, key)
			if i < 0 {
				break
			}
			rest = rest[i+len(key):]
			n, _, _ := strings.Cut(rest, "\n")
			if !slices.Contains(out, n) {
				out = append(out, n)
			}
		}
	}
	return out
}

// names returns the files text names, other than self, in the order named.
func (g *graph) names(text, self string) []string {
	var out []string
	g.each(text, self, func(rel, _ string) { out = append(out, rel) })
	return out
}

// each calls fn on each file text names, other than self, once, with the
// words that name it: an id, a [[link]], a #PR.
func (g *graph) each(text, self string, fn func(rel, via string)) {
	seen := map[string]bool{self: true}
	add := func(rel string, ok bool, via string) {
		if ok && !seen[rel] {
			seen[rel] = true
			fn(rel, via)
		}
	}
	low := strings.ToLower(text)
	for _, m := range linkRe.FindAllStringSubmatch(low, -1) {
		rel, ok := g.notes[strings.TrimSpace(m[1])]
		add(rel, ok, m[0])
	}
	for _, m := range idRe.FindAllString(low, -1) {
		rel, ok := g.ids[m]
		add(rel, ok, m)
		// A run's name holds its task's id (w-<task>, r-<task>-<sha>-0).
		if t := taskRe.FindString(m); t != "" && t != m {
			rel, ok := g.ids[t]
			add(rel, ok, t)
		}
	}
	for _, m := range prRe.FindAllStringSubmatch(low, -1) {
		for _, rel := range g.prs[m[1]] {
			add(rel, true, "#"+m[1])
		}
	}
}

type piece struct {
	text  string
	tf    []int
	size  int
	score float64
}

type kbFile struct {
	rel    string
	head   string // a note whole; a record file's first entry
	pieces []*piece
	ok     []bool // the terms anywhere in the file
}

// scoreFile counts each term in each entry of the file at rel (a whole
// note, or each of the record's entries), and returns the file with the
// entries that hold any (nil when none does) and how many entries it has.
func scoreFile(rel, raw string, terms []string) (*kbFile, int) {
	ps := []string{raw}
	head := raw
	if strings.HasPrefix(rel, "record/") {
		ps = strings.Split(raw, "\n### ")[1:]
		head = firstEntry(raw)
	}
	low := strings.ToLower(raw)
	if !slices.ContainsFunc(terms, func(t string) bool { return strings.Contains(low, t) }) {
		return nil, max(len(ps), 1)
	}
	dc := &kbFile{rel: rel, head: head, ok: make([]bool, len(terms))}
	for _, text := range ps {
		pc := &piece{text: text, tf: make([]int, len(terms)), size: len(text)}
		hit := false
		tokens(strings.ToLower(text), func(tok string) {
			for i, t := range terms {
				if matches(tok, t) {
					pc.tf[i]++
					dc.ok[i] = true
					hit = true
				}
			}
		})
		if hit {
			dc.pieces = append(dc.pieces, pc)
		}
	}
	if len(dc.pieces) == 0 {
		return nil, max(len(ps), 1)
	}
	return dc, max(len(ps), 1)
}

// Search returns the files of the knowledge base under dir that answer
// query best, each once, skipping the folders in hide (relative to dir). A
// living note the dream consolidated ranks first when it matches at least
// half as well as the best hit, and a retired note ranks last.
func Search(dir, query string, hide ...string) []Hit {
	low := strings.ToLower(query)
	var terms, words, said []string
	seen := map[string]bool{}
	tokens(low, func(w string) {
		if !seen[w] && (len(w) > 1 || unicode.IsDigit(rune(w[0]))) {
			seen[w] = true
			words = append(words, w)
		}
	})
	for _, w := range words {
		if !stopWords[w] {
			terms, said = append(terms, stem(w)), append(said, w)
		}
	}
	if len(terms) == 0 {
		terms, said = words, words
	}
	if len(terms) == 0 {
		return nil
	}
	g := &graph{ids: map[string]string{}, notes: map[string]string{}, prs: map[string][]string{}}
	var rels []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable folder has nothing to give; go on with the rest
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		// A dot folder is git's or a copy SeedSelf is swapping in, never notes.
		if d.IsDir() && p != dir && (strings.HasPrefix(d.Name(), ".") || slices.Contains(hide, rel)) {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(p, ".md") {
			rels = append(rels, rel)
		}
		return nil
	})
	// The files are read and scored on every core; the record is most of
	// the knowledge base and grows with every event.
	type read struct {
		ok          bool
		dc          *kbFile
		pieces, len int
		prs         []string
	}
	results := make([]read, len(rels))
	var next atomic.Int64
	var wg sync.WaitGroup
	for range min(runtime.GOMAXPROCS(0), 8) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := int(next.Add(1)) - 1; i < len(rels); i = int(next.Add(1)) - 1 {
				results[i] = func() read {
					b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rels[i])))
					if err != nil {
						return read{}
					}
					dc, pieces := scoreFile(rels[i], string(b), terms)
					return read{ok: true, dc: dc, pieces: pieces, len: len(b), prs: prNumbers(rels[i], string(b))}
				}()
			}
		}()
	}
	wg.Wait()
	var docs []*kbFile
	var all, size int
	for i, r := range results {
		if !r.ok {
			continue
		}
		g.add(rels[i], r.prs)
		all += r.pieces
		size += r.len
		if r.dc != nil {
			docs = append(docs, r.dc)
		}
	}
	if len(docs) == 0 {
		return nil
	}
	// Each term's weight: rarer across the entries counts for more.
	df := make([]int, len(terms))
	for _, dc := range docs {
		for _, pc := range dc.pieces {
			for i, n := range pc.tf {
				if n > 0 {
					df[i]++
				}
			}
		}
	}
	idf := make([]float64, len(terms))
	for i := range terms {
		idf[i] = math.Log(1 + (float64(all)-float64(df[i])+0.5)/(float64(df[i])+0.5))
	}
	avg := float64(size) / float64(max(all, 1))
	about := map[string]string{} // the files the query's own ids and numbers are about
	g.each(query, "", func(rel, via string) { about[rel] = "about " + via })
	byFile := map[string]*Hit{}
	var hits []*Hit
	for _, dc := range docs {
		for _, pc := range dc.pieces {
			for i, n := range pc.tf {
				if n > 0 {
					f := float64(n)
					pc.score += idf[i] * f * (bm25K + 1) / (f + bm25K*(1-bm25B+bm25B*float64(pc.size)/avg))
				}
			}
		}
		// The best entry, the newest among near equals (the record's last):
		// a few characters more don't make an older state the answer.
		best := dc.pieces[0]
		for _, pc := range dc.pieces[1:] {
			if pc.score >= 0.95*best.score {
				best = pc
			}
		}
		h := &Hit{File: dc.rel, Text: strings.TrimSpace(best.text), score: best.score, tier: 1}
		// A word the best entry lacks, found elsewhere in the file (an
		// earlier state of the same task), counts for half, and the entry
		// that holds it is shown with it.
		have := slices.Clone(best.tf)
		var matched []string
		for i := range terms {
			if best.tf[i] > 0 {
				matched = append(matched, said[i])
			} else if dc.ok[i] {
				h.score += idf[i] / 2
				matched = append(matched, said[i])
			}
		}
		for j := len(dc.pieces) - 1; j >= 0; j-- {
			pc := dc.pieces[j]
			if pc == best {
				continue
			}
			adds := false
			for i, n := range pc.tf {
				if n > 0 && have[i] == 0 {
					adds = true
					have[i] = n
				}
			}
			if adds && len(h.More) < 2 {
				h.More = append(h.More, strings.TrimSpace(pc.text))
			} else {
				h.Others++
			}
		}
		// A file holding all of the question's words beats one holding a
		// few of them often.
		h.score *= 0.5 + 0.5*float64(len(matched))/float64(len(terms))
		if !strings.HasPrefix(dc.rel, "record/") {
			h.score *= noteWeight
			switch head := header(dc.head); {
			case head["retired"] != "":
				h.tier = 2
			case head["consolidated"] != "":
				h.tier = 0
			}
		}
		h.Why = []string{"matched " + strings.Join(matched, ", ")}
		h.When = when(dc.rel, h.Text)
		h.Label = labelOf(dc.rel, dc.head)
		var named strings.Builder
		named.WriteString(h.Text)
		for _, m := range h.More {
			named.WriteString("\n" + m)
		}
		if strings.HasPrefix(dc.rel, "record/") {
			// Its first entry says what it is: a task's title, a run's task.
			named.WriteString("\n" + dc.head)
		}
		h.Links = g.names(named.String(), dc.rel)
		byFile[dc.rel] = h
		hits = append(hits, h)
	}
	top := 0.0
	for _, h := range hits {
		top = max(top, h.score)
	}
	// The files the query names come first.
	for rel, why := range about {
		h := byFile[rel]
		if h == nil {
			h = connected(dir, rel, g)
			if h == nil {
				continue
			}
			byFile[rel] = h
			hits = append(hits, h)
		}
		h.score += top
		h.Why = append([]string{why}, h.Why...)
	}
	// A share of the best hits' score goes to the files they name.
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	var lent []*Hit
	for _, h := range hits[:min(len(hits), 15)] {
		if len(h.Links) == 0 {
			continue
		}
		share := spread * h.score / math.Sqrt(float64(len(h.Links)))
		for _, rel := range h.Links {
			to := byFile[rel]
			if to == nil {
				if hidden(rel, hide) {
					continue
				}
				if to = connected(dir, rel, g); to == nil {
					continue
				}
				byFile[rel] = to
				lent = append(lent, to)
			}
			to.score += share
			if len(to.Why) < 4 {
				to.Why = append(to.Why, "named by "+h.File)
			}
		}
	}
	hits = append(hits, lent...)
	best := 0.0
	for _, h := range hits {
		best = max(best, h.score)
	}
	for _, h := range hits {
		if h.tier == 0 && 2*h.score < best {
			h.tier = 1
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].tier != hits[j].tier {
			return hits[i].tier < hits[j].tier
		}
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].When > hits[j].When
	})
	out := make([]Hit, len(hits))
	for i, h := range hits {
		out[i] = *h
	}
	return out
}

// hidden says whether rel sits in one of the folders hide names.
func hidden(rel string, hide []string) bool {
	for _, h := range hide {
		if rel == h || strings.HasPrefix(rel, h+"/") {
			return true
		}
	}
	return false
}

// connected is a hit for a file that matched none of the question's words
// but is named by one that did, or by the question: its newest entry.
func connected(dir, rel string, g *graph) *Hit {
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return nil
	}
	raw := string(b)
	text := raw
	if strings.HasPrefix(rel, "record/") {
		ps := strings.Split(raw, "\n### ")
		text = ps[len(ps)-1]
	}
	head := raw
	if strings.HasPrefix(rel, "record/") {
		head = firstEntry(raw)
	}
	h := &Hit{File: rel, Text: strings.TrimSpace(text), Label: labelOf(rel, head), tier: 1}
	h.When = when(rel, h.Text)
	h.Links = g.names(h.Text+"\n"+head, rel)
	return h
}

// firstEntry is a record file's first entry, or "" for a note.
func firstEntry(raw string) string {
	ps := strings.SplitN(raw, "\n### ", 3)
	if len(ps) < 2 {
		return ""
	}
	return ps[1]
}

// when is the date a note's header gives or the time a record entry begins with.
func when(rel, text string) string {
	if !strings.HasPrefix(rel, "record/") {
		if i := strings.Index(text, "date: "); i >= 0 {
			d, _, _ := strings.Cut(text[i+6:], "\n")
			return d
		}
		return ""
	}
	if len(text) >= 16 {
		return text[:16]
	}
	return ""
}

// labelOf says what a file is, from its head (a note whole, a record
// file's first entry): a note's title, a task's title, a run's role.
func labelOf(rel, head string) string {
	if !strings.HasPrefix(rel, "record/") {
		return header(head)["title"]
	}
	first := "\n" + head
	for _, k := range []string{"title", "role", "text", "brief"} {
		if i := strings.Index(first, "\n- "+k+": "); i >= 0 {
			v, _, _ := strings.Cut(first[i+len(k)+5:], "\n")
			if r := []rune(v); len(r) > 90 {
				v = string(r[:90]) + "…"
			}
			return k + ": " + v
		}
	}
	return ""
}

// excerpt is what Find shows of an entry: all of a short one; of a long
// one, its first line and the lines that hold the question's words, each
// cut to the part around the word, up to about room characters.
func excerpt(text string, terms []string, room int) string {
	if len(text) <= room {
		return text
	}
	lines := strings.Split(text, "\n")
	keep := map[int]bool{0: true}
	if strings.HasPrefix(text, "---\n") {
		keep[0] = false // a note's header: its title is in the hit's label
		if _, body, ok := strings.Cut(text[4:], "\n---\n"); ok {
			lines = strings.Split(strings.TrimSpace(body), "\n")
			keep = map[int]bool{0: true}
		}
	}
	hasTerm := func(l string) bool {
		found := false
		tokens(strings.ToLower(l), func(tok string) {
			for _, t := range terms {
				found = found || matches(tok, t)
			}
		})
		return found
	}
	for i, l := range lines {
		if hasTerm(l) {
			keep[i] = true
		}
	}
	var b strings.Builder
	last := -1
	for i, l := range lines {
		if !keep[i] {
			continue
		}
		if b.Len() > room {
			b.WriteString("  …\n")
			break
		}
		if last >= 0 && i > last+1 {
			b.WriteString("  …\n")
		}
		b.WriteString(around(l, terms, 280) + "\n")
		last = i
	}
	return strings.TrimRight(b.String(), "\n")
}

// around cuts a long line to about n characters around its first matching word.
func around(l string, terms []string, n int) string {
	r := []rune(l)
	if len(r) <= n {
		return l
	}
	at := -1
	low := strings.ToLower(l)
	for _, t := range terms {
		if i := strings.Index(low, t); i >= 0 && (at < 0 || i < at) {
			at = i
		}
	}
	at = len([]rune(low[:max(at, 0)]))
	from := max(0, min(at-n/3, len(r)-n))
	out := string(r[from : from+n])
	if from > 0 {
		out = "…" + out
	}
	if from+n < len(r) {
		out += "…"
	}
	return out
}

// Find searches the knowledge base under dir and renders the best limit
// files ("all", or a number; 10 when empty) for an agent or a person to
// read: each file once, what it is, why it matched, the part of its best
// entries that holds the words, and the files it names; then the files
// several of them name that it didn't show, and how many more matched.
// hide is as for Search.
func Find(dir, query, limit string, hide ...string) string {
	hits := Search(dir, query, hide...)
	if len(hits) == 0 {
		return "nothing in the knowledge base matches; the record doesn't cover it"
	}
	n, err := strconv.Atoi(limit)
	if limit == "all" || n > len(hits) {
		n = len(hits)
	} else if err != nil || n <= 0 {
		n = min(10, len(hits))
	}
	var terms []string
	tokens(strings.ToLower(query), func(w string) {
		if !stopWords[w] {
			terms = append(terms, stem(w))
		}
	})
	var b strings.Builder
	shown := map[string]bool{}
	for _, h := range hits[:n] {
		shown[h.File] = true
	}
	named := map[string]int{}
	for _, h := range hits[:n] {
		head := "== " + h.File
		if h.Label != "" {
			head += " · " + h.Label
		}
		fmt.Fprintf(&b, "%s\n(%s)\n", head, strings.Join(h.Why, "; "))
		b.WriteString(excerpt(h.Text, terms, 700) + "\n")
		for _, m := range h.More {
			b.WriteString("  ⋯\n" + excerpt(m, terms, 350) + "\n")
		}
		if h.Others > 0 {
			fmt.Fprintf(&b, "  (%d more entries here match; read %s)\n", h.Others, h.File)
		} else if len(h.Text) > 700 {
			fmt.Fprintf(&b, "  (clipped; the rest is in %s)\n", h.File)
		}
		if len(h.Links) > 0 {
			links := h.Links[:min(len(h.Links), 6)]
			more := ""
			if len(h.Links) > len(links) {
				more = fmt.Sprintf(" and %d more", len(h.Links)-len(links))
			}
			fmt.Fprintf(&b, "  names: %s%s\n", strings.Join(links, ", "), more)
		}
		for _, l := range h.Links {
			if !shown[l] {
				named[l]++
			}
		}
		b.WriteString("\n")
	}
	var also []string
	for l, k := range named {
		if k >= 2 {
			also = append(also, l)
		}
	}
	sort.Slice(also, func(i, j int) bool {
		if named[also[i]] != named[also[j]] {
			return named[also[i]] > named[also[j]]
		}
		return also[i] < also[j]
	})
	if len(also) > 0 {
		var parts []string
		for _, l := range also[:min(len(also), 6)] {
			parts = append(parts, fmt.Sprintf("%s (%d)", l, named[l]))
		}
		fmt.Fprintf(&b, "Also named by several of these: %s\n", strings.Join(parts, ", "))
	}
	if more := len(hits) - n; more > 0 {
		fmt.Fprintf(&b, "%d more files match; ask again with --limit all (or a bigger number) to see them.", more)
	}
	return strings.TrimSpace(b.String())
}

// header reads a note's "---" header into its fields.
func header(note string) map[string]string {
	out := map[string]string{}
	rest, ok := strings.CutPrefix(note, "---\n")
	if !ok {
		return out
	}
	head, _, _ := strings.Cut(rest, "\n---\n")
	for _, l := range strings.Split(head, "\n") {
		if k, v, ok := strings.Cut(l, ":"); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}
