package tools

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

// tgz builds a gzipped tar of name -> content (a "->" prefix is a symlink).
func tgz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, body := range files {
		h := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if target, ok := strings.CutPrefix(body, "->"); ok {
			h = &tar.Header{Name: name, Linkname: target, Typeflag: tar.TypeSymlink}
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			tw.Write([]byte(body))
		}
	}
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

// serve answers path -> body; an archive's .sha256 is served beside it.
func serve(t *testing.T, files map[string][]byte) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func sum(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }

func TestUnpackVerifiesAndInstalls(t *testing.T) {
	arc := tgz(t, map[string]string{"node-v1/bin/node": "#!/bin/sh\necho node\n", "node-v1/bin/npm": "->../lib/npm.js", "node-v1/lib/npm.js": "x"})
	url := serve(t, map[string][]byte{"/node.tar.gz": arc, "/node.tar.gz.sha256": []byte(sum(arc) + "  node.tar.gz\n")})
	folder := t.TempDir()
	v, err := unpack(folder, "node", "1.2.3", url+"/node.tar.gz", "")
	if err != nil || v != "1.2.3" {
		t.Fatalf("unpack: %q %v", v, err)
	}
	fi, err := os.Stat(filepath.Join(folder, "node", "bin", "node"))
	if err != nil || fi.Mode().Perm()&0o100 == 0 {
		t.Fatalf("node not installed executable: %v", err)
	}
	if target, _ := os.Readlink(filepath.Join(folder, "node", "bin", "npm")); target != "../lib/npm.js" {
		t.Fatalf("symlink: %q", target)
	}
	if got := Installed(folder); len(got) != 1 || got[0] != "node 1.2.3" {
		t.Fatalf("Installed: %q", got)
	}
}

func TestBadChecksumInstallsNothing(t *testing.T) {
	old := tgz(t, map[string]string{"go/bin/go": "old"})
	evil := tgz(t, map[string]string{"go/bin/go": "evil"})
	url := serve(t, map[string][]byte{"/go.tar.gz": evil})
	folder := t.TempDir()
	if _, err := unpackSum(folder, "go", "1", url+"/go.tar.gz", "", sum(old)); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("want a checksum mismatch, got %v", err)
	}
	if _, err := unpackSum(folder, "go", "1", url+"/go.tar.gz", "", ""); err == nil {
		t.Fatal("an archive with no checksum was installed")
	}
	if entries, _ := os.ReadDir(folder); len(entries) != 0 {
		t.Fatalf("folder not left empty: %v", entries)
	}
}

func TestArchiveCannotLeaveItsFolder(t *testing.T) {
	arc := tgz(t, map[string]string{"top/../../escaped": "x"})
	url := serve(t, map[string][]byte{"/x.tar.gz": arc})
	folder := t.TempDir()
	if _, err := unpackSum(folder, "uv", "1", url+"/x.tar.gz", "bin", sum(arc)); err == nil {
		t.Fatal("an entry outside the folder was written")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(folder), "escaped")); err == nil {
		t.Fatal("escaped file exists")
	}
}

func TestFetchRefusesOddInput(t *testing.T) {
	if _, err := Fetch(t.TempDir(), "python", "3.12; rm -rf /"); err == nil {
		t.Fatal("a version with a command in it was accepted")
	}
	if _, err := Fetch(t.TempDir(), "rust", ""); err == nil {
		t.Fatal("an unknown tool was accepted")
	}
	if _, err := (&Module{}).Install("rust", "", nil); err == nil || !strings.Contains(err.Error(), "no tool") {
		t.Fatalf("Install didn't refuse an unknown tool before starting a sandbox: %v", err)
	}
}

func TestSumFor(t *testing.T) {
	sums := []byte("aaa  node-v1-linux-x64.tar.gz\nbbb  node-v1-darwin-arm64.tar.gz\n")
	if got := sumFor(sums, "node-v1-darwin-arm64.tar.gz"); got != "bbb" {
		t.Fatalf("got %q", got)
	}
	if got := sumFor(sums, "node-v1-linux-arm64.tar.gz"); got != "" {
		t.Fatalf("got %q for a missing name", got)
	}
}

// ordered builds a gzipped tar from name, body pairs in order ("->" is a symlink).
func ordered(t *testing.T, pairs ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for i := 0; i < len(pairs); i += 2 {
		h := &tar.Header{Name: pairs[i], Mode: 0o755, Size: int64(len(pairs[i+1])), Typeflag: tar.TypeReg}
		if target, ok := strings.CutPrefix(pairs[i+1], "->"); ok {
			h = &tar.Header{Name: pairs[i], Linkname: target, Typeflag: tar.TypeSymlink}
		}
		tw.WriteHeader(h)
		if h.Typeflag == tar.TypeReg {
			tw.Write([]byte(pairs[i+1]))
		}
	}
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

func TestArchiveLinkCannotLeaveItsFolder(t *testing.T) {
	for _, link := range []string{"->../go/bin", "->/tmp"} {
		folder := t.TempDir()
		os.MkdirAll(filepath.Join(folder, "go", "bin"), 0o755)
		os.WriteFile(filepath.Join(folder, "go", "bin", "go"), []byte("real"), 0o755)
		arc := ordered(t, "top/pivot", link, "top/pivot/go", "evil")
		url := serve(t, map[string][]byte{"/x.tar.gz": arc})
		if _, err := unpackSum(folder, "node", "1", url+"/x.tar.gz", "", sum(arc)); err == nil {
			t.Fatalf("a link %s out of the folder was accepted", link)
		}
		if b, _ := os.ReadFile(filepath.Join(folder, "go", "bin", "go")); string(b) != "real" {
			t.Fatalf("another tool was overwritten through %s: %q", link, b)
		}
	}
}

func TestUnpackSweepsStaleTemps(t *testing.T) {
	folder := t.TempDir()
	stale := filepath.Join(folder, ".node-123")
	os.MkdirAll(filepath.Join(stale, "bin"), 0o755)
	arc := tgz(t, map[string]string{"node-v1/bin/node": "x"})
	url := serve(t, map[string][]byte{"/n.tar.gz": arc})
	if _, err := unpackSum(folder, "node", "1", url+"/n.tar.gz", "", sum(arc)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Fatal("a temp dir left by a timed-out install was not swept")
	}
}

// Seatbelt runs on host paths and lets a spec reach only its egress proxy,
// so the installer's spec there needs a writable HOME and the proxy.
func TestInstallerOnSeatbeltGetsProxyAndHome(t *testing.T) {
	folder, home := t.TempDir(), t.TempDir()
	sp := installSpec(sandbox.Bare{}, folder, home, []string{"/opt/hive"}, filepath.Join(home, "log"), "node", "", nil)
	env := strings.Join(sandbox.SeatbeltEnv(sp, "4242"), "\n")
	for _, want := range []string{"HTTPS_PROXY=http://127.0.0.1:4242", "https_proxy=http://127.0.0.1:4242", "HOME=" + home} {
		if !strings.Contains(env, want+"\n") && !strings.HasSuffix(env, want) {
			t.Fatalf("installer env under Seatbelt lacks %s:\n%s", want, env)
		}
	}
	rw := false
	for _, m := range sp.Mounts {
		rw = rw || m.RW && m.Host == home
	}
	if !rw || sp.Argv[0] != "/opt/hive" || sp.Argv[2] != folder {
		t.Fatalf("installer spec: home writable %v, argv %q", rw, sp.Argv)
	}
	sp.Network = false
	if env := strings.Join(sandbox.SeatbeltEnv(sp, "4242"), "\n"); strings.Contains(env, "PROXY") {
		t.Fatalf("a spec with no network got the proxy:\n%s", env)
	}
}

// An install asked by a run whose egress list leaves out nodejs.org must not
// reach it: the installer's proxy holds to the run's list.
func TestInstallerHoldsToTheRunsEgressList(t *testing.T) {
	home := t.TempDir()
	sp := installSpec(sandbox.Bare{}, t.TempDir(), home, []string{"/opt/hive"}, filepath.Join(home, "log"), "node", "", []string{"api.anthropic.com"})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var refused bytes.Buffer
	e := sandbox.ServeEgress(ln, sp.Allow, sp.Local, &refused)
	defer e.Close()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintf(c, "CONNECT nodejs.org:443 HTTP/1.1\r\nHost: nodejs.org:443\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusForbidden || refused.String() != "nodejs.org:443 unlisted\n" {
		t.Fatalf("installer reached nodejs.org off the run's list: %s, refused %q", resp.Status, refused.String())
	}
}
