package release

import (
	"errors"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// PreMerge runs on a change to the Hive's own project before it merges the
// paths that otherwise first break once it is released: the release scan, the
// release build of every target, and an update from the running Hive's binary
// to the candidate, handed over as update.yml does, with the candidate's tests
// already run in the same sandbox. back says why the change goes back to its
// worker, "" to go on; note says what was skipped or excused. A step that
// fails on main too is no fault of the change and is excused.
func (m *Module) PreMerge(p core.Project, sha, main string) (back, note string) {
	if !p.Self {
		return "", ""
	}
	scanAt := m.Scan
	if scanAt == nil {
		scanAt = m.scan
	}
	var backs, notes []string
	if err := scanAt(p, sha); err != nil {
		var nl noLists
		switch {
		case errors.As(err, &nl):
			notes = append(notes, "the release scan was skipped: "+core.Clip(err.Error(), 200))
		case sha != main && scanAt(p, main) == nil:
			backs = append(backs, "The release scan (hive-scan, as release.yml runs it) blocks it, so its release would fail:\n\n"+core.Clip(err.Error(), 1500))
		default:
			notes = append(notes, "the release scan fails on main at "+core.Short(main)+" too, so it doesn't hold this change")
		}
	}
	rehearse := m.Rehearse
	if rehearse == nil {
		rehearse = m.rehearse
	}
	c := rehearse(p, sha)
	switch {
	case c.CantRun != "":
		notes = append(notes, "the release build and update "+c.CantRun)
	case c.OK:
	case sha != main && rehearse(p, main).OK:
		backs = append(backs, "The release build or the update to it fails (the running Hive's binary hands over to yours as update.yml does), so its release would fail:\n\n"+core.Clip(c.Output, 3000))
	default:
		notes = append(notes, "the release build or update fails on main at "+core.Short(main)+" too, so it doesn't hold this change")
	}
	return strings.Join(backs, "\n\n"), strings.Join(notes, "; ")
}

// rehearse runs the rehearsal on sha in a clean checkout in the sandbox,
// once per commit.
func (m *Module) rehearse(p core.Project, sha string) core.Check {
	if c, ok := m.C.Effects.GetCheck(p.Name, sha, "release"); ok && c.CantRun == "" {
		return c
	}
	return m.C.Effects.RunCheck(p, sha, "release", rehearsal(p.Repo))
}

// RehearsedTag is the version the rehearsal's candidate calls itself.
const RehearsedTag = "v0.0.0-premerge"

// rehearsal builds what release.yml builds, with its flags, then installs the
// running Hive's binary (hive on the PATH, /hive/bin/hive in the sandbox) in
// a fresh home, starts it, and hands it over to the candidate as update.yml
// hands a pre-release over: shut down, swap the binary, `daemon --detach
// --wait-for`, and the candidate must come up on that home under its own
// version. It lives here, in the running binary, so a change can't rewrite
// the check it is judged by.
func rehearsal(repo string) string {
	return `set -u
tag=` + RehearsedTag + `
out=$(mktemp -d)
pkg=$(go list -m)/internal/core
say() { echo "hive-premerge: $*"; }
for target in linux/amd64 linux/arm64; do
  os=${target%/*}; arch=${target#*/}
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath \
    -ldflags "-s -w -X $pkg.Version=$tag -X $pkg.DefaultReleaseRepo=` + safeRepo(repo) + `" \
    -o "$out/hive-$os-$arch" ./cmd/hive || { say "the release build for $target fails"; exit 1; }
done
say "the release builds for linux/amd64 and linux/arm64 pass"
if [ "$(go env GOOS)" != linux ]; then say "not on Linux, so the update was not tried"; exit 0; fi
old=$(command -v hive 2>/dev/null || true)
[ -x "$old" ] || old=/hive/bin/hive
if [ ! -x "$old" ]; then say "no running Hive's binary here, so the update was not tried"; exit 0; fi
export HIVE_HOME="$out/home"
bin=$out/bin
mkdir -p "$HIVE_HOME/.hive" "$bin"
cp "$old" "$bin/hive"
from=$("$bin/hive" --version 2>&1 | head -1)
printf '{"harness": "fake", "auto_update": false}\n' > "$HIVE_HOME/.hive/config.json"
log() { tail -40 "$HIVE_HOME/.hive/daemon.log" 2>/dev/null; }
trap '"$bin/hive" shutdown >/dev/null 2>&1 || true' EXIT
"$bin/hive" daemon --detach || { say "the running Hive ($from) would not start"; log; exit 1; }
info=$HIVE_HOME/.hive/private/daemon.json
for i in $(seq 60); do [ -s "$info" ] && break; [ -s "$HIVE_HOME/.hive/daemon.json" ] && { info=$HIVE_HOME/.hive/daemon.json; break; }; sleep 1; done
[ -s "$info" ] || { say "the running Hive ($from) never wrote its daemon.json"; log; exit 1; }
cp "$info" "$out/daemon.json"
pid=$(sed -n 's/.*"pid": *\([0-9]*\).*/\1/p' "$info")
"$bin/hive" shutdown >/dev/null
cp "$out/daemon.json" "$info"
cp "$out/hive-linux-$(go env GOARCH)" "$bin/hive"
"$bin/hive" daemon --detach --wait-for "$pid" || { say "the update from $from: the candidate would not take over"; log; exit 1; }
info=$HIVE_HOME/.hive/private/daemon.json
for i in $(seq 60); do grep -q "\"version\":\"$tag\"" "$info" 2>/dev/null && break; sleep 1; done
grep -q "\"version\":\"$tag\"" "$info" 2>/dev/null || { say "the update from $from: the candidate never came up"; cat "$info" 2>/dev/null; log; exit 1; }
"$bin/hive" status > "$out/status.txt" 2>&1
grep -q "^Hive $tag " "$out/status.txt" || { say "the update from $from: hive status after it says"; cat "$out/status.txt"; log; exit 1; }
say "the update from $from to the candidate passes"
`
}

// safeRepo keeps only what an owner/name may hold, so the name is no code in
// the script.
func safeRepo(repo string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '-' || r == '_' || r == '.' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, repo)
}
