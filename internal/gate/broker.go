package gate

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Broker is a sandbox's GitHub: the forge's API and git over HTTPS for the
// run's own repositories in the test org, and nothing else. It sits in the
// meter, outside every sandbox: a CONNECT to one of GitHub's hosts is
// answered by the broker itself, over TLS with a certificate from the run's
// own authority (which the side trusts and whose key never leaves the gate),
// and each request is read before it goes on. One the run's repositories
// allow goes to GitHub with the org's token in place of whatever the side
// sent; any other is refused, there, with a 403. So a sub-Hive works on its
// repositories as the Hive does on its own, gh and git included (pull
// requests, checks, CI runs, merges, releases), and holds no login: the
// token is the broker's, and reaches only what the broker lets through.
type Broker struct {
	Owner string // the test org
	token string

	mu    sync.Mutex
	repos map[string]bool   // lower-cased names in Owner the side may reach
	ids   map[string]string // a repository's numeric id and node id → its name, once learned
	nodes map[string]string // a GraphQL node id → the owner/name it lives in ("" outside any)

	ca      *x509.Certificate
	caKey   *ecdsa.PrivateKey
	caPEM   []byte
	leaf    map[string]*tls.Certificate
	up      map[string]*url.URL // host → where it really is (tests point these at a fake)
	client  *http.Client
	proxies map[string]*httputil.ReverseProxy
	Log     func(format string, args ...any) // each refusal, when set
}

// brokerHosts are GitHub's hosts the broker answers for the side.
var brokerHosts = []string{"api.github.com", "github.com", "uploads.github.com"}

// NewBroker makes a broker for owner with its token, and the run's own
// certificate authority, valid for a week.
func NewBroker(owner, token string) (*Broker, error) {
	if owner == "" || token == "" {
		return nil, fmt.Errorf("a broker needs the org and its token")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "Hive gate broker for " + owner},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(7 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
		// It signs only GitHub's names, so even a side that kept it can't
		// pass it off for anything else.
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         brokerHosts,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	b := &Broker{Owner: owner, token: token, repos: map[string]bool{}, ids: map[string]string{}, nodes: map[string]string{},
		ca: ca, caKey: key, caPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		leaf: map[string]*tls.Certificate{}, up: map[string]*url.URL{}, proxies: map[string]*httputil.ReverseProxy{}}
	for _, h := range brokerHosts {
		b.up[h] = &url.URL{Scheme: "https", Host: h}
	}
	b.client = &http.Client{Timeout: time.Minute, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return b, nil
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	return n
}

// Allow lets the side reach owner/name.
func (b *Broker) Allow(name string) {
	b.mu.Lock()
	b.repos[strings.ToLower(name)] = true
	b.mu.Unlock()
}

// Intercepts is whether a CONNECT to hostport is the broker's to answer.
func (b *Broker) Intercepts(hostport string) bool {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil || port != "443" {
		return false
	}
	for _, h := range brokerHosts {
		if strings.EqualFold(host, h) {
			return true
		}
	}
	return false
}

// CAPEM is the run's authority, for the side to trust.
func (b *Broker) CAPEM() []byte { return b.caPEM }

// systemRoots are where this machine's trusted roots usually are (Go's own
// list for Linux and the BSDs).
var systemRoots = []string{"/etc/ssl/certs/ca-certificates.crt", "/etc/pki/tls/certs/ca-bundle.crt", "/etc/ssl/ca-bundle.pem",
	"/etc/pki/tls/cacert.pem", "/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem", "/etc/ssl/cert.pem"}

// WriteTrust writes, at path, the roots the gate trusts with the run's
// authority added, for a side's SSL_CERT_FILE (and git's CA file): the side
// still reaches the rest of the internet as before.
func (b *Broker) WriteTrust(path string) error {
	var bundle []byte
	for _, p := range append([]string{os.Getenv("SSL_CERT_FILE")}, systemRoots...) {
		if p == "" {
			continue
		}
		if data, err := os.ReadFile(p); err == nil {
			bundle = append(data, '\n')
			break
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(bundle, b.caPEM...), 0o644)
}

// TrustEnv points a side's TLS (Go, OpenSSL, git's curl) at the trust file,
// and gives gh a token that is no login, so it asks at all: the broker drops
// whatever a side sends and puts the org's in its place.
func TrustEnv(trust string) []string {
	return []string{"SSL_CERT_FILE=" + trust, "GIT_SSL_CAINFO=" + trust, "CURL_CA_BUNDLE=" + trust,
		"GH_TOKEN=" + brokerPlaceholder, "GITHUB_TOKEN=" + brokerPlaceholder, "GH_PROMPT_DISABLED=1"}
}

// brokerPlaceholder is the token a side holds: nothing on GitHub accepts it.
const brokerPlaceholder = "hive-broker-holds-the-login"

func (b *Broker) cert(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	host := strings.ToLower(hello.ServerName)
	if !b.Intercepts(net.JoinHostPort(host, "443")) {
		return nil, fmt.Errorf("the broker answers only for GitHub")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if c, ok := b.leaf[host]; ok {
		return c, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: b.ca.NotAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, b.ca, &key.PublicKey, b.caKey)
	if err != nil {
		return nil, err
	}
	c := &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	b.leaf[host] = c
	return c, nil
}

// Serve answers one CONNECT the meter took for the broker: TLS with the
// run's authority, then HTTP/1.1 until the side hangs up.
func (b *Broker) Serve(c net.Conn) {
	tc := tls.Server(c, &tls.Config{GetCertificate: b.cert, NextProtos: []string{"http/1.1"}, MinVersion: tls.VersionTLS12})
	tc.SetDeadline(time.Now().Add(30 * time.Second))
	if err := tc.Handshake(); err != nil {
		return
	}
	tc.SetDeadline(time.Time{})
	host := strings.ToLower(tc.ConnectionState().ServerName)
	ln := &oneConn{c: tc, done: make(chan struct{})}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { b.handle(host, w, r) }),
		ReadHeaderTimeout: time.Minute, IdleTimeout: 5 * time.Minute,
		ConnState: func(_ net.Conn, s http.ConnState) {
			if s == http.StateClosed || s == http.StateHijacked {
				ln.Close()
			}
		}}
	srv.Serve(ln)
}

// oneConn is a listener that yields one connection, then waits for it to end.
type oneConn struct {
	c    net.Conn
	once sync.Once
	done chan struct{}
	used bool
}

func (l *oneConn) Accept() (net.Conn, error) {
	if !l.used {
		l.used = true
		return l.c, nil
	}
	<-l.done
	return nil, net.ErrClosed
}

func (l *oneConn) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *oneConn) Addr() net.Addr { return l.c.LocalAddr() }

func (b *Broker) refuse(w http.ResponseWriter, r *http.Request, why string) {
	if b.Log != nil {
		b.Log("broker refused %s %s%s: %s", r.Method, r.Host, r.URL.Path, why)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(map[string]string{"message": "the Hive's broker reaches only this run's own repositories in " + b.Owner + ": " + why})
}

func (b *Broker) handle(host string, w http.ResponseWriter, r *http.Request) {
	if r.URL.RawPath != "" {
		b.refuse(w, r, "an escaped path the broker won't read")
		return
	}
	if !strings.EqualFold(stripPort(r.Host), host) {
		b.refuse(w, r, "the request names another host than its connection")
		return
	}
	var why string
	switch host {
	case "api.github.com", "uploads.github.com":
		if host == "api.github.com" && r.URL.Path == "/graphql" && r.URL.RawQuery != "" {
			why = "GraphQL with a query string (gh sends none)"
		} else if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); host == "api.github.com" && r.URL.Path == "/graphql" && r.Method == http.MethodPost && (mt != "application/json" || r.Header.Get("Content-Encoding") != "") {
			why = "GraphQL as anything but plain JSON"
		} else if host == "api.github.com" && r.URL.Path == "/graphql" && r.Method == http.MethodPost {
			body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
			if err != nil || len(body) > 1<<20 {
				b.refuse(w, r, "a GraphQL request too large to read")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))
			if why = b.graphqlOK(body); why != "" && b.Log != nil {
				b.Log("broker refused GraphQL: %s", body)
			}
		} else {
			why = b.restOK(host, r.Method, r.URL.Path)
		}
	case "github.com":
		why = b.gitOK(r.URL.Path)
	default:
		why = "not a host the broker answers for"
	}
	if why != "" {
		b.refuse(w, r, why)
		return
	}
	if host == "api.github.com" && r.URL.Path == "/user" {
		b.user(w)
		return
	}
	b.proxy(host).ServeHTTP(w, r)
}

func stripPort(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		return host
	}
	return h
}

func (b *Broker) proxy(host string) *httputil.ReverseProxy {
	b.mu.Lock()
	defer b.mu.Unlock()
	if p, ok := b.proxies[host]; ok {
		return p
	}
	up := b.up[host]
	p := &httputil.ReverseProxy{
		Transport:     b.client.Transport,
		FlushInterval: -1, // git's pack streams as it comes
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(up)
			pr.Out.Host = up.Host
			for _, h := range []string{"Authorization", "Cookie", "Proxy-Authorization"} {
				pr.Out.Header.Del(h)
			}
			if host == "github.com" {
				pr.Out.Header.Set("Authorization", "Basic "+basic(b.token))
			} else {
				pr.Out.Header.Set("Authorization", "Bearer "+b.token)
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del("Set-Cookie")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "the broker could not reach GitHub: "+err.Error(), http.StatusBadGateway)
		},
	}
	b.proxies[host] = p
	return p
}

// allowed is whether owner/name is one of the run's own repositories.
func (b *Broker) allowed(owner, name string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.EqualFold(owner, b.Owner) && b.repos[strings.ToLower(strings.TrimSuffix(name, ".git"))]
}

func (b *Broker) allowedFull(full string) bool {
	o, n, ok := strings.Cut(full, "/")
	return ok && b.allowed(o, n)
}

// repoParts are what a side may do within its own repository, by the first
// part of the path after it: the forge's work, never its settings (keys,
// hooks, collaborators, secrets, branch protection, transfer, forks).
var repoParts = map[string]bool{
	"pulls": true, "issues": true, "commits": true, "check-runs": true, "check-suites": true, "statuses": true,
	"git": true, "contents": true, "branches": true, "merges": true, "releases": true, "actions": true, "compare": true,
	"labels": true, "milestones": true, "tags": true, "readme": true, "languages": true, "comments": true, "events": true,
	"tarball": true, "zipball": true, "deployments": true, "rules": true, "topics": true, "dispatches": true,
}

// actionsParts are what a side may do in its repository's Actions: its runs,
// jobs, workflows and artifacts, never its secrets, variables or settings.
var actionsParts = map[string]bool{"runs": true, "jobs": true, "workflows": true, "artifacts": true}

var repoIDPath = regexp.MustCompile(`^/repositories/([0-9]+)(/.*)?$`)

// restOK is "" when the side may make this REST call, else why not.
func (b *Broker) restOK(host, method, path string) string {
	if strings.Contains(path, "/../") || strings.HasSuffix(path, "/..") || strings.Contains(path, "//") || strings.Contains(path, "%") {
		return "a path the broker won't read"
	}
	if host == "api.github.com" && (method == http.MethodGet || method == http.MethodHead) {
		switch path {
		case "/user", "/rate_limit", "/meta", "/octocat", "/zen", "/":
			return ""
		}
	}
	var rest string
	if m := repoIDPath.FindStringSubmatch(path); m != nil && host == "api.github.com" {
		if b.repoByID(m[1]) == "" {
			return "repository " + m[1] + " is not this run's"
		}
		rest = m[2]
	} else if p, ok := strings.CutPrefix(path, "/repos/"); ok {
		f := strings.SplitN(p, "/", 3)
		if len(f) < 2 || !b.allowed(f[0], f[1]) {
			return "not this run's repository"
		}
		if len(f) == 3 {
			rest = "/" + f[2]
		}
	} else {
		return "only this run's repositories"
	}
	if rest == "" || rest == "/" {
		if method == http.MethodGet || method == http.MethodHead {
			return ""
		}
		return "a repository's own settings stay the gate's"
	}
	f := strings.Split(strings.TrimPrefix(rest, "/"), "/")
	if host == "uploads.github.com" {
		if f[0] == "releases" {
			return ""
		}
		return "uploads go only to a release"
	}
	if !repoParts[f[0]] {
		return "/" + f[0] + " is a repository's settings, not the forge's work"
	}
	switch f[0] {
	case "actions":
		if len(f) < 2 || !actionsParts[f[1]] {
			return "only Actions' runs, jobs, workflows and artifacts"
		}
	case "branches":
		for _, p := range f[1:] {
			if p == "protection" {
				return "branch protection is the gate's"
			}
		}
	case "rules", "topics":
		if method != http.MethodGet && method != http.MethodHead {
			return "read only"
		}
	}
	return ""
}

// repoByID names the run's repository with this numeric id (GitHub's
// paginated links use them), asking GitHub for the ids it hasn't learned.
func (b *Broker) repoByID(id string) string {
	b.mu.Lock()
	name, ok := b.ids[id]
	var todo []string
	for r := range b.repos {
		if !b.known(r) {
			todo = append(todo, r)
		}
	}
	b.mu.Unlock()
	if ok {
		return name
	}
	for _, r := range todo {
		b.learn(r)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ids[id]
}

// known is whether the ids of repository r are learned; b.mu is held.
func (b *Broker) known(r string) bool {
	for _, n := range b.ids {
		if n == r {
			return true
		}
	}
	return false
}

// learn asks GitHub for repository r's ids.
func (b *Broker) learn(r string) {
	var v struct {
		ID     int64  `json:"id"`
		NodeID string `json:"node_id"`
	}
	if err := b.api(http.MethodGet, "/repos/"+b.Owner+"/"+r, nil, &v); err != nil || v.ID == 0 {
		return
	}
	b.mu.Lock()
	b.ids[fmt.Sprint(v.ID)] = r
	if v.NodeID != "" {
		b.ids[v.NodeID] = r
		b.nodes[v.NodeID] = b.Owner + "/" + r
	}
	b.mu.Unlock()
}

// api is the broker's own call to GitHub's API, with the org's token.
func (b *Broker) api(method, path string, body []byte, out any) error {
	u := *b.up["api.github.com"]
	u.Path = path
	req, err := http.NewRequest(method, u.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+b.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s %s: %s", method, path, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

var gitPath = regexp.MustCompile(`^/([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+)/(info/refs|git-upload-pack|git-receive-pack)$`)

// gitOK is "" when the side may make this git call over HTTPS.
func (b *Broker) gitOK(path string) string {
	m := gitPath.FindStringSubmatch(path)
	if m == nil {
		return "only git, on this run's repositories"
	}
	if !b.allowed(m[1], m[2]) {
		return "not this run's repository"
	}
	return ""
}

// graphqlOK is "" when the side may send this GraphQL request. A query may
// start only at one of the run's repositories (repository(owner, name), or
// node(id) for a node in one), or at the viewer or the rate limit; a
// mutation only with one of the forge's names, every id it is given living
// in one of the run's repositories. Below that, a field may open a selection
// of its own only if it stays in the repository (its pull requests, issues,
// refs, commits, checks, labels, comments, reviews, releases); any other
// (an author, an owner, another repository, a cross-reference) may select
// only scalars, so nothing in another repository is ever read through one.
func (b *Broker) graphqlOK(body []byte) string {
	// GitHub reads the exact keys; Go's decoder would match "Query" too and
	// keep the last of two, so the body is read key by key, and a key that
	// isn't exactly one of these three, or comes twice anywhere in the
	// body, is refused.
	if err := strictJSON(body); err != nil {
		return "a GraphQL request the broker won't read: " + err.Error()
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return "a GraphQL request the broker can't read"
	}
	for k := range top {
		if k != "query" && k != "variables" && k != "operationName" {
			return "a GraphQL request with a key " + strconv.Quote(k)
		}
	}
	var req struct {
		Query     string
		Variables map[string]json.RawMessage
	}
	if err := json.Unmarshal(top["query"], &req.Query); err != nil {
		return "a GraphQL request with no query"
	}
	if v, ok := top["variables"]; ok {
		if err := json.Unmarshal(v, &req.Variables); err != nil {
			return "GraphQL variables the broker can't read"
		}
	}
	doc, err := parseGraphQL(req.Query)
	if err != nil {
		return err.Error()
	}
	if len(doc.Ops) == 0 {
		return "no operation"
	}
	g := &gqlCheck{b: b, doc: doc, vars: req.Variables}
	for _, op := range doc.Ops {
		if err := g.op(op); err != nil {
			return err.Error()
		}
	}
	return ""
}

type gqlCheck struct {
	b    *Broker
	doc  *gqlDoc
	vars map[string]json.RawMessage
}

// stays are the fields that may open a selection of their own: they stay in
// the repository they are reached from.
var stays = map[string]bool{
	"pullRequest": true, "pullRequests": true, "issue": true, "issues": true, "issueOrPullRequest": true,
	"ref": true, "refs": true, "defaultBranchRef": true, "headRef": true, "baseRef": true, "object": true, "target": true,
	"history": true, "commits": true, "commit": true, "mergeCommit": true, "potentialMergeCommit": true, "parents": true, "tree": true, "entries": true,
	"statusCheckRollup": true, "status": true, "contexts": true, "checkSuites": true, "checkSuite": true, "checkRuns": true, "steps": true,
	"workflowRun": true,
	"labels":      true, "label": true, "comments": true, "comment": true, "reviews": true, "review": true, "latestReviews": true,
	"latestOpinionatedReviews": true, "reviewRequests": true, "reviewThreads": true, "files": true, "autoMergeRequest": true,
	"milestone": true, "milestones": true, "releases": true, "release": true, "latestRelease": true, "releaseAssets": true,
	"commentEdge": true, "reactionGroups": true, "projectCards": true,
	"nodes": true, "edges": true, "node": true, "pageInfo": true,
}

// mutations are the forge's work, by GraphQL name.
var mutations = map[string]bool{
	"createPullRequest": true, "updatePullRequest": true, "mergePullRequest": true, "closePullRequest": true, "reopenPullRequest": true,
	"markPullRequestReadyForReview": true, "convertPullRequestToDraft": true, "updatePullRequestBranch": true,
	"enablePullRequestAutoMerge": true, "disablePullRequestAutoMerge": true,
	"addComment": true, "updateIssueComment": true, "deleteIssueComment": true, "addPullRequestReview": true, "submitPullRequestReview": true,
	"createIssue": true, "updateIssue": true, "closeIssue": true, "reopenIssue": true, "addLabelsToLabelable": true, "removeLabelsFromLabelable": true,
	"createRef": true, "updateRef": true, "deleteRef": true, "createCommitOnBranch": true,
}

func (g *gqlCheck) op(op gqlOp) error {
	switch op.Kind {
	case "query":
		return g.each(op.Sels, 0, g.top)
	case "mutation":
		return g.each(op.Sels, 0, g.mutation)
	}
	return fmt.Errorf("a %s", op.Kind)
}

// each walks sels, fragments spread or inline, calling f on each field.
func (g *gqlCheck) each(sels []gqlSel, depth int, f func(gqlSel, int) error) error {
	if depth > 40 {
		return fmt.Errorf("fragments nested too deep")
	}
	for _, s := range sels {
		switch {
		case s.Spread != "":
			fr, ok := g.doc.Frags[s.Spread]
			if !ok {
				return fmt.Errorf("no fragment %s", s.Spread)
			}
			if err := g.each(fr, depth+1, f); err != nil {
				return err
			}
		case s.Inline:
			if err := g.each(s.Children, depth+1, f); err != nil {
				return err
			}
		default:
			if err := f(s, depth); err != nil {
				return err
			}
		}
	}
	return nil
}

func (g *gqlCheck) top(s gqlSel, _ int) error {
	switch s.Field {
	case "repository":
		owner, _ := g.str(s.Args["owner"])
		name, _ := g.str(s.Args["name"])
		if !g.b.allowed(owner, name) {
			return fmt.Errorf("repository %s/%s is not this run's", owner, name)
		}
		return g.within(s.Children, 0)
	case "node":
		id, _ := g.str(s.Args["id"])
		if err := g.node(id); err != nil {
			return err
		}
		return g.within(s.Children, 0)
	case "viewer":
		// The token acts as the person: their login, never their profile.
		return g.each(s.Children, 1, func(k gqlSel, _ int) error {
			if k.Field != "login" && k.Field != "id" && k.Field != "__typename" {
				return fmt.Errorf("the viewer is its login and id only, not %s", k.Field)
			}
			return nil
		})
	case "rateLimit", "__typename":
		return g.leaves(s, 0)
	case "__type", "__schema":
		return nil // the schema is public and holds no one's data
	}
	return fmt.Errorf("a query may start only at one of this run's repositories, not at %s", s.Field)
}

func (g *gqlCheck) mutation(s gqlSel, _ int) error {
	if s.Field == "__typename" {
		return nil
	}
	if !mutations[s.Field] {
		return fmt.Errorf("mutation %s is not the forge's work", s.Field)
	}
	for _, v := range s.Args {
		if err := g.ids(v, "", 0); err != nil {
			return fmt.Errorf("%s: %v", s.Field, err)
		}
	}
	return g.within(s.Children, 0)
}

// within checks a selection inside the run's repository.
func (g *gqlCheck) within(sels []gqlSel, depth int) error {
	return g.each(sels, depth, func(s gqlSel, d int) error {
		if !s.Has {
			return nil
		}
		if stays[s.Field] {
			return g.within(s.Children, d+1)
		}
		return g.leaves(s, d)
	})
}

// names are the fields a leaving field may still open, each to scalars (or
// to names) only: who owns it, what its default branch is called, what it
// was forked from, the people in a list. gh reads them to know a repository
// and a pull request's people; what they hold is names, never content.
var names = map[string]bool{"owner": true, "defaultBranchRef": true, "parent": true, "organization": true, "project": true, "column": true,
	"nodes": true, "edges": true, "node": true, "pageInfo": true}

// people are the fields that reach a person (the token acts as the
// person, and authors, mergers and reviewers are mostly them): below one,
// only personScalars, the names gh shows, never the rest of a profile.
var people = map[string]bool{"author": true, "editor": true, "actor": true, "mergedBy": true, "enabledBy": true, "requestedReviewer": true,
	"user": true, "users": true, "assignees": true, "participants": true, "viewer": true}

var personScalars = map[string]bool{"login": true, "id": true, "name": true, "slug": true, "__typename": true, "totalCount": true,
	"hasNextPage": true, "endCursor": true}

// leaves allows s only scalars below it, and names.
func (g *gqlCheck) leaves(s gqlSel, depth int) error { return g.leavesOf(s, depth, false) }

func (g *gqlCheck) leavesOf(s gqlSel, depth int, person bool) error {
	person = person || people[s.Field]
	return g.each(s.Children, depth+1, func(k gqlSel, d int) error {
		if !k.Has {
			if person && !personScalars[k.Field] {
				return fmt.Errorf("%s gives a person's login and name, not %s", s.Field, k.Field)
			}
			return nil
		}
		if names[k.Field] && d < 10 {
			return g.leavesOf(k, d, person)
		}
		return fmt.Errorf("%s may select only scalars, not %s", s.Field, k.Field)
	})
}

// strictJSON refuses a body that isn't one JSON value, or that has a key
// twice in any object.
func strictJSON(body []byte) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := strictValue(dec, 0); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("more after the request")
	}
	return nil
}

func strictValue(dec *json.Decoder, depth int) error {
	if depth > 64 {
		return fmt.Errorf("nested too deep")
	}
	t, err := dec.Token()
	if err != nil {
		return err
	}
	switch t {
	case json.Delim('{'):
		seen := map[string]bool{}
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return err
			}
			key := k.(string)
			if seen[key] {
				return fmt.Errorf("the key %q twice", key)
			}
			seen[key] = true
			if err := strictValue(dec, depth+1); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	case json.Delim('['):
		for dec.More() {
			if err := strictValue(dec, depth+1); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	}
	return nil
}

// str is the string a value holds, literal or through a variable.
func (g *gqlCheck) str(v gqlValue) (string, bool) {
	if v.Str != nil {
		return *v.Str, true
	}
	if v.Var != "" {
		var s string
		if json.Unmarshal(g.vars[v.Var], &s) == nil {
			return s, true
		}
	}
	return "", false
}

// ids checks every id an argument carries (keys "id", "…Id", "…Ids") lives
// in one of the run's repositories, and every repository it names is one.
func (g *gqlCheck) ids(v gqlValue, key string, depth int) error {
	if depth > 20 {
		return fmt.Errorf("an input nested too deep")
	}
	if v.Var != "" {
		// A variable the request doesn't send would be its default, which
		// the broker doesn't read: refused, not passed unchecked.
		sent, ok := g.vars[v.Var]
		if !ok {
			return fmt.Errorf("variable $%s is not sent", v.Var)
		}
		var raw any
		if err := json.Unmarshal(sent, &raw); err != nil {
			return err
		}
		return g.idsJSON(raw, key, depth)
	}
	switch {
	case v.Obj != nil:
		for k, e := range v.Obj {
			if err := g.ids(e, k, depth+1); err != nil {
				return err
			}
		}
	case v.List != nil:
		for _, e := range v.List {
			if err := g.ids(e, key, depth+1); err != nil {
				return err
			}
		}
	case v.Str != nil:
		return g.keyed(key, *v.Str)
	}
	return nil
}

func (g *gqlCheck) idsJSON(v any, key string, depth int) error {
	if depth > 20 {
		return fmt.Errorf("an input nested too deep")
	}
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if err := g.idsJSON(e, k, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, e := range x {
			if err := g.idsJSON(e, key, depth+1); err != nil {
				return err
			}
		}
	case string:
		return g.keyed(key, x)
	}
	return nil
}

func (g *gqlCheck) keyed(key, s string) error {
	switch {
	case key == "clientMutationId":
		return nil
	case key == "repositoryNameWithOwner":
		if !g.b.allowedFull(s) {
			return fmt.Errorf("repository %s is not this run's", s)
		}
	case key == "id" || strings.HasSuffix(key, "Id") || strings.HasSuffix(key, "Ids"):
		return g.node(s)
	}
	return nil
}

// node checks a GraphQL node id lives in one of the run's repositories,
// asking GitHub once per id.
func (g *gqlCheck) node(id string) error {
	if id == "" {
		return fmt.Errorf("a node with no id")
	}
	b := g.b
	b.mu.Lock()
	where, ok := b.nodes[id]
	b.mu.Unlock()
	if !ok {
		for r := range b.snapshot() {
			b.learn(r) // a repository's own node id is learned without a query
		}
		b.mu.Lock()
		where, ok = b.nodes[id]
		b.mu.Unlock()
	}
	if !ok {
		var err error
		if where, err = b.whereIs(id); err != nil {
			return err
		}
		b.mu.Lock()
		b.nodes[id] = where
		b.mu.Unlock()
	}
	if !b.allowedFull(where) {
		return fmt.Errorf("node %s is not in this run's repositories", id)
	}
	return nil
}

func (b *Broker) snapshot() map[string]bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := map[string]bool{}
	for r := range b.repos {
		if !b.known(r) {
			out[r] = true
		}
	}
	return out
}

const whereQuery = `query($id: ID!) { node(id: $id) { __typename
 ... on Repository { nameWithOwner }
 ... on PullRequest { repository { nameWithOwner } }
 ... on Issue { repository { nameWithOwner } }
 ... on IssueComment { repository { nameWithOwner } }
 ... on PullRequestReview { repository { nameWithOwner } }
 ... on PullRequestReviewComment { repository { nameWithOwner } }
 ... on Ref { repository { nameWithOwner } }
 ... on Commit { repository { nameWithOwner } }
 ... on Label { repository { nameWithOwner } }
 ... on Milestone { repository { nameWithOwner } }
 ... on Release { repository { nameWithOwner } }
} }`

// whereIs asks GitHub which repository a node lives in ("" when none).
func (b *Broker) whereIs(id string) (string, error) {
	body, _ := json.Marshal(map[string]any{"query": whereQuery, "variables": map[string]string{"id": id}})
	var out struct {
		Data struct {
			Node *struct {
				NameWithOwner string `json:"nameWithOwner"`
				Repository    *struct {
					NameWithOwner string `json:"nameWithOwner"`
				} `json:"repository"`
			} `json:"node"`
		} `json:"data"`
	}
	if err := b.api(http.MethodPost, "/graphql", body, &out); err != nil {
		return "", fmt.Errorf("can't tell where node %s lives: %v", id, err)
	}
	n := out.Data.Node
	switch {
	case n == nil:
		return "", errors.New("no node " + id)
	case n.Repository != nil:
		return n.Repository.NameWithOwner, nil
	}
	return n.NameWithOwner, nil
}

// useBroker makes c's meter its GitHub: a broker with the org's token that
// reaches c's own repository name alone, so the child does the forge's work
// there (pull requests, checks, merges, releases) as the Hive does on its
// own, holding no login. It checks the child reaches its repository through
// it with git over HTTPS.
func (c *Child) useBroker(cfg *Config, g GitHub, name string) error {
	b, err := NewBroker(g.Owner, g.Token)
	if err != nil {
		return err
	}
	b.Allow(name)
	b.Log = cfg.log
	trust := filepath.Join(c.Root, "broker", "trust.pem")
	if err := b.WriteTrust(trust); err != nil {
		return err
	}
	c.Trust = trust
	c.Meter.SetBroker(b)
	url := "https://github.com/" + g.Owner + "/" + name + ".git"
	if out, err := c.Exec(2*time.Minute, c.Root, "git", "ls-remote", url); err != nil {
		return fmt.Errorf("%s can't reach %s through the broker: %v: %s", c.Name, name, err, tail(out, 300))
	}
	return nil
}

// user answers GET /user with the login the token acts as and nothing more
// of the person's profile.
func (b *Broker) user(w http.ResponseWriter) {
	var u struct {
		Login  string `json:"login"`
		ID     int64  `json:"id"`
		NodeID string `json:"node_id"`
		Type   string `json:"type"`
	}
	if err := b.api(http.MethodGet, "/user", nil, &u); err != nil {
		http.Error(w, "the broker could not ask GitHub: "+err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(u)
}
