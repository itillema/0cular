package main

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

// Build details that release builds set at link time, for example
// -ldflags "-X main.version=v0.1.0 -X main.commit=<sha> -X main.date=<time>".
// Other builds fall back to what the Go toolchain recorded in the binary.
var (
	version string
	commit  string
	date    string
)

// buildInfo describes the running binary.
type buildInfo struct {
	version   string
	commit    string
	date      string
	modified  bool
	goVersion string
	platform  string
}

// currentBuildInfo combines the link-time details with the build information
// the Go toolchain embedded in the binary.
func currentBuildInfo() buildInfo {
	b := buildInfo{
		version:   version,
		commit:    commit,
		date:      date,
		goVersion: runtime.Version(),
		platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if b.version == "" {
			b.version = info.Main.Version
		}
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				if b.commit == "" {
					b.commit = s.Value
				}
			case "vcs.time":
				if b.date == "" {
					b.date = s.Value
				}
			case "vcs.modified":
				b.modified = s.Value == "true"
			}
		}
	}
	if b.version == "" {
		b.version = "(devel)"
	}
	return b
}

// String formats the build information, one detail per line.
func (b buildInfo) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "0cular %s\n", b.version)
	if b.commit != "" {
		fmt.Fprintf(&sb, "commit:   %s", b.commit)
		if b.modified {
			sb.WriteString(" (modified)")
		}
		sb.WriteString("\n")
	}
	if b.date != "" {
		fmt.Fprintf(&sb, "date:     %s\n", b.date)
	}
	fmt.Fprintf(&sb, "go:       %s\n", b.goVersion)
	fmt.Fprintf(&sb, "platform: %s\n", b.platform)
	return sb.String()
}

// newVersionCommand builds the version command.
func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version and build information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprint(cmd.OutOrStdout(), currentBuildInfo())
			return err
		},
	}
}
