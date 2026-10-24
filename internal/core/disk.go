package core

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

// Room for checks. Every check used to start in a fresh temp home, so each
// one downloaded every Go module and rebuilt from nothing, and the temp area's
// quota filled up within hours. Now:
//
//   - Go modules come from one shared cache, filled by the effects process
//     (outside the sandbox, running no code of the commit's: `go mod
//     download`, as the recursive gate's prefetch does) once per go.mod and
//     go.sum, and mounted read-only with GOPROXY=off and -mod=readonly.
//   - A check's own home (its build cache) lives under the workspace's cache
//     folder, is capped in size (config check_cache_max_mb) and removed after.
//     A build cache shared between checks would let one commit's check plant
//     outputs another commit's check trusts, so it is never shared.
//   - A check that fails for want of room is can't-run, not red, and raises
//     one alert that names what is using the room.
//   - The hive.clean job (Prune here, work's PruneCheckouts) removes what
//     outlived its use.

// Cache is the workspace's folder of things the Hive can always rebuild.
func (w Workspace) Cache() string { return filepath.Join(w.Hive(), "cache") }

// GoModCache is the shared, read-only-inside Go module cache.
func (w Workspace) GoModCache() string { return filepath.Join(w.Cache(), "gomod") }

// CheckHomes holds each running check's own home.
func (w Workspace) CheckHomes() string { return filepath.Join(w.Cache(), "homes") }

func (w Workspace) goSums() string { return filepath.Join(w.Cache(), "gomod-sums") }

// GoModInside is where checks see the module cache.
const GoModInside = "/hive/gomod"

const (
	checkCacheDefaultMB = 8192
	modCacheMax         = 20 << 30 // past this the hourly sweep empties the module cache
	staleAfter          = 3 * time.Hour
)

// homeEvery is how often a running check's home is measured against its cap.
var homeEvery = 20 * time.Second

// ErrHomeFull means a check's home outgrew its cap and the check was ended.
var ErrHomeFull = errors.New("the check's own home outgrew its cap")

var diskFull = regexp.MustCompile(`(?i)disk quota exceeded|no space left on device|\bENOSPC\b|\bEDQUOT\b`)

// DiskFull returns the line of a check's output that says the disk or a
// quota is full, or "". A test's own words never count: not when the output
// names a failing test (its message may quote the error, as this feature's
// own tests do), and not on an indented line, where test runners put what a
// test printed. What counts is a tool's own error at the start of a line
// ("go: writing go.mod cache: write ...: disk quota exceeded").
func DiskFull(out string) string {
	if len(FailedTests(out)) > 0 {
		return ""
	}
	for _, l := range strings.Split(out, "\n") {
		if l != "" && l[0] != ' ' && l[0] != '\t' && diskFull.MatchString(l) {
			return strings.TrimSpace(l)
		}
	}
	return ""
}

// staleModules is what a check prints when the shared module cache lacks a
// module its marker says is there (the cache was emptied under it).
const staleModules = "module lookup disabled by GOPROXY=off"

// checkCacheCap is one check's home cap in bytes.
func (e *Effects) checkCacheCap() int64 {
	mb := e.ws.LoadConfig().CheckCacheMaxMB
	if mb <= 0 {
		mb = checkCacheDefaultMB
	}
	return int64(mb) << 20
}

// goMods is what a check gets of the shared module cache: none (the check
// then fetches its own modules as before) with why, or the mount and
// environment, held against Prune until done is called.
type goMods struct {
	mounts []sandbox.Mount
	env    []string
	note   string
	marker string
	done   func()
}

// goModules readies the shared module cache for the checkout in dir. It only
// applies where the sandbox has its own paths on Linux; elsewhere a check has
// the host's home.
func (e *Effects) goModules(sb sandbox.Sandbox, dir string) goMods {
	none := goMods{done: func() {}}
	if !sb.Isolated() || runtime.GOOS != "linux" {
		return none
	}
	mod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return none
	}
	sum, _ := os.ReadFile(filepath.Join(dir, "go.sum"))
	h := sha256.Sum256(append(append(mod, 0), sum...))
	key := hex.EncodeToString(h[:8])
	marker := filepath.Join(e.ws.goSums(), key)
	e.modMu.RLock() // Prune empties the cache only while no check fetches into or reads it
	if !Exists(marker) {
		unlock := e.lock("gomod:" + key)
		if !Exists(marker) {
			if err := e.prefetch(dir); err != nil {
				unlock()
				e.modMu.RUnlock()
				none.note = "modules not prefetched: " + err.Error()
				return none
			}
			os.MkdirAll(e.ws.goSums(), 0o755)
			os.WriteFile(marker, nil, 0o644)
		}
		unlock()
	}
	os.MkdirAll(e.ws.GoModCache(), 0o755) // a module with nothing to fetch still mounts it
	return goMods{mounts: []sandbox.Mount{{Host: e.ws.GoModCache(), Inside: GoModInside}},
		env: []string{"GOMODCACHE=" + GoModInside, "GOFLAGS=-mod=readonly", "GOPROXY=off"}, marker: marker, done: e.modMu.RUnlock}
}

// prefetch downloads what dir's go.mod and go.sum name into the shared cache.
// It runs no code of the commit's: the go command reads go.mod and go.sum and
// fetches what they name from the module proxy, checked against go.sum and
// the checksum database. GOTOOLCHAIN=local, so a go.mod can't make it fetch
// and run another Go; GOVCS=*:off, so it never runs git or hg here against a
// URL the commit chose; GOWORK=off and GOENV=off, so no file but go.mod and
// go.sum steers it.
func (e *Effects) prefetch(dir string) error {
	goBin := filepath.Join(e.ws.Tools(), "go", "bin", "go")
	if !Exists(goBin) {
		p, err := exec.LookPath("go")
		if err != nil {
			return errors.New("no Go toolchain on the host")
		}
		goBin = p
	}
	os.MkdirAll(e.ws.Cache(), 0o755)
	tmp, err := os.MkdirTemp(e.ws.Cache(), "prefetch-")
	if err != nil {
		return err
	}
	defer RemoveTree(tmp)
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + tmp, "TMPDIR=" + tmp, "GOCACHE=" + filepath.Join(tmp, "gocache"), "GOPATH=" + filepath.Join(tmp, "gopath"),
		"GOMODCACHE=" + e.ws.GoModCache(), "GOFLAGS=-mod=readonly", "GOTOOLCHAIN=local", "GOVCS=*:off", "GOWORK=off", "GOENV=off",
		"CGO_ENABLED=0", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0"}
	for _, k := range []string{"GOPROXY", "GOSUMDB", "GONOSUMDB", "HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy", "NO_PROXY", "no_proxy", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, goBin, "mod", "download")
	cmd.Dir, cmd.Env = dir, env
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go mod download: %v: %s", err, Clip(strings.TrimSpace(string(out)), 300))
	}
	return nil
}

// newHome makes a check's own home under the cache folder, marked live until
// the returned func removes it.
func (e *Effects) newHome() (string, func()) {
	os.MkdirAll(e.ws.CheckHomes(), 0o755)
	home, err := os.MkdirTemp(e.ws.CheckHomes(), "home-")
	if err != nil {
		home, _ = os.MkdirTemp("", "hive-check-home-")
	}
	e.mu.Lock()
	if e.homes == nil {
		e.homes = map[string]bool{}
	}
	e.homes[home] = true
	e.mu.Unlock()
	return home, func() {
		RemoveTree(home)
		os.Remove(home + ".stop")
		e.mu.Lock()
		delete(e.homes, home)
		e.mu.Unlock()
	}
}

// watchHome ends the check (by its stop file) once its home passes capB.
// The returned func stops watching and says whether the cap was hit.
func watchHome(home string, capB int64) func() bool {
	var over atomic.Bool
	if capB <= 0 {
		return over.Load
	}
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(homeEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if Size(home) > capB {
					over.Store(true)
					os.WriteFile(home+".stop", nil, 0o600)
					return
				}
			}
		}
	}()
	return func() bool { close(done); return over.Load() }
}

// Size is the bytes of the files under path; symlinks aren't followed.
func Size(path string) int64 {
	var n int64
	filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

// RemoveTree removes path and everything in it, first making read-only
// folders writable (the Go module cache's are, and so may an agent's be).
// Symlinks are removed, never followed.
func RemoveTree(path string) error {
	if err := os.RemoveAll(path); err == nil {
		return nil
	}
	filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			os.Chmod(p, 0o755)
		}
		return nil
	})
	return os.RemoveAll(path)
}

// RemoveCheckout removes an agent's checkout of a project. The caller makes
// sure its run has ended.
func (e *Effects) RemoveCheckout(project, id string) error {
	if id == "" || strings.ContainsAny(id, `/\`) || id == "." || id == ".." {
		return fmt.Errorf("bad checkout id %q", id)
	}
	dir := filepath.Join(e.ws.Checkouts(project), id)
	if !Exists(dir) {
		return nil
	}
	defer e.lock(project)()
	return RemoveTree(dir)
}

// Prune removes what checks left behind: homes of checks no longer running,
// the temp homes older builds made, clean checkouts and prefetch folders a
// crash left, and the module cache once it is past its cap and no check is
// using it. It returns what it removed.
func (e *Effects) Prune() string {
	now := time.Now()
	e.mu.Lock()
	live := maps.Clone(e.homes)
	e.mu.Unlock()
	var gone []string
	var freed int64
	drop := func(p string) {
		n := Size(p)
		if RemoveTree(p) == nil {
			gone, freed = append(gone, p), freed+n
		}
	}
	old := func(p string) bool {
		fi, err := os.Lstat(p)
		return err == nil && now.Sub(fi.ModTime()) > staleAfter
	}
	globs := []string{filepath.Join(e.ws.CheckHomes(), "home-*"), filepath.Join(os.TempDir(), "hive-check-home-*"), filepath.Join(e.ws.Cache(), "prefetch-*")}
	for _, g := range globs {
		ms, _ := filepath.Glob(g)
		for _, p := range ms {
			if !live[p] && !strings.HasSuffix(p, ".stop") && old(p) {
				drop(p)
				os.Remove(p + ".stop")
			}
		}
	}
	projects, _ := os.ReadDir(e.ws.Gates())
	for _, pr := range projects {
		dirs, _ := os.ReadDir(filepath.Join(e.ws.Gates(), pr.Name()))
		for _, d := range dirs {
			if p := filepath.Join(e.ws.Gates(), pr.Name(), d.Name()); d.IsDir() && d.Name() != "recursive" && old(p) {
				drop(p)
			}
		}
	}
	if Size(e.ws.GoModCache()) > modCacheMax && e.modMu.TryLock() {
		RemoveTree(e.ws.goSums()) // first, so no marker outlives its modules
		drop(e.ws.GoModCache())
		e.modMu.Unlock()
	}
	if len(gone) == 0 {
		return ""
	}
	return fmt.Sprintf("removed %d leftover check folder(s), %s", len(gone), Bytes(freed))
}

// DiskPart is one kind of thing the Hive keeps on disk.
type DiskPart struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

// DiskUse is the Hive's disk use and the room left where it and its checks
// write, as last measured.
type DiskUse struct {
	At    time.Time  `json:"at"`
	Total int64      `json:"total"` // the whole workspace
	Parts []DiskPart `json:"parts"` // biggest first
	Room  []Room     `json:"room"`
}

// Room is a filesystem's free space and size (a quota on it shows as its size
// where the filesystem reports it so, as tmpfs and project quotas do).
type Room struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Free int64  `json:"free"`
	Size int64  `json:"size"`
}

// MeasureDisk measures the Hive's disk use now and keeps it for Disk.
func (e *Effects) MeasureDisk() DiskUse {
	ws := e.ws
	var checkouts int64
	projects, _ := os.ReadDir(ws.Projects())
	for _, p := range projects {
		checkouts += Size(ws.Checkouts(p.Name()))
	}
	homes := Size(ws.CheckHomes())
	if ms, _ := filepath.Glob(filepath.Join(os.TempDir(), "hive-check-home-*")); len(ms) > 0 {
		for _, m := range ms {
			homes += Size(m)
		}
	}
	d := DiskUse{At: time.Now().UTC(), Total: Size(ws.Root), Parts: []DiskPart{
		{"agents' checkouts", checkouts}, {"check homes", homes}, {"Go module cache", Size(ws.GoModCache())},
		{"check checkouts and gates", Size(ws.Gates())}, {"runs", Size(ws.Runs())}, {"tools", Size(ws.Tools())},
	}}
	slices.SortStableFunc(d.Parts, func(a, b DiskPart) int { return cmp.Compare(b.Bytes, a.Bytes) })
	for _, r := range []Room{{Name: "workspace", Path: ws.Root}, {Name: "temp", Path: os.TempDir()}} {
		if free, size, ok := statRoom(r.Path); ok {
			r.Free, r.Size = free, size
			d.Room = append(d.Room, r)
		}
	}
	e.mu.Lock()
	e.disk = d
	e.mu.Unlock()
	return d
}

// Disk is the last measurement (zero before the first).
func (e *Effects) Disk() DiskUse {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.disk
}

// String says the Hive's use and room in a line.
func (d DiskUse) String() string {
	var parts []string
	for _, p := range d.Parts {
		if p.Bytes > 0 {
			parts = append(parts, p.Name+" "+Bytes(p.Bytes))
		}
	}
	s := "the Hive uses " + Bytes(d.Total)
	if len(parts) > 0 {
		s += " (" + strings.Join(parts, ", ") + ")"
	}
	for _, r := range d.Room {
		s += fmt.Sprintf("; %s %s free of %s", r.Name, Bytes(r.Free), Bytes(r.Size))
	}
	return s
}

// Low reports whether a place the Hive writes has under a tenth of its room free.
func (d DiskUse) Low() bool {
	for _, r := range d.Room {
		if r.Size > 0 && r.Free < r.Size/10 {
			return true
		}
	}
	return false
}

// Bytes says n in the largest unit that keeps it at least 1.
func Bytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// diskAlert raises one alert while checks can't run for room, naming what
// uses it, and asks for the clean job; a check that runs again ends it.
func (e *Effects) diskAlert(line string) {
	e.mu.Lock()
	raised := e.diskFull
	e.diskFull = true
	e.mu.Unlock()
	if raised {
		return
	}
	d := e.MeasureDisk()
	e.j.MustAppend("alert.raised", map[string]string{"kind": "disk", "ref": "disk",
		"text": "Checks can't run: the disk or a quota is full where they write (" + Clip(line, 200) + "). T" + strings.TrimPrefix(d.String(), "t") + ". Nothing is judged red meanwhile; old checkouts and check folders are being cleaned."})
	if e.OnDiskFull != nil {
		go e.OnDiskFull()
	}
}
