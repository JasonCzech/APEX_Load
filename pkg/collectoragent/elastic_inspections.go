package collectoragent

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/elastic"
)

type ElasticInspectionJob struct {
	CleanupContinuations []*analysis.ProviderContinuation `json:"cleanup_continuations,omitempty"`
	ID                   string                           `json:"id"`
	ProjectID            string                           `json:"project_id"`
	ConnectionID         string                           `json:"connection_id"`
	ConnectionRevision   int64                            `json:"connection_revision"`
	CollectorID          string                           `json:"collector_id"`
	Kind                 string                           `json:"kind"`
	Config               elastic.Config                   `json:"config"`
	Indices              []string                         `json:"indices,omitempty"`
	Selection            *elastic.Selection               `json:"selection,omitempty"`
	RunID                string                           `json:"run_id,omitempty"`
	FromUTC              time.Time                        `json:"from_utc"`
	ToUTC                time.Time                        `json:"to_utc"`
	Credentials          *SealedCredentials               `json:"credentials"`
	OwnerToken           string                           `json:"owner_token"`
	LeaseExpiresAt       time.Time                        `json:"lease_expires_at"`
}
type SignedElasticInspectionJob struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}
type ElasticInspectionData struct {
	CleanupContinuations []*analysis.ProviderContinuation `json:"-"`
	Capabilities         elastic.Capabilities             `json:"capabilities"`
	Indices              []elastic.Index                  `json:"indices,omitempty"`
	Fields               []elastic.Field                  `json:"fields,omitempty"`
	Preview              *elastic.Result                  `json:"preview,omitempty"`
	Truncated            bool                             `json:"truncated,omitempty"`
}
type ElasticInspectionResult struct {
	CleanupContinuations []*analysis.ProviderContinuation `json:"cleanup_continuations,omitempty"`
	Job                  SignedElasticInspectionJob       `json:"job"`
	Data                 ElasticInspectionData            `json:"data"`
	Message              string                           `json:"message,omitempty"`
}

const normalElasticInspectionJobBytes = 96 << 10
const cleanupElasticInspectionJobBytes = 512 << 10

func elasticInspectionJobByteLimit(kind string) int {
	if kind == "cleanup" {
		return cleanupElasticInspectionJobBytes
	}
	return normalElasticInspectionJobBytes
}

func SignElasticInspectionJob(job ElasticInspectionJob, key ed25519.PrivateKey) (SignedElasticInspectionJob, error) {
	if len(key) != ed25519.PrivateKeySize {
		return SignedElasticInspectionJob{}, errors.New("inspection signing key unavailable")
	}
	body, err := json.Marshal(job)
	if err != nil || len(body) > elasticInspectionJobByteLimit(job.Kind) {
		return SignedElasticInspectionJob{}, errors.New("Elastic inspection exceeds limit")
	}
	return SignedElasticInspectionJob{string(body), base64.RawStdEncoding.EncodeToString(ed25519.Sign(key, body))}, nil
}
func VerifyElasticInspectionJob(envelope SignedElasticInspectionJob, key ed25519.PublicKey, collector string, now time.Time) (ElasticInspectionJob, error) {
	var job ElasticInspectionJob
	sig, err := base64.RawStdEncoding.DecodeString(envelope.Signature)
	if err != nil || len(key) != ed25519.PublicKeySize || len(envelope.Payload) > cleanupElasticInspectionJobBytes || !ed25519.Verify(key, []byte(envelope.Payload), sig) {
		return job, errors.New("Elastic inspection signature rejected")
	}
	if json.Unmarshal([]byte(envelope.Payload), &job) != nil || len(envelope.Payload) > elasticInspectionJobByteLimit(job.Kind) || job.ID == "" || job.ProjectID == "" || job.ConnectionID == "" || job.ConnectionRevision < 1 || job.CollectorID != collector || job.OwnerToken == "" || !job.LeaseExpiresAt.After(now) || job.LeaseExpiresAt.After(now.Add(2*time.Minute)) || job.Config.Validate() != nil || len(job.Indices) > 32 {
		return ElasticInspectionJob{}, errors.New("Elastic inspection identity rejected")
	}
	switch job.Kind {
	case "verify", "discover_indices", "discover_fields", "preview":
		if len(job.CleanupContinuations) != 0 {
			return ElasticInspectionJob{}, errors.New("unexpected Elastic inspection cleanup handles")
		}
	case "cleanup":
		if len(job.CleanupContinuations) == 0 || len(job.CleanupContinuations) > 32 {
			return ElasticInspectionJob{}, errors.New("invalid Elastic inspection cleanup handles")
		}
		for _, cursor := range job.CleanupContinuations {
			if cursor == nil || cursor.Validate() != nil || cursor.PITID == "" && cursor.AsyncID == "" || cursor.PITID != "" && cursor.AsyncID != "" || len(cursor.SearchAfter) != 0 || len(cursor.CompositeAfter) != 0 {
				return ElasticInspectionJob{}, errors.New("invalid Elastic inspection cleanup ownership")
			}
		}
	default:
		return ElasticInspectionJob{}, errors.New("invalid Elastic inspection kind")
	}
	if job.Selection != nil && (job.Selection.ProjectID != job.ProjectID || job.Selection.Validate() != nil) {
		return ElasticInspectionJob{}, errors.New("invalid Elastic inspection selection")
	}
	return job, nil
}
func elasticInspectionCredentialJob(job ElasticInspectionJob) RunJob {
	return RunJob{ID: "inspection:" + job.ID, ProjectID: job.ProjectID, CollectorID: job.CollectorID, SourceID: job.ConnectionID, SourceRevision: job.ConnectionRevision, Pack: "elastic-inspection", Destination: job.Config.Endpoint, LeaseExpiresAt: job.LeaseExpiresAt, Credentials: job.Credentials}
}
func SealElasticInspectionCredentials(job ElasticInspectionJob, key []byte, c Credentials) (*SealedCredentials, error) {
	return SealCredentials(elasticInspectionCredentialJob(job), key, c)
}

// InspectElastic is shared by direct and collector execution. Preview results
// are scrubbed, bounded and stripped of executor-private continuation handles.
func InspectElastic(ctx context.Context, client *elastic.Client, kind string, indices []string, selection *elastic.Selection, from, to time.Time, runID ...string) (ElasticInspectionData, error) {
	var out ElasticInspectionData
	var err error
	switch kind {
	case "verify":
		out.Capabilities, err = client.Verify(ctx, indices)
		if err == nil && selection != nil && (len(selection.Metrics) > 0 || len(selection.Queries) > 0 || selection.Logs || selection.Errors || selection.Traces || selection.Transactions) {
			fields, fieldErr := client.DiscoverFields(ctx, selection.Indices)
			if fieldErr == nil {
				fieldErr = elastic.ValidateSelectionFields(*selection, fields)
			}
			if fieldErr != nil {
				err = errors.New("selected Elastic schema is unavailable or incompatible")
			}
		}
	case "discover_indices":
		out.Indices, err = client.DiscoverIndices(ctx, indices)
		if len(out.Indices) > 200 {
			out.Indices = out.Indices[:200]
			out.Truncated = true
		}
	case "discover_fields":
		out.Fields, err = client.DiscoverFields(ctx, indices)
		if len(out.Fields) > 1000 {
			out.Fields = out.Fields[:1000]
			out.Truncated = true
		}
	case "preview":
		if selection == nil {
			return out, errors.New("preview selection required")
		}
		previewRunID := "preview"
		if len(runID) > 0 && runID[0] != "" {
			previewRunID = runID[0]
		}
		result, e := client.Preview(ctx, *selection, from, to, previewRunID)
		err = e
		if result.Continuation != nil {
			if owned := closeElasticInspectionPreviewCursor(client, result.Continuation); owned != nil {
				out.CleanupContinuations = append(out.CleanupContinuations, owned)
			}
			out.Truncated = true
		}
		out.CleanupContinuations = append(out.CleanupContinuations, result.CleanupContinuations...)
		result.CleanupContinuations = nil
		if !result.Progress.Complete {
			out.Truncated = true
		}
		result.Continuation = nil
		result.Progress.Continuation = nil
		if len(result.Evidence) > 100 {
			result.Evidence = result.Evidence[:100]
			out.Truncated = true
		}
		if len(result.Points) > 1000 {
			result.Points = result.Points[:1000]
			out.Truncated = true
		}
		out.Preview = &result
	default:
		return out, errors.New("unsupported Elastic inspection")
	}
	out.CleanupContinuations = mergeElasticInspectionCleanup(out.CleanupContinuations, client.PendingCleanupContinuations())
	if e := ValidateElasticInspectionData(out); e != nil {
		return ElasticInspectionData{CleanupContinuations: out.CleanupContinuations}, e
	}
	return out, err
}

// Preview normally releases active contexts in the provider. Preserve
// ownership defensively if a provider result still carries an active cursor.
func closeElasticInspectionPreviewCursor(client *elastic.Client, cursor *analysis.ProviderContinuation) *analysis.ProviderContinuation {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := client.CloseContinuation(cleanupCtx, cursor)
	cancel()
	var response *elastic.HTTPError
	if err == nil || errors.As(err, &response) && response.Status == 404 || !cursor.KeepAliveUntil.IsZero() && !cursor.KeepAliveUntil.After(time.Now().UTC()) {
		return nil
	}
	owned := *cursor
	owned.ResolvedIndices, owned.SearchAfter, owned.CompositeAfter = nil, nil, nil
	return &owned
}

// Preview copies pending ownership to reserve space before any new upstream
// work. Publish each owned handle once when merging that copy with its parent.
func mergeElasticInspectionCleanup(lists ...[]*analysis.ProviderContinuation) []*analysis.ProviderContinuation {
	out := []*analysis.ProviderContinuation{}
	for _, list := range lists {
		for _, cursor := range list {
			duplicate := false
			for _, prior := range out {
				if prior != nil && cursor != nil && prior.PITID == cursor.PITID && prior.AsyncID == cursor.AsyncID {
					duplicate = true
					break
				}
			}
			if !duplicate {
				out = append(out, cursor)
			}
		}
	}
	return out
}

func ValidateElasticInspectionData(data ElasticInspectionData) error {
	if len(data.CleanupContinuations) > 32 {
		return errors.New("too many private inspection cleanup handles")
	}
	for _, cursor := range data.CleanupContinuations {
		if cursor == nil || cursor.Validate() != nil || cursor.PITID == "" && cursor.AsyncID == "" || cursor.PITID != "" && cursor.AsyncID != "" {
			return errors.New("invalid private inspection cleanup handle")
		}
	}
	if len(data.Indices) > 200 || len(data.Fields) > 1000 || len(data.Capabilities.Status) > 32 || len(data.Capabilities.Version) > 128 {
		return errors.New("Elastic inspection limits exceeded")
	}
	for _, index := range data.Indices {
		if index.Name == "" || len(index.Name) > 256 {
			return errors.New("invalid discovered index")
		}
	}
	for _, field := range data.Fields {
		if field.Name == "" || len(field.Name) > 512 || len(field.Types) > 16 {
			return errors.New("invalid discovered field")
		}
	}
	if data.Preview != nil {
		if data.Preview.Continuation != nil || data.Preview.Progress.Continuation != nil || len(data.Preview.Evidence) > 100 || len(data.Preview.Points) > 1000 {
			return errors.New("invalid preview projection")
		}
		for i, e := range data.Preview.Evidence {
			if e.Validate() != nil {
				return errors.New("invalid preview evidence")
			}
			e = scrubElasticEvidence(e)
			data.Preview.Evidence[i] = e
		}
		for _, p := range data.Preview.Points {
			if p.Validate() != nil {
				return errors.New("invalid preview measurement")
			}
		}
	}
	b, err := json.Marshal(data)
	if err != nil || len(b) > 1<<20 {
		return errors.New("Elastic inspection result too large")
	}
	return nil
}
func (a *Agent) elasticInspectionTick(ctx context.Context) error {
	a.retryElasticInspectionSpool(ctx)
	capacity := cap(a.providerSlots) - len(a.providerSlots)
	if capacity < 1 {
		return nil
	}
	var jobs []SignedElasticInspectionJob
	if err := a.jobRequest(ctx, "GET", fmt.Sprintf("elastic-inspections?capacity=%d", capacity), nil, &jobs); err != nil {
		return err
	}
	if len(jobs) > capacity {
		return errors.New("too many Elastic inspection jobs")
	}
	for _, envelope := range jobs {
		job, err := VerifyElasticInspectionJob(envelope, a.cfg.ControllerPublicKey, a.cfg.CollectorID, time.Now().UTC())
		if err != nil {
			return err
		}
		a.stateMu.Lock()
		if a.providerClosing {
			a.stateMu.Unlock()
			return nil
		}
		key := "elastic-inspection:" + job.ID
		if a.providerBusy == nil {
			a.providerBusy = map[string]bool{}
		}
		if a.providerBusy[key] {
			a.stateMu.Unlock()
			continue
		}
		select {
		case a.providerSlots <- struct{}{}:
		default:
			a.stateMu.Unlock()
			continue
		}
		a.providerBusy[key] = true
		a.providerWorkers.Add(1)
		a.stateMu.Unlock()
		go func() {
			defer a.providerWorkers.Done()
			defer func() { <-a.providerSlots; a.stateMu.Lock(); delete(a.providerBusy, key); a.stateMu.Unlock() }()
			_ = a.executeElasticInspection(ctx, job, envelope)
		}()
	}
	return nil
}
func (a *Agent) executeElasticInspection(ctx context.Context, job ElasticInspectionJob, envelope SignedElasticInspectionJob) error {
	if job.Kind != "cleanup" {
		if replayed, err := a.replayElasticInspectionResult(ctx, job, envelope); replayed {
			return err
		}
	}
	key := elasticInspectionSpoolKey(job)
	reservation := "inspection-active:" + key
	if err := a.spoolBudget.reserveFresh(reservation, []string{"inspection:" + key}); err != nil {
		return err
	}
	defer a.spoolBudget.release(reservation)
	if job.Kind == "cleanup" {
		return a.executeElasticInspectionCleanup(ctx, job, envelope)
	}
	target, approved := a.adapterTarget(RunJob{Pack: "elastic", Destination: job.Config.Endpoint})
	if !approved {
		return errors.New("Elastic inspection destination is not locally approved")
	}
	indices := job.Indices
	if job.Selection != nil {
		indices = job.Selection.Indices
	}
	if len(indices) == 0 && (job.Selection == nil || elastic.HasIndexedSignals(*job.Selection)) {
		indices = target.Indices
	}
	needsIndices := job.Kind == "discover_indices" || job.Kind == "discover_fields" || len(indices) > 0
	if job.Selection != nil {
		needsIndices = needsIndices || elastic.HasIndexedSignals(*job.Selection)
	}
	if needsIndices && !elasticIndicesApproved(indices, target.Indices) {
		return errors.New("Elastic inspection indices are not locally approved")
	}
	credentials, err := OpenCredentials(elasticInspectionCredentialJob(job), a.cfg.CredentialPrivateKey, time.Now().UTC())
	if err != nil || credentials.Elastic == nil {
		return errors.New("Elastic inspection credential unavailable")
	}
	if job.Selection != nil {
		for _, query := range job.Selection.Queries {
			if query.Language == "esql" && !target.ElasticESQL {
				return errors.New("ES|QL is not locally approved")
			}
		}
		if (job.Selection.ClusterHealth || job.Selection.KibanaHealth || job.Selection.LogstashHealth) && !target.ElasticHealth {
			return errors.New("component health is not locally approved")
		}
	}
	kibana, logstash := false, false
	if job.Selection != nil {
		kibana, logstash = job.Selection.KibanaHealth, job.Selection.LogstashHealth
	} else if job.Kind == "verify" && target.ElasticHealth {
		// Connection verification probes each independently approved profile;
		// an unapproved optional component cannot block base search readiness.
		kibana = job.Config.KibanaEndpoint != "" && job.Config.KibanaEndpoint == target.KibanaEndpoint
		logstash = job.Config.LogstashEndpoint != "" && job.Config.LogstashEndpoint == target.LogstashEndpoint
	}
	cfg, err := elasticLocallyApprovedConfig(job.Config, target, kibana, logstash)
	if err != nil {
		return err
	}
	client, err := a.newElasticClient(cfg, credentials.Elastic.Runtime())
	if err != nil {
		return err
	}
	defer client.Close()
	client.SetVerificationPolicy(elastic.VerificationPolicy{ESQL: target.ElasticESQL, Health: target.ElasticHealth})
	leaseCtx, cancel := context.WithDeadline(ctx, job.LeaseExpiresAt)
	defer cancel()
	data, err := InspectElastic(leaseCtx, client, job.Kind, indices, job.Selection, job.FromUTC, job.ToUTC, job.RunID)
	message := ""
	if err != nil {
		message = "Elastic inspection failed"
	}
	result := &ElasticInspectionResult{Job: envelope, Data: data, Message: message, CleanupContinuations: data.CleanupContinuations}
	if err := a.persistElasticInspectionResult(job.ID, result); err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, cursor := range result.CleanupContinuations {
			_ = client.CloseContinuation(cleanup, cursor)
		}
		return err
	}
	return a.uploadElasticInspectionResult(leaseCtx, job.ID, result)
}
