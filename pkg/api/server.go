package api

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/recorder"
	"github.com/jasonczech/apexload/pkg/storage"
)

//go:embed static
var StaticFS embed.FS

var dashboardAssetRefPattern = regexp.MustCompile(`(?:href|src)="/(assets/[^"]+\.(?:css|js))"`)

type Server struct {
	httpServer         *http.Server
	hub                *Hub
	runner             *TestRunner
	scheduler          *scenarioScheduler
	browserReplay      *BrowserReplayManager
	scriptDebug        *ScriptDebugManager
	browserRecording   *BrowserRecordingManager
	browserInstall     *browserInstallState
	recorder           *recorder.Recorder
	store              storage.Store
	cache              CacheBackend
	controllerID       string
	authoringMu        sync.Mutex
	authoring          map[string]*storage.AuthoringSession
	libraryMu          sync.Mutex
	libraryRemotes     map[string]*LibraryRemote
	libraryLinks       map[string]*librarySyncLink
	libraryCreds       LibraryCredentialStore
	c4Mu               sync.Mutex
	slaWaivers         map[string][]*storage.ReleaseSLAWaiver
	resultImports      map[string]*storage.ExternalResultImport
	githubMu           sync.Mutex
	githubTokens       map[string]githubCachedToken
	mux                *http.ServeMux
	usageCancel        context.CancelFunc
	fanoutCancel       context.CancelFunc
	fanoutSub          FanoutSubscription
	generatorDiscovery *generatorDiscovery
	discoveryCancel    context.CancelFunc
	k8sRestart         K8sRestartSettings
	kubeRestarter      KubeRestarter
	restartMu          sync.Mutex
	restartInFlight    map[string]struct{}
	metricsAddr        string
	metricsServer      *http.Server
	metricsBoundAddr   string
	desktopMode        bool
	bridgeMode         bool
	recordingBridge    *BrowserRecordingBridge
	desktopAppData     string
	desktopLogPath     string
	bindScope          string
	boundAddr          string
	boundAddrMu        sync.RWMutex
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
	MetricsAddr        string
	ProxyAddr          string
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
	BridgeMode         bool
	DesktopAppDataPath string
	DesktopLogPath     string
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
	runner := NewTestRunner(cfg.MaxConcurrent, hub, cfg.Store)
	runner.setLiveStateStore(cache)
	staticTargets, discoverySpecs := splitGeneratorTargets(cfg.GeneratorTargets)
	var distributedBackend *distributedExecutionBackend
	var discovery *generatorDiscovery
	if len(staticTargets) > 0 || len(discoverySpecs) > 0 {
		distributedBackend = newDistributedExecutionBackend(staticTargets, cfg.GeneratorTimeout, nil)
		distributedBackend.ApplyGeneratorDrainStates(loadGeneratorDrainStates(cfg.Store))
		runner.setExecutionBackend(distributedBackend)
		if len(discoverySpecs) > 0 {
			discovery = newGeneratorDiscovery(staticTargets, discoverySpecs, distributedBackend)
		}
	}
	rec := recorder.New(cfg.ProxyAddr)

	s := &Server{
		hub:              hub,
		runner:           runner,
		scheduler:        newScenarioScheduler(runner),
		browserReplay:    NewBrowserReplayManager(hub),
		scriptDebug:      NewScriptDebugManager(hub),
		browserRecording: NewBrowserRecordingManager(hub),
		browserInstall:   newBrowserInstallState(),
		recorder:         rec,
		store:            cfg.Store,
		cache:            cache,
		controllerID:     controllerID,
		authoring:        make(map[string]*storage.AuthoringSession),
		libraryRemotes:   make(map[string]*LibraryRemote),
		libraryLinks:     make(map[string]*librarySyncLink),
		libraryCreds:     cfg.LibraryCredentials,
		slaWaivers:       make(map[string][]*storage.ReleaseSLAWaiver),
		resultImports:    make(map[string]*storage.ExternalResultImport),
		githubTokens:     make(map[string]githubCachedToken),
		mux:              http.NewServeMux(),
		desktopMode:      cfg.DesktopMode,
		bridgeMode:       cfg.BridgeMode,
		desktopAppData:   cfg.DesktopAppDataPath,
		desktopLogPath:   cfg.DesktopLogPath,
		bindScope:        cfg.BindScope,

		generatorDiscovery: discovery,
		k8sRestart:         normalizeK8sRestartSettings(cfg.K8sRestart),
		kubeRestarter:      cfg.KubeRestarter,
		restartInFlight:    make(map[string]struct{}),
		metricsAddr:        strings.TrimSpace(cfg.MetricsAddr),
		staticFS:           StaticFS,
	}

	s.loadLibraryState()
	s.routes(cfg.AllowedOrigins)

	s.httpServer = &http.Server{
		Addr:           cfg.Addr,
		Handler:        securityHeadersMiddleware(bodyLimitMiddleware(s.mux)),
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
	auth := authMiddleware(func() AuthSettings { return s.runner.GetSystemSettings().Auth }, s.store, s.cache)
	recorderViewAuth := projectScopedAuthMiddleware(
		func() AuthSettings { return s.runner.GetSystemSettings().Auth },
		s.store,
		s.cache,
		[]string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		[]string{RoleViewer, RoleEditor, RoleProjectAdmin},
		resolveRecorderListProjectID,
	)
	recorderCreateAuth := projectScopedAuthMiddleware(
		func() AuthSettings { return s.runner.GetSystemSettings().Auth },
		s.store,
		s.cache,
		[]string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		[]string{RoleEditor, RoleProjectAdmin},
		resolveRecorderCreateProjectID,
	)
	recorderSessionViewAuth := projectScopedAuthMiddleware(
		func() AuthSettings { return s.runner.GetSystemSettings().Auth },
		s.store,
		s.cache,
		[]string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		[]string{RoleViewer, RoleEditor, RoleProjectAdmin},
		resolveRecorderSessionProjectID(s.recorder),
	)
	recorderSessionEditAuth := projectScopedAuthMiddleware(
		func() AuthSettings { return s.runner.GetSystemSettings().Auth },
		s.store,
		s.cache,
		[]string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		[]string{RoleEditor, RoleProjectAdmin},
		resolveRecorderSessionProjectID(s.recorder),
	)
	browserReplaySessionViewAuth := projectScopedAuthMiddleware(
		func() AuthSettings { return s.runner.GetSystemSettings().Auth },
		s.store,
		s.cache,
		[]string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		[]string{RoleViewer, RoleEditor, RoleProjectAdmin},
		resolveBrowserReplaySessionProjectID(s.browserReplay),
	)
	browserReplaySessionEditAuth := projectScopedAuthMiddleware(
		func() AuthSettings { return s.runner.GetSystemSettings().Auth },
		s.store,
		s.cache,
		[]string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		[]string{RoleEditor, RoleProjectAdmin},
		resolveBrowserReplaySessionProjectID(s.browserReplay),
	)
	scriptDebugSessionViewAuth := projectScopedAuthMiddleware(
		func() AuthSettings { return s.runner.GetSystemSettings().Auth },
		s.store,
		s.cache,
		[]string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		[]string{RoleViewer, RoleEditor, RoleProjectAdmin},
		resolveScriptDebugSessionProjectID(s.scriptDebug),
	)
	scriptDebugSessionEditAuth := projectScopedAuthMiddleware(
		func() AuthSettings { return s.runner.GetSystemSettings().Auth },
		s.store,
		s.cache,
		[]string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		[]string{RoleEditor, RoleProjectAdmin},
		resolveScriptDebugSessionProjectID(s.scriptDebug),
	)
	browserRecordingCreateAuth := projectScopedAuthMiddleware(
		func() AuthSettings { return s.runner.GetSystemSettings().Auth },
		s.store,
		s.cache,
		[]string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		[]string{RoleEditor, RoleProjectAdmin},
		resolveRecorderCreateProjectID,
	)
	browserRecordingSessionViewAuth := projectScopedAuthMiddleware(
		func() AuthSettings { return s.runner.GetSystemSettings().Auth },
		s.store,
		s.cache,
		[]string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		[]string{RoleViewer, RoleEditor, RoleProjectAdmin},
		resolveBrowserRecordingSessionProjectID(s.browserRecording),
	)
	browserRecordingSessionEditAuth := projectScopedAuthMiddleware(
		func() AuthSettings { return s.runner.GetSystemSettings().Auth },
		s.store,
		s.cache,
		[]string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		[]string{RoleEditor, RoleProjectAdmin},
		resolveBrowserRecordingSessionProjectID(s.browserRecording),
	)

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
	s.mux.Handle("GET /api/v1/tests/{id}/archive/files/{name}", cors(log(auth(http.HandlerFunc(s.handleDownloadRunArchiveArtifact)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/browser-diagnostics", cors(log(auth(http.HandlerFunc(s.handleDownloadRunArchiveBrowserDiagnostics)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/transactions", cors(log(auth(http.HandlerFunc(s.handleGetRunArchiveTransactions)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/resources", cors(log(auth(http.HandlerFunc(s.handleGetRunArchiveResources)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/summaries", cors(log(auth(http.HandlerFunc(s.handleGetRunArchiveSummaries)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/report", cors(log(auth(http.HandlerFunc(s.handleGetRunArchiveReport)))))
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
	s.mux.Handle("POST /api/v1/scenarios/{id}/schedules", cors(log(auth(http.HandlerFunc(s.handleCreateScenarioSchedule)))))
	s.mux.Handle("GET /api/v1/scenarios/{id}/schedules", cors(log(auth(http.HandlerFunc(s.handleListScenarioSchedules)))))
	s.mux.Handle("PUT /api/v1/scenarios/{id}/schedules/{schedule_id}", cors(log(auth(http.HandlerFunc(s.handleUpdateScenarioSchedule)))))
	s.mux.Handle("DELETE /api/v1/scenarios/{id}/schedules/{schedule_id}", cors(log(auth(http.HandlerFunc(s.handleDeleteScenarioSchedule)))))
	s.mux.Handle("GET /api/v1/scenarios/{id}/schedules/{schedule_id}/occurrences", cors(log(auth(http.HandlerFunc(s.handleListScenarioScheduleOccurrences)))))
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

	// Monitoring collectors — restricted-network agent control plane.
	s.mux.Handle("POST /api/v1/monitoring/collectors", cors(log(auth(http.HandlerFunc(s.handleCreateMonitoringCollector)))))
	s.mux.Handle("GET /api/v1/monitoring/collectors", cors(log(auth(http.HandlerFunc(s.handleListMonitoringCollectors)))))
	s.mux.Handle("GET /api/v1/monitoring/collectors/{id}", cors(log(auth(http.HandlerFunc(s.handleGetMonitoringCollector)))))
	s.mux.Handle("PATCH /api/v1/monitoring/collectors/{id}", cors(log(auth(http.HandlerFunc(s.handlePatchMonitoringCollector)))))
	s.mux.Handle("DELETE /api/v1/monitoring/collectors/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteMonitoringCollector)))))
	s.mux.Handle("POST /api/v1/monitoring/collectors/{id}/rotate-token", cors(log(auth(http.HandlerFunc(s.handleRotateMonitoringCollectorToken)))))

	// Enterprise entitlements — read-only consumption vs limits.
	s.mux.Handle("GET /api/v1/system/entitlements", cors(log(auth(http.HandlerFunc(s.handleGetEntitlements)))))
	s.mux.Handle("GET /api/v1/system/entitlements/usage", cors(log(auth(http.HandlerFunc(s.handleGetEntitlementsUsage)))))

	// Script CRUD, validation, import, and export endpoints.
	s.mux.Handle("POST /api/v1/scripts", cors(log(auth(http.HandlerFunc(s.handleCreateScript)))))
	s.mux.Handle("GET /api/v1/scripts", cors(log(auth(http.HandlerFunc(s.handleListScripts)))))
	s.mux.Handle("GET /api/v1/scripts/{id}", cors(log(auth(http.HandlerFunc(s.handleGetScript)))))
	s.mux.Handle("PUT /api/v1/scripts/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateScript)))))
	s.mux.Handle("DELETE /api/v1/scripts/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteScript)))))
	s.mux.Handle("PATCH /api/v1/scripts/{id}/move", cors(log(auth(http.HandlerFunc(s.handleMoveScript)))))
	s.mux.Handle("POST /api/v1/scripts/validate", cors(log(auth(http.HandlerFunc(s.handleValidateDraftScript)))))
	s.mux.Handle("POST /api/v1/scripts/replay", cors(log(auth(http.HandlerFunc(s.handleReplayDraftScript)))))
	s.mux.Handle("POST /api/v1/scripts/replay/sessions", cors(log(auth(http.HandlerFunc(s.handleStartReplaySessionDraftScript)))))
	s.mux.Handle("POST /api/v1/scripts/debug/sessions", cors(log(auth(http.HandlerFunc(s.handleStartScriptDebugDraftScript)))))
	s.mux.Handle("POST /api/v1/scripts/browser-replay/sessions", cors(log(auth(http.HandlerFunc(s.handleStartBrowserReplayDraftScript)))))
	s.mux.Handle("POST /api/v1/scripts/browser-to-http/preview", cors(log(auth(http.HandlerFunc(s.handlePreviewBrowserToHTTP)))))
	s.mux.Handle("GET /api/v1/scripts/authoring/capabilities", cors(log(auth(http.HandlerFunc(s.handleAuthoringCapabilities)))))
	s.mux.Handle("GET /api/v1/scripts/authoring/templates", cors(log(auth(http.HandlerFunc(s.handleAuthoringTemplates)))))
	s.mux.Handle("GET /api/v1/scripts/authoring/components", cors(log(auth(http.HandlerFunc(s.handleAuthoringComponents)))))
	s.mux.Handle("POST /api/v1/scripts/authoring/sessions", cors(log(auth(http.HandlerFunc(s.handleCreateAuthoringSession)))))
	s.mux.Handle("GET /api/v1/scripts/authoring/sessions", cors(log(auth(http.HandlerFunc(s.handleListAuthoringSessions)))))
	s.mux.Handle("GET /api/v1/scripts/authoring/sessions/{id}", cors(log(auth(http.HandlerFunc(s.handleGetAuthoringSession)))))
	s.mux.Handle("PUT /api/v1/scripts/authoring/sessions/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateAuthoringSession)))))
	s.mux.Handle("DELETE /api/v1/scripts/authoring/sessions/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteAuthoringSession)))))
	s.mux.Handle("POST /api/v1/scripts/authoring/sessions/{id}/approve", cors(log(auth(http.HandlerFunc(s.handleApproveAuthoringSession)))))
	s.mux.Handle("POST /api/v1/scripts/authoring/sessions/{id}/correlations/preview", cors(log(auth(http.HandlerFunc(s.handlePreviewAuthoringCorrelation)))))
	s.mux.Handle("GET /api/v1/correlations/rules", cors(log(auth(http.HandlerFunc(s.handleListCorrelationRules)))))
	s.mux.Handle("GET /api/v1/dsl/actions", cors(log(auth(http.HandlerFunc(s.handleListDSLActions)))))
	s.mux.Handle("POST /api/v1/correlations/diagnose", cors(log(auth(http.HandlerFunc(s.handleDiagnoseCorrelations)))))
	s.mux.Handle("POST /api/v1/correlations/diff", cors(log(auth(http.HandlerFunc(s.handleDiffCorrelations)))))
	s.mux.Handle("POST /api/v1/selectors/diagnose", cors(log(auth(http.HandlerFunc(s.handleDiagnoseSelectors)))))
	s.mux.Handle("POST /api/v1/scripts/{id}/validate", cors(log(auth(http.HandlerFunc(s.handleValidateScript)))))
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
	s.mux.Handle("POST /api/v1/scripts/import/openapi", cors(log(auth(http.HandlerFunc(s.handleImportOpenAPI)))))
	s.mux.Handle("POST /api/v1/scripts/import/raml", cors(log(auth(http.HandlerFunc(s.handleImportRAML)))))
	s.mux.Handle("GET /api/v1/scripts/import/raml/support-matrix", cors(log(auth(http.HandlerFunc(s.handleRAMLSupportMatrix)))))
	s.mux.Handle("POST /api/v1/scripts/import/postman", cors(log(auth(http.HandlerFunc(s.handleImportPostman)))))
	s.mux.Handle("POST /api/v1/scripts/import/wsdl", cors(log(auth(http.HandlerFunc(s.handleImportWSDL)))))
	s.mux.Handle("POST /api/v1/scripts/import/asyncapi", cors(log(auth(http.HandlerFunc(s.handleImportAsyncAPI)))))
	s.mux.Handle("GET /api/v1/scripts/import/asyncapi/support-matrix", cors(log(auth(http.HandlerFunc(s.handleAsyncAPISupportMatrix)))))
	s.mux.Handle("POST /api/v1/scripts/import/parameterize/apply", cors(log(auth(http.HandlerFunc(s.handleApplyImportParameterization)))))

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
	s.mux.Handle("GET /api/v1/replay/sessions/{session_id}", cors(log(scriptDebugSessionViewAuth(http.HandlerFunc(s.handleGetScriptDebugSession)))))
	s.mux.Handle("GET /api/v1/replay/sessions/{session_id}/export", cors(log(scriptDebugSessionViewAuth(http.HandlerFunc(s.handleExportScriptDebugSession)))))
	s.mux.Handle("POST /api/v1/replay/sessions/{session_id}/pause", cors(log(scriptDebugSessionEditAuth(http.HandlerFunc(s.handlePauseScriptDebugSession)))))
	s.mux.Handle("POST /api/v1/replay/sessions/{session_id}/resume", cors(log(scriptDebugSessionEditAuth(http.HandlerFunc(s.handleResumeScriptDebugSession)))))
	s.mux.Handle("POST /api/v1/replay/sessions/{session_id}/step", cors(log(scriptDebugSessionEditAuth(http.HandlerFunc(s.handleStepScriptDebugSession)))))
	s.mux.Handle("POST /api/v1/replay/sessions/{session_id}/stop", cors(log(scriptDebugSessionEditAuth(http.HandlerFunc(s.handleStopScriptDebugSession)))))
	s.mux.Handle("PATCH /api/v1/replay/sessions/{session_id}/breakpoints", cors(log(scriptDebugSessionEditAuth(http.HandlerFunc(s.handleUpdateScriptDebugBreakpoints)))))

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
	s.mux.Handle("POST /api/v1/browser-recorder/sessions", cors(log(browserRecordingCreateAuth(http.HandlerFunc(s.handleStartBrowserRecording)))))
	s.mux.Handle("GET /api/v1/browser-recorder/sessions/{id}", cors(log(browserRecordingSessionViewAuth(http.HandlerFunc(s.handleGetBrowserRecording)))))
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
	s.mux.Handle("POST /api/v1/auth/logout", cors(log(http.HandlerFunc(s.handleOIDCLogout))))
	s.mux.Handle("GET /api/v1/auth/me", cors(log(http.HandlerFunc(s.handleGetAuthStatus))))
	s.mux.Handle("GET /api/v1/auth/diagnostics", cors(log(http.HandlerFunc(s.handleGetAuthDiagnostics))))
	s.mux.Handle("GET /api/v1/auth/roles", cors(log(http.HandlerFunc(s.handleGetAuthRoleCatalog))))
	s.mux.Handle("GET /api/v1/auth/access-matrix", cors(log(http.HandlerFunc(s.handleGetAuthAccessMatrix))))
	s.mux.Handle("GET /api/v1/auth/effective-access", cors(log(http.HandlerFunc(s.handleGetAuthEffectiveAccess))))
	s.mux.Handle("GET /api/v1/auth/users", cors(log(auth(http.HandlerFunc(s.handleListUsers)))))
	s.mux.Handle("POST /api/v1/auth/users", cors(log(auth(http.HandlerFunc(s.handleCreateUser)))))
	s.mux.Handle("GET /api/v1/auth/users/{id}", cors(log(auth(http.HandlerFunc(s.handleGetUser)))))
	s.mux.Handle("PATCH /api/v1/auth/users/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateUser)))))
	s.mux.Handle("POST /api/v1/auth/users/{id}/bindings", cors(log(auth(http.HandlerFunc(s.handleCreateUserBinding)))))
	s.mux.Handle("DELETE /api/v1/auth/users/{id}/bindings/{binding_id}", cors(log(auth(http.HandlerFunc(s.handleDeleteUserBinding)))))
	s.mux.Handle("POST /api/v1/auth/users/{id}/disable", cors(log(auth(http.HandlerFunc(s.handleDisableUser)))))
	s.mux.Handle("POST /api/v1/auth/users/{id}/enable", cors(log(auth(http.HandlerFunc(s.handleEnableUser)))))
	s.mux.Handle("GET /api/v1/auth/user-activity", cors(log(auth(http.HandlerFunc(s.handleGetUserActivity)))))
	s.mux.Handle("GET /api/v1/auth/user-activity/export", cors(log(auth(http.HandlerFunc(s.handleExportUserActivity)))))
	s.mux.Handle("GET /api/v1/auth/service-accounts", cors(log(auth(http.HandlerFunc(s.handleListServiceAccounts)))))
	s.mux.Handle("POST /api/v1/auth/service-accounts", cors(log(auth(http.HandlerFunc(s.handleCreateServiceAccount)))))
	s.mux.Handle("PATCH /api/v1/auth/service-accounts/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateServiceAccount)))))
	s.mux.Handle("POST /api/v1/auth/service-accounts/{id}/rotate", cors(log(auth(http.HandlerFunc(s.handleRotateServiceAccount)))))
	s.mux.Handle("DELETE /api/v1/auth/service-accounts/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteServiceAccount)))))
	s.mux.Handle("GET /api/v1/auth/audit-events", cors(log(auth(http.HandlerFunc(s.handleListAuditEvents)))))
	s.mux.Handle("GET /api/v1/auth/audit-events/export", cors(log(auth(http.HandlerFunc(s.handleExportAuditEvents)))))
	s.mux.Handle("GET /api/v1/system/health", cors(log(http.HandlerFunc(s.handleHealthCheck))))
	s.mux.Handle("GET /api/v1/system/version", cors(log(http.HandlerFunc(s.handleSystemVersion))))
	s.mux.Handle("GET /api/v1/system/readiness", cors(log(http.HandlerFunc(s.handleSystemReadiness))))
	s.mux.Handle("GET /api/v1/downloads/recording-agent/latest", cors(log(auth(http.HandlerFunc(s.handleDownloadRecordingAgent)))))
	s.mux.Handle("GET /api/v1/downloads/recording-agent/latest/checksum", cors(log(auth(http.HandlerFunc(s.handleRecordingAgentChecksum)))))
	s.mux.Handle("GET /api/v1/downloads/desktop/latest", cors(log(auth(http.HandlerFunc(s.handleDownloadDesktopApp)))))
	s.mux.Handle("GET /api/v1/downloads/desktop/latest/checksum", cors(log(auth(http.HandlerFunc(s.handleDesktopAppChecksum)))))
	// With a dedicated metrics listener (APEXLOAD_METRICS_ADDR) the scrape
	// endpoint moves off the API port entirely, so NetworkPolicy can grant the
	// monitoring namespace metrics access without exposing the API surface.
	if s.metricsAddr == "" {
		s.mux.Handle("GET /metrics", log(http.HandlerFunc(s.handlePrometheusMetrics)))
	}
	s.mux.Handle("GET /api/v1/system/inventory", cors(log(auth(http.HandlerFunc(s.handleGetSystemInventory)))))
	s.mux.Handle("GET /api/v1/system/services", cors(log(auth(http.HandlerFunc(s.handleGetSystemServices)))))
	s.mux.Handle("GET /api/v1/system/usage", cors(log(auth(http.HandlerFunc(s.handleGetUsageAccounting)))))
	s.mux.Handle("GET /api/v1/system/usage/export", cors(log(auth(http.HandlerFunc(s.handleExportUsageAccounting)))))
	s.mux.Handle("GET /api/v1/system/usage/snapshots", cors(log(auth(http.HandlerFunc(s.handleListUsageAccountingSnapshots)))))
	s.mux.Handle("POST /api/v1/system/usage/snapshots", cors(log(auth(http.HandlerFunc(s.handleCreateUsageAccountingSnapshot)))))
	s.mux.Handle("GET /api/v1/system/parity-status", cors(log(auth(http.HandlerFunc(s.handleGetParityStatus)))))
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
	s.mux.Handle("GET /api/v1/integrations/github/callback", cors(log(auth(http.HandlerFunc(s.handleGitHubCallback)))))
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
	s.mux.Handle("POST /api/v1/fleet/provision-requests", cors(log(auth(http.HandlerFunc(s.handleCreateProvisioningRequest)))))
	s.mux.Handle("GET /api/v1/fleet/provision-requests/{id}", cors(log(auth(http.HandlerFunc(s.handleGetProvisioningRequest)))))
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
	mode := normalizeAuthSettings(s.runner.GetSystemSettings().Auth).Mode
	if !isAuthBypassMode(mode) {
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

func (s *Server) Start() error {
	if s.bridgeMode {
		return s.startBridge()
	}
	if err := s.enforceAuthPosture(); err != nil {
		return err
	}
	// Start websocket broadcast loop before serving HTTP requests.
	go s.hub.Run()
	s.startCacheFanout()
	s.startGeneratorDiscovery()

	if err := s.recorder.StartProxy(); err != nil {
		return fmt.Errorf("starting recording proxy: %w", err)
	}
	s.startUsageAccountingSnapshotCadence()

	if err := s.startMetricsListener(); err != nil {
		return err
	}

	listener, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return err
	}
	s.setBoundAddr(listener.Addr().String())
	slog.Info("apexload API server starting", "addr", s.httpServer.Addr, "bound_addr", s.BoundAddr())
	if err := s.httpServer.Serve(listener); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) startBridge() error {
	go s.hub.Run()
	listener, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return err
	}
	s.setBoundAddr(listener.Addr().String())
	slog.Info("apexload browser recording bridge starting", "addr", s.httpServer.Addr, "bound_addr", s.BoundAddr())
	if err := s.httpServer.Serve(listener); err != http.ErrServerClosed {
		return err
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
	metricsMux.Handle("GET /metrics", loggingMiddleware(http.HandlerFunc(s.handlePrometheusMetrics)))
	s.metricsServer = &http.Server{
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
	s.boundAddrMu.Lock()
	s.metricsBoundAddr = listener.Addr().String()
	s.boundAddrMu.Unlock()
	slog.Info("metrics listener started", "addr", s.metricsAddr)
	go func() {
		if err := s.metricsServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			slog.Error("metrics listener error", "err", err)
		}
	}()
	return nil
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
	// Stop background components before closing active HTTP listeners.
	if s.usageCancel != nil {
		s.usageCancel()
		s.usageCancel = nil
	}
	if s.fanoutCancel != nil {
		s.fanoutCancel()
		s.fanoutCancel = nil
	}
	if s.discoveryCancel != nil {
		s.discoveryCancel()
		s.discoveryCancel = nil
	}
	if s.fanoutSub != nil {
		_ = s.fanoutSub.Close()
		s.fanoutSub = nil
	}
	if s.hub != nil {
		s.hub.Stop()
	}
	if s.recorder != nil {
		s.recorder.StopProxy()
	}
	if s.cache != nil {
		_ = s.cache.Close()
	}
	if s.metricsServer != nil {
		_ = s.metricsServer.Shutdown(ctx)
		s.metricsServer = nil
	}
	return s.httpServer.Shutdown(ctx)
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
	go func() {
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
			s.hub.broadcastLocal(envelope.Payload)
		}
	}()
}

func (s *Server) startUsageAccountingSnapshotCadence() {
	if s == nil || s.runner == nil || s.usageCancel != nil {
		return
	}
	interval := s.runner.UsageAccountingSnapshotInterval()
	if interval <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.usageCancel = cancel
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		s.runner.RunUsageAccountingSnapshotCadence(ctx, ticker.C)
	}()
}

func (s *Server) RunDueSchedules() error {
	if s == nil || s.scheduler == nil {
		return nil
	}
	return s.scheduler.RunDueSchedules()
}
