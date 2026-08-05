// Thin launcher: spawns the pre-built linux/amd64 controller binary and
// maps App Service's $PORT to APEXLOAD_ADDR so the Go process owns the socket.
// No npm dependencies — this file is the only runtime code.
"use strict";

const { spawn } = require("child_process");
const fs = require("fs");
const os = require("os");
const path = require("path");

// --- binary resolution --------------------------------------------------

const binPath =
  process.env.APEXLOAD_LAUNCHER_BINARY ||
  path.join(__dirname, "bin", "controller");

if (!fs.existsSync(binPath)) {
  console.error(
    `[launcher] binary not found: ${binPath}\n` +
      `  Build it with: CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o deployments/azure/appservice/bin/controller ./cmd/controller`
  );
  process.exit(1);
}

// On Linux verify ELF magic and x86-64 machine type before exec so that
// a wrong-arch build gives a clear error rather than a cryptic ENOEXEC.
if (process.platform === "linux") {
  const fd = fs.openSync(binPath, "r");
  const header = Buffer.alloc(20);
  fs.readSync(fd, header, 0, 20, 0);
  fs.closeSync(fd);
  const isElf = header[0] === 0x7f && header.slice(1, 4).toString() === "ELF";
  const isX64 = header.readUInt16LE(18) === 0x003e; // e_machine EM_X86_64
  if (!isElf || !isX64) {
    console.error(
      `[launcher] binary at ${binPath} is not a linux/amd64 ELF executable.\n` +
        `  Rebuild with: CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o deployments/azure/appservice/bin/controller ./cmd/controller`
    );
    process.exit(1);
  }
}

// --- env assembly -------------------------------------------------------

let env = Object.assign({}, process.env);

// App Service assigns the listen port via $PORT; controller reads APEXLOAD_ADDR.
if (env.PORT) {
  env.APEXLOAD_ADDR = ":" + env.PORT;
}

// App Service exposes a single HTTP port; suppress the optional metrics
// listener so the controller doesn't try to bind a second port.
delete env.APEXLOAD_METRICS_ADDR;

// Materialize any PEM blobs delivered as app settings (Key Vault refs) into
// temp files.  The gRPC transport only accepts file paths, not inline PEM.
env = materializePem(env);

// --- spawn --------------------------------------------------------------

const child = spawn(binPath, [], { env, stdio: "inherit" });

child.on("error", (err) => {
  console.error(`[launcher] failed to start controller: ${err.message}`);
  process.exit(1);
});

child.on("exit", (code, signal) => {
  if (signal) {
    const sigNum = require("os").constants.signals[signal] ?? 0;
    process.exit(128 + sigNum);
  }
  process.exit(code ?? 1);
});

// Forward termination signals to the child; the controller handles them
// gracefully (signal.NotifyContext in cmd/controller/main.go:64).
for (const sig of ["SIGTERM", "SIGINT"]) {
  process.on(sig, () => child.kill(sig));
}

// --- PEM materialization ------------------------------------------------

function materializePem(env) {
  const pairs = [
    ["APEXLOAD_GRPC_TLS_CA_PEM", "APEXLOAD_GRPC_TLS_CA"],
    ["APEXLOAD_GRPC_TLS_CERT_PEM", "APEXLOAD_GRPC_TLS_CERT"],
    ["APEXLOAD_GRPC_TLS_KEY_PEM", "APEXLOAD_GRPC_TLS_KEY"],
  ];
  const out = Object.assign({}, env);
  for (const [pemVar, fileVar] of pairs) {
    const pem = env[pemVar];
    if (!pem) continue;
    const dest = path.join(os.tmpdir(), `apexload-${pemVar.toLowerCase()}.pem`);
    fs.writeFileSync(dest, pem, { mode: 0o600 });
    out[fileVar] = dest;
    delete out[pemVar];
  }
  return out;
}
