package agentcli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A switch may stand bare before the next --key, but a key that takes a value
// keeps it even when the value itself starts with --.
func TestSwitchesAndValues(t *testing.T) {
	got, err := parseArgs([]string{"--self", "--text", "--help", "--name", "hive", "--isolated"})
	if err != nil {
		t.Fatal(err)
	}
	if got["self"] != true || got["text"] != "--help" || got["name"] != "hive" || got["isolated"] != true {
		t.Fatalf("parsed %v", got)
	}
}

// A switch given true or false, as --apart true or --apart=false, reads as a
// yes or no, while another key's "true" stays text.
func TestSwitchValues(t *testing.T) {
	got, err := parseArgs([]string{"--apart", "true", "--self=false", "--text", "true"})
	if err != nil {
		t.Fatal(err)
	}
	if got["apart"] != true || got["self"] != false || got["text"] != "true" {
		t.Fatalf("parsed %v", got)
	}
}

// A worker's finish done with changes no commit holds is refused before it
// reaches the Hive; finish blocked still goes through, and so does done once
// the tree is clean.
func TestFinishDoneRefusesADirtyTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir, box := t.TempDir(), t.TempDir()
	for _, a := range [][]string{{"init", "-q"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "x"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, a...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", a, err, out)
		}
	}
	os.WriteFile(filepath.Join(dir, "edited.go"), []byte("package x\n"), 0o644)
	os.MkdirAll(filepath.Join(box, "outbox"), 0o755)
	os.MkdirAll(filepath.Join(box, "inbox"), 0o755)
	t.Setenv("HIVE_BOX", box)
	t.Setenv("HIVE_ROLE", "worker")
	t.Chdir(dir)
	// The Hive's end: answer every request ok, and count them.
	sent := make(chan string, 4)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
			}
			names, _ := filepath.Glob(filepath.Join(box, "outbox", "*.json"))
			for _, n := range names {
				raw, _ := os.ReadFile(n)
				os.Remove(n)
				var req struct{ ID string }
				json.Unmarshal(raw, &req)
				sent <- string(raw)
				os.WriteFile(filepath.Join(box, "inbox", req.ID+".json"), []byte(`{"ok":true,"result":"recorded"}`), 0o644)
			}
		}
	}()
	if code := Main([]string{"finish", "--status", "done", "--account", "did it"}); code == 0 {
		t.Fatal("finish done with an uncommitted file went through")
	}
	if code := Main([]string{"finish", "--status", "blocked", "--account", "stuck"}); code != 0 {
		t.Fatal("finish blocked with a dirty tree was refused")
	}
	if req := <-sent; !strings.Contains(req, "blocked") {
		t.Fatalf("sent %s", req)
	}
	os.Remove(filepath.Join(dir, "edited.go"))
	if code := Main([]string{"finish", "--status", "done", "--account", "did it"}); code != 0 {
		t.Fatal("finish done on a clean tree was refused")
	}
	if req := <-sent; !strings.Contains(req, `"done"`) {
		t.Fatalf("sent %s", req)
	}
	if len(sent) != 0 {
		t.Fatal("the refused finish was sent")
	}
}
