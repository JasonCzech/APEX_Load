// @vitest-environment jsdom

import { act } from "react";
import { createRoot, Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import RecordingStudio from "./RecordingStudio";
import type { WailsDesktopBridge } from "../desktop/desktopBridge";
import {
  buildMacOSRestoreProxyCommand,
  buildMacOSStartProxyCommand,
  buildWindowsRestoreProxyCommand,
  buildWindowsStartProxyCommand,
  recorderProxyHost,
  recorderProxyHostIsLocal,
  recorderProxyPort,
} from "./recordingProxyCommands";
(
  globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true;

const {
  apiFetchMock,
  downloadBlobMock,
  hasDesktopApiCapabilityMock,
  saveApiResourceViaBridgeMock,
  startDesktopBrowserDownloadMock,
} = vi.hoisted(() => ({
  apiFetchMock: vi.fn(),
  downloadBlobMock: vi.fn(),
  hasDesktopApiCapabilityMock: vi.fn(),
  saveApiResourceViaBridgeMock: vi.fn(),
  startDesktopBrowserDownloadMock: vi.fn(),
}));

type BridgeWindow = typeof window & {
  go?: { main?: { App?: WailsDesktopBridge } };
};

const apiMock = {
  listRecordings: vi.fn(),
  getRecorderStatus: vi.fn(),
  getRecording: vi.fn(),
  previewRecordingConversion: vi.fn(),
  convertRecording: vi.fn(),
  applyImportParameterization: vi.fn(),
  importSalesforce: vi.fn(),
  applySecretBindings: vi.fn(),
  getProjectSecretStatuses: vi.fn(),
  createProjectSecrets: vi.fn(),
  migrateImportSecrets: vi.fn(),
  discardImportSecretSession: vi.fn(),
  createScript: vi.fn(),
  commitGenericImportBatch: vi.fn(),
  startRecording: vi.fn(),
  stopRecording: vi.fn(),
  pauseRecording: vi.fn(),
  resumeRecording: vi.fn(),
  deleteRecording: vi.fn(),
  updateRecordedRequest: vi.fn(),
  startBrowserRecording: vi.fn(),
  getBrowserRecording: vi.fn(),
  listBrowserRecordings: vi.fn(),
  deleteBrowserRecording: vi.fn(),
  stopBrowserRecording: vi.fn(),
  openBrowserRecordingControlPanel: vi.fn(),
  previewBrowserRecordingConversion: vi.fn(),
  convertBrowserRecording: vi.fn(),
  listBrowserRecorderDevices: vi.fn(),
  installBrowserRecorder: vi.fn(),
  suggestBrowserRecordingTransactions: vi.fn(),
  pairBrowserRecorderBridge: vi.fn(),
  verifyBrowserRecorderBridgeAgent: vi.fn(),
  clearBrowserRecorderBridge: vi.fn(),
  getBrowserRecorderBridgeStatus: vi.fn(),
  exportBrowserRecording: vi.fn(),
  getRecordingProfiles: vi.fn(),
  saveRecordingProfiles: vi.fn(),
  browserRecorderBridge: null as any,
  browserRecorderRequestScope: "api:http://127.0.0.1:3000",
};

const clipboardWriteMock = vi.fn();

vi.mock("../hooks/useApi", () => ({
  useApi: () => apiMock,
}));

vi.mock("../hooks/apiClient", () => ({
  API_ORIGIN: "http://127.0.0.1:3000",
  DESKTOP_CAPABILITY_SESSION_KEY: "apexload.desktopCapability",
  apiFetch: apiFetchMock,
  hasDesktopApiCapability: () => hasDesktopApiCapabilityMock(),
  trimTrailingSlash: (value: string) => value.replace(/\/+$/, ""),
}));

vi.mock("../utils/fileUtils", () => ({
  downloadBlob: downloadBlobMock,
  downloadFilenameFromDisposition: (_header: string | null, fallback: string) =>
    fallback,
}));

vi.mock("../desktop/desktopFiles", () => ({
  saveApiResourceViaBridge: (...args: unknown[]) =>
    saveApiResourceViaBridgeMock(...args),
  startDesktopBrowserDownload: (...args: unknown[]) =>
    startDesktopBrowserDownloadMock(...args),
}));

describe("RecordingStudio", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
    window.sessionStorage.clear();
    apiFetchMock.mockReset();
    apiFetchMock.mockResolvedValue({
      ok: true,
      headers: new Headers({ "content-type": "application/octet-stream" }),
      blob: vi.fn().mockResolvedValue(new Blob(["certificate"])),
    });
    downloadBlobMock.mockReset();
    saveApiResourceViaBridgeMock.mockReset();
    saveApiResourceViaBridgeMock.mockResolvedValue("unavailable");
    startDesktopBrowserDownloadMock.mockReset();
    startDesktopBrowserDownloadMock.mockResolvedValue(undefined);
    hasDesktopApiCapabilityMock.mockReset();
    hasDesktopApiCapabilityMock.mockReturnValue(false);
    apiMock.browserRecorderBridge = null;
    apiMock.browserRecorderRequestScope = "api:http://127.0.0.1:3000";
    apiMock.getRecordingProfiles.mockReset();
    apiMock.getRecordingProfiles.mockResolvedValue({ profiles: [], rules: [], updated_at: "2026-10-09T00:00:00Z" });
    clipboardWriteMock.mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: clipboardWriteMock },
    });

    apiMock.listRecordings.mockResolvedValue({
      sessions: [
        {
          id: "rec-1",
          name: "Checkout Capture",
          status: "stopped",
          started_at: "2026-05-12T20:00:00Z",
          base_url: "https://shop.example.com",
          requests: [
            {
              seq: 1,
              timestamp: "2026-05-12T20:00:01Z",
              method: "GET",
              url: "https://shop.example.com/",
              path: "/",
              host: "shop.example.com",
              status_code: 200,
              response_time_ms: 123,
              content_type: "text/html",
              body: "",
              think_time_ms: 0,
              is_static: false,
            },
          ],
          filters: { exclude_static: true },
        },
      ],
    });
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ":8081",
      ca_cert_path: "artifacts/recorder/ca-cert.pem",
      active: null,
      browser: {
        availability: { available: true },
        active: null,
        install: { status: "idle" },
      },
    });
    apiMock.getRecording.mockResolvedValue(null);
    apiMock.previewRecordingConversion.mockResolvedValue({
      script: {
        name: "Checkout Preview",
        protocol: "http",
        actions: [{ name: "01 - GET /" }],
      },
      correlations: [
        {
          name: "session_id",
          source: "response",
          expression: "$.sessionId",
          extract_type: "jsonpath",
          found_in_seq: 1,
          used_in_seqs: [2],
          sample_value: "abc123",
          rule_id: "json.session_id",
          confidence: 0.85,
          reasons: ["reused in a later request"],
        },
      ],
      warnings: ["review dynamic headers"],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      stats: {},
      summary: {},
    });
    apiMock.convertRecording.mockResolvedValue({
      script: {
        id: "script-1",
        name: "Checkout Converted",
        protocol: "http",
        actions: [{ name: "01 - GET /" }],
      },
      correlations: [
        {
          name: "session_id",
          source: "response",
          expression: "$.sessionId",
          extract_type: "jsonpath",
          found_in_seq: 1,
          used_in_seqs: [2],
          sample_value: "abc123",
        },
      ],
      warnings: ["review dynamic headers"],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      stats: {},
      summary: {},
    });
    apiMock.importSalesforce.mockResolvedValue({
      scripts: [],
      script: {
        name: "Salesforce Capture HTTP",
        version: "1.0.0",
        protocol: "http",
        config: { base_url: "https://acme.my.salesforce.com" },
        actions: [
          {
            name: "Aura Bootstrap",
            type: "request",
            method: "POST",
            target: "/aura",
          },
        ],
      },
      warnings: ["Review Salesforce correlation extractors before approval."],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      correlations: [],
      secret_binding_requirements: [
        {
          id: "salesforce-session-token",
          path: "/actions/0/headers/Authorization",
          label: "Salesforce session token",
          context: "Aura Bootstrap",
          suggested_name: "SALESFORCE_SESSION_TOKEN",
          replacement: "Bearer {{secret.SALESFORCE_SESSION_TOKEN}}",
        },
      ],
      stats: { converted_actions: 1 },
      summary: {
        salesforce_review: {
          detected: true,
          har_match_coverage: {
            har_provided: true,
            imported_requests: 1,
            matched_requests: 1,
            coverage_pct: 100,
          },
        },
      },
    });
    apiMock.applySecretBindings.mockImplementation(
      async ({ script }: { script: Record<string, unknown> }) => ({
        script,
        secret_binding_requirements: [],
      }),
    );
    apiMock.getProjectSecretStatuses.mockResolvedValue({
      project_id: "proj-1",
      capabilities: {
        provider: "encrypted_database",
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: false,
      },
      secrets: [],
    });
    apiMock.createProjectSecrets.mockResolvedValue({
      project_id: "proj-1",
      secrets: [],
      count: 0,
    });
    apiMock.migrateImportSecrets.mockResolvedValue({
      project_id: "proj-1",
      secrets: [],
      count: 0,
    });
    apiMock.createScript.mockImplementation(
      async (script: {
        name?: string;
        protocol?: string;
        version?: string;
      }) => ({
        id: "script-1",
        name: script.name || "Saved Script",
        protocol: script.protocol || "http",
        version: script.version || "1.0.0",
        created_at: "2026-05-17T00:00:00Z",
      }),
    );
    apiMock.commitGenericImportBatch.mockImplementation(
      async (payload: {
        scripts: Array<{ name?: string; protocol?: string; version?: string; project_id?: string }>;
        folder_id?: string | null;
        source_recording_session_ids?: string[];
      }) => {
        const sourceRecordingSessionId = payload.source_recording_session_ids?.[0];
        const stored = sourceRecordingSessionId
          ? await apiMock.createScript(
              payload.scripts[0],
              payload.scripts[0]?.project_id,
              payload.folder_id ?? null,
              sourceRecordingSessionId,
            )
          : await apiMock.createScript(
              payload.scripts[0],
              payload.scripts[0]?.project_id,
              payload.folder_id ?? null,
            );
        return { scripts: [stored], count: 1 };
      },
    );
    apiMock.startBrowserRecording.mockResolvedValue({
      session: {
        id: "brec-1",
        name: "Browser Recording",
        status: "recording",
        start_url: "https://app.example.com/login",
        capture_har: true,
        started_at: "2026-05-12T20:00:00Z",
        step_count: 0,
        navigation_count: 0,
        network_request_count: 0,
        websocket_count: 0,
        warning_count: 0,
        har_available: false,
      },
    });
    apiMock.listBrowserRecordings.mockResolvedValue({ sessions: [], count: 0 });
    apiMock.deleteBrowserRecording.mockResolvedValue({
      deleted: "brec-1",
      session: { id: "brec-1", name: "Browser Recording", status: "stopped" },
    });
    apiMock.openBrowserRecordingControlPanel.mockResolvedValue({
      session: {
        id: "brec-1",
        name: "Browser Recording",
        status: "recording",
        start_url: "https://app.example.com/login",
        capture_har: true,
        started_at: "2026-05-12T20:00:00Z",
        step_count: 0,
        navigation_count: 0,
        network_request_count: 0,
        websocket_count: 0,
        warning_count: 0,
        har_available: false,
      },
    });
    apiMock.getBrowserRecording.mockResolvedValue({
      session: {
        id: "brec-1",
        name: "Browser Recording",
        status: "recording",
        start_url: "https://app.example.com/login",
        capture_har: true,
        started_at: "2026-05-12T20:00:00Z",
        step_count: 1,
        navigation_count: 1,
        network_request_count: 3,
        websocket_count: 0,
        warning_count: 0,
        har_available: false,
        steps: [
          {
            seq: 1,
            kind: "click",
            timestamp: "2026-05-12T20:00:02Z",
            url: "https://app.example.com/login",
          },
        ],
      },
    });
    apiMock.listBrowserRecorderDevices.mockResolvedValue({
      devices: [{ name: "iphone_15_pro", platform: "ios" }],
    });
    apiMock.installBrowserRecorder.mockResolvedValue({
      install: { status: "installing" },
    });
    apiMock.suggestBrowserRecordingTransactions.mockResolvedValue({
      suggestions: [],
    });
    apiMock.pairBrowserRecorderBridge.mockResolvedValue({
      origin: "http://127.0.0.1:49152",
      token: "tok",
      session_id: "ws-1",
      status: {
        availability: { available: true },
        api_origin: "http://127.0.0.1:49152",
        paired: true,
      },
    });
    apiMock.verifyBrowserRecorderBridgeAgent.mockResolvedValue({
      status: "healthy",
      version: "test",
    });
    apiMock.clearBrowserRecorderBridge.mockReset();
    apiMock.getBrowserRecorderBridgeStatus.mockResolvedValue({
      bridge: {
        availability: { available: true },
        api_origin: "http://127.0.0.1:49152",
        paired: true,
        active: null,
      },
    });
    apiMock.exportBrowserRecording.mockResolvedValue({
      ok: true,
      blob: vi.fn().mockResolvedValue(new Blob(['{"log":{}}'])),
    });
    apiMock.browserRecorderBridge = null;
    vi.spyOn(window, "open").mockImplementation(() => null);
  });

  afterEach(async () => {
    await act(async () => {
      root.unmount();
    });
    container.remove();
    delete (window as BridgeWindow).go;
    vi.clearAllMocks();
  });

  it("renders an optional page header Back action", async () => {
    const onClose = vi.fn();

    await act(async () => {
      root.render(<RecordingStudio onClose={onClose} />);
    });

    const backButtons = Array.from(
      container.querySelectorAll(".page-header button"),
    ).filter((button) => button.textContent?.trim() === "Back");
    expect(backButtons).toHaveLength(1);

    await act(async () => {
      backButtons[0]?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("builds macOS proxy shortcuts that save and restore the selected service", () => {
    const startCommand = buildMacOSStartProxyCommand(
      "9090",
      `USB Bob's LAN`,
      "controller.example.com",
    );
    const authenticatedCommand = buildMacOSStartProxyCommand(
      "9090",
      "Wi-Fi",
      "localhost",
      `secret'password`,
    );
    const restoreCommand = buildMacOSRestoreProxyCommand(
      `USB Bob's LAN`,
      "localhost",
      "9090",
      `secret'password`,
    );

    expect(recorderProxyPort("127.0.0.1:9090")).toBe("9090");
    expect(recorderProxyPort("bad-port")).toBe("8081");
    expect(recorderProxyHost(":8081", "https://controller.example.com")).toBe(
      "controller.example.com",
    );
    expect(
      recorderProxyHost("127.0.0.1:8081", "https://controller.example.com"),
    ).toBe("localhost");
    expect(recorderProxyHostIsLocal("controller.example.com")).toBe(false);
    expect(recorderProxyHostIsLocal("localhost")).toBe(true);
    expect(startCommand).toContain(`SERVICE='USB Bob'\\''s LAN'`);
    expect(startCommand).toContain(`PROXY_HOST='controller.example.com'`);
    expect(startCommand).toContain(`PROXY_PORT='9090'`);
    expect(startCommand).toMatch(/^\(\nset -e\n/);
    expect(restoreCommand).toMatch(/^\(\nset -e\n/);
    expect(startCommand).toContain(
      `Library/Application Support/APEX Load/recorder-proxy-state`,
    );
    expect(restoreCommand).toContain(
      `Library/Application Support/APEX Load/recorder-proxy-state`,
    );
    expect(startCommand).not.toContain("TMPDIR");
    expect(restoreCommand).not.toContain("TMPDIR");
    expect(startCommand).toContain(`/usr/bin/shasum -a 256`);
    expect(restoreCommand).toContain(`/usr/bin/shasum -a 256`);
    expect(startCommand).toContain(`exec 9>"$LOCK_FILE"`);
    expect(restoreCommand).toContain(`exec 9>"$LOCK_FILE"`);
    expect(startCommand).toContain(`/usr/bin/lockf -s -t 0 9`);
    expect(restoreCommand).toContain(`/usr/bin/lockf -s -t 0 9`);
    expect(startCommand).not.toContain(`mkdir "$LOCK_DIR"`);
    expect(restoreCommand).not.toContain(`mkdir "$LOCK_DIR"`);
    expect(startCommand).not.toContain(`tr -c 'A-Za-z0-9._-'`);
    expect(startCommand).toContain(`chmod 700 "$STATE_DIR"`);
    expect(startCommand).toContain('networksetup -getwebproxy "$SERVICE"');
    expect(startCommand).toContain(
      'if [ -e "$STATE_HTTP" ] || [ -e "$STATE_HTTPS" ]; then',
    );
    expect(startCommand).toContain(
      "Preserving the existing original proxy backup",
    );
    expect(startCommand).toContain(
      "The macOS proxy state contains an authenticated proxy",
    );
    expect(startCommand).toContain(
      "macOS does not expose arbitrary proxy credentials needed for a safe automatic restore",
    );
    expect(startCommand.indexOf('if [ -e "$STATE_HTTP" ]')).toBeLessThan(
      startCommand.lastIndexOf('networksetup -getwebproxy "$SERVICE"'),
    );
    expect(
      startCommand.indexOf(
        'preflight_proxy_state_pair "$STATE_HTTP" "$STATE_HTTPS"',
      ),
    ).toBeLessThan(
      startCommand.indexOf('networksetup -setwebproxy "$SERVICE"'),
    );
    expect(startCommand).toContain(
      'networksetup -setsecurewebproxystate "$SERVICE" on',
    );
    expect(startCommand).toContain(
      "and rolling back the partial update also failed",
    );
    expect(authenticatedCommand).toContain(`PROXY_USER='apexload'`);
    expect(authenticatedCommand).toContain(
      `PROXY_PASSWORD='secret'\\''password'`,
    );
    expect(authenticatedCommand).toContain(
      'networksetup -setwebproxy "$SERVICE" "$PROXY_HOST" "$PROXY_PORT" on "$PROXY_USER" "$PROXY_PASSWORD"',
    );
    expect(restoreCommand).toContain(`SERVICE='USB Bob'\\''s LAN'`);
    expect(restoreCommand).toContain(`PROXY_HOST='localhost'`);
    expect(restoreCommand).toContain(`PROXY_PORT='9090'`);
    expect(restoreCommand).toContain(`PROXY_PASSWORD='secret'\\''password'`);
    expect(restoreCommand).toContain(
      `preflight_expected_apex_proxy_state_pair "$ROLLBACK_HTTP" "$ROLLBACK_HTTPS"`,
    );
    expect(restoreCommand).toContain(
      'networksetup "$APPLY_STATE_CMD" "$SERVICE" off',
    );
    expect(restoreCommand).toContain(
      'networksetup "$APPLY_SET_CMD" "$SERVICE" "$APPLY_SERVER" "$APPLY_PORT" off',
    );
    expect(restoreCommand).toContain(
      'networksetup "$APPLY_SET_CMD" "$SERVICE" "" 0 off',
    );
    expect(restoreCommand).toContain(
      'verify_proxy_state_pair "$STATE_HTTP" "$STATE_HTTPS" "$VERIFY_HTTP" "$VERIFY_HTTPS"',
    );
    expect(
      restoreCommand.indexOf('networksetup "$APPLY_SET_CMD"'),
    ).toBeLessThan(restoreCommand.indexOf('networksetup "$APPLY_STATE_CMD"'));
    expect(restoreCommand).toContain(
      "The macOS proxy state contains an authenticated proxy",
    );
    expect(restoreCommand).toContain(
      "and rolling back the partial update also failed",
    );
    expect(
      restoreCommand.indexOf(
        'preflight_proxy_state_pair "$STATE_HTTP" "$STATE_HTTPS"',
      ),
    ).toBeLessThan(
      restoreCommand.indexOf(
        'apply_validated_proxy_state_pair "$TARGET_HTTP_ENABLED" "$TARGET_HTTP_SERVER"',
      ),
    );
    expect(restoreCommand).toContain('rm -f "$STATE_HTTP" "$STATE_HTTPS"');
    expect(
      restoreCommand.lastIndexOf(
        'apply_validated_proxy_state_pair "$TARGET_HTTP_ENABLED" "$TARGET_HTTP_SERVER"',
      ),
    ).toBeLessThan(
      restoreCommand.indexOf('rm -f "$STATE_HTTP" "$STATE_HTTPS"'),
    );
    expect(startCommand).toContain(`trap 'exit 1' HUP INT TERM`);
    expect(restoreCommand).toContain(`trap 'exit 1' HUP INT TERM`);
    expect(startCommand).toContain(
      `trap handle_proxy_start_signal HUP INT TERM`,
    );
    expect(restoreCommand).toContain(
      `trap handle_proxy_restore_signal HUP INT TERM`,
    );
  });

  it("shows the protected desktop proxy credentials and lets the user copy the password", async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: "127.0.0.1:49161",
      proxy_auth_token: "per-launch-proxy-secret",
      ca_cert_path: "private/recorder/ca-cert.pem",
      active: null,
      browser: {
        availability: { available: true },
        active: null,
        install: { status: "idle" },
      },
    });
    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();
    await openProxyAdvanced(container);
    expect(container.textContent).toContain("per-launch-proxy-secret");
    expect(container.textContent).toContain("username apexload");
    const copy = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Copy password",
    );
    await act(async () => copy?.click());
    expect(clipboardWriteMock).toHaveBeenCalledWith("per-launch-proxy-secret");
  });

  it("builds Windows 11 proxy shortcuts that save and restore per-user proxy settings", () => {
    const startCommand = buildWindowsStartProxyCommand(
      "9090",
      "controller.example.com",
    );
    const restoreCommand = buildWindowsRestoreProxyCommand(
      "9090",
      "controller.example.com",
    );
    const fallbackStartCommand = buildWindowsStartProxyCommand("not-a-port");

    expect(startCommand).toContain(`$proxyHost = 'controller.example.com'`);
    expect(startCommand).toContain(`$proxyPort = '9090'`);
    expect(startCommand).toContain(
      `$proxyServer = 'http={0}:{1};https={0}:{1}' -f $proxyHost, $proxyPort`,
    );
    expect(startCommand).toContain(
      `$settingsPath = 'HKCU:\\Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings'`,
    );
    expect(startCommand).toContain(
      `[Environment+SpecialFolder]::LocalApplicationData`,
    );
    expect(restoreCommand).toContain(
      `[Environment+SpecialFolder]::LocalApplicationData`,
    );
    expect(restoreCommand).toContain(`$proxyHost = 'controller.example.com'`);
    expect(restoreCommand).toContain(`$proxyPort = '9090'`);
    expect(startCommand).toContain(`'APEX Load\\recorder-proxy-state'`);
    expect(startCommand).not.toContain(`$env:TEMP`);
    expect(restoreCommand).not.toContain(`$env:TEMP`);
    expect(startCommand).toContain(
      `Get-ItemProperty -LiteralPath $settingsPath -Name $Name`,
    );
    expect(startCommand).toContain(`$key.GetValueNames() -contains $Name`);
    expect(startCommand).toContain(
      `if (!(Test-Path -LiteralPath $statePath)) {`,
    );
    expect(startCommand).toContain(
      `Preserving the existing original proxy backup at {0}.`,
    );
    expect(
      startCommand.indexOf(`Test-Path -LiteralPath $statePath`),
    ).toBeLessThan(
      startCommand.indexOf(
        `[System.IO.File]::Move($stateTempPath, $statePath)`,
      ),
    );
    expect(startCommand).toContain(`[System.IO.FileShare]::None`);
    expect(restoreCommand).toContain(`[System.IO.FileShare]::None`);
    expect(startCommand).toContain(
      `if ($backupAlreadyPresent) { Assert-CurrentApexProxyState -SavedOriginal $savedOriginal }`,
    );
    expect(restoreCommand).toContain(
      `Assert-CurrentApexProxyState -SavedOriginal $state`,
    );
    expect(restoreCommand).toContain(
      `current Windows proxy state does not exactly match the active APEX Load proxy transaction`,
    );
    expect(restoreCommand).toContain(
      `$currentProxyOverride = Get-ProxyRegistryValue 'ProxyOverride'`,
    );
    expect(restoreCommand).toContain(`$null -eq $SavedOriginal.ProxyOverride`);
    expect(restoreCommand).toContain(
      `$currentProxyOverride -ceq [string]$SavedOriginal.ProxyOverride`,
    );
    expect(startCommand).toContain(`$stateStream.Flush($true)`);
    expect(startCommand).toContain(
      `[System.IO.File]::Move($stateTempPath, $statePath)`,
    );
    expect(startCommand).not.toContain(`Set-Content -Path $statePath`);
    expect(startCommand).toContain(
      `$savedOriginal = Read-SavedProxyState $statePath`,
    );
    expect(restoreCommand).toContain(
      `$state = Read-SavedProxyState $statePath`,
    );
    expect(startCommand).toContain(
      `saved APEX Load Windows proxy state is corrupt or incomplete`,
    );
    expect(startCommand).toContain(
      `New-ItemProperty -Path $settingsPath -Name ProxyEnable -PropertyType DWord -Value 1 -Force`,
    );
    expect(startCommand).toContain(`$rollbackState = [pscustomobject]@{`);
    expect(startCommand).toContain(
      `$null = Assert-SavedProxyState -Saved $rollbackState`,
    );
    expect(
      startCommand.indexOf(`$rollbackState = [pscustomobject]@{`),
    ).toBeLessThan(
      startCommand.indexOf(
        `  New-ItemProperty -Path $settingsPath -Name ProxyEnable -PropertyType DWord -Value 1 -Force`,
      ),
    );
    expect(
      startCommand.lastIndexOf(
        `Restore-ProxyRegistryState -SavedState $rollbackState`,
      ),
    ).toBeGreaterThan(
      startCommand.indexOf(
        `  New-ItemProperty -Path $settingsPath -Name ProxyEnable -PropertyType DWord -Value 1 -Force`,
      ),
    );
    expect(startCommand).toContain(
      `Setting the APEX Load Windows proxy failed and rolling back the partial registry update also failed`,
    );
    expect(startCommand).toContain(
      `Remove-ItemProperty -LiteralPath $settingsPath -Name AutoConfigURL -ErrorAction Stop`,
    );
    expect(startCommand).toContain("Notify-ProxySettingsChanged");
    expect(startCommand).toContain(`$sendResult = $native::SendMessageTimeout`);
    expect(startCommand).toContain(
      `[Runtime.InteropServices.Marshal]::GetLastWin32Error()`,
    );
    expect(startCommand).toContain(
      `Windows did not acknowledge the proxy settings change notification`,
    );
    expect(fallbackStartCommand).toContain(`$proxyPort = '8081'`);
    expect(restoreCommand).toContain(
      `Get-Content -LiteralPath $Path -Raw -ErrorAction Stop | ConvertFrom-Json -ErrorAction Stop`,
    );
    expect(restoreCommand).toContain(
      `Restore-DwordRegistryValue -Name ProxyEnable`,
    );
    expect(restoreCommand).toContain(
      `Restore-DwordRegistryValue -Name AutoDetect`,
    );
    expect(restoreCommand).toContain(
      `if (Has-SavedValue -SavedState $SavedState -Name $Name) {`,
    );
    expect(restoreCommand).toContain(`Remove-ProxyRegistryValue $Name`);
    expect(restoreCommand).toContain(
      `Remove-ItemProperty -LiteralPath $settingsPath -Name $Name -ErrorAction Stop`,
    );
    expect(restoreCommand).not.toContain(`DefaultValue`);
    expect(restoreCommand).not.toContain(
      `[string]::IsNullOrWhiteSpace([string]$state.$Name)`,
    );
    expect(restoreCommand).toContain(
      `Restore-StringRegistryValue -Name ProxyServer`,
    );
    expect(restoreCommand).toContain(
      `Restore-StringRegistryValue -Name AutoConfigURL`,
    );
    expect(restoreCommand).toContain(
      `Remove-Item -LiteralPath $statePath -ErrorAction Stop`,
    );
    expect(startCommand).toContain(
      `$null = Assert-SavedProxyState -Saved $snapshot`,
    );
    expect(restoreCommand).toContain(
      `$Saved -isnot [System.Management.Automation.PSCustomObject]`,
    );
    expect(restoreCommand).toContain(`$actual -cnotcontains $name`);
    expect(restoreCommand).toContain(
      `($value -isnot [int]) -and ($value -isnot [long])`,
    );
    expect(restoreCommand).toContain(
      `[long]$value -lt 0 -or [long]$value -gt 1`,
    );
    expect(restoreCommand).toContain(`$value -isnot [string]`);
    expect(
      restoreCommand.indexOf(`$state = Read-SavedProxyState $statePath`),
    ).toBeLessThan(
      restoreCommand.indexOf(`$rollbackState = [pscustomobject]@{`),
    );
    expect(
      restoreCommand.indexOf(`$rollbackState = [pscustomobject]@{`),
    ).toBeLessThan(
      restoreCommand.indexOf(`Restore-ProxyRegistryState -SavedState $state`),
    );
    expect(restoreCommand).toContain(
      `Restore-ProxyRegistryState -SavedState $rollbackState`,
    );
    expect(
      restoreCommand.indexOf(
        `Restore-ProxyRegistryState -SavedState $rollbackState`,
      ),
    ).toBeLessThan(restoreCommand.indexOf(`throw $restoreFailure`));
    expect(restoreCommand.indexOf(`throw $restoreFailure`)).toBeLessThan(
      restoreCommand.indexOf(
        `Remove-Item -LiteralPath $statePath -ErrorAction Stop`,
      ),
    );
  });

  it("renders corrected CA installation and trust instructions", async () => {
    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();
    await openProxyAdvanced(container);

    expect(container.textContent).toContain(
      "Install and trust the APEX Load CA certificate for SSL/TLS",
    );
    expect(container.textContent).toContain("ERR_CERT_AUTHORITY_INVALID");
    expect(container.textContent).toContain("Download CA Certificate (.crt)");
    expect(container.textContent).not.toContain(
      "Download CA Certificate (.pem)",
    );
    expect(container.textContent).toContain("import into your login keychain");
    expect(container.textContent).toContain("Secure Sockets Layer (SSL)");
    expect(container.textContent).toContain("Always Trust");
    expect(container.textContent).toContain(
      "Trusted Root Certification Authorities",
    );
    expect(container.textContent).toContain("Current User");
    expect(container.textContent).toContain(
      "same per-user trust store when recording is finished",
    );
    expect(container.textContent).toContain("administrator-only exception");
    expect(container.textContent).not.toContain("Local Machine → place it");
    expect(container.textContent).not.toContain(
      "Trust this CA to identify websites",
    );
    expect(container.textContent).not.toContain(
      "browser or distro trust store",
    );
    expect(container.textContent).toContain("remove older");

    const formatToggle = container.querySelector(
      ".rec-certificate-format-toggle",
    );
    expect(formatToggle).toBeDefined();
    const crtButton = Array.from(
      formatToggle?.querySelectorAll("button") || [],
    ).find((button) => button.textContent?.trim() === "CRT");
    const pemButton = Array.from(
      formatToggle?.querySelectorAll("button") || [],
    ).find((button) => button.textContent?.trim() === "PEM");
    expect(crtButton?.getAttribute("aria-pressed")).toBe("true");
    expect(pemButton?.getAttribute("aria-pressed")).toBe("false");
  });

  it("switches Step 1 certificate guidance to PEM format", async () => {
    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();
    await openProxyAdvanced(container);

    const formatToggle = container.querySelector(
      ".rec-certificate-format-toggle",
    );
    const crtButton = Array.from(
      formatToggle?.querySelectorAll("button") || [],
    ).find((button) => button.textContent?.trim() === "CRT");
    const pemButton = Array.from(
      formatToggle?.querySelectorAll("button") || [],
    ).find((button) => button.textContent?.trim() === "PEM");
    expect(crtButton).toBeDefined();
    expect(pemButton).toBeDefined();
    if (!pemButton) return;

    await act(async () => {
      pemButton.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(crtButton?.getAttribute("aria-pressed")).toBe("false");
    expect(pemButton.getAttribute("aria-pressed")).toBe("true");
    expect(container.textContent).toContain("Download CA Certificate (.pem)");
    expect(container.textContent).not.toContain(
      "Download CA Certificate (.crt)",
    );
    expect(container.textContent).toContain(
      "Trust this CA to identify websites",
    );
    expect(container.textContent).toContain("browser/profile trust store");
    expect(container.textContent).not.toContain(
      "import into your login keychain",
    );
    expect(container.textContent).not.toContain(
      "Trusted Root Certification Authorities",
    );
    expect(container.textContent).toContain("remove older");
  });

  it("downloads the selected Step 1 certificate format", async () => {
    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();
    await openProxyAdvanced(container);

    const crtDownload = Array.from(container.querySelectorAll("button")).find(
      (button) =>
        button.textContent?.trim() === "Download CA Certificate (.crt)",
    );
    expect(crtDownload).toBeDefined();
    if (!crtDownload) return;

    await act(async () => {
      crtDownload.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(apiFetchMock).toHaveBeenCalledTimes(1);
    expect(apiFetchMock).toHaveBeenLastCalledWith("/recorder/ca.pem");
    expect(downloadBlobMock).toHaveBeenCalledTimes(1);
    expect(downloadBlobMock).toHaveBeenLastCalledWith(
      expect.any(Blob),
      "apexload-ca.crt",
    );

    const formatToggle = container.querySelector(
      ".rec-certificate-format-toggle",
    );
    const pemButton = Array.from(
      formatToggle?.querySelectorAll("button") || [],
    ).find((button) => button.textContent?.trim() === "PEM");
    expect(pemButton).toBeDefined();
    if (!pemButton) return;

    await act(async () => {
      pemButton.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    const pemDownload = Array.from(container.querySelectorAll("button")).find(
      (button) =>
        button.textContent?.trim() === "Download CA Certificate (.pem)",
    );
    expect(pemDownload).toBeDefined();
    if (!pemDownload) return;

    await act(async () => {
      pemDownload.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(apiFetchMock).toHaveBeenCalledTimes(2);
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      "/recorder/ca.pem?format=pem",
    );
    expect(downloadBlobMock).toHaveBeenCalledTimes(2);
    expect(downloadBlobMock).toHaveBeenLastCalledWith(
      expect.any(Blob),
      "apexload-ca.pem",
    );
  });

  it("reviews a recording conversion and saves it through the shared script flow", async () => {
    const onImported = vi.fn();
    const onEdit = vi.fn();
    let resolveSave: ((value: unknown) => void) | undefined;
    apiMock.createScript.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveSave = resolve;
      }),
    );

    await act(async () => {
      root.render(<RecordingStudio onImported={onImported} onEdit={onEdit} />);
    });

    await flushPromises();
    await openProxyAdvanced(container);

    expect(container.textContent).toContain("Recorded Sessions");
    expect(container.textContent).toContain("Checkout Capture");

    const reviewButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Review Script",
    );
    expect(reviewButton).toBeDefined();
    if (!reviewButton) {
      return;
    }

    await act(async () => {
      reviewButton.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });

    await flushPromises();

    expect(apiMock.previewRecordingConversion).toHaveBeenCalledWith(
      "rec-1",
      "Checkout Capture",
      false,
    );
    expect(container.textContent).toContain(
      "Preview generated with 1 actions and 1 correlations",
    );
    expect(container.textContent).toContain("Checkout Preview");
    expect(container.textContent).toContain("review dynamic headers");
    expect(container.textContent).toContain("{{var.session_id}}");
    expect(container.textContent).toContain("85% confidence");
    expect(container.textContent).toContain("reused in a later request");
    expect(container.textContent).toContain("Save To Script Library");
    expect(container.textContent).toContain("Open In Visual Editor");

    const editButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Open In Visual Editor",
    );
    expect(editButton).toBeDefined();
    if (!editButton) {
      return;
    }

    await act(async () => {
      editButton.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });

    expect(onEdit).toHaveBeenCalledWith(
      expect.objectContaining({
        name: "Checkout Preview",
        protocol: "http",
        metadata: expect.objectContaining({
          import: expect.objectContaining({
            source: "recorder",
            preserved: expect.objectContaining({
              correlation_suggestions: [
                expect.objectContaining({
                  name: "session_id",
                  found_at: "Request 1",
                  used_at: ["Request 2"],
                }),
              ],
            }),
          }),
        }),
      }),
    );

    const saveButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Save To Script Library",
    );
    expect(saveButton).toBeDefined();
    if (!saveButton) {
      return;
    }

    act(() => {
      saveButton.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      saveButton.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(apiMock.createScript).toHaveBeenCalledTimes(1);
    const pendingSave = Array.from(
      container.querySelectorAll<HTMLButtonElement>("button"),
    ).find((button) => button.textContent?.trim() === "Saving...");
    expect(pendingSave?.disabled).toBe(true);
    const parameterizeInputs = container.querySelector<HTMLInputElement>(
      ".rec-convert-option input[type='checkbox']",
    );
    await act(async () => {
      parameterizeInputs?.click();
    });
    await act(async () => {
      resolveSave?.({
        id: "script-1",
        name: "Checkout Preview",
        protocol: "http",
        version: "1.0.0",
        created_at: "2026-05-17T00:00:00Z",
      });
      await Promise.resolve();
    });
    await flushPromises();

    expect(apiMock.convertRecording).not.toHaveBeenCalled();
    expect(apiMock.commitGenericImportBatch).toHaveBeenCalledWith(
      expect.objectContaining({
        scripts: [expect.objectContaining({ name: "Checkout Preview" })],
        folder_id: null,
        operation_id: expect.stringMatching(/^recording-proxy-import-/),
        source_profile: "recorder",
        source_recording_session_ids: ["rec-1"],
      }),
    );
    expect(onImported).toHaveBeenCalledWith(
      expect.objectContaining({
        scripts: [expect.objectContaining({ id: "script-1" })],
      }),
    );
    expect(apiMock.createScript).toHaveBeenCalledWith(
      expect.objectContaining({
        name: "Checkout Preview",
        protocol: "http",
        metadata: expect.objectContaining({
          import: expect.objectContaining({
            source: "recorder",
          }),
        }),
      }),
      undefined,
      null,
      "rec-1",
    );
    expect(container.textContent).not.toContain(
      'Script "Checkout Preview" created with 1 correlations detected',
    );
    expect(onImported).toHaveBeenCalledWith(
      expect.objectContaining({
        scripts: [
          expect.objectContaining({
            name: "Checkout Preview",
            protocol: "http",
          }),
        ],
      }),
    );
  });

  it("applies recording parameterization to the draft that is saved", async () => {
    apiMock.previewRecordingConversion.mockResolvedValueOnce({
      script: {
        name: "Parameterized Recording",
        version: "1.0.0",
        protocol: "http",
        actions: [
          {
            name: "Get Order",
            type: "request",
            method: "GET",
            target: "/orders?customer_id=42",
          },
        ],
      },
      correlations: [],
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      stats: {},
      summary: {},
      parameterization: {
        suggestions: [
          {
            id: "pz_recording_customer",
            kind: "data_param",
            var_name: "customer_id",
            replacement: "{{var.customer_id}}",
            sample_value: "42",
            confidence: 0.9,
            rule_id: "test.recording.customer",
            rationale: "Customer id should vary by virtual user",
            auto_apply: true,
            high_risk: false,
            locations: [
              {
                section: "actions",
                action_index: 0,
                action_name: "Get Order",
                field: "target",
              },
            ],
          },
        ],
        summary: {
          total: 1,
          by_kind: { data_param: 1 },
          auto_apply_count: 1,
          review_count: 0,
          high_risk_count: 0,
          pre_applied_count: 0,
        },
      },
    });
    apiMock.applyImportParameterization.mockResolvedValueOnce({
      script: {
        name: "Parameterized Recording",
        version: "1.0.0",
        protocol: "http",
        actions: [
          {
            name: "Get Order",
            type: "request",
            method: "GET",
            target: "/orders?customer_id={{var.customer_id}}",
          },
        ],
      },
      diffs: [
        {
          kind: "value_replaced",
          path: "/actions/0/target",
          suggestion_id: "pz_recording_customer",
        },
      ],
      applied_count: 1,
    });

    await act(async () => root.render(<RecordingStudio />));
    await flushPromises();
    await openProxyAdvanced(container);
    const reviewButton = Array.from(
      container.querySelectorAll<HTMLButtonElement>("button"),
    ).find((button) => button.textContent?.trim() === "Review Script");
    await act(async () => reviewButton?.click());
    await flushPromises();

    expect(container.textContent).toContain("Parameterization Review (1)");
    await act(async () => {
      container
        .querySelector<HTMLButtonElement>(".paramize-review button.btn-primary")
        ?.click();
      await Promise.resolve();
      await Promise.resolve();
    });
    const saveButton = Array.from(
      container.querySelectorAll<HTMLButtonElement>("button"),
    ).find((button) => button.textContent?.trim() === "Save To Script Library");
    await act(async () => {
      saveButton?.click();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(apiMock.applyImportParameterization).toHaveBeenCalledWith(
      expect.objectContaining({
        selected_ids: ["pz_recording_customer"],
      }),
    );
    expect(apiMock.createScript).toHaveBeenCalledWith(
      expect.objectContaining({
        actions: [
          expect.objectContaining({
            target: "/orders?customer_id={{var.customer_id}}",
          }),
        ],
      }),
      undefined,
      null,
      "rec-1",
    );
  });

  it("shows recorder credentials and saves safe references while values are deferred", async () => {
    const onImported = vi.fn();
    const onEdit = vi.fn();
    const maskedScript = {
      name: "Credential Preview",
      version: "1.0.0",
      protocol: "http",
      project_id: "proj-1",
      config: { base_url: "https://shop.example.com" },
      actions: [
        {
          name: "Login",
          type: "request",
          method: "POST",
          target: "/login",
          headers: { Authorization: "Bearer [REDACTED]" },
        },
      ],
    };
    const safeScript = {
      ...maskedScript,
      actions: [
        {
          ...maskedScript.actions[0],
          headers: { Authorization: "Bearer {{secret.LOGIN_TOKEN}}" },
        },
      ],
    };
    apiMock.previewRecordingConversion.mockResolvedValueOnce({
      script: maskedScript,
      scripts: [maskedScript],
      correlations: [],
      warnings: [],
      secret_binding_requirements: [
        {
          id: "login-token",
          path: "/actions/0/headers/Authorization",
          occurrence: 0,
          label: "Authorization credential",
          context: "Login",
          suggested_name: "LOGIN_TOKEN",
          replacement: "Bearer {{secret.LOGIN_TOKEN}}",
        },
      ],
    });
    apiMock.applySecretBindings.mockResolvedValueOnce({
      script: safeScript,
      secret_binding_requirements: [],
    });

    await act(async () => {
      root.render(
        <RecordingStudio
          activeProject={{ id: "proj-1", name: "Web" } as any}
          onImported={onImported}
          onEdit={onEdit}
        />,
      );
    });
    await flushPromises();
    await openProxyAdvanced(container);

    const reviewButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Review Script",
    );
    await act(async () => {
      reviewButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(container.textContent).toContain("Credential groups (1)");
    expect(container.textContent).toContain("Configure later");
    expect(container.textContent).toContain("Save with 1 secret to configure");
    const saveButton = Array.from(
      container.querySelectorAll<HTMLButtonElement>("button"),
    ).find(
      (button) =>
        button.textContent?.trim() === "Save with 1 secret to configure",
    );
    const editButton = Array.from(
      container.querySelectorAll<HTMLButtonElement>("button"),
    ).find((button) => button.textContent?.trim() === "Open In Visual Editor");
    const bindingInput = container.querySelector<HTMLInputElement>(
      '.secret-binding-review input[type="text"]',
    )!;
    act(() => setInput(bindingInput, "INVALID/NAME"));
    expect(saveButton?.disabled).toBe(true);
    expect(editButton?.disabled).toBe(true);
    expect(apiMock.applySecretBindings).not.toHaveBeenCalled();
    act(() => setInput(bindingInput, "LOGIN_TOKEN"));
    expect(saveButton?.disabled).toBe(false);
    expect(editButton?.disabled).toBe(false);
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(apiMock.applySecretBindings).toHaveBeenCalledWith({
      script: expect.objectContaining({ name: "Credential Preview" }),
      bindings: [{ requirement_id: "login-token", secret_name: "LOGIN_TOKEN" }],
    });
    expect(apiMock.createProjectSecrets).not.toHaveBeenCalled();
    expect(apiMock.createScript).toHaveBeenCalledWith(
      expect.objectContaining({
        actions: [
          expect.objectContaining({
            headers: { Authorization: "Bearer {{secret.LOGIN_TOKEN}}" },
          }),
        ],
      }),
      "proj-1",
      null,
      "rec-1",
    );
    expect(onImported).toHaveBeenCalled();
  });

  it("explains durable project-secret recovery and reuses the configured value on recorder save retry", async () => {
    const maskedScript = {
      name: "Credential Retry",
      version: "1.0.0",
      protocol: "http",
      project_id: "proj-1",
      config: { base_url: "https://shop.example.com" },
      actions: [{
        name: "Login",
        type: "request",
        method: "POST",
        target: "/login",
        headers: { Authorization: "Bearer [REDACTED]" },
      }],
    };
    const safeScript = {
      ...maskedScript,
      actions: [{
        ...maskedScript.actions[0],
        headers: { Authorization: "Bearer {{secret.LOGIN_TOKEN}}" },
      }],
    };
    apiMock.previewRecordingConversion.mockResolvedValueOnce({
      script: maskedScript,
      scripts: [maskedScript],
      correlations: [],
      warnings: [],
      secret_binding_requirements: [{
        id: "login-token",
        path: "/actions/0/headers/Authorization",
        occurrence: 0,
        label: "Authorization credential",
        context: "Login",
        suggested_name: "LOGIN_TOKEN",
        replacement: "Bearer {{secret.LOGIN_TOKEN}}",
      }],
    });
    apiMock.applySecretBindings.mockResolvedValueOnce({
      script: safeScript,
      secret_binding_requirements: [],
    });
    apiMock.commitGenericImportBatch.mockRejectedValueOnce(
      new Error("temporary recorder script-store failure"),
    );

    await act(async () => {
      root.render(
        <RecordingStudio
          activeProject={{ id: "proj-1", name: "Web" } as any}
          onImported={vi.fn()}
        />,
      );
    });
    await flushPromises();
    await openProxyAdvanced(container);
    const reviewButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Review Script",
    );
    await act(async () => {
      reviewButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    const valueInput = container.querySelector<HTMLInputElement>(
      'input[aria-label="Secret value for LOGIN_TOKEN"]',
    )!;
    act(() => setInput(valueInput, "write-only-recorder-value"));
    let saveButton = Array.from(
      container.querySelectorAll<HTMLButtonElement>(".rec-result-actions button"),
    ).find((button) => button.classList.contains("btn-primary"));
    await act(async () => {
      saveButton?.click();
    });
    await flushPromises();

    expect(apiMock.createProjectSecrets).toHaveBeenCalledWith("proj-1", [{
      name: "LOGIN_TOKEN",
      value: "write-only-recorder-value",
    }]);
    expect(container.textContent).toContain(
      "Project-secret values configured during this review remain in Project Secrets independently of script saving",
    );
    expect(container.textContent).toContain(
      "retrying this save reuses those configured values",
    );
    expect(container.textContent).toContain(
      "applies to scripts, not external secret-store writes",
    );
    expect(container.textContent).toContain("temporary recorder script-store failure");

    saveButton = Array.from(
      container.querySelectorAll<HTMLButtonElement>(".rec-result-actions button"),
    ).find((button) => button.classList.contains("btn-primary"));
    await act(async () => {
      saveButton?.click();
    });
    await flushPromises();

    expect(apiMock.createProjectSecrets).toHaveBeenCalledTimes(1);
    expect(apiMock.createScript).toHaveBeenCalledTimes(1);
    expect(apiMock.commitGenericImportBatch).toHaveBeenCalledTimes(2);
    const firstOperationID = apiMock.commitGenericImportBatch.mock.calls[0][0]
      .operation_id;
    expect(apiMock.commitGenericImportBatch.mock.calls[1][0]).toEqual(
      expect.objectContaining({
        operation_id: firstOperationID,
        source_profile: "recorder",
        source_recording_session_ids: ["rec-1"],
      }),
    );
    expect(container.textContent).toContain('Script "Credential Retry" created');
  });

  it("saves a preservation-marked recorder draft without masking its imported credential", async () => {
    const onImported = vi.fn();
    const onEdit = vi.fn();
    const preservedScript = {
      name: "Runnable Credential Preview",
      version: "1.0.0",
      protocol: "http",
      project_id: "proj-1",
      config: { base_url: "https://shop.example.com" },
      metadata: {
        import: {
          source: "recorder",
          preserved: { import_value_policy: "preserve" },
        },
      },
      actions: [
        {
          name: "Login",
          type: "request",
          method: "POST",
          target: "/login",
          headers: { Authorization: "Bearer imported-runtime-token" },
        },
      ],
    };
    apiMock.previewRecordingConversion.mockResolvedValueOnce({
      script: preservedScript,
      scripts: [preservedScript],
      import_draft_expires_at: "2099-07-26T12:30:00Z",
      correlations: [],
      warnings: [],
      secret_binding_requirements: [
        {
          id: "login-token",
          path: "/actions/0/headers/Authorization",
          occurrence: 0,
          label: "Authorization credential",
          context: "Login",
          suggested_name: "LOGIN_TOKEN",
          replacement: "Bearer {{secret.LOGIN_TOKEN}}",
          destination_value_preserved: true,
        },
      ],
    });

    await act(async () => {
      root.render(
        <RecordingStudio
          activeProject={{ id: "proj-1", name: "Web" } as any}
          onImported={onImported}
          onEdit={onEdit}
        />,
      );
    });
    await flushPromises();
    await openProxyAdvanced(container);
    const reviewButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Review Script",
    );
    await act(async () => {
      reviewButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(container.textContent).toContain("Preserved in script");
    expect(container.textContent).toContain("0 bindings need setup");
    expect(container.textContent).not.toContain("Save with 1 secret to configure");
    const saveButton = Array.from(
      container.querySelectorAll<HTMLButtonElement>("button"),
    ).find(
      (button) =>
        button.textContent?.trim() === "Save And Open In Visual Editor",
    );
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(apiMock.applySecretBindings).not.toHaveBeenCalled();
    expect(apiMock.createScript).toHaveBeenCalledWith(
      expect.objectContaining({
        actions: [
          expect.objectContaining({
            headers: { Authorization: "Bearer imported-runtime-token" },
          }),
        ],
      }),
      "proj-1",
      null,
      "rec-1",
    );
    expect(onImported).toHaveBeenCalled();
    expect(onEdit).not.toHaveBeenCalled();
  });

  it("locks recording parameterization while credentials are being prepared for the editor", async () => {
    const maskedScript = {
      name: "Credential Parameterization Preview",
      version: "1.0.0",
      protocol: "http",
      project_id: "proj-1",
      config: { base_url: "https://shop.example.com" },
      actions: [
        {
          name: "Login",
          type: "request",
          method: "POST",
          target: "/login?customer_id=42",
          headers: { Authorization: "Bearer [REDACTED]" },
        },
      ],
    };
    apiMock.previewRecordingConversion.mockResolvedValueOnce({
      script: maskedScript,
      scripts: [maskedScript],
      correlations: [],
      warnings: [],
      secret_binding_requirements: [
        {
          id: "login-token",
          path: "/actions/0/headers/Authorization",
          occurrence: 0,
          label: "Authorization credential",
          context: "Login",
          suggested_name: "LOGIN_TOKEN",
          replacement: "Bearer {{secret.LOGIN_TOKEN}}",
        },
      ],
      parameterization: {
        suggestions: [
          {
            id: "pz_recording_customer",
            kind: "data_param",
            var_name: "customer_id",
            replacement: "{{var.customer_id}}",
            sample_value: "42",
            confidence: 0.9,
            rule_id: "test.recording.customer",
            rationale: "Customer id should vary by virtual user",
            auto_apply: true,
            high_risk: false,
          },
        ],
        summary: {
          total: 1,
          by_kind: { data_param: 1 },
          auto_apply_count: 1,
          review_count: 0,
          high_risk_count: 0,
          pre_applied_count: 0,
        },
      },
    });
    apiMock.applySecretBindings.mockImplementationOnce(
      () => new Promise(() => {}),
    );

    await act(async () => {
      root.render(
        <RecordingStudio
          activeProject={{ id: "proj-1", name: "Web" } as any}
          onEdit={vi.fn()}
        />,
      );
    });
    await flushPromises();
    await openProxyAdvanced(container);
    const reviewButton = Array.from(
      container.querySelectorAll<HTMLButtonElement>("button"),
    ).find((button) => button.textContent?.trim() === "Review Script");
    await act(async () => reviewButton?.click());
    await flushPromises();
    const editButton = Array.from(
      container.querySelectorAll<HTMLButtonElement>("button"),
    ).find((button) => button.textContent?.trim() === "Open In Visual Editor");
    await act(async () => {
      editButton?.click();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(
      container.querySelector<HTMLButtonElement>(
        ".paramize-review button.btn-primary",
      )?.disabled,
    ).toBe(true);
    expect(container.textContent).toContain("Preparing editor...");
  });

  it("ignores a late recorder credential result after a newer conversion", async () => {
    const buildResult = (
      name: string,
      requirements: unknown[],
      sourceSecretSessionId?: string,
    ) => ({
      script: {
        name,
        version: "1.0.0",
        protocol: "http",
        config: { base_url: "" },
        actions: [
          { name: "GET /", type: "request", method: "GET", target: "/" },
        ],
      },
      scripts: [],
      correlations: [],
      warnings: [],
      secret_binding_requirements: requirements,
      source_secret_session_id: sourceSecretSessionId,
    });
    const requirement = {
      id: "auth",
      path: "/actions/0/headers/Authorization",
      label: "Authorization",
      context: "Bearer credential",
      suggested_name: "API_TOKEN",
    };
    apiMock.previewRecordingConversion
      .mockResolvedValueOnce(
        buildResult("Generation A", [requirement], "recording-source-a"),
      )
      .mockResolvedValueOnce(
        buildResult("Generation B", [], "recording-source-b"),
      );
    let resolveApply: ((value: unknown) => void) | undefined;
    apiMock.applySecretBindings.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveApply = resolve;
      }),
    );

    await act(async () =>
      root.render(
        <RecordingStudio
          activeProject={{ id: "proj-1", name: "Web" } as any}
        />,
      ),
    );
    await flushPromises();
    await openProxyAdvanced(container);
    const reviewButton = Array.from(
      container.querySelectorAll<HTMLButtonElement>("button"),
    ).find((button) => button.textContent?.trim() === "Review Script")!;
    await act(async () => reviewButton.click());
    await flushPromises();
    const saveButton = Array.from(
      container.querySelectorAll<HTMLButtonElement>("button"),
    ).find(
      (button) =>
        button.textContent?.trim() === "Save with 1 secret to configure",
    )!;
    act(() => saveButton.click());
    await flushPromises();
    await act(async () => reviewButton.click());
    await flushPromises();
    await act(async () => {
      resolveApply?.({
        script: buildResult("Resolved A", []).script,
        secret_binding_requirements: [],
      });
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(container.textContent).toContain("Generation B");
    expect(container.textContent).not.toContain("Resolved A");
    expect(apiMock.createScript).not.toHaveBeenCalled();
    expect(apiMock.discardImportSecretSession).toHaveBeenCalledWith({
      project_id: "proj-1",
      source_secret_session_id: "recording-source-a",
    });
  });

  it("treats preview scripts with empty ids as unsaved drafts and reconciles a save after Back", async () => {
    const onImported = vi.fn();
    const onEdit = vi.fn();
    const onClose = vi.fn();
    let resolveSave: ((value: unknown) => void) | undefined;
    apiMock.createScript.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveSave = resolve;
      }),
    );
    apiMock.previewRecordingConversion.mockResolvedValueOnce({
      script: {
        id: "",
        name: "Unsaved Preview",
        protocol: "http",
        version: "1.0.0",
        actions: [
          { name: "01 - GET /", type: "request", method: "GET", target: "/" },
        ],
      },
      correlations: [],
      warnings: [],
    });

    await act(async () => {
      root.render(
        <RecordingStudio
          onImported={onImported}
          onEdit={onEdit}
          onClose={onClose}
        />,
      );
    });
    await flushPromises();
    await openProxyAdvanced(container);

    const reviewButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Review Script",
    );
    expect(reviewButton).toBeDefined();
    await act(async () => {
      reviewButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(container.textContent).toContain("Unsaved Preview");
    const editButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Open In Visual Editor",
    );
    await act(async () => {
      editButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(onEdit).toHaveBeenCalledWith(
      expect.objectContaining({ name: "Unsaved Preview", protocol: "http" }),
    );

    const saveButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Save To Script Library",
    );
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(apiMock.createScript).toHaveBeenCalledWith(
      expect.objectContaining({ name: "Unsaved Preview", protocol: "http" }),
      undefined,
      null,
      "rec-1",
    );
    const backButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Back",
    );
    await act(async () => {
      backButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(onClose).toHaveBeenCalledTimes(1);

    await act(async () => {
      resolveSave?.({
        id: "saved-unsaved-preview",
        name: "Unsaved Preview",
        protocol: "http",
        version: "1.0.0",
        created_at: "2026-05-17T00:00:00Z",
      });
      await Promise.resolve();
    });
    await flushPromises();
    expect(onImported).toHaveBeenCalledWith(
      expect.objectContaining({
        scripts: [
          expect.objectContaining({
            id: "saved-unsaved-preview",
            name: "Unsaved Preview",
            protocol: "http",
          }),
        ],
        stayInImportReview: true,
      }),
    );
  });

  it("confirms before deleting a stopped proxy recording", async () => {
    const confirmSpy = vi
      .spyOn(window, "confirm")
      .mockReturnValueOnce(false)
      .mockReturnValueOnce(true);
    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();
    await openProxyAdvanced(container);

    const deleteButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Delete",
    );
    expect(deleteButton).toBeDefined();
    if (!deleteButton) return;

    await act(async () => {
      deleteButton.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(confirmSpy).toHaveBeenCalledWith(
      "Delete this stopped recording session? This cannot be undone.",
    );
    expect(apiMock.deleteRecording).not.toHaveBeenCalled();

    await act(async () => {
      deleteButton.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();
    expect(apiMock.deleteRecording).toHaveBeenCalledWith("rec-1");
  });

  it("streams proxy HAR exports through the native desktop bridge", async () => {
    saveApiResourceViaBridgeMock.mockResolvedValue("saved");
    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();
    await openProxyAdvanced(container);

    const exportButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Export HAR",
    );
    expect(exportButton).toBeDefined();
    await act(async () => {
      exportButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(saveApiResourceViaBridgeMock).toHaveBeenCalledWith(
      "/api/v1/recorder/rec-1/export?format=har",
      "checkout-capture.har",
    );
    expect(apiFetchMock).not.toHaveBeenCalledWith(
      "/recorder/rec-1/export?format=har",
    );
    expect(downloadBlobMock).not.toHaveBeenCalled();
    expect(container.textContent).toContain(
      'Exported "Checkout Capture" as HAR',
    );
  });

  it("does not report a cancelled native proxy HAR save as exported", async () => {
    saveApiResourceViaBridgeMock.mockResolvedValue("cancelled");
    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();
    await openProxyAdvanced(container);

    const exportButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Export HAR",
    );
    await act(async () => {
      exportButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(apiFetchMock).not.toHaveBeenCalledWith(
      "/recorder/rec-1/export?format=har",
    );
    expect(downloadBlobMock).not.toHaveBeenCalled();
    expect(container.textContent).not.toContain(
      'Exported "Checkout Capture" as HAR',
    );
  });

  it("retains the hosted Blob fallback for proxy HAR exports", async () => {
    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();
    await openProxyAdvanced(container);

    const exportButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Export HAR",
    );
    await act(async () => {
      exportButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(saveApiResourceViaBridgeMock).toHaveBeenCalledWith(
      "/api/v1/recorder/rec-1/export?format=har",
      "checkout-capture.har",
    );
    expect(apiFetchMock).toHaveBeenCalledWith(
      "/recorder/rec-1/export?format=har",
    );
    expect(downloadBlobMock).toHaveBeenCalledWith(
      expect.any(Blob),
      "checkout-capture.har",
    );
  });

  it("streams proxy HAR exports without Blob buffering in the desktop bootstrap browser", async () => {
    hasDesktopApiCapabilityMock.mockReturnValue(true);
    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();
    await openProxyAdvanced(container);

    const exportButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Export HAR",
    );
    await act(async () => {
      exportButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(startDesktopBrowserDownloadMock).toHaveBeenCalledWith(
      "/api/v1/recorder/rec-1/export?format=har",
      "checkout-capture.har",
    );
    expect(apiFetchMock).not.toHaveBeenCalledWith(
      "/recorder/rec-1/export?format=har",
    );
    expect(downloadBlobMock).not.toHaveBeenCalled();
    expect(container.textContent).toContain(
      'HAR download request opened for "Checkout Capture"',
    );
  });

  it("surfaces stopped browser recordings after reload and can delete them", async () => {
    const confirmSpy = vi.spyOn(window, "confirm").mockReturnValue(true);
    apiMock.listBrowserRecordings.mockResolvedValue({
      sessions: [
        {
          id: "brec-stopped",
          name: "Checkout Browser",
          status: "stopped",
          start_url: "https://shop.example/login",
          capture_har: true,
          started_at: "2026-05-12T20:00:00Z",
          stopped_at: "2026-05-12T20:05:00Z",
          step_count: 4,
          navigation_count: 2,
          network_request_count: 9,
          websocket_count: 0,
          warning_count: 0,
          har_available: true,
        },
      ],
      count: 1,
    });

    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();

    const recoveryList = container.querySelector(
      '[data-testid="browser-recording-recovery-list"]',
    );
    expect(recoveryList?.textContent).toContain("Recent Browser Recordings");
    expect(recoveryList?.textContent).toContain("Checkout Browser");
    expect(recoveryList?.textContent).toContain("4 steps");
    expect(recoveryList?.textContent).toContain("9 requests");

    const openButton = Array.from(
      recoveryList?.querySelectorAll("button") || [],
    ).find((button) => button.textContent?.trim() === "Open");
    await act(async () => {
      openButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();
    expect(apiMock.getBrowserRecording).toHaveBeenCalledWith("brec-stopped");

    const deleteButton = Array.from(
      recoveryList?.querySelectorAll("button") || [],
    ).find((button) => button.textContent?.trim() === "Delete");
    await act(async () => {
      deleteButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();
    expect(confirmSpy).toHaveBeenCalledWith(
      "Delete this stopped browser recording? This cannot be undone.",
    );
    expect(apiMock.deleteBrowserRecording).toHaveBeenCalledWith("brec-stopped");
  });

  it("normalizes stored proxy conversion results before edit or save handoff", async () => {
    const onImported = vi.fn();
    const onEdit = vi.fn();
    const nestedScript = {
      name: "Checkout Converted",
      version: "1.0.0",
      protocol: "http",
      config: { base_url: "https://shop.example.com" },
      actions: [
        { name: "01 - GET /", type: "request", method: "GET", target: "/" },
      ],
    };
    apiMock.previewRecordingConversion.mockResolvedValueOnce({
      script: {
        id: "stored-recorder-script",
        name: "Checkout Converted",
        protocol: "http",
        version: "1.0.0",
        created_at: "2026-05-17T00:00:00Z",
        script: nestedScript,
      },
      correlations: [],
      warnings: [],
    });

    await act(async () => {
      root.render(<RecordingStudio onImported={onImported} onEdit={onEdit} />);
    });
    await flushPromises();
    await openProxyAdvanced(container);

    const convertButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Convert to Script",
    );
    expect(convertButton).toBeDefined();
    if (!convertButton) return;
    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(container.textContent).toContain("stored-recorder-script");
    const editButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Open In Visual Editor",
    );
    expect(editButton).toBeDefined();
    await act(async () => {
      editButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(onEdit).toHaveBeenCalledWith(
      expect.objectContaining({ actions: nestedScript.actions }),
    );

    const saveButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Save To Script Library",
    );
    expect(saveButton).toBeDefined();
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });

    expect(apiMock.createScript).not.toHaveBeenCalled();
    expect(onImported).toHaveBeenCalledWith({
      scripts: [
        expect.objectContaining({
          id: "stored-recorder-script",
          script: expect.objectContaining({ actions: nestedScript.actions }),
        }),
      ],
    });
  });

  it("reviews Salesforce-looking proxy recordings through the native HTTP importer", async () => {
    const onImported = vi.fn();
    const onEdit = vi.fn();
    let resolveSave: ((value: unknown) => void) | undefined;
    apiMock.createScript.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveSave = resolve;
      }),
    );
    apiMock.listRecordings.mockResolvedValueOnce({
      sessions: [
        {
          id: "rec-sf",
          name: "Salesforce Capture",
          status: "stopped",
          started_at: "2026-05-12T20:00:00Z",
          base_url: "https://acme.my.salesforce.com",
          requests: [
            {
              seq: 1,
              timestamp: "2026-05-12T20:00:01Z",
              method: "POST",
              url: "https://acme.my.salesforce.com/aura",
              path: "/aura",
              host: "acme.my.salesforce.com",
              status_code: 200,
              response_time_ms: 88,
              content_type: "application/json",
              body: "",
              think_time_ms: 0,
              is_static: false,
            },
          ],
          filters: { exclude_static: true },
        },
      ],
    });

    await act(async () => {
      root.render(<RecordingStudio onImported={onImported} onEdit={onEdit} />);
    });
    await flushPromises();
    await openProxyAdvanced(container);

    expect(container.textContent).toContain("Review Salesforce HTTP");

    const reviewButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Review Salesforce HTTP",
    );
    expect(reviewButton).toBeDefined();
    if (!reviewButton) return;

    await act(async () => {
      reviewButton.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(apiFetchMock).toHaveBeenCalledWith(
      "/recorder/rec-sf/export?format=har",
    );
    expect(apiMock.importSalesforce).toHaveBeenCalledWith(
      expect.any(File),
      expect.objectContaining({
        name: "Salesforce Capture Salesforce HTTP",
        base_url: "https://acme.my.salesforce.com",
      }),
    );
    expect(container.textContent).toContain("Salesforce HTTP Draft");
    expect(container.textContent).toContain("Salesforce Capture HTTP");
    expect(container.textContent).toContain("100%");
    expect(container.textContent).toContain(
      "Review Salesforce correlation extractors before approval.",
    );

    expect(container.textContent).toContain("Save with 1 secret to configure");
    const editButton = Array.from(
      container.querySelectorAll<HTMLButtonElement>("button"),
    ).find((button) => button.textContent?.trim() === "Open In Visual Editor");
    const deferredSaveButton = Array.from(
      container.querySelectorAll<HTMLButtonElement>("button"),
    ).find(
      (button) =>
        button.textContent?.trim() === "Save with 1 secret to configure",
    );
    const bindingInput = container.querySelector<HTMLInputElement>(
      '.secret-binding-review input[type="text"]',
    )!;
    act(() => setInput(bindingInput, "INVALID/NAME"));
    expect(editButton?.disabled).toBe(true);
    expect(deferredSaveButton?.disabled).toBe(true);
    act(() => setInput(bindingInput, "SALESFORCE_SESSION_TOKEN"));
    expect(editButton?.disabled).toBe(false);
    expect(deferredSaveButton?.disabled).toBe(false);
    expect(editButton).toBeDefined();
    if (!editButton) return;
    await act(async () => {
      editButton.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();
    expect(onEdit).toHaveBeenCalledWith(
      expect.objectContaining({
        name: "Salesforce Capture HTTP",
        protocol: "http",
      }),
    );

    const saveButton = container.querySelector<HTMLButtonElement>(
      ".rec-convert-result .rec-result-actions .btn-primary",
    );
    expect(saveButton).not.toBeNull();
    expect(saveButton?.disabled).toBe(false);
    if (!saveButton) return;
    act(() => {
      saveButton.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      saveButton.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(apiMock.createScript).toHaveBeenCalledTimes(1);
    expect(
      container.querySelector<HTMLButtonElement>(
        ".rec-convert-result .rec-result-actions .btn-primary",
      )?.textContent,
    ).toBe("Saving...");
    expect(
      container.querySelector<HTMLButtonElement>(
        ".rec-convert-result .rec-result-actions .btn-primary",
      )?.disabled,
    ).toBe(true);
    // The Salesforce-analyzed draft is not a verbatim conversion of the
    // recording, so the save must not claim the recording session as its
    // source — the server's provenance attestation would reject it.
    expect(apiMock.createScript).toHaveBeenCalledWith(
      expect.objectContaining({ name: "Salesforce Capture HTTP" }),
      undefined,
      null,
    );
    expect(apiMock.commitGenericImportBatch).toHaveBeenCalledWith(
      expect.objectContaining({
        scripts: [expect.objectContaining({ name: "Salesforce Capture HTTP" })],
        source_profile: "salesforce",
        operation_id: expect.any(String),
      }),
    );
    await act(async () => {
      resolveSave?.({
        id: "salesforce-script",
        name: "Salesforce Capture HTTP",
        protocol: "http",
        version: "1.0.0",
        created_at: "2026-05-17T00:00:00Z",
      });
      await Promise.resolve();
    });
    await flushPromises();
    expect(onImported).toHaveBeenCalledWith(
      expect.objectContaining({
        scripts: [expect.objectContaining({ name: "Salesforce Capture HTTP" })],
      }),
    );
  });

  it("ignores a late Salesforce credential result after a newer native review", async () => {
    apiMock.listRecordings.mockResolvedValueOnce({
      sessions: [
        {
          id: "rec-sf",
          name: "Salesforce Capture",
          status: "stopped",
          started_at: "2026-05-12T20:00:00Z",
          base_url: "https://acme.my.salesforce.com",
          requests: [
            {
              seq: 1,
              method: "POST",
              url: "https://acme.my.salesforce.com/aura",
              path: "/aura",
              host: "acme.my.salesforce.com",
              status_code: 200,
              response_time_ms: 10,
              content_type: "application/json",
              body: "",
              think_time_ms: 0,
              is_static: false,
            },
          ],
          filters: { exclude_static: true },
        },
      ],
    });
    const buildResult = (name: string, requirements: unknown[]) => ({
      scripts: [],
      script: {
        name,
        version: "1.0.0",
        protocol: "http",
        config: { base_url: "https://acme.my.salesforce.com" },
        actions: [
          { name: "Aura", type: "request", method: "POST", target: "/aura" },
        ],
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      correlations: [],
      secret_binding_requirements: requirements,
      stats: { converted_actions: 1 },
      summary: {
        salesforce_review: {
          detected: true,
          har_match_coverage: { coverage_pct: 100 },
        },
      },
    });
    const requirement = {
      id: "sf-auth",
      path: "/actions/0/headers/Authorization",
      label: "Authorization",
      context: "Aura",
      suggested_name: "SF_TOKEN",
    };
    apiMock.importSalesforce
      .mockResolvedValueOnce(buildResult("Salesforce A", [requirement]))
      .mockResolvedValueOnce(buildResult("Salesforce B", []));
    let resolveApply: ((value: unknown) => void) | undefined;
    apiMock.applySecretBindings.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveApply = resolve;
      }),
    );

    await act(async () =>
      root.render(
        <RecordingStudio
          activeProject={{ id: "proj-1", name: "Web" } as any}
        />,
      ),
    );
    await flushPromises();
    await openProxyAdvanced(container);
    const reviewButton = Array.from(
      container.querySelectorAll<HTMLButtonElement>("button"),
    ).find(
      (button) => button.textContent?.trim() === "Review Salesforce HTTP",
    )!;
    await act(async () => reviewButton.click());
    await flushPromises();
    const saveButton = Array.from(
      container.querySelectorAll<HTMLButtonElement>("button"),
    ).find(
      (button) =>
        button.textContent?.trim() === "Save with 1 secret to configure",
    )!;
    act(() => saveButton.click());
    await flushPromises();
    await act(async () => reviewButton.click());
    await flushPromises();
    await act(async () => {
      resolveApply?.({
        script: buildResult("Resolved Salesforce A", []).script,
        secret_binding_requirements: [],
      });
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(container.textContent).toContain("Salesforce B");
    expect(container.textContent).not.toContain("Resolved Salesforce A");
    expect(apiMock.createScript).not.toHaveBeenCalled();
  });

  it("passes the parameterize option through when toggled", async () => {
    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();
    await openProxyAdvanced(container);

    const toggle = Array.from(
      container.querySelectorAll('input[type="checkbox"]'),
    ).find((input) =>
      (input as HTMLInputElement).parentElement?.textContent?.includes(
        "Parameterize input values",
      ),
    ) as HTMLInputElement | undefined;
    expect(toggle).toBeDefined();
    if (!toggle) return;

    await act(async () => {
      toggle.click();
    });

    const convertButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Convert to Script",
    );
    expect(convertButton).toBeDefined();
    if (!convertButton) return;

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(apiMock.previewRecordingConversion).toHaveBeenCalledWith(
      "rec-1",
      "Checkout Capture",
      true,
    );
    expect(apiMock.convertRecording).not.toHaveBeenCalled();
    expect(
      container.querySelector(".rec-convert-result")?.textContent,
    ).toContain("Checkout Preview");

    await act(async () => {
      toggle.click();
    });
    expect(container.querySelector(".rec-convert-result")).toBeNull();
  });

  it("threads the capture cookies option into the start request", async () => {
    apiMock.startRecording.mockResolvedValue({
      session: {
        id: "rec-cookie",
        name: "Recording Session",
        status: "recording",
        started_at: "2026-05-12T20:00:00Z",
        requests: [],
        filters: { exclude_static: true, capture_cookies: true },
      },
      proxy_addr: ":8081",
      ca_cert_path: "artifacts/recorder/ca-cert.pem",
    });

    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();
    await openProxyAdvanced(container);

    const captureToggle = Array.from(
      container.querySelectorAll('input[type="checkbox"]'),
    ).find((input) =>
      (input as HTMLInputElement).parentElement?.textContent?.includes(
        "Capture cookies",
      ),
    ) as HTMLInputElement | undefined;
    expect(captureToggle).toBeDefined();
    if (!captureToggle) return;

    await act(async () => {
      captureToggle.click();
    });

    const startButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Start Recording",
    );
    expect(startButton).toBeDefined();
    if (!startButton) return;

    await act(async () => {
      startButton.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(apiMock.startRecording).toHaveBeenCalledWith(
      expect.objectContaining({
        capture_cookies: true,
        exclude_static: true,
      }),
    );
  });

  it("threads the selected recording profile into the proxy start request", async () => {
    apiMock.getRecordingProfiles.mockResolvedValue({
      profiles: [{ id: "checkout", name: "Checkout profile", http_script_level: "html" }],
      rules: [],
      updated_at: "2026-10-09T00:00:00Z",
    });
    apiMock.startRecording.mockResolvedValue({
      session: {
        id: "rec-profile",
        name: "Recording Session",
        status: "recording",
        started_at: "2026-10-09T00:00:00Z",
        requests: [],
        filters: { exclude_static: true, recording_profile_id: "checkout" },
      },
      proxy_addr: ":8081",
      ca_cert_path: "artifacts/recorder/ca-cert.pem",
    });

    await act(async () => {
      root.render(<RecordingStudio activeProject={{ id: "proj-1", name: "Web" } as any} />);
    });
    await flushPromises();
    expect(apiMock.getRecordingProfiles).toHaveBeenCalledWith("proj-1");
    const picker = container.querySelector('select[aria-label="Recording profile"]') as HTMLSelectElement;
    expect(picker.textContent).toContain("Checkout profile");
    await act(async () => {
      picker.value = "checkout";
      picker.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await openProxyAdvanced(container);
    const startButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Start Recording",
    )!;
    await act(async () => {
      startButton.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(apiMock.startRecording).toHaveBeenCalledWith(
      expect.objectContaining({ project_id: "proj-1", recording_profile_id: "checkout" }),
    );
  });

  it("copies macOS start and restore proxy commands from the setup shortcut", async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: "127.0.0.1:49161",
      proxy_auth_token: "active-recorder-token",
      ca_cert_path: "private/recorder/ca-cert.pem",
      active: null,
      browser: {
        availability: { available: true },
        active: null,
        install: { status: "idle" },
      },
    });
    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();
    await openProxyAdvanced(container);

    expect(container.textContent).not.toContain("Copy Windows Start Command");
    const serviceInput = container.querySelector(
      'input[aria-label="macOS network service"]',
    ) as HTMLInputElement | null;
    expect(serviceInput).toBeDefined();
    if (!serviceInput) return;

    const startShortcut = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Copy Start Proxy Command",
    );
    expect(startShortcut).toBeDefined();
    if (!startShortcut) return;

    await act(async () => {
      startShortcut.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(clipboardWriteMock).toHaveBeenLastCalledWith(
      expect.stringContaining(`SERVICE='Wi-Fi'`),
    );
    expect(clipboardWriteMock).toHaveBeenLastCalledWith(
      expect.stringContaining(`PROXY_HOST='localhost'`),
    );
    expect(clipboardWriteMock).toHaveBeenLastCalledWith(
      expect.stringContaining(`PROXY_PORT='49161'`),
    );
    expect(container.textContent).toContain("macOS start proxy command copied");

    const restoreShortcut = Array.from(
      container.querySelectorAll("button"),
    ).find(
      (button) => button.textContent?.trim() === "Copy Restore Proxy Command",
    );
    expect(restoreShortcut).toBeDefined();
    if (!restoreShortcut) return;

    await act(async () => {
      restoreShortcut.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(clipboardWriteMock).toHaveBeenLastCalledWith(
      expect.stringContaining(
        `apply_validated_proxy_state_pair "$TARGET_HTTP_ENABLED" "$TARGET_HTTP_SERVER"`,
      ),
    );
    expect(clipboardWriteMock).toHaveBeenLastCalledWith(
      expect.stringContaining(`PROXY_PORT='49161'`),
    );
    expect(clipboardWriteMock).toHaveBeenLastCalledWith(
      expect.stringContaining(`PROXY_PASSWORD='active-recorder-token'`),
    );
    expect(container.textContent).toContain(
      "macOS restore proxy command copied",
    );
  });

  it("opens proxy settings through the feature-detected desktop bridge", async () => {
    const openSystemSettings = vi.fn().mockResolvedValue(undefined);
    (window as BridgeWindow).go = {
      main: { App: { OpenSystemSettings: openSystemSettings } },
    };

    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();
    await openProxyAdvanced(container);

    const macSettings = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Open Network Settings",
    );
    expect(macSettings).toBeDefined();
    await act(async () => {
      macSettings?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();
    expect(openSystemSettings).toHaveBeenCalledWith("network");

    const windowsToggle = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Windows 11",
    );
    await act(async () => {
      windowsToggle?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    const windowsSettings = Array.from(
      container.querySelectorAll("button"),
    ).find(
      (button) => button.textContent?.trim() === "Open Windows Proxy Settings",
    );
    await act(async () => {
      windowsSettings?.dispatchEvent(
        new MouseEvent("click", { bubbles: true }),
      );
    });
    await flushPromises();
    expect(openSystemSettings).toHaveBeenCalledWith("proxy");
  });

  it("shows manual proxy-settings guidance when the native bridge is unavailable", async () => {
    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();
    await openProxyAdvanced(container);

    const macSettings = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Open Network Settings",
    );
    await act(async () => {
      macSettings?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });

    expect(container.textContent).toContain(
      "Open System Settings > Network manually on this device.",
    );
  });

  it("copies Windows 11 start and restore proxy commands from the setup shortcut", async () => {
    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();
    await openProxyAdvanced(container);

    const windowsToggle = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Windows 11",
    );
    expect(windowsToggle).toBeDefined();
    if (!windowsToggle) return;

    await act(async () => {
      windowsToggle.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(
      container.querySelector('input[aria-label="macOS network service"]'),
    ).toBeNull();
    const startShortcut = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Copy Windows Start Command",
    );
    expect(startShortcut).toBeDefined();
    if (!startShortcut) return;

    await act(async () => {
      startShortcut.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(clipboardWriteMock).toHaveBeenLastCalledWith(
      expect.stringContaining(`$proxyPort = '8081'`),
    );
    expect(clipboardWriteMock).toHaveBeenLastCalledWith(
      expect.stringContaining(`$proxyHost = 'localhost'`),
    );
    expect(clipboardWriteMock).toHaveBeenLastCalledWith(
      expect.stringContaining("windows-11-wininet.json"),
    );
    expect(clipboardWriteMock).toHaveBeenLastCalledWith(
      expect.stringContaining("Notify-ProxySettingsChanged"),
    );
    expect(container.textContent).toContain(
      "Windows start proxy command copied",
    );

    const restoreShortcut = Array.from(
      container.querySelectorAll("button"),
    ).find(
      (button) => button.textContent?.trim() === "Copy Windows Restore Command",
    );
    expect(restoreShortcut).toBeDefined();
    if (!restoreShortcut) return;

    await act(async () => {
      restoreShortcut.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(clipboardWriteMock).toHaveBeenLastCalledWith(
      expect.stringContaining(`Restore-DwordRegistryValue -Name ProxyEnable`),
    );
    expect(clipboardWriteMock).toHaveBeenLastCalledWith(
      expect.stringContaining(`Restore-DwordRegistryValue -Name AutoDetect`),
    );
    expect(clipboardWriteMock).toHaveBeenLastCalledWith(
      expect.stringContaining(`Restore-StringRegistryValue -Name ProxyServer`),
    );
    expect(container.textContent).toContain(
      "Windows restore proxy command copied",
    );
  });

  it("renders browser and proxy recorder options side by side", async () => {
    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();

    expect(container.textContent).toContain("Record Browser Session");
    expect(
      container.querySelector('[data-testid="rec-proxy-recorder-option"]'),
    ).not.toBeNull();
    expect(container.textContent).toContain("Advanced Proxy Recorder");
    expect(container.textContent).toContain("Proxy Setup");
    expect(container.textContent).not.toContain("Download CA Certificate");
  });

  it("rejects non-http(s) start URLs before calling the API", async () => {
    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();

    const urlInput = container.querySelector(
      'input[aria-label="Start URL address"]',
    ) as HTMLInputElement;
    expect(urlInput).not.toBeNull();
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        window.HTMLInputElement.prototype,
        "value",
      )!.set!;
      setter.call(urlInput, "file:///etc/passwd");
      urlInput.dispatchEvent(new Event("input", { bubbles: true }));
    });
    const recordButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.includes("Start browser recording"),
    );
    expect(recordButton).toBeDefined();
    await act(async () => {
      recordButton!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(apiMock.startBrowserRecording).not.toHaveBeenCalled();
    expect(container.textContent).toContain("Enter an http(s) URL to record");
  });

  it("normalizes pasted full browser start URLs into scheme and address controls", async () => {
    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();

    const scheme = container.querySelector(
      'select[aria-label="Start URL scheme"]',
    ) as HTMLSelectElement;
    const urlInput = container.querySelector(
      'input[aria-label="Start URL address"]',
    ) as HTMLInputElement;
    expect(scheme).not.toBeNull();
    expect(urlInput).not.toBeNull();

    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        window.HTMLInputElement.prototype,
        "value",
      )!.set!;
      setter.call(urlInput, "http://app.example.com:8080/login?source=qa");
      urlInput.dispatchEvent(new Event("input", { bubbles: true }));
    });

    expect(scheme.value).toBe("http://");
    expect(urlInput.value).toBe("app.example.com:8080/login?source=qa");
  });

  it("starts a browser recording and docks the control panel", async () => {
    await act(async () => {
      root.render(
        <RecordingStudio
          activeProject={{ id: "proj-1", name: "Web" } as any}
          folderId="folder-9"
        />,
      );
    });
    await flushPromises();

    const urlInput = container.querySelector(
      'input[aria-label="Start URL address"]',
    ) as HTMLInputElement;
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        window.HTMLInputElement.prototype,
        "value",
      )!.set!;
      setter.call(urlInput, "app.example.com/login");
      urlInput.dispatchEvent(new Event("input", { bubbles: true }));
    });
    const recordButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.includes("Start browser recording"),
    );
    await act(async () => {
      recordButton!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(apiMock.startBrowserRecording).toHaveBeenCalledWith(
      expect.objectContaining({
        start_url: "https://app.example.com/login",
        project_id: "proj-1",
        capture_har: true,
      }),
    );
    // The companion panel opens in the recording browser context; the docked
    // panel stays visible as a fallback/control surface.
    expect(apiMock.openBrowserRecordingControlPanel).toHaveBeenCalledWith(
      "brec-1",
      expect.stringContaining("#/recorder-panel?session=brec-1"),
    );
    expect(apiMock.openBrowserRecordingControlPanel.mock.calls[0][1]).toContain("folderId=folder-9");
    expect(window.open).not.toHaveBeenCalled();
    expect(
      container.querySelector('[data-testid="browser-recorder-panel"]'),
    ).not.toBeNull();
    expect(apiMock.getBrowserRecording).toHaveBeenCalledWith("brec-1");
  });

  it("deduplicates rapid browser recording starts before the button rerenders", async () => {
    let resolveStart!: (value: {
      session: {
        id: string;
        name: string;
        status: string;
        start_url: string;
        capture_har: boolean;
        started_at: string;
        step_count: number;
        navigation_count: number;
        network_request_count: number;
        websocket_count: number;
        warning_count: number;
        har_available: boolean;
      };
    }) => void;
    apiMock.startBrowserRecording.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveStart = resolve;
      }),
    );
    await act(async () => {
      root.render(
        <RecordingStudio
          activeProject={{ id: "proj-1", name: "Web" } as any}
        />,
      );
    });
    await flushPromises();

    const urlInput = container.querySelector(
      'input[aria-label="Start URL address"]',
    ) as HTMLInputElement;
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        window.HTMLInputElement.prototype,
        "value",
      )!.set!;
      setter.call(urlInput, "app.example.com/login");
      urlInput.dispatchEvent(new Event("input", { bubbles: true }));
    });
    const recordButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.includes("Start browser recording"),
    );
    act(() => {
      recordButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      recordButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });

    expect(apiMock.startBrowserRecording).toHaveBeenCalledTimes(1);
    expect(container.textContent).toContain("Starting Browser…");
    await act(async () => {
      resolveStart({
        session: {
          id: "brec-rapid",
          name: "Browser Recording",
          status: "recording",
          start_url: "https://app.example.com/login",
          capture_har: true,
          started_at: "2026-05-12T20:00:00Z",
          step_count: 0,
          navigation_count: 0,
          network_request_count: 0,
          websocket_count: 0,
          warning_count: 0,
          har_available: false,
        },
      });
      await Promise.resolve();
    });
    await flushPromises();
    expect(apiMock.openBrowserRecordingControlPanel).toHaveBeenCalledTimes(1);
  });

  it("does not launch a companion browser panel from a Wails dev webview", async () => {
    // `wails dev` uses the Vite HTTP origin, so native bridge presence is the
    // signal that opening this URL would escape to an unauthenticated browser.
    (window as BridgeWindow).go = { main: { App: {} } };
    await act(async () => {
      root.render(
        <RecordingStudio
          activeProject={{ id: "proj-1", name: "Web" } as any}
        />,
      );
    });
    await flushPromises();

    const urlInput = container.querySelector(
      'input[aria-label="Start URL address"]',
    ) as HTMLInputElement;
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        window.HTMLInputElement.prototype,
        "value",
      )!.set!;
      setter.call(urlInput, "app.example.com/login");
      urlInput.dispatchEvent(new Event("input", { bubbles: true }));
    });
    const recordButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.includes("Start browser recording"),
    );
    await act(async () => {
      recordButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(apiMock.startBrowserRecording).toHaveBeenCalled();
    expect(apiMock.openBrowserRecordingControlPanel).not.toHaveBeenCalled();
    expect(window.open).not.toHaveBeenCalled();
  });

  it("hands the desktop capability to a safe popup when the recording browser cannot open the panel tab", async () => {
    apiMock.openBrowserRecordingControlPanel.mockRejectedValueOnce(
      new Error("control panel route unavailable"),
    );
    hasDesktopApiCapabilityMock.mockReturnValue(true);
    window.sessionStorage.setItem(
      "apexload.desktopCapability",
      "browser-tab-capability",
    );
    const childStorage = {
      clear: vi.fn(),
      setItem: vi.fn(),
    };
    const replace = vi.fn();
    const popup = {
      opener: window,
      sessionStorage: childStorage,
      location: { replace },
      close: vi.fn(),
    } as unknown as Window;
    vi.mocked(window.open).mockReturnValueOnce(popup);
    await act(async () => {
      root.render(
        <RecordingStudio
          activeProject={{ id: "proj-1", name: "Web" } as any}
        />,
      );
    });
    await flushPromises();

    const urlInput = container.querySelector(
      'input[aria-label="Start URL address"]',
    ) as HTMLInputElement;
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        window.HTMLInputElement.prototype,
        "value",
      )!.set!;
      setter.call(urlInput, "https://app.example.com/login");
      urlInput.dispatchEvent(new Event("input", { bubbles: true }));
    });
    const recordButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.includes("Start browser recording"),
    );
    await act(async () => {
      recordButton!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(apiMock.openBrowserRecordingControlPanel).toHaveBeenCalledWith(
      "brec-1",
      expect.stringContaining("#/recorder-panel?session=brec-1"),
    );
    expect(window.open).toHaveBeenCalledWith(
      "",
      "_blank",
      "width=440,height=760",
    );
    expect(childStorage.clear).toHaveBeenCalledTimes(1);
    expect(childStorage.setItem).toHaveBeenCalledWith(
      "apexload.desktopCapability",
      "browser-tab-capability",
    );
    expect((popup as unknown as { opener: unknown }).opener).toBeNull();
    expect(replace).toHaveBeenCalledWith(
      expect.stringContaining("#/recorder-panel?session=brec-1"),
    );
  });

  it("falls back to the Advanced proxy recorder when browser recording is unavailable", async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ":8081",
      ca_cert_path: "artifacts/recorder/ca-cert.pem",
      active: null,
      browser: {
        availability: {
          available: false,
          reason: "browser recording is disabled in production mode",
        },
        active: null,
      },
    });

    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();

    expect(
      container.querySelector('[data-testid="browser-recorder-unavailable"]'),
    ).not.toBeNull();
    expect(container.textContent).toContain("disabled in production mode");
    expect(container.textContent).toContain("Advanced Proxy Recorder");
    expect(container.textContent).toContain("Proxy Setup");
    expect(container.textContent).not.toContain("Download CA Certificate");
  });

  it("pairs a local recording bridge when the remote controller cannot launch a browser", async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ":8081",
      ca_cert_path: "artifacts/recorder/ca-cert.pem",
      active: null,
      browser: {
        availability: {
          available: false,
          reason: "browser recording is disabled in production mode",
        },
        active: null,
      },
    });

    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();

    expect(
      container.querySelector('[data-testid="browser-recorder-bridge-panel"]'),
    ).not.toBeNull();
    const downloadLink = Array.from(container.querySelectorAll("a")).find(
      (link) =>
        link.textContent?.includes("Download the APEX Load Recording Agent"),
    );
    expect(downloadLink).toBeDefined();
    expect(downloadLink?.getAttribute("href")).toContain(
      "/api/v1/downloads/recording-agent/latest",
    );
    expect(
      container.querySelector('select[aria-label="Recording Agent platform"]'),
    ).not.toBeNull();
    expect(container.textContent).toContain("SHA-256");
    expect(container.textContent).not.toContain(
      "Install it from your enterprise software catalog",
    );
    expect(container.textContent).toContain("1Download the Recording Agent");
    expect(container.textContent).toContain("2Run the local command");
    expect(container.textContent).toContain("3Verify the agent");
    expect(container.textContent).toContain("4Pair the local bridge");
    expect(container.textContent).toContain(
      "Paste this command into Terminal or PowerShell",
    );
    expect(container.textContent).toContain("Token entered at prompt");
    expect(container.textContent).toContain("paste this pairing token");
    expect(container.textContent).toContain("Verify Agent");
    const command =
      container.querySelector('[data-testid="browser-recorder-bridge-command"]')
        ?.textContent || "";
    const generatedToken =
      container.querySelector('[data-testid="browser-recorder-bridge-token"]')
        ?.textContent || "";
    expect(command).toContain(
      "apexload-record-agent --addr 127.0.0.1:27777 --token-stdin",
    );
    expect(command).not.toContain("--token ");
    expect(command).toContain("--allow-origin");
    expect(generatedToken).toMatch(/^[0-9a-f]{48}$/);

    const inputs = Array.from(
      container.querySelectorAll(".rec-bridge-panel input"),
    ) as HTMLInputElement[];
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        window.HTMLInputElement.prototype,
        "value",
      )!.set!;
      setter.call(inputs[0], "http://127.0.0.1:49152");
      inputs[0].dispatchEvent(new Event("input", { bubbles: true }));
    });
    const pairButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.includes("Pair Local Bridge"),
    );
    expect(pairButton).toBeDefined();
    await act(async () => {
      pairButton!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(apiMock.pairBrowserRecorderBridge).toHaveBeenCalledWith(
      "http://127.0.0.1:49152",
      generatedToken,
    );
    expect(container.textContent).toContain(
      "Local browser recording bridge paired",
    );
    expect(container.textContent).toContain("Start browser recording");
  });

  it("shows the selected Recording Agent platform and staged SHA-256", async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ":8081",
      ca_cert_path: "artifacts/recorder/ca-cert.pem",
      active: null,
      browser: {
        availability: {
          available: false,
          reason: "browser recording is disabled in production mode",
        },
        active: null,
      },
    });
    apiFetchMock.mockImplementation(async (input: string) => {
      if (
        input ===
        "/downloads/recording-agent/latest/checksum?platform=linux-amd64"
      ) {
        return {
          ok: true,
          headers: new Headers({ "content-type": "application/json" }),
          json: vi.fn().mockResolvedValue({
            algorithm: "sha-256",
            filename: "apexload-recording-agent-0.1.0-linux-amd64.tar.gz",
            sha256:
              "d8df2adeb0f3718d0beeebb225572273e1677b6208cef613c2378f3b3f41f3b2",
          }),
        };
      }
      return {
        ok: false,
        headers: new Headers({ "content-type": "application/json" }),
        json: vi.fn().mockResolvedValue({ error: "checksum not staged" }),
      };
    });

    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();

    const platformSelect = container.querySelector(
      'select[aria-label="Recording Agent platform"]',
    ) as HTMLSelectElement | null;
    expect(platformSelect).not.toBeNull();
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        window.HTMLSelectElement.prototype,
        "value",
      )!.set!;
      setter.call(platformSelect, "linux-amd64");
      platformSelect!.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await flushPromises();

    expect(apiFetchMock).toHaveBeenCalledWith(
      "/downloads/recording-agent/latest/checksum?platform=linux-amd64",
    );
    expect(container.textContent).toContain(
      "Download the APEX Load Recording Agent for Linux (x64)",
    );
    expect(container.textContent).toContain(
      "d8df2adeb0f3718d0beeebb225572273e1677b6208cef613c2378f3b3f41f3b2",
    );
    expect(container.textContent).toContain(
      "apexload-recording-agent-0.1.0-linux-amd64.tar.gz",
    );
  });

  it("streams a managed Recording Agent package through the native save bridge", async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ":8081",
      ca_cert_path: "artifacts/recorder/ca-cert.pem",
      active: null,
      browser: {
        availability: {
          available: false,
          reason: "browser recording is disabled in production mode",
        },
        active: null,
      },
    });
    apiFetchMock.mockImplementation(async (input: string) => {
      if (
        input ===
        "/downloads/recording-agent/latest/checksum?platform=linux-amd64"
      ) {
        return {
          ok: true,
          headers: new Headers({ "content-type": "application/json" }),
          json: vi.fn().mockResolvedValue({
            filename: "apexload-recording-agent-0.1.0-linux-amd64.tar.gz",
            sha256:
              "d8df2adeb0f3718d0beeebb225572273e1677b6208cef613c2378f3b3f41f3b2",
          }),
        };
      }
      return {
        ok: false,
        headers: new Headers(),
        json: vi.fn().mockResolvedValue({}),
      };
    });
    saveApiResourceViaBridgeMock
      .mockResolvedValueOnce("cancelled")
      .mockResolvedValue("saved");

    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();

    const platformSelect = container.querySelector(
      'select[aria-label="Recording Agent platform"]',
    ) as HTMLSelectElement | null;
    const downloadLink = Array.from(container.querySelectorAll("a")).find(
      (link) =>
        link.textContent?.includes("Download the APEX Load Recording Agent"),
    );
    expect(platformSelect).not.toBeNull();
    expect(downloadLink).toBeDefined();
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        window.HTMLSelectElement.prototype,
        "value",
      )!.set!;
      setter.call(platformSelect, "linux-amd64");
      platformSelect!.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await flushPromises();
    await act(async () => {
      downloadLink?.dispatchEvent(
        new MouseEvent("click", { bubbles: true, cancelable: true }),
      );
    });
    await flushPromises();

    expect(saveApiResourceViaBridgeMock).toHaveBeenCalledWith(
      "/api/v1/downloads/recording-agent/latest?platform=linux-amd64",
      "apexload-recording-agent-0.1.0-linux-amd64.tar.gz",
    );
    expect(apiFetchMock).not.toHaveBeenCalledWith(
      "/downloads/recording-agent/latest?platform=linux-amd64",
    );
    expect(downloadBlobMock).not.toHaveBeenCalled();
    expect(container.textContent).not.toContain(
      "APEX Load Recording Agent saved (Linux (x64))",
    );

    await act(async () => {
      downloadLink?.dispatchEvent(
        new MouseEvent("click", { bubbles: true, cancelable: true }),
      );
    });
    await flushPromises();
    expect(container.textContent).toContain(
      "APEX Load Recording Agent saved (Linux (x64))",
    );
  });

  it("streams a managed Recording Agent package in the desktop bootstrap browser", async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ":8081",
      ca_cert_path: "artifacts/recorder/ca-cert.pem",
      active: null,
      browser: {
        availability: {
          available: false,
          reason: "browser recording is disabled in production mode",
        },
        active: null,
      },
    });
    apiFetchMock.mockImplementation(async (input: string) => {
      if (
        input ===
        "/downloads/recording-agent/latest/checksum?platform=linux-amd64"
      ) {
        return {
          ok: true,
          headers: new Headers({ "content-type": "application/json" }),
          json: vi.fn().mockResolvedValue({
            filename: "apexload-recording-agent-0.1.0-linux-amd64.tar.gz",
            sha256:
              "d8df2adeb0f3718d0beeebb225572273e1677b6208cef613c2378f3b3f41f3b2",
          }),
        };
      }
      return {
        ok: false,
        headers: new Headers(),
        json: vi.fn().mockResolvedValue({}),
      };
    });
    hasDesktopApiCapabilityMock.mockReturnValue(true);

    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();

    const platformSelect = container.querySelector(
      'select[aria-label="Recording Agent platform"]',
    ) as HTMLSelectElement | null;
    const downloadLink = Array.from(container.querySelectorAll("a")).find(
      (link) =>
        link.textContent?.includes("Download the APEX Load Recording Agent"),
    );
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        window.HTMLSelectElement.prototype,
        "value",
      )!.set!;
      setter.call(platformSelect, "linux-amd64");
      platformSelect!.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await flushPromises();
    await act(async () => {
      downloadLink?.dispatchEvent(
        new MouseEvent("click", { bubbles: true, cancelable: true }),
      );
    });
    await flushPromises();

    expect(startDesktopBrowserDownloadMock).toHaveBeenCalledWith(
      "/api/v1/downloads/recording-agent/latest?platform=linux-amd64",
      "apexload-recording-agent-0.1.0-linux-amd64.tar.gz",
    );
    expect(apiFetchMock).not.toHaveBeenCalledWith(
      "/downloads/recording-agent/latest?platform=linux-amd64",
    );
    expect(downloadBlobMock).not.toHaveBeenCalled();
    expect(container.textContent).toContain(
      "APEX Load Recording Agent download request opened (Linux (x64))",
    );
  });

  it("verifies that the local recording agent is running before pairing", async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ":8081",
      ca_cert_path: "artifacts/recorder/ca-cert.pem",
      active: null,
      browser: {
        availability: {
          available: false,
          reason: "browser recording is disabled in production mode",
        },
        active: null,
      },
    });

    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();

    const verifyButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.includes("Verify Agent"),
    );
    expect(verifyButton).toBeDefined();
    await act(async () => {
      verifyButton!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(apiMock.verifyBrowserRecorderBridgeAgent).toHaveBeenCalledWith(
      "http://127.0.0.1:27777",
    );
    expect(container.textContent).toContain(
      "APEX Load Recording Agent verified",
    );
    expect(container.textContent).toContain(
      "APEX Load Recording Agent is running at http://127.0.0.1:27777",
    );
    expect(container.textContent).toContain(
      "Click Pair Local Bridge to connect",
    );
  });

  it("does not navigate to the dashboard shell when the agent download endpoint returns HTML", async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ":8081",
      ca_cert_path: "artifacts/recorder/ca-cert.pem",
      active: null,
      browser: {
        availability: {
          available: false,
          reason: "browser recording is disabled in production mode",
        },
        active: null,
      },
    });
    apiFetchMock.mockImplementation(async (input: string) => {
      if (input === "/downloads/recording-agent/latest?platform=linux-amd64") {
        return {
          ok: true,
          headers: new Headers({ "content-type": "text/html; charset=utf-8" }),
          blob: vi.fn().mockResolvedValue(new Blob(["<html>dashboard</html>"])),
        };
      }
      return {
        ok: false,
        headers: new Headers({ "content-type": "application/json" }),
        json: vi.fn().mockResolvedValue({ error: "checksum not staged" }),
      };
    });

    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();

    const downloadLink = Array.from(container.querySelectorAll("a")).find(
      (link) =>
        link.textContent?.includes("Download the APEX Load Recording Agent"),
    );
    expect(downloadLink).toBeDefined();
    const platformSelect = container.querySelector(
      'select[aria-label="Recording Agent platform"]',
    ) as HTMLSelectElement | null;
    expect(platformSelect).not.toBeNull();
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        window.HTMLSelectElement.prototype,
        "value",
      )!.set!;
      setter.call(platformSelect, "linux-amd64");
      platformSelect!.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await flushPromises();
    await act(async () => {
      downloadLink!.dispatchEvent(
        new MouseEvent("click", { bubbles: true, cancelable: true }),
      );
    });
    await flushPromises();

    expect(apiFetchMock).toHaveBeenCalledWith(
      "/downloads/recording-agent/latest?platform=linux-amd64",
    );
    expect(downloadBlobMock).not.toHaveBeenCalled();
    expect(container.textContent).toContain(
      "Recording Agent download endpoint returned the dashboard instead of an agent archive",
    );
  });

  it("shows an actionable message when the local recording agent is not reachable", async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ":8081",
      ca_cert_path: "artifacts/recorder/ca-cert.pem",
      active: null,
      browser: {
        availability: {
          available: false,
          reason: "browser recording is disabled in production mode",
        },
        active: null,
      },
    });
    apiMock.verifyBrowserRecorderBridgeAgent.mockRejectedValueOnce(
      new TypeError("Failed to fetch"),
    );

    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();

    const verifyButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.includes("Verify Agent"),
    );
    expect(verifyButton).toBeDefined();
    await act(async () => {
      verifyButton!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(container.textContent).toContain(
      "APEX Load Recording Agent is not reachable at http://127.0.0.1:27777",
    );
    expect(container.textContent).toContain(
      "Download and run the agent command",
    );
  });

  it("copies the bridge command without token argv and regenerates the pairing token", async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ":8081",
      ca_cert_path: "artifacts/recorder/ca-cert.pem",
      active: null,
      browser: {
        availability: {
          available: false,
          reason: "browser recording is disabled in production mode",
        },
        active: null,
      },
    });

    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();

    const commandBefore =
      container.querySelector('[data-testid="browser-recorder-bridge-command"]')
        ?.textContent || "";
    const tokenBefore =
      container.querySelector('[data-testid="browser-recorder-bridge-token"]')
        ?.textContent || "";
    expect(commandBefore).toContain("--token-stdin");
    expect(commandBefore).not.toContain(tokenBefore);
    expect(commandBefore).toContain("--allow-origin 'http://localhost:3000'");
    expect(commandBefore).toContain("--allow-origin 'http://127.0.0.1:3000'");
    const copyButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.includes("Copy Terminal Command"),
    );
    expect(copyButton).toBeDefined();

    await act(async () => {
      copyButton!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(clipboardWriteMock).toHaveBeenCalledWith(commandBefore);

    const copyTokenButton = Array.from(
      container.querySelectorAll("button"),
    ).find((button) => button.textContent?.includes("Copy Pairing Token"));
    expect(copyTokenButton).toBeDefined();
    await act(async () => {
      copyTokenButton!.dispatchEvent(
        new MouseEvent("click", { bubbles: true }),
      );
    });
    await flushPromises();

    expect(clipboardWriteMock).toHaveBeenCalledWith(tokenBefore);

    const regenerateButton = Array.from(
      container.querySelectorAll("button"),
    ).find((button) => button.textContent?.includes("Regenerate Token"));
    expect(regenerateButton).toBeDefined();
    await act(async () => {
      regenerateButton!.dispatchEvent(
        new MouseEvent("click", { bubbles: true }),
      );
    });
    await flushPromises();

    const commandAfter =
      container.querySelector('[data-testid="browser-recorder-bridge-command"]')
        ?.textContent || "";
    const tokenAfter =
      container.querySelector('[data-testid="browser-recorder-bridge-token"]')
        ?.textContent || "";
    expect(commandAfter).toBe(commandBefore);
    expect(tokenAfter).toMatch(/^[0-9a-f]{48}$/);
    expect(tokenAfter).not.toBe(tokenBefore);
  });

  it("falls back to selection copy when browser clipboard permission is denied", async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ":8081",
      ca_cert_path: "artifacts/recorder/ca-cert.pem",
      active: null,
      browser: {
        availability: {
          available: false,
          reason: "browser recording is disabled in production mode",
        },
        active: null,
      },
    });
    clipboardWriteMock.mockRejectedValueOnce(
      new Error("Write permission denied"),
    );
    const originalExecCommand = document.execCommand;
    const execCommandMock = vi.fn().mockReturnValue(true);
    Object.defineProperty(document, "execCommand", {
      configurable: true,
      value: execCommandMock,
    });

    try {
      await act(async () => {
        root.render(<RecordingStudio />);
      });
      await flushPromises();

      const command =
        container.querySelector(
          '[data-testid="browser-recorder-bridge-command"]',
        )?.textContent || "";
      const copyButton = Array.from(container.querySelectorAll("button")).find(
        (button) => button.textContent?.includes("Copy Terminal Command"),
      );
      expect(copyButton).toBeDefined();

      await act(async () => {
        copyButton!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      });
      await flushPromises();

      expect(clipboardWriteMock).toHaveBeenCalledWith(command);
      expect(execCommandMock).toHaveBeenCalledWith("copy");
      expect(container.textContent).toContain("Terminal command copied");
    } finally {
      Object.defineProperty(document, "execCommand", {
        configurable: true,
        value: originalExecCommand,
      });
    }
  });

  it("shows a manual-copy message when all clipboard paths are blocked", async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ":8081",
      ca_cert_path: "artifacts/recorder/ca-cert.pem",
      active: null,
      browser: {
        availability: {
          available: false,
          reason: "browser recording is disabled in production mode",
        },
        active: null,
      },
    });
    clipboardWriteMock.mockRejectedValueOnce(
      new Error("Write permission denied"),
    );
    const originalExecCommand = document.execCommand;
    const execCommandMock = vi.fn().mockReturnValue(false);
    Object.defineProperty(document, "execCommand", {
      configurable: true,
      value: execCommandMock,
    });

    try {
      await act(async () => {
        root.render(<RecordingStudio />);
      });
      await flushPromises();

      const copyButton = Array.from(container.querySelectorAll("button")).find(
        (button) => button.textContent?.includes("Copy Terminal Command"),
      );
      expect(copyButton).toBeDefined();

      await act(async () => {
        copyButton!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      });
      await flushPromises();

      expect(execCommandMock).toHaveBeenCalledWith("copy");
      expect(container.textContent).toContain(
        "Clipboard is blocked in this browser. Select the terminal command and press Cmd+C or Ctrl+C.",
      );
    } finally {
      Object.defineProperty(document, "execCommand", {
        configurable: true,
        value: originalExecCommand,
      });
    }
  });

  it("uses an already paired bridge as the browser recorder backend", async () => {
    apiMock.browserRecorderBridge = {
      origin: "http://127.0.0.1:49152",
      token: "tok",
      session_id: "ws-1",
    };
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ":8081",
      ca_cert_path: "artifacts/recorder/ca-cert.pem",
      active: null,
      browser: {
        availability: { available: false, reason: "production mode" },
        active: null,
      },
    });

    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();

    expect(apiMock.getBrowserRecorderBridgeStatus).toHaveBeenCalled();
    expect(
      container.querySelector(
        '[data-testid="browser-recorder-bridge-connected"]',
      ),
    ).not.toBeNull();
    expect(container.textContent).toContain(
      "Local bridge: http://127.0.0.1:49152",
    );
  });

  it("does not offer browser recording when the controller omits browser recorder capability", async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ":8081",
      ca_cert_path: "artifacts/recorder/ca-cert.pem",
      active: null,
    });

    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();

    expect(
      container.querySelector('[data-testid="browser-recorder-unavailable"]'),
    ).not.toBeNull();
    expect(container.textContent).toContain(
      "controller does not expose browser recording endpoints",
    );
    expect(
      Array.from(container.querySelectorAll("button")).some((button) =>
        button.textContent?.includes("Start browser recording"),
      ),
    ).toBe(false);
    expect(container.textContent).toContain("Proxy Setup");
    expect(container.textContent).not.toContain("Download CA Certificate");
  });

  it("adopts a live browser recording reported by the controller status", async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ":8081",
      ca_cert_path: "artifacts/recorder/ca-cert.pem",
      active: null,
      browser: {
        availability: { available: true },
        active: {
          id: "brec-1",
          name: "Browser Recording",
          status: "recording",
        },
      },
    });

    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();

    expect(
      container.querySelector('[data-testid="browser-recorder-panel"]'),
    ).not.toBeNull();
    expect(apiMock.getBrowserRecording).toHaveBeenCalledWith("brec-1");
  });

  it("labels device presets as workstation emulation and threads the selected preset into start", async () => {
    await act(async () => {
      root.render(
        <RecordingStudio
          activeProject={{ id: "proj-1", name: "Web" } as any}
        />,
      );
    });
    await flushPromises();

    const deviceSelect = container.querySelector(
      'select[aria-label="Emulated device preset"]',
    ) as HTMLSelectElement;
    expect(deviceSelect).not.toBeNull();
    expect(deviceSelect.textContent).toContain("iphone 15 pro");
    expect(container.textContent).toContain("recording workstation");
    expect(container.textContent).toContain("They do not qualify Chrome on a physical iPad.");

    const urlInput = container.querySelector(
      'input[aria-label="Start URL address"]',
    ) as HTMLInputElement;
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        window.HTMLInputElement.prototype,
        "value",
      )!.set!;
      setter.call(urlInput, "app.example.com/login");
      urlInput.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        window.HTMLSelectElement.prototype,
        "value",
      )!.set!;
      setter.call(deviceSelect, "iphone_15_pro");
      deviceSelect.dispatchEvent(new Event("change", { bubbles: true }));
    });
    const recordButton = Array.from(container.querySelectorAll("button")).find(
      (b) => b.textContent?.includes("Start browser recording"),
    );
    await act(async () => {
      recordButton!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();

    expect(apiMock.startBrowserRecording).toHaveBeenCalledWith(
      expect.objectContaining({ device_preset: "iphone_15_pro" }),
    );
  });

  it("offers an install affordance when browser recording is unavailable", async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ":8081",
      ca_cert_path: "artifacts/recorder/ca-cert.pem",
      active: null,
      browser: {
        availability: {
          available: false,
          code: "browsers_not_installed",
          reason: "Playwright Chromium is not installed on the controller host",
        },
        active: null,
        install: { status: "idle" },
      },
    });
    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();

    const installBtn = Array.from(container.querySelectorAll("button")).find(
      (b) => b.textContent?.includes("Install Recording Browser"),
    );
    expect(installBtn).toBeDefined();
    await act(async () => {
      installBtn!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushPromises();
    expect(apiMock.installBrowserRecorder).toHaveBeenCalled();
  });

  it("keeps install failures visible and retryable when browser dependencies are missing", async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ":8081",
      ca_cert_path: "artifacts/recorder/ca-cert.pem",
      active: null,
      browser: {
        availability: {
          available: false,
          reason: "no recording browser installed",
        },
        active: null,
        install: { status: "failed", error: "download failed" },
      },
    });
    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();

    const unavailable = container.querySelector(
      '[data-testid="browser-recorder-unavailable"]',
    );
    expect(unavailable?.textContent).toContain(
      "Recording browser install failed",
    );
    expect(unavailable?.textContent).toContain("download failed");
    expect(
      Array.from(unavailable?.querySelectorAll("button") || []).some((b) =>
        b.textContent?.includes("Install Recording Browser"),
      ),
    ).toBe(true);
  });

  it("exposes proxy setup as a named disclosure button", async () => {
    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();

    const setupToggle = container.querySelector<HTMLButtonElement>(
      "button.rec-setup-toggle",
    );
    expect(setupToggle).not.toBeNull();
    expect(setupToggle?.getAttribute("aria-expanded")).toBe("false");
    expect(setupToggle?.getAttribute("aria-controls")).toBe(
      "recording-proxy-setup-steps",
    );

    await act(async () => {
      setupToggle?.click();
    });

    expect(setupToggle?.getAttribute("aria-expanded")).toBe("true");
    expect(
      container.querySelector("#recording-proxy-setup-steps"),
    ).not.toBeNull();
  });

  it("loads only a bounded request tail for the live recorder feed", async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ":8081",
      ca_cert_path: "artifacts/recorder/ca-cert.pem",
      active: {
        id: "rec-live",
        name: "Live Capture",
        status: "recording",
        request_count: 250,
        started_at: "2026-05-12T20:00:00Z",
      },
      browser: {
        availability: { available: true },
        active: null,
        install: { status: "idle" },
      },
    });
    apiMock.getRecording.mockResolvedValue({
      id: "rec-live",
      name: "Live Capture",
      status: "recording",
      request_count: 250,
      last_seq: 250,
      started_at: "2026-05-12T20:00:00Z",
      requests: [],
      filters: { exclude_static: true },
    });

    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();

    expect(apiMock.getRecording).toHaveBeenCalledWith("rec-live", {
      tail: 20,
    });
    expect(
      container.querySelector(".rec-active-stats .rec-stat")?.textContent,
    ).toBe("250Requests");
  });

  it("fetches the complete request list before opening a summary session", async () => {
    apiMock.listRecordings.mockResolvedValue({
      sessions: [
        {
          id: "rec-summary",
          name: "Large Capture",
          status: "stopped",
          request_count: 125,
          started_at: "2026-05-12T20:00:00Z",
          filters: { exclude_static: true },
        },
      ],
    });
    apiMock.getRecording.mockResolvedValue({
      id: "rec-summary",
      name: "Large Capture",
      status: "stopped",
      request_count: 125,
      started_at: "2026-05-12T20:00:00Z",
      requests: Array.from({ length: 125 }, (_, index) => ({
        seq: index + 1,
        timestamp: "2026-05-12T20:00:01Z",
        method: "GET",
        url: `https://shop.example.com/items/${index + 1}`,
        path: `/items/${index + 1}`,
        host: "shop.example.com",
        status_code: 200,
        response_time_ms: 10,
        content_type: "application/json",
        body: "",
        think_time_ms: 0,
        is_static: false,
      })),
      filters: { exclude_static: true },
    });

    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();
    const select = container.querySelector<HTMLButtonElement>(
      'button[aria-label="Select recording session Large Capture"]',
    );
    expect(select).not.toBeNull();

    await act(async () => {
      select?.click();
    });
    await flushPromises();

    expect(apiMock.getRecording).toHaveBeenCalledWith("rec-summary", {
      includeRequests: true,
    });
    expect(container.textContent).toContain("Request Detail: Large Capture");
    expect(container.textContent).toContain("125 requests");
  });

  it("starts the new project refresh immediately and ignores the previous project response", async () => {
    let resolveOld!: (value: { sessions: any[] }) => void;
    const oldRequest = new Promise<{ sessions: any[] }>((resolve) => {
      resolveOld = resolve;
    });
    apiMock.listRecordings.mockImplementation((projectId?: string) => {
      if (projectId === "proj-1") return oldRequest;
      return Promise.resolve({
        sessions: [
          {
            id: "rec-new",
            name: "New Project Capture",
            status: "stopped",
            started_at: "2026-05-12T20:00:00Z",
            requests: [],
            filters: { exclude_static: true },
          },
        ],
      });
    });

    await act(async () => {
      root.render(
        <RecordingStudio
          activeProject={{ id: "proj-1", name: "Old" } as any}
        />,
      );
      await Promise.resolve();
    });
    await act(async () => {
      root.render(
        <RecordingStudio
          activeProject={{ id: "proj-2", name: "New" } as any}
        />,
      );
    });
    await flushPromises();

    expect(apiMock.listRecordings).toHaveBeenCalledWith("proj-2");
    expect(container.textContent).toContain("New Project Capture");

    await act(async () => {
      resolveOld({
        sessions: [
          {
            id: "rec-old",
            name: "Old Project Capture",
            status: "stopped",
            started_at: "2026-05-12T20:00:00Z",
            requests: [],
            filters: { exclude_static: true },
          },
        ],
      });
      await Promise.resolve();
    });
    expect(container.textContent).toContain("New Project Capture");
    expect(container.textContent).not.toContain("Old Project Capture");
  });

  it("starts a fresh refresh and ignores the old response when the recorder API origin changes", async () => {
    let resolveOld!: (value: { sessions: any[] }) => void;
    const oldRequest = new Promise<{ sessions: any[] }>((resolve) => {
      resolveOld = resolve;
    });
    apiMock.listRecordings
      .mockImplementationOnce(() => oldRequest)
      .mockResolvedValueOnce({
        sessions: [
          {
            id: "rec-bridge",
            name: "Bridge Origin Capture",
            status: "stopped",
            started_at: "2026-05-12T20:00:00Z",
            requests: [],
            filters: { exclude_static: true },
          },
        ],
      });

    await act(async () => {
      root.render(<RecordingStudio activeProject={{ id: "proj-1", name: "Project" } as any} />);
      await Promise.resolve();
    });
    apiMock.browserRecorderRequestScope =
      "bridge:http://127.0.0.1:8765:bridge-session-2";
    await act(async () => {
      root.render(<RecordingStudio activeProject={{ id: "proj-1", name: "Project" } as any} />);
    });
    await flushPromises();

    expect(apiMock.listRecordings).toHaveBeenCalledTimes(2);
    expect(container.textContent).toContain("Bridge Origin Capture");

    await act(async () => {
      resolveOld({
        sessions: [
          {
            id: "rec-controller",
            name: "Controller Origin Capture",
            status: "stopped",
            started_at: "2026-05-12T20:00:00Z",
            requests: [],
            filters: { exclude_static: true },
          },
        ],
      });
      await Promise.resolve();
    });
    expect(container.textContent).toContain("Bridge Origin Capture");
    expect(container.textContent).not.toContain("Controller Origin Capture");
  });

  it("ignores a bridge pairing result after the recorder API origin changes", async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ":8081",
      ca_cert_path: "artifacts/recorder/ca-cert.pem",
      active: null,
      browser: {
        availability: {
          available: false,
          reason: "browser recording is disabled in production mode",
        },
        active: null,
      },
    });
    let resolvePair!: (value: any) => void;
    apiMock.pairBrowserRecorderBridge.mockReturnValueOnce(
      new Promise((resolve) => {
        resolvePair = resolve;
      }),
    );

    await act(async () => {
      root.render(<RecordingStudio activeProject={{ id: "proj-1", name: "Project" } as any} />);
    });
    await flushPromises();
    const pairButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.includes("Pair Local Bridge"),
    )!;
    act(() => {
      pairButton.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });

    apiMock.browserRecorderRequestScope =
      "bridge:http://127.0.0.1:8765:bridge-session-new";
    await act(async () => {
      root.render(<RecordingStudio activeProject={{ id: "proj-1", name: "Project" } as any} />);
    });
    await flushPromises();
    await act(async () => {
      resolvePair({
        origin: "http://127.0.0.1:27777",
        token: "old-token",
        session_id: "old-session",
        status: {
          availability: { available: true },
          api_origin: "http://127.0.0.1:27777",
          paired: true,
        },
      });
      await Promise.resolve();
    });

    expect(container.textContent).not.toContain("Local browser recording bridge paired");
    expect(container.textContent).not.toContain("Connected to http://127.0.0.1:27777");
  });

  it("accepts the recorder scope transition created by its own successful pairing", async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ":8081",
      ca_cert_path: "artifacts/recorder/ca-cert.pem",
      active: null,
      browser: {
        availability: {
          available: false,
          reason: "browser recording is disabled in production mode",
        },
        active: null,
      },
    });
    let resolvePair!: (value: any) => void;
    apiMock.pairBrowserRecorderBridge.mockReturnValueOnce(
      new Promise((resolve) => {
        resolvePair = resolve;
      }),
    );

    await act(async () => {
      root.render(<RecordingStudio activeProject={{ id: "proj-1", name: "Project" } as any} />);
    });
    await flushPromises();
    const pairButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.includes("Pair Local Bridge"),
    )!;
    act(() => {
      pairButton.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });

    apiMock.browserRecorderRequestScope =
      "bridge:http://127.0.0.1:27777:paired-session";
    await act(async () => {
      root.render(<RecordingStudio activeProject={{ id: "proj-1", name: "Project" } as any} />);
    });
    await act(async () => {
      resolvePair({
        origin: "http://127.0.0.1:27777",
        token: "paired-token",
        session_id: "paired-session",
        status: {
          availability: { available: true },
          api_origin: "http://127.0.0.1:27777",
          paired: true,
        },
      });
      await Promise.resolve();
    });

    expect(container.textContent).toContain("Local browser recording bridge paired");
    expect(container.textContent).toContain("Start browser recording");
  });

  it("ignores an agent verification result after the recorder API origin changes", async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ":8081",
      ca_cert_path: "artifacts/recorder/ca-cert.pem",
      active: null,
      browser: {
        availability: {
          available: false,
          reason: "browser recording is disabled in production mode",
        },
        active: null,
      },
    });
    let resolveVerify!: (value: { status: string; version: string }) => void;
    apiMock.verifyBrowserRecorderBridgeAgent.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveVerify = resolve;
      }),
    );

    await act(async () => {
      root.render(<RecordingStudio activeProject={{ id: "proj-1", name: "Project" } as any} />);
    });
    await flushPromises();
    const verifyButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.includes("Verify Agent"),
    )!;
    act(() => {
      verifyButton.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });

    apiMock.browserRecorderRequestScope =
      "bridge:http://127.0.0.1:8765:bridge-session-new";
    await act(async () => {
      root.render(<RecordingStudio activeProject={{ id: "proj-1", name: "Project" } as any} />);
    });
    await flushPromises();
    await act(async () => {
      resolveVerify({ status: "healthy", version: "old" });
      await Promise.resolve();
    });

    expect(container.textContent).not.toContain("APEX Load Recording Agent verified");
    expect(container.textContent).not.toContain(
      "APEX Load Recording Agent is running at http://127.0.0.1:27777",
    );
  });

  it("ignores a recorder action that completes after the active project changes", async () => {
    let resolveStart!: (value: {
      session: any;
      proxy_addr: string;
      ca_cert_path: string;
    }) => void;
    const startRequest = new Promise<{
      session: any;
      proxy_addr: string;
      ca_cert_path: string;
    }>((resolve) => {
      resolveStart = resolve;
    });
    apiMock.startRecording.mockImplementationOnce(() => startRequest);

    await act(async () => {
      root.render(
        <RecordingStudio
          activeProject={{ id: "proj-1", name: "Old" } as any}
        />,
      );
    });
    await flushPromises();
    await openProxyAdvanced(container);
    const startButton = Array.from(container.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Start Recording",
    );
    expect(startButton).toBeDefined();
    await act(async () => {
      startButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });

    await act(async () => {
      root.render(
        <RecordingStudio
          activeProject={{ id: "proj-2", name: "New" } as any}
        />,
      );
    });
    await flushPromises();
    await act(async () => {
      resolveStart({
        session: {
          id: "rec-old-project",
          name: "Old Project Recording",
          status: "recording",
          started_at: "2026-05-12T20:00:00Z",
          requests: [],
          filters: { exclude_static: true },
        },
        proxy_addr: ":8081",
        ca_cert_path: "artifacts/recorder/ca-cert.pem",
      });
      await Promise.resolve();
    });

    expect(container.textContent).not.toContain("Old Project Recording");
    expect(container.textContent).toContain("New Recording");
  });

  it("does not offer install as a fix for no-display Docker controllers", async () => {
    apiMock.getRecorderStatus.mockResolvedValue({
      proxy_addr: ":8081",
      ca_cert_path: "artifacts/recorder/ca-cert.pem",
      active: null,
      browser: {
        availability: {
          available: false,
          reason:
            "no display detected on the controller host for a headed browser",
        },
        active: null,
        install: {
          status: "failed",
          error:
            "could not install driver: could not run driver: fork/exec /usr/bin/node: no such file or directory",
        },
      },
    });
    await act(async () => {
      root.render(<RecordingStudio />);
    });
    await flushPromises();

    const unavailable = container.querySelector(
      '[data-testid="browser-recorder-unavailable"]',
    );
    expect(unavailable?.textContent).toContain(
      "no display detected on the controller host for a headed browser",
    );
    expect(unavailable?.textContent).toContain(
      "Install cannot add a display to this controller",
    );
    expect(unavailable?.textContent).toContain(
      "Recording browser install failed",
    );
    expect(unavailable?.textContent).toContain("/usr/bin/node");
    expect(
      Array.from(unavailable?.querySelectorAll("button") || []).some((b) =>
        b.textContent?.includes("Install Recording Browser"),
      ),
    ).toBe(false);
  });
});

function setInput(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(
    window.HTMLInputElement.prototype,
    "value",
  )?.set;
  setter?.call(input, value);
  input.dispatchEvent(new Event("input", { bubbles: true }));
}

async function flushPromises() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

// The proxy recorder is a first-class option next to browser recording; keep
// this helper so older proxy tests still assert they are operating in that area.
async function openProxyAdvanced(container: HTMLElement) {
  expect(
    container.querySelector('[data-testid="rec-proxy-recorder-option"]'),
  ).not.toBeNull();
  if (!container.querySelector(".rec-setup-steps")) {
    const setupToggle = container.querySelector(".rec-setup-toggle");
    expect(setupToggle).not.toBeNull();
    await act(async () => {
      setupToggle!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
  }
  await flushPromises();
  expect(container.querySelector(".rec-setup-steps")).not.toBeNull();
}
