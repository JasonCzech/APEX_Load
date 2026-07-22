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
import { useDashboardTheme } from "./hooks/useDashboardTheme";
import { useDashboardZoomShortcuts } from "./hooks/useDashboardZoomShortcuts";
import { useAsyncTask } from "./hooks/useAsyncTask";
import { useKeyboardShortcuts } from "./hooks/KeyboardShortcutProvider";
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
import type { DashboardSnapshotAudience } from "./components/DashboardPage";
import TablePaginationManager from "./components/TablePaginationManager";
import {
  type AppView,
  handleGuardedRouteChange,
  normalizeReportViewForFeatureFlag,
  parseAppViewFromHash,
  parseDesktopDeepLink,
  readAppHistoryIndex,
  type TestWorkspaceTabID,
  viewToLocationHash,
  withAppHistoryIndex,
} from "./appRouting";
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
import ThemeToggle from "./components/ThemeToggle";
import DeploymentBanner from "./components/DeploymentBanner";
import ClientDownloadsMenu from "./components/ClientDownloadsMenu";
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
  isCurrentRepositoryRequest,
  isProjectScopedMessageCurrent,
  projectForActiveDomain,
  telemetryDiagnosticsForProject,
} from "./appProjectScope";
import type { ScriptsAuthoringSubView } from "./components/useScriptsAuthoringState";
import type { MonitorCategoryId } from "./components/MonitoringPage";
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
const ResultsPage = lazy(() => import("./components/ResultsPage"));
const NewTestPage = lazy(() => import("./components/NewTestPage"));
const MonitoringPage = lazy(() => import("./components/MonitoringPage"));
const ApiGuidePage = lazy(() => import("./components/ApiGuidePage"));
const SwaggerDocsPage = lazy(() => import("./components/SwaggerDocsPage"));
const RepositoryGuidePage = lazy(
  () => import("./components/RepositoryGuidePage"),
);
const ApplicationRepositoryPage = lazy(
  () => import("./components/ApplicationRepositoryPage"),
);
const AccessPage = lazy(() => import("./components/AccessPage"));
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

function formatDesktopRemoteHealth(status?: DesktopStatusResponse) {
  const count = status?.remote_libraries?.count || 0;
  if (count === 0) return "0 hosts";
  const statuses = status?.remote_libraries?.statuses || {};
  const healthy = statuses.healthy || 0;
  return `${healthy}/${count} hosts`;
}

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
    <button
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
    </button>
  );
}

function renderTopbarMetaChip(chip: TopbarMetaChip) {
  if (chip.hidden) return null;
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

function dashboardSnapshotAudience(
  authStatus: import("./types").DashboardAuthStatus | undefined,
  devViewMode: string,
  activeProjectId?: string,
): DashboardSnapshotAudience {
  if (authStatus?.mode === "dev") {
    return devViewMode === "user" ? "user" : "admin";
  }
  if (!authStatus || !authStatus.enabled) {
    return "admin";
  }

  const globalRoles = authStatus.user?.global_roles || [];
  const projectRoles = activeProjectId
    ? authStatus.user?.project_roles?.[activeProjectId] || []
    : [];
  return [...globalRoles, ...projectRoles].some(
    (role) => role === "system_admin" || role === "project_admin",
  )
    ? "admin"
    : "user";
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
  if (view.page === "monitoring" && view.category === "telemetry")
    return "telemetry";
  return "overview";
}

function viewForMonitorCategory(categoryId: MonitorCategoryId): AppView {
  if (categoryId === "fleet") return { page: "fleet" };
  if (categoryId === "schedules") return { page: "schedules" };
  if (categoryId === "telemetry")
    return { page: "monitoring", category: "telemetry" };
  return { page: "monitoring" };
}

function AppShell({
  authStatus,
  desktopHandlerCoordinator,
}: {
  authStatus?: import("./types").DashboardAuthStatus;
  desktopHandlerCoordinator: DesktopShellHandlerCoordinator;
}) {
  const [view, setView] = useState<AppView>(() =>
    typeof window === "undefined"
      ? { page: "dashboard" }
      : normalizeReportViewForFeatureFlag(
          parseAppViewFromHash(window.location.hash),
          isRunReportEnabled(),
        ),
  );
  const viewRef = useRef(view);
  const routeSyncInitialized = useRef(false);
  const appHistoryIndex = useRef(
    typeof window === "undefined"
      ? 0
      : (readAppHistoryIndex(window.history.state) ?? 0),
  );
  const popstateHashToIgnore = useRef<string | null>(null);
  const guardedHistoryTraversal = useRef<{
    phase: "restoring" | "proceeding";
    pending: PendingGuardedHistoryTraversal;
  } | null>(null);
  const mainContentRef = useRef<HTMLElement | null>(null);
  const metricsHistoryRef = useRef<Record<string, MetricsHistoryPoint[]>>({});
  const [, setMetricsHistoryVersion] = useState(0);
  const [testStatuses, setTestStatuses] = useState<Record<string, string>>({});
  const testStatusesRef = useRef(testStatuses);
  testStatusesRef.current = testStatuses;
  const protectedMetricsTestIDsRef = useRef<ReadonlySet<string>>(new Set());
  const [telemetryDiagnosticsByProject, setTelemetryDiagnosticsByProject] =
    useState<Record<string, TelemetryDiagnosticEntry[]>>({});
  const { theme, setTheme } = useDashboardTheme();
  const { mode: devViewMode } = useDevView();
  const { getAuthEffectiveAccess } = useSystemApi();
  const libraryApi = useLibraryApi();
  const repositoryApi = useRepositoryApi();
  const isDevUserPreview = authStatus?.mode === "dev" && devViewMode === "user";
  const scriptMode =
    view.page === "scripts" && "mode" in view ? view.mode : undefined;
  const scriptEditId =
    view.page === "scripts" && "scriptId" in view ? view.scriptId : undefined;
  const [scriptsNavigationVersion, setScriptsNavigationVersion] = useState(0);
  const [scriptsTopbarState, setScriptsTopbarState] = useState<{
    subView: ScriptsAuthoringSubView;
    editingId: string | null;
  } | null>(null);
  const [effectiveAccessResources, setEffectiveAccessResources] = useState<
    import("./types").DashboardAuthEffectiveAccessResource[]
  >([]);
  const [effectiveAccessLoaded, setEffectiveAccessLoaded] = useState(false);
  const [effectiveAccessError, setEffectiveAccessError] = useState(false);
  const [domains, setDomains] = useState<import("./types").Domain[]>([]);
  const [activeDomain, setActiveDomainState] = useState<
    import("./types").Domain | null
  >(null);
  const [loadedApplicationRepository, setApplicationRepository] = useState<
    import("./types").ApplicationRepository | null
  >(null);
  const [loadedRepositoryCategories, setRepositoryCategories] = useState<
    import("./types").RepositoryCategory[]
  >([]);
  const repositoryRequestGenerationRef = useRef(0);
  const activeDomainIDRef = useRef(activeDomain?.id || "");
  activeDomainIDRef.current = activeDomain?.id || "";
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
  const testApi = useTestApi();
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

      if (msg.type === "fleet_event") {
        setFleetEventSignal((n) => n + 1);
        return;
      }

      if (
        msg.type === "metrics_update" &&
        msg.data &&
        msg.test_id &&
        msg.timestamp
      ) {
        const testId = msg.test_id;
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
    activeProject: selectedProject,
    setActiveProject: setOuterActiveProject,
    createAndSelectProject: createOuterProject,
    refreshProjects,
  } = outerProjectContext;
  const domainProjects = useMemo(() => {
    const domainID = activeDomain?.id || "default";
    return projects.filter(
      (project) => (project.domain_id || "default") === domainID,
    );
  }, [activeDomain?.id, projects]);
  const activeProject = projectForActiveDomain(activeDomain, selectedProject);
  const setActiveProject = useCallback<ProjectContextValue["setActiveProject"]>(
    (project) => {
      if (projectForActiveDomain(activeDomain, project))
        setOuterActiveProject(project);
    },
    [activeDomain, setOuterActiveProject],
  );
  const createAndSelectProject = useCallback<
    ProjectContextValue["createAndSelectProject"]
  >(
    (project) => {
      const domainID = activeDomain?.id;
      const payload =
        typeof project === "string"
          ? { name: project, domain_id: domainID }
          : { ...project, domain_id: project.domain_id || domainID };
      return createOuterProject(payload);
    },
    [activeDomain?.id, createOuterProject],
  );
  const telemetryDiagnostics = telemetryDiagnosticsForProject(
    telemetryDiagnosticsByProject,
    activeProject?.id,
  );
  const handleProjectWSMessage = useCallback(
    (message: WSMessage) => handleWSMessage(message, activeProject?.id),
    [activeProject?.id, handleWSMessage],
  );
  const { connectionState } = useWebSocket({
    onMessage: handleProjectWSMessage,
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
  const activeRelease =
    activeApplication &&
    outerApplicationContext.activeRelease &&
    (!outerApplicationContext.activeRelease.application_id ||
      outerApplicationContext.activeRelease.application_id ===
        activeApplication.id)
      ? outerApplicationContext.activeRelease
      : null;
  const setActiveApplication = useCallback<
    ApplicationContextValue["setActiveApplication"]
  >(
    (application) => {
      if (
        activeProject &&
        (!application.project_id || application.project_id === activeProject.id)
      ) {
        outerApplicationContext.setActiveApplication(application);
      }
    },
    [activeProject, outerApplicationContext],
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
        outerApplicationContext.setActiveRelease(release);
      }
    },
    [activeApplication, outerApplicationContext],
  );
  const refreshApplications = useCallback(
    () =>
      activeProject
        ? outerApplicationContext.refreshApplications()
        : Promise.resolve(),
    [activeProject, outerApplicationContext],
  );
  const refreshReleases = useCallback(
    (applicationID?: string) =>
      activeApplication &&
      (!applicationID || applicationID === activeApplication.id)
        ? outerApplicationContext.refreshReleases(applicationID)
        : Promise.resolve(),
    [activeApplication, outerApplicationContext],
  );
  const createAndSelectApplication = useCallback<
    ApplicationContextValue["createAndSelectApplication"]
  >(
    async (name) => {
      if (!activeProject)
        throw new Error("Select a project before creating an application.");
      return outerApplicationContext.createAndSelectApplication(name);
    },
    [activeProject, outerApplicationContext],
  );
  const createAndSelectRelease = useCallback<
    ApplicationContextValue["createAndSelectRelease"]
  >(
    (name) => {
      if (!activeApplication) return Promise.resolve(null);
      return outerApplicationContext.createAndSelectRelease(name);
    },
    [activeApplication, outerApplicationContext],
  );
  const scopedProjectContextValue = useMemo<ProjectContextValue>(
    () => ({
      projects: domainProjects,
      activeProject,
      setActiveProject,
      refreshProjects,
      createAndSelectProject,
    }),
    [
      activeProject,
      createAndSelectProject,
      domainProjects,
      refreshProjects,
      setActiveProject,
    ],
  );
  const scopedApplicationContextValue = useMemo<ApplicationContextValue>(
    () => ({
      applications,
      releases,
      activeApplication,
      activeRelease,
      setActiveApplication,
      setActiveRelease,
      refreshApplications,
      refreshReleases,
      createAndSelectApplication,
      createAndSelectRelease,
    }),
    [
      activeApplication,
      activeRelease,
      applications,
      createAndSelectApplication,
      createAndSelectRelease,
      refreshApplications,
      refreshReleases,
      releases,
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
            summary: true,
            active_only: true,
            limit: 100,
          }
        : null,
    [activeApplication?.id, activeProject?.id],
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
  const snapshotAudience = dashboardSnapshotAudience(
    authStatus,
    devViewMode,
    activeProject?.id,
  );

  const setGuardedView = useCallback(
    (next: AppView | ((current: AppView) => AppView)) => {
      runGuarded(() => {
        setView((current) => {
          const resolved = typeof next === "function" ? next(current) : next;
          return normalizeReportViewForFeatureFlag(resolved, runReportEnabled);
        });
      });
    },
    [runGuarded, runReportEnabled],
  );

  const navigateToTest = useCallback(
    (id: string, tab?: TestWorkspaceTabID) => {
      setGuardedView(
        tab ? { page: "test", testId: id, tab } : { page: "test", testId: id },
      );
      void addRecent({
        kind: "test",
        id,
        name: id,
        route: viewToLocationHash({ page: "test", testId: id }),
      }).then(reloadRecents);
    },
    [reloadRecents, setGuardedView],
  );
  const navigateToTestArchive = useCallback(
    (id: string) => navigateToTest(id, "archive"),
    [navigateToTest],
  );
  const navigateToRunReport = useCallback(
    (id: string) => {
      if (!runReportEnabled) {
        navigateToTest(id, "archive");
        return;
      }
      const next = { page: "report", testId: id } as const;
      setGuardedView(next);
      void addRecent({
        kind: "test",
        id,
        name: id,
        route: viewToLocationHash(next),
      }).then(reloadRecents);
    },
    [navigateToTest, reloadRecents, runReportEnabled, setGuardedView],
  );
  const navigateToCommandCenter = useCallback(
    (id?: string) => {
      setGuardedView(
        id
          ? { page: "command-center", testId: id }
          : { page: "command-center" },
      );
    },
    [setGuardedView],
  );
  const openCompletedRun = runReportEnabled
    ? navigateToRunReport
    : navigateToTestArchive;
  const activePage: NavPage =
    view.page === "test"
      ? "tests"
      : view.page === "report"
        ? "results"
        : view.page === "swagger" || view.page === "repository-guide"
          ? "docs"
          : view.page === "recorder-panel"
            ? "scripts"
            : view.page;

  const navigateToScriptEdit = (scriptId: string) => {
    setGuardedView({ page: "scripts", mode: "edit", scriptId });
    void addRecent({
      kind: "script",
      id: scriptId,
      name: scriptId,
      route: viewToLocationHash({ page: "scripts", mode: "edit", scriptId }),
    }).then(reloadRecents);
  };
  const navigateToScenarioBuilder = () =>
    setGuardedView({ page: "tests", scenarioView: "builder" });
  const navigateToNewTestForScenario = (scenarioId: string) => {
    setView({ page: "new-test", scenarioId });
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
    setView({ page });
  }, []);

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
            setView({ page: "scripts" });
          });
        },
      );
    },
    [runGuarded],
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
        setGuardedView(parseAppViewFromHash(item.route));
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

  const handleDesktopMenuAction = useCallback(
    (action: string) => {
      if (action === "new-test") setGuardedView({ page: "new-test" });
      else handleNavigate("scripts");
    },
    [setGuardedView, handleNavigate],
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
      if (nextView) setGuardedView(nextView);
    },
    [setGuardedView],
  );
  const handleDesktopOpenRecent = useCallback(
    (route: string) => {
      // Native File > Open Recent click → navigate to the recent's route.
      if (route) setGuardedView(parseAppViewFromHash(route));
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
    (state: { subView: ScriptsAuthoringSubView; editingId: string | null }) => {
      setScriptsTopbarState(state);
    },
    [],
  );

  const handleScenarioViewChange = useCallback(
    (scenarioView: "library" | "builder" | "plans") => {
      setView((current) => {
        if (current.page !== "tests") return current;
        const next: AppView =
          scenarioView === "builder"
            ? { page: "tests", scenarioView: "builder" }
            : scenarioView === "plans"
              ? { page: "tests", scenarioView: "plans" }
              : { page: "tests" };
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
  const navigateBackToTests = useCallback(
    () => navigateBack("tests"),
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
        onTrigger: () => undefined,
      },
      {
        id: "global.zoom-out",
        label: "Zoom dashboard out",
        keys: "Cmd/Ctrl+-",
        group: "Global",
        preventDefault: false,
        priority: 20,
        onTrigger: () => undefined,
      },
      {
        id: "global.zoom-reset",
        label: "Reset dashboard zoom",
        keys: "Cmd/Ctrl+0",
        group: "Global",
        preventDefault: false,
        priority: 20,
        onTrigger: () => undefined,
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

    if (window.location.hash !== nextHash) {
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
      (route) => setGuardedView(parseAppViewFromHash(route)),
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

  const setActiveDomain = useCallback((domain: import("./types").Domain) => {
    setActiveDomainState(domain);
    writeStoredString("apexload.activeDomainId", domain.id);
  }, []);

  const refreshDomains = useCallback(
    async (preferredId?: string) => {
      try {
        const response = await repositoryApi.listDomains();
        const next = response.domains || [];
        setDomains(next);
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
      } catch {
        const fallback: import("./types").Domain = {
          id: "default",
          name: "Default Domain",
          created_at: new Date(0).toISOString(),
          updated_at: new Date(0).toISOString(),
        };
        setDomains([fallback]);
        setActiveDomainState(fallback);
        writeStoredString("apexload.activeDomainId", fallback.id);
        return [fallback];
      }
    },
    [repositoryApi],
  );

  const refreshRepository = useCallback(async () => {
    const domainID = activeDomain?.id || "";
    const requestGeneration = ++repositoryRequestGenerationRef.current;
    if (!domainID) {
      setApplicationRepository(null);
      setRepositoryCategories([]);
      return;
    }
    try {
      const [repo, categoryResponse] = await Promise.all([
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
      setApplicationRepository(repo);
      setRepositoryCategories(categoryResponse.categories || []);
    } catch (error) {
      if (
        isCurrentRepositoryRequest(
          requestGeneration,
          domainID,
          repositoryRequestGenerationRef.current,
          activeDomainIDRef.current,
        )
      ) {
        setApplicationRepository(null);
        setRepositoryCategories([]);
      }
      throw error;
    }
  }, [activeDomain?.id, repositoryApi]);

  useEffect(() => {
    void refreshDomains();
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
      setActiveProject(domainProjects[0]);
    }
  }, [activeDomain, domainProjects, selectedProject, setActiveProject]);

  const applicationRepository =
    loadedApplicationRepository?.domain_id === activeDomain?.id
      ? loadedApplicationRepository
      : null;
  const repositoryCategories = loadedRepositoryCategories.filter(
    (category) => category.domain_id === activeDomain?.id,
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
    devViewMode,
    getAuthEffectiveAccess,
  ]);

  useEffect(() => {
    if (!isDevUserPreview) {
      return;
    }
    setView((current) => {
      if (current.page === "settings") {
        return { page: "dashboard" };
      }
      return current;
    });
  }, [isDevUserPreview]);

  useEffect(() => {
    if (
      effectiveAccessLoaded &&
      view.page === "access" &&
      !canUseAccessWorkspace
    ) {
      setView({ page: "dashboard" });
    }
  }, [canUseAccessWorkspace, effectiveAccessLoaded, view.page]);

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
      ? pageTitleForScriptSubView(scriptsTopbarState.subView)
      : pageTitleForView(view, {
          repositoryName: applicationRepository?.name,
          testName: currentTestRun?.name,
        });
  const orderedTopbarRuns =
    view.page === "test" && currentTestRun
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
  // The route is the authoritative page identity. A lazily suspended previous
  // page can keep its topbar contribution mounted briefly, so using a
  // contribution title here would announce the old page after navigation.
  const effectiveTopbarTitle = topbarPageTitle;
  const showTopbarClientDownloads = shouldShowTopbarClientDownloads(view);
  const mainContentClassName = `main-content topbar-title-mode page-${view.page}`;
  const activeRouteHash = viewToLocationHash(view);

  useEffect(() => {
    if (typeof document === "undefined") return;
    document.title = `${effectiveTopbarTitle} | APEX Load`;
  }, [effectiveTopbarTitle]);

  useEffect(() => {
    mainContentRef.current?.focus({ preventScroll: true });
  }, [activeRouteHash]);

  const topbarActionShortcuts = useMemo(
    () =>
      [...topbarSecondaryActions, ...topbarPrimaryActions]
        .filter((action) => action.shortcut)
        .map((action) => ({
          id: `topbar.${activeTopbarContribution?.id || "active"}.${action.id}`,
          label: action.label,
          keys: action.shortcut!,
          group: action.shortcutGroup || "Current Page",
          disabled: action.disabled,
          hidden: action.hidden,
          allowInEditable: action.allowShortcutInEditable,
          priority: 700,
          onTrigger: action.onClick,
        })),
    [
      activeTopbarContribution?.id,
      topbarPrimaryActions,
      topbarSecondaryActions,
    ],
  );
  useKeyboardShortcuts(topbarActionShortcuts);

  // The recorder control panel renders chrome-less so it works as a compact
  // companion popup window next to the recording browser.
  if (view.page === "recorder-panel" && view.sessionId) {
    return (
      <ScopedAppContextProviders
        projectValue={scopedProjectContextValue}
        applicationValue={scopedApplicationContextValue}
      >
        <Suspense fallback={<div className="page-loading">Loading…</div>}>
          <BrowserRecorderControlPanel sessionId={view.sessionId} standalone />
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
          onProjectChange={setActiveProject}
          onCreateProject={createAndSelectProject}
          domains={domains}
          activeDomain={activeDomain}
          onDomainChange={setActiveDomain}
          applications={applications}
          releases={releases}
          activeApplication={activeApplication}
          activeRelease={activeRelease}
          onApplicationChange={setActiveApplication}
          onReleaseChange={setActiveRelease}
          onCreateApplication={createAndSelectApplication}
          showAccess={canUseAccessWorkspace}
        />
        <main
          ref={mainContentRef}
          className={mainContentClassName}
          tabIndex={-1}
          aria-labelledby="app-page-title"
        >
          <span
            className="app-route-announcement"
            role="status"
            aria-live="polite"
            aria-atomic="true"
          >
            {effectiveTopbarTitle}
          </span>
          <div
            className={`main-topbar${activeTopbarContribution ? " contextual" : ""}`}
            aria-label="Dashboard controls"
          >
            <div className="main-topbar-page">
              <h1 id="app-page-title" className="topbar-page-title">
                {effectiveTopbarTitle}
              </h1>
            </div>
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
            {showTopbarRunChips && (
              <nav className="topbar-active-runs" aria-label="Active test runs">
                {visibleTopbarRuns.map((run) => {
                  const statusLabel = topbarRunStatusLabel(run.status);
                  const tone = topbarRunTone(run.status);
                  return (
                    <a
                      key={run.id}
                      className={`topbar-run-chip ${tone}`}
                      href={viewToLocationHash({
                        page: "command-center",
                        testId: run.id,
                      })}
                      title={`Open ${run.name} (${statusLabel})`}
                      onClick={(event) => {
                        event.preventDefault();
                        navigateToCommandCenter(run.id);
                      }}
                    >
                      <span className="topbar-run-dot" aria-hidden="true" />
                      <span className="topbar-run-name">{run.name}</span>
                    </a>
                  );
                })}
                {hiddenTopbarRunCount > 0 && (
                  <a
                    className="topbar-run-chip topbar-run-more"
                    href={viewToLocationHash({ page: "tests" })}
                    onClick={(event) => {
                      event.preventDefault();
                      setGuardedView({ page: "tests" });
                    }}
                  >
                    +{hiddenTopbarRunCount} run
                    {hiddenTopbarRunCount === 1 ? "" : "s"}
                  </a>
                )}
              </nav>
            )}
            <div className="main-topbar-actions">
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
                <div
                  className="desktop-status-strip"
                  aria-label="Desktop controller status"
                >
                  <span className="script-health-token healthy">Desktop</span>
                  <span className="desktop-status-address">
                    {desktopStatus?.browser_url || desktopStatus?.api_address}
                  </span>
                  <span className="script-health-token">
                    {formatDesktopRemoteHealth(desktopStatus || undefined)}
                  </span>
                  {desktopBridge?.OpenInBrowser && (
                    <button
                      className="btn btn-sm btn-ghost"
                      type="button"
                      onClick={handleOpenDesktopBrowser}
                    >
                      Open Browser
                    </button>
                  )}
                  {desktopBridge?.OpenLogsFolder && (
                    <button
                      className="btn btn-sm btn-ghost"
                      type="button"
                      onClick={() => {
                        void desktopBridge.OpenLogsFolder?.();
                      }}
                    >
                      Logs
                    </button>
                  )}
                  {desktopBridge?.OpenAppDataFolder && (
                    <button
                      className="btn btn-sm btn-ghost"
                      type="button"
                      onClick={() => {
                        void desktopBridge.OpenAppDataFolder?.();
                      }}
                    >
                      App Data
                    </button>
                  )}
                </div>
              )}
              {showTopbarClientDownloads && <ClientDownloadsMenu />}
              <DeploymentBanner />
              <ThemeToggle theme={theme} onThemeChange={setTheme} />
            </div>
          </div>
          {activeTopbarContribution?.feedback && (
            <div className="main-topbar-feedback">
              {activeTopbarContribution.feedback}
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
          <LazyLoadBoundary
            key={activeRouteHash}
            title="This dashboard page could not be loaded."
          >
            <Suspense fallback={<div className="page">Loading page...</div>}>
              {view.page === "dashboard" && desktopSurface && (
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
              {view.page === "dashboard" && (
                <DashboardPage
                  testStatuses={testStatuses}
                  metricsHistory={metricsHistory}
                  connectionState={connectionState}
                  onSelectTest={navigateToTest}
                  onOpenTestArchive={openCompletedRun}
                  onOpenCommandCenter={navigateToCommandCenter}
                  onNavigate={handleNavigate}
                  snapshotAudience={snapshotAudience}
                />
              )}
              {view.page === "command-center" && (
                <CommandCenterPrototype
                  testId={view.testId}
                  history={view.testId ? metricsHistory[view.testId] || [] : []}
                  statusOverride={
                    view.testId ? testStatuses[view.testId] : undefined
                  }
                  applicationId={activeApplication?.id}
                  projectId={activeProject?.id}
                  onOpenExecution={navigateToTest}
                  onOpenArchive={openCompletedRun}
                  onOpenResults={() => handleNavigate("results")}
                />
              )}
              {view.page === "tests" && (
                <TestHub
                  applicationRepository={applicationRepository}
                  initialView={view.scenarioView}
                  testStatuses={testStatuses}
                  onSelectTest={navigateToTest}
                  onOpenCommandCenter={navigateToCommandCenter}
                  onCreateTestFromScenario={navigateToNewTestForScenario}
                  onNavigate={handleNavigate}
                  onScenarioViewChange={handleScenarioViewChange}
                />
              )}
              {view.page === "new-test" && (
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
                  onSelectTest={navigateToTest}
                  onOpenCommandCenter={navigateToCommandCenter}
                  onCreateRelease={createAndSelectRelease}
                  onOpenScenarioBuilder={navigateToScenarioBuilder}
                />
              )}
              {view.page === "test" && (
                <TestExecution
                  testId={view.testId}
                  history={metricsHistory[view.testId] || []}
                  initialWorkspaceTabId={view.tab}
                  onBack={navigateBackToTests}
                  onEditScript={navigateToScriptEdit}
                  onSelectTest={navigateToTest}
                  onOpenCommandCenter={navigateToCommandCenter}
                />
              )}
              {view.page === "report" && (
                <RunReportPage
                  testId={view.testId}
                  initialLens={view.lens}
                  initialCompare={view.compare}
                  statusOverride={testStatuses[view.testId]}
                  onOpenCommandCenter={navigateToCommandCenter}
                  onOpenLegacyArchive={navigateToTestArchive}
                  onNavigateReport={(next) =>
                    setGuardedView({
                      page: "report",
                      testId: view.testId,
                      ...next,
                    })
                  }
                />
              )}
              {view.page === "scripts" && (
                <ScriptsPage
                  key={`scripts-${scriptsNavigationVersion}-${scriptMode || "list"}-${scriptEditId || ""}`}
                  applicationRepository={applicationRepository}
                  initialMode={scriptMode}
                  initialEditId={scriptEditId}
                  nativeImportRequest={nativeImportRequest}
                  onNativeImportRequestHandled={() =>
                    setNativeImportRequest(null)
                  }
                  onSelectScenario={navigateToTest}
                  onNavigate={handleNavigate}
                  onAuthoringStateChange={handleScriptsAuthoringStateChange}
                />
              )}
              {view.page === "results" && (
                <ResultsPage
                  testStatuses={testStatuses}
                  onSelectTest={navigateToTest}
                  onOpenCommandCenter={navigateToCommandCenter}
                  onOpenTestArchive={openCompletedRun}
                  onNavigate={handleNavigate}
                  activeApplication={activeApplication}
                  activeRelease={activeRelease}
                />
              )}
              {(view.page === "monitoring" ||
                view.page === "fleet" ||
                view.page === "schedules") && (
                <MonitoringPage
                  connectionState={connectionState}
                  telemetryDiagnostics={telemetryDiagnostics}
                  onSelectTest={navigateToTest}
                  canManageSystem={canManageSystem}
                  onScheduleForDate={navigateToScheduleForDate}
                  activeCategoryId={monitorCategoryForView(view)}
                  onCategoryChange={navigateToMonitorCategory}
                  fleetEventSignal={fleetEventSignal}
                />
              )}
              {view.page === "docs" && (
                <ApiGuidePage
                  activePage={view.guidePage}
                  onNavigate={handleNavigate}
                  effectiveAccessResources={effectiveAccessResources}
                  effectiveAccessLoaded={effectiveAccessLoaded}
                  effectiveAccessError={effectiveAccessError}
                />
              )}
              {view.page === "swagger" && (
                <SwaggerDocsPage
                  effectiveAccessResources={effectiveAccessResources}
                  effectiveAccessLoaded={effectiveAccessLoaded}
                  effectiveAccessError={effectiveAccessError}
                />
              )}
              {view.page === "repository-guide" && (
                <RepositoryGuidePage effectiveAccessResources={effectiveAccessResources} />
              )}
              {view.page === "repository" && (
                <ApplicationRepositoryPage
                  domains={domains}
                  activeDomain={activeDomain}
                  activeApplication={activeApplication}
                  applicationRepository={applicationRepository}
                  categories={repositoryCategories}
                  projects={projects}
                  canManageRepository={canManageRepository}
                  canManageProjects={canManageRepositoryProjects}
                  canManageReleases={canManageRepositoryReleases}
                  onDomainChange={setActiveDomain}
                  onRepositoryRefresh={refreshRepository}
                  onProjectsRefresh={refreshProjects}
                  onProjectCreated={createAndSelectProject}
                  onOpenScript={navigateToScriptEdit}
                  onOpenTest={navigateToTest}
                  onOpenCommandCenter={navigateToCommandCenter}
                  onCreateTestFromScenario={navigateToNewTestForScenario}
                />
              )}
              {view.page === "access" && (
                <AccessPage
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
              {view.page === "settings" && (
                <SettingsPage
                  theme={theme}
                  onThemeChange={setTheme}
                  authStatus={authStatus}
                  onNavigate={handleNavigate}
                  domains={domains}
                  activeDomain={activeDomain}
                  applicationRepository={applicationRepository}
                  onDomainChange={setActiveDomain}
                  onDomainsRefresh={refreshDomains}
                />
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
  const [desktopHandlerCoordinator] = useState(
    () =>
      new DesktopShellHandlerCoordinator(announceDesktopOpenActivation),
  );
  const [aboutOpen, setAboutOpen] = useState(false);
  useDashboardZoomShortcuts();

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
