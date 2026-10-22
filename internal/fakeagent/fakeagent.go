// Package fakeagent is a scripted agent that speaks Claude Code's
// stream-json and uses the real mailbox. Keywords in the brief steer it
// (FAKE:block, FAKE:breaktests, FAKE:lowerscore, FAKE:reviewfix, FAKE:crash,
// FAKE:limit, FAKE:needpackage), so tests and the release
// dry run drive every path of the Hive without spending plan usage.
package fakeagent

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/agentcli"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// Main is `hive fake-agent --role R --session S [--resume] [--schema]`.
func Main(argv []string) int {
	var role, session string
	resume, schema := false, false
	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "--role":
			i++
			role = argv[i]
		case "--session":
			i++
			session = argv[i]
		case "--resume":
			resume = true
		case "--schema":
			schema = true
		}
	}
	in, _ := io.ReadAll(os.Stdin)
	prompt := string(in)
	brief, _ := os.ReadFile(filepath.Join(os.Getenv("HIVE_RUN"), "brief.md"))
	all := string(brief) + "\n" + prompt
	emit(map[string]any{"type": "system", "subtype": "init", "session_id": session, "model": "fake"})

	if strings.Contains(all, "FAKE:crash") && !resume {
		os.Exit(3)
	}
	if strings.Contains(all, "FAKE:limit") && !resume {
		reset := time.Now().Add(3 * time.Second).Unix()
		emit(map[string]any{"type": "rate_limit_event", "rate_limit_info": map[string]any{"status": "rejected", "resetsAt": reset, "rateLimitType": "five_hour", "utilization": 1.0}})
		emit(map[string]any{"type": "result", "subtype": "success", "is_error": true, "result": "Claude AI usage limit reached|" + fmt.Sprint(reset), "session_id": session})
		return 0
	}
	emit(map[string]any{"type": "rate_limit_event", "rate_limit_info": map[string]any{"status": "allowed", "resetsAt": time.Now().Add(5 * time.Hour).Unix(), "rateLimitType": "five_hour", "utilization": 0.1}})

	text, structured := "ok", any(nil)
	var err error
	switch role {
	case "lead":
		text, err = lead(prompt)
	case "worker":
		text, err = worker(all, prompt)
	case "reviewer":
		structured = review(all)
		raw, _ := json.Marshal(structured)
		text = string(raw)
	case "sweep":
		_, err = agentcli.Call("brief", map[string]string{"title": "FAKE sweep: find the next real gain", "done": "score improves", "known": "evidence: fake"}, time.Minute)
		if err == nil {
			_, err = agentcli.Call("finish", map[string]string{"status": "done", "account": "filed one ask"}, time.Minute)
		}
		text = "swept"
	default:
		_, err = agentcli.Call("finish", map[string]string{"status": "done", "account": "fake " + role + " done"}, time.Minute)
	}
	if err != nil {
		text = "error: " + err.Error()
	}
	res := map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": text, "session_id": session, "total_cost_usd": 0.0}
	if schema && structured != nil {
		res["structured_output"] = structured
	}
	emit(res)
	return 0
}

func emit(v any) {
	raw, _ := json.Marshal(v)
	os.Stdout.Write(append(raw, '\n'))
}

// Messages that queued during a turn arrive as one prompt, joined by core.PendingSep.
var personRe = regexp.MustCompile(`(?s)Person: (.*?)(?:` + regexp.QuoteMeta(core.PendingSep) + `|\z)`)
var projectRe = regexp.MustCompile(`create (?:an? )?(isolated )?project (?:called |named )?([A-Za-z0-9_-]+)`)

func lead(prompt string) (string, error) {
	ms := personRe.FindAllStringSubmatch(prompt, -1)
	if ms == nil {
		return "NOOP", nil
	}
	var replies []string
	for _, m := range ms {
		reply, err := leadAsk(strings.TrimSpace(m[1]))
		if err != nil {
			return "", err
		}
		replies = append(replies, reply)
	}
	return strings.Join(replies, "\n"), nil
}

func leadAsk(ask string) (string, error) {
	if pm := projectRe.FindStringSubmatch(ask); pm != nil {
		_, err := agentcli.Call("create_project", map[string]any{"name": pm[2], "isolated": pm[1] != "", "description": ask, "goals": ask}, 3*time.Minute)
		if err != nil {
			return "", err
		}
		return "Created project " + pm[2] + ".", nil
	}
	if mm := regexp.MustCompile(`install module (\S+)`).FindStringSubmatch(ask); mm != nil {
		_, err := agentcli.Call("install_module", map[string]string{"url": mm[1]}, 3*time.Minute)
		if err != nil {
			return "", err
		}
		return "Asked you to approve installing it.", nil
	}
	if strings.Contains(strings.ToLower(ask), "what's in the repo") {
		return "The repo has " + strings.Join(repoFiles(), ", ") + ".", nil
	}
	if strings.HasPrefix(strings.ToLower(ask), "hello") {
		return "Hello. I'm the lead. Tell me what you'd like done.", nil
	}
	_, err := agentcli.Call("brief", map[string]string{"title": clip(ask, 60), "done": "FAKE: the change is merged", "known": ask}, 3*time.Minute)
	if err != nil {
		return "", err
	}
	return "Understood: " + clip(ask, 80) + ". I've started a piece of work on it.", nil
}

// repoFiles lists what a lead sees of its project: its checkout, brought to
// main from $HIVE_RUN/main.bundle, when it has one (an isolated project's
// lead), otherwise the project's clone in $HIVE_PROJECTS.
func repoFiles() []string {
	dir := "."
	if _, err := os.Stat(".git"); err != nil {
		dir = filepath.Join(os.Getenv("HIVE_PROJECTS"), os.Getenv("HIVE_PROJECT"))
	} else if _, err := run("git", "fetch", "-q", filepath.Join(os.Getenv("HIVE_RUN"), "main.bundle"), "main"); err == nil {
		run("git", "checkout", "-q", "-f", "FETCH_HEAD")
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func worker(all, prompt string) (string, error) {
	if strings.Contains(all, "FAKE:block") {
		_, err := agentcli.Call("finish", map[string]string{"status": "blocked", "account": "FAKE: blocked on purpose"}, time.Minute)
		return "blocked", err
	}
	if strings.Contains(all, "FAKE:needpackage") && !strings.Contains(prompt, "[the person answered") {
		_, err := agentcli.Call("warden", map[string]string{"want": "install the left-pad package", "why": "the brief needs it", "steps": "echo installing left-pad"}, time.Minute)
		return "waiting for the person", err
	}
	agentcli.Call("progress", map[string]string{"text": "making the change"}, time.Minute)
	switch {
	case strings.Contains(all, "FAKE:breaktests"):
		os.WriteFile("BROKEN", []byte("tests fail while this exists\n"), 0o644)
	case strings.Contains(all, "FAKE:lowerscore"):
		os.WriteFile("score.txt", []byte("1\n"), 0o644)
	case strings.Contains(all, "FAKE:raisescore"):
		os.WriteFile("score.txt", []byte("9\n"), 0o644)
	case strings.Contains(all, "FAKE:docs"):
		appendFile("NOTES.md", "- a small doc change\n")
	case strings.Contains(prompt, "conflict"):
		if out, err := run("git", "fetch", "-q", filepath.Join(os.Getenv("HIVE_RUN"), "main.bundle"), "main"); err != nil {
			return "", fmt.Errorf("fetch main: %v %s", err, out)
		}
		run("git", "merge", "-X", "ours", "-m", "Merge main", "FETCH_HEAD")
	}
	n := time.Now().UnixNano()
	os.MkdirAll("work", 0o755)
	os.WriteFile(filepath.Join("work", fmt.Sprintf("change-%d.code", n)), []byte("change\n"), 0o644)
	if out, err := run("git", "add", "-A"); err != nil {
		return "", fmt.Errorf("git add: %v %s", err, out)
	}
	if out, err := run("git", "commit", "-q", "-m", "fake work"); err != nil {
		return "", fmt.Errorf("git commit: %v %s", err, out)
	}
	_, err := agentcli.Call("finish", map[string]string{"status": "done", "account": "FAKE: made the change", "tested": "ran nothing", "unsure": "everything"}, time.Minute)
	return "done", err
}

func review(all string) map[string]any {
	if strings.Contains(all, "FAKE:reviewfix") && strings.Contains(all, "Review round 1") {
		return map[string]any{"verdict": "fix", "summary": "needs one more pass", "findings": []map[string]any{{"file": "work", "issue": "FAKE finding"}}}
	}
	return map[string]any{"verdict": "land", "summary": "looks right", "findings": []any{}}
}

func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

func appendFile(path, s string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		f.WriteString(s)
		f.Close()
	}
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n]
	}
	return s
}
