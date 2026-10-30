package core

import (
	"os"
	"path/filepath"
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

// homeSecrets are the paths under home that hold a credential, covered in a
// run that sees the home: the Hive's own logins, the person's machine logins
// and every login file a tool keeps.
var homeSecrets = []string{
	"h0-org.token", ".config/gh", ".config/hive", ".ssh", ".gnupg", ".netrc", ".git-credentials",
	".claude/.credentials.json", ".codex/auth.json", ".aws", ".kube", ".docker/config.json",
	".npmrc", ".pypirc", ".local/share/keyrings", ".config/systemd/user/hive.service.d",
}

// AgentToken is the agents' GitHub token for repo's owner, "" when the person
// hasn't saved one: then agents there run with none, and their pushes and
// merges go through the Hive as before.
func AgentToken(repo string) string {
	owner, _, ok := strings.Cut(repo, "/")
	if !ok || owner == "" {
		return ""
	}
	entries, err := os.ReadDir(AgentTokens())
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if strings.EqualFold(e.Name(), owner) && e.Type().IsRegular() {
			raw, err := os.ReadFile(filepath.Join(AgentTokens(), e.Name()))
			if err == nil {
				return strings.TrimSpace(string(raw))
			}
		}
	}
	return ""
}

// agentGit is the environment that gives git and gh tok: GH_TOKEN for gh, and
// a credential helper for git's pushes to GitHub that reads it.
func agentGit(tok string) []string {
	if tok == "" {
		return nil
	}
	return []string{"GH_TOKEN=" + tok, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=credential.https://github.com.helper",
		`GIT_CONFIG_VALUE_0=!f() { test "$1" = get && printf 'username=x-access-token\npassword=%s\n' "$GH_TOKEN"; }; f`}
}

// homeMounts are the mounts that make r's run a session in home: home
// read-write at its own path, then a cover over each credential in it and
// over the workspace's covered folders, which the home holds.
func homeMounts(home string, covered []string) []sandbox.Mount {
	out := []sandbox.Mount{{Host: home, Inside: home, RW: true}}
	for _, rel := range homeSecrets {
		// Covered where it really is: a link resolves first, and one to a
		// place outside home (not there inside) or to nothing is skipped, so
		// a cover never creates a file in the real home through the bind.
		p, err := filepath.EvalSymlinks(filepath.Join(home, rel))
		if err != nil || !strings.HasPrefix(p, home+string(filepath.Separator)) {
			continue
		}
		out = append(out, sandbox.Mount{Host: p, Inside: p, Hide: true})
	}
	for _, d := range covered {
		if strings.HasPrefix(d, home+string(filepath.Separator)) {
			os.MkdirAll(d, 0o700)
			out = append(out, sandbox.Mount{Host: d, Inside: d, Hide: true})
		}
	}
	return out
}
