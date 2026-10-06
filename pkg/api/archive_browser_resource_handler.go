package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

const browserResourceReadTimeout = 10 * time.Second

// One heavy scan per process bounds the retained-record/parser/selection memory
// while native analytical workers may share the controller. Admission failures
// are explicit; no queue or process-local state becomes archive authority.
var browserResourceReadPermit = make(chan struct{}, 1)
var errBrowserResourceForbidden = errors.New("browser resource access is no longer authorized")

type browserResourceArchiveIdentityStore interface {
	ReadBrowserResourceArchiveIdentityContext(context.Context, string) (*storage.BrowserResourceArchiveIdentity, error)
}
type browserResourceReadOptions struct{ beforePublish func() }
type browserResourcePageResponse struct {
	Raw     []byte
	Release func()
}

type browserResourceReadArtifact struct {
	Name      string
	Format    string
	SHA256    string
	SizeBytes *int64
	Count     *int64
}
type browserResourceReadManifest struct {
	TestID                  string
	ProjectID               string
	RequestedAt             time.Time
	TransactionsFile        string
	TransactionsParquetFile string
	TransactionCount        *int64
	Artifacts               map[string]browserResourceReadArtifact
	SHA256                  string
	SizeBytes               int64
	Verified                bool
}

func resolveBrowserResourceArchiveProjectID(r *http.Request, store storage.Store) (string, int, string) {
	reader, ok := store.(browserResourceArchiveIdentityStore)
	if !ok {
		return "", http.StatusServiceUnavailable, "browser resource reader durable capability is unavailable"
	}
	identity, err := reader.ReadBrowserResourceArchiveIdentityContext(r.Context(), r.PathValue("id"))
	if err != nil || identity == nil {
		if r.Context().Err() != nil {
			return "", http.StatusRequestTimeout, "browser resource query canceled or timed out"
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", http.StatusServiceUnavailable, "browser resource archive authority is unavailable"
		}
		return "", http.StatusNotFound, "archive not found"
	}
	return normalizeProjectID(identity.ProjectID), 0, ""
}
func (s *Server) handleGetRunArchiveBrowserResources(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), browserResourceReadTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	response, err := s.readBrowserResourcePage(r, browserResourceReadOptions{})
	if err != nil {
		status := http.StatusUnprocessableEntity
		message := "browser resource source is unavailable or malformed"
		switch {
		case errors.Is(err, errBrowserResourceForbidden):
			status = http.StatusForbidden
			message = "forbidden"
		case errors.Is(err, sql.ErrNoRows):
			status = http.StatusNotFound
			message = "archive not found"
		case errors.Is(err, errBrowserResourceUnavailable):
			status = http.StatusServiceUnavailable
			message = errBrowserResourceUnavailable.Error()
		case errors.Is(err, errBrowserResourceQuery):
			status = http.StatusBadRequest
			message = errBrowserResourceQuery.Error()
		case errors.Is(err, errBrowserResourceCursor):
			status = http.StatusBadRequest
			message = errBrowserResourceCursor.Error()
		case errors.Is(err, errBrowserResourceReadChanged):
			status = http.StatusConflict
			message = errBrowserResourceReadChanged.Error()
		case errors.Is(err, errBrowserResourceReadLimit):
			status = http.StatusRequestEntityTooLarge
			message = errBrowserResourceReadLimit.Error()
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			status = http.StatusRequestTimeout
			message = "browser resource query canceled or timed out"
		}
		writeError(w, status, message)
		return
	}
	defer response.Release()
	if err := ctx.Err(); err != nil {
		writeError(w, http.StatusRequestTimeout, "browser resource query canceled or timed out")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(response.Raw)
}

func (s *Server) readBrowserResourcePage(r *http.Request, options browserResourceReadOptions) (*browserResourcePageResponse, error) {
	ctx := r.Context()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	q, err := ParseBrowserResourcePageQuery(r.URL.Query())
	if err != nil {
		return nil, errBrowserResourceQuery
	}
	reader, ok := s.store.(browserResourceArchiveIdentityStore)
	if !ok {
		return nil, errBrowserResourceUnavailable
	}
	identity, err := reader.ReadBrowserResourceArchiveIdentityContext(ctx, r.PathValue("id"))
	if err != nil {
		return nil, browserResourceIdentityError(ctx, err)
	}
	if identity == nil || identity.TestID == "" || identity.Archive.TestID != identity.TestID {
		return nil, errBrowserResourceReadChanged
	}
	project := normalizeProjectID(identity.ProjectID)
	if !browserResourceReadText(identity.TestID, 128) || !browserResourceReadText(project, 128) || project == "" {
		return nil, errBrowserResourceReadMalformed
	}
	if expected, scoped := authorizedProjectFromRequest(r); scoped && project != expected {
		return nil, errBrowserResourceForbidden
	}
	if explicit := r.URL.Query().Get("project_id"); explicit != "" && explicit != project {
		return nil, errBrowserResourceForbidden
	}
	authorize, err := s.browserResourceAuthorizer(r, project)
	if err != nil {
		return nil, err
	}
	if !authorize() {
		return nil, browserResourceContextOr(ctx, errBrowserResourceForbidden)
	}
	key, err := s.browserResourceCursorKey(ctx)
	if err != nil {
		return nil, err
	}
	authority := browserResourceArchiveRevision(identity, s.store)
	var cursor *browserResourceCursorPayload
	if q.Cursor != "" {
		cursor, err = browserResourceOpenCursor(key, q.Cursor, q, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		if cursor.TestID != identity.TestID || cursor.ProjectID != project || cursor.AuthorityRevision != authority {
			return nil, errBrowserResourceReadChanged
		}
	}
	select {
	case browserResourceReadPermit <- struct{}{}:
		defer func() { <-browserResourceReadPermit }()
	default:
		return nil, errBrowserResourceUnavailable
	}
	materialized := identity.Archive
	lease, err := acquireArchiveMaterialized(ctx, &materialized)
	if err != nil {
		return nil, browserResourceContextOr(ctx, errBrowserResourceUnavailable)
	}
	releaseOnReturn := true
	defer func() {
		if releaseOnReturn {
			lease.Release()
		}
	}()
	manifest, err := readBrowserResourceManifest(ctx, &materialized)
	if err != nil {
		return nil, err
	}
	if manifest.TestID != identity.TestID || manifest.ProjectID != "" && normalizeProjectID(manifest.ProjectID) != project || !manifest.RequestedAt.IsZero() && !identity.StartTime.IsZero() && !archiveRunTimestampMatches(manifest.RequestedAt, identity.StartTime, s.store) {
		return nil, errBrowserResourceReadChanged
	}
	name, format := manifest.TransactionsFile, "jsonl"
	if name == "" {
		name, format = manifest.TransactionsParquetFile, "parquet"
	}
	if name == "" {
		return nil, errBrowserResourceUnavailable
	}
	if !browserResourceFormatMatches(name, format) {
		return nil, errBrowserResourceUnavailable
	}
	artifact, declared := manifest.Artifacts[name]
	if declared && artifact.Format != "" && artifact.Format != format {
		return nil, errBrowserResourceReadMalformed
	}
	file, info, err := openArchiveArtifactForRead(materialized.ArchivePath, name)
	if err != nil {
		return nil, errBrowserResourceReadChanged
	}
	defer file.Close()
	stop := browserResourceCloseOnCancel(ctx, file)
	defer stop()
	if info.Size() > maxArchiveQueryBytes {
		return nil, errBrowserResourceReadLimit
	}
	if declared && artifact.SizeBytes != nil && *artifact.SizeBytes != info.Size() {
		return nil, errBrowserResourceReadChanged
	}
	var after *browserResourceSortKey
	if cursor != nil {
		if cursor.ManifestSHA256 != manifest.SHA256 || cursor.SourceFormat != format || cursor.ArtifactSize != info.Size() {
			return nil, errBrowserResourceReadChanged
		}
		after = &cursor.Boundary
	}
	var scan browserResourceScanResult
	if format == "parquet" {
		scan, err = scanBrowserResourceParquet(ctx, file, identity.TestID, q, after)
	} else {
		scan, err = scanBrowserResourceJSONL(ctx, file, identity.TestID, q, after)
	}
	if err != nil {
		return nil, err
	}
	if declared && artifact.SHA256 != "" && artifact.SHA256 != scan.SHA256 || declared && artifact.SizeBytes != nil && *artifact.SizeBytes != scan.SizeBytes || declared && artifact.Count != nil && *artifact.Count != scan.Transactions || manifest.TransactionCount != nil && *manifest.TransactionCount != scan.Transactions {
		return nil, errBrowserResourceReadChanged
	}
	sourceRevision := browserResourceReadDigest(struct {
		Authority, Manifest, Artifact, Format string
		Size                                  int64
	}{authority, manifest.SHA256, scan.SHA256, format, scan.SizeBytes})
	if cursor != nil && (cursor.ArtifactSHA256 != scan.SHA256 || cursor.SourceRevision != sourceRevision) {
		return nil, errBrowserResourceReadChanged
	}
	population := BrowserResourcePagePopulation{Availability: "unavailable", TransactionRecords: scan.Transactions}
	if scan.SourcePresent {
		population.Availability = "retained_observations"
		population.RetainedEntryObservations = &scan.Retained
		population.MatchedRetainedObservations = &scan.Matched
	}
	integrity := "legacy_scan_fingerprint"
	if manifest.Verified && declared && artifact.SHA256 != "" {
		integrity = "verified_manifest_and_artifact"
	}
	items := scan.Items
	if items == nil {
		items = []BrowserResourcePageItem{}
	}
	page := BrowserResourcePage{Version: 1, TestID: identity.TestID, ProjectID: project, SourceFormat: format, SourceRevision: sourceRevision, SourceIntegrity: integrity, SourceTimestampPrecision: scan.TimestampPrecision, Population: population, CaptureCoverage: BrowserResourcePageCoverage{Availability: "unavailable", Reason: "retained_observations_are_not_capture_coverage"}, Items: items}
	if scan.HasNext {
		page.NextCursor, err = browserResourceSealCursor(key, browserResourceCursorPayload{Version: 1, TestID: identity.TestID, ProjectID: project, AuthorityRevision: authority, ManifestSHA256: manifest.SHA256, ArtifactSHA256: scan.SHA256, ArtifactSize: scan.SizeBytes, SourceFormat: format, SourceRevision: sourceRevision, QuerySHA256: browserResourceQueryDigest(q), Boundary: scan.Last, ExpiresAt: time.Now().UTC().Add(browserResourceCursorTTL)})
		if err != nil {
			return nil, err
		}
	}
	if !ValidateBrowserResourcePage(&page) {
		return nil, errBrowserResourceReadMalformed
	}
	raw, err := json.Marshal(page)
	if err != nil || len(raw) > MaxBrowserResourcePageBytes {
		return nil, errBrowserResourceReadLimit
	}
	if options.beforePublish != nil {
		options.beforePublish()
	}
	// Final scope and exact durable pointer are independent from the materialized
	// paths and from any cached test/archive library state.
	current, err := reader.ReadBrowserResourceArchiveIdentityContext(ctx, identity.TestID)
	if err != nil {
		return nil, browserResourceIdentityError(ctx, err)
	}
	if current == nil || current.TestID != identity.TestID || browserResourceArchiveRevision(current, s.store) != authority {
		return nil, errBrowserResourceReadChanged
	}
	finalManifest, err := readBrowserResourceManifest(ctx, &materialized)
	if err != nil || finalManifest.SHA256 != manifest.SHA256 {
		return nil, browserResourceContextOr(ctx, errBrowserResourceReadChanged)
	}
	currentInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, currentInfo) || currentInfo.Size() != info.Size() || !currentInfo.ModTime().Equal(info.ModTime()) {
		return nil, browserResourceContextOr(ctx, errBrowserResourceReadChanged)
	}
	probe, probeInfo, err := openArchiveArtifactForRead(materialized.ArchivePath, name)
	if err != nil {
		return nil, browserResourceContextOr(ctx, errBrowserResourceReadChanged)
	}
	_ = probe.Close()
	if !os.SameFile(info, probeInfo) || probeInfo.Size() != info.Size() || !probeInfo.ModTime().Equal(info.ModTime()) {
		return nil, errBrowserResourceReadChanged
	}
	if !authorize() {
		return nil, browserResourceContextOr(ctx, errBrowserResourceForbidden)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	releaseOnReturn = false
	return &browserResourcePageResponse{Raw: raw, Release: lease.Release}, nil
}
func browserResourceIdentityError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, sql.ErrNoRows) {
		return sql.ErrNoRows
	}
	return errBrowserResourceUnavailable
}
func browserResourceArchiveRevision(identity *storage.BrowserResourceArchiveIdentity, store any) string {
	archive := identity.Archive
	archive.TrendPointJSON = ""
	return browserResourceReadDigest(struct {
		TestID, ProjectID, MaterializationKey string
		StartTime                             time.Time
		Archive                               storage.RunArchiveRecord
	}{identity.TestID, normalizeProjectID(identity.ProjectID), identity.MaterializationKey, archiveRunTimestamp(identity.StartTime, store).UTC(), archive})
}

func readBrowserResourceManifest(ctx context.Context, record *storage.RunArchiveRecord) (*browserResourceReadManifest, error) {
	relative, err := filepath.Rel(record.ArchivePath, record.ManifestPath)
	if err != nil {
		return nil, errBrowserResourceReadMalformed
	}
	normalized, err := normalizeArchiveArtifactName(filepath.ToSlash(relative))
	if err != nil || normalized != runArchiveManifestFileName {
		return nil, errBrowserResourceReadMalformed
	}
	file, info, err := openArchiveArtifactForRead(record.ArchivePath, normalized)
	if err != nil {
		return nil, errBrowserResourceReadChanged
	}
	defer file.Close()
	stop := browserResourceCloseOnCancel(ctx, file)
	defer stop()
	if info.Size() > maxArchiveManifestDownloadBytes {
		return nil, errBrowserResourceReadLimit
	}
	raw, err := io.ReadAll(io.LimitReader(&browserResourceCountingReader{r: file, ctx: ctx}, maxArchiveManifestDownloadBytes+1))
	if err != nil {
		return nil, browserResourceContextOr(ctx, errBrowserResourceReadMalformed)
	}
	if len(raw) > maxArchiveManifestDownloadBytes {
		return nil, errBrowserResourceReadLimit
	}
	fields, err := browserResourceReadObject(raw, []string{"test_id", "provenance", "transactions_file", "transactions_parquet_file", "transaction_count", "artifacts"})
	if err != nil {
		return nil, err
	}
	manifest := &browserResourceReadManifest{Artifacts: map[string]browserResourceReadArtifact{}, SHA256: sha256BytesHex(raw), SizeBytes: int64(len(raw))}
	if record.ManifestSHA256 != "" {
		if !browserResourceReadSHA(record.ManifestSHA256) || record.ManifestSHA256 != manifest.SHA256 {
			return nil, errBrowserResourceReadChanged
		}
		manifest.Verified = true
	}
	for _, field := range []struct {
		name   string
		target *string
	}{{"test_id", &manifest.TestID}, {"transactions_file", &manifest.TransactionsFile}, {"transactions_parquet_file", &manifest.TransactionsParquetFile}} {
		if raw, ok := fields[field.name]; ok {
			if !browserResourceReadOptionalString(raw, field.target) {
				return nil, errBrowserResourceReadMalformed
			}
		}
	}
	if manifest.TestID == "" || !browserResourceReadText(manifest.TestID, 128) {
		return nil, errBrowserResourceReadMalformed
	}
	if raw, ok := fields["provenance"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		p, err := browserResourceReadObject(raw, []string{"project_id", "requested_at"})
		if err != nil {
			return nil, err
		}
		if raw, ok := p["project_id"]; ok {
			if !browserResourceReadOptionalString(raw, &manifest.ProjectID) || !browserResourceReadText(manifest.ProjectID, 128) {
				return nil, errBrowserResourceReadMalformed
			}
		}
		if raw, ok := p["requested_at"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			var at time.Time
			if json.Unmarshal(raw, &at) != nil {
				return nil, errBrowserResourceReadMalformed
			}
			manifest.RequestedAt = at
		}
	}
	if raw, ok := fields["transaction_count"]; ok {
		var count int64
		if json.Unmarshal(raw, &count) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || count < 0 || count > maxArchiveQueryRecords {
			return nil, errBrowserResourceReadMalformed
		}
		manifest.TransactionCount = &count
	}
	for _, name := range []string{manifest.TransactionsFile, manifest.TransactionsParquetFile} {
		if name != "" {
			normalized, err := normalizeArchiveArtifactName(name)
			if err != nil || normalized != name || !browserResourceReadText(name, 1024) {
				return nil, errBrowserResourceReadMalformed
			}
		}
	}
	if raw, ok := fields["artifacts"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		d := json.NewDecoder(bytes.NewReader(raw))
		start, err := d.Token()
		if err != nil || start != json.Delim('[') {
			return nil, errBrowserResourceReadMalformed
		}
		for d.More() {
			if len(manifest.Artifacts) >= maxArchiveMaterializedArtifacts {
				return nil, errBrowserResourceReadLimit
			}
			var row json.RawMessage
			if d.Decode(&row) != nil {
				return nil, errBrowserResourceReadMalformed
			}
			fields, err := browserResourceReadObject(row, []string{"name", "format", "sha256", "size_bytes", "count"})
			if err != nil {
				return nil, err
			}
			a := browserResourceReadArtifact{}
			for _, f := range []struct {
				name   string
				target *string
			}{{"name", &a.Name}, {"format", &a.Format}, {"sha256", &a.SHA256}} {
				if raw, ok := fields[f.name]; ok {
					if !browserResourceReadOptionalString(raw, f.target) {
						return nil, errBrowserResourceReadMalformed
					}
				}
			}
			normalized, err := normalizeArchiveArtifactName(a.Name)
			if err != nil || normalized != a.Name || a.Name == "" {
				return nil, errBrowserResourceReadMalformed
			}
			if _, exists := manifest.Artifacts[a.Name]; exists {
				return nil, errBrowserResourceReadMalformed
			}
			if a.SHA256 != "" && !browserResourceReadSHA(a.SHA256) {
				return nil, errBrowserResourceReadMalformed
			}
			for _, f := range []struct {
				name   string
				target **int64
				max    int64
			}{{"size_bytes", &a.SizeBytes, math.MaxInt64}, {"count", &a.Count, math.MaxInt64}} {
				if raw, ok := fields[f.name]; ok {
					var value int64
					if json.Unmarshal(raw, &value) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || value < 0 || value > f.max {
						return nil, errBrowserResourceReadMalformed
					}
					*f.target = &value
				}
			}
			manifest.Artifacts[a.Name] = a
		}
		if end, err := d.Token(); err != nil || end != json.Delim(']') {
			return nil, errBrowserResourceReadMalformed
		}
		if _, err := d.Token(); err != io.EOF {
			return nil, errBrowserResourceReadMalformed
		}
	}
	// A historical manifest without project provenance retains durable scope
	// authority, but cannot attest producer/project identity.
	manifest.Verified = manifest.Verified && manifest.ProjectID != ""
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return manifest, nil
}

// Kept as a separate source-format decision. A corrupt expected source never
// falls back to another format and yields apparently complete missing rows.
func browserResourceFormatMatches(name, format string) bool {
	return format == "jsonl" && strings.HasSuffix(name, ".jsonl") || format == "parquet" && strings.HasSuffix(name, ".parquet")
}
