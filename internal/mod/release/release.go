// Package release is the Hive on itself: installs follow the Hive's releases,
// and when the Hive's own project merges a change that keeps its tests and a
// dry run green, it cuts the next release.
package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
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
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/report"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/scan"
	"github.com/ihsan-sa/hive-standalone-build/internal/setup"
)

// Module is the release module.
type Module struct {
	C *core.Core
	// Restart is called after a new binary is in place (the daemon re-execs).
	Restart func()
	locks   sync.Map // project -> *sync.Mutex held while a release is decided
	// refused is each project's last "sha: why" that stopped a release
	// before main's checks were read, raised once so a retry doesn't repeat it.
	refused sync.Map
	// scanSaid is each project's last "sha: why" a scan blocked, alerted once.
	scanSaid sync.Map
	// handlesAt is the newest commit each project's missing handle list
	// holds, released again when the person says the list is written.
	handlesAt sync.Map
	// PauseMerges stops new merges and waits up to the given time for the
	// running ones to end (the work module), so an update never restarts
	// mid-merge; ok false says one still runs, and resume lets merges go on.
	PauseMerges func(wait time.Duration) (resume func(), ok bool)
	// Gates says how many recursive gates run and since when the oldest
	// has, and HoldGates keeps new ones from starting until a time (the
	// work module): an automatic update waits for the running ones, since
	// the restart ends them and up to two hours of their work.
	Gates     func() (running int, oldest time.Time)
	HoldGates func(until time.Time)
	// gatesSince is when the update first waited for running gates.
	gatesSince time.Time
	// Canary runs the train's canary (train.go): a child Hive on the
	// candidate against one on this release, on real work; nil can't run one,
	// so a Hive on the train stays where it is.
	Canary func(ctx context.Context, j CanaryJob) core.GateResult
	// Scan checks what a release at sha publishes before it is cut; nil is
	// the identity and secret scan of the files on the commit's public list.
	Scan func(p core.Project, sha string) error
	// FileFix files a fix brief on a project (title, the failure it fixes,
	// done, known), or leaves it to an open one of the same title, adding
	// known when it is news (the work module, wired in app); nil files none.
	FileFix func(project, title, why, done, known string)
	// Diff lists the files that differ between two commits; nil is git diff
	// in the project's clone.
	Diff func(p core.Project, from, to string) ([]string, error)
	// ReadMerged gives a merge that lacks the read a release needs (its root
	// paths with no security read, or no read at all) that read from the
	// Hive's own seats: the work module's ReadMerged. passed says it has it
	// now; why says only the person can settle it; neither, still reading.
	// nil leaves such a merge to the person.
	ReadMerged func(p core.Project, pr int, head, mergeSHA, diff string, root []string) (passed bool, why string)
	// Contains says whether commit tagSHA has sha in its history; nil is
	// git merge-base in the project's clone.
	Contains func(p core.Project, sha, tagSHA string) (bool, error)
	// Line reads the release line (VERSION, "1.0") at sha; nil is git show
	// in the project's clone.
	Line func(p core.Project, sha string) (string, error)
	// announced is the newest release a pinned Hive has said is out.
	announced string
	// liftLooked is set once AutoUpdate has looked for a pin to lift.
	liftLooked bool
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
	// canarying is the pre-release the train's canary is running on, or "".
	canarying string
	red       map[string]verdict
	waits     []string
	// shas are the pre-releases' commits, by repo@tag; tried is the tag the
	// last update went for and said1 what follow last recorded.
	shas  map[string]string
	tried string
	said1 string
	// ahead are the releases newer than this one the last look read: every
	// one following pre-releases, the full ones otherwise; newest and behind
	// are what Lag reports, from the last Check.
	ahead  []string
	newest string
	behind int
	// skipped counts the looks in a row an update passed over because a
	// merge ran past mergeWait.
	skipped int
	// updating is held while an update runs: AutoUpdate, from its schedule
	// or follow, or `hive update`.
	updating sync.Mutex
}

// Site serves the releases: GitHub over plain HTTPS, so updating needs no
// login and follows the redirect from a renamed repo. Tests point it elsewhere.
var Site = "https://github.com"

// API serves each release's notes to a forge with no login to read them on
// (GitHub reads them through gh). Tests point it elsewhere.
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
// They are read on the forge's login when it has one, never on the anonymous
// 60 an hour this machine's agents share.
func Notes(f core.Forge, repo, from, to string) string {
	var rels []struct {
		Tag  string `json:"tag_name"`
		Body string
		URL  string `json:"html_url"`
	}
	path := "repos/" + repo + "/releases?per_page=100"
	var err error
	if r, ok := f.(core.APIReader); ok {
		var out string
		if out, err = r.APIGet(repo, path); err == nil {
			err = json.Unmarshal([]byte(out), &rels)
		}
	} else {
		var resp *http.Response
		resp, err = (&http.Client{Timeout: 30 * time.Second}).Get(API + "/" + path)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				err = errors.New(resp.Status)
			} else {
				err = json.NewDecoder(resp.Body).Decode(&rels)
			}
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
// newest of release_repo's whose checks for this system passed, when newer;
// when GitHub's rate limit stopped that look and nothing newer is in hand,
// the error says so and when it looks again.
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
	m.ahead = nil
	m.pmu.Unlock()
	var stopped *limited // a look the rate limit stopped
	if ch := m.Channel(); ch == Prerelease || ch == Train {
		after := core.Version
		if ch == Prerelease && modules.Newer(after, tag) {
			after = tag
		}
		t, waits, e := m.prerelease(up, after)
		errors.As(e, &stopped)
		if e != nil {
			waits = append(waits, e.Error())
		}
		if ch == Train {
			// Only what a canary kept moves the Hive, the latest release
			// included.
			var w []string
			t, w = m.train(up, t)
			waits = append(w, waits...)
			if t == "" {
				repo, tag = "", ""
			}
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
		// Releases read through the login count every one between, so the
		// lag means the same on both channels; without it, the latest alone.
		var ahead []string
		if out != "" && modules.Newer(core.Version, out) {
			if rels, e := m.C.Effects.Forge.Releases(up); e == nil {
				for _, r := range rels {
					if !r.Prerelease && modules.Newer(core.Version, r.Tag) {
						ahead = append(ahead, r.Tag)
					}
				}
			}
		}
		m.pmu.Lock()
		m.waits, m.ahead = nil, ahead
		m.pmu.Unlock()
	}
	if tag != "" {
		err = nil
	}
	if stopped != nil && (tag == "" || !modules.Newer(core.Version, tag)) {
		err = stopped // not "nothing newer": the look didn't finish
	}
	m.lag(out, tag)
	return repo, tag, tag != "" && modules.Newer(core.Version, tag), err
}

// lag records the newest release out and how many releases this one is
// behind it: every newer one the last look read, the latest release and the
// one Check took.
func (m *Module) lag(out ...string) {
	m.pmu.Lock()
	defer m.pmu.Unlock()
	seen := map[string]bool{}
	m.newest, m.behind = core.Version, 0
	for _, t := range append(slices.Clone(m.ahead), out...) {
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
// when it is a step back (see onward), or with "" or "latest" the newest that
// Check finds. Anything but a pinned step back clears any pin. Live runs are
// separate processes and the daemon reattaches to them after it restarts, so
// live work carries across.
func (m *Module) Update(tag string) (string, error) {
	if !m.updating.TryLock() {
		return busy, nil
	}
	defer m.updating.Unlock()
	return m.update(tag, false)
}

// busy is what an update says while another is being checked.
const busy = "an update is already being checked; try again in a minute"

// update installs tag, or the newest release when tag is "" or latest; on its
// own (auto), it never crosses into a new major version past 1.0, and waits
// for the recursive gates running (waitGates).
func (m *Module) update(tag string, auto bool) (string, error) {
	m.pmu.Lock()
	m.tried = tag
	m.pmu.Unlock()
	if !strings.HasPrefix(core.Version, "v") {
		return "this is a source build (" + core.Version + "); it doesn't update itself", nil
	}
	cfg := m.C.WS.LoadConfig()
	repo, chosen := cfg.ReleaseRepo, tag != "" && tag != "latest"
	pin := chosen && !m.onward(cfg, tag)
	if !chosen {
		var err error
		if repo, tag, _, err = m.Check(); err != nil {
			return "", err
		}
	}
	if !chosen && !modules.Newer(core.Version, tag) || chosen && tag == core.Version {
		msg := "already on " + core.Version
		if pin {
			msg += ", pinned there"
		}
		return msg, m.pin(cfg, tag, pin)
	}
	if auto && Breaking(core.Version, tag) {
		return tag + " is a new major version, which by the Hive's versioning would break this setup; read its notes, then `" + core.Cmd + " update " + tag + "` installs it", nil
	}
	m.pmu.Lock()
	m.tried = tag
	m.pmu.Unlock()
	if !chosen && m.rolledBack(tag) {
		return tag + " was rolled back after it failed to start here; `" + core.Cmd + " update " + tag + "` tries it again", nil
	}
	if msg := m.waitGates(tag, auto); msg != "" {
		return msg, nil
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
	// tasks still in merging carry across and go on after it. A merge that
	// runs past the wait look after look is hung, not slow: after maxSkips
	// the update goes ahead, and the task in merging finds after the restart
	// whether its merge landed.
	resume := func() {}
	if m.PauseMerges != nil {
		var ok bool
		if resume, ok = m.PauseMerges(mergeWait); !ok {
			m.skipped++
			text := fmt.Sprintf("a merge has run past %s at %d looks in a row", mergeWait, m.skipped)
			if m.skipped < maxSkips {
				resume()
				m.C.J.MustAppend("hive.update_held", map[string]any{"to": tag, "looks": m.skipped, "text": text})
				return "waiting: " + text + "; " + tag + " installs at the next look", nil
			}
			m.C.J.MustAppend("hive.update_held", map[string]any{"to": tag, "looks": m.skipped, "forced": true, "text": text + "; installing anyway"})
		}
	}
	m.skipped = 0
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
	notes := Notes(m.C.Effects.Forge, repo, core.Version, tag)
	unsigned := PublicKey == ""
	m.C.J.MustAppend("hive.updated", map[string]any{"from": core.Version, "to": tag, "notes": notes, "unsigned": unsigned, "pinned": pin})
	installed = true
	// Merges stay paused until the restart; one that never comes (a
	// supervisor that doesn't restart, a failed start) lets them go on.
	time.AfterFunc(restartGrace, func() {
		m.C.J.MustAppend("hive.update_unrestarted", map[string]any{"to": tag, "text": fmt.Sprintf("the restart into %s didn't come within %s; merges go on", tag, restartGrace)})
		resume()
	})
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

// onward reports whether a tag chosen with --release moves the Hive forward:
// no older than this one, nor than what auto-update would take now on the
// Hive's channel (on pre-releases, the newest safe here, else the latest
// release). Such a tag is installed with auto-update left as it is, so the
// Hive goes on following; anything older is a step back and pins, also one
// forward from an earlier step back that is still behind what the channel
// would install. When the look fails, only an unpinned Hive's tag no older
// than the latest release counts as onward, so a step back stays pinned.
func (m *Module) onward(cfg core.Config, tag string) bool {
	if modules.Newer(tag, core.Version) {
		return false
	}
	if _, want, _, err := m.Check(); err == nil && want != "" {
		return !modules.Newer(tag, want)
	}
	newest, err := latest(cfg.ReleaseRepo)
	return cfg.ReleasePin == "" && err == nil && !modules.Newer(tag, newest)
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

// maxSkips is how many looks in a row an update waits on a merge running
// past mergeWait before it installs anyway.
const maxSkips = 3

// gateWaitMax is the most an automatic update waits for the recursive gates
// running when it first looked: a gate's own limit and its grace, so a run
// that started just before ends, and a hung one doesn't keep the Hive behind.
var gateWaitMax = 2*time.Hour + 15*time.Minute

// gateHold is how long each look that waits for gates keeps new ones from
// starting; the next look renews it, so an update gone from the channel
// holds no gate back for long.
const gateHold = 15 * time.Minute

// waitGates says why an automatic update to tag waits, or "": while
// recursive gates run, it waits for them to end and starts no new ones,
// for at most gateWaitMax from its first look; then it installs anyway. A
// restart ends every gate in flight, and before this one each pre-release
// threw away whatever the gates running had done. `hive update` doesn't wait.
func (m *Module) waitGates(tag string, auto bool) string {
	n := 0
	var oldest time.Time
	if auto && m.Gates != nil {
		n, oldest = m.Gates()
	}
	if n == 0 {
		m.gatesSince = time.Time{}
		return ""
	}
	now := time.Now().UTC()
	first := m.gatesSince.IsZero()
	if first {
		m.gatesSince = now
	}
	by := m.gatesSince.Add(gateWaitMax)
	text := fmt.Sprintf("%d recursive gate(s) running, the oldest since %s", n, oldest.UTC().Format("15:04Z"))
	if !now.Before(by) {
		m.gatesSince = time.Time{}
		m.C.J.MustAppend("hive.update_held", map[string]any{"to": tag, "gates": n, "forced": true, "text": text + fmt.Sprintf("; waited %s, installing anyway", gateWaitMax)})
		return ""
	}
	if m.HoldGates != nil {
		m.HoldGates(now.Add(gateHold))
	}
	if first {
		m.C.J.MustAppend("hive.update_held", map[string]any{"to": tag, "gates": n, "text": text + "; no new gate starts until it installs"})
	}
	return "waiting: " + text + "; " + tag + " installs when they end, or at " + by.Format("15:04Z") + " at the latest"
}

// restartGrace is how long merges stay paused after an update installs,
// waiting for its restart.
var restartGrace = 30 * time.Minute

// liftForwardPin lifts a pin that an update forward left, from before such an
// update stopped pinning (issue #1795), when auto-update was on before it: the
// update that brought this release in, as the journal has it, came from an
// older one, and it was an update from before this build, which says in its
// event whether it pinned: a pin this build made is a step back, even one
// forward from an earlier step back, and stays. It runs once a daemon, from
// the scheduled job at start.
func (m *Module) liftForwardPin(cfg core.Config) (bool, error) {
	if cfg.ReleasePin == "" || !cfg.PinnedAuto || cfg.ReleasePin != core.Version {
		return false, nil
	}
	from := ""
	m.C.J.Replay(func(e core.Event) {
		var x struct {
			From, To string
			Pinned   *bool
		}
		if e.Kind == "hive.updated" && e.Decode(&x) == nil && x.To == cfg.ReleasePin {
			from = x.From
			if x.Pinned != nil {
				from = "" // this build's own update: a pin it made stays
			}
		}
	})
	if from == "" || !modules.Newer(from, cfg.ReleasePin) {
		return false, nil
	}
	if err := m.pin(cfg, "", false); err != nil {
		return false, err
	}
	m.C.J.MustAppend("release.unpinned", map[string]string{"pin": cfg.ReleasePin, "text": "the pin on " + cfg.ReleasePin + " was an update forward from " + from + ", not a step back; auto-update is on again"})
	return true, nil
}

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
	if !m.liftLooked {
		m.liftLooked = true
		if lifted, err := m.liftForwardPin(cfg); err != nil {
			return "", err
		} else if lifted {
			cfg = m.C.WS.LoadConfig()
		}
	}
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
	msg, err := m.update("", true)
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
	Sweep(OnDisk(exe))
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

// OnDisk is the path a binary an update renamed aside (to <name>.old, or
// <name>.old-<n> by makeWay) came from: Linux reports a running file by its
// new name.
func OnDisk(exe string) string {
	if i := strings.LastIndex(exe, ".old"); i >= 0 && strings.Trim(strings.TrimPrefix(exe[i+4:], "-"), "0123456789") == "" {
		return exe[:i]
	}
	return exe
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
	if err := makeWay(old); err != nil {
		os.Remove(next)
		return err
	}
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

// removeFile is os.Remove; a test fails it as Windows does for a running image.
var removeFile = os.Remove

// makeWay clears path for a rename onto it: it removes what is there or, when
// that fails (Windows can rename a running image but not delete it, as the
// daemon an earlier update replaced, still running from its .old), moves it
// aside to <path>-<n>, which Sweep removes on a later start.
func makeWay(path string) error {
	if removeFile(path) == nil || !core.Exists(path) {
		return nil
	}
	for n := 1; n < 1000; n++ {
		aside := fmt.Sprintf("%s-%d", path, n)
		if !core.Exists(aside) {
			return os.Rename(path, aside)
		}
	}
	return errors.New(path + " can't be removed or moved aside")
}

// Sweep removes what makeWay moved aside beside exe and the binaries an
// update installs with it, once nothing runs from them; one still running
// stays for the next start.
func Sweep(exe string) {
	dir := filepath.Dir(exe)
	for _, name := range append([]string{filepath.Base(exe)}, alongside[runtime.GOOS]...) {
		for _, kind := range []string{".old-", ".failed-"} {
			left, _ := filepath.Glob(filepath.Join(dir, name+kind+"*"))
			for _, f := range left {
				if n := strings.TrimPrefix(f, filepath.Join(dir, name+kind)); n != "" && strings.Trim(n, "0123456789") == "" {
					removeFile(f)
				}
			}
		}
	}
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
// change to a root path (what runs with elevated rights) goes out once the
// Hive's own security reads passed its diff (rootRead), and the person is told
// after. A merge missing either read gets it now from the Hive's own seats
// (ReadMerged) while the release waits (release.reading); it waits for the
// person's word only when that read can't pass: a seat asked for fixes, or its
// diff can't be read. While the newest release's release run failed, a merge
// that changes nothing the failed job reads waits too (unbuilt).
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
	// A refusal here comes again on each retry of a commit that waits on
	// main's checks (retryWaiting), so each is raised once per commit.
	once := func(why string) {
		text := "no release at " + core.Short(sha) + ": " + why
		if last, _ := m.refused.Swap(project, sha+": "+why); last != sha+": "+why {
			alert(text)
		}
	}
	if why := m.C.Effects.Stewardship(p, sha); why != "" {
		once(why)
		return
	}
	list, refuse := m.merges(project, sha)
	if refuse == notMerged {
		// A later merge of the Hive's carries this kept commit: Tick tries
		// the newest one (retryUnmerged) rather than waiting on the next
		// measurement.
		if last, _ := m.unmerged(project); last != sha {
			m.C.J.MustAppend("release.unmerged", map[string]string{"project": project, "sha": sha})
		}
	}
	if refuse != "" {
		once(refuse)
		return
	}
	approved := m.C.Effects.Approved("release", project, sha)
	// A merge without the read a release needs gets it from the Hive's own
	// seats (ReadMerged) while the release waits; only a read that can't
	// pass leaves it to the person.
	selfRead := m.ReadMerged
	if approved {
		selfRead = nil // the person's word already stands for them
	}
	var manifest strings.Builder
	var root, unread, rootRead, reading []string
	for _, mg := range list {
		reads, why := m.C.Effects.Reads(project, mg.SHA, mg.Diff), "no seat reads it"
		if len(reads) == 0 && mg.Diff != "" && selfRead != nil {
			var passed bool
			if passed, why = selfRead(p, mg.PR, mg.SHA, mg.MergeSHA, mg.Diff, nil); passed {
				reads = m.C.Effects.Reads(project, mg.SHA, mg.Diff)
			} else if why != "" {
				log.Printf("release: %s: #%d stays unread: %s", project, mg.PR, why)
			}
		}
		switch {
		case len(reads) > 0:
			fmt.Fprintf(&manifest, "- #%d at %s, read by %s\n", mg.PR, core.Short(mg.SHA), strings.Join(reads, ", "))
		case why == "":
			reading = append(reading, fmt.Sprintf("#%d's read", mg.PR))
			fmt.Fprintf(&manifest, "- #%d at %s, being read by the Hive's reviewer\n", mg.PR, core.Short(mg.SHA))
		default:
			unread = append(unread, fmt.Sprintf("#%d at %s", mg.PR, core.Short(mg.SHA)))
			fmt.Fprintf(&manifest, "- #%d at %s, not read\n", mg.PR, core.Short(mg.SHA))
		}
		if len(mg.Root) == 0 {
			continue
		}
		by := m.rootRead(project, mg)
		why = "no security read passed them"
		if by == "" && mg.Diff != "" && selfRead != nil {
			var passed bool
			if passed, why = selfRead(p, mg.PR, mg.SHA, mg.MergeSHA, mg.Diff, mg.Root); passed {
				by = m.rootRead(project, mg)
			}
		}
		switch {
		case by != "":
			fmt.Fprintf(&manifest, "  its root paths (%s) passed %s\n", strings.Join(mg.Root, ", "), by)
			rootRead = append(rootRead, fmt.Sprintf("#%d (%s)", mg.PR, strings.Join(mg.Root, ", ")))
		case mg.Diff == "":
			// Merged before effect.merged carried the diff: no read can be
			// matched to it.
			root = append(root, fmt.Sprintf("%s (#%d's merge record names no diff)", strings.Join(mg.Root, ", "), mg.PR))
		case why == "":
			reading = append(reading, fmt.Sprintf("#%d's security read (%s)", mg.PR, strings.Join(mg.Root, ", ")))
			fmt.Fprintf(&manifest, "  its root paths (%s) are being read by the Hive's security seat and panel\n", strings.Join(mg.Root, ", "))
		case selfRead == nil:
			root = append(root, mg.Root...)
		default:
			root = append(root, fmt.Sprintf("%s (#%d: %s)", strings.Join(mg.Root, ", "), mg.PR, why))
		}
	}
	if !approved && !m.mainGreen(p, sha) {
		return
	}
	// The tags are read only once main's checks let the cut go ahead, so a
	// commit retried while it waits on them (retryWaiting) costs no forge
	// calls or fetches for them while the lock is held.
	if refuse := m.tagged(p, sha); refuse != "" {
		once(refuse)
		return
	}
	m.refused.Delete(project)
	// The next tag is one past the highest anywhere (the release marked
	// latest may be older than pre-releases, and their tags are taken), or
	// the first of a higher line VERSION names.
	tags, err := m.C.Effects.Forge.Tags(p.Repo)
	if err != nil {
		alert("no release at " + core.Short(sha) + ": can't list the tags: " + err.Error())
		return
	}
	tags = append(tags, core.Version)
	next := nextTag(tags, m.line(p, sha))
	var why []string
	if major(next) > major(nextTag(tags, "")) {
		why = append(why, "it starts "+next+", a new major version, which is the person's call ("+lineWhy+")")
	}
	if len(root) > 0 {
		why = append(why, strings.Join(root, ", ")+" run with elevated rights and no security read passed them")
	}
	if len(unread) > 0 {
		why = append(why, strings.Join(unread, ", ")+" merged without a read of its exact commit")
	}
	if !approved {
		if held, tag, first := m.unbuilt(p, sha); held != "" {
			// Raised once per held tag, with its release.held, and keyed by
			// it: every kept merge while the hold lasts would say the same.
			if first {
				m.C.J.MustAppend("alert.raised", map[string]string{"kind": "release", "project": project, "ref": "release:held:" + tag,
					"text": "no release at " + core.Short(sha) + ": " + held})
			}
			why = append(why, held)
		}
	}
	readAt := m.reading(project)
	if len(reading) > 0 && len(why) == 0 && !approved {
		// Tick tries it again (retryReading) until the reads end.
		if readAt != sha {
			m.C.J.MustAppend("release.reading", map[string]any{"project": project, "sha": sha, "reads": reading})
		}
		return
	}
	if readAt != "" {
		m.C.J.MustAppend("release.read_ended", map[string]string{"project": project, "sha": readAt})
	}
	if len(why) > 0 && !approved {
		if len(reading) > 0 {
			why = append(why, "the Hive's own reads of "+strings.Join(reading, ", ")+" are still running")
		}
		m.hold(project, sha, why, manifest.String())
		return
	}
	scanAt := m.Scan
	if scanAt == nil {
		scanAt = m.scan
	}
	if err := scanAt(p, sha); err != nil {
		if errors.Is(err, scan.ErrNoHandles) {
			m.askHandles(project, sha, err)
			return
		}
		// A retry meets the same hits at the same commit: said once.
		if last, _ := m.scanSaid.Swap(project, sha+": "+err.Error()); last != sha+": "+err.Error() {
			alert("no release at " + core.Short(sha) + ", since its scan blocks it: " + err.Error())
			m.fileScanFix(p, sha, err)
		}
		return
	}
	m.scanSaid.Delete(project)
	m.handlesFound(project)
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
	body := "Released by the Hive after " + core.Short(sha) + " kept its tests, main's checks and dry run green. It is a pre-release until " +
		strings.Join(Gates, " and ") + " pass on that commit; then it is the latest, which installs follow.\n\nWhat was read:\n\n" + manifest.String()
	if approved {
		body += "\nThe person released it.\n"
	} else if len(rootRead) > 0 {
		body += "\nIts root paths went out on the Hive's own security reads; the person is told after.\n"
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
	// A release ask at one of the merges this release covers has nothing
	// left to answer.
	for _, a := range m.C.Person.Open() {
		_, at, _ := strings.Cut(a.Ref, "@")
		if a.Kind == "release" && a.Project == project && slices.ContainsFunc(list, func(mg merge) bool { return mg.MergeSHA == at }) {
			m.C.Person.Answer(a.ID, "hold", "the Hive, not the person: "+next+" released it")
		}
	}
	if len(rootRead) > 0 && !approved {
		m.C.Person.Tell(core.Ask{Ref: "release:" + project + "@" + next, Project: project,
			Title:  "Released " + next + " with changes to root paths: " + strings.Join(rootRead, ", "),
			Detail: "They run with elevated rights. The Hive released them itself once its security seat and panel (or the box's outside read) said land on each one's own diff.\n\n" + manifest.String()})
	}
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

// mainGreen says whether main's own checks (CI) passed at sha, so the Hive
// offers or cuts a release only at a commit its CI found green. A failure is
// raised once; one still running, that can't start or can't be read is
// journaled as release.ci_waiting once. Either way Tick tries the newest such
// commit again (retryWaiting), so a re-run that passes releases it, as does
// the next kept merge whose checks pass. A release needs the whole ci
// workflow green: core.Advisory's checks, set aside for merges, count here.
func (m *Module) mainGreen(p core.Project, sha string) bool {
	state, which := core.CheckPending, ""
	if runs, err := m.C.Effects.Forge.Checks(p.Repo, sha); err != nil {
		which = "can't read its checks (" + core.Clip(report.Scrub(err.Error(), ""), 200) + ")"
	} else {
		var bad []core.CheckRun
		state, bad = core.ChecksState(runs, m.C.Effects.RequiredChecks(p, sha))
		which = core.CheckNames(bad)
	}
	last := m.ciState(p.Name)
	switch state {
	case core.CheckPass:
		if last.SHA != sha || last.Kind != "release.ci_passed" {
			m.C.J.MustAppend("release.ci_passed", map[string]string{"project": p.Name, "sha": sha})
		}
		return true
	case core.CheckFail:
		if last.SHA != sha || last.Kind != "release.ci_failed" {
			m.C.J.MustAppend("release.ci_failed", map[string]string{"project": p.Name, "sha": sha, "checks": which})
			m.C.J.MustAppend("alert.raised", map[string]string{"kind": "release", "project": p.Name,
				"text": "no release at " + core.Short(sha) + ": its checks failed on main (" + which + "); it is released if a re-run of them passes, or the next kept merge whose checks pass is"})
		}
	default:
		if last.SHA != sha || last.Kind != "release.ci_waiting" {
			m.C.J.MustAppend("release.ci_waiting", map[string]string{"project": p.Name, "sha": sha, "state": state, "checks": which})
		}
	}
	return false
}

// ciWait is the newest of a project's release.ci_* events.
type ciWait struct {
	Kind, SHA string
	At        time.Time
}

// ciState is the project's newest release.ci_* event, its Kind empty when a
// release was cut since (nothing waits then).
func (m *Module) ciState(project string) (last ciWait) {
	m.C.J.Replay(func(e core.Event) {
		var x struct{ Project, SHA string }
		if !strings.HasPrefix(e.Kind, "release.ci_") && e.Kind != "hive.released" || e.Decode(&x) != nil || x.Project != project {
			return
		}
		if e.Kind == "hive.released" {
			last = ciWait{}
			return
		}
		if last.SHA != x.SHA || last.Kind != "release.ci_waiting" {
			last.At = e.At // a wait keeps the time it began
		}
		last.Kind, last.SHA = e.Kind, x.SHA
	})
	return last
}

// retryWaiting tries again the newest kept merge of each self project whose
// release waits on main's checks or found them failed (a re-run may pass),
// or the newest kept merge after it whose checks passed (greenAfter); one
// still waiting after lateAfter is raised once. A descendant chosen this way
// that is then held (an unread or root merge) or blocked by its scan still
// journals release.ci_passed, which ends the original's wait: the hold's or
// the scan's alert speaks for it from then on.
// Then it tries a commit refused as no merge the Hive made at the kept
// merge since (retryUnmerged).
func (m *Module) retryWaiting() {
	if m.said == nil {
		m.said = map[string]bool{}
	}
	for _, p := range m.C.Projects.List() {
		if !p.Self {
			continue
		}
		w := m.ciState(p.Name)
		if w.Kind != "release.ci_waiting" && w.Kind != "release.ci_failed" {
			continue
		}
		func() {
			defer m.lock(p.Name)()
			sha := w.SHA
			if next := m.greenAfter(p, w.SHA); next != "" {
				sha = next
			}
			m.afterKept(p.Name, sha)
		}()
		key := p.Name + "@ci:" + w.SHA
		if now := m.ciState(p.Name); now.Kind == "release.ci_waiting" && now.SHA == w.SHA && time.Since(w.At) > lateAfter && !m.said[key] {
			m.said[key] = true
			m.C.J.MustAppend("alert.raised", map[string]string{"kind": "release", "project": p.Name,
				"text": "no release at " + core.Short(w.SHA) + " yet: main's checks there haven't passed after " + lateAfter.String()})
		}
	}
	m.retryUnmerged()
	m.retryReading()
}

// reading is the commit a project's release waits at while the Hive's own
// seats read merges it covers (release.reading), "" once those reads ended or
// a release was cut since.
func (m *Module) reading(project string) (sha string) {
	m.C.J.Replay(func(e core.Event) {
		var x struct{ Project, SHA string }
		if (e.Kind != "release.reading" && e.Kind != "release.read_ended" && e.Kind != "hive.released") || e.Decode(&x) != nil || x.Project != project {
			return
		}
		sha = ""
		if e.Kind == "release.reading" {
			sha = x.SHA
		}
	})
	return sha
}

// retryReading tries again each self project's release that waits on the
// Hive's own reads of the merges it covers.
func (m *Module) retryReading() {
	for _, p := range m.C.Projects.List() {
		if !p.Self {
			continue
		}
		func() {
			defer m.lock(p.Name)()
			if sha := m.reading(p.Name); sha != "" {
				m.afterKept(p.Name, sha)
			}
		}()
	}
}

// unmerged is the kept commit a project's release last refused as no merge
// the Hive made, and the newest kept Hive merge journaled after that refusal
// (the commit itself, once its effect.merged comes late); both are "" once a
// release was cut or main's checks were read for one since.
func (m *Module) unmerged(project string) (sha, next string) {
	var later []string
	kept := map[string]bool{}
	m.C.J.Replay(func(e core.Event) {
		var x struct {
			Project, SHA string
			MergeSHA     string `json:"merge_sha"`
			Kept, Red    bool
			NoResult     bool `json:"no_result"`
		}
		if e.Decode(&x) != nil || x.Project != project {
			return
		}
		switch {
		case e.Kind == "release.unmerged":
			sha, later = x.SHA, nil
		case e.Kind == "hive.released" || strings.HasPrefix(e.Kind, "release.ci_"):
			sha, later = "", nil
		case e.Kind == "score.measured" && !x.NoResult:
			kept[x.SHA] = x.Kept
		case e.Kind == "score.covered":
			kept[x.SHA] = !x.Red
		case e.Kind == "effect.merged" && sha != "" && x.MergeSHA != "":
			later = append(later, x.MergeSHA)
		}
	})
	kept[sha] = sha != "" // AfterKept refused it, so improve kept it
	for i := len(later) - 1; i >= 0; i-- {
		if kept[later[i]] {
			return sha, later[i]
		}
	}
	return sha, ""
}

// retryUnmerged tries again each self project whose kept commit was refused
// as no merge the Hive made (a merge whose forge call failed though it went
// through, say), at the newest kept Hive merge since whose history holds it.
// Each is tried once; main's checks there are then waited on as any kept
// merge's are.
func (m *Module) retryUnmerged() {
	for _, p := range m.C.Projects.List() {
		if !p.Self {
			continue
		}
		sha, next := m.unmerged(p.Name)
		key := p.Name + "@unmerged:" + next
		if next == "" || m.said[key] {
			continue
		}
		m.said[key] = true
		contains := m.Contains
		if contains == nil {
			contains = m.contains
		}
		if next != sha {
			if in, err := contains(p, sha, next); err != nil || !in {
				continue
			}
		}
		func() {
			defer m.lock(p.Name)()
			m.afterKept(p.Name, next)
		}()
	}
}

// greenAfterN is how many of the newest merges after a waiting one
// greenAfter reads the checks of.
const greenAfterN = 10

// greenAfter is the newest of the project's kept merges after sha whose
// checks passed on main and whose history holds sha, or "" when none does. A
// commit whose ci run GitHub cancelled (one queued run replaces another) has
// no checks and would wait forever, and one that failed may be fixed after
// it: a green descendant carries it into a release. Only a merge improve kept
// (its own score.measured, or a later kept one covering it) is chosen, so an
// install following pre-releases never runs a merge measured red or worse,
// or one not judged yet, which its own AfterKept releases once kept. It only
// reads, journaling nothing; afterKept reads main's checks at the chosen
// commit again and journals them.
func (m *Module) greenAfter(p core.Project, sha string) string {
	var later []string
	kept := map[string]bool{}
	m.C.J.Replay(func(e core.Event) {
		var x struct {
			Project  string
			SHA      string
			MergeSHA string `json:"merge_sha"`
			Kept     bool
			Red      bool
			NoResult bool `json:"no_result"`
		}
		if e.Decode(&x) != nil || x.Project != p.Name {
			return
		}
		switch e.Kind {
		case "score.measured":
			if !x.NoResult {
				kept[x.SHA] = x.Kept // the newest measure of a commit stands
			}
		case "score.covered":
			kept[x.SHA] = !x.Red
		case "effect.merged":
			if x.MergeSHA == "" {
				return
			}
			if x.MergeSHA == sha {
				later = []string{} // only the merges after the newest sighting count
				return
			}
			if later != nil {
				later = append(later, x.MergeSHA)
			}
		}
	})
	contains := m.Contains
	if contains == nil {
		contains = m.contains
	}
	for i, n := len(later)-1, 0; i >= 0 && n < greenAfterN; i, n = i-1, n+1 {
		if !kept[later[i]] {
			continue
		}
		runs, err := m.C.Effects.Forge.Checks(p.Repo, later[i])
		if err != nil {
			continue
		}
		if state, _ := core.ChecksState(runs, m.C.Effects.RequiredChecks(p, later[i])); state != core.CheckPass {
			continue
		}
		// The journal's order of merges stands in for main's history; the
		// history itself says so, and one that can't be read is passed over.
		if in, err := contains(p, sha, later[i]); err != nil || !in {
			if err != nil {
				log.Printf("release: %s: can't tell whether %s contains %s: %v", p.Name, core.Short(later[i]), core.Short(sha), err)
			}
			continue
		}
		return later[i]
	}
	return ""
}

// rootRead says who passed a merge's root paths, as Effects.SecurityOK reads
// a change to a root path on the security list: an outside read decides when
// there is one, else the security seat and a majority of the panel said land
// on its own diff. "" when nobody did.
//
// mg.Root is the merge's root-class paths, each of which ChangeOf put in both
// Security and SecurityRoot at merge, listed or not; so SecurityRoot here asks
// for the panel majority on purpose. The seats read the whole own diff, so
// their land passes all of a merge's root paths together. Only root paths
// reach this: a high path's security read isn't checked again at release.
func (m *Module) rootRead(project string, mg merge) string {
	if !m.C.Effects.SecurityOK(project, core.Change{Security: mg.Root, SecurityRoot: mg.Root, DiffID: mg.Diff}) {
		return ""
	}
	if o, ok := m.C.Effects.OutsideRead(project, mg.Diff); ok {
		return "the box's " + o.By + " read"
	}
	return "the security seat and a majority of its panel"
}

// refusedSteps is what the person does about a refused release. Its
// pre-release already fired release.yml at the tag's commit, whose sign job
// waits on the release-signing environment's approval: only that approval
// stands between the other commit and the signing key.
const refusedSteps = "Its release run at that commit waits for approval in the release-signing environment: reject it, don't approve. Then delete the release and its tag."

// askHandles puts the missing handle list to the person as one standing ask
// in Waiting, never an alert per check: only they can write the file. The ask
// keeps its place while it stands; the newest commit it holds is retried when
// they answer done.
func (m *Module) askHandles(project, sha string, err error) {
	m.handlesAt.Store(project, sha)
	m.C.Person.Ask(core.Ask{Kind: "scan_handles", Ref: project, Project: project, Options: []string{"done"},
		Title: "Give the release scan its handle list: no release of " + project + " goes out without it",
		Detail: "The release scan refuses to pass: " + err.Error() + ". Only you can write it: the file is in the Hive's private folder, which no agent reads. " +
			"Until then nothing merged reaches a release (held since " + core.Short(sha) + "). Answer done once it is written and the Hive tries the release again."})
}

// handlesFound answers a standing handle-list ask once a scan reads a list;
// with nothing held any more, its handler has nothing to retry.
func (m *Module) handlesFound(project string) {
	m.handlesAt.Delete(project)
	for _, a := range m.C.Person.Open() {
		if a.Kind == "scan_handles" && a.Project == project {
			m.C.Person.Answer(a.ID, "done", "the Hive, not the person: the release scan read a handle list")
		}
	}
}

// onHandles is the person's word that the handle list is written: the commit
// it held is released again, or the ask comes back if the list still isn't
// there.
func (m *Module) onHandles(a core.Ask) {
	if a.Answer != "done" {
		return
	}
	sha, ok := m.handlesAt.Load(a.Project)
	if !ok {
		return
	}
	defer m.lock(a.Project)()
	m.afterKept(a.Project, sha.(string))
}

// New is the release module, with the person's answer to an upstream offer.
func New(c *core.Core) *Module {
	m := &Module{C: c}
	c.Person.Handle("upstream", m.onUpstream)
	c.Person.Handle("scan_handles", m.onHandles)
	c.Runs.Ops().Register(core.Op{Name: "train", Help: `{"why"} -> runs the release train again at the next look, whatever the day: the newest pre-release safe here rides as a canary against the running release, even one an earlier canary turned back (a canary that failed on its own environment, now fixed); why goes on the record. Refused while a canary runs or when this Hive doesn't ride the train`, Fn: m.opTrain})
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
func (m *Module) hold(project, sha string, why []string, manifest string) {
	ref := project + "@" + sha
	for _, a := range m.C.Person.Open() {
		if a.Kind == "release" && a.Project == project && a.Ref != ref {
			m.C.Person.Answer(a.ID, "hold", "the Hive, not the person: superseded, a release ask at "+core.Short(sha)+" covers it")
		}
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

// notMerged is merges' refusal of a commit no effect.merged names.
const notMerged = "it is not a merge the Hive made"

type merge struct {
	PR       int
	SHA      string
	MergeSHA string `json:"merge_sha"`
	Root     []string
	Diff     string // the head's own diff (core.DiffID), whose reads carry to it
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
		return nil, notMerged
	}
	return all[from : at+1], ""
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
		m.retryWaiting()
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
	failed := map[string]string{} // the gate each release.failed blamed, "" for the update test
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
			var g struct{ Gate string }
			e.Decode(&g)
			failed[x.Project+"@"+x.Tag] = g.Gate
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
		if why := m.C.Effects.Stewardship(p, "refs/remotes/origin/"+p.Branch); why != "" { // the record as main reads it now, which may postdate the tag
			out = append(out, c.Tag+": not published, "+why)
			continue
		}
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
			if _, said := failed[key]; !said {
				m.C.J.MustAppend("release.failed", map[string]string{"project": c.Project, "tag": c.Tag, "sha": c.SHA, "why": why})
				alert(c, c.Tag+" stays a pre-release, so installs stay where they are: "+why+". A re-run that passes publishes it.")
			}
			out = append(out, c.Tag+": "+why)
		case "unbuilt":
			// A release.failed from before the release run was told apart
			// blamed the update test; the corrected alert goes out once too.
			if g, said := failed[key]; !said || g != "release" {
				m.C.J.MustAppend("release.failed", map[string]string{"project": c.Project, "tag": c.Tag, "sha": c.SHA, "why": why, "gate": "release"})
				alert(c, c.Tag+" has no binaries, since its "+why+"; its update test has nothing to install, so an update failure there says nothing about updating. "+unbuiltSteps)
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
// else "unbuilt" (the release workflow itself failed, so no gate has binaries
// to test), "failed" or "waiting", and why.
func (m *Module) gates(repo, sha string) (state, why string) {
	runs, err := m.C.Effects.Forge.ReleaseRuns(repo, sha)
	if err != nil {
		return "waiting", "can't read its runs (" + core.Clip(err.Error(), 200) + ")"
	}
	if why, _ := m.buildFailed(repo, runs); why != "" {
		return "unbuilt", why
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

// unbuiltSteps is what the person does about a release whose release
// workflow failed.
const unbuiltSteps = "The Hive cuts no new release that leaves what failed unchanged, since it would be as empty: " +
	"a failed scan job most often means the repository secrets HIVE_SCAN_HANDLES and HIVE_SCAN_DENY are missing " +
	"(Settings, Secrets and variables, Actions); set them and re-run its release run, then its update run, and once they pass it is published and releases go on. " +
	"A failure in the code (a build or sign error, release.yml itself) is fixed in a new commit: " +
	"the first kept merge that changes what the failed job reads is cut as usual, and the person's \"release\" on the hold's ask cuts one anyway. " +
	"A scan hit its log names in a public file passed the daemon's own scan, so CI's lists differ from the daemon's: " +
	"a fix to that file alone is not cut by itself, so say \"release\" once it lands."

// buildFailed says why the release workflow (release.yml: scan, build, sign)
// failed among a release's runs, naming the jobs that failed, or "" when it
// passed, still runs or hasn't started. jobs are the failed jobs' names, none
// when they can't be read or none failed (a cancelled run).
func (m *Module) buildFailed(repo string, runs []core.WorkflowRun) (why string, jobs []string) {
	i := slices.IndexFunc(runs, func(r core.WorkflowRun) bool { return r.Name == "release" })
	if i < 0 || runs[i].Status != "completed" || runs[i].Conclusion == "success" {
		return "", nil
	}
	r, at := runs[i], ""
	if all, err := m.C.Effects.Forge.RunJobs(repo, r.ID); err == nil {
		for _, j := range all {
			if j.Status == "completed" && j.Conclusion != "success" && j.Conclusion != "skipped" {
				jobs = append(jobs, j.Name)
			}
		}
		if len(jobs) > 0 {
			at = " at its " + strings.Join(jobs, ", ") + " job"
		}
	}
	return "release run " + r.Conclusion + at + " (" + r.URL + ")", jobs
}

// reads says whether a release.yml job reads path. The scan reads every
// public file, but the daemon's own scan passed at the failed commit before
// it was cut, so a scan failure in CI is the lists or the scan itself, not
// the files; build and sign read the Go sources. Any other job, or none
// named, reads release.yml and the Go sources.
func reads(jobs []string, path string) bool {
	if path == ".github/workflows/release.yml" {
		return true
	}
	scanPath := path == scan.PublicList || strings.HasPrefix(path, "cmd/hive-scan/") || strings.HasPrefix(path, "internal/mod/scan/")
	build := path == "go.mod" || path == "go.sum" || strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go")
	if len(jobs) == 0 {
		return scanPath || build
	}
	for _, j := range jobs {
		if j == "scan" && scanPath || j != "scan" && build {
			return true
		}
	}
	return false
}

// unbuilt says why no release is cut at sha while the newest one's release
// workflow failed: a new tag that leaves what failed unchanged would only be
// another release with no binaries. A commit that changes a file the failed
// job reads is cut as usual, so a fix in the code is released; so is any
// commit once that run passes on a re-run, or the person says "release". It
// notes the hold once per tag in the journal, first saying it did; a release
// run or diff that can't be read never holds the cut.
func (m *Module) unbuilt(p core.Project, sha string) (why, tag string, first bool) {
	var at string
	held := map[string]bool{}
	m.C.J.Replay(func(e core.Event) {
		var x struct{ Project, Tag, SHA string }
		if e.Decode(&x) != nil || x.Project != p.Name {
			return
		}
		switch e.Kind {
		case "hive.released":
			tag, at = x.Tag, x.SHA
		case "release.held":
			held[x.Tag] = true
		}
	})
	if tag == "" || at == sha {
		return "", "", false
	}
	runs, err := m.C.Effects.Forge.ReleaseRuns(p.Repo, at)
	if err != nil {
		return "", "", false
	}
	why, jobs := m.buildFailed(p.Repo, runs)
	if why == "" {
		return "", "", false
	}
	diff := m.Diff
	if diff == nil {
		diff = m.diff
	}
	changed, err := diff(p, at, sha)
	if err != nil || slices.ContainsFunc(changed, func(f string) bool { return reads(jobs, f) }) {
		return "", "", false
	}
	if !held[tag] {
		m.C.J.MustAppend("release.held", map[string]string{"project": p.Name, "tag": tag, "sha": at, "why": why})
	}
	return tag + " has no binaries, since its " + why + ", and " + core.Short(sha) + " changes nothing that job reads. " + unbuiltSteps, tag, !held[tag]
}

// tagChecks is how many of the highest version tags tagged reads.
const tagChecks = 10

// tagged says why no release is cut at sha when one of the highest version
// tags already has it in its history, whoever cut that tag: merges only
// knows the Hive's own releases, and a tag cut by hand or a warden's step
// at a newer commit would otherwise get a higher number carrying older code,
// which an install following pre-releases takes as an update. A tag or
// history that can't be read never holds the cut.
func (m *Module) tagged(p core.Project, sha string) string {
	tags, err := m.C.Effects.Forge.Tags(p.Repo)
	if err != nil {
		return ""
	}
	var versions []string
	for _, t := range tags {
		if strings.HasPrefix(t, "v") && modules.Newer("v0.0.0", t) && !slices.Contains(versions, t) {
			versions = append(versions, t)
		}
	}
	slices.SortFunc(versions, func(a, b string) int {
		if modules.Newer(b, a) {
			return -1
		}
		if modules.Newer(a, b) {
			return 1
		}
		return 0
	})
	contains := m.Contains
	if contains == nil {
		contains = m.contains
	}
	for _, t := range versions[:min(len(versions), tagChecks)] {
		at, err := m.C.Effects.Forge.TagCommit(p.Repo, t)
		if err != nil || at == "" {
			continue
		}
		if at == sha {
			return t + " is already cut at it"
		}
		in, err := contains(p, sha, at)
		if err != nil {
			// The cut goes ahead, but a guard that can't read shows here.
			log.Printf("release: %s: can't tell whether %s at %s contains %s: %v", p.Name, t, core.Short(at), core.Short(sha), err)
			continue
		}
		if in {
			return t + " at " + core.Short(at) + " already contains it, so a new tag would carry older code under a higher number"
		}
	}
	return ""
}

// contains says whether sha is tagSHA or one of its ancestors, fetching
// tagSHA into the project's clone, with the token its owner's sync uses,
// when it hasn't got it. Both come to git only as hex object names, so a
// forge value can never be read as an option.
func (m *Module) contains(p core.Project, sha, tagSHA string) (bool, error) {
	if !hexName(sha) || !hexName(tagSHA) {
		return false, fmt.Errorf("not a commit name: %q, %q", core.Clip(sha, 50), core.Clip(tagSHA, 50))
	}
	dir := m.C.WS.Project(p.Name)
	full, err := core.Git(dir, "rev-parse", "--verify", sha+"^{commit}")
	if err != nil {
		return false, err
	}
	if _, err := core.Git(dir, "rev-parse", "--verify", tagSHA+"^{commit}"); err != nil {
		if _, err := core.GitEnvT(5*time.Minute, dir, core.ForgeEnv(p.Repo), "fetch", "-q", "--no-tags", "origin", tagSHA); err != nil {
			return false, err
		}
	}
	base, err := core.Git(dir, "merge-base", sha, tagSHA)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(base) == strings.TrimSpace(full), nil
}

// hexName says whether s is a full or abbreviated hex object name.
func hexName(s string) bool {
	if len(s) < 4 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

// diff lists the files that differ between two commits of the project.
func (m *Module) diff(p core.Project, from, to string) ([]string, error) {
	out, err := core.Git(m.C.WS.Project(p.Name), "diff", "--name-only", "-z", "--end-of-options", from, to, "--")
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(strings.Split(out, "\x00"), func(f string) bool { return f == "" }), nil
}

// nextTag is one past the highest vX.Y.Z among tags (v0.0.1 if none is),
// or vX.Y.0 when line ("X.Y", from VERSION) is above the highest's X.Y.
func nextTag(tags []string, line string) string {
	last := "v0.0.0"
	for _, t := range tags {
		if strings.HasPrefix(t, "v") && modules.Newer(last, t) {
			last = t
		}
	}
	var a, b int
	if n, _ := fmt.Sscanf(line, "%d.%d", &a, &b); n == 2 {
		if first := fmt.Sprintf("v%d.%d.0", a, b); modules.Newer(last, first) {
			return first
		}
	}
	return bump(last)
}

// lineWhy is what a new major version says, by docs/versions.md.
const lineWhy = "1.0 is the first release stable enough for the person's own projects; each major after it means an update would break an existing setup"

// major is a vX.Y.Z tag's X.
func major(tag string) int {
	var a int
	fmt.Sscanf(strings.TrimPrefix(tag, "v"), "%d", &a)
	return a
}

// Breaking says whether going from one release to another crosses a major
// version past 1.0, which by the Hive's versioning (docs/versions.md) means
// the update would break an existing setup: no update takes it on its own.
func Breaking(from, to string) bool {
	return strings.HasPrefix(from, "v") && major(from) >= 1 && major(to) > major(from)
}

// line reads the release line VERSION names at sha ("" when there is none).
func (m *Module) line(p core.Project, sha string) string {
	read := m.Line
	if read == nil {
		read = func(p core.Project, sha string) (string, error) {
			if !hexName(sha) {
				return "", fmt.Errorf("not a commit name: %q", core.Clip(sha, 50))
			}
			return core.Git(m.C.WS.Project(p.Name), "show", sha+":VERSION")
		}
	}
	v, err := read(p, sha)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

func bump(tag string) string {
	var a, b, c int
	fmt.Sscanf(strings.TrimPrefix(tag, "v"), "%d.%d.%d", &a, &b, &c)
	return fmt.Sprintf("v%d.%d.%d", a, b, c+1)
}
