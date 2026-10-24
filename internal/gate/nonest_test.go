package gate

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeBwrap is a bwrap that records how it was started (from inside one of
// the gate's sandboxes or not) and then runs the command without isolation:
// enough to drive a whole gate run where namespaces are not allowed.
const fakeBwrap = `#!/bin/sh
printf 'inside=%s %s\n' "${HIVE_GATE_SANDBOXED:-}" "$(echo "$*" | tr '\n' ' ' | cut -c1-200)" >> LOG
dir=/
while [ $# -gt 0 ]; do
	case "$1" in
	--) shift; break ;;
	--setenv) export "$2=$3"; shift 3 ;;
	--chdir) dir=$2; shift 2 ;;
	--ro-bind|--bind|--symlink) shift 3 ;;
	--dev|--proc|--tmpfs) shift 2 ;;
	*) shift ;;
	esac
done
cd "$dir" || exit 1
if [ "$2" = gate-fwd ]; then shift 4; fi
exec "$@"
`

// No sandbox is ever started from inside another: where user namespaces are
// restricted (Ubuntu's AppArmor), bubblewrap nests only one level. A whole
// gate run, with the e2e probe's sealed check and the nested gate of
// recurse, must start every bubblewrap from the unsandboxed gate.
func TestNoSandboxInsideASandbox(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the gate's sandbox is bubblewrap, on Linux")
	}
	hive := builtHive(t)
	// Not t.TempDir: with a stand-in bwrap there is no pid namespace to end a
	// side's daemon and agents with their sandbox, so for a moment after Run
	// returns they may still write here. Remove it once they have gone.
	dir, err := os.MkdirTemp("", "gate-nonest-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeAll(dir) })
	bin, log := filepath.Join(dir, "bin"), filepath.Join(dir, "bwrap.log")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "bwrap"), []byte(strings.ReplaceAll(fakeBwrap, "LOG", "'"+log+"'")), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(sandboxedEnv, "")

	// A stand-in evaluator: asks for a sealed run, as TestBenchmark does.
	evDir := filepath.Join(dir, "eval")
	os.MkdirAll(filepath.Join(evDir, "internal", "e2e"), 0o755)
	evTest := filepath.Join(evDir, "e2e.test")
	os.WriteFile(evTest, []byte("#!/bin/sh\n\"$HIVE_E2E_BIN\" --version >/dev/null || exit 1\n[ \"$(printf ok | \"$HIVE_E2E_CHECK\" cat)\" = ok ] || { echo no sealed run; exit 1; }\n"), 0o755)
	judged := func(cfg *Config, c *Child) Result {
		c.Eval = &Evaluator{Src: evDir, Test: evTest}
		defer func() { c.Eval = nil }()
		return probeE2E(cfg, c)
	}
	rep, err := Run(Config{CandBin: hive, CtrlBin: hive, Self: hive, Out: filepath.Join(dir, "out", "run"), Sandbox: "bwrap", Reps: 1, Depth: 1,
		Probes: []string{"daemon", "judged", "recurse"}, Probe: map[string]ProbeFunc{"judged": judged}, Org: Local{Dir: filepath.Join(dir, "org")}, Options: Options{AllowTie: true}})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range append(rep.Candidate, rep.Control...) {
		if !r.Pass {
			t.Errorf("%s on a side: %s", r.Probe, r.Detail)
		}
	}
	if left := daemonsUnder(dir); len(left) > 0 {
		t.Errorf("daemons %v outlived the gate run", left)
	}
	cand := filepath.Join(dir, "out", "run", "candidate")
	for _, bulky := range []string{"gopath", "gocache", "home"} {
		if _, err := os.Stat(filepath.Join(cand, bulky)); err == nil {
			t.Errorf("the candidate's %s outlived the run", bulky)
		}
	}
	if _, err := os.Stat(filepath.Join(cand, "hive", "daemon-probe.log")); err != nil {
		t.Errorf("the candidate's daemon log went with the run: %v", err)
	}
	raw, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 4 {
		t.Fatalf("bubblewrap started %d times: %q", len(lines), raw)
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "inside= ") {
			t.Errorf("a sandbox was started from inside a sandbox: %.300s", l)
		}
	}
}

// removeAll removes dir, making it writable first and retrying for a while
// while something that outlived its sandbox still writes into it.
func removeAll(dir string) {
	for i := 0; i < 50; i++ {
		exec.Command("chmod", "-R", "u+w", dir).Run()
		if os.RemoveAll(dir) == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}
