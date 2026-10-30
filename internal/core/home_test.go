package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A run in home gets home read-write and every credential in it covered, a
// link to one covered where it points, and nothing covered that isn't there
// or lies outside home; the agents' token for a repository's owner is read
// by the owner's name in any case, and none saved gives none.
func TestSessionInHome(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".config", "gh"), 0o700)
	os.WriteFile(filepath.Join(home, "h0-org.token"), []byte("org"), 0o600)
	os.MkdirAll(filepath.Join(home, "elsewhere-ssh"), 0o700)
	os.Symlink(filepath.Join(home, "elsewhere-ssh"), filepath.Join(home, ".ssh"))
	os.Symlink("/nonexistent/creds", filepath.Join(home, ".netrc"))
	ws := Workspace{Root: filepath.Join(home, "hive")}
	ms := homeMounts(home, ws.Covered())
	if ms[0].Host != home || ms[0].Inside != home || !ms[0].RW || ms[0].Hide {
		t.Fatalf("home isn't mounted read-write at its own path: %+v", ms[0])
	}
	var hidden []string
	for _, m := range ms[1:] {
		if !m.Hide || m.Host != m.Inside {
			t.Fatalf("not a cover at its own path: %+v", m)
		}
		hidden = append(hidden, strings.TrimPrefix(m.Inside, home+"/"))
	}
	for _, want := range []string{".config/gh", "h0-org.token", "elsewhere-ssh", "hive/.hive/private", "hive/.hive/logins"} {
		if !slices.Contains(hidden, want) {
			t.Errorf("%s isn't covered: %v", want, hidden)
		}
	}
	if slices.Contains(hidden, ".netrc") || slices.Contains(hidden, ".aws") {
		t.Errorf("covered what isn't there: %v", hidden)
	}

}

// Every run gets every owner's token: GH_TOKEN its project's owner's, and a
// git helper that hands each repository its owner's (GH_TOKEN for an owner
// with none saved); an isolated project's run gets its own owner's alone,
// and none saved gives none.
func TestAgentTokens(t *testing.T) {
	dir := t.TempDir()
	was := AgentTokens
	t.Cleanup(func() { AgentTokens = was })
	AgentTokens = func() string { return dir }
	if env := agentGit("ihsan-sa/hive", false); env != nil {
		t.Fatalf("a token with none saved: %v", env)
	}
	os.WriteFile(filepath.Join(dir, "Hive-RSI-Lab"), []byte("github_pat_lab\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "ihsan-sa"), []byte("github_pat_me\n"), 0o600)
	env := agentGit("hive-rsi-lab/hive-hwde", false)
	for _, want := range []string{"GH_TOKEN=github_pat_lab", "HIVE_GH_TOKEN_HIVE_RSI_LAB=github_pat_lab", "HIVE_GH_TOKEN_IHSAN_SA=github_pat_me"} {
		if !slices.Contains(env, want) {
			t.Fatalf("%s missing: %v", want, env)
		}
	}
	if env := agentGit("", false); !slices.Contains(env, "GH_TOKEN=github_pat_lab") {
		t.Fatalf("no project: %v", env)
	}
	if env := agentGit("ihsan-sa/x", true); slices.Contains(env, "HIVE_GH_TOKEN_HIVE_RSI_LAB=github_pat_lab") || !slices.Contains(env, "GH_TOKEN=github_pat_me") {
		t.Fatalf("isolated: %v", env)
	}
	if env := agentGit("someone/x", true); env != nil {
		t.Fatalf("isolated, its owner has none: %v", env)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	for path, want := range map[string]string{"ihsan-sa/hive.git": "github_pat_me", "Hive-RSI-Lab/pll": "github_pat_lab", "other/x": "github_pat_me", "$(touch pwned)/x": "github_pat_me"} {
		cmd := exec.Command("git", "credential", "fill")
		cmd.Dir = t.TempDir()
		cmd.Env = append(os.Environ(), "HOME="+cmd.Dir, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
		cmd.Env = append(cmd.Env, agentGit("ihsan-sa/hive", false)...)
		cmd.Stdin = strings.NewReader("protocol=https\nhost=github.com\npath=" + path + "\n\n")
		out, err := cmd.Output()
		if err != nil || !strings.Contains(string(out), "password="+want+"\n") || !strings.Contains(string(out), "username=x-access-token") {
			t.Errorf("%s: %v\n%s", path, err, out)
		}
		if _, err := os.Stat(filepath.Join(cmd.Dir, "pwned")); err == nil {
			t.Errorf("%s ran", path)
		}
	}
}

// A run in home gets the hive user's runtime folder at its own path, with
// the bus named (and XDG_RUNTIME_DIR unset, which would send systemctl to the
// private socket it can't use from a run), and Claude Code's per-pid sockets
// in a folder of its own; no runtime folder gives nothing.
func TestRuntimeMounts(t *testing.T) {
	if ms, env := runtimeMounts("", t.TempDir()); ms != nil || env != nil {
		t.Fatalf("no runtime folder: %v %v", ms, env)
	}
	own := filepath.Join(t.TempDir(), "cc-socks")
	ms, env := runtimeMounts("/run/user/1003", own)
	if len(ms) != 2 || ms[0].Host != "/run/user/1003" || ms[0].Inside != "/run/user/1003" || !ms[0].RW ||
		ms[1].Host != own || ms[1].Inside != "/run/user/1003/cc-socks" || !ms[1].RW {
		t.Fatalf("mounts %+v", ms)
	}
	if len(env) != 1 || env[0] != "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1003/bus" {
		t.Fatalf("env %v", env)
	}
	if fi, err := os.Stat(own); err != nil || !fi.IsDir() {
		t.Fatalf("own sockets folder not made: %v", err)
	}
}
