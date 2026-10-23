// Package agentcli is `hive agent`, how an agent asks the Hive for things.
// It writes a request into the run's outbox and waits for the reply in its
// inbox; the core decides who is asking from which outbox it came from.
package agentcli

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Call sends op with args and waits for the reply.
func Call(op string, args any, wait time.Duration) (json.RawMessage, error) {
	box := os.Getenv("HIVE_BOX")
	if box == "" {
		return nil, errors.New("HIVE_BOX is not set: `hive agent` only works inside a Hive run")
	}
	var b [6]byte
	rand.Read(b[:])
	id := time.Now().UTC().Format("150405") + "-" + hex.EncodeToString(b[:])
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	req, _ := json.Marshal(map[string]any{"id": id, "op": op, "args": json.RawMessage(raw)})
	tmp := filepath.Join(box, "outbox", "."+id+".tmp")
	if err := os.WriteFile(tmp, req, 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, filepath.Join(box, "outbox", id+".json")); err != nil {
		return nil, err
	}
	resp := filepath.Join(box, "inbox", id+".json")
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(resp); err == nil {
			os.Remove(resp)
			var r struct {
				OK     bool            `json:"ok"`
				Result json.RawMessage `json:"result"`
				Error  string          `json:"error"`
			}
			if err := json.Unmarshal(data, &r); err != nil {
				return nil, err
			}
			if !r.OK {
				return nil, errors.New(r.Error)
			}
			return r.Result, nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return nil, fmt.Errorf("no reply to %s within %s (the Hive may be restarting; the request is queued and will still be handled)", op, wait)
}

// Main is `hive agent <op> [args]`.
func Main(argv []string) int {
	if len(argv) == 0 || argv[0] == "help" || argv[0] == "--help" {
		fmt.Println(Usage)
		return 0
	}
	op := argv[0]
	args, err := parseArgs(argv[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "hive agent:", err)
		return 2
	}
	res, err := Call(op, args, 3*time.Minute)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hive agent "+op+":", err)
		return 1
	}
	var s string
	if json.Unmarshal(res, &s) == nil {
		fmt.Println(s)
	} else {
		var v any
		json.Unmarshal(res, &v)
		out, _ := json.MarshalIndent(v, "", "  ")
		fmt.Println(string(out))
	}
	return 0
}

// parseArgs accepts one JSON object, `-` for JSON on stdin, `--file x.json`,
// or --key value pairs (a value of @path reads that file's text).
func parseArgs(a []string) (map[string]any, error) {
	out := map[string]any{}
	if len(a) == 1 && strings.HasPrefix(strings.TrimSpace(a[0]), "{") {
		return out, json.Unmarshal([]byte(a[0]), &out)
	}
	if len(a) == 1 && a[0] == "-" {
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, err
		}
		return out, json.Unmarshal(raw, &out)
	}
	for i := 0; i < len(a); i++ {
		k := a[i]
		if !strings.HasPrefix(k, "--") {
			return nil, fmt.Errorf("expected --key, got %q", k)
		}
		k = strings.TrimPrefix(k, "--")
		if k, v, ok := strings.Cut(k, "="); ok {
			out[k] = v
			continue
		}
		if i+1 >= len(a) {
			out[k] = "true"
			continue
		}
		v := a[i+1]
		i++
		if k == "file" {
			raw, err := os.ReadFile(v)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(raw, &out); err != nil {
				return nil, err
			}
			continue
		}
		if strings.HasPrefix(v, "@") {
			raw, err := os.ReadFile(v[1:])
			if err != nil {
				return nil, err
			}
			v = string(raw)
		}
		out[k] = v
	}
	return out, nil
}

// Usage is the short help agents see.
const Usage = `hive agent <op> [--key value ...]   (or one JSON object, or - for JSON on stdin)

A value of @path reads that file. Common ops:
  progress  --text "where I am"                 shown on the dashboard
  finish    --status done|blocked --account "..." [--tested ...] [--unsure ...]
  message   --to <id|name|parent|person|lead> --text "..."
  inbox                                          messages waiting for you
  agents                                         who is running
  spawn     --role worker --brief "..." [--project p]   a helper; its account returns to you
  brief     --title "..." --done "what done looks like" [--known "..."] [--project p]
                                                 file an ask: it becomes an issue, a worker, a PR
  warden    --want "..." --why "..." --steps "..."   for anything past your reach; the person decides
  search    --query "words" [--limit n|all]     the knowledge base pieces that match, best first
  remember  --title "..." --text "..." [--tags a,b]   write to the knowledge base
  schedule  --in 2h --text "..."                 a wake-up message to yourself
  status                                         projects, work in flight, scores`
