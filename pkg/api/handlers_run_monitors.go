package api

import (
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/storage"
)

func runMonitoringEnabled(project string) bool {
	for _, p := range strings.Split(os.Getenv("APEXLOAD_RUN_MONITOR_PROJECTS"), ",") {
		if strings.TrimSpace(p) == project && project != "" {
			return true
		}
	}
	return false
}

func monitorSigningKey() (ed25519.PrivateKey, error) {
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("APEXLOAD_MONITOR_SIGNING_SEED")))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("run monitoring requires a persistent base64 Ed25519 signing seed")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func (s *Server) runMonitorStore(w http.ResponseWriter, project string) storage.RunMonitorStore {
	if !runMonitoringEnabled(project) {
		writeError(w, http.StatusConflict, "run monitoring is not enabled for this project")
		return nil
	}
	store, ok := s.store.(storage.RunMonitorStore)
	if !ok {
		writeError(w, http.StatusNotImplemented, "run monitoring storage unavailable")
		return nil
	}
	return store
}

func (s *Server) handleMonitorSources(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("id")
	store := s.runMonitorStore(w, project)
	if store == nil {
		return
	}
	if r.Method == http.MethodGet {
		sources, err := store.ListMonitorSources(project)
		if err != nil {
			writeError(w, 500, "cannot load monitor sources")
			return
		}
		for i := range sources {
			sources[i] = safeMonitorSource(sources[i])
		}
		writeJSON(w, 200, sources)
		return
	}
	if r.Method == http.MethodDelete {
		expected, err := strconv.ParseInt(r.URL.Query().Get("revision"), 10, 64)
		if err != nil {
			writeError(w, 400, "revision is required")
			return
		}
		if err = store.DeleteMonitorSource(project, r.PathValue("source_id"), expected); err != nil {
			writeError(w, 409, err.Error())
			return
		}
		s.recordAuditEvent(r, "monitor.source.delete", "success", "monitor_source", r.PathValue("source_id"), project, nil)
		w.WriteHeader(204)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var source storage.MonitorSource
	if !decodeJSONBody(w, r, &source) {
		return
	}
	if source.ProjectID != "" && source.ProjectID != project {
		writeError(w, 400, "monitor source project mismatch")
		return
	}
	source.ProjectID = project
	if source.AppDynamics != nil {
		source.AppDynamics.ProjectID = project
	}
	if source.Dynatrace != nil {
		source.Dynatrace.ProjectID = project
	}
	if source.Prometheus != nil {
		source.Prometheus.ProjectID = project
		source.Prometheus.CollectionID = ""
		source.Prometheus.QueryOffset = 0
		source.Prometheus.QueryTime = nil
		source.Prometheus.GridAnchor = nil
	}
	if r.Method == http.MethodPost {
		if source.ID != "" || source.Revision != 0 {
			writeError(w, 400, "new monitor source must omit id and revision")
			return
		}
		source.ID = uuid.NewString()
	} else {
		if source.ID != "" && source.ID != r.PathValue("source_id") {
			writeError(w, 400, "monitor source identity mismatch")
			return
		}
		source.ID = r.PathValue("source_id")
		if source.Revision < 1 {
			writeError(w, 400, "revision is required")
			return
		}
	}
	if source.ManagementScope != nil || source.ManagementIdentity != nil {
		if !messagingManagementEnabled(project) {
			writeError(w, 409, "messaging management is not enabled for this project")
			return
		}
		if source.ManagementIdentity == nil {
			writeError(w, 400, "management identity is required")
			return
		}
		if source.ManagementIdentity.CredentialRef != "" {
			key, keyErr := monitorSigningKey()
			if keyErr != nil {
				writeError(w, 503, "management signing unavailable")
				return
			}
			_, version, resolveErr := s.resolveManagementCredential(r.Context(), source, key)
			if resolveErr != nil {
				writeError(w, 400, "management credential unavailable or incompatible")
				return
			}
			source.ManagementIdentity.CredentialVersion = version
		} else if source.ManagementIdentity.CredentialVersion != "" {
			writeError(w, 400, "management credential version requires a credential reference")
			return
		}
	}
	saved, err := store.SaveMonitorSource(source, source.Revision)
	if err != nil {
		status := 400
		if errors.Is(err, storage.ErrMonitorRevision) {
			status = 409
		}
		writeError(w, status, err.Error())
		return
	}
	s.recordAuditEvent(r, "monitor.source.save", "success", "monitor_source", saved.ID, project, map[string]any{"revision": saved.Revision})
	writeJSON(w, 200, safeMonitorSource(*saved))
}

func (s *Server) handleMonitorBindings(w http.ResponseWriter, r *http.Request) {
	project, status, message := resolveScenarioProjectID(r, s.store)
	if status != 0 {
		writeError(w, status, message)
		return
	}
	store := s.runMonitorStore(w, project)
	if store == nil {
		return
	}
	if r.Method == http.MethodGet {
		b, err := store.GetMonitorBindings(project, r.PathValue("id"))
		if err != nil {
			writeError(w, 500, "cannot load monitor bindings")
			return
		}
		writeJSON(w, 200, b)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var b storage.MonitorBindings
	if !decodeJSONBody(w, r, &b) {
		return
	}
	if b.ProjectID != "" && b.ProjectID != project || b.ScenarioID != "" && b.ScenarioID != r.PathValue("id") {
		writeError(w, 400, "monitor binding ownership mismatch")
		return
	}
	b.ProjectID, b.ScenarioID = project, r.PathValue("id")
	saved, err := store.SaveMonitorBindings(b, b.Revision)
	if err != nil {
		status := 400
		if errors.Is(err, storage.ErrMonitorRevision) {
			status = 409
		}
		writeError(w, status, err.Error())
		return
	}
	s.recordAuditEvent(r, "monitor.bindings.save", "success", "scenario", b.ScenarioID, project, map[string]any{"revision": saved.Revision})
	writeJSON(w, 200, saved)
}

func (s *Server) authorizeMonitorCollector(w http.ResponseWriter, r *http.Request) *storage.MonitoringCollector {
	if s.store == nil {
		writeError(w, 503, "storage unavailable")
		return nil
	}
	c, ok := s.loadMonitoringCollector(w, r)
	if !ok {
		return nil
	}
	token := extractAPIKey(r)
	if token == "" || subtle.ConstantTimeCompare([]byte(hashAPIKey(token)), []byte(c.TokenHash)) != 1 {
		writeError(w, 401, "invalid collector token")
		return nil
	}
	return c
}

func (s *Server) handlePollRunMonitorJobs(w http.ResponseWriter, r *http.Request) {
	c := s.authorizeMonitorCollector(w, r)
	if c == nil {
		return
	}
	store := s.runMonitorStore(w, c.ProjectID)
	if store == nil {
		return
	}
	key, err := monitorSigningKey()
	if err != nil {
		writeError(w, 503, err.Error())
		return
	}
	now := time.Now().UTC()
	providerContracts := map[string]bool{}
	if advertisement := r.Header.Get("X-APEX-Monitor-Readiness"); advertisement != "" {
		var ad collectoragent.RunJobReadiness
		if len(advertisement) > 8192 || json.Unmarshal([]byte(advertisement), &ad) != nil || ad.SigningKey != base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)) {
			writeError(w, 403, "collector signing-key readiness rejected")
			return
		}
		if len(ad.Contracts) > 8 {
			writeError(w, 400, "invalid collector readiness contracts")
			return
		}
		if slices.ContainsFunc(ad.Packs, func(pack string) bool {
			return monitorprovider.IsCapabilityPack(pack) || pack == "prometheus-scrape.v2"
		}) {
			writeError(w, 400, "collector contracts must be advertised separately")
			return
		}
		for _, contract := range ad.Contracts {
			if provider, governed := providerForCapability(contract); governed {
				if !slices.Contains(ad.Packs, provider.Pack) {
					writeError(w, 400, provider.ContractLabel+" contract requires pack approval")
					return
				}
				providerContracts[provider.Pack] = true
				ad.Packs = append(ad.Packs, contract)
				continue
			}
			switch contract {
			case "prometheus-scrape.v2":
				if !slices.Contains(ad.Packs, "prometheus") {
					writeError(w, 400, "Prometheus scrape contract requires pack approval")
					return
				}
			default:
				writeError(w, 400, "unsupported collector readiness contract")
				return
			}
			ad.Packs = append(ad.Packs, contract)
		}
		var readinessErr error
		if ad.InstanceID != "" {
			managementStore, ok := s.store.(storage.ManagementMonitorStore)
			if !ok {
				writeError(w, 501, "management collector readiness storage unavailable")
				return
			}
			scopes := []storage.ManagementReadinessScope{}
			for _, scope := range ad.ManagementScopes {
				scopes = append(scopes, storage.ManagementReadinessScope{ScopeHash: scope.ScopeHash, IdentityHash: scope.IdentityHash})
			}
			readinessErr = managementStore.SaveCollectorManagementReadiness(c.ProjectID, c.ID, storage.CollectorManagementReadiness{InstanceID: ad.InstanceID, Capabilities: ad.Capabilities, Scopes: scopes, Packs: ad.Packs, Destinations: ad.Destinations, SeenAt: now})
		} else {
			readinessErr = store.SaveCollectorRunReadiness(c.ProjectID, c.ID, ad.Packs, ad.Destinations, now)
		}
		if readinessErr != nil {
			writeError(w, 400, "invalid collector readiness")
			return
		}
	}
	var jobs []storage.RunMonitorJob
	if r.URL.Query().Get("page_size") != "" {
		limit, parseErr := strconv.Atoi(r.URL.Query().Get("page_size"))
		after := r.URL.Query().Get("after_key")
		pages, supported := store.(storage.RunMonitorJobPageStore)
		if parseErr != nil || limit < 1 || limit > storage.MaxRunMonitorSources || len(after) > storage.MaxRunMonitorJobIDBytes || strings.TrimSpace(after) != after || strings.ContainsAny(after, "\r\n\x00") {
			writeError(w, 400, "invalid collector job page")
			return
		}
		if !supported {
			writeError(w, 501, "collector job pagination unavailable")
			return
		}
		var more bool
		jobs, more, err = pages.PollRunMonitorJobPage(c.ProjectID, c.ID, after, limit, now)
		w.Header().Set("X-APEX-Monitor-Page", "1")
		if more && len(jobs) > 0 {
			w.Header().Set("X-APEX-Monitor-Next-Key", jobs[len(jobs)-1].ID)
		}
	} else {
		jobs, err = store.PollRunMonitorJobs(c.ProjectID, c.ID, now)
	}
	if err != nil {
		writeError(w, 503, "cannot poll monitor jobs")
		return
	}
	signed := []collectoragent.SignedRunJob{}
	for _, j := range jobs {
		lease := now.Add(time.Minute)
		if j.ExpiresAt.Before(lease) {
			lease = j.ExpiresAt
		}
		job := collectoragent.RunJob{ID: j.ID, ProjectID: j.ProjectID, RunID: j.RunID, ScenarioID: j.ScenarioID, CollectorID: j.Source.CollectorID, SourceID: j.Source.ID, SourceRevision: j.Source.Revision, Pack: j.Source.Pack, Destination: j.Source.Destination, IntervalSeconds: j.Source.IntervalSeconds, StartedAt: j.StartedAt, ExpiresAt: j.ExpiresAt, LeaseExpiresAt: lease, NextSequence: j.NextSequence}
		if j.Source.ManagementScope != nil && j.Source.ManagementIdentity != nil {
			scopeHash, scopeErr := j.Source.ManagementScope.Hash()
			identityHash, identityErr := j.Source.ManagementIdentity.Fingerprint()
			if scopeErr != nil || identityErr != nil {
				writeError(w, 503, "invalid management source snapshot")
				return
			}
			job.Management = &collectoragent.ManagementJob{Version: 1, ScopeHash: scopeHash, IdentityHash: identityHash}
		}
		job.FlushOnly = j.FlushOnly
		if j.FlushOnly {
			job.LoadEndedAt = j.LoadEndedAt
			job.DrainDeadline = j.DrainDeadline
		}
		if provider, governed := lookupProvider(j.Source.Pack); governed {
			if !providerContracts[provider.Pack] {
				continue
			}
			if err := provider.PopulateGrant(s, r.Context(), &job, j); err != nil {
				job.UnavailableReason = provider.GrantUnavailable
			}
		}
		if j.Source.CredentialRef != "" {
			// Return a signed unavailable job instead of failing the collector's
			// entire poll. Healthy overlapping runs must continue collecting.
			if s.projectSecretStore == nil {
				job.UnavailableReason = "monitor credential store unavailable"
			} else {
				secret, _, resolveErr := s.projectSecretStore.Get(r.Context(), j.ProjectID, j.Source.CredentialRef)
				var credentials collectoragent.Credentials
				publicKey, decodeErr := base64.StdEncoding.DecodeString(j.Source.CollectorPublicKey)
				if resolveErr != nil || len(secret) > 16<<10 || json.Unmarshal([]byte(secret), &credentials) != nil || decodeErr != nil {
					job.UnavailableReason = "monitor credential unavailable"
				} else {
					job.Credentials, err = collectoragent.SealCredentials(job, publicKey, credentials)
					if err != nil {
						job.UnavailableReason = "cannot seal monitor credentials"
					}
				}
			}
		}
		envelope, err := collectoragent.SignRunJob(job, key)
		if err != nil {
			writeError(w, 503, "cannot sign monitor job")
			return
		}
		signed = append(signed, envelope)
	}
	writeJSON(w, 200, signed)
}

func (s *Server) handleRunMonitorSamples(w http.ResponseWriter, r *http.Request) {
	c := s.authorizeMonitorCollector(w, r)
	if c == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var batch collectoragent.RunSampleBatch
	if !decodeJSONBody(w, r, &batch) {
		return
	}
	if batch.EvidenceVersion != 0 || batch.MessagingManagement != nil || batch.ManagementGrant != nil {
		s.handleManagementSamples(w, r, c, batch)
		return
	}
	store := s.runMonitorStore(w, c.ProjectID)
	if store == nil || !s.requireAnalysisBackend(w) {
		return
	}
	key, err := monitorSigningKey()
	if err != nil {
		writeError(w, 503, err.Error())
		return
	}
	now := time.Now().UTC()
	grant, err := collectoragent.VerifyRunJob(batch.Job, key.Public().(ed25519.PublicKey), c.ID, now)
	if err != nil || grant.ProjectID != c.ProjectID || grant.ID != r.PathValue("job_id") {
		writeError(w, 403, "invalid monitor collection grant")
		return
	}
	job, err := store.GetRunMonitorJob(c.ProjectID, grant.ID)
	if err != nil {
		writeError(w, 404, "monitor job unavailable")
		return
	}
	if job.Source.ManagementScope != nil || grant.Management != nil {
		writeError(w, 400, "management source requires typed poll evidence")
		return
	}
	if provider, governed := lookupProvider(job.Source.Pack); governed {
		if status, message := provider.ValidateSubmission(grant, *job, c.ID, batch); status != 0 {
			writeError(w, status, message)
			return
		}
		payload := storage.ProviderMonitorBatch{Points: batch.Points, Evidence: batch.Evidence, Coverage: batch.Coverage, QueryWindowEnd: batch.QueryWindowEnd, Final: batch.Final, Topology: batch.Topology, Progress: batch.Progress, QueryFromUTC: batch.QueryFromUTC, QueryUntilUTC: batch.QueryUntilUTC}
		if err := provider.Ingest(s, r.Context(), *job, batch.Sequence, payload, executionLeaseTerm{}); err != nil {
			writeError(w, 409, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"accepted": true, "sequence": batch.Sequence})
		return
	}
	if job.FlushOnly && batch.Final && len(batch.Points) == 0 {
		if grant.FlushOnly && grant.Pack == "prometheus" && job.Source.CollectorID == c.ID && grant.RunID == job.RunID && grant.SourceID == job.Source.ID && grant.SourceRevision == job.Source.Revision {
			if legacy, ok := store.(interface {
				CompleteLegacyPrometheusFlush(string, string, string, time.Time) error
			}); ok {
				if err := legacy.CompleteLegacyPrometheusFlush(c.ProjectID, c.ID, job.ID, now); err != nil {
					writeError(w, 409, err.Error())
					return
				}
				writeJSON(w, 200, map[string]any{"accepted": true})
				return
			}
		}
		writeError(w, 403, "exporter flush grant rejected")
		return
	}
	if job.Source.CollectorID != c.ID || job.Source.ID != grant.SourceID || job.Source.Revision != grant.SourceRevision || job.RunID != grant.RunID || job.ScenarioID != grant.ScenarioID || len(batch.Points) < 1 || len(batch.Points) > 512 || batch.Sequence < 0 || batch.Sequence > grant.NextSequence {
		writeError(w, 400, "monitor batch does not match job")
		return
	}
	// Hash source observations before adding receive-time fields so retry
	// identity does not depend on which replica accepted the request.
	for i, p := range batch.Points {
		if err := p.Validate(); err != nil || p.Provider != nil || p.ProjectID != job.ProjectID || p.RunID != job.RunID || p.ScenarioID != job.ScenarioID || p.Source != job.Source.ID || p.DatasetID != "" || p.Host != c.ID || !p.ReceivedAt.IsZero() || p.ActiveRuntimeMS != nil || job.Source.Pack == "prometheus" && (p.TimestampUTC.Before(job.StartedAt) || job.LoadEndedAt != nil && p.TimestampUTC.After(*job.LoadEndedAt)) {
			writeError(w, 400, "invalid or misattributed monitor point")
			return
		}
		canonical, err := analysis.NormalizeRunClock(p, job.StartedAt)
		if err != nil {
			writeError(w, 400, "invalid monitor run timestamp")
			return
		}
		canonical.Service = job.Source.Service
		batch.Points[i] = canonical
	}
	body, err := json.Marshal(batch.Points)
	if err != nil {
		writeError(w, 400, "invalid monitor batch")
		return
	}
	artifact, digest := monitorArtifactKey(job.ProjectID, job.RunID, body)
	if batch.Sequence != job.NextSequence || job.State != "collecting" && !(job.Source.Pack == "prometheus" && job.State == "flushing" && job.FlushOnly) {
		receipts, readErr := store.ListMonitorBatchReceipts(job.ProjectID, job.RunID, job.ID, batch.Sequence-1, 1)
		if readErr == nil && len(receipts) == 1 && receipts[0].JobID == job.ID && receipts[0].Sequence == batch.Sequence && receipts[0].Digest == digest {
			writeJSON(w, 200, map[string]any{"accepted": true, "sequence": batch.Sequence})
			return
		}
		writeError(w, 409, "monitor sequence changed or collection has stopped")
		return
	}
	if err = writeMonitorArtifact(r.Context(), artifact, body); err != nil {
		writeError(w, 503, "monitor artifact persistence failed")
		return
	}
	receipt := storage.MonitorBatchReceipt{JobID: job.ID, Sequence: batch.Sequence, Digest: digest, ArtifactKey: artifact, SampleCount: len(batch.Points), ReceivedAt: now}
	if legacy, ok := store.(interface {
		AcceptLegacyPrometheusBatch(string, string, storage.MonitorBatchReceipt, []analysis.MetricPoint) error
	}); ok && job.Source.Pack == "prometheus" {
		err = legacy.AcceptLegacyPrometheusBatch(c.ProjectID, c.ID, receipt, batch.Points)
	} else {
		err = store.AcceptMonitorBatch(c.ProjectID, c.ID, receipt)
	}
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"accepted": true, "sequence": batch.Sequence})
}

func (s *Server) handleRunMonitors(w http.ResponseWriter, r *http.Request) {
	project, status, message := resolveTestProjectID(r, s.store)
	if status != 0 {
		writeError(w, status, message)
		return
	}
	store := s.runMonitorStore(w, project)
	if store == nil {
		return
	}
	jobs, err := store.ListRunMonitorJobs(project, r.PathValue("id"))
	if err != nil {
		writeError(w, 500, "cannot load monitor jobs")
		return
	}
	for i := range jobs {
		jobs[i] = safeMonitorJob(jobs[i])
	}
	writeJSON(w, 200, jobs)
}

func (s *Server) handleRunMonitorSeries(w http.ResponseWriter, r *http.Request) {
	project, status, message := resolveTestProjectID(r, s.store)
	if status != 0 {
		writeError(w, status, message)
		return
	}
	store := s.runMonitorStore(w, project)
	if store == nil {
		return
	}
	if !s.requireAnalysisBackend(w) {
		return
	}
	metric, source := r.URL.Query().Get("metric"), r.URL.Query().Get("source_id")
	if metric == "" || source == "" {
		writeError(w, 400, "metric and source_id are required")
		return
	}
	var from, to time.Time
	for key, target := range map[string]*time.Time{"from": &from, "to": &to} {
		if raw := r.URL.Query().Get(key); raw != "" {
			parsed, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				writeError(w, 400, "invalid series window timestamp")
				return
			}
			*target = parsed.UTC()
		}
	}
	if !from.IsZero() && !to.IsZero() && !to.After(from) {
		writeError(w, 400, "series window must have positive duration")
		return
	}
	selection := AnalysisSeriesSelection{Host: r.URL.Query().Get("host")}
	if raw := r.URL.Query().Get("dimensions"); raw != "" {
		if len(raw) > 8192 || json.Unmarshal([]byte(raw), &selection.Dimensions) != nil || len(selection.Dimensions) > 16 {
			writeError(w, 400, "invalid bounded dimensions")
			return
		}
		for k, v := range selection.Dimensions {
			if k == "" || len(k) > 128 || len(v) > 128 {
				writeError(w, 400, "invalid bounded dimensions")
				return
			}
		}
	}
	job, jobErr := store.GetRunMonitorJob(project, r.PathValue("id")+":"+source)
	if jobErr == nil && job.Source.CanonicalProvider() {
		limit := 999
		if raw := r.URL.Query().Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 || parsed > 999 {
				writeError(w, http.StatusBadRequest, "series limit must be between 1 and 999")
				return
			}
			limit = parsed
		}
		page, err := loadCanonicalMonitorData(r.Context(), store, project, r.PathValue("id"), source, "point", r.URL.Query().Get("after_key"), limit)
		if err != nil {
			writeError(w, 503, "canonical monitoring evidence unavailable or corrupt")
			return
		}
		points := []analysis.MetricPoint{}
		for _, p := range page.Points {
			if p.MetricName == metric && matchesAnalysisDimensions(p, selection) && (from.IsZero() || !p.TimestampUTC.Before(from)) && (to.IsZero() || !p.TimestampUTC.After(to)) {
				points = append(points, p)
			}
		}
		writeJSON(w, 200, map[string]any{"points": points, "has_more": page.HasMore, "after_key": page.AfterKey})
		return
	}
	receipts, err := store.ListMonitorBatchReceipts(project, r.PathValue("id"), r.URL.Query().Get("after_job"), parseMonitorSequence(r.URL.Query().Get("after_sequence")), 101)
	if err != nil {
		writeError(w, 500, "cannot load monitor evidence")
		return
	}
	more := len(receipts) > 100
	if more {
		receipts = receipts[:100]
	}
	points := []analysis.MetricPoint{}
	for _, receipt := range receipts {
		evidence, err := readMonitorEvidence(r.Context(), project, r.PathValue("id"), receipt)
		if err != nil {
			writeError(w, 503, "monitor evidence unavailable or corrupt")
			return
		}
		for _, p := range evidence.Points {
			if p.MetricName != metric || p.Source != source || !matchesAnalysisDimensions(p, selection) || (!from.IsZero() && p.TimestampUTC.Before(from)) || (!to.IsZero() && p.TimestampUTC.After(to)) {
				continue
			}
			p.ReceivedAt = receipt.ReceivedAt
			p.Quality = monitorPointQuality(p, receipt.ReceivedAt)
			if len(points) >= analysis.MaxSeriesPoints {
				writeError(w, 422, "series page exceeds analysis limit")
				return
			}
			points = append(points, p)
		}
	}
	response := map[string]any{"points": points, "has_more": more}
	if len(receipts) > 0 {
		last := receipts[len(receipts)-1]
		response["after_job"], response["after_sequence"] = last.JobID, last.Sequence
	}
	writeJSON(w, 200, response)
}

func parseMonitorSequence(raw string) int64 {
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return -1
	}
	return n
}
func monitorPointQuality(p analysis.MetricPoint, received time.Time) string {
	if p.Quality == analysis.QualityMissing || p.Quality == analysis.QualityDropped {
		return p.Quality
	}
	if p.Provider != nil {
		return p.Quality
	}
	if p.TimestampUTC.After(received.Add(30 * time.Second)) {
		return analysis.QualityClockSkewed
	}
	if p.TimestampUTC.Before(received.Add(-90 * time.Second)) {
		return analysis.QualityStale
	}
	return p.Quality
}
