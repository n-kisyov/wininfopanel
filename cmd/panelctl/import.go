package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/n-kisyov/wininfopanel/internal/config/importer"
	"github.com/n-kisyov/wininfopanel/internal/config/store"
	"github.com/n-kisyov/wininfopanel/internal/paths"
)

func runImport(_ context.Context, args []string) error {
	fs := newFlagSet("import")
	from := fs.String("from", "", `InfoPanel data directory (default %LOCALAPPDATA%\InfoPanel)`)
	to := fs.String("to", "", `target data directory (default %LOCALAPPDATA%\wininfopanel`)
	dryRun := fs.Bool("dry-run", false, "report what would be imported without writing anything")
	asJSON := fs.Bool("json", false, "emit the result as JSON")
	verbose := fs.Bool("v", false, "log engine activity to stderr")

	if err := fs.Parse(args); err != nil {
		return err
	}
	setupConsoleLogging(*verbose)

	targetDir := *to
	if targetDir == "" {
		if *dryRun {
			// A dry run must not create or touch the real configuration.
			var err error
			if targetDir, err = os.MkdirTemp("", "wininfopanel-dryrun-*"); err != nil {
				return fmt.Errorf("create a scratch directory for the dry run: %w", err)
			}
			defer os.RemoveAll(targetDir)
		} else {
			var err error
			if targetDir, err = paths.LocalRoot(); err != nil {
				return err
			}
		}
	}

	target, err := store.Open(targetDir)
	if err != nil {
		return err
	}

	imp, err := importer.New(importer.Options{SourceDir: *from, Target: target})
	if err != nil {
		return err
	}

	if !imp.Available() {
		return fmt.Errorf("no InfoPanel installation found at %s "+
			"(expected a profiles.xml there)", imp.SourceDir())
	}

	result, err := imp.Import()
	if err != nil {
		return err
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	fmt.Printf("imported from %s\n", result.SourceDir)
	if *dryRun {
		fmt.Println("(dry run: nothing was written to your configuration)")
	} else {
		fmt.Printf("           to %s\n", targetDir)
	}
	fmt.Printf("  %d profiles\n  %d display items\n  %d assets\n",
		result.Profiles, result.Items, result.Assets)

	if len(result.Skipped) > 0 {
		fmt.Println("\nskipped item types this version does not recognize:")
		names := make([]string, 0, len(result.Skipped))
		for name := range result.Skipped {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Printf("  %-32s %d\n", name, result.Skipped[name])
		}
	}

	if len(result.Warnings) > 0 {
		fmt.Println("\nwarnings:")
		for _, warning := range result.Warnings {
			fmt.Printf("  %s\n", warning)
		}
	}

	return nil
}
