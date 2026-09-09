import { useCallback, useEffect, useState } from "react";

import { useGitHubApi } from "../hooks/api/useGitHubApi";
import {
  openDesktopBrowserWithPreview,
  requestApiNavigation,
} from "../hooks/apiClient";
import { getDesktopBridge, isDesktopSurface } from "../desktop/desktopBridge";
import {
  DashboardGitHubSettings,
  GitHubConnectionResponse,
  GitHubDiagnosticsResponse,
} from "../types";
import { getErrorMessage } from "../utils/errorUtils";

type GitHubIntegrationPanelProps = {
  value: DashboardGitHubSettings;
  onChange: (next: DashboardGitHubSettings) => void;
  canEdit?: boolean;
};

function updateField<K extends keyof DashboardGitHubSettings>(
  value: DashboardGitHubSettings,
  key: K,
  next: DashboardGitHubSettings[K],
): DashboardGitHubSettings {
  return { ...value, [key]: next };
}

export default function GitHubIntegrationPanel({
  value,
  onChange,
  canEdit = true,
}: GitHubIntegrationPanelProps) {
  const githubApi = useGitHubApi();
  const [connection, setConnection] = useState<GitHubConnectionResponse | null>(
    null,
  );
  const [diagnostics, setDiagnostics] =
    useState<GitHubDiagnosticsResponse | null>(null);
  const [connectionError, setConnectionError] = useState("");
  const [connectionNotice, setConnectionNotice] = useState("");
  const [diagnosticsError, setDiagnosticsError] = useState("");
  const [loading, setLoading] = useState(false);

  const refreshConnection = useCallback(async () => {
    try {
      const response = await githubApi.getConnection();
      setConnection(response);
      setConnectionError("");
    } catch (error) {
      // A disabled/unconfigured integration returns 404/503 — treat as not connected.
      setConnection({ connected: false });
      setConnectionError(getErrorMessage(error, ""));
    }
  }, [githubApi]);

  useEffect(() => {
    void refreshConnection();
  }, [refreshConnection]);

  const refreshDiagnostics = useCallback(
    async (refreshInstallations = false) => {
      try {
        if (refreshInstallations) {
          await githubApi.listInstallations(true);
        }
        const response = await githubApi.getDiagnostics();
        setDiagnostics(response);
        setDiagnosticsError("");
      } catch (error) {
        setDiagnosticsError(
          getErrorMessage(error, "Unable to load GitHub diagnostics"),
        );
      }
    },
    [githubApi],
  );

  useEffect(() => {
    void refreshDiagnostics();
  }, [refreshDiagnostics]);

  async function connect() {
    setLoading(true);
    setConnectionError("");
    setConnectionNotice("");
    try {
      if (isDesktopSurface()) {
        const bridge = getDesktopBridge();
        if (!bridge?.OpenInBrowser) {
          throw new Error("Authenticated browser launch is unavailable");
        }
        if (typeof window !== "undefined" && window.location.hash) {
          await bridge.SetLastRoute?.(window.location.hash);
        }
        await openDesktopBrowserWithPreview(bridge);
        setConnectionNotice(
          "The authenticated dashboard is open in your system browser. Select Connect GitHub there, then return here and refresh the connection.",
        );
        setLoading(false);
        return;
      }
      const returnTo =
        typeof window !== "undefined"
          ? `${window.location.pathname}${window.location.search}${window.location.hash}` ||
            "/"
          : "/";
      const redirectURL = await requestApiNavigation(
        githubApi.loginUrl(returnTo),
      );
      window.location.assign(redirectURL);
    } catch (error) {
      setConnectionError(
        getErrorMessage(error, "Unable to start GitHub authentication"),
      );
      setLoading(false);
    }
  }

  async function disconnect() {
    setLoading(true);
    try {
      await githubApi.disconnect();
      await refreshConnection();
    } catch (error) {
      setConnectionError(getErrorMessage(error, "Unable to disconnect GitHub"));
    } finally {
      setLoading(false);
    }
  }

  const scopesText = value.scopes.join(", ");

  return (
    <div className="settings-subsection github-integration-panel">
      <div className="settings-manager-summary">
        <div>
          <span className="setting-label">GitHub Script Sync</span>
          <span className="setting-desc">
            Connect APEX Load to GitHub Enterprise Server (or github.com) to
            push scripts to a repository and import them back. The OAuth app is
            registered on your GHES instance; the client secret is read from an
            environment variable and never stored.
          </span>
        </div>
        <span
          className={`script-health-token ${value.enabled ? "healthy" : ""}`}
        >
          {value.enabled ? "enabled" : "disabled"}
        </span>
      </div>

      {/* --- Per-user connection (any authenticated user) --- */}
      <div className="settings-inline-actions" style={{ alignItems: "center" }}>
        {connection?.connected ? (
          <>
            <span className="script-health-token healthy">
              Connected as {connection.login || "unknown"}
            </span>
            <button
              className="btn btn-ghost"
              type="button"
              onClick={() => void disconnect()}
              disabled={loading}
              style={{ color: "var(--danger)" }}
            >
              Disconnect
            </button>
          </>
        ) : (
          <>
            <span className="script-health-token">Not connected</span>
            <button
              className="btn btn-secondary"
              type="button"
              onClick={() => {
                void connect();
              }}
              disabled={!value.enabled || loading}
            >
              Connect GitHub
            </button>
          </>
        )}
        <button
          className="btn btn-ghost"
          type="button"
          onClick={() => void refreshConnection()}
          disabled={loading}
        >
          Refresh
        </button>
      </div>
      {connection?.scopes && (
        <p className="setting-desc">Granted scopes: {connection.scopes}</p>
      )}
      {!value.enabled && (
        <p className="setting-desc">
          Enable and save the integration below before connecting.
        </p>
      )}
      {connectionError && (
        <p className="settings-error-inline">{connectionError}</p>
      )}
      {connectionNotice && (
        <p className="setting-desc" role="status">
          {connectionNotice}
        </p>
      )}

      <div className="settings-subsection">
        <div className="settings-manager-summary">
          <div>
            <span className="setting-label">Enterprise Health</span>
            <span className="setting-desc">
              GitHub App, webhook, PAT fallback, check-run, and GHES
              reachability status.
            </span>
          </div>
          <span
            className={`script-health-token ${diagnostics?.reachable || diagnostics?.app_configured ? "healthy" : ""}`}
          >
            {diagnostics?.ghes_version ||
              diagnostics?.automation_mode ||
              "not checked"}
          </span>
        </div>
        <div className="settings-inline-actions">
          <span
            className={`script-health-token ${diagnostics?.oauth_configured ? "healthy" : ""}`}
          >
            OAuth {diagnostics?.oauth_configured ? "ready" : "not ready"}
          </span>
          <span
            className={`script-health-token ${diagnostics?.app_configured ? "healthy" : ""}`}
          >
            App {diagnostics?.app_configured ? "ready" : "not ready"}
          </span>
          <span
            className={`script-health-token ${diagnostics?.webhook_configured ? "healthy" : ""}`}
          >
            Webhook {diagnostics?.webhook_configured ? "ready" : "not ready"}
          </span>
          <span
            className={`script-health-token ${diagnostics?.pat_configured ? "healthy" : ""}`}
          >
            PAT {diagnostics?.pat_configured ? "ready" : "fallback off"}
          </span>
          <button
            className="btn btn-ghost"
            type="button"
            onClick={() => void refreshDiagnostics()}
            disabled={loading}
          >
            Refresh Health
          </button>
          <button
            className="btn btn-secondary"
            type="button"
            onClick={() => void refreshDiagnostics(true)}
            disabled={loading || !value.app_id}
          >
            Refresh Installations
          </button>
        </div>
        {diagnostics?.known_installations !== undefined && (
          <p className="setting-desc">
            Known GitHub App installations: {diagnostics.known_installations}
          </p>
        )}
        {diagnosticsError && (
          <p className="settings-error-inline">{diagnosticsError}</p>
        )}
      </div>

      {/* --- Global OAuth app config (SystemAdmin) --- */}
      <div className="project-redaction-rule-grid" style={{ marginTop: 12 }}>
        <label
          className="project-redaction-rule-field"
          style={{ flexDirection: "row", alignItems: "center", gap: 8 }}
        >
          <input
            type="checkbox"
            checked={value.enabled}
            disabled={!canEdit}
            onChange={(event) =>
              onChange(updateField(value, "enabled", event.target.checked))
            }
          />
          <span>Enable GitHub integration</span>
        </label>
        <label className="project-redaction-rule-field">
          <span>OAuth Client ID</span>
          <input
            className="settings-input"
            value={value.client_id}
            disabled={!canEdit}
            onChange={(event) =>
              onChange(updateField(value, "client_id", event.target.value))
            }
            placeholder="Iv1.0123456789abcdef"
          />
        </label>
        <label className="project-redaction-rule-field">
          <span>Client Secret Env Var</span>
          <input
            className="settings-input"
            value={value.client_secret_env_var}
            disabled={!canEdit}
            onChange={(event) =>
              onChange(
                updateField(value, "client_secret_env_var", event.target.value),
              )
            }
            placeholder="APEXLOAD_GITHUB_CLIENT_SECRET"
          />
        </label>
        <details className="project-redaction-rule-field"><summary>GitHub App automation credentials</summary><p>Required for installation-based automation. Interactive OAuth uses the client settings above. Values are provided in the controller environment or mounted key file.</p>
        <label className="project-redaction-rule-field">
          <span>GitHub App ID</span>
          <input
            className="settings-input"
            type="number"
            value={value.app_id || 0}
            disabled={!canEdit}
            onChange={(event) =>
              onChange(
                updateField(value, "app_id", Number(event.target.value || 0)),
              )
            }
            placeholder="123456"
          />
        </label>
        <label className="project-redaction-rule-field">
          <span>GitHub App Slug</span>
          <input
            className="settings-input"
            value={value.app_slug}
            disabled={!canEdit}
            onChange={(event) =>
              onChange(updateField(value, "app_slug", event.target.value))
            }
            placeholder="apex-load"
          />
        </label>
        <label className="project-redaction-rule-field">
          <span>App Private Key Env Var</span>
          <input
            className="settings-input"
            value={value.app_private_key_env_var}
            disabled={!canEdit}
            onChange={(event) =>
              onChange(
                updateField(
                  value,
                  "app_private_key_env_var",
                  event.target.value,
                ),
              )
            }
            placeholder="APEXLOAD_GITHUB_APP_PRIVATE_KEY"
          />
        </label>
        <label className="project-redaction-rule-field">
          <span>App Private Key File</span>
          <input
            className="settings-input"
            value={value.app_private_key_file}
            disabled={!canEdit}
            onChange={(event) =>
              onChange(
                updateField(value, "app_private_key_file", event.target.value),
              )
            }
            placeholder="/etc/apexload/github-app.pem"
          />
        </label>
        </details>
        <label className="project-redaction-rule-field">
          <span>Webhook Secret Env Var</span>
          <input
            className="settings-input"
            value={value.webhook_secret_env_var}
            disabled={!canEdit}
            onChange={(event) =>
              onChange(
                updateField(
                  value,
                  "webhook_secret_env_var",
                  event.target.value,
                ),
              )
            }
            placeholder="APEXLOAD_GITHUB_WEBHOOK_SECRET"
          />
        </label>
        <label className="project-redaction-rule-field">
          <span>PAT Fallback Env Var</span>
          <input
            className="settings-input"
            value={value.pat_env_var}
            disabled={!canEdit}
            onChange={(event) =>
              onChange(updateField(value, "pat_env_var", event.target.value))
            }
            placeholder="APEXLOAD_GITHUB_PAT"
          />
        </label>
        <label className="project-redaction-rule-field">
          <span>Automation Mode</span>
          <select
            className="settings-input"
            value={value.automation_mode}
            disabled={!canEdit}
            onChange={(event) =>
              onChange(
                updateField(value, "automation_mode", event.target.value),
              )
            }
          >
            <option value="app_oauth_pat">App, OAuth, PAT</option>
            <option value="app">GitHub App only</option>
            <option value="oauth">OAuth only</option>
            <option value="pat">PAT only</option>
          </select>
        </label>
        <label
          className="project-redaction-rule-field"
          style={{ flexDirection: "row", alignItems: "center", gap: 8 }}
        >
          <input
            type="checkbox"
            checked={value.checks_enabled}
            disabled={!canEdit}
            onChange={(event) =>
              onChange(
                updateField(value, "checks_enabled", event.target.checked),
              )
            }
          />
          <span>Publish GitHub Checks</span>
        </label>
        <label className="project-redaction-rule-field">
          <span>API Base URL</span>
          <input
            className="settings-input"
            value={value.api_base_url}
            disabled={!canEdit}
            onChange={(event) =>
              onChange(updateField(value, "api_base_url", event.target.value))
            }
            placeholder="https://ghe.example.com/api/v3"
          />
        </label>
        <label className="project-redaction-rule-field">
          <span>Web Base URL</span>
          <input
            className="settings-input"
            value={value.web_base_url}
            disabled={!canEdit}
            onChange={(event) =>
              onChange(updateField(value, "web_base_url", event.target.value))
            }
            placeholder="https://ghe.example.com"
          />
        </label>
        <label className="project-redaction-rule-field">
          <span>Scopes (comma separated)</span>
          <input
            className="settings-input"
            value={scopesText}
            disabled={!canEdit}
            onChange={(event) =>
              onChange(
                updateField(
                  value,
                  "scopes",
                  event.target.value
                    .split(",")
                    .map((scope) => scope.trim())
                    .filter(Boolean),
                ),
              )
            }
            placeholder="repo"
          />
        </label>
        <label className="project-redaction-rule-field">
          <span>Default Repository (owner/name)</span>
          <input
            className="settings-input"
            value={value.default_repo}
            disabled={!canEdit}
            onChange={(event) =>
              onChange(updateField(value, "default_repo", event.target.value))
            }
            placeholder="my-org/perf-scripts"
          />
        </label>
        <label className="project-redaction-rule-field">
          <span>Default Branch</span>
          <input
            className="settings-input"
            value={value.default_branch}
            disabled={!canEdit}
            onChange={(event) =>
              onChange(updateField(value, "default_branch", event.target.value))
            }
            placeholder="main"
          />
        </label>
        <label className="project-redaction-rule-field">
          <span>Path Template</span>
          <input
            className="settings-input"
            value={value.path_template}
            disabled={!canEdit}
            onChange={(event) =>
              onChange(updateField(value, "path_template", event.target.value))
            }
            placeholder="apexload/scripts/{project}/{ref_key}.json"
          />
        </label>
        <label className="project-redaction-rule-field">
          <span>Internal CA Cert File (optional)</span>
          <input
            className="settings-input"
            value={value.ca_cert_file}
            disabled={!canEdit}
            onChange={(event) =>
              onChange(updateField(value, "ca_cert_file", event.target.value))
            }
            placeholder="/etc/apexload/ghe-ca.pem"
          />
        </label>
        <label
          className="project-redaction-rule-field"
          style={{ flexDirection: "row", alignItems: "center", gap: 8 }}
        >
          <input
            type="checkbox"
            checked={value.tls_insecure_skip_verify}
            disabled={!canEdit}
            onChange={(event) =>
              onChange(
                updateField(
                  value,
                  "tls_insecure_skip_verify",
                  event.target.checked,
                ),
              )
            }
          />
          <span>Skip TLS verification (lab only)</span>
        </label>
      </div>
      <p className="setting-desc">
        Changes here are saved with the main Save button. For GHES, point the
        base URLs at your instance (the host is automatically allowed through
        the egress guard) and supply an internal CA if needed.
      </p>
    </div>
  );
}
