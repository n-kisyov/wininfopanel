// Package plugins discovers, starts, and supervises plugin processes, and
// exposes the values they publish as a sensor source.
package plugins

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/ini.v1"
)

// Descriptor is a plugin found on disk, before it has been started.
type Descriptor struct {
	// Dir is the plugin's folder.
	Dir string `json:"dir"`
	// Executable is the program to run.
	Executable string `json:"executable"`

	// Metadata read from PluginInfo.ini, which is what the plugin browser
	// shows before a plugin has ever been started.
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Author      string `json:"author,omitempty"`
	Version     string `json:"version,omitempty"`
	Website     string `json:"website,omitempty"`

	// Bundled marks a plugin shipped with the application rather than
	// installed by the user.
	Bundled bool `json:"bundled"`
}

// manifestName is the metadata file beside a plugin's executable.
//
// The name and its [PluginInfo] section header match InfoPanel's, so a plugin
// author's existing manifest carries over unchanged.
const manifestName = "PluginInfo.ini"

// Discover finds plugins in the given directories.
//
// A plugin is a folder holding an executable and, optionally, a PluginInfo.ini
// describing it. Folders are used rather than loose executables so a plugin can
// ship its own data files without them colliding with another's.
//
// Later directories take precedence, so a user-installed plugin overrides a
// bundled one with the same folder name -- which is how someone tries a newer
// build of something that ships with the app.
func Discover(dirs ...string) []Descriptor {
	byName := make(map[string]Descriptor)
	var order []string

	for index, dir := range dirs {
		bundled := index == 0

		entries, err := os.ReadDir(dir)
		if err != nil {
			// A missing plugin directory is normal, not an error: most
			// installations have no external plugins at all.
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			descriptor, ok := describe(filepath.Join(dir, entry.Name()), bundled)
			if !ok {
				continue
			}

			key := strings.ToLower(entry.Name())
			if _, seen := byName[key]; !seen {
				order = append(order, key)
			}
			byName[key] = descriptor
		}
	}

	out := make([]Descriptor, 0, len(order))
	for _, key := range order {
		out = append(out, byName[key])
	}
	return out
}

// describe inspects one candidate folder.
//
// The executable is expected to share the folder's name, matching InfoPanel's
// convention; failing that, any single executable in the folder is taken,
// since a plugin folder holding exactly one program is unambiguous.
func describe(dir string, bundled bool) (Descriptor, bool) {
	name := filepath.Base(dir)

	executable := filepath.Join(dir, name+".exe")
	if _, err := os.Stat(executable); err != nil {
		found, ok := soleExecutable(dir)
		if !ok {
			return Descriptor{}, false
		}
		executable = found
	}

	descriptor := Descriptor{
		Dir:        dir,
		Executable: executable,
		Name:       name,
		Bundled:    bundled,
	}
	readManifest(filepath.Join(dir, manifestName), &descriptor)
	return descriptor, true
}

// soleExecutable returns the only .exe in a directory, if there is exactly one.
func soleExecutable(dir string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}

	var found string
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".exe") {
			continue
		}
		if found != "" {
			// More than one: which is the plugin is a guess, and guessing
			// wrong means starting the wrong program.
			return "", false
		}
		found = filepath.Join(dir, entry.Name())
	}
	return found, found != ""
}

// readManifest fills in metadata from PluginInfo.ini.
//
// A missing or malformed manifest is not fatal: the plugin still runs and
// reports its own metadata over the wire when it starts. The file exists so
// the plugin browser can describe a plugin without starting it.
func readManifest(path string, descriptor *Descriptor) {
	file, err := ini.Load(path)
	if err != nil {
		return
	}

	section := file.Section("PluginInfo")
	if name := section.Key("Name").String(); name != "" {
		descriptor.Name = name
	}
	descriptor.Description = section.Key("Description").String()
	descriptor.Author = section.Key("Author").String()
	descriptor.Version = section.Key("Version").String()
	descriptor.Website = section.Key("Website").String()
}

// WriteManifest writes a PluginInfo.ini, for tooling that generates plugins.
func WriteManifest(path string, descriptor Descriptor) error {
	file := ini.Empty()
	section, err := file.NewSection("PluginInfo")
	if err != nil {
		return err
	}

	for key, value := range map[string]string{
		"Name":        descriptor.Name,
		"Description": descriptor.Description,
		"Author":      descriptor.Author,
		"Version":     descriptor.Version,
		"Website":     descriptor.Website,
	} {
		if value == "" {
			continue
		}
		if _, err := section.NewKey(key, value); err != nil {
			return err
		}
	}

	if err := file.SaveTo(path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
