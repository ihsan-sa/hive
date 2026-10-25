// Command hive-soft is the soft-layer experiment's tool (docs/soft-layer.md):
//
//	hive-soft variants                                   # the arms
//	hive-soft show <variant> [--essay docs/the-hive-essay.md] [role]
//	hive-soft observe [--since RFC3339] [--until RFC3339] [--json] <events.jsonl>
//
// observe reads any Hive's journal, a sub-Hive's or this Hive's own, and says
// who ran, who talked to whom, and what landed. The arms themselves run in
// TestSoftLayer (internal/e2e).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/softlayer"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "variants":
		for _, v := range softlayer.Variants {
			fmt.Printf("%-12s %s\n", v.Name, v.About)
		}
	case "show":
		fs := flag.NewFlagSet("show", flag.ExitOnError)
		essay := fs.String("essay", "docs/the-hive-essay.md", "the essay's text")
		fs.Parse(reorder(os.Args[2:]))
		if fs.NArg() < 1 {
			usage()
		}
		v, ok := softlayer.Find(fs.Arg(0))
		if !ok {
			fail(fmt.Errorf("no variant %q", fs.Arg(0)))
		}
		text, _ := os.ReadFile(*essay)
		files, err := v.Build(string(text))
		fail(err)
		for _, r := range softlayer.Roles {
			if fs.NArg() > 1 && fs.Arg(1) != r {
				continue
			}
			fmt.Printf("==> %s.md (%d words)\n%s\n", r, words(files[r]), files[r])
		}
	case "observe":
		fs := flag.NewFlagSet("observe", flag.ExitOnError)
		since := fs.String("since", "", "only events at or after this time (RFC3339)")
		until := fs.String("until", "", "only events before this time (RFC3339)")
		asJSON := fs.Bool("json", false, "print the report as JSON")
		fs.Parse(reorder(os.Args[2:]))
		if fs.NArg() != 1 {
			usage()
		}
		f, err := os.Open(fs.Arg(0))
		fail(err)
		defer f.Close()
		rep, err := softlayer.Observe(f, at(*since), at(*until))
		fail(err)
		if *asJSON {
			out, _ := json.MarshalIndent(rep, "", "  ")
			fmt.Println(string(out))
			return
		}
		fmt.Print(softlayer.Table([]softlayer.Report{rep}))
	default:
		usage()
	}
}

// reorder puts flags before arguments, so `observe file --since x` works.
func reorder(args []string) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		if len(args[i]) > 1 && args[i][0] == '-' {
			flags = append(flags, args[i])
			if i+1 < len(args) && args[i] != "--json" && args[i] != "-json" {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		rest = append(rest, args[i])
	}
	return append(flags, rest...)
}

func at(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	fail(err)
	return t
}

func words(s string) int {
	n, in := 0, false
	for _, r := range s {
		sp := r == ' ' || r == '\n' || r == '\t'
		if !sp && !in {
			n++
		}
		in = !sp
	}
	return n
}

func fail(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "hive-soft:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: hive-soft variants | show <variant> [role] [--essay file] | observe [--since t] [--until t] [--json] <events.jsonl>")
	os.Exit(2)
}
