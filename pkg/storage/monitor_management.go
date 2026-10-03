package storage

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

const ManagementEvidenceCapability = "messaging_management_evidence_v1"
const ManagementPollLifetime = 30 * time.Second

var ErrManagementPollUnavailable = errors.New("management source has no available poll admission")
var ErrManagementPollConflict = errors.New("management poll ownership or replay conflict")

// Management readiness hashes identify a locally approved scope and principal
// policy, without disclosing inventories or secrets in heartbeat headers.
type ManagementReadinessScope struct {
	ScopeHash    string `json:"scope_hash"`
	IdentityHash string `json:"identity_hash"`
}
type CollectorManagementReadiness struct {
	InstanceID   string                     `json:"instance_id"`
	Capabilities []string                   `json:"capabilities"`
	Scopes       []ManagementReadinessScope `json:"management_scopes"`
	Packs        []string                   `json:"packs"`
	Destinations []string                   `json:"destinations"`
	SeenAt       time.Time                  `json:"seen_at"`
}
type ManagementPollMember struct {
	Job      RunMonitorJob `json:"job"`
	Sequence int64         `json:"next_sequence,string"`
}

// Terminal journal members avoid repeating the public source configuration.
// Decoding retains the same in-memory shape and exact signed cohort tuple.
func (m ManagementPollMember) MarshalJSON() ([]byte, error) {
	if m.Job.Source.ID == "" {
		type identity struct {
			ID        string `json:"id"`
			ProjectID string `json:"project_id"`
			RunID     string `json:"run_id"`
		}
		return json.Marshal(struct {
			Job      identity `json:"job"`
			Sequence int64    `json:"next_sequence,string"`
		}{identity{m.Job.ID, m.Job.ProjectID, m.Job.RunID}, m.Sequence})
	}
	type full ManagementPollMember
	return json.Marshal(full(m))
}

// ManagementPoll persists immutable run snapshots, even if mutable source
// definitions are edited or deleted. Only the minimal cohort hash is signed.
type ManagementPoll struct {
	ID                  string                 `json:"id"`
	ProjectID           string                 `json:"project_id"`
	SourceID            string                 `json:"source_id"`
	CollectorID         string                 `json:"collector_id"`
	CollectorInstanceID string                 `json:"collector_instance_id"`
	LeaderJobID         string                 `json:"leader_job_id"`
	Nonce               string                 `json:"nonce"`
	Generation          int64                  `json:"generation,string"`
	Sequence            int64                  `json:"poll_sequence,string"`
	Source              MonitorSource          `json:"source"`
	ScopeHash           string                 `json:"scope_hash"`
	IdentityHash        string                 `json:"identity_hash"`
	CohortHash          string                 `json:"cohort_hash"`
	Cohort              []ManagementPollMember `json:"cohort"`
	State               string                 `json:"state"`
	CreatedAt           time.Time              `json:"created_at"`
	Deadline            time.Time              `json:"deadline"`
	AcceptedCoreDigest  string                 `json:"accepted_core_digest,omitempty"`
	SubmittedReceipts   []MonitorBatchReceipt  `json:"submitted_receipts,omitempty"`
	PublishedReceipts   []MonitorBatchReceipt  `json:"published_receipts,omitempty"`
	DrainProof          *ManagementDrainProof  `json:"drain_proof,omitempty"`
	DrainConfirmedAt    *time.Time             `json:"drain_confirmed_at,omitempty"`
	GrantIssuedAt       *time.Time             `json:"grant_issued_at,omitempty"`
}

// Completed proves that this instance's actual observer calls and physical
// cleanup returned. ProcessStopped requires a separately authorized, audited
// operator attestation; lease expiry alone is never such proof.
type ManagementDrainProof struct {
	Kind    string `json:"kind"`
	AuditID string `json:"audit_id,omitempty"`
}
type ManagementMonitorStore interface {
	SaveCollectorManagementReadiness(string, string, CollectorManagementReadiness) error
	BeginManagementPoll(string, string, string, string, string, time.Time) (*ManagementPoll, error)
	GetManagementPoll(string, string) (*ManagementPoll, error)
	MarkManagementPollIssued(string, string, string, string, int64, time.Time) error
	CancelUnissuedManagementPoll(string, string, string, string, int64, time.Time) error
	AcceptManagementPoll(string, string, string, string, string, []MonitorBatchReceipt, time.Time) error
	AbandonManagementPoll(string, string, string, string, int64, ManagementDrainProof, time.Time) error
	QueueMonitorObjectCleanup(string, string, string) error
	IsMonitorArtifactReferenced(string, string) (bool, error)
}

func managementPack(kind string) string {
	switch kind {
	case "kafka_offsets":
		return "kafka-offsets"
	case "servicebus_runtime":
		return "servicebus-runtime"
	case "solace_semp":
		return "solace-semp"
	}
	return ""
}
func validManagementText(s string, max int) bool {
	return len(s) > 0 && len(s) <= max && utf8.ValidString(s) && strings.TrimSpace(s) == s && !strings.ContainsFunc(s, unicode.IsControl)
}
func validManagementDigest(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == s
}
func validateManagementMonitorSource(m MonitorSource) error {
	if m.ManagementScope == nil || m.ManagementIdentity == nil {
		return errors.New("management scope and identity are both required")
	}
	if err := m.ManagementScope.Validate(); err != nil {
		return err
	}
	if err := m.ManagementIdentity.Validate(); err != nil {
		return err
	}
	if m.Pack != managementPack(m.ManagementScope.Kind) {
		return errors.New("management scope does not match monitor pack")
	}
	for _, v := range []string{m.ID, m.ProjectID, m.Name, m.CollectorID} {
		if !validManagementText(v, 256) {
			return errors.New("invalid management source identity")
		}
	}
	if m.IntervalSeconds < 10 || m.IntervalSeconds > 3600 || m.Pack == "solace-semp" && m.IntervalSeconds < 30 {
		return errors.New("invalid management collection interval")
	}
	if m.CredentialRef != "" || len(m.Service) > 128 {
		return errors.New("management identity must use its separate credential policy")
	}
	if !validManagementText(m.Destination, 2048) {
		return errors.New("invalid management destination")
	}
	if err := validateManagementDestination(m.Destination); err != nil {
		return err
	}
	if m.ManagementIdentity.CredentialRef != "" && m.CollectorPublicKey == "" {
		return errors.New("management secret requires enrolled collector encryption key")
	}
	if m.CollectorPublicKey != "" {
		if err := validateManagementEncryptionKey(m.CollectorPublicKey); err != nil {
			return err
		}
	}
	b, err := json.Marshal(m)
	if err != nil || len(b) > 16<<10 {
		return errors.New("management source exceeds 16 KiB")
	}
	return nil
}
func managementSourceHashes(m MonitorSource) (string, string, error) {
	if err := validateManagementMonitorSource(m); err != nil {
		return "", "", err
	}
	s, e := m.ManagementScope.Hash()
	if e != nil {
		return "", "", e
	}
	i, e := m.ManagementIdentity.Fingerprint()
	return s, i, e
}

// Begin never performs target I/O. It selects the least recently serviced
// immutable configuration and pins all eligible member sequence numbers.
func (s *SQLiteStore) BeginManagementPoll(project, collector, instance, leader, nonce string, now time.Time) (*ManagementPoll, error) {
	if now.IsZero() || !validManagementText(instance, 128) || !validManagementText(nonce, 128) || !validManagementText(leader, 1024) {
		return nil, ErrManagementPollConflict
	}
	tx, e := s.db.Begin()
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if _, e = lockProjectForOwnedMutation(tx, project); e != nil {
		return nil, e
	}
	var oldID string
	e = tx.QueryRow(`SELECT id FROM monitor_management_polls WHERE project_id=? AND collector_id=? AND instance_id=? AND nonce=?`, project, collector, instance, nonce).Scan(&oldID)
	if e == nil {
		p, e := getManagementPollTx(tx, project, oldID)
		if e != nil {
			return nil, e
		}
		if p.LeaderJobID != leader {
			return nil, ErrManagementPollConflict
		}
		if e = tx.Commit(); e != nil {
			return nil, e
		}
		return p, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	j, e := getManagementJobTx(tx, project, leader)
	if e != nil {
		return nil, e
	}
	if j.Source.CollectorID != collector || !managementJobEligible(*j, now) {
		return nil, ErrManagementPollUnavailable
	}
	scope, identity, e := managementSourceHashes(j.Source)
	if e != nil {
		return nil, e
	}
	if e = managementReadinessTx(tx, j.Source, instance, now); e != nil {
		return nil, e
	}
	var generation, sequence int64
	var leaseState, priorInstance, priorCollector, priorScope, priorIdentity string
	var priorRevision int64
	var leaseExpires time.Time
	var leasePollID string
	e = tx.QueryRow(`SELECT generation,poll_sequence,state,instance_id,collector_id,source_revision,scope_hash,identity_hash,expires_at,poll_id FROM monitor_collection_leases WHERE project_id=? AND source_id=?`, project, j.Source.ID).Scan(&generation, &sequence, &leaseState, &priorInstance, &priorCollector, &priorRevision, &priorScope, &priorIdentity, &leaseExpires, &leasePollID)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	if e == nil && (generation <= 0 || sequence <= 0 || priorRevision <= 0 || !validManagementDigest(priorScope) || !validManagementDigest(priorIdentity)) {
		return nil, errors.New("invalid durable management fence")
	}
	if e == nil && leaseState != "released" {
		if leaseState == "collecting" && !leaseExpires.After(now) {
			if _, e = tx.Exec(`UPDATE monitor_collection_leases SET state='draining' WHERE project_id=? AND source_id=? AND poll_id=?`, project, j.Source.ID, leasePollID); e != nil {
				return nil, e
			}
			if _, e = tx.Exec(`UPDATE monitor_management_polls SET state='draining' WHERE project_id=? AND id=? AND state='collecting'`, project, leasePollID); e != nil {
				return nil, e
			}
			if e = tx.Commit(); e != nil {
				return nil, e
			}
		}
		return nil, ErrManagementPollUnavailable
	}
	if sequence == math.MaxInt64 || generation == math.MaxInt64 {
		return nil, errors.New("management generation or sequence exhausted")
	}
	if e != nil || priorInstance != instance || priorCollector != collector || priorRevision != j.Source.Revision || priorScope != scope || priorIdentity != identity {
		generation++
	}
	sequence++
	// A source index bounds the scan independently of unrelated project jobs.
	rows, e := tx.Query(`SELECT j.payload,j.state,j.next_sequence FROM run_monitor_jobs j JOIN tests t ON t.id=j.test_id WHERE j.project_id=? AND j.source_id=? AND j.state='collecting' AND j.expires_at>? AND t.status IN ('RUNNING','PAUSED') ORDER BY j.next_sequence,j.id LIMIT 1025`, project, j.Source.ID, now.UTC())
	if e != nil {
		return nil, e
	}
	jobs, e := readRunMonitorJobs(rows)
	if e != nil {
		return nil, e
	}
	if len(jobs) > 1024 {
		return nil, errors.New("management source active job limit exceeded")
	}
	type group struct {
		source          MonitorSource
		scope, identity string
		last            int64
		first           time.Time
		jobs            []RunMonitorJob
	}
	groups := map[string]*group{}
	wanted := managementConfigurationKey(j.Source, scope, identity)
	for _, candidate := range jobs {
		if candidate.Source.ManagementScope == nil {
			continue
		}
		sh, ih, err := managementSourceHashes(candidate.Source)
		if err != nil {
			return nil, err
		}
		key := managementConfigurationKey(candidate.Source, sh, ih)
		g := groups[key]
		if g == nil {
			g = &group{source: candidate.Source, scope: sh, identity: ih, first: candidate.StartedAt}
			groups[key] = g
		}
		g.jobs = append(g.jobs, candidate)
		if candidate.StartedAt.Before(g.first) {
			g.first = candidate.StartedAt
		}
	}
	var selected string
	var best *group
	for key, g := range groups {
		if err := managementReadinessTx(tx, g.source, "", now); errors.Is(err, ErrManagementPollUnavailable) {
			continue
		} else if err != nil {
			return nil, err
		}
		if e = tx.QueryRow(`SELECT COALESCE(MAX(poll_sequence),0) FROM monitor_management_polls WHERE project_id=? AND source_id=? AND collector_id=? AND source_revision=? AND scope_hash=? AND identity_hash=?`, project, j.Source.ID, g.source.CollectorID, g.source.Revision, g.scope, g.identity).Scan(&g.last); e != nil {
			return nil, e
		}
		if best == nil || g.last < best.last || g.last == best.last && (g.first.Before(best.first) || g.first.Equal(best.first) && key < selected) {
			best, selected = g, key
		}
	}
	if best == nil || selected != wanted {
		return nil, ErrManagementPollUnavailable
	}
	// Cohorts are bounded; new zero-sequence jobs receive their own next poll.
	if len(best.jobs) > MaxRunMonitorSources {
		best.jobs = best.jobs[:MaxRunMonitorSources]
	}
	p := &ManagementPoll{ID: uuid.NewString(), ProjectID: project, SourceID: j.Source.ID, CollectorID: collector, CollectorInstanceID: instance, LeaderJobID: leader, Nonce: nonce, Generation: generation, Sequence: sequence, Source: j.Source, ScopeHash: scope, IdentityHash: identity, State: "collecting", CreatedAt: now.UTC(), Deadline: now.UTC().Add(ManagementPollLifetime), Cohort: []ManagementPollMember{}}
	leaderPresent := false
	for _, member := range best.jobs {
		if member.NextSequence < 0 || member.NextSequence == math.MaxInt64 {
			return nil, errors.New("management job sequence exhausted")
		}
		p.Cohort = append(p.Cohort, ManagementPollMember{Job: member, Sequence: member.NextSequence})
		if member.ID == leader {
			leaderPresent = true
		}
		if member.ExpiresAt.Before(p.Deadline) {
			p.Deadline = member.ExpiresAt
		}
	}
	if !leaderPresent {
		return nil, ErrManagementPollUnavailable
	}
	p.CohortHash = managementCohortHash(p.Cohort)
	payload, e := json.Marshal(p)
	if e != nil || len(payload) > 1<<20 {
		return nil, errors.New("management cohort exceeds journal limit")
	}
	if _, e = tx.Exec(`INSERT INTO monitor_management_polls(id,project_id,source_id,collector_id,instance_id,nonce,generation,poll_sequence,source_revision,scope_hash,identity_hash,state,created_at,deadline,payload) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, p.ID, project, p.SourceID, collector, instance, nonce, generation, sequence, j.Source.Revision, scope, identity, p.State, p.CreatedAt, p.Deadline, string(payload)); e != nil {
		return nil, e
	}
	if _, e = tx.Exec(`INSERT INTO monitor_collection_leases(project_id,source_id,collector_id,instance_id,generation,poll_sequence,source_revision,scope_hash,identity_hash,state,poll_id,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(project_id,source_id) DO UPDATE SET collector_id=excluded.collector_id,instance_id=excluded.instance_id,generation=excluded.generation,poll_sequence=excluded.poll_sequence,source_revision=excluded.source_revision,scope_hash=excluded.scope_hash,identity_hash=excluded.identity_hash,state=excluded.state,poll_id=excluded.poll_id,expires_at=excluded.expires_at`, project, p.SourceID, collector, instance, generation, sequence, j.Source.Revision, scope, identity, "collecting", p.ID, p.Deadline); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return p, nil
}
func managementConfigurationKey(s MonitorSource, scope, identity string) string {
	return fmt.Sprintf("%s/%d/%s/%s", s.CollectorID, s.Revision, scope, identity)
}
func managementCohortHash(cohort []ManagementPollMember) string {
	type member struct {
		JobID        string `json:"job_id"`
		RunID        string `json:"run_id"`
		NextSequence int64  `json:"next_sequence,string"`
	}
	a := make([]member, 0, len(cohort))
	for _, m := range cohort {
		a = append(a, member{m.Job.ID, m.Job.RunID, m.Sequence})
	}
	sort.Slice(a, func(i, j int) bool { return a[i].JobID < a[j].JobID })
	b, _ := json.Marshal(a)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func getManagementJobTx(tx *storeTx, project, id string) (*RunMonitorJob, error) {
	var body, state, status string
	var seq int64
	var expires time.Time
	query := `SELECT j.payload,j.state,j.next_sequence,j.expires_at,t.status FROM run_monitor_jobs j JOIN tests t ON t.id=j.test_id WHERE j.id=? AND j.project_id=?`
	if tx.dialect == storeDialectPostgres {
		// A project fence alone cannot serialize older lifecycle writers that
		// update only tests. Keep its live status stable through publication.
		query += ` FOR UPDATE OF t`
	}
	e := tx.QueryRow(query, id, project).Scan(&body, &state, &seq, &expires, &status)
	if e != nil {
		return nil, e
	}
	var j RunMonitorJob
	if e = json.Unmarshal([]byte(body), &j); e != nil {
		return nil, e
	}
	j.State, j.NextSequence, j.ExpiresAt = state, seq, expires
	if status != "RUNNING" && status != "PAUSED" {
		j.State = "terminal"
	}
	return &j, nil
}
func managementJobEligible(j RunMonitorJob, now time.Time) bool {
	return j.State == "collecting" && j.ExpiresAt.After(now) && j.Source.Enabled && j.Source.ManagementScope != nil
}
func getManagementPollTx(tx *storeTx, project, id string) (*ManagementPoll, error) {
	var b, state string
	var core sql.NullString
	e := tx.QueryRow(`SELECT payload,state,core_digest FROM monitor_management_polls WHERE project_id=? AND id=?`, project, id).Scan(&b, &state, &core)
	if e != nil {
		return nil, e
	}
	var p ManagementPoll
	if e = json.Unmarshal([]byte(b), &p); e != nil {
		return nil, e
	}
	p.State = state
	p.AcceptedCoreDigest = core.String
	return &p, nil
}
func (s *SQLiteStore) GetManagementPoll(project, id string) (*ManagementPoll, error) {
	tx, e := s.db.Begin()
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	return getManagementPollTx(tx, project, id)
}

func validateManagementReceipt(b MonitorBatchReceipt, p *ManagementPoll) error {
	if !validManagementDigest(b.Digest) || b.Sequence < 0 || b.Sequence == math.MaxInt64 || b.SampleCount < 0 || b.SampleCount > 512 || b.ReceivedAt.IsZero() || (!validStoredMonitorArtifactKey(b.ArtifactKey) || !strings.HasSuffix(b.ArtifactKey, "/"+b.Digest+".json")) || b.EvidenceVersion == nil || *b.EvidenceVersion != 2 || b.ManagementSnapshotCount == nil || *b.ManagementSnapshotCount != 1 || b.ManagementRowCount == nil || *b.ManagementRowCount < 0 || *b.ManagementRowCount > 256 || b.CollectionGeneration == nil || *b.CollectionGeneration != p.Generation || b.ManagementPollID == nil || *b.ManagementPollID != p.ID {
		return errors.New("invalid management receipt")
	}
	return nil
}
func managementReceiptEqual(a, b MonitorBatchReceipt) bool {
	aa, bb := a, b
	aa.ReceivedAt = time.Time{}
	bb.ReceivedAt = time.Time{}
	x, _ := json.Marshal(aa)
	y, _ := json.Marshal(bb)
	return string(x) == string(y)
}
func receiptSetsEqual(a, b []MonitorBatchReceipt) bool {
	if len(a) != len(b) {
		return false
	}
	aa := slices.Clone(a)
	bb := slices.Clone(b)
	sort.Slice(aa, func(i, j int) bool { return aa[i].JobID < aa[j].JobID })
	sort.Slice(bb, func(i, j int) bool { return bb[i].JobID < bb[j].JobID })
	for i := range aa {
		if !managementReceiptEqual(aa[i], bb[i]) {
			return false
		}
	}
	return true
}

// Accept publishes only after callers durably close all referenced objects.
// Prior accepted core is checked before expiry, readiness and run liveness.
func (s *SQLiteStore) AcceptManagementPoll(project, collector, instance, id, core string, receipts []MonitorBatchReceipt, now time.Time) error {
	if !validManagementDigest(core) || now.IsZero() || len(receipts) > MaxRunMonitorSources {
		return ErrManagementPollConflict
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = lockProjectForOwnedMutation(tx, project); e != nil {
		return e
	}
	p, e := getManagementPollTx(tx, project, id)
	if e != nil {
		return e
	}
	if p.CollectorID != collector || p.CollectorInstanceID != instance {
		return ErrManagementPollConflict
	}
	if p.State == "accepted" {
		if p.AcceptedCoreDigest != core || !receiptSetsEqual(p.SubmittedReceipts, receipts) {
			return ErrManagementPollConflict
		}
		return tx.Commit()
	}
	if p.State != "collecting" || p.GrantIssuedAt == nil || !p.Deadline.After(now) {
		return ErrManagementPollUnavailable
	}
	if e = managementLeaseMatchesTx(tx, p); e != nil {
		return e
	}
	if e = managementReadinessTx(tx, p.Source, instance, now); e != nil {
		return e
	}
	members := map[string]ManagementPollMember{}
	for _, m := range p.Cohort {
		members[m.Job.ID] = m
	}
	submitted := map[string]MonitorBatchReceipt{}
	for _, b := range receipts {
		m, ok := members[b.JobID]
		if !ok || b.Sequence != m.Sequence {
			return ErrManagementPollConflict
		}
		if _, ok = submitted[b.JobID]; ok {
			return ErrManagementPollConflict
		}
		if !monitorObjectOwnedBy(project, m.Job.RunID, b.ArtifactKey) || b.ReceivedAt.Before(p.CreatedAt) || b.ReceivedAt.After(now) {
			return ErrManagementPollConflict
		}
		if e = validateManagementReceipt(b, p); e != nil {
			return e
		}
		submitted[b.JobID] = b
	}
	published := []MonitorBatchReceipt{}
	for _, m := range orderedManagementCohort(p.Cohort) {
		j, err := getManagementJobTx(tx, project, m.Job.ID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if !managementJobEligible(*j, now) {
			continue
		}
		if j.Source.CollectorID != collector || j.NextSequence != m.Sequence {
			return ErrManagementPollConflict
		}
		sh, ih, err := managementSourceHashes(j.Source)
		if err != nil || sh != p.ScopeHash || ih != p.IdentityHash || j.Source.Revision != p.Source.Revision {
			return ErrManagementPollConflict
		}
		b, ok := submitted[m.Job.ID]
		if !ok {
			return ErrManagementPollConflict
		}
		if _, e = tx.Exec(`INSERT INTO run_monitor_batches(job_id,sequence,digest,artifact_key,sample_count,received_at,evidence_version,management_snapshot_count,management_row_count,collection_generation,management_poll_id) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, b.JobID, b.Sequence, b.Digest, b.ArtifactKey, b.SampleCount, b.ReceivedAt.UTC(), *b.EvidenceVersion, *b.ManagementSnapshotCount, *b.ManagementRowCount, *b.CollectionGeneration, *b.ManagementPollID); e != nil {
			return e
		}
		if _, e = tx.Exec(`UPDATE run_monitor_jobs SET next_sequence=next_sequence+1 WHERE id=? AND project_id=?`, b.JobID, project); e != nil {
			return e
		}
		published = append(published, b)
	}
	p.State = "accepted"
	p.AcceptedCoreDigest = core
	p.SubmittedReceipts = slices.Clone(receipts)
	p.PublishedReceipts = published
	compactTerminalManagementCohort(p)
	b, e := json.Marshal(p)
	if e != nil {
		return e
	}
	if _, e = tx.Exec(`UPDATE monitor_management_polls SET state='accepted',core_digest=?,payload=? WHERE id=? AND project_id=?`, core, string(b), id, project); e != nil {
		return e
	}
	if _, e = tx.Exec(`UPDATE monitor_collection_leases SET state='released' WHERE project_id=? AND source_id=? AND poll_id=?`, project, p.SourceID, id); e != nil {
		return e
	}
	return tx.Commit()
}
func managementLeaseMatchesTx(tx *storeTx, p *ManagementPoll) error {
	var generation int64
	var id, state, collector, instance string
	e := tx.QueryRow(`SELECT generation,poll_id,state,collector_id,instance_id FROM monitor_collection_leases WHERE project_id=? AND source_id=?`, p.ProjectID, p.SourceID).Scan(&generation, &id, &state, &collector, &instance)
	if e != nil {
		return e
	}
	if generation != p.Generation || id != p.ID || state != "collecting" || collector != p.CollectorID || instance != p.CollectorInstanceID {
		return ErrManagementPollConflict
	}
	return nil
}

// Abandon can mark an unresolved poll draining without releasing authority.
// A completed/process-stop proof is required to release the exact generation.
func (s *SQLiteStore) AbandonManagementPoll(project, collector, instance, id string, generation int64, proof ManagementDrainProof, now time.Time) error {
	if now.IsZero() || generation <= 0 || proof.Kind != "" && proof.Kind != "completed" && proof.Kind != "process_stopped" || proof.Kind == "process_stopped" && !validManagementText(proof.AuditID, 256) {
		return ErrManagementPollConflict
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = lockProjectForOwnedMutation(tx, project); e != nil {
		return e
	}
	p, e := getManagementPollTx(tx, project, id)
	if e != nil {
		return e
	}
	if p.CollectorID != collector || p.CollectorInstanceID != instance || p.Generation != generation {
		return ErrManagementPollConflict
	}
	if p.State == "accepted" || p.State == "abandoned" {
		return tx.Commit()
	}
	if p.State != "collecting" && p.State != "draining" {
		return ErrManagementPollConflict
	}
	var leaseID string
	var gen int64
	if e = tx.QueryRow(`SELECT poll_id,generation FROM monitor_collection_leases WHERE project_id=? AND source_id=?`, project, p.SourceID).Scan(&leaseID, &gen); e != nil {
		return e
	}
	if leaseID != id || gen != generation {
		return ErrManagementPollConflict
	}
	state := "draining"
	if proof.Kind != "" {
		state = "released"
		p.State = "abandoned"
		p.DrainProof = &proof
		at := now.UTC()
		p.DrainConfirmedAt = &at
	} else {
		p.State = "draining"
	}
	compactTerminalManagementCohort(p)
	body, e := json.Marshal(p)
	if e != nil {
		return e
	}
	if _, e = tx.Exec(`UPDATE monitor_management_polls SET state=?,payload=? WHERE id=? AND project_id=?`, p.State, string(body), id, project); e != nil {
		return e
	}
	if _, e = tx.Exec(`UPDATE monitor_collection_leases SET state=? WHERE project_id=? AND source_id=? AND poll_id=?`, state, project, p.SourceID, id); e != nil {
		return e
	}
	return tx.Commit()
}

func validateManagementDestination(raw string) error {
	u, e := url.Parse(raw)
	if !validManagementText(raw, 2048) || e != nil || u.Scheme != "management" || u.Hostname() == "" || u.Host != u.Hostname() || u.Port() != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") || u.Fragment != "" || u.Path != "" || u.Opaque != "" {
		return errors.New("management destination must be a public management://alias without port, path, credentials, query or fragment")
	}
	alias := u.Hostname()
	if len(alias) > 128 || strings.ContainsFunc(alias, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.')
	}) {
		return errors.New("management destination alias requires 1..128 ASCII letters, digits, dots, underscores or hyphens")
	}
	return nil
}

// Mark commits the authority boundary after signing succeeds, immediately
// before the controller returns any grant to the collector. Cancel and Mark
// are serialized so a released unissued admission can never escape afterward.
func (s *SQLiteStore) MarkManagementPollIssued(project, collector, instance, id string, generation int64, now time.Time) error {
	if now.IsZero() || generation <= 0 {
		return ErrManagementPollConflict
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = lockProjectForOwnedMutation(tx, project); e != nil {
		return e
	}
	p, e := getManagementPollTx(tx, project, id)
	if e != nil {
		return e
	}
	if p.CollectorID != collector || p.CollectorInstanceID != instance || p.Generation != generation || p.State != "collecting" || !p.Deadline.After(now) {
		return ErrManagementPollConflict
	}
	if e = managementLeaseMatchesTx(tx, p); e != nil {
		return e
	}
	if e = managementReadinessTx(tx, p.Source, instance, now); e != nil {
		return e
	}
	if live, err := managementPollLiveCohortTx(tx, p, now); err != nil {
		return err
	} else if !live {
		return ErrManagementPollUnavailable
	}
	if p.GrantIssuedAt != nil {
		return tx.Commit()
	}
	issued := now.UTC()
	p.GrantIssuedAt = &issued
	b, e := json.Marshal(p)
	if e != nil {
		return e
	}
	if _, e = tx.Exec(`UPDATE monitor_management_polls SET payload=? WHERE id=? AND project_id=?`, string(b), id, project); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *SQLiteStore) CancelUnissuedManagementPoll(project, collector, instance, id string, generation int64, now time.Time) error {
	if now.IsZero() || generation <= 0 {
		return ErrManagementPollConflict
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = lockProjectForOwnedMutation(tx, project); e != nil {
		return e
	}
	p, e := getManagementPollTx(tx, project, id)
	if e != nil {
		return e
	}
	if p.CollectorID != collector || p.CollectorInstanceID != instance || p.Generation != generation || p.GrantIssuedAt != nil {
		return ErrManagementPollConflict
	}
	if p.State == "abandoned" && p.DrainProof != nil && p.DrainProof.Kind == "unissued" {
		return tx.Commit()
	}
	if p.State != "collecting" && p.State != "draining" {
		return ErrManagementPollConflict
	}
	var leaseID string
	var gen int64
	var owner, startup string
	if e = tx.QueryRow(`SELECT poll_id,generation,collector_id,instance_id FROM monitor_collection_leases WHERE project_id=? AND source_id=?`, project, p.SourceID).Scan(&leaseID, &gen, &owner, &startup); e != nil {
		return e
	}
	if leaseID != id || gen != generation || owner != collector || startup != instance {
		return ErrManagementPollConflict
	}
	p.State = "abandoned"
	p.DrainProof = &ManagementDrainProof{Kind: "unissued"}
	at := now.UTC()
	p.DrainConfirmedAt = &at
	compactTerminalManagementCohort(p)
	b, e := json.Marshal(p)
	if e != nil {
		return e
	}
	if _, e = tx.Exec(`UPDATE monitor_management_polls SET state='abandoned',payload=? WHERE id=? AND project_id=?`, string(b), id, project); e != nil {
		return e
	}
	if _, e = tx.Exec(`UPDATE monitor_collection_leases SET state='released' WHERE project_id=? AND source_id=? AND poll_id=?`, project, p.SourceID, id); e != nil {
		return e
	}
	return tx.Commit()
}

// Every lifecycle-sensitive read locks tests in the same immutable order.
func orderedManagementCohort(cohort []ManagementPollMember) []ManagementPollMember {
	ordered := slices.Clone(cohort)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Job.RunID != ordered[j].Job.RunID {
			return ordered[i].Job.RunID < ordered[j].Job.RunID
		}
		return ordered[i].Job.ID < ordered[j].Job.ID
	})
	return ordered
}

func managementPollLiveCohortTx(tx *storeTx, p *ManagementPoll, now time.Time) (bool, error) {
	live := false
	for _, member := range orderedManagementCohort(p.Cohort) {
		job, err := getManagementJobTx(tx, p.ProjectID, member.Job.ID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return false, err
		}
		if !managementJobEligible(*job, now) {
			continue
		}
		scope, identity, err := managementSourceHashes(job.Source)
		if err != nil || job.Source.ID != p.SourceID || job.Source.CollectorID != p.CollectorID || job.Source.Revision != p.Source.Revision || scope != p.ScopeHash || identity != p.IdentityHash || job.NextSequence != member.Sequence {
			return false, ErrManagementPollConflict
		}
		live = true
	}
	return live, nil
}

// Source configuration is retained once per terminal poll. Full immutable jobs
// remain in the run/archive evidence; replay and signed minimal cohorts need
// only these identities. Collecting/draining polls retain all execution fields.
func compactTerminalManagementCohort(p *ManagementPoll) {
	if p.State != "accepted" && p.State != "abandoned" {
		return
	}
	for i := range p.Cohort {
		job := p.Cohort[i].Job
		p.Cohort[i].Job = RunMonitorJob{ID: job.ID, RunID: job.RunID, ProjectID: job.ProjectID}
	}
}
