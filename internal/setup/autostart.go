package setup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// EnableAutostart makes the Hive start whenever the machine (or the person's
// session) starts, so an ask made in the evening is done in the morning.
func EnableAutostart(root string) error {
	if err := EnableAutostartAt(Executable(), root); err != nil {
		return err
	}
	if runtime.GOOS == "darwin" {
		// Unload first: load on a loaded label keeps its old path.
		exec.Command("launchctl", "unload", launchAgent()).Run()
		exec.Command("launchctl", "load", "-w", launchAgent()).Run()
	}
	return nil
}

// launchAgent is the macOS autostart's plist.
func launchAgent() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", "dev.hive.daemon.plist")
}

// EnableAutostartAt starts the binary at exe for the Hive in root. It runs
// during a handover too, so on macOS it only rewrites the plist: unloading
// the job then could stop the daemon doing the handover, and the next login
// reads the new plist. When the job's daemon exits, launchd kills its process
// group; the handover child has its own session (proc.StartDetached), and
// AbandonProcessGroup keeps launchd off it all the same.
func EnableAutostartAt(exe, root string) error {
	switch runtime.GOOS {
	case "windows":
		return windowsAutostart(exe, root, true)
	case "darwin":
		path := launchAgent()
		plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>dev.hive.daemon</string>
  <key>ProgramArguments</key><array><string>%s</string><string>daemon</string></array>
  <key>EnvironmentVariables</key><dict><key>HIVE_HOME</key><string>%s</string></dict>
  <key>RunAtLoad</key><true/>
  <key>AbandonProcessGroup</key><true/>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, exe, root, filepath.Join(root, ".hive", "daemon.log"), filepath.Join(root, ".hive", "daemon.log.err"))
		os.MkdirAll(filepath.Dir(path), 0o755)
		return os.WriteFile(path, []byte(plist), 0o644)
	default:
		home, _ := os.UserHomeDir()
		unit := userUnit()
		content := fmt.Sprintf("[Unit]\nDescription=The Hive\nAfter=network-online.target\n\n[Service]\nEnvironment=HIVE_HOME=%s\nExecStart=%s daemon\nKillMode=process\nRestart=on-failure\nRestartSec=10\n\n[Install]\nWantedBy=default.target\n", root, exe)
		os.MkdirAll(filepath.Dir(unit), 0o755)
		if err := os.WriteFile(unit, []byte(content), 0o644); err != nil {
			return err
		}
		desk := filepath.Join(home, ".config", "autostart", "hive.desktop")
		if exec.Command("systemctl", "--user", "daemon-reload").Run() == nil {
			exec.Command("systemctl", "--user", "enable", "hive.service").Run()
			// One launcher: a desktop autostart left from a start without
			// systemd would start a second daemon at login.
			os.Remove(desk)
			// Start at boot without a login session, where the system allows it.
			exec.Command("loginctl", "enable-linger").Run()
			return nil
		}
		// No systemd user session: fall back to the desktop autostart.
		os.Remove(unit)
		os.MkdirAll(filepath.Dir(desk), 0o755)
		return os.WriteFile(desk, []byte(fmt.Sprintf("[Desktop Entry]\nType=Application\nName=Hive\nExec=env HIVE_HOME=%s %s daemon\nX-GNOME-Autostart-enabled=true\n", root, exe)), 0o644)
	}
}

// cgroupFile names the systemd unit this process runs in, on Linux.
var cgroupFile = "/proc/self/cgroup"

// InUnit reports whether this process runs in the Hive's systemd user unit.
func InUnit() bool {
	name, user := Unit()
	return user && name == "hive.service"
}

// Unit names the systemd service this process runs in, from its cgroup, and
// whether it is a user unit; name is "" outside any service.
func Unit() (name string, user bool) {
	raw, _ := os.ReadFile(cgroupFile)
	for _, line := range strings.Split(string(raw), "\n") {
		parts := strings.SplitN(line, ":", 3) // v2 "0::/path", v1 "1:name=systemd:/path"
		if len(parts) < 3 || parts[1] != "" && parts[1] != "name=systemd" {
			continue
		}
		path := parts[2]
		if base := path[strings.LastIndex(path, "/")+1:]; strings.HasSuffix(base, ".service") {
			return base, strings.Contains(path, "/user@")
		}
	}
	return "", false
}

// UnitSettings reads a unit's Restart=, KillMode= and MainPID=, empty (0)
// where systemctl can't say. A process in the unit's cgroup that isn't its
// main process, such as one started from a shell in tmux.service, isn't what
// the unit supervises.
func UnitSettings(name string, user bool) (restart, killMode string, mainPID int) {
	args := []string{"show", "-p", "Restart", "-p", "KillMode", "-p", "MainPID", name}
	if user {
		args = append([]string{"--user"}, args...)
	}
	out, _ := exec.Command("systemctl", args...).Output()
	for _, line := range strings.Split(string(out), "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(line), "=")
		switch k {
		case "Restart":
			restart = v
		case "KillMode":
			killMode = v
		case "MainPID":
			mainPID, _ = strconv.Atoi(v)
		}
	}
	return restart, killMode, mainPID
}

// RestartUnit rewrites the unit to start exe and has systemd restart it. A
// unit from v0.1.2 or older lacks KillMode=process, so it kills a restart
// child left in the unit when the old daemon exits; systemd's own restart
// doesn't. It reports whether systemd took the restart.
func RestartUnit(exe, root string) bool {
	EnableAutostartAt(exe, root)
	return exec.Command("systemctl", "--user", "restart", "--no-block", "hive.service").Run() == nil
}

// userUnit is the Hive's systemd user unit.
func userUnit() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user", "hive.service")
}

// StartUnit has systemd start the Hive's user unit when it serves the
// workspace at root, so a command that finds no daemon starts the one the
// unit supervises instead of one beside it; starting an active unit does
// nothing. It reports whether systemd took the start.
func StartUnit(root string) bool {
	if runtime.GOOS != "linux" {
		return false
	}
	raw, err := os.ReadFile(userUnit())
	if err != nil || !strings.Contains(string(raw), "\nEnvironment=HIVE_HOME="+root+"\n") {
		return false
	}
	return exec.Command("systemctl", "--user", "start", "--no-block", "hive.service").Run() == nil
}

// DisableAutostart undoes EnableAutostart.
func DisableAutostart() {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		windowsAutostart("", "", false)
	case "darwin":
		exec.Command("launchctl", "unload", "-w", launchAgent()).Run()
		os.Remove(launchAgent())
	default:
		exec.Command("systemctl", "--user", "disable", "hive.service").Run()
		os.Remove(filepath.Join(home, ".config", "systemd", "user", "hive.service"))
		os.Remove(filepath.Join(home, ".config", "autostart", "hive.desktop"))
	}
}

// AutostartEnabled reports whether autostart is registered.
func AutostartEnabled() bool {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		return windowsAutostartEnabled()
	case "darwin":
		_, err := os.Stat(launchAgent())
		return err == nil
	default:
		for _, p := range []string{filepath.Join(home, ".config", "systemd", "user", "hive.service"), filepath.Join(home, ".config", "autostart", "hive.desktop")} {
			if _, err := os.Stat(p); err == nil {
				return true
			}
		}
		return false
	}
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }
