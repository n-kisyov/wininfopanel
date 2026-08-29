package app

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

const autostartKeyName = "WinInfoPanel"

// IsAutostartEnabled checks if the app is set to run at startup.
func IsAutostartEnabled() (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.QUERY_VALUE)
	if err != nil {
		return false, err
	}
	defer k.Close()

	_, _, err = k.GetStringValue(autostartKeyName)
	if err == registry.ErrNotExist {
		return false, nil
	}
	return err == nil, err
}

// EnableAutostart adds the app to the Windows startup registry.
func EnableAutostart() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	exe, err := os.Executable()
	if err != nil {
		return err
	}

	return k.SetStringValue(autostartKeyName, `"`+exe+`"`)
}

// DisableAutostart removes the app from the Windows startup registry.
func DisableAutostart() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	err = k.DeleteValue(autostartKeyName)
	if err == registry.ErrNotExist {
		return nil
	}
	return err
}
