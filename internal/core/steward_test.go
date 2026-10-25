package core

import (
	"path/filepath"
	"testing"
)

// A Hive's id is made once and kept; the record names its first word.
func TestHiveIDAndStewardRecord(t *testing.T) {
	ws := Workspace{Root: filepath.Join(t.TempDir(), "hive")}
	id := ws.HiveID()
	if id == "" || ws.HiveID() != id {
		t.Fatalf("the id isn't kept: %q then %q", id, ws.HiveID())
	}
	if got := ParseSteward("# who\n\n" + StewardRecord(id)); got != id {
		t.Fatalf("the record names %q, not %q", got, id)
	}
	if StewardRefusal("me/hive", "", id) != "" || StewardRefusal("me/hive", id, id) != "" || StewardRefusal("me/hive", "H0", id) == "" {
		t.Fatal("only another recorded steward refuses")
	}
}
