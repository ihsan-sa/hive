package main

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// bundleCap is just under the 100 MB the bundle endpoint takes.
const bundleCap = 100<<20 - 64<<10

const defaultBundleURL = "https://hive-api.ihsan.cc/v1/bundle"

// sharedSet is what `hive share` sends, relative to .hive. It never reaches
// logins, bin or projects.
var sharedSet = []string{"chats", "kb", "gates", "events.jsonl", "config.json", "daemon.log"}

// A part is one thing the bundle can leave out whole: an entry of sharedSet,
// or one run under runs/.
type part struct {
	name  string    // slash path under .hive
	run   bool      // a run dir, left out oldest first
	mod   time.Time // when a run last changed
	files []string  // slash paths under .hive
	size  int64     // bytes it takes in the zip
}

func cmdShare(args []string) int {
	fs := flag.NewFlagSet("share", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "send without asking")
	fs.BoolVar(yes, "y", false, "send without asking")
	if fs.Parse(args) != nil {
		return 2
	}
	ws, err := workspace()
	if err == nil {
		url := ws.LoadConfig().ShareBundleURL
		if url == "" {
			url = defaultBundleURL
		}
		err = share(ws, url, bundleCap, *yes, os.Stdin, os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, core.Cmd+" share:", err)
		return 1
	}
	return 0
}

// share lists the bundle, asks, sends it to url and prints the reference the
// endpoint answers with. Over limit it offers to leave parts out; a no sends
// nothing.
func share(ws core.Workspace, url string, limit int64, yes bool, in io.Reader, out io.Writer) error {
	parts := shareParts(ws)
	tmp, err := os.CreateTemp("", "hive-share-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	total, err := writeBundle(tmp, ws, parts)
	if err != nil {
		return err
	}
	var left []part
	if total > limit {
		overhead := total
		for _, p := range parts {
			overhead -= p.size
		}
		parts, left = fitParts(parts, limit-overhead)
		if total, err = rewriteBundle(tmp, ws, parts); err != nil {
			return err
		}
	}
	if len(parts) == 0 {
		return errors.New("nothing in this Hive fits in one bundle")
	}
	fmt.Fprintf(out, "This sends %s from %s:\n", mb(total), ws.Hive())
	listParts(out, parts)
	if len(left) > 0 {
		fmt.Fprintf(out, "Everything together is over the %s the endpoint takes, so it leaves out:\n", mb(limit))
		listParts(out, left)
	}
	fmt.Fprintln(out, "It never includes your logins, the Hive's binaries or your projects' code.")
	if !yes {
		fmt.Fprintf(out, "Send it to %s? [y/N] ", url)
		line, _ := bufio.NewReader(in).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Fprintln(out, "Nothing sent.")
			return nil
		}
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	req, err := http.NewRequest("POST", url, tmp)
	if err != nil {
		return err
	}
	req.ContentLength = total
	req.Header.Set("Content-Type", "application/zip")
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("the endpoint refused the bundle: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}
	fmt.Fprintln(out, "Sent. Your reference is", bundleRef(body))
	return nil
}

// bundleRef is the reference in the endpoint's answer: a JSON object's ref,
// reference or id, or else the plain text it sent.
func bundleRef(body []byte) string {
	var m map[string]any
	if json.Unmarshal(body, &m) == nil {
		for _, k := range []string{"ref", "reference", "id"} {
			if s, ok := m[k].(string); ok && s != "" {
				return s
			}
		}
	}
	return strings.TrimSpace(string(body))
}

// loginDirs are the old places of a run's login tokens, which never leave.
var loginDirs = []string{"runs/*/home", "runs/*/host/codex*"}

// shareParts finds every regular file of the shared set and the runs.
func shareParts(ws core.Workspace) []part {
	var parts []part
	add := func(p part) {
		filepath.WalkDir(filepath.Join(ws.Hive(), filepath.FromSlash(p.name)), func(file string, d fs.DirEntry, err error) error {
			rel, _ := filepath.Rel(ws.Hive(), file)
			rel = filepath.ToSlash(rel)
			for _, pat := range loginDirs {
				if ok, _ := path.Match(pat, rel); ok && d != nil && d.IsDir() {
					return fs.SkipDir
				}
			}
			if err == nil && d.Type().IsRegular() {
				p.files = append(p.files, rel)
			}
			return nil
		})
		if len(p.files) > 0 {
			parts = append(parts, p)
		}
	}
	for _, name := range sharedSet {
		add(part{name: name})
	}
	runs, _ := os.ReadDir(ws.Runs())
	for _, r := range runs {
		if fi, err := r.Info(); err == nil {
			add(part{name: "runs/" + r.Name(), run: true, mod: fi.ModTime()})
		}
	}
	return parts
}

// writeBundle zips parts into w as .hive/..., setting each part's size, and
// returns the zip's length. Files are read with readNoFollow, since agents
// can write under runs/.
func writeBundle(w io.Writer, ws core.Workspace, parts []part) (int64, error) {
	root, err := os.OpenRoot(ws.Hive())
	if err != nil {
		return 0, err
	}
	defer root.Close()
	cw := &countWriter{w: w}
	zw := zip.NewWriter(cw)
	for i := range parts {
		start := cw.n
		for _, rel := range parts[i].files {
			data, err := readNoFollow(root, rel)
			if err != nil {
				continue // gone or swapped since the walk
			}
			f, err := zw.CreateHeader(&zip.FileHeader{Name: ".hive/" + rel, Method: zip.Deflate, Modified: time.Now()})
			if err != nil {
				return 0, err
			}
			if _, err := f.Write(data); err != nil {
				return 0, err
			}
		}
		if err := zw.Flush(); err != nil {
			return 0, err
		}
		parts[i].size = cw.n - start
	}
	err = zw.Close()
	return cw.n, err
}

// readNoFollow reads the slash path rel under root one name at a time,
// refusing any name that is a symlink or is swapped between its check and
// its open, so a run that swaps a folder for a link mid-read never leads the
// read anywhere else, in .hive or out of it.
func readNoFollow(root *os.Root, rel string) ([]byte, error) {
	names := strings.Split(rel, "/")
	dir := root
	defer func() {
		if dir != root {
			dir.Close()
		}
	}()
	for _, name := range names[:len(names)-1] {
		before, err := dir.Lstat(name)
		if err != nil || !before.IsDir() {
			return nil, errors.New("not a folder")
		}
		sub, err := dir.OpenRoot(name)
		if err != nil {
			return nil, err
		}
		if dir != root {
			dir.Close()
		}
		dir = sub
		if after, err := dir.Stat("."); err != nil || !os.SameFile(before, after) {
			return nil, errors.New("folder changed while reading")
		}
	}
	name := names[len(names)-1]
	before, err := dir.Lstat(name)
	if err != nil || !before.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	f, err := dir.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if after, err := f.Stat(); err != nil || !os.SameFile(before, after) {
		return nil, errors.New("file changed while reading")
	}
	return io.ReadAll(f)
}

func rewriteBundle(f *os.File, ws core.Workspace, parts []part) (int64, error) {
	if err := f.Truncate(0); err != nil {
		return 0, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	return writeBundle(f, ws, parts)
}

// fitParts leaves out runs oldest first, then the largest other parts, until
// what is kept fits in limit.
func fitParts(parts []part, limit int64) (keep, left []part) {
	order := append([]part(nil), parts...)
	sort.SliceStable(order, func(i, j int) bool {
		a, b := order[i], order[j]
		if a.run != b.run {
			return a.run
		}
		if a.run {
			return a.mod.Before(b.mod)
		}
		return a.size > b.size
	})
	var total int64
	for _, p := range parts {
		total += p.size
	}
	out := map[string]bool{}
	for _, p := range order {
		if total <= limit {
			break
		}
		out[p.name] = true
		total -= p.size
		left = append(left, p)
	}
	for _, p := range parts {
		if !out[p.name] {
			keep = append(keep, p)
		}
	}
	return keep, left
}

// listParts prints each part with its size, the runs as one line.
func listParts(out io.Writer, parts []part) {
	var runs, runSize int64
	for _, p := range parts {
		if p.run {
			runs++
			runSize += p.size
			continue
		}
		fmt.Fprintf(out, "  %-14s %s\n", p.name, mb(p.size))
	}
	if runs > 0 {
		fmt.Fprintf(out, "  %-14s %s\n", fmt.Sprintf("runs (%d)", runs), mb(runSize))
	}
}

func mb(n int64) string {
	if n < 1<<20 {
		return fmt.Sprintf("%d KB", (n+1023)>>10)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
