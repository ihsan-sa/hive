package kb

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// The search's benchmark: real questions about what this Hive did, each with
// the files that answer it (testdata/bench, checked by hand against the
// record by an agent that never saw this search; questions.json from the
// night of 2026-10-24, heldout.json later and on other topics). It needs a
// live knowledge base, so it runs only when HIVE_KB_BENCH names one:
//
//	HIVE_KB_BENCH=$HIVE_KB go test ./internal/mod/kb -run TestSearchBenchmark -v
//
// Over the top 10 files: recall (the answer files among them), precision
// (the share of them that answer or are listed as relevant), MRR (one over
// the rank of the first answer file) and distinct (the share that are a
// file not shown already).
func TestSearchBenchmark(t *testing.T) {
	dir := os.Getenv("HIVE_KB_BENCH")
	if dir == "" {
		t.Skip("HIVE_KB_BENCH names no knowledge base")
	}
	for _, set := range []string{"questions", "heldout"} {
		raw, err := os.ReadFile(filepath.Join("testdata", "bench", set+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var b struct {
			Questions []struct {
				Q      string
				Expect []string
				Also   []string
			}
		}
		if err := json.Unmarshal(raw, &b); err != nil {
			t.Fatal(err)
		}
		var recall, precision, mrr, distinct float64
		var took time.Duration
		for _, q := range b.Questions {
			start := time.Now()
			hits := Search(dir, q.Q)
			took += time.Since(start)
			var files []string
			for _, h := range hits {
				files = append(files, h.File)
			}
			top := files[:min(10, len(files))]
			found, good, first := 0, 0, 0.0
			for i, f := range top {
				answers := slices.Contains(q.Expect, f)
				if answers {
					found++
					if first == 0 {
						first = 1 / float64(i+1)
					}
				}
				if answers || slices.Contains(q.Also, f) {
					good++
				}
			}
			uniq := map[string]bool{}
			for _, f := range top {
				uniq[f] = true
			}
			r := float64(found) / float64(len(q.Expect))
			recall += r
			if len(top) > 0 {
				precision += float64(good) / float64(len(top))
				distinct += float64(len(uniq)) / float64(len(top))
			}
			mrr += first
			if testing.Verbose() {
				fmt.Printf("  %.2f  %s\n", r, q.Q)
			}
		}
		n := float64(len(b.Questions))
		fmt.Printf("%s (%d): recall %.2f precision %.2f MRR %.2f distinct %.2f, %.1fs a question\n", set, len(b.Questions), recall/n, precision/n, mrr/n, distinct/n, took.Seconds()/n)
	}
}
