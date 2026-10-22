// Package release is the Hive on itself: installs follow the Hive's releases,
// and when the Hive's own project merges a change that keeps its tests and a
// dry run green, it cuts the next release.
package release

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
	"github.com/ihsan-sa/hive-standalone-build/internal/setup"
)

// Module is the release module.
type Module struct {
	C *core.Core
	// Restart is called after a new binary is in place (the daemon re-execs).
	Restart func()
	// Busy reports work mid-merge, which an automatic update waits out.
	Busy func() bool
}

// Site serves the releases: GitHub over plain HTTPS, so updating needs no
// login and follows the redirect from a renamed repo. Tests point it elsewhere.
var Site = "https://github.com"

// executable is the binary an update replaces, through any hive-v0 link.
var executable = setup.Executable

var client = &http.Client{Timeout: 10 * time.Minute}

func get(path string) (*http.Response, error) {
	resp, err := client.Get(Site + "/" + path)
	if err == nil && resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		err = fmt.Errorf("%s: %s", path, resp.Status)
	}
	return resp, err
}

// latest is the newest release's tag: releases/latest redirects to its page.
func latest(repo string) (string, error) {
	resp, err := get(repo + "/releases/latest")
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	_, tag, ok := strings.Cut(resp.Request.URL.Path, "/releases/tag/")
	if !ok {
		return "", fmt.Errorf("%s has no release", repo)
	}
	return tag, nil
}

func download(repo, tag, name, dir string) error {
	resp, err := get(repo + "/releases/download/" + tag + "/" + name)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// AssetName is the release asset for an OS and architecture.
func AssetName(goos, goarch string) string {
	n := "hive-" + goos + "-" + goarch
	if goos == "windows" {
		n += ".exe"
	}
	return n
}

// Check returns the newest release tag if it is newer than this build.
func (m *Module) Check() (string, bool, error) {
	tag, err := latest(m.C.WS.LoadConfig().ReleaseRepo)
	if err != nil {
		return "", false, err
	}
	return tag, modules.Newer(core.Version, tag), nil
}

// Update installs the newest release over this binary. Live runs are separate
// processes and the daemon reattaches to them after it restarts, so live work
// carries across.
func (m *Module) Update() (string, error) {
	if !strings.HasPrefix(core.Version, "v") {
		return "this is a source build (" + core.Version + "); it doesn't update itself", nil
	}
	tag, newer, err := m.Check()
	if err != nil {
		return "", err
	}
	if !newer {
		return "already on " + core.Version, nil
	}
	cfg := m.C.WS.LoadConfig()
	tmp, err := os.MkdirTemp("", "hive-update-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	names := []string{AssetName(runtime.GOOS, runtime.GOARCH)}
	if runtime.GOOS == "windows" {
		names = append(names, AssetName("linux", "amd64")) // used inside WSL
	}
	if err := download(cfg.ReleaseRepo, tag, "checksums.txt", tmp); err != nil {
		return "", err
	}
	sums, err := readSums(filepath.Join(tmp, "checksums.txt"))
	if err != nil {
		return "", err
	}
	exe := executable()
	dir := filepath.Dir(exe)
	for i, name := range names {
		if err := download(cfg.ReleaseRepo, tag, name, tmp); err != nil {
			return "", err
		}
		got, err := fileSum(filepath.Join(tmp, name))
		if err != nil {
			return "", err
		}
		if want := sums[name]; want == "" || want != got {
			return "", fmt.Errorf("%s: checksum mismatch; not installing", name)
		}
		target := exe
		if i > 0 {
			target = filepath.Join(dir, name)
		}
		if err := replace(filepath.Join(tmp, name), target); err != nil {
			return "", err
		}
	}
	m.C.J.MustAppend("hive.updated", map[string]string{"from": core.Version, "to": tag})
	if m.Restart != nil {
		go func() { time.Sleep(time.Second); m.Restart() }()
	}
	return "updated to " + tag, nil
}

// AutoUpdate is the scheduled job: update when allowed and nothing is mid-merge.
func (m *Module) AutoUpdate() (string, error) {
	if !m.C.WS.LoadConfig().AutoUpdate {
		return "auto-update is off", nil
	}
	if m.Busy != nil && m.Busy() {
		return "waiting: work is mid-merge", nil
	}
	return m.Update()
}

// Rename moves an install still called hive-v0 (v0.1.2 and older) to hive.
// It copies this binary to hive beside it, leaves hive-v0 forwarding there,
// points autostart at hive and returns the path the daemon should restart
// from, or "" when there is nothing to move.
func Rename(root string) string {
	exe := executable()
	dir, base := filepath.Split(exe)
	ext := filepath.Ext(base)
	if base != "hive-v0"+ext {
		os.Remove(filepath.Join(dir, "hive-v0"+ext+".old")) // left by a rename on Windows
		return ""
	}
	target := filepath.Join(dir, "hive"+ext)
	if err := replace(exe, target); err != nil {
		return ""
	}
	if runtime.GOOS == "windows" {
		// A running exe can be renamed but not removed; the .cmd forwards.
		os.Rename(exe, exe+".old")
		os.WriteFile(filepath.Join(dir, "hive-v0.cmd"), []byte("@\"%~dp0hive.exe\" %*\r\n"), 0o755)
	} else {
		os.Remove(exe)
		os.Symlink("hive", exe)
	}
	if setup.AutostartEnabled() {
		setup.EnableAutostartAt(target, root)
	}
	return target
}

// replace moves src over dst; a running Windows executable is renamed aside first.
func replace(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	next := dst + ".new"
	if err := os.WriteFile(next, raw, 0o755); err != nil {
		return err
	}
	old := dst + ".old"
	os.Remove(old)
	if core.Exists(dst) {
		if err := os.Rename(dst, old); err != nil {
			return err
		}
	}
	if err := os.Rename(next, dst); err != nil {
		os.Rename(old, dst)
		return err
	}
	return nil
}

func readSums(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 {
			out[strings.TrimPrefix(fields[1], "*")] = fields[0]
		}
	}
	if len(out) == 0 {
		return nil, errors.New("empty checksums.txt")
	}
	return out, sc.Err()
}

func fileSum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// AfterKept cuts a release when a kept merge lands in the Hive's own project
// and its dry run passes on that exact commit.
func (m *Module) AfterKept(project, sha string) {
	p, ok := m.C.Projects.Get(project)
	if !ok || !p.Self {
		return
	}
	cfg := m.C.Effects.Config(p, sha)
	if cfg.DryRun != "" {
		c := m.C.Effects.RunCheck(p, sha, "dryrun", cfg.DryRun)
		if !c.OK {
			m.C.J.MustAppend("alert.raised", map[string]string{"kind": "release", "project": project, "text": "dry run failed on " + core.Short(sha) + "; no release"})
			return
		}
	}
	last := "v0.0.0"
	if rel, err := m.C.Effects.Forge.LatestRelease(p.Repo); err == nil && rel.Tag != "" {
		last = rel.Tag
	}
	next := bump(last)
	if err := m.C.Effects.Forge.CreateRelease(p.Repo, next, sha, "Released by the Hive after "+core.Short(sha)+" kept its tests and dry run green."); err != nil {
		m.C.J.MustAppend("alert.raised", map[string]string{"kind": "release", "project": project, "text": "release failed: " + err.Error()})
		return
	}
	m.C.J.MustAppend("hive.released", map[string]string{"project": project, "tag": next, "sha": sha})
}

func bump(tag string) string {
	var a, b, c int
	fmt.Sscanf(strings.TrimPrefix(tag, "v"), "%d.%d.%d", &a, &b, &c)
	return fmt.Sprintf("v%d.%d.%d", a, b, c+1)
}
