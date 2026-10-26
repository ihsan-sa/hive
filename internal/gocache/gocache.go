// Package gocache is the Go build cache a check sees: `hive gocacheprog`, a
// GOCACHEPROG (https://pkg.go.dev/cmd/go/internal/cacheprog) that answers
// from two folders laid out as the go command lays out GOCACHE.
//
// The shared folder is read-only to it: only checks of a project's own main
// fill it (with a plain GOCACHE), so a check of any other commit reuses what
// main's checks built and passed and can't plant anything for the next check
// to trust. What this check builds goes in its own folder, removed with its
// home.
package gocache

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Main is `hive gocacheprog <shared> <own>`, started by the go command.
func Main(args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: hive gocacheprog <shared folder> <own folder>")
		return 2
	}
	if err := Serve(args[0], args[1], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "hive gocacheprog:", err)
		return 1
	}
	return 0
}

type request struct {
	ID       int64
	Command  string
	ActionID []byte `json:",omitempty"`
	OutputID []byte `json:",omitempty"`
	BodySize int64  `json:",omitempty"`
}

type response struct {
	ID            int64
	Err           string     `json:",omitempty"`
	KnownCommands []string   `json:",omitempty"`
	Miss          bool       `json:",omitempty"`
	OutputID      []byte     `json:",omitempty"`
	Size          int64      `json:",omitempty"`
	Time          *time.Time `json:",omitempty"`
	DiskPath      string     `json:",omitempty"`
}

// Serve answers the go command's requests on in until it closes or asks to.
func Serve(shared, own string, in io.Reader, out io.Writer) error {
	w := bufio.NewWriter(out)
	enc := json.NewEncoder(w)
	send := func(r response) error {
		if err := enc.Encode(r); err != nil {
			return err
		}
		return w.Flush()
	}
	if err := send(response{KnownCommands: []string{"get", "put", "close"}}); err != nil {
		return err
	}
	dec := json.NewDecoder(bufio.NewReader(in))
	for {
		var req request
		if err := dec.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		res := response{ID: req.ID}
		switch req.Command {
		case "get":
			if e, ok := lookup(own, req.ActionID); ok {
				res.OutputID, res.Size, res.Time, res.DiskPath = e.out, e.size, &e.at, e.path
			} else if e, ok := lookup(shared, req.ActionID); ok {
				res.OutputID, res.Size, res.Time, res.DiskPath = e.out, e.size, &e.at, e.path
			} else {
				res.Miss = true
			}
		case "put":
			var body []byte
			if req.BodySize > 0 {
				if err := dec.Decode(&body); err != nil {
					return err
				}
			}
			if int64(len(body)) != req.BodySize {
				res.Err = "body size mismatch"
			} else if path, err := put(own, req.ActionID, req.OutputID, body); err != nil {
				res.Err = err.Error()
			} else {
				res.DiskPath = path
			}
		case "close":
			return send(res)
		default:
			res.Err = "unknown command " + strconv.Quote(req.Command)
		}
		if err := send(res); err != nil {
			return err
		}
	}
}

type entry struct {
	out  []byte
	size int64
	at   time.Time
	path string
}

func fileName(dir string, id []byte, key string) string {
	return filepath.Join(dir, fmt.Sprintf("%02x", id[0]), fmt.Sprintf("%x", id)+"-"+key)
}

// lookup reads an action's entry ("v1 <action> <output> <size> <time>\n", as
// cmd/go/internal/cache writes it) and checks its output is a plain file of
// that size; anything else is a miss, as it is to the go command. An output
// that is a folder is a cached executable, which a GOCACHEPROG never serves.
func lookup(dir string, id []byte) (entry, bool) {
	if dir == "" || len(id) != 32 {
		return entry{}, false
	}
	raw, err := os.ReadFile(fileName(dir, id, "a"))
	if err != nil {
		return entry{}, false
	}
	f := strings.Fields(string(raw))
	if len(f) != 5 || f[0] != "v1" || f[1] != hex.EncodeToString(id) || !strings.HasSuffix(string(raw), "\n") {
		return entry{}, false
	}
	out, err := hex.DecodeString(f[2])
	size, err2 := strconv.ParseInt(f[3], 10, 64)
	ns, err3 := strconv.ParseInt(f[4], 10, 64)
	if err != nil || err2 != nil || err3 != nil || len(out) != 32 || size < 0 {
		return entry{}, false
	}
	path := fileName(dir, out, "d")
	if fi, err := os.Lstat(path); err != nil || !fi.Mode().IsRegular() || fi.Size() != size {
		return entry{}, false
	}
	return entry{out: out, size: size, at: time.Unix(0, ns), path: path}, true
}

// put keeps body in own, the output before the entry naming it, each written
// whole under a temporary name first: the go commands of one check (vet,
// then test) each run their own copy of this program over the same folder.
func put(dir string, id, out, body []byte) (string, error) {
	if len(id) != 32 || len(out) != 32 {
		return "", errors.New("bad action or output ID")
	}
	path := fileName(dir, out, "d")
	if err := writeFile(path, body); err != nil {
		return "", err
	}
	line := fmt.Sprintf("v1 %x %x %20d %20d\n", id, out, len(body), time.Now().UnixNano())
	if err := writeFile(fileName(dir, id, "a"), []byte(line)); err != nil {
		return "", err
	}
	return path, nil
}

func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return err
	}
	var r [8]byte
	rand.Read(r[:])
	tmp := path + ".tmp-" + hex.EncodeToString(r[:])
	if err := os.WriteFile(tmp, b, 0o666); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
