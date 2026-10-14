// Package bruno imports OpenCollection YAML HTTP requests as native APEX
// scripts and retains immutable source for review. Legacy canonical DTOs stay
// readable for protected sessions written before Bruno Runner was retired.
package bruno

import (
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

const (
	CompatibilityVersion = "3.5.2"
	ImporterVersion      = "apexload.bruno-import.v1"

	OutputModeCanonical      = "canonical"
	OutputModeNativeRequests = "native_requests"

	PreservationUnavailableSourceValue = "source_value_unavailable"
	PreservationUnavailableReference   = "existing_secret_reference"
	PreservationUnavailableDestination = "destination_mapping_incomplete"
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
	OutputMode               string   `json:"output_mode,omitempty"`
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
	OutputMode         string              `json:"output_mode"`
	SourceFormat       string              `json:"source_format"`
	RootPath           string              `json:"root_path"`
	Manifest           Manifest            `json:"manifest"`
	Collection         Collection          `json:"collection"`
	Environments       []Environment       `json:"environments,omitempty"`
	Capabilities       Capabilities        `json:"capabilities"`
	SecretRequirements []SecretRequirement `json:"secret_requirements,omitempty"`
	SecurityFindings   []SecurityFinding   `json:"security_findings,omitempty"`
	Fidelity           FidelityReport      `json:"fidelity"`
	NativeProjection   ProjectionReport    `json:"native_projection,omitempty,omitzero"`
	MissingVariables   []string            `json:"missing_variables,omitempty"`
	// Script decodes canonical runner sessions retained before retirement.
	// New inspections produce only native candidates.
	Script *dsl.Script `json:"script,omitempty"`
	// Scripts contains the ordered native HTTP candidates from inspection.
	Scripts          []NativeScriptCandidate `json:"scripts,omitempty"`
	RawArchive       []byte                  `json:"-"`
	SanitizedArchive []byte                  `json:"-"`
	OriginalUpload   []byte                  `json:"-"`
	InspectedAt      time.Time               `json:"inspected_at"`
	// nativeRequestPasses is retained only for focused importer regression tests
	// and performance diagnostics; it is never serialized.
	nativeRequestPasses int
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
	Name string `json:"name"`
	// NameDeclared is true when Name comes from the collection's own
	// metadata rather than a folder name, file name or default. Only a
	// declared name identifies a collection across imports.
	NameDeclared bool             `json:"name_declared,omitempty"`
	UID          string           `json:"uid,omitempty"`
	Items        []CollectionItem `json:"items"`
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
	RequiredSandbox    string   `json:"required_sandbox,omitempty"`
	RunnerProfile      string   `json:"runner_profile,omitempty"`
}

type SecretRequirement struct {
	ID            string `json:"id"`
	Path          string `json:"path"`
	Key           string `json:"key"`
	SuggestedName string `json:"suggested_name"`
	Replacement   string `json:"replacement"`
	Context       string `json:"context"`
	// The presentation fields below are disclosure-safe metadata. They identify
	// authored references and locations, never the protected values behind them.
	DisplayLabel             string   `json:"display_label,omitempty"`
	SourceVariable           string   `json:"source_variable,omitempty"`
	UnresolvedSourceVariable bool     `json:"unresolved_source_variable,omitempty"`
	AffectedPaths            []string `json:"affected_paths,omitempty"`
	SourceValueAvailable     bool     `json:"source_value_available,omitempty"`
	// DestinationValuePreserved is set only by the protected import API after
	// it proves this requirement's authored value can be restored into every
	// affected destination occurrence without another operator input.
	DestinationValuePreserved bool `json:"destination_value_preserved,omitempty"`
	// PreservationUnavailableReason is a disclosure-safe explanation for why
	// DestinationValuePreserved is false. It never identifies or includes the
	// protected source value.
	PreservationUnavailableReason string `json:"preservation_unavailable_reason,omitempty"`
	// ExistingReference identifies a source {{secret.*}} reference that was
	// already safe. It remains a configure-now-or-later requirement, but never
	// participates in source-value migration.
	ExistingReference bool `json:"existing_reference,omitempty"`
	// Value is retained only in the short-lived server-side import session so an
	// authorized writable secret store can migrate it. It is never serialized.
	Value string `json:"-"`
	// scrubTargets records every authored variable definition that contributed
	// to a projected credential. It is importer-internal state: the targets are
	// removed from the sanitized bundle but are never exposed or persisted.
	scrubTargets    []nativeSecretScrubTarget
	affectedPathSet map[string]struct{}
	scrubTargetSet  map[string]struct{}
}

type SecurityFinding struct {
	ID       string `json:"id,omitempty"`
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Path     string `json:"path,omitempty"`
	Summary  string `json:"summary"`
}

// NativeScriptCandidate is one create-only APEX HTTP script projected from an
// OpenCollection YAML request. CandidateID identifies the source item within
// the inspection; PlannedScriptID is the server-owned durable script identity
// reserved for commit.
type NativeScriptCandidate struct {
	CandidateID        string              `json:"candidate_id"`
	PlannedScriptID    string              `json:"planned_script_id"`
	SourcePath         string              `json:"source_path"`
	SourceUID          string              `json:"source_uid"`
	Sequence           int                 `json:"sequence,omitempty"`
	Method             string              `json:"method"`
	Target             string              `json:"target"`
	Script             *dsl.Script         `json:"script"`
	Fidelity           string              `json:"fidelity"`
	Diagnostics        []NativeDiagnostic  `json:"diagnostics,omitempty"`
	MissingVariables   []string            `json:"missing_variables,omitempty"`
	SecretRequirements []SecretRequirement `json:"secret_requirements,omitempty"`
	SourceMappings     []SourceMapping     `json:"source_mappings,omitempty"`
	// CredentialTargets are encrypted-session-only commit instructions. They
	// bind protected values to exact server-authored script occurrences and
	// must never cross the public inspection boundary.
	CredentialTargets []NativeCredentialTarget `json:"-"`
	// ExistingScript is set when an earlier import of this source request
	// already created a script in the project.
	ExistingScript *NativeExistingScript `json:"existing_script,omitempty"`
}

// NativeExistingScript is a script an earlier import created from the same
// source request. A re-import can update it as a new version instead of
// creating a duplicate.
type NativeExistingScript struct {
	ScriptID  string    `json:"script_id"`
	Name      string    `json:"name"`
	RefKey    string    `json:"ref_key,omitempty"`
	FolderID  string    `json:"folder_id,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
	// Matches counts every active script imported from this request. A script
	// from the same collection is offered first, then the newest.
	Matches int `json:"matches"`
	// SameCollection is true when the collection UIDs match or, with no UIDs,
	// the collection names do. Request paths alone can coincide across
	// unrelated collections, so other matches default to a copy.
	SameCollection bool `json:"same_collection"`
	// EditedSinceImport is true when the script changed after the import that
	// last saved it, including a rename or move, or that cannot be confirmed.
	// Updating replaces its steps and settings and keeps its name and folder.
	EditedSinceImport bool `json:"edited_since_import"`
}

// NativeDiagnostic describes a bounded conversion decision without retaining
// source JavaScript or secret-bearing source fragments.
type NativeDiagnostic struct {
	ID                      string `json:"id"`
	FindingID               string `json:"finding_id,omitempty"`
	Severity                string `json:"severity"`
	Code                    string `json:"code"`
	Path                    string `json:"path,omitempty"`
	Message                 string `json:"message"`
	AcknowledgementRequired bool   `json:"acknowledgement_required,omitempty"`
	// HeaderRepair is set on bruno_invalid_header_value findings. Committing
	// with a header_repairs choice for this finding fixes the header instead
	// of acknowledging it.
	HeaderRepair *NativeHeaderRepair `json:"header_repair,omitempty"`
}

// SourceMapping links a native candidate to the compact source records that
// affected it. Hashes refer to sanitized import artifacts.
type SourceMapping struct {
	Path   string `json:"path"`
	Role   string `json:"role"`
	SHA256 string `json:"sha256"`
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
