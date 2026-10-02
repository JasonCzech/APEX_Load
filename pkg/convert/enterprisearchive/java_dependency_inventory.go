package enterprisearchive

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"path"
	"regexp"
	"sort"
	"strings"
)

const javaDependencyInventoryKey = "java_dependency_inventory"
const sourceAssetInventoryKey = "source_asset_inventory"

type sourceAssetEvidence struct {
	SourcePath   string  `json:"source_path"`
	Role         string  `json:"role"`
	SizeBytes    int     `json:"size_bytes"`
	SHA256       string  `json:"sha256"`
	Preview      string  `json:"preview"`
	MajorVersion *uint16 `json:"major_version,omitempty"`
	MinorVersion *uint16 `json:"minor_version,omitempty"`
}
type javaDependencyCounts struct {
	JARs              int `json:"jars"`
	Classes           int `json:"classes"`
	Executables       int `json:"executables"`
	OtherBinaryAssets int `json:"other_binary_assets"`
}
type javaDependencyInventory struct {
	Contract           string                `json:"contract"`
	Classification     string                `json:"classification"`
	RuntimeProvisioned bool                  `json:"runtime_provisioned"`
	Counts             javaDependencyCounts  `json:"counts"`
	Files              []sourceAssetEvidence `json:"files"`
}
type sourceAssetInventory struct {
	Contract         string                `json:"contract"`
	Scope            string                `json:"scope"`
	FileCount        int                   `json:"file_count"`
	OpaqueAssetCount int                   `json:"opaque_asset_count"`
	Files            []sourceAssetEvidence `json:"files"`
}

// Inventory evidence never implies executable classpath availability and never
// carries binary content. Java source .txt normalization has already happened;
// supplemental archive provenance retains the original member name separately.
func attachJavaDependencyInventory(result *ConvertResult, files map[string]string) {
	if result == nil || result.ReviewArtifact == nil {
		return
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	assets := sourceAssetInventory{Contract: "apexload.source-asset-inventory.v1", Scope: "normalized_regular_files", FileCount: len(names)}
	dependencies := javaDependencyInventory{Contract: "apexload.java-dependency-inventory.v1", Classification: "supplemental_java_assets"}
	if result.Script == nil && dependencyBundleWithoutProject(files) {
		dependencies.Classification = "java_dependency_bundle"
	}
	for _, name := range names {
		content := files[name]
		sum := sha256.Sum256([]byte(content))
		row := sourceAssetEvidence{SourcePath: name, Role: sourceAssetRole(name, content), SizeBytes: len(content), SHA256: hex.EncodeToString(sum[:]), Preview: "text_evidence_subject_to_review_limits"}
		opaque := sourceview.IsOpaqueAsset(name, content)
		if opaque {
			row.Preview = "opaque_asset_evidence_only"
			assets.OpaqueAssetCount++
		}
		switch strings.ToLower(path.Ext(name)) {
		case ".jar":
			dependencies.Counts.JARs++
		case ".class":
			dependencies.Counts.Classes++
			if len(content) >= 8 && strings.HasPrefix(content, "\xca\xfe\xba\xbe") {
				major := binary.BigEndian.Uint16([]byte(content[6:8]))
				minor := binary.BigEndian.Uint16([]byte(content[4:6]))
				row.MajorVersion = &major
				row.MinorVersion = &minor
			}
		case ".exe", ".dll", ".so", ".dylib":
			dependencies.Counts.Executables++
		default:
			if opaque {
				dependencies.Counts.OtherBinaryAssets++
			}
		}
		assets.Files = append(assets.Files, row)
		if opaque {
			dependencies.Files = append(dependencies.Files, row)
		}
	}
	if dependencies.Counts.JARs+dependencies.Counts.Classes == 0 {
		return
	}
	if result.ReviewArtifact.Metadata == nil {
		result.ReviewArtifact.Metadata = map[string]any{}
	}
	result.ReviewArtifact.Metadata[javaDependencyInventoryKey] = dependencies
	result.ReviewArtifact.Metadata[sourceAssetInventoryKey] = assets
	if result.Script != nil {
		if result.Script.Metadata == nil {
			result.Script.Metadata = &dsl.ScriptMetadata{}
		}
		if result.Script.Metadata.Import == nil {
			result.Script.Metadata.Import = &dsl.ImportMetadata{Source: "enterprise_script_archive"}
		}
		if result.Script.Metadata.Import.Preserved == nil {
			result.Script.Metadata.Import.Preserved = map[string]any{}
		}
		result.Script.Metadata.Import.Preserved[javaDependencyInventoryKey] = dependencies
		result.Script.Metadata.Import.Preserved[sourceAssetInventoryKey] = assets
	}
	if dependencies.Classification == "java_dependency_bundle" {
		result.ReviewArtifact.NextActions = appendUnique(result.ReviewArtifact.NextActions, "Attach this dependency bundle to its Java project. These inert asset fingerprints do not provision a runner or establish effective classpath bindings.")
	}
}

func sourceAssetRole(name, content string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".jar":
		return "java_library"
	case ".class":
		return "java_bytecode"
	case ".exe", ".dll", ".so", ".dylib", ".bat", ".cmd":
		return "executable_support"
	case ".store", ".jks", ".keystore", ".p12", ".pfx", ".der", ".pem", ".crt", ".cer", ".p7b", ".p7c", ".key", ".db", ".sqlite", ".sqlite3", ".mdb":
		return "opaque_data_or_credential_store"
	case ".java":
		return "java_source"
	case ".usr":
		return "project_settings"
	case ".cfg", ".usp", ".ini", ".properties":
		return "source_settings"
	case ".prm":
		return "parameter_settings"
	case ".dat":
		return "parameter_data"
	case ".log", ".bak", ".tmp":
		return "prior_run_or_backup_evidence"
	}
	if sourceview.IsOpaqueAsset(name, content) {
		return "opaque_support_asset"
	}
	return "supporting_source_evidence"
}

var dependencyEntryPointPattern = regexp.MustCompile(`(?i)\b(?:main|action|run|init|end)\s*\(`)

func dependencyBundleWithoutProject(files map[string]string) bool {
	classes := make(map[string]bool)
	for name := range files {
		if strings.EqualFold(path.Ext(name), ".class") {
			classes[strings.ToLower(strings.TrimSuffix(name, path.Ext(name)))] = true
		}
	}
	for name, content := range files {
		switch strings.ToLower(path.Ext(name)) {
		case ".jar", ".class", ".exe", ".dll", ".so", ".dylib", ".txt", ".store", ".db":
		case ".java":
			paired := classes[strings.ToLower(strings.TrimSuffix(name, path.Ext(name)))]
			if !paired || strings.Contains(strings.ToLower(content), "lrapi") || dependencyEntryPointPattern.MatchString(content) {
				return false
			}
		default:
			return false
		}
	}
	return true
}
