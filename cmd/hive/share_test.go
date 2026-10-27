package main

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// shareFixture is a Hive folder with every shared file and everything that
// must never leave: logins, bin, projects and the daemon's own state.
func shareFixture(t *testing.T) core.Workspace {
	ws := core.Workspace{Root: t.TempDir()}
	for rel, body := range map[string]string{
		".hive/chats/lead.jsonl":         "{}",
		".hive/runs/r1/output.log":       "run one",
		".hive/runs/r2/turns/1.json":     "run two",
		".hive/kb/notes/a.md":            "a note",
		".hive/gates/web/check.sh":       "true",
		".hive/events.jsonl":             "{}",
		".hive/config.json":              `{"harness":"fake","share":"2026-01-02T03:04:05Z","install_id":"3f0c8a5e-2b7d-4c1e","share_url":"https://r.example/v1/report"}`,
		".hive/daemon.log":               "started",
		".hive/logins/claude/token":      "secret",
		".hive/bin/hive":                 "binary",
		".hive/daemon.json":              "{}",
		"projects/web/main.go":           "package main",
		".modules/x/module.json":         "{}",
		".hive/private/scan-handles.txt": "nobody-in-this-test\n",
	} {
		p := filepath.Join(ws.Root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return ws
}

// bundleServer answers each bundle with ref and keeps the bodies it got.
func bundleServer(t *testing.T, ref string) (*httptest.Server, *[][]byte) {
	var got [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/bundle" || r.Header.Get("Content-Type") != "application/zip" {
			http.Error(w, "bad bundle", 400)
			return
		}
		body, _ := io.ReadAll(r.Body)
		got = append(got, body)
		w.WriteHeader(201)
		w.Write([]byte(`{"ref":"` + ref + `"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func zipNames(t *testing.T, body []byte) []string {
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	return names
}

func TestShareSendsExactlyTheSetAndPrintsTheReference(t *testing.T) {
	ws := shareFixture(t)
	srv, got := bundleServer(t, "b-7f3a")
	var out bytes.Buffer
	if err := share(ws, srv.URL+"/v1/bundle", bundleCap, false, strings.NewReader("yes\n"), &out); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 1 {
		t.Fatalf("server got %d bundles, want 1", len(*got))
	}
	want := []string{".hive/chats/lead.jsonl", ".hive/config.json", ".hive/daemon.log", ".hive/events.jsonl",
		".hive/gates/web/check.sh", ".hive/kb/notes/a.md", ".hive/runs/r1/output.log", ".hive/runs/r2/turns/1.json"}
	if names := zipNames(t, (*got)[0]); strings.Join(names, " ") != strings.Join(want, " ") {
		t.Errorf("zip holds %q, want %q", names, want)
	}
	// The bundle can't be matched to the install's anonymous reports.
	zr, _ := zip.NewReader(bytes.NewReader((*got)[0]), int64(len((*got)[0])))
	for _, f := range zr.File {
		if f.Name == ".hive/config.json" {
			rc, _ := f.Open()
			cfg, _ := io.ReadAll(rc)
			rc.Close()
			if bytes.Contains(cfg, []byte("3f0c8a5e")) || bytes.Contains(cfg, []byte("r.example")) || !bytes.Contains(cfg, []byte(`"harness": "fake"`)) {
				t.Errorf("config.json in the bundle:\n%s", cfg)
			}
		}
	}
	if !strings.Contains(out.String(), "Your reference is b-7f3a\n") {
		t.Errorf("output doesn't print the server's reference:\n%s", out.String())
	}
	for _, name := range []string{"chats", "kb", "gates", "events.jsonl", "config.json", "daemon.log", "runs (2)"} {
		if !strings.Contains(out.String(), "  "+name) {
			t.Errorf("listing misses %s:\n%s", name, out.String())
		}
	}
}

func TestShareNoSendsNothing(t *testing.T) {
	ws := shareFixture(t)
	srv, got := bundleServer(t, "b-1")
	for _, answer := range []string{"n\n", "\n", ""} {
		var out bytes.Buffer
		if err := share(ws, srv.URL+"/v1/bundle", bundleCap, false, strings.NewReader(answer), &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "Nothing sent.") {
			t.Errorf("answer %q: output says nothing about not sending:\n%s", answer, out.String())
		}
	}
	if len(*got) != 0 {
		t.Errorf("a no sent %d bundles", len(*got))
	}
}

// Over the cap, the oldest run goes first and what is sent is whole and fits.
func TestShareOverCapLeavesOutTheOldestRun(t *testing.T) {
	ws := shareFixture(t)
	noise := make([]byte, 256<<10) // random, so it doesn't compress
	rand.Read(noise)
	for _, r := range []string{"r1", "r2"} {
		os.WriteFile(filepath.Join(ws.Run(r), "big.bin"), noise, 0o644)
	}
	old := time.Now().Add(-time.Hour)
	os.Chtimes(ws.Run("r1"), old, old)
	srv, got := bundleServer(t, "b-2")
	var out bytes.Buffer
	limit := int64(400 << 10)
	if err := share(ws, srv.URL+"/v1/bundle", limit, true, nil, &out); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 1 || int64(len((*got)[0])) > limit {
		t.Fatalf("server got %d bundles (want 1 under %d bytes)", len(*got), limit)
	}
	names := strings.Join(zipNames(t, (*got)[0]), " ")
	if strings.Contains(names, "runs/r1/") || !strings.Contains(names, ".hive/runs/r2/big.bin") || !strings.Contains(names, ".hive/events.jsonl") {
		t.Errorf("over the cap the zip holds %s; want r1 left out and the rest kept", names)
	}
	if !strings.Contains(out.String(), "it leaves out:\n  runs (1)") {
		t.Errorf("output doesn't report what it left out:\n%s", out.String())
	}
}

// When nothing fits, it says so and sends no part of it.
func TestShareNothingFitsSendsNothing(t *testing.T) {
	ws := shareFixture(t)
	srv, got := bundleServer(t, "b-3")
	if err := share(ws, srv.URL+"/v1/bundle", 10, true, nil, io.Discard); err == nil || !strings.Contains(err.Error(), "fits") {
		t.Errorf("err = %v, want one saying nothing fits", err)
	}
	if len(*got) != 0 {
		t.Errorf("sent %d bundles", len(*got))
	}
}

// A run's old login folders and leftover sandbox specs never leave, while the rest of its host folder does.
func TestShareLeavesOutRunLogins(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	for _, rel := range []string{"runs/r1/home/.claude/token", "runs/r1/host/codex/auth.json",
		"runs/r1/host/codex-sessions/s.json", "runs/r1/host/turn-001.jsonl", "runs/r1/host/turn-001.jsonl.sandbox.json"} {
		p := filepath.Join(ws.Hive(), filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x"), 0o600)
	}
	var buf bytes.Buffer
	if _, err := writeBundle(&buf, ws, shareParts(ws)); err != nil {
		t.Fatal(err)
	}
	if names := strings.Join(zipNames(t, buf.Bytes()), " "); names != ".hive/runs/r1/host/turn-001.jsonl" {
		t.Errorf("zip holds %s, want only the turn log", names)
	}
}

// A run that keeps swapping a folder of its box for a symlink, to a secret
// outside .hive or to the logins inside it, never gets the secret into a zip.
func TestShareNeverFollowsASwappedFolder(t *testing.T) {
	ws := shareFixture(t)
	box := ws.RunBox("r3")
	mid := filepath.Join(box, "mid")
	targets := []string{filepath.Join(t.TempDir(), "ssh"), filepath.Join(ws.Hive(), "logins", "fake")}
	for _, d := range append([]string{mid}, targets...) {
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "id"), []byte("FAKE-SECRET"), 0o600)
	}
	os.WriteFile(filepath.Join(mid, "id"), []byte("plain"), 0o644)
	links := []string{filepath.Join(box, "out"), filepath.Join(box, "in")}
	for i, l := range links {
		if err := os.Symlink(targets[i], l); err != nil {
			t.Skip("no symlinks here:", err)
		}
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			os.Rename(mid, filepath.Join(box, "real"))
			os.Rename(links[i%2], mid) // the link takes the folder's place
			os.Rename(mid, links[i%2])
			os.Rename(filepath.Join(box, "real"), mid)
		}
	}()
	defer func() { close(stop); <-done }()
	for i := 0; i < 300; i++ {
		var buf bytes.Buffer
		if _, err := writeBundle(&buf, ws, shareParts(ws)); err != nil {
			t.Fatal(err)
		}
		zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range zr.File {
			rc, _ := f.Open()
			data, _ := io.ReadAll(rc)
			rc.Close()
			if bytes.Contains(data, []byte("FAKE-SECRET")) {
				t.Fatalf("bundle %d holds the secret as %s", i, f.Name)
			}
		}
	}
}

// The bundle is scanned before anything is asked or sent: a key, a handle
// or no handle list sends nothing, and the failure prints no handle.
func TestShareScanBlocksTheBundle(t *testing.T) {
	srv, got := bundleServer(t, "b-3")
	key := "sk-ant-api03-" + strings.Repeat("Zq9xW2vB7nK4", 4)
	for name, c := range map[string]struct{ file, body, want string }{
		"a key in a run":   {".hive/runs/r1/output.log", "printed " + key + " (a fake token)", "runs/r1/output.log:1: an Anthropic key"},
		"a handle in chat": {".hive/chats/lead.jsonl", "{}\n{\"text\":\"ask Alice-K\"}", "chats/lead.jsonl:2: handle 2 of the handle list"},
		"no handle list":   {".hive/private/scan-handles.txt", "", "no handle list"},
	} {
		ws := shareFixture(t)
		os.WriteFile(filepath.Join(ws.Private(), "scan-handles.txt"), []byte("bob\nalice-k\n"), 0o600)
		os.WriteFile(filepath.Join(ws.Root, filepath.FromSlash(c.file)), []byte(c.body), 0o644)
		var out bytes.Buffer
		err := share(ws, srv.URL+"/v1/bundle", bundleCap, true, nil, &out)
		if err == nil || !strings.HasPrefix(err.Error(), "nothing sent: ") || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want %q, got %v", name, c.want, err)
		} else if strings.Contains(err.Error(), key) || strings.Contains(strings.ToLower(err.Error()), "alice") || strings.Contains(err.Error(), "bob") {
			t.Errorf("%s: the failure prints what it found: %v", name, err)
		}
	}
	if len(*got) != 0 {
		t.Fatalf("a bundle the scan blocked was sent %d times", len(*got))
	}
}
