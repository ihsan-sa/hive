package daemon

import "testing"

// After an update renames the running binary to hive.old, the restart runs
// the new hive, not the old code under its new name.
func TestRestartRunsNewBinary(t *testing.T) {
	for in, want := range map[string]string{"/b/hive.old": "/b/hive", `C:\b\hive.exe.old`: `C:\b\hive.exe`, "/b/hive": "/b/hive"} {
		if got := onDisk(in); got != want {
			t.Errorf("onDisk(%q) = %q, want %q", in, got, want)
		}
	}
}
