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
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
)

// Module is the release module.
type Module struct {
	C *core.Core
	// Restart is called after a new binary is in place (the daemon re-execs).
	Restart func()
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
	cfg := m.C.WS.LoadConfig()
	rel, err := m.C.Effects.Forge.LatestRelease(cfg.ReleaseRepo)
	if err != nil {
		return "", false, err
	}
	return rel.Tag, modules.Newer(core.Version, rel.Tag), nil
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
	if err := m.C.Effects.Forge.DownloadAsset(cfg.ReleaseRepo, tag, "checksums.txt", tmp); err != nil {
		return "", err
	}
	sums, err := readSums(filepath.Join(tmp, "checksums.txt"))
	if err != nil {
		return "", err
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(exe)
	for i, name := range names {
		if err := m.C.Effects.Forge.DownloadAsset(cfg.ReleaseRepo, tag, name, tmp); err != nil {
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
	return m.Update()
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
// merged since the last one and the reviewer runs that read its exact head; it
// refuses if one has no read, and a change to a root path waits for the person.
func (m *Module) AfterKept(project, sha string) {
	p, ok := m.C.Projects.Get(project)
	if !ok || !p.Self {
		return
	}
	alert := func(text string) {
		m.C.J.MustAppend("alert.raised", map[string]string{"kind": "release", "project": project, "text": text})
	}
	var manifest strings.Builder
	var root []string
	for _, mg := range m.merges(project, sha) {
		reads := m.C.Effects.Reads(project, mg.SHA)
		if len(reads) == 0 {
			alert(fmt.Sprintf("no release: PR #%d merged %s without a read of that exact commit", mg.PR, core.Short(mg.SHA)))
			return
		}
		fmt.Fprintf(&manifest, "- #%d at %s, read by %s\n", mg.PR, core.Short(mg.SHA), strings.Join(reads, ", "))
		root = append(root, mg.Root...)
	}
	if len(root) > 0 && !m.C.Effects.Approved(project, sha) {
		m.C.Person.Ask(core.Ask{Kind: "release", Ref: project + "@" + sha, Project: project,
			Title:  "Release the Hive at " + core.Short(sha) + "? It changes " + strings.Join(root, ", "),
			Detail: strings.Join(root, ", ") + " run with elevated rights, so the release waits for you.\n\n" + manifest.String(), Options: []string{"release", "hold"}})
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
	if len(root) > 0 {
		body += "\nThe person released the change to " + strings.Join(root, ", ") + ".\n"
	}
	if err := m.C.Effects.Forge.CreateRelease(p.Repo, next, sha, body); err != nil {
		alert("release failed: " + err.Error())
		return
	}
	m.C.J.MustAppend("hive.released", map[string]string{"project": project, "tag": next, "sha": sha})
}

// OnPerson is the person's word on a release held for a root path.
func (m *Module) OnPerson(a core.Ask) {
	project, sha, _ := strings.Cut(a.Ref, "@")
	if a.Answer == "release" || a.Answer == "yes" {
		m.C.Effects.Approve(project, sha)
		m.AfterKept(project, sha)
	}
}

type merge struct {
	PR       int
	SHA      string
	MergeSHA string `json:"merge_sha"`
	Root     []string
}

// merges lists the project's merges after its last release, up to the one that made upTo.
func (m *Module) merges(project, upTo string) []merge {
	var out []merge
	done := false
	m.C.J.Replay(func(e core.Event) {
		var x struct {
			merge
			Project string
		}
		if done || (e.Kind != "effect.merged" && e.Kind != "hive.released") || e.Decode(&x) != nil || x.Project != project {
			return
		}
		if e.Kind == "hive.released" {
			out = nil
			return
		}
		out = append(out, x.merge)
		done = x.MergeSHA == upTo
	})
	return out
}

func bump(tag string) string {
	var a, b, c int
	fmt.Sscanf(strings.TrimPrefix(tag, "v"), "%d.%d.%d", &a, &b, &c)
	return fmt.Sprintf("v%d.%d.%d", a, b, c+1)
}
