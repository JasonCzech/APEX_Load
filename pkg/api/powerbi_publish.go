package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

// powerBIPublishStore is the storage capability the publish flow needs; it is
// type-asserted off the server's store (pattern: githubEnterpriseStore).
type powerBIPublishStore interface {
	SavePowerBIPublish(publish *storage.PowerBIPublish) error
	UpdatePowerBIPublishStatus(id string, status string, runRows int, txRows int, errMsg string, publishedAt *time.Time) error
	GetLatestPowerBIPublishForTest(testID string) (*storage.PowerBIPublish, error)
	GetLatestPowerBIPublishForDataset(datasetID string) (*storage.PowerBIPublish, error)
	ListPowerBIPublishesForTest(testID string, limit int) ([]*storage.PowerBIPublish, error)
}

func (s *Server) powerBIPublishStoreOrNil() (powerBIPublishStore, bool) {
	if s == nil || s.store == nil {
		return nil, false
	}
	store, ok := s.store.(powerBIPublishStore)
	return store, ok
}

func (s *Server) powerBISettings() PowerBISettings {
	settings, _ := s.runner.systemSettingsSnapshot()
	return normalizePowerBISettings(settings.PowerBI)
}

// validatePowerBITransportURLs is the publish-time fail-closed check that both
// configured endpoints still satisfy the credential-transport policy (settings
// may have been persisted before production mode was enabled).
func validatePowerBITransportURLs(settings PowerBISettings) error {
	if err := validateCredentialTransportURL("power bi auth URL", settings.AuthBaseURL, powerBIInsecureHTTPEnv); err != nil {
		return err
	}
	return validateCredentialTransportURL("power bi api URL", settings.APIBaseURL, powerBIInsecureHTTPEnv)
}

// apexloadRunURL builds the deep link back to the run's report page that is
// pushed into the shared BI dataset. The admin-configured DashboardBaseURL
// wins; otherwise the publish request's own host is used after strict
// syntactic validation, because this value is durable content other workspace
// users will click.
func apexloadRunURL(r *http.Request, settings PowerBISettings, testID string) string {
	if settings.DashboardBaseURL != "" {
		return settings.DashboardBaseURL + "/tests/" + url.PathEscape(testID) + "/report"
	}
	host := strings.TrimSpace(r.Host)
	if !validPowerBILinkHost(host) {
		return ""
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s/tests/%s/report", scheme, host, url.PathEscape(testID))
}

// validPowerBILinkHost accepts only a plain host[:port] (or a bracketed IPv6
// literal) so a forged Host header cannot smuggle URL structure (paths,
// userinfo, a second authority) or produce a malformed URL in the links
// stored in the BI dataset.
func validPowerBILinkHost(raw string) bool {
	if raw == "" {
		return false
	}
	if host, port, err := net.SplitHostPort(raw); err == nil {
		// ParseUint rejects sign characters; port 0 is not routable.
		if parsed, perr := strconv.ParseUint(port, 10, 16); perr != nil || parsed == 0 {
			return false
		}
		if strings.Contains(host, ":") {
			// SplitHostPort only strips brackets from "[v6]:port" forms, so a
			// colon here means the original was properly bracketed.
			return net.ParseIP(host) != nil
		}
		return plainPowerBILinkHostname(host)
	}
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		inner := strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]")
		return inner != "" && strings.Contains(inner, ":") && net.ParseIP(inner) != nil
	}
	// A bare unbracketed IPv6 Host would render as a malformed URL — reject.
	if strings.Contains(raw, ":") {
		return false
	}
	return plainPowerBILinkHostname(raw)
}

func plainPowerBILinkHostname(host string) bool {
	if host == "" {
		return false
	}
	for _, r := range host {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' {
			continue
		}
		return false
	}
	return true
}

// powerBIPublishLocks serializes publishes per test so the dedup
// check-then-act below cannot race two concurrent clicks into duplicate rows.
// TryLock (not Lock) because a publish can hold the lock for the full external
// push; a second request must get an immediate "in progress" conflict rather
// than piling up a goroutine that cannot honor its own context deadline.
var powerBIPublishLocks = struct {
	sync.Mutex
	inFlight map[string]struct{}
}{inFlight: make(map[string]struct{})}

func tryLockPowerBIPublish(testID string) (func(), bool) {
	powerBIPublishLocks.Lock()
	defer powerBIPublishLocks.Unlock()
	if _, exists := powerBIPublishLocks.inFlight[testID]; exists {
		return nil, false
	}
	powerBIPublishLocks.inFlight[testID] = struct{}{}
	var releaseOnce sync.Once
	return func() {
		releaseOnce.Do(func() {
			powerBIPublishLocks.Lock()
			delete(powerBIPublishLocks.inFlight, testID)
			powerBIPublishLocks.Unlock()
		})
	}, true
}

func powerBIPublishLockCount() int {
	powerBIPublishLocks.Lock()
	defer powerBIPublishLocks.Unlock()
	return len(powerBIPublishLocks.inFlight)
}

// powerBIPublishConflict reports whether a prior publish of this test into the
// same remote dataset blocks a non-forced publish. Any prior attempt that
// pushed (or may have pushed) rows blocks: push datasets cannot delete rows,
// so re-publishing appends duplicates that reports must filter by publish_id.
func powerBIPublishConflict(latest *storage.PowerBIPublish, datasetID string, force bool) (bool, string) {
	if force || latest == nil || latest.DatasetID != datasetID {
		return false, ""
	}
	switch latest.Status {
	case storage.PowerBIPublishStatusComplete:
		return true, "this run was already published to the configured Power BI dataset; re-publishing appends duplicate rows — pass force=true to publish anyway"
	case storage.PowerBIPublishStatusPartial:
		return true, fmt.Sprintf("a previous publish pushed %d of %d transaction rows before failing; those rows remain in the dataset and re-publishing appends a full duplicate set — pass force=true to continue (reports must keep only the latest publish_id per run)", latest.TransactionRowsPushed, latest.TransactionRowsTotal)
	case storage.PowerBIPublishStatusPending:
		// A pending row with zero recorded rows is treated as a crashed attempt
		// that pushed nothing. This is necessarily optimistic: the service may
		// have applied a single-attempt chunk before the client crashed or saw an
		// ambiguous response. A forced re-publish remains distinguishable by its
		// new publish_id, as documented in docs/OBSERVABILITY_INTEGRATIONS.md.
		if latest.TransactionRowsPushed > 0 || latest.RunRowsPushed > 0 {
			return true, "a previous publish attempt pushed rows but never completed; re-publishing appends duplicates — pass force=true to continue"
		}
	}
	return false, ""
}

// applyPowerBIVerdictOverride replaces the auto-derived verdict with the run's
// analyst verdict, matching what the rendered HTML/PDF/CSV reports display.
func applyPowerBIVerdictOverride(store storage.Store, report *RunArchiveReport, testID string) {
	if store == nil || report == nil || report.Verdict == nil {
		return
	}
	verdict, err := loadRunReportVerdict(store, testID)
	if err != nil || verdict == nil || strings.TrimSpace(verdict.VerdictOverride) == "" {
		return
	}
	overridden := applyVerdictOverride(verdict.VerdictOverride, verdict.VerdictExplanation, *report.Verdict)
	report.Verdict = &overridden
}

type powerBIPublishRequest struct {
	Force bool `json:"force"`
}

func (s *Server) handlePublishRunToPowerBI(w http.ResponseWriter, r *http.Request) {
	settings := s.powerBISettings()
	if !settings.Enabled {
		writeError(w, http.StatusBadRequest, "power bi integration is not enabled")
		return
	}
	if err := validatePowerBITransportURLs(settings); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	store, ok := s.powerBIPublishStoreOrNil()
	if !ok {
		writeError(w, http.StatusInternalServerError, "power bi publish state storage is unavailable")
		return
	}

	var body powerBIPublishRequest
	if r.Body != nil {
		// An empty body means a plain publish; only a malformed non-empty body
		// is rejected.
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
	}

	testID := r.PathValue("id")
	unlock, acquired := tryLockPowerBIPublish(testID)
	if !acquired {
		// No publish record in the body: the frontend renders a record-less 409
		// as a plain error rather than the force-republish confirm.
		writeError(w, http.StatusConflict, "a Power BI publish for this run is already in progress")
		return
	}
	defer unlock()

	record, lease, ok := s.requireRunArchive(w, r)
	if !ok {
		return
	}
	defer lease.Release()

	report, err := cachedRunArchiveReport(record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build archive report: "+err.Error())
		return
	}
	attachReportVerdictAndSLA(report)
	applyPowerBIVerdictOverride(s.store, report, testID)
	// The archive files are no longer needed once the in-memory report is
	// built; release the materialization lease (idempotent) before the
	// potentially minutes-long external push I/O.
	lease.Release()

	publishedAt := time.Now().UTC()
	publishID := uuid.NewString()
	txRows := buildPowerBITransactionRows(report, publishID, publishedAt)
	runRow := buildPowerBIRunRow(report, publishID, publishedAt, apexloadRunURL(r, settings, testID))
	_, actorSubject, _ := s.resolveAuditActor(r)

	ctx := r.Context()
	client := newPowerBIClient(settings)
	datasetID, created, err := client.ensureDataset(ctx)
	if err != nil {
		// No rows were pushed, but the attempt must still be visible in the
		// publish history and audit trail (parity with later failure paths).
		failed := &storage.PowerBIPublish{
			ID:                   publishID,
			TestID:               testID,
			ProjectID:            report.Manifest.Provenance.ProjectID,
			WorkspaceID:          settings.WorkspaceID,
			DatasetName:          settings.DatasetName,
			SchemaVersion:        powerBIDatasetSchemaVersion,
			Status:               storage.PowerBIPublishStatusFailed,
			TransactionRowsTotal: len(txRows),
			Error:                err.Error(),
			RequestedBy:          actorSubject,
		}
		if saveErr := store.SavePowerBIPublish(failed); saveErr != nil {
			slog.Warn("failed to record power bi publish failure", "test_id", testID, "err", saveErr)
		}
		s.recordAuditEvent(r, "powerbi.publish", "failure", "test", testID, failed.ProjectID, map[string]any{
			"publish_id": publishID,
			"error":      err.Error(),
		})
		writeError(w, http.StatusBadGateway, "power bi publish failed: "+err.Error())
		return
	}

	// Dedup is keyed on the remote dataset id, so a recreated dataset (new id)
	// never false-conflicts with history from the old one.
	latest, err := store.GetLatestPowerBIPublishForTest(testID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load publish history: "+err.Error())
		return
	}
	if conflict, reason := powerBIPublishConflict(latest, datasetID, body.Force); conflict {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   reason,
			"publish": latest,
		})
		return
	}

	// Drift detection is dataset-scoped (fetched before our own pending row is
	// inserted): the last publish of *this test* can predate a schema bump that
	// other tests' publishes already carried into the dataset.
	var latestForDataset *storage.PowerBIPublish
	if !created {
		latestForDataset, err = store.GetLatestPowerBIPublishForDataset(datasetID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load dataset publish history: "+err.Error())
			return
		}
	}

	publish := &storage.PowerBIPublish{
		ID:                   publishID,
		TestID:               testID,
		ProjectID:            report.Manifest.Provenance.ProjectID,
		WorkspaceID:          settings.WorkspaceID,
		DatasetID:            datasetID,
		DatasetName:          settings.DatasetName,
		SchemaVersion:        powerBIDatasetSchemaVersion,
		Status:               storage.PowerBIPublishStatusPending,
		TransactionRowsTotal: len(txRows),
		RequestedBy:          actorSubject,
	}
	if err := store.SavePowerBIPublish(publish); err != nil {
		// ensureDataset already ran (and may have created a remote dataset), so
		// even this pre-push storage failure belongs in the audit trail.
		s.recordAuditEvent(r, "powerbi.publish", "failure", "test", testID, publish.ProjectID, map[string]any{
			"publish_id": publishID,
			"error":      "failed to record publish intent: " + err.Error(),
		})
		writeError(w, http.StatusInternalServerError, "failed to record publish intent: "+err.Error())
		return
	}

	failPublish := func(statusCode int, publishStatus string, pushedTx int, cause error) {
		message := cause.Error()
		if err := store.UpdatePowerBIPublishStatus(publishID, publishStatus, 0, pushedTx, message, nil); err != nil {
			message += " (and failed to persist publish state: " + err.Error() + ")"
		}
		s.recordAuditEvent(r, "powerbi.publish", "failure", "test", testID, publish.ProjectID, map[string]any{
			"publish_id": publishID,
			"error":      message,
		})
		writeError(w, statusCode, "power bi publish failed: "+message)
	}

	if !created {
		if err := s.checkPowerBIDatasetDrift(ctx, client, datasetID, latestForDataset); err != nil {
			failPublish(http.StatusConflict, storage.PowerBIPublishStatusFailed, 0, err)
			return
		}
	}

	pushedTx, err := client.pushRows(ctx, datasetID, powerBITransactionsTable, txRows, func(pushed int) {
		// Progress persistence is best-effort; the publish outcome is decided
		// by the final status update below.
		_ = store.UpdatePowerBIPublishStatus(publishID, storage.PowerBIPublishStatusPending, 0, pushed, "", nil)
	})
	if err != nil {
		status := storage.PowerBIPublishStatusFailed
		if pushedTx > 0 {
			status = storage.PowerBIPublishStatusPartial
		}
		failPublish(http.StatusBadGateway, status, pushedTx, err)
		return
	}

	// The runs row is pushed last as the commit marker: a run only appears in
	// trend reports once all of its transaction rows landed.
	if _, err := client.pushRows(ctx, datasetID, powerBIRunsTable, []map[string]any{runRow}, nil); err != nil {
		failPublish(http.StatusBadGateway, storage.PowerBIPublishStatusPartial, pushedTx, err)
		return
	}

	if err := store.UpdatePowerBIPublishStatus(publishID, storage.PowerBIPublishStatusComplete, 1, pushedTx, "", &publishedAt); err != nil {
		// The external push fully succeeded — that state mutation must be
		// audited even though the local completion record could not be written
		// (the row stays pending-with-rows, which still blocks a plain
		// re-publish).
		s.recordAuditEvent(r, "powerbi.publish", "partial", "test", testID, publish.ProjectID, map[string]any{
			"publish_id":       publishID,
			"dataset_id":       datasetID,
			"transaction_rows": pushedTx,
			"error":            "rows were published but recording completion failed: " + err.Error(),
		})
		writeError(w, http.StatusInternalServerError, "rows were published but recording completion failed: "+err.Error())
		return
	}
	publish.Status = storage.PowerBIPublishStatusComplete
	publish.RunRowsPushed = 1
	publish.TransactionRowsPushed = pushedTx
	publish.PublishedAt = &publishedAt

	s.recordAuditEvent(r, "powerbi.publish", "success", "test", testID, publish.ProjectID, map[string]any{
		"publish_id":       publishID,
		"dataset_id":       datasetID,
		"workspace_id":     settings.WorkspaceID,
		"transaction_rows": pushedTx,
		"forced":           body.Force,
	})
	writeJSON(w, http.StatusOK, map[string]any{"publish": publish})
}

// checkPowerBIDatasetDrift rejects publishing into a dataset whose layout no
// longer matches this build: either the remote table set differs, or the last
// publish was made with a different schema version. Push datasets cannot be
// altered in place, so the remedy is the admin recreate endpoint.
func (s *Server) checkPowerBIDatasetDrift(ctx context.Context, client *powerBIClient, datasetID string, latest *storage.PowerBIPublish) error {
	tables, err := client.listTables(ctx, datasetID)
	if err != nil {
		return fmt.Errorf("inspect dataset tables: %w", err)
	}
	found := map[string]bool{}
	for _, table := range tables {
		found[strings.ToLower(strings.TrimSpace(table.Name))] = true
	}
	if !found[powerBIRunsTable] || !found[powerBITransactionsTable] {
		return fmt.Errorf("dataset %q does not contain the expected runs/transactions tables; recreate the dataset from the Power BI integration settings", client.settings.DatasetName)
	}
	if latest != nil && latest.DatasetID == datasetID && latest.SchemaVersion != 0 && latest.SchemaVersion != powerBIDatasetSchemaVersion {
		return fmt.Errorf("dataset %q was created with schema version %d but this build expects %d; recreate the dataset from the Power BI integration settings", client.settings.DatasetName, latest.SchemaVersion, powerBIDatasetSchemaVersion)
	}
	return nil
}

func (s *Server) handleGetPowerBIPublishStatus(w http.ResponseWriter, r *http.Request) {
	settings := s.powerBISettings()
	response := map[string]any{
		"enabled":   settings.Enabled,
		"publishes": []*storage.PowerBIPublish{},
	}
	if store, ok := s.powerBIPublishStoreOrNil(); ok {
		publishes, err := store.ListPowerBIPublishesForTest(r.PathValue("id"), 20)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load publish history: "+err.Error())
			return
		}
		response["publishes"] = publishes
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleTestPowerBIConnection(w http.ResponseWriter, r *http.Request) {
	settings := s.powerBISettings()
	if !settings.Enabled {
		writeError(w, http.StatusBadRequest, "power bi integration is not enabled")
		return
	}
	if err := validatePowerBITransportURLs(settings); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	client := newPowerBIClient(settings)
	dataset, err := client.findDatasetByName(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	response := map[string]any{
		"ok":             true,
		"workspace_id":   settings.WorkspaceID,
		"dataset_name":   settings.DatasetName,
		"dataset_exists": dataset != nil,
	}
	if dataset != nil {
		response["dataset_id"] = dataset.ID
	}
	writeJSON(w, http.StatusOK, response)
}

// handleRecreatePowerBIDataset deletes and recreates the configured push
// dataset with the current schema. This destroys all previously pushed rows in
// Power BI (publish history in APEX Load is retained) — the dashboard confirms
// this with the admin before calling.
func (s *Server) handleRecreatePowerBIDataset(w http.ResponseWriter, r *http.Request) {
	settings := s.powerBISettings()
	if !settings.Enabled {
		writeError(w, http.StatusBadRequest, "power bi integration is not enabled")
		return
	}
	if err := validatePowerBITransportURLs(settings); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	client := newPowerBIClient(settings)
	ctx := r.Context()
	existing, err := client.findDatasetByName(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to inspect workspace: "+err.Error())
		return
	}
	if existing != nil {
		if err := client.deleteDataset(ctx, existing.ID); err != nil {
			writeError(w, http.StatusBadGateway, "failed to delete existing dataset: "+err.Error())
			return
		}
	}
	var out powerBIDataset
	if err := client.doJSON(ctx, http.MethodPost, client.groupURL("datasets"), powerBIDatasetDefinition(settings.DatasetName), &out); err != nil {
		writeError(w, http.StatusBadGateway, "failed to create dataset: "+err.Error())
		return
	}
	s.recordAuditEvent(r, "powerbi.dataset.recreate", "success", "system", settings.WorkspaceID, "", map[string]any{
		"dataset_name":     settings.DatasetName,
		"dataset_id":       out.ID,
		"deleted_previous": existing != nil,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"dataset_id":     out.ID,
		"dataset_name":   settings.DatasetName,
		"schema_version": powerBIDatasetSchemaVersion,
	})
}
