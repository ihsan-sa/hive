package main

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Note is one knowledge base file as the pass sees it: its header in order,
// its body without the Related section the pass keeps, and that section's
// links.
type Note struct {
	Path    string
	Head    [][2]string
	Body    string
	Related []string // note names ("[[name]]"), without .md
	was     string   // sha256 of the text read; "" for a note the pass makes
}

func hashOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

const relatedHeading = "\n## Related\n"

var linkRe = regexp.MustCompile(`\[\[([A-Za-z0-9._-]+)\]\]`)

// parse reads a note. A file with no "---" header keeps none.
func parse(p, text string) *Note {
	n := &Note{Path: p, was: hashOf(text)}
	body := text
	if rest, ok := strings.CutPrefix(text, "---\n"); ok {
		if head, after, ok := strings.Cut(rest, "\n---\n"); ok {
			for _, l := range strings.Split(head, "\n") {
				if k, v, ok := strings.Cut(l, ":"); ok {
					n.Head = append(n.Head, [2]string{strings.TrimSpace(k), strings.TrimSpace(v)})
				}
			}
			body = after
		}
	}
	body = strings.TrimSpace(body)
	if i := strings.LastIndex("\n"+body, relatedHeading); i >= 0 {
		tail := ("\n" + body)[i+len(relatedHeading):]
		ours := true
		var links []string
		for _, l := range strings.Split(strings.TrimSpace(tail), "\n") {
			m := linkRe.FindStringSubmatch(l)
			if strings.TrimSpace(l) == "" {
				continue
			}
			if !strings.HasPrefix(l, "- ") || m == nil {
				ours = false
				break
			}
			links = append(links, m[1])
		}
		if ours {
			n.Related = links
			body = strings.TrimSpace(body[:max(0, i-1)])
		}
	}
	n.Body = body
	return n
}

// render writes a note back as text.
func (n *Note) render(titles map[string]string) string {
	var b strings.Builder
	if len(n.Head) > 0 {
		b.WriteString("---\n")
		for _, kv := range n.Head {
			b.WriteString(kv[0] + ": " + kv[1] + "\n")
		}
		b.WriteString("---\n\n")
	}
	b.WriteString(n.Body)
	if len(n.Related) > 0 {
		b.WriteString("\n" + relatedHeading + "\n")
		for _, r := range n.Related {
			line := "- [[" + r + "]]"
			if t := titles[r]; t != "" {
				line += " · " + t
			}
			b.WriteString(line + "\n")
		}
		return b.String()
	}
	return b.String() + "\n"
}

func (n *Note) get(k string) string {
	for _, kv := range n.Head {
		if kv[0] == k {
			return kv[1]
		}
	}
	return ""
}

func (n *Note) set(k, v string) {
	for i, kv := range n.Head {
		if kv[0] == k {
			n.Head[i][1] = v
			return
		}
	}
	n.Head = append(n.Head, [2]string{k, v})
}

// name is a note's link name: its file name without .md.
func (n *Note) name() string { return strings.TrimSuffix(path.Base(n.Path), ".md") }

// title is the header's title, else the first heading, else the name.
func (n *Note) title() string {
	if t := n.get("title"); t != "" {
		return t
	}
	for _, l := range strings.Split(n.Body, "\n") {
		if t, ok := strings.CutPrefix(l, "# "); ok {
			return strings.TrimSpace(t)
		}
	}
	return n.name()
}

// topLevel says the note is an ordinary note, not a practice doc or digest.
func (n *Note) topLevel() bool { return !strings.Contains(n.Path, "/") }

// distilled says the pass wrote it from the record (a project summary).
func (n *Note) distilled() bool { return n.get("distilled") != "" }

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// slug is the file name the kb module gives a title, as `remember` does.
func slug(title string) string {
	s := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

var suffixRe = regexp.MustCompile(`-(\d+)$`)

// suffix is the number remember added to a name already taken (0 for none).
func suffix(name string) int {
	if m := suffixRe.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

// paragraphs splits text at blank lines, normalising space inside each.
func paragraphs(text string) []string {
	var out []string
	for _, p := range regexp.MustCompile(`\n\s*\n`).Split(strings.TrimSpace(text), -1) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func norm(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
