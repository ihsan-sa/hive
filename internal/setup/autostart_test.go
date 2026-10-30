package setup

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// Without a usable systemd, first run falls back to the desktop autostart
// and leaves no boot unit behind.
func TestAutostartFallbackLeavesNoUnit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd is Linux's")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir()) // no systemctl
	if err := EnableAutostart(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "systemd", "user", "hive.service")); err == nil {
		t.Fatal("the boot unit was left behind")
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "autostart", "hive.desktop")); err != nil {
		t.Fatalf("no desktop autostart: %v", err)
	}
}

// The daemon knows it runs in the Hive's unit from its cgroup.
func TestInUnit(t *testing.T) {
	f := filepath.Join(t.TempDir(), "cgroup")
	defer func(s string) { cgroupFile = s }(cgroupFile)
	cgroupFile = f
	for line, want := range map[string]bool{
		"0::/user.slice/user-1000.slice/user@1000.service/app.slice/hive.service\n": true,
		"0::/user.slice/user-1000.slice/session-3.scope\n":                          false,
	} {
		os.WriteFile(f, []byte(line), 0o644)
		if InUnit() != want {
			t.Errorf("InUnit for %q: want %v", line, want)
		}
	}
}

// A handover rewrites the macOS plist without touching launchd, so unloading
// the job can't stop the daemon doing the handover.
func TestHandoverLeavesLaunchdAlone(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("launchd is macOS's")
	}
	home, bin := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
	called := filepath.Join(bin, "called")
	os.WriteFile(filepath.Join(bin, "launchctl"), []byte("#!/bin/sh\necho \"$@\" >> "+called+"\n"), 0o755)
	if err := EnableAutostartAt("/new/hive", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(launchAgent()); err != nil || !strings.Contains(string(raw), "<string>/new/hive</string>") || !strings.Contains(string(raw), "<key>AbandonProcessGroup</key><true/>") {
		t.Fatalf("plist %q, %v", raw, err)
	}
	if raw, err := os.ReadFile(called); err == nil {
		t.Fatalf("the handover ran launchctl %q", raw)
	}
}

// The daemon names any systemd unit it runs in, user or system, and only the
// person's user unit hive.service counts as the Hive's own.
func TestUnit(t *testing.T) {
	f := filepath.Join(t.TempDir(), "cgroup")
	defer func(s string) { cgroupFile = s }(cgroupFile)
	cgroupFile = f
	for _, c := range []struct {
		line, name string
		user, own  bool
	}{
		{"0::/user.slice/user-1000.slice/user@1000.service/app.slice/hive.service\n", "hive.service", true, true},
		{"0::/user.slice/user-1000.slice/user@1000.service/app.slice/agents.service\n", "agents.service", true, false},
		{"0::/system.slice/hive.service\n", "hive.service", false, false},
		{"0::/system.slice/hive-ihsan.service\n", "hive-ihsan.service", false, false},
		{"12:cpu,cpuacct:/system.slice/x.service\n1:name=systemd:/system.slice/hive@box.service\n", "hive@box.service", false, false},
		{"0::/user.slice/user-1000.slice/session-3.scope\n", "", false, false},
	} {
		os.WriteFile(f, []byte(c.line), 0o644)
		if name, user := Unit(); name != c.name || user != c.user {
			t.Errorf("Unit for %q: got %q, %v", c.line, name, user)
		}
		if InUnit() != c.own {
			t.Errorf("InUnit for %q: want %v", c.line, c.own)
		}
	}
}

// A unit's Restart= and KillMode= come from systemctl, --user for a user unit.
func TestUnitSettings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in systemctl is a shell script")
	}
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	called := filepath.Join(bin, "called")
	os.WriteFile(filepath.Join(bin, "systemctl"), []byte("#!/bin/sh\necho \"$*\" >> "+called+"\nprintf 'Restart=on-failure\\nKillMode=control-group\\nMainPID=4242\\n'\n"), 0o755)
	if restart, kill, pid := UnitSettings("hive-box.service", false); restart != "on-failure" || kill != "control-group" || pid != 4242 {
		t.Fatalf("got Restart=%q KillMode=%q MainPID=%d", restart, kill, pid)
	}
	UnitSettings("agents.service", true)
	raw, _ := os.ReadFile(called)
	if want := "show -p Restart -p KillMode -p MainPID hive-box.service\n--user show -p Restart -p KillMode -p MainPID agents.service\n"; string(raw) != want {
		t.Fatalf("systemctl ran %q, want %q", raw, want)
	}
}

// A desktop autostart left from a start without systemd goes once the unit
// is enabled, so a restart and the next login don't each start a daemon.
func TestUnitReplacesALoginAutostart(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd is Linux's")
	}
	home, bin := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
	os.WriteFile(filepath.Join(bin, "systemctl"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	desk := filepath.Join(home, ".config", "autostart", "hive.desktop")
	os.MkdirAll(filepath.Dir(desk), 0o755)
	os.WriteFile(desk, []byte("[Desktop Entry]\nExec=env HIVE_HOME=/w /x/hive daemon\n"), 0o644)
	root := t.TempDir()
	if err := EnableAutostartAt("/x/hive", root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(desk); err == nil {
		t.Fatal("the login autostart is still there beside the unit")
	}
	if !StartUnit(root) {
		t.Fatal("the unit serving the workspace wasn't started")
	}
	if StartUnit(t.TempDir()) {
		t.Fatal("a unit serving another workspace was started")
	}
}

// The unit serves a workspace however its root is written (a trailing slash,
// a symlink), and a daemon starting beside it removes a desktop autostart
// left from an install that had both.
func TestUnitServesItsRootAndDropsADoubledAutostart(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd is Linux's")
	}
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	link := filepath.Join(t.TempDir(), "ws")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Dir(userUnit()), 0o755)
	desk := filepath.Join(home, ".config", "autostart", "hive.desktop")
	os.MkdirAll(filepath.Dir(desk), 0o755)
	for _, written := range []string{root, root + "/", link, `"` + root + `"`} {
		os.WriteFile(userUnit(), []byte("[Service]\nEnvironment=HIVE_HOME="+written+"\nExecStart=/x/hive daemon\n"), 0o644)
		if !unitServes(root) {
			t.Errorf("a unit naming %s doesn't serve %s", written, root)
		}
	}
	if unitServes(t.TempDir()) {
		t.Error("the unit serves another workspace")
	}
	os.WriteFile(desk, []byte("[Desktop Entry]\n"), 0o644)
	if DropDoubledAutostart(t.TempDir()) {
		t.Fatal("another workspace's start removed the autostart")
	}
	if !DropDoubledAutostart(root) {
		t.Fatal("the doubled autostart stayed")
	}
	if _, err := os.Stat(desk); err == nil {
		t.Error("hive.desktop is still there")
	}
}

// The unit carries a PATH with the person's own bin dirs and where the
// daemon's tools are, so a daemon started at boot, with systemd's default
// PATH, still finds claude; a rewrite from such a daemon keeps the PATH the
// unit had, and the Hive's tools folder, which agents write, stays off it.
func TestUnitCarriesAPath(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd is Linux's")
	}
	home, root, bin := t.TempDir(), t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin+":"+filepath.Join(root, ".hive", "tools", "go", "bin")+":/usr/bin:/bin")
	path := unitPath(home, root, nil)
	for _, want := range []string{bin, filepath.Join(home, ".local", "bin"), filepath.Join(home, "bin"), "/usr/bin"} {
		if !slices.Contains(filepath.SplitList(path), want) {
			t.Errorf("PATH %q lacks %s", path, want)
		}
	}
	if strings.Contains(path, filepath.Join(root, ".hive")) {
		t.Errorf("PATH %q has the Hive's tools folder", path)
	}
	unit := unitText("/x/hive", root, path)
	if !strings.Contains(unit, "\nEnvironment=\"PATH="+path+"\"\n") {
		t.Fatalf("unit lacks its PATH:\n%s", unit)
	}
	// Rewritten from a daemon systemd started with its default PATH.
	t.Setenv("PATH", "/usr/bin:/bin")
	if again := unitPath(home, root, []byte(unit)); !slices.Contains(filepath.SplitList(again), bin) {
		t.Errorf("the rewrite lost %s: %q", bin, again)
	}
	if got := unitText("/x/hive", root, "/a%b"); !strings.Contains(got, "PATH=/a%%b") {
		t.Errorf("a %% in PATH isn't escaped for systemd: %s", got)
	}
}
