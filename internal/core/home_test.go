package core

import (
	"os"
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

	dir := t.TempDir()
	was := AgentTokens
	t.Cleanup(func() { AgentTokens = was })
	AgentTokens = func() string { return dir }
	os.WriteFile(filepath.Join(dir, "Hive-RSI-Lab"), []byte("github_pat_x\n"), 0o600)
	if tok := AgentToken("hive-rsi-lab/hive-hwde"); tok != "github_pat_x" {
		t.Fatalf("token %q", tok)
	}
	if tok := AgentToken("someone/else"); tok != "" || agentGit(tok) != nil {
		t.Fatalf("a token for an owner with none saved: %q", tok)
	}
	env := agentGit("github_pat_x")
	if !slices.Contains(env, "GH_TOKEN=github_pat_x") || !slices.Contains(env, "GIT_CONFIG_KEY_0=credential.https://github.com.helper") {
		t.Fatalf("env %v", env)
	}
}
