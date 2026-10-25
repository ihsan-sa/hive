package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/daemon"
	"github.com/ihsan-sa/hive-standalone-build/internal/tui"
)

// cmdRemote opens the control center on this computer for a Hive on another
// one, over ssh: the control center then reads this computer's clipboard and
// files, and a pasted image goes to the Hive through its API.
func cmdRemote(args []string) int {
	var dest, hive string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--hive" && i+1 < len(args):
			hive = args[i+1]
			i++
		case strings.HasPrefix(a, "--hive="):
			hive = strings.TrimPrefix(a, "--hive=")
		case dest == "" && !strings.HasPrefix(a, "-"):
			dest = a
		default:
			fmt.Fprint(os.Stderr, commandHelp("remote"))
			return 2
		}
	}
	if dest == "" {
		fmt.Fprint(os.Stderr, commandHelp("remote"))
		return 2
	}
	ws, err := remoteWorkspace(dest)
	if err != nil {
		fmt.Fprintln(os.Stderr, core.Cmd+":", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "Connecting to the Hive on %s over ssh…\n", dest)
	r := &api.Remote{Dest: dest, Hive: hive}
	c, err := api.DialRemote(r)
	if err != nil {
		fmt.Fprintln(os.Stderr, core.Cmd+":", err)
		return 1
	}
	defer r.Close()
	if err := tui.Run(c, ws); err != nil {
		fmt.Fprintln(os.Stderr, core.Cmd+":", err)
		return 1
	}
	return 0
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// remoteWorkspace is where the control center keeps what it remembers of a
// remote Hive (what the person has seen): a folder of its own per host, on
// this computer.
func remoteWorkspace(dest string) (core.Workspace, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return core.Workspace{}, err
	}
	ws := core.Workspace{Root: filepath.Join(dir, "hive", "remote", unsafeName.ReplaceAllString(dest, "_"))}
	return ws, os.MkdirAll(ws.Hive(), 0o700)
}

// cmdRemoteInfo is what `hive remote` runs over ssh: it starts the Hive if
// it isn't running and prints its daemon.json; with --follow it prints it
// again whenever a restart changes it, until its input closes.
func cmdRemoteInfo(args []string, stdin io.Reader, stdout io.Writer) int {
	ws, err := workspace()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	c, err := daemon.Start(ws)
	if err != nil {
		fmt.Fprintln(os.Stderr, core.Cmd+":", err)
		return 1
	}
	return followInfo(ws, c.Info, len(args) > 0 && args[0] == "--follow", stdin, stdout, time.Second)
}

func followInfo(ws core.Workspace, last api.DaemonInfo, follow bool, stdin io.Reader, stdout io.Writer, every time.Duration) int {
	enc := json.NewEncoder(stdout)
	if enc.Encode(last) != nil || !follow {
		return 0
	}
	closed := make(chan struct{})
	go func() {
		io.Copy(io.Discard, stdin)
		close(closed)
	}()
	for {
		select {
		case <-closed:
			return 0
		case <-time.After(every):
		}
		cur, err := api.Dial(ws)
		if err != nil || cur.Info.PID == last.PID && cur.Info.Port == last.Port && cur.Info.Token == last.Token && cur.Info.Restarting == last.Restarting {
			continue
		}
		last = cur.Info
		if enc.Encode(last) != nil { // the session is gone
			return 0
		}
	}
}
