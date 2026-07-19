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
	"time"

	"github.com/jasonczech/apexload/pkg/brunoartifact"
	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/fileasset"
	"github.com/jasonczech/apexload/pkg/keywrap"
	"github.com/jasonczech/apexload/pkg/recorder"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

//go:embed static
var StaticFS embed.FS

var dashboardAssetRefPattern = regexp.MustCompile(`(?:href|src)="/(assets/[^"]+\.(?:css|js))"`)

type desktopBrowserBootstrap struct {
	expiresAt   time.Time
	capability  string
	devViewMode string
	projectID   string
}

const maxDesktopBrowserCapabilities = 32

type Server struct {
	httpServer       *http.Server
	hub              *Hub
	runner           *TestRunner
	scheduler        *scenarioScheduler
	browserReplay    *BrowserReplayManager
	scriptDebug      *ScriptDebugManager
	browserRecording *BrowserRecordingManager
	browserInstall   *browserInstallState
	recorder         *recorder.Recorder
	store            storage.Store
	cache            CacheBackend
	controllerID     string
	haExecution      bool
	authoringMu      sync.Mutex
	// authoringMutationMu serializes session edits, deletion, correlation
	// write-back, and approval within one controller. The storage CAS used by
	// those handlers extends the same fence across HA replicas.
	authoringMutationMu sync.Mutex
	authoring           map[string]*storage.AuthoringSession
	libraryMu           sync.Mutex
	// libraryStateMutationMu serializes the load/mutate/CAS transition for
	// remotes and synchronization links within one controller. Durable CAS still
	// provides the cross-controller fence.
	libraryStateMutationMu sync.Mutex
	// remoteConfigMu keeps remote transport/credential metadata stable for the
	// full lifetime of an outbound operation. Updates/deletion take the write
	// side so they cannot invalidate a cleanup intent created from an older
	// credential snapshot.
	remoteConfigMu sync.RWMutex
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
	c4Mu                   sync.Mutex
	slaWaivers             map[string][]*storage.ReleaseSLAWaiver
	resultImports          map[string]*storage.ExternalResultImport
	resultImportBytes      int64
	githubMu               sync.Mutex
	githubTokens           map[string]githubCachedToken
	// projectMutationMu prevents an acknowledged project-scoped HTTP mutation
	// from being erased by a concurrent project graph deletion. Project deletion
	// takes the write side; every other mutating request takes the read side.
	projectMutationMu sync.RWMutex
	oidcMu            sync.Mutex
	oidcRuntimeKey    string
	oidcRuntime       *oidcRuntime
	ciAuthMu          sync.Mutex
	ciOIDCProviders   map[string]*ciOIDCProviderRuntime
	ciOIDCExchanges   map[string]ciOIDCExchangeCacheEntry
	ciChecksCancel    context.CancelFunc
	// selfHealMu serializes replay/debug self-heal write-back. Both the browser
	// replay and script debug managers fire completion hooks into the shared
	// persistSelfHealToRepository, whose read-modify-write of the whole objects
	// blob would otherwise lose a concurrently appended candidate.
	selfHealMu                  sync.Mutex
	collectorMetrics            *collectorMetricStore
	fleetScaler                 fleetPoolScaler
	fleetProvisioningMu         sync.Mutex
	fleetProvisioningActive     map[string]string
	leadership                  *executionLeadership
	activeEndpointPublisher     ExecutionEndpointPublisher
	activeEndpointMu            sync.RWMutex
	activeEndpointPublished     bool
	activeEndpointOwnerID       string
	activeEndpointGeneration    uint64
	activeEndpointLastAttempt   time.Time
	mux                         *http.ServeMux
	backgroundMu                sync.Mutex
	backgroundStopping          bool
	backgroundWG                sync.WaitGroup
	usageCancel                 context.CancelFunc
	auditRetentionCancel        context.CancelFunc
	timeslotRetentionCancel     context.CancelFunc
	archiveCleanupCancel        context.CancelFunc
	archiveCleanupDone          chan struct{}
	archiveFinalizationCancel   context.CancelFunc
	archiveFinalizationWake     chan struct{}
	archiveFinalizationDone     chan struct{}
	accessReviewNotifyCancel    context.CancelFunc
	authAudit                   auditThrottle
	fanoutCancel                context.CancelFunc
	fanoutSub                   FanoutSubscription
	generatorInventoryCancel    context.CancelFunc
	generatorDiscovery          *generatorDiscovery
	discoveryCancel             context.CancelFunc
	k8sRestart                  K8sRestartSettings
	kubeRestarter               KubeRestarter
	kubePodsCacheMu             sync.Mutex
	kubePodsCache               generatorPodInventoryCache
	restartMu                   sync.Mutex
	restartInFlight             map[string]struct{}
	testNotesArchiveMu          sync.Mutex
	testNotesArchiveLocks       map[string]*sync.Mutex
	metricsAddr                 string
	metricsToken                string
	metricsServerMu             sync.Mutex
	metricsServer               *http.Server
	metricsServerStopped        bool
	metricsBoundAddr            string
	desktopMode                 bool
	desktopCapability           string
	desktopBootstrapMu          sync.Mutex
	desktopBootstrapNonces      map[string]desktopBrowserBootstrap
	desktopBrowserCapabilities  map[string]desktopBrowserCapability
	desktopBrowserDownloads     map[string]desktopBrowserDownloadGrant
	desktopOIDCStates           map[string]desktopOIDCStateGrant
	desktopGitHubStates         map[string]desktopGitHubStateGrant
	bridgeMode                  bool
	recordingBridge             *BrowserRecordingBridge
	desktopAppData              string
	desktopLogPath              string
	importAssetDir              string
	brunoImportMu               sync.Mutex
	brunoImportSessions         map[string]*brunoImportSession
	brunoArtifactMu             sync.Mutex
	brunoArtifacts              *brunoartifact.EncryptedStore
	brunoArtifactDir            string
	brunoArtifactWrapper        keywrap.KeyWrapper
	brunoSessionCleanupCancel   context.CancelFunc
	grafanaSessionCleanupCancel context.CancelFunc
	projectSecretStore          secretstore.Store
	protectedSourceSessions     *secretstore.ProtectedSourceSessionStore
	sourceSecretMu              sync.Mutex
	sourceSecretSessions        map[string]*sourceSecretSession
	sourceSecretSessionLifetime time.Duration
	sourceSecretCleanupCancel   context.CancelFunc
	ciRunCreateMu               sync.Mutex
	ciRunCreates                map[string]int
	beforeCIRunMaterialize      func(CreateTestRequest)
	afterCIRunMaterialize       func(*ManagedTest)
	bindScope                   string
	boundAddr                   string
	boundAddrMu                 sync.RWMutex
	lifecycleMu                 sync.Mutex
	startupStarted              bool
	startupDone                 chan struct{}
	startupDoneOnce             sync.Once
	shutdownStarted             bool
	shutdownDone                chan struct{}
	shutdownErr                 error
	// staticFS is the dashboard asset filesystem probed by readiness checks.
	// It defaults to the compile-time embedded StaticFS; tests override it so
	// the readiness suite does not depend on a prior `npm run build`.
	staticFS fs.FS
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
	GeneratorTimeout   time.Duration
	K8sRestart         K8sRestartSettings
	KubeRestarter      KubeRestarter
	DesktopMode        bool
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
	ProjectSecretStore secretstore.Store
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
	if len(staticTargets) > 0 || len(discoverySpecs) > 0 {
		distributedBackend = newDistributedExecutionBackend(staticTargets, cfg.GeneratorTimeout, nil)
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
	var protectedSourceSessions *secretstore.ProtectedSourceSessionStore
	if protectedRecords, ok := cfg.Store.(storage.ProtectedSourceSecretSessionStore); ok && protectedRecords != nil && wrapper != nil {
		protectedSourceSessions = &secretstore.ProtectedSourceSessionStore{Records: protectedRecords, Wrapper: wrapper}
	}
	runnerOpts = append(runnerOpts, WithProjectSecretStore(projectSecretStore))
	runner := NewTestRunner(cfg.MaxConcurrent, hub, cfg.Store, runnerOpts...)
	runner.setLiveStateStore(cache)
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

	s := &Server{
		hub:                         hub,
		runner:                      runner,
		scheduler:                   newScenarioScheduler(runner),
		browserReplay:               NewBrowserReplayManager(hub),
		scriptDebug:                 NewScriptDebugManager(hub),
		browserRecording:            browserRecording,
		browserInstall:              newBrowserInstallState(),
		recorder:                    rec,
		store:                       cfg.Store,
		cache:                       cache,
		controllerID:                controllerID,
		haExecution:                 cfg.HAExecutionLeadership,
		authoring:                   make(map[string]*storage.AuthoringSession),
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
		startupDone:             make(chan struct{}),
		shutdownDone:            make(chan struct{}),
		staticFS:                StaticFS,
	}
	if leadership != nil {
		leadership.setOnLoss(func() {
			s.clearActiveExecutionEndpoint()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := runner.teardownActiveTestsAfterLeadershipLoss(ctx); err != nil {
				slog.Error("failed to tear down stale runtimes after execution leadership loss", "err", err)
			}
		})
		leadership.setOnAcquire(func() {
			for leadership.IsLeader() {
				if err := runner.reloadAndReconcile(); err != nil {
					slog.Warn("execution leadership recovery failed; admission remains closed", "error", err)
				} else {
					ctx, cancel := leadership.operationContext(context.Background())
					err := s.reconcileProvisioningRequests(ctx)
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
	s.fleetScaler = newFleetPoolScaler(s.kubeRestarter, s.k8sRestart)

	// Apply a persisted metadata cordon to the live drain state when a generator
	// (re)connects, so a cordon issued while the generator was offline takes effect.
	if distributedBackend != nil {
		distributedBackend.setOnIdentityRecorded(func(generatorID string) {
			s.applyMetadataCordonOnReconnect(distributedBackend, generatorID)
		})
	}

	// Feed replay selector self-heals back to the bound object repository.
	s.browserReplay.setCompletionHook(s.persistSelfHealToRepository)
	s.scriptDebug.setCompletionHook(s.persistSelfHealToRepository)
	runner.setTerminalObserver(s.handleCIRunTerminal)

	s.loadLibraryState()
	s.routes(cfg.AllowedOrigins)

	handler := bodyLimitMiddleware(s.projectMutationAdmission(s.mux))
	if s.desktopCapability != "" {
		handler = desktopAuthMaterialMiddleware(s.desktopOIDCSessionForCapability, s.desktopDevPreviewForCapability)(handler)
		handler = desktopCapabilityMiddleware(s.desktopCapability, cfg.AllowedOrigins, s.validDesktopBrowserCapability)(handler)
	}
	s.httpServer = &http.Server{
		Addr:           cfg.Addr,
		Handler:        securityHeadersMiddleware(handler),
		ReadTimeout:    15 * time.Second,
		WriteTimeout:   60 * time.Second,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20, // 1 MiB
	}

	return s
}

func (s *Server) routes(allowedOrigins []string) {
	cors := corsMiddleware(allowedOrigins)
	log := loggingMiddleware
	auth := authMiddleware(func() AuthSettings { return s.runner.GetSystemSettings().Auth }, s.store, s.cache, withAuthAuditRecorder(s))
	ciAuth := s.ciAuthMiddleware(auth)
	recorderViewAuth := projectScopedAuthMiddleware(
		func() AuthSettings { return s.runner.GetSystemSettings().Auth },
		s.store,
		s.cache,
		[]string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		[]string{RoleViewer, RoleEditor, RoleProjectAdmin},
		resolveRecorderListProjectID,
		withAuthAuditRecorder(s),
	)
	recorderCreateAuth := projectScopedAuthMiddleware(
		func() AuthSettings { return s.runner.GetSystemSettings().Auth },
		s.store,
		s.cache,
		[]string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		[]string{RoleEditor, RoleProjectAdmin},
		resolveRecorderCreateProjectID,
		withAuthAuditRecorder(s),
	)
	recorderSessionViewAuth := projectScopedAuthMiddleware(
		func() AuthSettings { return s.runner.GetSystemSettings().Auth },
		s.store,
		s.cache,
		[]string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		[]string{RoleViewer, RoleEditor, RoleProjectAdmin},
		resolveRecorderSessionProjectID(s.recorder),
		withAuthAuditRecorder(s),
	)
	recorderSessionEditAuth := projectScopedAuthMiddleware(
		func() AuthSettings { return s.runner.GetSystemSettings().Auth },
		s.store,
		s.cache,
		[]string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		[]string{RoleEditor, RoleProjectAdmin},
		resolveRecorderSessionProjectID(s.recorder),
		withAuthAuditRecorder(s),
	)
	browserReplaySessionViewAuth := projectScopedAuthMiddleware(
		func() AuthSettings { return s.runner.GetSystemSettings().Auth },
		s.store,
		s.cache,
		[]string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		[]string{RoleViewer, RoleEditor, RoleProjectAdmin},
		resolveBrowserReplaySessionProjectID(s.browserReplay),
		withAuthAuditRecorder(s),
	)
	browserReplaySessionEditAuth := projectScopedAuthMiddleware(
		func() AuthSettings { return s.runner.GetSystemSettings().Auth },
		s.store,
		s.cache,
		[]string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		[]string{RoleEditor, RoleProjectAdmin},
		resolveBrowserReplaySessionProjectID(s.browserReplay),
		withAuthAuditRecorder(s),
	)
	scriptDebugSessionViewAuth := projectScopedAuthMiddleware(
		func() AuthSettings { return s.runner.GetSystemSettings().Auth },
		s.store,
		s.cache,
		[]string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		[]string{RoleViewer, RoleEditor, RoleProjectAdmin},
		resolveScriptDebugSessionProjectID(s.scriptDebug),
		withAuthAuditRecorder(s),
	)
	scriptDebugSessionEditAuth := projectScopedAuthMiddleware(
		func() AuthSettings { return s.runner.GetSystemSettings().Auth },
		s.store,
		s.cache,
		[]string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		[]string{RoleEditor, RoleProjectAdmin},
		resolveScriptDebugSessionProjectID(s.scriptDebug),
		withAuthAuditRecorder(s),
	)
	browserRecordingCreateAuth := projectScopedAuthMiddleware(
		func() AuthSettings { return s.runner.GetSystemSettings().Auth },
		s.store,
		s.cache,
		[]string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		[]string{RoleEditor, RoleProjectAdmin},
		resolveRecorderCreateProjectID,
		withAuthAuditRecorder(s),
	)
	browserRecordingSessionViewAuth := projectScopedAuthMiddleware(
		func() AuthSettings { return s.runner.GetSystemSettings().Auth },
		s.store,
		s.cache,
		[]string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		[]string{RoleViewer, RoleEditor, RoleProjectAdmin},
		resolveBrowserRecordingSessionProjectID(s.browserRecording),
		withAuthAuditRecorder(s),
	)
	browserRecordingSessionEditAuth := projectScopedAuthMiddleware(
		func() AuthSettings { return s.runner.GetSystemSettings().Auth },
		s.store,
		s.cache,
		[]string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		[]string{RoleEditor, RoleProjectAdmin},
		resolveBrowserRecordingSessionProjectID(s.browserRecording),
		withAuthAuditRecorder(s),
	)

	// Controller-native GitHub Actions discovery, authentication, and run API.
	s.mux.Handle("GET /.well-known/apexload-ci", cors(log(http.HandlerFunc(s.handleCIWellKnown))))
	s.mux.Handle("POST /api/v1/auth/ci/github/exchange", cors(log(http.HandlerFunc(s.handleGitHubCIExchange))))
	s.mux.Handle("POST /api/v1/ci/runs", cors(log(ciAuth(http.HandlerFunc(s.handleCreateCIRun)))))
	s.mux.Handle("GET /api/v1/ci/runs/{id}", cors(log(ciAuth(http.HandlerFunc(s.handleGetCIRun)))))
	s.mux.Handle("POST /api/v1/ci/runs/{id}/abort", cors(log(ciAuth(http.HandlerFunc(s.handleAbortCIRun)))))

	// Test lifecycle and archive endpoints.
	s.mux.Handle("POST /api/v1/domains", cors(log(auth(http.HandlerFunc(s.handleCreateDomain)))))
	s.mux.Handle("GET /api/v1/domains", cors(log(auth(http.HandlerFunc(s.handleListDomains)))))
	s.mux.Handle("GET /api/v1/domains/{id}", cors(log(auth(http.HandlerFunc(s.handleGetDomain)))))
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
	s.mux.Handle("POST /api/v1/projects/{id}/secrets/status", cors(log(auth(http.HandlerFunc(s.handleProjectSecretStatus)))))
	s.mux.Handle("POST /api/v1/projects/{id}/secrets", cors(log(auth(http.HandlerFunc(s.handleCreateProjectSecrets)))))
	s.mux.Handle("PUT /api/v1/projects/{id}/secrets/{name}", cors(log(auth(http.HandlerFunc(s.handleRotateProjectSecret)))))
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
	s.mux.Handle("PATCH /api/v1/tests/{id}/goal-targets", cors(log(auth(http.HandlerFunc(s.handleUpdateGoalTarget)))))
	s.mux.Handle("PATCH /api/v1/tests/{id}/duration", cors(log(auth(http.HandlerFunc(s.handleAdjustDuration)))))
	s.mux.Handle("DELETE /api/v1/tests/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteTest)))))
	s.mux.Handle("PATCH /api/v1/tests/{id}/move", cors(log(auth(http.HandlerFunc(s.handleMoveTest)))))
	s.mux.Handle("GET /api/v1/tests/{id}/metrics", cors(log(auth(http.HandlerFunc(s.handleGetMetrics)))))
	s.mux.Handle("GET /api/v1/tests/{id}/errors/evidence", cors(log(auth(http.HandlerFunc(s.handleGetLiveErrorEvidence)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive", cors(log(auth(http.HandlerFunc(s.handleDownloadRunArchive)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/manifest", cors(log(auth(http.HandlerFunc(s.handleGetRunArchiveManifest)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/verify", cors(log(auth(http.HandlerFunc(s.handleVerifyRunArchive)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/files/{name}", cors(log(auth(http.HandlerFunc(s.handleDownloadRunArchiveArtifact)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/browser-diagnostics", cors(log(auth(http.HandlerFunc(s.handleDownloadRunArchiveBrowserDiagnostics)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/transactions", cors(log(auth(http.HandlerFunc(s.handleGetRunArchiveTransactions)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/resources", cors(log(auth(http.HandlerFunc(s.handleGetRunArchiveResources)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/summaries", cors(log(auth(http.HandlerFunc(s.handleGetRunArchiveSummaries)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/report", cors(log(auth(http.HandlerFunc(s.handleGetRunArchiveReport)))))
	s.mux.Handle("POST /api/v1/tests/archive/reports", cors(log(auth(http.HandlerFunc(s.handleGetRunArchiveReportsBatch)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/analysis", cors(log(auth(http.HandlerFunc(s.handleGetRunArchiveAnalysis)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/root-cause", cors(log(auth(http.HandlerFunc(s.handleGetRunArchiveRootCause)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/ai-root-cause", cors(log(auth(http.HandlerFunc(s.handleGetRunArchiveAIRootCause)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/comparison", cors(log(auth(http.HandlerFunc(s.handleGetRunArchiveComparison)))))
	s.mux.Handle("POST /api/v1/archives/retention/plan", cors(log(auth(http.HandlerFunc(s.handlePlanArchiveRetention)))))
	s.mux.Handle("POST /api/v1/archives/retention/apply", cors(log(auth(http.HandlerFunc(s.handleApplyArchiveRetention)))))

	// Reusable scenario definitions.
	s.mux.Handle("POST /api/v1/scenarios", cors(log(auth(http.HandlerFunc(s.handleCreateScenario)))))
	s.mux.Handle("GET /api/v1/scenarios", cors(log(auth(http.HandlerFunc(s.handleListScenarios)))))
	s.mux.Handle("POST /api/v1/scenarios/validate-load-model", cors(log(auth(http.HandlerFunc(s.handleValidateScenarioLoadModel)))))
	s.mux.Handle("POST /api/v1/scenarios/size-from-goal", cors(log(auth(http.HandlerFunc(s.handleSizeScenarioFromGoal)))))
	s.mux.Handle("GET /api/v1/scenarios/{id}", cors(log(auth(http.HandlerFunc(s.handleGetScenario)))))
	s.mux.Handle("PUT /api/v1/scenarios/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateScenario)))))
	s.mux.Handle("DELETE /api/v1/scenarios/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteScenario)))))
	s.mux.Handle("GET /api/v1/scenarios/load-models/support-matrix", cors(log(auth(http.HandlerFunc(s.handleLoadModelSupportMatrix)))))
	s.mux.Handle("GET /api/v1/scenarios/load-models/templates", cors(log(auth(http.HandlerFunc(s.handleLoadModelTemplates)))))
	s.mux.Handle("POST /api/v1/scenarios/{id}/schedules/preview", cors(log(auth(http.HandlerFunc(s.handleScenarioSchedulePreview)))))
	s.mux.Handle("POST /api/v1/scenarios/{id}/schedules", cors(log(auth(http.HandlerFunc(s.handleCreateScenarioSchedule)))))
	s.mux.Handle("GET /api/v1/scenarios/{id}/schedules", cors(log(auth(http.HandlerFunc(s.handleListScenarioSchedules)))))
	s.mux.Handle("PUT /api/v1/scenarios/{id}/schedules/{schedule_id}", cors(log(auth(http.HandlerFunc(s.handleUpdateScenarioSchedule)))))
	s.mux.Handle("DELETE /api/v1/scenarios/{id}/schedules/{schedule_id}", cors(log(auth(http.HandlerFunc(s.handleDeleteScenarioSchedule)))))
	s.mux.Handle("GET /api/v1/scenarios/{id}/schedules/{schedule_id}/occurrences", cors(log(auth(http.HandlerFunc(s.handleListScenarioScheduleOccurrences)))))
	s.mux.Handle("DELETE /api/v1/scenarios/{id}/schedules/{schedule_id}/occurrences/{occurrence_id}", cors(log(auth(http.HandlerFunc(s.handleDeleteScenarioScheduleOccurrence)))))
	s.mux.Handle("GET /api/v1/scenarios/{id}/schedules/{schedule_id}/approvals", cors(log(auth(http.HandlerFunc(s.handleListScenarioScheduleApprovals)))))
	s.mux.Handle("POST /api/v1/scenarios/{id}/schedules/{schedule_id}/approvals/{approval_id}/approve", cors(log(auth(http.HandlerFunc(s.handleApproveScenarioScheduleApproval)))))
	s.mux.Handle("POST /api/v1/scenarios/{id}/schedules/{schedule_id}/approvals/{approval_id}/reject", cors(log(auth(http.HandlerFunc(s.handleRejectScenarioScheduleApproval)))))
	s.mux.Handle("GET /api/v1/schedules/health", cors(log(auth(http.HandlerFunc(s.handleScenarioScheduleHealth)))))
	s.mux.Handle("GET /api/v1/run-calendar", cors(log(auth(http.HandlerFunc(s.handleGetRunCalendar)))))
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
	s.mux.Handle("POST /api/v1/data-pools/{id}/rows", cors(log(auth(http.HandlerFunc(s.handleCreateDataPoolRow)))))
	s.mux.Handle("PUT /api/v1/data-pools/{id}/rows/{row_id}", cors(log(auth(http.HandlerFunc(s.handleUpdateDataPoolRow)))))
	s.mux.Handle("DELETE /api/v1/data-pools/{id}/rows/{row_id}", cors(log(auth(http.HandlerFunc(s.handleDeleteDataPoolRow)))))

	// Capacity reservations (timeslots) — forward-booking of future capacity windows.
	s.mux.Handle("POST /api/v1/capacity/timeslots", cors(log(auth(http.HandlerFunc(s.handleCreateCapacityReservation)))))
	s.mux.Handle("GET /api/v1/capacity/timeslots", cors(log(auth(http.HandlerFunc(s.handleListCapacityReservations)))))
	s.mux.Handle("POST /api/v1/capacity/timeslots/availability", cors(log(auth(http.HandlerFunc(s.handleCheckCapacityAvailability)))))
	s.mux.Handle("GET /api/v1/capacity/timeslots/{id}", cors(log(auth(http.HandlerFunc(s.handleGetCapacityReservation)))))
	s.mux.Handle("PUT /api/v1/capacity/timeslots/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateCapacityReservation)))))
	s.mux.Handle("DELETE /api/v1/capacity/timeslots/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteCapacityReservation)))))
	s.mux.Handle("POST /api/v1/capacity/timeslots/{id}/approve", cors(log(auth(http.HandlerFunc(s.handleApproveCapacityReservation)))))
	s.mux.Handle("POST /api/v1/capacity/timeslots/{id}/reject", cors(log(auth(http.HandlerFunc(s.handleRejectCapacityReservation)))))
	s.mux.Handle("POST /api/v1/capacity/timeslots/{id}/bind", cors(log(auth(http.HandlerFunc(s.handleBindCapacityTimeslot)))))

	// Execution plans — ordered cross-run orchestration (LRE test-set analog).
	s.mux.Handle("POST /api/v1/execution-plans", cors(log(auth(http.HandlerFunc(s.handleCreateExecutionPlan)))))
	s.mux.Handle("GET /api/v1/execution-plans", cors(log(auth(http.HandlerFunc(s.handleListExecutionPlans)))))
	s.mux.Handle("GET /api/v1/execution-plans/{id}", cors(log(auth(http.HandlerFunc(s.handleGetExecutionPlan)))))
	s.mux.Handle("PUT /api/v1/execution-plans/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateExecutionPlan)))))
	s.mux.Handle("DELETE /api/v1/execution-plans/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteExecutionPlan)))))
	s.mux.Handle("POST /api/v1/execution-plans/{id}/runs", cors(log(auth(http.HandlerFunc(s.handleMaterializeAndRunExecutionPlan)))))
	s.mux.Handle("GET /api/v1/execution-plans/{id}/runs", cors(log(auth(http.HandlerFunc(s.handleListExecutionPlanRuns)))))
	s.mux.Handle("GET /api/v1/execution-plans/{id}/runs/{runId}", cors(log(auth(http.HandlerFunc(s.handleGetExecutionPlanRun)))))
	s.mux.Handle("POST /api/v1/execution-plans/{id}/schedules", cors(log(auth(http.HandlerFunc(s.handleCreateExecutionPlanSchedule)))))
	s.mux.Handle("GET /api/v1/execution-plans/{id}/schedules", cors(log(auth(http.HandlerFunc(s.handleListExecutionPlanSchedules)))))
	s.mux.Handle("PUT /api/v1/execution-plans/{id}/schedules/{schedule_id}", cors(log(auth(http.HandlerFunc(s.handleUpdateExecutionPlanSchedule)))))
	s.mux.Handle("DELETE /api/v1/execution-plans/{id}/schedules/{schedule_id}", cors(log(auth(http.HandlerFunc(s.handleDeleteExecutionPlanSchedule)))))
	s.mux.Handle("GET /api/v1/execution-plans/{id}/schedules/{schedule_id}/occurrences", cors(log(auth(http.HandlerFunc(s.handleListExecutionPlanScheduleOccurrences)))))
	s.mux.Handle("GET /api/v1/execution-plans/{id}/approvals", cors(log(auth(http.HandlerFunc(s.handleListExecutionPlanApprovals)))))
	s.mux.Handle("POST /api/v1/execution-plans/{id}/approvals/{approval_id}/approve", cors(log(auth(http.HandlerFunc(s.handleApproveExecutionPlanApproval)))))
	s.mux.Handle("POST /api/v1/execution-plans/{id}/approvals/{approval_id}/reject", cors(log(auth(http.HandlerFunc(s.handleRejectExecutionPlanApproval)))))
	s.mux.Handle("POST /api/v1/object-repositories", cors(log(auth(http.HandlerFunc(s.handleCreateObjectRepository)))))
	s.mux.Handle("GET /api/v1/object-repositories", cors(log(auth(http.HandlerFunc(s.handleListObjectRepositories)))))
	s.mux.Handle("POST /api/v1/object-repositories/from-script", cors(log(auth(http.HandlerFunc(s.handleObjectRepositoryFromScript)))))
	s.mux.Handle("GET /api/v1/object-repositories/{id}", cors(log(auth(http.HandlerFunc(s.handleGetObjectRepository)))))
	s.mux.Handle("PUT /api/v1/object-repositories/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateObjectRepository)))))
	s.mux.Handle("DELETE /api/v1/object-repositories/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteObjectRepository)))))

	// Correlation rulesets — learned, reusable correlation rule catalogs.
	s.mux.Handle("POST /api/v1/correlation-rulesets", cors(log(auth(http.HandlerFunc(s.handleCreateCorrelationRuleset)))))
	s.mux.Handle("GET /api/v1/correlation-rulesets", cors(log(auth(http.HandlerFunc(s.handleListCorrelationRulesets)))))
	s.mux.Handle("POST /api/v1/correlation-rulesets/discover-from-script", cors(log(auth(http.HandlerFunc(s.handleDiscoverCorrelationRulesetFromScript)))))
	s.mux.Handle("GET /api/v1/correlation-rulesets/{id}", cors(log(auth(http.HandlerFunc(s.handleGetCorrelationRuleset)))))
	s.mux.Handle("PUT /api/v1/correlation-rulesets/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateCorrelationRuleset)))))
	s.mux.Handle("DELETE /api/v1/correlation-rulesets/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteCorrelationRuleset)))))
	s.mux.Handle("POST /api/v1/correlation-rulesets/{id}/apply-to-script", cors(log(auth(http.HandlerFunc(s.handleApplyCorrelationRulesetToScript)))))

	// Monitoring collectors — restricted-network agent control plane.
	s.mux.Handle("POST /api/v1/monitoring/collectors", cors(log(auth(http.HandlerFunc(s.handleCreateMonitoringCollector)))))
	s.mux.Handle("GET /api/v1/monitoring/collectors", cors(log(auth(http.HandlerFunc(s.handleListMonitoringCollectors)))))
	s.mux.Handle("GET /api/v1/monitoring/collectors/{id}", cors(log(auth(http.HandlerFunc(s.handleGetMonitoringCollector)))))
	s.mux.Handle("PATCH /api/v1/monitoring/collectors/{id}", cors(log(auth(http.HandlerFunc(s.handlePatchMonitoringCollector)))))
	s.mux.Handle("DELETE /api/v1/monitoring/collectors/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteMonitoringCollector)))))
	s.mux.Handle("POST /api/v1/monitoring/collectors/{id}/rotate-token", cors(log(auth(http.HandlerFunc(s.handleRotateMonitoringCollectorToken)))))
	// Agent-facing heartbeat — collector-token authenticated in-handler (no RBAC auth middleware).
	s.mux.Handle("POST /api/v1/monitoring/collectors/{id}/heartbeat", cors(log(http.HandlerFunc(s.handleCollectorHeartbeat))))
	s.mux.Handle("GET /api/v1/monitoring/collectors/{id}/metrics", cors(log(auth(http.HandlerFunc(s.handleGetCollectorMetrics)))))

	// Enterprise entitlements — read-only consumption vs limits.
	s.mux.Handle("GET /api/v1/system/entitlements", cors(log(auth(http.HandlerFunc(s.handleGetEntitlements)))))
	s.mux.Handle("GET /api/v1/system/entitlements/usage", cors(log(auth(http.HandlerFunc(s.handleGetEntitlementsUsage)))))
	s.mux.Handle("GET /api/v1/projects/{id}/entitlements/usage", cors(log(auth(http.HandlerFunc(s.handleGetProjectEntitlementsUsage)))))

	// Script CRUD, validation, import, and export endpoints.
	s.mux.Handle("POST /api/v1/scripts", cors(log(auth(http.HandlerFunc(s.handleCreateScript)))))
	s.mux.Handle("POST /api/v1/scripts/secret-bindings/apply", cors(log(auth(http.HandlerFunc(s.handleApplyScriptSecretBindings)))))
	s.mux.Handle("POST /api/v1/scripts/import/secrets/migrate", cors(log(auth(http.HandlerFunc(s.handleMigrateSourceSecrets)))))
	s.mux.Handle("POST /api/v1/scripts/import/secrets/discard", cors(log(auth(http.HandlerFunc(s.handleDiscardSourceSecretSession)))))
	s.mux.Handle("GET /api/v1/scripts", cors(log(auth(http.HandlerFunc(s.handleListScripts)))))
	s.mux.Handle("GET /api/v1/scripts/{id}", cors(log(auth(http.HandlerFunc(s.handleGetScript)))))
	s.mux.Handle("GET /api/v1/scripts/{id}/secret-status", cors(log(auth(http.HandlerFunc(s.handleScriptSecretStatus)))))
	s.mux.Handle("POST /api/v1/scripts/{id}/clone-draft", cors(log(auth(http.HandlerFunc(s.handleCloneScriptDraft)))))
	s.mux.Handle("PUT /api/v1/scripts/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateScript)))))
	s.mux.Handle("DELETE /api/v1/scripts/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteScript)))))
	s.mux.Handle("PATCH /api/v1/scripts/{id}/move", cors(log(auth(http.HandlerFunc(s.handleMoveScript)))))
	s.mux.Handle("GET /api/v1/scripts/{id}/bruno-imports/source", cors(log(auth(http.HandlerFunc(s.handleGetBrunoImportSource)))))
	s.mux.Handle("GET /api/v1/scripts/{id}/bruno-imports/revisions", cors(log(auth(http.HandlerFunc(s.handleListBrunoImportRevisions)))))
	s.mux.Handle("POST /api/v1/scripts/{id}/bruno-imports/rollback", cors(log(auth(http.HandlerFunc(s.handleRollbackBrunoImport)))))
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
	s.mux.Handle("GET /api/v1/dsl/config-defaults", cors(log(auth(http.HandlerFunc(s.handleConfigDefaults)))))
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
	s.mux.Handle("POST /api/v1/scripts/import/enterprise-script-archive/validate", cors(log(auth(http.HandlerFunc(s.handleValidateEnterpriseArchiveImport)))))
	s.mux.Handle("GET /api/v1/scripts/import/enterprise-script-archive/support-matrix", cors(log(auth(http.HandlerFunc(s.handleEnterpriseArchiveSupportMatrix)))))
	s.mux.Handle("POST /api/v1/scripts/import/jmeter", cors(log(auth(http.HandlerFunc(s.handleImportJMeter)))))
	s.mux.Handle("POST /api/v1/scripts/import/jmeter/save", cors(log(auth(http.HandlerFunc(s.handleSaveJMeterImport)))))
	s.mux.Handle("POST /api/v1/scripts/import/jmeter/validate", cors(log(auth(http.HandlerFunc(s.handleValidateJMeterImport)))))
	s.mux.Handle("GET /api/v1/scripts/import/jmeter/support-matrix", cors(log(auth(http.HandlerFunc(s.handleJMeterSupportMatrix)))))
	s.mux.Handle("GET /api/v1/scripts/web-api/support-matrix", cors(log(auth(http.HandlerFunc(s.handleWebAPIExecutionSupportMatrix)))))
	s.mux.Handle("POST /api/v1/scripts/import/sample", cors(log(auth(http.HandlerFunc(s.handleImportSample)))))
	s.mux.Handle("POST /api/v1/scripts/import/{id}/assets/upload", cors(log(auth(http.HandlerFunc(s.handleUploadImportAsset)))))
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
	s.mux.Handle("POST /api/v1/scripts/import/openapi", cors(log(auth(http.HandlerFunc(s.handleImportOpenAPI)))))
	s.mux.Handle("POST /api/v1/scripts/import/openapi/commit", cors(log(auth(http.HandlerFunc(s.handleCommitOpenAPIImport)))))
	s.mux.Handle("GET /api/v1/scripts/import/openapi/support-matrix", cors(log(auth(http.HandlerFunc(s.handleOpenAPISupportMatrix)))))
	s.mux.Handle("POST /api/v1/scripts/import/bruno/inspect", cors(log(auth(http.HandlerFunc(s.handleInspectBrunoImport)))))
	s.mux.Handle("POST /api/v1/scripts/import/bruno/commit", cors(log(auth(http.HandlerFunc(s.handleCommitBrunoImport)))))
	s.mux.Handle("GET /api/v1/scripts/import/bruno/support-matrix", cors(log(auth(http.HandlerFunc(s.handleBrunoSupportMatrix)))))
	s.mux.Handle("POST /api/v1/scripts/import/raml", cors(log(auth(http.HandlerFunc(s.handleImportRAML)))))
	s.mux.Handle("GET /api/v1/scripts/import/raml/support-matrix", cors(log(auth(http.HandlerFunc(s.handleRAMLSupportMatrix)))))
	s.mux.Handle("POST /api/v1/scripts/import/postman", cors(log(auth(http.HandlerFunc(s.handleImportPostman)))))
	s.mux.Handle("POST /api/v1/scripts/import/postman/validate", cors(log(auth(http.HandlerFunc(s.handleValidatePostmanImport)))))
	s.mux.Handle("POST /api/v1/scripts/import/postman/save", cors(log(auth(http.HandlerFunc(s.handleSavePostmanImport)))))
	s.mux.Handle("GET /api/v1/scripts/import/postman/support-matrix", cors(log(auth(http.HandlerFunc(s.handlePostmanSupportMatrix)))))
	s.mux.Handle("POST /api/v1/scripts/import/wsdl", cors(log(auth(http.HandlerFunc(s.handleImportWSDL)))))
	s.mux.Handle("POST /api/v1/scripts/import/asyncapi", cors(log(auth(http.HandlerFunc(s.handleImportAsyncAPI)))))
	s.mux.Handle("POST /api/v1/scripts/import/asyncapi/commit", cors(log(auth(http.HandlerFunc(s.handleCommitAsyncAPIImport)))))
	s.mux.Handle("GET /api/v1/scripts/import/asyncapi/support-matrix", cors(log(auth(http.HandlerFunc(s.handleAsyncAPISupportMatrix)))))
	s.mux.Handle("POST /api/v1/scripts/import/eventhubs-capture", cors(log(auth(http.HandlerFunc(s.handleImportEventHubsCapture)))))
	s.mux.Handle("GET /api/v1/scripts/import/eventhubs-capture/support-matrix", cors(log(auth(http.HandlerFunc(s.handleEventHubsCaptureSupportMatrix)))))
	s.mux.Handle("POST /api/v1/scripts/import/broker-config", cors(log(auth(http.HandlerFunc(s.handleImportBrokerConfig)))))
	s.mux.Handle("GET /api/v1/scripts/import/broker-config/support-matrix", cors(log(auth(http.HandlerFunc(s.handleBrokerConfigSupportMatrix)))))
	s.mux.Handle("POST /api/v1/scripts/import/parameterize/apply", cors(log(auth(http.HandlerFunc(s.handleApplyImportParameterization)))))
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
	s.mux.Handle("GET /api/v1/auth/callback", cors(log(http.HandlerFunc(s.handleOIDCCallback))))
	s.mux.Handle("POST /api/v1/auth/callback", cors(log(http.HandlerFunc(s.handleOIDCCallback))))
	s.mux.Handle("POST /api/v1/auth/logout", cors(log(http.HandlerFunc(s.handleOIDCLogout))))
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
	s.mux.Handle("GET /api/v1/docs/swagger.yaml", cors(log(auth(http.HandlerFunc(s.handleGetAPIDocsSwagger)))))
	s.mux.Handle("GET /api/v1/docs/endpoint-catalog", cors(log(auth(http.HandlerFunc(s.handleGetAPIDocsEndpointCatalog)))))
	s.mux.Handle("GET /api/v1/system/health", cors(log(http.HandlerFunc(s.handleHealthCheck))))
	s.mux.Handle("GET /api/v1/system/version", cors(log(http.HandlerFunc(s.handleSystemVersion))))
	s.mux.Handle("GET /api/v1/system/readiness", cors(log(http.HandlerFunc(s.handleSystemReadiness))))
	s.mux.Handle("GET /api/v1/system/execution-readiness", cors(log(http.HandlerFunc(s.handleExecutionReadiness))))
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
	s.mux.Handle("GET /api/v1/system/inventory", cors(log(auth(http.HandlerFunc(s.handleGetSystemInventory)))))
	s.mux.Handle("GET /api/v1/system/services", cors(log(auth(http.HandlerFunc(s.handleGetSystemServices)))))
	s.mux.Handle("GET /api/v1/system/usage", cors(log(auth(http.HandlerFunc(s.handleGetUsageAccounting)))))
	s.mux.Handle("GET /api/v1/system/usage/export", cors(log(auth(http.HandlerFunc(s.handleExportUsageAccounting)))))
	s.mux.Handle("GET /api/v1/system/usage/snapshots", cors(log(auth(http.HandlerFunc(s.handleListUsageAccountingSnapshots)))))
	s.mux.Handle("POST /api/v1/system/usage/snapshots", cors(log(auth(http.HandlerFunc(s.handleCreateUsageAccountingSnapshot)))))
	s.mux.Handle("GET /api/v1/system/parity-status", cors(log(auth(http.HandlerFunc(s.handleGetParityStatus)))))
	s.mux.Handle("GET /api/v1/system/deployment-lease", cors(log(auth(http.HandlerFunc(s.handleGetDeploymentLease)))))
	s.mux.Handle("POST /api/v1/system/deployment-lease/acquire", cors(log(auth(http.HandlerFunc(s.handleAcquireDeploymentLease)))))
	s.mux.Handle("POST /api/v1/system/deployment-lease/renew", cors(log(auth(http.HandlerFunc(s.handleRenewDeploymentLease)))))
	s.mux.Handle("POST /api/v1/system/deployment-lease/release", cors(log(auth(http.HandlerFunc(s.handleReleaseDeploymentLease)))))
	s.mux.Handle("GET /api/v1/system/browser-benchmarks", cors(log(auth(http.HandlerFunc(s.handleGetBrowserBenchmarkEvidence)))))
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
	s.mux.Handle("GET /api/v1/fleet/alerts", cors(log(auth(http.HandlerFunc(s.handleGetFleetAlerts)))))
	s.mux.Handle("PATCH /api/v1/fleet/alerts/{id}/ack", cors(log(auth(http.HandlerFunc(s.handleAckFleetAlert)))))
	s.mux.Handle("GET /api/v1/fleet/capacity", cors(log(auth(http.HandlerFunc(s.handleGetFleetCapacity)))))
	s.mux.Handle("GET /api/v1/fleet/load-zones", cors(log(auth(http.HandlerFunc(s.handleGetFleetLoadZones)))))
	s.mux.Handle("GET /api/v1/fleet/provisioner", cors(log(auth(http.HandlerFunc(s.handleGetFleetProvisioner)))))
	// Fleet management: pools, generator metadata, and cloud provisioning.
	s.mux.Handle("POST /api/v1/fleet/pools", cors(log(auth(http.HandlerFunc(s.handleCreateFleetPool)))))
	s.mux.Handle("GET /api/v1/fleet/pools", cors(log(auth(http.HandlerFunc(s.handleListFleetPools)))))
	s.mux.Handle("GET /api/v1/fleet/pools/{id}", cors(log(auth(http.HandlerFunc(s.handleGetFleetPool)))))
	s.mux.Handle("PUT /api/v1/fleet/pools/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateFleetPool)))))
	s.mux.Handle("DELETE /api/v1/fleet/pools/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteFleetPool)))))
	s.mux.Handle("GET /api/v1/fleet/generators", cors(log(auth(http.HandlerFunc(s.handleListGeneratorMetadata)))))
	s.mux.Handle("PATCH /api/v1/fleet/generators/{id}", cors(log(auth(http.HandlerFunc(s.handlePatchGeneratorMetadata)))))
	s.mux.Handle("POST /api/v1/fleet/generators/{id}/cordon", cors(log(auth(http.HandlerFunc(s.handleCordonGenerator)))))
	s.mux.Handle("POST /api/v1/fleet/generators/{id}/uncordon", cors(log(auth(http.HandlerFunc(s.handleUncordonGenerator)))))
	s.mux.Handle("POST /api/v1/fleet/generators/{id}/drain", cors(log(auth(http.HandlerFunc(s.handleDrainGenerator)))))
	s.mux.Handle("POST /api/v1/fleet/generators/{id}/evacuate", cors(log(auth(http.HandlerFunc(s.handleEvacuateGenerator)))))
	s.mux.Handle("POST /api/v1/fleet/provision-requests", cors(log(auth(http.HandlerFunc(s.handleCreateProvisioningRequest)))))
	s.mux.Handle("GET /api/v1/fleet/provision-requests", cors(log(auth(http.HandlerFunc(s.handleListProvisioningRequests)))))
	s.mux.Handle("GET /api/v1/fleet/provision-requests/{id}", cors(log(auth(http.HandlerFunc(s.handleGetProvisioningRequest)))))
	s.mux.Handle("POST /api/v1/fleet/provision-requests/{id}/cancel", cors(log(auth(http.HandlerFunc(s.handleCancelProvisioningRequest)))))
	s.mux.Handle("POST /api/v1/fleet/provision-requests/{id}/retry", cors(log(auth(http.HandlerFunc(s.handleRetryProvisioningRequest)))))
	s.mux.Handle("PATCH /api/v1/system/generators/drain", cors(log(auth(http.HandlerFunc(s.handleSetGeneratorDrainState)))))
	s.mux.Handle("GET /api/v1/system/restarts/capabilities", cors(log(auth(http.HandlerFunc(s.handleGetSystemRestartCapabilities)))))
	s.mux.Handle("POST /api/v1/system/restarts", cors(log(auth(http.HandlerFunc(s.handleSystemRestart)))))
	s.mux.Handle("GET /api/v1/system/settings", cors(log(auth(http.HandlerFunc(s.handleGetSystemSettings)))))
	s.mux.Handle("PUT /api/v1/system/settings", cors(log(auth(http.HandlerFunc(s.handleUpdateSystemSettings)))))

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
	if err := s.beginServerStartup(); err != nil {
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
	if s.bridgeMode {
		return s.startBridge()
	}
	if err := s.enforceAuthPosture(); err != nil {
		return err
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
	s.startGeneratorInventoryRefreshCadence()
	s.startAuditRetentionCadence()
	s.startTimeslotRetentionCadence()
	s.startArchiveCleanupRetry()
	s.startArchiveFinalizationRetry()
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
	s.setBoundAddr(listener.Addr().String())
	s.finishServerStartup()
	slog.Info("apexload API server starting", "addr", s.httpServer.Addr, "bound_addr", s.BoundAddr())
	if err := s.httpServer.Serve(listener); err != http.ErrServerClosed {
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

func (s *Server) finishShutdown(err error) {
	s.lifecycleMu.Lock()
	s.shutdownErr = err
	close(s.shutdownDone)
	s.lifecycleMu.Unlock()
}

func (s *Server) beginServerStartup() error {
	if s == nil {
		return http.ErrServerClosed
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
		return http.ErrServerClosed
	}
	if s.startupStarted {
		return fmt.Errorf("API server startup already began")
	}
	s.startupStarted = true
	return nil
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
	if s.runner != nil {
		s.runner.beginShutdown()
	}
	s.beginBackgroundShutdown()
	s.clearAllSourceSecretSessions()
	// Stop accepting new requests first. WebSocket connections are hijacked and
	// are closed by the hub below; ordinary in-flight handlers get the caller's
	// bounded drain window.
	var shutdownErr error
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
	if s.browserReplay != nil {
		s.browserReplay.Close()
	}
	if s.scriptDebug != nil {
		s.scriptDebug.Close()
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
// headless controller (scheduled runs, CI launches) would fall back to
// configured-only capacity and over-admit when generators are down.
//
// It is gated on the STRUCTURAL capability (a distributed inventory backend),
// NOT the availability SETTING, because that setting is runtime-mutable
// (UpdateSystemSettings): if we keyed the goroutine's existence on the setting
// at boot, flipping to configured_plus_live later would never start the cadence
// and the cache would stay cold. Instead the per-tick refresh checks the current
// setting and only probes when live availability actually feeds admission.
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
// not stall the refresh. It no-ops when live availability is not the active
// source, so a configured-only deployment carries only an idle ticker.
func (s *Server) refreshGeneratorInventory(ctx context.Context) {
	if s == nil || s.runner == nil {
		return
	}
	if s.runner.GetSystemSettings().CapacityPlanning.Availability.Source != capacityAvailabilityConfiguredPlusLive {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			slog.Error("generator inventory refresh panicked", "panic", r)
		}
	}()
	probeCtx, cancel := context.WithTimeout(ctx, generatorInventoryRefreshTimeout)
	defer cancel()
	s.systemInventory(probeCtx)
}

func (s *Server) RunDueSchedules() error {
	if s == nil || s.scheduler == nil {
		return nil
	}
	return s.scheduler.RunDueSchedules()
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
