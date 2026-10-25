package main

import (
	"fmt"
	"strings"
)

// Render writes the best n hits for an agent or a person to read: each file
// once, why it matched, and the lines of its best entries that say so.
func Render(q Query, hits []*Hit, n int) string {
	if len(hits) == 0 {
		return "nothing in the knowledge base matches; the record doesn't cover it"
	}
	var b strings.Builder
	shown := hits[:min(n, len(hits))]
	for i, h := range shown {
		d := h.Doc
		title := d.Title
		if r := []rune(title); len(r) > 110 {
			title = string(r[:110]) + "…"
		}
		fmt.Fprintf(&b, "%d. %s", i+1, d.Path)
		if title != "" && !strings.HasSuffix(d.Path, title) {
			fmt.Fprintf(&b, " — %s", oneLine(title))
		}
		if d.When != "" {
			fmt.Fprintf(&b, " (%s)", d.When)
		}
		b.WriteString("\n   why: " + why(h, q) + "\n")
		for _, l := range snippet(h, q) {
			b.WriteString("   > " + l + "\n")
		}
		if n := len(h.Also); n > 0 {
			fmt.Fprintf(&b, "   also %d more review(s) of this task: %s\n", n, strings.Join(h.Also[:min(n, 3)], ", "))
		}
		b.WriteString("\n")
	}
	if more := len(hits) - len(shown); more > 0 {
		fmt.Fprintf(&b, "%d more files match; ask again with --limit all (or a bigger number) to see them. ", more)
	}
	b.WriteString("Read a file under $HIVE_KB for the whole of it; `--deep \"question\"` digs and answers with sources.")
	return b.String()
}

func why(h *Hit, q Query) string {
	var parts []string
	var about, names []string
	for _, e := range h.Ents {
		if h.Doc.Home[e] {
			about = append(about, e)
		} else {
			names = append(names, e)
		}
	}
	if len(about) > 0 {
		parts = append(parts, "is about "+strings.Join(about, ", "))
	}
	if len(names) > 0 {
		parts = append(parts, "names "+strings.Join(names, ", "))
	}
	if len(h.Words) > 0 {
		said := make([]string, len(h.Words))
		for i, w := range h.Words {
			if said[i] = q.Said[w]; said[i] == "" {
				said[i] = w
			}
		}
		parts = append(parts, "words: "+strings.Join(said, ", "))
	}
	if h.Via != "" {
		parts = append(parts, "connected by "+h.Via)
	}
	if len(parts) == 0 {
		return "related words"
	}
	return strings.Join(parts, "; ")
}

// snippet picks the lines of the best entries that hold the question's words
// or entities, each entry headed by its first line (time, kind, seq).
func snippet(h *Hit, q Query) []string {
	var out []string
	need := map[string]bool{}
	for _, t := range q.Terms {
		need[t] = true
	}
	var vals []string
	for _, e := range q.Ents {
		if v := entValue(e); v != "" {
			vals = append(vals, strings.ToLower(v))
		}
	}
	match := func(l string) bool {
		low := strings.ToLower(l)
		for _, v := range vals {
			if strings.Contains(low, v) {
				return true
			}
		}
		for e := range entities(l) {
			for _, want := range q.Ents {
				if e == want {
					return true
				}
			}
		}
		for _, w := range words(l) {
			if need[w] {
				return true
			}
		}
		return false
	}
	// A task's or request's state now: its last stage and reason.
	if h.Doc.Kind == "record" {
		var now []string
		for _, k := range []string{"stage", "state", "status", "reason"} {
			if v := fields(h.Doc.Text, k); len(v) > 0 {
				now = append(now, k+" "+v[len(v)-1])
			}
		}
		if len(now) > 0 {
			out = append(out, "now: "+clip(strings.Join(now, " · "), 240))
		}
	}
	entries := h.Entries
	if len(entries) == 0 && len(h.Doc.Entries) > 0 {
		entries = []int{len(h.Doc.Entries) - 1}
	}
	for _, i := range entries {
		e := h.Doc.Entries[i]
		lines := strings.Split(e, "\n")
		if h.Doc.Kind == "record" {
			out = append(out, clip(lines[0], 80))
			lines = lines[1:]
		}
		n := 0
		for _, l := range lines {
			// A task's log line repeats its whole history at every save.
			if l = strings.TrimSpace(l); l == "" || l == "---" || strings.HasPrefix(l, "- log: ") || !match(l) {
				continue
			}
			out = append(out, "  "+clip(l, 260))
			if n++; n == 3 {
				break
			}
		}
		if n == 0 && len(lines) > 0 {
			for _, l := range lines {
				if l = strings.TrimSpace(l); l != "" && l != "---" {
					out = append(out, "  "+clip(l, 260))
					break
				}
			}
		}
	}
	return out
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
