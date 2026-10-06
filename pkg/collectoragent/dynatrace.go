package collectoragent

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/dynatrace"
)

type DynatraceCredentials struct {
	EnvironmentToken string `json:"environment_token,omitempty"`
	PlatformToken    string `json:"platform_token,omitempty"`
	ClientID         string `json:"client_id,omitempty"`
	ClientSecret     string `json:"client_secret,omitempty"`
	VersionIdentity  string `json:"version_identity,omitempty"`
}

func DynatraceCredentialBundle(c dynatrace.Credentials) Credentials {
	return Credentials{Dynatrace: &DynatraceCredentials{EnvironmentToken: c.EnvironmentToken, PlatformToken: c.PlatformToken, ClientID: c.ClientID, ClientSecret: c.ClientSecret, VersionIdentity: c.VersionIdentity}}
}
func (c *DynatraceCredentials) Runtime() dynatrace.Credentials {
	if c == nil {
		return dynatrace.Credentials{}
	}
	return dynatrace.Credentials{EnvironmentToken: c.EnvironmentToken, PlatformToken: c.PlatformToken, ClientID: c.ClientID, ClientSecret: c.ClientSecret, VersionIdentity: c.VersionIdentity}
}

type DynatraceInspectionJob struct {
	ID                 string                     `json:"id"`
	ProjectID          string                     `json:"project_id"`
	ConnectionID       string                     `json:"connection_id"`
	ConnectionRevision int64                      `json:"connection_revision"`
	CollectorID        string                     `json:"collector_id"`
	Kind               string                     `json:"kind"`
	Config             dynatrace.Config           `json:"config"`
	Selection          *dynatrace.Selection       `json:"selection,omitempty"`
	Discovery          dynatrace.DiscoveryRequest `json:"discovery,omitempty"`
	PreviewFromUTC     time.Time                  `json:"preview_from_utc,omitempty"`
	PreviewUntilUTC    time.Time                  `json:"preview_until_utc,omitempty"`
	Credentials        *SealedCredentials         `json:"credentials"`
	OwnerToken         string                     `json:"owner_token"`
	LeaseExpiresAt     time.Time                  `json:"lease_expires_at"`
}
type SignedDynatraceInspectionJob struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}
type DynatraceInspectionResult struct {
	Job          SignedDynatraceInspectionJob `json:"job"`
	Capabilities dynatrace.Capabilities       `json:"capabilities"`
	Discovery    dynatrace.DiscoveryResult    `json:"discovery"`
	Preview      *dynatrace.Result            `json:"preview,omitempty"`
	Message      string                       `json:"message,omitempty"`
}
type DynatraceScanReservationRequest struct {
	Job       SignedRunJob `json:"job"`
	AttemptID string       `json:"attempt_id"`
	Bytes     int64        `json:"bytes"`
}

func SignDynatraceInspectionJob(job DynatraceInspectionJob, key ed25519.PrivateKey) (SignedDynatraceInspectionJob, error) {
	if len(key) != ed25519.PrivateKeySize {
		return SignedDynatraceInspectionJob{}, errors.New("inspection signing key unavailable")
	}
	body, err := json.Marshal(job)
	if err != nil || len(body) > 96<<10 {
		return SignedDynatraceInspectionJob{}, errors.New("inspection job exceeds bound")
	}
	return SignedDynatraceInspectionJob{Payload: string(body), Signature: base64.RawStdEncoding.EncodeToString(ed25519.Sign(key, body))}, nil
}
func VerifyDynatraceInspectionJob(envelope SignedDynatraceInspectionJob, key ed25519.PublicKey, collector string, now time.Time) (DynatraceInspectionJob, error) {
	var job DynatraceInspectionJob
	sig, err := base64.RawStdEncoding.DecodeString(envelope.Signature)
	if err != nil || len(key) != ed25519.PublicKeySize || len(envelope.Payload) > 96<<10 || len(envelope.Signature) > 128 || !ed25519.Verify(key, []byte(envelope.Payload), sig) {
		return job, errors.New("Dynatrace inspection signature rejected")
	}
	if json.Unmarshal([]byte(envelope.Payload), &job) != nil || job.ID == "" || job.ProjectID == "" || job.ConnectionID == "" || job.ConnectionRevision < 1 || job.CollectorID != collector || job.OwnerToken == "" || !job.LeaseExpiresAt.After(now) || job.LeaseExpiresAt.After(now.Add(2*time.Minute)) || job.Config.Validate() != nil || (job.Kind != "verify" && job.Kind != "discover" && job.Kind != "preview") {
		return DynatraceInspectionJob{}, errors.New("Dynatrace inspection identity rejected")
	}
	if job.Selection != nil && (job.Selection.ProjectID != job.ProjectID || job.Selection.Validate() != nil) {
		return DynatraceInspectionJob{}, errors.New("Dynatrace inspection scope rejected")
	}
	if job.Kind == "preview" && job.Selection == nil {
		return DynatraceInspectionJob{}, errors.New("preview requires frozen selection")
	}
	if job.Kind == "preview" && (job.PreviewFromUTC.Location() != time.UTC || job.PreviewUntilUTC.Location() != time.UTC || job.PreviewFromUTC.IsZero() || !job.PreviewUntilUTC.After(job.PreviewFromUTC) || job.PreviewUntilUTC.Sub(job.PreviewFromUTC) > time.Minute || job.PreviewUntilUTC.After(now.Add(time.Second))) {
		return DynatraceInspectionJob{}, errors.New("preview requires a bounded frozen UTC window")
	}
	return job, nil
}
func dynatraceInspectionCredentialJob(job DynatraceInspectionJob) RunJob {
	destination := job.Config.EnvironmentURL
	if destination == "" {
		destination = job.Config.PlatformURL
	}
	return RunJob{ID: "dynatrace-inspection:" + job.ID, ProjectID: job.ProjectID, CollectorID: job.CollectorID, SourceID: job.ConnectionID, SourceRevision: job.ConnectionRevision, Pack: "dynatrace-inspection", Destination: destination, LeaseExpiresAt: job.LeaseExpiresAt, Credentials: job.Credentials, Dynatrace: &dynatrace.JobConfig{Config: job.Config}}
}
func SealDynatraceInspectionCredentials(job DynatraceInspectionJob, key []byte, c Credentials) (*SealedCredentials, error) {
	return SealCredentials(dynatraceInspectionCredentialJob(job), key, c)
}
func DynatraceRequiredOrigins(cfg dynatrace.Config) []string {
	origins := []string{}
	if cfg.EnvironmentURL != "" {
		origins = append(origins, cfg.EnvironmentURL)
	}
	if cfg.PlatformURL != "" {
		origins = append(origins, cfg.PlatformURL)
		if cfg.Defaults().PlatformAuthType == dynatrace.AuthOAuth {
			origins = append(origins, "https://sso.dynatrace.com")
		}
	}
	return origins
}
func (a *Agent) dynatraceTarget(job RunJob, cfg dynatrace.Config) (AdapterTarget, error) {
	target, ok := a.adapterTarget(job)
	if !ok {
		return target, errors.New("Dynatrace destination is not locally approved")
	}
	for _, origin := range DynatraceRequiredOrigins(cfg) {
		if origin != target.Destination && !slices.Contains(target.DynatraceOrigins, origin) {
			return target, errors.New("Dynatrace credential origin is not locally approved")
		}
	}
	return target, nil
}
func (a *Agent) dispatchDynatraceJob(ctx context.Context, job RunJob, envelope SignedRunJob) {
	a.dispatchProviderJob(ctx, job, envelope)
}
func (a *Agent) collectDynatraceJob(ctx context.Context, job RunJob, envelope SignedRunJob) error {
	return a.collectProviderJob(ctx, job, envelope, func(ctx context.Context, from, to time.Time) (providerResult, error) {
		result, err := a.readDynatraceJob(ctx, job, envelope)
		return providerResult{Points: result.Points, Evidence: result.Evidence, Coverage: result.Coverage, Topology: result.Topology, Progress: result.Progress}, err
	})
}
func (a *Agent) readDynatraceJob(ctx context.Context, job RunJob, envelope SignedRunJob) (providerResult, error) {
	if job.Dynatrace == nil || job.Dynatrace.Validate() != nil {
		return providerResult{}, errors.New("invalid Dynatrace job")
	}
	if _, err := a.dynatraceTarget(job, job.Dynatrace.Config); err != nil {
		return providerResult{}, err
	}
	credentials, err := OpenCredentials(job, a.cfg.CredentialPrivateKey, time.Now().UTC())
	if err != nil || credentials.Dynatrace == nil {
		return providerResult{}, errors.New("Dynatrace credential lease unavailable")
	}
	client, err := a.newDynatraceClient(job.Dynatrace.Config, credentials.Dynatrace.Runtime())
	if err != nil {
		return providerResult{}, err
	}
	from, to := providerQueryWindow(job, time.Now().UTC())
	leaseCtx, cancel := context.WithDeadline(ctx, job.LeaseExpiresAt)
	defer cancel()
	client.SetScanHook(func(attempt string, bytes int64) error {
		if err := a.jobRequest(leaseCtx, "POST", "jobs/"+url.PathEscape(job.ID)+"/scan-reservations", DynatraceScanReservationRequest{Job: envelope, AttemptID: attempt, Bytes: bytes}, nil); err != nil {
			return errors.Join(dynatrace.ErrBudget, err)
		}
		return nil
	})
	result, err := client.Collect(leaseCtx, job.Dynatrace.Selection, from, to, job.RunID)
	a.stateMu.Lock()
	if current, ok := a.dynatraceJobs[job.ID]; ok && current.Dynatrace != nil {
		copyConfig := *current.Dynatrace
		copyConfig.Selection.Cursor = result.Progress.Cursor
		copyConfig.Selection.QueryOffset = result.Progress.NextQuery
		current.Dynatrace = &copyConfig
		a.dynatraceJobs[job.ID] = current
	}
	a.stateMu.Unlock()
	return providerResult{Points: result.Points, Evidence: result.Evidence, Coverage: result.Coverage, Topology: result.Topology, Progress: result.Progress}, err
}
func (a *Agent) newDynatraceClient(cfg dynatrace.Config, credentials dynatrace.Credentials) (*dynatrace.Client, error) {
	if a.dynatraceClientFactory != nil {
		return a.dynatraceClientFactory(cfg, credentials)
	}
	return dynatrace.NewClient(cfg, credentials)
}

// Caller holds stateMu. A locally persisted pending token is newer than the
// last accepted controller cursor and needs cancellation on shutdown too.
func (a *Agent) dynatraceCancellationJob(job RunJob) RunJob {
	if job.Dynatrace == nil {
		return job
	}
	for _, batch := range append([]*RunSampleBatch{a.pendingBatches[job.ID]}, a.providerBacklog[job.ID]...) {
		if batch != nil && batch.Progress != nil && batch.Progress.Cursor != "" {
			cfg := *job.Dynatrace
			cfg.Selection.Cursor = batch.Progress.Cursor
			job.Dynatrace = &cfg
		}
	}
	return job
}
func (a *Agent) cancelDynatraceJob(ctx context.Context, job RunJob) error {
	if job.Dynatrace == nil || job.Dynatrace.Selection.Cursor == "" {
		return nil
	}
	if _, err := a.dynatraceTarget(job, job.Dynatrace.Config); err != nil {
		return err
	}
	credentials, err := OpenCredentials(job, a.cfg.CredentialPrivateKey, time.Now().UTC())
	if err != nil || credentials.Dynatrace == nil {
		return errors.New("cancellation credential lease unavailable")
	}
	client, err := a.newDynatraceClient(job.Dynatrace.Config, credentials.Dynatrace.Runtime())
	if err != nil {
		return err
	}
	return client.Cancel(ctx, job.Dynatrace.Selection.Cursor)
}
func (a *Agent) dispatchDynatraceCancellation(ctx context.Context, job RunJob) {
	if a.providerClosing {
		return
	}
	select {
	case a.dynatraceCancelSlots <- struct{}{}:
	default:
		return
	}
	a.providerWorkers.Add(1)
	go func() {
		defer a.providerWorkers.Done()
		defer func() { <-a.dynatraceCancelSlots }()
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = a.cancelDynatraceJob(cleanup, job)
	}()
}

// Caller holds stateMu. Cleanup fires while the originating sealed lease is
// valid, including when the controller stops returning terminal jobs.
func (a *Agent) rememberDynatraceJob(job RunJob) {
	if a.providerClosing {
		return
	}
	a.dynatraceJobs[job.ID] = job
	if timer := a.dynatraceCancelTimers[job.ID]; timer != nil {
		timer.Stop()
	}
	deadline := job.LeaseExpiresAt
	if job.DrainDeadline != nil && job.DrainDeadline.Before(deadline) {
		deadline = *job.DrainDeadline
	}
	if job.ExpiresAt.Before(deadline) {
		deadline = job.ExpiresAt
	}
	at := deadline.Add(-time.Second)
	lease := job.LeaseExpiresAt
	a.dynatraceCancelTimers[job.ID] = time.AfterFunc(max(0, time.Until(at)), func() {
		a.stateMu.Lock()
		defer a.stateMu.Unlock()
		current, ok := a.dynatraceJobs[job.ID]
		if !ok || a.providerClosing || !current.LeaseExpiresAt.Equal(lease) {
			return
		}
		a.dispatchDynatraceCancellation(context.Background(), a.dynatraceCancellationJob(current))
		delete(a.dynatraceCancelTimers, job.ID)
	})
}
func (a *Agent) stopDynatraceTimers() {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	a.providerClosing = true
	for id, timer := range a.dynatraceCancelTimers {
		timer.Stop()
		delete(a.dynatraceCancelTimers, id)
	}
}
func (a *Agent) cancelDynatraceJobs() {
	a.stateMu.Lock()
	jobs := make([]RunJob, 0, len(a.dynatraceJobs))
	for _, job := range a.dynatraceJobs {
		jobs = append(jobs, a.dynatraceCancellationJob(job))
	}
	a.stateMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	queue := make(chan RunJob, len(jobs))
	for _, job := range jobs {
		queue <- job
	}
	close(queue)
	var workers sync.WaitGroup
	for range min(4, len(jobs)) {
		workers.Go(func() {
			for job := range queue {
				if ctx.Err() != nil {
					return
				}
				_ = a.cancelDynatraceJob(ctx, job)
			}
		})
	}
	workers.Wait()
}
func (a *Agent) dynatraceInspectionTick(ctx context.Context) error {
	capacity := cap(a.providerSlots) - len(a.providerSlots)
	if capacity < 1 {
		return nil
	}
	var jobs []SignedDynatraceInspectionJob
	if err := a.jobRequest(ctx, "GET", fmt.Sprintf("dynatrace-operations?capacity=%d", capacity), nil, &jobs); err != nil {
		return err
	}
	if len(jobs) > capacity {
		return errors.New("too many Dynatrace inspection jobs")
	}
	for _, envelope := range jobs {
		job, err := VerifyDynatraceInspectionJob(envelope, a.cfg.ControllerPublicKey, a.cfg.CollectorID, time.Now().UTC())
		if err != nil {
			return err
		}
		a.stateMu.Lock()
		key := "dynatrace-inspection:" + job.ID
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
			_ = a.executeDynatraceInspection(ctx, job, envelope)
		}()
	}
	return nil
}
func (a *Agent) executeDynatraceInspection(ctx context.Context, job DynatraceInspectionJob, envelope SignedDynatraceInspectionJob) error {
	credentialJob := dynatraceInspectionCredentialJob(job)
	credentialJob.Pack = "dynatrace"
	if _, err := a.dynatraceTarget(credentialJob, job.Config); err != nil {
		return err
	}
	credentials, err := OpenCredentials(dynatraceInspectionCredentialJob(job), a.cfg.CredentialPrivateKey, time.Now().UTC())
	if err != nil || credentials.Dynatrace == nil {
		return errors.New("inspection credential unavailable")
	}
	client, err := a.newDynatraceClient(job.Config, credentials.Dynatrace.Runtime())
	if err != nil {
		return err
	}
	leaseCtx, cancel := context.WithDeadline(ctx, job.LeaseExpiresAt)
	defer cancel()
	result := DynatraceInspectionResult{Job: envelope}
	switch job.Kind {
	case "verify":
		if job.Selection != nil {
			result.Capabilities, err = client.TestSelection(leaseCtx, *job.Selection)
		} else {
			result.Capabilities, err = client.Test(leaseCtx)
		}
	case "discover":
		result.Discovery, err = client.Discover(leaseCtx, job.Discovery)
	case "preview":
		var preview dynatrace.Result
		preview, err = client.Preview(leaseCtx, *job.Selection, job.PreviewFromUTC, job.PreviewUntilUTC)
		preview.Progress.Cursor = ""
		preview.Progress.ScanReservationIDs = nil
		preview.Progress.ScanSettlements = nil
		preview.Progress.CursorExpiresAt = nil
		if err == nil {
			result.Preview = &preview
		}
	}
	if err != nil {
		result.Message = "Dynatrace inspection failed"
	}
	if encoded, e := json.Marshal(result); e != nil || len(encoded) > 1<<20 {
		return errors.New("inspection result exceeds transport bound")
	}
	return a.jobRequest(leaseCtx, "POST", "dynatrace-operations/"+url.PathEscape(job.ID)+"/result", result, nil)
}
func slicesContainsDynatrace(packs []string) bool { return slices.Contains(packs, "dynatrace") }
