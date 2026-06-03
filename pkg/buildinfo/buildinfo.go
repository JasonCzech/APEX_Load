// Package buildinfo is the single source of truth for build and runtime
// metadata surfaced to operators (version, commit, build time, environment).
//
// Version, Commit, and BuildTime are normally injected at link time via
// -ldflags "-X github.com/jasonczech/apexload/pkg/buildinfo.Version=..." (see the
// Makefile and Dockerfile). When they are absent (e.g. a plain `go build`),
// Get falls back to the module's embedded VCS stamp from runtime/debug.
package buildinfo

import (
	"os"
	"runtime"
	"runtime/debug"
	"strings"
)

// Injected at build time via -ldflags -X. Left empty for un-stamped builds.
var (
	Version   string
	Commit    string
	BuildTime string
)

// Info is the build/runtime metadata returned by the version endpoint.
type Info struct {
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	BuildTime   string `json:"build_time"`
	GoVersion   string `json:"go_version"`
	Environment string `json:"environment"`
}

// dockerEnvPath is the marker file Docker creates inside containers. It is a
// package var (not a const) so tests can point the probe at a fixture.
var dockerEnvPath = "/.dockerenv"

// Get returns the current build metadata, filling any link-time gaps from the
// embedded VCS stamp so the indicator is never blank in development builds.
func Get() Info {
	version := strings.TrimSpace(Version)
	commit := strings.TrimSpace(Commit)
	buildTime := strings.TrimSpace(BuildTime)

	if version == "" || commit == "" || buildTime == "" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			var revision, vcsTime string
			var modified bool
			for _, s := range bi.Settings {
				switch s.Key {
				case "vcs.revision":
					revision = s.Value
				case "vcs.time":
					vcsTime = s.Value
				case "vcs.modified":
					modified = s.Value == "true"
				}
			}
			if commit == "" && revision != "" {
				commit = shortCommit(revision)
				if modified {
					commit += "+dirty"
				}
			}
			if buildTime == "" && vcsTime != "" {
				buildTime = vcsTime
			}
			if version == "" {
				if v := strings.TrimSpace(bi.Main.Version); v != "" && v != "(devel)" {
					version = v
				}
			}
		}
	}

	if version == "" {
		version = "dev"
	}

	return Info{
		Version:     version,
		Commit:      commit,
		BuildTime:   buildTime,
		GoVersion:   runtime.Version(),
		Environment: Environment(),
	}
}

// shortCommit truncates a full git SHA to its conventional 7-char short form.
func shortCommit(revision string) string {
	if len(revision) > 7 {
		return revision[:7]
	}
	return revision
}

// Environment reports where the controller is running. Resolution order:
//  1. APEXLOAD_ENVIRONMENT (explicit override — how AKS/EKS get labeled).
//  2. KUBERNETES_SERVICE_HOST present ⇒ "kubernetes".
//  3. Docker marker file present ⇒ "docker".
//  4. otherwise ⇒ "local".
func Environment() string {
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("APEXLOAD_ENVIRONMENT"))); v != "" {
		return v
	}
	if strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_HOST")) != "" {
		return "kubernetes"
	}
	if _, err := os.Stat(dockerEnvPath); err == nil {
		return "docker"
	}
	return "local"
}
