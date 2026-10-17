package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/harsynth"
	"github.com/jasonczech/apexload/pkg/recordbridge"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Run archive HAR downloads (D7). A run's HARs are its archived native HARs
// and one synthetic HAR derived on request from its retained network entries
// (D6). Anyone who can view the run lists them; downloading one needs editor
// access to the project, because native HARs carry scrubbed response content.
// Only archived HARs are served: a HAR directory's working or published files
// are never reachable through these routes.

const browserHARListVersion = 1

// BrowserHARArchiveList is a run archive's HARs.
type BrowserHARArchiveList struct {
	Version   int                     `json:"version"`
	TestID    string                  `json:"test_id"`
	ProjectID string                  `json:"project_id,omitempty"`
	Artifacts []BrowserHARArchiveItem `json:"artifacts"`
	// Incomplete and Omitted describe native HARs the archive could not
	// retain (by reason).
	Incomplete bool             `json:"incomplete"`
	Omitted    map[string]int64 `json:"omitted,omitempty"`
	// DownloadRole is the least project role that may download a HAR.
	DownloadRole string `json:"download_role"`
	// CanDownload says whether the requester may.
	CanDownload bool `json:"can_download"`
}

// BrowserHARArchiveItem is one downloadable HAR.
type BrowserHARArchiveItem struct {
	Name     string `json:"name"`
	Fidelity string `json:"fidelity"`
	Kind     string `json:"kind"`
	// Available is false for a synthetic HAR whose archive has no
	// transactions to derive it from (UnavailableReason says so).
	Available         bool   `json:"available"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
	SourceIdentity    string `json:"source_identity,omitempty"`
	SourceGeneration  string `json:"source_generation,omitempty"`
	// EvidenceGeneration is the browser evidence generation of the context a
	// native HAR recorded: the browser resource rows with that
	// source_generation are in it.
	EvidenceGeneration string `json:"evidence_generation,omitempty"`
	Sequence           int64  `json:"sequence,omitempty"`
	SHA256             string `json:"sha256,omitempty"`
	Bytes              *int64 `json:"bytes,omitempty"`
	// CaptureMode and Derived describe a synthetic HAR: derived on request
	// from retained network entries, so its size and digest are not known
	// before it is downloaded.
	CaptureMode string `json:"capture_mode,omitempty"`
	Derived     bool   `json:"derived,omitempty"`
}

// canDownloadBrowserHAR reports whether the requester may download a
// project's HARs: editor or project administrator, globally or in the
// project, or any requester when authentication is bypassed.
func (s *Server) canDownloadBrowserHAR(r *http.Request, projectID string) bool {
	if user, ok := AuthenticatedUserFromContext(r.Context()); ok && user != nil {
		return user.hasGlobalRole(RoleEditor, RoleProjectAdmin, RoleSystemAdmin) ||
			userHasProjectOrParentRole(s.store, user, normalizeProjectID(projectID), RoleEditor, RoleProjectAdmin)
	}
	return isAuthBypassMode(s.authSettingsForRequest().Mode)
}

// browserHARArchiveItems lists a manifest's native HARs, then its synthetic
// HAR.
func browserHARArchiveItems(manifest *RunArchiveManifest) []BrowserHARArchiveItem {
	items := make([]BrowserHARArchiveItem, 0, len(manifest.BrowserHARArtifacts)+1)
	for _, har := range manifest.BrowserHARArtifacts {
		if !validDeclaredHAR(har) {
			continue
		}
		bytes := har.Bytes
		items = append(items, BrowserHARArchiveItem{Name: har.Name, Fidelity: "native", Kind: har.Kind, Available: true, SourceIdentity: har.SourceIdentity, SourceGeneration: har.SourceGeneration, EvidenceGeneration: har.EvidenceGeneration, Sequence: har.Sequence, SHA256: har.SHA256, Bytes: &bytes})
	}
	synthetic := BrowserHARArchiveItem{Name: RunArchiveSyntheticHARName, Fidelity: harsynth.Fidelity, Kind: storage.EvidenceKindHARSynthetic, CaptureMode: harsynth.CaptureRetainedNetworkEntries, Derived: true, Available: true}
	if strings.TrimSpace(manifest.TransactionsFile) == "" && strings.TrimSpace(manifest.TransactionsParquetFile) == "" {
		synthetic.Available, synthetic.UnavailableReason = false, "no_transaction_source"
	}
	return append(items, synthetic)
}

func (s *Server) handleListRunArchiveBrowserHARs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	archives := s.archiveDomainService()
	test, ok := archives.GetTest(id)
	if !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	if _, ok := archives.GetRunArchiveContext(r.Context(), id); !ok {
		writeError(w, http.StatusNotFound, "archive not found")
		return
	}
	manifest, err := archives.GetRunArchiveManifest(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	list := BrowserHARArchiveList{Version: browserHARListVersion, TestID: id, ProjectID: normalizeProjectID(test.ProjectID), Artifacts: browserHARArchiveItems(manifest), Incomplete: manifest.BrowserHARIncomplete, Omitted: manifest.BrowserHAROmitted, DownloadRole: RoleEditor, CanDownload: s.canDownloadBrowserHAR(r, test.ProjectID)}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleDownloadRunArchiveBrowserHAR(w http.ResponseWriter, r *http.Request) {
	clearArchiveWriteDeadline(w)
	prepCtx, cancelPrep := context.WithTimeout(r.Context(), configuredArchiveMaterializationTimeout())
	defer cancelPrep()
	id, name := r.PathValue("id"), r.PathValue("artifact")
	archives := s.archiveDomainService()
	test, ok := archives.GetTest(id)
	if !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	record, ok := archives.GetRunArchiveContext(r.Context(), id)
	if !ok {
		writeError(w, http.StatusNotFound, "archive not found")
		return
	}
	lease, err := acquireArchiveMaterialized(prepCtx, record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to materialize archive: "+err.Error())
		return
	}
	defer lease.Release()
	manifest, err := archives.GetRunArchiveManifest(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	filename := fmt.Sprintf("%s-%s-%s", sanitizeFilename(test.Name), id, name)
	if name == RunArchiveSyntheticHARName {
		var summary runArchiveSyntheticHARSummary
		staged, err := stageArchiveDownload(prepCtx, func(destination io.Writer) error {
			summary, err = writeRunArchiveSyntheticHAR(prepCtx, record.ArchivePath, manifest, destination)
			return err
		})
		if err != nil {
			if errors.Is(err, errSyntheticHARSourceUnavailable) {
				writeError(w, http.StatusNotFound, "this archive has no transactions to derive a synthetic HAR from")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to derive the synthetic HAR")
			return
		}
		defer removeStagedArchiveDownload(staged)
		writeBrowserHARHeaders(w, filename, harsynth.Fidelity)
		w.Header().Set("X-APEXLoad-HAR-Completeness", summary.Completeness)
		http.ServeContent(w, r, filename, record.CompletedAt.UTC(), staged.File)
		return
	}
	var declared *RunArchiveEvidenceArtifact
	for index := range manifest.BrowserHARArtifacts {
		if manifest.BrowserHARArtifacts[index].Name == name {
			declared = &manifest.BrowserHARArtifacts[index]
			break
		}
	}
	if declared == nil || !validDeclaredHAR(*declared) {
		writeError(w, http.StatusNotFound, "browser HAR not found in this archive")
		return
	}
	// An archived HAR must still be the artifact its manifest describes.
	file, info, err := openArchiveArtifactForRead(record.ArchivePath, name)
	if err != nil {
		writeError(w, http.StatusNotFound, "browser HAR not found in this archive")
		return
	}
	defer file.Close()
	sum, err := sha256Reader(io.LimitReader(file, declared.Bytes+1))
	if err != nil || info.Size() != declared.Bytes || sum != declared.SHA256 {
		writeError(w, http.StatusConflict, "archived browser HAR differs from its manifest")
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read the archived browser HAR")
		return
	}
	writeBrowserHARHeaders(w, filename, "native")
	http.ServeContent(w, r, filename, info.ModTime(), file)
}

func writeBrowserHARHeaders(w http.ResponseWriter, filename, fidelity string) {
	recordbridge.WriteBrowserHARHeaders(w, filename, fidelity)
}
