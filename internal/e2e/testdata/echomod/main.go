// Command echomod is the example module the end to end test installs. It
// serves a source and a channel hook through the control API, as a door to a
// chat service would, with two files in its HOME standing in for the service:
//
//	inbox   lines in: "ask <project> <title>" files an ask, "say <text>"
//	        steers the lead, "stop <run>" stops a run, "forbidden" tries an
//	        endpoint outside its hooks, "null" reports nothing, "raw" calls
//	        with a bare token, "hold" stops reading until a file "release"
//	        appears, "exit" ends the program
//	outbox  lines out: the lead's conversation, run notices, reports, call results
//	events  every event it read: "<seq> <kind>"
//
// It is plain HTTP and JSON, so a module in any language does the same.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	api   = os.Getenv("HIVE_API")
	token = os.Getenv("HIVE_MODULE_TOKEN")
	home  = os.Getenv("HOME")
)

func call(method, path string, body, out any) (int, error) {
	return callAs("Bearer "+token, method, path, body, out)
}

func callAs(auth, method, path string, body, out any) (int, error) {
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, api+path, bytes.NewReader(raw))
	req.Header.Set("Authorization", auth)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode, nil
}

func say(format string, a ...any) { write("outbox", format, a...) }

func write(file, format string, a ...any) {
	f, _ := os.OpenFile(filepath.Join(home, file), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	fmt.Fprintf(f, format+"\n", a...)
	f.Close()
}

func main() {
	call("POST", "/api/module/report", map[string]string{"text": "started"}, nil)
	done, _ := os.ReadFile(filepath.Join(home, "inbox.done")) // lines already taken, across restarts
	taken := len(strings.Fields(string(done)))
	seen := map[string]bool{}
	var after int64
	for misses := 0; misses < 10; time.Sleep(300 * time.Millisecond) {
		if _, err := call("GET", "/api/ping", nil, nil); err != nil {
			misses++ // the daemon is gone; it starts this program again when it is back
			continue
		}
		misses = 0
		raw, _ := os.ReadFile(filepath.Join(home, "inbox"))
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		for ; taken < len(lines) && lines[taken] != ""; taken++ {
			os.WriteFile(filepath.Join(home, "inbox.done"), []byte(strings.Repeat("x\n", taken+1)), 0o600)
			verb, rest, _ := strings.Cut(lines[taken], " ")
			switch verb {
			case "ask":
				project, title, _ := strings.Cut(rest, " ")
				code, _ := call("POST", "/api/ask", map[string]string{"project": project, "title": title, "from": "inbox"}, nil)
				say("ask %d", code)
			case "say":
				code, _ := call("POST", "/api/chat", map[string]string{"chat": "lead", "text": rest}, nil)
				say("say %d", code)
			case "stop":
				code, _ := call("POST", "/api/run/stop", map[string]string{"id": rest, "reason": "stopped from the inbox"}, nil)
				say("stop %d", code)
			case "forbidden":
				code, _ := call("GET", "/api/config", nil, nil)
				say("forbidden %d", code)
			case "null":
				code, _ := call("POST", "/api/module/report", nil, nil)
				say("null %d", code)
			case "raw":
				code, _ := callAs(token, "GET", "/api/ping", nil, nil)
				say("raw %d", code)
			case "hold":
				say("holding")
				for _, err := os.Stat(filepath.Join(home, "release")); err != nil; _, err = os.Stat(filepath.Join(home, "release")) {
					time.Sleep(100 * time.Millisecond)
				}
			case "exit":
				os.Exit(1)
			}
		}
		var msgs []struct{ ID, From, Text string }
		call("GET", "/api/chat?id=lead", nil, &msgs)
		for _, m := range msgs {
			if !seen[m.ID] && m.From != "person" && m.From != os.Getenv("HIVE_MODULE") {
				say("lead: %s", m.Text)
			}
			seen[m.ID] = true
		}
		var evs []struct {
			Seq  int64
			Kind string
			Data struct{ Module, Text string }
		}
		call("GET", fmt.Sprintf("/api/events?after=%d", after), nil, &evs)
		for _, e := range evs {
			after = e.Seq
			write("events", "%d %s", e.Seq, e.Kind)
			switch {
			case e.Kind == "run.turn_ended" || e.Kind == "effect.merged" || e.Kind == "hive.idle":
				say("notice %s", e.Kind)
			case e.Kind == "module.reported" && e.Data.Module == "":
				say("report %s", e.Data.Text)
			}
		}
	}
}
