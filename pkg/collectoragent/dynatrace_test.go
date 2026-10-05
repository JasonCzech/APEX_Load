package collectoragent

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/appdynamics"
	"github.com/jasonczech/apexload/pkg/dynatrace"
)

func dynatraceFixtureConfig() dynatrace.Config {
	return dynatrace.Config{EnvironmentURL: "https://sample.live.dynatrace.com", PlatformURL: "https://sample.apps.dynatrace.com", PlatformAuthType: dynatrace.AuthOAuth, OAuthResource: "urn:dtaccount:fixture", OAuthScopes: []string{"storage:logs:read", "storage:metrics:read"}}
}

func TestTypedProviderUnitsMatchDirectCollectionWithoutPartialScaling(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for _, unit := range []string{"%", "us", "count/5min", "bytes/s"} {
		t.Run(unit, func(t *testing.T) {
			value, sum, min, max := 20.0, 100.0, 10.0, 30.0
			point := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: "p", RunID: "run", Source: "source", TimestampUTC: start, MetricName: "metric", Kind: analysis.KindGauge, Value: &value, Unit: unit, Quality: analysis.QualityValid, Provider: &analysis.ProviderMeasurement{Name: "dynatrace", Backend: "classic", MetricID: "metrics:stable", BucketStart: start, BucketEnd: start.Add(time.Minute), ResolutionSeconds: 60, Statistic: "value", Sum: &sum, Min: &min, Max: &max}}
			if err := normalizeAdapterPoint(&point); err != nil {
				t.Fatal(err)
			}
			if point.Unit != unit || *point.Value != value || *point.Provider.Sum != sum || *point.Provider.Min != min || *point.Provider.Max != max {
				t.Fatal("collector changed frozen provider semantics", point)
			}
		})
	}
	point := analysis.MetricPoint{Unit: "us", Value: new(2000.0)}
	if err := normalizeAdapterPoint(&point); err != nil || point.Unit != "ms" || *point.Value != 2 {
		t.Fatal("existing untyped pack normalization changed", point, err)
	}
}

func TestDynatracePreviewResumesThenStripsPrivateContinuation(t *testing.T) {
	decrypt, _ := ecdh.X25519().GenerateKey(rand.Reader)
	now := time.Now().UTC()
	cfg := dynatrace.Config{PlatformURL: "https://sample.apps.dynatrace.com", PlatformAuthType: dynatrace.AuthToken}
	selection := dynatrace.Selection{ProjectID: "p", Grail: []dynatrace.GrailQuery{{Name: "logs", Kind: "log", Query: "fetch logs"}}}
	job := DynatraceInspectionJob{ID: "preview", ProjectID: "p", ConnectionID: "connection", ConnectionRevision: 1, CollectorID: "collector", Kind: "preview", Config: cfg, Selection: &selection, PreviewFromUTC: now.Add(-time.Minute), PreviewUntilUTC: now, OwnerToken: "owner", LeaseExpiresAt: now.Add(time.Minute)}
	var err error
	job.Credentials, err = SealDynatraceInspectionCredentials(job, decrypt.PublicKey().Bytes(), DynatraceCredentialBundle(dynatrace.Credentials{PlatformToken: "token", VersionIdentity: "v1"}))
	if err != nil {
		t.Fatal(err)
	}
	var submitted DynatraceInspectionResult
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/dynatrace-operations/preview/result") {
			t.Error("result route", r.URL)
		}
		if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
			t.Error(err)
		}
	}))
	defer controller.Close()
	a, err := New(Config{ControllerURL: controller.URL, CollectorID: "collector", Token: "token", CredentialPrivateKey: decrypt.Bytes(), AdapterTargets: []AdapterTarget{{Pack: "dynatrace", Destination: cfg.PlatformURL, DynatraceOrigins: []string{cfg.PlatformURL}}}})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, polled := 0, 0
	a.dynatraceClientFactory = func(cfg dynatrace.Config, credentials dynatrace.Credentials) (*dynatrace.Client, error) {
		return dynatrace.NewClientWithHTTPClient(cfg, credentials, &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
			body := `{"state":"RUNNING","requestToken":"preview-pending","ttlSeconds":60}`
			if strings.HasSuffix(r.URL.Path, "query:cancel") {
				cancelled++
				body = ""
			} else if strings.HasSuffix(r.URL.Path, "query:poll") {
				polled++
				body = `{"state":"SUCCEEDED","result":{"records":[],"metadata":{"grail":{"scannedBytes":0}}}}`
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		})})
	}
	if err = a.executeDynatraceInspection(t.Context(), job, SignedDynatraceInspectionJob{}); err != nil || cancelled != 0 || polled != 1 || submitted.Preview == nil || !submitted.Preview.Progress.Complete || submitted.Preview.Progress.Cursor != "" || len(submitted.Preview.Progress.ScanReservationIDs) != 0 {
		t.Fatal("preview abandoned or disclosed private token", submitted, err, cancelled)
	}
}

func TestDynatraceCollectorAdmitsScanBeforeExecuteAndCancelsWithSealedPrincipal(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		t.Run(map[bool]string{false: "denied", true: "admitted"}[allowed], func(t *testing.T) {
			public, private, _ := ed25519.GenerateKey(rand.Reader)
			decrypt, _ := ecdh.X25519().GenerateKey(rand.Reader)
			now := time.Now().UTC()
			cfg := dynatrace.Config{PlatformURL: "https://sample.apps.dynatrace.com", PlatformAuthType: dynatrace.AuthToken}
			job := RunJob{ID: "job", ProjectID: "p", RunID: "run", SourceID: "source", SourceRevision: 1, CollectorID: "collector", Pack: "dynatrace", Destination: cfg.PlatformURL, IntervalSeconds: 60, StartedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), LeaseExpiresAt: now.Add(time.Minute), QueryFromUTC: now.Add(-time.Minute), QueryUntilUTC: now, Dynatrace: &dynatrace.JobConfig{Config: cfg, Selection: dynatrace.Selection{ProjectID: "p", Grail: []dynatrace.GrailQuery{{Name: "logs", Kind: "log", Query: "fetch logs"}}}}}
			var err error
			job.Credentials, err = SealCredentials(job, decrypt.PublicKey().Bytes(), DynatraceCredentialBundle(dynatrace.Credentials{PlatformToken: "private-token-secret", VersionIdentity: "frozen-version"}))
			if err != nil {
				t.Fatal(err)
			}
			envelope, _ := SignRunJob(job, private)
			var admitted, executed, cancelled atomic.Int32
			controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/monitoring/collectors/collector/jobs/job/scan-reservations" || r.Method != http.MethodPost {
					t.Error("admission route", r.URL, r.Method)
				}
				var request DynatraceScanReservationRequest
				if json.NewDecoder(r.Body).Decode(&request) != nil || request.Job.Payload != envelope.Payload || request.Bytes != 1_000_000_000 || len(request.AttemptID) != 32 {
					t.Error("scan was not signed, bounded or unique", request)
				}
				if _, err := VerifyRunJob(request.Job, public, "collector", now); err != nil {
					t.Error(err)
				}
				if !allowed {
					w.WriteHeader(429)
					return
				}
				admitted.Add(1)
			}))
			defer controller.Close()
			a, err := New(Config{ControllerURL: controller.URL, CollectorID: "collector", Token: "collector-token", ControllerPublicKey: public, CredentialPrivateKey: decrypt.Bytes(), Packs: []string{"dynatrace"}, AdapterTargets: []AdapterTarget{{Pack: "dynatrace", Destination: cfg.PlatformURL, DynatraceOrigins: []string{cfg.PlatformURL}}}})
			if err != nil {
				t.Fatal(err)
			}
			defer a.stopDynatraceTimers()
			a.dynatraceClientFactory = func(cfg dynatrace.Config, credentials dynatrace.Credentials) (*dynatrace.Client, error) {
				return dynatrace.NewClientWithHTTPClient(cfg, credentials, &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
					if r.Header.Get("Authorization") != "Bearer private-token-secret" {
						t.Error("sealed principal changed")
					}
					body := `{"state":"RUNNING","requestToken":"upstream-opaque-token","ttlSeconds":60}`
					if strings.HasSuffix(r.URL.Path, "query:cancel") {
						cancelled.Add(1)
						body = ""
					} else {
						executed.Add(1)
						if admitted.Load() != 1 {
							t.Error("upstream query bypassed durable scan admission")
						}
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
				})})
			}
			a.dynatraceJobs[job.ID] = job
			result, err := a.readDynatraceJob(t.Context(), job, envelope)
			if err != nil {
				t.Fatal(err)
			}
			if !allowed {
				if executed.Load() != 0 || result.Coverage["grail:logs"].Status != "budget_exhausted" {
					t.Fatal("denied admission reached upstream", result, executed.Load())
				}
				return
			}
			if executed.Load() != 1 || result.Progress.Cursor == "" {
				t.Fatal("token was not retained", result)
			}
			// A signed drain deadline also cleans up without another controller
			// poll. This uses the still-valid originating sealed bundle.
			deadline := time.Now().UTC().Add(time.Second + 25*time.Millisecond)
			a.stateMu.Lock()
			pending := a.dynatraceJobs[job.ID]
			pending.DrainDeadline = &deadline
			a.rememberDynatraceJob(pending)
			a.stateMu.Unlock()
			until := time.Now().Add(time.Second)
			for cancelled.Load() == 0 && time.Now().Before(until) {
				time.Sleep(time.Millisecond)
			}
			if cancelled.Load() != 1 {
				t.Fatal("signed deadline left a known token running", cancelled.Load())
			}
			a.stopDynatraceTimers()
			a.providerWorkers.Wait()
			a.cancelDynatraceJobs()
			if cancelled.Load() != 2 {
				t.Fatal("collector termination did not clean up known handle", cancelled.Load())
			}
		})
	}
}
func TestDynatraceInspectionSignatureAndCredentialOrigins(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	decrypt, _ := ecdh.X25519().GenerateKey(rand.Reader)
	now := time.Now().UTC()
	job := DynatraceInspectionJob{ID: "inspect", ProjectID: "p", ConnectionID: "connection", ConnectionRevision: 3, CollectorID: "collector", Kind: "verify", OwnerToken: "owner", LeaseExpiresAt: now.Add(time.Minute), Config: dynatraceFixtureConfig()}
	var err error
	job.Credentials, err = SealDynatraceInspectionCredentials(job, decrypt.PublicKey().Bytes(), DynatraceCredentialBundle(dynatrace.Credentials{EnvironmentToken: "environment-secret", ClientID: "client", ClientSecret: "platform-secret", VersionIdentity: "version-3"}))
	if err != nil {
		t.Fatal(err)
	}
	signed, err := SignDynatraceInspectionJob(job, private)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(signed.Payload, "platform-secret") || strings.Contains(signed.Payload, "environment-secret") || strings.Contains(signed.Payload, `"run_id"`) {
		t.Fatal("inspection disclosed credentials or fabricated run")
	}
	checked, err := VerifyDynatraceInspectionJob(signed, public, "collector", now)
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := OpenCredentials(dynatraceInspectionCredentialJob(checked), decrypt.Bytes(), now)
	if err != nil || credentials.Dynatrace.Runtime().ClientSecret != "platform-secret" {
		t.Fatal("sealed bundle", err)
	}
	for name, mutate := range map[string]func(*DynatraceInspectionJob){
		"environment": func(j *DynatraceInspectionJob) { j.Config.EnvironmentURL = "https://other.live.dynatrace.com" },
		"platform":    func(j *DynatraceInspectionJob) { j.Config.PlatformURL = "https://other.apps.dynatrace.com" },
		"resource":    func(j *DynatraceInspectionJob) { j.Config.OAuthResource = "urn:dtaccount:other" },
		"scopes":      func(j *DynatraceInspectionJob) { j.Config.OAuthScopes = []string{"storage:spans:read"} },
		"revision":    func(j *DynatraceInspectionJob) { j.ConnectionRevision++ },
	} {
		t.Run(name, func(t *testing.T) {
			changed := job
			mutate(&changed)
			if _, err := OpenCredentials(dynatraceInspectionCredentialJob(changed), decrypt.Bytes(), now); err == nil {
				t.Fatal("sealed credential identity substitution accepted")
			}
		})
	}
	tampered := signed
	tampered.Payload = strings.Replace(tampered.Payload, "connection", "changed", 1)
	if _, err = VerifyDynatraceInspectionJob(tampered, public, "collector", now); err == nil {
		t.Fatal("tampered grant accepted")
	}
	if _, err = VerifyDynatraceInspectionJob(signed, public, "other", now); err == nil {
		t.Fatal("wrong collector accepted")
	}
	if _, err = VerifyDynatraceInspectionJob(signed, public, "collector", now.Add(time.Minute)); err == nil {
		t.Fatal("expired grant accepted")
	}
	for _, kind := range []string{"discover", "preview", "execute", "test"} {
		modified := job
		modified.Kind = kind
		if kind == "preview" {
			modified.Selection = &dynatrace.Selection{ProjectID: "p", Problems: true}
			modified.PreviewFromUTC, modified.PreviewUntilUTC = now.Add(-time.Minute), now
		}
		envelope, _ := SignDynatraceInspectionJob(modified, private)
		_, err := VerifyDynatraceInspectionJob(envelope, public, "collector", now)
		if (kind == "discover" || kind == "preview") != (err == nil) {
			t.Errorf("kind %s: %v", kind, err)
		}
	}
}

func TestDynatraceLocalOriginsEncryptionAndReadinessContract(t *testing.T) {
	public, _, _ := ed25519.GenerateKey(rand.Reader)
	decrypt, _ := ecdh.X25519().GenerateKey(rand.Reader)
	cfg := dynatraceFixtureConfig()
	local := AdapterTarget{Pack: "dynatrace", Destination: cfg.EnvironmentURL, DynatraceOrigins: []string{cfg.PlatformURL, "https://sso.dynatrace.com"}}
	a, err := New(Config{ControllerURL: "http://localhost:8080", CollectorID: "collector", Token: "token", ControllerPublicKey: public, CredentialPrivateKey: decrypt.Bytes(), Packs: []string{"dynatrace"}, AdapterTargets: []AdapterTarget{local}})
	if err != nil {
		t.Fatal(err)
	}
	job := RunJob{Pack: "dynatrace", Destination: cfg.EnvironmentURL}
	if _, err = a.dynatraceTarget(job, cfg); err != nil {
		t.Fatal(err)
	}
	// Local approval is owned by the agent and cannot be changed through the
	// caller's configuration slice or a controller grant.
	local.DynatraceOrigins[0] = "https://other.apps.dynatrace.com"
	if _, err = a.dynatraceTarget(job, cfg); err != nil {
		t.Fatal("approval alias mutated", err)
	}
	cfg.PlatformURL = "https://other.apps.dynatrace.com"
	if _, err = a.dynatraceTarget(job, cfg); err == nil {
		t.Fatal("unapproved platform origin accepted")
	}
	cfg = dynatraceFixtureConfig()
	a.cfg.AdapterTargets[0].DynatraceOrigins = []string{cfg.PlatformURL}
	if _, err = a.dynatraceTarget(job, cfg); err == nil {
		t.Fatal("OAuth SSO origin missing local approval")
	}
	for _, bad := range []AdapterTarget{
		{Pack: "dynatrace", Destination: "https://127.0.0.1", DynatraceOrigins: []string{cfg.PlatformURL}},
		{Pack: "dynatrace", Destination: cfg.EnvironmentURL, DynatraceOrigins: []string{"https://sample.apps.dynatrace.com.evil.test"}},
	} {
		if validateAdapterTargets(Config{AdapterTargets: []AdapterTarget{bad}}) == nil {
			t.Fatal("unsafe SaaS origin approved")
		}
	}
	if _, err = New(Config{ControllerURL: "http://localhost:8080", CollectorID: "collector", Token: "token", ControllerPublicKey: public, Packs: []string{"dynatrace"}, AdapterTargets: []AdapterTarget{local}}); err == nil {
		t.Fatal("unenrolled collector accepted")
	}
	var readiness RunJobReadiness
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/monitoring/collectors/collector/jobs" {
			t.Error(r.URL.Path)
		}
		if json.Unmarshal([]byte(r.Header.Get("X-APEX-Monitor-Readiness")), &readiness) != nil {
			t.Error("readiness missing")
		}
		json.NewEncoder(w).Encode([]SignedRunJob{})
	}))
	defer controller.Close()
	a.cfg.ControllerURL = controller.URL
	a.cfg.AdapterTargets[0].DynatraceOrigins = []string{cfg.PlatformURL, "https://sso.dynatrace.com"}
	if err = a.jobRequest(t.Context(), http.MethodGet, "jobs", nil, &[]SignedRunJob{}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(readiness.Contracts, DynatraceBatchContract) || len(readiness.Destinations) != 3 {
		t.Fatal("new batch contract or all origins absent", readiness)
	}
	keySuffix := "#" + base64.StdEncoding.EncodeToString(decrypt.PublicKey().Bytes())
	for _, origin := range DynatraceRequiredOrigins(cfg) {
		if !slices.Contains(readiness.Destinations, origin+keySuffix) {
			t.Fatal("origin not bound to enrolled encryption key", origin)
		}
	}
}

func TestDynatraceSignedRunContractAndDurableContinuation(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().UTC()
	from := now.Add(-time.Minute).Truncate(time.Second)
	job := RunJob{ID: "job", ProjectID: "p", RunID: "run", SourceID: "source", SourceRevision: 1, CollectorID: "collector", Pack: "dynatrace", Destination: "https://sample.live.dynatrace.com", IntervalSeconds: 60, StartedAt: from, ExpiresAt: now.Add(time.Hour), LeaseExpiresAt: now.Add(time.Minute), MaxBatches: 32, QueryFromUTC: from, QueryUntilUTC: now, Dynatrace: &dynatrace.JobConfig{Config: dynatrace.Config{EnvironmentURL: "https://sample.live.dynatrace.com"}, Selection: dynatrace.Selection{ProjectID: "p", Problems: true}}}
	envelope, err := SignRunJob(job, private)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyRunJob(envelope, public, "collector", now); err != nil {
		t.Fatal(err)
	}
	changed := job
	changed.IntervalSeconds = 1
	bad, _ := SignRunJob(changed, private)
	if _, err = VerifyRunJob(bad, public, "collector", now); err == nil {
		t.Fatal("provider minimum polling interval ignored")
	}
	changed = job
	copyConfig := *job.Dynatrace
	changed.Dynatrace = &copyConfig
	changed.Dynatrace.Selection.ProjectID = "other"
	bad, _ = SignRunJob(changed, private)
	if _, err = VerifyRunJob(bad, public, "collector", now); err == nil {
		t.Fatal("provider project isolation ignored")
	}

	var submitted RunSampleBatch
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/jobs/job/samples") {
			t.Error("submission path", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
			t.Error(err)
		}
	}))
	defer controller.Close()
	a, err := New(Config{ControllerURL: controller.URL, CollectorID: "collector", Token: "token", SpoolDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	expiry := now.Add(time.Minute)
	progress := analysis.ProviderQueryProgress{NextQuery: 0, TotalQueries: 1, Cursor: `{"token":"opaque"}`, CursorExpiresAt: &expiry, QueryCostKnown: false, ScanReservationBytes: 1_000_000_000, ScanReservationIDs: []string{"0123456789abcdef0123456789abcdef"}}
	reads := 0
	reader := func(context.Context, time.Time, time.Time) (providerResult, error) {
		reads++
		return providerResult{Progress: progress, Coverage: map[string]appdynamics.Coverage{"problems": {Status: "pending", From: from, To: now}}}, nil
	}
	if err = a.collectProviderJob(t.Context(), job, envelope, reader); err != nil {
		t.Fatal(err)
	}
	if reads != 1 || submitted.Progress == nil || submitted.Progress.Cursor != progress.Cursor || submitted.QueryWindowEnd != nil || submitted.Progress.ScanReservationBytes != progress.ScanReservationBytes || len(submitted.Progress.ScanReservationIDs) != 1 {
		t.Fatal("continuation/scan admission lost", submitted)
	}
	if a.nextCollection[job.ID].After(time.Now()) {
		t.Fatal("continuation waited for normal polling interval")
	}
	loaded, err := New(a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = loaded.loadSpool(); err != nil {
		t.Fatal(err)
	}
}
