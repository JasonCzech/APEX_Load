import { confirmAction } from './ux/confirmAction'
import {
  useState,
  useEffect,
  useCallback,
  useLayoutEffect,
  useMemo,
  useRef,
  type MouseEvent,
  type ReactNode,
} from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import {
  downloadBlob,
  downloadFilenameFromDisposition,
} from "../utils/fileUtils";
import { useAsyncTask } from "../hooks/useAsyncTask";
import { useApi } from "../hooks/useApi";
import {
  API_ORIGIN,
  apiFetch,
  DESKTOP_CAPABILITY_SESSION_KEY,
  hasDesktopApiCapability,
  trimTrailingSlash,
} from "../hooks/apiClient";
import {
  type BrowserRecorderAvailability,
  type BrowserRecorderSession,
  type BrowserRecorderStorageState,
} from "../hooks/api/useBrowserRecorderApi";
import { getDesktopBridge, isDesktopSurface } from "../desktop/desktopBridge";
import {
  saveApiResourceViaBridge,
  startDesktopBrowserDownload,
} from "../desktop/desktopFiles";
import { Project, type StoredScript } from "../types";
import {
  type RecorderImportResult,
  type SalesforceImportResult,
  type SecretBindingSelection,
} from "../types/scriptImports";
import { type ScriptDefinition } from "../types/scripts";
import {
  composeHttpURL,
  DEFAULT_HTTP_URL_SCHEME,
  normalizeHttpURLInput,
  type HttpURLScheme,
} from "../utils/httpUrlInput";
import { withFallbackPersistedScript } from "./importSaveUtils";
import { useSourceSecretSessionLifecycle } from "./sourceSecretSessionLifecycle";
import BrowserRecorderControlPanel from "./BrowserRecorderControlPanel";
import {
  CLIENT_DOWNLOAD_PLATFORMS,
  detectPlatformFromNavigator,
  platformLabel,
} from "./clientDownloadPlatforms";
import PageViewToggle from "./PageViewToggle";
import ParameterizationReview from "./ParameterizationReview";
import SecretBindingReview, {
  type PreparedSecretBindings,
  type SecretBindingReviewHandle,
} from "./SecretBindingReview";
import {
  credentialPreservingImportDraftRequiresSave,
  importDraftExpiryStatus,
} from "./protectedImportSession";
import {
  attachImportCorrelationSuggestions,
  createReplaySessionId,
} from "./scriptEditorUtils";
import { type ImportedSaveResult } from "./useScriptsAuthoringState";
import {
  DEFAULT_MACOS_NETWORK_SERVICE,
  buildMacOSRestoreProxyCommand,
  buildMacOSStartProxyCommand,
  buildWindowsRestoreProxyCommand,
  buildWindowsStartProxyCommand,
  recorderProxyHost,
  recorderProxyHostIsLocal,
  recorderProxyPort,
} from "./recordingProxyCommands";
import "./RecordingStudio.css";
import RecordingProfilesCard from "./RecordingProfilesCard";
import {
  RECORDING_REVIEW_STEP_LIMIT,
  recordedScriptSteps,
  sentenceCaseNote,
} from "./recordingReviewModel";
import { isNoReusableCorrelationNote } from "./importIssueWorkspaceModel";
import type { RecordingProfile } from "../types/recordingProfiles";
import { formatDateTime } from "../utils/dateTime";

interface RecordedRequest {
  seq: number;
  timestamp: string;
  method: string;
  url: string;
  scheme?: string;
  path: string;
  query_string?: string;
  host: string;
  status_code: number;
  response_time_ms: number;
  content_type: string;
  body: string;
  body_encoding?: string;
  body_size_bytes?: number;
  body_truncated?: boolean;
  response_body_encoding?: string;
  response_body_size_bytes?: number;
  response_body_truncated?: boolean;
  think_time_ms: number;
  is_static: boolean;
  resource_type?: string;
  step_name?: string;
  transaction_name?: string;
  note?: string;
  excluded?: boolean;
}

interface RecordingSession {
  id: string;
  name: string;
  status: string;
  started_at: string;
  stopped_at?: string;
  base_url?: string;
  requests?: RecordedRequest[];
  request_count?: number;
  last_seq?: number;
  // Requests the proxy refused for their target (a private address) and the
  // most recent such host; the browser got a 403 and nothing was recorded.
  rejected_requests?: number;
  last_rejected_host?: string;
  filters: {
    exclude_static: boolean;
    exclude_hosts?: string[];
    include_hosts?: string[];
    capture_cookies?: boolean;
  };
}

type RecentRecording =
  | { kind: "browser"; session: BrowserRecorderSession; at: string }
  | { kind: "proxy"; session: RecordingSession; at: string };

function recordingTime(at: string): number {
  const time = new Date(at).getTime();
  return Number.isNaN(time) ? 0 : time;
}

const STORAGE_STATE_NOT_KEPT =
  "The recorder could not keep this login state, so it cannot be reused.";

// hostFilterList is a profile's host filter without blank entries.
function hostFilterList(hosts?: string[]): string[] {
  return (hosts || []).map((host) => host.trim()).filter(Boolean);
}

// confidenceTone buckets a 0..1 correlation confidence for badge styling.
// >=0.8 is auto-applicable in the engine; the UI mirrors that threshold.
function confidenceTone(confidence?: number): "high" | "medium" | "low" | null {
  if (typeof confidence !== "number") return null;
  if (confidence >= 0.8) return "high";
  if (confidence >= 0.6) return "medium";
  return "low";
}

function safeRecordingFileName(name?: string): string {
  return (
    (name || "recording")
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-|-$/g, "") || "recording"
  );
}

function recordingLooksSalesforce(session: RecordingSession): boolean {
  const text = [
    session.base_url,
    ...(session.requests || []).flatMap((req) => [
      req.host,
      req.url,
      req.path,
      req.content_type,
    ]),
  ]
    .join(" ")
    .toLowerCase();
  return [
    "salesforce.com",
    ".force.com",
    "lightning.force",
    "/aura",
    "agentforce",
    "mulesoft",
    "anypoint",
  ].some((signal) => text.includes(signal));
}

function mergeRecordingSession(
  previous: RecordingSession | null,
  next: RecordingSession,
): RecordingSession {
  const incoming = next.requests || [];
  if (!previous || previous.id !== next.id) {
    return { ...next, requests: incoming };
  }
  if (incoming.length === 0) {
    return { ...previous, ...next, requests: previous.requests || [] };
  }
  const bySeq = new Map<number, RecordedRequest>();
  (previous.requests || []).forEach((req) => bySeq.set(req.seq, req));
  incoming.forEach((req) => bySeq.set(req.seq, req));
  return {
    ...previous,
    ...next,
    requests: Array.from(bySeq.values()).sort((a, b) => a.seq - b.seq).slice(-20),
  };
}

function isStoredScriptLike(value: unknown): value is StoredScript {
  const candidate = value as Partial<StoredScript> | null | undefined;
  return (
    !!candidate &&
    typeof candidate === "object" &&
    typeof candidate.id === "string" &&
    candidate.id.trim() !== "" &&
    typeof candidate.name === "string" &&
    typeof candidate.protocol === "string"
  );
}

function isScriptDefinitionLike(value: unknown): value is ScriptDefinition {
  const candidate = value as Partial<ScriptDefinition> | null | undefined;
  return (
    !!candidate &&
    typeof candidate === "object" &&
    typeof candidate.name === "string" &&
    typeof candidate.protocol === "string" &&
    (Array.isArray(candidate.actions) ||
      Array.isArray(candidate.init) ||
      Array.isArray(candidate.teardown))
  );
}

function recorderImportStoredScript(
  result?: RecorderImportResult | null,
): StoredScript | null {
  const script = result?.script as unknown;
  return isStoredScriptLike(script) ? script : null;
}

function recorderImportReviewScript(
  result?: RecorderImportResult | null,
): ScriptDefinition | null {
  const script = result?.script as unknown;
  if (isStoredScriptLike(script)) {
    return script.script || null;
  }
  return isScriptDefinitionLike(script) ? script : null;
}

// Whether this browser can record through the controller's HTTP proxy, and
// why not. reason is operator-facing; reason_code is what the studio words.
interface RecorderProxyAvailability {
  available: boolean;
  running?: boolean;
  reason_code?: string;
  reason?: string;
  private_targets_allowed?: boolean;
}

interface RecorderStatus {
  proxy_addr?: string;
  proxy?: RecorderProxyAvailability;
  local_client?: boolean;
  proxy_auth_token?: string;
  ca_cert_path?: string;
  active?: {
    id: string;
    name: string;
    status: string;
    request_count: number;
    started_at: string;
  } | null;
  browser?: {
    availability: BrowserRecorderAvailability;
    active?: { id: string; name: string; status: string } | null;
    install?: { status: string; error?: string };
  };
}

interface RecordingAgentChecksum {
  algorithm?: string;
  filename?: string;
  sha256?: string;
}

const MISSING_BROWSER_RECORDER_CAPABILITY: BrowserRecorderAvailability = {
  available: false,
  code: "not_supported",
  reason: "controller does not expose browser recording endpoints",
};
const DEFAULT_BRIDGE_ORIGIN = "http://127.0.0.1:27777";
const DEFAULT_RECORDING_AGENT_DOWNLOAD_PATH =
  "/api/v1/downloads/recording-agent/latest";
const BRIDGE_PAIRING_TOKEN_BYTES = 24;
const RECORDING_AGENT_COMMAND = "apexload-record-agent";

function generateBridgePairingToken(): string {
  const cryptoObj =
    typeof globalThis !== "undefined" ? globalThis.crypto : undefined;
  if (cryptoObj?.getRandomValues) {
    const bytes = new Uint8Array(BRIDGE_PAIRING_TOKEN_BYTES);
    cryptoObj.getRandomValues(bytes);
    return Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join(
      "",
    );
  }
  if (cryptoObj?.randomUUID) {
    return cryptoObj.randomUUID().replace(/-/g, "");
  }
  throw new Error(
    "Secure browser crypto is required to generate a bridge pairing token",
  );
}

function initialBridgePairingToken(): string {
  try {
    return generateBridgePairingToken();
  } catch {
    return "";
  }
}

function browserRecorderInstallRetryable(
  availability: BrowserRecorderAvailability | null,
  installState: { status: string; error?: string } | null,
): boolean {
  if (!installState || installState.status === "installed") {
    return false;
  }
  const code = availability?.code?.trim();
  if (code === "browsers_not_installed") {
    return true;
  }
  if (
    code &&
    [
      "display_missing",
      "production_disabled",
      "disabled",
      "not_supported",
    ].includes(code)
  ) {
    return false;
  }
  const reason = availability?.reason || "";
  const normalizedReason = reason.toLowerCase();
  return ![
    "no display detected",
    "production mode",
    "disabled via",
    "does not expose browser recording endpoints",
  ].some((blockingReason) => normalizedReason.includes(blockingReason));
}

function browserRecorderInstallTone(
  status: string,
): "info" | "success" | "error" {
  if (status === "failed") return "error";
  if (status === "installed") return "success";
  return "info";
}

function browserRecorderInstallLabel(status: string): string {
  switch (status) {
    case "failed":
      return "Recording browser install failed";
    case "installed":
      return "Recording browser install completed";
    case "installing":
      return "Recording browser install is running";
    default:
      return `Recording browser install: ${status}`;
  }
}

function currentDashboardOrigin(): string {
  if (
    typeof window === "undefined" ||
    !window.location.origin ||
    window.location.origin === "null"
  ) {
    return "https://your-apexload-controller.example";
  }
  return window.location.origin;
}

function bridgeAllowedDashboardOrigins(): string[] {
  const origin = currentDashboardOrigin();
  const origins = new Set([origin]);
  try {
    const url = new URL(origin);
    if (url.hostname === "127.0.0.1") {
      origins.add(
        `${url.protocol}//localhost${url.port ? `:${url.port}` : ""}`,
      );
    } else if (url.hostname === "localhost") {
      origins.add(
        `${url.protocol}//127.0.0.1${url.port ? `:${url.port}` : ""}`,
      );
    }
  } catch {
    // Keep the primary origin if parsing fails.
  }
  return Array.from(origins);
}

function shellQuote(value: string): string {
  return `'${value.replace(/'/g, `'\\''`)}'`;
}

function bridgeListenAddress(bridgeOrigin: string): string {
  try {
    const url = new URL(bridgeOrigin);
    return url.host || DEFAULT_BRIDGE_ORIGIN.replace(/^https?:\/\//i, "");
  } catch {
    return (
      bridgeOrigin
        .replace(/^https?:\/\//i, "")
        .replace(/\/+$/, "")
        .trim() || DEFAULT_BRIDGE_ORIGIN.replace(/^https?:\/\//i, "")
    );
  }
}

function bridgeRecordAgentCommand(bridgeOrigin: string): string {
  const parts = [
    RECORDING_AGENT_COMMAND,
    "--addr",
    bridgeListenAddress(bridgeOrigin),
    "--token-stdin",
  ];
  for (const origin of bridgeAllowedDashboardOrigins()) {
    parts.push("--allow-origin", shellQuote(origin));
  }
  return parts.join(" ");
}

function recordingAgentDownloadConfig(): { url: string; managed: boolean } {
  const env = import.meta.env?.VITE_APEXLOAD_RECORDING_AGENT_DOWNLOAD_URL;
  if (typeof env === "string" && env.trim()) {
    return { url: env.trim(), managed: false };
  }
  return {
    url: `${trimTrailingSlash(API_ORIGIN)}${DEFAULT_RECORDING_AGENT_DOWNLOAD_PATH}`,
    managed: true,
  };
}

// Requests the proxy blocked for their target, in words for the author.
function proxyRejectionSummary(session: RecordingSession): string {
  const count = session.rejected_requests ?? 0;
  const host = session.last_rejected_host?.trim();
  const what = count === 1 ? "1 request was" : `${count} requests were`;
  return `${what} blocked because ${host ? `${host} is` : "the target is"} a private or internal address; ${count === 1 ? "it was" : "they were"} not recorded.`;
}

// How long a proxy recording may stay empty before the studio suggests the
// browser is not using the proxy.
const PROXY_QUIET_HINT_MS = 20_000;

// The author-facing words for a proxy this browser cannot record through;
// the operator's cause goes under "Details for administrators".
function proxyUnavailableExplanation(
  availability: RecorderProxyAvailability | null,
): { summary: string; details: string } | null {
  if (!availability || availability.available) return null;
  const summary =
    availability.reason_code === "loopback_only"
      ? "HTTP proxy recording isn't available here: this controller's proxy only accepts browsers on its own host, so recording uses browser actions."
      : "HTTP proxy recording isn't available on this controller, so recording uses browser actions.";
  return { summary, details: availability.reason?.trim() || "" };
}

function bridgePairingErrorMessage(error: any, bridgeOrigin: string): string {
  const message = error?.message || "";
  if (
    error instanceof TypeError ||
    /failed to fetch|networkerror/i.test(message)
  ) {
    return `Could not reach ${bridgeOrigin}. Confirm ${RECORDING_AGENT_COMMAND} is running; if this dashboard is served over HTTPS, the browser may also be blocking localhost private-network access.`;
  }
  return message || "Failed to pair with the local recording bridge";
}

function bridgeVerificationErrorMessage(
  error: any,
  bridgeOrigin: string,
): string {
  const message = error?.message || "";
  if (
    error instanceof TypeError ||
    /failed to fetch|networkerror/i.test(message)
  ) {
    return `APEX Load Recording Agent is not reachable at ${bridgeOrigin}. Download and run the agent command, then verify again. If this dashboard is served over HTTPS, the browser may also be blocking localhost private-network access.`;
  }
  return (
    message || `APEX Load Recording Agent is not reachable at ${bridgeOrigin}`
  );
}

function copyTextWithSelectionFallback(text: string): boolean {
  if (typeof document === "undefined" || !document.body) return false;
  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.setAttribute("readonly", "");
  textarea.style.position = "fixed";
  textarea.style.left = "-9999px";
  textarea.style.top = "0";
  document.body.appendChild(textarea);
  textarea.focus();
  textarea.select();
  try {
    return document.execCommand?.("copy") === true;
  } catch {
    return false;
  } finally {
    document.body.removeChild(textarea);
  }
}

async function writeClipboardText(text: string): Promise<void> {
  let clipboardError: unknown;
  try {
    const clipboard =
      typeof navigator !== "undefined" ? navigator.clipboard : undefined;
    if (clipboard?.writeText) {
      await clipboard.writeText(text);
      return;
    }
  } catch (error) {
    clipboardError = error;
  }
  if (copyTextWithSelectionFallback(text)) return;
  if (clipboardError instanceof Error) {
    throw new Error(
      "Clipboard is blocked in this browser. Select the terminal command and press Cmd+C or Ctrl+C.",
    );
  }
  throw new Error("Clipboard access is not available in this browser");
}

// The desktop shell embeds the dashboard in a Wails WebView where window.open
// escapes to the system browser, so the popup panel is web-only there. Wails
// production has a custom origin while `wails dev` uses Vite's HTTP origin;
// isDesktopSurface covers both by also feature-detecting the native bridge.
function isWailsShell(): boolean {
  return isDesktopSurface();
}

function browserRecorderPanelURL(sessionId: string, folderId?: string | null): string {
  const params = new URLSearchParams({ session: sessionId });
  if (folderId) params.set("folderId", folderId);
  const fragment = `/recorder-panel?${params.toString()}`;
  if (typeof window === "undefined") return `#${fragment}`;
  const url = new URL(window.location.href);
  url.hash = fragment;
  return url.toString();
}

function openBrowserRecorderPanelPopup(panelURL: string): void {
  if (typeof window === "undefined") {
    throw new Error("The browser recorder control panel is unavailable");
  }
  const target = new URL(panelURL, window.location.href);
  if (
    target.origin !== window.location.origin ||
    target.username !== "" ||
    target.password !== "" ||
    !target.hash.startsWith("#/recorder-panel?session=")
  ) {
    throw new Error("The browser recorder returned an unsafe control panel URL");
  }

  const features = "width=440,height=760";
  if (!hasDesktopApiCapability()) {
    const popup = window.open(
      target.toString(),
      "apexload-browser-recorder",
      `${features},noopener,noreferrer`,
    );
    if (!popup) {
      throw new Error("Allow pop-ups to open the browser recorder control panel");
    }
    return;
  }

  // A noopener child does not inherit sessionStorage, which is where a
  // system-browser desktop tab keeps its controller capability. Create a fresh
  // same-origin blank child, reduce its copied storage to that one capability,
  // sever the opener synchronously, and only then navigate to the panel SPA.
  const capability = window.sessionStorage
    .getItem(DESKTOP_CAPABILITY_SESSION_KEY)
    ?.trim();
  if (!capability) {
    throw new Error(
      "Reopen APEX Load from the desktop app before opening the recorder control panel",
    );
  }
  const popup = window.open("", "_blank", features);
  if (!popup) {
    throw new Error("Allow pop-ups to open the browser recorder control panel");
  }
  try {
    popup.opener = null;
    popup.sessionStorage.clear();
    popup.sessionStorage.setItem(
      DESKTOP_CAPABILITY_SESSION_KEY,
      capability,
    );
    popup.location.replace(target.toString());
  } catch {
    popup.close();
    throw new Error(
      "Unable to authorize the browser recorder control panel safely",
    );
  }
}

interface RecordingStudioProps {
  activeProject?: Project | null;
  // Destination folder for saved recordings, matching the import surfaces. A
  // recording started while a folder is open belongs in that folder.
  folderId?: string | null;
  destinationControl?: ReactNode;
  onImported?: (result: ImportedSaveResult) => void;
  onEdit?: (script: ScriptDefinition) => void;
  /** The recording method, when the page holds it (to route it). */
  method?: "browser" | "proxy";
  /** byUser is false when the studio changes the method itself. */
  onMethodChange?: (
    method: "browser" | "proxy",
    choice: { byUser: boolean },
  ) => void;
  onClose?: () => void;
  onOperationBusyChange?: (busy: boolean) => void;
}

export default function RecordingStudio({
  activeProject,
  folderId,
  destinationControl,
  onImported,
  onEdit,
  onClose,
  onOperationBusyChange,
  method,
  onMethodChange,
}: RecordingStudioProps) {
  const [internalRecordingMode, setInternalRecordingMode] = useState<'browser' | 'proxy'>('browser');
  const recordingMode = method ?? internalRecordingMode;
  const setRecordingMode = useCallback((nextMode: 'browser' | 'proxy', byUser = true) => {
    if (onMethodChange) onMethodChange(nextMode, { byUser });
    else setInternalRecordingMode(nextMode);
  }, [onMethodChange]);
  const [browserURLError, setBrowserURLError] = useState('');
  const browserURLRef = useRef<HTMLInputElement>(null);
  const api = useApi();
  const studioActiveRef = useRef(true);
  useEffect(() => {
    studioActiveRef.current = true;
    return () => {
      studioActiveRef.current = false;
    };
  }, []);
  const sourceSecretSessions = useSourceSecretSessionLifecycle(
    api.discardImportSecretSession,
  );
  const [sessions, setSessions] = useState<RecordingSession[]>([]);
  const [activeSession, setActiveSession] = useState<RecordingSession | null>(
    null,
  );
  const [certificateFormatView, setCertificateFormatView] = useState<
    "crt" | "pem"
  >("crt");
  const [proxyPlatformView, setProxyPlatformView] = useState<
    "macos" | "windows"
  >("macos");
  const [proxyAddr, setProxyAddr] = useState("");
  const [proxyAuthToken, setProxyAuthToken] = useState("");
  const [caCertPath, setCaCertPath] = useState("");
  const [proxyAvailability, setProxyAvailability] =
    useState<RecorderProxyAvailability | null>(null);
  // Whether the last recorder status poll failed.
  const [recorderStatusFailed, setRecorderStatusFailed] = useState(false);
  // Whether this browser runs on the controller's host; null until known.
  const [localClient, setLocalClient] = useState<boolean | null>(null);
  const [macOSNetworkService, setMacOSNetworkService] = useState(
    DEFAULT_MACOS_NETWORK_SERVICE,
  );
  const [sessionName, setSessionName] = useState("");
  const [includeHosts, setIncludeHosts] = useState("");
  const [excludeHosts, setExcludeHosts] = useState("");
  const [captureCookies, setCaptureCookies] = useState(false);
  // The project recording profile recordings started here apply.
  const [recordingProfileId, setRecordingProfileId] = useState("");
  const [recordingProfileLevel, setRecordingProfileLevel] = useState<
    "url" | "html" | undefined
  >(undefined);
  // The selected profile, to show the host filters it adds to the form's.
  const [recordingProfile, setRecordingProfile] = useState<
    RecordingProfile | undefined
  >(undefined);
  const selectRecordingProfile = useCallback(
    (id: string, profile?: RecordingProfile) => {
      setRecordingProfileId(id);
      setRecordingProfile(id ? profile : undefined);
      setRecordingProfileLevel(profile?.http_script_level || undefined);
      // Show the profile's cookie capture; the session records what is shown.
      setCaptureCookies(profile?.capture_cookies === true);
    },
    [],
  );
  const [feedback, setFeedback] = useState("");
  const [feedbackType, setFeedbackType] = useState<"success" | "error">(
    "success",
  );
  const [selectedSession, setSelectedSession] =
    useState<RecordingSession | null>(null);
  const [selectedRequest, setSelectedRequest] =
    useState<RecordedRequest | null>(null);
  const requestTableRef = useRef<HTMLDivElement | null>(null);
  const selectedRequests = selectedSession?.requests || [];
  const virtualizeSelectedRequests = selectedRequests.length > 100;
  const selectedRequestVirtualizer = useVirtualizer({
    count: selectedRequests.length,
    getScrollElement: () => requestTableRef.current,
    estimateSize: () => 42,
    overscan: 12,
    enabled: virtualizeSelectedRequests,
  });
  const selectedRequestVirtualRows = virtualizeSelectedRequests
    ? selectedRequestVirtualizer.getVirtualItems()
    : [];
  const selectedRequestPaddingTop = selectedRequestVirtualRows[0]?.start || 0;
  const selectedRequestPaddingBottom = selectedRequestVirtualRows.length > 0
    ? Math.max(0, selectedRequestVirtualizer.getTotalSize() - selectedRequestVirtualRows[selectedRequestVirtualRows.length - 1].end)
    : 0;
  const [requestDraft, setRequestDraft] = useState({
    step_name: "",
    transaction_name: "",
    note: "",
    think_time_ms: 0,
    excluded: false,
  });
  const [showSetup, setShowSetup] = useState(false);
  const [parameterizeInputs, setParameterizeInputs] = useState(false);
  const [browserAvailability, setBrowserAvailability] =
    useState<BrowserRecorderAvailability | null>(null);
  const [browserSessions, setBrowserSessions] = useState<
    BrowserRecorderSession[]
  >([]);
  const [browserPanelSessionId, setBrowserPanelSessionId] = useState<
    string | null
  >(null);
  const [browserStartInProgress, setBrowserStartInProgress] = useState(false);
  const [browserName, setBrowserName] = useState("");
  const [browserStartScheme, setBrowserStartScheme] = useState<HttpURLScheme>(
    DEFAULT_HTTP_URL_SCHEME,
  );
  const [browserStartAddress, setBrowserStartAddress] = useState("");
  const [browserCaptureHAR, setBrowserCaptureHAR] = useState(true);
  const [browserDevice, setBrowserDevice] = useState("");
  const [browserDevices, setBrowserDevices] = useState<
    { name: string; platform: string }[]
  >([]);
  const [browserScreenshots, setBrowserScreenshots] = useState(false);
  const [browserWSPayloads, setBrowserWSPayloads] = useState(false);
  const [browserDisableQUIC, setBrowserDisableQUIC] = useState(false);
  // The saved login state (its storage_state_path) a recording starts with.
  const [browserStorageState, setBrowserStorageState] = useState("");
  const [storageStates, setStorageStates] = useState<
    BrowserRecorderStorageState[]
  >([]);
  // Whether this recorder lists saved login states; null until it answers.
  const [storageStatesListed, setStorageStatesListed] = useState<
    boolean | null
  >(null);
  const [storageStateSavingId, setStorageStateSavingId] = useState<
    string | null
  >(null);
  const [browserExcludeHosts, setBrowserExcludeHosts] = useState("");
  const [browserSidebar, setBrowserSidebar] = useState(true);
  const [browserOptionsOpen, setBrowserOptionsOpen] = useState(false);
  const [installState, setInstallState] = useState<{
    status: string;
    error?: string;
  } | null>(null);
  const [bridgeOrigin, setBridgeOrigin] = useState(DEFAULT_BRIDGE_ORIGIN);
  const [bridgeToken, setBridgeToken] = useState(initialBridgePairingToken);
  const [bridgeTokenRevealed, setBridgeTokenRevealed] = useState(false);
  const [bridgePairing, setBridgePairing] = useState(false);
  const [bridgeVerifying, setBridgeVerifying] = useState(false);
  const [bridgeStatus, setBridgeStatus] = useState("");
  const [bridgeStatusTone, setBridgeStatusTone] = useState<
    "info" | "success" | "error"
  >("info");
  const [recordingAgentPlatform, setRecordingAgentPlatform] = useState("");
  const [recordingAgentPlatformDetecting, setRecordingAgentPlatformDetecting] =
    useState(true);
  const [recordingAgentChecksum, setRecordingAgentChecksum] =
    useState<RecordingAgentChecksum | null>(null);
  const [recordingAgentChecksumStatus, setRecordingAgentChecksumStatus] =
    useState("");
  const convertTask = useAsyncTask<RecorderImportResult>();
  const convertResultRef = useRef<HTMLDivElement | null>(null);
  const salesforceTask = useAsyncTask<SalesforceImportResult>();
  const convertedCredentialReviewRef = useRef<SecretBindingReviewHandle | null>(
    null,
  );
  const salesforceCredentialReviewRef =
    useRef<SecretBindingReviewHandle | null>(null);
  const convertedReviewGenerationRef = useRef(0);
  const salesforceReviewGenerationRef = useRef(0);
  const [convertedReviewGeneration, setConvertedReviewGeneration] = useState(0);
  const [salesforceReviewGeneration, setSalesforceReviewGeneration] =
    useState(0);
  const [convertedConfiguredCredentialReviewIdentity, setConvertedConfiguredCredentialReviewIdentity] =
    useState("");
  const [salesforceConfiguredCredentialReviewIdentity, setSalesforceConfiguredCredentialReviewIdentity] =
    useState("");
  const convertedCredentialReviewIdentity = `${convertedReviewGeneration}:${activeProject?.id || ""}`;
  const salesforceCredentialReviewIdentity = `${salesforceReviewGeneration}:${activeProject?.id || ""}`;
  const convertedProjectSecretsConfiguredForReview =
    convertedConfiguredCredentialReviewIdentity === convertedCredentialReviewIdentity;
  const salesforceProjectSecretsConfiguredForReview =
    salesforceConfiguredCredentialReviewIdentity === salesforceCredentialReviewIdentity;
  const advanceConvertedReviewGeneration = useCallback(() => {
    const next = convertedReviewGenerationRef.current + 1;
    convertedReviewGenerationRef.current = next;
    setConvertedReviewGeneration(next);
    return next;
  }, []);
  const advanceSalesforceReviewGeneration = useCallback(() => {
    const next = salesforceReviewGenerationRef.current + 1;
    salesforceReviewGenerationRef.current = next;
    setSalesforceReviewGeneration(next);
    return next;
  }, []);
  const convertedPreparedScriptRef = useRef<ScriptDefinition | null>(null);
  const salesforcePreparedScriptRef = useRef<ScriptDefinition | null>(null);
  // A conversion can be launched directly from any session card without
  // selecting that session in the request-detail panel. Keep provenance with
  // the review result instead of deriving it later from selectedSession.
  const convertedSourceRecordingSessionIDRef = useRef<string | null>(null);
  const [convertedPreparedSecrets, setConvertedPreparedSecrets] =
    useState<PreparedSecretBindings | null>(null);
  const [salesforcePreparedSecrets, setSalesforcePreparedSecrets] =
    useState<PreparedSecretBindings | null>(null);
  const [
    convertedParameterizationApplying,
    setConvertedParameterizationApplying,
  ] = useState(false);
  const [
    salesforceParameterizationApplying,
    setSalesforceParameterizationApplying,
  ] = useState(false);
  const [
    convertedCredentialActionInProgress,
    setConvertedCredentialActionInProgress,
  ] = useState(false);
  const [
    salesforceCredentialActionInProgress,
    setSalesforceCredentialActionInProgress,
  ] = useState(false);
  const convertedSaveInFlightRef = useRef(false);
  const salesforceSaveInFlightRef = useRef(false);
  const convertedBatchSaveOperationRef = useRef<{
    generation: number;
    projectId?: string;
    folderId: string | null;
    recordingSessionId: string;
    operationId: string;
  } | null>(null);
  const salesforceBatchSaveOperationRef = useRef<{
    generation: number;
    projectId?: string;
    folderId: string | null;
    operationId: string;
  } | null>(null);
  const [convertedSaveInProgress, setConvertedSaveInProgress] = useState(false);
  const [salesforceSaveInProgress, setSalesforceSaveInProgress] =
    useState(false);
  const [browserPanelOperationBusy, setBrowserPanelOperationBusy] =
    useState(false);
  const operationBusy =
    convertTask.pending ||
    salesforceTask.pending ||
    convertedSaveInProgress ||
    salesforceSaveInProgress ||
    convertedParameterizationApplying ||
    salesforceParameterizationApplying ||
    convertedCredentialActionInProgress ||
    salesforceCredentialActionInProgress ||
    browserPanelOperationBusy;
  useEffect(() => {
    onOperationBusyChange?.(operationBusy);
    return () => onOperationBusyChange?.(false);
  }, [onOperationBusyChange, operationBusy]);
  const resetConvertTask = convertTask.reset;
  const resetSalesforceTask = salesforceTask.reset;
  const refreshInFlightRef = useRef<{
    projectId?: string;
    recorderRequestScope: string;
    request: Promise<void>;
  } | null>(null);
  const recorderRequestScope = api.browserRecorderRequestScope;
  const refreshScopeRef = useRef({
    projectId: activeProject?.id,
    recorderRequestScope,
    generation: 0,
  });
  const browserStartScopeRef = useRef<typeof refreshScopeRef.current | null>(
    null,
  );
  const previousRecorderRequestScopeRef = useRef(recorderRequestScope);
  const feedbackTimerRef = useRef<number | null>(null);

  if (
    refreshScopeRef.current.projectId !== activeProject?.id ||
    refreshScopeRef.current.recorderRequestScope !== recorderRequestScope
  ) {
    refreshScopeRef.current = {
      projectId: activeProject?.id,
      recorderRequestScope,
      generation: refreshScopeRef.current.generation + 1,
    };
  }
  const renderedProjectScope = refreshScopeRef.current;
  const isCurrentProject = () =>
    refreshScopeRef.current === renderedProjectScope;
  const activeSessionRef = useRef<RecordingSession | null>(null);

  const proxyPort = recorderProxyPort(proxyAddr);
  const proxyHost = recorderProxyHost(proxyAddr, currentDashboardOrigin());
  // Only a browser on the controller's host reaches a loopback proxy.
  const proxyHostIsLocal = recorderProxyHostIsLocal(proxyHost) && localClient !== false;

  useEffect(() => {
    activeSessionRef.current = activeSession;
  }, [activeSession]);

  // Leave the HTTP proxy method once the controller says this browser cannot
  // use it, unless a live proxy recording needs it to be stopped. Earlier
  // proxy recordings stay in Recent recordings, which every method shows.
  useEffect(() => {
    if (
      proxyAvailability?.available === false &&
      !activeSession &&
      recordingMode === "proxy"
    ) {
      setRecordingMode("browser", false);
    }
  }, [activeSession, proxyAvailability?.available, recordingMode, setRecordingMode]);

  // A proxy recording that stays empty suggests the browser is not sending
  // its traffic through the proxy; say so once it has been quiet a while.
  const activeSessionRequestCount =
    activeSession?.request_count ?? activeSession?.requests?.length ?? 0;
  const activeSessionId = activeSession?.id;
  const activeSessionStatus = activeSession?.status;
  const activeSessionStartedAt = activeSession?.started_at;
  const [quietProxySessionId, setQuietProxySessionId] = useState<string | null>(null);
  useEffect(() => {
    if (!activeSessionId || activeSessionStatus !== "recording" || activeSessionRequestCount > 0) {
      return undefined;
    }
    const startedAt = Date.parse(activeSessionStartedAt || "");
    const wait = Number.isNaN(startedAt)
      ? PROXY_QUIET_HINT_MS
      : Math.max(0, startedAt + PROXY_QUIET_HINT_MS - Date.now());
    const timer = window.setTimeout(() => setQuietProxySessionId(activeSessionId), wait);
    return () => window.clearTimeout(timer);
  }, [activeSessionId, activeSessionStatus, activeSessionStartedAt, activeSessionRequestCount]);
  const proxyQuiet =
    !!activeSession &&
    quietProxySessionId === activeSession.id &&
    activeSession.status === "recording" &&
    activeSessionRequestCount === 0;

  useEffect(() => {
    let cancelled = false;
    setRecordingAgentPlatformDetecting(true);
    detectPlatformFromNavigator()
      .then((platform) => {
        if (cancelled) return;
        setRecordingAgentPlatform(platform || "");
        setRecordingAgentPlatformDetecting(false);
      })
      .catch(() => {
        if (cancelled) return;
        setRecordingAgentPlatform("");
        setRecordingAgentPlatformDetecting(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    const agentDownload = recordingAgentDownloadConfig();
    const shouldShowBridgeDownload =
      browserAvailability?.available === false || !!api.browserRecorderBridge;
    if (
      !agentDownload.managed ||
      !shouldShowBridgeDownload ||
      !recordingAgentPlatform
    ) {
      setRecordingAgentChecksum(null);
      setRecordingAgentChecksumStatus("");
      return;
    }

    let cancelled = false;
    setRecordingAgentChecksum(null);
    setRecordingAgentChecksumStatus("Checking SHA-256…");
    apiFetch(
      `/downloads/recording-agent/latest/checksum?platform=${encodeURIComponent(recordingAgentPlatform)}`,
    )
      .then(async (response) => {
        if (cancelled) return;
        if (!response.ok) {
          setRecordingAgentChecksumStatus(
            "SHA-256 unavailable until this platform package is staged.",
          );
          return;
        }
        const payload = (await response.json()) as RecordingAgentChecksum;
        if (cancelled) return;
        setRecordingAgentChecksum(payload);
        setRecordingAgentChecksumStatus("");
      })
      .catch(() => {
        if (!cancelled)
          setRecordingAgentChecksumStatus(
            "SHA-256 unavailable until this platform package is staged.",
          );
      });
    return () => {
      cancelled = true;
    };
  }, [
    api.browserRecorderBridge,
    browserAvailability?.available,
    recordingAgentPlatform,
  ]);

  const handleCopyCAPath = async () => {
    if (!caCertPath) return;
    try {
      if (!navigator.clipboard?.writeText) {
        throw new Error("Clipboard access is not available in this browser");
      }
      await navigator.clipboard.writeText(caCertPath);
      showFeedback("CA certificate path copied");
    } catch (e: any) {
      showFeedback(e.message || "Failed to copy CA certificate path", "error");
    }
  };

  const handleDownloadCA = async (format: "crt" | "pem" = "crt") => {
    try {
      const url =
        format === "pem" ? "/recorder/ca.pem?format=pem" : "/recorder/ca.pem";
      const res = await apiFetch(url);
      if (!res.ok) throw new Error("Failed to download CA certificate");
      const blob = await res.blob();
      const filename = format === "pem" ? "apexload-ca.pem" : "apexload-ca.crt";
      await downloadBlob(blob, filename);
    } catch (e: any) {
      showFeedback(e.message || "Failed to download CA certificate", "error");
    }
  };

  const handleDownloadRecordingAgent = async (
    event: MouseEvent<HTMLAnchorElement>,
    managedDownload: boolean,
  ) => {
    if (!managedDownload) return;
    event.preventDefault();
    if (!recordingAgentPlatform) {
      showFeedback(
        "Choose a Recording Agent platform before downloading.",
        "error",
      );
      return;
    }
    setFeedback("");
    try {
      const resourcePath = `${DEFAULT_RECORDING_AGENT_DOWNLOAD_PATH}?platform=${encodeURIComponent(recordingAgentPlatform)}`;
      const filename =
        recordingAgentChecksum?.filename?.trim() ||
        "apexload-recording-agent.tar.gz";
      const nativeOutcome = await saveApiResourceViaBridge(
        resourcePath,
        filename,
      );
      if (nativeOutcome === "saved") {
        const sum = recordingAgentChecksum?.sha256;
        showFeedback(
          sum
            ? `APEX Load Recording Agent saved (${platformLabel(recordingAgentPlatform)}). SHA-256: ${sum}`
            : `APEX Load Recording Agent saved (${platformLabel(recordingAgentPlatform)})`,
        );
        return;
      }
      if (nativeOutcome === "cancelled") return;
      if (!isDesktopSurface() && hasDesktopApiCapability()) {
        await startDesktopBrowserDownload(resourcePath, filename);
        const sum = recordingAgentChecksum?.sha256;
        showFeedback(
          sum
            ? `APEX Load Recording Agent download request opened (${platformLabel(recordingAgentPlatform)}). SHA-256: ${sum}`
            : `APEX Load Recording Agent download request opened (${platformLabel(recordingAgentPlatform)}); any server error will appear in the browser.`,
        );
        return;
      }
      const res = await apiFetch(
        `/downloads/recording-agent/latest?platform=${encodeURIComponent(recordingAgentPlatform)}`,
      );
      const contentType = (res.headers.get("content-type") || "").toLowerCase();
      if (!res.ok)
        throw new Error(
          "Recording Agent package is not available from this controller",
        );
      if (contentType.includes("text/html")) {
        throw new Error(
          "Recording Agent download endpoint returned the dashboard instead of an agent archive. Restart the controller with the latest build and stage the agent packages.",
        );
      }
      const blob = await res.blob();
      const outcome = await downloadBlob(
        blob,
        downloadFilenameFromDisposition(
          res.headers.get("content-disposition"),
          filename,
        ),
      );
      if (outcome === "cancelled") return;
      const sum =
        res.headers.get("x-content-sha256") || recordingAgentChecksum?.sha256;
      showFeedback(
        sum
          ? `APEX Load Recording Agent download started (${platformLabel(recordingAgentPlatform)}). SHA-256: ${sum}`
          : `APEX Load Recording Agent download started (${platformLabel(recordingAgentPlatform)})`,
      );
    } catch (e: any) {
      showFeedback(
        e.message || "Failed to download the APEX Load Recording Agent",
        "error",
      );
    }
  };

  const copyProxyCommand = async (
    command: string,
    successMessage: string,
    failureMessage: string,
  ) => {
    try {
      await writeClipboardText(command);
      showFeedback(successMessage);
    } catch (e: any) {
      showFeedback(e.message || failureMessage, "error");
    }
  };

  const ensureBridgeToken = (): string => {
    if (bridgeToken) return bridgeToken;
    try {
      const token = generateBridgePairingToken();
      setBridgeToken(token);
      return token;
    } catch (e: any) {
      showFeedback(
        e.message || "Failed to generate bridge pairing token",
        "error",
      );
      return "";
    }
  };

  const handleCopyBridgeCommand = async () => {
    ensureBridgeToken();
    await copyProxyCommand(
      bridgeRecordAgentCommand(bridgeOrigin),
      "Terminal command copied. Paste it into a local terminal, then paste the pairing token at the prompt.",
      "Failed to copy local bridge command",
    );
  };

  const handleCopyBridgeToken = async () => {
    const token = ensureBridgeToken();
    if (!token) return;
    await copyProxyCommand(
      token,
      "Pairing token copied. Paste it into the local agent prompt.",
      "Failed to copy pairing token",
    );
  };

  const handleRegenerateBridgeToken = () => {
    try {
      setBridgeToken(generateBridgePairingToken());
      setBridgeStatus("");
      setBridgeStatusTone("info");
      showFeedback("New local bridge pairing token generated");
    } catch (e: any) {
      showFeedback(
        e.message || "Failed to generate bridge pairing token",
        "error",
      );
    }
  };

  const handleCopyMacOSProxyCommand = async (mode: "start" | "restore") => {
    const command =
      mode === "start"
        ? buildMacOSStartProxyCommand(
            proxyPort,
            macOSNetworkService,
            proxyHost,
            proxyAuthToken,
          )
        : buildMacOSRestoreProxyCommand(
            macOSNetworkService,
            proxyHost,
            proxyPort,
            proxyAuthToken,
          );
    await copyProxyCommand(
      command,
      mode === "start"
        ? "macOS start proxy command copied"
        : "macOS restore proxy command copied",
      "Failed to copy macOS proxy command",
    );
  };

  const handleCopyWindowsProxyCommand = async (mode: "start" | "restore") => {
    const command =
      mode === "start"
        ? buildWindowsStartProxyCommand(proxyPort, proxyHost)
        : buildWindowsRestoreProxyCommand(proxyPort, proxyHost);
    await copyProxyCommand(
      command,
      mode === "start"
        ? "Windows start proxy command copied"
        : "Windows restore proxy command copied",
      "Failed to copy Windows proxy command",
    );
  };

  const handleOpenMacOSNetworkSettings = async () => {
    const openSystemSettings = getDesktopBridge()?.OpenSystemSettings;
    if (!openSystemSettings) {
      showFeedback(
        "Open System Settings > Network manually on this device.",
        "error",
      );
      return;
    }
    try {
      await openSystemSettings("network");
    } catch (e: any) {
      showFeedback(
        e.message || "Failed to open macOS Network settings.",
        "error",
      );
    }
  };

  const handleOpenWindowsProxySettings = async () => {
    const openSystemSettings = getDesktopBridge()?.OpenSystemSettings;
    if (!openSystemSettings) {
      showFeedback(
        "Open Windows Settings > Network & internet > Proxy manually on this device.",
        "error",
      );
      return;
    }
    try {
      await openSystemSettings("proxy");
    } catch (e: any) {
      showFeedback(
        e.message || "Failed to open Windows Proxy settings.",
        "error",
      );
    }
  };

  const showFeedback = useCallback(
    (msg: string, type: "success" | "error" = "success") => {
      setFeedback(msg);
      setFeedbackType(type);
      if (feedbackTimerRef.current !== null)
        window.clearTimeout(feedbackTimerRef.current);
      feedbackTimerRef.current = window.setTimeout(() => {
        feedbackTimerRef.current = null;
        setFeedback("");
      }, 4000);
    },
    [],
  );

  const refresh = useCallback(async () => {
    const projectId = activeProject?.id;
    const inFlight = refreshInFlightRef.current;
    if (
      inFlight &&
      inFlight.projectId === projectId &&
      inFlight.recorderRequestScope === recorderRequestScope
    )
      return inFlight.request;
    if (typeof document !== "undefined" && document.hidden) return;
    const scope = { ...refreshScopeRef.current };
    const isCurrentScope = () =>
      refreshScopeRef.current.projectId === scope.projectId &&
      refreshScopeRef.current.recorderRequestScope ===
        scope.recorderRequestScope &&
      refreshScopeRef.current.generation === scope.generation;
    const request = (async () => {
      try {
        const [listRes, statusRes, browserListRes] = await Promise.all([
          api.listRecordings(projectId),
          api.getRecorderStatus(projectId),
          api
            .listBrowserRecordings(projectId)
            .catch(() => ({ sessions: [], count: 0 })),
        ]);
        if (!isCurrentScope()) return;
        const recorderStatus = statusRes as RecorderStatus;
        setSessions(listRes.sessions || []);
        setBrowserSessions(browserListRes.sessions || []);
        setProxyAddr(recorderStatus.proxy_addr || "");
        setProxyAuthToken(recorderStatus.proxy_auth_token || "");
        setCaCertPath(recorderStatus.ca_cert_path || "");
        // A controller that predates the field always offered its proxy.
        setProxyAvailability(recorderStatus.proxy ?? { available: true });
        setLocalClient(
          typeof recorderStatus.local_client === "boolean"
            ? recorderStatus.local_client
            : null,
        );
        let liveBrowser = recorderStatus.browser?.active;
        if (api.browserRecorderBridge) {
          try {
            const bridgeRes = await api.getBrowserRecorderBridgeStatus();
            if (!isCurrentScope()) return;
            if (bridgeRes?.bridge) {
              setBrowserAvailability(bridgeRes.bridge.availability);
              setInstallState(null);
              liveBrowser = bridgeRes.bridge.active || null;
              setBridgeStatus(
                `Connected to ${api.browserRecorderBridge.origin}`,
              );
            }
          } catch (e: any) {
            if (!isCurrentScope()) return;
            setBrowserAvailability({
              available: false,
              reason: bridgePairingErrorMessage(
                e,
                api.browserRecorderBridge.origin,
              ),
            });
            setBridgeStatus("Local recording bridge is not responding");
          }
        } else if (recorderStatus.browser) {
          setBrowserAvailability(recorderStatus.browser.availability);
          setInstallState(recorderStatus.browser.install ?? null);
        } else {
          setBrowserAvailability(MISSING_BROWSER_RECORDER_CAPABILITY);
          setInstallState(null);
        }
        if (liveBrowser?.id) {
          // Adopt a recording started elsewhere (popup, another tab, reload).
          setBrowserPanelSessionId((current) => current ?? liveBrowser.id);
        }
        if (recorderStatus.active) {
          // Refresh only the bounded tail rendered by the live feed. Tail
          // snapshots include in-place WebSocket/exclusion edits while avoiding
          // a full body download every two seconds.
          const full = await api.getRecording(recorderStatus.active.id, {
            tail: 20,
          });
          if (!isCurrentScope()) return;
          setActiveSession((prev) => mergeRecordingSession(prev, full));
        } else {
          setActiveSession(null);
        }
        if (isCurrentScope()) setRecorderStatusFailed(false);
      } catch {
        // The API may not be ready; the next poll retries. Until one answers,
        // say the proxy could not be checked rather than "checking".
        if (isCurrentScope()) setRecorderStatusFailed(true);
      }
    })();
    refreshInFlightRef.current = { projectId, recorderRequestScope, request };
    try {
      await request;
    } finally {
      if (refreshInFlightRef.current?.request === request)
        refreshInFlightRef.current = null;
    }
  }, [api, activeProject?.id, recorderRequestScope]);

  // Saved login states change only when one is saved or expires, so they are
  // listed on entry, when the browser options open and after a save — not on
  // every poll. A selection that has gone (expired) is cleared.
  // Only the latest listing is shown: an earlier one that answers later
  // must not hide a state saved since.
  const storageStatesRequestRef = useRef(0);
  const refreshStorageStates = useCallback(async () => {
    const scope = refreshScopeRef.current;
    const request = ++storageStatesRequestRef.current;
    const isLatest = () =>
      refreshScopeRef.current === scope &&
      storageStatesRequestRef.current === request;
    try {
      const res = await api.listBrowserRecordingStorageStates(scope.projectId);
      const states = res.states || [];
      if (isLatest()) {
        setStorageStates(states);
        setStorageStatesListed(true);
        setBrowserStorageState((current) =>
          current && !states.some((state) => state.path === current)
            ? ""
            : current,
        );
      }
      return states;
    } catch (e: any) {
      // A recorder that predates the listing (404) cannot offer a saved state;
      // another failure keeps what was listed, or offers nothing yet.
      if (isLatest()) {
        if (e?.status === 404) {
          setStorageStates([]);
          setStorageStatesListed(false);
          setBrowserStorageState("");
        } else {
          setStorageStatesListed((listed) => listed ?? false);
        }
      }
      return null;
    }
  }, [api]);

  useEffect(() => {
    void refreshStorageStates();
  }, [refreshStorageStates, activeProject?.id, recorderRequestScope]);

  useEffect(() => {
    if (browserOptionsOpen) void refreshStorageStates();
  }, [browserOptionsOpen, refreshStorageStates]);

  useLayoutEffect(() => {
    const recorderEndpointChanged =
      previousRecorderRequestScopeRef.current !== recorderRequestScope;
    previousRecorderRequestScopeRef.current = recorderRequestScope;
    sourceSecretSessions.clear("converted");
    sourceSecretSessions.clear("salesforce");
    if (feedbackTimerRef.current !== null) {
      window.clearTimeout(feedbackTimerRef.current);
      feedbackTimerRef.current = null;
    }
    setSessions([]);
    setActiveSession(null);
    setSelectedSession(null);
    setSelectedRequest(null);
    setBrowserSessions([]);
    setBrowserPanelSessionId(null);
    setStorageStates([]);
    setStorageStatesListed(null);
    setBrowserStorageState("");
    setStorageStateSavingId(null);
    browserStartScopeRef.current = null;
    setBrowserStartInProgress(false);
    if (recorderEndpointChanged) {
      setRecorderStatusFailed(false);
      setProxyAddr("");
      setProxyAuthToken("");
      setCaCertPath("");
      setProxyAvailability(null);
      setLocalClient(null);
      setBrowserAvailability(null);
      setInstallState(null);
      setBrowserDevices([]);
      setBridgePairing(false);
      setBridgeVerifying(false);
      setBridgeStatus("");
      setBridgeStatusTone("info");
    }
    setFeedback("");
    setConvertedParameterizationApplying(false);
    setSalesforceParameterizationApplying(false);
    setConvertedCredentialActionInProgress(false);
    setSalesforceCredentialActionInProgress(false);
    advanceConvertedReviewGeneration();
    advanceSalesforceReviewGeneration();
    resetConvertTask();
    resetSalesforceTask();
    convertedSourceRecordingSessionIDRef.current = null;
    convertedBatchSaveOperationRef.current = null;
  }, [
    activeProject?.id,
    recorderRequestScope,
    advanceConvertedReviewGeneration,
    advanceSalesforceReviewGeneration,
    resetConvertTask,
    resetSalesforceTask,
    sourceSecretSessions,
  ]);

  useEffect(() => {
    void refresh();
    const interval = setInterval(refresh, 2000);
    return () => clearInterval(interval);
  }, [refresh]);

  useEffect(
    () => () => {
      if (feedbackTimerRef.current !== null)
        window.clearTimeout(feedbackTimerRef.current);
    },
    [],
  );

  useEffect(() => {
    if (!selectedRequest) return;
    setRequestDraft({
      step_name: selectedRequest.step_name || "",
      transaction_name: selectedRequest.transaction_name || "",
      note: selectedRequest.note || "",
      think_time_ms: selectedRequest.think_time_ms || 0,
      excluded: !!selectedRequest.excluded,
    });
  }, [selectedRequest]);

  // Load device presets once for the launcher.
  useEffect(() => {
    const scope = refreshScopeRef.current;
    let cancelled = false;
    api
      .listBrowserRecorderDevices()
      .then((res) => {
        if (!cancelled && refreshScopeRef.current === scope) {
          setBrowserDevices(res.devices || []);
        }
      })
      .catch(() => {
        /* devices are optional */
      });
    return () => {
      cancelled = true;
    };
  }, [api, recorderRequestScope]);

  const handleInstallBrowsers = async () => {
    const sourceRecorderScope = refreshScopeRef.current;
    try {
      const res = await api.installBrowserRecorder();
      if (refreshScopeRef.current !== sourceRecorderScope) return;
      setInstallState(res.install);
      showFeedback(
        "Installing the recording browser — this can take a few minutes",
      );
    } catch (e: any) {
      if (refreshScopeRef.current === sourceRecorderScope) {
        showFeedback(e.message, "error");
      }
    }
  };

  const handlePairBridge = async () => {
    const sourceRecorderScope = refreshScopeRef.current;
    const origin = bridgeOrigin.trim().replace(/\/+$/, "");
    if (!/^https?:\/\/.+/i.test(origin)) {
      const message = `Enter the local bridge URL used by ${RECORDING_AGENT_COMMAND}`;
      setBridgeStatusTone("error");
      setBridgeStatus(message);
      showFeedback(message, "error");
      return;
    }
    const token = ensureBridgeToken();
    if (!token) return;
    setBridgePairing(true);
    setBridgeStatusTone("info");
    setBridgeStatus(`Connecting to ${origin}…`);
    try {
      const paired = await api.pairBrowserRecorderBridge(origin, token);
      // A successful pair deliberately changes the recorder request scope from
      // the controller to the newly paired bridge before this promise resumes.
      // Accept that exact transition, but reject a project/origin change or a
      // different bridge pairing that completed while this request was live.
      const pairedRequestScope = `bridge:${paired.origin.replace(/\/+$/, "")}:${paired.session_id}`;
      const currentScope = refreshScopeRef.current;
      if (
        currentScope.projectId !== sourceRecorderScope.projectId ||
        (currentScope !== sourceRecorderScope &&
          currentScope.recorderRequestScope !== pairedRequestScope)
      ) {
        return;
      }
      setBridgeStatusTone("success");
      setBridgeStatus(`Connected to ${origin}`);
      setBrowserAvailability(paired.status.availability);
      setInstallState(null);
      showFeedback("Local browser recording bridge paired");
    } catch (e: any) {
      if (refreshScopeRef.current !== sourceRecorderScope) return;
      const message = bridgePairingErrorMessage(e, origin);
      setBridgeStatusTone("error");
      setBridgeStatus(message);
      showFeedback(message, "error");
    } finally {
      if (refreshScopeRef.current === sourceRecorderScope) {
        setBridgePairing(false);
      }
    }
  };

  // Whether the Recording Agent answers at the bridge address; a failure says
  // why in the bridge status. False too when the recorder scope moved on.
  const verifyBridgeAgent = async (): Promise<boolean> => {
    const sourceRecorderScope = refreshScopeRef.current;
    const origin = bridgeOrigin.trim().replace(/\/+$/, "");
    if (!/^https?:\/\/.+/i.test(origin)) {
      setBridgeStatusTone("error");
      setBridgeStatus(
        `Enter the local bridge URL used by ${RECORDING_AGENT_COMMAND}`,
      );
      return false;
    }
    setBridgeVerifying(true);
    setBridgeStatusTone("info");
    setBridgeStatus(`Checking APEX Load Recording Agent at ${origin}…`);
    try {
      await api.verifyBrowserRecorderBridgeAgent(origin);
      if (refreshScopeRef.current !== sourceRecorderScope) return false;
      setBridgeOrigin(origin);
      setBridgeStatusTone("info");
      setBridgeStatus(
        `APEX Load Recording Agent is running at ${origin}. Connecting…`,
      );
      return true;
    } catch (e: any) {
      if (refreshScopeRef.current !== sourceRecorderScope) return false;
      const message = bridgeVerificationErrorMessage(e, origin);
      setBridgeStatusTone("error");
      setBridgeStatus(message);
      showFeedback(message, "error");
      return false;
    } finally {
      if (refreshScopeRef.current === sourceRecorderScope) {
        setBridgeVerifying(false);
      }
    }
  };

  // Connect = check that the agent answers, then pair with it, so a missing
  // agent is reported as such rather than as a pairing failure.
  const handleConnectBridge = async () => {
    if (await verifyBridgeAgent()) await handlePairBridge();
  };

  const handleDisconnectBridge = () => {
    api.clearBrowserRecorderBridge();
    setBridgeStatusTone("info");
    setBridgeStatus("");
    showFeedback("Local recording bridge disconnected");
  };

  const handleBrowserStartAddressChange = (value: string) => {
    const normalized = normalizeHttpURLInput(value, browserStartScheme);
    setBrowserStartScheme(normalized.scheme);
    setBrowserStartAddress(normalized.address);
  };

  const handleStartBrowserRecording = async () => {
    const sourceRecorderScope = refreshScopeRef.current;
    if (browserStartScopeRef.current === sourceRecorderScope) return;
    const startURL = composeHttpURL(browserStartScheme, browserStartAddress);
    if (!startURL) {
      setBrowserURLError("Enter an http(s) URL to record");
      browserURLRef.current?.focus();
      return;
    }
    setBrowserURLError('');
    browserStartScopeRef.current = sourceRecorderScope;
    setBrowserStartInProgress(true);
    try {
      const res = await api.startBrowserRecording({
        name: browserName.trim() || "Browser Recording",
        start_url: startURL,
        project_id: sourceRecorderScope.projectId,
        capture_har: browserCaptureHAR,
        device_preset: browserDevice || undefined,
        capture_screenshots: browserScreenshots,
        capture_ws_payloads: browserWSPayloads,
        quic_mode: browserDisableQUIC ? "off" : undefined,
        storage_state_path: browserStorageState.trim() || undefined,
        sidebar: browserSidebar,
        exclude_hosts: browserExcludeHosts.trim()
          ? browserExcludeHosts
              .split(",")
              .map((h) => h.trim())
              .filter(Boolean)
          : undefined,
      });
      if (refreshScopeRef.current !== sourceRecorderScope) return;
      const sessionId = res.session.id;
      setBrowserPanelSessionId(sessionId);
      const panelURL = browserRecorderPanelURL(sessionId, folderId);
      if (!isWailsShell()) {
        try {
          await api.openBrowserRecordingControlPanel(sessionId, panelURL);
          if (refreshScopeRef.current !== sourceRecorderScope) return;
          showFeedback(
            "Recording browser launched — control panel opened beside it",
          );
        } catch {
          if (refreshScopeRef.current !== sourceRecorderScope) return;
          // Older agents/controllers do not expose the same-browser companion
          // tab endpoint. Keep the separate popup as a fallback.
          try {
            openBrowserRecorderPanelPopup(panelURL);
            showFeedback(
              "Recording browser launched — interact with the page, then stop from the control panel",
            );
          } catch (popupError) {
            showFeedback(
              popupError instanceof Error
                ? `Recording started, but the control panel could not open: ${popupError.message}`
                : "Recording started, but the control panel could not open",
              "error",
            );
          }
        }
      } else {
        showFeedback(
          "Recording browser launched — interact with the page, then stop from the control panel",
        );
      }
    } catch (e: any) {
      // Older controllers return the browser engine's call log; keep only the cause.
      if (refreshScopeRef.current === sourceRecorderScope)
        showFeedback(String(e?.message || "Recording could not start").split(/\n\s*Call log:/)[0].trim(), "error");
    } finally {
      if (browserStartScopeRef.current === sourceRecorderScope) {
        browserStartScopeRef.current = null;
        setBrowserStartInProgress(false);
      }
    }
  };

  const handleOpenBrowserRecordingSession = (
    session: BrowserRecorderSession,
  ) => {
    // The control panel lives on the browser method's card; opening one is
    // not a choice of method to remember.
    setRecordingMode("browser", false);
    setBrowserPanelSessionId(session.id);
    showFeedback(
      activeSession
        ? `Opened browser recording "${session.name}". Your HTTP proxy recording is still running; choose HTTP proxy to stop it.`
        : `Opened browser recording "${session.name}"`,
    );
  };

  // Saving keeps the recording's cookies and storage on the controller (never
  // in the browser) so a later recording can start signed in with them.
  const handleSaveBrowserLoginState = async (
    session: BrowserRecorderSession,
  ) => {
    const scope = refreshScopeRef.current;
    setStorageStateSavingId(session.id);
    try {
      const saved = await api.saveBrowserRecordingStorageState(session.id);
      if (refreshScopeRef.current !== scope) return;
      const states = await refreshStorageStates();
      if (refreshScopeRef.current !== scope) return;
      // The recorder answers with the kept state's path; one that predates
      // that answer is checked against the list instead.
      const answeredPath = typeof saved?.path === "string" && saved.path !== "";
      if (
        !answeredPath &&
        states !== null &&
        !states.some((state) => state.recording_id === session.id)
      ) {
        showFeedback(STORAGE_STATE_NOT_KEPT, "error");
      } else if (states === null) {
        showFeedback(
          `Saved the login state from "${session.name}", but the saved login states could not be listed. Open Advanced options to try again.`,
        );
      } else {
        showFeedback(
          `Saved the login state from "${session.name}". Choose it under Start signed in to record signed in.`,
        );
      }
    } catch (e: any) {
      // The recorder refuses (500) a state it could not keep.
      if (refreshScopeRef.current === scope)
        showFeedback(e?.status === 500 ? STORAGE_STATE_NOT_KEPT : e.message, "error");
    } finally {
      if (refreshScopeRef.current === scope) setStorageStateSavingId(null);
    }
  };

  const handleDeleteBrowserRecordingSession = async (
    session: BrowserRecorderSession,
    event?: MouseEvent,
  ) => {
    event?.stopPropagation();
    if (
      typeof window !== "undefined" &&
      !(await confirmAction(
        "Delete this stopped browser recording? This cannot be undone.",
        { title: "Delete recording", confirmLabel: "Delete", destructive: true },
      ))
    ) {
      return;
    }
    try {
      await api.deleteBrowserRecording(session.id);
      if (!isCurrentProject()) return;
      if (browserPanelSessionId === session.id) {
        setBrowserPanelSessionId(null);
      }
      setBrowserSessions((prev) =>
        prev.filter((item) => item.id !== session.id),
      );
      showFeedback(`Deleted browser recording "${session.name}"`);
      void refresh();
    } catch (e: any) {
      if (isCurrentProject()) showFeedback(e.message, "error");
    }
  };

  const handleStart = async () => {
    try {
      const data: any = {
        name: sessionName || "Recording Session",
        project_id: activeProject?.id,
        exclude_static: true,
        capture_cookies: captureCookies,
      };
      if (recordingProfileId) data.recording_profile_id = recordingProfileId;
      if (includeHosts.trim()) {
        data.include_hosts = includeHosts
          .split(",")
          .map((h: string) => h.trim())
          .filter(Boolean);
      }
      if (excludeHosts.trim()) {
        data.exclude_hosts = excludeHosts
          .split(",")
          .map((h: string) => h.trim())
          .filter(Boolean);
      }
      const res = await api.startRecording(data);
      if (!isCurrentProject()) return;
      setActiveSession(res.session);
      showFeedback(
        "Recording started — configure your browser proxy and navigate the target site",
      );
    } catch (e: any) {
      if (isCurrentProject()) showFeedback(e.message, "error");
    }
  };

  const handleStop = async () => {
    if (!activeSession) return;
    try {
      await api.stopRecording(activeSession.id);
      if (!isCurrentProject()) return;
      const blocked = activeSession.rejected_requests ?? 0;
      showFeedback(
        `Recording stopped — ${activeSession.request_count ?? activeSession.requests?.length ?? 0} requests captured${blocked > 0 ? `, ${blocked} blocked (private address)` : ""}`,
      );
      setActiveSession(null);
      void refresh();
    } catch (e: any) {
      if (isCurrentProject()) showFeedback(e.message, "error");
    }
  };

  const handlePauseResume = async () => {
    if (!activeSession) return;
    try {
      const next =
        activeSession.status === "paused"
          ? await api.resumeRecording(activeSession.id)
          : await api.pauseRecording(activeSession.id);
      if (!isCurrentProject()) return;
      setActiveSession(next);
      showFeedback(
        next.status === "paused" ? "Recording paused" : "Recording resumed",
      );
    } catch (e: any) {
      if (isCurrentProject()) showFeedback(e.message, "error");
    }
  };

  const replaceRequest = (sessionId: string, updated: RecordedRequest) => {
    const apply = (session: RecordingSession | null) => {
      if (!session || session.id !== sessionId) return session;
      if (!session.requests) return session;
      return {
        ...session,
        requests: session.requests.map((req) =>
          req.seq === updated.seq ? updated : req,
        ),
      };
    };
    setSelectedSession(apply);
    setActiveSession(apply);
    setSessions((prev) => prev.map((session) => apply(session) || session));
    setSelectedRequest((current) =>
      current?.seq === updated.seq ? updated : current,
    );
  };

  const handleSelectSession = async (session: RecordingSession) => {
    if (selectedSession?.id === session.id) {
      setSelectedSession(null);
      setSelectedRequest(null);
      return;
    }
    setSelectedRequest(null);
    if (session.requests && session.requests.length > 0) {
      setSelectedSession(session);
      return;
    }
    try {
      const full = await api.getRecording(session.id, {
        includeRequests: true,
      });
      if (!isCurrentProject()) return;
      setSelectedSession({ ...full, requests: full.requests || [] });
    } catch {
      if (isCurrentProject()) setSelectedSession(session);
    }
  };

  const handleToggleRequestExcluded = async (
    session: RecordingSession,
    req: RecordedRequest,
  ) => {
    try {
      const updated = await api.updateRecordedRequest(session.id, req.seq, {
        excluded: !req.excluded,
      });
      if (!isCurrentProject()) return;
      replaceRequest(session.id, updated);
    } catch (e: any) {
      if (isCurrentProject()) showFeedback(e.message, "error");
    }
  };

  const handleSaveRequestDetails = async () => {
    if (!selectedSession || !selectedRequest) return;
    try {
      const updated = await api.updateRecordedRequest(
        selectedSession.id,
        selectedRequest.seq,
        requestDraft,
      );
      if (!isCurrentProject()) return;
      replaceRequest(selectedSession.id, updated);
      showFeedback(`Request #${updated.seq} updated`);
    } catch (e: any) {
      if (isCurrentProject()) showFeedback(e.message, "error");
    }
  };

  const runRecordingConversion = useCallback(
    async (session: RecordingSession) => {
      const sourceGeneration = advanceConvertedReviewGeneration();
      const sourceScope = refreshScopeRef.current;
      const sourceProjectId = sourceScope.projectId;
      const requestIsCurrent = () =>
        refreshScopeRef.current === sourceScope &&
        convertedReviewGenerationRef.current === sourceGeneration;
      try {
        setConvertedParameterizationApplying(false);
        setConvertedCredentialActionInProgress(false);
        convertedPreparedScriptRef.current = null;
        convertedSourceRecordingSessionIDRef.current = session.id;
        convertedBatchSaveOperationRef.current = null;
        setConvertedPreparedSecrets(null);
        const result = await convertTask.run(
          async () => {
            const response = await api.previewRecordingConversion(
              session.id,
              session.name,
              parameterizeInputs,
            );
            if (!requestIsCurrent()) {
              sourceSecretSessions.discardUnaccepted(
                sourceProjectId,
                response.source_secret_session_id,
              );
              throw new Error(
                "The recording source changed while conversion was running.",
              );
            }
            sourceSecretSessions.replace(
              "converted",
              sourceProjectId,
              response.source_secret_session_id,
            );
            return response;
          },
          { clearValue: true },
        );
        if (!requestIsCurrent()) return;
        const steps = recordedScriptSteps(recorderImportReviewScript(result)).length;
        const correlations = result.correlations?.length || 0;
        showFeedback(
          `Converted "${session.name}": ${steps} ${steps === 1 ? "step" : "steps"}, ${correlations} ${correlations === 1 ? "correlation" : "correlations"}. Review them below, then save.`,
        );
        window.requestAnimationFrame(() =>
          convertResultRef.current?.scrollIntoView?.({ block: "start" }),
        );
      } catch (e: any) {
        if (requestIsCurrent()) {
          sourceSecretSessions.clear("converted");
          showFeedback(e.message, "error");
        }
      }
    },
    [
      advanceConvertedReviewGeneration,
      convertedReviewGenerationRef,
      convertTask,
      parameterizeInputs,
      refreshScopeRef,
      api,
      sourceSecretSessions,
      showFeedback,
    ],
  );

  const handleConvert = (session: RecordingSession) => {
    void runRecordingConversion(session);
  };

  const handleExportHAR = async (session: RecordingSession) => {
    setFeedback("");
    try {
      const safeName = safeRecordingFileName(session.name);
      const filename = `${safeName}.har`;
      const nativeResourcePath = `/api/v1/recorder/${encodeURIComponent(session.id)}/export?format=har`;
      const nativeOutcome = await saveApiResourceViaBridge(
        nativeResourcePath,
        filename,
      );
      if (nativeOutcome === "saved") {
        if (isCurrentProject()) {
          showFeedback(`Exported "${session.name}" as HAR`);
        }
        return;
      }
      if (nativeOutcome === "cancelled") return;
      if (!isDesktopSurface() && hasDesktopApiCapability()) {
        await startDesktopBrowserDownload(nativeResourcePath, filename);
        if (isCurrentProject()) {
          showFeedback(
            `HAR download request opened for "${session.name}"; any server error will appear in the browser.`,
          );
        }
        return;
      }
      const res = await apiFetch(`/recorder/${session.id}/export?format=har`);
      if (!isCurrentProject()) return;
      if (!res.ok) {
        const payload = await res
          .json()
          .catch(() => ({ error: "Failed to export HAR" }));
        throw new Error(payload.error || "Failed to export HAR");
      }
      const blob = await res.blob();
      if (!isCurrentProject()) return;
      const outcome = await downloadBlob(blob, filename);
      if (outcome === "cancelled") return;
      showFeedback(`Exported "${session.name}" as HAR`);
    } catch (e: any) {
      if (isCurrentProject()) showFeedback(e.message, "error");
    }
  };

  const handleReviewSalesforceSession = async (session: RecordingSession) => {
    const sourceGeneration = advanceSalesforceReviewGeneration();
    const sourceScope = refreshScopeRef.current;
    const sourceProjectId = sourceScope.projectId;
    const requestIsCurrent = () =>
      refreshScopeRef.current === sourceScope &&
      salesforceReviewGenerationRef.current === sourceGeneration;
    try {
      setSalesforceParameterizationApplying(false);
      setSalesforceCredentialActionInProgress(false);
      salesforcePreparedScriptRef.current = null;
      setSalesforcePreparedSecrets(null);
      const result = await salesforceTask.run(
        async () => {
          const res = await apiFetch(
            `/recorder/${session.id}/export?format=har`,
          );
          if (!res.ok) {
            const payload = await res
              .json()
              .catch(() => ({ error: "Failed to export HAR" }));
            throw new Error(payload.error || "Failed to export HAR");
          }
          const blob = await res.blob();
          const file = new File(
            [blob],
            `${safeRecordingFileName(session.name)}.har`,
            { type: blob.type || "application/json" },
          );
          const response = await api.importSalesforce(file, {
            name: `${session.name} Salesforce HTTP`,
            base_url: session.base_url,
            project_id: sourceProjectId,
          });
          if (!requestIsCurrent()) {
            sourceSecretSessions.discardUnaccepted(
              sourceProjectId,
              response.source_secret_session_id,
            );
            throw new Error(
              "The recording source changed while Salesforce review was running.",
            );
          }
          sourceSecretSessions.replace(
            "salesforce",
            sourceProjectId,
            response.source_secret_session_id,
          );
          return response;
        },
        { clearValue: true },
      );
      if (!requestIsCurrent()) return;
      showFeedback(
        `Salesforce HTTP draft generated with ${result.script?.actions?.length || 0} actions`,
      );
    } catch (e: any) {
      if (requestIsCurrent()) {
        sourceSecretSessions.clear("salesforce");
        showFeedback(e.message, "error");
      }
    }
  };

  const buildReviewedScript = (): ScriptDefinition | null => {
    const script = recorderImportReviewScript(convertTask.value);
    if (!script) {
      return null;
    }
    return attachImportCorrelationSuggestions(
      script,
      "recorder",
      convertTask.value?.correlations,
    );
  };

  const handleApplyConvertedSecretBindings = async (
    bindings: SecretBindingSelection[],
  ) => {
    const result = convertTask.value;
    const script = buildReviewedScript();
    if (!result || !script) return;
    const sourceGeneration = convertedReviewGenerationRef.current;
    const sourceRecorderScope = refreshScopeRef.current;
    const resolved = await api.applySecretBindings({ script, bindings });
    if (
      convertedReviewGenerationRef.current !== sourceGeneration ||
      refreshScopeRef.current !== sourceRecorderScope
    ) {
      throw new Error(
        "The recording conversion changed while credentials were being prepared. Review the current draft and try again.",
      );
    }
    const preparedScript = attachImportCorrelationSuggestions(
      resolved.script,
      "recorder",
      result.correlations,
    );
    convertedPreparedScriptRef.current = preparedScript;
    convertTask.setValue({
      ...result,
      script: preparedScript,
      scripts: result.scripts?.length
        ? result.scripts.map((entry, index) =>
            index === 0 ? preparedScript : entry,
          )
        : [preparedScript],
      secret_binding_requirements: resolved.secret_binding_requirements,
      import_draft_expires_at: resolved.import_draft_expires_at,
    });
  };

  const handleConvertedProjectSecretsConfigured = useCallback(
    (names: readonly string[]) => {
      if (names.length > 0) {
        setConvertedConfiguredCredentialReviewIdentity(
          convertedCredentialReviewIdentity,
        );
      }
    },
    [convertedCredentialReviewIdentity],
  );

  const handleApplyConvertedParameterization = (
    parameterizedScript: ScriptDefinition,
    _diffs: unknown[],
    _selectedIds: string[],
    importDraftExpiresAt?: string,
  ) => {
    const result = convertTask.value;
    if (!result) return;
    advanceConvertedReviewGeneration();
    const preparedScript = attachImportCorrelationSuggestions(
      parameterizedScript,
      "recorder",
      result.correlations,
    );
    convertedPreparedScriptRef.current = null;
    convertTask.setValue({
      ...result,
      script: preparedScript,
      scripts: result.scripts?.length
        ? result.scripts.map((entry, index) =>
            index === 0 ? preparedScript : entry,
          )
        : [preparedScript],
      ...((result.scripts?.length ?? 1) <= 1
        ? { import_draft_expires_at: importDraftExpiresAt }
        : {}),
    });
  };

  const prepareConvertedScript = async (): Promise<ScriptDefinition | null> => {
    const requirements = convertTask.value?.secret_binding_requirements || [];
    if (requirements.length === 0) return buildReviewedScript();
    const reviewedScript = buildReviewedScript();
    const selectedBindingCount =
      convertedPreparedSecrets?.selectedBindingCount ??
      requirements.filter(
        (requirement) => requirement.destination_value_preserved !== true,
      ).length;
    const sourceGeneration = convertedReviewGenerationRef.current;
    const sourceRecorderScope = refreshScopeRef.current;
    convertedPreparedScriptRef.current = null;
    const committed = await convertedCredentialReviewRef.current?.commit();
    if (
      committed &&
      selectedBindingCount === 0 &&
      convertedReviewGenerationRef.current === sourceGeneration &&
      refreshScopeRef.current === sourceRecorderScope
    ) {
      return reviewedScript;
    }
    return committed &&
      convertedReviewGenerationRef.current === sourceGeneration &&
      refreshScopeRef.current === sourceRecorderScope
      ? convertedPreparedScriptRef.current
      : null;
  };

  const handleEditConvertedScript = async () => {
    if (convertedCredentialActionInProgress) return;
    setConvertedCredentialActionInProgress(true);
    try {
      const result = convertTask.value;
      // A conversion that is already a stored script opens as that script; as
      // a draft, saving it would make a second copy.
      if (recorderImportStoredScript(result)) {
        await handleSaveConvertedScript();
        return;
      }
      const reviewedScript = buildReviewedScript();
      if (
        credentialPreservingImportDraftRequiresSave(
          result,
          reviewedScript,
          result?.secret_binding_requirements,
        )
      ) {
        if (!onImported) {
          showFeedback(
            "Save this credential-preserving import before opening it in the Visual Editor.",
            "error",
          );
          return;
        }
        await handleSaveConvertedScript();
        return;
      }
      const script = await prepareConvertedScript();
      if (script) onEdit?.(script);
    } finally {
      setConvertedCredentialActionInProgress(false);
    }
  };

  const handleSaveConvertedScript = async () => {
    if (convertedSaveInFlightRef.current) return;
    convertedSaveInFlightRef.current = true;
    setConvertedSaveInProgress(true);
    const sourceGeneration = convertedReviewGenerationRef.current;
    const sourceRecorderScope = refreshScopeRef.current;
    const sourceProjectId = sourceRecorderScope.projectId;
    const sourceRecordingSessionId =
      convertedSourceRecordingSessionIDRef.current || undefined;
    const requestIsCurrent = () =>
      studioActiveRef.current &&
      convertedReviewGenerationRef.current === sourceGeneration &&
      refreshScopeRef.current === sourceRecorderScope;
    try {
      const hasSecretRequirements =
        (convertTask.value?.secret_binding_requirements?.length || 0) > 0;
      const script = await prepareConvertedScript();
      if (!requestIsCurrent()) return;
      if (!script) {
        if (hasSecretRequirements) return;
        const stored = recorderImportStoredScript(convertTask.value);
        if (stored) {
          onImported?.({ scripts: [stored] });
          showFeedback(
            `Script "${stored.name}" is already in the script library`,
          );
        }
        return;
      }
      const stored = recorderImportStoredScript(convertTask.value);
      if (stored) {
        onImported?.({
          scripts: [withFallbackPersistedScript(stored, script)],
        });
        showFeedback(
          `Script "${stored.name}" is already in the script library`,
        );
        return;
      }
      if (!sourceRecordingSessionId) {
        throw new Error(
          "The source recording session is unavailable. Generate the recording preview again before saving.",
        );
      }
      const destinationFolderId = script.folder_id ?? folderId ?? null;
      const retained = convertedBatchSaveOperationRef.current;
      const operationId = retained &&
        retained.generation === sourceGeneration &&
        retained.projectId === sourceProjectId &&
        retained.folderId === destinationFolderId &&
        retained.recordingSessionId === sourceRecordingSessionId
        ? retained.operationId
        : `recording-proxy-import-${createReplaySessionId()}`;
      convertedBatchSaveOperationRef.current = {
        generation: sourceGeneration,
        projectId: sourceProjectId,
        folderId: destinationFolderId,
        recordingSessionId: sourceRecordingSessionId,
        operationId,
      };
      const response = await api.commitGenericImportBatch({
        scripts: [script],
        folder_id: destinationFolderId,
        operation_id: operationId,
        source_profile: "recorder",
        source_recording_session_ids: [sourceRecordingSessionId],
      });
      if (response.count !== 1 || response.scripts.length !== 1) {
        throw new Error(
          "The atomic recording import save did not return the stored script. Retry the same save to reconcile its result.",
        );
      }
      if (convertedBatchSaveOperationRef.current?.operationId === operationId) {
        convertedBatchSaveOperationRef.current = null;
      }
      const savedScript = response.scripts[0];
      const persistedScript = withFallbackPersistedScript(savedScript, script);
      onImported?.({
        scripts: [persistedScript],
        ...(!requestIsCurrent() ? { stayInImportReview: true } : {}),
      });
      if (!requestIsCurrent()) return;
      showFeedback(
        `Script "${script.name}" created with ${convertTask.value?.correlations?.length || 0} correlations detected`,
      );
    } catch (e: any) {
      if (requestIsCurrent()) showFeedback(e.message, "error");
    } finally {
      convertedSaveInFlightRef.current = false;
      if (studioActiveRef.current) setConvertedSaveInProgress(false);
    }
  };

  const handleApplySalesforceSecretBindings = async (
    bindings: SecretBindingSelection[],
  ) => {
    const result = salesforceTask.value;
    const script = result?.script || result?.scripts?.[0];
    if (!result || !script) return;
    const sourceGeneration = salesforceReviewGenerationRef.current;
    const sourceRecorderScope = refreshScopeRef.current;
    const resolved = await api.applySecretBindings({ script, bindings });
    if (
      salesforceReviewGenerationRef.current !== sourceGeneration ||
      refreshScopeRef.current !== sourceRecorderScope
    ) {
      throw new Error(
        "The Salesforce draft changed while credentials were being prepared. Review the current draft and try again.",
      );
    }
    salesforcePreparedScriptRef.current = resolved.script;
    salesforceTask.setValue({
      ...result,
      script: resolved.script,
      scripts: result.scripts?.length
        ? result.scripts.map((entry, index) =>
            index === 0 ? resolved.script : entry,
          )
        : [resolved.script],
      secret_binding_requirements: resolved.secret_binding_requirements,
      import_draft_expires_at: resolved.import_draft_expires_at,
    });
  };

  const handleSalesforceProjectSecretsConfigured = useCallback(
    (names: readonly string[]) => {
      if (names.length > 0) {
        setSalesforceConfiguredCredentialReviewIdentity(
          salesforceCredentialReviewIdentity,
        );
      }
    },
    [salesforceCredentialReviewIdentity],
  );

  const handleApplySalesforceParameterization = (
    parameterizedScript: ScriptDefinition,
    _diffs: unknown[],
    _selectedIds: string[],
    importDraftExpiresAt?: string,
  ) => {
    const result = salesforceTask.value;
    if (!result) return;
    advanceSalesforceReviewGeneration();
    salesforcePreparedScriptRef.current = null;
    salesforceTask.setValue({
      ...result,
      script: parameterizedScript,
      scripts: result.scripts?.length
        ? result.scripts.map((entry, index) =>
            index === 0 ? parameterizedScript : entry,
          )
        : [parameterizedScript],
      ...((result.scripts?.length ?? 1) <= 1
        ? { import_draft_expires_at: importDraftExpiresAt }
        : {}),
    });
  };

  const prepareSalesforceScript =
    async (): Promise<ScriptDefinition | null> => {
      const result = salesforceTask.value;
      const script = result?.script || result?.scripts?.[0];
      if (!script) return null;
      if ((result?.secret_binding_requirements?.length || 0) === 0)
        return script;
      const selectedBindingCount =
        salesforcePreparedSecrets?.selectedBindingCount ??
        result?.secret_binding_requirements?.filter(
          (requirement) => requirement.destination_value_preserved !== true,
        ).length ??
        0;
      const sourceGeneration = salesforceReviewGenerationRef.current;
      const sourceRecorderScope = refreshScopeRef.current;
      salesforcePreparedScriptRef.current = null;
      const committed = await salesforceCredentialReviewRef.current?.commit();
      if (
        committed &&
        selectedBindingCount === 0 &&
        salesforceReviewGenerationRef.current === sourceGeneration &&
        refreshScopeRef.current === sourceRecorderScope
      ) {
        return script;
      }
      return committed &&
        salesforceReviewGenerationRef.current === sourceGeneration &&
        refreshScopeRef.current === sourceRecorderScope
        ? salesforcePreparedScriptRef.current
        : null;
    };

  const handleEditSalesforceScript = async () => {
    if (salesforceCredentialActionInProgress) return;
    setSalesforceCredentialActionInProgress(true);
    try {
      const result = salesforceTask.value;
      const reviewedScript = result?.script || result?.scripts?.[0];
      if (
        credentialPreservingImportDraftRequiresSave(
          result,
          reviewedScript,
          result?.secret_binding_requirements,
        )
      ) {
        if (!onImported) {
          showFeedback(
            "Save this credential-preserving import before opening it in the Visual Editor.",
            "error",
          );
          return;
        }
        await handleSaveSalesforceScript();
        return;
      }
      const script = await prepareSalesforceScript();
      if (script) onEdit?.(script);
    } finally {
      setSalesforceCredentialActionInProgress(false);
    }
  };

  const handleSaveSalesforceScript = async () => {
    if (salesforceSaveInFlightRef.current) return;
    salesforceSaveInFlightRef.current = true;
    setSalesforceSaveInProgress(true);
    const sourceGeneration = salesforceReviewGenerationRef.current;
    const sourceRecorderScope = refreshScopeRef.current;
    const sourceProjectId = sourceRecorderScope.projectId;
    const requestIsCurrent = () =>
      studioActiveRef.current &&
      salesforceReviewGenerationRef.current === sourceGeneration &&
      refreshScopeRef.current === sourceRecorderScope;
    try {
      const script = await prepareSalesforceScript();
      if (!script || !requestIsCurrent()) return;
      // The Salesforce-analyzed draft is not a verbatim conversion of the
      // recording, so it can never pass the server's provenance attestation —
      // save it without claiming the recording session as its source. The
      // stable batch receipt makes a lost successful response safe to retry.
      const destinationFolderId = script.folder_id ?? folderId ?? null;
      const retained = salesforceBatchSaveOperationRef.current;
      const operationId = retained &&
        retained.generation === sourceGeneration &&
        retained.projectId === sourceProjectId &&
        retained.folderId === destinationFolderId
        ? retained.operationId
        : `recording-salesforce-import-${createReplaySessionId()}`;
      salesforceBatchSaveOperationRef.current = {
        generation: sourceGeneration,
        projectId: sourceProjectId,
        folderId: destinationFolderId,
        operationId,
      };
      const response = await api.commitGenericImportBatch({
        scripts: [script],
        folder_id: destinationFolderId,
        operation_id: operationId,
        source_profile: "salesforce",
      });
      if (response.count !== 1 || response.scripts.length !== 1) {
        throw new Error(
          "The atomic Salesforce import save did not return the stored script. Retry the same save to reconcile its result.",
        );
      }
      if (salesforceBatchSaveOperationRef.current?.operationId === operationId) {
        salesforceBatchSaveOperationRef.current = null;
      }
      const savedScript = response.scripts[0];
      onImported?.({
        scripts: [withFallbackPersistedScript(savedScript, script)],
        ...(!requestIsCurrent() ? { stayInImportReview: true } : {}),
      });
      if (!requestIsCurrent()) return;
      showFeedback(
        `Salesforce HTTP script "${script.name}" saved to the script library`,
      );
    } catch (e: any) {
      if (requestIsCurrent()) showFeedback(e.message, "error");
    } finally {
      salesforceSaveInFlightRef.current = false;
      if (studioActiveRef.current) setSalesforceSaveInProgress(false);
    }
  };

  const handleDelete = async (id: string) => {
    if (
      typeof window !== "undefined" &&
      !(await confirmAction(
        "Delete this stopped recording session? This cannot be undone.",
        { title: "Delete recording session", confirmLabel: "Delete", destructive: true },
      ))
    ) {
      return;
    }
    try {
      await api.deleteRecording(id);
      if (!isCurrentProject()) return;
      if (selectedSession?.id === id) setSelectedSession(null);
      void refresh();
    } catch (e: any) {
      if (isCurrentProject()) showFeedback(e.message, "error");
    }
  };

  const stoppedSessions = sessions.filter((s) => s.status === "stopped");
  // One list of both methods' finished recordings, newest first.
  const recentRecordings: RecentRecording[] = [
    ...browserSessions
      .filter((s) => s.status === "stopped" || s.status === "failed")
      .map((session): RecentRecording => ({
        kind: "browser",
        session,
        at: session.stopped_at || session.started_at,
      })),
    ...stoppedSessions.map((session): RecentRecording => ({
      kind: "proxy",
      session,
      at: session.stopped_at || session.started_at,
    })),
  ].sort((a, b) => recordingTime(b.at) - recordingTime(a.at));
  const convertResult = convertTask.value;
  // Informational observations (for example origins that advertise HTTP/3);
  // warnings and blockers arrive in convertResult.warnings.
  const recordingNotes = (convertResult?.diagnostics || []).filter(
    (item) => item.severity === "info",
  );
  const convertedStoredScript = recorderImportStoredScript(convertResult);
  const convertedReviewScript = recorderImportReviewScript(convertResult);
  const convertedScript = convertedReviewScript || convertedStoredScript;
  const convertedSteps = useMemo(
    () => recordedScriptSteps(convertedReviewScript),
    [convertedReviewScript],
  );
  const convertedExcludedRequests = Number(
    (convertResult?.stats as { requests_excluded?: unknown } | undefined)
      ?.requests_excluded ?? 0,
  ) || 0;
  // "No correlation found" is information; the rest stay warnings.
  const convertedWarnings = (convertResult?.warnings || []).filter(
    (warning) => !isNoReusableCorrelationNote(warning),
  );
  const convertedInfoNotes = (convertResult?.warnings || []).filter(
    isNoReusableCorrelationNote,
  );
  const convertedSecretRequirements =
    convertResult?.secret_binding_requirements || [];
  const convertedRequiresCredentialPreservingSave =
    !convertedStoredScript &&
    credentialPreservingImportDraftRequiresSave(
      convertResult,
      convertedReviewScript,
      convertedSecretRequirements,
    );
  const convertedImportDraftExpiry = convertedStoredScript
    ? null
    : importDraftExpiryStatus(convertResult?.import_draft_expires_at);
  const convertedCredentialsValid =
    convertedSecretRequirements.length === 0 ||
    convertedPreparedSecrets?.valid === true;
  const convertedDeferredSecretCount =
    convertedPreparedSecrets?.configureLaterCount ??
    new Set(
      convertedSecretRequirements
        .filter(
          (requirement) => requirement.destination_value_preserved !== true,
        )
        .map((requirement) => requirement.suggested_name),
    ).size;
  const convertedSaveLabel = convertedSaveInProgress
    ? "Saving…"
    : convertedDeferredSecretCount > 0
      ? `Save with ${convertedDeferredSecretCount} ${convertedDeferredSecretCount === 1 ? "secret" : "secrets"} to configure`
      : "Save to script library";
  const converting = convertTask.pending;
  const salesforceResult = salesforceTask.value;
  const salesforceScript =
    salesforceResult?.script || salesforceResult?.scripts?.[0];
  const salesforceSecretRequirements =
    salesforceResult?.secret_binding_requirements || [];
  const salesforceRequiresCredentialPreservingSave =
    credentialPreservingImportDraftRequiresSave(
      salesforceResult,
      salesforceScript,
      salesforceSecretRequirements,
    );
  const salesforceImportDraftExpiry = importDraftExpiryStatus(
    salesforceResult?.import_draft_expires_at,
  );
  const salesforceCredentialsValid =
    salesforceSecretRequirements.length === 0 ||
    salesforcePreparedSecrets?.valid === true;
  const salesforceDeferredSecretCount =
    salesforcePreparedSecrets?.configureLaterCount ??
    new Set(
      salesforceSecretRequirements
        .filter(
          (requirement) => requirement.destination_value_preserved !== true,
        )
        .map((requirement) => requirement.suggested_name),
    ).size;
  const salesforceSaveLabel = salesforceSaveInProgress
    ? "Saving…"
    : salesforceDeferredSecretCount > 0
      ? `Save with ${salesforceDeferredSecretCount} ${salesforceDeferredSecretCount === 1 ? "secret" : "secrets"} to configure`
      : "Save to script library";
  const reviewingSalesforce = salesforceTask.pending;
  const browserUnavailableReason = browserAvailability?.reason?.trim() || "";
  const browserUnavailableCode = browserAvailability?.code?.trim() || "";
  const browserUnavailableDueToDisplay =
    browserUnavailableCode === "display_missing" ||
    browserUnavailableReason.toLowerCase().includes("no display detected");
  const canRetryBrowserInstall = browserRecorderInstallRetryable(
    browserAvailability,
    installState,
  );
  // Installing fixes only this controller lacking the browser, and only when
  // it can install one; a paired agent's missing browser is the workstation's.
  const browserInstallOffered =
    !api.browserRecorderBridge &&
    !!installState &&
    (canRetryBrowserInstall || installState.status === "installing");
  // A profile's host filters add to the typed ones: the proxy merges them when
  // a recording starts, and a browser recording's HTTP conversion applies them.
  const profileIncludeHosts = hostFilterList(recordingProfile?.include_hosts);
  const profileExcludeHosts = hostFilterList(recordingProfile?.exclude_hosts);
  const profileBrowserHostsNote =
    profileIncludeHosts.length > 0 || profileExcludeHosts.length > 0
      ? `Recording profile “${recordingProfile?.name}” also filters the HTTP script converted from this recording: ${[
          profileIncludeHosts.length > 0
            ? `keeps only ${profileIncludeHosts.join(", ")}`
            : "",
          profileExcludeHosts.length > 0
            ? `leaves out ${profileExcludeHosts.join(", ")}`
            : "",
        ]
          .filter(Boolean)
          .join("; ")}.`
      : "";
  // The browser control panel holds the destination while it is open; the
  // proxy conversion review shows it otherwise (never both at once).
  const destinationInBrowserPanel =
    !!browserPanelSessionId && recordingMode === "browser";
  const proxyOffered = proxyAvailability?.available === true;
  // A live proxy session keeps the proxy method usable so it can be stopped.
  const proxyCanRecord = proxyOffered || !!activeSession;
  const proxyUnavailableNote = proxyUnavailableExplanation(proxyAvailability);
  const browserLaunchDescription = api.browserRecorderBridge
    ? "Opens Chromium on your computer through the Recording Agent and records what you do."
    : !browserAvailability?.available
      ? "Records what you do in a Chromium browser and turns it into a script."
      : localClient === false
        ? "Opens Chromium on the controller's host and records what you do."
        : "Opens Chromium on this computer and records what you do.";
  const browserWindowLocation = api.browserRecorderBridge
    ? "Chromium opens on your computer"
    : localClient === false
      ? "Chromium opens on the controller's host"
      : "Chromium opens on this computer";
  const installStatusTone = installState
    ? browserRecorderInstallTone(installState.status)
    : "info";
  const installStatusVisible = !!installState && installState.status !== "idle";
  const bridgeCommand = bridgeRecordAgentCommand(bridgeOrigin);
  const agentDownload = recordingAgentDownloadConfig();
  const agentDownloadHref =
    agentDownload.managed && recordingAgentPlatform
      ? `${agentDownload.url}?platform=${encodeURIComponent(recordingAgentPlatform)}`
      : agentDownload.url;

  return (
    <div className="page recording-studio">
      <div className="page-header">
        <div>
          <h2>Recording Studio</h2>
          <p className="page-subtitle">
            Record a browser session and convert it to a test script
          </p>
        </div>
        {onClose && (
          <button
            type="button"
            className="btn btn-secondary"
            onClick={() => {
              studioActiveRef.current = false;
              convertedReviewGenerationRef.current += 1;
              salesforceReviewGenerationRef.current += 1;
              void convertedCredentialReviewRef.current?.discardSourceSession();
              void salesforceCredentialReviewRef.current?.discardSourceSession();
              onClose();
            }}
          >
            Back
          </button>
        )}
      </div>

      {feedback && (
        <div className={`rec-feedback ${feedbackType}`}>{feedback}</div>
      )}

      {proxyCanRecord ? (
        <PageViewToggle
          value={recordingMode}
          ariaLabel="Recording method"
          className="rec-mode-picker script-page-toggle"
          onChange={(value) => setRecordingMode(value)}
          options={[
            { value: "browser", label: "Browser actions" },
            { value: "proxy", label: "HTTP proxy (advanced)" },
          ]}
        />
      ) : proxyUnavailableNote ? (
        <div className="rec-method-note" data-testid="recording-proxy-unavailable">
          <p>{proxyUnavailableNote.summary}</p>
          {proxyUnavailableNote.details && (
            <details className="rec-admin-details">
              <summary>Details for administrators</summary>
              <p>{proxyUnavailableNote.details}</p>
            </details>
          )}
        </div>
      ) : null}
      <RecordingProfilesCard
        projectId={activeProject?.id}
        selectedId={recordingProfileId}
        onSelect={selectRecordingProfile}
      />
      <div className="rec-recorder-options-grid" style={{ gridTemplateColumns: 'minmax(0, 1fr)' }}>
        <div className="rec-controls-card rec-browser-launcher" hidden={recordingMode !== 'browser'}>
          <div className="rec-option-heading">
            <h3 className="rec-section-title">Record a browser session</h3>
            <p>{browserLaunchDescription}</p>
          </div>
          {!browserAvailability ? (
            <p className="rec-browser-note" role="status">
              Checking how this controller can record…
            </p>
          ) : !browserAvailability.available ? (
            <div
              className="rec-browser-workstation"
              data-testid="browser-recorder-unavailable"
            >
              {browserInstallOffered && (
                <div className="rec-browser-unavailable">
                  <div className="rec-browser-unavailable-title">
                    The recording browser isn't installed on this controller
                  </div>
                  <div>
                    Install it here, or record from your workstation below.
                  </div>
                  {installStatusVisible && (
                    <div
                      className={`rec-install-status ${installStatusTone}`}
                      role={installState.status === "failed" ? "alert" : "status"}
                    >
                      <strong>
                        {browserRecorderInstallLabel(installState.status)}
                      </strong>
                      {installState.error && <span>{installState.error}</span>}
                    </div>
                  )}
                  {installState && canRetryBrowserInstall && (
                    <button
                      className="btn btn-secondary btn-sm"
                      onClick={handleInstallBrowsers}
                      disabled={installState.status === "installing"}
                    >
                      {installState.status === "installing"
                        ? "Installing…"
                        : "Install recording browser"}
                    </button>
                  )}
                </div>
              )}
              {api.browserRecorderBridge && browserUnavailableReason && (
                <div className="rec-browser-unavailable" role="alert">
                  {browserUnavailableReason}
                </div>
              )}
              <div
                className="rec-bridge-panel"
                data-testid="browser-recorder-bridge-panel"
              >
                <div className="rec-browser-unavailable-title">
                  {browserInstallOffered
                    ? "Or record from your workstation"
                    : "Record from your workstation"}
                </div>
                <p className="rec-bridge-lead">
                  {api.browserRecorderBridge
                    ? "Run the command again on your computer, then connect to the Recording Agent."
                    : browserInstallOffered
                      ? "The browser opens on your computer instead, through the APEX Load Recording Agent."
                      : "This controller can't open a browser for you, so the browser opens on your computer instead, through the APEX Load Recording Agent."}
                </p>
                <div className="rec-bridge-steps">
                  <div className="rec-bridge-step">
                    <div className="rec-bridge-step-number">1</div>
                    <div className="rec-bridge-step-content">
                      <div className="rec-bridge-step-title">
                        Download the Recording Agent
                      </div>
                      {agentDownload.managed && (
                        <div className="rec-form-row rec-agent-platform-row">
                          <div className="rec-label">Platform</div>
                          <select
                            className="rec-input"
                            value={recordingAgentPlatform}
                            onChange={(event) =>
                              setRecordingAgentPlatform(event.target.value)
                            }
                            aria-label="Recording Agent platform"
                          >
                            <option value="">
                              {recordingAgentPlatformDetecting
                                ? "Detecting platform…"
                                : "Choose platform"}
                            </option>
                            {CLIENT_DOWNLOAD_PLATFORMS.map((platform) => (
                              <option
                                key={platform.value}
                                value={platform.value}
                              >
                                {platform.label}
                              </option>
                            ))}
                          </select>
                        </div>
                      )}
                      <div className="rec-bridge-install-note">
                        <a
                          href={agentDownloadHref}
                          target="_blank"
                          rel="noreferrer"
                          data-desktop-managed-resource={
                            agentDownload.managed ? "true" : undefined
                          }
                          onClick={(event) =>
                            handleDownloadRecordingAgent(
                              event,
                              agentDownload.managed,
                            )
                          }
                        >
                          Download the APEX Load Recording Agent
                          {agentDownload.managed && recordingAgentPlatform
                            ? ` for ${platformLabel(recordingAgentPlatform)}`
                            : ""}
                        </a>
                      </div>
                      {agentDownload.managed && (
                        <div className="rec-bridge-help">
                          {recordingAgentChecksum?.sha256 ? (
                            <>
                              SHA-256{" "}
                              <code>{recordingAgentChecksum.sha256}</code>
                              {recordingAgentChecksum.filename ? (
                                <>
                                  {" "}
                                  for{" "}
                                  <code>{recordingAgentChecksum.filename}</code>
                                </>
                              ) : null}
                            </>
                          ) : (
                            recordingAgentChecksumStatus ||
                            (recordingAgentPlatform
                              ? "SHA-256 will appear when the package manifest is staged."
                              : "Choose a platform to show the package SHA-256.")
                          )}
                        </div>
                      )}
                    </div>
                  </div>
                  <div className="rec-bridge-step">
                    <div className="rec-bridge-step-number">2</div>
                    <div className="rec-bridge-step-content">
                      <div className="rec-bridge-step-title">
                        Run the local command
                      </div>
                      <div className="rec-bridge-step-desc">
                        Paste this command into Terminal or PowerShell on your
                        workstation.
                      </div>
                      <div
                        className="rec-bridge-terminal"
                        data-testid="browser-recorder-bridge-terminal"
                      >
                        <div className="rec-bridge-terminal-header">
                          <span>Local terminal command</span>
                          <span>Token entered at prompt</span>
                        </div>
                        <div className="rec-bridge-command-row">
                          <span className="rec-terminal-prompt">$</span>
                          <code
                            className="rec-command"
                            data-testid="browser-recorder-bridge-command"
                          >
                            {bridgeCommand}
                          </code>
                        </div>
                        <div className="rec-bridge-command-actions">
                          <button
                            type="button"
                            className="btn btn-secondary btn-sm"
                            onClick={handleCopyBridgeCommand}
                          >
                            Copy terminal command
                          </button>
                          <button
                            type="button"
                            className="rec-inline-action"
                            onClick={handleRegenerateBridgeToken}
                          >
                            Regenerate token
                          </button>
                        </div>
                      </div>
                      <div className="rec-bridge-help">
                        When the agent asks, paste this pairing token. It is not
                        included in the command or process arguments.
                      </div>
                      <div className="rec-bridge-command-row">
                        <span className="rec-terminal-prompt">#</span>
                        {/* Masked so screen shares and recordings do not capture it. */}
                        <code
                          className="rec-command"
                          data-testid="browser-recorder-bridge-token"
                        >
                          {bridgeTokenRevealed ? (
                            bridgeToken
                          ) : (
                            <>
                              <span aria-hidden="true">
                                {"•".repeat(Math.min(Math.max(bridgeToken.length, 8), 32))}
                              </span>
                              <span className="sr-only">Pairing token hidden</span>
                            </>
                          )}
                        </code>
                      </div>
                      <div className="rec-bridge-command-actions">
                        <button
                          type="button"
                          className="btn btn-secondary btn-sm"
                          onClick={handleCopyBridgeToken}
                        >
                          Copy pairing token
                        </button>
                        <button
                          type="button"
                          className="rec-inline-action"
                          aria-pressed={bridgeTokenRevealed}
                          onClick={() => setBridgeTokenRevealed((revealed) => !revealed)}
                        >
                          {bridgeTokenRevealed ? "Hide token" : "Show token"}
                        </button>
                      </div>
                    </div>
                  </div>
                  <div className="rec-bridge-step">
                    <div className="rec-bridge-step-number">3</div>
                    <div className="rec-bridge-step-content">
                      <div className="rec-bridge-step-title">
                        Connect to the agent
                      </div>
                      <label className="rec-form-row">
                        <span className="rec-label">Agent address</span>
                        <input
                          className="rec-input"
                          value={bridgeOrigin}
                          onChange={(e) => setBridgeOrigin(e.target.value)}
                          placeholder="http://127.0.0.1:27777"
                        />
                      </label>
                      <button
                        className="btn btn-secondary btn-sm"
                        onClick={handleConnectBridge}
                        disabled={bridgeVerifying || bridgePairing}
                      >
                        {bridgeVerifying
                          ? "Checking the agent…"
                          : bridgePairing
                            ? "Connecting…"
                            : "Connect"}
                      </button>
                    </div>
                  </div>
                </div>
                {bridgeStatus && (
                  <div className={`rec-bridge-status ${bridgeStatusTone}`}>
                    {bridgeStatus}
                  </div>
                )}
              </div>
              {!api.browserRecorderBridge && browserUnavailableReason && (
                <details className="rec-admin-details">
                  <summary>Details for administrators</summary>
                  <p>{browserUnavailableReason}</p>
                  {browserUnavailableDueToDisplay && (
                    <p>
                      Installing the browser cannot add a display. Run the
                      controller on a desktop host with DISPLAY or
                      WAYLAND_DISPLAY to record on it.
                    </p>
                  )}
                  {installStatusVisible && !browserInstallOffered && (
                    <p>
                      {browserRecorderInstallLabel(installState.status)}
                      {installState.error ? `: ${installState.error}` : ""}
                    </p>
                  )}
                </details>
              )}
              {proxyOffered && (
                <p className="rec-browser-note">
                  You can also record HTTP traffic with the{" "}
                  <button
                    type="button"
                    className="rec-inline-action"
                    onClick={() => setRecordingMode("proxy")}
                  >
                    HTTP proxy
                  </button>{" "}
                  method.
                </p>
              )}
            </div>
          ) : (
            <div className="rec-start-form">
              {api.browserRecorderBridge && (
                <div
                  className="rec-bridge-connected"
                  data-testid="browser-recorder-bridge-connected"
                >
                  <span>Local bridge: {api.browserRecorderBridge.origin}</span>
                  <button
                    type="button"
                    className="rec-inline-action"
                    onClick={handleDisconnectBridge}
                  >
                    Disconnect
                  </button>
                </div>
              )}
              <div className="rec-form-row">
                <div className="rec-label">Start URL</div>
                <div className="rec-url-input-group">
                  <select
                    className="rec-input rec-url-scheme-select"
                    value={browserStartScheme}
                    onChange={(e) =>
                      setBrowserStartScheme(e.target.value as HttpURLScheme)
                    }
                    aria-label="Start URL scheme"
                  >
                    <option value="https://">https://</option>
                    <option value="http://">http://</option>
                  </select>
                  <input
                    className="rec-input"
                    value={browserStartAddress}
                    onChange={(e) =>
                      handleBrowserStartAddressChange(e.target.value)
                    }
                    placeholder="app.example.com/login"
                    aria-label="Start URL address"
                    ref={browserURLRef}
                    aria-invalid={!!browserURLError}
                    aria-describedby={browserURLError ? 'browser-url-error' : undefined}
                  />
                </div>
              </div>
              {browserURLError && <p id="browser-url-error" role="alert">{browserURLError}</p>}
              <div className="rec-form-row">
                <label className="rec-label" htmlFor="rec-browser-session-name">Session name</label>
                <input
                  id="rec-browser-session-name"
                  className="rec-input"
                  value={browserName}
                  onChange={(e) => setBrowserName(e.target.value)}
                  placeholder="e.g. Login & Checkout Flow"
                />
              </div>
              <div className="rec-form-row">
                <div className="rec-label">Emulated device preset</div>
                <select
                  className="rec-input"
                  value={browserDevice}
                  onChange={(e) => setBrowserDevice(e.target.value)}
                  aria-label="Emulated device preset"
                >
                  <option value="">Desktop (default)</option>
                  {browserDevices
                    .filter((d) => d.name !== "desktop_1080p")
                    .map((d) => (
                      <option key={d.name} value={d.name}>
                        {d.name.replace(/_/g, " ")} ({d.platform})
                      </option>
                    ))}
                </select>
              </div>
              <p className="rec-browser-emulation-note">
                Presets emulate viewport, mobile mode and touch on the recording
                workstation. They do not qualify Chrome on a physical iPad.
              </p>
              <label className="rec-checkbox-row">
                <input
                  type="checkbox"
                  checked={browserCaptureHAR}
                  onChange={(e) => setBrowserCaptureHAR(e.target.checked)}
                />
                <span>
                  Capture side-car HAR (network traffic for protocol-level
                  scripting)
                </span>
              </label>

              <button
                type="button"
                className="rec-disclosure"
                aria-expanded={browserOptionsOpen}
                aria-controls="rec-browser-options"
                onClick={() => setBrowserOptionsOpen((v) => !v)}
              >
                Advanced options
              </button>
              {browserOptionsOpen && (
                <div className="rec-browser-options" id="rec-browser-options">
                  <label className="rec-checkbox-row">
                    <input
                      type="checkbox"
                      checked={browserSidebar}
                      onChange={(e) => setBrowserSidebar(e.target.checked)}
                    />
                    <span>
                      Show in-page advanced sidebar{" "}
                      <span className="rec-hint">
                        (section, transactions, notes, recent steps — uncheck
                        for the minimal pause/assert toolbar)
                      </span>
                    </span>
                  </label>
                  <label className="rec-checkbox-row">
                    <input
                      type="checkbox"
                      checked={browserScreenshots}
                      onChange={(e) => setBrowserScreenshots(e.target.checked)}
                    />
                    <span>Capture per-step screenshot thumbnails</span>
                  </label>
                  <label className="rec-checkbox-row">
                    <input
                      type="checkbox"
                      checked={browserWSPayloads}
                      onChange={(e) => setBrowserWSPayloads(e.target.checked)}
                    />
                    <span>Capture WebSocket frame payloads (scrubbed)</span>
                  </label>
                  <label className="rec-checkbox-row">
                    <input
                      type="checkbox"
                      checked={browserDisableQUIC}
                      onChange={(e) => setBrowserDisableQUIC(e.target.checked)}
                    />
                    <span>Disable HTTP/3 (QUIC) in the recorded browser</span>
                  </label>
                  <div className="rec-form-row">
                    <div className="rec-label">
                      Exclude hosts{" "}
                      <span className="rec-hint">
                        (comma-separated, HAR noise filter)
                      </span>
                    </div>
                    <input
                      className="rec-input"
                      aria-label="Exclude hosts"
                      aria-describedby={profileBrowserHostsNote ? "rec-profile-browser-hosts" : undefined}
                      value={browserExcludeHosts}
                      onChange={(e) => setBrowserExcludeHosts(e.target.value)}
                      placeholder="analytics.google.com, cdn.example.com"
                    />
                    {profileBrowserHostsNote && (
                      <span className="rec-hint" id="rec-profile-browser-hosts">
                        {profileBrowserHostsNote}
                      </span>
                    )}
                  </div>
                  {storageStatesListed && (
                    <div className="rec-form-row">
                      <label className="rec-label" htmlFor="rec-browser-storage-state">
                        Start signed in{" "}
                        <span className="rec-hint">
                          (a login state saved from an earlier recording)
                        </span>
                      </label>
                      <select
                        id="rec-browser-storage-state"
                        className="rec-input"
                        value={browserStorageState}
                        onChange={(e) => setBrowserStorageState(e.target.value)}
                      >
                        <option value="">No, start signed out</option>
                        {storageStates.map((state) => (
                          <option key={state.path} value={state.path}>
                            {`${state.name} · saved ${formatDateTime(state.saved_at)}`}
                          </option>
                        ))}
                      </select>
                      {storageStates.length === 0 && (
                        <span className="rec-hint" data-testid="rec-storage-state-empty">
                          None saved yet. Stop a recording after signing in,
                          then choose Save login state on it in Recent
                          recordings.
                        </span>
                      )}
                    </div>
                  )}
                </div>
              )}

              <button
                aria-label="Start browser recording"
                className="btn btn-primary rec-start-btn"
                onClick={handleStartBrowserRecording}
                disabled={!!browserPanelSessionId || browserStartInProgress}
              >
                <svg
                  width="14"
                  height="14"
                  viewBox="0 0 24 24"
                  fill="currentColor"
                >
                  <circle cx="12" cy="12" r="10" />
                </svg>
                {browserStartInProgress ? "Starting browser…" : "Start browser recording"}
              </button>
              <p className="rec-browser-note">
                {browserWindowLocation} with an in-page recording sidebar
                (sections, transactions, notes, recent steps, pause and
                verification picker) you can drag to any corner. Manage the
                session from there or from the control panel docked below,
                which also opens as a compact popup window.
              </p>
            </div>
          )}

          {browserPanelSessionId && (
            <BrowserRecorderControlPanel
              destinationControl={destinationInBrowserPanel ? destinationControl : undefined}
              sessionId={browserPanelSessionId}
              recordingProfileId={recordingProfileId || undefined}
              recordingProfileHTTPScriptLevel={recordingProfileLevel}
              folderId={folderId}
              onImported={onImported}
              onEdit={onEdit}
              onOperationBusyChange={setBrowserPanelOperationBusy}
              onDismiss={() => {
                setBrowserPanelOperationBusy(false);
                setBrowserPanelSessionId(null);
                refresh();
              }}
            />
          )}

        </div>

        <div
          className="rec-controls-card rec-proxy-recorder-option"
          data-testid="rec-proxy-recorder-option"
          hidden={recordingMode !== 'proxy'}
        >
          <div className="rec-option-heading">
            <h3 className="rec-section-title">HTTP proxy recorder</h3>
            <p>
              Capture HTTP(S) traffic through the controller recording proxy
              when headed browser recording is unavailable.
            </p>
          </div>

          {proxyCanRecord ? (
          <>
          {/* Proxy setup */}
          <div className="rec-setup-section">
            <button
              aria-label="Toggle proxy recorder setup"
              type="button"
              className="rec-setup-toggle"
              aria-expanded={showSetup}
              aria-controls="recording-proxy-setup-steps"
              onClick={() => setShowSetup(!showSetup)}
            >
              <div className="rec-proxy-header">
                <svg
                  width="18"
                  height="18"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="var(--accent)"
                  strokeWidth="2"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                >
                  <circle cx="12" cy="12" r="10" />
                  <line x1="12" y1="16" x2="12" y2="12" />
                  <line x1="12" y1="8" x2="12.01" y2="8" />
                </svg>
                <span>Proxy setup</span>
              </div>
              <svg
                className={`rec-chevron ${showSetup ? "open" : ""}`}
                width="16"
                height="16"
                viewBox="0 0 24 24"
                fill="none"
                stroke="var(--text-muted)"
                strokeWidth="2"
                strokeLinecap="round"
                strokeLinejoin="round"
              >
                <polyline points="6 9 12 15 18 9" />
              </svg>
            </button>

            {showSetup && (
              <div className="rec-setup-steps" id="recording-proxy-setup-steps">
                <div className="rec-step">
                  <div className="rec-step-number">1</div>
                  <div className="rec-step-content">
                    <div className="rec-step-title">
                      Download and install the CA certificate
                    </div>
                    <div className="rec-step-desc">
                      Install and trust the APEX Load CA certificate for SSL/TLS
                      so the proxy can intercept HTTPS traffic.
                    </div>
                    <div className="rec-step-note">
                      Only trust this private recording CA on a workstation you
                      control. The recorder reuses the same CA across controller
                      restarts, so install it again only if you delete the CA
                      files or change the recorder CA directory.
                      {caCertPath && (
                        <div className="rec-step-note-path">
                          <span>Current certificate path:</span>
                          <code>{caCertPath}</code>
                          <button
                            type="button"
                            className="rec-inline-action"
                            onClick={handleCopyCAPath}
                          >
                            Copy path
                          </button>
                        </div>
                      )}
                      <div className="rec-step-note-troubleshoot">
                        If the browser still reports{" "}
                        <code>ERR_CERT_AUTHORITY_INVALID</code>, the CA is
                        imported but not trusted for SSL/TLS, the browser is
                        using a separate trust store, or an older APEX Load CA
                        is still selected.
                      </div>
                    </div>
                    <div className="rec-download-row">
                      <PageViewToggle
                        value={certificateFormatView}
                        ariaLabel="Certificate format"
                        className="rec-certificate-format-toggle script-page-toggle"
                        onChange={(value) => setCertificateFormatView(value)}
                        options={[
                          { value: "crt", label: "CRT" },
                          { value: "pem", label: "PEM" },
                        ]}
                      />
                      <button
                        className="rec-download-btn"
                        onClick={() => handleDownloadCA(certificateFormatView)}
                      >
                        <svg
                          width="14"
                          height="14"
                          viewBox="0 0 24 24"
                          fill="none"
                          stroke="currentColor"
                          strokeWidth="2"
                          strokeLinecap="round"
                          strokeLinejoin="round"
                        >
                          <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
                          <polyline points="7 10 12 15 17 10" />
                          <line x1="12" y1="15" x2="12" y2="3" />
                        </svg>
                        {certificateFormatView === "crt"
                          ? "Download CA certificate (.crt)"
                          : "Download CA certificate (.pem)"}
                      </button>
                    </div>
                    <div className="rec-os-instructions">
                      {certificateFormatView === "crt" ? (
                        <>
                          <div>
                            <span className="rec-os-label">
                              macOS Chrome, Edge, Safari:
                            </span>{" "}
                            Download .crt &rarr; open Keychain Access &rarr;
                            import into your login keychain &rarr; open
                            &ldquo;APEX Load Recording Proxy CA&rdquo; &rarr;
                            expand Trust &rarr; set &ldquo;When using this
                            certificate&rdquo; or &ldquo;Secure Sockets Layer
                            (SSL)&rdquo; to Always Trust &rarr; close the
                            window, enter your password, and restart the
                            browser.
                          </div>
                          <div>
                            <span className="rec-os-label">
                              Windows Chrome, Edge:
                            </span>{" "}
                            Download .crt &rarr; Install Certificate &rarr;
                            Current User &rarr; place it in &ldquo;Trusted Root
                            Certification Authorities&rdquo; &rarr; finish the
                            wizard and restart the browser.
                          </div>
                        </>
                      ) : (
                        <>
                          <div>
                            <span className="rec-os-label">Firefox:</span>{" "}
                            Download PEM format &rarr; Settings &rarr; Privacy
                            &amp; Security &rarr; Certificates &rarr; View
                            Certificates &rarr; Authorities &rarr; Import &rarr;
                            enable &ldquo;Trust this CA to identify
                            websites&rdquo; &rarr; restart Firefox. If Firefox
                            is configured to use system roots, restart after
                            trusting the OS root instead.
                          </div>
                          <div>
                            <span className="rec-os-label">Linux:</span>{" "}
                            Download PEM format and prefer the current
                            browser/profile trust store. Use a distro-wide trust
                            store only on a dedicated test host where an
                            administrator explicitly requires it.
                          </div>
                        </>
                      )}
                      <div>
                        <span className="rec-os-label">Cleanup:</span> Remove
                        the &ldquo;APEX Load Recording Proxy CA&rdquo; from the
                        same per-user trust store when recording is finished;
                        remove older entries before trusting the current CA.
                        Machine-wide System keychain or Local Machine trust is
                        an explicit administrator-only exception for dedicated
                        test hosts, not the default.
                      </div>
                    </div>
                  </div>
                </div>

                <div className="rec-step">
                  <div className="rec-step-number">2</div>
                  <div className="rec-step-content">
                    <div className="rec-step-title">
                      Configure browser proxy
                    </div>
                    <div className="rec-step-desc">
                      Route browser traffic through{" "}
                      <code>
                        {proxyHost}:{proxyPort}
                      </code>
                    </div>
                    {proxyAuthToken && (
                      <div className="rec-step-note">
                        When the browser requests proxy credentials, use
                        username <code>apexload</code> and password{" "}
                        <code>{proxyAuthToken}</code>. This per-launch password
                        changes whenever APEX Load restarts.{" "}
                        <button
                          className="rec-inline-action"
                          type="button"
                          onClick={() => {
                            void copyProxyCommand(
                              proxyAuthToken,
                              "Proxy password copied",
                              "Failed to copy proxy password",
                            );
                          }}
                        >
                          Copy password
                        </button>
                      </div>
                    )}
                    {proxyAvailability?.private_targets_allowed === false && (
                      <div className="rec-step-note rec-step-note-troubleshoot" data-testid="recording-proxy-public-only">
                        This controller's proxy records public addresses only.
                        Requests to private or internal addresses (such as
                        10.x.x.x, 192.168.x.x or localhost) are blocked unless
                        an administrator allows private targets.
                        <details className="rec-admin-details">
                          <summary>Details for administrators</summary>
                          <p>
                            Set APEXLOAD_RECORDER_ALLOW_PRIVATE_TARGETS=true on
                            the controller to record applications on private
                            networks.
                          </p>
                        </details>
                      </div>
                    )}
                    {!proxyHostIsLocal && (
                      <div className="rec-step-note rec-step-note-troubleshoot">
                        This controller is remote. Use the Recording Agent
                        bridge for workstation-local browser capture unless an
                        operator has explicitly exposed the proxy on{" "}
                        <code>{proxyHost}</code>.
                      </div>
                    )}
                    <div className="rec-os-instructions">
                      <div>
                        Use a browser extension like SwitchyOmega (Chrome) or
                        FoxyProxy (Firefox) to route traffic through the proxy.
                      </div>
                      <div>
                        <span className="rec-os-label">System proxy:</span>{" "}
                        macOS System Settings &rarr; Network &rarr; Wi-Fi &rarr;
                        Details &rarr; Proxies &rarr; Web Proxy (HTTP) &amp;
                        Secure Web Proxy (HTTPS) &rarr; <code>{proxyHost}</code>{" "}
                        port <code>{proxyPort}</code>
                      </div>
                      <div>
                        <span className="rec-os-label">Windows 11:</span>{" "}
                        Settings &rarr; Network &amp; internet &rarr; Proxy
                        &rarr; Manual proxy setup &rarr;{" "}
                        <code>{proxyHost}</code> port <code>{proxyPort}</code>
                      </div>
                    </div>
                    <div className="rec-proxy-shortcut">
                      <PageViewToggle
                        value={proxyPlatformView}
                        ariaLabel="Proxy command platform"
                        className="rec-proxy-platform-toggle script-page-toggle"
                        onChange={(value) => setProxyPlatformView(value)}
                        options={[
                          { value: "macos", label: "macOS" },
                          { value: "windows", label: "Windows 11" },
                        ]}
                      />
                      {proxyPlatformView === "macos" ? (
                        <div className="rec-proxy-platform">
                          <div className="rec-proxy-platform-title">macOS</div>
                          <label className="rec-proxy-service">
                            <span>Network service</span>
                            <input
                              className="rec-input"
                              aria-label="macOS network service"
                              value={macOSNetworkService}
                              onChange={(e) =>
                                setMacOSNetworkService(e.target.value)
                              }
                            />
                          </label>
                          <div className="rec-shortcut-actions">
                            <button
                              className="rec-download-alt"
                              type="button"
                              onClick={() =>
                                handleCopyMacOSProxyCommand("start")
                              }
                            >
                              Copy start proxy command
                            </button>
                            <button
                              className="rec-download-alt"
                              type="button"
                              onClick={() =>
                                handleCopyMacOSProxyCommand("restore")
                              }
                            >
                              Copy restore proxy command
                            </button>
                            <button
                              className="rec-download-alt"
                              type="button"
                              onClick={handleOpenMacOSNetworkSettings}
                            >
                              Open network settings
                            </button>
                          </div>
                        </div>
                      ) : (
                        <div className="rec-proxy-platform">
                          <div className="rec-proxy-platform-title">
                            Windows 11
                          </div>
                          <div className="rec-shortcut-actions">
                            <button
                              className="rec-download-alt"
                              type="button"
                              onClick={() =>
                                handleCopyWindowsProxyCommand("start")
                              }
                            >
                              Copy Windows start command
                            </button>
                            <button
                              className="rec-download-alt"
                              type="button"
                              onClick={() =>
                                handleCopyWindowsProxyCommand("restore")
                              }
                            >
                              Copy Windows restore command
                            </button>
                            <button
                              className="rec-download-alt"
                              type="button"
                              onClick={handleOpenWindowsProxySettings}
                            >
                              Open Windows proxy settings
                            </button>
                          </div>
                        </div>
                      )}
                    </div>
                  </div>
                </div>

                <div className="rec-step">
                  <div className="rec-step-number">3</div>
                  <div className="rec-step-content">
                    <div className="rec-step-title">Start recording</div>
                    <div className="rec-step-desc">
                      Click &ldquo;Start recording&rdquo; below, then browse the
                      target website. All HTTP and HTTPS traffic will be
                      captured.
                    </div>
                  </div>
                </div>

                <div className="rec-step">
                  <div className="rec-step-number">4</div>
                  <div className="rec-step-content">
                    <div className="rec-step-title">Stop and convert</div>
                    <div className="rec-step-desc">
                      When done, stop the recording and convert to a test
                      script. Remember to disable the proxy.
                    </div>
                  </div>
                </div>
              </div>
            )}
          </div>

          <div className="rec-proxy-run-panel">
            <h3 className="rec-section-title">
              {activeSession ? (
                <>
                  <span className="rec-live-dot" />
                  {activeSession.status === "paused"
                    ? "Paused"
                    : "Recording"}: {activeSession.name}
                </>
              ) : (
                "New recording"
              )}
            </h3>

            {!activeSession ? (
              <div className="rec-start-form">
                <div className="rec-form-row">
                  <label className="rec-label" htmlFor="rec-proxy-session-name">Session name</label>
                  <input
                    id="rec-proxy-session-name"
                    className="rec-input"
                    value={sessionName}
                    onChange={(e) => setSessionName(e.target.value)}
                    placeholder="e.g. Login & Checkout Flow"
                  />
                </div>
                <div className="rec-form-row">
                  <label className="rec-label" htmlFor="rec-proxy-include-hosts">
                    Include hosts{" "}
                    <span className="rec-hint">
                      (comma-separated, optional)
                    </span>
                  </label>
                  <input
                    id="rec-proxy-include-hosts"
                    className="rec-input"
                    aria-describedby={profileIncludeHosts.length > 0 ? "rec-profile-include-hosts" : undefined}
                    value={includeHosts}
                    onChange={(e) => setIncludeHosts(e.target.value)}
                    placeholder="e.g. api.example.com, app.example.com"
                  />
                  {profileIncludeHosts.length > 0 && (
                    <span className="rec-hint" id="rec-profile-include-hosts">
                      Recording profile “{recordingProfile?.name}” records
                      only these hosts, plus any typed here:{" "}
                      {profileIncludeHosts.join(", ")}
                    </span>
                  )}
                </div>
                <div className="rec-form-row">
                  <label className="rec-label" htmlFor="rec-proxy-exclude-hosts">
                    Exclude hosts{" "}
                    <span className="rec-hint">
                      (comma-separated, optional)
                    </span>
                  </label>
                  <input
                    id="rec-proxy-exclude-hosts"
                    className="rec-input"
                    aria-describedby={profileExcludeHosts.length > 0 ? "rec-profile-exclude-hosts" : undefined}
                    value={excludeHosts}
                    onChange={(e) => setExcludeHosts(e.target.value)}
                    placeholder="e.g. analytics.google.com, cdn.example.com"
                  />
                  {profileExcludeHosts.length > 0 && (
                    <span className="rec-hint" id="rec-profile-exclude-hosts">
                      Recording profile “{recordingProfile?.name}” also
                      leaves out: {profileExcludeHosts.join(", ")}
                    </span>
                  )}
                </div>
                <label className="rec-checkbox-row">
                  <input
                    type="checkbox"
                    checked={captureCookies}
                    onChange={(e) => setCaptureCookies(e.target.checked)}
                  />
                  <span>Capture cookies (may include secrets)</span>
                </label>
                <button
                  className="btn btn-primary rec-start-btn"
                  onClick={handleStart}
                >
                  <svg
                    width="14"
                    height="14"
                    viewBox="0 0 24 24"
                    fill="currentColor"
                  >
                    <circle cx="12" cy="12" r="10" />
                  </svg>
                  Start recording
                </button>
              </div>
            ) : (
              <div className="rec-active-panel">
                <div className="rec-active-stats">
                  <div className="rec-stat">
                    <span className="rec-stat-value">
                      {activeSession.request_count ??
                        activeSession.requests?.length ??
                        0}
                    </span>
                    <span className="rec-stat-label">Requests</span>
                  </div>
                  <div className="rec-stat">
                    <span className="rec-stat-value">
                      {activeSession.base_url || "—"}
                    </span>
                    <span className="rec-stat-label">Base URL</span>
                  </div>
                  <div className="rec-stat">
                    <span className="rec-stat-value">
                      {new Date(activeSession.started_at).toLocaleTimeString()}
                    </span>
                    <span className="rec-stat-label">Started</span>
                  </div>
                </div>
                {(activeSession.rejected_requests ?? 0) > 0 && (
                  <div className="rec-feedback error" role="alert" data-testid="recording-proxy-rejected">
                    {proxyRejectionSummary(activeSession)} An administrator can
                    allow private targets for the recording proxy.
                  </div>
                )}
                {proxyQuiet && (activeSession.rejected_requests ?? 0) === 0 && (
                  <div className="rec-feedback" role="status" data-testid="recording-proxy-quiet">
                    No requests yet. Check that your browser sends its traffic
                    through {proxyHost}:{proxyPort}.{" "}
                    <button
                      type="button"
                      className="rec-inline-action"
                      onClick={() => setShowSetup(true)}
                    >
                      Show proxy setup
                    </button>
                  </div>
                )}

                {/* Live request feed */}
                {activeSession.requests &&
                  activeSession.requests.length > 0 && (
                    <div className="rec-live-feed">
                      <div className="rec-feed-header">
                        <span>Live requests</span>
                        <span className="rec-feed-count">
                          {activeSession.request_count ??
                            activeSession.requests.length}
                        </span>
                      </div>
                      <div className="rec-feed-list">
                        {[...activeSession.requests]
                          .reverse()
                          .slice(0, 20)
                          .map((req) => (
                            <div key={req.seq} className="rec-feed-item">
                              <span
                                className={`rec-method ${req.method.toLowerCase()}`}
                              >
                                {req.method}
                              </span>
                              <span className="rec-path" title={req.url}>
                                {req.path}
                              </span>
                              <span
                                className={`rec-status ${req.status_code >= 400 ? "error" : ""}`}
                              >
                                {req.status_code}
                              </span>
                              <span className="rec-timing">
                                {req.response_time_ms.toFixed(0)}ms
                              </span>
                            </div>
                          ))}
                      </div>
                    </div>
                  )}

                <button
                  className="btn btn-secondary rec-stop-btn"
                  onClick={handleStop}
                >
                  <svg
                    width="14"
                    height="14"
                    viewBox="0 0 24 24"
                    fill="currentColor"
                  >
                    <rect x="4" y="4" width="16" height="16" rx="2" />
                  </svg>
                  Stop recording
                </button>
                <button
                  className="btn btn-secondary rec-stop-btn"
                  onClick={handlePauseResume}
                >
                  {activeSession.status === "paused"
                    ? "Resume recording"
                    : "Pause recording"}
                </button>
                <button
                  className="btn btn-secondary rec-stop-btn"
                  type="button"
                  onClick={() => handleCopyMacOSProxyCommand("restore")}
                >
                  Copy restore proxy command
                </button>
                <button
                  className="btn btn-secondary rec-stop-btn"
                  type="button"
                  onClick={() => handleCopyWindowsProxyCommand("restore")}
                >
                  Copy Windows restore command
                </button>
              </div>
            )}
          </div>
          </>
          ) : (
            // Earlier proxy recordings stay in Recent recordings; a new one cannot start.
            <div className="rec-method-note" data-testid="recording-proxy-card-unavailable">
              <p>
                {proxyAvailability === null
                  ? recorderStatusFailed
                    ? "Could not check whether this controller can record through its HTTP proxy. Retrying…"
                    : "Checking whether this controller can record through its HTTP proxy…"
                  : proxyUnavailableNote?.summary ||
                    "HTTP proxy recording isn't available on this controller."}
              </p>
            </div>
          )}
        </div>
      </div>

      {/* Recent recordings, of both methods */}
      {recentRecordings.length > 0 && (
        <div className="rec-sessions-section" data-testid="recent-recordings">
          <h3 className="rec-section-title">Recent recordings</h3>
          {stoppedSessions.length > 0 && (
            <label className="rec-convert-option">
              <input
                type="checkbox"
                checked={parameterizeInputs}
                onChange={(e) => {
                  sourceSecretSessions.clear("converted");
                  advanceConvertedReviewGeneration();
                  resetConvertTask();
                  convertedPreparedScriptRef.current = null;
                  convertedSourceRecordingSessionIDRef.current = null;
                  convertedBatchSaveOperationRef.current = null;
                  setConvertedPreparedSecrets(null);
                  setParameterizeInputs(e.target.checked);
                }}
              />
              Parameterize input values when converting an HTTP proxy
              recording (promote recorded form/query inputs into a data
              source)
            </label>
          )}
          <div className="rec-sessions-list">
            {recentRecordings.map((recording) =>
              recording.kind === "browser" ? (
                <div
                  key={`browser:${recording.session.id}`}
                  className={`rec-session-card ${browserPanelSessionId === recording.session.id ? "selected" : ""}`}
                >
                  <button
                    aria-label={`Open browser recording ${recording.session.name || "Browser Recording"}`}
                    type="button"
                    className="rec-session-select"
                    aria-pressed={browserPanelSessionId === recording.session.id}
                    onClick={() =>
                      handleOpenBrowserRecordingSession(recording.session)
                    }
                  >
                    <span className="rec-session-header">
                      <span className="rec-session-name">
                        {recording.session.name || "Browser Recording"}
                      </span>
                      <span className="rec-recording-kind">Browser</span>
                    </span>
                    <span className="rec-session-meta">
                      <span>{recording.session.step_count || 0} steps</span>
                      <span>
                        {recording.session.network_request_count || 0}{" "}
                        requests
                      </span>
                      {recording.session.status === "failed" && (
                        <span>Failed</span>
                      )}
                      <span>
                        {formatDateTime(recording.at, { seconds: true })}
                      </span>
                    </span>
                  </button>
                  <div className="rec-session-actions">
                    <button
                      type="button"
                      className="btn btn-sm btn-primary"
                      onClick={() =>
                        handleOpenBrowserRecordingSession(recording.session)
                      }
                    >
                      Open
                    </button>
                    {recording.session.storage_state_available &&
                      storageStatesListed && (
                        <button
                          type="button"
                          className="btn btn-sm btn-secondary"
                          disabled={storageStateSavingId !== null}
                          onClick={() => {
                            void handleSaveBrowserLoginState(recording.session);
                          }}
                        >
                          {storageStateSavingId === recording.session.id
                            ? "Saving login state…"
                            : storageStates.some(
                                  (state) =>
                                    state.recording_id === recording.session.id,
                                )
                              ? "Save login state again"
                              : "Save login state"}
                        </button>
                      )}
                    <button
                      type="button"
                      className="btn btn-sm btn-danger"
                      onClick={(event) =>
                        handleDeleteBrowserRecordingSession(
                          recording.session,
                          event,
                        )
                      }
                    >
                      Delete
                    </button>
                  </div>
                </div>
              ) : (
                <div
                  key={`proxy:${recording.session.id}`}
                  className={`rec-session-card ${selectedSession?.id === recording.session.id ? "selected" : ""}`}
                >
                  <button
                    aria-label={`Select recording session ${recording.session.name}`}
                    type="button"
                    className="rec-session-select"
                    aria-pressed={selectedSession?.id === recording.session.id}
                    onClick={() => handleSelectSession(recording.session)}
                  >
                    <span className="rec-session-header">
                      <span className="rec-session-name">
                        {recording.session.name}
                      </span>
                      <span className="rec-recording-kind">HTTP proxy</span>
                    </span>
                    <span className="rec-session-meta">
                      <span>
                        {recording.session.request_count ??
                          recording.session.requests?.length ??
                          0}{" "}
                        requests
                      </span>
                      <span>{recording.session.base_url || "No base URL"}</span>
                      {(recording.session.rejected_requests ?? 0) > 0 && (
                        <span>
                          {recording.session.rejected_requests} blocked
                          (private address)
                        </span>
                      )}
                      <span>
                        {formatDateTime(recording.at, { seconds: true })}
                      </span>
                    </span>
                  </button>
                  <div className="rec-session-actions">
                    <button
                      type="button"
                      className="btn btn-sm btn-primary"
                      onClick={(e) => {
                        e.stopPropagation();
                        handleConvert(recording.session);
                      }}
                      disabled={converting}
                    >
                      {converting ? "Converting…" : "Convert to script"}
                    </button>
                    {recordingLooksSalesforce(recording.session) && (
                      <button
                        type="button"
                        className="btn btn-sm btn-secondary"
                        onClick={(e) => {
                          e.stopPropagation();
                          handleReviewSalesforceSession(recording.session);
                        }}
                        disabled={reviewingSalesforce}
                      >
                        {reviewingSalesforce
                          ? "Reviewing…"
                          : "Review Salesforce HTTP"}
                      </button>
                    )}
                    <button
                      type="button"
                      className="btn btn-sm btn-secondary"
                      onClick={(e) => {
                        e.stopPropagation();
                        handleExportHAR(recording.session);
                      }}
                    >
                      Export HAR
                    </button>
                    <button
                      type="button"
                      className="btn btn-sm btn-danger"
                      onClick={(e) => {
                        e.stopPropagation();
                        handleDelete(recording.session.id);
                      }}
                    >
                      Delete
                    </button>
                  </div>
                </div>
              ),
            )}
          </div>
        </div>
      )}

      {/* Selected session detail */}
      {selectedSession && (
        <div className="rec-detail-section">
          <h3 className="rec-section-title">
            Request detail: {selectedSession.name}
            <span className="rec-detail-count">
              {selectedSession.request_count ??
                selectedSession.requests?.length ??
                0}{" "}
              requests
            </span>
          </h3>
          <div ref={requestTableRef} className="rec-detail-table-wrap">
            <table className="rec-detail-table">
              <thead>
                <tr>
                  <th>#</th>
                  <th>Method</th>
                  <th>Path</th>
                  <th>Status</th>
                  <th>Time</th>
                  <th>Think</th>
                  <th>Type</th>
                  <th>Size</th>
                  <th>Use</th>
                </tr>
              </thead>
              <tbody>
                {virtualizeSelectedRequests && selectedRequestPaddingTop > 0 && (
                  <tr aria-hidden="true"><td aria-label="Virtualized rows omitted" colSpan={9} style={{ height: selectedRequestPaddingTop, padding: 0, border: 0 }} /></tr>
                )}
                {(virtualizeSelectedRequests
                  ? selectedRequestVirtualRows.map(item => selectedRequests[item.index])
                  : selectedRequests
                ).map((req) => (
                  <tr
                    key={req.seq}
                    className={`${req.is_static ? "static" : ""} ${req.excluded ? "static" : ""} ${selectedRequest?.seq === req.seq ? "selected" : ""}`}
                  >
                    <td className="cell-mono">{req.seq}</td>
                    <td>
                      <span
                        className={`rec-method ${req.method.toLowerCase()}`}
                      >
                        {req.method}
                      </span>
                    </td>
                    <td className="cell-path" title={req.url}>
                      <button
                        aria-label={`Select request ${req.seq}`}
                        type="button"
                        className="rec-request-select"
                        aria-pressed={selectedRequest?.seq === req.seq}
                        onClick={() => setSelectedRequest(req)}
                      >
                        {req.step_name || req.path}
                        {req.query_string ? `?${req.query_string}` : ""}
                      </button>
                    </td>
                    <td className={req.status_code >= 400 ? "text-danger" : ""}>
                      {req.status_code}
                    </td>
                    <td className="cell-mono">
                      {req.response_time_ms.toFixed(0)}ms
                    </td>
                    <td className="cell-mono">
                      {req.think_time_ms > 0 ? `${req.think_time_ms}ms` : "—"}
                    </td>
                    <td className="cell-muted">
                      {req.resource_type ||
                        (req.content_type
                          ? req.content_type.split(";")[0]
                          : "—")}
                    </td>
                    <td className="cell-mono">
                      {(req.body_size_bytes || 0) +
                        (req.response_body_size_bytes || 0)}
                      B
                      {req.body_truncated || req.response_body_truncated
                        ? "*"
                        : ""}
                    </td>
                    <td>
                      <button
                        type="button"
                        className="btn btn-sm btn-secondary"
                        onClick={(e) => {
                          e.stopPropagation();
                          handleToggleRequestExcluded(selectedSession, req);
                        }}
                      >
                        {req.excluded ? "Include" : "Exclude"}
                      </button>
                    </td>
                  </tr>
                ))}
                {virtualizeSelectedRequests && selectedRequestPaddingBottom > 0 && (
                  <tr aria-hidden="true"><td aria-label="Virtualized rows omitted" colSpan={9} style={{ height: selectedRequestPaddingBottom, padding: 0, border: 0 }} /></tr>
                )}
              </tbody>
            </table>
          </div>
          {selectedRequest && (
            <div className="rec-request-editor">
              <div className="rec-editor-header">
                <div>
                  <span
                    className={`rec-method ${selectedRequest.method.toLowerCase()}`}
                  >
                    {selectedRequest.method}
                  </span>
                  <code>
                    {selectedRequest.path}
                    {selectedRequest.query_string
                      ? `?${selectedRequest.query_string}`
                      : ""}
                  </code>
                </div>
                <span className="cell-mono">#{selectedRequest.seq}</span>
              </div>
              <div className="rec-editor-grid">
                <label className="rec-form-row">
                  <span className="rec-label">Step name</span>
                  <input
                    className="rec-input"
                    value={requestDraft.step_name}
                    onChange={(e) =>
                      setRequestDraft((prev) => ({
                        ...prev,
                        step_name: e.target.value,
                      }))
                    }
                    placeholder={`${selectedRequest.method} ${selectedRequest.path}`}
                  />
                </label>
                <label className="rec-form-row">
                  <span className="rec-label">Transaction</span>
                  <input
                    className="rec-input"
                    value={requestDraft.transaction_name}
                    onChange={(e) =>
                      setRequestDraft((prev) => ({
                        ...prev,
                        transaction_name: e.target.value,
                      }))
                    }
                    placeholder="e.g. Login, Browse Catalog, Checkout"
                  />
                </label>
                <label className="rec-form-row">
                  <span className="rec-label">Think time (ms)</span>
                  <input
                    className="rec-input"
                    type="number"
                    min="0"
                    value={requestDraft.think_time_ms}
                    onChange={(e) =>
                      setRequestDraft((prev) => ({
                        ...prev,
                        think_time_ms: Math.max(0, Number(e.target.value) || 0),
                      }))
                    }
                  />
                </label>
                <label className="rec-editor-check">
                  <input
                    type="checkbox"
                    checked={!requestDraft.excluded}
                    onChange={(e) =>
                      setRequestDraft((prev) => ({
                        ...prev,
                        excluded: !e.target.checked,
                      }))
                    }
                  />
                  Use in generated script
                </label>
              </div>
              <label className="rec-form-row">
                <span className="rec-label">Note</span>
                <textarea
                  className="rec-input rec-textarea"
                  value={requestDraft.note}
                  onChange={(e) =>
                    setRequestDraft((prev) => ({
                      ...prev,
                      note: e.target.value,
                    }))
                  }
                  placeholder="Capture intent, manual correlation notes, or cleanup reminders"
                />
              </label>
              <div className="rec-editor-actions">
                <button
                  className="btn btn-primary btn-sm"
                  onClick={handleSaveRequestDetails}
                >
                  Save request
                </button>
                <button
                  className="btn btn-secondary btn-sm"
                  onClick={() => setSelectedRequest(null)}
                >
                  Close
                </button>
              </div>
            </div>
          )}
        </div>
      )}

      {/* Convert Result */}
      {convertResult && convertedScript && (
        <div className="rec-convert-result" ref={convertResultRef}>
          <h3 className="rec-section-title">Review the converted script</h3>
          {!destinationInBrowserPanel && destinationControl}
          {convertedSecretRequirements.length > 0 && (
            <SecretBindingReview
              ref={convertedCredentialReviewRef}
              requirements={convertedSecretRequirements}
              reviewGeneration={convertedCredentialReviewIdentity}
              projectId={activeProject?.id}
              sourceSecretSessionId={convertResult.source_secret_session_id}
              sourceSecretSessionExpiresAt={
                convertResult.source_secret_session_expires_at
              }
              sourceMigrationAllowed={
                convertResult.source_secret_migration_allowed
              }
              disabled={
                convertedSaveInProgress ||
                convertedCredentialActionInProgress ||
                convertedParameterizationApplying
              }
              onApply={handleApplyConvertedSecretBindings}
              onPreparedChange={setConvertedPreparedSecrets}
              onProjectSecretsConfigured={handleConvertedProjectSecretsConfigured}
            />
          )}
          {convertedProjectSecretsConfiguredForReview && (
            <div className="rec-feedback" role="status">
              Project-secret values configured during this review remain in Project Secrets independently of script saving.
              {" "}If script saving fails, retrying this save reuses those configured values. Any atomic-save promise on this page applies to scripts, not external secret-store writes.
            </div>
          )}
          {convertResult.parameterization &&
            (convertResult.parameterization.suggestions?.length || 0) > 0 &&
            convertedReviewScript && (
              <ParameterizationReview
                parameterization={convertResult.parameterization}
                script={convertedReviewScript}
                reviewGeneration={`${convertedReviewGeneration}:${activeProject?.id || ""}`}
                disabled={
                  convertedSaveInProgress || convertedCredentialActionInProgress
                }
                onApplyingChange={setConvertedParameterizationApplying}
                onApplied={handleApplyConvertedParameterization}
              />
            )}
          <div className="rec-result-summary">
            <div className="rec-result-item">
              <span className="rec-result-label">Script</span>
              <span className="rec-result-value">{convertedScript.name}</span>
            </div>
            <div className="rec-result-item">
              <span className="rec-result-label">Protocol</span>
              <span className="rec-result-value">
                {convertedScript.protocol}
              </span>
            </div>
            <div className="rec-result-item">
              <span className="rec-result-label">Steps</span>
              <span className="rec-result-value">{convertedSteps.length}</span>
            </div>
            <div className="rec-result-item">
              <span className="rec-result-label">Correlations</span>
              <span className="rec-result-value">
                {convertResult.correlations?.length || 0}
              </span>
            </div>
            {convertedExcludedRequests > 0 && (
              <div className="rec-result-item">
                <span className="rec-result-label">Left out</span>
                <span className="rec-result-value">
                  {convertedExcludedRequests} excluded{" "}
                  {convertedExcludedRequests === 1 ? "request" : "requests"}
                </span>
              </div>
            )}
            {convertedStoredScript?.id && (
              <div className="rec-result-item">
                <span className="rec-result-label">Script ID</span>
                <code className="rec-result-value">
                  {convertedStoredScript.id}
                </code>
              </div>
            )}
          </div>
          {convertedSteps.length > 0 && (
            <div className="rec-detail-table-wrap rec-review-steps">
              <table className="rec-detail-table" aria-label="Converted steps">
                <thead>
                  <tr>
                    <th>#</th>
                    <th>Step</th>
                    <th>Method</th>
                    <th>Target</th>
                    <th>Transaction</th>
                    <th>Think time</th>
                    <th>Checks</th>
                    <th>Extracts</th>
                  </tr>
                </thead>
                <tbody>
                  {convertedSteps.slice(0, RECORDING_REVIEW_STEP_LIMIT).map((step) => (
                    <tr key={step.index}>
                      <td className="cell-mono">{step.index}</td>
                      <td>{step.name}</td>
                      <td className="cell-mono">{step.method}</td>
                      <td className="cell-mono rec-review-target" title={step.target}>
                        {step.target}
                      </td>
                      <td>{step.transaction}</td>
                      <td>{step.thinkTime}</td>
                      <td>{step.checks || ""}</td>
                      <td>{step.extracts || ""}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
              {convertedSteps.length > RECORDING_REVIEW_STEP_LIMIT && (
                <p className="rec-hint">
                  And {convertedSteps.length - RECORDING_REVIEW_STEP_LIMIT}{" "}
                  more steps; open the script in the editor to see them all.
                </p>
              )}
            </div>
          )}
          <div className="rec-result-actions">
            <button
              className="btn btn-primary"
              type="button"
              onClick={() => {
                void handleSaveConvertedScript();
              }}
              disabled={
                convertedImportDraftExpiry?.expired ||
                !convertedCredentialsValid ||
                convertedSaveInProgress ||
                convertedCredentialActionInProgress ||
                convertedParameterizationApplying
              }
            >
              {convertedSaveLabel}
            </button>
            {onEdit && (
              <button
                className="btn btn-secondary"
                type="button"
                onClick={() => {
                  void handleEditConvertedScript();
                }}
                disabled={
                  convertedImportDraftExpiry?.expired ||
                  !convertedReviewScript ||
                  !convertedCredentialsValid ||
                  convertedSaveInProgress ||
                  convertedCredentialActionInProgress ||
                  convertedParameterizationApplying
                }
              >
                {convertedCredentialActionInProgress
                  ? "Preparing editor…"
                  : convertedRequiresCredentialPreservingSave
                    ? "Save and open in Visual Editor"
                    : "Open in Visual Editor"}
              </button>
            )}
          </div>
          {convertedImportDraftExpiry && (
            <div
              className={`rec-feedback ${convertedImportDraftExpiry.expired ? "error" : ""}`}
              role={convertedImportDraftExpiry.expired ? "alert" : "status"}
            >
              {convertedImportDraftExpiry.label}
            </div>
          )}

          {convertResult.correlations &&
            convertResult.correlations.length > 0 && (
              <div className="rec-correlations">
                <h4 className="rec-subsection-title">
                  Auto-detected correlations (
                  {convertResult.correlations.length})
                </h4>
                <div className="rec-corr-list">
                  {convertResult.correlations.map((c, i) => (
                    <div key={i} className="rec-corr-item">
                      <div className="rec-corr-header">
                        <code className="rec-corr-name">{`{{var.${c.name}}}`}</code>
                        {confidenceTone(c.confidence) && (
                          <span
                            className={`rec-corr-confidence tone-${confidenceTone(c.confidence)}`}
                            title={c.rule_id ? `rule: ${c.rule_id}` : undefined}
                          >
                            {Math.round((c.confidence as number) * 100)}%
                            confidence
                          </span>
                        )}
                        <span className="rec-corr-type">
                          {c.extract_type} / {c.source}
                        </span>
                      </div>
                      <div className="rec-corr-detail">
                        <span>Extracted from request #{c.found_in_seq}</span>
                        <span>Used in #{c.used_in_seqs.join(", #")}</span>
                      </div>
                      {c.reasons && c.reasons.length > 0 && (
                        <div className="rec-corr-reasons">
                          Why: {c.reasons.join("; ")}
                        </div>
                      )}
                      <div className="rec-corr-sample">
                        Sample: <code>{c.sample_value}</code>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            )}

          {convertedWarnings.length > 0 && (
            <div className="rec-warnings">
              {convertedWarnings.map((w, i) => (
                <div key={i} className="rec-warning-item">
                  {sentenceCaseNote(w)}
                </div>
              ))}
            </div>
          )}
          {(convertedInfoNotes.length > 0 || recordingNotes.length > 0) && (
            <div className="rec-notes" role="group" aria-label="Recording notes">
              {convertedInfoNotes.map((note, i) => (
                <div key={`note-${i}`} className="rec-note-item">
                  {sentenceCaseNote(note)}
                </div>
              ))}
              {recordingNotes.map((note) => (
                <div key={`${note.code}-${note.location}`} className="rec-note-item">
                  {note.element}
                  {note.location ? ` (${note.location})` : ""}: {note.detail}
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {salesforceResult && salesforceScript && (
        <div className="rec-convert-result">
          <h3 className="rec-section-title">Salesforce HTTP draft</h3>
          {salesforceSecretRequirements.length > 0 && (
            <SecretBindingReview
              ref={salesforceCredentialReviewRef}
              requirements={salesforceSecretRequirements}
              reviewGeneration={salesforceCredentialReviewIdentity}
              projectId={activeProject?.id}
              sourceSecretSessionId={salesforceResult.source_secret_session_id}
              sourceSecretSessionExpiresAt={
                salesforceResult.source_secret_session_expires_at
              }
              sourceMigrationAllowed={
                salesforceResult.source_secret_migration_allowed
              }
              disabled={
                salesforceSaveInProgress ||
                salesforceCredentialActionInProgress ||
                salesforceParameterizationApplying
              }
              onApply={handleApplySalesforceSecretBindings}
              onPreparedChange={setSalesforcePreparedSecrets}
              onProjectSecretsConfigured={handleSalesforceProjectSecretsConfigured}
            />
          )}
          {salesforceProjectSecretsConfiguredForReview && (
            <div className="rec-feedback" role="status">
              Project-secret values configured during this review remain in Project Secrets independently of script saving.
              {" "}If script saving fails, retrying this save reuses those configured values. Any atomic-save promise on this page applies to scripts, not external secret-store writes.
            </div>
          )}
          {salesforceResult.parameterization &&
            (salesforceResult.parameterization.suggestions?.length || 0) >
              0 && (
              <ParameterizationReview
                parameterization={salesforceResult.parameterization}
                script={salesforceScript}
                reviewGeneration={`${salesforceReviewGeneration}:${activeProject?.id || ""}`}
                disabled={
                  salesforceSaveInProgress ||
                  salesforceCredentialActionInProgress
                }
                onApplyingChange={setSalesforceParameterizationApplying}
                onApplied={handleApplySalesforceParameterization}
              />
            )}
          <div className="rec-result-summary">
            <div className="rec-result-item">
              <span className="rec-result-label">Script</span>
              <span className="rec-result-value">{salesforceScript.name}</span>
            </div>
            <div className="rec-result-item">
              <span className="rec-result-label">Protocol</span>
              <span className="rec-result-value">
                {salesforceScript.protocol}
              </span>
            </div>
            <div className="rec-result-item">
              <span className="rec-result-label">HAR coverage</span>
              <span className="rec-result-value">
                {Math.round(
                  salesforceResult.summary?.salesforce_review
                    ?.har_match_coverage?.coverage_pct || 0,
                )}
                %
              </span>
            </div>
          </div>
          <div className="rec-result-actions">
            <button
              className="btn btn-primary"
              type="button"
              onClick={() => {
                void handleSaveSalesforceScript();
              }}
              disabled={
                salesforceImportDraftExpiry?.expired ||
                !salesforceCredentialsValid ||
                salesforceSaveInProgress ||
                salesforceCredentialActionInProgress ||
                salesforceParameterizationApplying
              }
            >
              {salesforceSaveLabel}
            </button>
            {onEdit && (
              <button
                className="btn btn-secondary"
                type="button"
                onClick={() => {
                  void handleEditSalesforceScript();
                }}
                disabled={
                  salesforceImportDraftExpiry?.expired ||
                  !salesforceCredentialsValid ||
                  salesforceSaveInProgress ||
                  salesforceCredentialActionInProgress ||
                  salesforceParameterizationApplying
                }
              >
                {salesforceCredentialActionInProgress
                  ? "Preparing editor…"
                  : salesforceRequiresCredentialPreservingSave
                    ? "Save and open in Visual Editor"
                    : "Open in Visual Editor"}
              </button>
            )}
          </div>
          {salesforceImportDraftExpiry && (
            <div
              className={`rec-feedback ${salesforceImportDraftExpiry.expired ? "error" : ""}`}
              role={salesforceImportDraftExpiry.expired ? "alert" : "status"}
            >
              {salesforceImportDraftExpiry.label}
            </div>
          )}
          {(salesforceResult.warnings?.length || 0) > 0 && (
            <div className="rec-warnings">
              {salesforceResult.warnings.slice(0, 4).map((warning, idx) => (
                <div key={idx} className="rec-warning-item">
                  {warning}
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
