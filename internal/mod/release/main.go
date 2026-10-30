package release

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
)

// Main is the channel the Hive that releases the Hive follows (issue #1941):
// its own main, not a release's build, gates and canary. What is behaviour
// (the default modules: the agents' prompts, the modules' policies) is copied
// from main's tip into .modules/ on every merge, so the next turn reads it
// with no restart (Live). What is machinery, the binary, follows main too:
// the newest commit on main whose forge CI passed there is built here from
// the self project's clone, stamped <line>-main.<sha>, and installed between
// turns, as an update is, once the checks running end. The binary it
// replaced, the last one that came up healthy (an update waits until the
// running one settled), stays beside it as .old: a new daemon that doesn't
// come up is rolled back to it by its third start (Boot), and one that comes
// up but misbehaves is anyone's call: `hive agent fallback` (or `hive update
// --release back`) puts it back at once. Releases go on being cut for every
// other install.
const Main = "main"

// self is the project here that is release_repo itself: this is the Hive
// that releases the Hive.
func (m *Module) self(cfg core.Config) (core.Project, bool) {
	for _, p := range m.C.Projects.List() {
		if p.Self && strings.EqualFold(p.Repo, cfg.ReleaseRepo) {
			return p, true
		}
	}
	return core.Project{}, false
}

// released is a release the Hive cut, as hive.released has it.
type released struct {
	Project string
	Tag     string
	SHA     string
}

// releases are the releases this Hive cut from the self project, oldest
// first; none when it isn't the Hive that releases the Hive.
func (m *Module) releases() (core.Project, []released) {
	p, ok := m.self(m.C.WS.LoadConfig())
	if !ok {
		return p, nil
	}
	var out []released
	m.C.J.Replay(func(e core.Event) {
		var r released
		if e.Kind == "hive.released" && e.Decode(&r) == nil && r.Project == p.Name && r.Tag != "" && hexName(r.SHA) {
			out = append(out, r)
		}
	}, "hive.released")
	return p, out
}

// mainBuild is the commit a build of main is stamped with
// (v0.1.150-main.<sha>), or "" for a release or a source build.
func mainBuild(version string) string {
	if i := strings.LastIndex(version, "-main."); i > 0 && hexName(version[i+len("-main."):]) {
		return version[i+len("-main."):]
	}
	return ""
}

// mainLooks is how many of main's newest commits a look weighs: a tip whose
// CI still runs doesn't hold an older green one back. Merges and agents'
// own pushes land without waiting on CI, so a busy hour can put a dozen
// commits on main while CI runs on the first.
const mainLooks = 30

// mainTag is the build of main to install next: the newest of main's last
// mainLooks commits whose forge CI passed there, past this build (descended
// from the commit it was built from) and not rolled back here, stamped on
// this build's line; "" when there is none.
func (m *Module) mainTag() string {
	p, ok := m.self(m.C.WS.LoadConfig())
	if !ok {
		return ""
	}
	m.C.Effects.Sync(p) // a failed fetch weighs main as the clone last saw it
	clone := m.C.WS.Project(p.Name)
	ref := "refs/remotes/origin/" + p.Branch
	if _, err := core.Git(clone, "rev-parse", "--verify", "-q", ref); err != nil {
		ref = "refs/heads/" + p.Branch
	}
	out, err := core.Git(clone, "rev-list", "--first-parent", "-n", fmt.Sprint(mainLooks), ref)
	if err != nil {
		return ""
	}
	line, running := core.Version, mainBuild(core.Version)
	if i := strings.Index(line, "-main."); i > 0 {
		line = line[:i]
	}
	if running == "" {
		_, rs := m.releases()
		for _, r := range rs {
			if r.Tag == core.Version {
				running = r.SHA
			}
		}
	}
	back := m.rolledBackFrom()
	for _, sha := range strings.Fields(out) {
		if running != "" && strings.HasPrefix(sha, running) {
			return "" // this build is main's newest green commit already
		}
		tag := line + "-main." + sha[:8]
		if back[tag] {
			continue
		}
		if running != "" {
			if _, err := core.Git(clone, "merge-base", "--is-ancestor", running, sha); err != nil {
				continue
			}
		}
		if m.ciPassed(p, sha) {
			return tag
		}
	}
	return ""
}

// ciPassed reports whether the forge's CI passed on main at sha: the
// project's forge_tests and required checks, all green there.
func (m *Module) ciPassed(p core.Project, sha string) bool {
	need := m.C.Effects.Config(p, sha).WithForgeTests(m.C.Effects.RequiredChecks(p, sha))
	if len(need) == 0 {
		return false
	}
	runs, err := m.C.Effects.Forge.Checks(p.Repo, sha)
	if err != nil {
		return false
	}
	st, _ := core.ChecksState(runs, need)
	return st == core.CheckPass
}

// releasedHere is the self project and the commit of tag when this Hive
// follows main and tag is a build of main (or a release it cut itself), so
// it builds tag rather than download it; sha is "" otherwise.
func (m *Module) releasedHere(tag string) (p core.Project, sha string) {
	if m.Channel() != Main {
		return p, ""
	}
	p, rs := m.releases()
	if short := mainBuild(tag); short != "" && p.Name != "" {
		full, _ := core.Git(m.C.WS.Project(p.Name), "rev-parse", "--verify", "-q", short+"^{commit}")
		return p, strings.TrimSpace(full)
	}
	for _, r := range rs {
		if r.Tag == tag {
			sha = r.SHA
		}
	}
	return p, sha
}

// newer reports whether tag is past this build: a build of main that
// mainTag picked always is (it descends from this one); a release by its
// version.
func newer(tag string) bool {
	if mainBuild(tag) != "" {
		return tag != core.Version
	}
	return modules.Newer(core.Version, tag)
}

// buildAt builds the binaries named, stamped tag as release.yml stamps a
// release, from the self project's clone at sha into tmp, and returns their
// checksums.
func (m *Module) buildAt(p core.Project, sha, tag string, names []string, tmp string) (map[string]string, error) {
	clone := m.C.WS.Project(p.Name)
	if _, err := core.Git(clone, "cat-file", "-e", sha+"^{commit}"); err != nil {
		if _, err := m.C.Effects.Sync(p); err != nil {
			return nil, fmt.Errorf("can't fetch %s to build %s: %v", core.Short(sha), tag, err)
		}
	}
	src := filepath.Join(tmp, "src")
	if err := extract(clone, sha, src); err != nil {
		return nil, fmt.Errorf("can't read %s's tree: %v", core.Short(sha), err)
	}
	sum := map[string]string{}
	for _, name := range names {
		goos, goarch, ok := assetTarget(name)
		if !ok {
			return nil, fmt.Errorf("can't tell which system %s is for", name)
		}
		out := filepath.Join(tmp, name)
		if err := goBuild(m.C.WS, src, out, goos, goarch, tag, m.C.WS.LoadConfig().ReleaseRepo); err != nil {
			return nil, fmt.Errorf("%s at %s doesn't build: %v", tag, core.Short(sha), err)
		}
		got, err := fileSum(out)
		if err != nil {
			return nil, err
		}
		sum[name] = got
	}
	return sum, nil
}

// assetTarget is the system and architecture AssetName named.
func assetTarget(name string) (goos, goarch string, ok bool) {
	f := strings.Split(strings.TrimSuffix(strings.TrimPrefix(name, "hive-"), ".exe"), "-")
	if len(f) != 2 || AssetName(f[0], f[1]) != name {
		return "", "", false
	}
	return f[0], f[1], true
}

// extract writes the tree of commit sha in the clone, or only the paths
// named, under dst.
func extract(clone, sha, dst string, paths ...string) error {
	cmd := exec.Command("git", append([]string{"-C", clone, "archive", "--format=tar", sha}, paths...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	terr := untar(out, dst)
	io.Copy(io.Discard, out)
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("git archive: %v: %s", err, core.Clip(strings.TrimSpace(stderr.String()), 300))
	}
	return terr
}

// untar writes a tar's directories and regular files under dst, refusing a
// name that would land outside it; links and the rest are skipped.
func untar(r io.Reader, dst string) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.FromSlash(strings.TrimSuffix(h.Name, "/"))
		if name == "" || name == "." {
			continue
		}
		if !filepath.IsLocal(name) {
			return fmt.Errorf("%q is outside the tree", h.Name)
		}
		target := filepath.Join(dst, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode)&0o755|0o600)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, tr)
			f.Close()
			if err != nil {
				return err
			}
		}
	}
}

// goBuild builds ./cmd/hive in src into out, as release.yml builds a
// release. It runs no code of the commit's: the go command compiles it and
// fetches the modules go.sum names into the shared cache, checked against the
// checksum database. GOTOOLCHAIN=local, so go.mod can't make it fetch and run
// another Go; GOVCS=*:off, so it never runs git or hg against a URL the commit
// chose; GOWORK=off and GOENV=off, so no file but go.mod and go.sum steers it;
// CGO_ENABLED=0, so no C compiler runs. Its home and build cache are kept
// under the Hive's cache, so the next build is quick. Tests replace it.
var goBuild = func(ws core.Workspace, src, out, goos, goarch, tag, repo string) error {
	goBin := filepath.Join(ws.Tools(), "go", "bin", "go")
	if !core.Exists(goBin) {
		p, err := exec.LookPath("go")
		if err != nil {
			return errors.New("no Go toolchain on the host")
		}
		goBin = p
	}
	home := filepath.Join(ws.Cache(), "hive-build")
	for _, d := range []string{"home", "gocache", "gopath", "tmp"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			return err
		}
	}
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(home, "home"), "TMPDIR=" + filepath.Join(home, "tmp"),
		"GOCACHE=" + filepath.Join(home, "gocache"), "GOPATH=" + filepath.Join(home, "gopath"), "GOMODCACHE=" + ws.GoModCache(),
		"GOFLAGS=-mod=readonly", "GOTOOLCHAIN=local", "GOVCS=*:off", "GOWORK=off", "GOENV=off", "CGO_ENABLED=0",
		"GOOS=" + goos, "GOARCH=" + goarch, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0"}
	for _, k := range []string{"GOPROXY", "GOSUMDB", "GONOSUMDB", "HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy", "NO_PROXY", "no_proxy", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	const pkg = "github.com/ihsan-sa/hive-standalone-build/internal/core"
	cmd := exec.CommandContext(ctx, goBin, "build", "-trimpath", "-ldflags", "-s -w -X "+pkg+".Version="+tag+" -X "+pkg+".DefaultReleaseRepo="+repo, "-o", out, "./cmd/hive")
	cmd.Dir, cmd.Env = src, env
	if raw, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, core.ClipEnds(strings.TrimSpace(string(raw)), 600))
	}
	return nil
}

// checksWaitMax is the most an automatic update waits for the checks that
// were running when it first looked; the restart ends every check in flight.
var checksWaitMax = 45 * time.Minute

// waitChecks says why an automatic update to tag waits, or "": on a channel
// follow polls (main, pre-release), the checks running when it first looked
// for a release go on to their verdicts, for at most checksWaitMax, then it
// installs; one that starts meanwhile doesn't hold it, and dies with the
// restart. The wait runs from the first release that waited until one
// installs: a newer tag that lands meanwhile inherits it, so a main that
// merges more often than checksWaitMax still installs. `hive update`
// doesn't wait.
func (m *Module) waitChecks(tag string, auto bool) string {
	if !auto || m.Channel() == Latest {
		return "" // the latest channel looks every few hours, not every few minutes: a wait would hold it a look
	}
	running, _ := m.C.Effects.Slots.State()
	key := func(u core.SlotUse) string { return u.Project + "@" + u.SHA + " " + u.Kind + " " + u.Since.String() }
	now := time.Now()
	if m.checksAt == nil {
		m.checksAt, m.checksSince = map[string]bool{}, now
		for _, u := range running {
			m.checksAt[key(u)] = true
		}
	}
	n := 0
	for _, u := range running {
		if m.checksAt[key(u)] {
			n++
		}
	}
	by := m.checksSince.Add(checksWaitMax)
	if n == 0 || !now.Before(by) {
		if n > 0 {
			m.C.J.MustAppend("hive.update_held", map[string]any{"to": tag, "checks": n, "forced": true, "text": fmt.Sprintf("%d check(s) still running after %s; installing anyway", n, checksWaitMax)})
		}
		m.checksAt = nil
		return ""
	}
	return fmt.Sprintf("waiting: %d check(s) running; %s installs when they end, or at %s at the latest", n, tag, by.UTC().Format("15:04Z"))
}

// Fallback puts back the binary the last update replaced, kept beside this
// one as <name>.old, and restarts into it: the instant way back from a
// release that came up but misbehaves, which an agent judges (the fallback
// op), not a threshold. The release it left is journaled as rolled back, so
// it isn't installed again on its own; a newer one is.
func (m *Module) Fallback(why string) (string, error) {
	if strings.TrimSpace(why) == "" {
		return "", errors.New("say why: what the running release does wrong")
	}
	if !m.updating.TryLock() {
		return busy, nil
	}
	defer m.updating.Unlock()
	from := ""
	m.C.J.Replay(func(e core.Event) {
		var x struct{ From, To string }
		if e.Kind == "hive.updated" && e.Decode(&x) == nil && x.To == core.Version {
			from = x.From
		}
	}, "hive.updated")
	exe := executable()
	if from == "" || !core.Exists(exe+".old") {
		return "", fmt.Errorf("no binary an update replaced is kept beside %s, so there is nothing to fall back to from %s", exe, core.Version)
	}
	if err := tryBinary(exe+".old", from); err != nil {
		return "", fmt.Errorf("the binary kept from %s doesn't run (%v); staying on %s", from, err, core.Version)
	}
	paths := []string{exe}
	for _, name := range alongside[runtime.GOOS] {
		if p := filepath.Join(filepath.Dir(exe), name); core.Exists(p + ".old") {
			paths = append(paths, p)
		}
	}
	if err := rollBack(paths, nil); err != nil {
		return "", fmt.Errorf("putting %s back failed: %v", from, err)
	}
	os.Remove(pendingFile(m.C.WS)) // an unsettled update's start count is moot now
	m.C.J.MustAppend("hive.rolledback", map[string]string{"from": core.Version, "to": from, "why": why})
	m.C.J.MustAppend("alert.raised", map[string]string{"kind": "update", "ref": "restart",
		"text": "The Hive fell back from " + core.Version + " to " + from + ": " + core.Clip(why, 300) + ". It won't install " + core.Version + " on its own again; a newer release is."})
	if m.Restart != nil {
		go func() { time.Sleep(time.Second); m.Restart() }()
	}
	return "falling back to " + from + " and restarting", nil
}

// opFallback is the fallback op.
func (m *Module) opFallback(r core.Run, args json.RawMessage) (any, error) {
	var a struct{ Why string }
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	return m.Fallback(a.Why)
}

// live keeps .modules/ on main's tip: at is the commit last copied, running
// says a copy runs, and dirty that a merge came while it ran.
type live struct {
	mu             sync.Mutex
	started        bool
	running, dirty bool
	at             string
}

// onEvent starts a copy of main's default modules when a merge lands in the
// self project.
func (m *Module) onEvent(e core.Event) {
	if e.Kind != "effect.merged" || m.Live == nil {
		return
	}
	var x struct{ Project string }
	if e.Decode(&x) != nil {
		return
	}
	if p, ok := m.self(m.C.WS.LoadConfig()); ok && p.Name == x.Project {
		m.goLive(true)
	}
}

// goLive copies main's default modules in the background, one copy at a
// time; a merge during one asks for another after it. fetch says main moved
// on the forge since the clone last looked.
func (m *Module) goLive(fetch bool) {
	m.live.mu.Lock()
	if m.live.running {
		m.live.dirty = true
		m.live.mu.Unlock()
		return
	}
	m.live.running = true
	m.live.mu.Unlock()
	go func() {
		for {
			m.LiveSync(fetch)
			m.live.mu.Lock()
			if !m.live.dirty {
				m.live.running = false
				m.live.mu.Unlock()
				return
			}
			m.live.dirty, fetch = false, true
			m.live.mu.Unlock()
		}
	}()
}

// LiveSync copies the default modules from the self project's main into
// .modules/ (Live), when this Hive follows main and main has moved since the
// last copy. A module edited in .modules/ by hand stays as it is.
func (m *Module) LiveSync(fetch bool) string {
	cfg := m.C.WS.LoadConfig()
	p, ok := m.self(cfg)
	if !ok || m.Live == nil || m.Channel() != Main {
		return ""
	}
	clone := m.C.WS.Project(p.Name)
	sha := ""
	if fetch || !core.Exists(filepath.Join(clone, ".git")) {
		var err error
		if sha, err = m.C.Effects.Sync(p); err != nil {
			return "can't fetch main: " + err.Error()
		}
	} else {
		sha, _ = core.Git(clone, "rev-parse", "--verify", "-q", "refs/remotes/origin/"+p.Branch)
	}
	if sha == "" || sha == m.live.at {
		return ""
	}
	tmp, err := os.MkdirTemp("", "hive-live-")
	if err != nil {
		return err.Error()
	}
	defer os.RemoveAll(tmp)
	if err := extract(clone, sha, tmp, "modules"); err != nil {
		return "can't read main's modules: " + err.Error()
	}
	if err := m.Live(os.DirFS(filepath.Join(tmp, "modules")), "main@"+core.Short(sha)); err != nil {
		m.C.J.MustAppend("alert.raised", map[string]string{"kind": "update", "ref": "release:live",
			"text": "Copying main's modules at " + core.Short(sha) + " into .modules failed: " + core.Clip(err.Error(), 300)})
		return err.Error()
	}
	m.live.at = sha
	m.C.J.MustAppend("release.live", map[string]string{"project": p.Name, "sha": sha})
	return "modules live at " + core.Short(sha)
}
