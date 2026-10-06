package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/monitorprovider"
)

// Elasticsearch query source bounds.
const (
	ElasticMinBucketSeconds     = 30
	ElasticMaxBucketSeconds     = 300
	ElasticDefaultBucketSeconds = 60
	// ElasticMaxQueriedTermsFields is how many terms aggregations a query runs;
	// configured fields beyond it are dropped with a coverage warning.
	ElasticMaxQueriedTermsFields = 16
	elasticMaxConfiguredTerms    = 64
	ElasticDefaultTermsSize      = 10
	elasticMaxTermsSize          = 50
	// ElasticDefaultOverlapSeconds is the late-data re-read before the last
	// settled bucket; a bucket settles once it falls behind it.
	ElasticDefaultOverlapSeconds = 300
	// ElasticMaxTermsFieldBytes leaves room in the 256-byte metric name
	// logs.terms.<field>.<value> for the value.
	ElasticMaxTermsFieldBytes = 128

	// ElasticMaxBucketsPerPoll bounds the date buckets one poll reads.
	ElasticMaxBucketsPerPoll = 120
	// elasticMaxSearchAggBuckets keeps one search under every supported
	// server's search.max_buckets (10,000 on Elasticsearch before 7.9).
	elasticMaxSearchAggBuckets = 10000
	// elasticTraceLookupAggBuckets is the trace lookup aggregation's share.
	elasticTraceLookupAggBuckets = 1 + 64

	ElasticProfileElasticsearch = "elasticsearch"
	ElasticProfileOpenSearch    = "opensearch"
)

// ElasticQuerySelection is what an elastic-query source collects from the
// ELK connection it names: bucketed log counts, error counts and terms.
type ElasticQuerySelection struct {
	ProjectID     string   `json:"project_id"`
	BucketSeconds int      `json:"bucket_seconds,omitempty"`
	TermsFields   []string `json:"terms_fields,omitempty"`
	TermsSize     int      `json:"terms_size,omitempty"`
	// OverlapSeconds re-reads recent buckets for late documents (default 300).
	OverlapSeconds int `json:"overlap_seconds,omitempty"`
	// Profile selects the server dialect: elasticsearch (default) or
	// opensearch. Both take the same search; OpenSearch has no Elasticsearch
	// API keys, so an opensearch source needs a basic-auth connection.
	Profile string `json:"profile,omitempty"`
}

// Defaults fills unset values.
func (s ElasticQuerySelection) Defaults() ElasticQuerySelection {
	if s.BucketSeconds == 0 {
		s.BucketSeconds = ElasticDefaultBucketSeconds
	}
	if s.TermsSize == 0 {
		s.TermsSize = ElasticDefaultTermsSize
	}
	if s.OverlapSeconds == 0 {
		s.OverlapSeconds = ElasticDefaultOverlapSeconds
	}
	if s.Profile == "" {
		s.Profile = ElasticProfileElasticsearch
	}
	return s
}

// BucketsPerPoll is how many date buckets one search may read: each carries an
// error filter and every queried terms aggregation, and the whole search must
// stay under the server's aggregation bucket limit.
func (s ElasticQuerySelection) BucketsPerPoll() int {
	d := s.Defaults()
	perBucket := 2 + min(len(d.TermsFields), ElasticMaxQueriedTermsFields)*d.TermsSize
	return max(1, min(ElasticMaxBucketsPerPoll, (elasticMaxSearchAggBuckets-elasticTraceLookupAggBuckets)/perBucket))
}

// rereadBuckets is the most buckets a poll re-reads for late data: the
// overlap, plus one when the watermark falls inside a bucket.
func (s ElasticQuerySelection) rereadBuckets() int {
	d := s.Defaults()
	return (d.OverlapSeconds+d.BucketSeconds-1)/d.BucketSeconds + 1
}

// Validate bounds the selection. More than 16 terms fields is allowed and
// truncated at query time with a warning, up to 64 configured. Every poll must
// fit the late-data re-read plus at least one new bucket in one search.
func (s ElasticQuerySelection) Validate() error {
	d := s.Defaults()
	if strings.TrimSpace(s.ProjectID) == "" {
		return errors.New("Elasticsearch query selection requires a project")
	}
	if d.BucketSeconds < ElasticMinBucketSeconds || d.BucketSeconds > ElasticMaxBucketSeconds || 86400%d.BucketSeconds != 0 {
		return errors.New("Elasticsearch bucket_seconds must be 30-300 and divide a day evenly")
	}
	if d.TermsSize < 1 || d.TermsSize > elasticMaxTermsSize {
		return errors.New("Elasticsearch terms_size must be 1-50")
	}
	if d.OverlapSeconds < d.BucketSeconds || d.OverlapSeconds > 3600 {
		return errors.New("Elasticsearch overlap_seconds must cover at least one bucket and at most an hour")
	}
	if d.Profile != ElasticProfileElasticsearch && d.Profile != ElasticProfileOpenSearch {
		return errors.New("Elasticsearch profile must be elasticsearch or opensearch")
	}
	if len(s.TermsFields) > elasticMaxConfiguredTerms {
		return errors.New("Elasticsearch query selects too many terms fields")
	}
	seen := map[string]bool{}
	for _, field := range s.TermsFields {
		if strings.TrimSpace(field) != field || field == "" || len(field) > ElasticMaxTermsFieldBytes || strings.ContainsAny(field, "\r\n\x00\"") || seen[field] {
			return errors.New("Elasticsearch terms fields must be unique, non-empty and at most 128 bytes")
		}
		seen[field] = true
	}
	if s.rereadBuckets()+1 > s.BucketsPerPoll() {
		return errors.New("Elasticsearch overlap_seconds re-reads more buckets than one search can hold with these terms fields; shorten the overlap or select fewer terms")
	}
	return nil
}

// ElasticConnectionSnapshot freezes the ELK connection a run's elastic-query
// source reads. Secret fields are project secret names, never values.
type ElasticConnectionSnapshot struct {
	ConnectionID       string    `json:"connection_id"`
	ProjectID          string    `json:"project_id"`
	Endpoint           string    `json:"endpoint"`
	IndexPattern       string    `json:"index_pattern"`
	TimestampField     string    `json:"timestamp_field"`
	TraceIDField       string    `json:"trace_id_field,omitempty"`
	BaseFilter         string    `json:"base_filter,omitempty"`
	ErrorFilter        string    `json:"error_filter,omitempty"`
	AuthType           string    `json:"auth_type"`
	APIKeySecretName   string    `json:"api_key_secret_name,omitempty"`
	UsernameSecretName string    `json:"username_secret_name,omitempty"`
	PasswordSecretName string    `json:"password_secret_name,omitempty"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// ElasticSnapshot freezes this connection for an elastic-query run source.
func (c *ELKConnection) ElasticSnapshot() *ElasticConnectionSnapshot {
	if c == nil {
		return nil
	}
	return &ElasticConnectionSnapshot{
		ConnectionID: c.ID, ProjectID: c.ProjectID, Endpoint: c.Endpoint, IndexPattern: c.IndexPattern,
		TimestampField: c.TimestampField, TraceIDField: c.TraceIDField, BaseFilter: c.BaseFilter, ErrorFilter: c.ErrorFilter,
		AuthType: c.AuthType, APIKeySecretName: c.APIKeySecretName, UsernameSecretName: c.UsernameSecretName,
		PasswordSecretName: c.PasswordSecretName, UpdatedAt: c.UpdatedAt,
	}
}

func validateElasticMonitorSource(m MonitorSource, d monitorprovider.Descriptor) error {
	if m.ConnectionID == "" || m.ElasticQuery == nil || m.ElasticQuery.ProjectID != m.ProjectID || m.ElasticQuery.Validate() != nil || intervalOutside(m, d) ||
		m.CredentialRef != "" || m.Elastic != nil || m.AppDynamics != nil || m.Prometheus != nil || m.Dynatrace != nil || m.ExecutorMode != "direct" {
		return errors.New("invalid Elasticsearch query source")
	}
	return nil
}

// validateElasticSourceTx confirms the named ELK connection belongs to the
// source's project and is enabled.
func validateElasticSourceTx(tx *storeTx, m *MonitorSource) error {
	connection, err := scanELKConnection(tx.QueryRow(`SELECT `+elkConnectionColumns+` FROM elk_connections WHERE id=? AND project_id=?`, m.ConnectionID, m.ProjectID))
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("Elasticsearch query source connection unavailable in project")
	}
	if err != nil {
		return err
	}
	if !connection.Enabled {
		return errors.New("Elasticsearch query source connection is disabled")
	}
	return ValidateElasticSourceConnection(*m, connection.ElasticSnapshot())
}

// ErrElasticAuthUnsupported means an OpenSearch source's connection does not
// use basic authentication.
var ErrElasticAuthUnsupported = errors.New("OpenSearch has no Elasticsearch API keys; give the connection basic authentication")

// ValidateElasticSourceConnection checks a source against its frozen connection.
func ValidateElasticSourceConnection(source MonitorSource, snapshot *ElasticConnectionSnapshot) error {
	if snapshot == nil || source.ElasticQuery == nil || snapshot.ConnectionID != source.ConnectionID || snapshot.ProjectID != source.ProjectID {
		return errors.New("Elasticsearch query source does not match its connection")
	}
	if strings.TrimSpace(snapshot.Endpoint) == "" || strings.TrimSpace(snapshot.IndexPattern) == "" || strings.TrimSpace(snapshot.TimestampField) == "" {
		return errors.New("Elasticsearch connection lacks an endpoint, index pattern or timestamp field")
	}
	if snapshot.AuthType != ELKAuthAPIKey && snapshot.AuthType != ELKAuthBasic {
		return errors.New("Elasticsearch connection authentication is not configured")
	}
	if source.ElasticQuery.Defaults().Profile == ElasticProfileOpenSearch && snapshot.AuthType != ELKAuthBasic {
		return ErrElasticAuthUnsupported
	}
	return nil
}

// elasticAuthServesSourcesTx refuses a non-basic authentication for a
// connection that enabled OpenSearch sources read.
func elasticAuthServesSourcesTx(tx *storeTx, connection *ELKConnection) error {
	if connection.AuthType == ELKAuthBasic {
		return nil
	}
	rows, err := tx.Query(`SELECT payload FROM monitor_sources WHERE project_id=?`, connection.ProjectID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var body string
		var source MonitorSource
		if err := rows.Scan(&body); err != nil {
			return err
		}
		if json.Unmarshal([]byte(body), &source) != nil {
			continue
		}
		if source.Enabled && source.Pack == monitorprovider.PackElasticQuery && source.ConnectionID == connection.ID && source.ElasticQuery != nil && source.ElasticQuery.Defaults().Profile == ElasticProfileOpenSearch {
			return fmt.Errorf("%w (source %q reads this connection)", ErrElasticAuthUnsupported, source.Name)
		}
	}
	return rows.Err()
}

func elasticConnectionSnapshotTx(tx *storeTx, project, connectionID string) (*ElasticConnectionSnapshot, string, error) {
	connection, err := scanELKConnection(tx.QueryRow(`SELECT `+elkConnectionColumns+` FROM elk_connections WHERE id=? AND project_id=?`, connectionID, project))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "connection_deleted", nil
	}
	if err != nil {
		return nil, "", err
	}
	if !connection.Enabled {
		return nil, "connection_not_ready", nil
	}
	return connection.ElasticSnapshot(), "", nil
}
