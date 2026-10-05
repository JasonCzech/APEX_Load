package api

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/storage"
)

func messagingManagementEnabled(project string) bool {
	if !runMonitoringEnabled(project) {
		return false
	}
	for _, p := range strings.Split(os.Getenv("APEXLOAD_MESSAGING_MANAGEMENT_PROJECTS"), ",") {
		if project != "" && strings.TrimSpace(p) == project {
			return true
		}
	}
	return false
}

// Source credentials are pinned using an opaque keyed version, because secret
// providers do not all expose a stable remote version identifier. Rotation
// requires an administrator to save a new source revision before fresh polls.
func managementCredentialVersion(key ed25519.PrivateKey, project, ref, secret string) string {
	mac := hmac.New(sha256.New, key.Seed())
	encoded, _ := json.Marshal([]string{"apexload.management-credential-version.v1", project, ref, secret})
	_, _ = mac.Write(encoded)
	return "hmac-v1:" + hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) resolveManagementCredential(ctx context.Context, source storage.MonitorSource, key ed25519.PrivateKey) (collectoragent.ManagementCredentials, string, error) {
	var credentials collectoragent.ManagementCredentials
	if source.ManagementIdentity == nil {
		return credentials, "", errors.New("management identity unavailable")
	}
	identity := source.ManagementIdentity
	if identity.CredentialRef == "" {
		return credentials, "", nil
	}
	if s.projectSecretStore == nil {
		return credentials, "", errors.New("management credential unavailable")
	}
	secret, _, err := s.projectSecretStore.Get(ctx, source.ProjectID, identity.CredentialRef)
	if err != nil || len(secret) > 16<<10 || json.Unmarshal([]byte(secret), &credentials) != nil {
		return credentials, "", errors.New("management credential unavailable")
	}
	if credentials.Validate() != nil || !managementCredentialMatchesIdentity(credentials, *identity) {
		return credentials, "", errors.New("management credential identity mismatch")
	}
	return credentials, managementCredentialVersion(key, source.ProjectID, identity.CredentialRef, secret), nil
}

func managementCredentialMatchesIdentity(credentials collectoragent.ManagementCredentials, identity analysis.ManagementIdentity) bool {
	return credentials.MatchesIdentity(identity)
}

func (s *Server) handleManagementPoll(w http.ResponseWriter, r *http.Request) {
	collector := s.authorizeMonitorCollector(w, r)
	if collector == nil {
		return
	}
	store, ok := s.store.(storage.ManagementMonitorStore)
	if !ok {
		writeError(w, 501, "messaging management storage unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var control collectoragent.ManagementPollControl
	if !decodeJSONBody(w, r, &control) {
		return
	}
	if control.InstanceID == "" || len(control.InstanceID) > 128 {
		writeError(w, 400, "collector instance is required")
		return
	}
	key, keyErr := monitorSigningKey()
	if keyErr != nil {
		writeError(w, 503, "management signing unavailable")
		return
	}
	now := time.Now().UTC()
	var poll *storage.ManagementPoll
	var err error
	switch control.Operation {
	case "begin":
		if !messagingManagementEnabled(collector.ProjectID) {
			writeError(w, 409, "messaging management is not enabled for this project")
			return
		}
		poll, err = store.BeginManagementPoll(collector.ProjectID, collector.ID, control.InstanceID, r.PathValue("job_id"), control.Nonce, now)
	case "get", "abandon":
		poll, err = store.GetManagementPoll(collector.ProjectID, control.PollID)
	default:
		writeError(w, 400, "unsupported management poll operation")
		return
	}
	if err != nil {
		writeError(w, 409, "management poll admission unavailable or fenced")
		return
	}
	if poll.CollectorID != collector.ID || poll.CollectorInstanceID != control.InstanceID || poll.LeaderJobID != r.PathValue("job_id") {
		writeError(w, 403, "management poll ownership mismatch")
		return
	}
	if control.Operation == "abandon" {
		generation, e := analysis.ParseManagementInteger(control.Generation)
		if e != nil || generation != poll.Generation || !control.CompletionConfirmed {
			writeError(w, 400, "actual management completion proof is required")
			return
		}
		if err = store.AbandonManagementPoll(collector.ProjectID, collector.ID, control.InstanceID, poll.ID, generation, storage.ManagementDrainProof{Kind: "completed"}, now); err != nil {
			writeError(w, 409, "management completion is fenced")
			return
		}
		poll, err = store.GetManagementPoll(collector.ProjectID, poll.ID)
		if err != nil {
			writeError(w, 503, "management completion resolution unavailable")
			return
		}
	}
	grant := collectoragent.ManagementPollGrant{Version: collectoragent.ManagementPollVersion, PollID: poll.ID, ProjectID: poll.ProjectID, SourceID: poll.SourceID, SourceRevision: poll.Source.Revision, CollectorID: poll.CollectorID, CollectorInstanceID: poll.CollectorInstanceID, LeaderJobID: poll.LeaderJobID, Nonce: poll.Nonce, Pack: poll.Source.Pack, Destination: poll.Source.Destination, ScopeHash: poll.ScopeHash, IdentityHash: poll.IdentityHash, Generation: strconv.FormatInt(poll.Generation, 10), PollSequence: strconv.FormatInt(poll.Sequence, 10), CohortHash: poll.CohortHash, IssuedAt: poll.CreatedAt, Deadline: poll.Deadline, AuthorizationExpiresAt: poll.CreatedAt.Add(2 * time.Minute), Cohort: []collectoragent.ManagementPollMember{}}
	for _, member := range poll.Cohort {
		grant.Cohort = append(grant.Cohort, collectoragent.ManagementPollMember{JobID: member.Job.ID, RunID: member.Job.RunID, NextSequence: member.Sequence})
	}
	if poll.State == "accepted" {
		grant.AcceptedCoreDigest = poll.AcceptedCoreDigest
	}
	fresh := poll.State == "collecting" && poll.Deadline.After(now) && control.Operation != "abandon"
	if fresh && poll.Source.ManagementIdentity != nil && poll.Source.ManagementIdentity.CredentialRef != "" {
		credentials, version, e := s.resolveManagementCredential(r.Context(), poll.Source, key)
		if e != nil {
			grant.UnavailableReason = "permission_denied"
		} else if version != poll.Source.ManagementIdentity.CredentialVersion {
			grant.UnavailableReason = "identity_mismatch"
		} else {
			public, sealErr := base64.StdEncoding.DecodeString(poll.Source.CollectorPublicKey)
			if sealErr == nil {
				grant.Credentials, sealErr = collectoragent.SealManagementCredentials(grant, public, credentials)
			}
			if sealErr != nil {
				grant.UnavailableReason = "permission_denied"
			}
		}
	}
	signed, err := collectoragent.SignManagementPoll(grant, key)
	if err != nil {
		if control.Operation == "begin" {
			_ = store.CancelUnissuedManagementPoll(collector.ProjectID, collector.ID, control.InstanceID, poll.ID, poll.Generation, time.Now().UTC())
		}
		writeError(w, 503, "management grant unavailable")
		return
	}
	if fresh {
		if err = store.MarkManagementPollIssued(collector.ProjectID, collector.ID, control.InstanceID, poll.ID, poll.Generation, time.Now().UTC()); err != nil {
			if control.Operation == "begin" {
				_ = store.CancelUnissuedManagementPoll(collector.ProjectID, collector.ID, control.InstanceID, poll.ID, poll.Generation, time.Now().UTC())
			}
			writeError(w, 409, "management grant issue fenced")
			return
		}
	}
	writeJSON(w, 200, collectoragent.ManagementPollResponse{State: poll.State, Grant: &signed})
}

type MonitorManagementRecord struct {
	JobID      string                           `json:"job_id"`
	Sequence   string                           `json:"sequence"`
	ReceivedAt time.Time                        `json:"received_at"`
	Evidence   analysis.MonitorEvidenceEnvelope `json:"evidence"`
}

type MonitorManagementResponse struct {
	Version  uint32                    `json:"version"`
	Records  []MonitorManagementRecord `json:"records"`
	HasMore  bool                      `json:"has_more"`
	Cursor   string                    `json:"cursor,omitempty"`
	EntityID string                    `json:"entity_id,omitempty"`
}

type monitorManagementCursor struct {
	Binding  string `json:"binding"`
	JobID    string `json:"job_id"`
	Sequence string `json:"sequence"`
}

func managementCursorBinding(project, run, source, entity string, from, to time.Time) string {
	body, _ := json.Marshal([]string{project, run, source, entity, from.Format(time.RFC3339Nano), to.Format(time.RFC3339Nano)})
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}
func decodeManagementCursor(raw, binding string) (string, int64, error) {
	if raw == "" {
		return "", -1, nil
	}
	if len(raw) > 2048 {
		return "", 0, errors.New("cursor exceeds limit")
	}
	body, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return "", 0, err
	}
	var cursor monitorManagementCursor
	if json.Unmarshal(body, &cursor) != nil || cursor.Binding != binding || cursor.JobID == "" || len(cursor.JobID) > 1024 {
		return "", 0, errors.New("invalid management cursor")
	}
	sequence, err := analysis.ParseManagementInteger(cursor.Sequence)
	return cursor.JobID, sequence, err
}
func encodeManagementCursor(binding string, receipt storage.MonitorBatchReceipt) string {
	body, _ := json.Marshal(monitorManagementCursor{binding, receipt.JobID, strconv.FormatInt(receipt.Sequence, 10)})
	return base64.RawURLEncoding.EncodeToString(body)
}

// Entity selection returns the complete immutable source snapshot that contains
// the selected entity. This preserves the original inventory coverage proof.
func (s *Server) handleMonitorManagement(w http.ResponseWriter, r *http.Request) {
	if !s.requireAnalysisBackend(w) {
		return
	}
	project, status, message := resolveTestProjectID(r, s.store)
	if status != 0 {
		writeError(w, status, message)
		return
	}
	store, ok := s.store.(storage.RunMonitorStore)
	if !ok {
		writeError(w, 501, "monitor evidence storage unavailable")
		return
	}
	source, entity := r.URL.Query().Get("source_id"), r.URL.Query().Get("entity_id")
	if len(source) > 256 || entity != "" && !validManagementEntityID(entity) {
		writeError(w, 400, "invalid management selection")
		return
	}
	var from, to time.Time
	for name, dest := range map[string]*time.Time{"from": &from, "to": &to} {
		if raw := r.URL.Query().Get(name); raw != "" {
			parsed, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				writeError(w, 400, "invalid management window")
				return
			}
			*dest = parsed.UTC()
		}
	}
	if !from.IsZero() && !to.IsZero() && !to.After(from) {
		writeError(w, 400, "management window requires positive duration")
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeError(w, 400, "management page limit must be 1 through 100")
			return
		}
		limit = parsed
	}
	run := r.PathValue("id")
	binding := managementCursorBinding(project, run, source, entity, from, to)
	afterJob, afterSequence, err := decodeManagementCursor(r.URL.Query().Get("cursor"), binding)
	if err != nil {
		writeError(w, 400, "invalid management cursor")
		return
	}
	receipts, err := store.ListMonitorBatchReceipts(project, run, afterJob, afterSequence, limit+1)
	if err != nil {
		writeError(w, 503, "cannot load management evidence")
		return
	}
	out := MonitorManagementResponse{Version: 1, Records: []MonitorManagementRecord{}, EntityID: entity}
	size := 0
	for index, receipt := range receipts {
		if index >= limit {
			out.HasMore = true
			break
		}
		if receipt.EvidenceVersion == nil || *receipt.EvidenceVersion != int64(analysis.MonitorEvidenceVersion) {
			out.Cursor = encodeManagementCursor(binding, receipt)
			continue
		}
		evidence, e := readMonitorEvidence(r.Context(), project, run, receipt)
		if e != nil {
			writeError(w, 503, "management evidence unavailable or corrupt")
			return
		}
		snapshot := evidence.MessagingManagement
		selected := snapshot != nil && (source == "" || snapshot.SourceID == source) && (from.IsZero() || !snapshot.CapturedAt.Before(from)) && (to.IsZero() || snapshot.CapturedAt.Before(to))
		if selected && entity != "" {
			selected = false
			for _, row := range snapshot.Rows {
				id, _ := row.Entity.ID()
				if id == entity {
					selected = true
					break
				}
			}
		}
		if selected {
			record := MonitorManagementRecord{receipt.JobID, strconv.FormatInt(receipt.Sequence, 10), receipt.ReceivedAt, evidence}
			encoded, e := json.Marshal(record)
			if e != nil {
				writeError(w, 503, "invalid management record")
				return
			}
			if size+len(encoded) > (2<<20)-8192 {
				out.HasMore = true
				break
			}
			size += len(encoded)
			out.Records = append(out.Records, record)
		}
		out.Cursor = encodeManagementCursor(binding, receipt)
	}
	if !out.HasMore {
		out.Cursor = ""
	}
	writeJSON(w, 200, out)
}
func validManagementEntityID(id string) bool {
	bytes, err := hex.DecodeString(id)
	return err == nil && len(bytes) == 32 && hex.EncodeToString(bytes) == id
}

func managementGrantMatchesPoll(grant collectoragent.ManagementPollGrant, poll *storage.ManagementPoll) bool {
	return poll != nil && grant.PollID == poll.ID && grant.ProjectID == poll.ProjectID && grant.SourceID == poll.SourceID && grant.SourceRevision == poll.Source.Revision && grant.CollectorID == poll.CollectorID && grant.CollectorInstanceID == poll.CollectorInstanceID && grant.LeaderJobID == poll.LeaderJobID && grant.Nonce == poll.Nonce && grant.Pack == poll.Source.Pack && grant.Destination == poll.Source.Destination && grant.ScopeHash == poll.ScopeHash && grant.IdentityHash == poll.IdentityHash && grant.Generation == strconv.FormatInt(poll.Generation, 10) && grant.PollSequence == strconv.FormatInt(poll.Sequence, 10) && grant.CohortHash == poll.CohortHash && grant.IssuedAt.Equal(poll.CreatedAt) && grant.Deadline.Equal(poll.Deadline)
}

type MonitorManagementAccepted struct {
	Accepted      bool   `json:"accepted"`
	PollID        string `json:"poll_id"`
	PublishedRuns int    `json:"published_runs"`
}

func (s *Server) handleManagementSamples(w http.ResponseWriter, r *http.Request, collector *storage.MonitoringCollector, batch collectoragent.RunSampleBatch) {
	if !s.requireAnalysisBackend(w) {
		return
	}
	management, ok := s.store.(storage.ManagementMonitorStore)
	if !ok {
		writeError(w, 501, "management evidence storage unavailable")
		return
	}
	if batch.EvidenceVersion != analysis.MonitorEvidenceVersion || batch.MessagingManagement == nil || batch.ManagementGrant == nil || len(batch.Points) != 0 {
		writeError(w, 400, "management evidence requires one typed snapshot and an empty point array")
		return
	}
	key, err := monitorSigningKey()
	if err != nil {
		writeError(w, 503, "management signing unavailable")
		return
	}
	snapshot := batch.MessagingManagement
	grant, err := collectoragent.VerifyManagementPoll(*batch.ManagementGrant, key.Public().(ed25519.PublicKey), collector.ID, snapshot.CollectorInstanceID, time.Now().UTC(), true)
	if err != nil || grant.ProjectID != collector.ProjectID || grant.LeaderJobID != r.PathValue("job_id") {
		writeError(w, 403, "invalid management poll grant")
		return
	}
	poll, err := management.GetManagementPoll(collector.ProjectID, grant.PollID)
	if err != nil {
		writeError(w, 404, "management poll unavailable")
		return
	}
	if !managementGrantMatchesPoll(grant, poll) || snapshot.PollID != poll.ID || snapshot.Generation != grant.Generation || snapshot.PollSequence != grant.PollSequence || snapshot.CollectorInstanceID != grant.CollectorInstanceID || snapshot.CollectorID != collector.ID || snapshot.SourceID != grant.SourceID || snapshot.SourceRevision != grant.SourceRevision || snapshot.IntervalSeconds != poll.Source.IntervalSeconds || poll.Source.ManagementScope == nil || poll.Source.ManagementIdentity == nil || snapshot.ValidateFor(*poll.Source.ManagementScope, *poll.Source.ManagementIdentity) != nil {
		writeError(w, 400, "management evidence does not match admitted scope")
		return
	}
	leaderFound := false
	for _, member := range poll.Cohort {
		if member.Job.ID == grant.LeaderJobID {
			leaderFound = true
			if batch.Sequence != member.Sequence {
				writeError(w, 400, "management leader sequence mismatch")
				return
			}
		}
	}
	if !leaderFound {
		writeError(w, 403, "management leader is not admitted")
		return
	}
	core := analysis.MonitorEvidenceEnvelope{Version: analysis.MonitorEvidenceVersion, Points: []analysis.MetricPoint{}, MessagingManagement: snapshot}
	coreBytes, err := analysis.MonitorEvidenceCoreBytes(core)
	if err != nil {
		writeError(w, 400, "invalid management evidence")
		return
	}
	coreHash := sha256.Sum256(coreBytes)
	coreDigest := hex.EncodeToString(coreHash[:])
	// Resolve accepted replay before current feature flags, credentials, lease or
	// source existence. The retained journal can acknowledge only the same core.
	if poll.State == "accepted" {
		if poll.AcceptedCoreDigest != coreDigest {
			writeError(w, 409, "management poll replay conflicts")
			return
		}
		if err = management.AcceptManagementPoll(collector.ProjectID, collector.ID, grant.CollectorInstanceID, poll.ID, coreDigest, poll.SubmittedReceipts, time.Now().UTC()); err != nil {
			writeError(w, 409, "management receipt replay conflicts")
			return
		}
		writeJSON(w, 200, MonitorManagementAccepted{true, poll.ID, len(poll.PublishedReceipts)})
		return
	}
	now := time.Now().UTC()
	if poll.State != "collecting" || !poll.Deadline.After(now) || !grant.AuthorizationExpiresAt.After(now) {
		writeError(w, 409, "management poll expired or fenced")
		return
	}
	if grant.UnavailableReason != "" && (snapshot.UnavailableReason != grant.UnavailableReason || len(snapshot.Rows) != 0 || !snapshot.Incomplete) {
		writeError(w, 400, "unavailable management grant cannot publish observations")
		return
	}
	if poll.Source.ManagementIdentity.CredentialRef != "" {
		_, version, credentialErr := s.resolveManagementCredential(r.Context(), poll.Source, key)
		reason := ""
		if credentialErr != nil {
			reason = "permission_denied"
		} else if version != poll.Source.ManagementIdentity.CredentialVersion {
			reason = "identity_mismatch"
		}
		if reason != "" && (snapshot.UnavailableReason != reason || len(snapshot.Rows) != 0 || !snapshot.Incomplete) {
			writeError(w, 409, "management credential changed or unavailable")
			return
		}
	}
	// Prequeue every object before writing. This makes a crash during partial
	// cohort persistence reclaimable without deleting another live run's data.
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	receipts := make([]storage.MonitorBatchReceipt, 0, len(poll.Cohort))
	// PostgreSQL TIMESTAMPTZ retains microseconds. Use the same canonical
	// controller clock in immutable attribution and its durable receipt; source
	// capture timestamps retain their original precision.
	received := managementReceiptTime(now)
	for _, member := range poll.Cohort {
		if err = ctx.Err(); err != nil {
			writeError(w, 503, "management persistence cancelled")
			return
		}
		attributed := core.Clone()
		job := member.Job
		attributed.Attribution = &analysis.MonitorEvidenceAttribution{ProjectID: job.ProjectID, RunID: job.RunID, ScenarioID: job.ScenarioID, JobID: job.ID, SourceID: job.Source.ID, SourceRevision: job.Source.Revision, CollectorID: collector.ID, ReceivedAt: received}
		body, encodeErr := analysis.EncodeMonitorEvidence(attributed)
		if encodeErr != nil {
			writeError(w, 400, "invalid attributed management evidence")
			return
		}
		artifact, digest := monitorArtifactKey(job.ProjectID, job.RunID, body)
		if err = management.QueueMonitorObjectCleanup(job.ProjectID, job.RunID, artifact); err != nil {
			writeError(w, 503, "management object cleanup admission failed")
			return
		}
		if err = writeMonitorArtifact(ctx, artifact, body); err != nil {
			writeError(w, 503, "management artifact persistence failed")
			return
		}
		version, snapshots, rows, generation := int64(analysis.MonitorEvidenceVersion), int64(1), int64(len(snapshot.Rows)), poll.Generation
		pollID := poll.ID
		receipts = append(receipts, storage.MonitorBatchReceipt{JobID: job.ID, Sequence: member.Sequence, Digest: digest, ArtifactKey: artifact, SampleCount: 0, ReceivedAt: received, EvidenceVersion: &version, ManagementSnapshotCount: &snapshots, ManagementRowCount: &rows, CollectionGeneration: &generation, ManagementPollID: &pollID})
	}
	if err = ctx.Err(); err != nil {
		writeError(w, 503, "management persistence cancelled")
		return
	}
	if err = management.AcceptManagementPoll(collector.ProjectID, collector.ID, grant.CollectorInstanceID, poll.ID, coreDigest, receipts, time.Now().UTC()); err != nil {
		writeError(w, 409, "management publication fenced or incomplete")
		return
	}
	accepted, err := management.GetManagementPoll(collector.ProjectID, poll.ID)
	if err != nil {
		writeError(w, 503, "management receipt resolution unavailable")
		return
	}
	writeJSON(w, 200, MonitorManagementAccepted{true, poll.ID, len(accepted.PublishedReceipts)})
}

// managementReceiptTime is the shared database/artifact receive-clock precision.
// Source observation timestamps are not normalized by this function.
func managementReceiptTime(now time.Time) time.Time {
	return now.UTC().Truncate(time.Microsecond)
}
