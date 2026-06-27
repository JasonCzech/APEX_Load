package main

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/buildinfo"
)

// buildAboutMessage renders the macOS About panel body from live build metadata
// instead of a hardcoded string, so the version/commit/build-time are accurate.
// Pure + unit-testable.
func buildAboutMessage(info buildinfo.Info) string {
	var b strings.Builder
	b.WriteString("Enterprise Performance Testing Platform\n")

	version := strings.TrimSpace(info.DisplayVersion)
	if version == "" {
		version = strings.TrimSpace(info.Version)
	}
	if version != "" {
		if commit := strings.TrimSpace(info.Commit); commit != "" {
			fmt.Fprintf(&b, "Version %s (%s)\n", version, commit)
		} else {
			fmt.Fprintf(&b, "Version %s\n", version)
		}
	}
	if bt := strings.TrimSpace(info.BuildTime); bt != "" {
		fmt.Fprintf(&b, "Built %s\n", bt)
	}
	b.WriteString("© 2026 Project Stormrunner")
	return b.String()
}
