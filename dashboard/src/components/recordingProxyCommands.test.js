// @vitest-environment node

import { execFileSync, spawn, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import {
  buildMacOSRestoreProxyCommand,
  buildMacOSStartProxyCommand,
} from "./recordingProxyCommands";

const itOnMacOS = process.platform === "darwin" ? it : it.skip;

function proxyState({
  enabled = "No",
  server = "",
  port = "0",
  authenticated = "0",
} = {}) {
  return [
    `Enabled: ${enabled}`,
    `Server: ${server}`,
    `Port: ${port}`,
    `Authenticated Proxy Enabled: ${authenticated}`,
    "",
  ].join("\n");
}

function createMacOSProxyCommandFixture(prefix) {
  const homeDirectory = mkdtempSync(join(tmpdir(), prefix));
  const stateDirectory = join(
    homeDirectory,
    "Library",
    "Application Support",
    "APEX Load",
    "recorder-proxy-state",
  );
  const fakeBin = join(homeDirectory, "bin");
  const callsFile = join(homeDirectory, "networksetup-calls.txt");
  const stateKey = createHash("sha256").update("Wi-Fi").digest("hex");
  mkdirSync(stateDirectory, { recursive: true });
  mkdirSync(fakeBin, { recursive: true });
  writeFileSync(
    join(fakeBin, "networksetup"),
    [
      "#!/bin/sh",
      `printf '%s\\n' "$*" >> "$CALLS_FILE"`,
      'case "$1" in',
      "  -getwebproxy|-getsecurewebproxy)",
      "    printf '%s\\n' 'Enabled: No' 'Server: ' 'Port: 0' 'Authenticated Proxy Enabled: 0'",
      "    ;;",
      "esac",
    ].join("\n"),
    { mode: 0o755 },
  );
  return {
    callsFile,
    environment: {
      ...process.env,
      CALLS_FILE: callsFile,
      HOME: homeDirectory,
      PATH: `${fakeBin}:${process.env.PATH ?? ""}`,
    },
    httpState: join(stateDirectory, `${stateKey}.http`),
    httpsState: join(stateDirectory, `${stateKey}.https`),
  };
}

function recordedNetworkSetupCalls(path) {
  return existsSync(path)
    ? readFileSync(path, "utf8").split("\n").filter(Boolean)
    : [];
}

function createTransactionalMacOSFixture(prefix, currentHTTP, currentHTTPS) {
  const homeDirectory = mkdtempSync(join(tmpdir(), prefix));
  const stateDirectory = join(
    homeDirectory,
    "Library",
    "Application Support",
    "APEX Load",
    "recorder-proxy-state",
  );
  const fakeDirectory = join(homeDirectory, "fake-networksetup");
  const fakeBin = join(homeDirectory, "bin");
  const stateKey = createHash("sha256").update("Wi-Fi").digest("hex");
  const currentHTTPPath = join(fakeDirectory, "current-http.txt");
  const currentHTTPSPath = join(fakeDirectory, "current-https.txt");
  const mutationCountPath = join(fakeDirectory, "mutation-count.txt");
  const failureMarkerPath = join(fakeDirectory, "failure-injected");
  const signalMarkerPath = join(fakeDirectory, "signal-injected");
  const verificationMarkerPath = join(fakeDirectory, "verification-corrupted");
  mkdirSync(stateDirectory, { recursive: true });
  mkdirSync(fakeDirectory, { recursive: true });
  mkdirSync(fakeBin, { recursive: true });
  writeFileSync(currentHTTPPath, currentHTTP, { mode: 0o600 });
  writeFileSync(currentHTTPSPath, currentHTTPS, { mode: 0o600 });
  writeFileSync(mutationCountPath, "0\n", { mode: 0o600 });
  writeFileSync(
    join(fakeBin, "networksetup"),
    [
      "#!/bin/sh",
      "set -eu",
      'command_name="$1"',
      "state_file_for_command() {",
      '  case "$command_name" in',
      '    *secure*) printf "%s" "$FAKE_HTTPS_STATE" ;;',
      '    *) printf "%s" "$FAKE_HTTP_STATE" ;;',
      "  esac",
      "}",
      "field() {",
      '  awk -F\": \" -v name="$2" \'$1 == name { print $2 }\' "$1"',
      "}",
      "write_state() {",
      '  destination="$1"',
      '  enabled="$2"',
      '  server="$3"',
      '  port="$4"',
      '  authenticated="$5"',
      '  temp_file="${destination}.tmp.$$"',
      `  printf 'Enabled: %s\\nServer: %s\\nPort: %s\\nAuthenticated Proxy Enabled: %s\\n' "$enabled" "$server" "$port" "$authenticated" > "$temp_file"`,
      '  mv "$temp_file" "$destination"',
      "}",
      "record_mutation() {",
      '  mutation_count="$(cat "$FAKE_MUTATION_COUNT")"',
      '  mutation_count="$((mutation_count + 1))"',
      `  printf '%s\\n' "$mutation_count" > "$FAKE_MUTATION_COUNT"`,
      '  if [ "${SIGNAL_MUTATION_NUMBER:-0}" = "$mutation_count" ] && [ ! -e "$FAKE_SIGNAL_MARKER" ]; then',
      '    : > "$FAKE_SIGNAL_MARKER"',
      '    kill -TERM "$PPID"',
      "    sleep 0.1",
      "    return 1",
      "  fi",
      '  if [ "${FAIL_MUTATION_NUMBER:-0}" = "$mutation_count" ] && [ ! -e "$FAKE_FAILURE_MARKER" ]; then',
      '    : > "$FAKE_FAILURE_MARKER"',
      "    printf 'injected networksetup mutation failure %s\\n' \"$mutation_count\" >&2",
      "    return 1",
      "  fi",
      "}",
      'case "$command_name" in',
      "  -getwebproxy|-getsecurewebproxy)",
      '    state_file="$(state_file_for_command)"',
      '    mutation_count="$(cat "$FAKE_MUTATION_COUNT")"',
      '    if [ "${CORRUPT_VERIFY:-0}" = "1" ] && [ "$mutation_count" -ge 4 ] && [ ! -e "$FAKE_VERIFICATION_MARKER" ]; then',
      '      : > "$FAKE_VERIFICATION_MARKER"',
      `      printf 'Enabled: No\\nServer: verification-mismatch.invalid\\nPort: 6553\\nAuthenticated Proxy Enabled: 0\\n'`,
      "    else",
      '      cat "$state_file"',
      "    fi",
      "    ;;",
      "  -setwebproxy|-setsecurewebproxy)",
      "    record_mutation || exit 42",
      '    state_file="$(state_file_for_command)"',
      '    enabled="$(field "$state_file" Enabled)"',
      '    authenticated="0"',
      '    if [ "${5:-off}" = "on" ]; then authenticated="1"; fi',
      '    write_state "$state_file" "$enabled" "$3" "$4" "$authenticated"',
      "    ;;",
      "  -setwebproxystate|-setsecurewebproxystate)",
      "    record_mutation || exit 42",
      '    state_file="$(state_file_for_command)"',
      '    server="$(field "$state_file" Server)"',
      '    port="$(field "$state_file" Port)"',
      '    authenticated="$(field "$state_file" "Authenticated Proxy Enabled")"',
      '    enabled="No"',
      '    if [ "$3" = "on" ]; then enabled="Yes"; fi',
      '    write_state "$state_file" "$enabled" "$server" "$port" "$authenticated"',
      "    ;;",
      "  *) exit 64 ;;",
      "esac",
    ].join("\n"),
    { mode: 0o755 },
  );
  return {
    backupHTTPPath: join(stateDirectory, `${stateKey}.http`),
    backupHTTPSPath: join(stateDirectory, `${stateKey}.https`),
    currentHTTPPath,
    currentHTTPSPath,
    environment: {
      ...process.env,
      FAKE_FAILURE_MARKER: failureMarkerPath,
      FAKE_HTTP_STATE: currentHTTPPath,
      FAKE_HTTPS_STATE: currentHTTPSPath,
      FAKE_MUTATION_COUNT: mutationCountPath,
      FAKE_SIGNAL_MARKER: signalMarkerPath,
      FAKE_VERIFICATION_MARKER: verificationMarkerPath,
      HOME: homeDirectory,
      PATH: `${fakeBin}:${process.env.PATH ?? ""}`,
    },
  };
}

describe("recording proxy command execution", () => {
  it("restores a configured-but-disabled macOS proxy endpoint before disabling it", () => {
    const command = buildMacOSRestoreProxyCommand("Wi-Fi");
    const functionStart = command.indexOf("apply_validated_proxy_state() {");
    const functionEnd = command.indexOf(
      `\napply_validated_proxy_state_pair() {`,
      functionStart,
    );
    expect(functionStart).toBeGreaterThanOrEqual(0);
    expect(functionEnd).toBeGreaterThan(functionStart);
    const restoreFunction = command.slice(functionStart, functionEnd);

    const directory = mkdtempSync(join(tmpdir(), "apexload-proxy-restore-"));
    const stateFile = join(directory, "disabled-proxy.txt");
    const callsFile = join(directory, "networksetup-calls.txt");
    writeFileSync(
      stateFile,
      [
        "Enabled: No",
        "Server: disabled.proxy.example",
        "Port: 3128",
        "Authenticated Proxy Enabled: 0",
        "",
      ].join("\n"),
    );

    execFileSync(
      "bash",
      [
        "-c",
        [
          "set -e",
          `networksetup() { printf '%s\\n' "$*" >> "$CALLS_FILE"; }`,
          restoreFunction,
          `apply_validated_proxy_state No disabled.proxy.example 3128 -setwebproxy -setwebproxystate`,
        ].join("\n"),
      ],
      {
        env: {
          ...process.env,
          CALLS_FILE: callsFile,
          SERVICE: "Wi-Fi",
          STATE_FILE: stateFile,
        },
      },
    );

    expect(readFileSync(callsFile, "utf8").split("\n").filter(Boolean)).toEqual(
      [
        "-setwebproxy Wi-Fi disabled.proxy.example 3128 off",
        "-setwebproxystate Wi-Fi off",
      ],
    );
  });

  it("clears an APEX endpoint and credentials when the saved macOS proxy was blank", () => {
    const command = buildMacOSRestoreProxyCommand("Wi-Fi");
    const functionStart = command.indexOf("apply_validated_proxy_state() {");
    const functionEnd = command.indexOf(
      `\napply_validated_proxy_state_pair() {`,
      functionStart,
    );
    const restoreFunction = command.slice(functionStart, functionEnd);
    const directory = mkdtempSync(join(tmpdir(), "apexload-proxy-clear-"));
    const stateFile = join(directory, "blank-proxy.txt");
    const resultFile = join(directory, "restored-state.txt");
    writeFileSync(
      stateFile,
      [
        "Enabled: No",
        "Server: ",
        "Port: 0",
        "Authenticated Proxy Enabled: 0",
        "",
      ].join("\n"),
    );

    execFileSync(
      "bash",
      [
        "-c",
        [
          "set -e",
          `CURRENT_SERVER=localhost`,
          `CURRENT_PORT=49161`,
          `CURRENT_AUTH=on`,
          `CURRENT_USER=apexload`,
          `CURRENT_PASSWORD=per-launch-secret`,
          `CURRENT_ENABLED=on`,
          `networksetup() {`,
          `  case "$1" in`,
          `    -setwebproxy) CURRENT_SERVER="$3"; CURRENT_PORT="$4"; CURRENT_AUTH="\${5:-off}"; CURRENT_USER="\${6:-}"; CURRENT_PASSWORD="\${7:-}" ;;`,
          `    -setwebproxystate) CURRENT_ENABLED="$3" ;;`,
          `    *) return 2 ;;`,
          `  esac`,
          `}`,
          restoreFunction,
          `apply_validated_proxy_state No "" 0 -setwebproxy -setwebproxystate`,
          `printf 'server=%s\nport=%s\nauth=%s\nuser=%s\npassword=%s\nenabled=%s\n' "$CURRENT_SERVER" "$CURRENT_PORT" "$CURRENT_AUTH" "$CURRENT_USER" "$CURRENT_PASSWORD" "$CURRENT_ENABLED" > "$RESULT_FILE"`,
        ].join("\n"),
      ],
      {
        env: {
          ...process.env,
          RESULT_FILE: resultFile,
          SERVICE: "Wi-Fi",
          STATE_FILE: stateFile,
        },
      },
    );

    expect(readFileSync(resultFile, "utf8")).toBe(
      "server=\nport=0\nauth=off\nuser=\npassword=\nenabled=off\n",
    );
  });

  itOnMacOS(
    "refuses a corrupt existing HTTPS backup before starting the macOS proxy",
    () => {
      const fixture = createMacOSProxyCommandFixture(
        "apexload-proxy-start-preflight-",
      );
      writeFileSync(fixture.httpState, proxyState(), { mode: 0o600 });
      writeFileSync(fixture.httpsState, proxyState({ enabled: "sometimes" }), {
        mode: 0o600,
      });

      const result = spawnSync(
        "bash",
        ["-c", buildMacOSStartProxyCommand("8081", "Wi-Fi")],
        { encoding: "utf8", env: fixture.environment },
      );

      expect(result.status).not.toBe(0);
      expect(result.stderr).toContain("has an invalid enabled value");
      expect(recordedNetworkSetupCalls(fixture.callsFile)).toEqual([]);
    },
  );

  itOnMacOS(
    "preflights corrupt HTTPS state before any macOS restore mutation",
    () => {
      const fixture = createMacOSProxyCommandFixture(
        "apexload-proxy-restore-preflight-",
      );
      writeFileSync(
        fixture.httpState,
        proxyState({
          enabled: "Yes",
          server: "proxy.example.com",
          port: "3128",
        }),
        { mode: 0o600 },
      );
      writeFileSync(
        fixture.httpsState,
        proxyState({ server: "proxy.example.com", port: "70000" }),
        { mode: 0o600 },
      );

      const result = spawnSync(
        "bash",
        ["-c", buildMacOSRestoreProxyCommand("Wi-Fi")],
        { encoding: "utf8", env: fixture.environment },
      );

      expect(result.status).not.toBe(0);
      expect(result.stderr).toContain("has an invalid endpoint");
      expect(recordedNetworkSetupCalls(fixture.callsFile)).toEqual([]);
    },
  );

  itOnMacOS(
    "rolls back the complete macOS start state after every setter failure point",
    () => {
      const currentHTTP = proxyState({
        enabled: "Yes",
        server: "recorder.example.com",
        port: "8081",
      });
      const currentHTTPS = proxyState({
        enabled: "Yes",
        server: "recorder.example.com",
        port: "8081",
      });
      const originalHTTP = proxyState({
        server: "original-http.example.com",
        port: "8001",
      });
      const originalHTTPS = proxyState({
        server: "original-https.example.com",
        port: "8002",
      });

      for (const failurePoint of [1, 2, 3, 4]) {
        const fixture = createTransactionalMacOSFixture(
          `apexload-proxy-start-rollback-${failurePoint}-`,
          currentHTTP,
          currentHTTPS,
        );
        writeFileSync(fixture.backupHTTPPath, originalHTTP, { mode: 0o600 });
        writeFileSync(fixture.backupHTTPSPath, originalHTTPS, { mode: 0o600 });

        const result = spawnSync(
          "bash",
          [
            "-c",
            buildMacOSStartProxyCommand(
              "8081",
              "Wi-Fi",
              "recorder.example.com",
            ),
          ],
          {
            encoding: "utf8",
            env: {
              ...fixture.environment,
              FAIL_MUTATION_NUMBER: String(failurePoint),
            },
          },
        );

        expect(result.status).not.toBe(0);
        expect(result.stderr).toContain(
          "The complete previous proxy state was restored",
        );
        expect(readFileSync(fixture.currentHTTPPath, "utf8")).toBe(currentHTTP);
        expect(readFileSync(fixture.currentHTTPSPath, "utf8")).toBe(
          currentHTTPS,
        );
        expect(readFileSync(fixture.backupHTTPPath, "utf8")).toBe(originalHTTP);
        expect(readFileSync(fixture.backupHTTPSPath, "utf8")).toBe(
          originalHTTPS,
        );
      }
    },
    20_000,
  );

  itOnMacOS(
    "rolls back the complete macOS restore state after every setter failure point",
    () => {
      const currentHTTP = proxyState({
        enabled: "Yes",
        server: "localhost",
        port: "8081",
      });
      const currentHTTPS = proxyState({
        enabled: "Yes",
        server: "localhost",
        port: "8081",
      });
      const savedHTTP = proxyState({
        enabled: "No",
        server: "saved-http.example.com",
        port: "8001",
      });
      const savedHTTPS = proxyState({
        enabled: "Yes",
        server: "saved-https.example.com",
        port: "8002",
      });

      for (const failurePoint of [1, 2, 3, 4]) {
        const fixture = createTransactionalMacOSFixture(
          `apexload-proxy-restore-rollback-${failurePoint}-`,
          currentHTTP,
          currentHTTPS,
        );
        writeFileSync(fixture.backupHTTPPath, savedHTTP, { mode: 0o600 });
        writeFileSync(fixture.backupHTTPSPath, savedHTTPS, { mode: 0o600 });

        const result = spawnSync(
          "bash",
          ["-c", buildMacOSRestoreProxyCommand("Wi-Fi")],
          {
            encoding: "utf8",
            env: {
              ...fixture.environment,
              FAIL_MUTATION_NUMBER: String(failurePoint),
            },
          },
        );

        expect(result.status).not.toBe(0);
        expect(result.stderr).toContain(
          "The complete pre-restore proxy state was restored",
        );
        expect(readFileSync(fixture.currentHTTPPath, "utf8")).toBe(currentHTTP);
        expect(readFileSync(fixture.currentHTTPSPath, "utf8")).toBe(
          currentHTTPS,
        );
        expect(readFileSync(fixture.backupHTTPPath, "utf8")).toBe(savedHTTP);
        expect(readFileSync(fixture.backupHTTPSPath, "utf8")).toBe(savedHTTPS);
      }
    },
    20_000,
  );

  itOnMacOS(
    "rolls back macOS start when TERM interrupts an in-flight setter",
    () => {
      const currentHTTP = proxyState({
        enabled: "Yes",
        server: "recorder.example.com",
        port: "8081",
      });
      const currentHTTPS = proxyState({
        enabled: "Yes",
        server: "recorder.example.com",
        port: "8081",
      });
      const savedHTTP = proxyState();
      const savedHTTPS = proxyState();

      for (const signalPoint of [2, 4]) {
        const fixture = createTransactionalMacOSFixture(
          `apexload-proxy-start-signal-${signalPoint}-`,
          currentHTTP,
          currentHTTPS,
        );
        writeFileSync(fixture.backupHTTPPath, savedHTTP, { mode: 0o600 });
        writeFileSync(fixture.backupHTTPSPath, savedHTTPS, { mode: 0o600 });

        const result = spawnSync(
          "bash",
          [
            "-c",
            buildMacOSStartProxyCommand(
              "8081",
              "Wi-Fi",
              "recorder.example.com",
            ),
          ],
          {
            encoding: "utf8",
            env: {
              ...fixture.environment,
              SIGNAL_MUTATION_NUMBER: String(signalPoint),
            },
          },
        );

        expect(result.status).not.toBe(0);
        expect(result.stderr).toContain("proxy update was interrupted");
        expect(result.stderr).toContain(
          "The complete previous proxy state was restored",
        );
        expect(readFileSync(fixture.currentHTTPPath, "utf8")).toBe(currentHTTP);
        expect(readFileSync(fixture.currentHTTPSPath, "utf8")).toBe(
          currentHTTPS,
        );
        expect(readFileSync(fixture.backupHTTPPath, "utf8")).toBe(savedHTTP);
        expect(readFileSync(fixture.backupHTTPSPath, "utf8")).toBe(savedHTTPS);
      }
    },
    20_000,
  );

  itOnMacOS(
    "rolls back macOS restore when TERM interrupts an in-flight setter",
    () => {
      const currentHTTP = proxyState({
        enabled: "Yes",
        server: "localhost",
        port: "8081",
      });
      const currentHTTPS = proxyState({
        enabled: "Yes",
        server: "localhost",
        port: "8081",
      });
      const savedHTTP = proxyState();
      const savedHTTPS = proxyState({
        enabled: "Yes",
        server: "saved-https.example.com",
        port: "8002",
      });

      for (const signalPoint of [2, 4]) {
        const fixture = createTransactionalMacOSFixture(
          `apexload-proxy-restore-signal-${signalPoint}-`,
          currentHTTP,
          currentHTTPS,
        );
        writeFileSync(fixture.backupHTTPPath, savedHTTP, { mode: 0o600 });
        writeFileSync(fixture.backupHTTPSPath, savedHTTPS, { mode: 0o600 });

        const result = spawnSync(
          "bash",
          ["-c", buildMacOSRestoreProxyCommand("Wi-Fi")],
          {
            encoding: "utf8",
            env: {
              ...fixture.environment,
              SIGNAL_MUTATION_NUMBER: String(signalPoint),
            },
          },
        );

        expect(result.status).not.toBe(0);
        expect(result.stderr).toContain("proxy restore was interrupted");
        expect(result.stderr).toContain(
          "The complete pre-restore proxy state was restored",
        );
        expect(readFileSync(fixture.currentHTTPPath, "utf8")).toBe(currentHTTP);
        expect(readFileSync(fixture.currentHTTPSPath, "utf8")).toBe(
          currentHTTPS,
        );
        expect(readFileSync(fixture.backupHTTPPath, "utf8")).toBe(savedHTTP);
        expect(readFileSync(fixture.backupHTTPSPath, "utf8")).toBe(savedHTTPS);
      }
    },
    20_000,
  );

  itOnMacOS(
    "rolls back macOS restore when post-restore verification fails",
    () => {
      const currentHTTP = proxyState({
        enabled: "Yes",
        server: "localhost",
        port: "8081",
      });
      const currentHTTPS = proxyState({
        enabled: "Yes",
        server: "localhost",
        port: "8081",
      });
      const savedHTTP = proxyState();
      const savedHTTPS = proxyState({
        enabled: "Yes",
        server: "saved-https.example.com",
        port: "8002",
      });
      const fixture = createTransactionalMacOSFixture(
        "apexload-proxy-restore-verify-rollback-",
        currentHTTP,
        currentHTTPS,
      );
      writeFileSync(fixture.backupHTTPPath, savedHTTP, { mode: 0o600 });
      writeFileSync(fixture.backupHTTPSPath, savedHTTPS, { mode: 0o600 });

      const result = spawnSync(
        "bash",
        ["-c", buildMacOSRestoreProxyCommand("Wi-Fi")],
        {
          encoding: "utf8",
          env: { ...fixture.environment, CORRUPT_VERIFY: "1" },
        },
      );

      expect(result.status).not.toBe(0);
      expect(result.stderr).toContain(
        "The complete pre-restore proxy state was restored",
      );
      expect(readFileSync(fixture.currentHTTPPath, "utf8")).toBe(currentHTTP);
      expect(readFileSync(fixture.currentHTTPSPath, "utf8")).toBe(currentHTTPS);
      expect(readFileSync(fixture.backupHTTPPath, "utf8")).toBe(savedHTTP);
      expect(readFileSync(fixture.backupHTTPSPath, "utf8")).toBe(savedHTTPS);
    },
  );

  itOnMacOS(
    "restores after an authenticated APEX proxy start using the active token",
    () => {
      const originalHTTP = proxyState({
        server: "original-http.example.com",
        port: "8001",
      });
      const originalHTTPS = proxyState({
        server: "original-https.example.com",
        port: "8002",
      });
      const fixture = createTransactionalMacOSFixture(
        "apexload-authenticated-start-restore-",
        originalHTTP,
        originalHTTPS,
      );
      const proxyPassword = "active-recorder-token";

      execFileSync(
        "bash",
        [
          "-c",
          buildMacOSStartProxyCommand(
            "8081",
            "Wi-Fi",
            "recorder.example.com",
            proxyPassword,
          ),
        ],
        { env: fixture.environment },
      );

      const authenticatedState = proxyState({
        authenticated: "1",
        enabled: "Yes",
        server: "recorder.example.com",
        port: "8081",
      });
      expect(readFileSync(fixture.currentHTTPPath, "utf8")).toBe(
        authenticatedState,
      );
      expect(readFileSync(fixture.currentHTTPSPath, "utf8")).toBe(
        authenticatedState,
      );

      execFileSync(
        "bash",
        [
          "-c",
          buildMacOSRestoreProxyCommand(
            "Wi-Fi",
            "recorder.example.com",
            "8081",
            proxyPassword,
          ),
        ],
        { env: fixture.environment },
      );

      expect(readFileSync(fixture.currentHTTPPath, "utf8")).toBe(originalHTTP);
      expect(readFileSync(fixture.currentHTTPSPath, "utf8")).toBe(
        originalHTTPS,
      );
      expect(existsSync(fixture.backupHTTPPath)).toBe(false);
      expect(existsSync(fixture.backupHTTPSPath)).toBe(false);
    },
    20_000,
  );

  itOnMacOS(
    "rejects an unrelated authenticated current proxy before restore mutation",
    () => {
      const unrelatedAuthenticated = proxyState({
        authenticated: "1",
        enabled: "Yes",
        server: "unrelated.example.com",
        port: "8888",
      });
      const fixture = createTransactionalMacOSFixture(
        "apexload-unrelated-authenticated-restore-",
        unrelatedAuthenticated,
        unrelatedAuthenticated,
      );
      const savedHTTP = proxyState();
      const savedHTTPS = proxyState();
      writeFileSync(fixture.backupHTTPPath, savedHTTP, { mode: 0o600 });
      writeFileSync(fixture.backupHTTPSPath, savedHTTPS, { mode: 0o600 });

      const result = spawnSync(
        "bash",
        [
          "-c",
          buildMacOSRestoreProxyCommand(
            "Wi-Fi",
            "recorder.example.com",
            "8081",
            "active-recorder-token",
          ),
        ],
        { encoding: "utf8", env: fixture.environment },
      );

      expect(result.status).not.toBe(0);
      expect(result.stderr).toContain(
        "does not exactly match the active APEX Load proxy endpoint",
      );
      expect(readFileSync(fixture.currentHTTPPath, "utf8")).toBe(
        unrelatedAuthenticated,
      );
      expect(readFileSync(fixture.currentHTTPSPath, "utf8")).toBe(
        unrelatedAuthenticated,
      );
      expect(readFileSync(fixture.backupHTTPPath, "utf8")).toBe(savedHTTP);
      expect(readFileSync(fixture.backupHTTPSPath, "utf8")).toBe(savedHTTPS);
    },
  );

  itOnMacOS(
    "rejects an unrelated unauthenticated current proxy before restore mutation",
    () => {
      const unrelatedProxy = proxyState({
        enabled: "Yes",
        server: "corporate-proxy.example.com",
        port: "8888",
      });
      const fixture = createTransactionalMacOSFixture(
        "apexload-unrelated-unauthenticated-restore-",
        unrelatedProxy,
        unrelatedProxy,
      );
      const savedHTTP = proxyState();
      const savedHTTPS = proxyState();
      writeFileSync(fixture.backupHTTPPath, savedHTTP, { mode: 0o600 });
      writeFileSync(fixture.backupHTTPSPath, savedHTTPS, { mode: 0o600 });

      const result = spawnSync(
        "bash",
        ["-c", buildMacOSRestoreProxyCommand("Wi-Fi")],
        { encoding: "utf8", env: fixture.environment },
      );

      expect(result.status).not.toBe(0);
      expect(result.stderr).toContain(
        "does not exactly match the active APEX Load proxy endpoint",
      );
      expect(readFileSync(fixture.currentHTTPPath, "utf8")).toBe(
        unrelatedProxy,
      );
      expect(readFileSync(fixture.currentHTTPSPath, "utf8")).toBe(
        unrelatedProxy,
      );
      expect(readFileSync(fixture.backupHTTPPath, "utf8")).toBe(savedHTTP);
      expect(readFileSync(fixture.backupHTTPSPath, "utf8")).toBe(savedHTTPS);
    },
  );

  itOnMacOS(
    "ignores an unlocked stale lock file but excludes a live macOS proxy command",
    async () => {
      const homeDirectory = mkdtempSync(join(tmpdir(), "apexload-proxy-lock-"));
      const stateDirectory = join(
        homeDirectory,
        "Library",
        "Application Support",
        "APEX Load",
        "recorder-proxy-state",
      );
      const fakeBin = join(homeDirectory, "bin");
      mkdirSync(stateDirectory, { recursive: true });
      mkdirSync(fakeBin, { recursive: true });
      writeFileSync(
        join(fakeBin, "networksetup"),
        [
          "#!/bin/sh",
          'case "$1" in',
          "  -getwebproxy|-getsecurewebproxy)",
          "    printf '%s\\n' 'Enabled: No' 'Server: ' 'Port: 0' 'Authenticated Proxy Enabled: 0'",
          "    ;;",
          "esac",
        ].join("\n"),
        { mode: 0o755 },
      );

      const stateKey = createHash("sha256").update("Wi-Fi").digest("hex");
      const lockFile = join(stateDirectory, `${stateKey}.lock`);
      const readyFile = join(homeDirectory, "lock-ready");
      const command = buildMacOSStartProxyCommand("8081", "Wi-Fi");
      const environment = {
        ...process.env,
        HOME: homeDirectory,
        PATH: `${fakeBin}:${process.env.PATH ?? ""}`,
      };

      // A file left behind by a killed process is not ownership. The kernel
      // advisory lock is what excludes another command.
      writeFileSync(lockFile, "stale lock file\n", { mode: 0o600 });
      execFileSync("bash", ["-c", command], { env: environment });
      expect(existsSync(lockFile)).toBe(true);

      const holder = spawn(
        "bash",
        [
          "-c",
          'exec 9>"$1"; /usr/bin/lockf -s -t 0 9 || exit 2; : > "$2"; exec /bin/sleep 30',
          "apexload-lock-holder",
          lockFile,
          readyFile,
        ],
        { stdio: "ignore" },
      );
      try {
        for (
          let attempt = 0;
          attempt < 100 && !existsSync(readyFile);
          attempt += 1
        ) {
          await new Promise((resolve) => setTimeout(resolve, 10));
        }
        expect(existsSync(readyFile)).toBe(true);

        const blocked = spawnSync("bash", ["-c", command], {
          encoding: "utf8",
          env: environment,
        });
        expect(blocked.status).not.toBe(0);
        expect(blocked.stderr).toContain(
          "Another APEX Load proxy command is running",
        );
      } finally {
        holder.kill("SIGKILL");
        await new Promise((resolve) => holder.once("close", resolve));
      }

      // SIGKILL releases the advisory lock even though the lock file remains.
      // This fixture's fake setters do not persist an active proxy endpoint, so
      // clear the first command's backups to model another initial start.
      rmSync(join(stateDirectory, `${stateKey}.http`), { force: true });
      rmSync(join(stateDirectory, `${stateKey}.https`), { force: true });
      execFileSync("bash", ["-c", command], { env: environment });
    },
  );
});
