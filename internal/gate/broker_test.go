package gate

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGitHub is GitHub's API and git hosts for a broker under test: it
// records what reached it, with which token, and answers from answer.
type fakeGitHub struct {
	srv    *httptest.Server
	mu     sync.Mutex
	got    []string // "METHOD host path auth"
	bodies []string
	answer func(r *http.Request, body []byte) (int, string)
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	f := &fakeGitHub{}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.got = append(f.got, r.Method+" "+r.Host+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
		f.bodies = append(f.bodies, string(body))
		f.mu.Unlock()
		code, out := 200, "{}"
		if f.answer != nil {
			code, out = f.answer(r, body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		io.WriteString(w, out)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHub) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.got...)
}

// testBroker is a broker on Hive-RSI-Lab whose GitHub is f.
func testBroker(t *testing.T, f *fakeGitHub, repos ...string) *Broker {
	b, err := NewBroker("Hive-RSI-Lab", "org-secret")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(f.srv.URL)
	for h := range b.up {
		b.up[h] = u
	}
	b.client = f.srv.Client()
	b.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for _, r := range repos {
		b.Allow(r)
	}
	return b
}

// sideEnv is what a side's gh and git get: the meter as their proxy, the
// broker's trust, a home of their own.
func sideEnv(t *testing.T, m *Meter, b *Broker) []string {
	dir := t.TempDir()
	trust := filepath.Join(dir, "trust.pem")
	if err := b.WriteTrust(trust); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "XDG_CONFIG_HOME=" + filepath.Join(dir, ".config"),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GH_NO_UPDATE_NOTIFIER=1", "GH_HOST=github.com",
		"TMPDIR=" + dir} // gh caches API answers under $TMPDIR for a day: not across runs
	env = append(env, m.Env(false)...)
	return append(env, TrustEnv(trust)...)
}

// gitHubAnswers is enough of GitHub for gh's pull request commands on
// Hive-RSI-Lab/gate-x-candidate, and node lookups that place PR_1 there and
// PR_other in Hive-RSI-Lab/hive.
func gitHubAnswers(r *http.Request, body []byte) (int, string) {
	if r.URL.Path != "/graphql" {
		if strings.HasPrefix(r.URL.Path, "/repos/Hive-RSI-Lab/gate-x-candidate") {
			return 200, `{"id":101,"node_id":"R_cand","full_name":"Hive-RSI-Lab/gate-x-candidate"}`
		}
		return 200, `{}`
	}
	var q struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	json.Unmarshal(body, &q)
	pr := `{"id":"PR_1","number":3,"state":"OPEN","isDraft":true,"url":"https://github.com/Hive-RSI-Lab/gate-x-candidate/pull/3","headRefOid":"abc","title":"t","headRefName":"b","isCrossRepository":false,"headRepositoryOwner":{"id":"O","login":"Hive-RSI-Lab"},"mergeCommit":null}`
	switch {
	case q.Query == whereQuery:
		where := map[string]string{"PR_1": "Hive-RSI-Lab/gate-x-candidate", "PR_other": "Hive-RSI-Lab/hive"}[fmt.Sprint(q.Variables["id"])]
		if where == "" {
			return 200, `{"data":{"node":null}}`
		}
		return 200, `{"data":{"node":{"__typename":"PullRequest","repository":{"nameWithOwner":"` + where + `"}}}}`
	case strings.Contains(q.Query, "__type("):
		// gh's feature check asks for some of these types per query, and
		// refuses an answer with one it didn't ask for.
		var types []string
		for _, ty := range []string{"PullRequest", "StatusCheckRollupContextConnection", "WorkflowRun"} {
			if strings.Contains(q.Query, ty+": __type(") {
				types = append(types, `"`+ty+`":{"fields":[{"name":"id"}]}`)
			}
		}
		return 200, `{"data":{` + strings.Join(types, ",") + `}}`
	case strings.Contains(q.Query, "RepositoryInfo"):
		return 200, `{"data":{"repository":{"id":"R_cand","name":"gate-x-candidate","owner":{"login":"Hive-RSI-Lab"},"viewerPermission":"ADMIN","defaultBranchRef":{"name":"main"},"parent":null,"mergeCommitAllowed":true,"squashMergeAllowed":true,"rebaseMergeAllowed":true}}}`
	case strings.Contains(q.Query, "createPullRequest"):
		return 200, `{"data":{"createPullRequest":{"pullRequest":` + pr + `}}}`
	case strings.HasPrefix(strings.TrimSpace(q.Query), "mutation"):
		return 200, `{"data":{}}`
	case strings.Contains(q.Query, "pullRequest("):
		return 200, `{"data":{"repository":{"pullRequest":` + pr + `}}}`
	}
	return 200, `{"data":{"viewer":{"login":"ihsan-sa"},"repository":{"pullRequests":{"totalCount":0,"nodes":[],"pageInfo":{"hasNextPage":false}}}}}`
}

// A side's gh does the forge's work on its own repository through the
// broker, which puts the org's token in place of the side's placeholder;
// anything outside its repository is refused at the broker and never
// reaches GitHub.
func TestBrokerIsTheSidesGitHub(t *testing.T) {
	if _, err := exec.LookPath("gh"); err != nil {
		t.Skip("no gh")
	}
	f := newFakeGitHub(t)
	f.answer = gitHubAnswers
	b := testBroker(t, f, "gate-x-candidate")
	b.Log = t.Logf
	m, err := StartMeter(filepath.Join(shortTmp(), "bt.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	m.SetBroker(b)
	env := sideEnv(t, m, b)
	gh := func(args ...string) (string, error) {
		cmd := exec.Command("gh", args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	const repo = "Hive-RSI-Lab/gate-x-candidate"
	for _, args := range [][]string{
		{"api", "repos/" + repo},
		{"api", "repos/" + repo + "/commits/abc/check-runs"},
		{"pr", "list", "-R", repo, "--head", "b", "--state", "all", "--json", "number,url,state,headRefOid,mergeable,mergeStateStatus,isDraft,mergeCommit,headRepositoryOwner"},
		{"pr", "view", "3", "-R", repo, "--json", "number,url,state,headRefOid,mergeable,mergeStateStatus,isDraft,body,mergeCommit,headRepositoryOwner"},
		{"pr", "view", "3", "-R", repo},
		{"pr", "create", "-R", repo, "--head", "b", "--base", "main", "--title", "t", "--body", "b"},
		{"pr", "ready", "3", "-R", repo},
		{"pr", "merge", "3", "-R", repo, "--squash", "--match-head-commit", "abc", "--subject", "s"},
		{"pr", "close", "3", "-R", repo, "--comment", "c"},
	} {
		if out, err := gh(args...); err != nil {
			t.Errorf("gh %s: %v %s", strings.Join(args, " "), err, out)
		}
	}
	before := len(f.requests())
	for _, args := range [][]string{
		{"api", "repos/Hive-RSI-Lab/hive"},
		{"api", "repos/Hive-RSI-Lab/gate-x-candidate/keys"},
		{"api", "-X", "DELETE", "repos/" + repo},
		{"api", "-X", "POST", "repos/" + repo + "/forks"},
		{"api", "orgs/Hive-RSI-Lab/repos"},
		{"api", "graphql", "-f", "query=query{organization(login:\"Hive-RSI-Lab\"){repositories(first:5){nodes{name}}}}"},
		{"api", "graphql", "-f", "query=query{repository(owner:\"Hive-RSI-Lab\",name:\"gate-x-candidate\"){owner{... on Organization{repositories(first:5){nodes{name}}}}}}"},
		{"pr", "view", "1", "-R", "Hive-RSI-Lab/hive"},
	} {
		if out, err := gh(args...); err == nil || !strings.Contains(out, "403") {
			t.Errorf("gh %s got through: %v %s", strings.Join(args, " "), err, out)
		}
	}
	if got := f.requests()[before:]; len(got) != 0 {
		t.Errorf("refused calls reached GitHub: %q", got)
	}
	// A mutation on a node elsewhere is refused once GitHub says where it is.
	out, err := gh("api", "graphql", "-f", "query=mutation($id:ID!){mergePullRequest(input:{pullRequestId:$id}){clientMutationId}}", "-f", "id=PR_other")
	if err == nil || !strings.Contains(out, "not in this run's repositories") {
		t.Errorf("a merge elsewhere got through: %v %s", err, out)
	}
	for _, r := range f.requests() {
		if !strings.HasSuffix(r, " Bearer org-secret") {
			t.Errorf("GitHub saw %q, not the org's token", r)
		}
		if strings.Contains(r, brokerPlaceholder) {
			t.Errorf("the side's placeholder reached GitHub: %q", r)
		}
	}
}

// git over HTTPS reaches the side's own repository with the org's token, and
// no other.
func TestBrokerGit(t *testing.T) {
	f := newFakeGitHub(t)
	f.answer = func(r *http.Request, _ []byte) (int, string) { return 404, "" }
	b := testBroker(t, f, "gate-x-candidate")
	m, err := StartMeter(filepath.Join(shortTmp(), "bg.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	m.SetBroker(b)
	env := sideEnv(t, m, b)
	git := func(url string) string {
		cmd := exec.Command("git", "ls-remote", url)
		cmd.Env = env
		out, _ := cmd.CombinedOutput()
		return string(out)
	}
	git("https://github.com/Hive-RSI-Lab/gate-x-candidate.git")
	got := f.requests()
	if len(got) == 0 || !strings.Contains(got[0], "GET") || !strings.Contains(got[0], "/Hive-RSI-Lab/gate-x-candidate.git/info/refs Basic "+basic("org-secret")) {
		t.Fatalf("git's call reached GitHub as %q", got)
	}
	if out := git("https://github.com/Hive-RSI-Lab/hive.git"); !strings.Contains(out, "403") {
		t.Errorf("another repository: %s", out)
	}
	if len(f.requests()) != len(got) {
		t.Errorf("a refused git call reached GitHub: %q", f.requests())
	}
	if m.Bytes() == 0 {
		t.Error("the meter counted nothing that went through the broker")
	}
}

// What the broker lets through to GitHub, without asking GitHub anything.
func TestBrokerPolicy(t *testing.T) {
	b, err := NewBroker("Hive-RSI-Lab", "org-secret")
	if err != nil {
		t.Fatal(err)
	}
	b.Allow("gate-x-candidate")
	b.ids["101"], b.ids["R_cand"] = "gate-x-candidate", "gate-x-candidate"
	b.nodes["R_cand"], b.nodes["PR_1"], b.nodes["PR_other"] = "Hive-RSI-Lab/gate-x-candidate", "Hive-RSI-Lab/gate-x-candidate", "Hive-RSI-Lab/hive"
	const r = "/repos/Hive-RSI-Lab/gate-x-candidate"
	for _, c := range []struct {
		host, method, path string
		ok                 bool
	}{
		{"api.github.com", "GET", r, true},
		{"api.github.com", "GET", "/repos/hive-rsi-lab/GATE-X-CANDIDATE/pulls", true},
		{"api.github.com", "POST", r + "/pulls", true},
		{"api.github.com", "PUT", r + "/pulls/3/merge", true},
		{"api.github.com", "DELETE", r + "/git/refs/heads/b", true},
		{"api.github.com", "GET", r + "/actions/runs", true},
		{"api.github.com", "GET", r + "/actions/jobs/9/logs", true},
		{"api.github.com", "POST", r + "/releases", true},
		{"api.github.com", "GET", "/repositories/101/pulls", true},
		{"api.github.com", "GET", "/user", true},
		{"uploads.github.com", "POST", r + "/releases/5/assets", true},
		{"api.github.com", "PATCH", r, false},
		{"api.github.com", "DELETE", r, false},
		{"api.github.com", "GET", r + "/keys", false},
		{"api.github.com", "POST", r + "/hooks", false},
		{"api.github.com", "PUT", r + "/collaborators/someone", false},
		{"api.github.com", "GET", r + "/actions/secrets", false},
		{"api.github.com", "PUT", r + "/branches/main/protection", false},
		{"api.github.com", "POST", r + "/transfer", false},
		{"api.github.com", "POST", r + "/forks", false},
		{"api.github.com", "PUT", r + "/topics", false},
		{"api.github.com", "GET", r + "/../hive", false},
		{"api.github.com", "GET", "/repos/Hive-RSI-Lab/hive", false},
		{"api.github.com", "GET", "/repos/ihsan-sa/gate-x-candidate", false},
		{"api.github.com", "POST", "/orgs/Hive-RSI-Lab/repos", false},
		{"api.github.com", "GET", "/user/repos", false},
		{"uploads.github.com", "POST", r + "/contents/x", false},
	} {
		if why := b.restOK(c.host, c.method, c.path); (why == "") != c.ok {
			t.Errorf("%s %s%s: allowed %v (%s)", c.method, c.host, c.path, why == "", why)
		}
	}
	for _, c := range []struct {
		body string
		ok   bool
	}{
		{`{"query":"query($o:String!,$n:String!){repository(owner:$o,name:$n){pullRequest(number:3){id author{login} commits(last:1){nodes{commit{statusCheckRollup{state}}}}}}}","variables":{"o":"Hive-RSI-Lab","n":"gate-x-candidate"}}`, true},
		{`{"query":"{ viewer { login } }"}`, true},
		{`{"query":"mutation($i:CreatePullRequestInput!){createPullRequest(input:$i){pullRequest{url}}}","variables":{"i":{"repositoryId":"R_cand","baseRefName":"main","headRefName":"b","title":"t"}}}`, true},
		{`{"query":"mutation{mergePullRequest(input:{pullRequestId:\"PR_1\",expectedHeadOid:\"abc\"}){clientMutationId}}"}`, true},
		{`{"query":"mutation{createCommitOnBranch(input:{branch:{repositoryNameWithOwner:\"Hive-RSI-Lab/gate-x-candidate\",branchName:\"b\"},expectedHeadOid:\"abc\"}){clientMutationId}}"}`, true},
		{`{"query":"mutation{createCommitOnBranch(input:{branch:{repositoryNameWithOwner:\"Hive-RSI-Lab/hive\",branchName:\"b\"},expectedHeadOid:\"abc\"}){clientMutationId}}"}`, false},
		{`{"query":"mutation{mergePullRequest(input:{pullRequestId:\"PR_other\"}){clientMutationId}}"}`, false},
		{`{"query":"mutation{deleteRepository(input:{repositoryId:\"R_cand\"}){clientMutationId}}"}`, false},
		{`{"query":"mutation{createRepository(input:{name:\"x\"}){clientMutationId}}"}`, false},
		{`{"query":"{ repository(owner:\"Hive-RSI-Lab\",name:\"hive\"){ id } }"}`, false},
		{`{"query":"{ node(id:\"PR_other\"){ id } }"}`, false},
		{`{"query":"{ node(id:\"PR_1\"){ ... on PullRequest { title } } }"}`, true},
		{`{"query":"{ viewer { repositories(first:5){ nodes { name } } } }"}`, false},
		{`{"query":"{ search(query:\"org:Hive-RSI-Lab\", type: REPOSITORY, first: 5){ issueCount } }"}`, false},
		{`{"query":"{ repository(owner:\"Hive-RSI-Lab\",name:\"gate-x-candidate\"){ pullRequest(number:1){ headRepository { object(expression:\"main:x\"){ ... on Blob { text } } } } } }"}`, false},
		{`{"query":"{ repository(owner:\"Hive-RSI-Lab\",name:\"gate-x-candidate\"){ ...F } } fragment F on Repository { owner { ... on Organization { repository(name:\"hive\"){ id } } } }"}`, false},
		{`{"query":"subscription { x }"}`, false},
		{`{"query":"{ repository(owner:\"Hive-RSI-Lab\", name:\"gate-x-candidate\") { id "}`, false},
	} {
		if why := b.graphqlOK([]byte(c.body)); (why == "") != c.ok {
			t.Errorf("%s: allowed %v (%s)", c.body, why == "", why)
		}
	}
}

// A child, bare or under bubblewrap (where the meter is its only way out),
// reaches its own repository through the broker with the environment the
// gate gives it, and holds no login: GitHub sees only the org's token.
func TestChildReachesItsRepoThroughTheBroker(t *testing.T) {
	if _, err := exec.LookPath("gh"); err != nil {
		t.Skip("no gh")
	}
	for _, sb := range []string{"bare", "bwrap"} {
		t.Run(sb, func(t *testing.T) {
			if sb == "bwrap" {
				needBwrap(t)
			}
			f := newFakeGitHub(t)
			f.answer = gitHubAnswers
			b := testBroker(t, f, "gate-x-candidate")
			m, err := StartMeter(filepath.Join(shortTmp(), "bc-"+sb+".sock"))
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			m.SetBroker(b)
			c := &Child{Name: "candidate", Root: filepath.Join(t.TempDir(), "candidate"), Sandbox: sb, Meter: m}
			if sb == "bwrap" {
				c.Fwd = builtHive(t)
			}
			if err := c.Prepare(); err != nil {
				t.Fatal(err)
			}
			c.Trust = filepath.Join(c.Root, "broker", "trust.pem")
			if err := b.WriteTrust(c.Trust); err != nil {
				t.Fatal(err)
			}
			for _, e := range c.Env() {
				if strings.Contains(e, "org-secret") {
					t.Errorf("the child's environment holds the org's token: %s", e)
				}
			}
			out, err := c.Exec(time.Minute, c.Root, "gh", "api", "repos/Hive-RSI-Lab/gate-x-candidate", "-q", ".full_name")
			if err != nil || strings.TrimSpace(out) != "Hive-RSI-Lab/gate-x-candidate" {
				t.Errorf("gh in the child: %v %s", err, out)
			}
			if out, err := c.Exec(time.Minute, c.Root, "gh", "api", "repos/Hive-RSI-Lab/hive"); err == nil || !strings.Contains(out, "403") {
				t.Errorf("another repository from the child: %v %s", err, out)
			}
			if got := f.requests(); len(got) != 1 || !strings.HasSuffix(got[0], "Bearer org-secret") {
				t.Errorf("GitHub saw %q", got)
			}
		})
	}
}

// The standalone broker serves GitHub's hosts on its socket and nothing else.
func TestServeBroker(t *testing.T) {
	f := newFakeGitHub(t)
	f.answer = gitHubAnswers
	b := testBroker(t, f, "gate-x-candidate")
	sock := filepath.Join(shortTmp(), "bs.sock")
	os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go ServeBroker(ln, b)
	connect := func(host string) (net.Conn, string) {
		c, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", host, host)
		resp, err := http.ReadResponse(bufio.NewReader(c), nil)
		if err != nil {
			t.Fatal(err)
		}
		return c, resp.Status
	}
	if c, st := connect("example.com:443"); !strings.HasPrefix(st, "403") {
		t.Errorf("example.com: %s", st)
	} else {
		c.Close()
	}
	c, st := connect("api.github.com:443")
	defer c.Close()
	if !strings.HasPrefix(st, "200") {
		t.Fatalf("api.github.com: %s", st)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(b.CAPEM())
	tc := tls.Client(c, &tls.Config{ServerName: "api.github.com", RootCAs: pool})
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/Hive-RSI-Lab/gate-x-candidate", nil)
	req.Header.Set("Authorization", "Bearer "+brokerPlaceholder)
	req.Write(tc)
	resp, err := http.ReadResponse(bufio.NewReader(tc), req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "gate-x-candidate") {
		t.Errorf("through the standalone broker: %s %s", resp.Status, body)
	}
	if got := f.requests(); len(got) != 1 || !strings.HasSuffix(got[0], "Bearer org-secret") {
		t.Errorf("GitHub saw %q", got)
	}
}

// The broker counts what a side asked of it: the calls it let through, those
// that read a commit's checks (REST or GraphQL), and those it refused.
func TestBrokerTally(t *testing.T) {
	f := newFakeGitHub(t)
	f.answer = gitHubAnswers
	b := testBroker(t, f, "gate-x-candidate")
	const repo = "/repos/Hive-RSI-Lab/gate-x-candidate"
	for _, c := range []struct{ method, path, body string }{
		{"GET", repo, ""},
		{"GET", repo + "/commits/abc/check-runs", ""},
		{"POST", "/graphql", `{"query":"query{repository(owner:\"Hive-RSI-Lab\",name:\"gate-x-candidate\"){pullRequest(number:3){statusCheckRollup{state}}}}"}`},
		{"GET", "/repos/Hive-RSI-Lab/hive/commits/abc/check-runs", ""},
	} {
		r := httptest.NewRequest(c.method, "https://api.github.com"+c.path, strings.NewReader(c.body))
		b.handle("api.github.com", httptest.NewRecorder(), r)
	}
	if passed, refused, checks := b.Tally(); passed != 3 || refused != 1 || checks != 2 {
		t.Fatalf("tally: passed %d refused %d checks %d, want 3 1 2", passed, refused, checks)
	}
}
