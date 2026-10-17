package api

// archive_integrity.go — re-verification of an archived run against the SHA-256
// hashes recorded per artifact in its manifest (see updateRunArchiveArtifactIntegrity
// in archive_writer.go). Recording the hashes at Finalize time is only half of
// tamper-evidence; this file recomputes each artifact's hash and compares it back
// to the manifest, classifying every artifact as match / mismatch (tampered) /
// missing (corrupt) and rolling those up into an overall verdict.

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/archivecontract"
	"github.com/jasonczech/apexload/pkg/storage"
)

const runArchiveIntegritySchemaVersion = "apexload.archive_integrity.v1"

// Overall archive integrity verdicts.
const (
	integrityStatusVerified        = "verified"         // every recorded artifact recomputed to its recorded SHA-256
	integrityStatusVerifiedPartial = "verified_partial" // all hashed artifacts match, but some carried no recorded hash to check
	integrityStatusTampered        = "tampered"         // at least one artifact's content no longer matches its recorded hash
	integrityStatusCorrupt         = "corrupt"          // at least one recorded artifact is missing/unreadable (and none mismatched)
	integrityStatusUnverifiable    = "unverifiable"     // nothing could be checked (no manifest, no recorded hashes)
)

// Per-artifact integrity verdicts.
const (
	integrityArtifactMatch        = "match"
	integrityArtifactMismatch     = "mismatch"
	integrityArtifactMissing      = "missing"
	integrityArtifactInvalid      = "invalid"
	integrityArtifactUnverifiable = "unverifiable"
)

// RunArchiveIntegrityArtifact is the verification outcome for a single archived
// artifact: the hash the manifest recorded versus the hash recomputed now.
type RunArchiveIntegrityArtifact = archivecontract.RunArchiveIntegrityArtifact

// RunArchiveIntegrityReport is the rolled-up result of re-verifying every artifact
// recorded in an archive manifest against its on-disk content.
type RunArchiveIntegrityReport = archivecontract.RunArchiveIntegrityReport

// verifyRunArchiveIntegrity recomputes the SHA-256 of every artifact the manifest
// recorded and compares it to the stored hash. It never returns an error for a
// tampered or corrupt archive — that is the expected, reportable outcome; the
// verdict lives in the returned report. The archive root comes from the record so
// artifact names resolve under the same path-confinement guard the download path
// uses (archiveArtifactPath).
func verifyRunArchiveIntegrity(record *storage.RunArchiveRecord, manifest *RunArchiveManifest) *RunArchiveIntegrityReport {
	report := &RunArchiveIntegrityReport{
		SchemaVersion: runArchiveIntegritySchemaVersion,
		VerifiedAt:    time.Now().UTC(),
	}
	if manifest != nil {
		report.TestID = manifest.TestID
	}
	root := ""
	if record != nil {
		root = record.ArchivePath
	}

	if manifest == nil {
		report.Status = integrityStatusUnverifiable
		report.Summary = "Archive manifest is missing; integrity cannot be verified."
		return report
	}

	report.Total = len(manifest.Artifacts)
	verifyRunArchiveManifestTrust(record, report)
	for _, artifact := range manifest.Artifacts {
		entry := RunArchiveIntegrityArtifact{
			Name:           artifact.Name,
			ExpectedSHA256: strings.TrimSpace(artifact.SHA256),
			SizeBytes:      artifact.SizeBytes,
		}
		if entry.ExpectedSHA256 == "" {
			entry.Status = integrityArtifactUnverifiable
			entry.Detail = "manifest recorded no SHA-256 for this artifact"
			report.Unverifiable++
			report.Artifacts = append(report.Artifacts, entry)
			continue
		}

		path, err := archiveArtifactPath(root, artifact.Name)
		if err != nil {
			entry.Status = integrityArtifactInvalid
			entry.Detail = "manifest artifact path is invalid: " + err.Error()
			report.Mismatched++
			report.Artifacts = append(report.Artifacts, entry)
			continue
		}
		info, statErr := os.Stat(path)
		if statErr != nil {
			entry.Status = integrityArtifactMissing
			if os.IsNotExist(statErr) {
				entry.Detail = "artifact file is missing from the archive"
			} else {
				entry.Detail = "artifact file is unreadable: " + statErr.Error()
			}
			report.Missing++
			report.Artifacts = append(report.Artifacts, entry)
			continue
		}
		entry.SizeBytes = info.Size()
		sum, hashErr := sha256File(path)
		if hashErr != nil {
			entry.Status = integrityArtifactMissing
			entry.Detail = "failed to read artifact for hashing: " + hashErr.Error()
			report.Missing++
			report.Artifacts = append(report.Artifacts, entry)
			continue
		}
		entry.ActualSHA256 = sum
		if strings.EqualFold(sum, entry.ExpectedSHA256) {
			entry.Status = integrityArtifactMatch
			report.Matched++
		} else {
			entry.Status = integrityArtifactMismatch
			entry.Detail = "recomputed SHA-256 does not match the manifest (artifact modified)"
			report.Mismatched++
		}
		report.Artifacts = append(report.Artifacts, entry)
	}

	finalizeRunArchiveIntegrityStatus(report)
	return report
}

func verifyRunArchiveManifestTrust(record *storage.RunArchiveRecord, report *RunArchiveIntegrityReport) {
	if record == nil || report == nil || strings.TrimSpace(record.ManifestSHA256) == "" {
		return
	}
	report.Total++
	entry := RunArchiveIntegrityArtifact{
		Name:           runArchiveManifestFileName,
		ExpectedSHA256: strings.TrimSpace(record.ManifestSHA256),
	}
	sum, err := sha256File(record.ManifestPath)
	if err != nil {
		entry.Status = integrityArtifactMissing
		entry.Detail = "manifest file is unreadable for root-of-trust verification: " + err.Error()
		report.Missing++
		report.Artifacts = append(report.Artifacts, entry)
		return
	}
	entry.ActualSHA256 = sum
	if strings.EqualFold(sum, entry.ExpectedSHA256) {
		entry.Status = integrityArtifactMatch
		entry.Detail = "manifest matches the archive metadata trust anchor"
		report.Matched++
	} else {
		entry.Status = integrityArtifactMismatch
		entry.Detail = "manifest SHA-256 does not match the archive metadata trust anchor"
		report.Mismatched++
	}
	report.Artifacts = append(report.Artifacts, entry)
}

// finalizeRunArchiveIntegrityStatus rolls the per-artifact counts into the overall
// verdict. Tampering (a content mismatch) outranks corruption (a missing artifact)
// because a hash mismatch is the stronger tamper signal; a clean run with at least
// one matched artifact is "verified". Anything else (empty manifest, or only
// hash-less artifacts) is honestly "unverifiable" rather than a false pass.
func finalizeRunArchiveIntegrityStatus(report *RunArchiveIntegrityReport) {
	switch {
	case report.Mismatched > 0:
		report.Status = integrityStatusTampered
		report.Verified = false
	case report.Missing > 0:
		report.Status = integrityStatusCorrupt
		report.Verified = false
	case report.Matched > 0 && report.Unverifiable > 0:
		// Some artifacts checked out clean but others carried no recorded hash, so
		// integrity is only partially attestable. A tamper-evidence verdict must not
		// signal a fully-clean pass here: Verified stays false so the CLI exits
		// non-zero and the banner renders a caution rather than a green "verified".
		report.Status = integrityStatusVerifiedPartial
		report.Verified = false
	case report.Matched > 0:
		report.Status = integrityStatusVerified
		report.Verified = true
	default:
		report.Status = integrityStatusUnverifiable
		report.Verified = false
	}
	report.Summary = runArchiveIntegritySummary(report)
}

func runArchiveIntegritySummary(report *RunArchiveIntegrityReport) string {
	switch report.Status {
	case integrityStatusVerified:
		return fmt.Sprintf("All %d artifacts verified against their recorded SHA-256.", report.Matched)
	case integrityStatusVerifiedPartial:
		return fmt.Sprintf("%d of %d artifacts verified against their recorded SHA-256; %d had no recorded hash and could not be checked.",
			report.Matched, report.Total, report.Unverifiable)
	case integrityStatusTampered:
		return fmt.Sprintf("%d of %d artifacts failed integrity verification: content or manifest references no longer match the recorded archive metadata (tampered).",
			report.Mismatched, report.Total)
	case integrityStatusCorrupt:
		return fmt.Sprintf("%d of %d recorded artifacts are missing or unreadable (corrupt archive).",
			report.Missing, report.Total)
	default:
		return "No artifact hashes were available to verify; integrity is unverifiable."
	}
}
