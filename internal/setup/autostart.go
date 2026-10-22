package setup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// EnableAutostart makes the Hive start whenever the machine (or the person's
// session) starts, so an ask made in the evening is done in the morning.
func EnableAutostart(root string) error { return EnableAutostartAt(Executable(), root) }

// EnableAutostartAt starts the binary at exe for the Hive in root.
func EnableAutostartAt(exe, root string) error {
	switch runtime.GOOS {
	case "windows":
		return windowsAutostart(exe, root, true)
	case "darwin":
		home, _ := os.UserHomeDir()
		path := filepath.Join(home, "Library", "LaunchAgents", "dev.hive.daemon.plist")
		plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>dev.hive.daemon</string>
  <key>ProgramArguments</key><array><string>%s</string><string>daemon</string></array>
  <key>EnvironmentVariables</key><dict><key>HIVE_HOME</key><string>%s</string></dict>
  <key>RunAtLoad</key><true/>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, exe, root, filepath.Join(root, ".hive", "daemon.log"), filepath.Join(root, ".hive", "daemon.log.err"))
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(plist), 0o644); err != nil {
			return err
		}
		exec.Command("launchctl", "load", "-w", path).Run()
		return nil
	default:
		home, _ := os.UserHomeDir()
		unit := filepath.Join(home, ".config", "systemd", "user", "hive.service")
		content := fmt.Sprintf("[Unit]\nDescription=The Hive\nAfter=network-online.target\n\n[Service]\nEnvironment=HIVE_HOME=%s\nExecStart=%s daemon\nKillMode=process\nRestart=on-failure\nRestartSec=10\n\n[Install]\nWantedBy=default.target\n", root, exe)
		os.MkdirAll(filepath.Dir(unit), 0o755)
		if err := os.WriteFile(unit, []byte(content), 0o644); err != nil {
			return err
		}
		if exec.Command("systemctl", "--user", "daemon-reload").Run() == nil {
			exec.Command("systemctl", "--user", "enable", "hive.service").Run()
			// Start at boot without a login session, where the system allows it.
			exec.Command("loginctl", "enable-linger").Run()
			return nil
		}
		// No systemd user session: fall back to the desktop autostart.
		os.Remove(unit)
		desk := filepath.Join(home, ".config", "autostart", "hive.desktop")
		os.MkdirAll(filepath.Dir(desk), 0o755)
		return os.WriteFile(desk, []byte(fmt.Sprintf("[Desktop Entry]\nType=Application\nName=Hive\nExec=env HIVE_HOME=%s %s daemon\nX-GNOME-Autostart-enabled=true\n", root, exe)), 0o644)
	}
}

// cgroupFile names the systemd unit this process runs in, on Linux.
var cgroupFile = "/proc/self/cgroup"

// InUnit reports whether this process runs in the Hive's systemd user unit.
func InUnit() bool {
	raw, _ := os.ReadFile(cgroupFile)
	return strings.Contains(string(raw), "/hive.service")
}

// RestartUnit rewrites the unit to start exe and has systemd restart it. A
// unit from v0.1.2 or older lacks KillMode=process, so it kills a restart
// child left in the unit when the old daemon exits; systemd's own restart
// doesn't. It reports whether systemd took the restart.
func RestartUnit(exe, root string) bool {
	EnableAutostartAt(exe, root)
	return exec.Command("systemctl", "--user", "restart", "--no-block", "hive.service").Run() == nil
}

// DisableAutostart undoes EnableAutostart.
func DisableAutostart() {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		windowsAutostart("", "", false)
	case "darwin":
		path := filepath.Join(home, "Library", "LaunchAgents", "dev.hive.daemon.plist")
		exec.Command("launchctl", "unload", "-w", path).Run()
		os.Remove(path)
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
		_, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", "dev.hive.daemon.plist"))
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
