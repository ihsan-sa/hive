package gate

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// githubProbeMax is how long the child has to take one change from an ask to
// a merged pull request on GitHub.
const githubProbeMax = 15 * time.Minute

// probeGitHub has the child Hive itself work on GitHub through its broker:
// its daemon (the fake harness, GitHub as its forge, no HIVE_FORGE) takes its
// own repository in the org as a project, and one ask goes through the
// child's own forge code to a merged pull request there: the issue, the
// branch pushed over HTTPS, the pull request, its checks read, the merge.
// Inside the side's sandbox a driver (`gate-drive`, the gate's own code)
// only asks the daemon and waits; what is judged the gate reads from GitHub
// itself, with the org's token: that the pull request the driver names is
// merged into the repository's main, and the time it took by the gate's
// clock. With no broker (a local org, or none) it passes, skipped, on both
// sides alike.
func probeGitHub(cfg *Config, c *Child) Result {
	g, ok := cfg.Org.(GitHub)
	if !ok || c.Trust == "" || c.Meter == nil || c.Meter.broker.Load() == nil || c.Repo == "" {
		return Result{Pass: true, Detail: "no GitHub org with a broker in this run: the child's GitHub round trip is skipped"}
	}
	b := c.Meter.broker.Load()
	repo := g.Owner + "/" + c.Repo
	ws := c.Workspace()
	os.RemoveAll(ws)
	os.MkdirAll(filepath.Join(ws, ".hive"), 0o755)
	// As the e2e suite runs a fake Hive: nothing scheduled, no update, and
	// the machine's load never holds a fake turn.
	conf := `{"harness":"fake","sandbox":"bare","bare_accepted":"` + time.Now().Format(time.RFC3339) + `","auto_update":false,` +
		`"mem_floor_mb":0,"mem_pressure_max":0,"cpu_pressure_max":0,"load_max":0,"plateau_merges":0,` +
		`"schedules":{"improve.sweep":"off","improve.stepback":"off","kb.file":"off","modules.update":"off","hive.update":"off"}}`
	os.WriteFile(filepath.Join(ws, ".hive", "config.json"), []byte(conf), 0o644)
	// A repository with no commits yet becomes the demo project; one the
	// demo already set up (a later rep) is taken as it is.
	demo := "repo"
	if out, err := g.gh("repo", "view", repo, "--json", "isEmpty", "-q", ".isEmpty"); err != nil {
		return Result{Detail: "can't read " + repo + " with the org's token: " + tail(out, 200)}
	} else if out == "true" {
		demo = "demo"
	}
	nb := make([]byte, 4)
	rand.Read(nb)
	nonce := hex.EncodeToString(nb)
	fwd0, ref0, chk0 := b.Tally()
	script := `"$0" daemon >"$HIVE_HOME/daemon-github.log" 2>&1 &
d=$!
"$1" gate-drive "$2" "$3" "$4"
s=$?
"$0" shutdown >/dev/null 2>&1; sleep 1; kill $d 2>/dev/null
[ $s -eq 0 ] || tail -c 1500 "$HIVE_HOME/daemon-github.log"
exit $s`
	start := time.Now()
	out, err := c.Exec(githubProbeMax+2*time.Minute, c.Root, "sh", "-c", script, c.Bin, c.fwd(), repo, demo, nonce)
	secs := time.Since(start).Seconds()
	fwd1, ref1, chk1 := b.Tally()
	seen := fmt.Sprintf("the broker let %d calls through (%d reading checks) and refused %d", fwd1-fwd0, chk1-chk0, ref1-ref0)
	if err != nil {
		return Result{Detail: fmt.Sprintf("%v; %s: %s", err, seen, tail(out, 600))}
	}
	m := driveLine.FindStringSubmatch(out)
	if m == nil {
		return Result{Detail: "the driver named no pull request; " + seen + ": " + tail(out, 400)}
	}
	n, _ := strconv.Atoi(m[1])
	// Judged from GitHub, not from what passed through the sandbox.
	raw, err := g.gh("api", fmt.Sprintf("repos/%s/pulls/%d", repo, n))
	var pr struct {
		Merged bool   `json:"merged"`
		SHA    string `json:"merge_commit_sha"`
		Base   struct {
			Ref string `json:"ref"`
		} `json:"base"`
		Created time.Time `json:"created_at"`
	}
	if err != nil || json.Unmarshal([]byte(raw), &pr) != nil {
		return Result{Detail: fmt.Sprintf("can't read PR #%d in %s: %s", n, repo, tail(raw, 200))}
	}
	switch {
	case !pr.Merged:
		return Result{Detail: fmt.Sprintf("PR #%d in %s is not merged; %s", n, repo, seen)}
	case pr.Base.Ref != "main":
		return Result{Detail: fmt.Sprintf("PR #%d in %s merged into %s, not main", n, repo, pr.Base.Ref)}
	case pr.Created.Before(start.Add(-time.Minute)):
		return Result{Detail: fmt.Sprintf("PR #%d in %s was opened before this run", n, repo)}
	}
	return Result{Pass: true, Detail: fmt.Sprintf("PR #%d in %s merged at %s in %.0fs; %s", n, repo, short(pr.SHA), secs, seen),
		Metrics: []Metric{{Name: "seconds", Value: secs},
			{Name: "broker_calls", Value: float64(fwd1 - fwd0), Info: true},
			{Name: "check_reads", Value: float64(chk1 - chk0), Higher: true, Info: true}}}
}

var driveLine = regexp.MustCompile(`(?m)^GATE-DRIVE merged pr=([0-9]{1,9})$`)

// Drive is `hive gate-drive <owner/repo> demo|repo <nonce>`, run inside a
// side's sandbox beside the child's daemon (HIVE_HOME): it adds the
// repository as a project, asks for one change in it, and waits for that
// task to be done, then prints the pull request it merged. It holds no
// login and judges nothing: the gate reads the result from GitHub.
func Drive(args []string) int {
	if len(args) != 3 || !strings.Contains(args[0], "/") {
		fmt.Fprintln(os.Stderr, "usage: hive gate-drive <owner/repo> demo|repo <nonce>")
		return 2
	}
	if err := drive(args[0], args[1] == "demo", args[2]); err != nil {
		fmt.Println("gate-drive:", err)
		return 1
	}
	return 0
}

func drive(repo string, demo bool, nonce string) error {
	ws := core.Workspace{Root: os.Getenv("HIVE_HOME")}
	call := func(method, path string, body, out any) error {
		raw, err := os.ReadFile(ws.DaemonFile())
		if err != nil {
			return err
		}
		var info struct {
			Port  int    `json:"port"`
			Token string `json:"token"`
		}
		if err := json.Unmarshal(raw, &info); err != nil {
			return err
		}
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, err := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", info.Port, path), rd)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+info.Token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := (&http.Client{Timeout: 5 * time.Minute, Transport: &http.Transport{}}).Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, tail(string(data), 300))
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(data, out)
	}
	type task struct {
		Title  string `json:"title"`
		Stage  string `json:"stage"`
		Reason string `json:"reason"`
		PR     struct {
			Number int `json:"number"`
		} `json:"pr"`
		Log []struct {
			Text string `json:"text"`
		} `json:"log"`
	}
	var state struct {
		Projects []struct {
			Name  string `json:"name"`
			Ready bool   `json:"ready"`
			Tasks []task `json:"tasks"`
		} `json:"projects"`
	}
	up := time.Now().Add(time.Minute)
	for call("GET", "/api/state", nil, &state) != nil {
		if time.Now().After(up) {
			return fmt.Errorf("the daemon never answered")
		}
		time.Sleep(time.Second)
	}
	spec := map[string]any{"repo": repo}
	if demo {
		spec["demo"] = true
	} else {
		spec["name"] = "textkit"
	}
	var p struct {
		Name string `json:"name"`
	}
	if err := call("POST", "/api/project", spec, &p); err != nil {
		return fmt.Errorf("adding %s as a project: %v", repo, err)
	}
	// The title is the ask's first 60 characters: the nonce comes first.
	ask := "Probe " + nonce + ": add a line to NOTES.md FAKE:docs"
	if err := call("POST", "/api/chat", map[string]string{"chat": "project:" + p.Name, "text": ask}, nil); err != nil {
		return fmt.Errorf("asking: %v", err)
	}
	var last task
	for end := time.Now().Add(githubProbeMax); time.Now().Before(end); time.Sleep(3 * time.Second) {
		if call("GET", "/api/state", nil, &state) != nil {
			continue
		}
		for _, pv := range state.Projects {
			for _, t := range pv.Tasks {
				if pv.Name != p.Name || !strings.Contains(t.Title, nonce) {
					continue
				}
				last = t
				if t.Stage == "done" && t.PR.Number > 0 {
					fmt.Printf("GATE-DRIVE merged pr=%d\n", t.PR.Number)
					return nil
				}
			}
		}
	}
	if last.Title == "" {
		return fmt.Errorf("no task for the ask in %s after %s", p.Name, githubProbeMax)
	}
	var log []string
	for i := max(0, len(last.Log)-6); i < len(last.Log); i++ {
		log = append(log, clip(last.Log[i].Text, 200))
	}
	return fmt.Errorf("the task is %s after %s (%s): %s", last.Stage, githubProbeMax, clip(last.Reason, 200), strings.Join(log, " | "))
}
