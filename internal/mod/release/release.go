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
	"slices"
	"strings"
	"sync"
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
	locks   sync.Map // project -> *sync.Mutex held while AfterKept runs
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

// Rename moves an install still called hive-v0 to hive, when this daemon
// replaces one of v0.1.2 or older (from), which updated its hive-v0 binary in
// place; a hive-v0 installed on purpose (HIVE_CMD) keeps its name. It copies
// this binary to hive beside it, leaves hive-v0 forwarding there, points
// autostart at hive and returns the path the daemon should restart from, or
// "" when there is nothing to move.
func Rename(root, from string) string {
	exe := executable()
	dir, base := filepath.Split(exe)
	ext := filepath.Ext(base)
	if base != "hive-v0"+ext || (from != "v0.1.0" && from != "v0.1.1" && from != "v0.1.2") {
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
	autostart(target, root)
	return target
}

// autostart points a registered autostart at exe.
var autostart = func(exe, root string) {
	if setup.AutostartEnabled() {
		setup.EnableAutostartAt(exe, root)
	}
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
// and its dry run passes on that exact commit. The release names each change
// merged since the last one and the reviewer runs that read its exact head. A
// head with no read, or a change to a root path, waits for the person's word.
func (m *Module) AfterKept(project, sha string) {
	p, ok := m.C.Projects.Get(project)
	if !ok || !p.Self {
		return
	}
	// One at a time per project, so two callers never race for the same tag.
	l, _ := m.locks.LoadOrStore(project, &sync.Mutex{})
	l.(*sync.Mutex).Lock()
	defer l.(*sync.Mutex).Unlock()
	alert := func(text string) {
		m.C.J.MustAppend("alert.raised", map[string]string{"kind": "release", "project": project, "text": text})
	}
	list, stale := m.merges(project, sha)
	if stale {
		alert("no release at " + core.Short(sha) + ": a newer release already covers it")
		return
	}
	var manifest strings.Builder
	var root, unread []string
	for _, mg := range list {
		if reads := m.C.Effects.Reads(project, mg.SHA); len(reads) > 0 {
			fmt.Fprintf(&manifest, "- #%d at %s, read by %s\n", mg.PR, core.Short(mg.SHA), strings.Join(reads, ", "))
		} else {
			unread = append(unread, fmt.Sprintf("#%d at %s", mg.PR, core.Short(mg.SHA)))
			fmt.Fprintf(&manifest, "- #%d at %s, not read\n", mg.PR, core.Short(mg.SHA))
		}
		root = append(root, mg.Root...)
	}
	approved := m.C.Effects.Approved("release", project, sha)
	if (len(root) > 0 || len(unread) > 0) && !approved {
		m.hold(project, sha, root, unread, manifest.String())
		return
	}
	cfg := m.C.Effects.Config(p, sha)
	if cfg.DryRun != "" {
		c := m.C.Effects.RunCheck(p, sha, "dryrun", cfg.DryRun)
		if !c.OK {
			alert("dry run failed on " + core.Short(sha) + "; no release")
			return
		}
	}
	last := "v0.0.0"
	if rel, err := m.C.Effects.Forge.LatestRelease(p.Repo); err == nil && rel.Tag != "" {
		last = rel.Tag
	}
	next := bump(last)
	body := "Released by the Hive after " + core.Short(sha) + " kept its tests and dry run green.\n\nWhat was read:\n\n" + manifest.String()
	if approved {
		body += "\nThe person released it.\n"
	}
	if err := m.C.Effects.Forge.CreateRelease(p.Repo, next, sha, body); err != nil {
		alert("release failed: " + err.Error())
		return
	}
	m.C.J.MustAppend("hive.released", map[string]string{"project": project, "tag": next, "sha": sha})
}

// hold asks the person to release sha, and answers any older release ask for
// the project, which this one covers, so a stale answer can't release.
func (m *Module) hold(project, sha string, root, unread []string, manifest string) {
	ref := project + "@" + sha
	for _, a := range m.C.Person.Open() {
		if a.Kind == "release" && a.Project == project && a.Ref != ref {
			m.C.Person.Answer(a.ID, "superseded", "a release ask at "+core.Short(sha)+" covers it")
		}
	}
	var why []string
	if len(root) > 0 {
		why = append(why, strings.Join(root, ", ")+" run with elevated rights")
	}
	if len(unread) > 0 {
		why = append(why, strings.Join(unread, ", ")+" merged without a read of its exact commit")
	}
	m.C.Person.Ask(core.Ask{Kind: "release", Ref: ref, Project: project,
		Title:  "Release the Hive at " + core.Short(sha) + "? " + strings.Join(why, "; "),
		Detail: strings.Join(why, ", and ") + ", so the release waits for you.\n\n" + manifest, Options: []string{"release", "hold"}})
}

// OnPerson is the person's word on a held release; "release" also stands for
// any unread head the ask named.
func (m *Module) OnPerson(a core.Ask) {
	project, sha, _ := strings.Cut(a.Ref, "@")
	if a.Answer == "release" || a.Answer == "yes" {
		m.C.Effects.Approve("release", project, sha)
		m.AfterKept(project, sha)
	}
}

type merge struct {
	PR       int
	SHA      string
	MergeSHA string `json:"merge_sha"`
	Root     []string
}

// merges lists the project's merges after the one its last release was cut
// at, up to the one that made upTo. stale means a release already covers upTo.
func (m *Module) merges(project, upTo string) (out []merge, stale bool) {
	var all []merge
	from, at := 0, -1
	m.C.J.Replay(func(e core.Event) {
		var x struct {
			merge
			Project string
		}
		if (e.Kind != "effect.merged" && e.Kind != "hive.released") || e.Decode(&x) != nil || x.Project != project {
			return
		}
		if e.Kind == "effect.merged" {
			all = append(all, x.merge)
			if x.MergeSHA == upTo {
				at = len(all) - 1
			}
			return
		}
		i := slices.IndexFunc(all, func(mg merge) bool { return mg.MergeSHA == x.SHA })
		if i < 0 {
			i = len(all) - 1 // a release at a commit this journal didn't merge
		}
		from = max(from, i+1)
	})
	if at >= 0 && at < from {
		return nil, true
	}
	if at < 0 {
		at = len(all) - 1
	}
	return all[from : at+1], false
}

func bump(tag string) string {
	var a, b, c int
	fmt.Sscanf(strings.TrimPrefix(tag, "v"), "%d.%d.%d", &a, &b, &c)
	return fmt.Sprintf("v%d.%d.%d", a, b, c+1)
}
