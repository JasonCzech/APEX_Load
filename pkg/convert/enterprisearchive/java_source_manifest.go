package enterprisearchive

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/javarunner"
)

const (
	// JavaVuserSourceManifestMetadataKey is stored inside the server-produced
	// review artifact. It binds an approval to every full uploaded .java file
	// without placing unredacted source in the bounded UI preview.
	JavaVuserSourceManifestMetadataKey = "java_source_manifest"
	JavaVuserSourceManifestContract    = "apexload.java_vuser.source_manifest.v1"
)

type JavaVuserSourceManifest struct {
	Contract   string                        `json:"contract"`
	Digest     string                        `json:"digest"`
	FileCount  int                           `json:"file_count"`
	TotalBytes int64                         `json:"total_bytes"`
	Files      []JavaVuserSourceManifestFile `json:"files"`
}

type JavaVuserSourceManifestFile struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// JavaVuserSourceTexts returns every complete .java file keyed by its source
// manifest path, or nil when a name cannot be canonicalized or two names
// collide; the manifest is then absent too.
func JavaVuserSourceTexts(files map[string]string) map[string]string {
	javaFiles := make(map[string]string)
	for originalName, content := range files {
		separatorNormalized := strings.ReplaceAll(strings.TrimSpace(originalName), "\\", "/")
		if !strings.EqualFold(path.Ext(separatorNormalized), ".java") {
			continue
		}
		name, ok := CanonicalJavaVuserSourcePath(separatorNormalized)
		if !ok {
			return nil
		}
		for existing := range javaFiles {
			if strings.EqualFold(existing, name) {
				return nil
			}
		}
		javaFiles[name] = content
	}
	if len(javaFiles) == 0 {
		return nil
	}
	return javaFiles
}

// BuildJavaVuserSourceManifest hashes every complete Java source byte before
// sourceview applies redaction, file-count limits, or per-file truncation.
func BuildJavaVuserSourceManifest(files map[string]string) *JavaVuserSourceManifest {
	javaFiles := JavaVuserSourceTexts(files)
	names := make([]string, 0, len(javaFiles))
	for name := range javaFiles {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil
	}

	manifest := &JavaVuserSourceManifest{
		Contract:  JavaVuserSourceManifestContract,
		FileCount: len(names),
		Files:     make([]JavaVuserSourceManifestFile, 0, len(names)),
	}
	for _, name := range names {
		content := []byte(javaFiles[name])
		sum := sha256.Sum256(content)
		manifest.Files = append(manifest.Files, JavaVuserSourceManifestFile{
			Path:   name,
			Bytes:  int64(len(content)),
			SHA256: "sha256:" + hex.EncodeToString(sum[:]),
		})
		manifest.TotalBytes += int64(len(content))
	}
	manifest.Digest = javaVuserSourceManifestDigest(manifest.Files)
	return manifest
}

// DecodeJavaVuserSourceManifest accepts both the in-process typed value and its
// ordinary JSON map representation, then validates the canonical digest.
func DecodeJavaVuserSourceManifest(value any) (*JavaVuserSourceManifest, error) {
	var manifest JavaVuserSourceManifest
	switch typed := value.(type) {
	case *JavaVuserSourceManifest:
		if typed == nil {
			return nil, fmt.Errorf("Java source manifest is missing")
		}
		manifest = *typed
		manifest.Files = append([]JavaVuserSourceManifestFile(nil), typed.Files...)
	case JavaVuserSourceManifest:
		manifest = typed
		manifest.Files = append([]JavaVuserSourceManifestFile(nil), typed.Files...)
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode Java source manifest: %w", err)
		}
		if err := json.Unmarshal(data, &manifest); err != nil {
			return nil, fmt.Errorf("decode Java source manifest: %w", err)
		}
	}
	if err := ValidateJavaVuserSourceManifest(&manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func ValidateJavaVuserSourceManifest(manifest *JavaVuserSourceManifest) error {
	if manifest == nil || manifest.Contract != JavaVuserSourceManifestContract {
		return fmt.Errorf("Java source manifest contract is invalid")
	}
	if manifest.FileCount <= 0 || manifest.FileCount != len(manifest.Files) {
		return fmt.Errorf("Java source manifest file count is invalid")
	}
	var totalBytes int64
	seen := make(map[string]struct{}, len(manifest.Files))
	previous := ""
	for _, file := range manifest.Files {
		canonicalPath, ok := CanonicalJavaVuserSourcePath(file.Path)
		if !ok || file.Path != canonicalPath || file.Bytes < 0 {
			return fmt.Errorf("Java source manifest file entry is invalid")
		}
		if previous != "" && canonicalPath <= previous {
			return fmt.Errorf("Java source manifest file order is not canonical")
		}
		previous = canonicalPath
		if _, exists := seen[strings.ToLower(canonicalPath)]; exists {
			return fmt.Errorf("Java source manifest contains duplicate paths")
		}
		seen[strings.ToLower(canonicalPath)] = struct{}{}
		if !validSHA256Reference(file.SHA256) {
			return fmt.Errorf("Java source manifest file digest is invalid")
		}
		totalBytes += file.Bytes
	}
	if manifest.TotalBytes != totalBytes {
		return fmt.Errorf("Java source manifest byte count is invalid")
	}
	if manifest.Digest != javaVuserSourceManifestDigest(manifest.Files) {
		return fmt.Errorf("Java source manifest digest is invalid")
	}
	return nil
}

// CanonicalJavaVuserSourcePath returns the one relative, slash-separated path
// representation accepted by source manifests and approval-time review
// matching. Aliasing spellings are rejected instead of cleaned silently.
func CanonicalJavaVuserSourcePath(value string) (string, bool) {
	raw := strings.TrimSpace(value)
	if raw == "" || strings.ContainsRune(raw, '\x00') || strings.Contains(raw, "\\") {
		return "", false
	}
	if strings.HasPrefix(raw, "/") || hasWindowsDrivePrefix(raw) {
		return "", false
	}
	cleaned := path.Clean(raw)
	if cleaned != raw || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	if !strings.EqualFold(path.Ext(cleaned), ".java") {
		return "", false
	}
	return cleaned, true
}

func hasWindowsDrivePrefix(value string) bool {
	if len(value) < 2 || value[1] != ':' {
		return false
	}
	first := value[0]
	return (first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z')
}

func javaVuserSourceManifestDigest(files []JavaVuserSourceManifestFile) string {
	canonical, _ := json.Marshal(files)
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validSHA256Reference(value string) bool {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "sha256:") {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && len(decoded) == sha256.Size
}

// JavaVuserScriptClassesEntry is the classpath entry an imported Java Vuser's
// own classes take. VuGen compiles a script's .java files before replay and
// puts the script directory on the classpath; APEX puts a jar here instead,
// compiled from the reviewed source or attached by the user. Unbound, the
// entry fails closed at launch like any other missing classpath entry.
const JavaVuserScriptClassesEntry = "apexload-script-classes.jar"

const javaVuserScriptClassesNextAction = "Compile the script's Java source under Resolve Java Dependencies, or attach a jar of its compiled classes to the " + JavaVuserScriptClassesEntry + " classpath entry."

// addJavaVuserScriptClassesEntry puts the script-classes entry first on each
// Java Vuser draft's classpath and keeps the complete sources for a compile.
func addJavaVuserScriptClassesEntry(result *ConvertResult, files map[string]string) {
	texts := JavaVuserSourceTexts(files)
	if result == nil || len(texts) == 0 {
		return
	}
	added := false
	scripts := append([]*dsl.Script{result.Script}, result.Scripts...)
	for index, script := range scripts {
		if script == nil || slices.Contains(scripts[:index], script) || !strings.EqualFold(strings.TrimSpace(script.Protocol), "java_vuser") {
			continue
		}
		classpath := script.Config.JavaVuser.Classpath
		if slices.Contains(classpath, JavaVuserScriptClassesEntry) {
			added = true
			continue
		}
		if len(classpath) >= javarunner.MaxClasspathEntries {
			result.Warnings = append(result.Warnings, "The Java classpath already holds the maximum number of entries, so no entry was added for the script's own classes; replace an entry with a jar of the compiled script classes.")
			continue
		}
		script.Config.JavaVuser.Classpath = append([]string{JavaVuserScriptClassesEntry}, classpath...)
		added = true
	}
	if !added {
		return
	}
	result.JavaSourceTexts = texts
	if result.ReviewArtifact != nil {
		result.ReviewArtifact.NextActions = appendUnique(result.ReviewArtifact.NextActions, javaVuserScriptClassesNextAction)
	}
}
