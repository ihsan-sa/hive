package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// Every login can be named and renamed, the machine's own included, and the
// name is only what the person sees: the key the records use stays.
func TestNameLogin(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	if err := os.MkdirAll(filepath.Join(ws.LoginsDir("claude"), "work"), 0o700); err != nil {
		t.Fatal(err)
	}
	name := func(args ...string) (string, error) {
		var b strings.Builder
		err := NameLogin(ws, args, &b)
		return b.String(), err
	}
	label := func(key string) string { return LoginLabel(LoginNames(ws), key) }

	if _, err := name("claude", "cc2"); err != nil || label("claude") != "cc2 (claude)" {
		t.Fatalf("the machine's own login takes a name: %v %q", err, label("claude"))
	}
	if _, err := name("work", "office"); err != nil || label("claude:work") != "office (claude:work)" {
		t.Fatalf("an added login is found by its folder: %v", err)
	}
	for _, taken := range []string{"office", "work", "claude:work", "codex"} {
		if _, err := name("cc2", taken); err == nil {
			t.Fatalf("%s is another login's already, so it's refused", taken)
		}
	}
	if _, err := name("cc2", "cc3"); err != nil || label("claude") != "cc3 (claude)" {
		t.Fatalf("a login is renamed by its name: %v", err)
	}
	if _, err := name("claude:work", ""); err != nil || label("claude:work") != "claude:work" {
		t.Fatalf("an empty name clears it: %v", err)
	}
	for _, bad := range [][]string{{"", "x"}, {"nope", "x"}, {"claude"}} {
		if _, err := name(bad...); err == nil {
			t.Fatalf("%q is refused", bad)
		}
	}
	out, err := name()
	if err != nil || !strings.Contains(out, "claude  cc3") || !strings.Contains(out, "claude:work  (no name)") || !strings.Contains(out, "codex  (no name)") {
		t.Fatalf("no args lists every login and its name: %v\n%s", err, out)
	}
	if ls := core.NewTokens(ws).Logins("claude"); len(ls) != 2 {
		t.Fatalf("the names file isn't taken for an added login: %v", ls)
	}
}
