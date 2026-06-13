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
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

// Injected at build time via -ldflags -X. Left empty for un-stamped builds.
var (
	Version   string
	Commit    string
	BuildTime string
)

// Info is the build/runtime metadata returned by the version endpoint.
type Info struct {
	Version        string `json:"version"`
	DisplayVersion string `json:"display_version"`
	Commit         string `json:"commit"`
	Revision       string `json:"revision,omitempty"`
	BuildTime      string `json:"build_time"`
	GoVersion      string `json:"go_version"`
	Environment    string `json:"environment"`
}

// dockerEnvPath is the marker file Docker creates inside containers. It is a
// package var (not a const) so tests can point the probe at a fixture.
var dockerEnvPath = "/.dockerenv"

var rawBuildVersionPattern = regexp.MustCompile(`^[0-9a-fA-F]{12,64}$`)

// Get returns the current build metadata, filling any link-time gaps from the
// embedded VCS stamp so the indicator is never blank in development builds.
func Get() Info {
	version := strings.TrimSpace(Version)
	revision := strings.TrimSpace(Commit)
	buildTime := strings.TrimSpace(BuildTime)
	var vcsRevision, vcsTime string
	var vcsModified bool
	var usingVCSRevision bool

	if version == "" || revision == "" || buildTime == "" || IsRawBuildVersion(version) {
		if bi, ok := debug.ReadBuildInfo(); ok {
			for _, s := range bi.Settings {
				switch s.Key {
				case "vcs.revision":
					vcsRevision = s.Value
				case "vcs.time":
					vcsTime = s.Value
				case "vcs.modified":
					vcsModified = s.Value == "true"
				}
			}
			if revision == "" && vcsRevision != "" {
				revision = vcsRevision
				usingVCSRevision = true
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

	if revision == "" && IsRawBuildVersion(version) {
		revision = version
	}
	version = ResolveVersion(version, revision, buildTime)
	commit := shortCommit(revision)
	if vcsModified && usingVCSRevision && commit != "" {
		version = markDirtyVersion(version)
	}
	displayVersion := DisplayVersion(version)

	return Info{
		Version:        version,
		DisplayVersion: displayVersion,
		Commit:         commit,
		Revision:       fullRevision(revision),
		BuildTime:      buildTime,
		GoVersion:      runtime.Version(),
		Environment:    Environment(),
	}
}

// ResolveVersion turns an injected build version into the machine-friendly
// version surfaced by APIs and artifact names.
func ResolveVersion(rawVersion string, revision string, buildTime string) string {
	version := NormalizeVersion(rawVersion)
	rev := strings.TrimSpace(revision)
	short := shortCommit(rev)
	if IsRawBuildVersion(version) {
		short = shortCommit(firstNonEmpty(rev, version))
		if date := buildDate(buildTime); date != "" && short != "" {
			return "0.1.0-dev." + date + "+" + short
		}
		if short != "" {
			return "dev+" + short
		}
		return "dev"
	}
	if version == "" {
		if short != "" {
			if date := buildDate(buildTime); date != "" {
				return "0.1.0-dev." + date + "+" + short
			}
			return "dev+" + short
		}
		return "dev"
	}
	if version == "dev" && short != "" {
		return "dev+" + short
	}
	return version
}

// NormalizeVersion removes cosmetic release prefixes and trims build metadata
// without making raw CI SHAs presentable.
func NormalizeVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" || version == "(devel)" {
		return ""
	}
	if len(version) > 1 && (version[0] == 'v' || version[0] == 'V') && version[1] >= '0' && version[1] <= '9' {
		return version[1:]
	}
	return version
}

// DisplayVersion is the user-facing label for a normalized build version.
func DisplayVersion(version string) string {
	version = NormalizeVersion(version)
	if version == "" {
		return "dev"
	}
	if version[0] >= '0' && version[0] <= '9' {
		return "v" + version
	}
	return version
}

// NormalizeVersionForCompare canonicalizes version strings for equality checks
// between local and hosted clients.
func NormalizeVersionForCompare(version string) string {
	return strings.ToLower(NormalizeVersion(version))
}

// VersionsEqual compares version labels while ignoring cosmetic v-prefixes.
func VersionsEqual(a string, b string) bool {
	return NormalizeVersionForCompare(a) == NormalizeVersionForCompare(b)
}

// IsRawBuildVersion identifies SHA-like or long numeric CI identifiers that
// should not be shown as version labels.
func IsRawBuildVersion(version string) bool {
	return rawBuildVersionPattern.MatchString(strings.TrimSpace(version))
}

// shortCommit truncates a git revision to its conventional 7-char short form.
func shortCommit(revision string) string {
	revision = strings.TrimSpace(revision)
	if len(revision) > 7 {
		return revision[:7]
	}
	return revision
}

func fullRevision(revision string) string {
	revision = strings.TrimSpace(revision)
	if len(revision) > 7 && IsRawBuildVersion(revision) {
		return revision
	}
	return ""
}

func buildDate(buildTime string) string {
	buildTime = strings.TrimSpace(buildTime)
	if buildTime == "" {
		return ""
	}
	if parsed, err := time.Parse(time.RFC3339, buildTime); err == nil {
		return parsed.UTC().Format("20060102")
	}
	digits := strings.NewReplacer("-", "", ":", "", "T", "", "Z", "", " ", "").Replace(buildTime)
	if len(digits) >= 8 {
		return digits[:8]
	}
	return ""
}

func markDirtyVersion(version string) string {
	if strings.Contains(version, "dirty") {
		return version
	}
	if strings.Contains(version, "+") {
		return version + ".dirty"
	}
	return version + "+dirty"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
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
