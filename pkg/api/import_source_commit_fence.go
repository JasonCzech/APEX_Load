package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

const protectedImportSourceCommitFenceVersion = 1
const protectedImportSourceCommitFenceMaxPredecessors = 128

type protectedImportSourceCommitFence struct {
	Version                int      `json:"version"`
	OwnerDigest            string   `json:"owner_digest"`
	SourceProfile          string   `json:"source_profile"`
	SourceSHA256           string   `json:"source_sha256"`
	SourceSizeBytes        int64    `json:"source_size_bytes"`
	ScriptFingerprint      string   `json:"script_fingerprint"`
	SupersededOwnerDigests []string `json:"superseded_owner_digests,omitempty"`
}

func protectedImportSourceCommitOwnerDigest(profile, kind, capability string) string {
	profile = strings.ToLower(strings.TrimSpace(profile))
	kind = strings.ToLower(strings.TrimSpace(kind))
	capability = strings.TrimSpace(capability)
	if profile == "" || kind == "" || capability == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(
		"apexload:protected-import-source-commit-owner:v1\x00" +
			profile + "\x00" + kind + "\x00" + capability,
	))
	return hex.EncodeToString(sum[:])
}

func newProtectedImportSourceCommitFence(
	ownerDigest string,
	sourceProfile string,
	source []byte,
	scriptFingerprint string,
	supersededOwnerDigests ...string,
) (protectedImportSourceCommitFence, error) {
	ownerDigest = strings.ToLower(strings.TrimSpace(ownerDigest))
	sourceProfile = strings.ToLower(strings.TrimSpace(sourceProfile))
	scriptFingerprint = strings.ToLower(strings.TrimSpace(scriptFingerprint))
	if len(source) == 0 {
		return protectedImportSourceCommitFence{}, errors.New("protected import source is required")
	}
	if !isSHA256Hex(ownerDigest) || sourceProfile == "" ||
		!isSHA256Hex(scriptFingerprint) {
		return protectedImportSourceCommitFence{}, errors.New("protected import source commit fence is invalid")
	}
	superseded, err := normalizeProtectedImportSourceCommitOwners(
		ownerDigest,
		supersededOwnerDigests,
	)
	if err != nil {
		return protectedImportSourceCommitFence{}, err
	}
	sourceSum := sha256.Sum256(source)
	return protectedImportSourceCommitFence{
		Version:                protectedImportSourceCommitFenceVersion,
		OwnerDigest:            ownerDigest,
		SourceProfile:          sourceProfile,
		SourceSHA256:           hex.EncodeToString(sourceSum[:]),
		SourceSizeBytes:        int64(len(source)),
		ScriptFingerprint:      scriptFingerprint,
		SupersededOwnerDigests: superseded,
	}, nil
}

func attachProtectedImportSourceCommitFence(
	script *dsl.Script,
	fence protectedImportSourceCommitFence,
) {
	if script == nil {
		return
	}
	ensureScriptImportPreserved(script, fence.SourceProfile)[storage.ImportSourceCommitFenceMetadataKey] = fence
}

var stripProtectedImportSourceCommitFence = importreview.StripProtectedImportSourceCommitFence

func readProtectedImportSourceCommitFence(
	script *dsl.Script,
) (protectedImportSourceCommitFence, bool, error) {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil ||
		script.Metadata.Import.Preserved == nil {
		return protectedImportSourceCommitFence{}, false, nil
	}
	raw, exists := script.Metadata.Import.Preserved[storage.ImportSourceCommitFenceMetadataKey]
	if !exists {
		return protectedImportSourceCommitFence{}, false, nil
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return protectedImportSourceCommitFence{}, true, fmt.Errorf("encode protected import source commit fence: %w", err)
	}
	var fence protectedImportSourceCommitFence
	if err := json.Unmarshal(payload, &fence); err != nil {
		return protectedImportSourceCommitFence{}, true, fmt.Errorf("decode protected import source commit fence: %w", err)
	}
	fence.OwnerDigest = strings.ToLower(strings.TrimSpace(fence.OwnerDigest))
	fence.SourceProfile = strings.ToLower(strings.TrimSpace(fence.SourceProfile))
	fence.SourceSHA256 = strings.ToLower(strings.TrimSpace(fence.SourceSHA256))
	fence.ScriptFingerprint = strings.ToLower(strings.TrimSpace(fence.ScriptFingerprint))
	superseded, err := normalizeProtectedImportSourceCommitOwners(
		fence.OwnerDigest,
		fence.SupersededOwnerDigests,
	)
	if err != nil {
		return protectedImportSourceCommitFence{}, true, err
	}
	fence.SupersededOwnerDigests = superseded
	if fence.Version != protectedImportSourceCommitFenceVersion ||
		!isSHA256Hex(fence.OwnerDigest) ||
		fence.SourceProfile == "" ||
		!isSHA256Hex(fence.SourceSHA256) ||
		fence.SourceSizeBytes <= 0 ||
		!isSHA256Hex(fence.ScriptFingerprint) {
		return protectedImportSourceCommitFence{}, true, errors.New("protected import source commit fence is invalid")
	}
	return fence, true, nil
}

func normalizeProtectedImportSourceCommitOwners(
	currentOwner string,
	owners []string,
) ([]string, error) {
	if len(owners) > protectedImportSourceCommitFenceMaxPredecessors {
		return nil, errors.New("protected import source commit fence predecessor limit exceeded")
	}
	currentOwner = strings.ToLower(strings.TrimSpace(currentOwner))
	seen := make(map[string]struct{}, len(owners))
	normalized := make([]string, 0, len(owners))
	for _, owner := range owners {
		owner = strings.ToLower(strings.TrimSpace(owner))
		if !isSHA256Hex(owner) {
			return nil, errors.New("protected import source commit fence predecessor is invalid")
		}
		if owner == currentOwner {
			continue
		}
		if _, duplicate := seen[owner]; duplicate {
			continue
		}
		seen[owner] = struct{}{}
		normalized = append(normalized, owner)
	}
	return normalized, nil
}

func protectedImportSourceCommitFenceContainsOwner(
	fence protectedImportSourceCommitFence,
	ownerDigest string,
) bool {
	ownerDigest = strings.ToLower(strings.TrimSpace(ownerDigest))
	if ownerDigest == "" {
		return false
	}
	if fence.OwnerDigest == ownerDigest {
		return true
	}
	for _, predecessor := range fence.SupersededOwnerDigests {
		if predecessor == ownerDigest {
			return true
		}
	}
	return false
}

func (s *Server) retainedImportSourceCommitOwnerWasPublished(
	scriptID string,
	projectID string,
	ownerDigest string,
	sourceProfile string,
	source []byte,
) (bool, error) {
	revisions, ok := s.store.(storage.ImportSourceStore)
	if !ok || revisions == nil || len(source) == 0 {
		return false, nil
	}
	sourceSum := sha256.Sum256(source)
	sourceSHA256 := hex.EncodeToString(sourceSum[:])
	rows, err := revisions.ListImportSourceRevisions(
		strings.TrimSpace(scriptID),
		normalizeProjectID(projectID),
	)
	if err != nil {
		return false, err
	}
	for _, revision := range rows {
		if revision == nil || revision.CanonicalScript == nil {
			continue
		}
		fence, exists, fenceErr := readProtectedImportSourceCommitFence(revision.CanonicalScript)
		if fenceErr != nil {
			return false, fenceErr
		}
		if exists &&
			fence.OwnerDigest == ownerDigest &&
			strings.EqualFold(fence.SourceProfile, sourceProfile) &&
			fence.SourceSHA256 == sourceSHA256 &&
			fence.SourceSizeBytes == int64(len(source)) {
			return true, nil
		}
	}
	return false, nil
}

func (s *Server) retainedImportSourceCommitFenceMatchesActive(
	scriptID string,
	projectID string,
	fence protectedImportSourceCommitFence,
) bool {
	revisions, ok := s.store.(storage.ImportSourceStore)
	if !ok || revisions == nil {
		return false
	}
	projectID = normalizeProjectID(projectID)
	active, err := revisions.GetActiveImportSourceRevision(
		strings.TrimSpace(scriptID),
		projectID,
	)
	if err != nil || active == nil || active.CanonicalScript == nil {
		return false
	}
	activeFence, exists, err := readProtectedImportSourceCommitFence(active.CanonicalScript)
	if err != nil || !exists ||
		activeFence.OwnerDigest != fence.OwnerDigest ||
		activeFence.SourceProfile != fence.SourceProfile ||
		activeFence.SourceSHA256 != fence.SourceSHA256 ||
		activeFence.SourceSizeBytes != fence.SourceSizeBytes {
		return false
	}
	artifact, err := revisions.GetImportSourceArtifact(active.RawArtifactID, projectID)
	return err == nil && artifact != nil &&
		artifact.ProjectID == projectID &&
		strings.EqualFold(strings.TrimSpace(artifact.ArtifactKind), "raw") &&
		strings.EqualFold(strings.TrimSpace(artifact.SHA256), fence.SourceSHA256) &&
		artifact.SizeBytes == fence.SourceSizeBytes
}

func isSHA256Hex(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}
