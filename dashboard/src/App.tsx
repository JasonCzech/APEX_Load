import TooltipButton from './components/ux/TooltipButton'
import { takeMonitoringTabFocus } from './components/monitoringWorkspaceModel';
import { contextualHelpHash } from './contextualHelp';
import { scriptsRouteForAuthoringState, terminalRunReportView } from './appRouting';
import PageHeader from './components/ux/PageHeader'
import { settingsOwner } from "./components/settingsNavigation";
import {
  Suspense,
  lazy,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { usePersonalAppearance } from "./hooks/usePersonalPreferences";
import "./components/PersonalSettings.css";
import { useDashboardTheme } from "./hooks/useDashboardTheme";
import { requestDashboardZoom, useDashboardZoomShortcuts } from "./hooks/useDashboardZoomShortcuts";
import { useDesktopWindowTitle } from "./desktop/useDesktopWindowTitle";
import { useAsyncTask } from "./hooks/useAsyncTask";
import { useKeyboardShortcutHelp, useKeyboardShortcuts } from "./hooks/KeyboardShortcutProvider";
import { KeyboardShortcutProvider } from "./hooks/KeyboardShortcutRoot";
import { useWebSocket } from "./hooks/useWebSocket";
import { ApplicationProvider } from "./hooks/ApplicationProvider";
import { useApplication } from "./hooks/useApplication";
import {
  ApplicationContext,
  type ApplicationContextValue,
} from "./hooks/applicationContext";
import {
  MetricsHistoryPoint,
  TelemetryDiagnostic,
  TelemetryDiagnosticEntry,
  WSMessage,
} from "./types";
import Sidebar, { type NavPage } from "./components/Sidebar";
import TablePaginationManager from "./components/TablePaginationManager";
import {
  type AppView,
  handleGuardedRouteChange,
  mergeAppViewWithWorkspace,
  navPageForAppView,
  normalizeReportViewForFeatureFlag,
  parseAppViewFromHash,
  parseDesktopDeepLink,
  projectAssetForAppView,
  projectIdForAppView,
  readAppHistoryIndex,
  recoveryViewForUnscopedProjectAsset,
  resetProjectScopedAssetView,
  scopeAppViewToProject,
  scopeAppViewToWorkspace,
  testIdForAppView,
  type TestWorkspaceTabID,
  unscopedProjectAssetForAppView,
  viewToLocationHash,
  withAppHistoryIndex,
  workspaceScopeForAppView,
} from "./appRouting";
import { requestProjectPicker } from "./workspacePickerEvents";
import type { CommandCenterRouteView } from "./appRouting";
import { appendBoundedMetricsHistory } from "./appMetricsHistory";
import { mapExecutionMetricToHistoryPoint } from "./components/testExecutionMetricsModel";
import AuthGate from "./components/AuthGate";
import { useAuthStatus } from "./hooks/useAuthStatus";
import { ProjectProvider } from "./hooks/ProjectProvider";
import { useProject } from "./hooks/useProject";
import {
  ProjectContext,
  type ProjectContextValue,
} from "./hooks/projectContext";
import { DevViewProvider } from "./hooks/DevViewProvider";
import { useDevView } from "./hooks/useDevView";
import { useSystemApi } from "./hooks/api/useSystemApi";
import { useRepositoryApi } from "./hooks/api/useRepositoryApi";
import { useLibraryApi } from "./hooks/api/useLibraryApi";
import { useScriptApi } from "./hooks/api/useScriptApi";
import { useTestApi } from "./hooks/api/useTestApi";
import { useSharedTestList } from "./hooks/useSharedTestList";
import {
  useActiveTopbarContribution,
  type TopbarAction,
  type TopbarMetaChip,
} from "./hooks/TopbarContributionProvider";
import { TopbarContributionProvider } from "./hooks/TopbarContributionRoot";
import { useNavigationGuardRunner } from "./hooks/NavigationGuardProvider";
import { NavigationGuardProvider } from "./hooks/NavigationGuardRoot";
import { isEffectiveOperationAllowed } from "./utils/authAccessUtils";
import { isRunReportEnabled } from "./utils/featureFlags";
import {
  readStoredString,
  removeStoredItem,
  writeStoredString,
} from "./utils/browserStorage";
import { DesktopStatusResponse } from "./types/library";
import {
  getDesktopBridge,
  isDesktopSurface,
  isMacDesktop,
} from "./desktop/desktopBridge";
import { useDesktopNotifications } from "./hooks/useDesktopNotifications";
import {
  announceDesktopOpenActivation,
  useDesktopShellEvents,
  type DesktopShellEventHandlers,
} from "./desktop/useDesktopShellEvents";
import { DesktopShellHandlerCoordinator } from "./desktop/desktopShellHandlerCoordinator";
import { useKeepAwakeDuringRuns } from "./desktop/useKeepAwake";
import { useDockBadge } from "./desktop/useDockBadge";
import {
  filesFromDroppedPaths,
  nativeFileReadErrorMessage,
} from "./desktop/desktopFiles";
import { useDesktopRoutePersistence } from "./desktop/useDesktopRoutePersistence";
import AboutDialog from "./components/AboutDialog";
import LazyLoadBoundary from "./components/LazyLoadBoundary";
import DesktopHome, { type DesktopQuickAction } from "./components/DesktopHome";
import {
  addRecent,
  clearRecents,
  getRecents,
  toggleRecentPin,
  type RecentItem,
} from "./desktop/desktopRecents";
import DeploymentBanner from "./components/DeploymentBanner";
import ClientDownloadsMenu from "./components/ClientDownloadsMenu";
import ThemeToggle from "./components/ThemeToggle";
import {
  pageTitleForScriptSubView,
  pageTitleForView,
  selectTopbarActiveRuns,
  shouldShowTopbarClientDownloads,
  topbarRunStatusLabel,
  topbarRunTone,
  updateTopbarActiveRuns,
  type TopbarRunSummary,
} from "./appTopbar";
import {
  buildNavigationShortcuts,
  focusCurrentPageSearch,
} from "./appKeyboardShortcuts";
import { openDesktopBrowserWithPreview } from "./hooks/apiClient";
import {
  appendProjectTelemetryDiagnostic,
  dashboardSnapshotAudience,
  isCurrentApplicationCreateScope,
  isCurrentCatalogRequest,
  isCurrentRepositoryRequest,
  isProjectScopedMessageCurrent,
  projectForActiveDomain,
  resolveApplicationCreateTarget,
  telemetryDiagnosticsForProject,
} from "./appProjectScope";
import {
  createRepositoryCatalogState,
  createRepositoryCategoryMutationLedger,
  mergeRepositoryCatalogRefresh,
  reconcileRepositoryCategorySnapshot,
  recordCommittedRepositoryCategory,
  repositoryCatalogForDomain,
  type RepositoryCatalogRefreshPatch,
} from "./repositoryCatalogState";
import {
  acknowledgeProjectThresholdAlert,
  appendProjectThresholdAlert,
  isThresholdAlertTerminalStatus,
  resolveProjectThresholdAlertsBefore,
  resolveProjectThresholdAlertsForTest,
  thresholdAlertFromMessage,
  thresholdAlertsForProject,
  type ThresholdAlertEntry,
} from "./thresholdAlerts";
import type { ScriptsAuthoringSubView } from "./components/useScriptsAuthoringState";
import type { MonitorCategoryId } from "./components/MonitoringPage";
import { commitGuardedNavigation } from "./guardedNavigation";
import {
  resolveNativeImportRequest,
  type NativeImportRequest,
} from "./nativeImportRouting";
import "./App.css";
import "./components/Pages.css";

const ScriptsPage = lazy(() => import("./components/ScriptsPage"));
const DashboardPage = lazy(() => import("./components/DashboardPage"));
const CommandCenterPrototype = lazy(
  () => import("./components/CommandCenterPrototype"),
);
const TestHub = lazy(() => import("./components/TestHub"));
const TestExecution = lazy(() => import("./components/TestExecution"));
const RunReportPage = lazy(() => import("./components/RunReportPage"));
import ActivityWarning from "./components/ActivityWarning";
import DesktopStatusMenu from "./components/DesktopStatusMenu";
import LoadingState from "./components/ux/LoadingState";
const ActivityPage = lazy(() => import("./components/ActivityPage"));
const ResultsPage = lazy(() => import("./components/ResultsPage"));
const NewTestPage = lazy(() => import("./components/NewTestPage"));
const MonitoringPage = lazy(() => import("./components/MonitoringPage"));
const ApiGuidePage = lazy(() => import("./components/ApiGuidePage"));
const SwaggerDocsPage = lazy(() => import("./components/SwaggerDocsPage"));
const ApplicationRepositoryPage = lazy(
  () => import("./components/ApplicationRepositoryPage"),
);
const AccessPage = lazy(() => import("./components/AccessPage"));
const UserSettingsPage = lazy(() => import("./components/UserSettingsPage"));
const SharedTestDataReadOnly = lazy(() => import("./components/SharedTestDataReadOnly"));
const ProjectLiveTablesPage = lazy(() => import("./components/ProjectLiveTablesPage"));
const SettingsPage = lazy(() => import("./components/SettingsPage"));
const BrowserRecorderControlPanel = lazy(
  () => import("./components/BrowserRecorderControlPanel"),
);

const getWailsDesktopBridge = getDesktopBridge;
const PROTECTED_METRICS_STATUSES = new Set([
  "SHAKEOUT",
  "SHAKEOUT_REVIEW",
  "INITIALIZING",
  "RUNNING",
  "PAUSED",
  "STOPPING",
]);

type PendingGuardedHistoryTraversal = {
  originHash: string;
  originIndex: number;
  targetHash: string;
  targetIndex: number;
  targetView: AppView;
};

function topbarActionClass(action: TopbarAction): string {
  const tone = action.tone || "secondary";
  const btnClass =
    tone === "primary"
      ? "btn-primary"
      : tone === "success"
        ? "btn-success"
        : tone === "danger"
          ? "btn-danger"
          : tone === "ghost"
            ? "btn-ghost"
            : "btn-secondary";
  return [
    "btn",
    "btn-sm",
    "topbar-action-button",
    btnClass,
    action.className,
    action.compactLabel ? "has-compact-label" : "",
    action.pressed ? "topbar-action-pressed" : "",
  ]
    .filter(Boolean)
    .join(" ");
}

function renderTopbarAction(action: TopbarAction) {
  if (action.hidden) return null;
  return (
    <TooltipButton
      key={action.id}
      type="button"
      className={topbarActionClass(action)}
      aria-label={action.ariaLabel}
      aria-pressed={action.pressed}
      title={action.title}
      disabled={action.disabled}
      onClick={action.onClick}
    >
      {action.icon && (
        <span className="topbar-action-icon" aria-hidden="true">
          {action.icon}
        </span>
      )}
      <span className="topbar-action-label">{action.label}</span>
      {action.compactLabel && (
        <span className="topbar-action-compact-label">
          {action.compactLabel}
        </span>
      )}
    </TooltipButton>
  );
}

function renderTopbarMetaChip(chip: TopbarMetaChip) {
  if (chip.hidden) return null;
  if (chip.onClick) {
    return (
      <TooltipButton
        key={chip.id}
        type="button"
        className={`topbar-meta-chip issue-pill-button ${chip.tone || "default"}`}
        title={chip.title}
        aria-label={chip.ariaLabel}
        onClick={chip.onClick}
      >
        {chip.label}
      </TooltipButton>
    );
  }
  return (
    <span
      key={chip.id}
      className={`topbar-meta-chip ${chip.tone || "default"}`}
      title={chip.title}
    >
      {chip.label}
    </span>
  );
}

function viewConsumesMetrics(view: AppView, testId: string) {
  if (view.page === "dashboard") return true;
  if (view.page === "test") return view.testId === testId;
  if (view.page === "command-center") return view.testId === testId;
  return false;
}

export function ScopedAppContextProviders({
  projectValue,
  applicationValue,
  children,
}: {
  projectValue: ProjectContextValue;
  applicationValue: ApplicationContextValue;
  children: ReactNode;
}) {
  return (
    <ProjectContext.Provider value={projectValue}>
      <ApplicationContext.Provider value={applicationValue}>
        {children}
      </ApplicationContext.Provider>
    </ProjectContext.Provider>
  );
}

function monitorCategoryForView(view: AppView): MonitorCategoryId {
  if (view.page === "fleet") return "fleet";
  if (view.page === "schedules") return "schedules";
  if (view.page === "monitoring" && view.category) return view.category;
  return "overview";
}

function viewForMonitorCategory(categoryId: MonitorCategoryId): AppView {
  if (categoryId === "fleet") return { page: "fleet" };
  if (categoryId === "schedules") return { page: "schedules" };
  if (categoryId === "telemetry" || categoryId === "alerts")
    return { page: "monitoring", category: categoryId };
  return { page: "monitoring" };
}

function AppShell({
  authStatus,
  desktopHandlerCoordinator,
  onDevPreviewLogout,
}: {
  authStatus?: import("./types").DashboardAuthStatus;
  desktopHandlerCoordinator: DesktopShellHandlerCoordinator;
  onDevPreviewLogout?: () => void;
}) {
  const [view, setView] = useState<AppView>(() =>
    typeof window === "undefined"
      ? { page: "dashboard" }
      : normalizeReportViewForFeatureFlag(
          parseAppViewFromHash(window.location.hash),
          isRunReportEnabled(),
        ),
  );
  // Keep the cockpit mounted during a same-run details visit so staged load,
  // filters, and graph choices survive the return. Release it on other routes.
  const [detailsOrigin, setDetailsOrigin] = useState<{ testId: string; projectId?: string; routeKey: string } | null>(null);
  const retainedCommandCenter = view.page === "test" && detailsOrigin?.testId === view.testId
    && detailsOrigin.projectId === view.projectId;
  const commandCenterTestId = view.page === "command-center" ? view.testId : retainedCommandCenter ? detailsOrigin.testId : undefined;
  const returningCommandCenter = view.page === "command-center" && detailsOrigin
    && (!view.testId || view.testId === detailsOrigin.testId) && view.projectId === detailsOrigin.projectId;
  useEffect(() => {
    if (!returningCommandCenter && !retainedCommandCenter) setDetailsOrigin(null);
  }, [returningCommandCenter, retainedCommandCenter]);
  const viewRef = useRef(view);
  const routeSyncInitialized = useRef(false);
  const appHistoryIndex = useRef(
    typeof window === "undefined"
      ? 0
      : (readAppHistoryIndex(window.history.state) ?? 0),
  );
  const popstateHashToIgnore = useRef<string | null>(null);
  const replaceRouteOnNextViewSync = useRef(false);
  const guardedHistoryTraversal = useRef<{
    phase: "restoring" | "proceeding";
    pending: PendingGuardedHistoryTraversal;
  } | null>(null);
  const mainContentRef = useRef<HTMLElement | null>(null);
  const commandRunViewMemory = useRef(new Map<string, Map<string, unknown>>());
  const [settingsFocusRequest, setSettingsFocusRequest] = useState<{ destination: string; control: string } | null>(null);
  const clearSettingsFocusRequest = useCallback(() => setSettingsFocusRequest(null), []);
  const metricsHistoryRef = useRef<Record<string, MetricsHistoryPoint[]>>({});
  const [, setMetricsHistoryVersion] = useState(0);
  const [testStatuses, setTestStatuses] = useState<Record<string, string>>({});
  const testStatusesRef = useRef(testStatuses);
  testStatusesRef.current = testStatuses;
  const protectedMetricsTestIDsRef = useRef<ReadonlySet<string>>(new Set());
  const [telemetryDiagnosticsByProject, setTelemetryDiagnosticsByProject] =
    useState<Record<string, TelemetryDiagnosticEntry[]>>({});
  const [thresholdAlertsByProject, setThresholdAlertsByProject] = useState<
    Record<string, ThresholdAlertEntry[]>
  >({});
  const thresholdAlertSampleTimestampByTestRef = useRef<Record<string, string>>(
    {},
  );
  const { theme, setTheme } = useDashboardTheme();
  usePersonalAppearance();
  const { mode: devViewMode } = useDevView();
  const { getAuthEffectiveAccess } = useSystemApi();
  const libraryApi = useLibraryApi();
  const repositoryApi = useRepositoryApi();
  const isDevUserPreview = authStatus?.mode === "dev" && devViewMode === "user";
  const scriptMode =
    view.page === "scripts" && "mode" in view ? view.mode : undefined;
  // An import route names its importer (the route boundary translates older
  // addresses); the page opens it by launcher descriptor.
  const scriptImport =
    view.page === "scripts" && view.mode === "import" && view.importLauncher
      ? { launcher: view.importLauncher, mode: view.importMode || null }
      : null;
  const scriptEditId =
    view.page === "scripts" && "scriptId" in view ? view.scriptId : undefined;
  const [scriptsNavigationVersion, setScriptsNavigationVersion] = useState(0);
  const [scenarioScriptWorkflow, setScenarioScriptWorkflow] = useState(false);
  const [scriptsTopbarState, setScriptsTopbarState] = useState<{
    subView: ScriptsAuthoringSubView;
    editingId: string | null;
    importLauncher?: string | null;
    importMode?: string | null;
  } | null>(null);
  const [effectiveAccessResources, setEffectiveAccessResources] = useState<
    import("./types").DashboardAuthEffectiveAccessResource[]
  >([]);
  const [effectiveAccessLoaded, setEffectiveAccessLoaded] = useState(false);
  const [accessPermissionsInitialized, setAccessPermissionsInitialized] = useState(false);
  const [effectiveAccessError, setEffectiveAccessError] = useState(false);
  const [domains, setDomains] = useState<import("./types").Domain[]>([]);
  const [domainsLoaded, setDomainsLoaded] = useState(false);
  const [domainsError, setDomainsError] = useState("");
  const domainsRef = useRef(domains);
  domainsRef.current = domains;
  const domainsRequestGenerationRef = useRef(0);
  const [activeDomain, setActiveDomainState] = useState<
    import("./types").Domain | null
  >(null);
  const [repositoryCatalogState, setRepositoryCatalogState] = useState(
    createRepositoryCatalogState,
  );
  const repositoryCategoryMutationLedgerRef = useRef(
    createRepositoryCategoryMutationLedger(),
  );
  const repositoryRequestGenerationRef = useRef(0);
  const activeDomainIDRef = useRef(activeDomain?.id || "");
  activeDomainIDRef.current = activeDomain?.id || "";
  const setActiveDomain = useCallback((domain: import("./types").Domain) => {
    // Mutation responses are authoritative even when a follow-up catalog read
    // fails or observes an older snapshot. Keep that row available locally.
    setDomains((current) => {
      const next = current.some((candidate) => candidate.id === domain.id)
        ? current.map((candidate) =>
            candidate.id === domain.id ? domain : candidate,
          )
        : [...current, domain];
      return next.sort((left, right) => left.name.localeCompare(right.name));
    });
    setActiveDomainState(domain);
    writeStoredString("apexload.activeDomainId", domain.id);
  }, []);
  const [desktopStatus, setDesktopStatus] =
    useState<DesktopStatusResponse | null>(null);
  const [activeTopbarRuns, setActiveTopbarRuns] = useState<TopbarRunSummary[]>(
    [],
  );
  const [nativeImportRequest, setNativeImportRequest] =
    useState<NativeImportRequest | null>(null);
  const nativeImportRequestGenerationRef = useRef(0);
  const [fleetEventSignal, setFleetEventSignal] = useState(0);
  // Bumped by the content-free `access_review_changed` WebSocket ping so the Access page
  // refetches its (admin-gated) review when the posture changes server-side.
  const [accessReviewSignal, setAccessReviewSignal] = useState(0);
  const [featurePolicySignal, setFeaturePolicySignal] = useState(0);
  const testApi = useTestApi();
  const scriptApi = useScriptApi();
  const runReportEnabled = useMemo(() => isRunReportEnabled(), []);
  const activeTopbarContribution = useActiveTopbarContribution();
  const { hasDirtyGuard, runGuarded } = useNavigationGuardRunner();
  const { notifyTestStatus } = useDesktopNotifications();
  const desktopSurface = isDesktopSurface();
  const [recents, setRecents] = useState<RecentItem[]>([]);
  const [desktopStartupError, setDesktopStartupError] = useState("");
  const [desktopImportError, setDesktopImportError] = useState("");
  const {
    run: runDesktopBrowserLaunch,
    error: desktopBrowserLaunchError,
  } = useAsyncTask<void>();
  const handleOpenDesktopBrowser = useCallback(async () => {
    // useAsyncTask records the rejection for the topbar alert and fences stale
    // overlapping launches. The terminal catch prevents an unhandled promise
    // while preserving that visible error state.
    try {
      await runDesktopBrowserLaunch(() =>
        openDesktopBrowserWithPreview(getDesktopBridge()),
      );
    } catch {
      // useAsyncTask already exposed the actionable error in the topbar.
    }
  }, [runDesktopBrowserLaunch]);
  const reloadRecents = useCallback(() => {
    void getRecents().then(setRecents);
  }, []);
  useEffect(() => {
    if (!desktopSurface) {
      return;
    }
    reloadRecents();
    const bridge = getDesktopBridge();
    if (bridge?.GetStartupError) {
      void bridge
        .GetStartupError()
        .then(setDesktopStartupError)
        .catch(() => undefined);
    }
  }, [desktopSurface, reloadRecents]);

  const handleWSMessage = useCallback(
    (msg: WSMessage, projectScopeID?: string) => {
      if (!isProjectScopedMessageCurrent(msg.project_id, projectScopeID))
        return;

      if (msg.type === "access_review_changed") {
        setAccessReviewSignal((n) => n + 1);
        return;
      }

      if (msg.type === "domain_feature_policy_changed") {
        setFeaturePolicySignal((n) => n + 1);
        return;
      }

      if (msg.type === "fleet_event") {
        setFleetEventSignal((n) => n + 1);
        return;
      }

      if (msg.type === "threshold_alert" && projectScopeID) {
        const entry = thresholdAlertFromMessage(msg);
        if (entry) {
          setThresholdAlertsByProject((prev) =>
            appendProjectThresholdAlert(prev, projectScopeID, entry),
          );
        }
        return;
      }

      if (
        msg.type === "metrics_update" &&
        msg.data &&
        msg.test_id &&
        msg.timestamp
      ) {
        const testId = msg.test_id;
        const previousSampleTimestamp =
          thresholdAlertSampleTimestampByTestRef.current[testId];
        thresholdAlertSampleTimestampByTestRef.current[testId] = msg.timestamp;
        if (projectScopeID && previousSampleTimestamp) {
          // Threshold alerts follow metrics_update on the same websocket. Use
          // the prior sample as the cutoff so a still-active alert has one
          // message cycle to refresh without flickering, while a recovered
          // breach resolves after one omitted sample.
          setThresholdAlertsByProject((prev) =>
            resolveProjectThresholdAlertsBefore(
              prev,
              projectScopeID,
              testId,
              previousSampleTimestamp,
            ),
          );
        }
        const point = mapExecutionMetricToHistoryPoint({
          ...msg.data,
          timestamp: String(msg.timestamp),
        });
        const protectedTestIDs = new Set(protectedMetricsTestIDsRef.current);
        protectedTestIDs.add(testId);
        metricsHistoryRef.current = appendBoundedMetricsHistory(
          metricsHistoryRef.current,
          testId,
          point,
          undefined,
          undefined,
          protectedTestIDs,
        );
        if (viewConsumesMetrics(viewRef.current, testId)) {
          setMetricsHistoryVersion((version) => version + 1);
        }
      }

      if (
        msg.type === "telemetry_diagnostic" &&
        msg.data &&
        msg.test_id &&
        msg.timestamp &&
        projectScopeID
      ) {
        if (msg.project_id && msg.project_id !== projectScopeID) return;
        const diagnostic = msg.data as unknown as TelemetryDiagnostic;
        if (diagnostic.summary && diagnostic.severity) {
          const testId = msg.test_id;
          const timestamp = msg.timestamp;
          setTelemetryDiagnosticsByProject((prev) =>
            appendProjectTelemetryDiagnostic(prev, projectScopeID, {
              test_id: testId,
              test_name: msg.test_name,
              timestamp,
              diagnostic,
            }),
          );
        }
      }

      if (msg.status && msg.test_id) {
        const testId = msg.test_id;
        if (
          projectScopeID &&
          isThresholdAlertTerminalStatus(msg.status)
        ) {
          delete thresholdAlertSampleTimestampByTestRef.current[testId];
          setThresholdAlertsByProject((prev) =>
            resolveProjectThresholdAlertsForTest(
              prev,
              projectScopeID,
              testId,
            ),
          );
        }
        setTestStatuses((prev) => ({ ...prev, [testId]: msg.status! }));
        setActiveTopbarRuns((prev) =>
          updateTopbarActiveRuns(prev, {
            id: testId,
            name: msg.test_name,
            status: msg.status,
            timestamp: msg.timestamp,
          }),
        );
        notifyTestStatus(msg.type, msg.test_name, testId);
      }
    },
    [notifyTestStatus],
  );
  const metricsHistory = metricsHistoryRef.current;

  const outerProjectContext = useProject();
  const {
    projects,
    projectsLoading,
    projectsError,
    activeProject: selectedProject,
    setActiveProject: setOuterActiveProject,
		removeProject: removeOuterProject,
    createAndSelectProject: createOuterProject,
		updateProject: updateOuterProject,
		deleteProject: deleteOuterProject,
    refreshProjects,
  } = outerProjectContext;
  const applicationCreateScopeRef = useRef({
    projectId: selectedProject?.id,
    generation: 0,
  });
  if (applicationCreateScopeRef.current.projectId !== selectedProject?.id) {
    applicationCreateScopeRef.current = {
      projectId: selectedProject?.id,
      generation: applicationCreateScopeRef.current.generation + 1,
    };
  }
  const domainProjects = useMemo(() => {
    const domainID = activeDomain?.id || "default";
    return projects.filter(
      (project) => (project.domain_id || "default") === domainID,
    );
  }, [activeDomain?.id, projects]);
  const activeProject = projectForActiveDomain(activeDomain, selectedProject);
  const setActiveProject = useCallback<ProjectContextValue["setActiveProject"]>(
    (project) => {
      if (projectForActiveDomain(activeDomain, project)) {
        const projectChanged = selectedProject?.id !== project.id;
        setOuterActiveProject(project);
        if (projectChanged) {
          setView((current) =>
            scopeAppViewToWorkspace(
              resetProjectScopedAssetView(current, project.id),
              { projectId: project.id },
            ),
          );
        }
      }
    },
    [activeDomain, selectedProject?.id, setOuterActiveProject],
  );
  const createAndSelectProject = useCallback<
    ProjectContextValue["createAndSelectProject"]
  >(
    async (project) => {
      const domainID = activeDomain?.id;
      const payload =
        typeof project === "string"
          ? { name: project, domain_id: domainID }
          : { ...project, domain_id: project.domain_id || domainID };
      const created = await createOuterProject(payload);
      setView((current) =>
        scopeAppViewToWorkspace(
          resetProjectScopedAssetView(current, created.id),
          { projectId: created.id },
        ),
      );
      return created;
    },
    [activeDomain?.id, createOuterProject],
  );
	const removeProject = useCallback<ProjectContextValue["removeProject"]>(
		(projectID) => {
			if (domainProjects.some((project) => project.id === projectID)) {
				removeOuterProject(projectID);
				if (selectedProject?.id === projectID) {
					setView((current) =>
						scopeAppViewToWorkspace(resetProjectScopedAssetView(current), {}),
					);
				}
			}
		},
		[domainProjects, removeOuterProject, selectedProject?.id],
	);
	const updateProject = useCallback<ProjectContextValue["updateProject"]>(
		(projectID, project) => {
			if (!domainProjects.some((candidate) => candidate.id === projectID))
				return Promise.reject(new Error("The project belongs to a different domain."));
			return updateOuterProject(projectID, project);
		},
		[domainProjects, updateOuterProject],
	);
	const deleteProject = useCallback<ProjectContextValue["deleteProject"]>(
		async (projectID) => {
			if (!domainProjects.some((candidate) => candidate.id === projectID))
				return Promise.reject(new Error("The project belongs to a different domain."));
			const deleted = await deleteOuterProject(projectID);
			if (selectedProject?.id === projectID) {
				setView((current) =>
					scopeAppViewToWorkspace(resetProjectScopedAssetView(current), {}),
				);
			}
			return deleted;
		},
		[deleteOuterProject, domainProjects, selectedProject?.id],
	);
  const telemetryDiagnostics = telemetryDiagnosticsForProject(
    telemetryDiagnosticsByProject,
    activeProject?.id,
  );
  const thresholdAlerts = thresholdAlertsForProject(
    thresholdAlertsByProject,
    activeProject?.id,
  );
  const activeProjectIdForAlerts = activeProject?.id;
  const acknowledgeThresholdAlertByKey = useCallback(
    (key: string) => {
      if (!activeProjectIdForAlerts) return;
      setThresholdAlertsByProject((prev) =>
        acknowledgeProjectThresholdAlert(prev, activeProjectIdForAlerts, key),
      );
    },
    [activeProjectIdForAlerts],
  );
  const handleProjectWSMessage = useCallback(
    (message: WSMessage) => handleWSMessage(message, activeProject?.id),
    [activeProject?.id, handleWSMessage],
  );
  // A dropped socket may have missed a `domain_feature_policy_changed`
  // broadcast, so the policy is resynced when it comes back. The very first
  // open is skipped because mount already fetched, and rapid reconnects are
  // coalesced: consumers of this signal refetch whole pages, and one of them
  // abandons an in-progress importer when its probe fails, so a flaky link must
  // not turn into a request storm.
  const featurePolicyResyncRef = useRef<{
    opened: boolean;
    timer: ReturnType<typeof setTimeout> | null;
  }>({ opened: false, timer: null });
  const handleProjectWSOpen = useCallback(() => {
    const resync = featurePolicyResyncRef.current;
    if (!resync.opened) {
      resync.opened = true;
      return;
    }
    if (resync.timer) return;
    resync.timer = setTimeout(() => {
      resync.timer = null;
      setFeaturePolicySignal((n) => n + 1);
    }, 2000);
  }, []);
  useEffect(() => {
    const resync = featurePolicyResyncRef.current;
    return () => {
      if (resync.timer) clearTimeout(resync.timer);
    };
  }, []);
  const { connectionState } = useWebSocket({
    onMessage: handleProjectWSMessage,
    onOpen: handleProjectWSOpen,
    projectId: activeProject?.id,
    // Keep an unfiltered global socket alive before a project is selected so
    // connection health and global events remain observable.
    enabled: true,
  });
  const outerApplicationContext = useApplication();
  const applications = useMemo(
    () =>
      activeProject
        ? outerApplicationContext.applications.filter(
            (application) =>
              !application.project_id ||
              application.project_id === activeProject.id,
          )
        : [],
    [activeProject, outerApplicationContext.applications],
  );
  const applicationsLoading = activeProject
    ? outerApplicationContext.applicationsLoading
    : false;
  const applicationsError = activeProject
    ? outerApplicationContext.applicationsError
    : "";
  const activeApplication =
    activeProject &&
    outerApplicationContext.activeApplication &&
    (!outerApplicationContext.activeApplication.project_id ||
      outerApplicationContext.activeApplication.project_id === activeProject.id)
      ? outerApplicationContext.activeApplication
      : null;
  const releases = useMemo(
    () =>
      activeApplication
        ? outerApplicationContext.releases.filter(
            (release) =>
              !release.application_id ||
              release.application_id === activeApplication.id,
          )
        : [],
    [activeApplication, outerApplicationContext.releases],
  );
  const releasesLoading = activeApplication
    ? outerApplicationContext.releasesLoading
    : false;
  const releasesError = activeApplication
    ? outerApplicationContext.releasesError
    : "";
  const activeRelease =
    activeApplication &&
    outerApplicationContext.activeRelease &&
    (!outerApplicationContext.activeRelease.application_id ||
      outerApplicationContext.activeRelease.application_id ===
        activeApplication.id)
      ? outerApplicationContext.activeRelease
      : null;
  const currentWorkspaceScope = useMemo(
    () => ({
      ...(activeProject?.id ? { projectId: activeProject.id } : {}),
      ...(activeApplication?.id
        ? { applicationId: activeApplication.id }
        : {}),
      ...(activeRelease?.id ? { releaseId: activeRelease.id } : {}),
    }),
    [activeApplication?.id, activeProject?.id, activeRelease?.id],
  );
  const setActiveApplication = useCallback<
    ApplicationContextValue["setActiveApplication"]
  >(
    (application) => {
      if (
        activeProject &&
        (!application.project_id || application.project_id === activeProject.id)
      ) {
        const applicationChanged = activeApplication?.id !== application.id;
        outerApplicationContext.setActiveApplication(application);
        if (applicationChanged) {
          setView((current) =>
            scopeAppViewToWorkspace(
              resetProjectScopedAssetView(current, activeProject.id),
              { projectId: activeProject.id, applicationId: application.id },
            ),
          );
        }
      }
    },
    [activeApplication?.id, activeProject, outerApplicationContext],
  );
  const setActiveRelease = useCallback<
    ApplicationContextValue["setActiveRelease"]
  >(
    (release) => {
      if (
        activeApplication &&
        (!release.application_id ||
          release.application_id === activeApplication.id)
      ) {
        const releaseChanged = activeRelease?.id !== release.id;
        outerApplicationContext.setActiveRelease(release);
        if (releaseChanged) {
          setView((current) =>
            scopeAppViewToWorkspace(
              resetProjectScopedAssetView(current, activeProject?.id),
              {
                ...(activeProject?.id ? { projectId: activeProject.id } : {}),
                applicationId: activeApplication.id,
                releaseId: release.id,
              },
            ),
          );
        }
      }
    },
    [activeApplication, activeProject?.id, activeRelease?.id, outerApplicationContext],
  );
  const removeApplication = useCallback<
    ApplicationContextValue["removeApplication"]
  >(
    (applicationID) => {
      if (applications.some((application) => application.id === applicationID)) {
        outerApplicationContext.removeApplication(applicationID);
        if (activeApplication?.id === applicationID) {
          setView((current) =>
            scopeAppViewToWorkspace(
              resetProjectScopedAssetView(current, activeProject?.id),
              activeProject?.id ? { projectId: activeProject.id } : {},
            ),
          );
        }
      }
    },
    [activeApplication?.id, activeProject?.id, applications, outerApplicationContext],
  );
  const removeRelease = useCallback<ApplicationContextValue["removeRelease"]>(
    (releaseID) => {
      if (releases.some((release) => release.id === releaseID)) {
        outerApplicationContext.removeRelease(releaseID);
        if (activeRelease?.id === releaseID) {
          setView((current) =>
            scopeAppViewToWorkspace(
              resetProjectScopedAssetView(current, activeProject?.id),
              {
                ...(activeProject?.id ? { projectId: activeProject.id } : {}),
                ...(activeApplication?.id
                  ? { applicationId: activeApplication.id }
                  : {}),
              },
            ),
          );
        }
      }
    },
    [
      activeApplication?.id,
      activeProject?.id,
      activeRelease?.id,
      outerApplicationContext,
      releases,
    ],
  );
  const refreshApplications = useCallback<
    ApplicationContextValue["refreshApplications"]
  >(
    (options) =>
      activeProject
        ? outerApplicationContext.refreshApplications(options)
        : Promise.resolve(),
    [activeProject, outerApplicationContext],
  );
  const refreshReleases = useCallback<
    ApplicationContextValue["refreshReleases"]
  >(
    (applicationID, options) =>
      activeApplication &&
      (!applicationID || applicationID === activeApplication.id)
        ? outerApplicationContext.refreshReleases(applicationID, options)
        : Promise.resolve(),
    [activeApplication, outerApplicationContext],
  );
  const createAndSelectApplication = useCallback<
    ApplicationContextValue["createAndSelectApplication"]
  >(
    async (name, projectID) => {
      const invocationScope = { ...applicationCreateScopeRef.current };
      const target = resolveApplicationCreateTarget(
        projects,
        domainProjects,
        activeProject,
        projectID,
      );
      if (!target)
        throw new Error(
          projectID
            ? "The project selected for this application is no longer available. Choose another project."
            : "Select a project before creating an application.",
        );
      // Defensive: an explicitly pinned project outside the active domain.
      // Today every scope switch remounts the page and clears drafts before
      // this can be reached, but if a draft ever survives one, commit it to
      // its pinned project without pulling the workspace to a hidden scope.
      if (!target.inDomain)
        return outerApplicationContext.createAndSelectApplication(
          name,
          target.project.id,
        );
      // Create before touching workspace scope: a failed create must leave the
      // workspace where it was, and flipping the project first would put the
      // view and the active project out of step for a commit, unmounting the
      // page that initiated the create.
      const created = await outerApplicationContext.createAndSelectApplication(
        name,
        target.project.id,
      );
      if (!isCurrentApplicationCreateScope(invocationScope, applicationCreateScopeRef.current)) {
        return created;
      }
      if (target.project.id !== selectedProject?.id) {
        // The provider refused to select the created application because the
        // workspace still pointed at the previous project. Flip the scope in
        // the same commit as the view, and hand the provider the created row:
        // its storage cursor makes the post-flip refresh select it. Do not
        // park the applicationId in the route — it is not active until that
        // refresh lands, and a pending route scope blocks rendering.
        setOuterActiveProject(target.project);
        outerApplicationContext.setActiveApplication(created);
        setView((current) =>
          scopeAppViewToWorkspace(
            resetProjectScopedAssetView(current, target.project.id),
            { projectId: target.project.id },
          ),
        );
        return created;
      }
      setView((current) =>
        scopeAppViewToWorkspace(
          resetProjectScopedAssetView(current, target.project.id),
          { projectId: target.project.id, applicationId: created.id },
        ),
      );
      return created;
    },
    [
      activeProject,
      domainProjects,
      outerApplicationContext,
      projects,
      selectedProject?.id,
      setOuterActiveProject,
    ],
  );
  const createAndSelectRelease = useCallback<
    ApplicationContextValue["createAndSelectRelease"]
  >(
    async (name) => {
      if (!activeApplication) return null;
      const created = await outerApplicationContext.createAndSelectRelease(name);
      if (!created) return null;
      setView((current) =>
        scopeAppViewToWorkspace(
          resetProjectScopedAssetView(current, activeProject?.id),
          {
            ...(activeProject?.id ? { projectId: activeProject.id } : {}),
            applicationId: activeApplication.id,
            releaseId: created.id,
          },
        ),
      );
      return created;
    },
    [activeApplication, activeProject?.id, outerApplicationContext],
  );
  const scopedProjectContextValue = useMemo<ProjectContextValue>(
    () => ({
      projects: domainProjects,
      projectsLoading,
      projectsError,
      activeProject,
      setActiveProject,
			removeProject,
      refreshProjects,
      createAndSelectProject,
			updateProject,
			deleteProject,
    }),
    [
      activeProject,
      createAndSelectProject,
			deleteProject,
      domainProjects,
      projectsError,
      projectsLoading,
      refreshProjects,
			removeProject,
      setActiveProject,
			updateProject,
    ],
  );
  const scopedApplicationContextValue = useMemo<ApplicationContextValue>(
    () => ({
      applications,
      applicationsLoading,
      applicationsError,
      releases,
      releasesLoading,
      releasesError,
      activeApplication,
      activeRelease,
      setActiveApplication,
      setActiveRelease,
      removeApplication,
      removeRelease,
      refreshApplications,
      refreshReleases,
      createAndSelectApplication,
      createAndSelectRelease,
    }),
    [
      activeApplication,
      activeRelease,
      applications,
      applicationsError,
      applicationsLoading,
      createAndSelectApplication,
      createAndSelectRelease,
      removeApplication,
      removeRelease,
      refreshApplications,
      refreshReleases,
      releases,
      releasesError,
      releasesLoading,
      setActiveApplication,
      setActiveRelease,
    ],
  );
  const scopedTestListParams = useMemo(
    () =>
      activeProject?.id
        ? {
            project_id: activeProject.id,
            application_id: activeApplication?.id,
            release_id: activeRelease?.id,
            summary: true,
            active_only: true,
            limit: 100,
          }
        : null,
    [activeApplication?.id, activeProject?.id, activeRelease?.id],
  );
  const desktopTestListParams = useMemo(
    () =>
      desktopSurface
        ? {
            summary: true,
            active_only: true,
            limit: 100,
          }
        : null,
    [desktopSurface],
  );
  const { tests: scopedTestSnapshots } = useSharedTestList(
    scopedTestListParams,
    {
      loader: testApi.listTests,
      pollIntervalMs: 10000,
    },
  );
  const { tests: desktopTestSnapshots } = useSharedTestList(
    desktopTestListParams,
    {
      loader: testApi.listTests,
      pollIntervalMs: 10000,
      // The native sleep inhibitor is safety-critical while the window is in the
      // background: scheduled or remote runs in any project must still be seen.
      pollWhenHidden: true,
    },
  );
  protectedMetricsTestIDsRef.current = new Set([
    ...activeTopbarRuns.map((run) => run.id),
    ...scopedTestSnapshots
      .filter((test) =>
        PROTECTED_METRICS_STATUSES.has(
          (test.status || "").trim().toUpperCase(),
        ),
      )
      .map((test) => test.id),
  ]);
  useEffect(() => {
    if (!activeProject?.id) {
      setActiveTopbarRuns([]);
      return;
    }
    setActiveTopbarRuns(
      selectTopbarActiveRuns(scopedTestSnapshots, testStatusesRef.current),
    );
  }, [activeApplication?.id, activeProject?.id, scopedTestSnapshots]);
  const desktopActiveRunCount = useMemo(() => {
    if (!desktopSurface) return activeTopbarRuns.length;
    const globalSnapshotCount = selectTopbarActiveRuns(
      desktopTestSnapshots,
      testStatuses,
    ).length;
    return Math.max(globalSnapshotCount, activeTopbarRuns.length);
  }, [
    activeTopbarRuns.length,
    desktopSurface,
    desktopTestSnapshots,
    testStatuses,
  ]);
  const canManageServiceAccounts = isEffectiveOperationAllowed(
    effectiveAccessResources,
    "manage_service_accounts",
    false,
  );
  const canManageUserLifecycle = isEffectiveOperationAllowed(
    effectiveAccessResources,
    "manage_users",
    false,
  );
  const canManageUserBindings =
    canManageUserLifecycle ||
    isEffectiveOperationAllowed(
      effectiveAccessResources,
      "manage_user_bindings",
      false,
    );
  const canViewUserActivity = isEffectiveOperationAllowed(
    effectiveAccessResources,
    "view_user_activity",
    false,
  );
  const canUseAccessWorkspace =
    canManageServiceAccounts || canManageUserBindings || canViewUserActivity;
  const canManageRepositoryProjects = isEffectiveOperationAllowed(
    effectiveAccessResources,
    "manage_repository_projects",
    false,
  );
  const canManageRepositoryReleases = isEffectiveOperationAllowed(
    effectiveAccessResources,
    "manage_repository_releases",
    false,
  );
  const canManageApplications = effectiveAccessLoaded && !effectiveAccessError && isEffectiveOperationAllowed(
    effectiveAccessResources,
    "manage_applications",
    false,
  );
  const canCreateReleases = effectiveAccessLoaded && !effectiveAccessError && (
    canManageApplications || canManageRepositoryReleases
  );
  const canManageRepository = isEffectiveOperationAllowed(
    effectiveAccessResources,
    "manage_application_repository",
    false,
  );
  const canManageSystem = isEffectiveOperationAllowed(
    effectiveAccessResources,
    "manage_system",
    false,
  );
  const canManageProjects = effectiveAccessLoaded && !effectiveAccessError && isEffectiveOperationAllowed(
    effectiveAccessResources,
    "manage_projects",
    false,
  );
  const selectProjectScopeForRoute = useCallback(
    (projectID: string) => {
      const project = projects.find((candidate) => candidate.id === projectID);
      if (!project) return false;

      const projectDomainID = project.domain_id || "default";
      if (activeDomain?.id !== projectDomainID) {
        const projectDomain = domains.find(
          (candidate) => candidate.id === projectDomainID,
        );
        if (!projectDomain) return false;
        setActiveDomain(projectDomain);
      }
      setOuterActiveProject(project);
      return true;
    },
    [activeDomain?.id, domains, projects, setActiveDomain, setOuterActiveProject],
  );
  const requestedRouteProjectID = projectIdForAppView(view);
  const requestedRouteApplicationID = view.applicationId;
  const requestedRouteReleaseID = view.releaseId;
  const routeTestID = testIdForAppView(view);
  const requestedRouteProject = requestedRouteProjectID
    ? projects.find((project) => project.id === requestedRouteProjectID)
    : undefined;
  const requestedRouteDomainID = requestedRouteProject
    ? requestedRouteProject.domain_id || "default"
    : undefined;
  const routeProjectScopePending = Boolean(
    requestedRouteProjectID && activeProject?.id !== requestedRouteProjectID,
  );
  const routeProjectMissing = Boolean(
    requestedRouteProjectID &&
      !projectsLoading &&
      !requestedRouteProject,
  );
  const routeProjectDomainMissing = Boolean(
    requestedRouteProject &&
      requestedRouteDomainID &&
      domainsLoaded &&
      !domains.some((domain) => domain.id === requestedRouteDomainID),
  );
  const routeProjectScopeMissing =
    routeProjectMissing || routeProjectDomainMissing;
  const routeWorkspaceHierarchyError = requestedRouteApplicationID && !requestedRouteProjectID
    ? "Application workspace scope requires a project scope."
    : requestedRouteReleaseID && !requestedRouteApplicationID
      ? "Release workspace scope requires an application scope."
      : "";
  const genericRouteWorkspaceKey =
    !routeTestID && requestedRouteProjectID && requestedRouteApplicationID
      ? `${requestedRouteProjectID}\u0000${requestedRouteApplicationID}\u0000${requestedRouteReleaseID || ""}`
      : "";
  const [genericRouteWorkspaceResolution, setGenericRouteWorkspaceResolution] =
    useState<{
      key: string;
      status: "reconciling" | "resolved" | "error";
      applicationsRefreshRequested?: boolean;
      releasesRefreshRequested?: boolean;
      message?: string;
    } | null>(null);

  useEffect(() => {
    setGenericRouteWorkspaceResolution(
      genericRouteWorkspaceKey
        ? { key: genericRouteWorkspaceKey, status: "reconciling" }
        : null,
    );
  }, [genericRouteWorkspaceKey]);

  useEffect(() => {
    const resolution = genericRouteWorkspaceResolution;
    if (
      !resolution ||
      resolution.key !== genericRouteWorkspaceKey ||
      resolution.status !== "reconciling" ||
      !requestedRouteProjectID ||
      !requestedRouteApplicationID ||
      activeProject?.id !== requestedRouteProjectID
    ) {
      return;
    }

    const targetApplication = applications.find(
      (application) => application.id === requestedRouteApplicationID,
    );
    if (!targetApplication) {
      if (applicationsLoading) return;
      if (!resolution.applicationsRefreshRequested) {
        setGenericRouteWorkspaceResolution({
          ...resolution,
          applicationsRefreshRequested: true,
        });
        void outerApplicationContext.refreshApplications().catch(() => undefined);
        return;
      }
      setGenericRouteWorkspaceResolution({
        ...resolution,
        status: "error",
        message:
          applicationsError ||
          `Application ${requestedRouteApplicationID} is unavailable in project ${requestedRouteProjectID}.`,
      });
      return;
    }

    if (activeApplication?.id !== targetApplication.id) {
      outerApplicationContext.setActiveApplication(targetApplication);
      setGenericRouteWorkspaceResolution({
        ...resolution,
        releasesRefreshRequested: Boolean(requestedRouteReleaseID),
      });
      if (requestedRouteReleaseID) {
        void outerApplicationContext
          .refreshReleases(targetApplication.id)
          .catch(() => undefined);
      }
      return;
    }

    if (!requestedRouteReleaseID) {
      setGenericRouteWorkspaceResolution({ ...resolution, status: "resolved" });
      return;
    }

    const targetRelease = releases.find(
      (release) => release.id === requestedRouteReleaseID,
    );
    if (!targetRelease) {
      if (releasesLoading) return;
      if (!resolution.releasesRefreshRequested) {
        setGenericRouteWorkspaceResolution({
          ...resolution,
          releasesRefreshRequested: true,
        });
        void outerApplicationContext
          .refreshReleases(targetApplication.id)
          .catch(() => undefined);
        return;
      }
      setGenericRouteWorkspaceResolution({
        ...resolution,
        status: "error",
        message:
          releasesError ||
          `Release ${requestedRouteReleaseID} is unavailable in application ${targetApplication.name}.`,
      });
      return;
    }

    if (activeRelease?.id !== targetRelease.id) {
      outerApplicationContext.setActiveRelease(targetRelease);
    }
    setGenericRouteWorkspaceResolution({ ...resolution, status: "resolved" });
  }, [
    activeApplication?.id,
    activeProject?.id,
    activeRelease?.id,
    applications,
    applicationsError,
    applicationsLoading,
    genericRouteWorkspaceKey,
    genericRouteWorkspaceResolution,
    outerApplicationContext,
    releases,
    releasesError,
    releasesLoading,
    requestedRouteApplicationID,
    requestedRouteProjectID,
    requestedRouteReleaseID,
    setActiveApplication,
    setActiveRelease,
  ]);

  // Route canonicalization writes ids that are already active, so a scope the
  // workspace already satisfies has nothing to reconcile. Without this
  // shortcut, the commits between the key changing and the reconciliation
  // effect resolving block the route and unmount the current page — losing
  // per-page state (for example the open Settings section) on every
  // canonicalized scope write.
  const genericRouteWorkspaceSettled = Boolean(
    requestedRouteProjectID &&
      activeProject?.id === requestedRouteProjectID &&
      (!requestedRouteApplicationID ||
        activeApplication?.id === requestedRouteApplicationID) &&
      (!requestedRouteReleaseID ||
        activeRelease?.id === requestedRouteReleaseID),
  );
  const genericRouteWorkspacePending = Boolean(
    genericRouteWorkspaceKey &&
      !genericRouteWorkspaceSettled &&
      (genericRouteWorkspaceResolution?.key !== genericRouteWorkspaceKey ||
        genericRouteWorkspaceResolution.status === "reconciling"),
  );
  // An error verdict is stale once the workspace already satisfies the route
  // (for example a failed list refresh after a create whose application is
  // nonetheless active): blocking the page then would report "unavailable"
  // about a coherent workspace with no retry affordance.
  const genericRouteWorkspaceError =
    !genericRouteWorkspaceSettled &&
    genericRouteWorkspaceResolution?.key === genericRouteWorkspaceKey &&
    genericRouteWorkspaceResolution.status === "error"
      ? genericRouteWorkspaceResolution.message || "Workspace scope is unavailable."
      : "";

  const unscopedRouteAsset = unscopedProjectAssetForAppView(view);
  const unscopedRouteAssetKind = unscopedRouteAsset?.kind;
  const unscopedRouteAssetID = unscopedRouteAsset?.id;
  const unscopedRouteAssetKey = unscopedRouteAsset
    ? `${unscopedRouteAsset.kind}:${unscopedRouteAsset.id}`
    : "";
  const [unscopedRouteScopeResolution, setUnscopedRouteScopeResolution] =
    useState<{
      key: string;
      status: "resolving" | "error";
      message?: string;
    } | null>(null);

  useEffect(() => {
    if (!unscopedRouteAssetKind || !unscopedRouteAssetID) {
      setUnscopedRouteScopeResolution(null);
      return;
    }

    const key = `${unscopedRouteAssetKind}:${unscopedRouteAssetID}`;
    let cancelled = false;
    setUnscopedRouteScopeResolution({ key, status: "resolving" });

    const ownerRequest =
      unscopedRouteAssetKind === "script"
        ? scriptApi.getScript(unscopedRouteAssetID)
        : unscopedRouteAssetKind === "scenario"
          ? scriptApi.getScenario(unscopedRouteAssetID)
          : testApi.getTest(unscopedRouteAssetID);

    void ownerRequest
      .then((asset) => {
        if (cancelled) return;
        const ownerProjectID = asset.project_id?.trim();
        if (!ownerProjectID) {
          throw new Error("the server response did not identify an owning project");
        }
        setView((current) => {
          const currentAsset = unscopedProjectAssetForAppView(current);
          if (
            !currentAsset ||
            `${currentAsset.kind}:${currentAsset.id}` !== key
          ) {
            return current;
          }
          const scopedView = scopeAppViewToProject(current, ownerProjectID);
          if (typeof window !== "undefined") {
            window.history.replaceState(
              window.history.state,
              "",
              viewToLocationHash(scopedView),
            );
          }
          return scopedView;
        });
      })
      .catch((error: unknown) => {
        if (cancelled) return;
        const detail =
          error instanceof Error && error.message
            ? ` ${error.message}`
            : "";
        setUnscopedRouteScopeResolution({
          key,
          status: "error",
          message: `This older link does not include project scope, and the owning project could not be resolved.${detail}`,
        });
      });

    return () => {
      cancelled = true;
    };
  }, [
    scriptApi,
    testApi,
    unscopedRouteAssetID,
    unscopedRouteAssetKind,
  ]);

  const unscopedRouteScopeError =
    unscopedRouteScopeResolution?.key === unscopedRouteAssetKey &&
    unscopedRouteScopeResolution.status === "error"
      ? unscopedRouteScopeResolution.message || "Project scope is unavailable."
      : "";
  const scopedRouteAssetCandidate = requestedRouteProjectID
    ? projectAssetForAppView(view)
    : null;
  // Runs also reconcile application/release identity below. Scripts and
  // scenarios still need an owner lookup: a stale projectId must never cause
  // their pages to mount against another project's catalog.
  const scopedRouteAsset =
    scopedRouteAssetCandidate?.kind === "test"
      ? null
      : scopedRouteAssetCandidate;
  const scopedRouteAssetKind = scopedRouteAsset?.kind;
  const scopedRouteAssetID = scopedRouteAsset?.id;
  const scopedRouteAssetKey =
    scopedRouteAsset && requestedRouteProjectID
      ? `${scopedRouteAsset.kind}:${scopedRouteAsset.id}:${requestedRouteProjectID}`
      : "";
  const [scopedRouteScopeResolution, setScopedRouteScopeResolution] =
    useState<{
      key: string;
      status: "resolving" | "resolved" | "error";
      message?: string;
    } | null>(null);

  useEffect(() => {
    if (
      !scopedRouteAssetKind ||
      !scopedRouteAssetID ||
      !requestedRouteProjectID
    ) {
      setScopedRouteScopeResolution(null);
      return;
    }

    const key = `${scopedRouteAssetKind}:${scopedRouteAssetID}:${requestedRouteProjectID}`;
    let cancelled = false;
    setScopedRouteScopeResolution({ key, status: "resolving" });
    const ownerRequest =
      scopedRouteAssetKind === "script"
        ? scriptApi.getScript(scopedRouteAssetID)
        : scriptApi.getScenario(scopedRouteAssetID);

    void ownerRequest
      .then((asset) => {
        if (cancelled) return;
        const ownerProjectID = asset.project_id?.trim();
        if (!ownerProjectID) {
          throw new Error("the server response did not identify an owning project");
        }
        if (ownerProjectID !== requestedRouteProjectID) {
          setView((current) => {
            const currentAsset = projectAssetForAppView(current);
            if (
              projectIdForAppView(current) !== requestedRouteProjectID ||
              !currentAsset ||
              `${currentAsset.kind}:${currentAsset.id}` !==
                `${scopedRouteAssetKind}:${scopedRouteAssetID}`
            ) {
              return current;
            }
            const corrected = scopeAppViewToProject(current, ownerProjectID);
            if (typeof window !== "undefined") {
              window.history.replaceState(
                window.history.state,
                "",
                viewToLocationHash(corrected),
              );
            }
            return corrected;
          });
          return;
        }
        setScopedRouteScopeResolution({ key, status: "resolved" });
      })
      .catch((cause: unknown) => {
        if (cancelled) return;
        const detail =
          cause instanceof Error && cause.message ? ` ${cause.message}` : "";
        setScopedRouteScopeResolution({
          key,
          status: "error",
          message: `The ${scopedRouteAssetKind} workspace could not be resolved.${detail}`,
        });
      });

    return () => {
      cancelled = true;
    };
  }, [
    requestedRouteProjectID,
    scopedRouteAssetID,
    scopedRouteAssetKind,
    scriptApi,
  ]);

  const scopedRouteScopePending = Boolean(
    scopedRouteAssetKey &&
      (scopedRouteScopeResolution?.key !== scopedRouteAssetKey ||
        scopedRouteScopeResolution.status === "resolving"),
  );
  const scopedRouteScopeError =
    scopedRouteScopeResolution?.key === scopedRouteAssetKey &&
    scopedRouteScopeResolution.status === "error"
      ? scopedRouteScopeResolution.message || "Project scope is unavailable."
      : "";
  const routeTestScopeKey =
    routeTestID && requestedRouteProjectID
      ? `${routeTestID}\u0000${requestedRouteProjectID}\u0000${requestedRouteApplicationID || ""}\u0000${requestedRouteReleaseID || ""}`
      : "";
  const [routeTestScopeResolution, setRouteTestScopeResolution] = useState<{
    key: string;
    status: "resolving" | "reconciling" | "resolved" | "error";
    test?: import("./types").TestSummary;
    applicationsRefreshRequested?: boolean;
    releasesRefreshRequested?: boolean;
    message?: string;
  } | null>(null);
  // Bumped by "Retry" after a failed run lookup (an outage must not dead-end).
  const [routeTestScopeAttempt, setRouteTestScopeAttempt] = useState(0);

  useEffect(() => {
    if (!routeTestID || !requestedRouteProjectID || unscopedRouteAsset) {
      setRouteTestScopeResolution(null);
      return;
    }
    const key = `${routeTestID}\u0000${requestedRouteProjectID}\u0000${requestedRouteApplicationID || ""}\u0000${requestedRouteReleaseID || ""}`;
    let cancelled = false;
    setRouteTestScopeResolution({ key, status: "resolving" });
    void testApi
      .getTest(routeTestID)
      .then((test) => {
        if (cancelled) return;
        const ownerProjectID = test.project_id?.trim();
        if (!ownerProjectID) {
          throw new Error("the run response did not identify an owning project");
        }
        if (!test.application_id?.trim() || !test.release_id?.trim()) {
          throw new Error(
            "the run response did not identify its application and release",
          );
        }
        const ownerScope = {
          projectId: ownerProjectID,
          applicationId: test.application_id,
          releaseId: test.release_id,
        };
        if (
          ownerProjectID !== requestedRouteProjectID ||
          requestedRouteApplicationID !== test.application_id ||
          requestedRouteReleaseID !== test.release_id
        ) {
          setView((current) => {
            if (
              testIdForAppView(current) !== routeTestID ||
              current.projectId !== requestedRouteProjectID ||
              current.applicationId !== requestedRouteApplicationID ||
              current.releaseId !== requestedRouteReleaseID
            ) {
              return current;
            }
            const corrected = scopeAppViewToWorkspace(current, ownerScope);
            if (typeof window !== "undefined") {
              window.history.replaceState(
                window.history.state,
                "",
                viewToLocationHash(corrected),
              );
            }
            return corrected;
          });
          return;
        }
        setRouteTestScopeResolution({ key, status: "reconciling", test });
      })
      .catch((cause: unknown) => {
        if (cancelled) return;
        const detail =
          cause instanceof Error && cause.message ? ` ${cause.message}` : "";
        setRouteTestScopeResolution({
          key,
          status: "error",
          message: `The run workspace could not be resolved.${detail}`,
        });
      });
    return () => {
      cancelled = true;
    };
  }, [
    requestedRouteApplicationID,
    requestedRouteProjectID,
    requestedRouteReleaseID,
    routeTestID,
    routeTestScopeAttempt,
    testApi,
    unscopedRouteAsset,
  ]);

  useEffect(() => {
    const resolution = routeTestScopeResolution;
    if (
      !resolution ||
      resolution.key !== routeTestScopeKey ||
      resolution.status !== "reconciling" ||
      !resolution.test ||
      activeProject?.id !== resolution.test.project_id
    )
      return;

    const targetApplication = applications.find(
      (application) => application.id === resolution.test?.application_id,
    );
    if (!targetApplication) {
      if (applicationsLoading) return;
      if (!resolution.applicationsRefreshRequested) {
        setRouteTestScopeResolution((current) =>
          current?.key === resolution.key
            ? { ...current, applicationsRefreshRequested: true }
            : current,
        );
        void outerApplicationContext.refreshApplications().catch(() => undefined);
        return;
      }
      setRouteTestScopeResolution({
        ...resolution,
        status: "error",
        message:
          applicationsError ||
          `Application ${resolution.test.application_id} for this run is unavailable in project ${resolution.test.project_id}.`,
      });
      return;
    }

    if (activeApplication?.id !== targetApplication.id) {
      outerApplicationContext.setActiveApplication(targetApplication);
      setRouteTestScopeResolution((current) =>
        current?.key === resolution.key
          ? { ...current, releasesRefreshRequested: true }
          : current,
      );
      void outerApplicationContext
        .refreshReleases(targetApplication.id)
        .catch(() => undefined);
      return;
    }

    const targetRelease = releases.find(
      (release) => release.id === resolution.test?.release_id,
    );
    if (!targetRelease) {
      if (releasesLoading) return;
      if (!resolution.releasesRefreshRequested) {
        setRouteTestScopeResolution((current) =>
          current?.key === resolution.key
            ? { ...current, releasesRefreshRequested: true }
            : current,
        );
        void outerApplicationContext
          .refreshReleases(targetApplication.id)
          .catch(() => undefined);
        return;
      }
      setRouteTestScopeResolution({
        ...resolution,
        status: "error",
        message:
          releasesError ||
          `Release ${resolution.test.release_id} for this run is unavailable in application ${targetApplication.name}.`,
      });
      return;
    }

    if (activeRelease?.id !== targetRelease.id) {
      outerApplicationContext.setActiveRelease(targetRelease);
    }
    setRouteTestScopeResolution({ ...resolution, status: "resolved" });
  }, [
    activeApplication?.id,
    activeProject?.id,
    activeRelease?.id,
    applications,
    applicationsError,
    applicationsLoading,
    outerApplicationContext,
    releases,
    releasesError,
    releasesLoading,
    routeTestScopeKey,
    routeTestScopeResolution,
    setActiveApplication,
    setActiveRelease,
  ]);

  const routeTestScopePending = Boolean(
    routeTestScopeKey &&
      (routeTestScopeResolution?.key !== routeTestScopeKey ||
        routeTestScopeResolution.status === "resolving" ||
        routeTestScopeResolution.status === "reconciling"),
  );
  const routeTestScopeError =
    routeTestScopeResolution?.key === routeTestScopeKey &&
    routeTestScopeResolution.status === "error"
      ? routeTestScopeResolution.message || "The run workspace is unavailable."
      : "";
  const routeProjectScopeError = projectsError && requestedRouteProjectID
    ? projectsError
    : routeProjectMissing
    ? `The project in this link (ID ${requestedRouteProjectID}) is not available to you or no longer exists. Choose a project to continue.`
    : routeProjectDomainMissing
      ? `The domain that owns the project in this link (domain ID ${requestedRouteDomainID}, project ID ${requestedRouteProjectID}) is not available to you. Choose a project to continue.`
      : "";
  const routeAssetOwnerScopeError =
    unscopedRouteScopeError || scopedRouteScopeError;
  const recoverableRouteAsset = unscopedRouteAsset || scopedRouteAsset;
  const routeAssetScopeError =
    routeWorkspaceHierarchyError ||
    routeAssetOwnerScopeError ||
    routeProjectScopeError ||
    genericRouteWorkspaceError ||
    routeTestScopeError;
  const routeAssetScopeBlocked = Boolean(
    routeWorkspaceHierarchyError ||
      unscopedRouteAsset ||
      scopedRouteScopePending ||
      scopedRouteScopeError ||
      routeProjectScopePending ||
      routeProjectScopeMissing ||
      genericRouteWorkspacePending ||
      genericRouteWorkspaceError ||
      routeTestScopePending ||
      routeTestScopeError,
  );

  useEffect(() => {
    if (!requestedRouteProjectID || activeProject?.id === requestedRouteProjectID)
      return;
    selectProjectScopeForRoute(requestedRouteProjectID);
  }, [activeProject?.id, requestedRouteProjectID, selectProjectScopeForRoute]);

  useEffect(() => {
    // A project-scoped generic route may initially omit child scope while the
    // provider loads it (for example immediately after a project selection).
    // Canonicalize only after the route's requested parents are active; never
    // fill a deep link from the previous workspace's transient context.
    if (
      !requestedRouteProjectID ||
      routeTestID ||
      routeWorkspaceHierarchyError ||
      routeProjectScopeMissing ||
      genericRouteWorkspacePending ||
      genericRouteWorkspaceError ||
      activeProject?.id !== requestedRouteProjectID ||
      (requestedRouteApplicationID &&
        activeApplication?.id !== requestedRouteApplicationID) ||
      (requestedRouteReleaseID && activeRelease?.id !== requestedRouteReleaseID)
    ) {
      return;
    }

    const nextScope = {
      projectId: requestedRouteProjectID,
      ...(requestedRouteApplicationID
        ? { applicationId: requestedRouteApplicationID }
        : activeApplication?.id
          ? { applicationId: activeApplication.id }
          : {}),
      ...(requestedRouteReleaseID
        ? { releaseId: requestedRouteReleaseID }
        : activeRelease?.id &&
            (requestedRouteApplicationID || activeApplication?.id) ===
              activeApplication?.id
          ? { releaseId: activeRelease.id }
          : {}),
    };
    const currentScope = workspaceScopeForAppView(view);
    if (
      currentScope.projectId === nextScope.projectId &&
      currentScope.applicationId === nextScope.applicationId &&
      currentScope.releaseId === nextScope.releaseId
    ) {
      return;
    }

    const currentHash = viewToLocationHash(view);
    setView((current) => {
      if (viewToLocationHash(current) !== currentHash) return current;
      replaceRouteOnNextViewSync.current = true;
      return scopeAppViewToWorkspace(current, nextScope);
    });
  }, [
    activeApplication?.id,
    activeProject?.id,
    activeRelease?.id,
    genericRouteWorkspaceError,
    genericRouteWorkspacePending,
    requestedRouteApplicationID,
    requestedRouteProjectID,
    requestedRouteReleaseID,
    routeProjectScopeMissing,
    routeTestID,
    routeWorkspaceHierarchyError,
    view,
  ]);

  const setGuardedView = useCallback(
    (
      next: AppView | ((current: AppView) => AppView),
      afterCommit?: () => void,
      options?: { preserveUnscopedWorkspace?: boolean },
    ) => {
      commitGuardedNavigation(runGuarded, () => {
        const prepareView = (candidate: AppView) =>
          options?.preserveUnscopedWorkspace
            ? candidate
            : mergeAppViewWithWorkspace(candidate, currentWorkspaceScope);
        if (typeof next !== "function") {
          const nextProjectID = projectIdForAppView(prepareView(next));
          if (nextProjectID) selectProjectScopeForRoute(nextProjectID);
        }
        setView((current) => {
          const candidate = typeof next === "function" ? next(current) : next;
          const resolved = prepareView(candidate);
          return normalizeReportViewForFeatureFlag(resolved, runReportEnabled);
        });
      }, afterCommit);
    },
    [currentWorkspaceScope, runGuarded, runReportEnabled, selectProjectScopeForRoute],
  );

  const handleSidebarDomainChange = useCallback(
    (domain: import("./types").Domain) => {
      // Settings also routes domain mutations (rename/save) through this
      // handler. A same-id call is a metadata refresh, not a switch: take the
      // new object without guards, project moves, or a view rewrite — the
      // full switch path would pop a spurious unsaved-changes prompt (the
      // dirty ref has not flushed yet) and remount the page, losing the
      // save/create outcome messages.
      if (domain.id === activeDomain?.id) {
        setActiveDomain(domain);
        return;
      }
      runGuarded(() => {
        const targetProject = projects.find(
          (project) => (project.domain_id || "default") === domain.id,
        );
        setActiveDomain(domain);
        // Move the workspace project in the same commit as the domain and the
        // view. Leaving it to the auto-select effect puts view.projectId ahead
        // of the active project for one commit, which blocks the route scope
        // and unmounts the current page mid-switch.
        if (targetProject) setOuterActiveProject(targetProject);
        setView((current) =>
          scopeAppViewToWorkspace(
            resetProjectScopedAssetView(current, targetProject?.id),
            targetProject?.id ? { projectId: targetProject.id } : {},
          ),
        );
      });
    },
    [activeDomain?.id, projects, runGuarded, setActiveDomain, setOuterActiveProject],
  );
  const handleSidebarProjectChange = useCallback(
    (project: import("./types").Project) => {
      runGuarded(() => {
        setActiveProject(project);
        setView((current) =>
          scopeAppViewToWorkspace(
            resetProjectScopedAssetView(current, project.id),
            { projectId: project.id },
          ),
        );
      });
    },
    [runGuarded, setActiveProject],
  );
  const handleSidebarApplicationChange = useCallback(
    (application: import("./types").Application) => {
      runGuarded(() => {
        setActiveApplication(application);
        setView((current) =>
          scopeAppViewToWorkspace(
            resetProjectScopedAssetView(current, activeProject?.id),
            {
              ...(activeProject?.id ? { projectId: activeProject.id } : {}),
              applicationId: application.id,
            },
          ),
        );
      });
    },
    [activeProject?.id, runGuarded, setActiveApplication],
  );
  const handleSidebarReleaseChange = useCallback(
    (release: import("./types").Release) => {
      runGuarded(() => {
        setActiveRelease(release);
        setView((current) =>
          scopeAppViewToWorkspace(
            resetProjectScopedAssetView(current, activeProject?.id),
            {
              ...(activeProject?.id ? { projectId: activeProject.id } : {}),
              ...(activeApplication?.id
                ? { applicationId: activeApplication.id }
                : {}),
              releaseId: release.id,
            },
          ),
        );
      });
    },
    [activeApplication?.id, activeProject?.id, runGuarded, setActiveRelease],
  );

  const navigateToTest = useCallback(
    (id: string, tab?: TestWorkspaceTabID, projectID = activeProject?.id) => {
      const next: Extract<AppView, { page: "test" }> = {
        page: "test",
        testId: id,
        ...(tab ? { tab } : {}),
        ...(projectID ? { projectId: projectID } : {}),
      };
      setGuardedView(next, () => {
        void addRecent({
          kind: "test",
          id,
          name: id,
          route: viewToLocationHash(next),
        }).then(reloadRecents);
      });
    },
    [activeProject?.id, reloadRecents, setGuardedView],
  );
  const handleTestWorkspaceTabChange = useCallback(
    (tab: TestWorkspaceTabID | undefined, options: { replace: boolean }) => {
      setView((current) => {
        if (current.page !== "test" || current.tab === tab) return current;
        replaceRouteOnNextViewSync.current = options.replace;
        return tab
          ? { ...current, tab }
          : {
              page: "test",
              testId: current.testId,
              ...workspaceScopeForAppView(current),
            };
      });
    },
    [],
  );
  // Activity filters and page follow the URL without adding history entries.
  const handleCommandCenterViewChange = useCallback((ccView: CommandCenterRouteView | undefined) => {
    setView((current) => {
      if (current.page !== "command-center" || current.ccView === ccView) return current;
      replaceRouteOnNextViewSync.current = true;
      const next = { ...current };
      delete next.ccView;
      return ccView ? { ...next, ccView } : next;
    });
  }, []);
  const handleActivityQueryChange = useCallback((activityQuery: string) => {
    setView((current) => {
      if (current.page !== "activity" || (current.activityQuery || "") === activityQuery) return current;
      replaceRouteOnNextViewSync.current = true;
      return { ...current, activityQuery: activityQuery || undefined };
    });
  }, []);
  const navigateToTestArchive = useCallback(
    (id: string) => navigateToTest(id, "archive"),
    [navigateToTest],
  );
  const navigateToRunReport = useCallback(
    (id: string, projectID = activeProject?.id) => {
      if (!runReportEnabled) {
        navigateToTest(id, "archive", projectID);
        return;
      }
      const next: Extract<AppView, { page: "report" }> = {
        page: "report",
        testId: id,
        ...(projectID ? { projectId: projectID } : {}),
      };
      setGuardedView(next, () => {
        void addRecent({
          kind: "test",
          id,
          name: id,
          route: viewToLocationHash(next),
        }).then(reloadRecents);
      });
    },
    [activeProject?.id, navigateToTest, reloadRecents, runReportEnabled, setGuardedView],
  );
  const navigateToCommandCenter = useCallback(
    (id?: string, projectID = activeProject?.id) => {
      setGuardedView({
        page: "command-center",
        ...(id ? { testId: id } : {}),
        ...(projectID ? { projectId: projectID } : {}),
        // Switching runs inside Command Center keeps the analysis view open.
        ...(id && viewRef.current.page === "command-center" && viewRef.current.ccView ? { ccView: viewRef.current.ccView } : {}),
      });
    },
    [activeProject?.id, setGuardedView],
  );
  const openCompletedRun = runReportEnabled
    ? navigateToRunReport
    : navigateToTestArchive;
  const openRunRequestRef = useRef(0);
  const openRun = useCallback((id: string, projectID = activeProject?.id) => {
    const request = ++openRunRequestRef.current;
    const sourceView = viewRef.current;
    void testApi.getTest(id).then(test => {
      if (request !== openRunRequestRef.current || sourceView !== viewRef.current) return;
      const status = testStatusesRef.current[id] || test.status;
      if (["COMPLETE", "FAILED", "ABORTED"].includes(status)) openCompletedRun(id, projectID);
      else navigateToTest(id, undefined, projectID);
    }).catch(() => {
      if (request === openRunRequestRef.current && sourceView === viewRef.current) navigateToTest(id, undefined, projectID);
    });
  }, [activeProject?.id, navigateToTest, openCompletedRun, testApi]);
  const terminalRouteStatus = routeTestScopeResolution?.key === routeTestScopeKey
    ? testStatuses[routeTestID || ""] || routeTestScopeResolution?.test?.status || ""
    : "";
  useEffect(() => {
    if (routeAssetScopeBlocked) return;
    const next = terminalRunReportView(view, terminalRouteStatus, runReportEnabled);
    if (next === view) return;
    runGuarded(() => {
      replaceRouteOnNextViewSync.current = true;
      setDetailsOrigin(null);
      setView(next);
    });
  }, [view, terminalRouteStatus, routeAssetScopeBlocked, runReportEnabled, runGuarded]);
  const activePage = navPageForAppView(view);

  const navigateToScriptEdit = (scriptId: string, projectID = activeProject?.id) => {
    const next: Extract<AppView, { page: "scripts" }> = {
      page: "scripts",
      mode: "edit",
      scriptId,
      ...(projectID ? { projectId: projectID } : {}),
    };
    setGuardedView(next, () => {
      void addRecent({
        kind: "script",
        id: scriptId,
        name: scriptId,
        route: viewToLocationHash(next),
      }).then(reloadRecents);
    });
  };
  const navigateToScenarioBuilder = () =>
    setGuardedView({ page: "tests", scenarioView: "builder" });
  const navigateToNewTestForScenario = (
    scenarioId: string,
    projectID = activeProject?.id,
  ) => {
    setGuardedView({
      page: "new-test",
      scenarioId,
      ...(projectID ? { projectId: projectID } : {}),
    });
  };

  const navigateToScheduleForDate = useCallback(
    (prefillStartLocal: string) => {
      setGuardedView({ page: "new-test", prefillStartLocal });
    },
    [setGuardedView],
  );
  const navigateToMonitorCategory = useCallback(
    (categoryId: MonitorCategoryId) => {
      setGuardedView(viewForMonitorCategory(categoryId));
    },
    [setGuardedView],
  );

  const performNavigate = useCallback((page: NavPage) => {
    if (page === "scripts") {
      setScriptsNavigationVersion((version) => version + 1);
      setScriptsTopbarState(null);
    }
    setView(mergeAppViewWithWorkspace({ page }, currentWorkspaceScope));
  }, [currentWorkspaceScope]);

  const handleNavigate = useCallback(
    (page: NavPage) => {
      runGuarded(() => performNavigate(page));
    },
    [performNavigate, runGuarded],
  );

  const openNativeImportFiles = useCallback(
    (files: File[]) => {
      const usable = files.filter(Boolean);
      if (usable.length === 0) return;
      setDesktopImportError("");
      const requestID = ++nativeImportRequestGenerationRef.current;
      const isCurrent = (candidate: number) =>
        nativeImportRequestGenerationRef.current === candidate;
      void resolveNativeImportRequest(usable, requestID, isCurrent).then(
        (request) => {
          if (!request) return;
          runGuarded(() => {
            if (!isCurrent(requestID)) return;
            setNativeImportRequest(request);
            setScriptsNavigationVersion((version) => version + 1);
            setScriptsTopbarState(null);
            setView(
              mergeAppViewWithWorkspace(
                { page: "scripts" },
                currentWorkspaceScope,
              ),
            );
          });
        },
      );
    },
    [currentWorkspaceScope, runGuarded],
  );

  // Desktop workspace hub wiring.
  const handleDesktopQuickAction = useCallback(
    (action: DesktopQuickAction) => {
      switch (action) {
        case "new-test":
          setGuardedView({ page: "new-test" });
          break;
        case "import":
        case "record":
        case "scripts":
          handleNavigate("scripts");
          break;
        case "results":
          handleNavigate("results");
          break;
      }
    },
    [setGuardedView, handleNavigate],
  );

  const handleOpenRecent = useCallback(
    (item: RecentItem) => {
      if (item.route) {
        setGuardedView(parseAppViewFromHash(item.route), undefined, {
          preserveUnscopedWorkspace: true,
        });
      }
    },
    [setGuardedView],
  );

  const handleClearRecents = useCallback(() => {
    void clearRecents().then(reloadRecents);
  }, [reloadRecents]);

  const handleTogglePin = useCallback(
    (item: RecentItem) => {
      void toggleRecentPin(item.kind, item.id).then(reloadRecents);
    },
    [reloadRecents],
  );

  const handleRevealFolder = useCallback((which: "logs" | "appdata") => {
    const bridge = getDesktopBridge();
    if (which === "logs") void bridge?.OpenLogsFolder?.();
    else void bridge?.OpenAppDataFolder?.();
  }, []);

  const openShortcutHelp = useKeyboardShortcutHelp();
  const handleDesktopMenuAction = useCallback(
    (action: string) => {
      if (action === "new-test") setGuardedView({ page: "new-test" });
      else if (action === "help") handleNavigate("docs");
      else if (action === "shortcuts") openShortcutHelp();
      else handleNavigate("scripts");
    },
    [setGuardedView, handleNavigate, openShortcutHelp],
  );
  const handleDesktopOpenFile = useCallback(
    async (path: string) => {
      try {
        const files = await filesFromDroppedPaths([path]);
        if (files.length > 0) {
          setDesktopImportError("");
          openNativeImportFiles(files);
        } else {
          handleNavigate("scripts");
        }
      } catch (error) {
        setDesktopImportError(nativeFileReadErrorMessage(error));
        handleNavigate("scripts");
      }
    },
    [handleNavigate, openNativeImportFiles],
  );
  const handleDesktopOpenURL = useCallback(
    (url: string) => {
      const nextView = parseDesktopDeepLink(url);
      if (nextView) {
        setGuardedView(nextView, undefined, {
          preserveUnscopedWorkspace: true,
        });
      }
    },
    [setGuardedView],
  );
  const handleDesktopOpenRecent = useCallback(
    (route: string) => {
      // Native File > Open Recent click → navigate to the recent's route.
      if (route) {
        setGuardedView(parseAppViewFromHash(route), undefined, {
          preserveUnscopedWorkspace: true,
        });
      }
    },
    [setGuardedView],
  );

  const desktopShellHandlers = useMemo<DesktopShellEventHandlers>(
    () => ({
      onMenuOpenBrowser: handleOpenDesktopBrowser,
      onMenuAction: handleDesktopMenuAction,
      onOpenFile: handleDesktopOpenFile,
      onOpenURL: handleDesktopOpenURL,
      onOpenRecent: handleDesktopOpenRecent,
      onRecentsChanged: reloadRecents,
    }),
    [
      handleOpenDesktopBrowser,
      handleDesktopMenuAction,
      handleDesktopOpenFile,
      handleDesktopOpenURL,
      handleDesktopOpenRecent,
      reloadRecents,
    ],
  );
  // Keep the machine awake while runs are active so long tests aren't corrupted
  // by the host sleeping (desktop shell only; no-op in the browser).
  const desktopShellActiveRunCount = desktopSurface
    ? desktopActiveRunCount
    : activeTopbarRuns.length;
  useKeepAwakeDuringRuns(desktopShellActiveRunCount);
  // Reflect the active-run count on the dock/taskbar badge.
  useDockBadge(desktopShellActiveRunCount);

  const handleScriptsAuthoringStateChange = useCallback(
    (state: { subView: ScriptsAuthoringSubView; editingId: string | null; importLauncher?: string | null; importMode?: string | null }) => {
      setScriptsTopbarState(state);
      // Keep the URL naming what is open (library, editor, import) so a
      // refresh or a shared link returns to it. replaceState, not setView:
      // the page is keyed by its route and a view change would remount it.
      const current = parseAppViewFromHash(window.location.hash);
      if (current.page !== "scripts") return;
      const next = viewToLocationHash(scriptsRouteForAuthoringState(state, workspaceScopeForAppView(current)));
      if (next !== window.location.hash) window.history.replaceState(window.history.state, "", next);
    },
    [],
  );

  const handleScenarioViewChange = useCallback(
    (scenarioView: "library" | "builder" | "plans") => {
      setView((current) => {
        if (current.page !== "tests") return current;
        const next: AppView =
          scenarioView === "builder"
            ? {
                page: "tests",
                scenarioView: "builder",
                ...workspaceScopeForAppView(current),
              }
            : scenarioView === "plans"
              ? {
                  page: "tests",
                  scenarioView: "plans",
                  ...workspaceScopeForAppView(current),
                }
              : {
                  page: "tests",
                  ...workspaceScopeForAppView(current),
                };
        return viewToLocationHash(current) === viewToLocationHash(next)
          ? current
          : next;
      });
    },
    [],
  );

  useEffect(() => {
    if (view.page !== "scripts") {
      setScriptsTopbarState(null);
    }
  }, [view.page]);

  const navigateBack = useCallback(
    (fallbackPage: NavPage = "dashboard") => {
      if (appHistoryIndex.current > 0) {
        runGuarded(() => window.history.back());
      } else {
        setGuardedView({ page: fallbackPage });
      }
    },
    [runGuarded, setGuardedView],
  );
  const navigateBackFromTest = useCallback(
    () => {
      const current = viewRef.current;
      navigateBack(
        current.page === "test"
          ? navPageForAppView(current)
          : "command-center",
      );
    },
    [navigateBack],
  );

  const appShellShortcuts = useMemo(
    () => [
      {
        id: "global.back",
        label: "Go back",
        keys: "Backspace",
        group: "Global",
        priority: 600,
        onTrigger: () => navigateBack("dashboard"),
      },
      {
        id: "current-page.search",
        label: "Focus page search or filter",
        keys: "/",
        group: "Current Page",
        priority: 200,
        onTrigger: () => {
          focusCurrentPageSearch();
        },
      },
      {
        id: "global.zoom-in",
        label: "Zoom dashboard in",
        keys: "Cmd/Ctrl+=",
        group: "Global",
        preventDefault: false,
        priority: 20,
        onTrigger: () => requestDashboardZoom("in"),
      },
      {
        id: "global.zoom-out",
        label: "Zoom dashboard out",
        keys: "Cmd/Ctrl+-",
        group: "Global",
        preventDefault: false,
        priority: 20,
        onTrigger: () => requestDashboardZoom("out"),
      },
      {
        id: "global.zoom-reset",
        label: "Reset dashboard zoom",
        keys: "Cmd/Ctrl+0",
        group: "Global",
        preventDefault: false,
        priority: 20,
        onTrigger: () => requestDashboardZoom("reset"),
      },
      ...buildNavigationShortcuts({
        canUseAccessWorkspace,
        isDevUserPreview,
        onNavigate: handleNavigate,
      }),
    ],
    [canUseAccessWorkspace, handleNavigate, isDevUserPreview, navigateBack],
  );
  useKeyboardShortcuts(appShellShortcuts);

  useEffect(() => {
    viewRef.current = view;
  }, [view]);

  useEffect(() => {
    if (typeof window === "undefined") return undefined;

    const handleRouteChange = (event: PopStateEvent | HashChangeEvent) => {
      const isPopState = event.type === "popstate";
      if (
        !isPopState &&
        popstateHashToIgnore.current === window.location.hash
      ) {
        popstateHashToIgnore.current = null;
        return;
      }
      if (!isPopState) {
        popstateHashToIgnore.current = null;
      }
      if (isPopState) {
        popstateHashToIgnore.current = window.location.hash;
      }

      const targetHistoryIndex = isPopState
        ? readAppHistoryIndex((event as PopStateEvent).state)
        : null;

      if (
        isPopState &&
        targetHistoryIndex !== null &&
        guardedHistoryTraversal.current
      ) {
        const traversal = guardedHistoryTraversal.current;
        if (traversal.phase === "restoring") {
          guardedHistoryTraversal.current = null;
          appHistoryIndex.current = traversal.pending.originIndex;
          runGuarded(() => {
            if (
              appHistoryIndex.current !== traversal.pending.originIndex ||
              viewToLocationHash(viewRef.current) !==
                traversal.pending.originHash
            ) {
              return;
            }
            guardedHistoryTraversal.current = {
              phase: "proceeding",
              pending: traversal.pending,
            };
            window.history.go(
              traversal.pending.targetIndex - traversal.pending.originIndex,
            );
          });
          return;
        }

        guardedHistoryTraversal.current = null;
        appHistoryIndex.current = traversal.pending.targetIndex;
        const normalizedTarget = normalizeReportViewForFeatureFlag(
          traversal.pending.targetView,
          runReportEnabled,
        );
        const normalizedHash = traversal.pending.targetHash;
        if (
          window.location.hash !== normalizedHash ||
          readAppHistoryIndex(window.history.state) !==
            traversal.pending.targetIndex
        ) {
          popstateHashToIgnore.current = normalizedHash;
          window.history.replaceState(
            withAppHistoryIndex(
              window.history.state,
              traversal.pending.targetIndex,
            ),
            "",
            normalizedHash,
          );
        }
        setView((current) =>
          viewToLocationHash(current) === normalizedHash
            ? current
            : normalizedTarget,
        );
        return;
      }

      if (isPopState && targetHistoryIndex !== null && hasDirtyGuard()) {
        const originIndex = appHistoryIndex.current;
        const restoreDelta = originIndex - targetHistoryIndex;
        if (restoreDelta !== 0) {
          const targetView = normalizeReportViewForFeatureFlag(
            parseAppViewFromHash(window.location.hash),
            runReportEnabled,
          );
          guardedHistoryTraversal.current = {
            phase: "restoring",
            pending: {
              originHash: viewToLocationHash(viewRef.current),
              originIndex,
              targetHash: viewToLocationHash(targetView),
              targetIndex: targetHistoryIndex,
              targetView,
            },
          };
          window.history.go(restoreDelta);
          return;
        }
      }

      handleGuardedRouteChange({
        currentView: viewRef.current,
        locationHash: window.location.hash,
        hasDirtyGuard,
        runGuarded,
        restoreHash: (hash) => {
          window.history.pushState(
            withAppHistoryIndex(window.history.state, appHistoryIndex.current),
            "",
            hash,
          );
        },
        applyView: (nextView) => {
          const normalizedView = normalizeReportViewForFeatureFlag(
            nextView,
            runReportEnabled,
          );
          const nextHash = viewToLocationHash(normalizedView);
          const nextHistoryIndex =
            targetHistoryIndex ?? appHistoryIndex.current + 1;
          appHistoryIndex.current = nextHistoryIndex;
          if (
            window.location.hash !== nextHash ||
            readAppHistoryIndex(window.history.state) !== nextHistoryIndex
          ) {
            if (isPopState) popstateHashToIgnore.current = nextHash;
            window.history.replaceState(
              withAppHistoryIndex(window.history.state, nextHistoryIndex),
              "",
              nextHash,
            );
          }
          setView((current) =>
            viewToLocationHash(current) === nextHash ? current : normalizedView,
          );
        },
      });
    };

    window.addEventListener("popstate", handleRouteChange);
    window.addEventListener("hashchange", handleRouteChange);
    return () => {
      window.removeEventListener("popstate", handleRouteChange);
      window.removeEventListener("hashchange", handleRouteChange);
      popstateHashToIgnore.current = null;
    };
  }, [hasDirtyGuard, runGuarded, runReportEnabled]);

  useEffect(() => {
    if (typeof window === "undefined") return;

    const nextHash = viewToLocationHash(view);
    if (!routeSyncInitialized.current) {
      routeSyncInitialized.current = true;
      appHistoryIndex.current =
        readAppHistoryIndex(window.history.state) ?? appHistoryIndex.current;
      const indexedState = withAppHistoryIndex(
        window.history.state,
        appHistoryIndex.current,
      );
      if (!window.location.hash && view.page === "dashboard") {
        window.history.replaceState(indexedState, "", window.location.href);
        return;
      }
      if (window.location.hash !== nextHash) {
        window.history.replaceState(indexedState, "", nextHash);
      } else if (
        readAppHistoryIndex(window.history.state) !== appHistoryIndex.current
      ) {
        window.history.replaceState(indexedState, "", window.location.href);
      }
      return;
    }

    const replaceCurrentRoute = replaceRouteOnNextViewSync.current;
    replaceRouteOnNextViewSync.current = false;
    if (window.location.hash !== nextHash) {
      if (replaceCurrentRoute) {
        window.history.replaceState(
          withAppHistoryIndex(window.history.state, appHistoryIndex.current),
          "",
          nextHash,
        );
        return;
      }
      const nextHistoryIndex = appHistoryIndex.current + 1;
      appHistoryIndex.current = nextHistoryIndex;
      popstateHashToIgnore.current = null;
      window.history.pushState(
        withAppHistoryIndex(window.history.state, nextHistoryIndex),
        "",
        nextHash,
      );
    }
  }, [view]);

  useDesktopRoutePersistence({
    enabled: desktopSurface,
    route: viewToLocationHash(view),
    restoreRoute: useCallback(
      (route) => setGuardedView(parseAppViewFromHash(route), undefined, {
        preserveUnscopedWorkspace: true,
      }),
      [setGuardedView],
    ),
  });

  // The renderer handshake lives above AuthGate, but these handlers must not
  // become deliverable until AppShell's route-restoration listener is mounted.
  // This effect follows useDesktopRoutePersistence and runs before the parent
  // AppContent effect, retaining gated native intents without acknowledging a
  // no-op handler.
  useEffect(() => {
    desktopHandlerCoordinator.setWorkspaceHandlers(desktopShellHandlers);
    return () => {
      desktopHandlerCoordinator.clearWorkspaceHandlers(desktopShellHandlers);
    };
  }, [desktopHandlerCoordinator, desktopShellHandlers]);

  const refreshDomains = useCallback(
    async (
      preferredId?: string,
      reconciliation?: {
        preserveDomain?: import("./types").Domain;
        removeDomainId?: string;
      },
    ) => {
      const requestGeneration = ++domainsRequestGenerationRef.current;
      const requestIsCurrent = () =>
        isCurrentCatalogRequest(
          requestGeneration,
          domainsRequestGenerationRef.current,
        );
      const reconcile = (catalog: import("./types").Domain[]) => {
        const withoutRemoved = reconciliation?.removeDomainId
          ? catalog.filter(
              (domain) => domain.id !== reconciliation.removeDomainId,
            )
          : catalog;
        const preserved = reconciliation?.preserveDomain;
        const merged = preserved
          ? withoutRemoved.some((domain) => domain.id === preserved.id)
            ? withoutRemoved.map((domain) =>
                domain.id === preserved.id ? preserved : domain,
              )
            : [...withoutRemoved, preserved]
          : withoutRemoved;
        return [...merged].sort((left, right) => left.name.localeCompare(right.name));
      };
      try {
        const response = await repositoryApi.listDomains();
        if (!requestIsCurrent()) return domainsRef.current;
        const next = reconcile(response.domains || []);
        setDomains(next);
        setDomainsLoaded(true);
        setDomainsError("");
        setActiveDomainState((prev) => {
          const storedId = readStoredString("apexload.activeDomainId");
          const selected =
            next.find((domain) => domain.id === preferredId) ||
            next.find((domain) => domain.id === storedId) ||
            next.find((domain) => domain.id === prev?.id) ||
            next[0] ||
            null;
          if (selected)
            writeStoredString("apexload.activeDomainId", selected.id);
          else removeStoredItem("apexload.activeDomainId");
          return selected;
        });
        return next;
      } catch (cause: unknown) {
        if (!requestIsCurrent()) return domainsRef.current;
        const message =
          cause instanceof Error && cause.message
            ? `Unable to load domains: ${cause.message}`
            : "Unable to load domains.";
        setDomains((current) => reconcile(current));
        setDomainsLoaded(true);
        setDomainsError(message);
        if (reconciliation?.removeDomainId) {
          setActiveDomainState((current) => {
            if (current?.id !== reconciliation.removeDomainId) return current;
            removeStoredItem("apexload.activeDomainId");
            return null;
          });
        }
        throw cause;
      }
    },
    [repositoryApi],
  );

  const refreshRepository = useCallback(async () => {
    const domainID = activeDomain?.id || "";
    const requestGeneration = ++repositoryRequestGenerationRef.current;
    if (!domainID) return;

    const [repositoryResult, categoriesResult] = await Promise.allSettled([
      repositoryApi.getApplicationRepository(domainID),
      repositoryApi.listRepositoryCategories(domainID),
    ]);
    if (
      !isCurrentRepositoryRequest(
        requestGeneration,
        domainID,
        repositoryRequestGenerationRef.current,
        activeDomainIDRef.current,
      )
    )
      return;

    const patch: RepositoryCatalogRefreshPatch = {};
    if (repositoryResult.status === "fulfilled") {
      patch.repository = repositoryResult.value;
    }
    if (categoriesResult.status === "fulfilled") {
      const reconciliation = reconcileRepositoryCategorySnapshot(
        domainID,
        categoriesResult.value.categories || [],
        repositoryCategoryMutationLedgerRef.current,
      );
      for (const categoryID of reconciliation.acknowledgedCommittedIDs) {
        repositoryCategoryMutationLedgerRef.current.committedByID.delete(categoryID);
      }
      patch.categories = reconciliation.categories;
    }
    setRepositoryCatalogState((current) =>
      mergeRepositoryCatalogRefresh(current, domainID, patch),
    );

    const failedResult = [repositoryResult, categoriesResult].find(
      (result) => result.status === "rejected",
    );
    if (failedResult?.status === "rejected") throw failedResult.reason;
  }, [activeDomain?.id, repositoryApi]);

  const createRepositoryCategory = useCallback(
    async (
      domainID: string,
      data: { name: string; description?: string; sort_order?: number },
    ) => {
      const category = await repositoryApi.createRepositoryCategory(domainID, data);
      recordCommittedRepositoryCategory(
        repositoryCategoryMutationLedgerRef.current,
        category,
      );
      setRepositoryCatalogState((current) => {
        const previousCategories = current.categoriesByDomain[domainID] || [];
        return mergeRepositoryCatalogRefresh(current, domainID, {
          categories: [
            ...previousCategories.filter((candidate) => candidate.id !== category.id),
            category,
          ],
        });
      });
      return category;
    },
    [repositoryApi],
  );

  useEffect(() => {
    void refreshDomains().catch(() => undefined);
  }, [refreshDomains]);

  useEffect(() => {
    void refreshRepository().catch(() => undefined);
  }, [refreshRepository]);

  useEffect(() => {
    if (!activeDomain || domainProjects.length === 0) return;
    if (
      !selectedProject ||
      (selectedProject.domain_id || "default") !== activeDomain.id
    ) {
      // Provider initialization is a fallback, not a user navigation. Keep an
      // explicit route workspace authoritative until its reconciliation effect
      // selects the requested project.
      setOuterActiveProject(domainProjects[0]);
    }
  }, [activeDomain, domainProjects, selectedProject, setOuterActiveProject]);

  const { repository: applicationRepository, categories: repositoryCategories } =
    repositoryCatalogForDomain(
      repositoryCatalogState,
      activeDomain?.id,
    );
  const snapshotAudience = dashboardSnapshotAudience(
    authStatus,
    devViewMode,
    activeProject?.id,
    activeDomain?.id,
    applicationRepository?.id,
  );

  useEffect(() => {
    let cancelled = false;
    const loadEffectiveAccess = async () => {
      setEffectiveAccessLoaded(false);
      setEffectiveAccessError(false);
      try {
        const response = await getAuthEffectiveAccess(activeProject?.id, {
          domain_id: activeDomain?.id,
          application_repository_id: applicationRepository?.id,
        });
        if (!cancelled) {
          setEffectiveAccessResources(response.resources || []);
        }
      } catch {
        if (!cancelled) {
          setEffectiveAccessResources([]);
          setEffectiveAccessError(true);
        }
      } finally {
        if (!cancelled) {
          setEffectiveAccessLoaded(true);
          setAccessPermissionsInitialized(true);
        }
      }
    };

    loadEffectiveAccess();
    return () => {
      cancelled = true;
    };
  }, [
    activeProject?.id,
    activeDomain?.id,
    applicationRepository?.id,
    authStatus?.mode,
    accessReviewSignal,
    devViewMode,
    getAuthEffectiveAccess,
  ]);


  // A link to Access that the session's role cannot open lands on the
  // Dashboard with a note, rather than silently somewhere else.
  const [accessRedirectNotice, setAccessRedirectNotice] = useState(false);
  useEffect(() => {
    if (view.page !== "dashboard") setAccessRedirectNotice(false);
  }, [view.page]);
  useEffect(() => {
    if (
      effectiveAccessLoaded &&
      view.page === "access" && !view.setting &&
      !canUseAccessWorkspace
    ) {
      setAccessRedirectNotice(true);
      setView((current) =>
        scopeAppViewToWorkspace(
          { page: "dashboard" },
          workspaceScopeForAppView(current),
        ),
      );
    }
  }, [canUseAccessWorkspace, effectiveAccessLoaded, view]);

  useEffect(() => {
    let cancelled = false;
    const loadDesktopStatus = async () => {
      try {
        const status = await libraryApi.getDesktopStatus();
        if (!cancelled) {
          setDesktopStatus(status);
        }
      } catch {
        if (!cancelled) {
          setDesktopStatus(null);
        }
      }
    };

    void loadDesktopStatus();
    const interval = window.setInterval(() => {
      void loadDesktopStatus();
    }, 30000);
    return () => {
      cancelled = true;
      window.clearInterval(interval);
    };
  }, [libraryApi]);

  const desktopBridge = getWailsDesktopBridge();
  const showDesktopStatus = Boolean(desktopStatus?.desktop_mode);
  const currentRouteTestId =
    view.page === "test" || view.page === "command-center"
      ? view.testId
      : undefined;
  const currentTestRun = currentRouteTestId
    ? activeTopbarRuns.find((run) => run.id === currentRouteTestId)
    : undefined;
  const topbarPageTitle =
    view.page === "scripts" && scriptsTopbarState
      ? pageTitleForScriptSubView(scriptsTopbarState.subView, scriptsTopbarState.importLauncher)
      : pageTitleForView(view, {
          testName: currentTestRun?.name,
        });
  const orderedTopbarRuns =
    (view.page === "test" || view.page === "command-center") && currentTestRun
      ? [
          currentTestRun,
          ...activeTopbarRuns.filter((run) => run.id !== currentTestRun.id),
        ]
      : activeTopbarRuns;
  const visibleTopbarRuns = orderedTopbarRuns.slice(0, 2);
  const hiddenTopbarRunCount = Math.max(
    0,
    orderedTopbarRuns.length - visibleTopbarRuns.length,
  );
  const topbarPrimaryActions = useMemo(
    () =>
      (activeTopbarContribution?.primaryActions || []).filter(
        (action) => !action.hidden,
      ),
    [activeTopbarContribution?.primaryActions],
  );
  const topbarSecondaryActions = useMemo(
    () =>
      (activeTopbarContribution?.secondaryActions || []).filter(
        (action) => !action.hidden,
      ),
    [activeTopbarContribution?.secondaryActions],
  );
  const topbarMeta = useMemo(
    () => (activeTopbarContribution?.meta || []).filter((chip) => !chip.hidden),
    [activeTopbarContribution?.meta],
  );
  const hasContextualTopbarActions =
    topbarPrimaryActions.length > 0 ||
    topbarSecondaryActions.length > 0 ||
    Boolean(activeTopbarContribution?.controls);
  const showTopbarRunChips =
    visibleTopbarRuns.length > 0 && !hasContextualTopbarActions;
  // Pages with their own top-bar controls still show that a run is live (the
  // engineer editing a script or scenario must not lose sight of it), as one
  // compact chip instead of the full list.
  const compactTopbarRun =
    hasContextualTopbarActions && orderedTopbarRuns.length > 0
      ? orderedTopbarRuns[0]
      : undefined;
  // The route is the authoritative page identity. A lazily suspended previous
  // page can keep its contribution mounted briefly. Only an active script
  // editor may refine its route (or an explicitly active scenario detour) title.
  const effectiveTopbarTitle = ((view.page === "scripts" && scriptsTopbarState?.editingId) || (view.page === "tests" && scenarioScriptWorkflow)) && activeTopbarContribution?.id === "script-editor"
    ? activeTopbarContribution?.title || topbarPageTitle
    : view.page === "tests" && scenarioScriptWorkflow ? "Script Builder" : topbarPageTitle;
  const taskHelpHash = view.page === "tests" && scenarioScriptWorkflow ? "#/docs?guide=user-guide&article=authoring" : contextualHelpHash(view, view.page === "scripts" ? scriptsTopbarState?.subView : undefined);
  const showTopbarClientDownloads = shouldShowTopbarClientDownloads(view);
  const mainContentClassName = `main-content topbar-title-mode page-${view.page}`;
  // Application/release ids are appended to the route by canonicalization
  // after their catalogs load — up to two extra hash writes per scope switch.
  // Remounting the page subtree (and stealing focus) for those appends would
  // wipe state the operator is already typing into, so the boundary identity
  // excludes them: page changes, project switches, and asset navigation still
  // remount; child-scope appends do not.
  const routeBoundaryKey = (retainedCommandCenter || returningCommandCenter) && detailsOrigin
    ? detailsOrigin.routeKey
    : viewToLocationHash({
    ...view,
    // Scenario subviews share one editor instance. Re-keying on its own view
    // toggle loses the selected saved scenario and the save confirmation.
    ...(view.page === "tests" ? { scenarioView: undefined } : {}),
    // Access subtabs share directory selections, filters, drafts, and issued keys.
    ...(view.page === "access" ? { accessView: undefined } : {}),
    // Report sections/lenses belong to the same run. Keep focus and population
    // selection while their controlled route props update inside that page.
    ...(view.page === "report" ? { section: undefined, lens: undefined, compare: undefined } : {}),
    // Workspace tabs of a run switch in place; re-keying remounted the run page
    // behind a loading fallback and moved keyboard focus off the tab list.
    ...(view.page === "test" ? { tab: undefined } : {}),
    ...(view.page === "activity" ? { activityQuery: undefined } : {}),
    // Command Center analysis views switch in place.
    ...(view.page === "command-center" ? { ccView: undefined } : {}),
    applicationId: undefined,
    releaseId: undefined,
  });
  // A crashed page no longer heals on application/release switches (they do
  // not re-key the boundary), so its Retry must be able to rebuild the
  // subtree itself instead of falling back to a full page reload.
  const [routeBoundaryRetryNonce, setRouteBoundaryRetryNonce] = useState(0);
  const retryRouteBoundary = useCallback(
    () => setRouteBoundaryRetryNonce((nonce) => nonce + 1),
    [],
  );

  useEffect(() => {
    if (typeof document === "undefined") return;
    document.title = `${effectiveTopbarTitle} | APEX Load`;
  }, [effectiveTopbarTitle]);

  useEffect(() => {
    const returnTarget = returningCommandCenter
      ? mainContentRef.current?.querySelector<HTMLElement>("[data-command-details]")
      : null;
    const monitorFocus = takeMonitoringTabFocus();
    if (monitorFocus) {
      // Topbar contributions settle after the route panel mounts.
      const frame = requestAnimationFrame(() => {
        const tab = document.querySelector<HTMLElement>(`[data-monitor-category="${monitorFocus}"]`);
        (tab || mainContentRef.current)?.focus({ preventScroll: true });
      });
      return () => cancelAnimationFrame(frame);
    }
    (returnTarget || mainContentRef.current)?.focus({ preventScroll: true });
  }, [routeBoundaryKey, view.page, returningCommandCenter]);


  // The recorder control panel renders chrome-less so it works as a compact
  // companion popup window next to the recording browser.
  if (
    view.page === "recorder-panel" &&
    view.sessionId &&
    !routeAssetScopeBlocked
  ) {
    return (
      <ScopedAppContextProviders
        projectValue={scopedProjectContextValue}
        applicationValue={scopedApplicationContextValue}
      >
        <Suspense fallback={<LoadingState what="page" />}>
          <BrowserRecorderControlPanel sessionId={view.sessionId} folderId={view.folderId} standalone />
        </Suspense>
      </ScopedAppContextProviders>
    );
  }

  return (
    <ScopedAppContextProviders
      projectValue={scopedProjectContextValue}
      applicationValue={scopedApplicationContextValue}
    >
      <div className="app-layout">
        <Sidebar
          active={activePage}
          onNavigate={handleNavigate}
          connectionState={connectionState}
          authStatus={authStatus}
          projects={domainProjects}
          activeProject={activeProject}
          onProjectChange={handleSidebarProjectChange}
          onCreateProject={createAndSelectProject}
          domains={domains}
          activeDomain={activeDomain}
          applicationRepository={applicationRepository}
          onDomainChange={handleSidebarDomainChange}
          applications={applications}
          releases={releases}
          activeApplication={activeApplication}
          activeRelease={activeRelease}
          onApplicationChange={handleSidebarApplicationChange}
          onReleaseChange={handleSidebarReleaseChange}
          onCreateApplication={createAndSelectApplication}
          onCreateRelease={createAndSelectRelease}
          canCreateProjects={canManageRepositoryProjects}
          canCreateReleases={canCreateReleases}
          showAccess={canUseAccessWorkspace}
          onDevPreviewLogout={onDevPreviewLogout}
        />
        <main
          ref={mainContentRef}
          className={mainContentClassName}
          tabIndex={-1}
          aria-labelledby="app-page-title"
        >
          <ActivityWarning />
          <span
            className="app-route-announcement"
            role="status"
            aria-live="polite"
            aria-atomic="true"
          >
            {effectiveTopbarTitle}
          </span>
          <PageHeader title={effectiveTopbarTitle} contextual={Boolean(activeTopbarContribution)}>

            {topbarMeta.length > 0 && (
              <div className="topbar-meta-strip" aria-label="Current context">
                {topbarMeta.map(renderTopbarMetaChip)}
              </div>
            )}
            {activeTopbarContribution?.controls && (
              <div className="topbar-contribution-controls">
                {activeTopbarContribution.controls}
              </div>
            )}
            {compactTopbarRun && (
              <nav className="topbar-active-runs compact" aria-label="Active test runs">
                {orderedTopbarRuns.length === 1 ? (
                  <a
                    className={`topbar-run-chip compact ${topbarRunTone(compactTopbarRun.status)}`}
                    aria-current={currentRouteTestId === compactTopbarRun.id ? "page" : undefined}
                    href={viewToLocationHash(
                      mergeAppViewWithWorkspace(
                        { page: "command-center", testId: compactTopbarRun.id },
                        currentWorkspaceScope,
                      ),
                    )}
                    title={`Open ${compactTopbarRun.name} (${topbarRunStatusLabel(compactTopbarRun.status)})`}
                    onClick={(event) => {
                      event.preventDefault();
                      navigateToCommandCenter(compactTopbarRun.id);
                    }}
                  >
                    <span className="topbar-run-dot" aria-hidden="true" />
                    <span className="topbar-run-name">{compactTopbarRun.name}</span>
                    <span className="sr-only">{topbarRunStatusLabel(compactTopbarRun.status)}</span>
                  </a>
                ) : (
                  <a
                    className="topbar-run-chip compact"
                    href={viewToLocationHash(
                      mergeAppViewWithWorkspace(
                        { page: "command-center" },
                        currentWorkspaceScope,
                      ),
                    )}
                    title="Open Command Center"
                    onClick={(event) => {
                      event.preventDefault();
                      navigateToCommandCenter();
                    }}
                  >
                    <span className="topbar-run-dot" aria-hidden="true" />
                    <span className="topbar-run-name">{orderedTopbarRuns.length} active runs</span>
                  </a>
                )}
              </nav>
            )}
            {showTopbarRunChips && (
              <nav className="topbar-active-runs" aria-label="Active test runs">
                {visibleTopbarRuns.map((run) => {
                  const statusLabel = topbarRunStatusLabel(run.status);
                  const tone = topbarRunTone(run.status);
                  return (
                    <a
                      key={run.id}
                      className={`topbar-run-chip ${tone}`}
                      aria-current={currentRouteTestId === run.id ? "page" : undefined}
                      href={viewToLocationHash(
                        mergeAppViewWithWorkspace(
                          { page: "command-center", testId: run.id },
                          currentWorkspaceScope,
                        ),
                      )}
                      title={`Open ${run.name} (${statusLabel})`}
                      onClick={(event) => {
                        event.preventDefault();
                        navigateToCommandCenter(run.id);
                      }}
                    >
                      <span className="topbar-run-dot" aria-hidden="true" />
                      <span className="topbar-run-name">{run.name}</span>
                      <span className="topbar-run-status">{statusLabel}</span>
                    </a>
                  );
                })}
                {hiddenTopbarRunCount > 0 && (
                  <a
                    className="topbar-run-chip topbar-run-more"
                    href={viewToLocationHash(
                      mergeAppViewWithWorkspace(
                        { page: "command-center" },
                        currentWorkspaceScope,
                      ),
                    )}
                    onClick={(event) => {
                      event.preventDefault();
                      navigateToCommandCenter();
                    }}
                  >
                    +{hiddenTopbarRunCount} run
                    {hiddenTopbarRunCount === 1 ? "" : "s"}
                  </a>
                )}
              </nav>
            )}
            <div className="main-topbar-actions">
              {taskHelpHash && <a className="btn btn-sm btn-ghost" href={taskHelpHash}>Help for this page</a>}
              {topbarSecondaryActions.length > 0 && (
                <div
                  className="topbar-action-group secondary"
                  aria-label="Contextual secondary actions"
                >
                  {topbarSecondaryActions.map(renderTopbarAction)}
                </div>
              )}
              {topbarPrimaryActions.length > 0 && (
                <div
                  className="topbar-action-group primary"
                  aria-label="Contextual primary actions"
                >
                  {topbarPrimaryActions.map(renderTopbarAction)}
                </div>
              )}
              {showDesktopStatus && (
                <DesktopStatusMenu
                  status={desktopStatus}
                  onOpenBrowser={desktopBridge?.OpenInBrowser ? () => void handleOpenDesktopBrowser() : undefined}
                  onOpenLogs={desktopBridge?.OpenLogsFolder ? () => void desktopBridge.OpenLogsFolder?.() : undefined}
                  onOpenAppData={desktopBridge?.OpenAppDataFolder ? () => void desktopBridge.OpenAppDataFolder?.() : undefined}
                />
              )}
              {showTopbarClientDownloads && <ClientDownloadsMenu />}
              <DeploymentBanner />
              <ThemeToggle theme={theme} onThemeChange={setTheme} />

            </div>
          </PageHeader>
          {activeTopbarContribution?.feedback && (
            <div className="main-topbar-feedback">
              {activeTopbarContribution.feedback}
            </div>
          )}
          {accessRedirectNotice && view.page === "dashboard" && (
            <div className="main-topbar-feedback" role="status">
              <span>Access is for administrators. Your role in this workspace cannot open it, so the Dashboard opened instead.</span>
              <button className="btn btn-sm btn-ghost" type="button" onClick={() => setAccessRedirectNotice(false)}>Dismiss</button>
            </div>
          )}
          {desktopImportError && (
            <div className="main-topbar-feedback" role="alert">
              {desktopImportError}
            </div>
          )}
          {desktopBrowserLaunchError && (
            <div className="main-topbar-feedback" role="alert">
              {desktopBrowserLaunchError}
            </div>
          )}
          {(projectsError || domainsError) && (
            <div className="main-topbar-feedback" role="alert">
              <span>{[projectsError, domainsError].filter(Boolean).join(" ")}</span>
              <button
                className="btn btn-sm btn-secondary"
                type="button"
                onClick={() => {
                  void Promise.allSettled([refreshProjects(), refreshDomains()]);
                }}
              >
                Retry workspace catalogs
              </button>
            </div>
          )}
          <LazyLoadBoundary
            key={`${routeBoundaryKey}#${routeBoundaryRetryNonce}`}
            title="This dashboard page could not be loaded."
            onRetry={retryRouteBoundary}
          >
            <Suspense fallback={<LoadingState what="page" className="page" />}>
              {routeAssetScopeBlocked && (
                <div
                  className={routeAssetScopeError ? "error-message" : "page-loading"}
                  role={routeAssetScopeError ? "alert" : "status"}
                >
                  {routeAssetScopeError ||
                    (unscopedRouteAsset
                      ? "Resolving the asset's project workspace…"
                      : scopedRouteScopePending
                        ? "Verifying the asset's project workspace…"
                      : genericRouteWorkspacePending
                        ? "Restoring the application and release workspace…"
                      : routeTestScopePending
                        ? "Loading the run's application and release workspace…"
                        : "Loading project workspace…")}
                  {routeProjectScopeError && !projectsError && routeAssetScopeError === routeProjectScopeError && (
                    <div className="route-scope-recovery">
                      <button className="btn btn-sm btn-secondary" type="button" onClick={requestProjectPicker}>
                        Choose project
                      </button>
                      {activeProject && activeProject.id !== requestedRouteProjectID && (
                        <button
                          className="btn btn-sm btn-ghost"
                          type="button"
                          onClick={() => setGuardedView(resetProjectScopedAssetView(view, activeProject.id))}
                        >
                          Open in {activeProject.name}
                        </button>
                      )}
                    </div>
                  )}
                  {routeTestScopeError && routeAssetScopeError === routeTestScopeError && (
                    <div className="route-scope-recovery">
                      <button
                        className="btn btn-sm btn-secondary"
                        type="button"
                        onClick={() => setRouteTestScopeAttempt((attempt) => attempt + 1)}
                      >
                        Retry
                      </button>
                      <button
                        className="btn btn-sm btn-ghost"
                        type="button"
                        onClick={() => setGuardedView(mergeAppViewWithWorkspace({ page: "command-center" }, currentWorkspaceScope))}
                      >
                        Open Command Center
                      </button>
                      <button
                        className="btn btn-sm btn-ghost"
                        type="button"
                        onClick={() => setGuardedView(mergeAppViewWithWorkspace({ page: "results" }, currentWorkspaceScope))}
                      >
                        Open Results
                      </button>
                    </div>
                  )}
                  {routeAssetOwnerScopeError && recoverableRouteAsset && (
                    <div>
                      <button
                        className="btn btn-sm btn-secondary"
                        type="button"
                        onClick={() =>
                          setGuardedView(
                            recoveryViewForUnscopedProjectAsset(
                              recoverableRouteAsset,
                            ),
                          )
                        }
                      >
                        {recoverableRouteAsset.kind === "script"
                          ? "Open Script Library"
                          : recoverableRouteAsset.kind === "scenario"
                            ? "Open Scenario Library"
                            : "Open Results"}
                      </button>
                    </div>
                  )}
                </div>
              )}
              {view.page === "dashboard" &&
                desktopSurface &&
                !routeAssetScopeBlocked && (
                <DesktopHome
                  status={desktopStatus}
                  startupError={desktopStartupError}
                  recents={recents}
                  runningTests={activeTopbarRuns}
                  onQuickAction={handleDesktopQuickAction}
                  onOpenRecent={handleOpenRecent}
                  onOpenRun={navigateToTest}
                  onTogglePin={handleTogglePin}
                  onClearRecents={handleClearRecents}
                  onReveal={handleRevealFolder}
                  onDropFiles={openNativeImportFiles}
                  onDropError={setDesktopImportError}
                />
              )}
              {view.page === "dashboard" && !routeAssetScopeBlocked && (
                <DashboardPage
                  testStatuses={testStatuses}
                  metricsHistory={metricsHistory}
                  connectionState={connectionState}
                  onSelectTest={openRun}
                  onOpenTestArchive={openCompletedRun}
                  onOpenAnalysisReport={runReportEnabled ? (id, context) => setGuardedView({page: "report", testId: id, lens: "engineer", section: "performance", ...context, ...(activeProject?.id ? {projectId: activeProject.id} : {})}) : undefined}
                  onOpenRootCause={runReportEnabled ? (id, rootCause) => setGuardedView({page: "report", testId: id, section: "diagnostics", ...(rootCause.analysis ? {analysis: rootCause.analysis} : {}), ...(rootCause.hypothesis ? {hypothesis: rootCause.hypothesis} : {focus: "root-cause"}), ...(activeProject?.id ? {projectId: activeProject.id} : {})}) : undefined}
                  onOpenCommandCenter={navigateToCommandCenter}
                  onNavigate={handleNavigate}
                  snapshotAudience={snapshotAudience}
                />
              )}
              {(view.page === "command-center" || retainedCommandCenter) && !routeAssetScopeBlocked && (
                <div hidden={view.page !== "command-center"}>
                <CommandCenterPrototype
                  routeView={view.page === "command-center" ? view.ccView : undefined}
                  onRouteViewChange={handleCommandCenterViewChange}
                  canManageTests={effectiveAccessLoaded && !effectiveAccessError && isEffectiveOperationAllowed(effectiveAccessResources, "manage_tests", false)}
                  // Only when access is known: the server still enforces every control.
                  liveStreamState={connectionState}
                  runControlsReadOnlyReason={effectiveAccessLoaded && !effectiveAccessError && !isEffectiveOperationAllowed(effectiveAccessResources, "manage_tests", false) ? "Your role in this project can watch this run but not control it. A project editor or admin can pause, stop or adjust it." : undefined}
                  runViewMemory={commandRunViewMemory.current}
                  testId={commandCenterTestId}
                  active={view.page === "command-center"}
                  history={commandCenterTestId ? metricsHistory[commandCenterTestId] || [] : []}
                  statusOverride={
                    commandCenterTestId ? testStatuses[commandCenterTestId] : undefined
                  }
                  applicationId={activeApplication?.id}
                  projectId={activeProject?.id}
                  releaseId={activeRelease?.id}
                  scopeLabel={`${activeProject?.name || "Choose a project"} / ${activeApplication?.name || "All applications"} / ${activeRelease?.name || "All releases"}`}
                  onSelectRun={navigateToCommandCenter}
                  onOpenOverview={() => navigateToCommandCenter()}
                  overviewActions={{
                    onNewTest: () => handleNavigate("new-test"),
                    newTestDisabledReason: !effectiveAccessLoaded ? "Checking permissions…" : effectiveAccessError ? "Permissions could not be verified. Open New Test from the sidebar to retry." : !isEffectiveOperationAllowed(effectiveAccessResources, "manage_tests", false) ? "Your current role cannot create test runs." : undefined,
                    onBrowseScenarios: () => handleNavigate("tests"),
                    releases,
                    onSelectRelease: handleSidebarReleaseChange,
                    onOpenRepository: () => handleNavigate("repository"),
                  }}
                  thresholdAlerts={thresholdAlerts}
                  onAcknowledgeThresholdAlert={acknowledgeThresholdAlertByKey}
                  onOpenExecution={id => {
                    setDetailsOrigin({ testId: id, projectId: activeProject?.id, routeKey: routeBoundaryKey });
                    navigateToTest(id, "overview");
                  }}
                  onOpenArchive={openCompletedRun}
                  onOpenRootCause={runReportEnabled ? id => setGuardedView({page: "report", testId: id, section: "diagnostics", focus: "root-cause", ...(activeProject?.id ? {projectId: activeProject.id} : {})}) : undefined}
                  onOpenResults={() => handleNavigate("results")}
                />
                </div>
              )}
              {view.page === "tests" && !routeAssetScopeBlocked && (
                <TestHub
                  onScriptWorkflowChange={setScenarioScriptWorkflow}
                  featurePolicySignal={featurePolicySignal}
                  applicationRepository={applicationRepository}
                  initialView={view.scenarioView}
                  testStatuses={testStatuses}
                  onSelectTest={openRun}
                  onOpenCommandCenter={navigateToCommandCenter}
                  onCreateTestFromScenario={navigateToNewTestForScenario}
                  onNavigate={handleNavigate}
                  onScenarioViewChange={handleScenarioViewChange}
                />
              )}
              {view.page === "new-test" && !routeAssetScopeBlocked && (
                <NewTestPage
                  activeApplication={activeApplication}
                  applicationRepository={applicationRepository}
                  activeRelease={activeRelease}
                  releases={releases}
                  initialScenarioId={
                    "scenarioId" in view ? view.scenarioId : undefined
                  }
                  initialPrefillStartLocal={
                    "prefillStartLocal" in view
                      ? view.prefillStartLocal
                      : undefined
                  }
                  onSelectTest={openRun}
                  onOpenCommandCenter={navigateToCommandCenter}
                  onCreateRelease={createAndSelectRelease}
                  onOpenScenarioBuilder={navigateToScenarioBuilder}
                />
              )}
              {view.page === "test" && !routeAssetScopeBlocked && (
                <TestExecution
                  canManageTests={effectiveAccessLoaded && !effectiveAccessError && isEffectiveOperationAllowed(effectiveAccessResources, "manage_tests", false)}
                  testId={view.testId}
                  history={metricsHistory[view.testId] || []}
                  initialWorkspaceTabId={view.tab}
                  onBack={navigateBackFromTest}
                  onEditScript={navigateToScriptEdit}
                  onWorkspaceTabChange={handleTestWorkspaceTabChange}
                  onSelectTest={openRun}
                  onOpenCommandCenter={navigateToCommandCenter}
                />
              )}
              {view.page === "report" && !routeAssetScopeBlocked && (
                <RunReportPage
                  testId={view.testId}
                  canPublishConfluence={effectiveAccessLoaded && !effectiveAccessError && isEffectiveOperationAllowed(effectiveAccessResources, "manage_tests", false)}
                  canManageProject={canManageProjects}
                  initialSection={view.section}
                  initialFocus={view.focus}
                  initialLens={view.lens}
                  initialCompare={view.compare}
                  initialComparisonId={view.comparisonId}
                  initialTransaction={view.transaction}
                  initialPopulation={view.population}
                  initialRootCause={{ analysis: view.analysis, rev: view.rev, hypothesis: view.hypothesis, window: view.window }}
                  statusOverride={testStatuses[view.testId]}
                  onOpenCommandCenter={navigateToCommandCenter}
                  onNavigateReport={(next) =>
                    setGuardedView({
                      page: "report",
                      testId: view.testId,
                      ...(view.comparisonId ? {comparisonId: view.comparisonId} : {}),
                      ...(view.transaction ? {transaction: view.transaction} : {}),
                      ...(view.population ? {population: view.population} : {}),
                      ...(view.analysis ? {analysis: view.analysis} : {}),
                      ...(view.rev ? {rev: view.rev} : {}),
                      ...(view.hypothesis ? {hypothesis: view.hypothesis} : {}),
                      ...(view.window ? {window: view.window} : {}),
                      ...(view.projectId ? { projectId: view.projectId } : {}),
                      ...(view.applicationId ? { applicationId: view.applicationId } : {}),
                      ...(view.releaseId ? { releaseId: view.releaseId } : {}),
                      ...next,
                    })
                  }
                />
              )}
              {view.page === "scripts" && !routeAssetScopeBlocked && (
                <ScriptsPage
                  featurePolicySignal={featurePolicySignal}
                  key={`scripts-${scriptsNavigationVersion}-${scriptMode || "list"}-${scriptEditId || ""}-${scriptImport ? `${scriptImport.launcher}:${scriptImport.mode || ""}` : ""}`}
                  applicationRepository={applicationRepository}
                  initialMode={scriptMode === "import" ? undefined : scriptMode}
                  initialImport={scriptImport}
                  initialEditId={scriptEditId}
                  nativeImportRequest={nativeImportRequest}
                  onNativeImportRequestHandled={() =>
                    setNativeImportRequest(null)
                  }
                  onSelectScenario={openRun}
                  onNavigate={handleNavigate}
                  onAuthoringStateChange={handleScriptsAuthoringStateChange}
                />
              )}
              {view.page === "activity" && !routeAssetScopeBlocked && <ActivityPage key={activeProject?.id} projectId={activeProject?.id} projectName={activeProject?.name} query={view.activityQuery} onQueryChange={handleActivityQueryChange} />}
              {view.page === "results" && !routeAssetScopeBlocked && (
                <ResultsPage
                  testStatuses={testStatuses}
                  onSelectTest={openRun}
                  onOpenCommandCenter={navigateToCommandCenter}
                  onOpenTestArchive={navigateToTestArchive}
                  onOpenReport={runReportEnabled ? navigateToRunReport : undefined}
                  onNavigate={handleNavigate}
                  activeApplication={activeApplication}
                  activeRelease={activeRelease}
                  activeProjectName={activeProject?.name}
                />
              )}
              {!routeAssetScopeBlocked &&
                (view.page === "monitoring" ||
                  view.page === "fleet" ||
                  view.page === "schedules") && (
                <MonitoringPage
                  canManageTests={effectiveAccessLoaded && !effectiveAccessError && isEffectiveOperationAllowed(effectiveAccessResources, "manage_tests", false)}
                  connectionState={connectionState}
                  telemetryDiagnostics={telemetryDiagnostics}
                  thresholdAlerts={thresholdAlerts}
                  onAcknowledgeThresholdAlert={acknowledgeThresholdAlertByKey}
                  onSelectTest={openRun}
                  canManageSystem={canManageSystem}
                  canManageProjects={canManageProjects}
                  // An access lookup that failed leaves the observers to the
                  // controller to refuse, as before; loading asks for nothing yet.
                  globalObservers={!effectiveAccessLoaded ? "loading" : effectiveAccessError || isEffectiveOperationAllowed(effectiveAccessResources, "view_global_observers", true) ? "allowed" : "denied"}
                  onScheduleForDate={navigateToScheduleForDate}
                  activeCategoryId={monitorCategoryForView(view)}
                  onCategoryChange={navigateToMonitorCategory}
                  fleetEventSignal={fleetEventSignal}
                />
              )}
              {view.page === "docs" && !routeAssetScopeBlocked && (
                <ApiGuidePage
                  activePage={view.guidePage}
                  onNavigate={handleNavigate}
                  effectiveAccessResources={effectiveAccessResources}
                  effectiveAccessLoaded={effectiveAccessLoaded}
                  effectiveAccessError={effectiveAccessError}
                />
              )}
              {view.page === "swagger" && !routeAssetScopeBlocked && (
                <SwaggerDocsPage
                  effectiveAccessResources={effectiveAccessResources}
                  effectiveAccessLoaded={effectiveAccessLoaded}
                  effectiveAccessError={effectiveAccessError}
                />
              )}
              {view.page === "repository" && !view.setting && !routeAssetScopeBlocked && (
                <ApplicationRepositoryPage
                  domains={domains}
                  activeDomain={activeDomain}
                  activeApplication={activeApplication}
                  activeProject={activeProject}
                  activeRelease={activeRelease}
                  applicationRepository={applicationRepository}
                  categories={repositoryCategories}
                  projects={projects}
                  canManageRepository={canManageRepository}
                  canManageProjects={canManageRepositoryProjects}
                  canManageReleases={canManageRepositoryReleases}
                  onDomainChange={handleSidebarDomainChange}
                  onProjectSelect={handleSidebarProjectChange}
                  onRepositoryRefresh={refreshRepository}
                  onRepositoryCategoryCreate={createRepositoryCategory}
                  onProjectsRefresh={refreshProjects}
                  onProjectCreated={createAndSelectProject}
					onProjectDelete={deleteProject}
                  onOpenScript={navigateToScriptEdit}
                  onOpenTest={openRun}
                  onOpenCommandCenter={navigateToCommandCenter}
                  onCreateTestFromScenario={navigateToNewTestForScenario}
                />
              )}
              {view.page === "access" && !view.setting && !routeAssetScopeBlocked && !accessPermissionsInitialized && <p role="status">Loading Access permissions…</p>}
              {view.page === "access" && !view.setting && !routeAssetScopeBlocked && accessPermissionsInitialized && (
                <AccessPage
                  accessView={view.accessView}
                  onViewChange={accessView => setGuardedView({ ...view, accessView })}
                  authMode={authStatus?.mode}
                  projects={projects}
                  domains={domains}
                  applicationRepository={applicationRepository}
                  activeDomain={activeDomain}
                  activeProject={activeProject}
                  canManageUserLifecycle={canManageUserLifecycle}
                  canManageUserBindings={canManageUserBindings}
                  canViewUserActivity={canViewUserActivity}
                  canManageServiceAccounts={canManageServiceAccounts}
                  accessReviewRefreshSignal={accessReviewSignal}
                />
              )}
              {view.page === "repository" && view.setting === "live-tables" && !routeAssetScopeBlocked && (
                <ProjectLiveTablesPage key={activeProject?.id || "unscoped"} projectId={activeProject?.id} projectName={activeProject?.name} canCreate={effectiveAccessLoaded && !effectiveAccessError && isEffectiveOperationAllowed(effectiveAccessResources, "manage_data_pools", false)} />
              )}
              {(view.page === "settings" || ((view.page === "access" || view.page === "repository") && view.setting && view.setting !== "live-tables")) && !routeAssetScopeBlocked && (
                (!isDevUserPreview && effectiveAccessLoaded && !effectiveAccessError &&
                  (["manage_system", "manage_domains", "manage_projects"] as const).some(operation => isEffectiveOperationAllowed(effectiveAccessResources, operation, false)) && !view.setting?.startsWith("personal-")) ? <SettingsPage
                  destinationId={view.setting}
                  focusRequest={settingsFocusRequest}
                  onFocusHandled={clearSettingsFocusRequest}
                  onDestinationChange={(setting, control) => setGuardedView({ page: settingsOwner(setting), setting, returnTo: "returnTo" in view ? view.returnTo : undefined }, () => setSettingsFocusRequest(control ? { destination: setting, control } : null))}
                  theme={theme}
                  onThemeChange={setTheme}
                  authStatus={authStatus}
                  onNavigate={handleNavigate}
                  domains={domains}
                  activeDomain={activeDomain}
                  applicationRepository={applicationRepository}
                  onDomainChange={handleSidebarDomainChange}
                  onDomainsRefresh={refreshDomains}
                  onProjectUpdate={updateOuterProject}
                /> : view.page === "repository" && view.setting === "test-data"
                  ? (isDevUserPreview || effectiveAccessLoaded || effectiveAccessError
                    ? <SharedTestDataReadOnly projectId={activeProject?.id} projectName={activeProject?.name} />
                    // Until access is known, do not tell an admin they lack the role.
                    : <p className="page" role="status">Checking access…</p>)
                  : <UserSettingsPage destinationId={view.setting} onDestinationChange={setting => setGuardedView({ page: "settings", setting })} theme={theme} onThemeChange={setTheme} authStatus={authStatus} />
              )}
            </Suspense>
          </LazyLoadBoundary>
        </main>
      </div>
    </ScopedAppContextProviders>
  );
}

export default function App() {
  useEffect(() => {
    if (typeof window === "undefined") return;
    if (isDesktopSurface()) {
      document.body.classList.add("is-desktop");
      if (isMacDesktop()) {
        document.body.classList.add("is-mac");
      }
    }
  }, []);

  return (
    <DevViewProvider>
      <AppContent />
    </DevViewProvider>
  );
}

function AppContent() {
  const authState = useAuthStatus();
  const { setMode: setDevViewMode } = useDevView();
  const [devPersonaSignedOut, setDevPersonaSignedOut] = useState(
    () => readStoredString("apexload.devPersonaSignedOut") === "true",
  );
  const [desktopHandlerCoordinator] = useState(
    () =>
      new DesktopShellHandlerCoordinator(announceDesktopOpenActivation),
  );
  const [aboutOpen, setAboutOpen] = useState(false);
  useDashboardZoomShortcuts();
  useDesktopWindowTitle();

  // Own the renderer handshake above AuthGate. In native-auth configurations
  // AppShell is intentionally not rendered, but About, Zoom, external-link
  // routing, and "Open in System Browser" must still remain functional.
  useDesktopShellEvents(
    {
      onMenuAbout: () => setAboutOpen(true),
      onMenuOpenBrowser: desktopHandlerCoordinator.dispatchMenuOpenBrowser,
      onMenuAction: desktopHandlerCoordinator.dispatchMenuAction,
      onOpenFile: desktopHandlerCoordinator.dispatchOpenFile,
      onOpenURL: desktopHandlerCoordinator.dispatchOpenURL,
      onOpenRecent: desktopHandlerCoordinator.dispatchOpenRecent,
      onRecentsChanged: desktopHandlerCoordinator.dispatchRecentsChanged,
    },
    authState.status !== "loading",
  );

  return (
    <>
      <AuthGate
        state={authState}
        onDesktopHandlersChange={desktopHandlerCoordinator.setGatedHandlers}
        onAuthenticationChanged={() => window.location.reload()}
        devPersonaSignedOut={devPersonaSignedOut}
        onDevPersonaLogin={(mode) => {
          setDevViewMode(mode);
          removeStoredItem("apexload.devPersonaSignedOut");
          setDevPersonaSignedOut(false);
        }}
      >
        <ProjectProvider>
          <ApplicationProvider>
            <KeyboardShortcutProvider>
              <TopbarContributionProvider>
                <NavigationGuardProvider>
                  <TablePaginationManager />
                  <AppShell
                    authStatus={
                      authState.status === "ready" ? authState.auth : undefined
                    }
                    desktopHandlerCoordinator={desktopHandlerCoordinator}
                    onDevPreviewLogout={() => {
                      writeStoredString("apexload.devPersonaSignedOut", "true");
                      setDevPersonaSignedOut(true);
                    }}
                  />
                </NavigationGuardProvider>
              </TopbarContributionProvider>
            </KeyboardShortcutProvider>
          </ApplicationProvider>
        </ProjectProvider>
      </AuthGate>
      <AboutDialog open={aboutOpen} onClose={() => setAboutOpen(false)} />
    </>
  );
}
