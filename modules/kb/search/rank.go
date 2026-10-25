package main

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// Query is what a search looks for: words (stems), the words they stand for,
// and the entities it names.
type Query struct {
	Text  string
	Terms []string
	Near  map[string]float64 // a related word, and how much it counts against the word itself
	Ents  []string
	Said  map[string]string // a stem as the question wrote it
}

// concepts are words the Hive's record says in other words: a question about
// a revert finds a rollback, one about a block finds what waits or is held.
// Each line is one group.
var concepts = [][]string{
	{"revert", "rollback", "undo"},
	{"block", "stuck", "wait", "hold", "held", "stall"},
	{"merge", "land", "kept", "keep"},
	{"security", "perms", "panel", "seat", "architect"},
	{"skip", "without", "miss", "bypass"},
	{"fail", "red", "broke", "error", "failure"},
	{"reason", "because", "cause"},
	{"decide", "decision", "chose", "verdict", "answer", "approve", "decline"},
	{"person", "owner", "ihsan"},
	{"warden", "tribunal", "risk", "request"},
	{"model", "harness", "trial", "route"},
	{"bug", "fix", "broken"},
	{"slow", "stall", "hung", "hang"},
	{"test", "check", "ci"},
	{"gate", "candidate"},
}

var near = map[string][]string{}

func init() {
	for _, g := range concepts {
		for _, a := range g {
			for _, b := range g {
				if a, b = stem(a), stem(b); a != b {
					near[a] = append(near[a], b)
				}
			}
		}
	}
}

// ParseQuery reads the words and entities of a question. A bare number after
// "pr" or "issue", and "#n", name a PR or issue; ids, commits, files and tests
// are named as written.
func ParseQuery(text string, ix *Index) Query {
	q := Query{Text: text, Near: map[string]float64{}, Said: map[string]string{}}
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if t := words(w); len(t) == 1 && q.Said[t[0]] == "" {
			q.Said[t[0]] = w
		}
	}
	for e := range entities(text) {
		if strings.HasPrefix(e, "file:") && !strings.Contains(e, ".") && !strings.Contains(e, "/") {
			continue
		}
		q.Ents = append(q.Ents, e)
	}
	low := strings.ToLower(text)
	for _, w := range strings.Fields(low) {
		w = strings.Trim(w, `"'.,?!:;()`)
		if ix != nil && ix.home("project:"+w) {
			q.Ents = append(q.Ents, "project:"+w)
		}
	}
	seen := map[string]bool{}
	for _, t := range words(text) {
		if digits(t) && slices.Contains(q.Ents, "#"+t) || (t == "pr" || t == "issue") && len(q.Ents) > 0 {
			continue // a PR's number is an entity here, not a word
		}
		if !seen[t] {
			seen[t] = true
			q.Terms = append(q.Terms, t)
		}
	}
	for _, t := range q.Terms {
		for _, n := range near[t] {
			if !seen[n] {
				q.Near[n] = max(q.Near[n], 0.35)
			}
		}
	}
	sort.Strings(q.Ents)
	return q
}

// Hit is one document a search returns, with why it matched.
type Hit struct {
	Doc     *Doc
	Score   float64
	Words   []string // query words found here
	Ents    []string // query entities named here
	Via     string   // a connection that brought it in: "<entity> from <file>"
	Entries []int    // the entries that match best, best first
	Also    []string // files folded into this one: the same task's other reviews
	direct  float64
}

const (
	k1 = 1.2
	b  = 0.75
)

func (ix *Index) home(e string) bool {
	for _, i := range ix.ByEnt[e] {
		if ix.Docs[i].Home[e] {
			return true
		}
	}
	return false
}

func (ix *Index) idf(t string) float64 {
	n, df := float64(len(ix.Docs)), float64(ix.DF[t])
	return math.Log(1 + (n-df+0.5)/(df+0.5))
}

func (ix *Index) entIDF(e string) float64 {
	n, df := float64(len(ix.Docs)), float64(ix.EntDF[e])
	return math.Log(1 + (n-df+0.5)/(df+0.5))
}

func bm25(tf, length, avg, idf float64) float64 {
	return idf * tf * (k1 + 1) / (tf + k1*(1-b+b*length/avg))
}

// Search ranks the documents for q: words by BM25 over the file and over its
// best entry, entities by whether the file is about one or names it, then
// connections: the files that share a rare entity or a link with the best
// direct hits are pulled up behind them.
func (ix *Index) Search(q Query) []*Hit {
	hits := map[int]*Hit{}
	get := func(i int) *Hit {
		if hits[i] == nil {
			hits[i] = &Hit{Doc: ix.Docs[i]}
		}
		return hits[i]
	}
	terms := map[string]float64{}
	for _, t := range q.Terms {
		terms[t] = 1
	}
	for t, w := range q.Near {
		terms[t] = w
	}
	for i, d := range ix.Docs {
		s := 0.0
		var found []string
		for t, w := range terms {
			if tf := d.Terms[t]; tf > 0 {
				s += 0.4 * w * bm25(float64(tf), float64(d.Len), ix.AvgLen, ix.idf(t))
				if w == 1 {
					found = append(found, t)
				}
			}
		}
		// A word the question asks for counts only next to another, unless
		// it is all the question asks.
		if len(found) == 0 || (len(found) == 1 && len(q.Terms) > 2 && len(q.Ents) == 0 && ix.DF[found[0]] > len(ix.Docs)/4) {
			continue
		}
		h := get(i)
		sort.Strings(found)
		h.Words = found
		h.direct += s * d.Prior
	}
	for _, e := range q.Ents {
		idf := ix.entIDF(e)
		for _, i := range ix.ByEnt[e] {
			d := ix.Docs[i]
			h := get(i)
			w := 4.0
			if d.Home[e] {
				w = 30 // the file is the thing the question names
			}
			h.direct += w * idf / 4 * (1 + math.Log(float64(d.Ents[e]))/4)
			h.Ents = append(h.Ents, e)
		}
	}
	// Entries: the best entry of a record file says more than words spread
	// over a long chat. Only the strongest files are read entry by entry.
	cand := ranked(hits)
	for _, h := range cand[:min(len(cand), 80)] {
		// The best of thousands of entries is a better match by chance alone,
		// so a file's best entry counts for less the more entries it has.
		best := ix.entries(h, terms, q.Ents)
		h.direct += best * h.Doc.Prior / (1 + 0.2*math.Log(1+float64(len(h.Doc.Entries))))
	}
	for _, h := range hits {
		h.Score = h.direct
	}
	ix.connect(hits, get)
	return fold(ranked(hits))
}

// fold keeps one reviewer run per task: a task's reviews are near copies of
// one another (the same brief, a read per head), so the best stands for the
// rest, which it lists.
func fold(hits []*Hit) []*Hit {
	out := hits[:0:0]
	first := map[string]*Hit{}
	for _, h := range hits {
		name := strings.TrimSuffix(h.Doc.Path, ".md")
		if rest, ok := strings.CutPrefix(name, "record/run/r-"); ok {
			if i := idAt(rest); i == 0 {
				key := rest[:24]
				if f := first[key]; f != nil {
					f.Also = append(f.Also, h.Doc.Path)
					continue
				}
				first[key] = h
			}
		}
		out = append(out, h)
	}
	return out
}

// entries scores h's entries and keeps the best few; it returns the best one's
// score. A long record file's entries are scored as short documents.
func (ix *Index) entries(h *Hit, terms map[string]float64, ents []string) float64 {
	type es struct {
		i int
		s float64
	}
	var all []es
	avg := 120.0
	for i, e := range h.Doc.Entries {
		e = withoutLog(content(e, ix.Boiler))
		low := strings.ToLower(e)
		var s float64
		tf := map[string]int{}
		n := 0
		for _, w := range words(e) {
			tf[w]++
			n++
		}
		for t, w := range terms {
			if c := tf[t]; c > 0 {
				s += w * bm25(float64(c), float64(n), avg, ix.idf(t))
			}
		}
		if len(ents) > 0 {
			has := entities(e)
			for _, en := range ents {
				if has[en] > 0 || strings.Contains(low, strings.ToLower(entValue(en))) {
					s += 2 * ix.entIDF(en)
				}
			}
		}
		if s > 0 {
			all = append(all, es{i, s})
		}
	}
	// Newest first among equals: the latest state of a thing is its truth.
	sort.SliceStable(all, func(a, b int) bool {
		if all[a].s != all[b].s {
			return all[a].s > all[b].s
		}
		return all[a].i > all[b].i
	})
	for _, e := range all[:min(len(all), 2)] {
		h.Entries = append(h.Entries, e.i)
	}
	if len(all) == 0 {
		return 0
	}
	return all[0].s
}

// withoutLog drops a task's log line, which repeats its whole history at
// every save, so the entry that says a thing outranks every later one.
func withoutLog(e string) string {
	if !strings.Contains(e, "\n- log: ") {
		return e
	}
	var b strings.Builder
	for _, l := range strings.Split(e, "\n") {
		if !strings.HasPrefix(l, "- log: ") {
			b.WriteString(l + "\n")
		}
	}
	return b.String()
}

// entValue is what an entity looks like in text.
func entValue(e string) string {
	_, v, _ := strings.Cut(e, ":")
	if strings.HasPrefix(e, "#") {
		return e
	}
	return v
}

// connect pulls up what the best direct hits are tied to: a file about an
// entity a seed names (a task's run, a run's task, a PR's task), a file that
// shares a rare entity with it, and the notes it links to or that link to it.
// A connection counts for less than the seed it hangs from and less for an
// entity many files share.
func (ix *Index) connect(hits map[int]*Hit, get func(int) *Hit) {
	seeds := ranked(hits)
	seeds = seeds[:min(len(seeds), 6)]
	if len(seeds) == 0 {
		return
	}
	top := seeds[0].direct
	// A file tied to the seeds more than one way gets its best tie and half
	// of each of the others.
	best, rest := map[int]float64{}, map[int]float64{}
	via := map[int]string{}
	add := func(i int, v float64, why string) {
		if v > best[i] {
			rest[i] += best[i]
			best[i], via[i] = v, why
		} else {
			rest[i] += v
		}
	}
	for _, s := range seeds {
		if s.direct < top/4 {
			break
		}
		self := ix.index(s.Doc)
		for e, n := range s.Doc.Ents {
			df := ix.EntDF[e]
			if df < 2 || df > 30 || strings.HasPrefix(e, "project:") || strings.HasPrefix(e, "module:") || (strings.HasPrefix(e, "file:") && !s.Doc.Home[e] && n < 2) {
				continue
			}
			for _, i := range ix.ByEnt[e] {
				if i == self {
					continue
				}
				d := ix.Docs[i]
				w, spread := 0.25, math.Sqrt(float64(df))
				switch {
				case d.Home[e] && slices.Contains(s.Doc.Points, e) || s.Doc.Home[e] && slices.Contains(d.Points, e):
					w, spread = 1, math.Pow(float64(df), 0.35) // a revert or follow-up of the seed, or the seed's own subject
				case d.Home[e] || s.Doc.Home[e]:
					w = 0.5 // one of the two is about it
				}
				add(i, w*s.direct/spread, fmt.Sprintf("%s, from %s", e, s.Doc.Path))
			}
		}
		for _, l := range s.Doc.Links {
			if i, ok := ix.ByNote[l]; ok && i != self {
				add(i, 0.4*s.direct, "linked from "+s.Doc.Path)
			}
		}
		if s.Doc.Kind == "note" || s.Doc.Kind == "practice" {
			name := strings.TrimSuffix(s.Doc.Path[strings.LastIndex(s.Doc.Path, "/")+1:], ".md")
			for _, i := range ix.ByEnt["note:"+name] {
				if i != self {
					add(i, 0.3*s.direct, "links to "+s.Doc.Path)
				}
			}
		}
	}
	for i, v := range best {
		v += rest[i] / 2
		h := get(i)
		h.Score += min(v, 0.6*top)
		if h.direct < v {
			h.Via = via[i]
		}
	}
}

func (ix *Index) index(d *Doc) int {
	return sort.Search(len(ix.Docs), func(i int) bool { return ix.Docs[i].Path >= d.Path })
}

func ranked(hits map[int]*Hit) []*Hit {
	out := make([]*Hit, 0, len(hits))
	for _, h := range hits {
		if h.Score > 0 || h.direct > 0 {
			out = append(out, h)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		si, sj := max(out[i].Score, out[i].direct), max(out[j].Score, out[j].direct)
		if si != sj {
			return si > sj
		}
		if out[i].Doc.When != out[j].Doc.When {
			return out[i].Doc.When > out[j].Doc.When
		}
		return out[i].Doc.Path < out[j].Doc.Path
	})
	return out
}
