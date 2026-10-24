package gate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// No sandbox is ever started from inside another: where unprivileged user
// namespaces are restricted (Ubuntu's AppArmor setting), bubblewrap works one
// level deep only. So the gate, the unsandboxed parent that runs no candidate
// code, starts every sandbox itself, side by side. When code inside a side's
// sandbox needs a sandbox of its own (the evaluator running code the side
// landed), it asks the parent through a Launcher.

// sandboxedEnv marks a process inside one of the gate's sandboxes. A gate
// that sees it never starts bubblewrap (see pickSandbox, BwrapWorks).
const sandboxedEnv = "HIVE_GATE_SANDBOXED"

// insideSandbox says this process runs inside one of the gate's sandboxes.
func insideSandbox() bool { return os.Getenv(sandboxedEnv) == "1" }

// Launcher serves, on a unix socket bound into a side's sandbox, requests to
// run a command in the sealed sandbox Child (no network, an empty root of its
// own, nothing of either Hive), with the request's stdin, answering its output
// and exit code. One at a time, each under a deadline.
type Launcher struct {
	Sock  string
	Child *Child
	ln    net.Listener
	mu    sync.Mutex
}

type launchReq struct {
	Argv  []string `json:"argv"`
	Stdin []byte   `json:"stdin"`
}

type launchResp struct {
	Out  []byte `json:"out"`
	Code int    `json:"code"`
}

// StartLauncher serves sealed runs of child on sock.
func StartLauncher(sock string, child *Child) (*Launcher, error) {
	os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return nil, err
	}
	l := &Launcher{Sock: sock, Child: child, ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go l.serve(c)
		}
	}()
	return l, nil
}

func (l *Launcher) Close() { l.ln.Close(); os.Remove(l.Sock) }

func (l *Launcher) serve(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Minute))
	var req launchReq
	if json.NewDecoder(io.LimitReader(c, 4<<20)).Decode(&req) != nil || len(req.Argv) == 0 {
		return
	}
	l.mu.Lock()
	out, err := l.Child.ExecIn(2*time.Minute, l.Child.Root, bytes.NewReader(req.Stdin), req.Argv...)
	l.mu.Unlock()
	code := 0
	var x *exec.ExitError
	if errors.As(err, &x) {
		code = x.ExitCode()
	} else if err != nil {
		code = 125
	}
	if len(out) > 1<<20 {
		out = out[len(out)-1<<20:]
	}
	json.NewEncoder(c).Encode(launchResp{Out: []byte(out), Code: code})
}

// LaunchClient is `hive gate-check <sock> -- argv...`, run inside a side's
// sandbox: it hands argv and its stdin to the parent's Launcher and passes
// back the output and the exit code.
func LaunchClient(sock string, argv []string) int {
	in, _ := io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
	c, err := net.Dial("unix", sock)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gate-check:", err)
		return 125
	}
	defer c.Close()
	if err := json.NewEncoder(c).Encode(launchReq{Argv: argv, Stdin: in}); err != nil {
		fmt.Fprintln(os.Stderr, "gate-check:", err)
		return 125
	}
	var resp launchResp
	if err := json.NewDecoder(io.LimitReader(c, 4<<20)).Decode(&resp); err != nil {
		fmt.Fprintln(os.Stderr, "gate-check: no answer:", err)
		return 125
	}
	os.Stdout.Write(resp.Out)
	return resp.Code
}

// checkScript is the HIVE_E2E_CHECK the evaluator calls inside a side's
// sandbox: a request to the parent's Launcher.
func checkScript(path, gateBin, sock string) error {
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	return os.WriteFile(path, []byte("#!/bin/sh\n# written by the Hive gate: a sealed run, started by the gate\nexec "+q(gateBin)+" gate-check "+q(sock)+` -- "$@"`+"\n"), 0o755)
}
