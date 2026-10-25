package kb

import (
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// Change is a knowledge base entry that is new or changed lately.
type Change struct {
	Path  string    // from the knowledge base's root, slash-separated
	Title string    // its title: the header's, else its first heading, else its name
	At    time.Time // when it last changed
	New   bool      // that change added it; else it edited it
}

// Recent lists the n entries changed most lately, newest first, from the
// knowledge base's git history: notes and practice, which the filer and
// `remember` commit. The record is kept out of git and rebuilt on its own,
// and the Hive's own source is reseeded at every start, so neither shows;
// nor do the dream's digests, which the Knowledge tab pins above these.
func Recent(dir string, n int) []Change {
	out, err := core.Git(dir, "-c", "core.quotePath=false", "log", "--no-renames", "--name-status", "--format=%x01%ct", "-n", "300", "--", ".", ":(exclude)"+SelfDir, ":(exclude)"+dreamDir)
	if err != nil {
		return nil
	}
	var changes []Change
	seen := map[string]bool{}
	var at time.Time
	for _, l := range strings.Split(out, "\n") {
		if ts, ok := strings.CutPrefix(l, "\x01"); ok {
			sec, _ := strconv.ParseInt(ts, 10, 64)
			at = time.Unix(sec, 0)
			continue
		}
		status, p, ok := strings.Cut(l, "\t")
		if !ok || seen[p] {
			continue
		}
		seen[p] = true
		if status == "D" || strings.HasPrefix(path.Base(p), ".") || p == "README.md" || !strings.HasSuffix(p, ".md") {
			continue
		}
		raw, err := core.SafeRead(filepath.Join(dir, filepath.FromSlash(p)), 8<<10)
		if err != nil { // gone since, or no plain file
			continue
		}
		changes = append(changes, Change{Path: p, Title: titleOf(string(raw), p), At: at, New: status == "A"})
		if len(changes) == n {
			break
		}
	}
	return changes
}

// titleOf is a note's title: the header's "title:", else its first heading,
// else its file name.
func titleOf(text, p string) string {
	header := strings.HasPrefix(text, "---\n")
	for i, l := range strings.Split(text, "\n") {
		if header && i > 0 && l == "---" {
			header = false
			continue
		}
		if t, ok := strings.CutPrefix(l, "title:"); ok && header && strings.TrimSpace(t) != "" {
			return strings.TrimSpace(t)
		}
		if t, ok := strings.CutPrefix(l, "# "); ok && !header && strings.TrimSpace(t) != "" {
			return strings.TrimSpace(t)
		}
	}
	return strings.TrimSuffix(path.Base(p), ".md")
}
