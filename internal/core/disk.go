package core

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
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
//   - A check's own home lives under the workspace's cache folder, is capped
//     in size (config check_cache_max_mb) and removed after.
//   - A Go project's build cache is shared only from main: a check of a
//     commit on main's line writes the project's cache, and a check of any
//     other commit reads it (read-only, through `hive gocacheprog`) and keeps
//     what it builds in its home. So a check reuses what main's checks built
//     and passed, and no commit's check can plant outputs another trusts.
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

// GoBuildCache is a project's build cache, written only by checks of main.
func (w Workspace) GoBuildCache(project string) string {
	return filepath.Join(w.Cache(), "gobuild", project)
}

// GoModInside is where checks see the module cache.
const GoModInside = "/hive/gomod"

// GoBuildInside is where checks see the project's build cache.
const GoBuildInside = "/hive/gobuild"

// homeTmp is the check's temp folder (TMPDIR) inside its home.
const homeTmp = "tmp"

const (
	checkCacheDefaultMB = 8192
	modCacheMax         = 20 << 30 // past this the hourly sweep empties the module cache
	buildCacheMax       = 10 << 30 // and a project's build cache past this
	staleAfter          = 3 * time.Hour
	stopGrace           = time.Minute // a stop file outlives its home this long, so its sandbox sees it
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
	return toolDiskFull(out)
}

// toolDiskFull is DiskFull's line whether or not tests failed. RunCheck
// takes it when a place checks write is short of room as the check ends:
// a real full disk can make a test fail too, and then the failure is the
// disk's.
func toolDiskFull(out string) string {
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

// ownModules marks a go.mod and go.sum whose check needed a module they don't
// name (`go run x@latest`, a tool fetched by version): the shared cache can't
// serve it, so later checks of the same files fetch their own from the start
// instead of running twice.
const ownModules = ".own"

var missingModule = regexp.MustCompile(`(\S+)@(\S+): ` + regexp.QuoteMeta(staleModules))

// cacheLacked says why a check that printed staleModules failed: the shared
// cache lacked a module go.sum names (its fault: true), or the check wanted
// one go.sum doesn't name (false). A line it can't read counts as the cache's.
func cacheLacked(out string, sum []byte) bool {
	m := missingModule.FindStringSubmatch(out)
	if m == nil {
		return true
	}
	mod, ver := m[1], m[2]
	s := "\n" + string(sum)
	return strings.Contains(s, "\n"+mod+" "+ver+" ") || strings.Contains(s, "\n"+mod+" "+ver+"/go.mod ")
}

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
	sum    []byte // the go.sum the marker is for
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
	// go.mod and go.sum are the commit's and the go command reads them here,
	// outside the sandbox: each must be a plain file (a symlink could point it
	// at a host file), and every replace pointing at a folder must stay in the
	// checkout through no symlink.
	if _, err := os.Lstat(filepath.Join(dir, "go.mod")); err != nil {
		return none
	}
	mod, err := SafeRead(filepath.Join(dir, "go.mod"), 1<<20)
	if err != nil {
		none.note = "modules not prefetched: go.mod is not a plain file"
		return none
	}
	var sum []byte
	if _, err := os.Lstat(filepath.Join(dir, "go.sum")); err == nil {
		if sum, err = SafeRead(filepath.Join(dir, "go.sum"), 16<<20); err != nil {
			none.note = "modules not prefetched: go.sum is not a plain file"
			return none
		}
	}
	if why := localReplace(dir, string(mod)); why != "" {
		none.note = "modules not prefetched: " + why
		return none
	}
	h := sha256.Sum256(append(append(mod, 0), sum...))
	key := hex.EncodeToString(h[:8])
	marker := filepath.Join(e.ws.goSums(), key)
	if Exists(marker + ownModules) {
		none.note = "modules not prefetched: a check of this go.mod and go.sum needed a module they don't name"
		return none
	}
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
			// A go.mod can name modules enough to fill the disk; past its
			// cap the cache serves no new marker and the clean job empties
			// it once no check reads it.
			if Size(e.ws.GoModCache()) > modCacheMax {
				unlock()
				e.modMu.RUnlock()
				if e.OnDiskFull != nil {
					go e.OnDiskFull()
				}
				none.note = "modules not prefetched: the shared module cache is past its cap until the clean job empties it"
				return none
			}
			os.MkdirAll(e.ws.goSums(), 0o755)
			os.WriteFile(marker, nil, 0o644)
		}
		unlock()
	}
	os.MkdirAll(e.ws.GoModCache(), 0o755) // a module with nothing to fetch still mounts it
	return goMods{mounts: []sandbox.Mount{{Host: e.ws.GoModCache(), Inside: GoModInside}},
		env: []string{"GOMODCACHE=" + GoModInside, "GOFLAGS=-mod=readonly", "GOPROXY=off"}, marker: marker, sum: sum, done: e.modMu.RUnlock}
}

// goBuild readies the project's shared build cache for a check of sha in dir,
// where goModules would share the module cache. A commit on main's line
// writes it; any other reads it through `hive gocacheprog` and writes only
// its home. Either way the checkout's file times are made a function of
// their content first, so a test that reads files is reused when they match.
func (e *Effects) goBuild(sb sandbox.Sandbox, p Project, sha, dir string) goMods {
	none := goMods{done: func() {}}
	if !sb.Isolated() || runtime.GOOS != "linux" {
		return none
	}
	if fi, err := os.Lstat(filepath.Join(dir, "go.mod")); err != nil || !fi.Mode().IsRegular() {
		return none
	}
	if err := stableTimes(dir); err != nil {
		none.note = "build cache not shared: " + err.Error()
		return none
	}
	shared := e.ws.GoBuildCache(p.Name)
	e.buildMu.RLock() // Prune empties it only while no check reads or writes it
	if e.MainLine(p)[sha] {
		os.MkdirAll(shared, 0o755)
		return goMods{mounts: []sandbox.Mount{{Host: shared, Inside: GoBuildInside, RW: true}},
			env: []string{"GOCACHE=" + GoBuildInside}, done: e.buildMu.RUnlock}
	}
	if !Exists(shared) {
		e.buildMu.RUnlock()
		return none // nothing of main's built yet
	}
	return goMods{mounts: []sandbox.Mount{{Host: shared, Inside: GoBuildInside}},
		env: []string{"GOCACHE=/hive/home/gocache", "GOCACHEPROG=/hive/bin/hive gocacheprog " + GoBuildInside + " /hive/home/gobuild"}, done: e.buildMu.RUnlock}
}

// stableTimes sets each tracked file's and folder's time in the checkout at
// dir from its git object ID, so the same content has the same time in every
// check. The go command reuses a passed test only if every file it opened
// has the size and time it had then; a fresh checkout's times never match.
// Times run over 17 years to the nanosecond: two contents sharing a size and
// a time is a 1 in 2^59 chance. Symlinks are left as they are, and os.Root
// keeps every change inside dir.
func stableTimes(dir string) error {
	out, err := Git(dir, "ls-tree", "-r", "-t", "-z", "HEAD")
	if err != nil {
		return err
	}
	top, err := Git(dir, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	at := func(id string) time.Time {
		v, _ := strconv.ParseUint(id[:min(len(id), 15)], 16, 64)
		return time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(v % (1 << 29 * uint64(time.Second))))
	}
	// Deepest first: a folder's time is set after what is in it.
	recs := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	for i := len(recs) - 1; i >= 0; i-- {
		meta, path, ok := strings.Cut(recs[i], "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 || f[0] == "120000" || f[1] == "commit" {
			continue
		}
		if fi, err := root.Lstat(path); err == nil && fi.Mode()&fs.ModeSymlink == 0 {
			root.Chtimes(path, at(f[2]), at(f[2]))
		}
	}
	return root.Chtimes(".", at(strings.TrimSpace(top)), at(strings.TrimSpace(top)))
}

// localReplace says why go.mod's replace directives would lead the go command
// out of the checkout at dir, or "": a folder target must be relative, stay
// inside dir and reach it through no symlink. It doesn't parse directives or
// blocks (go accepts "replace(" as well as "replace ("): it splits each line
// into tokens as go's lexer does, every line with an "=>" token is a
// replacement, and the token after it, quoted or not, is its target. A line
// it can't read, or with more than one "=>", refuses the prefetch.
func localReplace(dir, mod string) string {
	for _, l := range strings.Split(mod, "\n") {
		toks, ok := modTokens(l)
		arrows, named := 0, 0
		var target string
		for i, t := range toks {
			if t.text == "=>" && !t.quoted {
				arrows++
				if i+1 < len(toks) {
					target = toks[i+1].text
				}
			}
			if strings.Contains(t.text, "=>") {
				named++
			}
		}
		if !ok && strings.Contains(l, "=>") || named > 1 || named == 1 && (arrows != 1 || target == "") {
			return "a replace in go.mod can't be read"
		}
		if arrows == 0 || !dirPath(target) {
			continue // no replace, or a module path, fetched from the proxy
		}
		if strings.ContainsAny(target, `\:`) || strings.HasPrefix(target, "/") {
			return "a replace names a folder outside the checkout"
		}
		joined := filepath.Join(dir, target)
		rel, err := filepath.Rel(dir, joined)
		if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			return "a replace names a folder outside the checkout"
		}
		realDir, _ := filepath.EvalSymlinks(dir)
		if real, err := filepath.EvalSymlinks(joined); err == nil && real != filepath.Join(realDir, rel) {
			return "a replace reaches its folder through a symlink"
		}
		if fi, err := os.Lstat(filepath.Join(joined, "go.mod")); err == nil && !fi.Mode().IsRegular() {
			return "a replace's go.mod is not a plain file"
		}
	}
	return ""
}

// modToken is one token of a go.mod line, unquoted.
type modToken struct {
	text   string
	quoted bool
}

// modTokens splits a go.mod line into tokens as go's lexer
// (golang.org/x/mod/modfile) does, up to a comment: a string quoted at a
// token's start, one of the punctuation characters ()[]{}, or a run of
// anything else but space, which a "//" ends. So "=>" is the arrow only as a
// token of its own ("a=>b" is one token to go). ok is false for a string with
// no end or one that doesn't unquote.
func modTokens(l string) (toks []modToken, ok bool) {
	for i := 0; i < len(l); {
		c := l[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case strings.HasPrefix(l[i:], "//"):
			return toks, true
		case c == '"' || c == '`':
			j := closing(l, i)
			if j < 0 {
				return toks, false
			}
			t := l[i+1 : j]
			if c == '"' {
				var err error
				if t, err = strconv.Unquote(l[i : j+1]); err != nil {
					return toks, false
				}
			}
			toks, i = append(toks, modToken{t, true}), j+1
		case strings.IndexByte("()[]{},", c) >= 0:
			toks, i = append(toks, modToken{text: string(c)}), i+1
		default:
			j := i
			for j < len(l) && strings.IndexByte(" \t\r()[]{},", l[j]) < 0 && !strings.HasPrefix(l[j:], "//") {
				j++
			}
			toks, i = append(toks, modToken{text: l[i:j]}), j
		}
	}
	return toks, true
}

// closing is the index of the quote closing the string that starts at i, or -1.
func closing(l string, i int) int {
	q := l[i]
	for j := i + 1; j < len(l); j++ {
		if q == '"' && l[j] == '\\' {
			j++
			continue
		}
		if l[j] == q {
			return j
		}
	}
	return -1
}

// dirPath reports whether a replace target is a folder, as the go command
// reads one (modfile.IsDirectoryPath), or could be on some system.
func dirPath(t string) bool {
	return t == "." || t == ".." || strings.HasPrefix(t, "./") || strings.HasPrefix(t, "../") || strings.HasPrefix(t, "/") ||
		strings.HasPrefix(t, `.\`) || strings.HasPrefix(t, `..\`) || strings.HasPrefix(t, `\`) || len(t) >= 2 && t[1] == ':'
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
	// Telemetry off: in a fresh home go always takes its daily upload token
	// and starts a detached child that can outlive it, still writing under
	// tmp while RemoveTree empties it (issue #514).
	off, err := goTelemetryOff(tmp)
	if err != nil {
		return fmt.Errorf("can't turn go's telemetry off: %v", err)
	}
	env = append(env, off...)
	for _, k := range []string{"GOPROXY", "GOSUMDB", "GONOSUMDB", "HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy", "NO_PROXY", "no_proxy", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, goBin, "mod", "download")
	cmd.Dir, cmd.Env = dir, env
	// go's output stays here: it can quote the files it read, and none of it
	// may reach a check's log, a PR or an alert.
	if _, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go mod download failed (%v)", err)
	}
	return nil
}

// newHome makes a check's own home under the cache folder, marked live until
// the returned func removes it. It is made and marked in one step, so Prune
// never sees a home of this daemon's that isn't marked. The home holds the
// check's temp folder (homeTmp) too: the sandbox's own /tmp is memory, and a
// Go build's test binaries there would take gigabytes of it per check.
func (e *Effects) newHome() (string, func()) {
	os.MkdirAll(e.ws.CheckHomes(), 0o755)
	e.mu.Lock()
	home, err := os.MkdirTemp(e.ws.CheckHomes(), "home-")
	if err != nil {
		home, _ = os.MkdirTemp("", "hive-check-home-")
	}
	if e.homes == nil {
		e.homes = map[string]bool{}
	}
	e.homes[home] = true
	e.mu.Unlock()
	os.Mkdir(filepath.Join(home, homeTmp), 0o700)
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
	every := homeEvery // read here: a test sets it, and the goroutine may outlive the test
	go func() {
		t := time.NewTicker(every)
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
	// Under the project's lock it is only moved aside, into the cache (on
	// the same filesystem, so a rename): removing a large checkout there
	// would hold up a push and other git work on the project meanwhile.
	// Marked live, Prune leaves it to this call; one a crash left goes there.
	os.MkdirAll(e.ws.Cache(), 0o755)
	aside := filepath.Join(e.ws.Cache(), "removing-"+NewID())
	e.markLive(aside, true)
	defer e.markLive(aside, false)
	unlock := e.lock(project)
	err := os.Rename(dir, aside)
	if err != nil {
		err = RemoveTree(dir)
	}
	unlock()
	if err == nil {
		err = RemoveTree(aside)
	}
	if err != nil {
		return err
	}
	e.j.MustAppend("effect.checkout_removed", map[string]string{"project": project, "id": id})
	return nil
}

// markLive marks a folder Prune must leave alone, or clears the mark.
func (e *Effects) markLive(p string, on bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.homes == nil {
		e.homes = map[string]bool{}
	}
	if on {
		e.homes[p] = true
	} else {
		delete(e.homes, p)
	}
}

// Prune removes what checks left behind: homes of checks no longer running,
// the temp homes older builds made, clean checkouts and prefetch folders a
// crash left, sandboxes' runtime folders and unread specs, and the module
// cache once it is past its cap and no check is using it. It returns what it
// removed.
//
// A home under the workspace that this daemon hasn't marked live belongs to
// a check whose daemon is gone (only one daemon runs on a workspace), so it
// goes at once, not after staleAfter: Prune first writes its stop file,
// which ends that check's sandbox within a second if it still runs (one
// started by a build that didn't end checks with their daemon). The stop
// file stays until a later sweep finds it alone and stopGrace old. So only
// the daemon calls Prune (the hive.clean job), holding the workspace's lock:
// any other Effects over the same workspace marks no home live, and its
// Prune would remove the running daemon's checks' homes.
func (e *Effects) Prune() string {
	now := time.Now()
	var gone []string
	var freed int64
	drop := func(p string) {
		n := Size(p)
		if RemoveTree(p) == nil {
			gone, freed = append(gone, p), freed+n
		}
	}
	old := func(p string, after time.Duration) bool {
		fi, err := os.Lstat(p)
		return err == nil && now.Sub(fi.ModTime()) > after
	}
	live := func(p string) bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.homes[p]
	}
	homes, _ := filepath.Glob(filepath.Join(e.ws.CheckHomes(), "home-*"))
	for _, p := range homes {
		switch {
		case strings.HasSuffix(p, ".stop"):
			if !Exists(strings.TrimSuffix(p, ".stop")) && old(p, stopGrace) {
				os.Remove(p)
			}
		case !live(p):
			os.WriteFile(p+".stop", nil, 0o600)
			drop(p)
		}
	}
	// The temp area is shared with other workspaces' Hives, whose homes this
	// daemon can't tell from dead ones but by age.
	for _, g := range []string{filepath.Join(os.TempDir(), "hive-check-home-*"), filepath.Join(e.ws.Cache(), "prefetch-*"), filepath.Join(e.ws.Cache(), "removing-*")} {
		ms, _ := filepath.Glob(g)
		for _, p := range ms {
			if !live(p) && !strings.HasSuffix(p, ".stop") && old(p, staleAfter) {
				drop(p)
				os.Remove(p + ".stop")
			}
		}
	}
	sandboxes := e.sweep()
	projects, _ := os.ReadDir(e.ws.Gates())
	for _, pr := range projects {
		dirs, _ := os.ReadDir(filepath.Join(e.ws.Gates(), pr.Name()))
		for _, d := range dirs {
			if p := filepath.Join(e.ws.Gates(), pr.Name(), d.Name()); d.IsDir() && d.Name() != "recursive" && !live(p) && old(p, staleAfter) {
				drop(p)
			}
		}
	}
	if Size(e.ws.GoModCache()) > modCacheMax && e.modMu.TryLock() {
		RemoveTree(e.ws.goSums()) // first, so no marker outlives its modules
		drop(e.ws.GoModCache())
		e.modMu.Unlock()
	}
	builds, _ := filepath.Glob(e.ws.GoBuildCache("*"))
	for _, b := range builds {
		if Size(b) > buildCacheMax && e.buildMu.TryLock() {
			drop(b)
			e.buildMu.Unlock()
		}
	}
	var did []string
	if len(gone) > 0 {
		did = append(did, fmt.Sprintf("removed %d leftover check folder(s), %s", len(gone), Bytes(freed)))
	}
	if sandboxes > 0 {
		did = append(did, fmt.Sprintf("%d dead sandbox's runtime folder(s) or spec(s)", sandboxes))
	}
	return strings.Join(did, ", ")
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

// MeasureDisk measures the Hive's disk use now and keeps it for Disk. It
// walks the workspace once: each part's folder is sized as the walk reaches
// it and counted in the total too, so nothing is walked twice.
func (e *Effects) MeasureDisk() DiskUse {
	ws := e.ws
	parts := []DiskPart{{Name: "agents' checkouts"}, {Name: "check homes"}, {Name: "Go module cache"},
		{Name: "check checkouts and gates"}, {Name: "runs"}, {Name: "tools"}, {Name: "Go build caches"}}
	at := map[string]int{ws.CheckHomes(): 1, ws.GoModCache(): 2, ws.Gates(): 3, ws.Runs(): 4, ws.Tools(): 5, filepath.Join(ws.Cache(), "gobuild"): 6}
	projects, _ := os.ReadDir(ws.Projects())
	for _, p := range projects {
		at[ws.Checkouts(p.Name())] = 0
	}
	var total int64
	other := map[string]int64{}
	filepath.WalkDir(ws.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if i, ok := at[p]; ok && d.IsDir() {
			n := Size(p)
			parts[i].Bytes += n
			total += n
			return filepath.SkipDir
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				total += fi.Size()
				other[topOf(ws.Root, p)] += fi.Size()
			}
		}
		return nil
	})
	// What no part names is told by the biggest folder holding it, so a
	// total far past the parts says where the rest went.
	var top string
	for k, n := range other {
		if k != "" && (top == "" || n > other[top]) {
			top = k
		}
	}
	if top != "" {
		parts = append(parts, DiskPart{Name: "other, most in " + top, Bytes: other[top]})
	}
	if ms, _ := filepath.Glob(filepath.Join(os.TempDir(), "hive-check-home-*")); len(ms) > 0 {
		for _, m := range ms {
			parts[1].Bytes += Size(m)
		}
	}
	d := DiskUse{At: time.Now().UTC(), Total: total, Parts: parts}
	slices.SortStableFunc(d.Parts, func(a, b DiskPart) int { return cmp.Compare(b.Bytes, a.Bytes) })
	d.Room = e.room()
	e.mu.Lock()
	e.disk = d
	e.mu.Unlock()
	return d
}

// room is the room left now where the Hive and its checks write. It is a
// statfs each, no walk, so it is cheap to read as a check ends.
func (e *Effects) room() []Room {
	var rs []Room
	for _, r := range []Room{{Name: "workspace", Path: e.ws.Root}, {Name: "temp", Path: os.TempDir()}} {
		if free, size, ok := roomOf(r.Path); ok {
			r.Free, r.Size = free, size
			rs = append(rs, r)
		}
	}
	return rs
}

// roomOf is statRoom, swapped in tests.
var roomOf = statRoom

// topOf is the folder two levels under root that holds p ("" for a file
// directly in root), as a slash path.
func topOf(root, p string) string {
	rel, err := filepath.Rel(root, filepath.Dir(p))
	if err != nil || rel == "." {
		return ""
	}
	if parts := strings.SplitN(filepath.ToSlash(rel), "/", 3); len(parts) > 2 {
		rel = parts[0] + "/" + parts[1]
	}
	return filepath.ToSlash(rel)
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
	if len(d.Room) > 0 && !quotaShown {
		s += " (a per-user quota doesn't show in these)"
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
	// The hourly hive.clean job's measurement names what uses the room; the
	// workspace is walked again here only if there is none from the last
	// hours, as a walk while checks wait on room only adds to their wait.
	d := e.Disk()
	if time.Since(d.At) > 2*time.Hour {
		d = e.MeasureDisk()
	} else {
		d.Room = e.room()
	}
	// The line is a check's output, so it is quoted as such.
	e.j.MustAppend("alert.raised", map[string]string{"kind": "disk", "ref": "disk",
		"text": "Checks can't run: the disk or a quota is full where they write (" + strconv.Quote(Clip(line, 200)) + "). T" + strings.TrimPrefix(d.String(), "t") + ". Nothing is judged red meanwhile; old checkouts and check folders are being cleaned."})
	if e.OnDiskFull != nil {
		go e.OnDiskFull()
	}
}

// goTelemetryOff keeps go from collecting telemetry under home, and from
// starting the detached child that writes there after go itself has
// exited: on a throwaway home that child raced RemoveTree, which then
// failed with "directory not empty" (TestPrefetchReadsOnlyTheCheckout,
// TestChecksShareTheModuleCache, in the gate's sandbox most of all). The
// mode file is what `go telemetry off` writes, put where go's
// os.UserConfigDir looks for it with HOME at home on this OS: $HOME/.config
// on Linux (XDG_CONFIG_HOME, which the returned env pins whatever the
// host's), Library/Application Support on macOS, AppData on Windows (the
// env names it there too, as go reads only the variable).
func goTelemetryOff(home string) ([]string, error) {
	var cfg string
	var env []string
	switch runtime.GOOS {
	case "darwin":
		cfg = filepath.Join(home, "Library", "Application Support")
	case "windows":
		cfg = filepath.Join(home, "AppData", "Roaming")
		env = append(env, "AppData="+cfg)
	default:
		cfg = filepath.Join(home, ".config")
		env = append(env, "XDG_CONFIG_HOME="+cfg)
	}
	dir := filepath.Join(cfg, "go", "telemetry")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "mode"), []byte("off"), 0o644); err != nil {
		return nil, err
	}
	return env, nil
}

// checkTmp points a check's TMPDIR at the temp folder in its home, on disk,
// counted against its cap and removed with it.
func checkTmp(env []string) []string {
	for i, kv := range env {
		if strings.HasPrefix(kv, "TMPDIR=") {
			env[i] = "TMPDIR=/hive/home/" + homeTmp
		}
	}
	return env
}
