package report

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/report/contracttest"
)

// gen makes a token body of n characters from alphabet, so no key-shaped
// literal sits in the source (push protection refuses those, fake or not).
func gen(n int, alphabet string) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[(i*7+3)%len(alphabet)]
	}
	return string(b)
}

const (
	alnum = "aB3cD4eF5gH6iJ7kL8mN9oP0qR1sT2uVwXyZ"
	upper = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
)

// Each line holds a secret (the second field) that must not survive Scrub.
var leaks = func() []struct{ in, secret string } {
	tok := func(prefix string, n int) string { return prefix + gen(n, alnum) }
	ghp, gho, pat := tok("gh"+"p_", 36), tok("gh"+"o_", 36), tok("github_"+"pat_11", 70)
	ant, oat, oai := tok("sk"+"-ant-api03-", 32), tok("sk"+"-ant-oat01-", 40), tok("sk"+"-proj-", 30)
	stripe, slack := tok("sk"+"_live_", 24), "xo"+"xb-2048-1234567890-"+gen(24, alnum)
	aws, awsSecret := "AK"+"IA"+gen(16, upper), gen(16, alnum)+"/K7MDENG/"+gen(16, alnum)
	google, gitlab := tok("AI"+"za", 35), tok("gl"+"pat-", 20)
	npm, hf := tok("np"+"m_", 36), tok("h"+"f_", 34)
	jwtSig := gen(43, alnum)
	return []struct{ in, secret string }{
		{"push failed: remote: Invalid username or token " + ghp, ghp},
		{"GH_TOKEN=" + gho + " gh pr create", gho},
		{"using " + pat, pat[:40]},
		{"ANTHROPIC_API_KEY: " + ant + "-jK2lQwErTy_AA", ant},
		{"CLAUDE_CODE_OAUTH_TOKEN=" + oat, oat[:32]},
		{"openai error for key " + oai, oai},
		{"stripe: " + stripe, stripe},
		{"slack " + slack + " said 401", slack},
		{"aws_access_key_id = " + aws, aws},
		{"export AWS_SECRET_ACCESS_KEY=" + awsSecret, awsSecret},
		{"maps key " + google + " in config", google},
		{"gitlab " + gitlab, gitlab},
		{"//registry.npmjs.org/:_authToken=" + npm, npm},
		{"HF " + hf, hf},
		{"Authorization: Bearer eyJ" + gen(20, alnum) + ".eyJ" + gen(24, alnum) + "." + jwtSig, jwtSig},
		{"Authorization: Basic dXNlcjpodW50ZXIy", "dXNlcjpodW50ZXIy"},
		{"curl -H 'Authorization: token 7f3a9c2e1b8d4f6a0e5c9b2d7a1f8e3c' api", "7f3a9c2e1b8d4f6a0e5c9b2d7a1f8e3c"},
		{"fatal: could not read from https://ihsan:hunter2pass@github.com/me/repo.git", "hunter2pass"},
		{"DATABASE_URL=postgres://admin:S3cr3tP%40ss@db.internal:5432/app", "S3cr3tP%40ss"},
		{`{"password": "correct-horse-battery", "user": "x"}`, "correct-horse-battery"},
		{"db_password=Tr0ub4dor&3", "Tr0ub4dor&3"},
		{"client_secret: 'q8w7e6r5t4y3'", "q8w7e6r5t4y3"},
		{"Set-Cookie: session=4f9a1c7e2b; Path=/", "4f9a1c7e2b"},
		{"Cookie: theme=dark; sid=hunter2pass", "hunter2pass"},
		{"api-key: 0123abcd", "0123abcd"},
		{"auth_token=9f8e7d6c5b", "9f8e7d6c5b"},
		{"authkey: k3yk3yk3y", "k3yk3yk3y"},
		{"X-Auth-Token: abc123def", "abc123def"},
		{"oauth_token=zzq1w2e3", "zzq1w2e3"},
		{"AUTH=plainpass1", "plainpass1"},
		{"authorization=dXNlcjpw", "dXNlcjpw"},
		{"TS_AUTHKEY=tskey9q8w7e6", "tskey9q8w7e6"},
		{"AUTH_KEY=k9j8h7g6f5", "k9j8h7g6f5"},
		{"auth_key=k9j8h7g6f5", "k9j8h7g6f5"},
		{"auth_header=h4h3h2h1", "h4h3h2h1"},
		{"auth_code=c0d3c0d3", "c0d3c0d3"},
		{"authorization-header=hunter2pass", "hunter2pass"},
		{"authorization.header=hunter2pass", "hunter2pass"},
		{"Authorization-Key: abc", ": abc"},
		{"authorization_code=abc", "=abc"},
		{"-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQ\nAAAAMwAAAAtzc2gtZW\n-----END OPENSSH PRIVATE KEY-----", "AAAAMwAAAAtzc2gtZW"},
		{"-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA7bq9 (clipped", "MIIEpAIBAAKCAQEA7bq9"},
		{"commit by Ihsan Salari <ihsan.salari@example.com>", "ihsan.salari@example.com"},
		{"mail me at a.b+hive@mail.co.uk please", "a.b+hive@mail.co.uk"},
		{"open /home/ihsan/hive/projects/x/main.go: no such file", "ihsan"},
		{"stat /Users/jane.doe/Library/hive: denied", "jane.doe"},
		{`C:\Users\Marta\hive\.hive\runs\x failed`, "Marta"},
		{"token at /root/.config/gh/hosts.yml", "/root"},
		{"daemon token 9b1e4c7a2f6d8e3b5a0c9f1e7d2b4a6c in daemon.json", "9b1e4c7a2f6d8e3b5a0c9f1e7d2b4a6c"},
		{"random blob Zm9vYmFyYmF6cXV4MTIzNDU2Nzg5MGFiY2RlZmdo end", "Zm9vYmFyYmF6cXV4MTIzNDU2Nzg5MGFiY2RlZmdo"},
	}
}()

func TestScrubLeavesNoSecret(t *testing.T) {
	for _, c := range leaks {
		if out := Scrub(c.in, ""); strings.Contains(out, c.secret) {
			t.Errorf("secret survived:\n  in:  %s\n  out: %s", c.in, out)
		}
	}
}

func TestScrubHomeAndRoot(t *testing.T) {
	home := core.HomeDir()
	out := Scrub("open "+filepath.Join(home, "x")+" and /srv/hive/.hive/events.jsonl", "/srv/hive")
	if strings.Contains(out, home) || strings.Contains(out, "/srv/hive") || !strings.Contains(out, "<hive>/.hive/events.jsonl") {
		t.Fatalf("home or Hive folder left: %s", out)
	}
}

// What a person needs to read a failure survives.
func TestScrubKeepsPlainText(t *testing.T) {
	for _, s := range []string{
		"stopped after 3 rounds of fixes; what remains: TestParseHeader fails",
		"the worker finished without any commits",
		"go test ./internal/verylongpackagenamewithoutdigitsatall/...",
		"exit status 1",
		"author: Ihsan",
		"authors: Ihsan, Marta",
		"Author = Ihsan",
	} {
		if out := Scrub(s, ""); out != s {
			t.Errorf("changed plain text:\n  in:  %s\n  out: %s", s, out)
		}
	}
}

// open makes a core on a fresh workspace with the given config.
func open(t *testing.T, edit func(*core.Config)) *core.Core {
	t.Helper()
	ws := core.Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	cfg := ws.LoadConfig()
	edit(&cfg)
	ws.SaveConfig(cfg)
	c, err := core.Open(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// sharing opens a core with sharing on since a minute ago, sending to url,
// and n doctor failures waiting.
func sharing(t *testing.T, url string, n int) *core.Core {
	c := open(t, func(cfg *core.Config) {
		cfg.SetShare(true)
		cfg.ShareURL, cfg.Share = url, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	})
	for i := 0; i < n; i++ {
		Note(c.WS, Failure{Kind: "doctor", Text: fmt.Sprint("check ", i)})
	}
	return c
}

func requests(e *contracttest.Endpoint) int { raw, _ := e.Got(); return len(raw) }

// copies says the local copies are byte for byte the bodies that left.
func copies(t *testing.T, ws core.Workspace, bodies [][]byte) {
	t.Helper()
	sent := Sent(ws)
	if len(sent) != len(bodies) {
		t.Fatalf("%d copies for %d reports sent", len(sent), len(bodies))
	}
	for i, name := range sent {
		if raw, _ := os.ReadFile(name); string(raw) != string(bodies[i]) {
			t.Fatalf("copy %s differs from what was sent:\n%s\n%s", name, raw, bodies[i])
		}
	}
}

func TestOffSendsNothing(t *testing.T) {
	e := contracttest.New(t)
	c := open(t, func(cfg *core.Config) { cfg.ShareURL = e.URL })
	c.J.MustAppend("task.saved", map[string]any{"id": "t1", "stage": "blocked", "reason": "no"})
	if res, _ := (&Module{C: c}).Send(); res != "sharing is off" || requests(e) != 0 || len(Sent(c.WS)) != 0 {
		t.Fatalf("off: %q, %d requests, %d copies", res, requests(e), len(Sent(c.WS)))
	}
}

// One send is one report per failure, oldest first, then the usage report.
func TestSendOnce(t *testing.T) {
	e := contracttest.New(t)
	c := open(t, func(cfg *core.Config) { cfg.ShareURL = e.URL })
	c.J.MustAppend("task.saved", map[string]any{"id": "old", "stage": "blocked", "reason": "before sharing was on"})
	cfg := c.WS.LoadConfig()
	cfg.SetShare(true) // in the same second as the old event, which stays home
	c.WS.SaveConfig(cfg)
	c.J.MustAppend("run.created", map[string]any{"id": "w1", "role": "worker"})
	c.J.MustAppend("run.turn_ended", map[string]any{"id": "w1", "status": "parked", "reason": "turn failed", "result": map[string]any{"text": "boom with " + leaks[0].secret, "is_error": true, "cost_usd": 0.5}})
	c.J.MustAppend("task.saved", map[string]any{"id": "t1", "stage": "working"})
	c.J.MustAppend("task.saved", map[string]any{"id": "t1", "stage": "stopped", "reason": "red tests", "log": []map[string]string{{"text": "tests failed"}}})
	c.J.MustAppend("task.saved", map[string]any{"id": "t1", "stage": "stopped", "reason": "red tests"})
	m := &Module{C: c}
	res, _ := m.Send()
	raw, got := e.Got()
	if res != "sent 3, refused 0" || len(got) != 3 {
		t.Fatalf("want 3 reports, got %d (%s)", len(got), res)
	}
	for _, r := range got {
		if r.Install != cfg.InstallID {
			t.Fatalf("install %q, want %q", r.Install, cfg.InstallID)
		}
	}
	if r := got[0]; r.Type != "failure" || r.Kind != "run-error:worker" || r.Message != "turn failed" || !strings.HasPrefix(r.Log, "boom with ") {
		t.Fatalf("first: %+v", r)
	}
	if r := got[1]; r.Type != "failure" || r.Kind != "task-stopped" || r.Log != "tests failed" {
		t.Fatalf("second: %+v", r)
	}
	if r := got[2]; r.Type != "usage" || r.Usage["runs"] != 1 || r.Usage["turns"] != 1 || r.Usage["cost_usd"] != 0.5 || r.At == "" {
		t.Fatalf("usage: %+v", r)
	}
	for _, b := range raw {
		if strings.Contains(string(b), leaks[0].secret) || strings.Contains(string(b), "before sharing") {
			t.Fatalf("a secret or an event from before sharing went out: %s", b)
		}
	}
	copies(t, c.WS, raw)
	if res, _ := m.Send(); res != "nothing new" || requests(e) != 3 {
		t.Fatalf("second send: %q, %d requests", res, requests(e))
	}
}

func TestUnreachableKeepsTheReports(t *testing.T) {
	c := sharing(t, "http://127.0.0.1:1/report", 1)
	c.J.MustAppend("alert.raised", map[string]string{"kind": "quiet", "text": "x"})
	m := &Module{C: c}
	if res, err := m.Send(); err != nil || !strings.Contains(res, "2 wait for the next run") || len(Sent(c.WS)) != 0 || len(Waiting(c.WS)) != 2 {
		t.Fatalf("unreachable: %q %v, %d waiting", res, err, len(Waiting(c.WS)))
	}
	e := contracttest.New(t)
	cfg := c.WS.LoadConfig()
	cfg.ShareURL = e.URL
	c.WS.SaveConfig(cfg)
	if res, _ := m.Send(); res != "sent 2, refused 0" || requests(e) != 2 || len(Waiting(c.WS)) != 0 {
		t.Fatalf("next run: %q, %d requests", res, requests(e))
	}
}

// A 400 drops the report for good; a 429 keeps it and the rest for the next
// run.
func TestAnswers(t *testing.T) {
	e := contracttest.New(t)
	e.Answer = func(n int) int {
		if code, ok := map[int]int{0: 400, 2: 429}[n]; ok {
			return code
		}
		return 202
	}
	c := sharing(t, e.URL, 4)
	m := &Module{C: c}
	if res, _ := m.Send(); res != "sent 1, refused 1; "+e.URL+" answered 429; 2 wait for the next run" || len(Waiting(c.WS)) != 2 {
		t.Fatalf("first run: %q, %d waiting", res, len(Waiting(c.WS)))
	}
	if res, _ := m.Send(); res != "sent 2, refused 0" || len(Waiting(c.WS)) != 0 {
		t.Fatalf("second run: %q", res)
	}
	raw, got := e.Got()
	if len(got) != 5 || got[0].Message != "check 0" || got[2].Message != "check 2" || string(raw[3]) != string(raw[2]) || got[4].Message != "check 3" {
		t.Fatalf("want check 0, 1, 2, then 2 again and 3: %+v", got)
	}
	copies(t, c.WS, append(raw[:2:2], raw[3:]...)) // the 429'd copy is gone
	if res, _ := m.Send(); res != "nothing new" {
		t.Fatalf("the refused report came back: %q", res)
	}
}

// A run sends at most 25: the newest failures, then a usage report that
// counts the rest.
func TestCap(t *testing.T) {
	e := contracttest.New(t)
	c := sharing(t, e.URL, 30)
	if res, _ := (&Module{C: c}).Send(); res != "sent 25, refused 0" {
		t.Fatalf("send: %q", res)
	}
	_, got := e.Got()
	if got[0].Message != "check 6" || got[23].Message != "check 29" || got[24].Type != "usage" || got[24].Usage["failures_dropped"] != 6 {
		t.Fatalf("first %+v, last %+v", got[0], got[24])
	}
}

// The default address speaks the schema version the contract tests hold the
// Hive to.
func TestDefaultURLIsTheContractVersion(t *testing.T) {
	if !strings.HasSuffix(DefaultURL, contracttest.Path) {
		t.Fatalf("%s doesn't end in the contract's %s", DefaultURL, contracttest.Path)
	}
}

// What the Hive makes fits the contract's patterns whatever it starts from.
func TestFieldsFitTheContract(t *testing.T) {
	var cfg core.Config
	cfg.SetShare(true)
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`).MatchString(cfg.InstallID) {
		t.Fatalf("install id %q", cfg.InstallID)
	}
	defer func(v string) { core.Version = v }(core.Version)
	for in, want := range map[string]string{"0.0.0-dev": "0.0.0-dev", "1.2.0+dirty build/x": "1.2.0+dirty-build-x", "": "0", strings.Repeat("9", 40): strings.Repeat("9", 32)} {
		if core.Version = in; Version() != want {
			t.Errorf("version %q became %q, want %q", in, Version(), want)
		}
	}
	for f, want := range map[Failure]string{{Kind: "run-error", Role: "Lead Dev"}: "run-error:lead-dev", {Kind: "Ünï"}: "-n-", {}: "unknown"} {
		if got := failure(cfg, f, "").Kind; got != want {
			t.Errorf("kind %+v became %q, want %q", f, got, want)
		}
	}
}

// A config that can't be read as on, or has no install id, sends nothing.
func TestOnlyAValidOptInSends(t *testing.T) {
	for _, edit := range []func(*core.Config){
		func(cfg *core.Config) { cfg.Share = "yes" },
		func(cfg *core.Config) { cfg.SetShare(false) },
		func(cfg *core.Config) { cfg.SetShare(true); cfg.InstallID = "" },
	} {
		c := open(t, edit)
		c.J.MustAppend("alert.raised", map[string]string{"kind": "quiet", "text": "x"})
		if res, err := (&Module{C: c}).Send(); err != nil || !strings.HasPrefix(res, "sharing ") || len(Sent(c.WS)) != 0 {
			t.Fatalf("share %q id %q: %q %v", c.WS.LoadConfig().Share, c.WS.LoadConfig().InstallID, res, err)
		}
	}
}

// A malformed address or a copy that can't be kept fails the job and keeps
// the report.
func TestBrokenSendKeepsTheReport(t *testing.T) {
	c := sharing(t, "http://%", 1)
	if _, err := (&Module{C: c}).Send(); err == nil || len(Waiting(c.WS)) != 1 {
		t.Fatalf("malformed url: %v, %d waiting", err, len(Waiting(c.WS)))
	}
	e := contracttest.New(t)
	c = sharing(t, e.URL, 1)
	os.WriteFile(filepath.Join(Dir(c.WS), "sent"), nil, 0o600)
	if _, err := (&Module{C: c}).Send(); err == nil || requests(e) != 0 || len(Waiting(c.WS)) != 1 {
		t.Fatalf("no copy: %v, %d requests, %d waiting", err, requests(e), len(Waiting(c.WS)))
	}
}

// A doctor failure noted while a run is sending waits for the next one.
func TestNotedDuringSendWaits(t *testing.T) {
	e := contracttest.New(t)
	c := sharing(t, e.URL, 1)
	e.Answer = func(int) int { Note(c.WS, Failure{Kind: "doctor", Text: "B"}); return 202 }
	if res, err := (&Module{C: c}).Send(); err != nil || len(Sent(c.WS)) != 1 {
		t.Fatalf("send: %q %v", res, err)
	}
	if fails, _, _, _ := (&Module{C: c}).Build(c.WS.LoadConfig()); len(fails) != 1 || fails[0].Message != "B" {
		t.Fatalf("want B waiting, got %+v", fails)
	}
}

// A doctor failure appended after a run read the outbox, but before the run
// marked it read, isn't lost: the next run builds it.
func TestNotedBeforeMarkWaits(t *testing.T) {
	e := contracttest.New(t)
	c := sharing(t, e.URL, 1)
	beforeMark = func() { Note(c.WS, Failure{Kind: "doctor", Text: "B"}) }
	defer func() { beforeMark = func() {} }()
	_, err := (&Module{C: c}).Send()
	beforeMark = func() {}
	if err != nil {
		t.Fatal(err)
	}
	if fails, _, _, _ := (&Module{C: c}).Build(c.WS.LoadConfig()); len(fails) != 1 || fails[0].Message != "B" {
		t.Fatalf("want B waiting, got %+v", fails)
	}
	if res, _ := (&Module{C: c}).Send(); !strings.HasPrefix(res, "sent 1") {
		t.Fatalf("next run: %q", res)
	}
	if fails, _, _, _ := (&Module{C: c}).Build(c.WS.LoadConfig()); len(fails) != 0 {
		t.Fatalf("B built twice: %+v", fails)
	}
}

// A resumed session's result lines carry its running total, so the usage
// report counts the rise, not each total again.
func TestUsageCostOfResumedSession(t *testing.T) {
	c := open(t, func(cfg *core.Config) { cfg.SetShare(true) })
	c.J.MustAppend("run.created", map[string]any{"id": "lead", "role": "lead"})
	for i, total := range []float64{0.5, 0.75, 1} {
		c.J.MustAppend("run.turn_started", map[string]any{"id": "lead", "session": "s1", "turn": i + 1, "resume": i > 0})
		c.J.MustAppend("run.turn_ended", map[string]any{"id": "lead", "status": "idle", "session": "s1", "result": map[string]any{"text": "ok", "cost_usd": total}})
	}
	if _, usage, _, _ := (&Module{C: c}).Build(c.WS.LoadConfig()); usage.Usage["cost_usd"] != 1 || usage.Usage["turns"] != 3 {
		t.Fatalf("usage %+v, want $1 over 3 turns", usage.Usage)
	}
}
