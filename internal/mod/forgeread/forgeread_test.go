package forgeread

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// apiForge is the local forge with an API on the login that counts its reads.
type apiForge struct {
	*core.Local
	gets  []string
	limit bool
	gone  bool // GitHub answers 404
}

func (f *apiForge) APIGet(repo, path string) (string, error) {
	if f.limit {
		return "", &core.RateLimited{Until: time.Date(2026, 10, 24, 9, 30, 0, 0, time.UTC), Text: "HTTP 403: API rate limit exceeded"}
	}
	f.gets = append(f.gets, path)
	if f.gone {
		return "", errors.New("gh api " + path + ": gh: Not Found (HTTP 404)")
	}
	return `{"path":"` + path + `"}`, nil
}

func (f *apiForge) RateReset(string) (time.Time, error) { return time.Now(), nil }

func setup(t *testing.T) (*Module, *apiForge, *time.Time) {
	dir := t.TempDir()
	f := &apiForge{Local: &core.Local{Dir: filepath.Join(dir, "forge")}}
	c, err := core.Open(core.Workspace{Root: filepath.Join(dir, "hive")}, f)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	for _, p := range []core.Project{{Name: "p", Repo: "me/p"}, {Name: "q", Repo: "me/q"}, {Name: "secret", Repo: "me/secret", Isolated: true}} {
		if _, err := c.Projects.Add(p); err != nil {
			t.Fatal(err)
		}
	}
	m := New(c)
	now := time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)
	m.Now = func() time.Time { return now }
	return m, f, &now
}

func call(m *Module, project, args string) (string, error) {
	r := core.Run{RunSpec: core.RunSpec{ID: "w1", Role: "worker", Project: project}}
	out, err := m.C.Runs.Ops().Call(r, "github", json.RawMessage(args))
	s, _ := out.(string)
	return s, err
}

// An agent's reads of PRs, checks and annotations go to the login, once a
// minute however often they are asked.
func TestReadsGoThroughTheLoginCached(t *testing.T) {
	m, f, now := setup(t)
	for args, want := range map[string]string{
		`{"pr":"407"}`:                         "repos/me/p/pulls/407",
		`{"pr":407}`:                           "repos/me/p/pulls/407",
		`{"checks":"cbca162"}`:                 "repos/me/p/commits/cbca162/check-runs?per_page=100",
		`{"annotations":"99","repo":"me/q"}`:   "repos/me/q/check-runs/99/annotations?per_page=100",
		`{"annotations":30000000000}`:          "repos/me/p/check-runs/30000000000/annotations?per_page=100",
		`{"path":"/repos/me/p/pulls/3/files"}`: "repos/me/p/pulls/3/files",
	} {
		out, err := call(m, "p", args)
		if err != nil || !strings.Contains(out, want) {
			t.Errorf("%s: %q, %v; want %s", args, out, err, want)
		}
	}
	if len(f.gets) != 5 {
		t.Fatalf("the same PR read twice within a minute reached GitHub: %q", f.gets)
	}
	*now = now.Add(2 * time.Minute)
	call(m, "p", `{"pr":"407"}`)
	if len(f.gets) != 6 {
		t.Fatalf("a stale answer was served: %q", f.gets)
	}
}

// Reads stay inside what the run can see, as safe paths only.
func TestReadsStayInReach(t *testing.T) {
	m, f, _ := setup(t)
	for project, args := range map[string]string{
		"p":      `{"path":"repos/me/secret/pulls/1"}`,
		"secret": `{"path":"repos/me/p/pulls/1"}`,
		"":       `{"path":"repos/someone/else/pulls/1"}`,
	} {
		if _, err := call(m, project, args); err == nil {
			t.Errorf("%s read %s", project, args)
		}
	}
	for _, args := range []string{`{"path":"repos/me/p/../q/pulls/1"}`, `{"path":"repos/me/p/%2e%2e/q/pulls/1"}`, `{"path":"user"}`, `{"path":"-X"}`, `{"pr":"1; rm"}`, `{}`,
		`{"path":"repos/me/p/hooks"}`, `{"path":"repos/me/p/hooks/1/config"}`, `{"path":"repos/me/p/actions/variables"}`,
		`{"path":"repos/me/p/actions/secrets"}`, `{"path":"repos/me/p/environments/prod/variables"}`, `{"path":"repos/me/p/keys"}`,
		`{"path":"repos/me/p/collaborators"}`, `{"path":"repos/me/p/invitations"}`, `{"path":"repos/me/p/pulls/1/../../hooks"}`,
		`{"path":"repos/./p/pulls/1"}`, `{"path":"repos/me/p/./hooks"}`, `{"path":"repos/me/p/pulls?per_page=1;x"}`,
		`{"path":"repos/me/p/pulls?q=a b"}`, `{"path":"repos/me/p/pulls?per_page=1#x"}`} {
		if _, err := call(m, "p", args); err == nil {
			t.Errorf("read %s", args)
		}
	}
	if _, err := call(m, "secret", `{"pr":"1"}`); err != nil {
		t.Errorf("an isolated project can't read its own repo: %v", err)
	}
	for _, args := range []string{`{"path":"repos/me/p/pulls?per_page=5&state=closed"}`, `{"path":"repos/me/p/contents/docs/a.md?ref=main"}`} {
		if _, err := call(m, "p", args); err != nil {
			t.Errorf("an allowed query refused: %s: %v", args, err)
		}
	}
	if len(f.gets) != 3 {
		t.Fatalf("reads out of reach reached GitHub: %q", f.gets)
	}
}

// A rate-limited read says when to try again; agents' reads stop at the budget.
func TestRateLimitAndBudget(t *testing.T) {
	m, f, now := setup(t)
	f.limit = true
	if _, err := call(m, "p", `{"pr":"1"}`); err == nil || !strings.Contains(err.Error(), "rate limit") || !strings.Contains(err.Error(), "09:30Z") {
		t.Fatalf("rate-limited read: %v", err)
	}
	f.limit = false
	for i := 1; i < budget; i++ {
		if _, err := call(m, "p", `{"pr":"`+strconv.Itoa(i)+`"}`); err != nil {
			t.Fatal(err)
		}
		*now = now.Add(time.Second)
	}
	if _, err := call(m, "p", `{"pr":"`+strconv.Itoa(budget-1)+`"}`); err != nil {
		t.Fatalf("a cached answer past the budget: %v", err)
	}
	if _, err := call(m, "p", `{"pr":"1000"}`); err == nil || !strings.Contains(err.Error(), "most they may") {
		t.Fatalf("past the budget: %v", err)
	}
}

// A path GitHub refuses is kept as an answer is: asked again within the
// minute, it costs no read.
func TestRefusalsAreCached(t *testing.T) {
	m, f, now := setup(t)
	f.gone = true
	for range 3 {
		if _, err := call(m, "p", `{"pr":"404"}`); err == nil || !strings.Contains(err.Error(), "404") {
			t.Fatalf("refused read: %v", err)
		}
	}
	if len(f.gets) != 1 {
		t.Fatalf("a refusal asked again reached GitHub: %q", f.gets)
	}
	f.gone = false
	*now = now.Add(2 * time.Minute)
	if _, err := call(m, "p", `{"pr":"404"}`); err != nil || len(f.gets) != 2 {
		t.Fatalf("a stale refusal was served: %v, %q", err, f.gets)
	}
}
