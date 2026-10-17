package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/archiveingest"
	"github.com/jasonczech/apexload/pkg/scrub"
)

const (
	maxEnterpriseArchiveCollectionMembers = 100
	enterpriseArchiveCollectionTimeout    = 3 * time.Minute
	enterpriseArchiveCollectionErrorGrace = 5 * time.Second
)

type enterpriseArchiveCollectionMember struct {
	SourceName string         `json:"source_name"`
	SourceSize int            `json:"source_size"`
	Response   map[string]any `json:"response,omitempty"`
	Error      string         `json:"error,omitempty"`
}

// readEnterpriseArchiveCollection recognizes a transport package of script
// archives, not the assets nested inside an ordinary script project. Every
// child stays independent so its protocol, settings, credentials, and approval
// requirements cannot be attributed to a different script in the package.
func readEnterpriseArchiveCollection(data []byte) ([]archiveingest.File, bool, error) {
	if err := archiveingest.ValidateZIPDirectory(data, enterprisearchive.MaxArchiveEntries); err != nil {
		return nil, false, nil //nolint:nilerr // the ordinary converter reports invalid ZIPs
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, false, nil //nolint:nilerr // the ordinary converter reports invalid ZIPs
	}
	candidate := enterpriseArchiveCollectionCandidate(reader)
	if !candidate && !enterpriseArchiveUnanchoredSourcesWithArchives(reader) {
		return nil, false, nil
	}
	files, err := archiveingest.ReadZIP(data, archiveingest.Options{Limits: archiveingest.Limits{
		MaxEntries: enterprisearchive.MaxArchiveEntries, MaxEntryBytes: enterprisearchive.MaxArchiveEntryBytes,
		MaxTotalBytes: enterprisearchive.MaxArchiveTotalBytes, MaxDepth: 32,
	}})
	if err != nil {
		if !candidate {
			return nil, false, nil // Ordinary source archives retain their ingestion error.
		}
		return nil, true, fmt.Errorf("invalid script archive collection: %w", err)
	}
	if !candidate {
		// A standalone source project can legitimately contain ZIP assets.
		// Explicit USZ packages or a nested ZIP project descriptor establish
		// a different packaging boundary. Reject the mixed layout rather than
		// publishing only the loose source and silently omitting its projects.
		for _, file := range files {
			if enterpriseArchiveNestedScriptProject(file) {
				return nil, true, fmt.Errorf("archive mixes loose source files with nested script projects; import each nested script archive separately, then attach loose files as supplemental sources to its intended project")
			}
		}
		return nil, false, nil
	}
	members := make([]archiveingest.File, 0)
	var total uint64
	entries := len(reader.File)
	for _, file := range files {
		total += uint64(len(file.Data))
	}
	for _, file := range files {
		if ext := strings.ToLower(path.Ext(file.Name)); ext != ".usz" && ext != ".zip" {
			continue
		}
		members = append(members, file)
		if len(members) > maxEnterpriseArchiveCollectionMembers {
			return nil, true, fmt.Errorf("script archive collection must contain at most %d archives", maxEnterpriseArchiveCollectionMembers)
		}
		if err := archiveingest.ValidateZIPDirectory(file.Data, enterprisearchive.MaxArchiveEntries); err != nil {
			continue // Invalid members are reviewed individually without allocating their directory.
		}
		child, childErr := zip.NewReader(bytes.NewReader(file.Data), int64(len(file.Data)))
		if childErr != nil {
			continue // Preserve a per-member conversion error beside valid reviews.
		}
		entries += len(child.File)
		if entries > enterprisearchive.MaxArchiveEntries {
			return nil, true, fmt.Errorf("script archive collection exceeds the maximum combined entry count of %d", enterprisearchive.MaxArchiveEntries)
		}
		for _, entry := range child.File {
			if entry.UncompressedSize64 > uint64(enterprisearchive.MaxArchiveEntryBytes) ||
				entry.UncompressedSize64 > uint64(enterprisearchive.MaxArchiveTotalBytes)-total {
				return nil, true, fmt.Errorf("script archive collection exceeds the combined decompression limit of %d bytes", enterprisearchive.MaxArchiveTotalBytes)
			}
			total += entry.UncompressedSize64
		}
	}
	// ZIP entry order is packaging metadata, not script execution order.
	sort.Slice(members, func(i, j int) bool { return members[i].Name < members[j].Name })
	return members, true, nil
}

func enterpriseArchiveUnanchoredSourcesWithArchives(reader *zip.Reader) bool {
	hasSource, hasArchive := false, false
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		switch strings.ToLower(path.Ext(entry.Name)) {
		case ".usr":
			return false
		case ".c", ".java", ".js", ".jmx":
			hasSource = true
		case ".usz", ".zip":
			hasArchive = true
		}
	}
	return hasSource && hasArchive
}

func enterpriseArchiveNestedScriptProject(file archiveingest.File) bool {
	switch strings.ToLower(path.Ext(file.Name)) {
	case ".usz":
		return true // This extension explicitly identifies a script package.
	case ".zip":
		// Only inspect the bounded central directory; do not decompress an
		// ordinary project's ZIP assets or guess from library source files.
		if archiveingest.ValidateZIPDirectory(file.Data, enterprisearchive.MaxArchiveEntries) != nil {
			return false
		}
		reader, err := zip.NewReader(bytes.NewReader(file.Data), int64(len(file.Data)))
		if err != nil {
			return false
		}
		for _, entry := range reader.File {
			if !entry.FileInfo().IsDir() && strings.EqualFold(path.Ext(entry.Name), ".usr") {
				return true
			}
		}
	}
	return false
}

func enterpriseArchiveCollectionCandidate(reader *zip.Reader) bool {
	hasArchive := false
	for _, entry := range reader.File {
		switch strings.ToLower(path.Ext(entry.Name)) {
		case ".usr", ".c", ".java", ".js", ".jmx":
			return false
		case ".usz", ".zip":
			hasArchive = true
		}
	}
	return hasArchive
}

func selectEnterpriseArchiveCollectionMember(data []byte, name string) ([]byte, error) {
	members, collection, err := readEnterpriseArchiveCollection(data)
	if err != nil {
		return nil, err
	}
	if !collection {
		return nil, fmt.Errorf("archive_member requires a script archive collection")
	}
	for _, member := range members {
		if member.Name == name {
			if err := archiveingest.ValidateZIPDirectory(member.Data, enterprisearchive.MaxArchiveEntries); err == nil {
				if child, err := zip.NewReader(bytes.NewReader(member.Data), int64(len(member.Data))); err == nil && enterpriseArchiveCollectionCandidate(child) {
					return nil, fmt.Errorf("selected archive_member contains another archive collection; import that collection separately")
				}
			}
			return member.Data, nil
		}
	}
	return nil, fmt.Errorf("selected archive_member was not found in this script archive collection")
}

func (s *Server) writeEnterpriseArchiveCollectionResponse(w http.ResponseWriter, r *http.Request, req *enterpriseArchiveImportRequest) bool {
	members, collection, err := readEnterpriseArchiveCollection(req.Data)
	if !collection {
		return false
	}
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return true
	}
	if len(req.SupplementalData) > 0 || len(req.HARData) > 0 || req.SalesforceNative {
		writeError(w, http.StatusBadRequest, "select an archive_member before attaching supplemental sources, HAR evidence, or Salesforce conversion to a script archive collection")
		return true
	}
	r, cancelRequest := enterpriseArchiveCollectionRequest(w, r)
	defer cancelRequest()
	archives := make([]enterpriseArchiveCollectionMember, 0, len(members))
	var drafts []*dsl.Script
	var sourceSessions []string
	published := false
	defer func() {
		if !published {
			s.discardUnpublishedEnterpriseArchiveCollection(r, req.ProjectID, drafts, sourceSessions)
		}
	}()
	successes := 0
	var unassigned []string
	if outer, err := zip.NewReader(bytes.NewReader(req.Data), int64(len(req.Data))); err == nil {
		for _, entry := range outer.File {
			if !entry.FileInfo().IsDir() {
				if ext := strings.ToLower(path.Ext(entry.Name)); ext != ".zip" && ext != ".usz" {
					unassigned = append(unassigned, entry.Name)
				}
			}
		}
	}
	sort.Strings(unassigned)
	warnings := []string{}
	if len(unassigned) > 0 {
		warnings = append(warnings, "Collection contains loose files that were not applied to its scripts: "+strings.Join(unassigned, ", ")+". Select the intended archive member and attach these files as supplemental sources before reviewing that script again.")
	}
	for _, member := range members {
		if r.Context().Err() != nil {
			writeError(w, http.StatusRequestTimeout, "script archive collection conversion canceled")
			return true
		}
		item := enterpriseArchiveCollectionMember{SourceName: member.Name, SourceSize: len(member.Data)}
		if err := archiveingest.ValidateZIPDirectory(member.Data, enterprisearchive.MaxArchiveEntries); err != nil {
			item.Error = scrub.Value("invalid script archive member: "+err.Error(), scrub.Policy{})
			archives = append(archives, item)
			continue
		}
		if child, err := zip.NewReader(bytes.NewReader(member.Data), int64(len(member.Data))); err == nil && enterpriseArchiveCollectionCandidate(child) {
			item.Error = "Nested archive collections require a separate import; each collection member must contain one script project."
			archives = append(archives, item)
			continue
		}
		opts := req.Opts
		// A package name override must never rename every child to the same name.
		opts.ScriptName = ""
		result, conversionErr := enterprisearchive.ConvertZip(member.Data, opts)
		if conversionErr != nil {
			item.Error = scrub.Value("conversion failed: "+conversionErr.Error(), scrub.Policy{})
			archives = append(archives, item)
			continue
		}
		result.Warnings = append(result.Warnings, warnings...)
		if result.Script != nil {
			result.Script.ProjectID = normalizeProjectID(req.ProjectID)
		}
		for _, script := range result.Scripts {
			if script != nil {
				script.ProjectID = normalizeProjectID(req.ProjectID)
			}
		}
		preserveEnterpriseArchiveCollectionMember(result, member.Name, len(member.Data))
		item.Response = s.newEnterpriseArchiveImportResponse(r, result, req.ReadinessPolicy, req.ExpireDraft)
		childDrafts := uniqueImportDraftScripts(importDraftScriptPointers(result.Script, result.Scripts))
		var childSourceSessions []string
		if sessionID, _ := item.Response["source_secret_session_id"].(string); sessionID != "" {
			childSourceSessions = append(childSourceSessions, sessionID)
		}
		if failed, _ := item.Response[importDraftResponseFailureKey].(bool); failed {
			s.discardUnpublishedEnterpriseArchiveCollection(r, req.ProjectID, childDrafts, childSourceSessions)
			item.Response = nil
			item.Error = "Protected import review could not be secured. Retry this archive after verifying the controller import-artifact storage."
		} else {
			drafts = append(drafts, childDrafts...)
			sourceSessions = append(sourceSessions, childSourceSessions...)
			successes++
		}
		archives = append(archives, item)
	}
	if r.Context().Err() != nil {
		writeError(w, http.StatusRequestTimeout, "script archive collection conversion canceled")
		return true
	}
	// A valid collection has a reviewable result even if every child failed.
	// The queue presents each failure and allows retrying that source alone.
	// Child builders have already secured their exact drafts. Encode once, and
	// retain capabilities only when the envelope was successfully published.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	err = json.NewEncoder(w).Encode(map[string]any{
		"archive_collection": true, "archives": archives,
		"count": len(archives), "successful_count": successes, "failed_count": len(archives) - successes,
		"unassigned_files": unassigned, "warnings": warnings,
	})
	published = err == nil && r.Context().Err() == nil
	return true
}

// A bounded package review can legitimately exceed the server's ordinary
// single-request write timeout. Keep both conversion and error delivery finite,
// and preserve any shorter deadline supplied by the caller.
func enterpriseArchiveCollectionRequest(w http.ResponseWriter, r *http.Request) (*http.Request, context.CancelFunc) {
	deadline := time.Now().Add(enterpriseArchiveCollectionTimeout)
	if callerDeadline, ok := r.Context().Deadline(); ok && callerDeadline.Before(deadline) {
		deadline = callerDeadline
	}
	_ = http.NewResponseController(w).SetWriteDeadline(deadline.Add(enterpriseArchiveCollectionErrorGrace))
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	return r.WithContext(ctx), cancel
}

func (s *Server) discardUnpublishedEnterpriseArchiveCollection(r *http.Request, projectID string, drafts []*dsl.Script, sourceSessions []string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	cleanupRequest := r.WithContext(ctx)
	for _, draft := range uniqueImportDraftScripts(drafts) {
		s.discardImportDraftAttestation(cleanupRequest, draft)
	}
	for _, sessionID := range sourceSessions {
		if _, err := s.discardSourceSecretSession(cleanupRequest, projectID, sessionID); err != nil {
			slog.Warn("failed to discard unpublished enterprise archive source-secret session")
		}
	}
}

func preserveEnterpriseArchiveCollectionMember(result *enterprisearchive.ConvertResult, name string, size int) {
	if result == nil || name == "" {
		return
	}
	scripts := append([]*dsl.Script{result.Script}, result.Scripts...)
	for _, script := range scripts {
		if script != nil {
			preserved := ensureScriptImportPreserved(script, "enterprise_script_archive")
			preserved["source_archive_member"] = name
			preserved["source_archive_size"] = size
		}
	}
}
