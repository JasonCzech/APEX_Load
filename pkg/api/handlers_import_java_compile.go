package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/javasidecar"
	javavuser "github.com/jasonczech/apexload/pkg/protocol/java_vuser"
)

// Compiling an imported Java Vuser's source.
//
// The importer keeps a Java Vuser's complete .java files and puts an
// apexload-script-classes.jar entry first on its classpath. This route
// compiles those reviewed files on the operator's Java runner and stores the
// jar exactly as an uploaded one, so the review binds it to that entry as a
// classpath asset. The asset names the source manifest it was compiled from;
// the server keeps that provenance beside the jar and checks it on save.

const (
	// javaSourceTextKind keeps imported Java source beside the import assets.
	// Unlike source-review texts, no route serves it to readers.
	javaSourceTextKind = "java-source"
	// javaSourceTextMaxPerImport bounds the source one import keeps.
	javaSourceTextMaxPerImport = javarunner.MaxCompileSourceBytes
	// javaCompileMaxBodyBytes bounds the compile request, which names the
	// sources and classpath but carries no file content.
	javaCompileMaxBodyBytes int64 = 1 << 20
	// javaCompileProvenanceSuffix names the provenance file beside a jar.
	javaCompileProvenanceSuffix = ".compiled-from"
)

// retainJavaSourceTexts keeps the import's Java source, content-addressed per
// project, for a later compile. Nothing is kept while classpath uploads are
// off, since a compiled jar is bound like an uploaded one.
func (s *Server) retainJavaSourceTexts(ctx context.Context, projectID string, texts map[string]string) {
	projectID = normalizeProjectID(projectID)
	if s == nil || len(texts) == 0 || strings.TrimSpace(projectID) == "" || !config.JavaClasspathUploadEnabled() {
		return
	}
	total := 0
	for _, text := range texts {
		total += len(text)
	}
	if total > javaSourceTextMaxPerImport {
		slog.Warn("imported Java source was not kept for compiling; it exceeds the compile limit", "project_id", projectID, "bytes", total)
		return
	}
	for name, text := range texts {
		if len(text) > sourceview.MaxFullTextBytes {
			continue
		}
		sum := sha256.Sum256([]byte(text))
		if err := s.storeProjectText(ctx, javaSourceTextKind, projectID, hex.EncodeToString(sum[:]), text); err != nil {
			slog.Warn("imported Java source was not kept for compiling", "project_id", projectID, "path", name, "error", err)
		}
	}
}

type javaCompileRequest struct {
	ProjectID      string                                     `json:"project_id,omitempty"`
	TargetClass    string                                     `json:"target_class"`
	SourceManifest *enterprisearchive.JavaVuserSourceManifest `json:"source_manifest"`
	JavaVuser      javaCompileSettings                        `json:"java_vuser"`
}

// javaCompileSettings are the Java VM settings of the reviewed draft.
type javaCompileSettings struct {
	JDKSelection    string                   `json:"jdk_selection,omitempty"`
	JDKHome         string                   `json:"jdk_home,omitempty"`
	Classpath       []string                 `json:"classpath"`
	ClasspathAssets []dsl.JavaClasspathAsset `json:"classpath_assets,omitempty"`
}

type javaCompileResponse struct {
	javaClasspathAssetUploadResponse
	CompiledFrom         string                         `json:"compiled_from"`
	Classes              []string                       `json:"classes"`
	Diagnostics          []javarunner.CompileDiagnostic `json:"diagnostics,omitempty"`
	UnavailableClasspath []string                       `json:"unavailable_classpath,omitempty"`
	CompilerVersion      string                         `json:"compiler_version,omitempty"`
	Release              int                            `json:"release,omitempty"`
}

type javaCompileFailureResponse struct {
	Error                string                         `json:"error"`
	Code                 string                         `json:"code"`
	Diagnostics          []javarunner.CompileDiagnostic `json:"diagnostics,omitempty"`
	UnavailableClasspath []string                       `json:"unavailable_classpath,omitempty"`
}

// handleCompileJavaSource compiles an imported Java Vuser's reviewed source.
func (s *Server) handleCompileJavaSource(w http.ResponseWriter, r *http.Request) {
	if !config.JavaClasspathUploadEnabled() {
		writeError(w, http.StatusNotFound, "Java source compile is not enabled on this controller. An operator must set APEXLOAD_JAVA_VUSER_CLASSPATH_UPLOAD_ENABLED=true, or attach a jar of the compiled classes instead.")
		return
	}
	var request javaCompileRequest
	if err := decodeRequestBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	projectID := normalizeProjectID(firstNonEmpty(strings.TrimSpace(r.URL.Query().Get("project_id")), request.ProjectID))
	if authorizedProjectID, scoped := authorizedProjectFromRequest(r); scoped {
		projectID = authorizedProjectID
	}
	if strings.TrimSpace(projectID) == "" {
		writeError(w, http.StatusBadRequest, "project_id is required")
		return
	}
	manifest, err := enterprisearchive.DecodeJavaVuserSourceManifest(request.SourceManifest)
	if err != nil || manifest.FileCount > javarunner.MaxCompileSources || !javarunner.ValidPooledTargetClass(request.TargetClass) {
		writeError(w, http.StatusBadRequest, "a valid Java source manifest and target class are required")
		return
	}
	entryIndex := slices.Index(request.JavaVuser.Classpath, enterprisearchive.JavaVuserScriptClassesEntry)
	if entryIndex < 0 || len(request.JavaVuser.Classpath) > javarunner.MaxClasspathEntries {
		writeError(w, http.StatusBadRequest, "the classpath has no "+enterprisearchive.JavaVuserScriptClassesEntry+" entry for the compiled classes; re-import the archive")
		return
	}
	// A jar compiled earlier is replaced, never compiled against.
	assets := make([]dsl.JavaClasspathAsset, 0, len(request.JavaVuser.ClasspathAssets))
	for _, asset := range request.JavaVuser.ClasspathAssets {
		if asset.Entry != enterprisearchive.JavaVuserScriptClassesEntry {
			assets = append(assets, asset)
		}
	}
	compileClasspath := slices.Delete(slices.Clone(request.JavaVuser.Classpath), entryIndex, entryIndex+1)
	check := &dsl.Script{Protocol: "java_vuser", ProjectID: projectID}
	check.Config.JavaVuser.ClasspathAssets = assets
	if err := s.validateAuthoringJavaClasspathAssets(check, compileClasspath); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sources := make([]javasidecar.CompileSourceFile, 0, len(manifest.Files))
	for _, file := range manifest.Files {
		text, err := s.loadProjectText(r.Context(), javaSourceTextKind, projectID, strings.TrimPrefix(file.SHA256, "sha256:"))
		if err != nil || int64(len(text)) != file.Bytes {
			writeError(w, http.StatusConflict, "the reviewed Java source is no longer available on this controller; re-import the archive to compile it")
			return
		}
		sources = append(sources, javasidecar.CompileSourceFile{Path: file.Path, Content: []byte(text)})
	}
	refs := make([]javasidecar.ClasspathAssetRef, 0, len(assets))
	for _, asset := range assets {
		refs = append(refs, javasidecar.ClasspathAssetRef{Entry: asset.Entry, SHA256: asset.SHA256, StorageRef: asset.StorageRef})
	}
	result, err := compileJavaSource(r.Context(), config.LoadJavaVuserOperatorConfig(), javasidecar.CompileRequest{
		Runtime:     javasidecar.RuntimeSettings{JDKSelection: request.JavaVuser.JDKSelection, JDKHome: request.JavaVuser.JDKHome, Classpath: compileClasspath, ClasspathAssets: refs},
		TargetClass: request.TargetClass,
		Sources:     sources,
	})
	unavailable := make([]string, 0, len(result.UnavailableClasspath))
	for _, index := range result.UnavailableClasspath {
		if index >= 0 && index < len(compileClasspath) {
			unavailable = append(unavailable, compileClasspath[index])
		}
	}
	if err != nil {
		status, message, code := javaCompileRefusal(err)
		writeJSON(w, status, javaCompileFailureResponse{Error: message, Code: code})
		return
	}
	if result.ErrorCode != "" {
		writeJSON(w, http.StatusUnprocessableEntity, javaCompileFailureResponse{Error: javaCompileFailureMessage(result.ErrorCode), Code: result.ErrorCode, Diagnostics: result.Diagnostics, UnavailableClasspath: unavailable})
		return
	}
	sum := sha256.Sum256(result.Jar)
	digest := hex.EncodeToString(sum[:])
	if digest != result.SHA256 || len(result.Jar) < len(jarLocalFileHeaderSignature) || len(result.Jar) > javarunner.MaxCompiledJarBytes || !bytes.HasPrefix(result.Jar, jarLocalFileHeaderSignature) {
		writeError(w, http.StatusBadGateway, "the Java runner returned an invalid jar")
		return
	}
	importID := r.PathValue("id")
	stored, err := storeImportAssetCapabilityFromReader(s.importAssetDir, projectID, importID, digest, bytes.NewReader(result.Jar))
	if err == nil {
		err = os.WriteFile(stored+javaCompileProvenanceSuffix, []byte(manifest.Digest), 0o600)
		if err != nil {
			_ = os.Remove(stored)
		}
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store the compiled jar")
		return
	}
	writeJSON(w, http.StatusCreated, javaCompileResponse{
		javaClasspathAssetUploadResponse: javaClasspathAssetUploadResponse{
			ImportID:    safeImportAssetSegment(firstNonEmpty(importID, "import")),
			Entry:       enterprisearchive.JavaVuserScriptClassesEntry,
			FileName:    javaCompiledJarName(request.TargetClass),
			ContentType: "application/java-archive",
			SizeBytes:   int64(len(result.Jar)),
			SHA256:      digest,
			StorageRef:  (&url.URL{Scheme: "file", Path: stored}).String(),
			UploadedAt:  time.Now().UTC(),
		},
		CompiledFrom:         manifest.Digest,
		Classes:              result.Classes,
		Diagnostics:          result.Diagnostics,
		UnavailableClasspath: unavailable,
		CompilerVersion:      result.CompilerVersion,
		Release:              result.Release,
	})
}

// compileJavaSource is the runner call, replaceable in tests.
var compileJavaSource = javavuser.CompileSource

func javaCompiledJarName(target string) string {
	return sanitizeFilename(target + "-compiled.jar")
}

// javaCompileRefusal maps a runner's refusal to a status and a remedy.
func javaCompileRefusal(err error) (int, string, string) {
	var remote *javasidecar.RemoteError
	if !errors.As(err, &remote) {
		return http.StatusBadGateway, "The Java runner is unavailable. Start it, or attach a jar of the compiled classes instead.", "runner_unavailable"
	}
	code := remote.Code
	switch {
	case code == "runner_disabled":
		return http.StatusServiceUnavailable, "The Java runner is not enabled on this controller, so it cannot compile. Attach a jar of the compiled classes instead.", code
	case code == javarunner.CompileCompilerUnavailable:
		return http.StatusConflict, javaCompileFailureMessage(code), code
	case code == "compile_capacity_unavailable" || code == "process_capacity_unavailable":
		return http.StatusTooManyRequests, "The Java runner is busy with another compile or with VUsers. Try again when it is free.", code
	case strings.HasPrefix(code, "jdk_"):
		return http.StatusConflict, fmt.Sprintf("The script's Java runtime is not available on the Java runner (%s). Fix the Java VM settings, then compile again.", code), code
	case strings.HasPrefix(code, "classpath_"):
		return http.StatusConflict, fmt.Sprintf("The Java runner could not prepare the script's classpath (%s).", code), code
	case code == "harness_unavailable":
		return http.StatusConflict, "The Java runner has no harness jar to compile the lr API against. An operator must set APEXLOAD_JAVA_VUSER_HARNESS_JAR.", code
	case code == javarunner.CompileSourcesInvalid:
		return http.StatusBadRequest, javaCompileFailureMessage(code), code
	default:
		return http.StatusBadGateway, fmt.Sprintf("The Java runner refused the compile (%s).", code), code
	}
}

func javaCompileFailureMessage(code string) string {
	switch code {
	case javarunner.CompileFailed:
		return "The script's Java source did not compile. Fix the errors below, or attach the missing dependency jars, then compile again."
	case javarunner.CompileTargetMissing:
		return "The source compiled but did not produce the script's Actions class."
	case javarunner.CompileCompilerUnavailable:
		return "The script's Java runtime has no compiler (javac). Select a JDK in the Java VM settings, or have an operator set APEXLOAD_JAVA_VUSER_COMPILER_JDK_HOME."
	case javarunner.CompileOutputTooLarge:
		return "The compiled classes exceed the jar size limit."
	case javarunner.CompileTimedOut:
		return "The compile did not finish in time."
	case javarunner.CompileSourcesInvalid:
		return "The script's Java source files cannot be compiled as named."
	default:
		return fmt.Sprintf("The compile failed (%s).", code)
	}
}

// verifyJavaCompiledProvenance checks that a compiled jar's binding names the
// manifest the server recorded when it compiled the jar.
func verifyJavaCompiledProvenance(asset dsl.JavaClasspathAsset) error {
	ref, err := url.Parse(asset.StorageRef)
	if err != nil || ref.Scheme != "file" || !filepath.IsAbs(ref.Path) {
		return fmt.Errorf("Java classpath attachment is not a valid compiled jar")
	}
	recorded, err := os.ReadFile(filepath.Clean(ref.Path) + javaCompileProvenanceSuffix)
	if err != nil || len(recorded) > 128 || string(recorded) != asset.CompiledFrom {
		return fmt.Errorf("Java classpath attachment was not compiled from the source it names")
	}
	return nil
}

// scriptJavaSourceManifestDigest is the reviewed source manifest digest an
// imported Java Vuser carries, or "".
func scriptJavaSourceManifestDigest(script *dsl.Script) string {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		return ""
	}
	manifest, err := importedJavaVuserSourceManifest(script.Metadata.Import.Preserved)
	if err != nil {
		return ""
	}
	return manifest.Digest
}
