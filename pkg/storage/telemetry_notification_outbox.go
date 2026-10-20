package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	MaxTelemetryNotificationBytes        = 64 << 10
	MaxTelemetryNotificationsPerRun      = 32
	MaxTelemetryNotificationDestinations = 64
	MaxTelemetryNotificationAttempts     = 8
	telemetryNotificationLegacyMarker    = "telemetry-outbox:v1"
)

// TelemetryNotification is an immutable, already scrubbed announcement. The
// caller supplies only authorized notification fields in Payload, never a
// manifest or webhook configuration. Destinations are opaque HMAC fingerprints
// of the authorized configuration; URLs and credentials must not be stored.
// AssessmentRevision is the SHA-256 of the versioned canonical assessment.
type TelemetryNotification struct {
	TestID             string          `json:"test_id"`
	ProjectID          string          `json:"project_id"`
	RunIncarnation     string          `json:"run_incarnation"`
	PolicyID           string          `json:"policy_id"`
	PolicyRevision     int64           `json:"policy_revision"`
	AssessmentRevision string          `json:"assessment_revision"`
	Outcome            string          `json:"outcome"`
	CompletedAt        time.Time       `json:"completed_at"`
	Payload            json.RawMessage `json:"payload"`
	Destinations       []string        `json:"destinations"`
}

type TelemetryNotificationDelivery struct {
	EventID     string
	Destination string
	State       string
	Attempts    int
	OwnerToken  string
	NextAttempt time.Time
}

func telemetryNotificationDigest(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func canonicalTelemetryNotification(in TelemetryNotification) (TelemetryNotification, string, string, error) {
	invalid := func() (TelemetryNotification, string, string, error) {
		return TelemetryNotification{}, "", "", errors.New("invalid telemetry notification")
	}
	for _, v := range []string{in.TestID, in.ProjectID, in.RunIncarnation, in.PolicyID} {
		if len(v) == 0 || len(v) > 256 || !utf8.ValidString(v) || strings.TrimSpace(v) != v || strings.ContainsAny(v, "\x00\r\n") {
			return invalid()
		}
	}
	if in.PolicyRevision <= 0 || !telemetryNotificationDigest(in.AssessmentRevision) || (in.Outcome != "fail" && in.Outcome != "inconclusive") || in.CompletedAt.IsZero() || len(in.Payload) == 0 || len(in.Payload) > MaxTelemetryNotificationBytes || !utf8.Valid(in.Payload) || len(in.Destinations) > MaxTelemetryNotificationDestinations {
		return invalid()
	}
	var payload bytes.Buffer
	if err := json.Compact(&payload, in.Payload); err != nil {
		return invalid()
	}
	if payload.Bytes()[0] != '{' {
		return invalid()
	}
	in.Payload = append(json.RawMessage(nil), payload.Bytes()...)
	in.CompletedAt = in.CompletedAt.UTC()
	in.Destinations = append([]string{}, in.Destinations...)
	slices.Sort(in.Destinations)
	for i, destination := range in.Destinations {
		if !telemetryNotificationDigest(destination) || (i > 0 && in.Destinations[i-1] == destination) {
			return invalid()
		}
	}
	identity, _ := json.Marshal([]any{"telemetry-notification/v1", in.ProjectID, in.TestID, in.RunIncarnation, in.PolicyID, in.PolicyRevision, in.AssessmentRevision, in.Outcome})
	digest := sha256.Sum256(identity)
	raw, err := json.Marshal(in)
	if err != nil || len(raw) > MaxTelemetryNotificationBytes {
		return invalid()
	}
	return in, "tna1_" + hex.EncodeToString(digest[:]), string(raw), err
}

// lockTelemetryNotificationRun serializes enqueue with run deletion and other
// enqueue operations. The no-op SQLite write obtains a writer reservation before
// reading, avoiding read-to-write upgrade failures across independent stores.
func (s *SQLiteStore) lockTelemetryNotificationRun(tx *storeTx, in TelemetryNotification) (bool, error) {
	if s.db.dialect == storeDialectSQLite {
		if _, err := tx.Exec(`UPDATE tests SET id=id WHERE id=?`, in.TestID); err != nil {
			return false, err
		}
	}
	query := `SELECT COALESCE(project_id,''),status,end_time,COALESCE(initial_provenance_json,'') FROM tests WHERE id=?`
	if s.db.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	var project, status, provenance string
	var ended sql.NullTime
	if err := tx.QueryRow(query, in.TestID).Scan(&project, &status, &ended, &provenance); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	incarnation, err := testRunIncarnationJSON(provenance)
	if err != nil {
		return false, err
	}
	return incarnation == in.RunIncarnation && project == in.ProjectID && ended.Valid && archiveIdentityTimestampEqual(s.db.dialect, ended.Time, in.CompletedAt) && (status == "COMPLETE" || status == "FAILED" || status == "ABORTED"), nil
}

// EnqueueTelemetryNotification returns enqueued, replayed, legacy_claim, or
// fenced. A legacy timestamp claim has unknown delivery status and is never
// blindly resent. The marker and bounded destination rows commit atomically.
// An empty destination set is an immutable no-recipient decision; enabling a
// webhook later must not silently reroute this already assessed outcome.
func (s *SQLiteStore) EnqueueTelemetryNotification(ctx context.Context, in TelemetryNotification, now time.Time) (string, string, error) {
	in, id, raw, err := canonicalTelemetryNotification(in)
	if err != nil || now.IsZero() {
		return "", "", errors.New("invalid telemetry notification enqueue")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	id, state, err := s.enqueueTelemetryNotificationTx(tx, in, id, raw, now)
	if err != nil {
		return id, state, err
	}
	return id, state, tx.Commit()
}

var errTelemetryNotificationCapacity = errors.New("telemetry notification run limit reached")

// The archive completion path shares this transaction; it must not acknowledge
// the catalog before the immutable notification decision is durable.
func (s *SQLiteStore) enqueueTelemetryNotificationTx(tx *storeTx, in TelemetryNotification, id, raw string, now time.Time) (string, string, error) {
	held, err := s.lockTelemetryNotificationRun(tx, in)
	if err != nil || !held {
		return id, "fenced", err
	}
	var existing string
	var version int
	err = tx.QueryRow(`SELECT format_version,envelope FROM telemetry_notification_events WHERE id=?`, id).Scan(&version, &existing)
	if err == nil {
		if version != 1 {
			return id, "", errors.New("unsupported telemetry notification version")
		}
		if existing != raw {
			return id, "", errors.New("telemetry notification immutable content conflict")
		}
		return id, "replayed", nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return id, "", err
	}
	key := RunNotificationClaimPrefix(in.TestID) + "telemetry_acceptance:" + in.Outcome
	if _, err := tx.Exec(`INSERT INTO app_settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO NOTHING`, key, telemetryNotificationLegacyMarker, now.UTC()); err != nil {
		return id, "", err
	}
	var marker string
	if err := tx.QueryRow(`SELECT value FROM app_settings WHERE key=?`, key).Scan(&marker); err != nil {
		return id, "", err
	}
	if marker != telemetryNotificationLegacyMarker {
		return id, "legacy_claim", nil
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM telemetry_notification_events WHERE test_id=?`, in.TestID).Scan(&count); err != nil {
		return id, "", err
	}
	if count >= MaxTelemetryNotificationsPerRun {
		return id, "", errTelemetryNotificationCapacity
	}
	if _, err := tx.Exec(`INSERT INTO telemetry_notification_events(id,test_id,project_id,format_version,envelope,created_at) VALUES(?,?,?,1,?,?)`, id, in.TestID, in.ProjectID, raw, now.UTC()); err != nil {
		return id, "", err
	}
	for _, destination := range in.Destinations {
		if _, err := tx.Exec(`INSERT INTO telemetry_notification_deliveries(event_id,destination,state,attempts,owner_token,next_attempt) VALUES(?,?,'pending',0,'',?)`, id, destination, now.UTC()); err != nil {
			return id, "", err
		}
	}
	return id, "enqueued", nil
}

// GetTelemetryNotification is an internal read, not an authorization boundary.
func (s *SQLiteStore) GetTelemetryNotification(ctx context.Context, id string) (*TelemetryNotification, error) {
	var raw string
	var version int
	if err := s.db.QueryRowContext(ctx, `SELECT format_version,envelope FROM telemetry_notification_events WHERE id=?`, id).Scan(&version, &raw); err != nil {
		return nil, err
	}
	if version != 1 {
		return nil, errors.New("unsupported telemetry notification version")
	}
	var event TelemetryNotification
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		return nil, err
	}
	return &event, nil
}

// ListDueTelemetryNotificationDeliveries bounds scanning and excludes all
// terminal delivery states. A lease expiry allows an at-least-once retry; it
// says nothing about whether an external receiver committed the prior request.
func (s *SQLiteStore) ListDueTelemetryNotificationDeliveries(ctx context.Context, now time.Time, limit int) ([]TelemetryNotificationDelivery, error) {
	if now.IsZero() || limit < 1 || limit > 100 {
		return nil, errors.New("invalid telemetry notification scan")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT event_id,destination,state,attempts,owner_token,next_attempt FROM telemetry_notification_deliveries WHERE state IN ('pending','leased') AND next_attempt<=? AND EXISTS(SELECT 1 FROM telemetry_notification_events e WHERE e.id=event_id AND e.format_version=1) ORDER BY next_attempt,event_id,destination LIMIT ?`, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TelemetryNotificationDelivery
	for rows.Next() {
		var item TelemetryNotificationDelivery
		if err := rows.Scan(&item.EventID, &item.Destination, &item.State, &item.Attempts, &item.OwnerToken, &item.NextAttempt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) lockTelemetryNotificationEvent(tx *storeTx, id string) (bool, error) {
	// Reserve the SQLite writer before the first read, including across stores.
	if s.db.dialect == storeDialectSQLite {
		if _, err := tx.Exec(`UPDATE telemetry_notification_events SET id=id WHERE id=?`, id); err != nil {
			return false, err
		}
	}
	var raw string
	var version int
	if err := tx.QueryRow(`SELECT format_version,envelope FROM telemetry_notification_events WHERE id=?`, id).Scan(&version, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if version != 1 {
		return false, errors.New("unsupported telemetry notification version")
	}
	var event TelemetryNotification
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		return false, err
	}
	return s.lockTelemetryNotificationRun(tx, event)
}

// ClaimTelemetryNotificationDelivery conditionally claims the exact observed
// attempt. Tokens are generated here, not supplied by an untrusted caller.
func (s *SQLiteStore) ClaimTelemetryNotificationDelivery(ctx context.Context, candidate TelemetryNotificationDelivery, now time.Time, lease time.Duration) (*TelemetryNotificationDelivery, error) {
	if now.IsZero() || lease < time.Second || lease > 2*time.Minute || candidate.Attempts < 0 {
		return nil, errors.New("invalid telemetry notification lease")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	held, err := s.lockTelemetryNotificationEvent(tx, candidate.EventID)
	if err != nil {
		return nil, err
	}
	if !held {
		return nil, retireFencedTelemetryNotification(tx, candidate.EventID)
	}
	token := uuid.NewString()
	state := "leased"
	attempts := candidate.Attempts + 1
	due := now.Add(lease).UTC()
	if candidate.Attempts >= MaxTelemetryNotificationAttempts {
		state = "exhausted"
		attempts = candidate.Attempts
		token = ""
		due = now.UTC()
	}
	result, err := tx.Exec(`UPDATE telemetry_notification_deliveries SET state=?,attempts=?,owner_token=?,next_attempt=? WHERE event_id=? AND destination=? AND attempts=? AND state IN ('pending','leased') AND next_attempt<=?`, state, attempts, token, due, candidate.EventID, candidate.Destination, candidate.Attempts, now.UTC())
	if err != nil {
		return nil, err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if state == "exhausted" {
		return nil, nil
	}
	return &TelemetryNotificationDelivery{EventID: candidate.EventID, Destination: candidate.Destination, State: state, Attempts: attempts, OwnerToken: token, NextAttempt: due}, nil
}

// FinishTelemetryNotificationDelivery accepts only fixed outcome codes; remote
// error bodies and credentials cannot enter durable state. Late acknowledgements
// cannot finish an expired or replaced lease. retry is bounded and observable.
func (s *SQLiteStore) FinishTelemetryNotificationDelivery(ctx context.Context, claim TelemetryNotificationDelivery, outcome string, now time.Time) (bool, error) {
	if now.IsZero() || claim.OwnerToken == "" || claim.Attempts < 1 || claim.Attempts > MaxTelemetryNotificationAttempts {
		return false, errors.New("invalid telemetry notification completion")
	}
	state := outcome
	due := now.UTC()
	switch outcome {
	case "delivered", "revoked":
	case "retry":
		if claim.Attempts >= MaxTelemetryNotificationAttempts {
			state = "exhausted"
		} else {
			state = "pending"
			due = now.Add(min(time.Second*time.Duration(1<<uint(claim.Attempts-1)), time.Minute)).UTC()
		}
	default:
		return false, fmt.Errorf("invalid telemetry notification outcome")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	held, err := s.lockTelemetryNotificationEvent(tx, claim.EventID)
	if err != nil {
		return false, err
	}
	if !held {
		return false, retireFencedTelemetryNotification(tx, claim.EventID)
	}
	result, err := tx.Exec(`UPDATE telemetry_notification_deliveries SET state=?,owner_token='',next_attempt=? WHERE event_id=? AND destination=? AND state='leased' AND owner_token=? AND attempts=? AND next_attempt>?`, state, due, claim.EventID, claim.Destination, claim.OwnerToken, claim.Attempts, now.UTC())
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return false, err
	}
	return true, tx.Commit()
}

// Keep obsolete outcomes as evidence, but exclude them from the bounded due
// scan so an altered/deleted incarnation cannot starve other deliveries.
func retireFencedTelemetryNotification(tx *storeTx, id string) error {
	if _, err := tx.Exec(`UPDATE telemetry_notification_deliveries SET state='revoked',owner_token='' WHERE event_id=? AND state IN ('pending','leased')`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// TelemetryNotificationID returns the versioned identity used by enqueue. It
// validates the whole candidate so a malformed projection is never reused.
func TelemetryNotificationID(in TelemetryNotification) (string, error) {
	_, id, _, err := canonicalTelemetryNotification(in)
	return id, err
}
