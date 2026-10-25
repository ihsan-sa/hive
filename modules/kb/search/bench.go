package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Question is one benchmark question with the files that answer it.
type Question struct {
	Q      string   `json:"q"`
	Answer string   `json:"answer"`
	Expect []string `json:"expect"` // the files that hold the answer
	Also   []string `json:"also,omitempty"`
}

// Score is how one engine did on the benchmark, over its top K files.
type Score struct {
	Engine    string    `json:"engine"`
	K         int       `json:"k"`
	Recall    float64   `json:"recall"`    // of the expected files, the share in the top K
	Precision float64   `json:"precision"` // of the top K results, the share that are expected or also-relevant
	MRR       float64   `json:"mrr"`       // 1 / rank of the first expected file
	Distinct  float64   `json:"distinct"`  // of the top K results, the share that are a file not shown already
	Each      []QResult `json:"each"`
}

// QResult is one question's result.
type QResult struct {
	Q         string   `json:"q"`
	Recall    float64  `json:"recall"`
	Precision float64  `json:"precision"`
	First     int      `json:"first"` // rank of the first expected file; 0 if none in the top K
	Missed    []string `json:"missed,omitempty"`
}

// Measure runs every question through rank (the files it returns, best first,
// a file repeated as often as the engine repeats it) and scores the top k.
func Measure(qs []Question, k int, engine string, rank func(string) []string) Score {
	s := Score{Engine: engine, K: k}
	for _, q := range qs {
		got := rank(q.Q)
		got = got[:min(k, len(got))]
		want := map[string]bool{}
		for _, f := range q.Expect {
			want[f] = true
		}
		ok := map[string]bool{}
		for _, f := range append(slices.Clone(q.Expect), q.Also...) {
			ok[f] = true
		}
		r := QResult{Q: q.Q}
		found, good, distinct := map[string]bool{}, 0, map[string]bool{}
		for i, f := range got {
			if ok[f] {
				good++
			}
			if want[f] && !found[f] {
				found[f] = true
				if r.First == 0 {
					r.First = i + 1
				}
			}
			distinct[f] = true
		}
		for _, f := range q.Expect {
			if !found[f] {
				r.Missed = append(r.Missed, f)
			}
		}
		if len(want) > 0 {
			r.Recall = float64(len(found)) / float64(len(want))
		}
		if len(got) > 0 {
			r.Precision = float64(good) / float64(len(got))
			s.Distinct += float64(len(distinct)) / float64(len(got))
		}
		if r.First > 0 {
			s.MRR += 1 / float64(r.First)
		}
		s.Recall += r.Recall
		s.Precision += r.Precision
		s.Each = append(s.Each, r)
	}
	if n := float64(len(qs)); n > 0 {
		s.Recall, s.Precision, s.MRR, s.Distinct = s.Recall/n, s.Precision/n, s.MRR/n, s.Distinct/n
	}
	return s
}

func bench(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("bench", flag.ExitOnError)
	dir := fs.String("kb", os.Getenv("HIVE_KB"), "the knowledge base")
	k := fs.Int("k", 10, "how many results count")
	engine := fs.String("engine", "graph", "graph (this search) or words (the word match it replaced)")
	asJSON := fs.Bool("json", false, "print the scores as JSON")
	show := fs.Bool("show", false, "print each question's top files")
	fs.Parse(args)
	if fs.NArg() != 1 || *dir == "" {
		fmt.Fprintln(os.Stderr, "usage: search bench [--kb dir] [--k 10] [--engine graph|words] [--json] questions.json")
		return 2
	}
	raw, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "bench:", err)
		return 1
	}
	var set struct{ Questions []Question }
	if err := json.Unmarshal(raw, &set); err != nil {
		fmt.Fprintln(os.Stderr, "bench:", err)
		return 1
	}
	var rank func(string) []string
	switch *engine {
	case "graph":
		ix := Load(*dir, nil)
		rank = func(q string) []string {
			var out []string
			for _, h := range ix.Search(ParseQuery(q, ix)) {
				out = append(out, h.Doc.Path)
			}
			return out
		}
	case "words":
		rank = func(q string) []string { return wordRank(*dir, q) }
	default:
		fmt.Fprintln(os.Stderr, "bench: no engine", *engine)
		return 2
	}
	if *show {
		inner := rank
		rank = func(q string) []string {
			got := inner(q)
			fmt.Fprintf(out, "%s\n", q)
			for i, f := range got[:min(*k, len(got))] {
				fmt.Fprintf(out, "  %2d %s\n", i+1, f)
			}
			return got
		}
	}
	s := Measure(set.Questions, *k, *engine, rank)
	if *asJSON {
		b, _ := json.MarshalIndent(s, "", "  ")
		fmt.Fprintln(out, string(b))
		return 0
	}
	fmt.Fprintf(out, "%s, top %d, %d questions: recall %.2f, precision %.2f, MRR %.2f, distinct files %.2f\n", s.Engine, s.K, len(s.Each), s.Recall, s.Precision, s.MRR, s.Distinct)
	for _, r := range s.Each {
		fmt.Fprintf(out, "  %.2f %.2f  first %-2d  %s\n", r.Recall, r.Precision, r.First, r.Q)
		for _, m := range r.Missed {
			fmt.Fprintf(out, "              missed %s\n", m)
		}
	}
	return 0
}

// wordRank is the search this one replaced (internal/mod/kb Search before
// #273), kept to measure against: notes whole and record files entry by
// entry, scored by how many of the query's words they hold (two for a word in
// the entry, one for a word elsewhere in its file), newest first among
// equals; a file comes back once per matching entry.
func wordRank(dir, query string) []string {
	var terms []string
	for _, w := range strings.Fields(strings.ToLower(query)) {
		if len(w) > 1 {
			terms = append(terms, w)
		}
	}
	type hit struct {
		file, when string
		score      int
	}
	var hits []hit
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		if d.IsDir() && path != dir && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		raw, _ := os.ReadFile(path)
		rel = filepath.ToSlash(rel)
		whole := strings.ToLower(rel + " " + string(raw))
		pieces := []string{string(raw)}
		if strings.HasPrefix(rel, "record/") {
			pieces = strings.Split(string(raw), "\n### ")[1:]
			slices.Reverse(pieces)
		}
		for _, p := range pieces {
			low := strings.ToLower(p)
			h := hit{file: rel}
			in := false
			for _, t := range terms {
				if strings.Contains(low, t) {
					h.score += 2
					in = true
				}
				if strings.Contains(whole, t) {
					h.score++
				}
			}
			if !in {
				continue
			}
			if i := strings.Index(p, "date: "); i >= 0 && !strings.HasPrefix(rel, "record/") {
				h.when, _, _ = strings.Cut(p[i+6:], "\n")
			} else if len(p) >= 16 {
				h.when = p[:16]
			}
			hits = append(hits, h)
		}
		return nil
	})
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].when > hits[j].when
	})
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.file
	}
	return out
}
