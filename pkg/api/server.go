package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/fileasset"
	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/importsession"
	"github.com/jasonczech/apexload/pkg/keywrap"
	"github.com/jasonczech/apexload/pkg/prometheus"
	"github.com/jasonczech/apexload/pkg/recorder"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
	"github.com/jasonczech/apexload/pkg/telemetry"
)

//go:embed static
var StaticFS embed.FS

var dashboardAssetRefPattern = regexp.MustCompile(`(?:href|src)="/(assets/[^"]+\.(?:css|js))"`)

// startupGateListener lets http.Server register its listener before startup
// performs the irreversible Local Test Login generation rotation. Accept stays
// closed until that rotation and the lifecycle publication commit together.
type startupGateListener struct {
	net.Listener
	ready     chan struct{}
	release   chan struct{}
	readyOnce sync.Once
	openOnce  sync.Once
}

func newStartupGateListener(listener net.Listener) *startupGateListener {
	return &startupGateListener{Listener: listener, ready: make(chan struct{}), release: make(chan struct{})}
}

func (l *startupGateListener) Accept() (net.Conn, error) {
	l.readyOnce.Do(func() { close(l.ready) })
	<-l.release
	return l.Listener.Accept()
}

func (l *startupGateListener) open() {
	l.openOnce.Do(func() { close(l.release) })
}

func (l *startupGateListener) abort() {
	_ = l.Listener.Close()
	l.open()
}

type desktopBrowserBootstrap struct {
	expiresAt   time.Time
	capability  string
	devViewMode string
	projectID   string
}

const maxDesktopBrowserCapabilities = 32

type Server struct {
	prometheusClient func(prometheus.Config, prometheus.Credentials) (*prometheus.Client, error)

	httpServer *http.Server
	hub        *Hub
	runner     *TestRunner
	// startupAuthSettings is the authentication configuration activated for this
	// controller process. Transitions into or out of Local Test Login are held at
	// this snapshot until restart so saving the restart-required setting cannot
	// strand the current operator before fixtures have been initialized.
	authRuntimeMu             sync.RWMutex
	startupAuthSettings       AuthSettings
	startupAuthSettingsSet    bool
	localAuthGenerationMu     sync.RWMutex
	localAuthGeneration       string
	settingsService           systemSettingsService
	archiveService            archiveService
	schedulingService         schedulingService
	executionPlanService      executionPlanService
	objectRepositoryService   objectRepositoryService
	correlationRulesetService correlationRulesetService
	scheduler                 *scenarioScheduler
	browserReplay             *BrowserReplayManager
	scriptDebug               *ScriptDebugManager
	// replaySessionReservations owns the caller-visible progress-stream ID
	// namespace across classic, browser, and script-debug replay modes. Entries
	// track active ownership or a retained terminal-ID tombstone; owner tokens
	// prevent delayed releases from erasing a newer reservation for the same ID.
	replaySessionMu           sync.Mutex
	replaySessionReservations map[string]replaySessionReservation
	replaySessionLeaseTTL     time.Duration
	browserRecording          *BrowserRecordingManager
	browserInstall            *browserInstallState
	recorder                  *recorder.Recorder
	store                     storage.Store
	importAssets              storage.ImportAssetStore
	cache                     CacheBackend
	controllerID              string
	haExecution               bool
	authoringMu               sync.Mutex
	// authoringMutationMu serializes session edits, deletion, correlation
	// write-back, and approval within one controller. The storage CAS used by
	// those handlers extends the same fence across HA replicas.
	authoringMutationMu      sync.Mutex
	authoring                map[string]*storage.AuthoringSession
	importDraftAttestationMu sync.Mutex
	// importDraftAttestations contains only credential digests and scope
	// metadata. Raw imported values remain in the operator's editable response
	// and never enter this compatibility handoff.
	importDraftAttestations map[string]*importDraftAttestation
	libraryMu               sync.Mutex
	// libraryStateMutationMu serializes the load/mutate/CAS transition for
	// remotes and synchronization links within one controller. Durable CAS still
	// provides the cross-controller fence.
	libraryStateMutationMu sync.Mutex
	// remoteConfigMu serializes snapshots of remote transport/credential
	// metadata with updates and deletion. Outbound network calls use a deep copy
	// with a pinned credential and never retain this lock.
	remoteConfigMu sync.RWMutex
	// remoteRunCreates fences the snapshot-to-cleanup-intent interval. Once the
	// cleanup intent is durable, the existing outbox check becomes the long-lived
	// transport/credential fence and this in-memory reservation is released.
	remoteRunCreates map[string]int
	// libraryArchiveImportMu protects the staged archive swap that accompanies
	// an external-result upsert so a failed retry cannot destroy the prior copy.
	libraryArchiveImportMu sync.Mutex
	libraryRemotes         map[string]*LibraryRemote
	libraryLinks           map[string]*librarySyncLink
	libraryRemotesRaw      string
	libraryLinksRaw        string
	libraryCreds           LibraryCredentialStore
	remoteCleanupRetryMu   sync.Mutex
	remoteCleanupCancel    context.CancelFunc
	remoteCleanupDone      chan struct{}
	// fleetAlertNotificationMu makes the persisted route-once state a single
	// read-modify-write transition within this controller.
	fleetAlertNotificationMu sync.Mutex
	c4Mu                     sync.Mutex
	slaWaivers               map[string][]*storage.ReleaseSLAWaiver
	resultImports            map[string]*storage.ExternalResultImport
	resultImportBytes        int64
	githubMu                 sync.Mutex
	githubTokens             map[string]githubCachedToken
	// projectMutations prevents an acknowledged project-scoped HTTP mutation
	// from being erased by deletion of that same project without stalling work
	// for unrelated projects.
	projectMutations projectMutationCoordinator
	// secretReferenceMutations fences authoritative reference snapshots against
	// script CRUD and secret create/rotation for the same project.
	secretReferenceMutations *projectMutationCoordinator
	oidcMu                   sync.Mutex
	oidcRuntimeKey           string
	oidcRuntime              *oidcRuntime
	ciAuthMu                 sync.Mutex
	ciOIDCProviders          map[string]*ciOIDCProviderRuntime
	ciOIDCExchanges          map[string]ciOIDCExchangeCacheEntry
	ciChecksCancel           context.CancelFunc
	// selfHealMu serializes replay/debug self-heal write-back. Both the browser
	// replay and script debug managers fire completion hooks into the shared
	// persistSelfHealToRepository, whose read-modify-write of the whole objects
	// blob would otherwise lose a concurrently appended candidate.
	selfHealMu sync.Mutex
	// healthSnapshot is the last health payload built from live runner state, with
	// the time it was published. The liveness probe answers from it while a durable
	// write is in flight rather than queueing behind the runner lock that write
	// holds, but only until the copy is older than
	// healthCheckDurableWriteBypassBudget; see
	// healthCheckSnapshotWithoutRunnerLock.
	healthSnapshot                    atomic.Pointer[healthCheckSnapshot]
	collectorMetrics                  *collectorMetricStore
	fleetScaler                       fleetPoolScaler
	clusterStore                      storage.ClusterConnectionStore
	cloudStore                        storage.CloudConnectionStore
	executionTargetStore              storage.ExecutionTargetStore
	capacityBackends                  capacityBackendPoolResolver
	capacityHeartbeatProbe            func(string) bool
	reverseGenerators                 *generatorrpc.ReverseRegistry
	reverseTargetMu                   sync.RWMutex
	reverseTargetSessions             map[string]map[string]struct{}
	reverseEnrollmentMu               sync.Mutex
	reverseEnrollmentNonces           map[string]time.Time
	domainTargets                     storage.DomainDeploymentTargetStore
	clusterClients                    *clusterClientCache
	clusterScalers                    clusterScalerResolver
	clusterSecretWrapper              keywrap.KeyWrapper
	clusterProbes                     *clusterProbeThrottle
	fleetProvisioningMu               sync.Mutex
	fleetProvisioningActive           map[string]string
	leadership                        *executionLeadership
	activeEndpointPublisher           ExecutionEndpointPublisher
	activeEndpointMu                  sync.RWMutex
	activeEndpointPublished           bool
	activeEndpointOwnerID             string
	activeEndpointGeneration          uint64
	activeEndpointLastAttempt         time.Time
	mux                               *http.ServeMux
	backgroundMu                      sync.Mutex
	backgroundStopping                bool
	backgroundWG                      sync.WaitGroup
	readinessSettingsMu               sync.Mutex
	readinessSettingsCadence          *readinessSettingsCadence
	usageCancel                       context.CancelFunc
	auditRetentionCancel              context.CancelFunc
	timeslotRetentionCancel           context.CancelFunc
	scheduleOccurrenceRetentionCancel context.CancelFunc
	archiveCleanupCancel              context.CancelFunc
	archiveCleanupDone                chan struct{}
	appDynamicsCancel                 context.CancelFunc
	archiveFinalizationCancel         context.CancelFunc
	archiveFinalizationWake           chan struct{}
	archiveOrphanWake                 chan struct{}
	archiveFinalizationDone           chan struct{}
	accessReviewNotifyCancel          context.CancelFunc
	authAudit                         auditThrottle
	fanoutCancel                      context.CancelFunc
	fanoutSub                         FanoutSubscription
	generatorInventoryCancel          context.CancelFunc
	generatorDiscovery                *generatorDiscovery
	discoveryCancel                   context.CancelFunc
	k8sRestart                        K8sRestartSettings
	kubeRestarter                     KubeRestarter
	kubeScaleLimitMu                  sync.Mutex
	kubeScaleLimitCache               generatorScaleLimitCache
	kubePodsCacheMu                   sync.Mutex
	kubePodsCache                     generatorPodInventoryCache
	metadataReadinessMu               sync.Mutex
	metadataReadiness                 legacyMetadataReadinessProbeState
	archiveReadinessMu                sync.Mutex
	archiveReadiness                  archiveReadinessProbeState
	archiveReadinessProbe             func() SystemReadinessCheck
	restartMu                         sync.Mutex
	restartInFlight                   map[string]struct{}
	testNotesArchiveMu                sync.Mutex
	testNotesArchiveLocks             map[string]*sync.Mutex
	metricsAddr                       string
	metricsToken                      string
	metricsServerMu                   sync.Mutex
	metricsServer                     *http.Server
	metricsServerStopped              bool
	metricsBoundAddr                  string
	desktopMode                       bool
	desktopCapability                 string
	desktopLocalSessionToken          string
	desktopBootstrapMu                sync.Mutex
	desktopBootstrapNonces            map[string]desktopBrowserBootstrap
	desktopBrowserCapabilities        map[string]desktopBrowserCapability
	desktopBrowserDownloads           map[string]desktopBrowserDownloadGrant
	desktopOIDCStates                 map[string]desktopOIDCStateGrant
	desktopGitHubStates               map[string]desktopGitHubStateGrant
	bridgeMode                        bool
	recordingBridge                   *BrowserRecordingBridge
	desktopAppData                    string
	desktopLogPath                    string
	importAssetDir                    string
	brunoImportMu                     sync.Mutex
	brunoImportSessions               map[string]*brunoImportSession
	brunoArtifactMu                   sync.Mutex
	brunoArtifacts                    *importartifact.EncryptedStore
	// brunoArtifactKeyCustody records which envelope key actually opened the
	// store. Without it, caching the store would make every later custody
	// report claim operator-managed custody regardless of the key in use.
	brunoArtifactKeyCustody         string
	importSessions                  *importsession.Manager
	brunoArtifactDir                string
	brunoArtifactWrapper            keywrap.KeyWrapper
	brunoSessionCleanupCancel       context.CancelFunc
	grafanaSessionCleanupCancel     context.CancelFunc
	projectSecretStore              secretstore.Store
	applicationSecretStore          secretstore.Store
	protectedSourceSessions         *secretstore.ProtectedSourceSessionStore
	sourceSecretMu                  sync.Mutex
	sourceSecretSessions            map[string]*sourceSecretSession
	sourceSecretSessionLifetime     time.Duration
	sourceSecretCleanupCancel       context.CancelFunc
	ciRunCreateMu                   sync.Mutex
	ciRunCreates                    map[string]int
	beforeCIRunMaterialize          func(CreateTestRequest)
	afterCIRunMaterialize           func(*ManagedTest)
	afterLibraryImportClaimsPersist func()
	bindScope                       string
	boundAddr                       string
	boundAddrMu                     sync.RWMutex
	lifecycleMu                     sync.Mutex
	startupContext                  context.Context
	startupCancel                   context.CancelFunc
	startupStarted                  bool
	startupDone                     chan struct{}
	startupDoneOnce                 sync.Once
	shutdownStarted                 bool
	shutdownDone                    chan struct{}
	shutdownErr                     error
	// staticFS is the dashboard asset filesystem probed by readiness checks.
	// It defaults to the compile-time embedded StaticFS; tests override it so
	// the readiness suite does not depend on a prior `npm run build`.
	staticFS fs.FS
	// tracing is the process startup result, not merely the presence of OTel
	// libraries. Integration readiness must never advertise an inactive exporter.
	tracing telemetry.Status
}

// ServerConfig configures API server startup behavior and integrations.
type ServerConfig struct {
	Addr string
	// BindScope is "loopback" or "wide" for the API listener. When omitted it
	// is inferred from Addr.
	BindScope string
	// MetricsAddr, when set (e.g. ":9091"), serves GET /metrics on its own
	// listener instead of the API port.
	MetricsAddr  string
	MetricsToken string
	// ProxyAddr configures the HTTP(S) recording proxy listener. Desktop mode
	// defaults an omitted value to an ephemeral loopback port; hosted mode keeps
	// the recorder package's documented default.
	ProxyAddr string
	// ProxyAuthToken is a recorder-only bearer capability. It must not reuse the
	// desktop API capability. Desktop mode requires an explicit high-entropy value
	// of at least 32 bytes; hosted mode retains the
	// APEXLOAD_RECORDER_PROXY_TOKEN fallback.
	ProxyAuthToken string
	// RecorderCACertPath and RecorderCAKeyPath locate the persistent interception
	// CA. Desktop shells should place both below their private application-data
	// directory so launch working directories cannot redirect CA persistence.
	RecorderCACertPath string
	RecorderCAKeyPath  string
	MaxConcurrent      int
	AllowedOrigins     []string
	Store              storage.Store
	Cache              CacheBackend
	LibraryCredentials LibraryCredentialStore
	GeneratorTargets   []string
	// EnableDynamicGenerators selects the distributed execution backend even
	// when no static targets are configured. Reverse PaaS sessions then become
	// live run targets as they authenticate with the gateway.
	EnableDynamicGenerators bool
	// PaaSComputeEnabled is an explicit operator opt-in for provider mutations.
	// Registration, validation, and UX remain available while disabled.
	PaaSComputeEnabled           bool
	GeneratorTimeout             time.Duration
	GeneratorRunSupervisionTTL   time.Duration
	GeneratorRunSupervisionRenew time.Duration
	K8sRestart                   K8sRestartSettings
	KubeRestarter                KubeRestarter
	DesktopMode                  bool
	// DesktopCapability is a high-entropy, per-launch bearer capability required
	// by every /api/v1 request when DesktopMode is enabled. When omitted, NewServer
	// generates one so desktop mode can never accidentally expose an unauthenticated
	// loopback API. Native shells should pass an explicit value and expose it only
	// through their privileged renderer bridge.
	DesktopCapability  string
	BridgeMode         bool
	DesktopAppDataPath string
	DesktopLogPath     string
	ImportAssetDir     string
	// BrunoArtifactDir stores encrypted, content-addressed Bruno source
	// objects. BrunoArtifactWrapper may inject a KMS/test wrapper; when omitted,
	// Key Vault or an operator/local-keychain key is resolved lazily.
	BrunoArtifactDir     string
	BrunoArtifactWrapper keywrap.KeyWrapper
	// ProjectSecretStore optionally injects a project-scoped writable provider.
	// When omitted, Key Vault or encrypted database storage is configured from
	// the environment and the artifact wrapping key.
	ProjectSecretStore     secretstore.Store
	ApplicationSecretStore secretstore.Store
	// HAExecutionLeadership enables leader election so that, when multiple
	// controller replicas run against a shared database, only the lease holder
	// performs test execution. Requires a store that supports scheduler leases
	// (SQLite or PostgreSQL). Off by default (single-active controller).
	HAExecutionLeadership bool
	// ActiveEndpointPublisher maintains the selectorless Kubernetes Service used
	// for leader-only execution/CI traffic. It is configured only for HA
	// Kubernetes deployments; ordinary read traffic continues to use the
	// all-replica Service.
	ActiveEndpointPublisher ExecutionEndpointPublisher
	// TracingStatus reports whether process-level OTLP export was successfully
	// initialized. The zero value is the deliberately disabled default.
	TracingStatus telemetry.Status
}

// scriptRecordingLeadershipRepairStore is intentionally optional so test and
// alternate Store implementations do not need to know about SQLite/PostgreSQL
// rolling-upgrade repair. Production stores implement it directly.
type scriptRecordingLeadershipRepairStore interface {
	RepairLegacyScriptRecordings() error
}

func (s *Server) repairScriptRecordingsOnLeadershipAcquire() error {
	if s == nil || s.store == nil {
		return nil
	}
	repairStore, ok := s.store.(scriptRecordingLeadershipRepairStore)
	if !ok {
		return nil
	}
	return repairStore.RepairLegacyScriptRecordings()
}

// NewServer wires the HTTP server, test runner, websocket hub, recorder, and routes.
func NewServer(cfg ServerConfig) *Server {
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}
	cfg.BindScope = strings.TrimSpace(cfg.BindScope)
	if cfg.BindScope == "" {
		cfg.BindScope = config.ClassifyBindScope(cfg.Addr)
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 10
	}
	if cfg.LibraryCredentials == nil {
		cfg.LibraryCredentials = NewEnvLibraryCredentialStore()
	}
	desktopCapability := strings.TrimSpace(cfg.DesktopCapability)
	if cfg.DesktopMode && desktopCapability == "" {
		generated, err := generateDesktopSecret()
		if err != nil {
			panic(fmt.Sprintf("generate desktop API capability: %v", err))
		}
		desktopCapability = generated
	}

	hub := NewHub()
	hub.SetAllowedOrigins(cfg.AllowedOrigins)
	cache := cfg.Cache
	if cache == nil {
		cache = configuredCacheBackend()
	}
	controllerID := controllerInstanceID()
	if cacheBackendIsRemote(cache.Info().Backend) {
		hub.SetFanout(cache, controllerID, cache.Info().FanoutChannel)
	}
	// Under HA leader election, build the coordinator BEFORE the runner so its
	// not-yet-leader state suppresses boot reconciliation; the eventual leader
	// reconciles via reloadAndReconcile on acquisition.
	var leadership *executionLeadership
	var runnerOpts []TestRunnerOption
	if cfg.HAExecutionLeadership {
		if leaseStore, ok := cfg.Store.(executionLeaseStore); ok && leaseStore != nil {
			leadership = newExecutionLeadership(leaseStore, controllerID)
			runnerOpts = append(runnerOpts, WithExecutionLeadership(leadership))
		} else {
			slog.Warn("HA execution leadership requested but the store does not support scheduler leases; running single-active")
		}
	}
	staticTargets, discoverySpecs := splitGeneratorTargets(cfg.GeneratorTargets)
	var distributedBackend *distributedExecutionBackend
	if cfg.EnableDynamicGenerators || len(staticTargets) > 0 || len(discoverySpecs) > 0 {
		distributedBackend = newDistributedExecutionBackend(staticTargets, cfg.GeneratorTimeout, nil)
		distributedBackend.enableRunSupervision(cfg.GeneratorRunSupervisionTTL, cfg.GeneratorRunSupervisionRenew)
		distributedBackend.ApplyGeneratorDrainStates(loadGeneratorDrainStates(cfg.Store))
		runnerOpts = append(runnerOpts, WithExecutionBackend(distributedBackend))
	}
	projectSecretStore := cfg.ProjectSecretStore
	var records storage.ProjectSecretCipherStore
	if candidate, ok := cfg.Store.(storage.ProjectSecretCipherStore); ok {
		records = candidate
	}
	wrapper := cfg.BrunoArtifactWrapper
	if wrapper == nil && records != nil && projectSecretStore == nil {
		if configured, wrapperErr := keywrap.NewConfiguredKeyWrapper(cfg.DesktopMode); wrapperErr == nil {
			wrapper = configured
		} else {
			slog.Warn("encrypted project secret store is unavailable", "error", wrapperErr)
		}
	}
	if projectSecretStore == nil {
		if configured, secretErr := secretstore.NewConfigured(records, wrapper); secretErr == nil {
			projectSecretStore = configured
		} else {
			slog.Warn("project secret store configuration is unavailable", "error", secretErr)
			projectSecretStore = secretstore.Chain{Stores: []secretstore.Store{secretstore.EnvironmentStore{}}}
		}
	}
	applicationSecretStore := cfg.ApplicationSecretStore
	if applicationSecretStore == nil {
		var db *storage.SQLiteStore
		switch records := cfg.Store.(type) {
		case *storage.SQLiteStore:
			db = records
		case *storage.PostgresStore:
			db = records.SQLiteStore
		}
		if db != nil {
			if configured, secretErr := secretstore.NewConfigured(storage.ApplicationSecretStorage{SQLiteStore: db}, wrapper); secretErr == nil {
				applicationSecretStore = configured
			} else {
				slog.Warn("application secret store configuration is unavailable", "error", secretErr)
			}
		}
	}
	if applicationSecretStore == nil {
		applicationSecretStore = secretstore.Chain{Stores: []secretstore.Store{secretstore.EnvironmentStore{}}}
	}
	var protectedSourceSessions *secretstore.ProtectedSourceSessionStore
	if protectedRecords, ok := cfg.Store.(storage.ProtectedSourceSecretSessionStore); ok && protectedRecords != nil && wrapper != nil {
		protectedSourceSessions = &secretstore.ProtectedSourceSessionStore{Records: protectedRecords, Wrapper: wrapper}
	}
	secretReferenceMutations := &projectMutationCoordinator{}
	secretReferenceMutations.configureDurableLease(cfg.Store)
	runnerOpts = append(runnerOpts,
		WithProjectSecretStore(projectSecretStore),
		WithApplicationSecretStore(applicationSecretStore),
		withSecretReferenceMutationCoordinator(secretReferenceMutations),
		WithRunContinuityKeyWrapper(wrapper),
	)
	runner := NewTestRunner(cfg.MaxConcurrent, hub, cfg.Store, runnerOpts...)
	runner.setLiveStateStore(cache)
	startupAuthSettings := normalizeAuthSettings(runner.GetSystemSettings().Auth)
	var discovery *generatorDiscovery
	if distributedBackend != nil && len(discoverySpecs) > 0 {
		discovery = newGeneratorDiscovery(staticTargets, discoverySpecs, distributedBackend)
	}
	proxyAddr := strings.TrimSpace(cfg.ProxyAddr)
	if cfg.DesktopMode && proxyAddr == "" {
		proxyAddr = "127.0.0.1:0"
	}
	rec := recorder.NewWithOptions(recorder.Options{
		ProxyAddr:        proxyAddr,
		ProxyAuthToken:   cfg.ProxyAuthToken,
		CACertPath:       cfg.RecorderCACertPath,
		CAKeyPath:        cfg.RecorderCAKeyPath,
		RequireProxyAuth: cfg.DesktopMode,
		RequireLoopback:  cfg.DesktopMode,
	})
	browserRecording := NewBrowserRecordingManager(hub)
	browserRecording.SetHARArtifactDir(runner.GetSystemSettings().ArchiveRetention.HARArtifactDir)

	scheduler := newScenarioScheduler(runner)
	startupCtx, startupCancel := context.WithCancel(context.Background())
	s := &Server{
		hub:                         hub,
		runner:                      runner,
		startupAuthSettings:         startupAuthSettings,
		startupAuthSettingsSet:      true,
		settingsService:             newTestRunnerSystemSettingsService(runner),
		archiveService:              newTestRunnerArchiveService(runner, cfg.Store),
		schedulingService:           newTestRunnerSchedulingService(runner, scheduler),
		executionPlanService:        newTestRunnerExecutionPlanService(runner, cfg.Store),
		objectRepositoryService:     newTestRunnerObjectRepositoryService(runner, cfg.Store),
		correlationRulesetService:   newTestRunnerCorrelationRulesetService(runner, cfg.Store),
		scheduler:                   scheduler,
		browserReplay:               NewBrowserReplayManager(hub),
		scriptDebug:                 NewScriptDebugManager(hub),
		replaySessionReservations:   make(map[string]replaySessionReservation),
		replaySessionLeaseTTL:       defaultReplaySessionLeaseTTL,
		browserRecording:            browserRecording,
		browserInstall:              newBrowserInstallState(),
		recorder:                    rec,
		store:                       cfg.Store,
		cache:                       cache,
		controllerID:                controllerID,
		haExecution:                 cfg.HAExecutionLeadership,
		authoring:                   make(map[string]*storage.AuthoringSession),
		importDraftAttestations:     make(map[string]*importDraftAttestation),
		libraryRemotes:              make(map[string]*LibraryRemote),
		libraryLinks:                make(map[string]*librarySyncLink),
		libraryCreds:                cfg.LibraryCredentials,
		slaWaivers:                  make(map[string][]*storage.ReleaseSLAWaiver),
		resultImports:               make(map[string]*storage.ExternalResultImport),
		githubTokens:                make(map[string]githubCachedToken),
		ciOIDCProviders:             make(map[string]*ciOIDCProviderRuntime),
		ciOIDCExchanges:             make(map[string]ciOIDCExchangeCacheEntry),
		collectorMetrics:            newCollectorMetricStore(),
		fleetProvisioningActive:     make(map[string]string),
		leadership:                  leadership,
		activeEndpointPublisher:     cfg.ActiveEndpointPublisher,
		mux:                         http.NewServeMux(),
		desktopMode:                 cfg.DesktopMode,
		desktopCapability:           desktopCapability,
		desktopBootstrapNonces:      make(map[string]desktopBrowserBootstrap),
		desktopBrowserCapabilities:  make(map[string]desktopBrowserCapability),
		desktopBrowserDownloads:     make(map[string]desktopBrowserDownloadGrant),
		desktopOIDCStates:           make(map[string]desktopOIDCStateGrant),
		desktopGitHubStates:         make(map[string]desktopGitHubStateGrant),
		bridgeMode:                  cfg.BridgeMode,
		desktopAppData:              cfg.DesktopAppDataPath,
		desktopLogPath:              cfg.DesktopLogPath,
		importAssetDir:              resolveImportAssetDir(cfg),
		brunoImportSessions:         make(map[string]*brunoImportSession),
		brunoArtifactDir:            strings.TrimSpace(cfg.BrunoArtifactDir),
		brunoArtifactWrapper:        cfg.BrunoArtifactWrapper,
		projectSecretStore:          projectSecretStore,
		applicationSecretStore:      applicationSecretStore,
		secretReferenceMutations:    secretReferenceMutations,
		protectedSourceSessions:     protectedSourceSessions,
		sourceSecretSessions:        make(map[string]*sourceSecretSession),
		sourceSecretSessionLifetime: sourceSecretSessionTTL,
		bindScope:                   cfg.BindScope,

		generatorDiscovery:      discovery,
		k8sRestart:              normalizeK8sRestartSettings(cfg.K8sRestart),
		kubeRestarter:           cfg.KubeRestarter,
		restartInFlight:         make(map[string]struct{}),
		testNotesArchiveLocks:   make(map[string]*sync.Mutex),
		metricsAddr:             strings.TrimSpace(cfg.MetricsAddr),
		metricsToken:            strings.TrimSpace(cfg.MetricsToken),
		archiveFinalizationWake: make(chan struct{}, 1),
		archiveOrphanWake:       make(chan struct{}, 1),
		startupContext:          startupCtx,
		startupDone:             make(chan struct{}),
		startupCancel:           startupCancel,
		shutdownDone:            make(chan struct{}),
		staticFS:                StaticFS,
		tracing:                 cfg.TracingStatus,
	}
	s.configureReplaySessionReservations()
	runner.appDynamicsPrepare = s.verifyPreparedAppDynamics
	runner.prometheusPrepare = s.verifyPreparedPrometheus
	hub.SetOriginAuthProvider(s.authSettingsForRequest)
	if leadership != nil {
		leadership.setOnLoss(func() {
			s.clearActiveExecutionEndpoint()
			// The epoch cancellation stops the scan; closing a slow directory
			// stream must not delay generator teardown on lease loss.
			go runner.closeOrphanArchiveScanner()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := runner.teardownActiveTestsAfterLeadershipLoss(ctx); err != nil {
				slog.Error("failed to tear down stale runtimes after execution leadership loss", "err", err)
			}
		})
		leadership.setOnAcquire(func() {
			for leadership.IsLeader() {
				if runner.runAdoptionEnabled() {
					s.publishExecutionEndpoint(true)
				}
				if err := s.reloadGeneratorDrainStatesOnLeadershipAcquire(); err != nil {
					slog.Warn("generator drain-state leadership refresh failed; admission remains closed", "error", err)
				} else if err := s.repairScriptRecordingsOnLeadershipAcquire(); err != nil {
					slog.Warn("script recording leadership repair failed; admission remains closed", "error", err)
				} else if err := runner.reloadAndReconcile(); err != nil {
					slog.Warn("execution leadership recovery failed; admission remains closed", "error", err)
				} else {
					ctx, cancel := leadership.operationContext(context.Background())
					err := s.reconcileProvisioningRequests(ctx)
					if err == nil {
						err = s.reconcileOrphanedCapacity(ctx)
					}
					cancel()
					if err == nil {
						return
					}
					if leadership.IsLeader() {
						slog.Warn("provisioning leadership recovery failed; admission remains closed", "error", err)
					}
				}
				if !leadership.IsLeader() {
					return
				}
				ctx, cancel := leadership.operationContext(context.Background())
				timer := time.NewTimer(250 * time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
					cancel()
					return
				case <-timer.C:
					cancel()
				}
			}
		})
		leadership.setOnAdmit(func() {
			// Restore adopted runtimes before publishing normal execution readiness.
			runner.resumeAdoptedRuns()
			s.publishActiveExecutionEndpoint()
			s.launchBackground(func() {
				runner.ReconcilePendingTests()
				s.wakeArchiveFinalizationRetry()
			})
		})
	}
	if err := fileasset.RegisterProjectRoot(s.importAssetDir); err != nil {
		slog.Warn("invalid import asset store; file assets will be unavailable", "dir", s.importAssetDir, "error", err)
	}

	// Select the fleet provisioner backend (in-cluster Kubernetes scaler when
	// enabled + selected, else the unconfigured default → not_configured).
	// This stays the fallback for pools with no cluster binding, which is every
	// pool that predates the cluster registry.
	s.fleetScaler = newFleetPoolScaler(s.kubeRestarter, s.k8sRestart)

	// Administrator-registered cluster connections. The key wrapper is shared
	// with the encrypted project-secret store; when it is unconfigured, cluster
	// credentials cannot be sealed and the write handlers fail closed with 503.
	if candidate, ok := cfg.Store.(storage.ClusterConnectionStore); ok {
		s.clusterStore = candidate
	}
	if candidate, ok := cfg.Store.(storage.CloudConnectionStore); ok {
		s.cloudStore = candidate
	}
	if candidate, ok := cfg.Store.(storage.ExecutionTargetStore); ok {
		s.executionTargetStore = candidate
	}
	if candidate, ok := cfg.Store.(storage.ImportAssetStore); ok {
		s.importAssets = candidate
	}
	// Per-domain deployment targets are another optional store capability, so
	// older Store implementations and test doubles keep compiling.
	if candidate, ok := cfg.Store.(storage.DomainDeploymentTargetStore); ok {
		s.domainTargets = candidate
	}
	s.clusterSecretWrapper = wrapper
	s.clusterClients = newClusterClientCache(s.clusterSecretWrapper, s.clusterDialGuard())
	s.clusterProbes = &clusterProbeThrottle{lastRun: make(map[string]time.Time)}
	s.clusterScalers = &registryFleetScalerResolver{
		store:   s.clusterStore,
		clients: s.clusterClients,
		// Read the field rather than capturing it: tests and any later
		// reconfiguration replace s.fleetScaler after construction, and a
		// captured value would silently keep using the stale scaler.
		fallback:      func() fleetPoolScaler { return s.fleetScaler },
		quotaChecker:  s,
		domainTargets: s.domainTargets,
	}
	s.capacityBackends = &capacityBackendResolver{
		enabled: cfg.PaaSComputeEnabled, targets: s.executionTargetStore, clouds: s.cloudStore, controllerID: s.controllerID, server: s,
	}
	s.reverseGenerators = generatorrpc.DefaultReverseRegistry
	s.reverseTargetSessions = make(map[string]map[string]struct{})
	s.configureImportAssetObjectStore()

	// Apply a persisted metadata cordon to the live drain state when a generator
	// (re)connects, so a cordon issued while the generator was offline takes effect.
	if distributedBackend != nil {
		distributedBackend.setOnIdentityRecorded(func(generatorID string) {
			s.applyMetadataCordonOnReconnect(distributedBackend, generatorID)
		})
		distributedBackend.setCapacityLifecycle(s.ensureRunCapacity, s.releaseRunCapacity)
	}

	// Feed replay selector self-heals back to the bound object repository.
	s.browserReplay.setCompletionHook(s.persistSelfHealToRepository)
	s.scriptDebug.setCompletionHook(s.persistSelfHealToRepository)
	runner.setTerminalObserver(s.handleCIRunTerminal)

	// Recover browser-recording publication before this Server can be observed.
	// Any replica may adopt terminal committed claims; HA replicas take an
	// uncommitted staged set only after atomically winning its expired lease.
	// Other single-controller direct imports can be compensated immediately,
	// while HA retains their ordinary lease fence and publication snapshot.
	if err := s.recoverStagedBrowserRecordingConversionsAtStartup(); err != nil {
		// Do not compensate anything when adoption inspection itself failed: the
		// durable claim fence keeps those rows hidden until a later healthy startup
		// can distinguish a complete browser save from an incomplete transaction.
		slog.Warn("failed to recover staged browser recording during construction; preserving import claims", "error", err)
	} else if err := s.recoverAbandonedDirectImportsAtStartup(); err != nil {
		slog.Warn("failed to recover abandoned direct import during construction", "error", err)
	}
	runner.setImportPublicationSnapshot(s.store, s.pendingDirectImportAssets)

	s.loadLibraryState()
	s.routes(cfg.AllowedOrigins)

	handler := bodyLimitMiddleware(s.mux)
	if s.desktopCapability != "" {
		handler = desktopAuthMaterialMiddleware(s.desktopOIDCSessionForCapability, s.desktopLocalSessionForCapability, s.desktopDevPreviewForCapability)(handler)
		handler = desktopCapabilityMiddleware(s.desktopCapability, cfg.AllowedOrigins, s.validDesktopBrowserCapability)(handler)
	}
	s.httpServer = &http.Server{
		Addr:           cfg.Addr,
		Handler:        telemetry.HTTPMiddleware(securityHeadersMiddleware(handler)),
		ReadTimeout:    15 * time.Second,
		WriteTimeout:   60 * time.Second,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20, // 1 MiB
	}

	return s
}

// authSettingsForRequest returns the authentication configuration activated for
// this controller process. Local Test Login is deliberately restart-required:
// entering it needs fixture initialization, while leaving it must not silently
// drop the active login gate. Other authentication modes retain their existing
// live-update behavior.
func (s *Server) authSettingsForRequest() AuthSettings {
	if s == nil || s.runner == nil {
		return normalizeAuthSettings(AuthSettings{})
	}
	return s.authSettingsForRuntime(s.runner.GetSystemSettings().Auth)
}

// authSettingsForRuntime applies the restart-required local-auth rule to an
// already resolved settings snapshot. Readiness uses this path so its auth row
// does not independently refresh durable settings.
func (s *Server) authSettingsForRuntime(current AuthSettings) AuthSettings {
	current = normalizeAuthSettings(current)
	s.authRuntimeMu.RLock()
	startup := s.startupAuthSettings
	captured := s.startupAuthSettingsSet
	s.authRuntimeMu.RUnlock()
	if !captured {
		return current
	}
	startup = normalizeAuthSettings(startup)
	if startup.Mode == authModeLocal || current.Mode == authModeLocal {
		return startup
	}
	return current
}

// captureStartupAuthSettings snapshots the already-refreshed runner state
// immediately before listeners are started. NewServer also initializes it so
// handler-only tests and embedded callers that do not invoke Start observe the
// same semantics.
func (s *Server) captureStartupAuthSettings() {
	if s == nil || s.runner == nil {
		return
	}
	s.runner.mu.RLock()
	settings := normalizeAuthSettings(s.runner.settings.Auth)
	s.runner.mu.RUnlock()
	s.authRuntimeMu.Lock()
	s.startupAuthSettings = settings
	s.startupAuthSettingsSet = true
	s.authRuntimeMu.Unlock()
}

func (s *Server) routes(allowedOrigins []string) {
	cors := corsMiddleware(allowedOrigins, s.authSettingsForRequest)
	log := loggingMiddleware
	baseAuth := authMiddleware(s.authSettingsForRequest, s.store, s.cache, withAuthAuditRecorder(s), withLocalAuthGeneration(s.activeLocalAuthGeneration))
	auth := s.projectMutationCoordinatedAuth(baseAuth)
	standardRouteMiddleware := apiRouteMiddlewareStack{cors: cors, log: log, auth: auth}
	ciAuth := s.ciAuthMiddleware(baseAuth)
	ciMutationAuth := s.projectMutationCoordinatedAuth(ciAuth)
	recorderViewAccess := mustSpecializedRouteAccessProfile("recorderViewAuth")
	recorderCreateAccess := mustSpecializedRouteAccessProfile("recorderCreateAuth")
	recorderSessionViewAccess := mustSpecializedRouteAccessProfile("recorderSessionViewAuth")
	recorderSessionEditAccess := mustSpecializedRouteAccessProfile("recorderSessionEditAuth")
	browserReplaySessionViewAccess := mustSpecializedRouteAccessProfile("browserReplaySessionViewAuth")
	browserReplaySessionEditAccess := mustSpecializedRouteAccessProfile("browserReplaySessionEditAuth")
	scriptDebugSessionViewAccess := mustSpecializedRouteAccessProfile("scriptDebugSessionViewAuth")
	scriptDebugSessionEditAccess := mustSpecializedRouteAccessProfile("scriptDebugSessionEditAuth")
	browserRecordingCreateAccess := mustSpecializedRouteAccessProfile("browserRecordingCreateAuth")
	browserRecordingSessionViewAccess := mustSpecializedRouteAccessProfile("browserRecordingSessionViewAuth")
	browserRecordingSessionEditAccess := mustSpecializedRouteAccessProfile("browserRecordingSessionEditAuth")
	recorderViewAuth := s.projectMutationCoordinatedAuth(projectScopedAuthMiddleware(
		s.authSettingsForRequest,
		s.store,
		s.cache,
		recorderViewAccess.GlobalRoles,
		recorderViewAccess.ProjectRoles,
		resolveRecorderListProjectID,
		withAuthAuditRecorder(s),
		withLocalAuthGeneration(s.activeLocalAuthGeneration),
	))
	recorderCreateAuth := s.projectMutationCoordinatedAuth(projectScopedAuthMiddleware(
		s.authSettingsForRequest,
		s.store,
		s.cache,
		recorderCreateAccess.GlobalRoles,
		recorderCreateAccess.ProjectRoles,
		resolveRecorderCreateProjectID,
		withAuthAuditRecorder(s),
		withLocalAuthGeneration(s.activeLocalAuthGeneration),
	))
	recorderSessionViewAuth := s.projectMutationCoordinatedAuth(projectScopedAuthMiddleware(
		s.authSettingsForRequest,
		s.store,
		s.cache,
		recorderSessionViewAccess.GlobalRoles,
		recorderSessionViewAccess.ProjectRoles,
		resolveRecorderSessionProjectID(s.recorder),
		withAuthAuditRecorder(s),
		withLocalAuthGeneration(s.activeLocalAuthGeneration),
	))
	recorderSessionEditAuth := s.projectMutationCoordinatedAuth(projectScopedAuthMiddleware(
		s.authSettingsForRequest,
		s.store,
		s.cache,
		recorderSessionEditAccess.GlobalRoles,
		recorderSessionEditAccess.ProjectRoles,
		resolveRecorderSessionProjectID(s.recorder),
		withAuthAuditRecorder(s),
		withLocalAuthGeneration(s.activeLocalAuthGeneration),
	))
	browserReplaySessionViewAuth := s.projectMutationCoordinatedAuth(projectScopedAuthMiddleware(
		s.authSettingsForRequest,
		s.store,
		s.cache,
		browserReplaySessionViewAccess.GlobalRoles,
		browserReplaySessionViewAccess.ProjectRoles,
		resolveBrowserReplaySessionProjectID(s.browserReplay),
		withAuthAuditRecorder(s),
		withLocalAuthGeneration(s.activeLocalAuthGeneration),
	))
	browserReplaySessionEditAuth := s.projectMutationCoordinatedAuth(projectScopedAuthMiddleware(
		s.authSettingsForRequest,
		s.store,
		s.cache,
		browserReplaySessionEditAccess.GlobalRoles,
		browserReplaySessionEditAccess.ProjectRoles,
		resolveBrowserReplaySessionProjectID(s.browserReplay),
		withAuthAuditRecorder(s),
		withLocalAuthGeneration(s.activeLocalAuthGeneration),
	))
	scriptDebugSessionViewAuth := s.projectMutationCoordinatedAuth(projectScopedAuthMiddleware(
		s.authSettingsForRequest,
		s.store,
		s.cache,
		scriptDebugSessionViewAccess.GlobalRoles,
		scriptDebugSessionViewAccess.ProjectRoles,
		resolveScriptDebugSessionProjectID(s.scriptDebug),
		withAuthAuditRecorder(s),
		withLocalAuthGeneration(s.activeLocalAuthGeneration),
	))
	scriptDebugSessionEditAuth := s.projectMutationCoordinatedAuth(projectScopedAuthMiddleware(
		s.authSettingsForRequest,
		s.store,
		s.cache,
		scriptDebugSessionEditAccess.GlobalRoles,
		scriptDebugSessionEditAccess.ProjectRoles,
		resolveScriptDebugSessionProjectID(s.scriptDebug),
		withAuthAuditRecorder(s),
		withLocalAuthGeneration(s.activeLocalAuthGeneration),
	))
	browserRecordingCreateAuth := s.projectMutationCoordinatedAuth(projectScopedAuthMiddleware(
		s.authSettingsForRequest,
		s.store,
		s.cache,
		browserRecordingCreateAccess.GlobalRoles,
		browserRecordingCreateAccess.ProjectRoles,
		resolveRecorderCreateProjectID,
		withAuthAuditRecorder(s),
		withLocalAuthGeneration(s.activeLocalAuthGeneration),
	))
	browserRecordingSessionViewAuth := s.projectMutationCoordinatedAuth(projectScopedAuthMiddleware(
		s.authSettingsForRequest,
		s.store,
		s.cache,
		browserRecordingSessionViewAccess.GlobalRoles,
		browserRecordingSessionViewAccess.ProjectRoles,
		resolveBrowserRecordingSessionProjectID(s),
		withAuthAuditRecorder(s),
		withLocalAuthGeneration(s.activeLocalAuthGeneration),
	))
	browserRecordingSessionEditAuth := s.projectMutationCoordinatedAuth(projectScopedAuthMiddleware(
		s.authSettingsForRequest,
		s.store,
		s.cache,
		browserRecordingSessionEditAccess.GlobalRoles,
		browserRecordingSessionEditAccess.ProjectRoles,
		resolveBrowserRecordingSessionProjectID(s),
		withAuthAuditRecorder(s),
		withLocalAuthGeneration(s.activeLocalAuthGeneration),
	))
	recorderViewAuth = s.domainFeatureScopedAuth(recorderViewAuth, "script_builder.browser_recording", resolveRecorderListProjectID)
	recorderCreateAuth = s.domainFeatureScopedAuth(recorderCreateAuth, "script_builder.browser_recording", resolveRecorderCreateProjectID)
	recorderSessionViewAuth = s.domainFeatureScopedAuth(recorderSessionViewAuth, "script_builder.browser_recording", resolveRecorderSessionProjectID(s.recorder))
	recorderSessionEditAuth = s.domainFeatureScopedAuth(recorderSessionEditAuth, "script_builder.browser_recording", resolveRecorderSessionProjectID(s.recorder))
	browserRecordingCreateAuth = s.domainFeatureScopedAuth(browserRecordingCreateAuth, "script_builder.browser_recording", resolveRecorderCreateProjectID)
	browserRecordingSessionViewAuth = s.domainFeatureScopedAuth(browserRecordingSessionViewAuth, "script_builder.browser_recording", resolveBrowserRecordingSessionProjectID(s))
	browserRecordingSessionEditAuth = s.domainFeatureScopedAuth(browserRecordingSessionEditAuth, "script_builder.browser_recording", resolveBrowserRecordingSessionProjectID(s))

	// Controller-native GitHub Actions discovery, authentication, and run API.
	s.mux.Handle("GET /.well-known/apexload-ci", cors(log(http.HandlerFunc(s.handleCIWellKnown))))
	s.mux.Handle("POST /api/v1/auth/ci/github/exchange", cors(log(http.HandlerFunc(s.handleGitHubCIExchange))))
	s.mux.Handle("POST /api/v1/ci/runs", cors(log(ciMutationAuth(http.HandlerFunc(s.handleCreateCIRun)))))
	s.mux.Handle("GET /api/v1/ci/runs/{id}", cors(log(ciAuth(http.HandlerFunc(s.handleGetCIRun)))))
	s.mux.Handle("POST /api/v1/ci/runs/{id}/abort", cors(log(ciMutationAuth(http.HandlerFunc(s.handleAbortCIRun)))))

	// Test lifecycle and archive endpoints.
	s.mux.Handle("POST /api/v1/domains", cors(log(auth(http.HandlerFunc(s.handleCreateDomain)))))
	s.mux.Handle("GET /api/v1/domains", cors(log(auth(http.HandlerFunc(s.handleListDomains)))))
	s.mux.Handle("GET /api/v1/domains/{id}", cors(log(auth(http.HandlerFunc(s.handleGetDomain)))))
	s.mux.Handle("GET /api/v1/domains/{id}/features", cors(log(auth(http.HandlerFunc(s.handleGetDomainFeatures)))))
	s.mux.Handle("PUT /api/v1/domains/{id}/features", cors(log(auth(http.HandlerFunc(s.handleUpdateDomainFeatures)))))
	s.mux.Handle("PUT /api/v1/domains/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateDomain)))))
	s.mux.Handle("DELETE /api/v1/domains/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteDomain)))))
	s.mux.Handle("GET /api/v1/domains/{id}/application-repository", cors(log(auth(http.HandlerFunc(s.handleGetApplicationRepository)))))
	s.mux.Handle("PUT /api/v1/domains/{id}/application-repository", cors(log(auth(http.HandlerFunc(s.handleUpdateApplicationRepository)))))
	s.mux.Handle("GET /api/v1/domains/{id}/application-repository/categories", cors(log(auth(http.HandlerFunc(s.handleListRepositoryCategories)))))
	s.mux.Handle("POST /api/v1/domains/{id}/application-repository/categories", cors(log(auth(http.HandlerFunc(s.handleCreateRepositoryCategory)))))
	s.mux.Handle("PUT /api/v1/application-repository/categories/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateRepositoryCategory)))))
	s.mux.Handle("DELETE /api/v1/application-repository/categories/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteRepositoryCategory)))))

	s.mux.Handle("POST /api/v1/applications", cors(log(auth(http.HandlerFunc(s.handleCreateApplication)))))
	s.mux.Handle("GET /api/v1/applications", cors(log(auth(http.HandlerFunc(s.handleListApplications)))))
	s.mux.Handle("GET /api/v1/applications/{id}", cors(log(auth(http.HandlerFunc(s.handleGetApplication)))))
	s.mux.Handle("PUT /api/v1/applications/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateApplication)))))
	s.mux.Handle("DELETE /api/v1/applications/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteApplication)))))
	s.mux.Handle("GET /api/v1/applications/{id}/secrets", cors(log(auth(http.HandlerFunc(s.handleListApplicationSecrets)))))
	s.mux.Handle("POST /api/v1/applications/{id}/secrets", cors(log(auth(http.HandlerFunc(s.handleCreateApplicationSecrets)))))
	s.mux.Handle("POST /api/v1/applications/{id}/secrets/status", cors(log(auth(http.HandlerFunc(s.handleApplicationSecretStatus)))))
	s.mux.Handle("GET /api/v1/applications/{id}/secrets/{name}/usage", cors(log(auth(http.HandlerFunc(s.handleApplicationSecretUsage)))))
	s.mux.Handle("PUT /api/v1/applications/{id}/secrets/{name}", cors(log(auth(http.HandlerFunc(s.handleRotateApplicationSecret)))))
	s.mux.Handle("GET /api/v1/applications/{id}/load-comparison", cors(log(auth(http.HandlerFunc(s.handleGetApplicationLoadComparison)))))
	s.mux.Handle("POST /api/v1/applications/{id}/releases", cors(log(auth(http.HandlerFunc(s.handleCreateRelease)))))
	s.mux.Handle("GET /api/v1/applications/{id}/releases", cors(log(auth(http.HandlerFunc(s.handleListReleases)))))
	s.mux.Handle("GET /api/v1/releases/{id}", cors(log(auth(http.HandlerFunc(s.handleGetRelease)))))
	s.mux.Handle("PUT /api/v1/releases/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateRelease)))))
	s.mux.Handle("POST /api/v1/releases/{id}/archive", cors(log(auth(http.HandlerFunc(s.handleArchiveRelease)))))
	s.mux.Handle("GET /api/v1/releases/{id}/trend", cors(log(auth(http.HandlerFunc(s.handleGetReleaseTrend)))))
	s.mux.Handle("GET /api/v1/releases/{id}/trend/export", cors(log(auth(http.HandlerFunc(s.handleGetReleaseTrendExport)))))
	s.mux.Handle("GET /api/v1/releases/{id}/sla-governance", cors(log(auth(http.HandlerFunc(s.handleGetReleaseSLAGovernance)))))
	s.mux.Handle("POST /api/v1/releases/{id}/sla-governance/waivers", cors(log(auth(http.HandlerFunc(s.handleCreateReleaseSLAWaiver)))))
	s.mux.Handle("DELETE /api/v1/releases/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteRelease)))))

	s.mux.Handle("POST /api/v1/projects", cors(log(auth(http.HandlerFunc(s.handleCreateProject)))))
	s.mux.Handle("GET /api/v1/projects", cors(log(auth(http.HandlerFunc(s.handleListProjects)))))
	s.mux.Handle("GET /api/v1/projects/{id}", cors(log(auth(http.HandlerFunc(s.handleGetProject)))))
	s.mux.Handle("PUT /api/v1/projects/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateProject)))))
	s.mux.Handle("DELETE /api/v1/projects/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteProject)))))
	s.mux.Handle("GET /api/v1/projects/{id}/secrets", cors(log(auth(http.HandlerFunc(s.handleListProjectSecrets)))))
	s.mux.Handle("GET /api/v1/projects/{id}/secrets/{name}/usage", cors(log(auth(http.HandlerFunc(s.handleProjectSecretUsage)))))
	s.mux.Handle("POST /api/v1/projects/{id}/secrets/status", cors(log(auth(http.HandlerFunc(s.handleProjectSecretStatus)))))
	s.mux.Handle("POST /api/v1/projects/{id}/secrets", cors(log(auth(http.HandlerFunc(s.handleCreateProjectSecrets)))))
	s.mux.Handle("PUT /api/v1/projects/{id}/secrets/{name}", cors(log(auth(http.HandlerFunc(s.handleRotateProjectSecret)))))
	s.mux.Handle("POST /api/v1/projects/{id}/monitoring/elk-connections", cors(log(auth(http.HandlerFunc(s.handleCreateELKConnection)))))
	s.mux.Handle("GET /api/v1/projects/{id}/monitoring/elk-connections", cors(log(auth(http.HandlerFunc(s.handleListELKConnections)))))
	s.mux.Handle("GET /api/v1/projects/{id}/monitoring/elk-connections/{connection_id}", cors(log(auth(http.HandlerFunc(s.handleGetELKConnection)))))
	s.mux.Handle("PUT /api/v1/projects/{id}/monitoring/elk-connections/{connection_id}", cors(log(auth(http.HandlerFunc(s.handleUpdateELKConnection)))))
	s.mux.Handle("DELETE /api/v1/projects/{id}/monitoring/elk-connections/{connection_id}", cors(log(auth(http.HandlerFunc(s.handleDeleteELKConnection)))))
	s.mux.Handle("POST /api/v1/projects/{id}/monitoring/elk-connections/{connection_id}/test", cors(log(auth(http.HandlerFunc(s.handleTestELKConnection)))))
	s.mux.Handle("POST /api/v1/projects/{id}/monitoring/dynatrace-connections", cors(log(auth(http.HandlerFunc(s.handleCreateDynatraceConnection)))))
	s.mux.Handle("GET /api/v1/projects/{id}/monitoring/dynatrace-connections", cors(log(auth(http.HandlerFunc(s.handleListDynatraceConnections)))))
	s.mux.Handle("GET /api/v1/projects/{id}/monitoring/dynatrace-connections/{connection_id}", cors(log(auth(http.HandlerFunc(s.handleGetDynatraceConnection)))))
	s.mux.Handle("PUT /api/v1/projects/{id}/monitoring/dynatrace-connections/{connection_id}", cors(log(auth(http.HandlerFunc(s.handleUpdateDynatraceConnection)))))
	s.mux.Handle("DELETE /api/v1/projects/{id}/monitoring/dynatrace-connections/{connection_id}", cors(log(auth(http.HandlerFunc(s.handleDeleteDynatraceConnection)))))
	s.mux.Handle("POST /api/v1/projects/{id}/monitoring/dynatrace-connections/{connection_id}/test", cors(log(auth(http.HandlerFunc(s.handleTestDynatraceConnection)))))
	s.mux.Handle("GET /api/v1/projects/{id}/github/policy", cors(log(auth(http.HandlerFunc(s.handleGetProjectGitHubPolicy)))))
	s.mux.Handle("PUT /api/v1/projects/{id}/github/policy", cors(log(auth(http.HandlerFunc(s.handleUpdateProjectGitHubPolicy)))))
	s.mux.Handle("POST /api/v1/projects/{id}/releases", cors(log(auth(http.HandlerFunc(s.handleCreateProjectRelease)))))
	s.mux.Handle("GET /api/v1/projects/{id}/releases", cors(log(auth(http.HandlerFunc(s.handleListProjectReleases)))))
	s.mux.Handle("POST /api/v1/projects/{pid}/folders", cors(log(auth(http.HandlerFunc(s.handleCreateFolder)))))
	s.mux.Handle("GET /api/v1/projects/{pid}/folders", cors(log(auth(http.HandlerFunc(s.handleListFolders)))))
	s.mux.Handle("PUT /api/v1/folders/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateFolder)))))
	s.mux.Handle("DELETE /api/v1/folders/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteFolder)))))

	s.mux.Handle("POST /api/v1/tests", cors(log(auth(http.HandlerFunc(s.handleCreateTest)))))
	s.mux.Handle("POST /api/v1/tests/estimate", cors(log(auth(http.HandlerFunc(s.handleEstimateTest)))))
	s.mux.Handle("POST /api/v1/tests/preflight", cors(log(auth(http.HandlerFunc(s.handlePreflightTest)))))
	s.mux.Handle("GET /api/v1/tests", cors(log(auth(http.HandlerFunc(s.handleListTests)))))
	s.mux.Handle("GET /api/v1/tests/{id}", cors(log(auth(http.HandlerFunc(s.handleGetTest)))))
	s.mux.Handle("GET /api/v1/tests/{id}/manifest", cors(log(auth(http.HandlerFunc(s.handleGetTestManifest)))))
	s.mux.Handle("GET /api/v1/tests/{id}/sla-status", cors(log(auth(http.HandlerFunc(s.handleGetTestSLAStatus)))))
	s.mux.Handle("GET /api/v1/tests/{id}/report", cors(log(auth(http.HandlerFunc(s.handleGetTestHTMLReport)))))
	s.mux.Handle("GET /api/v1/tests/{id}/report/annotations", cors(log(auth(http.HandlerFunc(s.handleGetReportAnnotations)))))
	s.mux.Handle("PUT /api/v1/tests/{id}/report/annotations", cors(log(auth(http.HandlerFunc(s.handleSaveReportAnnotations)))))
	s.mux.Handle("GET /api/v1/tests/{id}/notes", cors(log(auth(http.HandlerFunc(s.handleListTestNotes)))))
	s.mux.Handle("POST /api/v1/tests/{id}/notes", cors(log(auth(http.HandlerFunc(s.handleCreateTestNote)))))
	s.mux.Handle("PATCH /api/v1/tests/{id}/notes/{note_id}", cors(log(auth(http.HandlerFunc(s.handleUpdateTestNote)))))
	s.mux.Handle("DELETE /api/v1/tests/{id}/notes/{note_id}", cors(log(auth(http.HandlerFunc(s.handleDeleteTestNote)))))
	s.mux.Handle("POST /api/v1/tests/{id}/start", cors(log(auth(http.HandlerFunc(s.handleStartTest)))))
	s.mux.Handle("POST /api/v1/tests/{id}/comparison-baseline/pin", cors(log(auth(http.HandlerFunc(s.handlePinComparisonBaseline)))))
	s.mux.Handle("DELETE /api/v1/tests/{id}/comparison-baseline/pin", cors(log(auth(http.HandlerFunc(s.handleUnpinComparisonBaseline)))))
	s.mux.Handle("POST /api/v1/tests/{id}/stop", cors(log(auth(http.HandlerFunc(s.handleStopTest)))))
	s.mux.Handle("POST /api/v1/tests/{id}/abort", cors(log(auth(http.HandlerFunc(s.handleAbortTest)))))
	s.mux.Handle("POST /api/v1/tests/{id}/pause", cors(log(auth(http.HandlerFunc(s.handlePauseTest)))))
	s.mux.Handle("POST /api/v1/tests/{id}/resume", cors(log(auth(http.HandlerFunc(s.handleResumeTest)))))
	s.mux.Handle("PATCH /api/v1/tests/{id}/vusers", cors(log(auth(http.HandlerFunc(s.handleScaleVUsers)))))
	s.mux.Handle("GET /api/v1/tests/{id}/groups/{group}/runtime-settings", cors(log(auth(http.HandlerFunc(s.handleGetTestGroupRuntimeSettings)))))
	s.mux.Handle("PATCH /api/v1/tests/{id}/groups/{group}/runtime-settings", cors(log(auth(http.HandlerFunc(s.handleUpdateTestGroupRuntimeSettings)))))
	s.mux.Handle("GET /api/v1/tests/{id}/groups/{group}/parameters", cors(log(auth(http.HandlerFunc(s.handleGetTestGroupParameters)))))
	s.mux.Handle("PUT /api/v1/tests/{id}/groups/{group}/parameters", cors(log(auth(http.HandlerFunc(s.handleUpdateTestGroupParameters)))))
	s.mux.Handle("PATCH /api/v1/tests/{id}/goal-targets", cors(log(auth(http.HandlerFunc(s.handleUpdateGoalTarget)))))
	s.mux.Handle("PATCH /api/v1/tests/{id}/duration", cors(log(auth(http.HandlerFunc(s.handleAdjustDuration)))))
	s.mux.Handle("POST /api/v1/tests/{id}/runtime-adjustments/preview", cors(log(auth(http.HandlerFunc(s.handlePreviewRuntimeAdjustment)))))
	s.mux.Handle("PATCH /api/v1/tests/{id}/runtime-adjustments", cors(log(auth(http.HandlerFunc(s.handleApplyRuntimeAdjustment)))))
	s.mux.Handle("DELETE /api/v1/tests/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteTest)))))
	s.mux.Handle("PATCH /api/v1/tests/{id}/move", cors(log(auth(http.HandlerFunc(s.handleMoveTest)))))
	s.mux.Handle("GET /api/v1/tests/{id}/metrics", cors(log(auth(http.HandlerFunc(s.handleGetMetrics)))))
	s.mux.Handle("GET /api/v1/tests/{id}/errors/evidence", cors(log(auth(http.HandlerFunc(s.handleGetLiveErrorEvidence)))))
	s.mux.Handle("GET /api/v1/tests/{id}/transactions/live/{event_id}", cors(log(auth(http.HandlerFunc(s.handleGetLiveTransactionDetail)))))
	s.registerAPIRouteCatalog(runArchiveAPIRouteCatalog, standardRouteMiddleware)
	s.registerAPIRouteCatalog(archiveRetentionAPIRouteCatalog, standardRouteMiddleware)
	s.registerAPIRouteCatalog(powerBIAPIRouteCatalog, standardRouteMiddleware)
	s.registerAPIRouteCatalog(confluenceAPIRouteCatalog, standardRouteMiddleware)

	// Reusable scenario definitions.
	s.mux.Handle("POST /api/v1/scenarios", cors(log(auth(http.HandlerFunc(s.handleCreateScenario)))))
	s.mux.Handle("GET /api/v1/scenarios", cors(log(auth(http.HandlerFunc(s.handleListScenarios)))))
	s.mux.Handle("POST /api/v1/scenarios/validate-load-model", cors(log(auth(http.HandlerFunc(s.handleValidateScenarioLoadModel)))))
	s.mux.Handle("POST /api/v1/scenarios/size-from-goal", cors(log(auth(http.HandlerFunc(s.handleSizeScenarioFromGoal)))))
	s.mux.Handle("GET /api/v1/scenarios/traffic-profile/sources", cors(log(auth(http.HandlerFunc(s.handleTrafficProfileSources)))))
	s.mux.Handle("POST /api/v1/scenarios/traffic-profile/analyze", cors(log(auth(http.HandlerFunc(s.handleAnalyzeTrafficProfile)))))
	s.mux.Handle("POST /api/v1/scenarios/traffic-profile/derive", cors(log(auth(http.HandlerFunc(s.handleDeriveTrafficProfile)))))
	s.mux.Handle("POST /api/v1/scenarios/csv-driven/analyze", cors(log(auth(http.HandlerFunc(s.handleAnalyzeCSVDriven)))))
	s.mux.Handle("POST /api/v1/scenarios/csv-driven/derive", cors(log(auth(http.HandlerFunc(s.handleDeriveCSVDriven)))))
	s.mux.Handle("GET /api/v1/scenarios/{id}", cors(log(auth(http.HandlerFunc(s.handleGetScenario)))))
	s.mux.Handle("PUT /api/v1/scenarios/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateScenario)))))
	s.mux.Handle("DELETE /api/v1/scenarios/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteScenario)))))
	s.mux.Handle("GET /api/v1/scenarios/load-models/support-matrix", cors(log(auth(http.HandlerFunc(s.handleLoadModelSupportMatrix)))))
	s.mux.Handle("GET /api/v1/scenarios/load-models/templates", cors(log(auth(http.HandlerFunc(s.handleLoadModelTemplates)))))
	s.registerAPIRouteCatalog(scenarioSchedulingAPIRouteCatalog, standardRouteMiddleware)
	s.registerAPIRouteCatalog(baselineComparisonAPIRouteCatalog, standardRouteMiddleware)
	s.registerAPIRouteCatalog(runMonitoringAPIRouteCatalog, standardRouteMiddleware)
	s.registerAPIRouteCatalog(appDynamicsAPIRouteCatalog, standardRouteMiddleware)
	s.registerAPIRouteCatalog(prometheusAPIRouteCatalog, standardRouteMiddleware)
	s.registerAPIRouteCatalog(analysisAPIRouteCatalog, standardRouteMiddleware)
	s.registerAPIRouteCatalog(objectActivityAPIRouteCatalog, standardRouteMiddleware)
	s.mux.Handle("GET /api/v1/scenarios/{id}/results", cors(log(auth(http.HandlerFunc(s.handleScenarioResults)))))
	s.mux.Handle("GET /api/v1/scenarios/{id}/runtime-settings/report", cors(log(auth(http.HandlerFunc(s.handleGetScenarioRuntimeSettingsReport)))))
	s.mux.Handle("POST /api/v1/scenarios/{id}/runtime/apply-to-groups", cors(log(auth(http.HandlerFunc(s.handleApplyScenarioRuntimeToGroups)))))
	s.mux.Handle("GET /api/v1/scenarios/{id}/data-pools", cors(log(auth(http.HandlerFunc(s.handleListScenarioDataPools)))))
	s.mux.Handle("PUT /api/v1/scenarios/{id}/data-pools", cors(log(auth(http.HandlerFunc(s.handleUpdateScenarioDataPools)))))

	// Shared data pools used across scripts in a scenario run.
	s.mux.Handle("POST /api/v1/data-pools", cors(log(auth(http.HandlerFunc(s.handleCreateDataPool)))))
	s.mux.Handle("GET /api/v1/data-pools", cors(log(auth(http.HandlerFunc(s.handleListDataPools)))))
	s.mux.Handle("GET /api/v1/data-pools/{id}", cors(log(auth(http.HandlerFunc(s.handleGetDataPool)))))
	s.mux.Handle("PUT /api/v1/data-pools/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateDataPool)))))
	s.mux.Handle("DELETE /api/v1/data-pools/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteDataPool)))))
	s.mux.Handle("GET /api/v1/data-pools/{id}/rows", cors(log(auth(http.HandlerFunc(s.handleListDataPoolRows)))))
	s.mux.Handle("GET /api/v1/data-pools/{id}/rows/export", cors(log(auth(http.HandlerFunc(s.handleExportDataPoolRows)))))
	s.mux.Handle("POST /api/v1/data-pools/{id}/rows", cors(log(auth(http.HandlerFunc(s.handleCreateDataPoolRow)))))
	s.mux.Handle("POST /api/v1/data-pools/{id}/rows/import", cors(log(auth(http.HandlerFunc(s.handleImportDataPoolRows)))))
	s.mux.Handle("PUT /api/v1/data-pools/{id}/rows/{row_id}", cors(log(auth(http.HandlerFunc(s.handleUpdateDataPoolRow)))))
	s.mux.Handle("DELETE /api/v1/data-pools/{id}/rows/{row_id}", cors(log(auth(http.HandlerFunc(s.handleDeleteDataPoolRow)))))

	// Capacity reservations (timeslots) — forward-booking of future capacity windows.
	s.registerAPIRouteCatalog(capacityAPIRouteCatalog, standardRouteMiddleware)

	// Execution plans — ordered cross-run orchestration (LRE test-set analog).
	s.registerAPIRouteCatalog(executionPlanAPIRouteCatalog, standardRouteMiddleware)
	s.registerAPIRouteCatalog(objectRepositoryAPIRouteCatalog, standardRouteMiddleware)

	// Correlation rulesets — learned, reusable correlation rule catalogs.
	s.registerAPIRouteCatalog(correlationRulesetAPIRouteCatalog, standardRouteMiddleware)

	// Monitoring collectors — restricted-network agent control plane.
	s.mux.Handle("POST /api/v1/monitoring/collectors", cors(log(auth(http.HandlerFunc(s.handleCreateMonitoringCollector)))))
	s.mux.Handle("GET /api/v1/monitoring/threshold-incidents", cors(log(auth(http.HandlerFunc(s.handleListThresholdIncidents)))))
	s.mux.Handle("PATCH /api/v1/monitoring/threshold-incidents/{id}/ack", cors(log(auth(http.HandlerFunc(s.handleAcknowledgeThresholdIncident)))))
	s.mux.Handle("GET /api/v1/monitoring/collectors", cors(log(auth(http.HandlerFunc(s.handleListMonitoringCollectors)))))
	s.mux.Handle("GET /api/v1/monitoring/collectors/{id}", cors(log(auth(http.HandlerFunc(s.handleGetMonitoringCollector)))))
	s.mux.Handle("PATCH /api/v1/monitoring/collectors/{id}", cors(log(auth(http.HandlerFunc(s.handlePatchMonitoringCollector)))))
	s.mux.Handle("DELETE /api/v1/monitoring/collectors/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteMonitoringCollector)))))
	s.mux.Handle("POST /api/v1/monitoring/collectors/{id}/rotate-token", cors(log(auth(http.HandlerFunc(s.handleRotateMonitoringCollectorToken)))))
	// Agent-facing heartbeat — collector-token authenticated in-handler (no RBAC auth middleware).
	s.mux.Handle("POST /api/v1/monitoring/collectors/{id}/heartbeat", cors(log(http.HandlerFunc(s.handleCollectorHeartbeat))))
	s.mux.Handle("GET /api/v1/monitoring/collectors/{id}/metrics", cors(log(auth(http.HandlerFunc(s.handleGetCollectorMetrics)))))

	// Enterprise entitlements — read-only consumption vs limits.
	s.registerAPIRouteCatalog(systemEntitlementsAPIRouteCatalog, standardRouteMiddleware)
	s.mux.Handle("GET /api/v1/projects/{id}/entitlements/usage", cors(log(auth(http.HandlerFunc(s.handleGetProjectEntitlementsUsage)))))

	// Script CRUD, validation, import, and export endpoints.
	s.mux.Handle("POST /api/v1/scripts", cors(log(auth(http.HandlerFunc(s.handleCreateScript)))))
	s.mux.Handle("POST /api/v1/scripts/secret-bindings/apply", cors(log(auth(http.HandlerFunc(s.handleApplyScriptSecretBindings)))))
	s.mux.Handle("POST /api/v1/scripts/secret-references/rebind", cors(log(auth(http.HandlerFunc(s.handleRebindScriptSecretReference)))))
	s.mux.Handle("POST /api/v1/scripts/import/secrets/migrate", cors(log(auth(http.HandlerFunc(s.handleMigrateSourceSecrets)))))
	s.mux.Handle("POST /api/v1/scripts/import/secrets/discard", cors(log(auth(http.HandlerFunc(s.handleDiscardSourceSecretSession)))))
	s.mux.Handle("GET /api/v1/scripts", cors(log(auth(http.HandlerFunc(s.handleListScripts)))))
	s.mux.Handle("GET /api/v1/scripts/{id}", cors(log(auth(http.HandlerFunc(s.handleGetScript)))))
	s.mux.Handle("GET /api/v1/scripts/{id}/secret-status", cors(log(auth(http.HandlerFunc(s.handleScriptSecretStatus)))))
	s.mux.Handle("POST /api/v1/scripts/{id}/clone-draft", cors(log(auth(http.HandlerFunc(s.handleCloneScriptDraft)))))
	s.mux.Handle("PUT /api/v1/scripts/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateScript)))))
	s.mux.Handle("DELETE /api/v1/scripts/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteScript)))))
	s.mux.Handle("PUT /api/v1/scripts/{id}/library-assignment", cors(log(auth(http.HandlerFunc(s.handleScriptLibraryAssignment)))))
	s.mux.Handle("PUT /api/v1/scenarios/{id}/library-assignment", cors(log(auth(http.HandlerFunc(s.handleScenarioLibraryAssignment)))))
	s.mux.Handle("PATCH /api/v1/scripts/{id}/move", cors(log(auth(http.HandlerFunc(s.handleMoveScript)))))
	// Protected import source, revision history, and rollback. These serve every
	// format that retains durable source, not just Bruno, so the canonical paths
	// are format-neutral. The /bruno-imports/ paths are retained as permanent
	// aliases: they are a published API surface, and released dashboards and the
	// desktop client still request them. Both spellings must resolve to the same
	// handler with the same authorization -- see TestImportRouteAliasesMatch.
	s.mux.Handle("GET /api/v1/scripts/{id}/imports/source", cors(log(auth(http.HandlerFunc(s.handleGetImportSource)))))
	s.mux.Handle("GET /api/v1/scripts/{id}/imports/revisions", cors(log(auth(http.HandlerFunc(s.handleListImportSourceRevisions)))))
	s.mux.Handle("POST /api/v1/scripts/{id}/imports/rollback", cors(log(auth(http.HandlerFunc(s.handleRollbackImportSource)))))
	// The bruno-imports/* paths predate source retention generalizing to every
	// importer. They stay as aliases for existing clients.
	s.mux.Handle("GET /api/v1/scripts/{id}/bruno-imports/source", cors(log(auth(http.HandlerFunc(s.handleGetImportSource)))))
	s.mux.Handle("GET /api/v1/scripts/{id}/bruno-imports/revisions", cors(log(auth(http.HandlerFunc(s.handleListImportSourceRevisions)))))
	s.mux.Handle("POST /api/v1/scripts/{id}/bruno-imports/rollback", cors(log(auth(http.HandlerFunc(s.handleRollbackImportSource)))))
	s.mux.Handle("POST /api/v1/scripts/{id}/bruno-projection/preview", cors(log(auth(http.HandlerFunc(s.handlePreviewBrunoProjection)))))
	s.mux.Handle("POST /api/v1/scripts/{id}/bruno-projection/commit", cors(log(auth(http.HandlerFunc(s.handleCommitBrunoProjection)))))
	s.mux.Handle("POST /api/v1/scripts/validate", cors(log(auth(http.HandlerFunc(s.handleValidateDraftScript)))))
	s.mux.Handle("POST /api/v1/scripts/replay", cors(log(auth(http.HandlerFunc(s.handleReplayDraftScript)))))
	s.mux.Handle("POST /api/v1/scripts/replay/sessions", cors(log(auth(http.HandlerFunc(s.handleStartReplaySessionDraftScript)))))
	s.mux.Handle("POST /api/v1/scripts/debug/sessions", cors(log(auth(http.HandlerFunc(s.handleStartScriptDebugDraftScript)))))
	s.mux.Handle("POST /api/v1/scripts/browser-replay/sessions", cors(log(auth(http.HandlerFunc(s.handleStartBrowserReplayDraftScript)))))
	s.mux.Handle("POST /api/v1/scripts/browser-to-http/preview", cors(log(auth(http.HandlerFunc(s.handlePreviewBrowserToHTTP)))))
	s.mux.Handle("GET /api/v1/scripts/authoring/capabilities", cors(log(auth(http.HandlerFunc(s.handleAuthoringCapabilities)))))
	s.mux.Handle("GET /api/v1/scripts/authoring/templates", cors(log(auth(http.HandlerFunc(s.handleAuthoringTemplates)))))
	s.mux.Handle("GET /api/v1/scripts/authoring/components", cors(log(auth(http.HandlerFunc(s.handleAuthoringComponents)))))
	s.mux.Handle("POST /api/v1/scripts/authoring/components", cors(log(auth(http.HandlerFunc(s.handleCreateReusableBlock)))))
	s.mux.Handle("DELETE /api/v1/scripts/authoring/components/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteReusableBlock)))))
	s.mux.Handle("POST /api/v1/scripts/authoring/sessions", cors(log(auth(http.HandlerFunc(s.handleCreateAuthoringSession)))))
	s.mux.Handle("GET /api/v1/scripts/authoring/sessions", cors(log(auth(http.HandlerFunc(s.handleListAuthoringSessions)))))
	s.mux.Handle("GET /api/v1/scripts/authoring/sessions/{id}", cors(log(auth(http.HandlerFunc(s.handleGetAuthoringSession)))))
	s.mux.Handle("PUT /api/v1/scripts/authoring/sessions/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateAuthoringSession)))))
	s.mux.Handle("DELETE /api/v1/scripts/authoring/sessions/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteAuthoringSession)))))
	s.mux.Handle("POST /api/v1/scripts/authoring/sessions/{id}/approve", cors(log(auth(http.HandlerFunc(s.handleApproveAuthoringSession)))))
	s.mux.Handle("POST /api/v1/scripts/authoring/sessions/{id}/correlations/preview", cors(log(auth(http.HandlerFunc(s.handlePreviewAuthoringCorrelation)))))
	s.mux.Handle("GET /api/v1/correlations/rules", cors(log(auth(http.HandlerFunc(s.handleListCorrelationRules)))))
	s.mux.Handle("GET /api/v1/dsl/actions", cors(log(auth(http.HandlerFunc(s.handleListDSLActions)))))
	s.mux.Handle("POST /api/v1/dsl/parameter-preview", cors(log(auth(http.HandlerFunc(s.handlePreviewParameter)))))
	s.mux.Handle("GET /api/v1/dsl/config-defaults", cors(log(auth(http.HandlerFunc(s.handleConfigDefaults)))))
	s.mux.Handle("GET /api/v1/dsl/browser-emulation-catalog", cors(log(auth(http.HandlerFunc(s.handleBrowserEmulationCatalog)))))
	s.mux.Handle("POST /api/v1/correlations/diagnose", cors(log(auth(http.HandlerFunc(s.handleDiagnoseCorrelations)))))
	s.mux.Handle("POST /api/v1/correlations/diff", cors(log(auth(http.HandlerFunc(s.handleDiffCorrelations)))))
	s.mux.Handle("POST /api/v1/selectors/diagnose", cors(log(auth(http.HandlerFunc(s.handleDiagnoseSelectors)))))
	s.mux.Handle("POST /api/v1/scripts/{id}/validate", cors(log(auth(http.HandlerFunc(s.handleValidateScript)))))
	s.mux.Handle("GET /api/v1/scripts/{id}/recording/status", cors(log(auth(http.HandlerFunc(s.handleGetScriptRecordingStatus)))))
	s.mux.Handle("POST /api/v1/scripts/{id}/diagnose-replay", cors(log(auth(http.HandlerFunc(s.handleDiagnoseScriptReplay)))))
	s.mux.Handle("POST /api/v1/scripts/{id}/replay", cors(log(auth(http.HandlerFunc(s.handleReplayScript)))))
	s.mux.Handle("POST /api/v1/scripts/{id}/replay/sessions", cors(log(auth(http.HandlerFunc(s.handleStartReplaySessionScript)))))
	s.mux.Handle("POST /api/v1/scripts/{id}/debug/sessions", cors(log(auth(http.HandlerFunc(s.handleStartScriptDebugScript)))))
	s.mux.Handle("POST /api/v1/scripts/{id}/browser-replay/sessions", cors(log(auth(http.HandlerFunc(s.handleStartBrowserReplayScript)))))
	s.mux.Handle("POST /api/v1/scripts/{id}/export", cors(log(auth(http.HandlerFunc(s.handleExportScript)))))
	s.mux.Handle("GET /api/v1/scripts/{id}/download", cors(log(auth(http.HandlerFunc(s.handleDownloadScript)))))
	s.mux.Handle("GET /api/v1/scripts/{id}/runtime-settings/report", cors(log(auth(http.HandlerFunc(s.handleGetScriptRuntimeSettingsReport)))))
	s.mux.Handle("POST /api/v1/scripts/import/enterprise-script-archive", cors(log(auth(http.HandlerFunc(s.handleImportEnterpriseArchive)))))
	s.mux.Handle("POST /api/v1/scripts/import/enterprise-script-archive/save", cors(log(auth(http.HandlerFunc(s.handleSaveEnterpriseArchiveImport)))))
	s.mux.Handle("POST /api/v1/scripts/import/repairs/preview", cors(log(auth(http.HandlerFunc(s.handlePreviewImportRepair)))))
	s.mux.Handle("POST /api/v1/scripts/import/repairs/apply", cors(log(auth(http.HandlerFunc(s.handleApplyImportRepair)))))
	s.mux.Handle("POST /api/v1/scripts/import/commit-batch", cors(log(auth(http.HandlerFunc(s.handleCommitGenericImportBatch)))))
	s.mux.Handle("POST /api/v1/scripts/import/enterprise-script-archive/validate", cors(log(auth(http.HandlerFunc(s.handleValidateEnterpriseArchiveImport)))))
	s.mux.Handle("GET /api/v1/scripts/import/enterprise-script-archive/support-matrix", cors(log(auth(http.HandlerFunc(s.handleEnterpriseArchiveSupportMatrix)))))
	s.mux.Handle("POST /api/v1/scripts/import/jmeter", cors(log(auth(http.HandlerFunc(s.handleImportJMeter)))))
	s.mux.Handle("POST /api/v1/scripts/import/jmeter/save", cors(log(auth(http.HandlerFunc(s.handleSaveJMeterImport)))))
	s.mux.Handle("POST /api/v1/scripts/import/jmeter/validate", cors(log(auth(http.HandlerFunc(s.handleValidateJMeterImport)))))
	s.mux.Handle("GET /api/v1/scripts/import/jmeter/support-matrix", cors(log(auth(http.HandlerFunc(s.handleJMeterSupportMatrix)))))
	s.mux.Handle("GET /api/v1/scripts/web-api/support-matrix", cors(log(auth(http.HandlerFunc(s.handleWebAPIExecutionSupportMatrix)))))
	s.mux.Handle("POST /api/v1/scripts/import/sample", cors(log(auth(http.HandlerFunc(s.handleImportSample)))))
	s.mux.Handle("POST /api/v1/scripts/import/{id}/assets/upload", cors(log(auth(http.HandlerFunc(s.handleUploadImportAsset)))))
	s.mux.Handle("POST /api/v1/scripts/import/{id}/java-classpath/upload", cors(log(auth(http.HandlerFunc(s.handleUploadJavaClasspathJar)))))
	s.mux.Handle("POST /api/v1/scripts/import/k6", cors(log(auth(http.HandlerFunc(s.handleImportK6)))))
	s.mux.Handle("POST /api/v1/scripts/export/k6", cors(log(auth(http.HandlerFunc(s.handleExportK6)))))
	s.mux.Handle("GET /api/v1/scripts/import/k6/support-matrix", cors(log(auth(http.HandlerFunc(s.handleK6SupportMatrix)))))
	s.mux.Handle("POST /api/v1/scripts/import/devweb", cors(log(auth(http.HandlerFunc(s.handleImportDevWeb)))))
	s.mux.Handle("GET /api/v1/scripts/import/devweb/support-matrix", cors(log(auth(http.HandlerFunc(s.handleDevWebSupportMatrix)))))
	s.mux.Handle("GET /api/v1/scripts/import/oracle-ebusiness/support-matrix", cors(log(auth(http.HandlerFunc(s.handleOracleEBusinessSupportMatrix)))))
	s.mux.Handle("POST /api/v1/scripts/import/oracle-ebusiness/review", cors(log(auth(http.HandlerFunc(s.handleReviewOracleEBusinessImport)))))
	s.mux.Handle("POST /api/v1/scripts/import/har", cors(log(auth(http.HandlerFunc(s.handleImportHAR)))))
	s.mux.Handle("POST /api/v1/scripts/import/salesforce", cors(log(auth(http.HandlerFunc(s.handleImportSalesforce)))))
	s.mux.Handle("POST /api/v1/scripts/import/recorder", cors(log(auth(http.HandlerFunc(s.handleImportRecorder)))))
	s.mux.Handle("POST /api/v1/scripts/import/recorder/validate", cors(log(auth(http.HandlerFunc(s.handleValidateRecorderImport)))))
	s.mux.Handle("POST /api/v1/scripts/import/recorder/replay", cors(log(auth(http.HandlerFunc(s.handleReplayRecorderImport)))))
	s.mux.Handle("POST /api/v1/scripts/import/browser-recorder/attest", cors(log(auth(http.HandlerFunc(s.handleAttestBridgedBrowserRecordingImport)))))
	s.mux.Handle("POST /api/v1/scripts/import/openapi", cors(log(auth(http.HandlerFunc(s.handleImportOpenAPI)))))
	s.mux.Handle("POST /api/v1/scripts/import/openapi/commit", cors(log(auth(http.HandlerFunc(s.handleCommitOpenAPIImport)))))
	s.mux.Handle("GET /api/v1/scripts/import/openapi/support-matrix", cors(log(auth(http.HandlerFunc(s.handleOpenAPISupportMatrix)))))
	s.mux.Handle("POST /api/v1/scripts/import/bruno/inspect", cors(log(auth(http.HandlerFunc(s.handleInspectBrunoImport)))))
	s.mux.Handle("GET /api/v1/scripts/import/bruno/sessions/{id}/source", cors(log(auth(http.HandlerFunc(s.handleInspectBrunoImportSource)))))
	s.mux.Handle("POST /api/v1/scripts/import/bruno/commit", cors(log(auth(http.HandlerFunc(s.handleCommitBrunoImport)))))
	s.mux.Handle("GET /api/v1/scripts/import/bruno/support-matrix", cors(log(auth(http.HandlerFunc(s.handleBrunoSupportMatrix)))))
	s.mux.Handle("POST /api/v1/scripts/import/cloudtest", cors(log(auth(http.HandlerFunc(s.handleImportCloudTest)))))
	s.mux.Handle("GET /api/v1/scripts/import/cloudtest/support-matrix", cors(log(auth(http.HandlerFunc(s.handleCloudTestSupportMatrix)))))
	s.mux.Handle("POST /api/v1/scripts/import/raml", cors(log(auth(http.HandlerFunc(s.handleImportRAML)))))
	s.mux.Handle("GET /api/v1/scripts/import/raml/support-matrix", cors(log(auth(http.HandlerFunc(s.handleRAMLSupportMatrix)))))
	s.mux.Handle("POST /api/v1/scripts/import/postman", cors(log(auth(http.HandlerFunc(s.handleImportPostman)))))
	s.mux.Handle("POST /api/v1/scripts/import/postman/validate", cors(log(auth(http.HandlerFunc(s.handleValidatePostmanImport)))))
	s.mux.Handle("POST /api/v1/scripts/import/postman/save", cors(log(auth(http.HandlerFunc(s.handleSavePostmanImport)))))
	s.mux.Handle("GET /api/v1/scripts/import/postman/support-matrix", cors(log(auth(http.HandlerFunc(s.handlePostmanSupportMatrix)))))
	s.mux.Handle("POST /api/v1/scripts/import/wsdl", cors(log(auth(http.HandlerFunc(s.handleImportWSDL)))))
	s.mux.Handle("POST /api/v1/scripts/import/asyncapi", cors(log(auth(http.HandlerFunc(s.handleImportAsyncAPI)))))
	s.mux.Handle("POST /api/v1/scripts/import/asyncapi/commit", cors(log(auth(http.HandlerFunc(s.handleCommitAsyncAPIImport)))))
	s.mux.Handle("POST /api/v1/scripts/import/asyncapi/commit-batch", cors(log(auth(http.HandlerFunc(s.handleCommitAsyncAPIImportBatch)))))
	s.mux.Handle("GET /api/v1/scripts/import/asyncapi/support-matrix", cors(log(auth(http.HandlerFunc(s.handleAsyncAPISupportMatrix)))))
	s.mux.Handle("POST /api/v1/scripts/import/eventhubs-capture", cors(log(auth(http.HandlerFunc(s.handleImportEventHubsCapture)))))
	s.mux.Handle("GET /api/v1/scripts/import/eventhubs-capture/support-matrix", cors(log(auth(http.HandlerFunc(s.handleEventHubsCaptureSupportMatrix)))))
	s.mux.Handle("POST /api/v1/scripts/import/broker-config", cors(log(auth(http.HandlerFunc(s.handleImportBrokerConfig)))))
	s.mux.Handle("GET /api/v1/scripts/import/broker-config/support-matrix", cors(log(auth(http.HandlerFunc(s.handleBrokerConfigSupportMatrix)))))
	s.mux.Handle("POST /api/v1/scripts/import/settings", cors(log(auth(http.HandlerFunc(s.handleImportSettings)))))
	s.mux.Handle("POST /api/v1/scripts/import/parameterize/apply", cors(log(auth(http.HandlerFunc(s.handleApplyImportParameterization)))))
	s.mux.Handle("POST /api/v1/scripts/import/shared-values/apply", cors(log(auth(http.HandlerFunc(s.handleApplyImportSharedValues)))))
	s.mux.Handle("POST /api/v1/scripts/{id}/parameterize/suggest", cors(log(auth(http.HandlerFunc(s.handleSuggestScriptParameterization)))))

	// Governed AI-assisted authoring, repair, explanation, and analysis.
	s.mux.Handle("GET /api/v1/ai/authoring/contract", cors(log(auth(http.HandlerFunc(s.handleAIAuthoringContract)))))
	s.mux.Handle("GET /api/v1/ai/policy", cors(log(auth(http.HandlerFunc(s.handleAIPolicy)))))
	s.mux.Handle("GET /api/v1/ai/review-queue", cors(log(auth(http.HandlerFunc(s.handleAIReviewQueue)))))
	s.mux.Handle("POST /api/v1/ai/drafts", cors(log(auth(http.HandlerFunc(s.handleAIDraftGeneration)))))
	s.mux.Handle("POST /api/v1/ai/repairs", cors(log(auth(http.HandlerFunc(s.handleAIRepairSuggestions)))))
	s.mux.Handle("POST /api/v1/ai/explain", cors(log(auth(http.HandlerFunc(s.handleAIExplain)))))
	s.mux.Handle("GET /api/v1/browser-replay/sessions/{session_id}", cors(log(browserReplaySessionViewAuth(http.HandlerFunc(s.handleGetBrowserReplaySession)))))
	s.mux.Handle("POST /api/v1/browser-replay/sessions/{session_id}/pause", cors(log(browserReplaySessionEditAuth(http.HandlerFunc(s.handlePauseBrowserReplaySession)))))
	s.mux.Handle("POST /api/v1/browser-replay/sessions/{session_id}/resume", cors(log(browserReplaySessionEditAuth(http.HandlerFunc(s.handleResumeBrowserReplaySession)))))
	s.mux.Handle("POST /api/v1/browser-replay/sessions/{session_id}/stop", cors(log(browserReplaySessionEditAuth(http.HandlerFunc(s.handleStopBrowserReplaySession)))))
	s.mux.Handle("GET /api/v1/script-debug/sessions/{session_id}", cors(log(scriptDebugSessionViewAuth(http.HandlerFunc(s.handleGetScriptDebugSession)))))
	s.mux.Handle("GET /api/v1/script-debug/sessions/{session_id}/export", cors(log(scriptDebugSessionViewAuth(http.HandlerFunc(s.handleExportScriptDebugSession)))))
	s.mux.Handle("POST /api/v1/script-debug/sessions/{session_id}/pause", cors(log(scriptDebugSessionEditAuth(http.HandlerFunc(s.handlePauseScriptDebugSession)))))
	s.mux.Handle("POST /api/v1/script-debug/sessions/{session_id}/resume", cors(log(scriptDebugSessionEditAuth(http.HandlerFunc(s.handleResumeScriptDebugSession)))))
	s.mux.Handle("POST /api/v1/script-debug/sessions/{session_id}/step", cors(log(scriptDebugSessionEditAuth(http.HandlerFunc(s.handleStepScriptDebugSession)))))
	s.mux.Handle("POST /api/v1/script-debug/sessions/{session_id}/stop", cors(log(scriptDebugSessionEditAuth(http.HandlerFunc(s.handleStopScriptDebugSession)))))
	s.mux.Handle("PATCH /api/v1/script-debug/sessions/{session_id}/breakpoints", cors(log(scriptDebugSessionEditAuth(http.HandlerFunc(s.handleUpdateScriptDebugBreakpoints)))))
	s.mux.Handle("PATCH /api/v1/script-debug/sessions/{session_id}/options", cors(log(scriptDebugSessionEditAuth(http.HandlerFunc(s.handleUpdateScriptDebugOptions)))))
	s.mux.Handle("GET /api/v1/replay/sessions/{session_id}", cors(log(scriptDebugSessionViewAuth(http.HandlerFunc(s.handleGetScriptDebugSession)))))
	s.mux.Handle("GET /api/v1/replay/sessions/{session_id}/export", cors(log(scriptDebugSessionViewAuth(http.HandlerFunc(s.handleExportScriptDebugSession)))))
	s.mux.Handle("POST /api/v1/replay/sessions/{session_id}/pause", cors(log(scriptDebugSessionEditAuth(http.HandlerFunc(s.handlePauseScriptDebugSession)))))
	s.mux.Handle("POST /api/v1/replay/sessions/{session_id}/resume", cors(log(scriptDebugSessionEditAuth(http.HandlerFunc(s.handleResumeScriptDebugSession)))))
	s.mux.Handle("POST /api/v1/replay/sessions/{session_id}/step", cors(log(scriptDebugSessionEditAuth(http.HandlerFunc(s.handleStepScriptDebugSession)))))
	s.mux.Handle("POST /api/v1/replay/sessions/{session_id}/stop", cors(log(scriptDebugSessionEditAuth(http.HandlerFunc(s.handleStopScriptDebugSession)))))
	s.mux.Handle("PATCH /api/v1/replay/sessions/{session_id}/breakpoints", cors(log(scriptDebugSessionEditAuth(http.HandlerFunc(s.handleUpdateScriptDebugBreakpoints)))))
	s.mux.Handle("PATCH /api/v1/replay/sessions/{session_id}/options", cors(log(scriptDebugSessionEditAuth(http.HandlerFunc(s.handleUpdateScriptDebugOptions)))))

	// Recording proxy control and artifact retrieval.
	s.mux.Handle("POST /api/v1/recorder/start", cors(log(recorderCreateAuth(http.HandlerFunc(s.handleStartRecording)))))
	s.mux.Handle("POST /api/v1/recorder/{id}/pause", cors(log(recorderSessionEditAuth(http.HandlerFunc(s.handlePauseRecording)))))
	s.mux.Handle("POST /api/v1/recorder/{id}/resume", cors(log(recorderSessionEditAuth(http.HandlerFunc(s.handleResumeRecording)))))
	s.mux.Handle("POST /api/v1/recorder/{id}/stop", cors(log(recorderSessionEditAuth(http.HandlerFunc(s.handleStopRecording)))))
	s.mux.Handle("GET /api/v1/recorder/{id}", cors(log(recorderSessionViewAuth(http.HandlerFunc(s.handleGetRecording)))))
	s.mux.Handle("GET /api/v1/recorder", cors(log(recorderViewAuth(http.HandlerFunc(s.handleListRecordings)))))
	s.mux.Handle("DELETE /api/v1/recorder/{id}", cors(log(recorderSessionEditAuth(http.HandlerFunc(s.handleDeleteRecording)))))
	s.mux.Handle("PATCH /api/v1/recorder/{id}/requests/{seq}", cors(log(recorderSessionEditAuth(http.HandlerFunc(s.handleUpdateRecordedRequest)))))
	s.mux.Handle("POST /api/v1/recorder/{id}/convert/preview", cors(log(recorderSessionEditAuth(http.HandlerFunc(s.handlePreviewRecordingConversion)))))
	s.mux.Handle("POST /api/v1/recorder/{id}/convert", cors(log(recorderSessionEditAuth(http.HandlerFunc(s.handleConvertRecording)))))
	s.mux.Handle("GET /api/v1/recorder/{id}/export", cors(log(recorderSessionViewAuth(http.HandlerFunc(s.handleExportRecording)))))
	s.mux.Handle("GET /api/v1/recorder/status", cors(log(auth(http.HandlerFunc(s.handleRecorderStatus)))))
	s.mux.Handle("GET /api/v1/recorder/ca.pem", cors(log(auth(http.HandlerFunc(s.handleRecorderCACert)))))

	// Headed browser recording (local/desktop controllers): lifecycle control,
	// conversion to the script library, and side-car HAR export.
	s.mux.Handle("GET /api/v1/browser-recorder/sessions", cors(log(auth(http.HandlerFunc(s.handleListBrowserRecordings)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions", cors(log(browserRecordingCreateAuth(http.HandlerFunc(s.handleStartBrowserRecording)))))
	s.mux.Handle("GET /api/v1/browser-recorder/sessions/{id}", cors(log(browserRecordingSessionViewAuth(http.HandlerFunc(s.handleGetBrowserRecording)))))
	s.mux.Handle("DELETE /api/v1/browser-recorder/sessions/{id}", cors(log(browserRecordingSessionEditAuth(http.HandlerFunc(s.handleDeleteBrowserRecording)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/stop", cors(log(browserRecordingSessionEditAuth(http.HandlerFunc(s.handleStopBrowserRecording)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/force-stop", cors(log(browserRecordingSessionEditAuth(http.HandlerFunc(s.handleForceStopBrowserRecording)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/pause", cors(log(browserRecordingSessionEditAuth(http.HandlerFunc(s.handlePauseBrowserRecording)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/resume", cors(log(browserRecordingSessionEditAuth(http.HandlerFunc(s.handleResumeBrowserRecording)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/navigate", cors(log(browserRecordingSessionEditAuth(http.HandlerFunc(s.handleNavigateBrowserRecording)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/focus", cors(log(browserRecordingSessionEditAuth(http.HandlerFunc(s.handleFocusBrowserRecording)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/control-panel", cors(log(browserRecordingSessionEditAuth(http.HandlerFunc(s.handleOpenBrowserRecordingControlPanel)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/transactions/start", cors(log(browserRecordingSessionEditAuth(http.HandlerFunc(s.handleStartBrowserRecordingTransaction)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/transactions/end", cors(log(browserRecordingSessionEditAuth(http.HandlerFunc(s.handleEndBrowserRecordingTransaction)))))
	s.mux.Handle("GET /api/v1/browser-recorder/sessions/{id}/transactions/suggestions", cors(log(browserRecordingSessionViewAuth(http.HandlerFunc(s.handleSuggestBrowserRecordingTransactions)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/notes", cors(log(browserRecordingSessionEditAuth(http.HandlerFunc(s.handleAddBrowserRecordingNote)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/section", cors(log(browserRecordingSessionEditAuth(http.HandlerFunc(s.handleSetBrowserRecordingSection)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/pick-mode", cors(log(browserRecordingSessionEditAuth(http.HandlerFunc(s.handleSetBrowserRecordingPickMode)))))
	s.mux.Handle("DELETE /api/v1/browser-recorder/sessions/{id}/steps/last", cors(log(browserRecordingSessionEditAuth(http.HandlerFunc(s.handleDeleteLastBrowserRecordingStep)))))
	s.mux.Handle("PATCH /api/v1/browser-recorder/sessions/{id}/steps/{seq}", cors(log(browserRecordingSessionEditAuth(http.HandlerFunc(s.handleUpdateBrowserRecordingStep)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/verify", cors(log(browserRecordingSessionEditAuth(http.HandlerFunc(s.handleVerifyBrowserRecording)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/convert/preview", cors(log(browserRecordingSessionEditAuth(http.HandlerFunc(s.handlePreviewBrowserRecordingConversion)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/convert", cors(log(browserRecordingSessionEditAuth(http.HandlerFunc(s.handleConvertBrowserRecording)))))
	s.mux.Handle("GET /api/v1/browser-recorder/sessions/{id}/export", cors(log(browserRecordingSessionViewAuth(http.HandlerFunc(s.handleExportBrowserRecording)))))
	s.mux.Handle("GET /api/v1/browser-recorder/sessions/{id}/storage-state", cors(log(browserRecordingSessionEditAuth(http.HandlerFunc(s.handleExportBrowserRecordingStorageState)))))
	s.mux.Handle("GET /api/v1/browser-recorder/devices", cors(log(auth(http.HandlerFunc(s.handleListBrowserRecorderDevices)))))
	s.mux.Handle("POST /api/v1/browser-recorder/install", cors(log(auth(http.HandlerFunc(s.handleBrowserRecorderInstall)))))
	s.mux.Handle("GET /api/v1/browser-recorder/install", cors(log(auth(http.HandlerFunc(s.handleGetBrowserRecorderInstall)))))

	// System endpoints for health and global settings.
	s.mux.Handle("GET /api/v1/auth/login", cors(log(http.HandlerFunc(s.handleOIDCLogin))))
	s.mux.Handle("POST /api/v1/auth/login", cors(log(http.HandlerFunc(s.handleLocalLogin))))
	s.mux.Handle("GET /api/v1/auth/callback", cors(log(http.HandlerFunc(s.handleOIDCCallback))))
	s.mux.Handle("POST /api/v1/auth/callback", cors(log(http.HandlerFunc(s.handleOIDCCallback))))
	s.mux.Handle("POST /api/v1/auth/logout", cors(log(http.HandlerFunc(s.handleAuthLogout))))
	s.mux.Handle("GET /api/v1/auth/me", cors(log(http.HandlerFunc(s.handleGetAuthStatus))))
	s.mux.Handle("GET /api/v1/auth/diagnostics", cors(log(http.HandlerFunc(s.handleGetAuthDiagnostics))))
	s.mux.Handle("GET /api/v1/auth/roles", cors(log(http.HandlerFunc(s.handleGetAuthRoleCatalog))))
	s.mux.Handle("GET /api/v1/auth/access-matrix", cors(log(http.HandlerFunc(s.handleGetAuthAccessMatrix))))
	s.mux.Handle("GET /api/v1/auth/policy", cors(log(http.HandlerFunc(s.handleGetAuthPolicy))))
	s.mux.Handle("GET /api/v1/auth/effective-access", cors(log(http.HandlerFunc(s.handleGetAuthEffectiveAccess))))
	s.mux.Handle("GET /api/v1/auth/effective-access/principal", cors(log(auth(http.HandlerFunc(s.handleGetAuthEffectiveAccessForPrincipal)))))
	s.mux.Handle("GET /api/v1/auth/users", cors(log(auth(http.HandlerFunc(s.handleListUsers)))))
	s.mux.Handle("POST /api/v1/auth/users", cors(log(auth(http.HandlerFunc(s.handleCreateUser)))))
	s.mux.Handle("POST /api/v1/auth/users/onboard", cors(log(auth(http.HandlerFunc(s.handleOnboardUser)))))
	s.mux.Handle("GET /api/v1/auth/users/{id}", cors(log(auth(http.HandlerFunc(s.handleGetUser)))))
	s.mux.Handle("PATCH /api/v1/auth/users/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateUser)))))
	s.mux.Handle("DELETE /api/v1/auth/users/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteUser)))))
	s.mux.Handle("POST /api/v1/auth/users/{id}/bindings", cors(log(auth(http.HandlerFunc(s.handleCreateUserBinding)))))
	s.mux.Handle("DELETE /api/v1/auth/users/{id}/bindings/{binding_id}", cors(log(auth(http.HandlerFunc(s.handleDeleteUserBinding)))))
	s.mux.Handle("POST /api/v1/auth/users/{id}/disable", cors(log(auth(http.HandlerFunc(s.handleDisableUser)))))
	s.mux.Handle("POST /api/v1/auth/users/{id}/enable", cors(log(auth(http.HandlerFunc(s.handleEnableUser)))))
	s.mux.Handle("POST /api/v1/auth/users/{id}/revoke-sessions", cors(log(auth(http.HandlerFunc(s.handleRevokeUserSessions)))))
	s.mux.Handle("GET /api/v1/auth/user-activity", cors(log(auth(http.HandlerFunc(s.handleGetUserActivity)))))
	s.mux.Handle("GET /api/v1/auth/user-activity/export", cors(log(auth(http.HandlerFunc(s.handleExportUserActivity)))))
	s.mux.Handle("GET /api/v1/auth/access-review", cors(log(auth(http.HandlerFunc(s.handleGetAccessReview)))))
	s.mux.Handle("GET /api/v1/auth/service-accounts", cors(log(auth(http.HandlerFunc(s.handleListServiceAccounts)))))
	s.mux.Handle("POST /api/v1/auth/service-accounts", cors(log(auth(http.HandlerFunc(s.handleCreateServiceAccount)))))
	s.mux.Handle("PATCH /api/v1/auth/service-accounts/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateServiceAccount)))))
	s.mux.Handle("POST /api/v1/auth/service-accounts/{id}/rotate", cors(log(auth(http.HandlerFunc(s.handleRotateServiceAccount)))))
	s.mux.Handle("DELETE /api/v1/auth/service-accounts/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteServiceAccount)))))
	s.mux.Handle("GET /api/v1/auth/audit-events", cors(log(auth(http.HandlerFunc(s.handleListAuditEvents)))))
	s.mux.Handle("GET /api/v1/auth/audit-events/export", cors(log(auth(http.HandlerFunc(s.handleExportAuditEvents)))))
	s.registerAPIRouteCatalog(documentationAPIRouteCatalog, standardRouteMiddleware)
	s.registerAPIRouteCatalog(systemFoundationAPIRouteCatalog, standardRouteMiddleware)
	s.registerAPIRouteCatalog(generatorGatewayAPIRouteCatalog, standardRouteMiddleware)
	s.mux.Handle("GET /api/v1/downloads/recording-agent/latest", cors(log(auth(http.HandlerFunc(s.handleDownloadRecordingAgent)))))
	s.mux.Handle("GET /api/v1/downloads/recording-agent/latest/checksum", cors(log(auth(http.HandlerFunc(s.handleRecordingAgentChecksum)))))
	s.mux.Handle("GET /api/v1/downloads/desktop/latest", cors(log(auth(http.HandlerFunc(s.handleDownloadDesktopApp)))))
	s.mux.Handle("GET /api/v1/downloads/desktop/latest/checksum", cors(log(auth(http.HandlerFunc(s.handleDesktopAppChecksum)))))
	// With a dedicated metrics listener (APEXLOAD_METRICS_ADDR) the scrape
	// endpoint moves off the API port entirely, so NetworkPolicy can grant the
	// monitoring namespace metrics access without exposing the API surface.
	if s.metricsAddr == "" {
		s.mux.Handle("GET /metrics", log(http.HandlerFunc(s.handlePrometheusMetricsWithAuth)))
	}
	s.registerAPIRouteCatalog(systemObservabilityAPIRouteCatalog, standardRouteMiddleware)
	s.registerAPIRouteCatalog(systemAdministrationAPIRouteCatalog, standardRouteMiddleware)
	s.mux.Handle("GET /api/v1/desktop/status", cors(log(auth(http.HandlerFunc(s.handleDesktopStatus)))))
	s.mux.Handle("GET /api/v1/runtime-settings/support-matrix", cors(log(auth(http.HandlerFunc(s.handleRuntimeSettingsSupportMatrix)))))
	s.mux.Handle("GET /api/v1/protocol-bundles/support-matrix", cors(log(auth(http.HandlerFunc(s.handleProtocolBundleSupportMatrix)))))
	s.mux.Handle("GET /api/v1/protocols/native-support-matrix", cors(log(auth(http.HandlerFunc(s.handleNativeProtocolSupportMatrix)))))
	s.mux.Handle("GET /api/v1/integrations/apm/profiles", cors(log(auth(http.HandlerFunc(s.handleListAPMIntegrationProfiles)))))
	s.mux.Handle("POST /api/v1/integrations/dynatrace/dry-run", cors(log(auth(http.HandlerFunc(s.handleDynatraceDryRun)))))
	s.mux.Handle("GET /api/v1/integrations/github/config", cors(log(auth(http.HandlerFunc(s.handleGitHubConfig)))))
	s.mux.Handle("GET /api/v1/integrations/github/diagnostics", cors(log(auth(http.HandlerFunc(s.handleGitHubDiagnostics)))))
	s.mux.Handle("GET /api/v1/integrations/github/installations", cors(log(auth(http.HandlerFunc(s.handleGitHubInstallations)))))
	s.mux.Handle("POST /api/v1/integrations/github/sync-jobs", cors(log(auth(http.HandlerFunc(s.handleCreateGitHubSyncJob)))))
	s.mux.Handle("GET /api/v1/integrations/github/sync-jobs", cors(log(auth(http.HandlerFunc(s.handleListGitHubSyncJobs)))))
	s.mux.Handle("GET /api/v1/integrations/github/sync-jobs/{id}", cors(log(auth(http.HandlerFunc(s.handleGetGitHubSyncJob)))))
	s.mux.Handle("POST /api/v1/integrations/github/webhook", cors(log(http.HandlerFunc(s.handleGitHubWebhook))))
	s.mux.Handle("GET /api/v1/integrations/github/webhooks", cors(log(auth(http.HandlerFunc(s.handleListGitHubWebhookDeliveries)))))
	s.mux.Handle("GET /api/v1/integrations/github/check-links", cors(log(auth(http.HandlerFunc(s.handleListGitHubCheckLinks)))))
	s.mux.Handle("GET /api/v1/integrations/github/login", cors(log(auth(http.HandlerFunc(s.handleGitHubLogin)))))
	s.mux.Handle("GET /api/v1/integrations/github/callback", cors(log(s.desktopGitHubCallbackLanding(auth(http.HandlerFunc(s.handleGitHubCallback))))))
	s.mux.Handle("POST /api/v1/integrations/github/callback", cors(log(auth(http.HandlerFunc(s.handleGitHubCallback)))))
	s.mux.Handle("GET /api/v1/integrations/github/connection", cors(log(auth(http.HandlerFunc(s.handleGitHubConnection)))))
	s.mux.Handle("DELETE /api/v1/integrations/github/connection", cors(log(auth(http.HandlerFunc(s.handleGitHubDisconnect)))))
	s.mux.Handle("GET /api/v1/integrations/github/repos", cors(log(auth(http.HandlerFunc(s.handleGitHubRepos)))))
	s.mux.Handle("GET /api/v1/integrations/github/branches", cors(log(auth(http.HandlerFunc(s.handleGitHubBranches)))))
	s.mux.Handle("GET /api/v1/integrations/github/contents", cors(log(auth(http.HandlerFunc(s.handleGitHubContents)))))
	s.mux.Handle("POST /api/v1/scripts/import/github", cors(log(auth(http.HandlerFunc(s.handleImportGitHub)))))
	s.mux.Handle("POST /api/v1/scripts/{id}/github/push", cors(log(auth(http.HandlerFunc(s.handlePushScriptGitHub)))))
	s.mux.Handle("GET /api/v1/scripts/{id}/github/status", cors(log(auth(http.HandlerFunc(s.handleScriptGitHubStatus)))))
	s.mux.Handle("POST /api/v1/tests/{id}/github/check-run", cors(log(auth(http.HandlerFunc(s.handlePublishGitHubCheckRun)))))
	s.mux.Handle("GET /api/v1/monitoring/packs", cors(log(auth(http.HandlerFunc(s.handleListInfrastructureMonitorPacks)))))
	s.mux.Handle("GET /api/v1/monitoring/prometheus/graphs", cors(log(auth(http.HandlerFunc(s.handlePrometheusGraphs)))))

	// Imported Grafana dashboards: two-phase import/commit, CRUD, and panel resolution.
	s.mux.Handle("GET /api/v1/monitoring/dashboards/import/grafana/support-matrix", cors(log(auth(http.HandlerFunc(s.handleGrafanaDashboardSupportMatrix)))))
	s.mux.Handle("POST /api/v1/monitoring/dashboards/import/grafana", cors(log(auth(http.HandlerFunc(s.handleImportGrafanaDashboard)))))
	s.mux.Handle("POST /api/v1/monitoring/dashboards/import/grafana/live", cors(log(auth(http.HandlerFunc(s.handleImportGrafanaDashboardByUID)))))
	s.mux.Handle("POST /api/v1/monitoring/dashboards/import/grafana/commit", cors(log(auth(http.HandlerFunc(s.handleCommitGrafanaDashboardImport)))))
	s.mux.Handle("POST /api/v1/monitoring/dashboards/import/grafana/preview", cors(log(auth(http.HandlerFunc(s.handlePreviewGrafanaDashboardImport)))))
	s.mux.Handle("POST /api/v1/monitoring/dashboards/import/grafana/label-values", cors(log(auth(http.HandlerFunc(s.handleGrafanaImportLabelValues)))))
	s.mux.Handle("GET /api/v1/monitoring/dashboards", cors(log(auth(http.HandlerFunc(s.handleListGrafanaDashboards)))))
	s.mux.Handle("GET /api/v1/monitoring/dashboards/graphs", cors(log(auth(http.HandlerFunc(s.handleImportedDashboardGraphs)))))
	s.mux.Handle("GET /api/v1/monitoring/dashboards/{id}", cors(log(auth(http.HandlerFunc(s.handleGetGrafanaDashboard)))))
	s.mux.Handle("PATCH /api/v1/monitoring/dashboards/{id}", cors(log(auth(http.HandlerFunc(s.handlePatchGrafanaDashboard)))))
	s.mux.Handle("DELETE /api/v1/monitoring/dashboards/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteGrafanaDashboard)))))
	s.mux.Handle("GET /api/v1/monitoring/dashboards/{id}/panels", cors(log(auth(http.HandlerFunc(s.handleQueryGrafanaDashboardPanels)))))
	s.mux.Handle("POST /api/v1/monitoring/dashboard-definitions", cors(log(auth(http.HandlerFunc(s.handleCreateDashboardDefinition)))))
	s.mux.Handle("GET /api/v1/monitoring/dashboard-definitions", cors(log(auth(http.HandlerFunc(s.handleListDashboardDefinitions)))))
	s.mux.Handle("GET /api/v1/monitoring/dashboard-definitions/{id}", cors(log(auth(http.HandlerFunc(s.handleGetDashboardDefinition)))))
	s.mux.Handle("PATCH /api/v1/monitoring/dashboard-definitions/{id}", cors(log(auth(http.HandlerFunc(s.handlePatchDashboardDefinition)))))
	s.mux.Handle("DELETE /api/v1/monitoring/dashboard-definitions/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteDashboardDefinition)))))
	s.mux.Handle("GET /api/v1/reports/profiles", cors(log(auth(http.HandlerFunc(s.handleListReportProfiles)))))
	s.mux.Handle("GET /api/v1/library/capabilities", cors(log(auth(http.HandlerFunc(s.handleLibraryCapabilities)))))
	s.mux.Handle("GET /api/v1/library/catalog", cors(log(auth(http.HandlerFunc(s.handleLibraryCatalog)))))
	s.mux.Handle("POST /api/v1/library/export", cors(log(auth(http.HandlerFunc(s.handleLibraryExport)))))
	s.mux.Handle("POST /api/v1/library/import", cors(log(auth(http.HandlerFunc(s.handleLibraryImport)))))
	s.mux.Handle("POST /api/v1/library/import/archive", cors(log(auth(http.HandlerFunc(s.handleLibraryImportArchive)))))
	s.mux.Handle("GET /api/v1/library/results/{id}/archive", cors(log(auth(http.HandlerFunc(s.handleLibraryResultArchive)))))
	s.mux.Handle("GET /api/v1/library/remotes", cors(log(auth(http.HandlerFunc(s.handleLibraryListRemotes)))))
	s.mux.Handle("POST /api/v1/library/remotes", cors(log(auth(http.HandlerFunc(s.handleLibraryCreateRemote)))))
	s.mux.Handle("PUT /api/v1/library/remotes/{id}", cors(log(auth(http.HandlerFunc(s.handleLibraryUpdateRemote)))))
	s.mux.Handle("DELETE /api/v1/library/remotes/{id}", cors(log(auth(http.HandlerFunc(s.handleLibraryDeleteRemote)))))
	s.mux.Handle("POST /api/v1/library/remotes/{id}/probe", cors(log(auth(http.HandlerFunc(s.handleLibraryProbeRemote)))))
	s.mux.Handle("GET /api/v1/library/remotes/{id}/catalog", cors(log(auth(http.HandlerFunc(s.handleLibraryRemoteCatalog)))))
	s.mux.Handle("POST /api/v1/library/remotes/{id}/pull", cors(log(auth(http.HandlerFunc(s.handleLibraryRemotePull)))))
	s.mux.Handle("POST /api/v1/library/remotes/{id}/push", cors(log(auth(http.HandlerFunc(s.handleLibraryRemotePush)))))
	s.mux.Handle("POST /api/v1/library/remotes/{id}/diff", cors(log(auth(http.HandlerFunc(s.handleLibraryRemoteDiff)))))
	s.mux.Handle("POST /api/v1/remotes/{id}/runs", cors(log(auth(http.HandlerFunc(s.handleCreateRemoteRun)))))
	s.mux.Handle("GET /api/v1/remotes/{id}/runs/{remote_test_id}", cors(log(auth(http.HandlerFunc(s.handleGetRemoteRun)))))
	s.mux.Handle("POST /api/v1/remotes/{id}/runs/{remote_test_id}/start", cors(log(auth(s.handleRemoteRunAction("start")))))
	s.mux.Handle("POST /api/v1/remotes/{id}/runs/{remote_test_id}/stop", cors(log(auth(s.handleRemoteRunAction("stop")))))
	s.mux.Handle("POST /api/v1/remotes/{id}/runs/{remote_test_id}/abort", cors(log(auth(s.handleRemoteRunAction("abort")))))
	s.mux.Handle("POST /api/v1/remotes/{id}/runs/{remote_test_id}/pause", cors(log(auth(s.handleRemoteRunAction("pause")))))
	s.mux.Handle("POST /api/v1/remotes/{id}/runs/{remote_test_id}/resume", cors(log(auth(s.handleRemoteRunAction("resume")))))
	s.mux.Handle("POST /api/v1/results/import", cors(log(auth(http.HandlerFunc(s.handleImportExternalResults)))))
	s.mux.Handle("GET /api/v1/results/imports", cors(log(auth(http.HandlerFunc(s.handleListExternalResultImports)))))
	s.registerAPIRouteCatalog(fleetObservabilityAPIRouteCatalog, standardRouteMiddleware)
	// Fleet management: pools, generator metadata, and cloud provisioning.
	s.registerAPIRouteCatalog(fleetAdministrationAPIRouteCatalog, standardRouteMiddleware)
	// Cluster registry: Kubernetes/AKS connections and pool host allocation.
	s.registerAPIRouteCatalog(clusterAdministrationAPIRouteCatalog, standardRouteMiddleware)
	s.registerAPIRouteCatalog(computeAdministrationAPIRouteCatalog, standardRouteMiddleware)
	s.registerAPIRouteCatalog(domainDeploymentTargetAPIRouteCatalog, standardRouteMiddleware)
	// WebSocket — no CORS wrapper (handled by upgrade check)
	s.mux.Handle("GET /api/v1/ws", log(auth(http.HandlerFunc(s.handleWebSocket))))
	s.mux.Handle("GET /api/v1/tests/{id}/stream", log(auth(http.HandlerFunc(s.handleTestStream))))

	// OPTIONS preflight for all API routes
	s.mux.Handle("OPTIONS /api/", cors(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
	if s.desktopCapability != "" {
		s.mux.Handle("GET /desktop/bootstrap", http.HandlerFunc(s.handleDesktopBrowserBootstrap))
		s.mux.Handle("POST /api/v1/desktop/browser-downloads", cors(log(auth(http.HandlerFunc(s.handleCreateDesktopBrowserDownload)))))
		s.mux.Handle("GET /desktop/download", http.HandlerFunc(s.handleDesktopBrowserDownload))
	}

	// Dashboard static files — serve from embedded FS, fallback to index.html for SPA routing
	staticSub, err := fs.Sub(StaticFS, "static")
	if err != nil {
		slog.Warn("embedded static files not available, dashboard will show build instructions")
		s.mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if serveAPINotFound(w, r) {
				return
			}
			serveDashboardBuildMissingOrNotFound(w, r)
		}))
	} else if ok, reason := dashboardStaticReady(staticSub); !ok {
		slog.Warn("embedded static files incomplete, dashboard will show build instructions", "reason", reason)
		s.mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if serveAPINotFound(w, r) {
				return
			}
			serveDashboardBuildMissingOrNotFound(w, r)
		}))
	} else {
		fileServer := http.FileServer(http.FS(staticSub))
		s.mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if serveAPINotFound(w, r) {
				return
			}
			setDashboardStaticHeaders(w)
			// Serve real files directly. Only extensionless client routes fall back to the SPA shell.
			path := strings.TrimPrefix(r.URL.Path, "/")
			if path == "" {
				path = "index.html"
			}
			if _, err := fs.Stat(staticSub, path); err != nil {
				if path != "index.html" && strings.Contains(path, ".") {
					http.NotFound(w, r)
					return
				}
				r.URL.Path = "/"
			}
			fileServer.ServeHTTP(w, r)
		}))
	}
}

func dashboardStaticReady(staticFS fs.FS) (bool, string) {
	index, err := fs.ReadFile(staticFS, "index.html")
	if err != nil {
		return false, "missing dashboard index.html"
	}
	matches := dashboardAssetRefPattern.FindAllSubmatch(index, -1)
	if len(matches) == 0 {
		return false, "dashboard index.html does not reference bundled JS/CSS assets"
	}
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		assetPath := string(match[1])
		if _, err := fs.Stat(staticFS, assetPath); err != nil {
			return false, fmt.Sprintf("dashboard asset %s is missing", assetPath)
		}
	}
	return true, ""
}

func serveAPINotFound(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		return false
	}
	writeError(w, http.StatusNotFound, "API endpoint not found")
	return true
}

func serveDashboardBuildMissingOrNotFound(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if strings.HasPrefix(path, "assets/") {
		http.NotFound(w, r)
		return
	}
	serveDashboardBuildMissing(w)
}

func serveDashboardBuildMissing(w http.ResponseWriter) {
	setDashboardStaticHeaders(w)
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte("<!DOCTYPE html><html><body><h1>APEX Load</h1><p>Dashboard not built. Run: cd dashboard && npm run build</p></body></html>"))
}

func setDashboardStaticHeaders(w http.ResponseWriter) {
	w.Header().Set("Permissions-Policy", "clipboard-write=(self)")
	w.Header().Set("Feature-Policy", "clipboard-write 'self'")
}

// enforceAuthPosture fails closed when the server is configured for production
// but the effective auth mode bypasses authentication. In non-production it
// only warns loudly so operators know the API is unauthenticated.
func (s *Server) enforceAuthPosture() error {
	settings := normalizeAuthSettings(s.runner.GetSystemSettings().Auth)
	mode := settings.Mode
	if mode == authModeLocal && productionMode() {
		return fmt.Errorf("refusing to start: auth mode %q uses fixed non-production credentials and cannot run with production authentication posture", authModeLocal)
	}
	if !isAuthBypassMode(mode) {
		// Trusted-headers mode trusts forwarded identity headers. In production it must
		// be fronted by a verifiable trust boundary (a trusted-proxy CIDR allowlist or a
		// shared secret); without one, anyone who can reach the controller port forges
		// identity. Fail closed loudly at boot rather than silently per request.
		if mode == authModeTrustedHeaders && productionMode() && !trustedProxyGateConfigured(settings) {
			return fmt.Errorf("refusing to start: auth mode %q in production requires a trusted-proxy gate; set auth.trusted_proxy_cidrs or auth.trusted_proxy_secret_env_var", authModeTrustedHeaders)
		}
		if mode == authModeTrustedHeaders && productionMode() && strings.TrimSpace(settings.TrustedProxySecretEnvVar) != "" {
			if _, err := trustedProxySharedSecret(settings); err != nil {
				return fmt.Errorf("refusing to start: %w", err)
			}
		}
		if mode == authModeTrustedHeaders && !trustedProxyGateConfigured(settings) {
			slog.Warn("trusted-headers auth has no trusted-proxy gate; forwarded identity headers are trusted from any source — configure auth.trusted_proxy_cidrs or auth.trusted_proxy_secret_env_var before production")
		}
		return nil
	}
	if productionMode() {
		return fmt.Errorf("refusing to start: auth mode %q bypasses authentication but APEXLOAD_ENV=production; configure %q or %q auth", mode, authModeOIDC, authModeTrustedHeaders)
	}
	if s.bindScope == config.BindScopeWide {
		slog.Warn("controller is wide-bound while auth is in a bypass mode; all reachable API endpoints are unauthenticated",
			"auth_mode", mode,
			"addr", s.httpServer.Addr,
			"bind_scope", s.bindScope)
		return nil
	}
	slog.Warn("auth is in a bypass mode — all API endpoints are unauthenticated; do not use this configuration in production",
		"auth_mode", mode)
	return nil
}

func (s *Server) Start() (startErr error) {
	startupCtx, err := s.beginServerStartup()
	if err != nil {
		return err
	}
	// Start owns every component it launches. If any later bind or Serve step
	// fails, tear the already-started listeners and background workers down
	// before returning so an embedding process cannot retain a hidden metrics
	// endpoint or orphaned janitor goroutines after a failed startup.
	defer func() {
		if startErr == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			startErr = errors.Join(startErr, fmt.Errorf("rollback failed server startup: %w", err))
		}
	}()
	// Registered after the rollback defer so it runs first on an error; rollback
	// Shutdown can then observe that startup has stopped publishing components.
	defer s.finishServerStartup()
	if err := s.refreshReadinessSettingsForStartup(); err != nil {
		return err
	}
	s.captureStartupAuthSettings()
	if s.bridgeMode {
		return s.startBridge()
	}
	if err := s.enforceAuthPosture(); err != nil {
		return err
	}
	localAuthStartup := s.authSettingsForRequest().Mode == authModeLocal
	if localAuthStartup {
		if err := s.ensureLocalAuthFixtures(); err != nil {
			return fmt.Errorf("initialize local test login: %w", err)
		}
	}
	// Import-claim recovery used to run in an untracked goroutine from
	// NewServer. That goroutine could race an embedding caller immediately after
	// construction and could continue using storage after Shutdown returned.
	// Complete the bounded durable recovery before listeners and background
	// workers start so startup owns its full lifetime.
	if err := s.recoverExpiredLibraryImportClaims(); err != nil {
		slog.Warn("failed to recover expired library import claims", "error", err)
	}
	if s.shutdownRequested() {
		return http.ErrServerClosed
	}
	s.startPendingRemoteRunCleanupRetry()
	// Start websocket broadcast loop before serving HTTP requests.
	s.launchBackground(s.hub.Run)
	s.startReplayRetentionJanitors()
	s.startBrowserRecordingRetentionJanitor()
	s.startBrunoImportSessionJanitor()
	s.startGrafanaImportSessionJanitor()
	s.startProtectedSourceSecretSessionJanitor()
	s.startCacheFanout()
	s.startGeneratorDiscovery()

	if err := s.startRecorderProxy(); err != nil {
		return err
	}
	s.startUsageAccountingSnapshotCadence()
	s.startReadinessSettingsRefreshCadence()
	s.startGeneratorInventoryRefreshCadence()
	s.startAuditRetentionCadence()
	s.startTimeslotRetentionCadence()
	s.startScheduleOccurrenceRetentionCadence()
	s.startArchiveCleanupRetry()
	s.startArchiveFinalizationRetry()
	s.startAppDynamicsMonitoring()
	s.startAccessReviewNotifierCadence()
	s.startCIGitHubCheckReconciliation()

	if err := s.startMetricsListener(); err != nil {
		return err
	}

	listener, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return err
	}
	if s.shutdownRequested() {
		_ = listener.Close()
		return http.ErrServerClosed
	}
	gate := newStartupGateListener(listener)
	serveDone := make(chan error, 1)
	go func() { serveDone <- s.httpServer.Serve(gate) }()
	select {
	case <-gate.ready:
	case err := <-serveDone:
		return err
	}

	// Rotate Local Test Login only after every configured listener has bound and
	// http.Server has registered the API listener. Holding lifecycleMu makes the
	// rotation and publication one boundary with Shutdown: either cancellation
	// wins first and no sessions are touched, or this controller becomes the
	// published generation before Shutdown can proceed.
	// A replacement process that cannot start must not revoke sessions on a
	// still-healthy controller sharing the same database. The durable generation
	// also fences an overlapping old process from minting sessions accepted by
	// this newly started controller.
	s.lifecycleMu.Lock()
	if s.shutdownStarted {
		gate.abort()
		s.lifecycleMu.Unlock()
		<-serveDone
		return http.ErrServerClosed
	}
	if _, ok := s.store.(localAuthStore); ok {
		activationCtx, cancelActivation := context.WithTimeout(startupCtx, localAuthStartupTimeout)
		invalidated, err := s.activateLocalAuthGenerationForStartupContext(activationCtx)
		cancelActivation()
		if err != nil {
			gate.abort()
			s.lifecycleMu.Unlock()
			<-serveDone
			// database/sql may surface a context-canceled transaction as
			// sql.ErrTxDone rather than context.Canceled. The immutable parent
			// context distinguishes Shutdown cancellation from this operation's
			// own startup timeout without depending on the driver's error shape.
			if startupCtx.Err() != nil {
				return http.ErrServerClosed
			}
			return fmt.Errorf("activate local test login for startup: %w", err)
		}
		if invalidated > 0 {
			slog.Info("invalidated local test login sessions for controller startup", "session_count", invalidated)
		}
	}
	s.setBoundAddr(listener.Addr().String())
	s.finishServerStartup()
	slog.Info("apexload API server starting", "addr", s.httpServer.Addr, "bound_addr", s.BoundAddr())
	gate.open()
	s.lifecycleMu.Unlock()
	if err := <-serveDone; err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) startBridge() error {
	if s.shutdownRequested() {
		return http.ErrServerClosed
	}
	s.launchBackground(s.hub.Run)
	s.startReplayRetentionJanitors()
	s.startArchiveCleanupRetry()
	listener, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return err
	}
	if s.shutdownRequested() {
		_ = listener.Close()
		return http.ErrServerClosed
	}
	s.setBoundAddr(listener.Addr().String())
	s.finishServerStartup()
	slog.Info("apexload browser recording bridge starting", "addr", s.httpServer.Addr, "bound_addr", s.BoundAddr())
	if err := s.httpServer.Serve(listener); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) startReplayRetentionJanitors() {
	if s == nil {
		return
	}
	if s.browserReplay != nil {
		s.browserReplay.StartJanitor(context.Background())
	}
	if s.scriptDebug != nil {
		s.scriptDebug.StartJanitor(context.Background())
	}
}

func (s *Server) startBrowserRecordingRetentionJanitor() {
	if s == nil || s.browserRecording == nil {
		return
	}
	s.browserRecording.StartJanitor(context.Background())
}

func (s *Server) startRecorderProxy() error {
	if s == nil || s.recorder == nil {
		return nil
	}
	if s.shutdownRequested() {
		return http.ErrServerClosed
	}
	if err := s.recorder.StartProxy(); err != nil {
		if s.desktopMode {
			return fmt.Errorf("start protected desktop recording proxy: %w", err)
		}
		slog.Warn("recording proxy disabled", "error", err)
		return nil
	}
	if s.shutdownRequested() {
		_ = s.recorder.StopProxy()
		return http.ErrServerClosed
	}
	return nil
}

// startMetricsListener serves GET /metrics on its own listener when
// MetricsAddr is configured, keeping the scrape surface off the API port.
func (s *Server) startMetricsListener() error {
	if s.metricsAddr == "" {
		return nil
	}
	metricsMux := http.NewServeMux()
	metricsMux.Handle("GET /metrics", loggingMiddleware(http.HandlerFunc(s.handlePrometheusMetricsWithAuth)))
	metricsServer := &http.Server{
		Addr:         s.metricsAddr,
		Handler:      metricsMux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	listener, err := net.Listen("tcp", s.metricsAddr)
	if err != nil {
		return fmt.Errorf("metrics listener %s: %w", s.metricsAddr, err)
	}
	s.metricsServerMu.Lock()
	if s.metricsServerStopped {
		s.metricsServerMu.Unlock()
		_ = listener.Close()
		return http.ErrServerClosed
	}
	s.metricsServer = metricsServer
	s.metricsServerMu.Unlock()
	s.boundAddrMu.Lock()
	s.metricsBoundAddr = listener.Addr().String()
	s.boundAddrMu.Unlock()
	slog.Info("metrics listener started", "addr", s.metricsAddr)
	s.launchBackground(func() {
		if err := metricsServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			slog.Error("metrics listener error", "err", err)
		}
	})
	return nil
}

func (s *Server) handlePrometheusMetricsWithAuth(w http.ResponseWriter, r *http.Request) {
	if s.metricsToken != "" {
		got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.metricsToken)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="apexload-metrics"`)
			writeError(w, http.StatusUnauthorized, "metrics token required")
			return
		}
	}
	s.handlePrometheusMetrics(w, r)
}

// MetricsBoundAddr reports the dedicated metrics listener address, if any.
func (s *Server) MetricsBoundAddr() string {
	if s == nil {
		return ""
	}
	s.boundAddrMu.RLock()
	defer s.boundAddrMu.RUnlock()
	return s.metricsBoundAddr
}

func (s *Server) setBoundAddr(addr string) {
	s.boundAddrMu.Lock()
	defer s.boundAddrMu.Unlock()
	s.boundAddr = addr
}

func (s *Server) clearBoundAddrs() {
	if s == nil {
		return
	}
	s.boundAddrMu.Lock()
	s.boundAddr = ""
	s.metricsBoundAddr = ""
	s.boundAddrMu.Unlock()
}

func (s *Server) BoundAddr() string {
	if s == nil {
		return ""
	}
	s.boundAddrMu.RLock()
	defer s.boundAddrMu.RUnlock()
	if s.boundAddr != "" {
		return s.boundAddr
	}
	return s.httpServer.Addr
}

// BoundAddrReady reports whether the HTTP listener has bound its address. It is
// false until Start has called net.Listen, which matters when the server is
// configured with a random port (":0"): callers that need the real address
// (e.g. the desktop bridge reporting the API origin to the dashboard) must wait
// for this to become true before reading BrowserURL, otherwise they would
// observe the unbound ":0" placeholder.
func (s *Server) BoundAddrReady() bool {
	if s == nil {
		return false
	}
	s.boundAddrMu.RLock()
	defer s.boundAddrMu.RUnlock()
	return s.boundAddr != ""
}

// DesktopCapability returns the in-memory per-launch capability required by
// desktop REST and WebSocket clients. It is empty for ordinary hosted servers.
// Callers must never persist or log this value.
func (s *Server) DesktopCapability() string {
	if s == nil {
		return ""
	}
	return s.desktopCapability
}

func generateDesktopSecret() (string, error) {
	var tokenBytes [32]byte
	if _, err := rand.Read(tokenBytes[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(tokenBytes[:]), nil
}

// NewDesktopBrowserURL returns a short-lived, single-use URL that seeds a
// separate browser capability into port-scoped sessionStorage. The bootstrap
// page replaces its history entry, and neither the native renderer capability
// nor a host-wide loopback cookie is exposed.
func (s *Server) NewDesktopBrowserURL(devViewMode, projectID string) (string, error) {
	if s == nil || s.desktopCapability == "" {
		return "", fmt.Errorf("desktop browser bootstrap is unavailable")
	}
	if !s.BoundAddrReady() {
		return "", fmt.Errorf("desktop API server is not ready")
	}
	preview, err := ParseDesktopDevPreviewContext(devViewMode, projectID)
	if err != nil {
		return "", err
	}
	nonce, err := generateDesktopSecret()
	if err != nil {
		return "", fmt.Errorf("generate desktop browser bootstrap: %w", err)
	}
	browserCapability, err := generateDesktopSecret()
	if err != nil {
		return "", fmt.Errorf("generate desktop browser capability: %w", err)
	}
	now := time.Now()
	s.desktopBootstrapMu.Lock()
	for existing, grant := range s.desktopBootstrapNonces {
		if !grant.expiresAt.After(now) {
			delete(s.desktopBootstrapNonces, existing)
		}
	}
	s.desktopBootstrapNonces[nonce] = desktopBrowserBootstrap{
		expiresAt:   now.Add(time.Minute),
		capability:  browserCapability,
		devViewMode: preview.Mode(),
		projectID:   preview.ProjectID(),
	}
	s.desktopBootstrapMu.Unlock()
	return s.BrowserURL() + "/desktop/bootstrap?nonce=" + nonce, nil
}

func (s *Server) handleDesktopBrowserBootstrap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	nonce := strings.TrimSpace(r.URL.Query().Get("nonce"))
	now := time.Now()
	s.desktopBootstrapMu.Lock()
	grant, ok := s.desktopBootstrapNonces[nonce]
	if ok {
		delete(s.desktopBootstrapNonces, nonce)
	}
	if nonce == "" || !ok || !grant.expiresAt.After(now) {
		s.desktopBootstrapMu.Unlock()
		http.Error(w, "desktop browser bootstrap is invalid or expired", http.StatusForbidden)
		return
	}
	s.desktopBootstrapMu.Unlock()

	// Cookies are scoped by host but not by port. A capability cookie for
	// 127.0.0.1 would therefore be sent to unrelated loopback services. Seed a
	// per-tab sessionStorage value instead: browser storage origins include the
	// random API port, and the dashboard attaches the value explicitly as the
	// desktop capability header/WebSocket query parameter.
	scriptNonce, err := generateDesktopSecret()
	if err != nil {
		http.Error(w, "desktop browser bootstrap failed", http.StatusInternalServerError)
		return
	}
	s.desktopBootstrapMu.Lock()
	if len(s.desktopBrowserCapabilities) >= maxDesktopBrowserCapabilities {
		var oldestCapability string
		var oldestIssuedAt time.Time
		for existing, browserGrant := range s.desktopBrowserCapabilities {
			if oldestCapability == "" || browserGrant.issuedAt.Before(oldestIssuedAt) {
				oldestCapability = existing
				oldestIssuedAt = browserGrant.issuedAt
			}
		}
		delete(s.desktopBrowserCapabilities, oldestCapability)
	}
	s.desktopBrowserCapabilities[grant.capability] = desktopBrowserCapability{
		issuedAt:    now,
		devViewMode: grant.devViewMode,
		projectID:   grant.projectID,
	}
	s.desktopBootstrapMu.Unlock()
	expireLegacyDesktopOAuthCookies(w)
	capabilityJSON, _ := json.Marshal(grant.capability)
	devViewModeJSON, _ := json.Marshal(grant.devViewMode)
	projectIDJSON, _ := json.Marshal(grant.projectID)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+scriptNonce+"'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><title>Opening APEX Load</title></head><body><script nonce="%s">sessionStorage.setItem("apexload.desktopCapability",%s);localStorage.setItem("apexload.devViewMode",%s);const project=%s;if(project){localStorage.setItem("apexload.activeProjectId",project)}else{localStorage.removeItem("apexload.activeProjectId")}const route=location.hash.startsWith("#/")&&!location.hash.startsWith("#//")?location.hash:"";location.replace("/"+route);</script><noscript>JavaScript is required to open APEX Load.</noscript></body></html>`, scriptNonce, capabilityJSON, devViewModeJSON, projectIDJSON)
}

func (s *Server) validDesktopBrowserCapability(capability string) bool {
	if s == nil || capability == "" {
		return false
	}
	s.desktopBootstrapMu.Lock()
	_, ok := s.desktopBrowserCapabilities[capability]
	s.desktopBootstrapMu.Unlock()
	return ok
}

func (s *Server) BrowserURL() string {
	addr := s.BoundAddr()
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	if strings.HasPrefix(addr, "[::]:") {
		addr = "127.0.0.1:" + strings.TrimPrefix(addr, "[::]:")
	}
	if strings.HasPrefix(addr, "0.0.0.0:") {
		addr = "127.0.0.1:" + strings.TrimPrefix(addr, "0.0.0.0:")
	}
	return "http://" + addr
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// startupCancel is immutable after construction and safe for concurrent use.
	// Cancel before acquiring lifecycleMu so a generation activation blocked in
	// storage releases the publication lock and lets this deadline be honored.
	s.cancelServerStartup()
	s.lifecycleMu.Lock()
	if s.shutdownDone == nil {
		s.shutdownDone = make(chan struct{})
	}
	if s.startupDone == nil {
		s.startupDone = make(chan struct{})
	}
	if s.shutdownStarted {
		done := s.shutdownDone
		s.lifecycleMu.Unlock()
		select {
		case <-done:
			s.lifecycleMu.Lock()
			err := s.shutdownErr
			s.lifecycleMu.Unlock()
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.shutdownStarted = true
	startupStarted := s.startupStarted
	startupDone := s.startupDone
	s.lifecycleMu.Unlock()
	if startupStarted && startupDone != nil {
		select {
		case <-startupDone:
		case <-ctx.Done():
			// Preserve the no-resurrection invariant even when the caller's
			// bounded wait expires: once startup reaches its publication boundary,
			// finish teardown in the background and wake any later Shutdown caller.
			go func() {
				<-startupDone
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				s.finishShutdown(s.shutdown(cleanupCtx))
			}()
			return ctx.Err()
		}
	}

	err := s.shutdown(ctx)
	s.finishShutdown(err)
	return err
}

func (s *Server) cancelServerStartup() {
	if s == nil {
		return
	}
	// NewServer initializes startupCancel immutably. Keep zero-value Servers
	// useful for narrow unit tests without racing lazy initialization against
	// Shutdown's pre-lock cancellation path.
	if s.startupCancel != nil {
		s.startupCancel()
	}
}

func (s *Server) finishShutdown(err error) {
	s.lifecycleMu.Lock()
	s.shutdownErr = err
	close(s.shutdownDone)
	s.lifecycleMu.Unlock()
}

func (s *Server) beginServerStartup() (context.Context, error) {
	if s == nil {
		return nil, http.ErrServerClosed
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.startupDone == nil {
		s.startupDone = make(chan struct{})
	}
	if s.shutdownDone == nil {
		s.shutdownDone = make(chan struct{})
	}
	if s.shutdownStarted {
		return nil, http.ErrServerClosed
	}
	if s.startupStarted {
		return nil, fmt.Errorf("API server startup already began")
	}
	s.startupStarted = true
	if s.startupContext == nil {
		return context.Background(), nil
	}
	return s.startupContext, nil
}

func (s *Server) finishServerStartup() {
	if s == nil {
		return
	}
	s.startupDoneOnce.Do(func() {
		close(s.startupDone)
	})
}

func (s *Server) shutdownRequested() bool {
	if s == nil {
		return true
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	return s.shutdownStarted
}

func (s *Server) shutdown(ctx context.Context) error {
	var shutdownErr error
	// Request evaluator cancellation before marking the runner as shutting down,
	// but do not let a slow in-flight sweep delay listener closure. The bounded
	// scheduler wait happens below before dependent components are torn down.
	if s.scheduler != nil {
		s.scheduler.stop()
	}
	if s.runner != nil {
		s.runner.beginShutdown()
	}
	s.readinessSettingsMu.Lock()
	readinessSettingsCadence := s.readinessSettingsCadence
	s.readinessSettingsCadence = nil
	s.readinessSettingsMu.Unlock()
	if readinessSettingsCadence != nil {
		readinessSettingsCadence.cancel()
	}
	s.beginBackgroundShutdown()
	s.clearAllSourceSecretSessions()
	// Stop accepting new requests first. WebSocket connections are hijacked and
	// are closed by the hub below; ordinary in-flight handlers get the caller's
	// bounded drain window.
	httpDone := make(chan error, 1)
	if s.httpServer != nil {
		go func() { httpDone <- s.httpServer.Shutdown(ctx) }()
	} else {
		httpDone <- nil
	}
	// Keep the hub, cache, and generator clients alive while active tests receive
	// their stop request. This is also used by the HA lease-loss callback.
	if s.runner != nil {
		if err := s.runner.stopActiveTests(ctx); err != nil {
			shutdownErr = err
		}
	}
	select {
	case err := <-httpDone:
		if err != nil && shutdownErr == nil {
			shutdownErr = err
		}
	case <-ctx.Done():
		if shutdownErr == nil {
			shutdownErr = ctx.Err()
		}
	}
	if err := s.ShutdownScheduler(ctx); err != nil {
		shutdownErr = errors.Join(shutdownErr, err)
	}

	if s.runner != nil && s.runner.thresholdJournal != nil {
		shutdownErr = errors.Join(shutdownErr, s.runner.thresholdJournal.shutdown(ctx))
	}

	// With listeners drained and load stopped, tear down background components.
	if s.usageCancel != nil {
		s.usageCancel()
		s.usageCancel = nil
	}
	if s.auditRetentionCancel != nil {
		s.auditRetentionCancel()
		s.auditRetentionCancel = nil
	}
	if s.timeslotRetentionCancel != nil {
		s.timeslotRetentionCancel()
		s.timeslotRetentionCancel = nil
	}
	if s.scheduleOccurrenceRetentionCancel != nil {
		s.scheduleOccurrenceRetentionCancel()
		s.scheduleOccurrenceRetentionCancel = nil
	}
	if s.brunoSessionCleanupCancel != nil {
		s.brunoSessionCleanupCancel()
		s.brunoSessionCleanupCancel = nil
	}
	if s.grafanaSessionCleanupCancel != nil {
		s.grafanaSessionCleanupCancel()
		s.grafanaSessionCleanupCancel = nil
	}
	if s.sourceSecretCleanupCancel != nil {
		s.sourceSecretCleanupCancel()
		s.sourceSecretCleanupCancel = nil
	}
	if s.archiveCleanupCancel != nil {
		s.archiveCleanupCancel()
		s.archiveCleanupCancel = nil
		if s.archiveCleanupDone != nil {
			select {
			case <-s.archiveCleanupDone:
			case <-ctx.Done():
			}
			s.archiveCleanupDone = nil
		}
	}
	if s.appDynamicsCancel != nil {
		s.appDynamicsCancel()
		s.appDynamicsCancel = nil
	}
	if s.archiveFinalizationCancel != nil {
		s.archiveFinalizationCancel()
		s.archiveFinalizationCancel = nil
		if s.archiveFinalizationDone != nil {
			select {
			case <-s.archiveFinalizationDone:
			case <-ctx.Done():
			}
			s.archiveFinalizationDone = nil
		}
	}
	s.stopRemoteRunCleanupRetry(ctx)
	if s.accessReviewNotifyCancel != nil {
		s.accessReviewNotifyCancel()
		s.accessReviewNotifyCancel = nil
	}
	if s.ciChecksCancel != nil {
		s.ciChecksCancel()
		s.ciChecksCancel = nil
	}
	if s.fanoutCancel != nil {
		s.fanoutCancel()
		s.fanoutCancel = nil
	}
	if s.discoveryCancel != nil {
		s.discoveryCancel()
		s.discoveryCancel = nil
	}
	if s.generatorInventoryCancel != nil {
		s.generatorInventoryCancel()
		s.generatorInventoryCancel = nil
	}
	if s.fanoutSub != nil {
		_ = s.fanoutSub.Close()
		s.fanoutSub = nil
	}
	if s.hub != nil {
		s.hub.Stop()
	}
	if s.runner != nil && s.runner.notifier != nil {
		s.runner.notifier.Close()
	}
	if err := s.closeReplayManagers(ctx); err != nil {
		shutdownErr = errors.Join(shutdownErr, err)
	}
	if s.browserRecording != nil {
		s.browserRecording.Close()
	}
	if s.recorder != nil {
		if err := s.recorder.StopProxy(); err != nil && shutdownErr == nil {
			shutdownErr = err
		}
	}
	if s.cache != nil {
		_ = s.cache.Close()
	}
	s.metricsServerMu.Lock()
	s.metricsServerStopped = true
	metricsServer := s.metricsServer
	s.metricsServer = nil
	s.metricsServerMu.Unlock()
	if metricsServer != nil {
		if err := metricsServer.Shutdown(ctx); err != nil && shutdownErr == nil {
			shutdownErr = err
		}
	}
	if err := s.waitForBackground(ctx); err != nil && shutdownErr == nil {
		shutdownErr = err
	}
	s.clearBoundAddrs()
	return shutdownErr
}

// closeReplayManagers begins both independent manager drains together and
// applies the Shutdown caller's single deadline to the pair. Each manager has
// its own defensive worker timeout, but waiting for those timeouts serially
// would let replay cleanup exceed the server's requested shutdown window.
func (s *Server) closeReplayManagers(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var managers sync.WaitGroup
	if s.browserReplay != nil {
		managers.Add(1)
		go func() {
			defer managers.Done()
			s.browserReplay.Close()
		}()
	}
	if s.scriptDebug != nil {
		managers.Add(1)
		go func() {
			defer managers.Done()
			s.scriptDebug.Close()
		}()
	}
	done := make(chan struct{})
	go func() {
		managers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// launchBackground starts a Server-owned goroutine and accounts for its full
// lifetime. Once shutdown begins no new owned work is admitted, which makes the
// WaitGroup safe to wait and prevents leadership callbacks from racing teardown.
func (s *Server) launchBackground(run func()) bool {
	if s == nil || run == nil {
		return false
	}
	s.backgroundMu.Lock()
	if s.backgroundStopping {
		s.backgroundMu.Unlock()
		return false
	}
	s.backgroundWG.Add(1)
	s.backgroundMu.Unlock()
	go func() {
		defer s.backgroundWG.Done()
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("server background task panicked", "panic", recovered)
			}
		}()
		run()
	}()
	return true
}

func (s *Server) beginBackgroundShutdown() {
	if s == nil {
		return
	}
	s.backgroundMu.Lock()
	s.backgroundStopping = true
	s.backgroundMu.Unlock()
}

func (s *Server) waitForBackground(ctx context.Context) error {
	if s == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		s.backgroundWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) startCacheFanout() {
	if s == nil || s.cache == nil || s.hub == nil {
		return
	}
	info := s.cache.Info()
	if !cacheBackendIsRemote(info.Backend) {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	sub, err := s.cache.Subscribe(ctx, info.FanoutChannel)
	if err != nil {
		cancel()
		slog.Warn("redis fanout subscription unavailable", "err", err)
		return
	}
	s.fanoutCancel = cancel
	s.fanoutSub = sub
	s.launchBackground(func() {
		for payload := range sub.Messages() {
			var envelope websocketFanoutEnvelope
			if err := json.Unmarshal(payload, &envelope); err != nil {
				slog.Warn("dropping malformed cache fanout envelope", "err", err)
				continue
			}
			if envelope.Origin == "" || envelope.Origin == s.controllerID {
				continue
			}
			if len(envelope.Payload) == 0 {
				continue
			}
			broadcast := hubBroadcast{
				payload:      envelope.Payload,
				testID:       envelope.TestID,
				projectID:    envelope.ProjectID,
				broadcastAll: envelope.BroadcastAll && envelope.TestID == "" && envelope.ProjectID == "",
			}
			// Backward compatibility during a rolling HA upgrade: an envelope from
			// a not-yet-upgraded replica carries no top-level routing fields, so
			// fall back to the scoping embedded in the message payload (the
			// pre-envelope wire format) rather than dropping it for every scoped
			// subscriber.
			if envelope.TestID == "" && envelope.ProjectID == "" && !envelope.BroadcastAll {
				broadcast = newHubBroadcast(envelope.Payload)
			}
			s.hub.broadcastLocal(broadcast)
		}
	})
}

func (s *Server) startUsageAccountingSnapshotCadence() {
	if s == nil || s.runner == nil || s.usageCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.usageCancel = cancel
	s.launchBackground(func() {
		// Poll settings at a small fixed cadence so enabling, disabling, or
		// changing the snapshot interval takes effect without a restart.
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		var enabledAt time.Time
		var lastCapture time.Time
		wasLeader := false
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if !s.isExecutionLeader() {
					wasLeader = false
					continue
				}
				if !wasLeader {
					wasLeader = true
					enabledAt = now
					var err error
					lastCapture, err = s.runner.latestGlobalUsageSnapshotTime()
					if err != nil {
						lastCapture = time.Time{}
						slog.Warn("failed to load latest usage accounting snapshot after leadership acquisition", "error", err)
					}
				}
				interval := s.runner.UsageAccountingSnapshotInterval()
				if interval <= 0 {
					enabledAt = time.Time{}
					lastCapture = time.Time{}
					continue
				}
				if enabledAt.IsZero() {
					enabledAt = now
				}
				reference := lastCapture
				if reference.IsZero() {
					reference = enabledAt
				}
				if now.Sub(reference) < interval {
					continue
				}
				if _, err := s.runner.captureUsageSnapshotForCadence(""); err != nil {
					slog.Warn("failed to save cadence usage accounting snapshot", "error", err)
					lastCapture = now
					continue
				}
				lastCapture = now
			}
		}
	})
}

// generatorInventoryRefreshInterval keeps the cached fleet inventory well within
// its TTL so capacity gates read fresh data without probing under tr.mu.
const generatorInventoryRefreshInterval = defaultGeneratorSnapshotTTL / 2

// generatorInventoryRefreshTimeout bounds each background probe so a slow or
// unreachable fleet cannot hang the refresh goroutine indefinitely (which would
// leave the cache cold and the gate over-admitting).
const generatorInventoryRefreshTimeout = 10 * time.Second

// startGeneratorInventoryRefreshCadence periodically refreshes the cached
// generator inventory out-of-band. Capacity/launch gates read that cache under
// tr.mu and must never probe the fleet synchronously themselves; without this
// cadence the cache is only populated by dashboard/inventory HTTP traffic, so a
// headless controller (scheduled runs, CI launches) would either fall back to
// configured-only capacity or be unable to prove protocol runtime capabilities.
//
// It is gated on the STRUCTURAL capability (a distributed inventory backend),
// NOT the availability SETTING, because that setting is runtime-mutable
// (UpdateSystemSettings): if we keyed the goroutine's existence on the setting
// at boot, flipping to configured_plus_live later would never start the cadence
// and the cache would stay cold. Runtime capability and protocol-specific
// capacity gates need the same heartbeat evidence even when capacity advisories
// use the default configured-only source, so every distributed deployment keeps
// this cache warm. Capacity advisory readers still ignore live inventory unless
// configured_plus_live is selected.
func (s *Server) startGeneratorInventoryRefreshCadence() {
	if s == nil || s.runner == nil || s.generatorInventoryCancel != nil {
		return
	}
	if _, ok := s.runner.getExecutionBackend().(inventoryExecutionBackend); !ok {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.generatorInventoryCancel = cancel
	s.launchBackground(func() {
		// Prime the cache immediately so the first gate after startup sees live
		// data, then refresh on the cadence.
		s.refreshGeneratorInventory(ctx)
		ticker := time.NewTicker(generatorInventoryRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.refreshGeneratorInventory(ctx)
			}
		}
	})
}

// refreshGeneratorInventory performs one bounded, panic-safe inventory probe for
// the background cadence. A panic in the broad systemInventory surface (fleet
// RPC, k8s enrichment) must NOT escape and crash the controller into a
// timer-driven restart loop, and each probe is time-bounded so a hung fleet does
// not stall the refresh. Runtime protocol/capacity gates consume this cache under
// both availability-source modes; configured-only capacity advisories continue
// to ignore the cached live-count projection.
func (s *Server) refreshGeneratorInventory(ctx context.Context) {
	if s == nil || s.runner == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			slog.Error("generator inventory refresh panicked", "panic", r)
		}
	}()
	probeCtx, cancel := context.WithTimeout(ctx, generatorInventoryRefreshTimeout)
	defer cancel()
	inventory := s.systemInventory(probeCtx)
	if s.runner.isExecutionLeader() {
		s.runner.reconcilePodCapacity(probeCtx, inventory.Generators)
		// A cold fleet can gain capacity without another test completing or an
		// operator touching the queue. Wake the coalesced, term-bound promotion
		// worker after refreshing its evidence; it rechecks every admission gate
		// outside this inventory probe and never starts an unrequested QUEUED row.
		if probeCtx.Err() == nil {
			if term, err := s.runner.captureExecutionLeaseTerm(); err == nil {
				s.runner.enqueuePendingPromotionRetryWithTerm(term)
			}
		}
		alerts := buildFleetAlerts(inventory, s.runner.GetSystemSettings().Thresholds)
		s.applyFleetAlertAcknowledgements(alerts)
		s.routeFleetAlertNotifications(alerts)
	}
}

func (s *Server) RunDueSchedules() error {
	if s == nil || s.scheduler == nil {
		return nil
	}
	return s.scheduler.RunDueSchedules()
}

// StartScheduler starts the Server-owned scheduling service worker. The direct
// RunDueSchedules method remains available for deterministic tests and callers
// that intentionally drive single sweeps.
func (s *Server) StartScheduler(ctx context.Context) error {
	if s == nil || s.scheduler == nil {
		return nil
	}
	return s.scheduler.Start(ctx)
}

// ShutdownScheduler performs a bounded worker stop using the caller's context.
func (s *Server) ShutdownScheduler(ctx context.Context) error {
	if s == nil || s.scheduler == nil {
		return nil
	}
	return s.scheduler.Shutdown(ctx)
}

// RunExecutionLeadership renews the HA execution lease until ctx is cancelled.
// It is a no-op when leader election is not enabled (single-active controller).
func (s *Server) RunExecutionLeadership(ctx context.Context) {
	if s == nil || s.leadership == nil {
		return
	}
	var activeEndpointDone chan struct{}
	if s.activeEndpointPublisher != nil {
		activeEndpointDone = make(chan struct{})
		go func() {
			defer close(activeEndpointDone)
			s.runActiveExecutionEndpointPublisher(ctx)
		}()
	}
	// A generator will not revive an expired execution generation. Keep the
	// current durable term alive across the configured fleet even while no test
	// is running, otherwise the first run after a long idle period would be
	// rejected by every generator that had already observed this term.
	var keeperDone chan struct{}
	keeperCtx, keeperCancel := context.WithCancel(ctx)
	if s.runner != nil {
		if backend, ok := s.runner.getExecutionBackend().(*distributedExecutionBackend); ok {
			keeperDone = make(chan struct{})
			go func() {
				defer close(keeperDone)
				backend.maintainGeneratorExecutionEpoch(keeperCtx, s.runner)
			}()
		}
	}
	s.leadership.Run(ctx)
	keeperCancel()
	if keeperDone != nil {
		<-keeperDone
	}
	if activeEndpointDone != nil {
		<-activeEndpointDone
	}
}

// isExecutionLeader reports whether this replica may serve execution-mutating
// API calls. Always true when leader election is disabled.
func (s *Server) isExecutionLeader() bool {
	if s == nil {
		return true
	}
	return s.runner.isExecutionLeader()
}
