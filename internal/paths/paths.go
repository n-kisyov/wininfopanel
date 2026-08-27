// Package paths resolves the on-disk locations wininfopanel uses for user data.
//
// The layout mirrors InfoPanel's, rooted at %LOCALAPPDATA%\wininfopanel for
// per-user data and %ProgramData%\wininfopanel for machine-wide plugin
// installs:
//
//	%LOCALAPPDATA%\wininfopanel\
//	  settings.json            application settings
//	  profiles.json            profile list
//	  profiles\{guid}.json     display items for one profile
//	  assets\{guid}\           images and media owned by a profile
//	  autosave\                autosave backups, mirroring the above layout
//	  logs\                    rolling daily logs
//	  plugins\                 plugin configuration state
//	  updates\                 downloaded update installers
//
//	%ProgramData%\wininfopanel\
//	  plugins\                 user-installed external plugins
package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

// AppName is the folder name used under LOCALAPPDATA and ProgramData.
const AppName = "wininfopanel"

// LocalRoot returns %LOCALAPPDATA%\wininfopanel, creating it if needed.
func LocalRoot() (string, error) {
	base, err := os.UserCacheDir() // on Windows this is %LOCALAPPDATA%
	if err != nil {
		return "", fmt.Errorf("resolve LOCALAPPDATA: %w", err)
	}
	return ensureDir(filepath.Join(base, AppName))
}

// ProgramDataRoot returns %ProgramData%\wininfopanel.
//
// It does not create the directory: writing here usually requires elevation,
// and a missing folder simply means no external plugins are installed.
func ProgramDataRoot() (string, error) {
	base := os.Getenv("ProgramData")
	if base == "" {
		return "", fmt.Errorf("ProgramData environment variable is not set")
	}
	return filepath.Join(base, AppName), nil
}

// SettingsFile returns the path of the application settings file.
func SettingsFile() (string, error) { return underRoot("settings.json") }

// ProfilesFile returns the path of the profile list.
func ProfilesFile() (string, error) { return underRoot("profiles.json") }

// ProfilesDir returns the directory holding per-profile display items.
func ProfilesDir() (string, error) { return ensureUnderRoot("profiles") }

// ProfileFile returns the display-items file for a single profile.
func ProfileFile(profileID string) (string, error) {
	dir, err := ProfilesDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, profileID+".json"), nil
}

// AssetsDir returns the root of profile-owned assets.
func AssetsDir() (string, error) { return ensureUnderRoot("assets") }

// ProfileAssetsDir returns the asset directory belonging to one profile,
// creating it if needed.
func ProfileAssetsDir(profileID string) (string, error) {
	dir, err := AssetsDir()
	if err != nil {
		return "", err
	}
	return ensureDir(filepath.Join(dir, profileID))
}

// AutosaveDir returns the root of autosave backups.
func AutosaveDir() (string, error) { return ensureUnderRoot("autosave") }

// LogsDir returns the directory rolling log files are written to.
func LogsDir() (string, error) { return ensureUnderRoot("logs") }

// PluginConfigDir returns where plugin configuration state is persisted.
func PluginConfigDir() (string, error) { return ensureUnderRoot("plugins") }

// UpdatesDir returns where downloaded update installers are staged.
func UpdatesDir() (string, error) { return ensureUnderRoot("updates") }

// ExternalPluginsDir returns %ProgramData%\wininfopanel\plugins, where
// user-installed plugins live. The directory may not exist.
func ExternalPluginsDir() (string, error) {
	root, err := ProgramDataRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "plugins"), nil
}

// BundledPluginsDir returns the plugins directory shipped alongside the
// executable.
func BundledPluginsDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	return filepath.Join(filepath.Dir(exe), "plugins"), nil
}

// InfoPanelLocalRoot returns %LOCALAPPDATA%\InfoPanel, the original
// application's data directory. It is the default source for imports and is
// not created.
func InfoPanelLocalRoot() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve LOCALAPPDATA: %w", err)
	}
	return filepath.Join(base, "InfoPanel"), nil
}

func underRoot(name string) (string, error) {
	root, err := LocalRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, name), nil
}

func ensureUnderRoot(name string) (string, error) {
	p, err := underRoot(name)
	if err != nil {
		return "", err
	}
	return ensureDir(p)
}

func ensureDir(path string) (string, error) {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", fmt.Errorf("create directory %s: %w", path, err)
	}
	return path, nil
}
