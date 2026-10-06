package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/analysis"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/storage"
)

// This version binds authored mapping identity to exact canonical query selectors.
// Build availability and operator configuration remain separately advertised.
const analysisImportedIdentityVersion = 2

const analysisUploadChunk = 4 << 20
const analysisImportMaxBytes = int64(20 << 30)

// Admission includes staging and durable publication, not only native queries.
var analysisPipelineJobs = make(chan struct{}, 1)

func admitAnalysisPipeline(w http.ResponseWriter) bool {
	if analytics.NativeBusy() {
		writeError(w, 503, "native analytical ownership is retained; retry after completion")
		return false
	}
	select {
	case analysisPipelineJobs <- struct{}{}:
		return true
	default:
		writeError(w, 503, "an analytical job is already running; retry later")
		return false
	}
}

func analysisProjectEnabled(project string) bool {
	return projectListed("APEXLOAD_ANALYSIS_PROJECTS", project)
}
func resolveAnalysisProject(r *http.Request, _ storage.Store) (string, int, string) {
	p := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if p == "" {
		return "", 400, "project_id is required"
	}
	return p, 0, ""
}
func resolveAnalysisImportProject(r *http.Request, store storage.Store) (string, int, string) {
	s, ok := store.(storage.AnalysisImportStore)
	if !ok {
		return "", 503, "analysis storage unavailable"
	}
	in, err := s.GetAnalysisImport(r.PathValue("id"))
	if err != nil {
		return "", 404, "analysis import not found"
	}
	return in.ProjectID, 0, ""
}

func (s *Server) analysisImportStore(w http.ResponseWriter, project string) storage.AnalysisImportStore {
	if !analysisProjectEnabled(project) {
		writeError(w, 409, "Parquet analysis is not enabled for this project")
		return nil
	}
	if !analytics.Available() {
		writeError(w, 501, analytics.ErrUnavailable.Error())
		return nil
	}
	if !s.requireAnalysisBackend(w) {
		return nil
	}
	return s.analysisMetadataStore(w)
}

func (s *Server) analysisMetadataStore(w http.ResponseWriter) storage.AnalysisImportStore {
	store, ok := s.store.(storage.AnalysisImportStore)
	if !ok {
		writeError(w, 503, "analysis storage unavailable")
		return nil
	}
	return store
}
func (s *Server) loadAnalysisImport(w http.ResponseWriter, r *http.Request) (storage.AnalysisImportStore, *storage.AnalysisImport) {
	store, in := s.loadAnalysisImportMetadata(w, r)
	if store == nil {
		return nil, nil
	}
	if s.analysisImportStore(w, in.ProjectID) == nil {
		return nil, nil
	}
	return store, in
}
func (s *Server) loadAnalysisImportMetadata(w http.ResponseWriter, r *http.Request) (storage.AnalysisImportStore, *storage.AnalysisImport) {
	_, status, message := resolveAnalysisImportProject(r, s.store)
	if status != 0 {
		writeError(w, status, message)
		return nil, nil
	}
	store := s.analysisMetadataStore(w)
	if store == nil {
		return nil, nil
	}
	in, err := store.GetAnalysisImport(r.PathValue("id"))
	if err != nil {
		writeError(w, 404, "analysis import not found")
		return nil, nil
	}
	return store, in
}

// AnalysisCapabilitiesResponse exposes measurement definitions independently
// of runtime capture and query availability. A catalog entry alone does not
// advertise a generator, dataset or provider capability.
type AnalysisCapabilitiesResponse struct {
	DiagnosticsEnabled            bool                   `json:"diagnostics_enabled"`
	MessagingManagementEnabled    bool                   `json:"messaging_management_enabled"`
	MessagingManagementVersion    uint32                 `json:"messaging_management_version"`
	MessagingManagementPacks      []string               `json:"messaging_management_packs"`
	MonitoringEnabled             bool                   `json:"monitoring_enabled"`
	PreparationEnabled            bool                   `json:"preparation_enabled"`
	NativeParquet                 bool                   `json:"native_parquet"`
	PartitionManifestVersion      uint32                 `json:"partition_manifest_version"`
	PartitionPruningAvailable     bool                   `json:"partition_pruning_available"`
	ImportedIdentityVersion       uint32                 `json:"imported_identity_version"`
	Enabled                       bool                   `json:"enabled"`
	ChunkBytes                    int64                  `json:"chunk_bytes"`
	MaxImportBytes                int64                  `json:"max_import_bytes"`
	HeavyJobs                     int                    `json:"heavy_jobs"`
	Threads                       int                    `json:"threads"`
	MemoryLimit                   string                 `json:"memory_limit"`
	SpillLimit                    string                 `json:"spill_limit"`
	ResourceConfigurationAccepted bool                   `json:"resource_configuration_accepted"`
	MetricCatalog                 analysis.MetricCatalog `json:"metric_catalog"`
	// SummaryStatistics lists the statistics a summary measure may declare;
	// absent on controllers that import summaries only as display values.
	SummaryStatistics []string `json:"summary_statistics,omitempty"`
	// HistogramBoundLimit is the most bounds a histogram measure may declare;
	// absent on controllers that cannot import histograms.
	HistogramBoundLimit int `json:"histogram_bound_limit,omitempty"`
}

func (s *Server) handleAnalysisCapabilities(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project_id")
	memory, spill, accepted := nativeAnalysisResourceCapability()
	writeJSON(w, 200, AnalysisCapabilitiesResponse{
		MessagingManagementEnabled: messagingManagementEnabled(project), MessagingManagementVersion: analysis.MessagingManagementVersion, MessagingManagementPacks: collectoragent.ManagementPacks(),
		DiagnosticsEnabled: diagnosticsProjectEnabled(project), MonitoringEnabled: runMonitoringEnabled(project), PreparationEnabled: preparationProjectEnabled(project),
		NativeParquet: analytics.Available(), PartitionManifestVersion: analysis.PartitionManifestVersion, PartitionPruningAvailable: analytics.Available(), ImportedIdentityVersion: analysisImportedIdentityVersion, Enabled: analysisProjectEnabled(project), ChunkBytes: analysisUploadChunk, MaxImportBytes: analysisImportMaxBytes,
		HeavyJobs: 1, Threads: 2, MemoryLimit: memory, SpillLimit: spill, ResourceConfigurationAccepted: accepted, MetricCatalog: analysis.Catalog(),
		SummaryStatistics: slices.Clone(analysis.SummaryStatistics), HistogramBoundLimit: analysis.MaxHistogramBounds,
	})
}

func (s *Server) handleAnalysisImports(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project_id")
	store := s.analysisMetadataStore(w)
	if r.Method != http.MethodGet {
		store = s.analysisImportStore(w, project)
	}
	if store == nil {
		return
	}
	if r.Method == http.MethodGet {
		limit, offset := parseLimitOffsetQuery(r, 50)
		list, err := store.ListAnalysisImports(project, min(limit, 100), offset)
		if err != nil {
			writeError(w, 500, "cannot list imports")
			return
		}
		for i := range list {
			for j := range list[i].Files {
				list[i].Files[j].Chunks = nil
			}
		}
		writeJSON(w, 200, list)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	var req struct {
		Name  string `json:"name"`
		Files []struct {
			RelativePath string `json:"relative_path"`
			Size         int64  `json:"size"`
		} `json:"files"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if len(req.Files) < 1 || len(req.Files) > 1024 {
		writeError(w, 400, "import requires 1-1024 files")
		return
	}
	in := storage.AnalysisImport{ID: uuid.NewString(), ProjectID: project, Name: req.Name, State: "uploading", UploadWriterVersion: storage.AnalysisUploadWriterVersion}
	var total int64
	seen := map[string]bool{}
	for _, file := range req.Files {
		p := file.RelativePath
		if !validAnalysisRelativePath(p) || seen[p] || file.Size < 12 || file.Size > analysisImportMaxBytes-total {
			writeError(w, 400, "invalid, duplicate, or excessive import file")
			return
		}
		seen[p] = true
		total += file.Size
		in.Files = append(in.Files, storage.AnalysisImportFile{ID: uuid.NewString(), RelativePath: p, Size: file.Size})
	}
	saved, err := store.SaveAnalysisImport(in, 0)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	s.recordAuditEvent(r, "analysis.import.create", "success", "analysis_import", saved.ID, project, nil)
	writeJSON(w, 201, saved)
}

func validAnalysisRelativePath(p string) bool {
	return p != "" && len(p) <= 1024 && !strings.HasPrefix(p, "/") && path.Clean(p) == p && !strings.ContainsAny(p, "\\\x00:\r\n") && p != ".." && !strings.HasPrefix(p, "../") && strings.HasSuffix(strings.ToLower(p), ".parquet")
}

func (s *Server) handleAnalysisImport(w http.ResponseWriter, r *http.Request) {
	store, in := s.loadAnalysisImportMetadata(w, r)
	if store == nil {
		return
	}
	if r.Method == http.MethodGet {
		if strings.Contains(r.URL.Path, "/datasets/") && in.State != "ready" {
			writeError(w, 404, "committed dataset not found")
			return
		}
		writeJSON(w, 200, in)
		return
	}
	// Capture the approved immutable namespace before the first metadata
	// mutation. A later environment edit must refuse cleanup rather than
	// substitute another backend after durable ownership was checked.
	backend, err := nativeAnalysisArtifactBackend(r.Context())
	if err != nil || backend.validate() != nil {
		writeError(w, 503, "dataset cleanup storage authority unavailable")
		return
	}
	if err = ensureAnalysisBackendFor(s.store, backend); err != nil {
		writeError(w, 503, "dataset cleanup storage authority unavailable")
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), nativeAnalysisArtifactBackendKey{}, backend))
	if in.State == "deleted" {
		w.WriteHeader(204)
		return
	}
	if in.State == "ready" || in.State == "deleting" {
		in.State = "deleting"
	} else {
		in.State = "cancelled"
	}
	saved, err := store.SaveAnalysisImport(*in, in.Revision)
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	// Cancellation invalidates the worker's revision immediately. Repeating
	// DELETE retries any physical cleanup interrupted by a storage outage.
	cleanup, ok := s.store.(interface {
		AnalysisDatasetCleanupReady(context.Context, string, string) (bool, error)
	})
	if !ok {
		writeError(w, 503, "dataset cleanup ownership unavailable")
		return
	}
	ready, readyErr := cleanup.AnalysisDatasetCleanupReady(r.Context(), saved.ProjectID, saved.ID)
	if readyErr != nil || !ready {
		writeError(w, 503, "dataset cleanup pending physical writer completion")
		return
	}
	if err = deleteAnalysisObjects(r.Context(), saved); err != nil {
		writeError(w, 503, "dataset cleanup pending; retry DELETE")
		return
	}
	if saved.State == "deleting" {
		saved.State = "deleted"
		if _, err = store.SaveAnalysisImport(*saved, saved.Revision); err != nil {
			writeError(w, 409, err.Error())
			return
		}
	}
	s.recordAuditEvent(r, "analysis.import.delete", "success", "analysis_import", in.ID, in.ProjectID, nil)
	w.WriteHeader(204)
}

func analysisChunkName(file storage.AnalysisImportFile, offset int64, digest string) string {
	return fmt.Sprintf("chunk-%s-%012d-%s", file.ID, offset, digest)
}

func (s *Server) handleAnalysisUpload(w http.ResponseWriter, r *http.Request) {
	// Pin SQL and immutable artifact namespace before body allocation/admission.
	// A request-side Store replacement requires a newly configured Server.
	if s == nil || !nativeSameStore(s.store, s.nativeReconciliationStore) {
		writeError(w, 503, "upload storage authority unavailable")
		return
	}
	writers, writerOK := s.nativeReconciliationStore.(storage.AnalysisUploadWriterStore)
	store, storeOK := s.nativeReconciliationStore.(storage.AnalysisImportStore)
	if !writerOK || !storeOK {
		writeError(w, 503, "upload writer ownership unavailable")
		return
	}
	bound, _, err := s.bindAnalysisUploadContext(r.Context(), writers)
	if err != nil {
		writeError(w, 503, "upload storage authority unavailable")
		return
	}
	r = r.WithContext(bound)
	in, err := store.GetAnalysisImport(r.PathValue("id"))
	if err != nil || in == nil || !analysisQueryContextLive(r.Context()) {
		writeError(w, 404, "analysis import not found")
		return
	}
	if !requireAuthorizedProjectMatch(w, r, in.ProjectID) {
		return
	}
	if !analysisProjectEnabled(in.ProjectID) {
		writeError(w, 409, "Parquet analysis is not enabled for this project")
		return
	}
	if !analytics.Available() {
		writeError(w, 501, analytics.ErrUnavailable.Error())
		return
	}
	if in.UploadWriterVersion != storage.AnalysisUploadWriterVersion {
		writeError(w, 503, "upload writer ownership unavailable for this session")
		return
	}
	if in.State != "uploading" {
		writeError(w, 409, "import is not accepting uploads")
		return
	}
	index := -1
	for i, file := range in.Files {
		if file.ID == r.PathValue("file_id") {
			index = i
			break
		}
	}
	if index < 0 {
		writeError(w, 404, "import file not found")
		return
	}
	file := &in.Files[index]
	offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || offset < 0 || offset > file.Uploaded {
		writeError(w, 409, "upload offset does not match durable cursor")
		return
	}
	if !analysisQueryContextLive(r.Context()) {
		writeError(w, 503, "upload request authority expired")
		return
	}
	uploadOwner, admitted := acquireAnalysisUploadOwner()
	if !admitted {
		writeError(w, 429, "upload writer capacity unavailable")
		return
	}
	defer uploadOwner.endRequest()
	uploadCtx, cancelUpload := context.WithTimeout(r.Context(), analysisUploadOperationTimeout)
	defer cancelUpload()
	r = r.WithContext(context.WithValue(uploadCtx, analysisUploadOwnerKey{}, uploadOwner))
	r.Body = http.MaxBytesReader(w, r.Body, analysisUploadChunk)
	data, err := readAnalysisUploadBody(r.Context(), uploadOwner, r.Body)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errAnalysisUploadPhysical) {
		writeError(w, 503, "upload body ownership remains pending")
		return
	}
	if err != nil || len(data) > analysisUploadChunk {
		writeError(w, 413, "upload chunk exceeds 4 MiB")
		return
	}
	if len(data) == 0 || int64(len(data)) > file.Size-offset {
		writeError(w, 400, "chunk exceeds declared file size")
		return
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	if offset < file.Uploaded {
		for _, chunk := range file.Chunks {
			if chunk.Offset == offset && chunk.Size == int64(len(data)) && chunk.SHA256 == digest {
				if err := s.writeAnalysisUploadCursor(w, r, *in, file.ID, file.Uploaded, in.Revision); err != nil {
					writeAnalysisUploadCursorError(w, err)
				}
				return
			}
		}
		writeError(w, 409, "conflicting upload retry")
		return
	}
	if offset+int64(len(data)) < file.Size && len(data) != analysisUploadChunk {
		writeError(w, 400, "nonfinal chunks must contain exactly 4 MiB")
		return
	}
	s.writeAnalysisUploadChunk(w, r, writers, *in, *file, offset, data, digest)

}

func materializeAnalysisOriginal(ctx context.Context, in *storage.AnalysisImport, file storage.AnalysisImportFile, dir string) (retPath string, retDigest string, retErr error) {
	if file.Uploaded != file.Size {
		return "", "", errors.New("file upload incomplete")
	}
	lease, err := analytics.NativePipelineIO(ctx)
	if err != nil {
		return "", "", err
	}
	defer lease.Joined()
	local := filepath.Join(dir, file.ID+".parquet")
	out, err := os.OpenFile(local, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", "", err
	}
	lease.TrackOwner(out)
	defer func() { retErr = errors.Join(retErr, analysisCloseNativeOwner(lease, out)) }()
	hash := sha256.New()
	remaining := file.Size
	staging := &analysisStagingWriter{out: out, dir: dir, remaining: &remaining}
	var offset int64
	for _, chunk := range file.Chunks {
		if chunk.Offset != offset || chunk.Size < 1 {
			return "", "", errors.New("invalid durable upload sequence")
		}
		key, keyErr := storage.AnalysisUploadChunkObjectKey(in.ProjectID, in.ID, file, chunk)
		if keyErr != nil {
			return "", "", keyErr
		}
		digest, err := analysisObjectRead(ctx, key, io.MultiWriter(staging, hash), chunk.Size)
		if err != nil {
			return "", "", err
		}
		if digest != chunk.SHA256 {
			return "", "", errors.New("uploaded chunk hash mismatch")
		}
		offset += chunk.Size
	}
	if offset != file.Size {
		return "", "", errors.New("uploaded file size mismatch")
	}
	if err = out.Sync(); err != nil {
		return "", "", err
	}
	return local, hex.EncodeToString(hash.Sum(nil)), nil
}

func analysisScratch() (string, error) {
	root := filepath.Join(currentArchiveBackend().LocalRoot, "_analysis-cache")
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	return os.MkdirTemp(root, "job-")
}

func (s *Server) handleAnalysisPreview(w http.ResponseWriter, r *http.Request) {
	if !admitAnalysisPipeline(w) {
		return
	}
	defer func() { <-analysisPipelineJobs }()
	_, in := s.loadAnalysisImport(w, r)
	if in == nil {
		return
	}
	if !nativePreviewStateAvailable(in.State) {
		writeError(w, 409, "import is not available")
		return
	}
	var selected *storage.AnalysisImportFile
	for i := range in.Files {
		if in.Files[i].ID == r.PathValue("file_id") {
			selected = &in.Files[i]
			break
		}
	}
	if selected == nil {
		writeError(w, 404, "import file not found")
		return
	}
	ctx, pipeline, dir, err := beginNativeAnalysisPipeline(r.Context(), selected.Size, 12)
	if err != nil {
		writeError(w, 503, "analysis staging unavailable")
		return
	}
	defer closeNativeAnalysisPipeline(pipeline, dir)
	file, _, err := materializeAnalysisOriginal(ctx, in, *selected, dir)
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	engine, err := analytics.NewForNativePipeline(ctx, dir)
	if err != nil {
		writeError(w, 503, err.Error())
		return
	}
	preview, err := engine.Preview(ctx, file)
	if err != nil {
		writeError(w, 422, "Cannot inspect Parquet: invalid file, unsupported schema, or analytical resource limit.")
		return
	}
	s.writeNativeAnalysisPreview(w, r, in, preview)
}

func (s *Server) handleAnalysisMapping(w http.ResponseWriter, r *http.Request) {
	store, in := s.loadAnalysisImport(w, r)
	if store == nil {
		return
	}
	if in.State != "uploading" && in.State != "mapped" && in.State != "failed" {
		writeError(w, 409, "mapping is immutable in this import state")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	var req struct {
		Revision        int64           `json:"revision"`
		Mapping         json.RawMessage `json:"mapping"`
		AllowRejected   bool            `json:"allow_rejected"`
		MappingID       string          `json:"mapping_id"`
		MappingRevision int64           `json:"mapping_revision"`
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			writeError(w, 413, "mapping exceeds request byte limit")
		} else {
			writeError(w, 400, "invalid mapping request")
		}
		return
	}
	if err := decodeAnalysisDefinitionJSON(body, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	mapping, err := analysis.DecodeParquetMappingDefinition(req.Mapping)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := mapping.RequireDeclaredSummaries(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if req.Revision != in.Revision {
		writeError(w, 409, "mapping revision conflict")
		return
	}
	for _, file := range in.Files {
		if file.Uploaded != file.Size {
			writeError(w, 409, "all files must finish uploading before mapping")
			return
		}
	}
	if req.MappingID != "" {
		definitions, ok := s.store.(storage.AnalysisDefinitionStore)
		if !ok || req.MappingRevision < 1 {
			writeError(w, 400, "saved mapping revision required")
			return
		}
		definition, err := definitions.GetAnalysisDefinition(in.ProjectID, req.MappingID, req.MappingRevision)
		if err != nil || definition.Kind != "mapping" || definition.Deleted {
			writeError(w, 400, "saved mapping does not match this revision")
			return
		}
		expectedMapping, err := analysis.DecodeParquetMappingDefinition(definition.Definition)
		if err != nil || !reflect.DeepEqual(expectedMapping, mapping) {
			writeError(w, 400, "saved mapping does not match this revision")
			return
		}
	}
	in.MappingID, in.MappingRevision = req.MappingID, req.MappingRevision
	in.Mapping = &mapping
	in.AllowRejected = req.AllowRejected
	in.State = "mapped"
	in.Error = ""
	saved, err := store.SaveAnalysisImport(*in, in.Revision)
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, saved)
}

func (s *Server) handleAnalysisCommit(w http.ResponseWriter, r *http.Request) {
	store, in := s.loadAnalysisImport(w, r)
	if store == nil {
		return
	}
	if in.State == "ready" {
		writeJSON(w, 200, in)
		return
	}
	if in.State == "normalizing" && in.LeaseExpiresAt.After(time.Now()) {
		writeJSON(w, 202, in)
		return
	}
	if in.Mapping == nil || (in.State != "mapped" && in.State != "failed" && in.State != "normalizing") {
		writeError(w, 409, "a completed upload and mapping are required")
		return
	}
	if len(in.Files)*len(in.Mapping.Measures) > 1024 {
		writeError(w, 422, "mapping exceeds the 1024 normalized partition limit")
		return
	}
	if !admitAnalysisPipeline(w) {
		return
	}
	launched := false
	defer func() {
		if !launched {
			<-analysisPipelineJobs
		}
	}()
	attempts, ok := s.store.(storage.AnalysisAttemptStore)
	if !ok {
		writeError(w, 503, "native attempt authority unavailable")
		return
	}
	var inputBytes int64
	for _, file := range in.Files {
		if file.Size < 12 || file.Size > analysisImportMaxBytes-inputBytes {
			writeError(w, 422, "invalid analytical resource geometry")
			return
		}
		inputBytes += file.Size
	}
	slots := int64(len(in.Files) * len(in.Mapping.Measures))
	if in.AllowRejected {
		slots *= 2
	}
	outputBytes, err := nativeNormalizationGeometry(slots, inputBytes)
	if err != nil {
		writeError(w, 422, err.Error())
		return
	}
	parent, stop := s.runner.archiveOperationContext(s.startupContext)
	ctx, pipeline, dir, err := beginNativeAnalysisPipeline(parent, inputBytes, outputBytes)
	if err != nil {
		stop()
		writeError(w, 503, "analytical admission unavailable")
		return
	}
	claimed, _, err := attempts.ClaimAnalysisImportAttempt(r.Context(), in.ID, in.ProjectID, in.Revision, time.Minute)
	if err != nil {
		stop()
		_ = closeNativeAnalysisPipeline(pipeline, dir)
		writeError(w, 409, "analytical claim changed or requires reconciliation")
		return
	}
	launched = s.launchBackground(func() {
		defer func() { <-analysisPipelineJobs }()
		defer stop()
		s.normalizeAnalysisImportOwned(ctx, store, attempts, *claimed, pipeline, dir)
	})
	if !launched {
		s.failUnlaunchedAnalysisImport(ctx, r.Context(), attempts, *claimed, pipeline, dir, stop)
		writeError(w, 503, "controller is shutting down")
		return
	}
	s.recordAuditEvent(r, "analysis.import.commit", "started", "analysis_import", in.ID, in.ProjectID, nil)
	writeJSON(w, 202, claimed)
}

// Resolve the complete exact plan before physical I/O. New upload writers use
// opaque durable chunk keys; historical chunks retain their validated content
// key. A malformed/foreign later key cannot cause a partially deleted dataset.
func analysisDatasetDeletionKeys(in *storage.AnalysisImport) ([]string, error) {
	const maxKeys = 131072
	if in == nil || len(in.ID) != 36 || len(in.ProjectID) > 128 || len(in.Files) < 1 || len(in.Files) > 1024 || len(in.Normalized) > 65536 || len(in.RejectionArtifacts) > 65536 || len(in.ObsoleteObjects) > 4096 {
		return nil, storage.ErrAnalysisAuthority
	}
	total := len(in.Files) + len(in.Normalized) + len(in.RejectionArtifacts) + len(in.ObsoleteObjects)
	for _, file := range in.Files {
		if len(file.ID) != 36 || len(file.Chunks) > 16384 || len(file.Chunks) > maxKeys-total {
			return nil, storage.ErrAnalysisAuthority
		}
		total += len(file.Chunks)
	}
	if total > maxKeys || strings.TrimSpace(in.ProjectID) == "" {
		return nil, storage.ErrAnalysisAuthority
	}
	parsed, err := uuid.Parse(in.ID)
	if err != nil || parsed.String() != in.ID {
		return nil, storage.ErrAnalysisAuthority
	}
	for _, file := range in.Files {
		parsed, err := uuid.Parse(file.ID)
		if err != nil || parsed.String() != file.ID {
			return nil, storage.ErrAnalysisAuthority
		}
	}
	prefix := analysisObjectKey(in.ProjectID, in.ID, "")
	keys := []string{}
	seen := map[string]bool{}
	add := func(key string) error {
		if len(key) > 274 || !analysisObjectPattern.MatchString(key) || !strings.HasPrefix(key, prefix) {
			return storage.ErrAnalysisAuthority
		}
		if !seen[key] {
			if len(keys) == maxKeys {
				return storage.ErrAnalysisAuthority
			}
			seen[key] = true
			keys = append(keys, key)
		}
		return nil
	}
	for _, file := range in.Files {
		if len(file.Chunks) > 16384 {
			return nil, storage.ErrAnalysisAuthority
		}
		for _, chunk := range file.Chunks {
			key, err := storage.AnalysisUploadChunkObjectKey(in.ProjectID, in.ID, file, chunk)
			if err != nil {
				return nil, err
			}
			if err = add(key); err != nil {
				return nil, err
			}
		}
		if err := add(analysisObjectKey(in.ProjectID, in.ID, "original-"+file.ID+".parquet")); err != nil {
			return nil, err
		}
	}
	for _, group := range [][]string{in.Normalized, in.ObsoleteObjects, in.RejectionArtifacts} {
		for _, key := range group {
			if err := add(key); err != nil {
				return nil, err
			}
		}
	}
	return keys, nil
}
func deleteAnalysisObjects(ctx context.Context, in *storage.AnalysisImport) error {
	keys, err := analysisDatasetDeletionKeys(in)
	if err != nil {
		return err
	}
	backend, err := nativeAnalysisArtifactBackend(ctx)
	if err != nil || backend.validate() != nil {
		return errNativeReconciliationRetained
	}
	for _, key := range keys {
		if err = deleteAnalysisObjectPhysical(ctx, backend, key); err != nil {
			return err
		}
	}
	// The caller has already revoked the live state and obtained the durable
	// all-writers-quiesced namespace proof. Closed abandoned upload intents and
	// temporary files may not appear in the accepted metadata cursor; they must
	// also be physically removed before a successful terminal DELETE response.
	return deleteAnalysisNamespacePhysical(ctx, backend, filepath.Dir(analysisObjectKey(in.ProjectID, in.ID, "validation")))
}

func (s *Server) handleAnalysisDatasetSeries(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(withAnalysisArtifactSelections(r.Context()), time.Minute)
	defer cancel()
	r = r.WithContext(ctx)
	if !admitAnalysisPipeline(w) {
		return
	}
	defer func() { <-analysisPipelineJobs }()
	_, in := s.loadAnalysisImportMetadata(w, r)
	if in == nil {
		return
	}
	if !analysisQueryContextLive(r.Context()) {
		writeError(w, 409, "analytical query authority expired")
		return
	}
	if s.analysisImportStore(w, in.ProjectID) == nil {
		return
	}
	if in.State != "ready" {
		writeError(w, 409, "dataset is not committed")
		return
	}
	var query analytics.SeriesQuery
	if !decodeAnalysisQueryRequest(w, r, &query) {
		return
	}
	if query.ProjectID != "" && query.ProjectID != in.ProjectID || query.DatasetID != "" && query.DatasetID != in.ID {
		writeError(w, 400, "analytical query ownership does not match this dataset")
		return
	}
	query.ProjectID, query.DatasetID = in.ProjectID, in.ID
	query, err := bindImportedNativeSeriesQuery(in, query)
	if err != nil {
		writeError(w, 422, err.Error())
		return
	}
	window := nativeAnalysisTimeWindow(query.From, query.To)
	query.From, query.To = window.From, window.To
	if err := query.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	r = r.WithContext(withAnalysisTimeWindow(r.Context(), window))
	points, err := queryAnalysisDataset(r.Context(), in, query)
	if err != nil {
		writeError(w, 422, err.Error())
		return
	}
	s.writeNativeDatasetSeries(w, r, in, points)
}
