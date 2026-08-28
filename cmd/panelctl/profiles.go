package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/n-kisyov/wininfopanel/internal/config/model"
	"github.com/n-kisyov/wininfopanel/internal/config/store"
	"github.com/n-kisyov/wininfopanel/internal/paths"
)

// runProfiles inspects and activates stored profiles.
//
// Which profiles are active is otherwise only reachable by hand-editing
// profiles.json: the importer faithfully carries InfoPanel's own active flag,
// so an imported panel that was switched off there stays off here, and until
// the desktop shell exists nothing else can turn it back on.
func runProfiles(args []string) error {
	fs := newFlagSet("profiles")
	if err := fs.Parse(args); err != nil {
		return err
	}

	setupConsoleLogging(false)

	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("usage: panelctl profiles <list|activate|deactivate> [flags]")
	}

	switch rest[0] {
	case "list":
		return profilesList(rest[1:])
	case "activate":
		return profilesSetActive(rest[1:], true)
	case "deactivate":
		return profilesSetActive(rest[1:], false)
	default:
		return fmt.Errorf("unknown subcommand %q (want list, activate, or deactivate)", rest[0])
	}
}

// openProfileStore opens the configuration store, defaulting to the location
// the application itself uses.
func openProfileStore(dataDir string) (*store.Store, error) {
	if dataDir == "" {
		var err error
		if dataDir, err = paths.LocalRoot(); err != nil {
			return nil, err
		}
	}
	return store.Open(dataDir)
}

func profilesList(args []string) error {
	fs := newFlagSet("profiles list")
	dataDir := fs.String("data-dir", "", "configuration directory; empty uses the standard location")

	if err := fs.Parse(args); err != nil {
		return err
	}

	configStore, err := openProfileStore(*dataDir)
	if err != nil {
		return err
	}

	profiles := configStore.Profiles()
	if len(profiles) == 0 {
		fmt.Println("No profiles are configured.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ACTIVE\tNAME\tSIZE\tITEMS\tBACKGROUND\tID")

	for _, profile := range profiles {
		active := ""
		if profile.Active {
			active = "*"
		}

		// An item count is what distinguishes a profile that shows nothing
		// from one that is merely switched off, which is the whole question
		// someone runs this command to answer.
		items := "?"
		if layout, err := configStore.Layout(profile.ID); err == nil {
			items = fmt.Sprint(len(model.FlattenAll(layout)))
		}

		fmt.Fprintf(w, "%s\t%s\t%dx%d\t%s\t%s\t%s\n",
			active, profile.Name, profile.Width, profile.Height,
			items, profile.BackgroundColor, profile.ID)
	}

	if err := w.Flush(); err != nil {
		return err
	}

	fmt.Printf("\n%d profile(s); * shows an overlay\n", len(profiles))
	return nil
}

// profilesSetActive turns one profile's overlay on or off.
func profilesSetActive(args []string, active bool) error {
	verb := "deactivate"
	if active {
		verb = "activate"
	}

	fs := newFlagSet("profiles " + verb)
	dataDir := fs.String("data-dir", "", "configuration directory; empty uses the standard location")
	exclusive := fs.Bool("only", false, "deactivate every other profile")

	// flag stops parsing at the first non-flag argument, so the target is
	// lifted off the front before parsing. That way it reads naturally either
	// side of the flags, rather than silently swallowing a trailing -data-dir.
	target, rest := splitLeadingArg(args)
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if target == "" {
		target = fs.Arg(0)
	}
	if target == "" || fs.NArg() > 1 {
		return fmt.Errorf("usage: panelctl profiles %s <id-or-name> [flags]", verb)
	}

	configStore, err := openProfileStore(*dataDir)
	if err != nil {
		return err
	}

	profile, err := findProfile(configStore, target)
	if err != nil {
		return err
	}

	if active && *exclusive {
		for _, other := range configStore.Profiles() {
			if other.ID == profile.ID || !other.Active {
				continue
			}
			if err := setActive(configStore, other.ID, false); err != nil {
				return err
			}
			fmt.Printf("deactivated %s (%s)\n", other.Name, other.ID)
		}
	}

	if err := setActive(configStore, profile.ID, active); err != nil {
		return err
	}
	fmt.Printf("%sd %s (%s)\n", verb, profile.Name, profile.ID)

	fmt.Println("\nRestart wininfopanel for the change to take effect.")
	return nil
}

func setActive(configStore *store.Store, id string, active bool) error {
	return configStore.UpdateProfile(id, func(p *model.Profile) { p.Active = active })
}

// findProfile resolves a profile by ID, by exact name, or by a unique
// case-insensitive name prefix, so the long UUIDs stay optional.
func findProfile(configStore *store.Store, needle string) (*model.Profile, error) {
	if profile, ok := configStore.Profile(needle); ok {
		return profile, nil
	}

	var matches []*model.Profile
	for _, profile := range configStore.Profiles() {
		if strings.EqualFold(profile.Name, needle) ||
			strings.HasPrefix(strings.ToLower(profile.ID), strings.ToLower(needle)) {
			matches = append(matches, profile)
		}
	}

	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return nil, fmt.Errorf("no profile matches %q; run 'panelctl profiles list'", needle)
	default:
		// Importing the same InfoPanel profile twice leaves two profiles with
		// the same name, so a name alone is not always an answer.
		var b strings.Builder
		fmt.Fprintf(&b, "%q matches %d profiles; use an ID:", needle, len(matches))
		for _, profile := range matches {
			fmt.Fprintf(&b, "\n  %s  %s", profile.ID, profile.Name)
		}
		return nil, fmt.Errorf("%s", b.String())
	}
}

// splitLeadingArg peels a leading positional argument off the front, leaving
// the flags behind for the flag set.
func splitLeadingArg(args []string) (string, []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}
	return "", args
}
