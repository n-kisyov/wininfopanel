package main

import (
	"context"
	"fmt"
	"runtime"
	"runtime/debug"
)

// version is stamped at build time with -ldflags "-X main.version=...".
// It stays "dev" for local builds.
var version = "dev"

func runVersion(_ context.Context, args []string) error {
	fs := newFlagSet("version")
	if err := fs.Parse(args); err != nil {
		return err
	}

	fmt.Printf("panelctl %s\n", version)
	fmt.Printf("go       %s\n", runtime.Version())
	fmt.Printf("platform %s/%s\n", runtime.GOOS, runtime.GOARCH)

	// The VCS stamp is only present in builds from a clean checkout, so its
	// absence is normal rather than an error.
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				fmt.Printf("revision %s\n", setting.Value)
			case "vcs.time":
				fmt.Printf("built    %s\n", setting.Value)
			case "vcs.modified":
				if setting.Value == "true" {
					fmt.Println("tree     modified")
				}
			}
		}
	}
	return nil
}
