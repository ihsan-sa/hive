package core

import "testing"

// Closing an issue that is already closed succeeds, as Forge promises.
func TestLocalCloseIssueTwice(t *testing.T) {
	l := &Local{Dir: t.TempDir()}
	is, err := l.CreateIssue("me/p", "x", "body", "<!-- m -->")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := l.CloseIssue("me/p", is.Number); err != nil {
			t.Fatalf("close %d: %v", i+1, err)
		}
	}
	if s := l.load().Issues["me/p"][0].State; s != "CLOSED" {
		t.Fatalf("state %q", s)
	}
}
