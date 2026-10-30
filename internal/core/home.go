package core

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

// An agent's run is a session in the hive user's home (the person, 10-30:
// "Make an agent's run what this session is"): that home, read-write at its
// own path, with the projects' checkouts and the tools in it, and git and gh
// on the Hive's repo logins. The sandbox keeps the person's own home and
// other users' out (no /home but this one is there) and covers every
// credential in this one: the agent's own GitHub token comes in its
// environment, for its project's owner alone.

// AgentTokens is the folder of the agents' own GitHub tokens, one file per
// repository owner, each minted by the person with contents, pull-request and
// workflow write and no administration. The Hive's own logins (the org token,
// the gh login) can delete repositories and never reach a run.
var AgentTokens = func() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "hive", "agent-tokens")
}

// homeSecrets are the paths under home that hold a login, covered in a run
// that sees the home: the Hive's own logins, which hold administration
// (h0-org.token, the gh login), Claude's and Codex's, and every login file a
// tool keeps. The rest of home is the run's to read and write, the Hive's
// .hive/private and its service's settings included (the person, 10-30).
var homeSecrets = []string{
	"h0-org.token", ".config/gh", ".claude/.credentials.json", ".codex/auth.json",
	".netrc", ".git-credentials", ".gnupg", ".aws", ".kube", ".docker/config.json", ".npmrc", ".pypirc", ".local/share/keyrings",
}

// homeReadOnly are the paths under home a run reads but doesn't change:
// ~/.ssh's authorized_keys says who logs in to this machine, and a run
// adding to it would open the machine.
var homeReadOnly = []string{".ssh"}

// agentTokens is every token saved, by owner as its file is named.
func agentTokens() map[string]string {
	out := map[string]string{}
	entries, _ := os.ReadDir(AgentTokens())
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		if raw, err := os.ReadFile(filepath.Join(AgentTokens(), e.Name())); err == nil && strings.TrimSpace(string(raw)) != "" {
			out[e.Name()] = strings.TrimSpace(string(raw))
		}
	}
	return out
}

// tokenVar is the variable that holds owner's token in a run.
func tokenVar(owner string) string {
	return "HIVE_GH_TOKEN_" + strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' {
			return r - 'a' + 'A'
		}
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, owner)
}

// gitHelper is git's credential helper for GitHub in a run: the token of the
// repository's owner (git hands it the path, useHttpPath), else GH_TOKEN.
// The owner is cut to letters, digits and _ before it names a variable.
const gitHelper = `!f() { test "$1" = get || return 0; o=; while read -r l; do case $l in path=*) o=${l#path=};; esac; done; ` +
	`o=$(printf %s "${o%%/*}" | tr a-z A-Z | tr -c A-Z0-9 _); t=$GH_TOKEN; [ -z "$o" ] || eval "t=\${HIVE_GH_TOKEN_$o:-\$GH_TOKEN}"; ` +
	`printf 'username=x-access-token\npassword=%s\n' "$t"; }; f`

// agentGit is the environment that gives git and gh the agents' tokens: one
// HIVE_GH_TOKEN_<OWNER> per owner saved (only repo's owner's when only), and
// GH_TOKEN for gh, repo's owner's or, with none saved for it, the first
// owner's; git's helper picks the token by the repository's owner. None
// saved gives none.
func agentGit(repo string, only bool) []string {
	toks := agentTokens()
	owner, _, _ := strings.Cut(repo, "/")
	names := slices.Sorted(maps.Keys(toks))
	main := ""
	for _, n := range names {
		if strings.EqualFold(n, owner) {
			main = n
		}
	}
	if only {
		names = nil
		if main != "" {
			names = []string{main}
		}
	}
	if len(names) == 0 {
		return nil
	}
	if main == "" {
		main = names[0]
	}
	env := []string{"GH_TOKEN=" + toks[main], "GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=credential.https://github.com.helper", "GIT_CONFIG_VALUE_0=" + gitHelper,
		"GIT_CONFIG_KEY_1=credential.https://github.com.useHttpPath", "GIT_CONFIG_VALUE_1=true"}
	for _, n := range names {
		env = append(env, tokenVar(n)+"="+toks[n])
	}
	return env
}

// homeMounts are the mounts that make r's run a session in home: home
// read-write at its own path, then a cover over each login in it, over the
// workspace's covered folders and files (covered, files), which the home
// holds, and ~/.ssh read-only.
func homeMounts(home string, covered, files []string) []sandbox.Mount {
	out := []sandbox.Mount{{Host: home, Inside: home, RW: true}}
	for _, rel := range append(slices.Clone(homeSecrets), homeReadOnly...) {
		// Covered where it really is: a link resolves first, and one to a
		// place outside home (not there inside) or to nothing is skipped, so
		// a cover never creates a file in the real home through the bind.
		p, err := filepath.EvalSymlinks(filepath.Join(home, rel))
		if err != nil || !strings.HasPrefix(p, home+string(filepath.Separator)) {
			continue
		}
		out = append(out, sandbox.Mount{Host: p, Inside: p, Hide: !slices.Contains(homeReadOnly, rel)})
	}
	for _, d := range covered {
		if strings.HasPrefix(d, home+string(filepath.Separator)) {
			os.MkdirAll(d, 0o700)
			out = append(out, sandbox.Mount{Host: d, Inside: d, Hide: true})
		}
	}
	for _, f := range files {
		if strings.HasPrefix(f, home+string(filepath.Separator)) {
			out = append(out, sandbox.Mount{Host: f, Inside: f, Hide: true})
		}
	}
	return out
}

// UserRuntime is the hive user's runtime folder (/run/user/<uid>), where its
// service manager's bus is; "" when there is none (not Linux, no session).
var UserRuntime = func() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	dir := fmt.Sprintf("/run/user/%d", os.Getuid())
	if fi, err := os.Stat(filepath.Join(dir, "bus")); err != nil || fi.Mode()&os.ModeSocket == 0 {
		return ""
	}
	return dir
}

// runtimeMounts give a run in home the hive user's runtime folder rt, read-write
// at its own path, and its bus in DBUS_SESSION_BUS_ADDRESS, so systemctl
// --user, systemd-run --user and journalctl --user reach the hive user's
// service manager (restart hive.service, run a unit, read its journal).
// XDG_RUNTIME_DIR stays unset: with it, systemctl goes to systemd's private
// socket and refuses it, since systemd's pid is outside the run's pid
// namespace ("No data available"); through the bus it works. Claude Code's
// sockets there are named by pid, which every run's pid namespace repeats,
// so cc-socks is a folder of the run's own (own).
func runtimeMounts(rt, own string) ([]sandbox.Mount, []string) {
	if rt == "" {
		return nil, nil
	}
	os.MkdirAll(own, 0o700)
	return []sandbox.Mount{{Host: rt, Inside: rt, RW: true}, {Host: own, Inside: filepath.Join(rt, "cc-socks"), RW: true}},
		[]string{"DBUS_SESSION_BUS_ADDRESS=unix:path=" + filepath.Join(rt, "bus")}
}
