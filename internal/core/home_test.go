package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

// A run in home gets home read-write, .hive/private included, with each login
// in it covered (a link to one where it points; nothing that isn't there or
// lies outside home), the workspace's covered folders and files too, and
// ~/.ssh and ~/hive-backups read-only.
func TestSessionInHome(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".config", "gh"), 0o700)
	os.MkdirAll(filepath.Join(home, ".config", "hive", "agent-tokens"), 0o700)
	os.WriteFile(filepath.Join(home, "h0-org.token"), []byte("org"), 0o600)
	os.MkdirAll(filepath.Join(home, ".claude"), 0o700)
	os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte("{}"), 0o600)
	os.MkdirAll(filepath.Join(home, "elsewhere-ssh"), 0o700)
	os.Symlink(filepath.Join(home, "elsewhere-ssh"), filepath.Join(home, ".ssh"))
	os.Symlink("/nonexistent/creds", filepath.Join(home, ".netrc"))
	os.MkdirAll(filepath.Join(home, "hive-backups"), 0o755)
	ws := Workspace{Root: filepath.Join(home, "hive")}
	os.MkdirAll(ws.Private(), 0o700)
	os.WriteFile(ws.DaemonFile(), []byte(`{"token":"t"}`), 0o600)
	ms := homeMounts(home, ws.Covered(), ws.PrivateFiles())
	if ms[0].Host != home || ms[0].Inside != home || !ms[0].RW || ms[0].Hide {
		t.Fatalf("home isn't mounted read-write at its own path: %+v", ms[0])
	}
	var hidden, readOnly []string
	for _, m := range ms[1:] {
		if m.Host != m.Inside || m.RW {
			t.Fatalf("not a cover at its own path: %+v", m)
		}
		rel := strings.TrimPrefix(m.Inside, home+"/")
		if m.Hide {
			hidden = append(hidden, rel)
		} else {
			readOnly = append(readOnly, rel)
		}
	}
	for _, want := range []string{".config/gh", "h0-org.token", ".claude/.credentials.json", "hive/.hive/logins", "hive/.hive/modules", "hive/.hive/secrets",
		"hive/.hive/private/keys", "hive/.hive/private/tmp", "hive/.hive/private/daemon.json"} {
		if !slices.Contains(hidden, want) {
			t.Errorf("%s isn't covered: %v", want, hidden)
		}
	}
	for _, open := range []string{"hive/.hive/private", ".config/hive", ".netrc", ".aws", "elsewhere-ssh"} {
		if slices.Contains(hidden, open) {
			t.Errorf("%s is covered: %v", open, hidden)
		}
	}
	if !slices.Equal(readOnly, []string{"elsewhere-ssh", "hive-backups"}) {
		t.Errorf("read-only: %v, want ~/.ssh where it points and the backups", readOnly)
	}
}

// A worker's run in home builds on the hive user's caches, not its own
// HOME's empty ones; a reader, which isn't in home, keeps its own HOME's.
func TestRunInHomeSharesCaches(t *testing.T) {
	c, _ := testHive(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	h, _ := harness.Get("fake")
	env := func(r Run) []string {
		s, err := c.Runs.sandboxSpec(c.WS.LoadConfig(), isolatedSB{}, h, r, 1, false, filepath.Join(t.TempDir(), "p"), nil, "")
		if err != nil {
			t.Fatal(err)
		}
		return s.Env
	}
	got := env(Run{RunSpec: RunSpec{ID: "w1", Role: "worker", Harness: "fake"}})
	for _, want := range []string{"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"), "GOMODCACHE=" + filepath.Join(home, "go", "pkg", "mod")} {
		if !slices.Contains(got, want) {
			t.Errorf("a worker in home lacks %s: %v", want, got)
		}
	}
	for _, kv := range env(Run{RunSpec: RunSpec{ID: "r1", Role: "reviewer", Harness: "fake", ReadOnly: true}}) {
		if strings.HasPrefix(kv, "XDG_CACHE_HOME=") || strings.HasPrefix(kv, "GOMODCACHE=") {
			t.Errorf("a reader, not in home, got %s", kv)
		}
	}
}

// A run in home has the hive user's home as its HOME, as a session started
// there does, with Claude Code's config folder still the run's own (its
// sessions and the links into home); a reader and a module's harness, whose
// login and sessions live in HOME, keep the run's own.
func TestRunInHomeHasRealHome(t *testing.T) {
	c, _ := testHive(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	env := func(h harness.Harness, r Run) []string {
		p := filepath.Join(t.TempDir(), "t.prompt")
		os.WriteFile(p, []byte("hi"), 0o644)
		s, err := c.Runs.sandboxSpec(c.WS.LoadConfig(), isolatedSB{}, h, r, 1, false, p, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		return s.Env
	}
	claude, _ := harness.Get("claude")
	got := env(claude, Run{RunSpec: RunSpec{ID: "w1", Role: "worker", Harness: "claude"}})
	for _, want := range []string{"HOME=" + home, "CLAUDE_CONFIG_DIR=/hive/home/.claude"} {
		if !slices.Contains(got, want) {
			t.Errorf("a Claude worker in home lacks %s: %v", want, got)
		}
	}
	fake, _ := harness.Get("fake")
	if got := env(fake, Run{RunSpec: RunSpec{ID: "w2", Role: "worker", Harness: "fake"}}); !slices.Contains(got, "HOME="+home) || slices.ContainsFunc(got, func(kv string) bool { return strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") }) {
		t.Errorf("a worker in home on another harness: %v", got)
	}
	if got := env(claude, Run{RunSpec: RunSpec{ID: "r1", Role: "reviewer", Harness: "claude", ReadOnly: true}}); !slices.Contains(got, "HOME=/hive/home") {
		t.Errorf("a reader: %v", got)
	}
	mod := &harness.Spec{ID: "mod", Bin: "mod"}
	if got := env(mod, Run{RunSpec: RunSpec{ID: "w3", Role: "worker", Harness: "mod"}}); !slices.Contains(got, "HOME=/hive/home") {
		t.Errorf("a module's harness: %v", got)
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
		// The home's own .gitconfig names a helper (gh's, on GH_TOKEN), as
		// the hive user's does; the owner's token still wins.
		os.WriteFile(filepath.Join(cmd.Dir, ".gitconfig"), []byte("[credential \"https://github.com\"]\n\thelper = !echo password=from_gitconfig; :\n"), 0o644)
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

// A Claude session in home links the hive user's settings, skills and
// plugins into the run's own home (only those that are there), shares the
// project's memory with that user's sessions in its clone, and never
// replaces what the run's home already has.
func TestShareClaudeHome(t *testing.T) {
	home, own := t.TempDir(), t.TempDir()
	if shareClaudeHome(own, home, "/work", home); exists(filepath.Join(own, ".claude")) {
		t.Fatal("a home with no ~/.claude linked something")
	}
	os.MkdirAll(filepath.Join(home, ".claude", "skills", "synced"), 0o755)
	os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"theme":"dark"}`), 0o644)
	os.MkdirAll(filepath.Join(own, ".claude", "plugins"), 0o755)
	os.MkdirAll(filepath.Join(home, ".claude", "plugins"), 0o755)
	dir := filepath.Join(home, "hive", "projects", "pll")
	for range 2 {
		shareClaudeHome(own, home, "/work", dir)
	}
	for _, n := range []string{"settings.json", "skills"} {
		if to, err := os.Readlink(filepath.Join(own, ".claude", n)); err != nil || to != filepath.Join(home, ".claude", n) {
			t.Errorf("%s isn't linked from home: %q %v", n, to, err)
		}
	}
	for _, n := range []string{"CLAUDE.md", "agents", "commands"} {
		if _, err := os.Lstat(filepath.Join(own, ".claude", n)); err == nil {
			t.Errorf("%s is linked though home has none", n)
		}
	}
	if fi, err := os.Lstat(filepath.Join(own, ".claude", "plugins")); err != nil || !fi.IsDir() {
		t.Errorf("the run's own plugins folder was replaced: %v", err)
	}
	mem := filepath.Join(home, ".claude", "projects", claudeName(dir), "memory")
	if to, err := os.Readlink(filepath.Join(own, ".claude", "projects", "-work", "memory")); err != nil || to != mem || !exists(mem) {
		t.Errorf("memory isn't the clone's in home: %q %v", to, err)
	}
	if got := claudeName("/home/hive/openup-261030"); got != "-home-hive-openup-261030" {
		t.Errorf("claudeName = %q", got)
	}
}
