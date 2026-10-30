// Package heavycli is `hive agent heavy -- cmd`, the agent's end of the
// work module's lease on heavy work (internal/mod/work/heavy.go). It is
// outside the core: it reaches the daemon through agentcli.Call alone.
package heavycli

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/agentcli"
)

// Call is how a request reaches the daemon; a test answers it itself.
var Call = agentcli.Call

// heavyPatience is how long `hive agent heavy` waits for a lease before it
// runs the command anyway, saying so: the pace is a default, not a gate, and
// a turn's shell command has a timeout of its own (Claude Code's is at most
// 10 minutes), which the wait must leave the command most of. --patience
// sets another.
const heavyPatience = 3 * time.Minute

// heavyPoll is how often a waiting command asks again, and heavyRenew how
// often a running one renews its lease (the work module drops one not
// renewed for three minutes).
var heavyPoll, heavyRenew = 20 * time.Second, time.Minute

// Main is `hive agent heavy [--what "..."] [--patience 5m] -- cmd args`:
// it waits for the work module's lease on heavy work (internal/mod/work/
// heavy.go), runs cmd, and gives the lease back when cmd ends, returning
// cmd's exit code. Outside a Hive run (no HIVE_BOX), as when the person runs
// a module's wrapper, it runs cmd at once.
func Main(argv []string) int {
	i := slices.Index(argv, "--")
	if i < 0 || i == len(argv)-1 {
		fmt.Fprintln(os.Stderr, `hive agent heavy: name the command after --: hive agent heavy --what "the test suite" -- cmd args`)
		return 2
	}
	args, err := parseArgs(argv[:i])
	if err != nil {
		fmt.Fprintln(os.Stderr, "hive agent heavy:", err)
		return 2
	}
	cmdv := argv[i+1:]
	what := args["what"]
	if what == "" {
		what = strings.Join(cmdv, " ")
		if len(what) > 80 {
			what = what[:80]
		}
	}
	patience := heavyPatience
	if p := args["patience"]; p != "" {
		if patience, err = time.ParseDuration(p); err != nil {
			fmt.Fprintln(os.Stderr, "hive agent heavy: --patience:", err)
			return 2
		}
	}
	var b [6]byte
	rand.Read(b[:])
	id := hex.EncodeToString(b[:])
	leased := os.Getenv("HIVE_BOX") != "" && waitLease(id, what, patience)

	cmd := exec.Command(cmdv[0], cmdv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		if leased {
			Call("heavy", map[string]any{"id": id, "done": true}, 30*time.Second)
		}
		fmt.Fprintln(os.Stderr, "hive agent heavy:", err)
		return 127
	}
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	ended := make(chan struct{})
	go func() {
		t := time.NewTicker(heavyRenew)
		defer t.Stop()
		for {
			select {
			case <-ended:
				return
			case s := <-sig:
				cmd.Process.Signal(s)
			case <-t.C:
				if leased {
					Call("heavy", map[string]any{"id": id, "what": what, "running": true}, heavyRenew)
				}
			}
		}
	}()
	err = cmd.Wait()
	signal.Stop(sig)
	close(ended)
	if leased {
		Call("heavy", map[string]any{"id": id, "done": true}, 30*time.Second)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "hive agent heavy:", err)
		return 1
	}
	return 0
}

// waitLease asks for the lease until it is given or patience runs out, and
// says why it waits each time the reason changes. It says whether a lease
// is held (and so must be given back).
func waitLease(id, what string, patience time.Duration) bool {
	start, said := time.Now(), ""
	for {
		raw, err := Call("heavy", map[string]any{"id": id, "what": what}, 3*time.Minute)
		if err != nil {
			fmt.Fprintln(os.Stderr, "hive agent heavy: no lease to be had, running it now:", err)
			return false
		}
		var r struct {
			Go      bool     `json:"go"`
			Wait    string   `json:"wait"`
			Running []string `json:"running"`
		}
		json.Unmarshal(raw, &r)
		if r.Go {
			if said != "" {
				fmt.Fprintf(os.Stderr, "hive agent heavy: running it after %s\n", time.Since(start).Round(time.Second))
			}
			return true
		}
		if time.Since(start) >= patience {
			fmt.Fprintf(os.Stderr, "hive agent heavy: waited %s, running it now: %s\n", patience, r.Wait)
			return false
		}
		if r.Wait != said {
			said = r.Wait
			fmt.Fprintf(os.Stderr, "hive agent heavy: waiting (at most %s): %s; running: %s\n", patience, r.Wait, strings.Join(r.Running, "; "))
		}
		time.Sleep(min(heavyPoll, patience-time.Since(start)))
	}
}

// parseArgs reads --what and --patience, as --key value or --key=value.
func parseArgs(a []string) (map[string]string, error) {
	out := map[string]string{}
	for i := 0; i < len(a); i++ {
		k, v, eq := strings.Cut(strings.TrimPrefix(a[i], "--"), "=")
		if !strings.HasPrefix(a[i], "--") || k != "what" && k != "patience" {
			return nil, fmt.Errorf("unknown argument %q: give --what and --patience, then -- and the command", a[i])
		}
		if !eq {
			if i+1 >= len(a) {
				return nil, fmt.Errorf("--%s needs a value", k)
			}
			v, i = a[i+1], i+1
		}
		out[k] = v
	}
	return out, nil
}
