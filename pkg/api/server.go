package api

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/stormrunner/pkg/recorder"
	"github.com/jasonczech/stormrunner/pkg/storage"
)

//go:embed static
var staticFS embed.FS

type Server struct {
	httpServer    *http.Server
	hub           *Hub
	runner        *TestRunner
	scheduler     *scenarioScheduler
	browserReplay *BrowserReplayManager
	recorder      *recorder.Recorder
	store         storage.Store
	authoringMu   sync.Mutex
	authoring     map[string]*storage.AuthoringSession
	c4Mu          sync.Mutex
	slaWaivers    map[string][]*storage.ReleaseSLAWaiver
	resultImports map[string]*storage.ExternalResultImport
	mux           *http.ServeMux
	usageCancel   context.CancelFunc
}

// ServerConfig configures API server startup behavior and integrations.
type ServerConfig struct {
	Addr             string
	ProxyAddr        string
	MaxConcurrent    int
	AllowedOrigins   []string
	Store            storage.Store
	GeneratorTargets []string
	GeneratorTimeout time.Duration
}

// NewServer wires the HTTP server, test runner, websocket hub, recorder, and routes.
func NewServer(cfg ServerConfig) *Server {
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 10
	}

	hub := NewHub()
	runner := NewTestRunner(cfg.MaxConcurrent, hub, cfg.Store)
	if len(cfg.GeneratorTargets) > 0 {
		backend := newDistributedExecutionBackend(cfg.GeneratorTargets, cfg.GeneratorTimeout, nil)
		backend.ApplyGeneratorDrainStates(loadGeneratorDrainStates(cfg.Store))
		runner.setExecutionBackend(backend)
	}
	rec := recorder.New(cfg.ProxyAddr)

	s := &Server{
		hub:           hub,
		runner:        runner,
		scheduler:     newScenarioScheduler(runner),
		browserReplay: NewBrowserReplayManager(hub),
		recorder:      rec,
		store:         cfg.Store,
		authoring:     make(map[string]*storage.AuthoringSession),
		slaWaivers:    make(map[string][]*storage.ReleaseSLAWaiver),
		resultImports: make(map[string]*storage.ExternalResultImport),
		mux:           http.NewServeMux(),
	}

	s.routes(cfg.AllowedOrigins)

	s.httpServer = &http.Server{
		Addr:         cfg.Addr,
		Handler:      s.mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return s
}

func (s *Server) routes(allowedOrigins []string) {
	cors := corsMiddleware(allowedOrigins)
	log := loggingMiddleware
	auth := authMiddleware(func() AuthSettings { return s.runner.GetSystemSettings().Auth }, s.store)
	recorderViewAuth := projectScopedAuthMiddleware(
		func() AuthSettings { return s.runner.GetSystemSettings().Auth },
		s.store,
		[]string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		[]string{RoleViewer, RoleEditor, RoleProjectAdmin},
		resolveRecorderListProjectID,
	)
	recorderCreateAuth := projectScopedAuthMiddleware(
		func() AuthSettings { return s.runner.GetSystemSettings().Auth },
		s.store,
		[]string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		[]string{RoleEditor, RoleProjectAdmin},
		resolveRecorderCreateProjectID,
	)
	recorderSessionViewAuth := projectScopedAuthMiddleware(
		func() AuthSettings { return s.runner.GetSystemSettings().Auth },
		s.store,
		[]string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		[]string{RoleViewer, RoleEditor, RoleProjectAdmin},
		resolveRecorderSessionProjectID(s.recorder),
	)
	recorderSessionEditAuth := projectScopedAuthMiddleware(
		func() AuthSettings { return s.runner.GetSystemSettings().Auth },
		s.store,
		[]string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		[]string{RoleEditor, RoleProjectAdmin},
		resolveRecorderSessionProjectID(s.recorder),
	)
	browserReplaySessionViewAuth := projectScopedAuthMiddleware(
		func() AuthSettings { return s.runner.GetSystemSettings().Auth },
		s.store,
		[]string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		[]string{RoleViewer, RoleEditor, RoleProjectAdmin},
		resolveBrowserReplaySessionProjectID(s.browserReplay),
	)
	browserReplaySessionEditAuth := projectScopedAuthMiddleware(
		func() AuthSettings { return s.runner.GetSystemSettings().Auth },
		s.store,
		[]string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		[]string{RoleEditor, RoleProjectAdmin},
		resolveBrowserReplaySessionProjectID(s.browserReplay),
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
	s.mux.Handle("POST /api/v1/applications/{id}/releases", cors(log(auth(http.HandlerFunc(s.handleCreateRelease)))))
	s.mux.Handle("GET /api/v1/applications/{id}/releases", cors(log(auth(http.HandlerFunc(s.handleListReleases)))))
	s.mux.Handle("GET /api/v1/releases/{id}", cors(log(auth(http.HandlerFunc(s.handleGetRelease)))))
	s.mux.Handle("PUT /api/v1/releases/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateRelease)))))
	s.mux.Handle("POST /api/v1/releases/{id}/archive", cors(log(auth(http.HandlerFunc(s.handleArchiveRelease)))))
	s.mux.Handle("GET /api/v1/releases/{id}/trend/export", cors(log(auth(http.HandlerFunc(s.handleGetReleaseTrendExport)))))
	s.mux.Handle("GET /api/v1/releases/{id}/sla-governance", cors(log(auth(http.HandlerFunc(s.handleGetReleaseSLAGovernance)))))
	s.mux.Handle("POST /api/v1/releases/{id}/sla-governance/waivers", cors(log(auth(http.HandlerFunc(s.handleCreateReleaseSLAWaiver)))))
	s.mux.Handle("DELETE /api/v1/releases/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteRelease)))))

	s.mux.Handle("POST /api/v1/projects", cors(log(auth(http.HandlerFunc(s.handleCreateProject)))))
	s.mux.Handle("GET /api/v1/projects", cors(log(auth(http.HandlerFunc(s.handleListProjects)))))
	s.mux.Handle("GET /api/v1/projects/{id}", cors(log(auth(http.HandlerFunc(s.handleGetProject)))))
	s.mux.Handle("PUT /api/v1/projects/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateProject)))))
	s.mux.Handle("DELETE /api/v1/projects/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteProject)))))
	s.mux.Handle("POST /api/v1/projects/{id}/releases", cors(log(auth(http.HandlerFunc(s.handleCreateProjectRelease)))))
	s.mux.Handle("GET /api/v1/projects/{id}/releases", cors(log(auth(http.HandlerFunc(s.handleListProjectReleases)))))
	s.mux.Handle("POST /api/v1/projects/{pid}/folders", cors(log(auth(http.HandlerFunc(s.handleCreateFolder)))))
	s.mux.Handle("GET /api/v1/projects/{pid}/folders", cors(log(auth(http.HandlerFunc(s.handleListFolders)))))
	s.mux.Handle("PUT /api/v1/folders/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateFolder)))))
	s.mux.Handle("DELETE /api/v1/folders/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteFolder)))))

	s.mux.Handle("POST /api/v1/tests", cors(log(auth(http.HandlerFunc(s.handleCreateTest)))))
	s.mux.Handle("POST /api/v1/tests/estimate", cors(log(auth(http.HandlerFunc(s.handleEstimateTest)))))
	s.mux.Handle("GET /api/v1/tests", cors(log(auth(http.HandlerFunc(s.handleListTests)))))
	s.mux.Handle("GET /api/v1/tests/{id}", cors(log(auth(http.HandlerFunc(s.handleGetTest)))))
	s.mux.Handle("GET /api/v1/tests/{id}/sla-status", cors(log(auth(http.HandlerFunc(s.handleGetTestSLAStatus)))))
	s.mux.Handle("GET /api/v1/tests/{id}/report", cors(log(auth(http.HandlerFunc(s.handleGetTestHTMLReport)))))
	s.mux.Handle("POST /api/v1/tests/{id}/start", cors(log(auth(http.HandlerFunc(s.handleStartTest)))))
	s.mux.Handle("POST /api/v1/tests/{id}/comparison-baseline/pin", cors(log(auth(http.HandlerFunc(s.handlePinComparisonBaseline)))))
	s.mux.Handle("DELETE /api/v1/tests/{id}/comparison-baseline/pin", cors(log(auth(http.HandlerFunc(s.handleUnpinComparisonBaseline)))))
	s.mux.Handle("POST /api/v1/tests/{id}/stop", cors(log(auth(http.HandlerFunc(s.handleStopTest)))))
	s.mux.Handle("POST /api/v1/tests/{id}/abort", cors(log(auth(http.HandlerFunc(s.handleAbortTest)))))
	s.mux.Handle("POST /api/v1/tests/{id}/pause", cors(log(auth(http.HandlerFunc(s.handlePauseTest)))))
	s.mux.Handle("POST /api/v1/tests/{id}/resume", cors(log(auth(http.HandlerFunc(s.handleResumeTest)))))
	s.mux.Handle("PATCH /api/v1/tests/{id}/vusers", cors(log(auth(http.HandlerFunc(s.handleScaleVUsers)))))
	s.mux.Handle("PATCH /api/v1/tests/{id}/duration", cors(log(auth(http.HandlerFunc(s.handleAdjustDuration)))))
	s.mux.Handle("DELETE /api/v1/tests/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteTest)))))
	s.mux.Handle("PATCH /api/v1/tests/{id}/move", cors(log(auth(http.HandlerFunc(s.handleMoveTest)))))
	s.mux.Handle("GET /api/v1/tests/{id}/metrics", cors(log(auth(http.HandlerFunc(s.handleGetMetrics)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive", cors(log(auth(http.HandlerFunc(s.handleDownloadRunArchive)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/manifest", cors(log(auth(http.HandlerFunc(s.handleGetRunArchiveManifest)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/files/{name}", cors(log(auth(http.HandlerFunc(s.handleDownloadRunArchiveArtifact)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/browser-diagnostics", cors(log(auth(http.HandlerFunc(s.handleDownloadRunArchiveBrowserDiagnostics)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/transactions", cors(log(auth(http.HandlerFunc(s.handleGetRunArchiveTransactions)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/resources", cors(log(auth(http.HandlerFunc(s.handleGetRunArchiveResources)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/summaries", cors(log(auth(http.HandlerFunc(s.handleGetRunArchiveSummaries)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/report", cors(log(auth(http.HandlerFunc(s.handleGetRunArchiveReport)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/root-cause", cors(log(auth(http.HandlerFunc(s.handleGetRunArchiveRootCause)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/ai-root-cause", cors(log(auth(http.HandlerFunc(s.handleGetRunArchiveAIRootCause)))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/comparison", cors(log(auth(http.HandlerFunc(s.handleGetRunArchiveComparison)))))
	s.mux.Handle("POST /api/v1/archives/retention/plan", cors(log(auth(http.HandlerFunc(s.handlePlanArchiveRetention)))))
	s.mux.Handle("POST /api/v1/archives/retention/apply", cors(log(auth(http.HandlerFunc(s.handleApplyArchiveRetention)))))

	// Reusable scenario definitions.
	s.mux.Handle("POST /api/v1/scenarios", cors(log(auth(http.HandlerFunc(s.handleCreateScenario)))))
	s.mux.Handle("GET /api/v1/scenarios", cors(log(auth(http.HandlerFunc(s.handleListScenarios)))))
	s.mux.Handle("POST /api/v1/scenarios/validate-load-model", cors(log(auth(http.HandlerFunc(s.handleValidateScenarioLoadModel)))))
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
	s.mux.Handle("GET /api/v1/scenarios/{id}/results", cors(log(auth(http.HandlerFunc(s.handleScenarioResults)))))
	s.mux.Handle("GET /api/v1/scenarios/{id}/runtime-settings/report", cors(log(auth(http.HandlerFunc(s.handleGetScenarioRuntimeSettingsReport)))))
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

	// Script CRUD, validation, import, and export endpoints.
	s.mux.Handle("POST /api/v1/scripts", cors(log(auth(http.HandlerFunc(s.handleCreateScript)))))
	s.mux.Handle("GET /api/v1/scripts", cors(log(auth(http.HandlerFunc(s.handleListScripts)))))
	s.mux.Handle("GET /api/v1/scripts/{id}", cors(log(auth(http.HandlerFunc(s.handleGetScript)))))
	s.mux.Handle("PUT /api/v1/scripts/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateScript)))))
	s.mux.Handle("DELETE /api/v1/scripts/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteScript)))))
	s.mux.Handle("PATCH /api/v1/scripts/{id}/move", cors(log(auth(http.HandlerFunc(s.handleMoveScript)))))
	s.mux.Handle("POST /api/v1/scripts/validate", cors(log(auth(http.HandlerFunc(s.handleValidateDraftScript)))))
	s.mux.Handle("POST /api/v1/scripts/replay", cors(log(auth(http.HandlerFunc(s.handleReplayDraftScript)))))
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
	s.mux.Handle("POST /api/v1/scripts/{id}/validate", cors(log(auth(http.HandlerFunc(s.handleValidateScript)))))
	s.mux.Handle("POST /api/v1/scripts/{id}/replay", cors(log(auth(http.HandlerFunc(s.handleReplayScript)))))
	s.mux.Handle("POST /api/v1/scripts/{id}/browser-replay/sessions", cors(log(auth(http.HandlerFunc(s.handleStartBrowserReplayScript)))))
	s.mux.Handle("POST /api/v1/scripts/{id}/export", cors(log(auth(http.HandlerFunc(s.handleExportScript)))))
	s.mux.Handle("GET /api/v1/scripts/{id}/download", cors(log(auth(http.HandlerFunc(s.handleDownloadScript)))))
	s.mux.Handle("GET /api/v1/scripts/{id}/runtime-settings/report", cors(log(auth(http.HandlerFunc(s.handleGetScriptRuntimeSettingsReport)))))
	s.mux.Handle("POST /api/v1/scripts/import/loadrunner", cors(log(auth(http.HandlerFunc(s.handleImportLoadRunner)))))
	s.mux.Handle("POST /api/v1/scripts/import/loadrunner/validate", cors(log(auth(http.HandlerFunc(s.handleValidateLoadRunnerImport)))))
	s.mux.Handle("GET /api/v1/scripts/import/loadrunner/support-matrix", cors(log(auth(http.HandlerFunc(s.handleLoadRunnerSupportMatrix)))))
	s.mux.Handle("POST /api/v1/scripts/import/jmeter", cors(log(auth(http.HandlerFunc(s.handleImportJMeter)))))
	s.mux.Handle("POST /api/v1/scripts/import/jmeter/save", cors(log(auth(http.HandlerFunc(s.handleSaveJMeterImport)))))
	s.mux.Handle("POST /api/v1/scripts/import/jmeter/validate", cors(log(auth(http.HandlerFunc(s.handleValidateJMeterImport)))))
	s.mux.Handle("GET /api/v1/scripts/import/jmeter/support-matrix", cors(log(auth(http.HandlerFunc(s.handleJMeterSupportMatrix)))))
	s.mux.Handle("GET /api/v1/scripts/web-api/support-matrix", cors(log(auth(http.HandlerFunc(s.handleWebAPIExecutionSupportMatrix)))))
	s.mux.Handle("POST /api/v1/scripts/import/sample", cors(log(auth(http.HandlerFunc(s.handleImportSample)))))
	s.mux.Handle("POST /api/v1/scripts/import/devweb", cors(log(auth(http.HandlerFunc(s.handleImportDevWeb)))))
	s.mux.Handle("GET /api/v1/scripts/import/devweb/support-matrix", cors(log(auth(http.HandlerFunc(s.handleDevWebSupportMatrix)))))
	s.mux.Handle("GET /api/v1/scripts/import/oracle-ebusiness/support-matrix", cors(log(auth(http.HandlerFunc(s.handleOracleEBusinessSupportMatrix)))))
	s.mux.Handle("POST /api/v1/scripts/import/oracle-ebusiness/review", cors(log(auth(http.HandlerFunc(s.handleReviewOracleEBusinessImport)))))
	s.mux.Handle("POST /api/v1/scripts/import/har", cors(log(auth(http.HandlerFunc(s.handleImportHAR)))))
	s.mux.Handle("POST /api/v1/scripts/import/openapi", cors(log(auth(http.HandlerFunc(s.handleImportOpenAPI)))))
	s.mux.Handle("POST /api/v1/scripts/import/wsdl", cors(log(auth(http.HandlerFunc(s.handleImportWSDL)))))

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

	// System endpoints for health and global settings.
	s.mux.Handle("GET /api/v1/auth/login", cors(log(http.HandlerFunc(s.handleOIDCLogin))))
	s.mux.Handle("GET /api/v1/auth/callback", cors(log(http.HandlerFunc(s.handleOIDCCallback))))
	s.mux.Handle("POST /api/v1/auth/logout", cors(log(http.HandlerFunc(s.handleOIDCLogout))))
	s.mux.Handle("GET /api/v1/auth/me", cors(log(http.HandlerFunc(s.handleGetAuthStatus))))
	s.mux.Handle("GET /api/v1/auth/diagnostics", cors(log(http.HandlerFunc(s.handleGetAuthDiagnostics))))
	s.mux.Handle("GET /api/v1/auth/roles", cors(log(http.HandlerFunc(s.handleGetAuthRoleCatalog))))
	s.mux.Handle("GET /api/v1/auth/access-matrix", cors(log(http.HandlerFunc(s.handleGetAuthAccessMatrix))))
	s.mux.Handle("GET /api/v1/auth/effective-access", cors(log(http.HandlerFunc(s.handleGetAuthEffectiveAccess))))
	s.mux.Handle("GET /api/v1/auth/service-accounts", cors(log(auth(http.HandlerFunc(s.handleListServiceAccounts)))))
	s.mux.Handle("POST /api/v1/auth/service-accounts", cors(log(auth(http.HandlerFunc(s.handleCreateServiceAccount)))))
	s.mux.Handle("PATCH /api/v1/auth/service-accounts/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateServiceAccount)))))
	s.mux.Handle("POST /api/v1/auth/service-accounts/{id}/rotate", cors(log(auth(http.HandlerFunc(s.handleRotateServiceAccount)))))
	s.mux.Handle("DELETE /api/v1/auth/service-accounts/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteServiceAccount)))))
	s.mux.Handle("GET /api/v1/auth/audit-events", cors(log(auth(http.HandlerFunc(s.handleListAuditEvents)))))
	s.mux.Handle("GET /api/v1/auth/audit-events/export", cors(log(auth(http.HandlerFunc(s.handleExportAuditEvents)))))
	s.mux.Handle("GET /api/v1/system/health", cors(log(http.HandlerFunc(s.handleHealthCheck))))
	s.mux.Handle("GET /api/v1/system/readiness", cors(log(http.HandlerFunc(s.handleSystemReadiness))))
	s.mux.Handle("GET /metrics", log(http.HandlerFunc(s.handlePrometheusMetrics)))
	s.mux.Handle("GET /api/v1/system/inventory", cors(log(auth(http.HandlerFunc(s.handleGetSystemInventory)))))
	s.mux.Handle("GET /api/v1/system/usage", cors(log(auth(http.HandlerFunc(s.handleGetUsageAccounting)))))
	s.mux.Handle("GET /api/v1/system/usage/export", cors(log(auth(http.HandlerFunc(s.handleExportUsageAccounting)))))
	s.mux.Handle("GET /api/v1/system/usage/snapshots", cors(log(auth(http.HandlerFunc(s.handleListUsageAccountingSnapshots)))))
	s.mux.Handle("POST /api/v1/system/usage/snapshots", cors(log(auth(http.HandlerFunc(s.handleCreateUsageAccountingSnapshot)))))
	s.mux.Handle("GET /api/v1/system/parity-status", cors(log(auth(http.HandlerFunc(s.handleGetParityStatus)))))
	s.mux.Handle("GET /api/v1/system/browser-benchmarks", cors(log(auth(http.HandlerFunc(s.handleGetBrowserBenchmarkEvidence)))))
	s.mux.Handle("GET /api/v1/runtime-settings/support-matrix", cors(log(auth(http.HandlerFunc(s.handleRuntimeSettingsSupportMatrix)))))
	s.mux.Handle("GET /api/v1/protocol-bundles/support-matrix", cors(log(auth(http.HandlerFunc(s.handleProtocolBundleSupportMatrix)))))
	s.mux.Handle("GET /api/v1/integrations/apm/profiles", cors(log(auth(http.HandlerFunc(s.handleListAPMIntegrationProfiles)))))
	s.mux.Handle("GET /api/v1/monitoring/packs", cors(log(auth(http.HandlerFunc(s.handleListInfrastructureMonitorPacks)))))
	s.mux.Handle("GET /api/v1/reports/profiles", cors(log(auth(http.HandlerFunc(s.handleListReportProfiles)))))
	s.mux.Handle("POST /api/v1/results/import", cors(log(auth(http.HandlerFunc(s.handleImportExternalResults)))))
	s.mux.Handle("GET /api/v1/results/imports", cors(log(auth(http.HandlerFunc(s.handleListExternalResultImports)))))
	s.mux.Handle("GET /api/v1/fleet/alerts", cors(log(auth(http.HandlerFunc(s.handleGetFleetAlerts)))))
	s.mux.Handle("GET /api/v1/fleet/capacity", cors(log(auth(http.HandlerFunc(s.handleGetFleetCapacity)))))
	s.mux.Handle("PATCH /api/v1/system/generators/drain", cors(log(auth(http.HandlerFunc(s.handleSetGeneratorDrainState)))))
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
	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		slog.Warn("embedded static files not available, dashboard will 404")
		s.mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<!DOCTYPE html><html><body><h1>Stormrunner</h1><p>Dashboard not built. Run: cd dashboard && npm run build</p></body></html>"))
		}))
	} else {
		fileServer := http.FileServer(http.FS(staticSub))
		s.mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func (s *Server) Start() error {
	// Start websocket broadcast loop before serving HTTP requests.
	go s.hub.Run()

	if err := s.recorder.StartProxy(); err != nil {
		return fmt.Errorf("starting recording proxy: %w", err)
	}
	s.startUsageAccountingSnapshotCadence()

	slog.Info("stormrunner API server starting", "addr", s.httpServer.Addr)
	if err := s.httpServer.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	// Stop background components before closing active HTTP listeners.
	if s.usageCancel != nil {
		s.usageCancel()
		s.usageCancel = nil
	}
	s.hub.Stop()
	s.recorder.StopProxy()
	return s.httpServer.Shutdown(ctx)
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
