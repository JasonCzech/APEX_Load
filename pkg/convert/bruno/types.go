// Package bruno imports Bruno collections without translating away source
// behavior. It builds an immutable, deterministic bundle and a canonical APEX
// bruno_run script that delegates execution to the compatibility runner.
package bruno

import (
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

const (
	CompatibilityVersion = "3.5.2"
	ImporterVersion      = "apexload.bruno-import.v1"
)

type Limits struct {
	MaxFiles             int
	MaxFileBytes         int64
	MaxExpandedBytes     int64
	MaxMaterializedBytes int64
	MaxPathDepth         int
	MaxCompressionRatio  int64
}

func DefaultLimits() Limits {
	return Limits{
		MaxFiles:             100_000,
		MaxFileBytes:         64 << 20,
		MaxExpandedBytes:     32 << 20,
		MaxMaterializedBytes: 1 << 30,
		MaxPathDepth:         128,
		MaxCompressionRatio:  1_000,
	}
}

type Input struct {
	Name      string
	ProjectID string
	Filename  string
	Content   []byte
	Files     map[string][]byte
	Sandbox   string
	RootPath  string
	Options   InspectOptions
	Limits    Limits
}

type InspectOptions struct {
	DefaultEnvironment       string   `json:"default_environment,omitempty"`
	DefaultGlobalEnvironment string   `json:"default_global_environment,omitempty"`
	Selection                string   `json:"selection,omitempty"`
	Paths                    []string `json:"paths,omitempty"`
	IncludeTags              []string `json:"include_tags,omitempty"`
	ExcludeTags              []string `json:"exclude_tags,omitempty"`
	Recursive                bool     `json:"recursive,omitempty"`
	Execution                string   `json:"execution,omitempty"`
	Bail                     bool     `json:"bail,omitempty"`
	TestsOnly                bool     `json:"tests_only,omitempty"`
	DelayMS                  int      `json:"delay_ms,omitempty"`
	StreamTimeoutMS          int      `json:"stream_timeout_ms,omitempty"`
	PersistSession           bool     `json:"persist_session,omitempty"`
}

type Result struct {
	SchemaVersion      string              `json:"schema_version"`
	SourceFormat       string              `json:"source_format"`
	RootPath           string              `json:"root_path"`
	Manifest           Manifest            `json:"manifest"`
	Collection         Collection          `json:"collection"`
	Environments       []Environment       `json:"environments,omitempty"`
	Capabilities       Capabilities        `json:"capabilities"`
	SecretRequirements []SecretRequirement `json:"secret_requirements,omitempty"`
	SecurityFindings   []SecurityFinding   `json:"security_findings,omitempty"`
	Fidelity           FidelityReport      `json:"fidelity"`
	NativeProjection   ProjectionReport    `json:"native_projection"`
	MissingVariables   []string            `json:"missing_variables,omitempty"`
	Script             *dsl.Script         `json:"script"`
	RawArchive         []byte              `json:"-"`
	SanitizedArchive   []byte              `json:"-"`
	OriginalUpload     []byte              `json:"-"`
	InspectedAt        time.Time           `json:"inspected_at"`
}

type Manifest struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	// RawArchiveSHA256 is retained only for server-side protected-artifact
	// integrity. Raw-source fingerprints must never cross the import-review or
	// durable-script disclosure boundary.
	RawArchiveSHA256 string         `json:"-"`
	BundleSHA256     string         `json:"bundle_sha256"`
	TotalBytes       int64          `json:"total_bytes"`
	Files            []ManifestFile `json:"files"`
}

type ManifestFile struct {
	Path       string `json:"path"`
	Role       string `json:"role"`
	MediaType  string `json:"media_type,omitempty"`
	SizeBytes  int64  `json:"size_bytes"`
	SHA256     string `json:"sha256"`
	Mode       int64  `json:"mode"`
	Executable bool   `json:"executable,omitempty"`
}

type Collection struct {
	Name  string           `json:"name"`
	UID   string           `json:"uid,omitempty"`
	Items []CollectionItem `json:"items"`
}

type CollectionItem struct {
	UID        string           `json:"uid"`
	Name       string           `json:"name"`
	Path       string           `json:"path"`
	Type       string           `json:"type"`
	Sequence   int              `json:"sequence,omitempty"`
	Tags       []string         `json:"tags,omitempty"`
	Executable bool             `json:"executable"`
	HasScripts bool             `json:"has_scripts,omitempty"`
	HasTests   bool             `json:"has_tests,omitempty"`
	Children   []CollectionItem `json:"children,omitempty"`
}

type Environment struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"`
}

type Capabilities struct {
	RequestTypes       []string `json:"request_types"`
	HasJavaScript      bool     `json:"has_javascript"`
	HasNPMDependencies bool     `json:"has_npm_dependencies"`
	HasLocalModules    bool     `json:"has_local_modules"`
	HasFileBodies      bool     `json:"has_file_bodies"`
	HasCertificates    bool     `json:"has_certificates"`
	HasPrivateKeyFiles bool     `json:"has_private_key_files"`
	HasBrunoApps       bool     `json:"has_bruno_apps"`
	RequiredSandbox    string   `json:"required_sandbox"`
	RunnerProfile      string   `json:"runner_profile,omitempty"`
}

type SecretRequirement struct {
	ID                   string `json:"id"`
	Path                 string `json:"path"`
	Key                  string `json:"key"`
	SuggestedName        string `json:"suggested_name"`
	Replacement          string `json:"replacement"`
	Context              string `json:"context"`
	SourceValueAvailable bool   `json:"source_value_available,omitempty"`
	// ExistingReference identifies a source {{secret.*}} reference that was
	// already safe. It remains a configure-now-or-later requirement, but never
	// participates in source-value migration.
	ExistingReference bool `json:"existing_reference,omitempty"`
	// Value is retained only in the short-lived server-side import session so an
	// authorized writable secret store can migrate it. It is never serialized.
	Value string `json:"-"`
}

type SecurityFinding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Path     string `json:"path,omitempty"`
	Summary  string `json:"summary"`
}

type FidelityReport struct {
	Status                string   `json:"status"`
	PreservedFiles        int      `json:"preserved_files"`
	ExecutableItems       int      `json:"executable_items"`
	PreservedApps         int      `json:"preserved_apps"`
	DroppedItems          int      `json:"dropped_items"`
	Compatibility         string   `json:"compatibility"`
	IntentionalExtensions []string `json:"intentional_extensions,omitempty"`
}

type ProjectionReport struct {
	Eligible bool     `json:"eligible"`
	Reasons  []string `json:"reasons,omitempty"`
}
