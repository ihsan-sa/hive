package core

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeGH puts a gh on PATH that logs its arguments, one call per line, and
// prints reply. It returns the log's path.
func fakeGH(t *testing.T, reply string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a sh script")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\nprintf '%s\\n' '" + reply + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// A fork lands in the organization asked for (child Hives and test repos
// fork into Hive-RSI-Lab), and in the person's account when none is named.
func TestGitHubForkOwner(t *testing.T) {
	ready := forkReady
	t.Cleanup(func() { forkReady = ready })
	forkReady = func(string) bool { return true }

	log := fakeGH(t, "Hive-RSI-Lab/hive")
	fork, err := GitHub{}.Fork("ihsan-sa/hive", "Hive-RSI-Lab")
	if err != nil || fork != "Hive-RSI-Lab/hive" {
		t.Fatalf("fork %q, %v", fork, err)
	}
	if _, err := (GitHub{}).Fork("ihsan-sa/hive", ""); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(log)
	calls := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(calls) != 2 {
		t.Fatalf("calls: %q", calls)
	}
	if !strings.Contains(calls[0], "repos/ihsan-sa/hive/forks") || !strings.Contains(calls[0], "-f organization=Hive-RSI-Lab") {
		t.Errorf("org fork called gh %q", calls[0])
	}
	if strings.Contains(calls[1], "organization") {
		t.Errorf("a fork to the person's account named an organization: %q", calls[1])
	}
}

func TestLocalForkOwner(t *testing.T) {
	l := &Local{Dir: t.TempDir()}
	if _, err := l.EnsureRepo("me/p", ""); err != nil {
		t.Fatal(err)
	}
	for owner, want := range map[string]string{"": "local/p", "lab": "lab/p"} {
		fork, err := l.Fork("me/p", owner)
		if err != nil || fork != want || !Exists(l.RepoPath(fork)) {
			t.Errorf("Fork(me/p, %q) = %q, %v", owner, fork, err)
		}
	}
}
