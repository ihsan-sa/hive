// Package release is the Hive on itself: installs follow the Hive's releases,
// and when the Hive's own project merges a change that keeps its tests and a
// dry run green, it cuts the next release.
package release

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	locks   sync.Map // project -> *sync.Mutex held while a release is decided
	// PauseMerges stops new merges and waits up to the given time for the
	// running ones to end (the work module), so an update never restarts
	// mid-merge; ok false says one still runs, and resume lets merges go on.
	PauseMerges func(wait time.Duration) (resume func(), ok bool)
	// SampleOpen says the head sha merged unread and its sampled read hasn't
	// said land yet (the work module), so it doesn't go out unread meanwhile.
	SampleOpen func(project, sha string) bool
	// announced is the newest release a pinned Hive has said is out.
	announced string
	// said holds project@tag for each pre-release whose wait or failed
	// publish Publish has already raised in this life of the daemon.
	said map[string]bool
	// published is when Tick last started Publish; publishing says it runs.
	published  time.Time
	publishing bool
	pmu        sync.Mutex
	// followed is when follow last looked for a pre-release; red holds each
	// pre-release whose checks here failed, and waits what the newer ones
	// wait for, as the last look found.
	followed time.Time
	red      map[string]verdict
	waits    []string
	// shas are the pre-releases' commits, by repo@tag; tried is the tag the
	// last update went for and said1 what follow last recorded.
	shas  map[string]string
	tried string
	said1 string
	// pre are the pre-releases newer than this one the last look read;
	// newest and behind are what Lag reports, from the last Check.
	pre    []string
	newest string
	behind int
	// updating is held while an update runs: AutoUpdate, from its schedule
	// or follow, or `hive update`.
	updating sync.Mutex
}

// Site serves the releases: GitHub over plain HTTPS, so updating needs no
// login and follows the redirect from a renamed repo. Tests point it elsewhere.
var Site = "https://github.com"

// API serves each release's notes. Tests point it elsewhere.
var API = "https://api.github.com"

// executable is the binary an update replaces, through any hive-v0 link.
var executable = setup.Executable

// alongside are the binaries an update installs beside this system's own:
// Windows keeps the Linux one for inside WSL.
var alongside = map[string][]string{"windows": {AssetName("linux", "amd64")}}

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

// Notes are the release notes of each version after from up to to, newest
// first, each with its page; when they can't be fetched, the releases page.
func Notes(repo, from, to string) string {
	var rels []struct {
		Tag  string `json:"tag_name"`
		Body string
		URL  string `json:"html_url"`
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Get(API + "/repos/" + repo + "/releases?per_page=100")
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			err = errors.New(resp.Status)
		} else {
			err = json.NewDecoder(resp.Body).Decode(&rels)
		}
	}
	var b strings.Builder
	for _, r := range rels {
		if modules.Newer(from, r.Tag) && !modules.Newer(to, r.Tag) {
			fmt.Fprintf(&b, "%s: %s\n%s\n\n", r.Tag, r.URL, strings.TrimSpace(r.Body))
		}
	}
	if err != nil || b.Len() == 0 {
		return NotesLink(repo)
	}
	return strings.TrimSpace(b.String())
}

// NotesLink points at the releases page.
func NotesLink(repo string) string { return "What changed: " + Site + "/" + repo + "/releases" }

// AssetName is the release asset for an OS and architecture.
func AssetName(goos, goarch string) string {
	n := "hive-" + goos + "-" + goarch
	if goos == "windows" {
		n += ".exe"
	}
	return n
}

// Check returns the newest release, and where it is, if it is newer than this
// build. It looks at the Hive's releases and at the Hive's own project, so a
// change the person's Hive released on their fork is the next update. Only
// release_repo and the person's own fork of it are followed, never a
// repository an agent named, and a release whose checksums don't list this
// platform's binary is passed over. Following pre-releases, it takes the
// newest of release_repo's whose checks for this system passed, when newer.
func (m *Module) Check() (repo, tag string, newer bool, err error) {
	up := m.C.WS.LoadConfig().ReleaseRepo
	repos := []string{up}
	if user, e := m.C.Effects.Forge.User(); e == nil {
		fork := user + "/" + up[strings.LastIndex(up, "/")+1:]
		for _, p := range m.C.Projects.List() {
			if p.Self && p.Repo == fork && fork != up {
				repos = append(repos, fork)
			}
		}
	}
	out := "" // the upstream's latest release, which may lack a binary here yet
	for i, r := range repos {
		t, e := latest(r)
		if i == 0 && e == nil {
			out = t
		}
		if e == nil {
			e = hasBinary(r, t)
		}
		if e != nil {
			if i == 0 {
				err = e
			}
			continue
		}
		if tag == "" || modules.Newer(tag, t) {
			repo, tag = r, t
		}
	}
	m.pmu.Lock()
	m.pre = nil
	m.pmu.Unlock()
	if m.Channel() == Prerelease {
		after := core.Version
		if modules.Newer(after, tag) {
			after = tag
		}
		t, waits, e := m.prerelease(up, after)
		if e != nil {
			waits = append(waits, e.Error())
		}
		m.pmu.Lock()
		m.waits = waits
		m.pmu.Unlock()
		if t != "" {
			repo, tag = up, t
		} else if tag == "" && err == nil {
			err = e
		}
	} else {
		m.pmu.Lock()
		m.waits = nil
		m.pmu.Unlock()
	}
	if tag != "" {
		err = nil
	}
	m.lag(out, tag)
	return repo, tag, tag != "" && modules.Newer(core.Version, tag), err
}

// lag records the newest release out and how many releases this one is
// behind it: every pre-release the last look read, when following them, the
// latest release and the one Check took.
func (m *Module) lag(out ...string) {
	m.pmu.Lock()
	defer m.pmu.Unlock()
	seen := map[string]bool{}
	m.newest, m.behind = core.Version, 0
	for _, t := range append(slices.Clone(m.pre), out...) {
		if t == "" || seen[t] || !modules.Newer(core.Version, t) {
			continue
		}
		seen[t] = true
		m.behind++
		if modules.Newer(m.newest, t) {
			m.newest = t
		}
	}
}

// Lag is the newest release out, as the last look found, and how many
// releases newer than this one there are; "" before any look.
func (m *Module) Lag() (newest string, behind int) {
	m.pmu.Lock()
	defer m.pmu.Unlock()
	return m.newest, m.behind
}

// hasBinary errs unless the release's checksums are signed as this build
// requires and list this platform's binary.
func hasBinary(repo, tag string) error {
	sum, err := sums(repo, tag)
	if err == nil && sum[AssetName(runtime.GOOS, runtime.GOARCH)] == "" {
		err = fmt.Errorf("%s %s has no %s", repo, tag, AssetName(runtime.GOOS, runtime.GOARCH))
	}
	return err
}

// Update installs a release over this binary: tag, which pins the Hive there
// and may be older, or with "" or "latest" the newest that Check finds, which
// clears any pin. Live runs are separate processes and the daemon reattaches
// to them after it restarts, so live work carries across.
func (m *Module) Update(tag string) (string, error) {
	if !m.updating.TryLock() {
		return busy, nil
	}
	defer m.updating.Unlock()
	return m.update(tag)
}

// busy is what an update says while another is being checked.
const busy = "an update is already being checked; try again in a minute"

func (m *Module) update(tag string) (string, error) {
	m.pmu.Lock()
	m.tried = tag
	m.pmu.Unlock()
	if !strings.HasPrefix(core.Version, "v") {
		return "this is a source build (" + core.Version + "); it doesn't update itself", nil
	}
	cfg := m.C.WS.LoadConfig()
	repo, pin := cfg.ReleaseRepo, tag != "" && tag != "latest"
	if !pin {
		var err error
		if repo, tag, _, err = m.Check(); err != nil {
			return "", err
		}
	}
	if !pin && !modules.Newer(core.Version, tag) || pin && tag == core.Version {
		msg := "already on " + core.Version
		if pin {
			msg += ", pinned there"
		}
		return msg, m.pin(cfg, tag, pin)
	}
	m.pmu.Lock()
	m.tried = tag
	m.pmu.Unlock()
	if !pin && m.rolledBack(tag) {
		return tag + " was rolled back after it failed to start here; `" + core.Cmd + " update " + tag + "` tries it again", nil
	}
	tmp, err := os.MkdirTemp("", "hive-update-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	names := append([]string{AssetName(runtime.GOOS, runtime.GOARCH)}, alongside[runtime.GOOS]...)
	sum, err := sums(repo, tag)
	if err != nil {
		return "", err
	}
	for _, name := range names {
		if sum[name] == "" {
			return "", fmt.Errorf("release %s has no %s; not installing", tag, name)
		}
	}
	exe := executable()
	dir := filepath.Dir(exe)
	for _, name := range names {
		if err := download(repo, tag, name, tmp); err != nil {
			return "", err
		}
		got, err := fileSum(filepath.Join(tmp, name))
		if err != nil {
			return "", err
		}
		if sum[name] != got {
			return "", fmt.Errorf("%s: checksum mismatch; not installing", name)
		}
	}
	// No merge starts from here to the restart, and any running ends first;
	// tasks still in merging carry across and go on after it.
	resume := func() {}
	if m.PauseMerges != nil {
		var ok bool
		if resume, ok = m.PauseMerges(mergeWait); !ok {
			resume()
			return "waiting: a merge has run past " + mergeWait.String() + "; " + tag + " installs at the next look", nil
		}
	}
	installed := false
	defer func() {
		if !installed {
			resume()
		}
	}()
	// An update to tag from here whose restart never came left its binaries
	// in place; what it added has no .old and is still the update's to remove.
	var before []string
	if p, ok := readPending(m.C.WS); ok && p.From == core.Version && p.To == tag && p.RolledBack == "" {
		before = p.Added
	}
	var paths, added []string // replaced, each with its .old beside it, and put where none was
	for i, name := range names {
		target := exe
		if i > 0 {
			target = filepath.Join(dir, name)
		}
		if on, _ := fileSum(target); on == sum[name] {
			// In place already, from an update whose restart never came: its
			// .old is still the binary to roll back to.
			if slices.Contains(before, target) {
				added = append(added, target)
				continue
			}
			if core.Exists(target + ".old") {
				paths = append(paths, target)
				continue
			}
		}
		fresh := !core.Exists(target)
		if err := replace(filepath.Join(tmp, name), target); err != nil {
			rollBack(paths, added)
			return "", err
		}
		if fresh {
			added = append(added, target)
		} else {
			paths = append(paths, target)
		}
	}
	if err := tryBinary(exe, tag); err != nil {
		if e := rollBack(paths, added); e != nil {
			return "", fmt.Errorf("%s doesn't run here (%v), and putting %s back failed: %v", tag, err, core.Version, e)
		}
		return "", fmt.Errorf("%s doesn't run here (%v); kept %s", tag, err, core.Version)
	}
	if err := writePending(m.C.WS, pending{From: core.Version, To: tag, Paths: paths, Added: added}); err != nil {
		return "", err
	}
	if err := m.pin(cfg, tag, pin); err != nil {
		return "", err
	}
	notes := Notes(repo, core.Version, tag)
	unsigned := PublicKey == ""
	m.C.J.MustAppend("hive.updated", map[string]any{"from": core.Version, "to": tag, "notes": notes, "unsigned": unsigned})
	installed = true
	// Merges stay paused until the restart; one that never comes (a
	// supervisor that doesn't restart, a failed start) lets them go on.
	time.AfterFunc(restartGrace, resume)
	if m.Restart != nil {
		go func() { time.Sleep(time.Second); m.Restart() }()
	}
	msg := "updated to " + tag
	if pin {
		msg += ", pinned there with auto-update off: `" + core.Cmd + " update` goes back to the newest"
	}
	if unsigned {
		msg += "\n\n" + Unsigned
	}
	return msg + "\n\n" + notes, nil
}

// pin keeps the Hive on tag, or with on false follows the newest release.
// Pinning turns auto_update off, because releases older than the pin itself
// read only that and would update straight back to the newest; clearing the
// pin turns it back on if it was on before.
func (m *Module) pin(cfg core.Config, tag string, on bool) error {
	switch {
	case on && cfg.ReleasePin == "":
		cfg.PinnedAuto, cfg.AutoUpdate = cfg.AutoUpdate, false
	case !on && cfg.ReleasePin != "":
		cfg.AutoUpdate = cfg.AutoUpdate || cfg.PinnedAuto
		cfg.PinnedAuto = false
	case !on || cfg.ReleasePin == tag:
		return nil
	}
	if !on {
		tag = ""
	}
	cfg.ReleasePin = tag
	return m.C.WS.SaveConfig(cfg)
}

// mergeWait is the most an update waits for the merges already running.
var mergeWait = 2 * time.Minute

// restartGrace is how long merges stay paused after an update installs,
// waiting for its restart.
var restartGrace = 30 * time.Minute

// AutoUpdate is the scheduled job: update when allowed, pausing merges for
// the swap rather than waiting for a Hive with none in merging, which a busy
// one never is. A pinned Hive that was updating itself stays put and says once
// when a newer release is out. Following pre-releases, it also runs from
// follow, every few minutes.
func (m *Module) AutoUpdate() (string, error) {
	if !m.updating.TryLock() {
		return busy, nil
	}
	defer m.updating.Unlock()
	cfg := m.C.WS.LoadConfig()
	if cfg.ReleasePin != "" && (cfg.AutoUpdate || cfg.PinnedAuto) {
		tag, err := latest(cfg.ReleaseRepo)
		if err != nil || !modules.Newer(cfg.ReleasePin, tag) {
			return "pinned to " + cfg.ReleasePin, err
		}
		text := "pinned to " + cfg.ReleasePin + "; " + tag + " is out, and `" + core.Cmd + " update` installs it"
		if m.announced != tag {
			m.announced = tag
			m.C.J.MustAppend("alert.raised", map[string]string{"kind": "release", "ref": "release:pin", "text": text})
		}
		return text, nil
	}
	if !cfg.AutoUpdate || cfg.ReleasePin != "" {
		return "auto-update is off", nil
	}
	msg, err := m.update("")
	m.pmu.Lock()
	waits := m.waits
	m.pmu.Unlock()
	if strings.HasPrefix(msg, "already on") && len(waits) > 0 {
		msg += "; " + strings.Join(waits, "; ")
	}
	return msg, err
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

func parseSums(raw []byte) (map[string]string, error) {
	out := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			out[strings.TrimPrefix(fields[1], "*")] = fields[0]
		}
	}
	if len(out) == 0 {
		return nil, errors.New("empty checksums.txt")
	}
	return out, nil
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
// The release numbers on from this build, so the install switches to it at its
// next update. In the person's fork it also asks the person whether to offer
// the change upstream, one pull request that grows with each kept merge.
func (m *Module) AfterKept(project, sha string) {
	defer m.lock(project)()
	m.afterKept(project, sha)
}

// lock holds the project's release lock until the returned func runs, so two
// callers never race for the same tag or approve a release another covered.
func (m *Module) lock(project string) func() {
	l, _ := m.locks.LoadOrStore(project, &sync.Mutex{})
	l.(*sync.Mutex).Lock()
	return l.(*sync.Mutex).Unlock
}

func (m *Module) afterKept(project, sha string) {
	p, ok := m.C.Projects.Get(project)
	if !ok || !p.Self {
		return
	}
	alert := func(text string) {
		m.C.J.MustAppend("alert.raised", map[string]string{"kind": "release", "project": project, "text": text})
	}
	list, refuse := m.merges(project, sha)
	if refuse != "" {
		alert("no release at " + core.Short(sha) + ": " + refuse)
		return
	}
	var manifest strings.Builder
	var root, unread []string
	for _, mg := range list {
		if reads := m.C.Effects.Reads(project, mg.SHA, mg.Diff); len(reads) > 0 {
			fmt.Fprintf(&manifest, "- #%d at %s, read by %s\n", mg.PR, core.Short(mg.SHA), strings.Join(reads, ", "))
		} else if mg.Unread && len(mg.Root) == 0 && !m.sampleOpen(project, mg.SHA) && !m.readFix(project, mg.SHA) {
			// Merged unread by the gate's own rule: small, and no root, high,
			// protected, security-listed or gate-defining path, so the release
			// needn't wait, unless its sampled read is still to come or said fix.
			fmt.Fprintf(&manifest, "- #%d at %s, merged on its tests and checks (%d lines, no guarded path)\n", mg.PR, core.Short(mg.SHA), mg.Lines)
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
		if c.CantRun != "" {
			alert("dry run " + c.CantRun + " on " + core.Short(sha) + "; no release")
			return
		}
		if !c.OK {
			alert("dry run failed on " + core.Short(sha) + "; no release")
			return
		}
	}
	// The next tag is one past the highest anywhere: the release marked
	// latest may be older than pre-releases, and their tags are taken.
	tags, err := m.C.Effects.Forge.Tags(p.Repo)
	if err != nil {
		alert("no release at " + core.Short(sha) + ": can't list the tags: " + err.Error())
		return
	}
	next := nextTag(append(tags, core.Version))
	body := "Released by the Hive after " + core.Short(sha) + " kept its tests and dry run green. It is a pre-release until " +
		strings.Join(Gates, " and ") + " pass on that commit; then it is the latest, which installs follow.\n\nWhat was read:\n\n" + manifest.String()
	if approved {
		body += "\nThe person released it.\n"
	}
	if err := m.C.Effects.Forge.CreateRelease(p.Repo, next, sha, body); err != nil {
		alert("release failed: " + err.Error())
		return
	}
	// A tag made since the listing keeps its own commit, which the release
	// would then build and sign: such a release is refused, never published,
	// and these merges wait for the next one.
	rec := map[string]any{"project": project, "tag": next, "sha": sha, "prerelease": true}
	at, err := m.C.Effects.Forge.TagCommit(p.Repo, next)
	switch {
	case err != nil:
		// Publish reads the tag again before it publishes, so this only
		// says why the first check didn't happen.
		rec["tag_unread"] = core.Clip(err.Error(), 200)
		alert("released " + next + " at " + core.Short(sha) + " but can't read its tag's commit (" + core.Clip(err.Error(), 200) + "); it stays a pre-release until the tag reads as " + core.Short(sha) + ".")
	case at != sha:
		m.C.J.MustAppend("release.refused", map[string]string{"project": project, "tag": next, "sha": sha, "tag_sha": at})
		alert(next + " points at " + core.Short(at) + ", not the gated " + core.Short(sha) + " (a tag made outside the Hive); it stays a pre-release and is never published. " + refusedSteps)
		return
	default:
		rec["tag_sha"] = at
	}
	m.C.J.MustAppend("hive.released", rec)
	f, up := m.C.Effects.Forge, m.C.WS.LoadConfig().ReleaseRepo
	if p.Repo == up {
		return
	}
	head := strings.SplitN(p.Repo, "/", 2)[0] + ":" + p.Branch
	if _, open, _ := f.FindPR(up, head); open {
		return // the open pull request grows with the branch
	}
	// A pull request on someone else's repository goes out in the person's
	// name, so it waits for their yes.
	m.C.Person.Ask(core.Ask{Kind: "upstream", Ref: head, Project: project,
		Title:  "Offer the Hive's changes to " + up + "?",
		Detail: "The Hive released " + next + " on " + p.Repo + ". Yes opens one pull request on " + up + " in your name, which grows with each kept change."})
}

// refusedSteps is what the person does about a refused release. Its
// pre-release already fired release.yml at the tag's commit, whose sign job
// waits on the release-signing environment's approval: only that approval
// stands between the other commit and the signing key.
const refusedSteps = "Its release run at that commit waits for approval in the release-signing environment: reject it, don't approve. Then delete the release and its tag."

// New is the release module, with the person's answer to an upstream offer.
func New(c *core.Core) *Module {
	m := &Module{C: c}
	c.Person.Handle("upstream", m.onUpstream)
	return m
}

func (m *Module) onUpstream(a core.Ask) {
	if a.Answer != "yes" {
		return
	}
	f, up := m.C.Effects.Forge, m.C.WS.LoadConfig().ReleaseRepo
	pr, err := f.CreatePR(up, a.Ref, f.DefaultBranch(up), "Improvements from "+a.Project,
		"Changes the Hive made to itself, each kept by its tests, review and dry run.")
	if err != nil {
		m.C.J.MustAppend("alert.raised", map[string]string{"kind": "release", "project": a.Project, "text": "offering the change upstream failed: " + err.Error()})
		return
	}
	m.C.J.MustAppend("hive.offered", map[string]string{"project": a.Project, "pr": pr.URL})
}

// hold asks the person to release sha, and answers any older release ask for
// the project, which this one covers, so a stale answer can't release.
func (m *Module) hold(project, sha string, root, unread []string, manifest string) {
	ref := project + "@" + sha
	for _, a := range m.C.Person.Open() {
		if a.Kind == "release" && a.Project == project && a.Ref != ref {
			m.C.Person.Answer(a.ID, "hold", "the Hive, not the person: superseded, a release ask at "+core.Short(sha)+" covers it")
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
		defer m.lock(project)()
		if _, refuse := m.merges(project, sha); refuse != "" {
			m.C.J.MustAppend("release.superseded", map[string]string{"project": project, "sha": sha, "why": refuse})
			return
		}
		m.C.Effects.Approve("release", project, sha)
		m.afterKept(project, sha)
	}
}

type merge struct {
	PR       int
	SHA      string
	MergeSHA string `json:"merge_sha"`
	Root     []string
	Diff     string // the head's own diff (core.DiffID), whose reads carry to it
	// Unread is a merge on its tests and checks alone: the gate found it
	// small and touching no guarded path (core.Change.Unread), or docs only.
	Unread bool
	Lines  int
}

// merges lists the project's merges after the one its last release was cut
// at, up to the one that made upTo. refuse says why there is nothing to
// release: a release already covers upTo, or upTo is no merge the journal saw.
func (m *Module) merges(project, upTo string) (out []merge, refuse string) {
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
		return nil, "a newer release already covers it"
	}
	if at < 0 {
		return nil, "it is not a merge the Hive made"
	}
	return all[from : at+1], ""
}

func (m *Module) sampleOpen(project, sha string) bool {
	return m.SampleOpen != nil && m.SampleOpen(project, sha)
}

// readFix reports whether the newest read of sha (a sampled read after merge)
// asked for fixes.
func (m *Module) readFix(project, sha string) bool {
	v, ok := m.C.Effects.GetVerdict(project, sha)
	return ok && v.Verdict == "fix"
}

// Gates are the workflows that must pass in a release's own runs, on its
// commit, before Publish makes it the latest.
var Gates = []string{"ci", "update"}

// lateAfter is how long a pre-release waits for its gates before it is raised.
const lateAfter = 6 * time.Hour

// publishEvery is how often Tick checks the Hive's pre-releases.
const publishEvery = 10 * time.Minute

// Tick runs Publish every publishEvery, in the background, and follows
// pre-releases when the Hive does.
func (m *Module) Tick() {
	m.follow()
	m.pmu.Lock()
	defer m.pmu.Unlock()
	if m.publishing || time.Since(m.published) < publishEvery {
		return
	}
	m.published, m.publishing = time.Now(), true
	go func() {
		m.Publish()
		m.pmu.Lock()
		m.publishing = false
		m.pmu.Unlock()
	}()
}

// Publish runs from Tick and says what it did. AfterKept cuts each release
// as a pre-release, which installs and auto-update don't follow; once every gate
// passed on its commit, Publish makes it the latest. Of the pre-releases cut
// since the last one published, the newest that passed is published and
// older ones stay pre-releases. A failed gate leaves the release a
// pre-release and raises an alert once; it is still checked, so a re-run
// that passes publishes it, until a newer release is published.
func (m *Module) Publish() string {
	type cand struct {
		Project, Tag, SHA string
		At                time.Time
	}
	var cands []cand
	failed := map[string]bool{}
	m.C.J.Replay(func(e core.Event) {
		var x struct {
			Project, Tag, SHA string
			Prerelease        bool
		}
		if e.Decode(&x) != nil {
			return
		}
		switch e.Kind {
		case "hive.released":
			if x.Prerelease { // one cut before releases waited for their gates is published already
				cands = append(cands, cand{x.Project, x.Tag, x.SHA, e.At})
			}
		case "hive.published":
			cands = slices.DeleteFunc(cands, func(c cand) bool { return c.Project == x.Project && !modules.Newer(x.Tag, c.Tag) })
		case "release.failed":
			failed[x.Project+"@"+x.Tag] = true
		case "release.refused":
			cands = slices.DeleteFunc(cands, func(c cand) bool { return c.Project == x.Project && c.Tag == x.Tag })
		}
	})
	if m.said == nil {
		m.said = map[string]bool{}
	}
	alert := func(c cand, text string) {
		m.C.J.MustAppend("alert.raised", map[string]string{"kind": "release", "project": c.Project, "ref": "release:" + c.Tag, "text": text})
	}
	var out []string
	done, checked := map[string]bool{}, map[string]int{}
	for i := len(cands) - 1; i >= 0; i-- {
		c, key := cands[i], cands[i].Project+"@"+cands[i].Tag
		p, ok := m.C.Projects.Get(c.Project)
		if !ok || done[c.Project] || checked[c.Project] >= 5 {
			continue // an older one stays a pre-release
		}
		checked[c.Project]++
		state, why := m.gates(p.Repo, c.SHA)
		switch state {
		case "passed":
			// The gates passed at c.SHA; the tag must still point there.
			at, err := m.C.Effects.Forge.TagCommit(p.Repo, c.Tag)
			if err != nil {
				out = append(out, c.Tag+": can't read its tag's commit ("+core.Clip(err.Error(), 200)+")")
				continue
			}
			if at != c.SHA {
				m.C.J.MustAppend("release.refused", map[string]string{"project": c.Project, "tag": c.Tag, "sha": c.SHA, "tag_sha": at})
				alert(c, c.Tag+" points at "+core.Short(at)+", not the gated "+core.Short(c.SHA)+"; it stays a pre-release and is never published. "+refusedSteps)
				out = append(out, c.Tag+": refused, its tag points at "+core.Short(at))
				continue
			}
			if err := m.C.Effects.Forge.PublishRelease(p.Repo, c.Tag); err != nil {
				if !m.said[key] {
					m.said[key] = true
					alert(c, "publishing "+c.Tag+" failed: "+err.Error())
				}
				out = append(out, c.Tag+": "+err.Error())
				continue
			}
			m.C.J.MustAppend("hive.published", map[string]string{"project": c.Project, "tag": c.Tag, "sha": c.SHA})
			done[c.Project] = true
			out = append(out, "published "+c.Tag)
		case "failed":
			if !failed[key] {
				m.C.J.MustAppend("release.failed", map[string]string{"project": c.Project, "tag": c.Tag, "sha": c.SHA, "why": why})
				alert(c, c.Tag+" stays a pre-release, so installs stay where they are: "+why+". A re-run that passes publishes it.")
			}
			out = append(out, c.Tag+": "+why)
		default:
			if time.Since(c.At) > lateAfter && !m.said[key] {
				m.said[key] = true
				alert(c, c.Tag+" is still a pre-release after "+lateAfter.String()+": "+why)
			}
			out = append(out, c.Tag+": "+why)
		}
	}
	if len(out) == 0 {
		return "nothing to publish"
	}
	return strings.Join(out, "; ")
}

// gates says "passed" when every gate passed in the release runs at sha,
// else "failed" or "waiting", and why.
func (m *Module) gates(repo, sha string) (state, why string) {
	runs, err := m.C.Effects.Forge.ReleaseRuns(repo, sha)
	if err != nil {
		return "waiting", "can't read its runs (" + core.Clip(err.Error(), 200) + ")"
	}
	var bad, wait []string
	for _, g := range Gates {
		i := slices.IndexFunc(runs, func(r core.WorkflowRun) bool { return r.Name == g })
		switch {
		case i < 0:
			wait = append(wait, g+" (no run yet)")
		case runs[i].Status != "completed":
			wait = append(wait, g)
		case runs[i].Conclusion != "success":
			bad = append(bad, g+" "+runs[i].Conclusion+" ("+runs[i].URL+")")
		}
	}
	switch {
	case len(bad) > 0:
		return "failed", strings.Join(bad, ", ")
	case len(wait) > 0:
		return "waiting", "waiting for " + strings.Join(wait, ", ")
	}
	return "passed", ""
}

// nextTag is one past the highest vX.Y.Z among tags (v0.0.1 if none is).
func nextTag(tags []string) string {
	last := "v0.0.0"
	for _, t := range tags {
		if strings.HasPrefix(t, "v") && modules.Newer(last, t) {
			last = t
		}
	}
	return bump(last)
}

func bump(tag string) string {
	var a, b, c int
	fmt.Sscanf(strings.TrimPrefix(tag, "v"), "%d.%d.%d", &a, &b, &c)
	return fmt.Sprintf("v%d.%d.%d", a, b, c+1)
}
