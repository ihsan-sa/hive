package release

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/scan"
)

// scan is the identity and secret scan of a release at sha: the files of the
// commit that its public list (scan.PublicList, read at sha) names, against
// the deny and handle lists in the Hive's private folder. Any hit, or no
// list, blocks the release.
func (m *Module) scan(p core.Project, sha string) error {
	deny, handles, err := scan.RunTime(os.Getenv, m.C.WS.Private())
	if err != nil {
		return err
	}
	dir := m.C.WS.Project(p.Name)
	out, err := core.Git(dir, "ls-tree", "-r", "-z", sha)
	if err != nil {
		return errors.New("can't list the files at " + core.Short(sha) + ": " + core.Clip(err.Error(), 200))
	}
	var names []string
	blobs := map[string]string{} // path -> blob id; a submodule has none
	for _, e := range strings.Split(strings.TrimRight(out, "\x00"), "\x00") {
		meta, name, ok := strings.Cut(e, "\t")
		if f := strings.Fields(meta); ok && len(f) == 3 {
			names = append(names, name)
			if f[1] == "blob" {
				blobs[name] = f[2]
			}
		}
	}
	l := scan.Lists{Public: scan.ParseList(m.C.Effects.ReadFile(p, sha, scan.PublicList), false), Deny: deny, Handles: handles}
	var ids []string
	for _, f := range scan.Files(names, l.Public) {
		if id := blobs[f]; id != "" {
			ids = append(ids, id)
		}
	}
	data, err := catBlobs(dir, ids)
	if err != nil {
		return errors.New("can't read the files at " + core.Short(sha) + ": " + core.Clip(err.Error(), 200))
	}
	read := func(name string) ([]byte, error) {
		if b, ok := data[blobs[name]]; ok {
			return b, nil
		}
		return nil, errors.New("not a file")
	}
	return scan.Run(names, read, l)
}

// catBlobs reads the blobs ids name in one `git cat-file --batch`, rather
// than one git per published file while the release waits on it.
func catBlobs(dir string, ids []string) (map[string][]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "cat-file", "--batch")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	cmd.Stdin = strings.NewReader(strings.Join(ids, "\n") + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	r := bufio.NewReader(stdout)
	var readErr error
	for range ids {
		head, err := r.ReadString('\n')
		f := strings.Fields(head)
		if err != nil || len(f) != 3 {
			readErr = fmt.Errorf("git cat-file said %q", strings.TrimSpace(head))
			break
		}
		size, _ := strconv.Atoi(f[2])
		body := make([]byte, size+1) // the object and its newline
		if _, err := io.ReadFull(r, body); err != nil {
			readErr = err
			break
		}
		out[f[0]] = body[:size]
	}
	io.Copy(io.Discard, r)
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("git cat-file: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, readErr
}
