package gate

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// With no GitHub org and broker (a local org, or none), the github probe
// passes, skipped, on both sides alike.
func TestGitHubProbeSkipsWithoutABroker(t *testing.T) {
	for _, org := range []Org{nil, Local{Dir: t.TempDir()}, GitHub{Owner: "Hive-RSI-Lab", Token: "x"}} {
		r := probeGitHub(&Config{Org: org}, &Child{Name: "candidate", Root: t.TempDir(), Repo: "gate-x-candidate"})
		if !r.Pass || !strings.Contains(r.Detail, "skipped") {
			t.Errorf("org %T: %+v", org, r)
		}
	}
	if !strings.Contains(strings.Join(DefaultProbes, " "), "daemon github org") {
		t.Errorf("github must run before org, which pushes to the repository: %v", DefaultProbes)
	}
}

// The driver adds the repository as a project (the demo while it is empty),
// asks for one change in the project's chat, and names the pull request once
// that task is done.
func TestDriveNamesTheMergedPR(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HIVE_HOME", home)
	var mu sync.Mutex
	var spec map[string]any
	var asked map[string]string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/project", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		json.NewDecoder(r.Body).Decode(&spec)
		io.WriteString(w, `{"name":"textkit","repo":"Hive-RSI-Lab/gate-x-candidate"}`)
	})
	mux.HandleFunc("/api/chat", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		json.NewDecoder(r.Body).Decode(&asked)
		io.WriteString(w, `{}`)
	})
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		tasks := `[{"title":"something else","stage":"done","pr":{"number":1}}]`
		if asked != nil {
			tasks = `[{"title":"` + asked["text"] + `","stage":"done","pr":{"number":7}}]`
		}
		io.WriteString(w, `{"projects":[{"name":"textkit","tasks":`+tasks+`}]}`)
	})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	defer srv.Close()
	ws := core.Workspace{Root: home}
	os.MkdirAll(filepath.Dir(ws.DaemonFile()), 0o755)
	info, _ := json.Marshal(map[string]any{"port": ln.Addr().(*net.TCPAddr).Port, "token": "tok"})
	os.WriteFile(ws.DaemonFile(), info, 0o600)

	r, w, _ := os.Pipe()
	stdout := os.Stdout
	os.Stdout = w
	code := Drive([]string{"Hive-RSI-Lab/gate-x-candidate", "demo", "abcd1234"})
	os.Stdout = stdout
	w.Close()
	out, _ := io.ReadAll(r)
	if code != 0 || driveLine.FindStringSubmatch(string(out)) == nil || !strings.Contains(string(out), "pr=7") {
		t.Fatalf("exit %d: %s", code, out)
	}
	if spec["repo"] != "Hive-RSI-Lab/gate-x-candidate" || spec["demo"] != true {
		t.Errorf("project spec: %v", spec)
	}
	if asked["chat"] != "project:textkit" || !strings.HasPrefix(asked["text"], "Probe abcd1234") {
		t.Errorf("ask: %v", asked)
	}
}
