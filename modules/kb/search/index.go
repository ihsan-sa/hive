package main

import (
	"hash/fnv"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// A Doc is one note or one record file: a search returns each at most once.
type Doc struct {
	Path    string // relative to the knowledge base, with slashes
	Kind    string // note, practice, record or source (the Hive's own copy under hive/)
	Title   string
	When    string
	Text    string
	Entries []string       // a record file's entries, oldest first; a note is one entry
	Terms   map[string]int // stemmed words, title and path included
	Len     int
	Ents    map[string]int // entities named, with how often
	Home    map[string]bool
	Points  []string // entities of the subjects a record file is of: a revert's task, a follow-up's
	Links   []string // [[note]] links, the dream pass's Related list included
	Related []string // the Related list alone
	Prior   float64  // how much a match here counts against the same match elsewhere
}

// Index is the whole knowledge base, read for one search.
type Index struct {
	Docs   []*Doc
	DF     map[string]int // documents a term is in
	EntDF  map[string]int // documents an entity is in
	ByEnt  map[string][]int
	ByNote map[string]int // note name (file name without .md) to its doc
	AvgLen float64
	Boiler map[uint64]bool // lines too many files repeat to say anything
}

var linkRe = regexp.MustCompile(`\[\[([A-Za-z0-9._-]+)\]\]`)

// modules is what "X module" may name; anything else is a plain word.
var modules = map[string]bool{"agents": true, "chat": true, "kb": true, "improve": true, "system": true, "risk": true, "work": true,
	"runner": true, "release": true, "tools": true, "feedback": true, "models": true, "report": true, "stats": true, "projects": true, "modules": true}

var roots = []string{"internal/", "cmd/", "docs/", "modules/", "tests/", "install/", "site/", ".hive/"}

var exts = []string{".go", ".md", ".json", ".sh", ".ps1", ".yml", ".yaml", ".toml", ".txt"}

// entities finds the things that tie notes together: ids of tasks, requests
// and runs, PR and issue numbers, commits, files, tests, notes, modules and
// projects. Each is "kind:value". It reads the text a word at a time, since
// a regular expression per kind over the whole record is too slow to run
// for every search.
func entities(text string) map[string]int {
	out := map[string]int{}
	prev := ""
	for _, w := range strings.FieldsFunc(text, func(r rune) bool {
		return !(r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_./#:-", r)))
	}) {
		w = strings.TrimRight(w, ".:,-/")
		w = strings.TrimLeft(w, ".:-/")
		entity(w, prev, out)
		prev = strings.ToLower(w)
	}
	for rest := text; ; {
		i := strings.Index(rest, "[[")
		if i < 0 {
			break
		}
		rest = rest[i+2:]
		if j := strings.Index(rest, "]]"); j > 0 && j < 100 && linkRe.MatchString("[["+rest[:j+2]) {
			out["note:"+rest[:j]]++
		}
	}
	return out
}

func entity(w, prev string, out map[string]int) {
	if w == "" {
		return
	}
	low := strings.ToLower(w)
	switch prev {
	case "pr", "issue", "pr.number:", "issue.number:", "pr.number", "issue.number":
		if digits(w) && len(w) <= 5 {
			out["#"+strings.TrimLeft(w, "0")]++
			return
		}
	case "project:":
		out["project:"+low]++
	case "module":
		if modules[low] {
			out["module:"+low]++
		}
	}
	if low == "module" && modules[prev] {
		out["module:"+prev]++
	}
	if strings.HasPrefix(w, "#") {
		if n := w[1:]; digits(n) && len(n) <= 5 {
			out["#"+strings.TrimLeft(n, "0")]++
		}
		return
	}
	for _, k := range []string{"/pull/", "/issues/"} {
		if i := strings.Index(w, k); i >= 0 {
			n := w[i+len(k):]
			if j := strings.IndexFunc(n, func(r rune) bool { return r < '0' || r > '9' }); j >= 0 {
				n = n[:j]
			}
			if n != "" && len(n) <= 5 {
				out["#"+strings.TrimLeft(n, "0")]++
			}
			return
		}
	}
	if i := idAt(w); i >= 0 {
		out["id:"+w[i:i+24]]++
		if r := runID(w, i); r != "" {
			out["run:"+r]++
		}
		return
	}
	if strings.HasPrefix(w, "gate-") || strings.HasPrefix(w, "lead-") {
		if p := strings.Split(w, "-"); len(p) == 3 && len(p[2]) >= 8 && hex(p[2]) {
			out["run:"+w]++
			return
		}
	}
	if len(w) >= 7 && len(w) <= 40 && hex(w) && strings.ContainsAny(w, "abcdef") && strings.ContainsAny(w, "0123456789") {
		out["commit:"+w[:7]]++
		return
	}
	if strings.HasPrefix(w, "Test") && len(w) > 5 && w[4] >= 'A' && w[4] <= 'Z' && !strings.ContainsAny(w, "./:#") {
		out["test:"+w]++
		return
	}
	for _, r := range roots {
		if strings.HasPrefix(w, r) && len(w) > len(r) && !strings.ContainsAny(w, "#:") {
			out["file:"+w]++
			out["file:"+path.Base(w)]++
			for d := path.Dir(w); strings.Contains(d, "/"); d = path.Dir(d) {
				out["file:"+d]++
			}
			if p := strings.Split(w, "/"); len(p) > 2 && (p[0] == "internal" && p[1] == "mod" || p[0] == "modules") {
				m := p[1]
				if p[0] == "internal" {
					m = p[2]
				}
				if modules[m] {
					out["module:"+m]++
				}
			}
			return
		}
	}
	if !strings.ContainsAny(w, "/#:") {
		for _, e := range exts {
			if strings.HasSuffix(low, e) && len(w) > len(e) {
				out["file:"+w]++
				return
			}
		}
	}
}

func digits(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' }) < 0
}

func hex(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool { return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') }) < 0
}

// idAt is where a task or request id (261024-074959-0cdbbee20e) starts in w, or -1.
func idAt(w string) int {
	for i := 0; i+24 <= len(w); i++ {
		if (i == 0 || w[i-1] == '-') && digits(w[i:i+6]) && w[i+6] == '-' && digits(w[i+7:i+13]) && w[i+13] == '-' && hex(w[i+14:i+24]) &&
			(i+24 == len(w) || w[i+24] == '-' || w[i+24] == '.') {
			return i
		}
	}
	return -1
}

// runID is the run id an id at i is part of, as w-<id>, r-<id>-<hex>-<n>,
// sweep-<id>, or "" when w is a bare id.
func runID(w string, i int) string {
	if i == 0 {
		return ""
	}
	pre := w[:i]
	if strings.IndexFunc(pre, func(r rune) bool { return !(r >= 'a' && r <= 'z' || r == '-') }) >= 0 {
		return ""
	}
	end := i + 24
	if p := strings.Split(w[end:], "-"); len(p) >= 3 && p[0] == "" && len(p[1]) == 8 && hex(p[1]) && digits(p[2]) {
		end += 1 + 8 + 1 + len(p[2])
		if len(p) >= 4 && strings.HasPrefix(p[3], "read") && digits(p[3][4:]) {
			end += 1 + len(p[3])
		}
	}
	return w[:end]
}

// Load reads every note and record file under dir, skipping dot folders and
// the folders in hide (relative to dir, with slashes).
func Load(dir string, hide []string) *Index {
	ix := &Index{DF: map[string]int{}, EntDF: map[string]int{}, ByEnt: map[string][]int{}, ByNote: map[string]int{}}
	var paths []string
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if path != dir && (strings.HasPrefix(d.Name(), ".") || hidden(rel, hide)) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(rel, ".md") && d.Type().IsRegular() && !hidden(rel, hide) {
			paths = append(paths, rel)
		}
		return nil
	})
	// Reading is most of a search's time, so the files are read side by side:
	// first every file, counting the files each long line is in, then each
	// file parsed without the lines many files repeat.
	raws := make([]string, len(paths))
	lines := make([]map[uint64]bool, len(paths))
	each(len(paths), func(i int) {
		if raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(paths[i]))); err == nil {
			raws[i], lines[i] = string(raw), lineSet(string(raw))
		}
	})
	count := map[uint64]int{}
	for _, set := range lines {
		for h := range set {
			count[h]++
		}
	}
	ix.Boiler = map[uint64]bool{}
	for h, n := range count {
		if n >= boilerFiles {
			ix.Boiler[h] = true
		}
	}
	docs := make([]*Doc, len(paths))
	each(len(paths), func(i int) {
		if lines[i] != nil {
			docs[i] = parse(paths[i], raws[i], ix.Boiler)
		}
	})
	for _, d := range docs {
		if d != nil {
			ix.Docs = append(ix.Docs, d)
		}
	}
	sort.Slice(ix.Docs, func(i, j int) bool { return ix.Docs[i].Path < ix.Docs[j].Path })
	total := 0
	for i, d := range ix.Docs {
		total += d.Len
		for t := range d.Terms {
			ix.DF[t]++
		}
		for e := range d.Ents {
			ix.EntDF[e]++
			ix.ByEnt[e] = append(ix.ByEnt[e], i)
		}
		if d.Kind == "note" || d.Kind == "practice" {
			ix.ByNote[strings.TrimSuffix(path.Base(d.Path), ".md")] = i
		}
	}
	if len(ix.Docs) > 0 {
		ix.AvgLen = float64(total) / float64(len(ix.Docs))
	}
	return ix
}

// boilerFiles is how many files a line must be in to be boilerplate: the
// lessons every brief quotes, the same instructions in every run. It says
// nothing about the file it is in, so it neither matches nor connects.
const boilerFiles = 15

// each runs fn on 0..n-1 on every core.
func each(n int, fn func(int)) {
	next := make(chan int)
	var wg sync.WaitGroup
	for range max(1, runtime.NumCPU()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				fn(i)
			}
		}()
	}
	for i := range n {
		next <- i
	}
	close(next)
	wg.Wait()
}

func lineHash(l string) (uint64, bool) {
	l = strings.TrimSpace(l)
	if len(l) < 24 {
		return 0, false
	}
	h := fnv.New64a()
	h.Write([]byte(l))
	return h.Sum64(), true
}

func lineSet(raw string) map[uint64]bool {
	out := map[uint64]bool{}
	for _, l := range strings.Split(raw, "\n") {
		if h, ok := lineHash(l); ok {
			out[h] = true
		}
	}
	return out
}

// content is raw without its boilerplate lines.
func content(raw string, boiler map[uint64]bool) string {
	if len(boiler) == 0 {
		return raw
	}
	var b strings.Builder
	for _, l := range strings.Split(raw, "\n") {
		if h, ok := lineHash(l); ok && boiler[h] {
			continue
		}
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String()
}

func hidden(rel string, hide []string) bool {
	for _, h := range hide {
		if h = strings.Trim(h, "/"); h != "" && (rel == h || strings.HasPrefix(rel, h+"/")) {
			return true
		}
	}
	return false
}

func parse(rel, raw string, boiler map[uint64]bool) *Doc {
	d := &Doc{Path: rel, Text: raw, Home: map[string]bool{}, Prior: 1}
	switch {
	case strings.HasPrefix(rel, "record/"):
		d.Kind = "record"
	case strings.HasPrefix(rel, "practice/"):
		d.Kind = "practice"
	case strings.HasPrefix(rel, "hive/"):
		d.Kind, d.Prior = "source", 0.6
	default:
		d.Kind, d.Prior = "note", 1.3
	}
	name := strings.TrimSuffix(path.Base(rel), ".md")
	if d.Kind == "record" {
		head, _, _ := strings.Cut(raw, "\n")
		d.Title = strings.TrimPrefix(head, "# ")
		d.Entries = strings.Split(raw, "\n### ")[1:]
		if n := len(d.Entries); n > 0 {
			d.When = d.Entries[n-1][:min(16, len(d.Entries[n-1]))]
		}
		if t := field(raw, "title"); t != "" {
			d.Title += ": " + t
		}
		// A file is about the subject it is named for.
		if i := idAt(name); i >= 0 {
			d.Home["id:"+name[i:i+24]] = true
		}
		if e := entities(name); e["run:"+name] > 0 {
			d.Home["run:"+name] = true
		}
		// A revert, a follow-up or a request points at the task it is of:
		// as close a tie as being about it.
		for _, k := range []string{"revert_of", "follow_up_of", "ref", "ask"} {
			for _, v := range fields(raw, k) {
				if idAt(v) == 0 && len(v) == 24 {
					d.Points = append(d.Points, "id:"+v)
				}
			}
		}
		if strings.HasPrefix(rel, "record/task/") {
			for _, k := range []string{"pr.number", "issue.number"} {
				for _, v := range fields(raw, k) {
					d.Home["#"+v] = true
				}
			}
		}
		if strings.HasPrefix(rel, "record/project/") {
			d.Home["project:"+name] = true
		}
	} else {
		h := header(raw)
		d.Title, d.When = h["title"], h["date"]
		if d.Title == "" {
			first, _, _ := strings.Cut(strings.TrimSpace(raw), "\n")
			d.Title = strings.TrimLeft(first, "# ")
		}
		switch {
		case h["retired"] != "":
			d.Prior *= 0.5
		case h["consolidated"] != "":
			d.Prior *= 1.2
		}
		d.Entries = []string{raw}
		d.Home["note:"+name] = true
		if _, rel, ok := strings.Cut(raw, "\n## Related\n"); ok {
			for _, m := range linkRe.FindAllStringSubmatch(rel, -1) {
				d.Related = append(d.Related, m[1])
			}
		}
		for _, m := range linkRe.FindAllStringSubmatch(raw, -1) {
			d.Links = append(d.Links, m[1])
		}
	}
	own := content(raw, boiler)
	d.Ents = entities(own)
	for e := range d.Home {
		d.Ents[e]++
	}
	d.Terms = map[string]int{}
	for _, t := range words(own) {
		d.Terms[t]++
		d.Len++
	}
	// The title and the path say what the file is about: they count again.
	for _, t := range words(d.Title + " " + strings.ReplaceAll(rel, "-", " ")) {
		d.Terms[t] += 2
	}
	return d
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

// field is the first "- key: value" line's value in a record file.
func field(raw, key string) string {
	if v := fields(raw, key); len(v) > 0 {
		return v[0]
	}
	return ""
}

func fields(raw, key string) []string {
	var out []string
	for rest := raw; ; {
		i := strings.Index(rest, "\n- "+key+": ")
		if i < 0 {
			return out
		}
		rest = rest[i+len(key)+5:]
		v, _, _ := strings.Cut(rest, "\n")
		out = append(out, strings.TrimSpace(v))
	}
}

var stop = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`a an and are as at be why what which whose been but by can could did do does for from had has have how i if in into is it its me my no not of on or our so than that the their them then there these they this to was we were what when where which who whom will with would you your about after again all also any before being both each few more most other over same should some such too under until very just only own off out up down via per vs yes ok`) {
		stop[w] = true
	}
}

// words splits text into lower-case stems: "merges", "merged" and "merging"
// are one word, as are "skip" and "skipped".
func words(s string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len(w) < 2 || len(w) > 40 || stop[w] {
			continue
		}
		out = append(out, stem(w))
	}
	return out
}

func stem(w string) string {
	if strings.IndexFunc(w, unicode.IsLetter) < 0 || len(w) <= 3 {
		return w
	}
	switch {
	case strings.HasSuffix(w, "ies") && len(w) > 4:
		w = w[:len(w)-3] + "y"
	case strings.HasSuffix(w, "ss"):
	case strings.HasSuffix(w, "s"):
		w = w[:len(w)-1]
	}
	for _, suf := range []string{"ing", "ed"} {
		if strings.HasSuffix(w, suf) && len(w)-len(suf) >= 3 {
			w = w[:len(w)-len(suf)]
			if n := len(w); n >= 2 && w[n-1] == w[n-2] && !strings.ContainsRune("lsz", rune(w[n-1])) {
				w = w[:n-1]
			}
			break
		}
	}
	if len(w) > 3 && strings.HasSuffix(w, "e") {
		w = w[:len(w)-1]
	}
	return w
}
