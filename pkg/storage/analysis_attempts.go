package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/analysis"
)

var ErrAnalysisAuthority = errors.New("analytical attempt authority lost")
var ErrAnalysisPublicationUnknown = errors.New("analytical publication requires authoritative reconciliation")

type AnalysisAttempt struct {
	PartitionManifestVersion int             `json:"partition_manifest_version,omitempty"`
	ID                       string          `json:"id"`
	ProjectID                string          `json:"project_id"`
	ImportID                 string          `json:"import_id"`
	Revision                 int64           `json:"revision"`
	InputDigest              string          `json:"input_digest"`
	Normalized               []string        `json:"normalized"`
	Rejections               []string        `json:"rejections"`
	Obsolete                 []string        `json:"obsolete,omitempty"`
	ObsoleteQuiesced         map[string]bool `json:"obsolete_quiesced,omitempty"`
	CreatedAt                time.Time       `json:"created_at"`
	AbsoluteExpiry           time.Time       `json:"absolute_expiry"`
	PublicationDigest        string          `json:"publication_digest,omitempty"`
	Published                *AnalysisImport `json:"published,omitempty"`
	LegacyWriterUnknown      bool            `json:"legacy_writer_unknown,omitempty"`
	WritersQuiesced          bool            `json:"writers_quiesced"`
	CleanupQueued            bool            `json:"cleanup_queued"`
}
type AnalysisAttemptPublication struct {
	Import    AnalysisImport
	AttemptID string
	Digest    string
}
type AnalysisAttemptResolution struct {
	Published         bool
	PublicationDigest string
	Import            *AnalysisImport
}
type AnalysisAttemptStore interface {
	ClaimAnalysisImportAttempt(context.Context, string, string, int64, time.Duration) (*AnalysisImport, *AnalysisAttempt, error)
	RenewAnalysisImportAttempt(context.Context, string, string, string, int64, time.Duration) (time.Time, error)
	PublishAnalysisImportAttempt(context.Context, AnalysisAttemptPublication) (*AnalysisImport, error)
	FailAnalysisImportAttempt(context.Context, string, string, string, int64) error
	ResolveAnalysisAttemptPublication(context.Context, string, string, string, string) (AnalysisAttemptResolution, error)
	ConfirmAnalysisAttemptWritersQuiesced(context.Context, string, string, string) error
}

// Migration must create analysis_attempt_journal before adoption. It has no
// project FK: publication receipts and exact cleanup plans outlive deletion.
func analysisAttemptInputDigest(in AnalysisImport) (string, error) {
	body, err := analysis.MarshalBoundedAnalysisJSON(struct {
		PartitionManifestVersion       int `json:"partition_manifest_version,omitempty"`
		UploadWriterVersion            int
		Files                          []AnalysisImportFile
		Mapping                        any
		AllowRejected                  bool
		MappingID                      string
		MappingRevision                int64
		PrecisionReviewSHA256          string `json:",omitempty"`
		PrecisionAcknowledgementSHA256 string `json:",omitempty"`
		PrecisionConsentRevision       int64  `json:",omitempty"`
	}{in.PartitionManifestVersion, in.UploadWriterVersion, in.Files, in.Mapping, in.AllowRejected, in.MappingID, in.MappingRevision, in.PrecisionReviewSHA256, in.PrecisionAcknowledgementSHA256, in.PrecisionConsentRevision}, 2<<20)
	if err != nil || len(body) > 2<<20 {
		return "", ErrAnalysisAuthority
	}
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:]), nil
}
func analysisAttemptKey(project, id, name string) string {
	hash := sha256.Sum256([]byte(project))
	return "datasets/v1/" + hex.EncodeToString(hash[:]) + "/" + id + "/" + name
}
func analysisAttemptLoad(ctx context.Context, tx *storeTx, id, project string) (*AnalysisImport, error) {
	var in AnalysisImport
	var body string
	query := `SELECT payload,state,revision,owner,lease_expires_at FROM analysis_imports WHERE id=? AND project_id=?`
	if tx.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	if err := tx.QueryRowContext(ctx, query, id, project).Scan(&body, &in.State, &in.Revision, &in.Owner, &in.LeaseExpiresAt); err != nil {
		return nil, err
	}
	state, revision, owner, expiry := in.State, in.Revision, in.Owner, in.LeaseExpiresAt
	if json.Unmarshal([]byte(body), &in) != nil || in.State != state || in.Revision != revision || in.ID != id || in.ProjectID != project {
		return nil, ErrAnalysisAuthority
	}
	in.Owner, in.LeaseExpiresAt = owner, expiry
	return &in, nil
}
func analysisAttemptJournalLoad(ctx context.Context, tx *storeTx, id, project string) (*AnalysisAttempt, error) {
	var body string
	query := `SELECT payload FROM analysis_attempt_journal WHERE id=? AND project_id=?`
	if tx.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	if err := tx.QueryRowContext(ctx, query, id, project).Scan(&body); err != nil {
		return nil, err
	}
	var a AnalysisAttempt
	if json.Unmarshal([]byte(body), &a) != nil || a.ID != id || a.ProjectID != project {
		return nil, ErrAnalysisAuthority
	}
	return &a, nil
}
func analysisAttemptJournalWrite(ctx context.Context, tx *storeTx, a *AnalysisAttempt) error {
	body, err := analysis.MarshalBoundedAnalysisJSON(a, 4<<20)
	if err != nil || len(body) > 4<<20 {
		return ErrAnalysisAuthority
	}
	result, err := tx.ExecContext(ctx, `UPDATE analysis_attempt_journal SET payload=? WHERE id=? AND project_id=? AND import_id=?`, string(body), a.ID, a.ProjectID, a.ImportID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrAnalysisAuthority
	}
	return err
}
func analysisAttemptJournalInsert(ctx context.Context, tx *storeTx, a *AnalysisAttempt) error {
	body, err := analysis.MarshalBoundedAnalysisJSON(a, 4<<20)
	if err != nil || len(body) > 4<<20 {
		return ErrAnalysisAuthority
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO analysis_attempt_journal(id,project_id,import_id,payload) VALUES(?,?,?,?) ON CONFLICT(id) DO NOTHING`, a.ID, a.ProjectID, a.ImportID, string(body))
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrAnalysisAuthority
	}
	return nil
}

var analysisAttemptOutputName = regexp.MustCompile(`^normalized-[a-z0-9][a-z0-9_.-]{0,140}\.parquet$`)

func analysisAttemptOwnedOutputKey(project, id, key string) bool {
	if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id {
		return false
	}
	prefix := analysisAttemptKey(project, id, "")
	return strings.HasPrefix(key, prefix) && analysisAttemptOutputName.MatchString(strings.TrimPrefix(key, prefix))
}

// A physical seal may arrive after a takeover. Read all retained journals under
// the project lock at cleanup time; a snapshot at Claim cannot be its authority.
func analysisAttemptQuiescedKeys(ctx context.Context, tx *storeTx, project, id string) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT payload FROM analysis_attempt_journal WHERE project_id=? AND import_id=?`, project, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := map[string]bool{}
	count := 0
	for rows.Next() {
		count++
		if count > 4096 {
			return nil, ErrAnalysisAuthority
		}
		var body string
		if err = rows.Scan(&body); err != nil {
			return nil, err
		}
		var prior AnalysisAttempt
		if json.Unmarshal([]byte(body), &prior) != nil || prior.ProjectID != project || prior.ImportID != id {
			return nil, ErrAnalysisAuthority
		}
		for _, key := range append(append([]string{}, prior.Normalized...), prior.Rejections...) {
			if !analysisAttemptOwnedOutputKey(project, id, key) {
				return nil, ErrAnalysisAuthority
			}
			if prior.WritersQuiesced {
				keys[key] = true
			}
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	return keys, nil
}
func analysisAttemptUpdate(ctx context.Context, tx *storeTx, in *AnalysisImport, oldRevision int64, oldOwner, oldState string) error {
	body, err := analysis.MarshalBoundedAnalysisJSON(in, 2<<20)
	if err != nil || len(body) > 2<<20 {
		return ErrAnalysisAuthority
	}
	result, err := tx.ExecContext(ctx, `UPDATE analysis_imports SET state=?,revision=?,owner=?,lease_expires_at=?,payload=? WHERE id=? AND project_id=? AND revision=? AND owner=? AND state=?`, in.State, in.Revision, in.Owner, analysisAttemptLeaseValue(tx, in.LeaseExpiresAt), string(body), in.ID, in.ProjectID, oldRevision, oldOwner, oldState)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrAnalysisAuthority
	}
	return nil
}
func analysisAttemptLeaseValue(tx *storeTx, expiry time.Time) any {
	if tx.dialect == storeDialectPostgres {
		return expiry
	}
	return expiry.UTC().Format(time.RFC3339Nano)
}
func analysisAttemptPublishUpdate(ctx context.Context, tx *storeTx, in *AnalysisImport, oldRevision int64, oldOwner string, absolute time.Time) error {
	body, err := analysis.MarshalBoundedAnalysisJSON(in, 2<<20)
	if err != nil || len(body) > 2<<20 {
		return ErrAnalysisAuthority
	}
	fence := ` AND lease_expires_at>clock_timestamp() AND ?>clock_timestamp()`
	var absoluteValue any = absolute
	if tx.dialect != storeDialectPostgres {
		fence = ` AND julianday(lease_expires_at)>julianday('now') AND julianday(?)>julianday('now')`
		absoluteValue = absolute.UTC().Format(time.RFC3339Nano)
	}
	result, err := tx.ExecContext(ctx, `UPDATE analysis_imports SET state=?,revision=?,owner=?,lease_expires_at=?,payload=? WHERE id=? AND project_id=? AND revision=? AND owner=? AND state='normalizing'`+fence, in.State, in.Revision, in.Owner, analysisAttemptLeaseValue(tx, in.LeaseExpiresAt), string(body), in.ID, in.ProjectID, oldRevision, oldOwner, absoluteValue)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrAnalysisAuthority
	}
	return nil
}
func (s *SQLiteStore) ClaimAnalysisImportAttempt(ctx context.Context, id, project string, revision int64, lease time.Duration) (*AnalysisImport, *AnalysisAttempt, error) {
	if revision < 1 || revision >= 1<<62 || lease < time.Second || lease > 16*time.Minute {
		return nil, nil, ErrAnalysisAuthority
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, project); err != nil {
		return nil, nil, err
	}
	in, err := analysisAttemptLoad(ctx, tx, id, project)
	if err != nil {
		return nil, nil, err
	}
	now, err := preciseDatabaseTimeContext(ctx, tx, tx.dialect)
	if err != nil {
		return nil, nil, err
	}
	if in.Revision != revision || in.Mapping == nil || len(in.Files) < 1 || len(in.Files) > 1024 || len(in.Mapping.Measures) < 1 || len(in.Files)*len(in.Mapping.Measures) > 1024 {
		return nil, nil, ErrAnalysisAuthority
	}
	if err := validateAnalysisPrecisionConsent(*in, revision); err != nil {
		return nil, nil, ErrAnalysisAuthority
	}
	switch in.State {
	case "mapped", "failed":
	case "normalizing":
		if now.Before(in.LeaseExpiresAt) {
			return nil, nil, ErrAnalysisAuthority
		}
	default:
		return nil, nil, ErrAnalysisAuthority
	}
	for _, file := range in.Files {
		if file.Uploaded != file.Size || file.Size < 12 {
			return nil, nil, ErrAnalysisAuthority
		}
	}
	if in.State == "normalizing" {
		if err = queueAnalysisImportAttemptsCleanupTx(tx, project, id); err != nil {
			return nil, nil, err
		}
	}
	if in.Owner != "" {
		if prior, err := analysisAttemptJournalLoad(ctx, tx, in.Owner, project); err == nil {
			prior.CleanupQueued = true
			if err = analysisAttemptJournalWrite(ctx, tx, prior); err != nil {
				return nil, nil, err
			}
			if err = queueAnalysisCleanupTx(tx, "attempt", project, prior.ID); err != nil {
				return nil, nil, err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, nil, err
		}
	}
	// Claim upgrades only the new immutable attempt; retained version0 owners
	// keep their historical digest/publication contract until reconciled.
	in.PartitionManifestVersion = analysis.PartitionManifestVersion
	in.PartitionManifest = nil
	inputDigest, err := analysisAttemptInputDigest(*in)
	if err != nil {
		return nil, nil, err
	}
	oldOwner, oldState := in.Owner, in.State
	a := &AnalysisAttempt{PartitionManifestVersion: analysis.PartitionManifestVersion, ID: uuid.NewString(), ProjectID: project, ImportID: id, Revision: revision + 1, InputDigest: inputDigest, CreatedAt: now, AbsoluteExpiry: now.Add(16 * time.Minute), Normalized: []string{}, Rejections: []string{}}
	for i := range in.Files {
		for j := range in.Mapping.Measures {
			a.Normalized = append(a.Normalized, analysisAttemptKey(project, id, fmt.Sprintf("normalized-%s-part-%04d-%02d.parquet", a.ID, i, j)))
			if in.AllowRejected {
				a.Rejections = append(a.Rejections, analysisAttemptKey(project, id, fmt.Sprintf("normalized-%s-reject-%04d-%02d.parquet", a.ID, i, j)))
			}
		}
	}
	in.ObsoleteObjects = append(in.ObsoleteObjects, in.Normalized...)
	in.ObsoleteObjects = append(in.ObsoleteObjects, in.RejectionArtifacts...)
	a.Obsolete = append([]string{}, in.ObsoleteObjects...)
	a.ObsoleteQuiesced, err = analysisAttemptQuiescedKeys(ctx, tx, project, id)
	if err != nil {
		return nil, nil, err
	}
	for _, key := range a.Obsolete {
		if !analysisAttemptOwnedOutputKey(project, id, key) {
			return nil, nil, ErrAnalysisAuthority
		}
	}

	if len(in.ObsoleteObjects) > 4096 {
		return nil, nil, ErrAnalysisAuthority
	}
	in.State, in.Owner, in.Revision, in.UpdatedAt = "normalizing", a.ID, a.Revision, now
	in.LeaseExpiresAt = now.Add(lease)
	in.Error = ""
	in.Accepted, in.Rejected = 0, 0
	in.Rejections = nil
	in.Normalized = append([]string{}, a.Normalized...)
	in.RejectionArtifacts = append([]string{}, a.Rejections...)
	in.NormalizedSHA256 = nil
	in.RejectionSHA256 = nil
	if err = analysisAttemptJournalInsert(ctx, tx, a); err != nil {
		return nil, nil, err
	}
	if len(a.Obsolete) > 0 {
		if err = queueAnalysisCleanupTx(tx, "attempt", project, a.ID); err != nil {
			return nil, nil, err
		}
	}
	if err = analysisAttemptUpdate(ctx, tx, in, revision, oldOwner, oldState); err != nil {
		return nil, nil, err
	}
	if tx.Commit() != nil {
		return nil, nil, ErrAnalysisPublicationUnknown
	}
	return in, a, nil
}
func (s *SQLiteStore) RenewAnalysisImportAttempt(ctx context.Context, id, project, owner string, revision int64, lease time.Duration) (time.Time, error) {
	if lease < time.Second || lease > 16*time.Minute {
		return time.Time{}, ErrAnalysisAuthority
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, project); err != nil {
		return time.Time{}, err
	}
	in, err := analysisAttemptLoad(ctx, tx, id, project)
	if err != nil {
		return time.Time{}, err
	}
	a, err := analysisAttemptJournalLoad(ctx, tx, owner, project)
	if err != nil {
		return time.Time{}, err
	}
	now, err := preciseDatabaseTimeContext(ctx, tx, tx.dialect)
	if err != nil {
		return time.Time{}, err
	}
	if in.State != "normalizing" || in.Owner != owner || in.Revision != revision || a.Revision != revision || !now.Before(in.LeaseExpiresAt) || !now.Before(a.AbsoluteExpiry) {
		return time.Time{}, ErrAnalysisAuthority
	}
	expiry := minAnalysisTime(now.Add(lease), a.AbsoluteExpiry)
	fence := ` AND lease_expires_at>clock_timestamp() AND ?>clock_timestamp()`
	var absoluteValue any = a.AbsoluteExpiry
	if tx.dialect != storeDialectPostgres {
		fence = ` AND julianday(lease_expires_at)>julianday('now') AND julianday(?)>julianday('now')`
		absoluteValue = a.AbsoluteExpiry.UTC().Format(time.RFC3339Nano)
	}
	result, err := tx.ExecContext(ctx, `UPDATE analysis_imports SET lease_expires_at=? WHERE id=? AND project_id=? AND revision=? AND owner=? AND state='normalizing'`+fence, analysisAttemptLeaseValue(tx, expiry), id, project, revision, owner, absoluteValue)
	if err != nil {
		return time.Time{}, err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return time.Time{}, ErrAnalysisAuthority
	}
	if tx.Commit() != nil {
		return time.Time{}, ErrAnalysisPublicationUnknown
	}
	return expiry, nil
}
func minAnalysisTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
func analysisCanonicalHash(text string) bool {
	if len(text) != 64 || strings.ToLower(text) != text {
		return false
	}
	body, err := hex.DecodeString(text)
	return err == nil && len(body) == 32
}
func AnalysisAttemptResultDigest(in AnalysisImport) (string, error) {
	// Version1 hashes the durable representation: these owner-managed output
	// collections use omitempty in AnalysisImport, so an empty []/{} reloads as
	// nil. Historical version0 receipts retain their exact original shape.
	if in.PartitionManifestVersion == analysis.PartitionManifestVersion {
		if len(in.Normalized) == 0 {
			in.Normalized = nil
		}
		if len(in.NormalizedSHA256) == 0 {
			in.NormalizedSHA256 = nil
		}
		if len(in.RejectionArtifacts) == 0 {
			in.RejectionArtifacts = nil
		}
		if len(in.RejectionSHA256) == 0 {
			in.RejectionSHA256 = nil
		}
		if len(in.Rejections) == 0 {
			in.Rejections = nil
		}
	}
	body, err := analysis.MarshalBoundedAnalysisJSON(struct {
		PartitionManifestVersion int                                   `json:"partition_manifest_version,omitempty"`
		PartitionManifest        *analysis.NormalizedPartitionManifest `json:"partition_manifest,omitempty"`
		Files                    []AnalysisImportFile
		Normalized               []string
		NormalizedSHA256         map[string]string
		RejectionArtifacts       []string
		RejectionSHA256          map[string]string
		Rejections               any
		Accepted, Rejected       int64
	}{in.PartitionManifestVersion, in.PartitionManifest, in.Files, in.Normalized, in.NormalizedSHA256, in.RejectionArtifacts, in.RejectionSHA256, in.Rejections, in.Accepted, in.Rejected}, 2<<20)
	if err != nil || len(body) > 2<<20 {
		return "", ErrAnalysisAuthority
	}
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:]), nil
}
func ValidateAnalysisPublication(in AnalysisImport, a AnalysisAttempt) error {
	if in.ID != a.ImportID || in.ProjectID != a.ProjectID || in.Owner != a.ID || in.Revision != a.Revision || in.State != "normalizing" || in.Accepted < 0 || in.Rejected < 0 || !reflect.DeepEqual(in.Normalized, a.Normalized) || len(in.NormalizedSHA256) != len(a.Normalized) || len(in.RejectionArtifacts) != len(in.RejectionSHA256) {
		return ErrAnalysisAuthority
	}
	if in.PartitionManifestVersion != a.PartitionManifestVersion {
		return ErrAnalysisAuthority
	}
	switch a.PartitionManifestVersion {
	case 0:
		if in.PartitionManifest != nil {
			return ErrAnalysisAuthority
		}
	case analysis.PartitionManifestVersion:
		if in.PartitionManifest == nil || in.Mapping == nil {
			return ErrAnalysisAuthority
		}
		mappingHash, err := analysis.PartitionMappingDigest(*in.Mapping)
		if err != nil || in.PartitionManifest.MatchesArtifacts(in.ProjectID, in.ID, mappingHash, in.Mapping.Version, in.Normalized, in.NormalizedSHA256, in.Accepted) != nil {
			return ErrAnalysisAuthority
		}
	default:
		return ErrAnalysisAuthority
	}
	for _, key := range a.Normalized {
		if !analysisCanonicalHash(in.NormalizedSHA256[key]) {
			return ErrAnalysisAuthority
		}
	}
	allowed := map[string]bool{}
	for _, key := range a.Rejections {
		allowed[key] = true
	}
	seen := map[string]bool{}
	for _, key := range in.RejectionArtifacts {
		if !allowed[key] || seen[key] || !analysisCanonicalHash(in.RejectionSHA256[key]) {
			return ErrAnalysisAuthority
		}
		seen[key] = true
	}
	for _, file := range in.Files {
		if !analysisCanonicalHash(file.SHA256) {
			return ErrAnalysisAuthority
		}
	}
	if in.Rejected > 0 && !in.AllowRejected {
		return ErrAnalysisAuthority
	}
	return nil
}
func (s *SQLiteStore) PublishAnalysisImportAttempt(ctx context.Context, p AnalysisAttemptPublication) (*AnalysisImport, error) {
	if !analysisCanonicalHash(p.Digest) || p.AttemptID == "" {
		return nil, ErrAnalysisAuthority
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, p.Import.ProjectID); err != nil {
		return nil, err
	}
	in, err := analysisAttemptLoad(ctx, tx, p.Import.ID, p.Import.ProjectID)
	if err != nil {
		return nil, err
	}
	a, err := analysisAttemptJournalLoad(ctx, tx, p.AttemptID, p.Import.ProjectID)
	if err != nil {
		return nil, err
	}
	digest, err := AnalysisAttemptResultDigest(p.Import)
	if err != nil || digest != p.Digest {
		return nil, ErrAnalysisAuthority
	}
	if a.PublicationDigest != "" {
		if a.PublicationDigest != p.Digest || a.Published == nil {
			return nil, ErrAnalysisAuthority
		}
		return a.Published, nil
	}
	now, err := preciseDatabaseTimeContext(ctx, tx, tx.dialect)
	if err != nil {
		return nil, err
	}
	if in.State != "normalizing" || in.Revision != a.Revision || in.Owner != a.ID || !now.Before(in.LeaseExpiresAt) || !now.Before(a.AbsoluteExpiry) || !a.WritersQuiesced || ValidateAnalysisPublication(p.Import, *a) != nil {
		return nil, ErrAnalysisAuthority
	}
	expectedInput, err := analysisAttemptInputDigest(*in)
	if err != nil || expectedInput != a.InputDigest {
		return nil, ErrAnalysisAuthority
	}
	// File seals may be added only by this attempt; every upload cursor/chunk and
	// the mapping/source identity are held at the original durable revision.
	if len(p.Import.Files) != len(in.Files) {
		return nil, ErrAnalysisAuthority
	}
	submitted := p.Import
	submitted.Files = append([]AnalysisImportFile{}, p.Import.Files...)
	for i := range submitted.Files {
		file := submitted.Files[i]
		if in.Files[i].SHA256 != "" && file.SHA256 != in.Files[i].SHA256 {
			return nil, ErrAnalysisAuthority
		}
		file.SHA256 = in.Files[i].SHA256
		submitted.Files[i] = file
	}
	submittedInput, err := analysisAttemptInputDigest(submitted)
	if err != nil || submittedInput != a.InputDigest {
		return nil, ErrAnalysisAuthority
	}
	committed := p.Import
	committed.State, committed.Owner, committed.LeaseExpiresAt = "ready", "", time.Time{}
	committed.Revision++
	committed.UpdatedAt = now
	committed.CreatedAt = in.CreatedAt
	committed.Name = in.Name
	committed.RunID = in.RunID
	committed.Error = ""
	committed.ObsoleteObjects = nil
	a.PublicationDigest = p.Digest
	a.Published = &committed
	if err = analysisAttemptJournalWrite(ctx, tx, a); err != nil {
		return nil, err
	}
	// Re-read DB time immediately before publication, after validation and locks.
	fresh, err := preciseDatabaseTimeContext(ctx, tx, tx.dialect)
	if err != nil || !fresh.Before(in.LeaseExpiresAt) || !fresh.Before(a.AbsoluteExpiry) {
		return nil, ErrAnalysisAuthority
	}
	if err = analysisAttemptPublishUpdate(ctx, tx, &committed, in.Revision, in.Owner, a.AbsoluteExpiry); err != nil {
		return nil, err
	}
	if tx.Commit() != nil {
		return nil, ErrAnalysisPublicationUnknown
	}
	return &committed, nil
}
func (s *SQLiteStore) FailAnalysisImportAttempt(ctx context.Context, id, project, owner string, revision int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, project); err != nil {
		return err
	}
	in, err := analysisAttemptLoad(ctx, tx, id, project)
	if err != nil {
		return err
	}
	a, err := analysisAttemptJournalLoad(ctx, tx, owner, project)
	if err != nil {
		return err
	}
	if in.State != "normalizing" || in.Revision != revision || in.Owner != owner || a.Revision != revision || a.PublicationDigest != "" {
		return ErrAnalysisAuthority
	}
	now, err := preciseDatabaseTimeContext(ctx, tx, tx.dialect)
	if err != nil {
		return err
	}
	// An expired, still-current owner may record terminal failure/cleanup only.
	in.State, in.Owner, in.LeaseExpiresAt = "failed", "", time.Time{}
	in.Revision++
	in.UpdatedAt = now
	in.Error = "Normalization failed; inspect mapping, schema, rejection and resource evidence."
	a.CleanupQueued = true
	if err = analysisAttemptJournalWrite(ctx, tx, a); err != nil {
		return err
	}
	if err = queueAnalysisCleanupTx(tx, "attempt", project, a.ID); err != nil {
		return err
	}
	if err = analysisAttemptUpdate(ctx, tx, in, revision, owner, "normalizing"); err != nil {
		return err
	}
	if tx.Commit() != nil {
		return ErrAnalysisPublicationUnknown
	}
	return nil
}
func (s *SQLiteStore) ResolveAnalysisAttemptPublication(ctx context.Context, id, project, owner, digest string) (AnalysisAttemptResolution, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AnalysisAttemptResolution{}, err
	}
	defer tx.Rollback()
	// A missing project means deletion won; the journal still protects the exact
	// accepted plan and deletion cleanup may proceed. Ordinary reads cannot race
	// an in-flight commit because the project lock serializes this decision.
	if _, err = lockProjectForOwnedMutation(tx, project); err != nil {
		var present int
		if e := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id=?`, project).Scan(&present); e != nil || present != 0 {
			return AnalysisAttemptResolution{}, ErrAnalysisPublicationUnknown
		}
	}
	a, err := analysisAttemptJournalLoad(ctx, tx, owner, project)
	if err != nil {
		return AnalysisAttemptResolution{}, ErrAnalysisPublicationUnknown
	}
	if a.ImportID != id || a.PublicationDigest != "" && a.PublicationDigest != digest {
		return AnalysisAttemptResolution{}, ErrAnalysisAuthority
	}
	result := AnalysisAttemptResolution{Published: a.PublicationDigest != "", PublicationDigest: a.PublicationDigest, Import: a.Published}
	if tx.Commit() != nil {
		return AnalysisAttemptResolution{}, ErrAnalysisPublicationUnknown
	}
	return result, nil
}

// Only the parent-owned pipeline calls this after exact process reap, joined
// workers/readers and synchronous durable writer closure. It is not an HTTP
// operation and is never driven by a request boolean, an expiry or an outbox.
// The database records the parent's proof; it does not observe an OS process.
func (s *SQLiteStore) ConfirmAnalysisAttemptWritersQuiesced(ctx context.Context, id, project, owner string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, project); err != nil {
		var present int
		if e := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id=?`, project).Scan(&present); e != nil || present != 0 {
			return ErrAnalysisPublicationUnknown
		}
	}
	_, err = analysisAttemptLoad(ctx, tx, id, project)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	a, err := analysisAttemptJournalLoad(ctx, tx, owner, project)
	if err != nil {
		return err
	}
	if a.ImportID != id || a.LegacyWriterUnknown {
		return ErrAnalysisAuthority
	}
	// A replaced owner may seal only its own retained journal, never mutate the
	// new import or acquire publication. An active owner has the matching token.
	a.WritersQuiesced = true
	if err = analysisAttemptJournalWrite(ctx, tx, a); err != nil {
		return err
	}
	if tx.Commit() != nil {
		return ErrAnalysisPublicationUnknown
	}
	return nil
}

type AnalysisAttemptCleanupPlan struct {
	Keys    []string
	Blocked bool
}

func (s *SQLiteStore) AnalysisAttemptCleanupPlan(ctx context.Context, job AnalysisCleanupJob) (AnalysisAttemptCleanupPlan, error) {
	if job.Kind != "attempt" {
		return AnalysisAttemptCleanupPlan{}, ErrAnalysisAuthority
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AnalysisAttemptCleanupPlan{}, err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, job.ProjectID); err != nil {
		var present int
		if e := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id=?`, job.ProjectID).Scan(&present); e != nil || present != 0 {
			return AnalysisAttemptCleanupPlan{}, ErrAnalysisPublicationUnknown
		}
	}
	var importID string
	if err = tx.QueryRowContext(ctx, `SELECT import_id FROM analysis_attempt_journal WHERE id=? AND project_id=?`, job.ResourceID, job.ProjectID).Scan(&importID); err != nil {
		return AnalysisAttemptCleanupPlan{}, err
	}
	in, err := analysisAttemptLoad(ctx, tx, importID, job.ProjectID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return AnalysisAttemptCleanupPlan{}, err
	}
	a, err := analysisAttemptJournalLoad(ctx, tx, job.ResourceID, job.ProjectID)
	if err != nil {
		return AnalysisAttemptCleanupPlan{}, err
	}
	plan := AnalysisAttemptCleanupPlan{Keys: []string{}}
	for _, key := range append(append(append([]string{}, a.Normalized...), a.Rejections...), a.Obsolete...) {
		if !analysisAttemptOwnedOutputKey(job.ProjectID, importID, key) {
			return AnalysisAttemptCleanupPlan{}, ErrAnalysisAuthority
		}
	}
	quiesced, err := analysisAttemptQuiescedKeys(ctx, tx, job.ProjectID, importID)
	if err != nil {
		return AnalysisAttemptCleanupPlan{}, err
	}
	for _, key := range a.Obsolete {
		if !analysisAttemptOwnedOutputKey(job.ProjectID, importID, key) {
			return AnalysisAttemptCleanupPlan{}, ErrAnalysisAuthority
		}
		if quiesced[key] {
			plan.Keys = append(plan.Keys, key)
		} else {
			plan.Blocked = true
		}
	}
	// Unknown legacy/foreign physical writer ownership remains retained. Expiry
	// and a directory/SQL timestamp never substitute for a quiescence proof.
	// Queuing the current attempt for obsolete cleanup does not abandon its
	// planned outputs. Publication receipts/current references protect them.
	if a.CleanupQueued {
		if !a.WritersQuiesced {
			plan.Blocked = true
		} else if a.PublicationDigest == "" || in == nil || in.State == "cancelled" || in.State == "deleted" || in.State == "deleting" {
			plan.Keys = append(plan.Keys, a.Normalized...)
			plan.Keys = append(plan.Keys, a.Rejections...)
		}
	}
	if in != nil {
		protected := map[string]bool{}
		for _, key := range in.Normalized {
			protected[key] = true
		}
		for _, key := range in.RejectionArtifacts {
			protected[key] = true
		}
		filtered := plan.Keys[:0]
		for _, key := range plan.Keys {
			if !protected[key] || in.State == "cancelled" || in.State == "deleted" || in.State == "deleting" || in.State == "failed" {
				filtered = append(filtered, key)
			}
		}
		plan.Keys = filtered
	}
	if tx.Commit() != nil {
		return AnalysisAttemptCleanupPlan{}, ErrAnalysisPublicationUnknown
	}
	return plan, nil
}
