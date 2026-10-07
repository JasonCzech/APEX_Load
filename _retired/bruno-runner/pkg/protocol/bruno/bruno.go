// Package bruno implements the canonical Bruno protocol by delegating source
// behavior to the version-pinned APEX Bruno compatibility runner.
package bruno

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const (
	CompatibilityVersion      = "3.5.2"
	defaultRunnerURL          = "http://127.0.0.1:8097"
	defaultOutputLimit        = 4 << 20
	maxOutputLimit            = 1 << 30
	defaultCleanupTimeout     = 10 * time.Second
	runnerOwnerLeaseTTL       = 5 * time.Second
	runnerOwnerLeaseRenew     = time.Second
	runnerOwnerLeaseFeature   = "runner_owner_lease_v1"
	runnerSessionLeaseFeature = "bruno_session_lease_v1"
	runnerSessionLeaseTTL     = 5 * time.Second
	runnerSessionLeaseRenew   = time.Second
)

type Protocol struct {
	mu                 sync.Mutex
	client             *http.Client
	runnerURL          string
	bundleID           string
	bundleCacheKey     string
	bundleReleaseDone  bool
	bundleDeleteNeeded bool
	sessionID          string
	projectID          string
	sandbox            string
	outputLimit        int64
	streamTimeout      time.Duration
	maxJumps           int64
	maxRunRequestDepth int64
	maxParallel        int64
	persistSession     bool
	compatibility      string
	dependencySnapshot string
	closed             bool
	teardownInFlight   chan struct{}
	runnerOwnerID      string
	runnerOwnerEpoch   string
	runnerOwnerMu      sync.RWMutex
	sessionLeaseToken  string
	sessionLeaseCancel context.CancelFunc
	sessionLeaseDone   chan struct{}
	// containmentUnproven is terminal for this protocol instance. It covers a
	// client-known session whose create response was ambiguous and whose cleanup
	// could not be acknowledged, even though sessionID was never published to the
	// ordinary teardown path.
	containmentUnproven bool
}

var brunoRunnerProcessOwner struct {
	sync.Once
	id string
}

func brunoRunnerOwnerID() string {
	brunoRunnerProcessOwner.Do(func() {
		var value [32]byte
		if _, err := rand.Read(value[:]); err != nil {
			panic("generate Bruno runner owner identity: " + err.Error())
		}
		brunoRunnerProcessOwner.id = "generator-" + hex.EncodeToString(value[:])
	})
	return brunoRunnerProcessOwner.id
}

func (p *Protocol) claimRunnerOwner(ctx context.Context) error {
	payload, err := json.Marshal(map[string]any{"owner_id": brunoRunnerOwnerID(), "lease_ttl_ms": runnerOwnerLeaseTTL.Milliseconds()})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.runnerURL+"/v1/owner/claim", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		return fmt.Errorf("claim Bruno runner owner lease: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return runnerHTTPError(response, 64<<10)
	}
	var claim struct {
		OwnerID    string `json:"owner_id"`
		OwnerEpoch string `json:"owner_epoch"`
		LeaseTTLMS int64  `json:"lease_ttl_ms"`
		Accepted   bool   `json:"accepted"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&claim); err != nil {
		return err
	}
	if !claim.Accepted || claim.OwnerID != brunoRunnerOwnerID() || len(claim.OwnerEpoch) != 64 || claim.LeaseTTLMS != runnerOwnerLeaseTTL.Milliseconds() {
		return fmt.Errorf("Bruno runner returned an invalid owner lease")
	}
	p.runnerOwnerMu.Lock()
	p.runnerOwnerID, p.runnerOwnerEpoch = claim.OwnerID, claim.OwnerEpoch
	p.runnerOwnerMu.Unlock()
	return nil
}

func (p *Protocol) renewRunnerOwner(ctx context.Context) {
	ticker := time.NewTicker(runnerOwnerLeaseRenew)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			renewCtx, cancel := context.WithTimeout(ctx, runnerOwnerLeaseRenew)
			_ = p.claimRunnerOwner(renewCtx)
			cancel()
		}
	}
}

func (p *Protocol) setRunnerOwnerHeaders(request *http.Request) {
	p.runnerOwnerMu.RLock()
	ownerID, ownerEpoch := p.runnerOwnerID, p.runnerOwnerEpoch
	p.runnerOwnerMu.RUnlock()
	if ownerID != "" && ownerEpoch != "" {
		request.Header.Set("X-Apexload-Runner-Owner-ID", ownerID)
		request.Header.Set("X-Apexload-Runner-Owner-Epoch", ownerEpoch)
	}
}

func (p *Protocol) setSessionLeaseHeader(request *http.Request) {
	p.mu.Lock()
	token := p.sessionLeaseToken
	p.mu.Unlock()
	if token != "" {
		request.Header.Set("X-Apexload-Session-Lease-Token", token)
	}
}

func (p *Protocol) startSessionLeaseKeeperLocked(parent context.Context) {
	if p.sessionLeaseToken == "" || p.sessionID == "" {
		return
	}
	if parent == nil {
		parent = context.Background()
	}
	keeperCtx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	p.sessionLeaseCancel, p.sessionLeaseDone = cancel, done
	go p.renewSessionLease(keeperCtx, done)
}

func (p *Protocol) stopSessionLeaseKeeper() {
	p.mu.Lock()
	cancel, done := p.sessionLeaseCancel, p.sessionLeaseDone
	p.sessionLeaseCancel, p.sessionLeaseDone = nil, nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (p *Protocol) renewSessionLease(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(runnerSessionLeaseRenew)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.mu.Lock()
			runnerURL, sessionID, token, client := p.runnerURL, p.sessionID, p.sessionLeaseToken, p.client
			p.mu.Unlock()
			if runnerURL == "" || sessionID == "" || token == "" || client == nil {
				continue
			}
			p.renewKnownSessionLease(ctx, runnerURL, sessionID, token, client)
		}
	}
}

func (p *Protocol) renewKnownSessionLease(ctx context.Context, runnerURL, sessionID, token string, client *http.Client) {
	renewCtx, cancel := context.WithTimeout(ctx, runnerSessionLeaseRenew)
	defer cancel()
	request, err := http.NewRequestWithContext(renewCtx, http.MethodPost,
		runnerURL+"/v1/sessions/"+url.PathEscape(sessionID)+"/lease/renew", nil)
	if err != nil {
		return
	}
	p.setRunnerOwnerHeaders(request)
	request.Header.Set("X-Apexload-Session-Lease-Token", token)
	response, err := client.Do(request)
	if err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	response.Body.Close()
}

type sharedBundle struct {
	id    string
	refs  int
	ready chan struct{}
	err   error
}

var sharedBundles = struct {
	sync.Mutex
	byKey map[string]sharedBundle
}{byKey: make(map[string]sharedBundle)}

type openBundleResponse struct {
	BundleID string `json:"bundle_id"`
}

type createSessionRequest struct {
	SessionID             string            `json:"session_id"`
	BundleID              string            `json:"bundle_id"`
	ProjectID             string            `json:"project_id,omitempty"`
	Sandbox               string            `json:"sandbox"`
	CompatibilityVersion  string            `json:"compatibility_version"`
	DependencySnapshotRef string            `json:"dependency_snapshot_ref,omitempty"`
	Environment           string            `json:"environment,omitempty"`
	GlobalEnvironment     string            `json:"global_environment,omitempty"`
	PersistSession        bool              `json:"persist_session"`
	VUserID               int               `json:"vuser_id"`
	RunnerProfile         string            `json:"runner_profile,omitempty"`
	Limits                map[string]int64  `json:"limits,omitempty"`
	Secrets               map[string]string `json:"secrets,omitempty"`
	AllowedTargetHosts    []string          `json:"allowed_target_hosts,omitempty"`
	SessionLeaseToken     string            `json:"session_lease_token,omitempty"`
}

type createSessionResponse struct {
	SessionID         string `json:"session_id"`
	SessionLeaseToken string `json:"session_lease_token"`
	LeaseTTLMS        int64  `json:"lease_ttl_ms"`
}

type executeRequest struct {
	Action protocol.Action `json:"action"`
}

type runnerEvent struct {
	Type   string       `json:"type"`
	Result runnerResult `json:"result"`
	Error  string       `json:"error,omitempty"`
}

type runnerResult struct {
	ActionName       string            `json:"action_name"`
	SampleKind       string            `json:"sample_kind,omitempty"`
	ParentActionName string            `json:"parent_action_name,omitempty"`
	StartTime        time.Time         `json:"start_time,omitempty"`
	DurationMS       float64           `json:"duration_ms"`
	CorrelationID    string            `json:"correlation_id,omitempty"`
	DependencyType   string            `json:"dependency_type,omitempty"`
	DependencyTarget string            `json:"dependency_target,omitempty"`
	DNSTimeMS        float64           `json:"dns_ms,omitempty"`
	ConnectTimeMS    float64           `json:"connect_ms,omitempty"`
	TLSTimeMS        float64           `json:"tls_ms,omitempty"`
	TTFBMS           float64           `json:"ttfb_ms,omitempty"`
	BytesSent        int64             `json:"bytes_sent,omitempty"`
	BytesReceived    int64             `json:"bytes_received,omitempty"`
	StatusCode       int               `json:"status_code,omitempty"`
	Success          bool              `json:"success"`
	ErrorMessage     string            `json:"error_message,omitempty"`
	ResponseBody     string            `json:"response_body,omitempty"`
	Extractions      map[string]string `json:"extractions,omitempty"`
	Evidence         map[string]any    `json:"evidence,omitempty"`
	SourcePath       string            `json:"source_path,omitempty"`
	SourceUID        string            `json:"source_uid,omitempty"`
	RequestType      string            `json:"request_type,omitempty"`
	OperationKind    string            `json:"operation_kind,omitempty"`
}

func init() {
	protocol.Register("bruno", func() protocol.Protocol { return &Protocol{} })
}

func (*Protocol) Name() string { return "bruno" }

func (*Protocol) RuntimeCapabilities() protocol.RuntimeCapabilities {
	features := []string{"bruno_runner_" + CompatibilityVersion, "bruno_safe_sandbox", "bruno_governed_egress", "bruno_shared_encrypted_artifacts_v1"}
	available := distributedExecutionAvailable(context.Background())
	if available {
		features = append(features, runnerOwnerLeaseFeature, runnerSessionLeaseFeature)
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("APEXLOAD_BRUNO_DEVELOPER_EXECUTION_ENABLED")), "true") {
		features = append(features, "bruno_developer_sandbox")
	}
	return protocol.RuntimeCapabilities{Available: available, Features: features, MaxVUsers: configuredSessionCapacity()}
}

func (*Protocol) RuntimeHealthy(ctx context.Context) bool { return RunnerHealthy(ctx) }

func configuredSessionCapacity() int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(os.Getenv("APEXLOAD_BRUNO_MAX_SESSIONS")), 10, 64)
	if err != nil || value <= 0 {
		return 256
	}
	return value
}

// DistributedExecutionAvailable is deliberately stricter than protocol
// registration. A generator may advertise Bruno only when the operator has
// enabled distributed execution and this process has an immutable artifact
// delivery resolver installed. The runner must also positively advertise and
// require both owner and per-session leases; an enabled generator flag alone
// is not evidence that an external worker will fence itself.
func DistributedExecutionAvailable() bool {
	return distributedExecutionAvailable(context.Background())
}

func distributedExecutionConfigured() bool {
	return featureEnabled("APEXLOAD_BRUNO_DISTRIBUTED_EXECUTION_ENABLED", false) &&
		importartifact.ResolverAvailable() &&
		GovernedEgressConfigured()
}

func distributedExecutionAvailable(ctx context.Context) bool {
	if !distributedExecutionConfigured() || !featureEnabled("APEXLOAD_BRUNO_RUNNER_OWNER_LEASE_V1", false) {
		return false
	}
	healthy, features := probeRunner(ctx)
	if !healthy || !runnerLeaseContractSatisfied(features) {
		return false
	}
	return true
}

func runnerLeaseContractSatisfied(features []string) bool {
	return protocol.RuntimeFeaturesSatisfy(features, runnerOwnerLeaseFeature) &&
		protocol.RuntimeFeaturesSatisfy(features, runnerSessionLeaseFeature)
}

// GovernedEgressConfigured reports whether this process has an explicit path
// for Bruno traffic. Direct egress is never implicit; the override exists only
// for isolated local conformance environments.
func GovernedEgressConfigured() bool {
	if featureEnabled("APEXLOAD_BRUNO_ALLOW_DIRECT_EGRESS", false) {
		return true
	}
	raw := strings.TrimSpace(os.Getenv("APEXLOAD_BRUNO_EGRESS_BROKER_URL"))
	if raw == "" {
		return false
	}
	parsed, err := url.Parse(raw)
	return err == nil &&
		parsed.Scheme == "http" &&
		parsed.Host != "" &&
		parsed.User == nil &&
		parsed.RawQuery == "" &&
		parsed.Fragment == ""
}

// RunnerHealthy verifies the configured sidecar is live and version-compatible.
// Heartbeats use this before advertising Bruno so a missing or stale runner is
// not selected for new work.
func RunnerHealthy(ctx context.Context) (healthy bool) {
	healthy, _ = probeRunner(ctx)
	return healthy
}

func probeRunner(ctx context.Context) (healthy bool, features []string) {
	runnerURL := strings.TrimRight(strings.TrimSpace(os.Getenv("APEXLOAD_BRUNO_RUNNER_URL")), "/")
	if runnerURL == "" {
		runnerURL = defaultRunnerURL
	}
	return probeRunnerAt(ctx, runnerURL, true)
}

func probeRunnerAt(ctx context.Context, runnerURL string, allowCached bool) (healthy bool, features []string) {
	if ctx == nil {
		ctx = context.Background()
	}
	runnerURL = strings.TrimRight(strings.TrimSpace(runnerURL), "/")
	if _, err := validatedRunnerOrigin(runnerURL); err != nil {
		return false, nil
	}
	runnerHealthCache.Lock()
	if allowCached && runnerHealthCache.url == runnerURL && time.Since(runnerHealthCache.checkedAt) < 2*time.Second {
		healthy := runnerHealthCache.healthy
		features := append([]string(nil), runnerHealthCache.features...)
		runnerHealthCache.Unlock()
		return healthy, features
	}
	runnerHealthCache.Unlock()
	defer func() {
		runnerHealthCache.Lock()
		runnerHealthCache.url, runnerHealthCache.checkedAt, runnerHealthCache.healthy = runnerURL, time.Now(), healthy
		runnerHealthCache.features = append(runnerHealthCache.features[:0], features...)
		runnerHealthCache.Unlock()
	}()
	checkCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(checkCtx, http.MethodGet, runnerURL+"/v1/health", nil)
	if err != nil {
		return false, nil
	}
	response, err := newRunnerHTTPClient(0).Do(request)
	if err != nil {
		return false, nil
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return false, nil
	}
	var health struct {
		CompatibilityVersion string `json:"compatibility_version"`
	}
	healthy = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&health) == nil && health.CompatibilityVersion == CompatibilityVersion
	response.Body.Close()
	if !healthy {
		return false, nil
	}
	request, err = http.NewRequestWithContext(checkCtx, http.MethodGet, runnerURL+"/v1/capabilities", nil)
	if err != nil {
		return false, nil
	}
	response, err = newRunnerHTTPClient(0).Do(request)
	if err != nil {
		return false, nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, nil
	}
	var capabilities struct {
		CompatibilityVersion string   `json:"compatibility_version"`
		Features             []string `json:"features"`
		CleanupFailed        bool     `json:"cleanup_failed"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&capabilities) != nil ||
		capabilities.CompatibilityVersion != CompatibilityVersion || capabilities.CleanupFailed {
		return false, nil
	}
	return true, append([]string(nil), capabilities.Features...)
}

var runnerHealthCache struct {
	sync.Mutex
	url       string
	checkedAt time.Time
	healthy   bool
	features  []string
}

func (p *Protocol) Init(ctx context.Context, config map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.teardownInFlight != nil || p.sessionID != "" || p.bundleID != "" {
		return fmt.Errorf("Bruno runner session cleanup is still pending")
	}
	p.closed = true
	if !featureEnabled("APEXLOAD_BRUNO_SAFE_EXECUTION_ENABLED", true) {
		return fmt.Errorf("Bruno safe execution is disabled by operator policy")
	}
	p.sandbox = strings.ToLower(strings.TrimSpace(stringValue(config, "bruno_sandbox")))
	if p.sandbox == "" {
		p.sandbox = "safe"
	}
	if p.sandbox != "safe" && p.sandbox != "developer" {
		return fmt.Errorf("Bruno sandbox must be safe or developer")
	}
	if p.sandbox == "developer" && !featureEnabled("APEXLOAD_BRUNO_DEVELOPER_EXECUTION_ENABLED", false) {
		return fmt.Errorf("Bruno developer execution requires an operator-enabled isolated runner")
	}
	p.compatibility = strings.TrimSpace(stringValue(config, "bruno_compatibility_version"))
	if p.compatibility == "" {
		p.compatibility = CompatibilityVersion
	}
	if p.compatibility != CompatibilityVersion {
		return fmt.Errorf("Bruno compatibility version %q is not supported; runner adapter is pinned to %s", p.compatibility, CompatibilityVersion)
	}
	p.projectID = strings.TrimSpace(stringValue(config, "project_id"))
	p.dependencySnapshot = strings.TrimSpace(stringValue(config, "bruno_dependency_snapshot_ref"))
	p.persistSession = boolValue(config, "bruno_persist_session_across_iterations")
	p.maxJumps = narrowBrunoRuntimeLimit(
		int64Value(config, "bruno_max_jumps"),
		10_000,
		operatorBrunoRuntimeLimit("APEXLOAD_BRUNO_MAX_JUMPS", 10_000, 1_000_000),
	)
	p.maxRunRequestDepth = narrowBrunoRuntimeLimit(
		int64Value(config, "bruno_max_run_request_depth"),
		64,
		operatorBrunoRuntimeLimit("APEXLOAD_BRUNO_MAX_RUN_REQUEST_DEPTH", 64, 1_024),
	)
	p.maxParallel = narrowBrunoRuntimeLimit(
		int64Value(config, "bruno_max_parallel_requests"),
		100,
		operatorBrunoRuntimeLimit("APEXLOAD_BRUNO_MAX_PARALLEL_REQUESTS", 100, 100_000),
	)
	p.outputLimit = narrowBrunoRuntimeLimit(
		int64Value(config, "bruno_max_output_bytes"),
		defaultOutputLimit,
		operatorBrunoRuntimeLimit("APEXLOAD_BRUNO_MAX_OUTPUT_BYTES", defaultOutputLimit, maxOutputLimit),
	)
	streamTimeoutMS := narrowBrunoRuntimeLimit(
		int64Value(config, "bruno_default_stream_timeout_ms"),
		60_000,
		operatorBrunoRuntimeLimit("APEXLOAD_BRUNO_MAX_STREAM_TIMEOUT_MS", 60_000, 86_400_000),
	)
	p.streamTimeout = time.Duration(streamTimeoutMS) * time.Millisecond
	p.runnerURL = strings.TrimRight(strings.TrimSpace(os.Getenv("APEXLOAD_BRUNO_RUNNER_URL")), "/")
	if configured := strings.TrimSpace(stringValue(config, "bruno_runner_url")); configured != "" {
		p.runnerURL = strings.TrimRight(configured, "/")
	}
	if p.runnerURL == "" {
		p.runnerURL = defaultRunnerURL
	}
	if _, err := validatedRunnerOrigin(p.runnerURL); err != nil {
		return err
	}
	if unresolved := unresolvedBrunoSecretBindings(config["bruno_secret_bindings"]); len(unresolved) > 0 {
		return fmt.Errorf("unresolved Bruno secret binding(s) %s: refusing to create a runner session with placeholder values", strings.Join(unresolved, ", "))
	}
	p.client = newRunnerHTTPClient(0)
	if featureEnabled("APEXLOAD_BRUNO_DISTRIBUTED_EXECUTION_ENABLED", false) {
		if !featureEnabled("APEXLOAD_BRUNO_RUNNER_OWNER_LEASE_V1", false) {
			return fmt.Errorf("distributed Bruno execution requires %s and %s; runner owner leases are disabled", runnerOwnerLeaseFeature, runnerSessionLeaseFeature)
		}
		healthy, runnerFeatures := probeRunnerAt(ctx, p.runnerURL, false)
		if !healthy || !runnerLeaseContractSatisfied(runnerFeatures) {
			return fmt.Errorf("distributed Bruno execution requires the runner to advertise %s and %s", runnerOwnerLeaseFeature, runnerSessionLeaseFeature)
		}
	}
	if featureEnabled("APEXLOAD_BRUNO_RUNNER_OWNER_LEASE_V1", false) {
		if err := p.claimRunnerOwner(ctx); err != nil {
			return err
		}
		go p.renewRunnerOwner(ctx)
	}
	expectedDigest := strings.ToLower(strings.TrimSpace(stringValue(config, "bruno_bundle_sha256")))
	if id, key, hit, cacheErr := p.retainCachedBundle(ctx, expectedDigest); cacheErr != nil {
		return cacheErr
	} else if hit {
		p.assignBundleLocked(id, key)
		sessionID, leaseToken, createErr := p.createSession(ctx, config)
		if createErr != nil {
			if runnerResourceUnavailable(createErr) {
				// Runner bundle lifetime is independently bounded. Evict only the
				// exact stale generation, then use the normal singleflight upload
				// path below for one bounded retry.
				invalidateSharedBundle(key, id)
				p.clearBundleLocked()
			} else {
				cleanupCtx, cleanupCancel := freshCleanupContext()
				cleanupErr := p.releaseBundleLocked(cleanupCtx)
				cleanupCancel()
				return errors.Join(createErr, cleanupErr)
			}
		} else {
			p.sessionID, p.sessionLeaseToken, p.closed = sessionID, leaseToken, false
			p.startSessionLeaseKeeperLocked(ctx)
			return nil
		}
	}

	var bundleReader io.ReadCloser
	var bundleSize int64
	bundle, ok := config["bruno_bundle_bytes"].([]byte)
	if ok {
		bundle = append([]byte(nil), bundle...)
		bundleReader = io.NopCloser(bytes.NewReader(bundle))
		bundleSize = int64(len(bundle))
	} else {
		opened, size, openErr := importartifact.Open(ctx, importartifact.Request{
			Reference: stringValue(config, "bruno_bundle_ref"),
			ProjectID: p.projectID,
			SHA256:    stringValue(config, "bruno_bundle_sha256"),
		})
		if openErr != nil {
			return openErr
		}
		bundleReader, bundleSize = opened, size
	}
	defer bundleReader.Close()
	if expectedDigest != "" && ok {
		sum := sha256.Sum256(bundle)
		if actual := hex.EncodeToString(sum[:]); actual != expectedDigest {
			return fmt.Errorf("Bruno bundle digest mismatch: got %s", actual)
		}
	}
	bundleID, bundleCacheKey, err := p.acquireBundle(ctx, bundleReader, bundleSize, expectedDigest, stringValue(config, "bruno_manifest_sha256"))
	if err != nil {
		return err
	}
	p.assignBundleLocked(bundleID, bundleCacheKey)
	sessionID, leaseToken, err := p.createSession(ctx, config)
	if err != nil {
		cleanupCtx, cleanupCancel := freshCleanupContext()
		cleanupErr := p.releaseBundleLocked(cleanupCtx)
		cleanupCancel()
		return errors.Join(err, cleanupErr)
	}
	p.sessionID = sessionID
	p.sessionLeaseToken = leaseToken
	p.closed = false
	p.startSessionLeaseKeeperLocked(ctx)
	return nil
}

// releaseBundleLocked releases an Init-time bundle while p.mu is already held.
func (p *Protocol) releaseBundleLocked(ctx context.Context) error {
	bundleID, shouldDelete := p.prepareBundleDeleteLocked()
	if !shouldDelete {
		return nil
	}
	if err := p.delete(ctx, "/v1/bundles/"+url.PathEscape(bundleID)); err != nil {
		return err
	}
	if p.bundleID == bundleID {
		p.clearBundleLocked()
	}
	return nil
}

func (p *Protocol) assignBundleLocked(bundleID, cacheKey string) {
	p.bundleID = bundleID
	p.bundleCacheKey = cacheKey
	p.bundleReleaseDone = false
	p.bundleDeleteNeeded = false
}

func (p *Protocol) clearBundleLocked() {
	p.bundleID = ""
	p.bundleCacheKey = ""
	p.bundleReleaseDone = false
	p.bundleDeleteNeeded = false
}

func (p *Protocol) prepareBundleDeleteLocked() (string, bool) {
	if p.bundleID == "" {
		return "", false
	}
	if !p.bundleReleaseDone {
		p.bundleDeleteNeeded = releaseSharedBundle(p.bundleCacheKey, p.bundleID)
		p.bundleReleaseDone = true
	}
	if !p.bundleDeleteNeeded {
		p.clearBundleLocked()
		return "", false
	}
	return p.bundleID, true
}

func (p *Protocol) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	return p.ExecuteStream(ctx, action, func(*protocol.Result) error { return nil })
}

func (p *Protocol) ExecuteStream(ctx context.Context, action *protocol.Action, emit func(*protocol.Result) error) (*protocol.Result, error) {
	if action == nil {
		return nil, fmt.Errorf("Bruno action is required")
	}
	p.mu.Lock()
	sessionID := p.sessionID
	runnerURL := p.runnerURL
	client := p.client
	closed := p.closed
	outputLimit := p.outputLimit
	p.mu.Unlock()
	if closed || sessionID == "" || client == nil {
		return nil, fmt.Errorf("Bruno runner session is not initialized")
	}
	if action.Type != "bruno_run" {
		return nil, fmt.Errorf("unsupported Bruno action type %q", action.Type)
	}
	payload, err := json.Marshal(executeRequest{Action: *action})
	if err != nil {
		return nil, fmt.Errorf("marshal Bruno execution request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, runnerURL+"/v1/sessions/"+url.PathEscape(sessionID)+"/execute", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/x-ndjson")
	p.setRunnerOwnerHeaders(request)
	p.setSessionLeaseHeader(request)
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("execute Bruno selection: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, runnerHTTPError(response, outputLimit)
	}
	decoder := json.NewDecoder(&boundedReader{reader: response.Body, remaining: outputLimit})
	var aggregate *protocol.Result
	for {
		var event runnerEvent
		if err := decoder.Decode(&event); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decode Bruno runner event: %w", err)
		}
		switch event.Type {
		case "result":
			child := event.Result.protocolResult()
			if child.SampleKind == "" {
				child.SampleKind = "child"
			}
			if err := emit(child); err != nil {
				cleanupCtx, cleanupCancel := freshCleanupContext()
				_ = p.cancel(cleanupCtx)
				cleanupCancel()
				return nil, err
			}
		case "complete":
			aggregate = event.Result.protocolResult()
			if aggregate.SampleKind == "" {
				aggregate.SampleKind = "aggregate"
			}
		case "error":
			// Runner messages are untrusted execution output and may echo a
			// credential used by the collection. Keep the transport-level failure
			// actionable without reflecting any runner-controlled text.
			return nil, fmt.Errorf("Bruno runner execution failed")
		default:
			return nil, fmt.Errorf("Bruno runner returned an unknown event type")
		}
	}
	if aggregate == nil {
		return nil, fmt.Errorf("Bruno runner stream ended without an aggregate result")
	}
	return aggregate, nil
}

func (p *Protocol) Teardown(ctx context.Context) error {
	p.stopSessionLeaseKeeper()
	cleanupCtx := ctx
	if cleanupCtx == nil {
		cleanupCtx = context.Background()
	}
	for {
		p.mu.Lock()
		if inFlight := p.teardownInFlight; inFlight != nil {
			unproven := p.containmentUnproven
			p.mu.Unlock()
			select {
			case <-inFlight:
				continue
			case <-cleanupCtx.Done():
				if unproven {
					return errors.Join(protocol.ErrContainmentUnproven, cleanupCtx.Err())
				}
				return cleanupCtx.Err()
			}
		}
		p.closed = true
		unproven := p.containmentUnproven
		if p.sessionID == "" && p.bundleID == "" {
			p.mu.Unlock()
			if unproven {
				return protocol.ErrContainmentUnproven
			}
			return nil
		}
		inFlight := make(chan struct{})
		p.teardownInFlight = inFlight
		p.mu.Unlock()

		err := func() error {
			defer func() {
				p.mu.Lock()
				if p.teardownInFlight == inFlight {
					p.teardownInFlight = nil
					close(inFlight)
				}
				p.mu.Unlock()
			}()
			return p.teardownAttempt(cleanupCtx)
		}()
		if err == nil && unproven {
			return protocol.ErrContainmentUnproven
		}
		return err
	}
}

func (p *Protocol) teardownAttempt(ctx context.Context) error {
	p.mu.Lock()
	sessionID := p.sessionID
	p.mu.Unlock()
	if sessionID != "" {
		p.mu.Lock()
		leaseToken := p.sessionLeaseToken
		p.mu.Unlock()
		for {
			err := p.deleteSession(ctx, sessionID, leaseToken)
			if err == nil {
				break
			}
			select {
			case <-ctx.Done():
				return errors.Join(
					protocol.ErrContainmentUnproven,
					fmt.Errorf("Bruno session reap remained unproven: %w", ctx.Err()),
				)
			case <-time.After(100 * time.Millisecond):
			}
		}
		p.mu.Lock()
		if p.sessionID == sessionID {
			p.sessionID = ""
			p.sessionLeaseToken = ""
		}
		p.mu.Unlock()
	}

	p.mu.Lock()
	bundleID, shouldDelete := p.prepareBundleDeleteLocked()
	p.mu.Unlock()
	if !shouldDelete {
		return nil
	}
	if err := p.delete(ctx, "/v1/bundles/"+url.PathEscape(bundleID)); err != nil {
		return err
	}
	p.mu.Lock()
	if p.bundleID == bundleID {
		p.clearBundleLocked()
	}
	p.mu.Unlock()
	return nil
}

func cleanupTimeout() time.Duration {
	if configured := strings.TrimSpace(os.Getenv("APEXLOAD_BRUNO_CLEANUP_TIMEOUT")); configured != "" {
		if duration, err := time.ParseDuration(configured); err == nil && duration > 0 {
			return duration
		}
	}
	return defaultCleanupTimeout
}

func freshCleanupContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), cleanupTimeout())
}

func boundedCleanupContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, cleanupTimeout())
}

func (p *Protocol) HealthCheck(ctx context.Context) error {
	p.mu.Lock()
	runnerURL := p.runnerURL
	client := p.client
	p.mu.Unlock()
	if runnerURL == "" {
		runnerURL = strings.TrimRight(strings.TrimSpace(os.Getenv("APEXLOAD_BRUNO_RUNNER_URL")), "/")
		if runnerURL == "" {
			runnerURL = defaultRunnerURL
		}
	}
	if _, err := validatedRunnerOrigin(runnerURL); err != nil {
		return err
	}
	if client == nil {
		client = newRunnerHTTPClient(5 * time.Second)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, runnerURL+"/v1/health", nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return runnerHTTPError(response, 64<<10)
	}
	var health struct {
		CompatibilityVersion string `json:"compatibility_version"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&health); err != nil {
		return err
	}
	if health.CompatibilityVersion != CompatibilityVersion {
		return fmt.Errorf("Bruno runner compatibility is %q, require %s", health.CompatibilityVersion, CompatibilityVersion)
	}
	return nil
}

func newRunnerHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: http.DefaultTransport,
		Timeout:   timeout,
		// Every runner endpoint is an exact-origin control API. Do not permit
		// health, upload, execution, cancellation, or secret-bearing session
		// operations to be redirected or downgraded.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (p *Protocol) openBundle(ctx context.Context, bundle io.Reader, size int64, digest, manifestDigest string) (string, error) {
	openCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	request, err := http.NewRequestWithContext(openCtx, http.MethodPost, p.runnerURL+"/v1/bundles/open", bundle)
	if err != nil {
		return "", err
	}
	request.ContentLength = size
	request.Header.Set("Content-Type", "application/x-tar")
	request.Header.Set("X-Bruno-SHA256", digest)
	request.Header.Set("X-Bruno-Manifest-SHA256", strings.TrimSpace(manifestDigest))
	request.Header.Set("X-Bruno-Compatibility-Version", p.compatibility)
	response, err := p.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("open Bruno bundle: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return "", runnerHTTPError(response, 1<<20)
	}
	var decoded openBundleResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&decoded); err != nil {
		return "", err
	}
	if strings.TrimSpace(decoded.BundleID) == "" {
		return "", fmt.Errorf("Bruno runner returned an empty bundle id")
	}
	return decoded.BundleID, nil
}

func (p *Protocol) acquireBundle(ctx context.Context, bundle io.Reader, size int64, digest, manifestDigest string) (string, string, error) {
	digest = strings.ToLower(strings.TrimSpace(digest))
	if digest == "" {
		id, err := p.openBundle(ctx, bundle, size, digest, manifestDigest)
		return id, "", err
	}
	key := p.runnerURL + "\x00" + p.compatibility + "\x00" + digest
	for {
		sharedBundles.Lock()
		existing, ok := sharedBundles.byKey[key]
		if !ok {
			pending := sharedBundle{refs: 1, ready: make(chan struct{})}
			sharedBundles.byKey[key] = pending
			sharedBundles.Unlock()
			id, err := p.openBundle(ctx, bundle, size, digest, manifestDigest)
			sharedBundles.Lock()
			if err != nil {
				pending.err = err
				close(pending.ready)
				delete(sharedBundles.byKey, key)
				sharedBundles.Unlock()
				return "", "", err
			}
			pending.id = id
			sharedBundles.byKey[key] = pending
			close(pending.ready)
			sharedBundles.Unlock()
			return id, key, nil
		}
		ready := existing.ready
		sharedBundles.Unlock()
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-ready:
		}
		sharedBundles.Lock()
		existing, ok = sharedBundles.byKey[key]
		if ok && existing.ready != ready {
			sharedBundles.Unlock()
			continue
		}
		if !ok || existing.err != nil {
			sharedBundles.Unlock()
			if ok {
				return "", "", existing.err
			}
			return "", "", errors.New("Bruno shared bundle upload failed")
		}
		existing.refs++
		sharedBundles.byKey[key] = existing
		sharedBundles.Unlock()
		return existing.id, key, nil
	}
}

func (p *Protocol) retainCachedBundle(ctx context.Context, digest string) (string, string, bool, error) {
	digest = strings.ToLower(strings.TrimSpace(digest))
	if digest == "" {
		return "", "", false, nil
	}
	key := p.runnerURL + "\x00" + p.compatibility + "\x00" + digest
	for {
		sharedBundles.Lock()
		existing, ok := sharedBundles.byKey[key]
		if !ok {
			sharedBundles.Unlock()
			return "", "", false, nil
		}
		ready := existing.ready
		sharedBundles.Unlock()
		select {
		case <-ctx.Done():
			return "", "", false, ctx.Err()
		case <-ready:
		}
		sharedBundles.Lock()
		existing, ok = sharedBundles.byKey[key]
		if ok && existing.ready != ready {
			sharedBundles.Unlock()
			continue
		}
		if !ok || existing.err != nil {
			sharedBundles.Unlock()
			if ok {
				return "", "", false, existing.err
			}
			return "", "", false, nil
		}
		existing.refs++
		sharedBundles.byKey[key] = existing
		sharedBundles.Unlock()
		return existing.id, key, true, nil
	}
}

func releaseSharedBundle(key, bundleID string) bool {
	if key == "" {
		return true
	}
	sharedBundles.Lock()
	defer sharedBundles.Unlock()
	existing, ok := sharedBundles.byKey[key]
	if !ok || existing.id != bundleID {
		return true
	}
	if existing.refs <= 1 {
		delete(sharedBundles.byKey, key)
		return true
	}
	existing.refs--
	sharedBundles.byKey[key] = existing
	return false
}

func invalidateSharedBundle(key, bundleID string) {
	if key == "" || bundleID == "" {
		return
	}
	sharedBundles.Lock()
	defer sharedBundles.Unlock()
	if existing, ok := sharedBundles.byKey[key]; ok && existing.id == bundleID {
		delete(sharedBundles.byKey, key)
	}
}

func (p *Protocol) createSession(ctx context.Context, config map[string]any) (string, string, error) {
	sessionID, err := newClientSessionID()
	if err != nil {
		return "", "", err
	}
	leaseToken, err := newSessionLeaseToken()
	if err != nil {
		return "", "", err
	}
	payload := createSessionRequest{
		SessionID:             sessionID,
		BundleID:              p.bundleID,
		ProjectID:             p.projectID,
		Sandbox:               p.sandbox,
		CompatibilityVersion:  p.compatibility,
		DependencySnapshotRef: p.dependencySnapshot,
		Environment:           stringValue(config, "bruno_default_environment"),
		GlobalEnvironment:     stringValue(config, "bruno_default_global_environment"),
		PersistSession:        p.persistSession,
		VUserID:               int(int64Value(config, "vuser_id")),
		RunnerProfile:         stringValue(config, "bruno_runner_profile"),
		Limits: map[string]int64{
			"max_jumps":                 p.maxJumps,
			"max_run_request_depth":     p.maxRunRequestDepth,
			"max_parallel_requests":     p.maxParallel,
			"max_output_bytes":          p.outputLimit,
			"default_stream_timeout_ms": int64(p.streamTimeout / time.Millisecond),
		},
		Secrets:            stringMapValue(config["bruno_secret_bindings"]),
		AllowedTargetHosts: protocol.TargetPolicyHosts(config),
		SessionLeaseToken:  leaseToken,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", "", err
	}
	// The runner reserves the client-generated session before awaiting a
	// potentially slow developer-worker factory. Keep that reserved generation
	// alive while the create response is pending; the ordinary session keeper
	// cannot start until Init receives and validates that response.
	provisionalCtx, stopProvisional := context.WithCancel(ctx)
	provisionalDone := make(chan struct{})
	go func() {
		defer close(provisionalDone)
		ticker := time.NewTicker(runnerSessionLeaseRenew)
		defer ticker.Stop()
		for {
			select {
			case <-provisionalCtx.Done():
				return
			case <-ticker.C:
				p.renewKnownSessionLease(provisionalCtx, p.runnerURL, sessionID, leaseToken, p.client)
			}
		}
	}()
	defer func() {
		stopProvisional()
		<-provisionalDone
	}()
	var uncertainErr error
	for attempt := 0; attempt < 2; attempt++ {
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, p.runnerURL+"/v1/sessions", bytes.NewReader(body))
		if requestErr != nil {
			return "", "", requestErr
		}
		request.Header.Set("Content-Type", "application/json")
		p.setRunnerOwnerHeaders(request)
		response, requestErr := p.client.Do(request)
		if requestErr != nil {
			uncertainErr = fmt.Errorf("create Bruno runner session: %w", requestErr)
			if ctx.Err() != nil {
				break
			}
			continue
		}
		if response.StatusCode != http.StatusCreated {
			httpErr := runnerHTTPError(response, 1<<20)
			response.Body.Close()
			return "", "", httpErr
		}
		var decoded createSessionResponse
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&decoded)
		response.Body.Close()
		if decodeErr != nil {
			uncertainErr = fmt.Errorf("decode Bruno runner session: %w", decodeErr)
			continue
		}
		if strings.TrimSpace(decoded.SessionID) != sessionID || decoded.SessionLeaseToken != leaseToken ||
			decoded.LeaseTTLMS != runnerSessionLeaseTTL.Milliseconds() {
			uncertainErr = fmt.Errorf("Bruno runner did not acknowledge the requested session id")
			continue
		}
		return sessionID, leaseToken, nil
	}
	// The server reserves the client ID before provisioning a worker. Even if
	// both create responses were lost, this exact ID can cancel an in-progress
	// placeholder or delete the committed secret-bearing session.
	cleanupCtx, cleanupCancel := freshCleanupContext()
	cleanupErr := p.deleteSession(cleanupCtx, sessionID, leaseToken)
	cleanupCancel()
	if cleanupErr != nil {
		p.containmentUnproven = true
		cleanupErr = errors.Join(protocol.ErrContainmentUnproven, cleanupErr)
	}
	return "", "", errors.Join(uncertainErr, cleanupErr)
}

func newClientSessionID() (string, error) {
	var material [16]byte
	if _, err := rand.Read(material[:]); err != nil {
		return "", fmt.Errorf("generate Bruno session identifier: %w", err)
	}
	return "session-" + hex.EncodeToString(material[:]), nil
}

func newSessionLeaseToken() (string, error) {
	var material [32]byte
	n, err := rand.Read(material[:])
	if err != nil {
		return "", fmt.Errorf("generate Bruno session lease token: %w", err)
	}
	if n != len(material) {
		return "", fmt.Errorf("generate Bruno session lease token: %w", io.ErrUnexpectedEOF)
	}
	return hex.EncodeToString(material[:]), nil
}

func (p *Protocol) cancel(ctx context.Context) error {
	p.mu.Lock()
	sessionID := p.sessionID
	p.mu.Unlock()
	if sessionID == "" {
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.runnerURL+"/v1/sessions/"+url.PathEscape(sessionID)+"/cancel", nil)
	if err != nil {
		return err
	}
	p.setRunnerOwnerHeaders(request)
	p.setSessionLeaseHeader(request)
	response, err := p.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return runnerHTTPError(response, 64<<10)
	}
	return nil
}

func (p *Protocol) delete(ctx context.Context, path string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, p.runnerURL+path, nil)
	if err != nil {
		return err
	}
	p.setRunnerOwnerHeaders(request)
	response, err := p.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusNotFound {
		return runnerHTTPError(response, 64<<10)
	}
	return nil
}

func (p *Protocol) deleteSession(ctx context.Context, sessionID, leaseToken string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		p.runnerURL+"/v1/sessions/"+url.PathEscape(sessionID), nil)
	if err != nil {
		return err
	}
	p.setRunnerOwnerHeaders(request)
	if leaseToken != "" {
		request.Header.Set("X-Apexload-Session-Lease-Token", leaseToken)
	}
	response, err := p.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusNotFound {
		return runnerHTTPError(response, 64<<10)
	}
	return nil
}

func (value runnerResult) protocolResult() *protocol.Result {
	start := value.StartTime
	if start.IsZero() {
		start = time.Now().Add(-milliseconds(value.DurationMS))
	}
	return &protocol.Result{
		ActionName:       value.ActionName,
		SampleKind:       value.SampleKind,
		ParentActionName: value.ParentActionName,
		StartTime:        start,
		Duration:         milliseconds(value.DurationMS),
		CorrelationID:    value.CorrelationID,
		DependencyType:   value.DependencyType,
		DependencyTarget: value.DependencyTarget,
		DNSTime:          milliseconds(value.DNSTimeMS),
		ConnectTime:      milliseconds(value.ConnectTimeMS),
		TLSTime:          milliseconds(value.TLSTimeMS),
		TTFB:             milliseconds(value.TTFBMS),
		BytesSent:        value.BytesSent,
		BytesReceived:    value.BytesReceived,
		StatusCode:       value.StatusCode,
		Success:          value.Success,
		ErrorMessage:     value.ErrorMessage,
		ResponseBody:     value.ResponseBody,
		Extractions:      value.Extractions,
		ProtocolEvidence: value.Evidence,
		SourcePath:       value.SourcePath,
		SourceUID:        value.SourceUID,
		RequestType:      value.RequestType,
		OperationKind:    value.OperationKind,
	}
}

type boundedReader struct {
	reader    io.Reader
	remaining int64
}

func (r *boundedReader) Read(buffer []byte) (int, error) {
	if r.remaining < 0 {
		return 0, fmt.Errorf("Bruno runner output exceeds configured limit")
	}
	if r.remaining == 0 {
		var probe [1]byte
		n, err := r.reader.Read(probe[:])
		if n > 0 {
			return 0, fmt.Errorf("Bruno runner output exceeds configured limit")
		}
		return 0, err
	}
	if int64(len(buffer)) > r.remaining {
		buffer = buffer[:r.remaining]
	}
	n, err := r.reader.Read(buffer)
	r.remaining -= int64(n)
	return n, err
}

type runnerHTTPStatusError struct {
	StatusCode int
	Status     string
}

func (e *runnerHTTPStatusError) Error() string {
	return fmt.Sprintf("Bruno runner returned %s", e.Status)
}

func runnerResourceUnavailable(err error) bool {
	var statusErr *runnerHTTPStatusError
	return errors.As(err, &statusErr) && (statusErr.StatusCode == http.StatusNotFound || statusErr.StatusCode == http.StatusGone)
}

func runnerHTTPError(response *http.Response, _ int64) error {
	statusText := http.StatusText(response.StatusCode)
	if statusText == "" {
		statusText = "unknown status"
	}
	return &runnerHTTPStatusError{
		StatusCode: response.StatusCode,
		Status:     fmt.Sprintf("%d %s", response.StatusCode, statusText),
	}
}

func featureEnabled(name string, defaultValue bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue
	}
	value, err := strconv.ParseBool(raw)
	return err == nil && value
}

func operatorBrunoRuntimeLimit(name string, fallback, absoluteMaximum int64) int64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return fallback
	}
	return min(value, absoluteMaximum)
}

func narrowBrunoRuntimeLimit(requested, fallback, operatorMaximum int64) int64 {
	if operatorMaximum <= 0 {
		operatorMaximum = fallback
	}
	if requested <= 0 {
		requested = fallback
	}
	return min(requested, operatorMaximum)
}

func stringValue(config map[string]any, key string) string {
	if config == nil {
		return ""
	}
	value, _ := config[key].(string)
	return value
}

func boolValue(config map[string]any, key string) bool {
	if config == nil {
		return false
	}
	value, _ := config[key].(bool)
	return value
}

func int64Value(config map[string]any, key string) int64 {
	if config == nil {
		return 0
	}
	return int64FromAny(config[key])
}

func int64FromAny(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		result, _ := typed.Int64()
		return result
	default:
		return 0
	}
}

func stringMapValue(value any) map[string]string {
	switch typed := value.(type) {
	case map[string]string:
		out := make(map[string]string, len(typed))
		for key, item := range typed {
			out[key] = item
		}
		return out
	case map[string]any:
		out := make(map[string]string, len(typed))
		for key, item := range typed {
			if text, ok := item.(string); ok {
				out[key] = text
			}
		}
		return out
	default:
		return nil
	}
}

func unresolvedBrunoSecretBindings(value any) []string {
	bindings := stringMapValue(value)
	if len(bindings) == 0 {
		return nil
	}
	unresolved := make([]string, 0)
	for name, binding := range bindings {
		if strings.Contains(binding, "{{secret.") {
			unresolved = append(unresolved, name)
		}
	}
	sort.Strings(unresolved)
	return unresolved
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func validatedRunnerOrigin(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("invalid Bruno runner URL %q", raw)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("Bruno runner URL must be an HTTP(S) origin without credentials, query, or fragment")
	}
	if !isLoopbackHost(parsed.Hostname()) {
		return nil, fmt.Errorf("Bruno runner URL %q must use a loopback host; remote runners are disabled until authenticated transport is available", raw)
	}
	return parsed, nil
}

func milliseconds(value float64) time.Duration {
	return time.Duration(value * float64(time.Millisecond))
}
