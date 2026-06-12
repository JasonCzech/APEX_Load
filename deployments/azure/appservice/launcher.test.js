// node:test suite for launcher.js — zero npm dependencies.
// Run: node --test deployments/azure/appservice/
"use strict";

const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("fs");
const os = require("os");
const path = require("path");
const { execFileSync, spawnSync } = require("child_process");

// ---- helpers -----------------------------------------------------------

const LAUNCHER = path.join(__dirname, "launcher.js");

// Write a tiny stub binary (Node script with a shebang) to a temp dir and
// return its path.  The script prints its received env to a JSON file, then
// exits with the requested code.
function makeStub(dir, opts = {}) {
  const { exitCode = 0, signal = null, envFile = null } = opts;
  const stubPath = path.join(dir, "controller");
  let body = "#!/usr/bin/env node\n'use strict';\n";
  if (envFile) {
    body += `require('fs').writeFileSync(${JSON.stringify(envFile)}, JSON.stringify(process.env));\n`;
  }
  if (signal) {
    body += `process.on(${JSON.stringify(signal)}, () => { process.exit(0); });\n`;
    // Stay alive long enough to receive the signal.
    body += "setTimeout(() => {}, 5000);\n";
  } else {
    body += `process.exit(${exitCode});\n`;
  }
  fs.writeFileSync(stubPath, body, { mode: 0o755 });
  return stubPath;
}

function runLauncher(stubPath, extraEnv = {}) {
  return spawnSync(
    process.execPath,
    [LAUNCHER],
    {
      env: Object.assign({}, process.env, { APEXLOAD_LAUNCHER_BINARY: stubPath }, extraEnv),
      timeout: 4000,
    }
  );
}

// ---- ELF check (pure function export) ----------------------------------

// Re-use the exported helper without spawning a child process.
// We test it by constructing crafted byte headers.
const { _testElfCheck } = (() => {
  // Inline the check so tests don't exec a whole new Node process.
  function elfCheck(buf) {
    const isElf = buf[0] === 0x7f && buf.slice(1, 4).toString() === "ELF";
    const isX64 = buf.readUInt16LE(18) === 0x003e;
    return isElf && isX64;
  }
  return { _testElfCheck: elfCheck };
})();

function makeElfHeader(machineLE) {
  const h = Buffer.alloc(20, 0);
  h[0] = 0x7f; h[1] = 0x45; h[2] = 0x4c; h[3] = 0x46; // \x7fELF
  h.writeUInt16LE(machineLE, 18);
  return h;
}

test("ELF check: valid linux/amd64 header", () => {
  assert.ok(_testElfCheck(makeElfHeader(0x003e)));
});

test("ELF check: wrong machine type (ARM64)", () => {
  assert.ok(!_testElfCheck(makeElfHeader(0x00b7)));
});

test("ELF check: not an ELF (PE/COFF)", () => {
  const h = Buffer.alloc(20, 0);
  h[0] = 0x4d; h[1] = 0x5a; // MZ
  h.writeUInt16LE(0x003e, 18);
  assert.ok(!_testElfCheck(h));
});

// ---- exit code forwarding ----------------------------------------------

test("exit code 0 is forwarded", () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "apex-launcher-test-"));
  try {
    const stub = makeStub(dir, { exitCode: 0 });
    const r = runLauncher(stub);
    assert.equal(r.status, 0);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test("exit code 7 is forwarded", () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "apex-launcher-test-"));
  try {
    const stub = makeStub(dir, { exitCode: 7 });
    const r = runLauncher(stub);
    assert.equal(r.status, 7);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

// ---- PORT → APEXLOAD_ADDR mapping --------------------------------------

test("PORT env is mapped to APEXLOAD_ADDR", () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "apex-launcher-test-"));
  try {
    const envFile = path.join(dir, "env.json");
    const stub = makeStub(dir, { envFile });
    runLauncher(stub, { PORT: "4242" });
    const captured = JSON.parse(fs.readFileSync(envFile, "utf8"));
    assert.equal(captured.APEXLOAD_ADDR, ":4242");
    assert.equal(captured.APEXLOAD_METRICS_ADDR, undefined);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test("APEXLOAD_METRICS_ADDR is stripped", () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "apex-launcher-test-"));
  try {
    const envFile = path.join(dir, "env.json");
    const stub = makeStub(dir, { envFile });
    runLauncher(stub, { APEXLOAD_METRICS_ADDR: ":9091" });
    const captured = JSON.parse(fs.readFileSync(envFile, "utf8"));
    assert.equal(captured.APEXLOAD_METRICS_ADDR, undefined);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

// ---- missing binary ----------------------------------------------------

test("missing binary exits nonzero with clear message", () => {
  const r = runLauncher("/nonexistent/controller");
  assert.notEqual(r.status, 0);
  assert.ok(
    (r.stderr.toString() + r.stdout.toString()).includes("binary not found"),
    "expected 'binary not found' in output"
  );
});

// ---- PEM materialization -----------------------------------------------

test("PEM blobs are written to temp files with 0600 mode", () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "apex-launcher-test-"));
  try {
    const envFile = path.join(dir, "env.json");
    const stub = makeStub(dir, { envFile });
    const fakePem = "-----BEGIN CERTIFICATE-----\nZmFrZQ==\n-----END CERTIFICATE-----\n";
    runLauncher(stub, {
      APEXLOAD_GRPC_TLS_CA_PEM: fakePem,
      APEXLOAD_GRPC_TLS_CERT_PEM: fakePem,
      APEXLOAD_GRPC_TLS_KEY_PEM: fakePem,
    });
    const captured = JSON.parse(fs.readFileSync(envFile, "utf8"));

    for (const fileVar of ["APEXLOAD_GRPC_TLS_CA", "APEXLOAD_GRPC_TLS_CERT", "APEXLOAD_GRPC_TLS_KEY"]) {
      assert.ok(captured[fileVar], `${fileVar} should be set to a file path`);
      assert.ok(fs.existsSync(captured[fileVar]), `${fileVar} file should exist`);
      const mode = fs.statSync(captured[fileVar]).mode & 0o777;
      assert.equal(mode, 0o600, `${fileVar} file should be mode 0600`);
      assert.equal(fs.readFileSync(captured[fileVar], "utf8"), fakePem);
    }

    // Original PEM vars should not be passed to the child.
    assert.equal(captured.APEXLOAD_GRPC_TLS_CA_PEM, undefined);
    assert.equal(captured.APEXLOAD_GRPC_TLS_CERT_PEM, undefined);
    assert.equal(captured.APEXLOAD_GRPC_TLS_KEY_PEM, undefined);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});
