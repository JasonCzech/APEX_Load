package collectoragent

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/prometheus"
	"time"
)

type InspectionJob struct {
	Provider           string                `json:"provider,omitempty"`
	Prometheus         *prometheus.JobConfig `json:"prometheus,omitempty"`
	Selector           string                `json:"selector,omitempty"`
	Label              string                `json:"label,omitempty"`
	FromUTC            time.Time             `json:"from_utc,omitempty"`
	ToUTC              time.Time             `json:"to_utc,omitempty"`
	ID                 string                `json:"id"`
	ProjectID          string                `json:"project_id"`
	ConnectionID       string                `json:"connection_id"`
	ConnectionRevision int64                 `json:"connection_revision"`
	CollectorID        string                `json:"collector_id"`
	Kind               string                `json:"kind"`
	ApplicationID      string                `json:"application_id,omitempty"`
	EntityKind         string                `json:"entity_kind,omitempty"`
	Path               string                `json:"path,omitempty"`
	Credentials        *SealedCredentials    `json:"credentials"`
	OwnerToken         string                `json:"owner_token"`
	LeaseExpiresAt     time.Time             `json:"lease_expires_at"`
}
type SignedInspectionJob struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}
type InspectionResult struct {
	Entities               []monitorprovider.Entity            `json:"entities,omitempty"`
	PrometheusCapabilities *prometheus.Capabilities            `json:"prometheus_capabilities,omitempty"`
	Points                 []analysis.MetricPoint              `json:"points,omitempty"`
	Coverage               map[string]monitorprovider.Coverage `json:"coverage,omitempty"`
	Job                    SignedInspectionJob                 `json:"job"`
	Message                string                              `json:"message,omitempty"`
	Truncated              bool                                `json:"truncated,omitempty"`
}

func SignInspectionJob(job InspectionJob, key ed25519.PrivateKey) (SignedInspectionJob, error) {
	if job.Prometheus == nil || job.Provider != monitorprovider.ProviderPrometheus {
		return SignedInspectionJob{}, errors.New("AppDynamics monitoring is retired")
	}
	if len(key) != ed25519.PrivateKeySize {
		return SignedInspectionJob{}, errors.New("inspection signing key unavailable")
	}
	body, err := json.Marshal(job)
	if err != nil || len(body) > 96<<10 {
		return SignedInspectionJob{}, errors.New("inspection job exceeds limit")
	}
	return SignedInspectionJob{Payload: string(body), Signature: base64.RawStdEncoding.EncodeToString(ed25519.Sign(key, body))}, nil
}
func VerifyInspectionJob(envelope SignedInspectionJob, key ed25519.PublicKey, collector string, now time.Time) (InspectionJob, error) {
	var job InspectionJob
	sig, err := base64.RawStdEncoding.DecodeString(envelope.Signature)
	if err != nil || len(key) != ed25519.PublicKeySize || len(envelope.Payload) > 96<<10 || !ed25519.Verify(key, []byte(envelope.Payload), sig) {
		return job, errors.New("inspection signature rejected")
	}
	if json.Unmarshal([]byte(envelope.Payload), &job) != nil || job.ID == "" || job.ProjectID == "" || job.ConnectionID == "" || job.ConnectionRevision < 1 || job.CollectorID != collector || job.OwnerToken == "" || !job.LeaseExpiresAt.After(now) || job.LeaseExpiresAt.After(now.Add(2*time.Minute)) || (job.Kind != "test" && job.Kind != "discover" && job.Kind != "preview") || job.Prometheus == nil || (job.Prometheus != nil && (job.Provider != monitorprovider.ProviderPrometheus || job.Prometheus.Config.Validate() != nil)) {
		return InspectionJob{}, errors.New("inspection identity or lease rejected")
	}
	if job.Prometheus == nil && job.Kind == "preview" {
		return InspectionJob{}, errors.New("preview requires Prometheus assignment")
	}
	if job.Prometheus != nil && job.Kind == "preview" && (len(job.Prometheus.Selection.Queries) == 0 || !job.ToUTC.After(job.FromUTC) || job.ToUTC.Sub(job.FromUTC) > 10*time.Minute || job.ToUTC.After(now.Add(30*time.Second))) {
		return InspectionJob{}, errors.New("invalid preview window")
	}
	if job.Prometheus != nil && len(job.Prometheus.Selection.Queries) > 0 && (job.Prometheus.Selection.ProjectID != job.ProjectID || job.Prometheus.Selection.Validate() != nil) {
		return InspectionJob{}, errors.New("Prometheus inspection selection rejected")
	}
	return job, nil
}
func inspectionCredentialJob(job InspectionJob) RunJob {
	if job.Prometheus != nil {
		return RunJob{ID: "inspection:" + job.ID, ProjectID: job.ProjectID, CollectorID: job.CollectorID, SourceID: job.ConnectionID, SourceRevision: job.ConnectionRevision, Pack: "prometheus-inspection", Destination: job.Prometheus.Config.Endpoint, LeaseExpiresAt: job.LeaseExpiresAt, Credentials: job.Credentials}
	}
	return RunJob{}
}
func SealInspectionCredentials(job InspectionJob, key []byte, c Credentials) (*SealedCredentials, error) {
	return SealCredentials(inspectionCredentialJob(job), key, c)
}
func (a *Agent) dispatchProviderJob(ctx context.Context, job RunJob, envelope SignedRunJob) {
	// Caller holds stateMu. The bounded worker set keeps provider requests away
	// from availability heartbeats and unrelated monitor packs.
	if a.providerBusy == nil {
		a.providerBusy = map[string]bool{}
	}
	if a.providerBusy[job.ID] {
		return
	}
	if a.pendingBatches[job.ID] == nil && len(a.providerBacklog[job.ID]) == 0 && time.Now().Before(a.nextCollection[job.ID]) {
		return
	}
	select {
	case a.providerSlots <- struct{}{}:
	default:
		return
	}
	a.providerBusy[job.ID] = true
	a.providerWorkers.Add(1)
	go func() {
		defer a.providerWorkers.Done()
		defer func() { <-a.providerSlots; a.stateMu.Lock(); delete(a.providerBusy, job.ID); a.stateMu.Unlock() }()
		if job.Prometheus != nil {
			_ = a.collectPrometheusJob(ctx, job, envelope)
		} else if job.Pack == monitorprovider.PackDynatrace {
			_ = a.collectDynatraceJob(ctx, job, envelope)
		}
	}()
}
func (a *Agent) prometheusInspectionTick(ctx context.Context) error {
	capacity := cap(a.providerSlots) - len(a.providerSlots)
	if capacity < 1 {
		return nil
	}
	var jobs []SignedInspectionJob
	if err := a.jobRequest(ctx, "GET", fmt.Sprintf("inspections?capacity=%d", capacity), nil, &jobs); err != nil {
		return err
	}
	if len(jobs) > capacity {
		return errors.New("too many inspection jobs")
	}
	for _, envelope := range jobs {
		job, err := VerifyInspectionJob(envelope, a.cfg.ControllerPublicKey, a.cfg.CollectorID, time.Now().UTC())
		if err != nil {
			return err
		}
		a.stateMu.Lock()
		key := "inspection:" + job.ID
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
			_ = a.executePrometheusInspection(ctx, job, envelope)
		}()
	}
	return nil
}
