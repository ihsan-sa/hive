// Command search is the kb module's search: it ranks the knowledge base's
// notes and record files by their words, the entities that tie them together
// (task, request and run ids, PR and issue numbers, commits, files, tests,
// notes, modules, projects) and the links between them, and returns each file
// once with why it matched.
//
// It is the module's own program, standard-library Go with nothing from the
// Hive's source, run with `go run` as the module's "search" op:
//
//	search op       the op's arguments as JSON on stdin ({"query", "limit"}),
//	                the knowledge base in $HIVE_KB, the folders the caller
//	                can't see in $HIVE_KB_HIDE (one per line); the answer on stdout
//	search [--kb dir] [--limit n|all] words...
//	search bench [--kb dir] [--k 10] [--engine graph|words] [--json] questions.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "op" {
		os.Exit(op(os.Stdin, os.Stdout))
	}
	if len(os.Args) > 1 && os.Args[1] == "bench" {
		os.Exit(bench(os.Args[2:], os.Stdout))
	}
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	dir := fs.String("kb", os.Getenv("HIVE_KB"), "the knowledge base")
	limit := fs.String("limit", "", "how many files: a number or all")
	fs.Parse(os.Args[1:])
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "search: no knowledge base: set HIVE_KB or pass --kb")
		os.Exit(2)
	}
	fmt.Println(Find(*dir, strings.Join(fs.Args(), " "), *limit, hideList(os.Getenv("HIVE_KB_HIDE"))))
}

func op(in io.Reader, out io.Writer) int {
	var a struct {
		Query string
		Limit json.RawMessage
	}
	raw, _ := io.ReadAll(io.LimitReader(in, 1<<20))
	if err := json.Unmarshal(raw, &a); err != nil {
		fmt.Fprintln(os.Stderr, "search: bad arguments:", err)
		return 2
	}
	dir := os.Getenv("HIVE_KB")
	if dir == "" {
		fmt.Fprintln(os.Stderr, "search: HIVE_KB is not set")
		return 2
	}
	if strings.TrimSpace(a.Query) == "" {
		fmt.Fprintln(out, `say what to look for: --query "words" (or --deep "a question" for an answer with sources)`)
		return 0
	}
	fmt.Fprintln(out, Find(dir, a.Query, strings.Trim(string(a.Limit), `"`), hideList(os.Getenv("HIVE_KB_HIDE"))))
	return 0
}

func hideList(s string) []string {
	var out []string
	for _, l := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == ',' }) {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// Find searches the knowledge base under dir and renders the best limit
// files ("all", or a number; 10 when empty).
func Find(dir, query, limit string, hide []string) string {
	ix := Load(dir, hide)
	q := ParseQuery(query, ix)
	hits := ix.Search(q)
	n, err := strconv.Atoi(limit)
	switch {
	case limit == "all":
		n = len(hits)
	case err != nil || n <= 0:
		n = 10
	}
	return Render(q, hits, n)
}
