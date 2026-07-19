export const DEFAULT_MACOS_NETWORK_SERVICE = "Wi-Fi";
const DEFAULT_RECORDER_PROXY_PORT = "8081";
const DEFAULT_RECORDER_PROXY_HOST = "localhost";

function shellQuote(value: string) {
  return `'${value.replace(/'/g, `'\\''`)}'`;
}

function powerShellQuote(value: string) {
  return `'${value.replace(/'/g, `''`)}'`;
}

function normalizeProxyPort(value: string) {
  const trimmed = value.trim();
  return /^\d+$/.test(trimmed) ? trimmed : DEFAULT_RECORDER_PROXY_PORT;
}

function normalizeProxyHost(value: string) {
  const trimmed = value.trim();
  return trimmed && !/\s/.test(trimmed) ? trimmed : DEFAULT_RECORDER_PROXY_HOST;
}

function hostFromAddress(value: string) {
  const trimmed = value.trim();
  if (!trimmed) return "";
  try {
    const url = new URL(trimmed);
    return url.hostname;
  } catch {
    // Fall through to host:port parsing for bare listener addresses.
  }
  if (trimmed.startsWith(":")) return "";
  if (trimmed.startsWith("[")) {
    const end = trimmed.indexOf("]");
    return end > 0 ? trimmed.slice(1, end) : "";
  }
  const colon = trimmed.indexOf(":");
  return colon >= 0 ? trimmed.slice(0, colon) : trimmed;
}

export function recorderProxyHost(
  proxyAddr: string,
  dashboardOrigin: string = "",
) {
  const explicitHost = hostFromAddress(proxyAddr);
  if (explicitHost && !["0.0.0.0", "::"].includes(explicitHost)) {
    return explicitHost === "127.0.0.1"
      ? DEFAULT_RECORDER_PROXY_HOST
      : explicitHost;
  }
  const dashboardHost = hostFromAddress(dashboardOrigin);
  if (
    !dashboardHost ||
    dashboardHost === "127.0.0.1" ||
    dashboardHost === "::1"
  ) {
    return DEFAULT_RECORDER_PROXY_HOST;
  }
  return dashboardHost;
}

export function recorderProxyHostIsLocal(proxyHost: string) {
  const normalized = proxyHost.trim().toLowerCase();
  return (
    normalized === "" ||
    normalized === "localhost" ||
    normalized === "127.0.0.1" ||
    normalized === "::1"
  );
}

export function recorderProxyPort(proxyAddr: string) {
  return normalizeProxyPort(
    (proxyAddr || `:${DEFAULT_RECORDER_PROXY_PORT}`).split(":").pop() ||
      DEFAULT_RECORDER_PROXY_PORT,
  );
}

function macOSProxyStatePreflightCommands() {
  return [
    `validate_proxy_state_file() {`,
    `  VALIDATION_FILE="$1"`,
    `  ALLOW_AUTHENTICATED="\${2:-0}"`,
    `  ENABLED_COUNT="$(awk '/^Enabled:/ { count++ } END { print count + 0 }' "$VALIDATION_FILE" 2>/dev/null)"`,
    `  SERVER_COUNT="$(awk '/^Server:/ { count++ } END { print count + 0 }' "$VALIDATION_FILE" 2>/dev/null)"`,
    `  PORT_COUNT="$(awk '/^Port:/ { count++ } END { print count + 0 }' "$VALIDATION_FILE" 2>/dev/null)"`,
    `  AUTH_COUNT="$(awk '/^Authenticated Proxy Enabled:/ { count++ } END { print count + 0 }' "$VALIDATION_FILE" 2>/dev/null)"`,
    `  if [ "$ENABLED_COUNT" != "1" ] || [ "$SERVER_COUNT" != "1" ] || [ "$PORT_COUNT" != "1" ] || [ "$AUTH_COUNT" != "1" ]; then`,
    `    printf '%s\n' "Proxy state in $VALIDATION_FILE is corrupt or incomplete; proxy settings were not changed." >&2`,
    `    return 1`,
    `  fi`,
    `  VALIDATED_ENABLED="$(awk -F': ' '/^Enabled:/ { print $2 }' "$VALIDATION_FILE" 2>/dev/null)"`,
    `  VALIDATED_SERVER="$(awk -F': ' '/^Server:/ { print $2 }' "$VALIDATION_FILE" 2>/dev/null)"`,
    `  VALIDATED_PORT="$(awk -F': ' '/^Port:/ { print $2 }' "$VALIDATION_FILE" 2>/dev/null)"`,
    `  VALIDATED_AUTH="$(awk -F': ' '/^Authenticated Proxy Enabled:/ { print $2 }' "$VALIDATION_FILE" 2>/dev/null | tr '[:upper:]' '[:lower:]')"`,
    `  case "$VALIDATED_ENABLED" in Yes|No) ;; *) printf '%s\n' "Proxy state in $VALIDATION_FILE has an invalid enabled value; proxy settings were not changed." >&2; return 1;; esac`,
    `  case "$VALIDATED_AUTH" in`,
    `    0|no|off|false) VALIDATED_AUTHENTICATED=0 ;;`,
    `    1|yes|on|true)`,
    `      if [ "$ALLOW_AUTHENTICATED" != "1" ]; then`,
    `        printf '%s\n' 'The macOS proxy state contains an authenticated proxy. APEX Load did not change proxy settings because macOS does not expose arbitrary proxy credentials needed for a safe automatic restore. Configure or restore that proxy manually in System Settings.' >&2`,
    `        return 1`,
    `      fi`,
    `      VALIDATED_AUTHENTICATED=1`,
    `      ;;`,
    `    *) printf '%s\n' "Proxy state in $VALIDATION_FILE has an invalid authenticated-proxy value; proxy settings were not changed." >&2; return 1;;`,
    `  esac`,
    `  VALIDATED_ENDPOINT_PRESENT=0`,
    `  if [ -n "$VALIDATED_SERVER" ] || [ "$VALIDATED_PORT" != "0" ]; then`,
    `    case "$VALIDATED_PORT" in ''|*[!0-9]*|??????*) printf '%s\n' "Proxy state in $VALIDATION_FILE has an invalid port; proxy settings were not changed." >&2; return 1;; esac`,
    `    if [ "$VALIDATED_PORT" -lt 1 ] || [ "$VALIDATED_PORT" -gt 65535 ] || [ -z "$VALIDATED_SERVER" ]; then`,
    `      printf '%s\n' "Proxy state in $VALIDATION_FILE has an invalid endpoint; proxy settings were not changed." >&2`,
    `      return 1`,
    `    fi`,
    `    VALIDATED_ENDPOINT_PRESENT=1`,
    `  fi`,
    `  if [ "$VALIDATED_ENABLED" = "Yes" ] && [ "$VALIDATED_ENDPOINT_PRESENT" -ne 1 ]; then`,
    `    printf '%s\n' "Proxy state in $VALIDATION_FILE enables a proxy without a valid endpoint; proxy settings were not changed." >&2`,
    `    return 1`,
    `  fi`,
    `}`,
    `preflight_proxy_state_pair() {`,
    `  validate_proxy_state_file "$1" "\${3:-0}"`,
    `  SAVED_HTTP_ENABLED="$VALIDATED_ENABLED"`,
    `  SAVED_HTTP_SERVER="$VALIDATED_SERVER"`,
    `  SAVED_HTTP_PORT="$VALIDATED_PORT"`,
    `  SAVED_HTTP_AUTHENTICATED="$VALIDATED_AUTHENTICATED"`,
    `  validate_proxy_state_file "$2" "\${3:-0}"`,
    `  SAVED_HTTPS_ENABLED="$VALIDATED_ENABLED"`,
    `  SAVED_HTTPS_SERVER="$VALIDATED_SERVER"`,
    `  SAVED_HTTPS_PORT="$VALIDATED_PORT"`,
    `  SAVED_HTTPS_AUTHENTICATED="$VALIDATED_AUTHENTICATED"`,
    `}`,
    `preflight_rollbackable_proxy_state_pair() {`,
    `  preflight_proxy_state_pair "$1" "$2" 1`,
    `  if [ "$SAVED_HTTP_AUTHENTICATED" = "1" ] || [ "$SAVED_HTTPS_AUTHENTICATED" = "1" ]; then`,
    `    if [ "$SAVED_HTTP_AUTHENTICATED" != "1" ] || [ "$SAVED_HTTPS_AUTHENTICATED" != "1" ] || [ "$SAVED_HTTP_ENABLED" != "Yes" ] || [ "$SAVED_HTTPS_ENABLED" != "Yes" ] || [ "$SAVED_HTTP_SERVER" != "$PROXY_HOST" ] || [ "$SAVED_HTTPS_SERVER" != "$PROXY_HOST" ] || [ "$SAVED_HTTP_PORT" != "$PROXY_PORT" ] || [ "$SAVED_HTTPS_PORT" != "$PROXY_PORT" ] || [ -z "\${PROXY_PASSWORD:-}" ]; then`,
    `      printf '%s\n' 'The authenticated current macOS proxy state does not exactly match the active APEX Load proxy endpoint and credentials. Proxy settings were not changed; restore it manually in System Settings.' >&2`,
    `      return 1`,
    `    fi`,
    `  fi`,
    `}`,
    `preflight_expected_apex_proxy_state_pair() {`,
    `  preflight_proxy_state_pair "$1" "$2" 1`,
    `  if [ "$SAVED_HTTP_ENABLED" != "Yes" ] || [ "$SAVED_HTTPS_ENABLED" != "Yes" ] || [ "$SAVED_HTTP_SERVER" != "$PROXY_HOST" ] || [ "$SAVED_HTTPS_SERVER" != "$PROXY_HOST" ] || [ "$SAVED_HTTP_PORT" != "$PROXY_PORT" ] || [ "$SAVED_HTTPS_PORT" != "$PROXY_PORT" ]; then`,
    `    printf '%s\n' 'The current macOS proxy state does not exactly match the active APEX Load proxy endpoint. Proxy settings were not changed; restore it manually in System Settings if necessary.' >&2`,
    `    return 1`,
    `  fi`,
    `  if [ -n "\${PROXY_PASSWORD:-}" ]; then`,
    `    if [ "$SAVED_HTTP_AUTHENTICATED" != "1" ] || [ "$SAVED_HTTPS_AUTHENTICATED" != "1" ]; then`,
    `      printf '%s\n' 'The current macOS proxy authentication state does not match the active authenticated APEX Load proxy. Proxy settings were not changed.' >&2`,
    `      return 1`,
    `    fi`,
    `  elif [ "$SAVED_HTTP_AUTHENTICATED" != "0" ] || [ "$SAVED_HTTPS_AUTHENTICATED" != "0" ]; then`,
    `    printf '%s\n' 'The current macOS proxy authentication state does not match the active unauthenticated APEX Load proxy. Proxy settings were not changed.' >&2`,
    `    return 1`,
    `  fi`,
    `}`,
  ];
}

function macOSProxyStateApplyCommands() {
  return [
    `apply_validated_proxy_state() {`,
    `  APPLY_ENABLED="$1"`,
    `  APPLY_SERVER="$2"`,
    `  APPLY_PORT="$3"`,
    `  APPLY_SET_CMD="$4"`,
    `  APPLY_STATE_CMD="$5"`,
    `  APPLY_AUTHENTICATED="$6"`,
    `  if [ -n "$APPLY_SERVER" ] || [ "$APPLY_PORT" != "0" ]; then`,
    `    if [ "$APPLY_AUTHENTICATED" = "1" ]; then`,
    `      networksetup "$APPLY_SET_CMD" "$SERVICE" "$APPLY_SERVER" "$APPLY_PORT" on "$APPLY_PROXY_USER" "$APPLY_PROXY_PASSWORD" || return 1`,
    `    else`,
    `      networksetup "$APPLY_SET_CMD" "$SERVICE" "$APPLY_SERVER" "$APPLY_PORT" off || return 1`,
    `    fi`,
    `  else`,
    `    networksetup "$APPLY_SET_CMD" "$SERVICE" "" 0 off || return 1`,
    `  fi`,
    `  if [ "$APPLY_ENABLED" = "Yes" ]; then`,
    `    networksetup "$APPLY_STATE_CMD" "$SERVICE" on || return 1`,
    `  else`,
    `    networksetup "$APPLY_STATE_CMD" "$SERVICE" off || return 1`,
    `  fi`,
    `}`,
    `apply_validated_proxy_state_pair() {`,
    `  apply_validated_proxy_state "$1" "$2" "$3" -setwebproxy -setwebproxystate "$4" || return 1`,
    `  apply_validated_proxy_state "$5" "$6" "$7" -setsecurewebproxy -setsecurewebproxystate "$8" || return 1`,
    `}`,
    `verify_proxy_state_pair() {`,
    `  networksetup -getwebproxy "$SERVICE" > "$3" || return 1`,
    `  networksetup -getsecurewebproxy "$SERVICE" > "$4" || return 1`,
    `  cmp -s "$1" "$3" || return 1`,
    `  cmp -s "$2" "$4" || return 1`,
    `}`,
  ];
}

export function buildMacOSStartProxyCommand(
  proxyPort: string,
  networkService: string = DEFAULT_MACOS_NETWORK_SERVICE,
  proxyHost: string = DEFAULT_RECORDER_PROXY_HOST,
  proxyPassword: string = "",
) {
  const service = networkService.trim() || DEFAULT_MACOS_NETWORK_SERVICE;
  const port = normalizeProxyPort(proxyPort);
  const host = normalizeProxyHost(proxyHost);
  const authentication = [
    `PROXY_USER='apexload'`,
    `PROXY_PASSWORD=${shellQuote(proxyPassword)}`,
  ];
  const proxyAuthArgs = proxyPassword.trim()
    ? ` on "$PROXY_USER" "$PROXY_PASSWORD"`
    : "";
  return [
    `(`,
    `set -e`,
    `SERVICE=${shellQuote(service)}`,
    `PROXY_HOST=${shellQuote(host)}`,
    `PROXY_PORT=${shellQuote(port)}`,
    ...authentication,
    `umask 077`,
    `STATE_DIR="\${HOME:?}/Library/Application Support/APEX Load/recorder-proxy-state"`,
    `mkdir -p "$STATE_DIR"`,
    `chmod 700 "$STATE_DIR"`,
    `STATE_KEY="$(printf '%s' "$SERVICE" | /usr/bin/shasum -a 256 | awk '{print $1}')"`,
    `case "$STATE_KEY" in ''|*[!0-9a-f]*) printf '%s\n' 'Could not derive a safe proxy-state key. Proxy settings were not changed.' >&2; exit 1;; esac`,
    `STATE_HTTP="$STATE_DIR/$STATE_KEY.http"`,
    `STATE_HTTPS="$STATE_DIR/$STATE_KEY.https"`,
    `LOCK_FILE="$STATE_DIR/$STATE_KEY.lock"`,
    `exec 9>"$LOCK_FILE"`,
    `if ! /usr/bin/lockf -s -t 0 9; then`,
    `  printf '%s\\n' 'Another APEX Load proxy command is running. Proxy settings were not changed.' >&2`,
    `  exit 1`,
    `fi`,
    `HTTP_CURRENT=""`,
    `HTTPS_CURRENT=""`,
    `ROLLBACK_HTTP=""`,
    `ROLLBACK_HTTPS=""`,
    `ROLLBACK_VERIFY_HTTP=""`,
    `ROLLBACK_VERIFY_HTTPS=""`,
    `TRANSACTION_ACTIVE=0`,
    `BACKUP_ALREADY_PRESENT=0`,
    `cleanup_proxy_start() {`,
    `  [ -z "$HTTP_CURRENT" ] || rm -f "$HTTP_CURRENT"`,
    `  [ -z "$HTTPS_CURRENT" ] || rm -f "$HTTPS_CURRENT"`,
    `  [ -z "$ROLLBACK_HTTP" ] || rm -f "$ROLLBACK_HTTP"`,
    `  [ -z "$ROLLBACK_HTTPS" ] || rm -f "$ROLLBACK_HTTPS"`,
    `  [ -z "$ROLLBACK_VERIFY_HTTP" ] || rm -f "$ROLLBACK_VERIFY_HTTP"`,
    `  [ -z "$ROLLBACK_VERIFY_HTTPS" ] || rm -f "$ROLLBACK_VERIFY_HTTPS"`,
    `}`,
    `trap cleanup_proxy_start EXIT`,
    `trap 'exit 1' HUP INT TERM`,
    ...macOSProxyStatePreflightCommands(),
    ...macOSProxyStateApplyCommands(),
    `if [ -e "$STATE_HTTP" ] || [ -e "$STATE_HTTPS" ]; then`,
    `  if [ ! -f "$STATE_HTTP" ] || [ ! -f "$STATE_HTTPS" ]; then`,
    `    printf '%s\\n' "Incomplete saved APEX Load proxy state exists in $STATE_DIR. Proxy settings were not changed; verify and restore the original settings before removing the saved state." >&2`,
    `    exit 1`,
    `  fi`,
    `  preflight_proxy_state_pair "$STATE_HTTP" "$STATE_HTTPS"`,
    `  BACKUP_ALREADY_PRESENT=1`,
    `  printf '%s\\n' "Preserving the existing original proxy backup in $STATE_DIR."`,
    `else`,
    `  HTTP_CURRENT="$(mktemp "$STATE_DIR/$STATE_KEY.http.current.XXXXXX")"`,
    `  HTTPS_CURRENT="$(mktemp "$STATE_DIR/$STATE_KEY.https.current.XXXXXX")"`,
    `  if ! networksetup -getwebproxy "$SERVICE" > "$HTTP_CURRENT" || ! networksetup -getsecurewebproxy "$SERVICE" > "$HTTPS_CURRENT"; then`,
    `    printf '%s\\n' 'Could not read the current macOS proxy settings. Proxy settings were not changed.' >&2`,
    `    exit 1`,
    `  fi`,
    `  preflight_proxy_state_pair "$HTTP_CURRENT" "$HTTPS_CURRENT"`,
    `  mv "$HTTP_CURRENT" "$STATE_HTTP"`,
    `  HTTP_CURRENT=""`,
    `  mv "$HTTPS_CURRENT" "$STATE_HTTPS"`,
    `  HTTPS_CURRENT=""`,
    `fi`,
    `ROLLBACK_HTTP="$(mktemp "$STATE_DIR/$STATE_KEY.http.rollback.XXXXXX")"`,
    `ROLLBACK_HTTPS="$(mktemp "$STATE_DIR/$STATE_KEY.https.rollback.XXXXXX")"`,
    `ROLLBACK_VERIFY_HTTP="$(mktemp "$STATE_DIR/$STATE_KEY.http.rollback-verify.XXXXXX")"`,
    `ROLLBACK_VERIFY_HTTPS="$(mktemp "$STATE_DIR/$STATE_KEY.https.rollback-verify.XXXXXX")"`,
    `if ! networksetup -getwebproxy "$SERVICE" > "$ROLLBACK_HTTP" || ! networksetup -getsecurewebproxy "$SERVICE" > "$ROLLBACK_HTTPS"; then`,
    `  printf '%s\\n' 'Could not read the current macOS proxy settings for transactional rollback. Proxy settings were not changed.' >&2`,
    `  exit 1`,
    `fi`,
    `if [ "$BACKUP_ALREADY_PRESENT" = "1" ]; then`,
    `  preflight_expected_apex_proxy_state_pair "$ROLLBACK_HTTP" "$ROLLBACK_HTTPS"`,
    `else`,
    `  preflight_rollbackable_proxy_state_pair "$ROLLBACK_HTTP" "$ROLLBACK_HTTPS"`,
    `fi`,
    `ROLLBACK_HTTP_ENABLED="$SAVED_HTTP_ENABLED"`,
    `ROLLBACK_HTTP_SERVER="$SAVED_HTTP_SERVER"`,
    `ROLLBACK_HTTP_PORT="$SAVED_HTTP_PORT"`,
    `ROLLBACK_HTTP_AUTHENTICATED="$SAVED_HTTP_AUTHENTICATED"`,
    `ROLLBACK_HTTPS_ENABLED="$SAVED_HTTPS_ENABLED"`,
    `ROLLBACK_HTTPS_SERVER="$SAVED_HTTPS_SERVER"`,
    `ROLLBACK_HTTPS_PORT="$SAVED_HTTPS_PORT"`,
    `ROLLBACK_HTTPS_AUTHENTICATED="$SAVED_HTTPS_AUTHENTICATED"`,
    `APPLY_PROXY_USER="$PROXY_USER"`,
    `APPLY_PROXY_PASSWORD="$PROXY_PASSWORD"`,
    `set_recorder_proxy() {`,
    `  networksetup -setwebproxy "$SERVICE" "$PROXY_HOST" "$PROXY_PORT"${proxyAuthArgs} || return 1`,
    `  networksetup -setsecurewebproxy "$SERVICE" "$PROXY_HOST" "$PROXY_PORT"${proxyAuthArgs} || return 1`,
    `  networksetup -setwebproxystate "$SERVICE" on || return 1`,
    `  networksetup -setsecurewebproxystate "$SERVICE" on || return 1`,
    `}`,
    `rollback_proxy_start_transaction() {`,
    `  trap '' HUP INT TERM`,
    `  apply_validated_proxy_state_pair "$ROLLBACK_HTTP_ENABLED" "$ROLLBACK_HTTP_SERVER" "$ROLLBACK_HTTP_PORT" "$ROLLBACK_HTTP_AUTHENTICATED" "$ROLLBACK_HTTPS_ENABLED" "$ROLLBACK_HTTPS_SERVER" "$ROLLBACK_HTTPS_PORT" "$ROLLBACK_HTTPS_AUTHENTICATED" && verify_proxy_state_pair "$ROLLBACK_HTTP" "$ROLLBACK_HTTPS" "$ROLLBACK_VERIFY_HTTP" "$ROLLBACK_VERIFY_HTTPS"`,
    `}`,
    `handle_proxy_start_signal() {`,
    `  trap '' HUP INT TERM`,
    `  if [ "$TRANSACTION_ACTIVE" = "1" ]; then`,
    `    if rollback_proxy_start_transaction; then`,
    `      printf '%s\\n' "The APEX Load macOS proxy update was interrupted. The complete previous proxy state was restored and the original backup was preserved in $STATE_DIR." >&2`,
    `    else`,
    `      SIGNAL_ROLLBACK_STATUS="$?"`,
    `      printf '%s\\n' "The APEX Load macOS proxy update was interrupted, and rolling back the partial update also failed (status $SIGNAL_ROLLBACK_STATUS). The original backup was preserved in $STATE_DIR; inspect and restore the proxy settings manually." >&2`,
    `    fi`,
    `  fi`,
    `  exit 1`,
    `}`,
    `trap handle_proxy_start_signal HUP INT TERM`,
    `TRANSACTION_ACTIVE=1`,
    `if set_recorder_proxy; then`,
    `  TRANSACTION_ACTIVE=0`,
    `else`,
    `  START_STATUS="$?"`,
    `  if rollback_proxy_start_transaction; then`,
    `    printf '%s\\n' "Setting the APEX Load macOS proxy failed (status $START_STATUS). The complete previous proxy state was restored and the original backup was preserved in $STATE_DIR." >&2`,
    `  else`,
    `    ROLLBACK_STATUS="$?"`,
    `    printf '%s\\n' "Setting the APEX Load macOS proxy failed (status $START_STATUS), and rolling back the partial update also failed (status $ROLLBACK_STATUS). The original backup was preserved in $STATE_DIR; inspect and restore the proxy settings manually." >&2`,
    `  fi`,
    `  exit 1`,
    `fi`,
    `)`,
  ].join("\n");
}

export function buildMacOSRestoreProxyCommand(
  networkService: string = DEFAULT_MACOS_NETWORK_SERVICE,
  proxyHost: string = DEFAULT_RECORDER_PROXY_HOST,
  proxyPort: string = DEFAULT_RECORDER_PROXY_PORT,
  proxyPassword: string = "",
) {
  const service = networkService.trim() || DEFAULT_MACOS_NETWORK_SERVICE;
  const host = normalizeProxyHost(proxyHost);
  const port = normalizeProxyPort(proxyPort);
  return [
    `(`,
    `set -e`,
    `SERVICE=${shellQuote(service)}`,
    `PROXY_HOST=${shellQuote(host)}`,
    `PROXY_PORT=${shellQuote(port)}`,
    `PROXY_USER='apexload'`,
    `PROXY_PASSWORD=${shellQuote(proxyPassword)}`,
    `STATE_DIR="\${HOME:?}/Library/Application Support/APEX Load/recorder-proxy-state"`,
    `STATE_KEY="$(printf '%s' "$SERVICE" | /usr/bin/shasum -a 256 | awk '{print $1}')"`,
    `case "$STATE_KEY" in ''|*[!0-9a-f]*) printf '%s\n' 'Could not derive a safe proxy-state key. Proxy settings were not changed.' >&2; exit 1;; esac`,
    `STATE_HTTP="$STATE_DIR/$STATE_KEY.http"`,
    `STATE_HTTPS="$STATE_DIR/$STATE_KEY.https"`,
    `LOCK_FILE="$STATE_DIR/$STATE_KEY.lock"`,
    `umask 077`,
    `mkdir -p "$STATE_DIR"`,
    `chmod 700 "$STATE_DIR"`,
    `exec 9>"$LOCK_FILE"`,
    `if ! /usr/bin/lockf -s -t 0 9; then`,
    `  printf '%s\\n' 'Another APEX Load proxy command is running. Proxy settings were not changed.' >&2`,
    `  exit 1`,
    `fi`,
    `VERIFY_HTTP=""`,
    `VERIFY_HTTPS=""`,
    `ROLLBACK_HTTP=""`,
    `ROLLBACK_HTTPS=""`,
    `TRANSACTION_ACTIVE=0`,
    `cleanup_proxy_restore() {`,
    `  [ -z "$VERIFY_HTTP" ] || rm -f "$VERIFY_HTTP"`,
    `  [ -z "$VERIFY_HTTPS" ] || rm -f "$VERIFY_HTTPS"`,
    `  [ -z "$ROLLBACK_HTTP" ] || rm -f "$ROLLBACK_HTTP"`,
    `  [ -z "$ROLLBACK_HTTPS" ] || rm -f "$ROLLBACK_HTTPS"`,
    `}`,
    `trap cleanup_proxy_restore EXIT`,
    `trap 'exit 1' HUP INT TERM`,
    ...macOSProxyStatePreflightCommands(),
    ...macOSProxyStateApplyCommands(),
    `if [ ! -f "$STATE_HTTP" ] || [ ! -f "$STATE_HTTPS" ]; then`,
    `  printf '%s\\n' 'No complete saved APEX Load proxy state was found. Proxy settings were not changed.' >&2`,
    `  exit 1`,
    `fi`,
    `preflight_proxy_state_pair "$STATE_HTTP" "$STATE_HTTPS"`,
    `TARGET_HTTP_ENABLED="$SAVED_HTTP_ENABLED"`,
    `TARGET_HTTP_SERVER="$SAVED_HTTP_SERVER"`,
    `TARGET_HTTP_PORT="$SAVED_HTTP_PORT"`,
    `TARGET_HTTP_AUTHENTICATED="$SAVED_HTTP_AUTHENTICATED"`,
    `TARGET_HTTPS_ENABLED="$SAVED_HTTPS_ENABLED"`,
    `TARGET_HTTPS_SERVER="$SAVED_HTTPS_SERVER"`,
    `TARGET_HTTPS_PORT="$SAVED_HTTPS_PORT"`,
    `TARGET_HTTPS_AUTHENTICATED="$SAVED_HTTPS_AUTHENTICATED"`,
    `ROLLBACK_HTTP="$(mktemp "$STATE_DIR/$STATE_KEY.http.rollback.XXXXXX")"`,
    `ROLLBACK_HTTPS="$(mktemp "$STATE_DIR/$STATE_KEY.https.rollback.XXXXXX")"`,
    `if ! networksetup -getwebproxy "$SERVICE" > "$ROLLBACK_HTTP" || ! networksetup -getsecurewebproxy "$SERVICE" > "$ROLLBACK_HTTPS"; then`,
    `  printf '%s\\n' 'Could not read the current macOS proxy settings for transactional rollback. The original backup was preserved and no proxy settings were changed.' >&2`,
    `  exit 1`,
    `fi`,
    `preflight_expected_apex_proxy_state_pair "$ROLLBACK_HTTP" "$ROLLBACK_HTTPS"`,
    `ROLLBACK_HTTP_ENABLED="$SAVED_HTTP_ENABLED"`,
    `ROLLBACK_HTTP_SERVER="$SAVED_HTTP_SERVER"`,
    `ROLLBACK_HTTP_PORT="$SAVED_HTTP_PORT"`,
    `ROLLBACK_HTTP_AUTHENTICATED="$SAVED_HTTP_AUTHENTICATED"`,
    `ROLLBACK_HTTPS_ENABLED="$SAVED_HTTPS_ENABLED"`,
    `ROLLBACK_HTTPS_SERVER="$SAVED_HTTPS_SERVER"`,
    `ROLLBACK_HTTPS_PORT="$SAVED_HTTPS_PORT"`,
    `ROLLBACK_HTTPS_AUTHENTICATED="$SAVED_HTTPS_AUTHENTICATED"`,
    `APPLY_PROXY_USER="$PROXY_USER"`,
    `APPLY_PROXY_PASSWORD="$PROXY_PASSWORD"`,
    `VERIFY_HTTP="$(mktemp "$STATE_DIR/$STATE_KEY.http.verify.XXXXXX")"`,
    `VERIFY_HTTPS="$(mktemp "$STATE_DIR/$STATE_KEY.https.verify.XXXXXX")"`,
    `rollback_proxy_restore_transaction() {`,
    `  trap '' HUP INT TERM`,
    `  apply_validated_proxy_state_pair "$ROLLBACK_HTTP_ENABLED" "$ROLLBACK_HTTP_SERVER" "$ROLLBACK_HTTP_PORT" "$ROLLBACK_HTTP_AUTHENTICATED" "$ROLLBACK_HTTPS_ENABLED" "$ROLLBACK_HTTPS_SERVER" "$ROLLBACK_HTTPS_PORT" "$ROLLBACK_HTTPS_AUTHENTICATED" && verify_proxy_state_pair "$ROLLBACK_HTTP" "$ROLLBACK_HTTPS" "$VERIFY_HTTP" "$VERIFY_HTTPS"`,
    `}`,
    `handle_proxy_restore_signal() {`,
    `  trap '' HUP INT TERM`,
    `  if [ "$TRANSACTION_ACTIVE" = "1" ]; then`,
    `    if rollback_proxy_restore_transaction; then`,
    `      printf '%s\\n' "The saved macOS proxy restore was interrupted. The complete pre-restore proxy state was restored and the original backup was preserved in $STATE_DIR." >&2`,
    `    else`,
    `      SIGNAL_ROLLBACK_STATUS="$?"`,
    `      printf '%s\\n' "The saved macOS proxy restore was interrupted, and rolling back the partial update also failed (status $SIGNAL_ROLLBACK_STATUS). The original backup was preserved in $STATE_DIR; inspect and restore the proxy settings manually." >&2`,
    `    fi`,
    `  fi`,
    `  exit 1`,
    `}`,
    `trap handle_proxy_restore_signal HUP INT TERM`,
    `TRANSACTION_ACTIVE=1`,
    `if apply_validated_proxy_state_pair "$TARGET_HTTP_ENABLED" "$TARGET_HTTP_SERVER" "$TARGET_HTTP_PORT" "$TARGET_HTTP_AUTHENTICATED" "$TARGET_HTTPS_ENABLED" "$TARGET_HTTPS_SERVER" "$TARGET_HTTPS_PORT" "$TARGET_HTTPS_AUTHENTICATED" && verify_proxy_state_pair "$STATE_HTTP" "$STATE_HTTPS" "$VERIFY_HTTP" "$VERIFY_HTTPS"; then`,
    `  TRANSACTION_ACTIVE=0`,
    `  rm -f "$STATE_HTTP" "$STATE_HTTPS"`,
    `else`,
    `  RESTORE_STATUS="$?"`,
    `  if rollback_proxy_restore_transaction; then`,
    `    printf '%s\\n' "Restoring the saved macOS proxy state or verifying it failed (status $RESTORE_STATUS). The complete pre-restore proxy state was restored and the original backup was preserved in $STATE_DIR." >&2`,
    `  else`,
    `    ROLLBACK_STATUS="$?"`,
    `    printf '%s\\n' "Restoring the saved macOS proxy state or verifying it failed (status $RESTORE_STATUS), and rolling back the partial update also failed (status $ROLLBACK_STATUS). The original backup was preserved in $STATE_DIR; inspect and restore the proxy settings manually." >&2`,
    `  fi`,
    `  exit 1`,
    `fi`,
    `)`,
  ].join("\n");
}

function windowsProxyChangeNotifierCommand() {
  return [
    `function Notify-ProxySettingsChanged {`,
    `  $signature = '[DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Auto)] public static extern IntPtr SendMessageTimeout(IntPtr hWnd, uint Msg, UIntPtr wParam, string lParam, uint fuFlags, uint uTimeout, out UIntPtr lpdwResult);'`,
    `  try { $native = [ApexLoad.NativeMethods] } catch { $native = Add-Type -MemberDefinition $signature -Name NativeMethods -Namespace ApexLoad -PassThru }`,
    `  $result = [UIntPtr]::Zero`,
    `  $sendResult = $native::SendMessageTimeout([IntPtr]0xffff, 0x1A, [UIntPtr]::Zero, 'Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings', 0x2, 5000, [ref]$result)`,
    `  if ($sendResult -eq [IntPtr]::Zero) {`,
    `    $errorCode = [Runtime.InteropServices.Marshal]::GetLastWin32Error()`,
    `    throw ('Windows did not acknowledge the proxy settings change notification (Win32 error {0}).' -f $errorCode)`,
    `  }`,
    `}`,
  ];
}

function windowsProxyStateValidationCommands() {
  return [
    `function Assert-SavedProxyState {`,
    `  param([object]$Saved)`,
    `  if ($null -eq $Saved -or $Saved -isnot [System.Management.Automation.PSCustomObject]) {`,
    `    throw 'The saved APEX Load Windows proxy state must be a JSON object. Proxy settings were not changed; preserve the file for manual recovery.'`,
    `  }`,
    `  $required = @('ProxyEnable', 'ProxyServer', 'ProxyOverride', 'AutoConfigURL', 'AutoDetect')`,
    `  $actual = @($Saved.PSObject.Properties.Name)`,
    `  foreach ($name in $required) {`,
    `    if ($actual -cnotcontains $name) {`,
    `      throw ('The saved APEX Load Windows proxy state is missing {0}. Proxy settings were not changed; preserve the file for manual recovery.' -f $name)`,
    `    }`,
    `  }`,
    `  foreach ($name in $actual) {`,
    `    if ($required -cnotcontains $name) {`,
    `      throw ('The saved APEX Load Windows proxy state contains unexpected property {0}. Proxy settings were not changed; preserve the file for manual recovery.' -f $name)`,
    `    }`,
    `  }`,
    `  foreach ($name in @('ProxyEnable', 'AutoDetect')) {`,
    `    $value = $Saved.$name`,
    `    if ($null -ne $value) {`,
    `      if (($value -isnot [int]) -and ($value -isnot [long])) {`,
    `        throw ('The saved APEX Load Windows proxy state has a non-integer {0}. Proxy settings were not changed; preserve the file for manual recovery.' -f $name)`,
    `      }`,
    `      if ([long]$value -lt 0 -or [long]$value -gt 1) {`,
    `        throw ('The saved APEX Load Windows proxy state has {0} outside the supported range 0..1. Proxy settings were not changed; preserve the file for manual recovery.' -f $name)`,
    `      }`,
    `    }`,
    `  }`,
    `  foreach ($name in @('ProxyServer', 'ProxyOverride', 'AutoConfigURL')) {`,
    `    $value = $Saved.$name`,
    `    if ($null -ne $value -and $value -isnot [string]) {`,
    `      throw ('The saved APEX Load Windows proxy state has a non-string {0}. Proxy settings were not changed; preserve the file for manual recovery.' -f $name)`,
    `    }`,
    `    if ($null -ne $value -and ($value.Length -gt 1048576 -or $value.IndexOf([char]0) -ge 0)) {`,
    `      throw ('The saved APEX Load Windows proxy state has an invalid {0} string. Proxy settings were not changed; preserve the file for manual recovery.' -f $name)`,
    `    }`,
    `  }`,
    `  return $Saved`,
    `}`,
    `function Read-SavedProxyState {`,
    `  param([string]$Path)`,
    `  try { $saved = Get-Content -LiteralPath $Path -Raw -ErrorAction Stop | ConvertFrom-Json -ErrorAction Stop } catch {`,
    `    throw 'The saved APEX Load Windows proxy state is corrupt or incomplete. Proxy settings were not changed; preserve the file for manual recovery.'`,
    `  }`,
    `  return Assert-SavedProxyState -Saved $saved`,
    `}`,
  ];
}

function windowsProxyLockAcquireCommands() {
  return [
    `$lockPath = $statePath + '.lock'`,
    `$lockStream = $null`,
    `try {`,
    `  $lockStream = [System.IO.File]::Open($lockPath, [System.IO.FileMode]::OpenOrCreate, [System.IO.FileAccess]::ReadWrite, [System.IO.FileShare]::None)`,
    `} catch {`,
    `  throw 'Another APEX Load Windows proxy command is running. Proxy settings were not changed.'`,
    `}`,
  ];
}

function windowsProxyLockReleaseCommands() {
  return [
    `  if ($null -ne $stateTempPath -and (Test-Path -LiteralPath $stateTempPath)) { Remove-Item -LiteralPath $stateTempPath -Force -ErrorAction SilentlyContinue }`,
    `  if ($null -ne $lockStream) { $lockStream.Dispose() }`,
    `  Remove-Item -LiteralPath $lockPath -Force -ErrorAction SilentlyContinue`,
  ];
}

function windowsProxyRegistryStateMutationCommands() {
  return [
    `function Has-SavedValue {`,
    `  param([object]$SavedState, [string]$Name)`,
    `  return $SavedState.PSObject.Properties.Name -ccontains $Name -and $null -ne $SavedState.$Name`,
    `}`,
    `function Remove-ProxyRegistryValue {`,
    `  param([string]$Name)`,
    `  if (Test-ProxyRegistryValue $Name) {`,
    `    Remove-ItemProperty -LiteralPath $settingsPath -Name $Name -ErrorAction Stop`,
    `  }`,
    `}`,
    `function Restore-StringRegistryValue {`,
    `  param([string]$Name, [object]$SavedState)`,
    `  if (Has-SavedValue -SavedState $SavedState -Name $Name) {`,
    `    New-ItemProperty -Path $settingsPath -Name $Name -PropertyType String -Value ([string]$SavedState.$Name) -Force | Out-Null`,
    `  } else {`,
    `    Remove-ProxyRegistryValue $Name`,
    `  }`,
    `}`,
    `function Restore-DwordRegistryValue {`,
    `  param([string]$Name, [object]$SavedState)`,
    `  if (Has-SavedValue -SavedState $SavedState -Name $Name) {`,
    `    New-ItemProperty -Path $settingsPath -Name $Name -PropertyType DWord -Value ([int]$SavedState.$Name) -Force | Out-Null`,
    `  } else {`,
    `    Remove-ProxyRegistryValue $Name`,
    `  }`,
    `}`,
    `function Restore-ProxyRegistryState {`,
    `  param([object]$SavedState)`,
    `  Restore-DwordRegistryValue -Name ProxyEnable -SavedState $SavedState`,
    `  Restore-StringRegistryValue -Name ProxyServer -SavedState $SavedState`,
    `  Restore-StringRegistryValue -Name ProxyOverride -SavedState $SavedState`,
    `  Restore-StringRegistryValue -Name AutoConfigURL -SavedState $SavedState`,
    `  Restore-DwordRegistryValue -Name AutoDetect -SavedState $SavedState`,
    `}`,
  ];
}

function windowsExpectedApexProxyStateCommands() {
  return [
    `function Assert-CurrentApexProxyState {`,
    `  param([object]$SavedOriginal)`,
    `  $null = Assert-SavedProxyState -Saved $SavedOriginal`,
    `  $currentProxyEnable = Get-ProxyRegistryValue 'ProxyEnable'`,
    `  $currentProxyServer = Get-ProxyRegistryValue 'ProxyServer'`,
    `  $currentProxyOverride = Get-ProxyRegistryValue 'ProxyOverride'`,
    `  $currentAutoConfigURL = Get-ProxyRegistryValue 'AutoConfigURL'`,
    `  $currentAutoDetect = Get-ProxyRegistryValue 'AutoDetect'`,
    `  $proxyEnableMatches = (($currentProxyEnable -is [int]) -or ($currentProxyEnable -is [long])) -and ([long]$currentProxyEnable -eq 1)`,
    `  $proxyOverrideMatches = if ($null -eq $SavedOriginal.ProxyOverride) { $null -eq $currentProxyOverride } else { $currentProxyOverride -is [string] -and $currentProxyOverride -ceq [string]$SavedOriginal.ProxyOverride }`,
    `  $autoDetectMatches = (($currentAutoDetect -is [int]) -or ($currentAutoDetect -is [long])) -and ([long]$currentAutoDetect -eq 0)`,
    `  if (!$proxyEnableMatches -or $currentProxyServer -isnot [string] -or $currentProxyServer -cne $proxyServer -or !$proxyOverrideMatches -or $null -ne $currentAutoConfigURL -or !$autoDetectMatches) {`,
    `    throw 'The current Windows proxy state does not exactly match the active APEX Load proxy transaction. Proxy settings were not changed; restore it manually in Windows Settings if necessary.'`,
    `  }`,
    `}`,
  ];
}

export function buildWindowsStartProxyCommand(
  proxyPort: string,
  proxyHost: string = DEFAULT_RECORDER_PROXY_HOST,
) {
  const port = normalizeProxyPort(proxyPort);
  const host = normalizeProxyHost(proxyHost);
  return [
    `$ErrorActionPreference = 'Stop'`,
    `$proxyHost = ${powerShellQuote(host)}`,
    `$proxyPort = ${powerShellQuote(port)}`,
    `$proxyServer = 'http={0}:{1};https={0}:{1}' -f $proxyHost, $proxyPort`,
    `$settingsPath = 'HKCU:\\Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings'`,
    `$localAppData = [Environment]::GetFolderPath([Environment+SpecialFolder]::LocalApplicationData)`,
    `if ([string]::IsNullOrWhiteSpace($localAppData)) { throw 'Local application data is unavailable; proxy settings were not changed.' }`,
    `$stateDir = Join-Path $localAppData 'APEX Load\\recorder-proxy-state'`,
    `$statePath = Join-Path $stateDir 'windows-11-wininet.json'`,
    `New-Item -ItemType Directory -Force -Path $stateDir | Out-Null`,
    ...windowsProxyStateValidationCommands(),
    `function Test-ProxyRegistryValue {`,
    `  param([string]$Name)`,
    `  $key = Get-Item -LiteralPath $settingsPath -ErrorAction Stop`,
    `  return $key.GetValueNames() -contains $Name`,
    `}`,
    `function Get-ProxyRegistryValue {`,
    `  param([string]$Name)`,
    `  if (!(Test-ProxyRegistryValue $Name)) { return $null }`,
    `  return (Get-ItemProperty -LiteralPath $settingsPath -Name $Name -ErrorAction Stop).$Name`,
    `}`,
    ...windowsExpectedApexProxyStateCommands(),
    ...windowsProxyRegistryStateMutationCommands(),
    ...windowsProxyChangeNotifierCommand(),
    ...windowsProxyLockAcquireCommands(),
    `$stateTempPath = $null`,
    `$backupAlreadyPresent = $false`,
    `$savedOriginal = $null`,
    `try {`,
    `if (!(Test-Path -LiteralPath $statePath)) {`,
    `  $snapshot = [pscustomobject]@{`,
    `    ProxyEnable = Get-ProxyRegistryValue 'ProxyEnable'`,
    `    ProxyServer = Get-ProxyRegistryValue 'ProxyServer'`,
    `    ProxyOverride = Get-ProxyRegistryValue 'ProxyOverride'`,
    `    AutoConfigURL = Get-ProxyRegistryValue 'AutoConfigURL'`,
    `    AutoDetect = Get-ProxyRegistryValue 'AutoDetect'`,
    `  }`,
    `  $null = Assert-SavedProxyState -Saved $snapshot`,
    `  $json = $snapshot | ConvertTo-Json`,
    `  $stateTempPath = Join-Path $stateDir ('windows-11-wininet.{0}.tmp' -f [Guid]::NewGuid().ToString('N'))`,
    `  $bytes = [System.Text.UTF8Encoding]::new($false).GetBytes($json)`,
    `  $stateStream = [System.IO.File]::Open($stateTempPath, [System.IO.FileMode]::CreateNew, [System.IO.FileAccess]::Write, [System.IO.FileShare]::None)`,
    `  try {`,
    `    $stateStream.Write($bytes, 0, $bytes.Length)`,
    `    $stateStream.Flush($true)`,
    `  } finally {`,
    `    $stateStream.Dispose()`,
    `  }`,
    `  [System.IO.File]::Move($stateTempPath, $statePath)`,
    `  $stateTempPath = $null`,
    `} else {`,
    `  $savedOriginal = Read-SavedProxyState $statePath`,
    `  $backupAlreadyPresent = $true`,
    `  Write-Host ('Preserving the existing original proxy backup at {0}.' -f $statePath)`,
    `}`,
    `if ($backupAlreadyPresent) { Assert-CurrentApexProxyState -SavedOriginal $savedOriginal }`,
    `$rollbackState = [pscustomobject]@{`,
    `  ProxyEnable = Get-ProxyRegistryValue 'ProxyEnable'`,
    `  ProxyServer = Get-ProxyRegistryValue 'ProxyServer'`,
    `  ProxyOverride = Get-ProxyRegistryValue 'ProxyOverride'`,
    `  AutoConfigURL = Get-ProxyRegistryValue 'AutoConfigURL'`,
    `  AutoDetect = Get-ProxyRegistryValue 'AutoDetect'`,
    `}`,
    `$null = Assert-SavedProxyState -Saved $rollbackState`,
    `try {`,
    `  New-ItemProperty -Path $settingsPath -Name ProxyEnable -PropertyType DWord -Value 1 -Force | Out-Null`,
    `  New-ItemProperty -Path $settingsPath -Name ProxyServer -PropertyType String -Value $proxyServer -Force | Out-Null`,
    `  if (Test-ProxyRegistryValue 'AutoConfigURL') { Remove-ItemProperty -LiteralPath $settingsPath -Name AutoConfigURL -ErrorAction Stop }`,
    `  New-ItemProperty -Path $settingsPath -Name AutoDetect -PropertyType DWord -Value 0 -Force | Out-Null`,
    `  Notify-ProxySettingsChanged`,
    `} catch {`,
    `  $startFailure = $_`,
    `  try {`,
    `    Restore-ProxyRegistryState -SavedState $rollbackState`,
    `    Notify-ProxySettingsChanged`,
    `  } catch {`,
    `    throw ('Setting the APEX Load Windows proxy failed and rolling back the partial registry update also failed. The original backup was preserved. Start error: {0}; rollback error: {1}' -f $startFailure.Exception.Message, $_.Exception.Message)`,
    `  }`,
    `  throw $startFailure`,
    `}`,
    `Write-Host ('APEX Load Windows proxy set to {0}:{1}. Saved previous state to {2}' -f $proxyHost, $proxyPort, $statePath)`,
    `} finally {`,
    ...windowsProxyLockReleaseCommands(),
    `}`,
  ].join("\n");
}

export function buildWindowsRestoreProxyCommand(
  proxyPort: string = DEFAULT_RECORDER_PROXY_PORT,
  proxyHost: string = DEFAULT_RECORDER_PROXY_HOST,
) {
  const port = normalizeProxyPort(proxyPort);
  const host = normalizeProxyHost(proxyHost);
  return [
    `$ErrorActionPreference = 'Stop'`,
    `$proxyHost = ${powerShellQuote(host)}`,
    `$proxyPort = ${powerShellQuote(port)}`,
    `$proxyServer = 'http={0}:{1};https={0}:{1}' -f $proxyHost, $proxyPort`,
    `$settingsPath = 'HKCU:\\Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings'`,
    `$localAppData = [Environment]::GetFolderPath([Environment+SpecialFolder]::LocalApplicationData)`,
    `if ([string]::IsNullOrWhiteSpace($localAppData)) { throw 'Local application data is unavailable; proxy settings were not changed.' }`,
    `$stateDir = Join-Path $localAppData 'APEX Load\\recorder-proxy-state'`,
    `$statePath = Join-Path $stateDir 'windows-11-wininet.json'`,
    `New-Item -ItemType Directory -Force -Path $stateDir | Out-Null`,
    ...windowsProxyStateValidationCommands(),
    ...windowsProxyLockAcquireCommands(),
    `$stateTempPath = $null`,
    `try {`,
    `if (!(Test-Path -LiteralPath $statePath -PathType Leaf)) {`,
    `  Write-Warning 'No saved APEX Load Windows proxy state was found.'`,
    `  return`,
    `}`,
    `$state = Read-SavedProxyState $statePath`,
    `function Test-ProxyRegistryValue {`,
    `  param([string]$Name)`,
    `  $key = Get-Item -LiteralPath $settingsPath -ErrorAction Stop`,
    `  return $key.GetValueNames() -contains $Name`,
    `}`,
    `function Get-ProxyRegistryValue {`,
    `  param([string]$Name)`,
    `  if (!(Test-ProxyRegistryValue $Name)) { return $null }`,
    `  return (Get-ItemProperty -LiteralPath $settingsPath -Name $Name -ErrorAction Stop).$Name`,
    `}`,
    ...windowsExpectedApexProxyStateCommands(),
    ...windowsProxyRegistryStateMutationCommands(),
    ...windowsProxyChangeNotifierCommand(),
    `Assert-CurrentApexProxyState -SavedOriginal $state`,
    `$rollbackState = [pscustomobject]@{`,
    `  ProxyEnable = Get-ProxyRegistryValue 'ProxyEnable'`,
    `  ProxyServer = Get-ProxyRegistryValue 'ProxyServer'`,
    `  ProxyOverride = Get-ProxyRegistryValue 'ProxyOverride'`,
    `  AutoConfigURL = Get-ProxyRegistryValue 'AutoConfigURL'`,
    `  AutoDetect = Get-ProxyRegistryValue 'AutoDetect'`,
    `}`,
    `$null = Assert-SavedProxyState -Saved $rollbackState`,
    `try {`,
    `  Restore-ProxyRegistryState -SavedState $state`,
    `  Notify-ProxySettingsChanged`,
    `} catch {`,
    `  $restoreFailure = $_`,
    `  try {`,
    `    Restore-ProxyRegistryState -SavedState $rollbackState`,
    `    Notify-ProxySettingsChanged`,
    `  } catch {`,
    `    throw ('Restoring the saved APEX Load Windows proxy state failed and rolling back the partial registry update also failed. The backup was preserved. Restore error: {0}; rollback error: {1}' -f $restoreFailure.Exception.Message, $_.Exception.Message)`,
    `  }`,
    `  throw $restoreFailure`,
    `}`,
    `Remove-Item -LiteralPath $statePath -ErrorAction Stop`,
    `Write-Host 'APEX Load Windows proxy settings restored.'`,
    `} finally {`,
    ...windowsProxyLockReleaseCommands(),
    `}`,
  ].join("\n");
}
