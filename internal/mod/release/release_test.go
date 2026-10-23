package release

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// serve is a release site with tag as the latest release of me/hive, holding
// each asset with a checksums.txt over them (lie corrupts the sums). The repo's
// old name, old/name, redirects to it as GitHub does after a rename.
func serve(t *testing.T, tag string, assets map[string]string, lie bool) {
	sums := ""
	for name, body := range assets {
		h := sha256.Sum256([]byte(body))
		if lie {
			h[0]++
		}
		sums += hex.EncodeToString(h[:]) + "  " + name + "\n"
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasPrefix(p, "/old/name/"):
			http.Redirect(w, r, "/me/hive/"+strings.TrimPrefix(p, "/old/name/"), http.StatusMovedPermanently)
		case p == "/me/hive/releases/latest":
			http.Redirect(w, r, "/me/hive/releases/tag/"+tag, http.StatusFound)
		case p == "/me/hive/releases/tag/"+tag:
		case p == "/me/hive/releases/download/"+tag+"/checksums.txt":
			w.Write([]byte(sums))
		case strings.HasPrefix(p, "/me/hive/releases/download/"+tag+"/") && assets[filepath.Base(p)] != "":
			w.Write([]byte(assets[filepath.Base(p)]))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	site := Site
	t.Cleanup(func() { Site = site })
	Site = srv.URL
}

// releaseAt is the release module in a build stamped version, following repo,
// whose binary is a file in a temp dir that an update replaces.
func releaseAt(t *testing.T, version, repo string) (*Module, string) {
	dir := t.TempDir()
	c, err := core.Open(core.Workspace{Root: filepath.Join(dir, "hive")}, &core.Local{Dir: filepath.Join(dir, "forge")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	cfg := c.WS.LoadConfig()
	cfg.ReleaseRepo = repo
	if err := c.WS.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, AssetName(runtime.GOOS, runtime.GOARCH))
	os.WriteFile(exe, []byte("old"), 0o755)
	v, e := core.Version, executable
	t.Cleanup(func() { core.Version, executable = v, e })
	core.Version, executable = version, func() string { return exe }
	return &Module{C: c}, exe
}

func newBuild() map[string]string {
	return map[string]string{AssetName(runtime.GOOS, runtime.GOARCH): "new", AssetName("linux", "amd64"): "new"}
}

// A source build (no release version) never replaces itself with a release.
func TestSourceBuildNeverUpdates(t *testing.T) {
	serve(t, "v9.9.9", newBuild(), false)
	m, exe := releaseAt(t, "0.0.0-dev", "me/hive")
	msg, err := m.Update()
	if raw, _ := os.ReadFile(exe); err != nil || !strings.Contains(msg, "source build") || string(raw) != "old" {
		t.Fatalf("source build: %q, %v, binary %q", msg, err, raw)
	}
}

// An install that still follows the repo's old name finds the release
// through the rename's redirect, checks it and puts it in place.
func TestUpdateThroughRenamedRepo(t *testing.T) {
	serve(t, "v9.9.9", newBuild(), false)
	m, exe := releaseAt(t, "v0.1.2", "old/name")
	msg, err := m.Update()
	if raw, _ := os.ReadFile(exe); err != nil || msg != "updated to v9.9.9" || string(raw) != "new" {
		t.Fatalf("update: %q, %v, binary %q", msg, err, raw)
	}
	core.Version = "v9.9.9"
	if msg, err := m.Update(); err != nil || msg != "already on v9.9.9" {
		t.Fatalf("second update: %q, %v", msg, err)
	}
}

// A download whose checksum doesn't match is never installed.
func TestChecksumMismatchKeepsBinary(t *testing.T) {
	serve(t, "v9.9.9", newBuild(), true)
	m, exe := releaseAt(t, "v0.1.2", "me/hive")
	_, err := m.Update()
	if raw, _ := os.ReadFile(exe); err == nil || !strings.Contains(err.Error(), "checksum") || string(raw) != "old" {
		t.Fatalf("mismatch: %v, binary %q", err, raw)
	}
}

// An automatic update waits while work is mid-merge, then goes ahead.
func TestAutoUpdateWaitsOutAMerge(t *testing.T) {
	serve(t, "v9.9.9", newBuild(), false)
	m, exe := releaseAt(t, "v0.1.2", "me/hive")
	merging := true
	m.Busy = func() bool { return merging }
	if msg, _ := m.AutoUpdate(); !strings.Contains(msg, "mid-merge") {
		t.Fatalf("updated mid-merge: %q", msg)
	}
	merging = false
	if msg, err := m.AutoUpdate(); err != nil || msg != "updated to v9.9.9" {
		t.Fatalf("after the merge: %q, %v", msg, err)
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "new" {
		t.Fatalf("binary %q", raw)
	}
}

// A binary still called hive-v0 that replaced a v0.1.2 daemon copies itself
// to hive and leaves hive-v0 forwarding there; one called hive, or a hive-v0
// that replaced a newer daemon (installed so on purpose), has nothing to move.
func TestRenameMovesHiveV0ToHive(t *testing.T) {
	a := autostart
	t.Cleanup(func() { autostart = a })
	var started string
	autostart = func(exe, _ string) { started = exe } // never the machine's own autostart
	ext := filepath.Ext(AssetName(runtime.GOOS, "amd64"))
	dir := t.TempDir()
	old := filepath.Join(dir, "hive-v0"+ext)
	os.WriteFile(old, []byte("bin"), 0o755)
	e := executable
	t.Cleanup(func() { executable = e })
	executable = func() string { return old }
	if got := Rename(dir, "v0.1.3"); got != "" || core.Exists(filepath.Join(dir, "hive"+ext)) {
		t.Fatalf("a chosen hive-v0 renamed: %q", got)
	}
	got := Rename(dir, "v0.1.2")
	hive := filepath.Join(dir, "hive"+ext)
	if raw, _ := os.ReadFile(hive); got != hive || string(raw) != "bin" || started != hive {
		t.Fatalf("rename: %q, hive %q, autostart %q", got, raw, started)
	}
	alias := old
	if runtime.GOOS == "windows" {
		alias = filepath.Join(dir, "hive-v0.cmd")
	} else if l, _ := os.Readlink(old); l != "hive" {
		t.Fatalf("hive-v0 links to %q", l)
	}
	if !core.Exists(alias) {
		t.Fatal("no hive-v0 alias")
	}
	executable = func() string { return hive }
	if got := Rename(dir, "v0.1.2"); got != "" {
		t.Fatalf("hive renamed again: %q", got)
	}
}
