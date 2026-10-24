//go:build windows

package setup

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// windowsAutostart registers `hive daemon` to run at logon for this user.
// A profile may have no Run key yet (GitHub's Windows runners have none), so
// turning it on creates the key, and with no key or no value it is already off.
func windowsAutostart(exe, root string, on bool) error {
	if !on {
		k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		defer k.Close()
		if err := k.DeleteValue("Hive"); !errors.Is(err, registry.ErrNotExist) {
			return err
		}
		return nil
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	// HIVE_HOME is found through the pointer file, so the command needs no environment.
	return k.SetStringValue("Hive", quote(exe)+" daemon --detach")
}

func windowsAutostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue("Hive")
	return err == nil
}
