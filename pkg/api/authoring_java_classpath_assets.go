package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/fileasset"
	"github.com/jasonczech/apexload/pkg/javarunner"
)

const maxJavaClasspathReviewTotalBytes int64 = 512 << 20

// Review attachment is a narrowly authorized delta to an imported Java draft.
// It does not change the credential fingerprint contract: compare against the
// trusted baseline with its original assets, then reattach verified bindings.
func (s *Server) prepareAuthoringJavaClasspathAttestation(submitted, trusted *dsl.Script) (*dsl.Script, []dsl.JavaClasspathAsset, error) {
	if submitted == nil || trusted == nil || !strings.EqualFold(strings.TrimSpace(submitted.Protocol), "java_vuser") || !strings.EqualFold(strings.TrimSpace(trusted.Protocol), "java_vuser") || normalizeProjectID(submitted.ProjectID) != normalizeProjectID(trusted.ProjectID) {
		return nil, nil, errImportDraftAttestationInvalid
	}
	assets := append([]dsl.JavaClasspathAsset(nil), submitted.Config.JavaVuser.ClasspathAssets...)
	unchanged := len(submitted.Config.JavaVuser.ClasspathAssets) == 0 && len(trusted.Config.JavaVuser.ClasspathAssets) == 0 ||
		reflect.DeepEqual(submitted.Config.JavaVuser.ClasspathAssets, trusted.Config.JavaVuser.ClasspathAssets)
	if !config.JavaClasspathUploadEnabled() {
		// Existing attested evidence remains reviewable after an operator turns
		// uploads off. No binding delta or runner privilege is authorized.
		if !unchanged {
			return nil, nil, errImportDraftAttestationInvalid
		}
	} else if err := s.validateAuthoringJavaClasspathAssets(submitted, trusted.Config.JavaVuser.Classpath); err != nil {
		return nil, nil, errImportDraftAttestationInvalid
	}
	candidate := cloneScriptDefinition(submitted)
	if candidate == nil {
		return nil, nil, errImportDraftAttestationInvalid
	}
	candidate.Config.JavaVuser.ClasspathAssets = append([]dsl.JavaClasspathAsset(nil), trusted.Config.JavaVuser.ClasspathAssets...)
	return candidate, assets, nil
}

// Credential-set equality alone permits some ordinary import repairs. The
// authoring attachment exception must prove that every other executable field
// is still the exact server-owned baseline after secret reconciliation.
func verifyAuthoringJavaClasspathBaseline(submitted, trusted *dsl.Script) error {
	current, err := fullImportDraftExecutionDigest(submitted)
	if err != nil {
		return errImportDraftAttestationInvalid
	}
	baseline, err := fullImportDraftExecutionDigest(trusted)
	if err != nil || current != baseline {
		return errImportDraftAttestationInvalid
	}
	return nil
}

// A saved review's existing bindings remain inert evidence even if uploads
// are subsequently disabled. A PUT may change them only through the same
// exact-entry, project-owned content verification used by creation. Protocol
// transitions and activation cannot promote ignored or stale evidence.
func (s *Server) validateAuthoringJavaClasspathAssetMutation(submitted, trusted *dsl.Script) error {
	if submitted == nil {
		return fmt.Errorf("Java classpath attachment draft is required")
	}
	current := submitted.Config.JavaVuser.ClasspathAssets
	if trusted == nil && len(current) == 0 {
		return nil
	}
	if trusted != nil {
		prior := trusted.Config.JavaVuser.ClasspathAssets
		if len(current) == 0 && len(prior) == 0 {
			return nil
		}
		unchangedJavaFamily := strings.EqualFold(strings.TrimSpace(submitted.Protocol), "java_vuser") && strings.EqualFold(strings.TrimSpace(trusted.Protocol), "java_vuser")
		unchangedClasspath := len(submitted.Config.JavaVuser.Classpath) == 0 && len(trusted.Config.JavaVuser.Classpath) == 0 || reflect.DeepEqual(submitted.Config.JavaVuser.Classpath, trusted.Config.JavaVuser.Classpath)
		unchangedProject := normalizeProjectID(submitted.ProjectID) == normalizeProjectID(trusted.ProjectID)
		activating := submitted.Config.JavaVuser.Enabled && !trusted.Config.JavaVuser.Enabled
		if unchangedJavaFamily && unchangedClasspath && unchangedProject && !activating && reflect.DeepEqual(current, prior) {
			return nil
		}
	}
	if !config.JavaClasspathUploadEnabled() {
		return fmt.Errorf("Java classpath attachments are not available")
	}
	return s.validateAuthoringJavaClasspathAssets(submitted, submitted.Config.JavaVuser.Classpath)
}

func (s *Server) validateAuthoringJavaClasspathAssets(script *dsl.Script, classpath []string) error {
	if script == nil || len(script.Config.JavaVuser.ClasspathAssets) == 0 {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(script.Protocol), "java_vuser") || !config.JavaClasspathUploadEnabled() || len(script.Config.JavaVuser.ClasspathAssets) > javarunner.MaxClasspathEntries {
		return fmt.Errorf("Java classpath attachments are not available")
	}
	entries := make(map[string]bool, len(classpath))
	for _, entry := range classpath {
		entries[entry] = true
	}
	seen := make(map[string]bool)
	var total int64
	manifestDigest := scriptJavaSourceManifestDigest(script)
	for _, asset := range script.Config.JavaVuser.ClasspathAssets {
		if !entries[asset.Entry] || seen[asset.Entry] || asset.StagedAsset != "" || asset.SizeBytes < 4 || asset.SizeBytes > fileasset.MaxJarBytes || asset.SizeBytes > maxJavaClasspathReviewTotalBytes-total {
			return fmt.Errorf("Java classpath attachment does not match an exact declared entry or exceeds its limits")
		}
		seen[asset.Entry] = true
		total += asset.SizeBytes
		if err := s.verifyAuthoringJavaClasspathCapability(script.ProjectID, asset); err != nil {
			return err
		}
		if asset.CompiledFrom == "" {
			continue
		}
		// A compiled jar is the script's own classes, built from the source
		// manifest this draft was reviewed with.
		if asset.Entry != enterprisearchive.JavaVuserScriptClassesEntry || manifestDigest != "" && asset.CompiledFrom != manifestDigest {
			return fmt.Errorf("Java classpath attachment was compiled from different source than this script's")
		}
		if err := verifyJavaCompiledProvenance(asset); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) verifyAuthoringJavaClasspathCapability(projectID string, asset dsl.JavaClasspathAsset) error {
	invalid := func() error { return fmt.Errorf("Java classpath attachment is not a valid project-owned uploaded JAR") }
	decoded, err := hex.DecodeString(asset.SHA256)
	if err != nil || len(decoded) != sha256.Size || asset.SHA256 != strings.ToLower(asset.SHA256) {
		return invalid()
	}
	ref, err := url.Parse(asset.StorageRef)
	if err != nil || ref.Scheme != "file" || ref.Host != "" || ref.User != nil || ref.RawQuery != "" || ref.Fragment != "" || !filepath.IsAbs(ref.Path) || filepath.Clean(ref.Path) != ref.Path {
		return invalid()
	}
	projectDir, err := fileasset.ProjectAssetDir(s.importAssetDir, normalizeProjectID(projectID))
	if err != nil {
		return invalid()
	}
	rel, err := filepath.Rel(projectDir, ref.Path)
	if err != nil || filepath.IsAbs(rel) {
		return invalid()
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 4 || safeImportAssetSegment(parts[0]) != parts[0] || parts[1] != asset.SHA256[:2] || parts[3] != asset.SHA256 {
		return invalid()
	}
	capability, err := hex.DecodeString(parts[2])
	if err != nil || len(capability) != 32 {
		return invalid()
	}
	// os.Root confines symlinks as well as lexical paths to this exact project's
	// server-owned upload namespace. Generic allowed file roots are insufficient.
	root, err := os.OpenRoot(projectDir)
	if err != nil {
		return invalid()
	}
	defer root.Close()
	file, err := root.Open(rel)
	if err != nil {
		return invalid()
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != asset.SizeBytes {
		return invalid()
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(file, header); err != nil || !reflect.DeepEqual(header, jarLocalFileHeaderSignature) {
		return invalid()
	}
	hasher := sha256.New()
	_, _ = hasher.Write(header)
	copied, err := io.Copy(hasher, io.LimitReader(file, asset.SizeBytes-4+1))
	if err != nil || copied != asset.SizeBytes-4 || hex.EncodeToString(hasher.Sum(nil)) != asset.SHA256 {
		return invalid()
	}
	return nil
}
