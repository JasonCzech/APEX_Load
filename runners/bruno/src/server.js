'use strict';

const crypto = require('node:crypto');
const fs = require('node:fs');
const fsp = require('node:fs/promises');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');
const { Worker } = require('node:worker_threads');
const tar = require('tar-stream');

const COMPATIBILITY_VERSION = '3.5.2';
const RUNNER_VERSION = 'apexload-bruno-runner.v1';
const DEFAULT_MAX_BUNDLE_BYTES = 4 * 1024 * 1024 * 1024;
const DEFAULT_MAX_FILES = 100000;
const DEFAULT_MAX_PENDING_EVENTS = 10000;
const DEFAULT_MAX_SESSIONS = 256;
const DEFAULT_PENDING_DRAIN_TIMEOUT_MS = 5000;
const DEFAULT_WORKER_TERMINATION_TIMEOUT_MS = 5000;
const DEFAULT_WORKER_PROVISION_TIMEOUT_MS = 30000;
const DEFAULT_MAX_OUTPUT_BYTES = 4 * 1024 * 1024;
const MAX_OUTPUT_BYTES = 1024 * 1024 * 1024;
const DEFAULT_SESSION_TTL_MS = 24 * 60 * 60 * 1000;
const DEFAULT_SESSION_IDLE_TTL_MS = 30 * 60 * 1000;
const DEFAULT_BUNDLE_TTL_MS = 24 * 60 * 60 * 1000;
const DEFAULT_BUNDLE_IDLE_TTL_MS = 30 * 60 * 1000;
const DEFAULT_STALE_ROOT_TTL_MS = 24 * 60 * 60 * 1000;
const RUNNER_ROOT_PREFIX = 'apexload-bruno-runner-';
const RUNNER_OWNER_FILE = '.apexload-owner.json';
const MAX_RUNNER_OWNER_BYTES = 16 * 1024;

function positiveInteger(value, fallback, name) {
  const parsed = Number(value ?? fallback);
  if (!Number.isSafeInteger(parsed) || parsed <= 0) {
    throw new Error(`${name} must be a positive integer`);
  }
  return parsed;
}

function waitForDrainOrClose(stream, timeoutMS = DEFAULT_PENDING_DRAIN_TIMEOUT_MS) {
  if (stream.destroyed || stream.writableEnded) return Promise.resolve(true);
	return new Promise((resolve) => {
		let timer;
		const finish = (drained) => {
			stream.removeListener('drain', onDrain);
			stream.removeListener('close', closed);
			stream.removeListener('error', closed);
			clearTimeout(timer);
			resolve(drained);
		};
		const onDrain = () => finish(true);
		const closed = () => finish(true);
		stream.once('drain', onDrain);
		stream.once('close', closed);
		stream.once('error', closed);
		timer = setTimeout(() => finish(false), timeoutMS);
	});
}

async function finishPendingSession(session, error, timeoutMS = DEFAULT_PENDING_DRAIN_TIMEOUT_MS) {
  const pending = Array.from(session?.pending?.entries?.() || []);
  const completions = [];
  for (const [requestID, entry] of pending) {
    const handler = typeof entry === 'function' ? entry : entry?.handle;
    if (typeof handler !== 'function') continue;
    try {
      completions.push(Promise.resolve(handler({ type: 'error', requestID, error })));
    } catch {}
  }
  if (completions.length === 0) return true;
  let timer;
  const settled = await Promise.race([
    Promise.allSettled(completions).then(() => true),
    new Promise((resolve) => { timer = setTimeout(() => resolve(false), timeoutMS); })
  ]).finally(() => clearTimeout(timer));
  if (settled) return true;
  // A handler can be queued behind an arbitrary number of blocked writes (or
  // be faulty and never settle). Abort every associated response chain before
  // proceeding to worker termination; unresolved promises are never awaited a
  // second time and therefore cannot hold session deletion or runner shutdown.
  for (const [, entry] of pending) {
    if (typeof entry?.abort !== 'function') continue;
    try { entry.abort(); } catch {}
  }
  return false;
}

function sessionOutputLimit(payload) {
  const value = Number(payload?.limits?.max_output_bytes ?? DEFAULT_MAX_OUTPUT_BYTES);
  if (!Number.isSafeInteger(value) || value <= 0 || value > MAX_OUTPUT_BYTES) {
    const error = new Error(`Bruno max_output_bytes must be between 1 and ${MAX_OUTPUT_BYTES}`);
    error.statusCode = 422;
    throw error;
  }
  return value;
}

function workerResourceLimits(payload) {
  const outputMiB = Math.ceil(sessionOutputLimit(payload) / (1024 * 1024));
  // Keep the operator-selected output envelope intact. The extra generation
  // headroom covers parsed response objects, scripts, and collection state; it
  // remains a finite per-worker ceiling even when an operator raises output.
  return {
    maxOldGenerationSizeMb: Math.max(128, outputMiB * 2 + 128),
    maxYoungGenerationSizeMb: Math.max(16, Math.min(64, Math.ceil(outputMiB / 4) + 16)),
    stackSizeMb: 8
  };
}

async function terminateWorkerBounded(worker, timeoutMS = DEFAULT_WORKER_TERMINATION_TIMEOUT_MS) {
  if (!worker || typeof worker.terminate !== 'function') return true;
  let timer;
  const termination = Promise.resolve()
    .then(() => worker.terminate())
    // A rejected termination is not proof that the worker stopped. Treat it
    // exactly like a timeout so callers poison the runner and stop accepting
    // work until the process can be restarted cleanly.
    .then(() => true, () => false);
  const completed = await Promise.race([
    termination,
    new Promise((resolve) => {
      timer = setTimeout(() => resolve(false), timeoutMS);
    })
  ]).finally(() => clearTimeout(timer));
  // Keep a rejection handler attached to a late termination without awaiting it
  // again. Callers fail the runner closed when this returns false.
  void termination.catch(() => {});
  return completed;
}

async function provisionWorkerBounded(
  factory,
  signal,
  provisionTimeoutMS = DEFAULT_WORKER_PROVISION_TIMEOUT_MS,
  terminationTimeoutMS = DEFAULT_WORKER_TERMINATION_TIMEOUT_MS,
  terminationFailed = () => {}
) {
  const pending = Promise.resolve().then(factory);
  let timer;
  let interrupted = false;
  let abortHandler;
  const interruption = new Promise((_, reject) => {
    const stop = (message, statusCode) => {
      if (interrupted) return;
      interrupted = true;
      const error = new Error(message);
      error.statusCode = statusCode;
      reject(error);
    };
    abortHandler = () => stop('session creation was cancelled before the worker became ready', 409);
    if (signal?.aborted) abortHandler();
    else signal?.addEventListener?.('abort', abortHandler, { once: true });
    timer = setTimeout(() => stop('Bruno worker provisioning exceeded its deadline', 504), provisionTimeoutMS);
  });
  try {
    return await Promise.race([pending, interruption]);
  } catch (error) {
    if (interrupted) {
      // A non-cooperative factory may still return a worker after its caller was
      // cancelled. Dispose that late worker independently; never reattach it to
      // released capacity or await it from the request/shutdown path.
      void pending.then(async (worker) => {
        if (!await terminateWorkerBounded(worker, terminationTimeoutMS)) terminationFailed();
      }, () => {});
    }
    throw error;
  } finally {
    clearTimeout(timer);
    signal?.removeEventListener?.('abort', abortHandler);
  }
}

async function destroySessionResources(
  session,
  terminalError,
  timeoutMS,
  release,
  terminationTimeoutMS = DEFAULT_WORKER_TERMINATION_TIMEOUT_MS,
  terminationFailed = () => {}
) {
  session.expiring = true;
  session.provisionAbort?.abort();
  if (session.creating && typeof session.readyReject === 'function') {
    session.readyReject(new Error(terminalError));
  }
  if (session.expiryTimer) clearTimeout(session.expiryTimer);
  session.expiryTimer = null;
  try { session.worker?.postMessage({ type: 'cancel' }); } catch {}
  try {
    await finishPendingSession(session, terminalError, timeoutMS);
    session.closed = true;
    if (!await terminateWorkerBounded(session.worker, terminationTimeoutMS)) {
      terminationFailed();
    }
  } finally {
    session.closed = true;
    release(session);
  }
}

function json(response, status, value) {
  const body = Buffer.from(JSON.stringify(value));
  response.writeHead(status, {
    'content-type': 'application/json',
    'content-length': body.length
  });
  response.end(body);
}

function empty(response, status) {
  response.writeHead(status);
  response.end();
}

function safeID(prefix) {
  return `${prefix}_${crypto.randomUUID().replaceAll('-', '')}`;
}

function processIsAlive(pid) {
  if (!Number.isSafeInteger(pid) || pid <= 0) return false;
  try {
    process.kill(pid, 0);
    return true;
  } catch (error) {
    return error?.code === 'EPERM';
  }
}

// Remove only roots whose owner heartbeat is stale and whose recorded process
// is no longer alive. Symlinks, active siblings, unowned legacy roots, and
// unexpected directory names are deliberately preserved.
function sweepStaleRunnerRoots(parent, options = {}) {
  const staleRootTTLMS = positiveInteger(
    options.staleRootTTLMS ?? process.env.APEXLOAD_BRUNO_STALE_ROOT_TTL_MS,
    DEFAULT_STALE_ROOT_TTL_MS,
    'Bruno stale runner root TTL milliseconds'
  );
  const nowMS = Number.isFinite(options.nowMS) ? Number(options.nowMS) : Date.now();
  const alive = typeof options.processAlive === 'function' ? options.processAlive : processIsAlive;
  let entries;
  try {
    entries = fs.readdirSync(parent, { withFileTypes: true });
  } catch {
    return [];
  }
  const removed = [];
  for (const entry of entries) {
    if (!entry.name.startsWith(RUNNER_ROOT_PREFIX) || entry.isSymbolicLink() || !entry.isDirectory()) continue;
    const candidate = path.join(parent, entry.name);
    let rootStat;
    try {
      rootStat = fs.lstatSync(candidate);
    } catch {
      continue;
    }
    if (!rootStat.isDirectory() || rootStat.isSymbolicLink()) continue;
    const currentUID = process.getuid?.();
    if (Number.isSafeInteger(currentUID) && Number.isSafeInteger(rootStat.uid) && rootStat.uid !== currentUID) continue;
    let ownerPID = 0;
    let heartbeatMS = rootStat.mtimeMs;
    const ownerPath = path.join(candidate, RUNNER_OWNER_FILE);
    try {
      const ownerStat = fs.lstatSync(ownerPath);
      if (!ownerStat.isFile() || ownerStat.isSymbolicLink() || ownerStat.size > MAX_RUNNER_OWNER_BYTES) continue;
      heartbeatMS = ownerStat.mtimeMs;
      const owner = JSON.parse(fs.readFileSync(ownerPath, 'utf8'));
      ownerPID = Number(owner.pid || 0);
      if (!Number.isSafeInteger(ownerPID) || ownerPID <= 0) continue;
    } catch {
      // A root without a trustworthy owner record may still belong to an
      // older active runner. Preserve it rather than guessing ownership.
      continue;
    }
    if (alive(ownerPID) || nowMS - heartbeatMS < staleRootTTLMS) continue;
    try {
      fs.rmSync(candidate, { recursive: true, force: true });
      removed.push(candidate);
    } catch {}
  }
  return removed;
}

function validRelativePath(value) {
  if (typeof value !== 'string' || value.length === 0 || value.includes('\0') || /[\x00-\x1f\x7f]/.test(value)) {
    return null;
  }
  const normalized = value.replaceAll('\\', '/');
  if (normalized.startsWith('/') || /^[A-Za-z]:\//.test(normalized)) return null;
  const clean = path.posix.normalize(normalized);
  if (clean === '.' || clean === '..' || clean.startsWith('../')) return null;
  return clean;
}

async function readBody(request, maximum) {
  const chunks = [];
  let total = 0;
  for await (const chunk of request) {
    total += chunk.length;
    if (total > maximum) throw new Error(`request body exceeds ${maximum} bytes`);
    chunks.push(chunk);
  }
  return Buffer.concat(chunks);
}

async function spoolRequest(request, filename, maximum) {
  const declared = Number(request.headers['content-length'] || 0);
  if (Number.isFinite(declared) && declared > maximum) {
    const error = new Error(`request body exceeds ${maximum} bytes`);
    error.statusCode = 413;
    throw error;
  }
  const hash = crypto.createHash('sha256');
  const handle = await fsp.open(filename, 'wx', 0o600);
  let total = 0;
  let completed = false;
  try {
    for await (const chunk of request) {
      total += chunk.length;
      if (total > maximum) {
        const error = new Error(`request body exceeds ${maximum} bytes`);
        error.statusCode = 413;
        throw error;
      }
      hash.update(chunk);
      await handle.write(chunk);
    }
    completed = true;
    return { sha256: hash.digest('hex'), sizeBytes: total };
  } finally {
    await handle.close();
    if (!completed) await fsp.rm(filename, { force: true });
  }
}

async function extractBundle(data, destination, limits = {}) {
  const maxFiles = positiveInteger(
    limits.maxFiles ?? process.env.APEXLOAD_BRUNO_MAX_FILES,
    DEFAULT_MAX_FILES,
    'Bruno maximum file count'
  );
  const maxBytes = positiveInteger(
    limits.maxBytes ?? process.env.APEXLOAD_BRUNO_MAX_BUNDLE_BYTES,
    DEFAULT_MAX_BUNDLE_BYTES,
    'Bruno maximum bundle bytes'
  );
  const extract = tar.extract();
  const seen = new Map();
  let files = 0;
  let total = 0;
  const pending = [];

  const completion = new Promise((resolve, reject) => {
    const abort = (error) => {
      if (!extract.destroyed) extract.destroy();
      reject(error);
    };
    extract.on('entry', (header, stream, next) => {
      const clean = validRelativePath(header.name);
      if (!clean) {
        stream.resume();
        abort(new Error(`unsafe Bruno archive path ${JSON.stringify(header.name)}`));
        return;
      }
      if (header.type !== 'file') {
        stream.resume();
        abort(new Error(`Bruno archive entry ${JSON.stringify(clean)} is not a regular file`));
        return;
      }
      const folded = clean.normalize('NFC').toLocaleLowerCase('en-US');
      if (seen.has(folded)) {
        stream.resume();
        abort(new Error(`Bruno archive paths ${JSON.stringify(seen.get(folded))} and ${JSON.stringify(clean)} collide`));
        return;
      }
      seen.set(folded, clean);
      files += 1;
      total += Number(header.size || 0);
      if (files > maxFiles || total > maxBytes) {
        stream.resume();
        const error = new Error('Bruno archive exceeds configured extraction limits');
        error.statusCode = 413;
        abort(error);
        return;
      }
      const target = path.join(destination, ...clean.split('/'));
      const task = (async () => {
        await fsp.mkdir(path.dirname(target), { recursive: true, mode: 0o700 });
        const handle = await fsp.open(target, 'wx', Number(header.mode || 0o600) & 0o700);
        try {
          for await (const chunk of stream) {
            await handle.write(chunk);
          }
        } finally {
          await handle.close();
        }
      })().then(next, abort);
      pending.push(task);
    });
    extract.on('finish', async () => {
      try {
        await Promise.all(pending);
        resolve();
      } catch (error) {
        reject(error);
      }
    });
    extract.on('error', reject);
  });
  if (Buffer.isBuffer(data) || data instanceof Uint8Array) {
    extract.end(data);
  } else if (data && typeof data.pipe === 'function') {
    data.once('error', (error) => extract.destroy(error));
    data.pipe(extract);
  } else {
    throw new TypeError('Bruno bundle input must be a Buffer or readable stream');
  }
  await completion;
  return { files, totalBytes: total };
}

async function walk(root) {
  const out = [];
  async function visit(current) {
    const entries = await fsp.readdir(current, { withFileTypes: true });
    for (const entry of entries) {
      const absolute = path.join(current, entry.name);
      if (entry.isDirectory()) {
        await visit(absolute);
      } else if (entry.isFile()) {
        out.push(path.relative(root, absolute).split(path.sep).join('/'));
      }
    }
  }
  await visit(root);
  out.sort();
  return out;
}

async function detectCollectionRoot(bundleRoot) {
  const files = await walk(bundleRoot);
  const roots = files
    .filter((file) => ['opencollection.yml', 'bruno.json', 'collection.bru'].includes(path.posix.basename(file).toLowerCase()))
    .map((file) => path.posix.dirname(file) === '.' ? '' : path.posix.dirname(file))
    .sort((left, right) => left.split('/').length - right.split('/').length || left.localeCompare(right));
  if (roots.length > 0) {
    const relative = roots[0];
    const root = relative ? path.join(bundleRoot, ...relative.split('/')) : bundleRoot;
    const rootFiles = new Set(files.filter((file) => path.posix.dirname(file) === (relative || '.')).map((file) => path.posix.basename(file).toLowerCase()));
    let synthetic = false;
    if (rootFiles.has('collection.bru') && !rootFiles.has('bruno.json') && !rootFiles.has('opencollection.yml')) {
      await fsp.writeFile(path.join(root, 'bruno.json'), JSON.stringify({
        version: '1',
        name: 'Imported Bruno Collection',
        type: 'collection',
        ignore: ['node_modules', '.git']
      }, null, 2), { mode: 0o600, flag: 'wx' });
      synthetic = true;
    }
    return {
      root,
      relative,
      synthetic,
      files
    };
  }
  const requestFiles = files.filter((file) => /\.(bru|ya?ml)$/i.test(file));
  if (requestFiles.length === 0) {
    throw new Error('Bruno bundle has no collection root and is not a standalone request');
  }
  if (requestFiles.length > 1) {
    const directories = requestFiles.map((file) => path.posix.dirname(file).split('/').filter(Boolean));
    const common = [];
    for (let index = 0; ; index += 1) {
      const segment = directories[0]?.[index];
      if (!segment || !directories.every((parts) => parts[index] === segment)) break;
      common.push(segment);
    }
    const relative = common.join('/');
    const root = relative ? path.join(bundleRoot, ...common) : bundleRoot;
    await fsp.writeFile(path.join(root, 'bruno.json'), JSON.stringify({ version: '1', name: 'Imported Bruno Collection', type: 'collection' }, null, 2));
    return { root, relative, synthetic: true, files };
  }
  const requestPath = requestFiles[0];
  const requestDir = path.posix.dirname(requestPath);
  const root = requestDir === '.' ? bundleRoot : path.join(bundleRoot, ...requestDir.split('/'));
  if (/\.bru$/i.test(requestPath)) {
    await fsp.writeFile(path.join(root, 'bruno.json'), JSON.stringify({
      version: '1',
      name: path.posix.basename(requestPath, '.bru'),
      type: 'collection',
      ignore: ['node_modules', '.git']
    }, null, 2), { mode: 0o600, flag: 'wx' });
    await fsp.writeFile(path.join(root, 'collection.bru'), 'meta {\n  name: Imported Bruno Request\n}\n', { mode: 0o600, flag: 'wx' });
  } else {
    await fsp.writeFile(path.join(root, 'opencollection.yml'), 'opencollection: "1.0.0"\ninfo:\n  name: Imported Bruno Request\nextensions:\n  bruno:\n    ignore:\n      - node_modules\n      - .git\n', { mode: 0o600, flag: 'wx' });
  }
  return { root, relative: requestDir === '.' ? '' : requestDir, synthetic: true, files };
}

function createRunnerServer(options = {}) {
  const bundles = new Map();
  const sessions = new Map();
  const maxBundleBytes = positiveInteger(
    options.maxBundleBytes ?? process.env.APEXLOAD_BRUNO_MAX_BUNDLE_BYTES,
    DEFAULT_MAX_BUNDLE_BYTES,
    'Bruno maximum bundle bytes'
  );
  const maxPendingEvents = positiveInteger(
    options.maxPendingEvents ?? process.env.APEXLOAD_BRUNO_MAX_PENDING_EVENTS,
    DEFAULT_MAX_PENDING_EVENTS,
    'Bruno maximum pending events'
  );
	const maxOutputBytes = sessionOutputLimit({
		limits: {
			max_output_bytes: options.maxOutputBytes ?? process.env.APEXLOAD_BRUNO_MAX_OUTPUT_BYTES ?? DEFAULT_MAX_OUTPUT_BYTES
		}
	});
	const maxSessions = positiveInteger(
    options.maxSessions ?? process.env.APEXLOAD_BRUNO_MAX_SESSIONS,
    DEFAULT_MAX_SESSIONS,
    'Bruno maximum sessions'
	);
	const pendingDrainTimeoutMS = positiveInteger(
		options.pendingDrainTimeoutMS ?? process.env.APEXLOAD_BRUNO_PENDING_DRAIN_TIMEOUT_MS,
		DEFAULT_PENDING_DRAIN_TIMEOUT_MS,
		'Bruno pending response drain timeout milliseconds'
	);
	const workerTerminationTimeoutMS = positiveInteger(
		options.workerTerminationTimeoutMS ?? process.env.APEXLOAD_BRUNO_WORKER_TERMINATION_TIMEOUT_MS,
		DEFAULT_WORKER_TERMINATION_TIMEOUT_MS,
		'Bruno worker termination timeout milliseconds'
	);
	const workerProvisionTimeoutMS = positiveInteger(
		options.workerProvisionTimeoutMS ?? process.env.APEXLOAD_BRUNO_WORKER_PROVISION_TIMEOUT_MS,
		DEFAULT_WORKER_PROVISION_TIMEOUT_MS,
		'Bruno worker provision timeout milliseconds'
	);
  const sessionTTLMS = positiveInteger(
    options.sessionTTLMS ?? process.env.APEXLOAD_BRUNO_SESSION_TTL_MS,
    DEFAULT_SESSION_TTL_MS,
    'Bruno session TTL milliseconds'
  );
  const sessionIdleTTLMS = positiveInteger(
    options.sessionIdleTTLMS ?? process.env.APEXLOAD_BRUNO_SESSION_IDLE_TTL_MS,
    DEFAULT_SESSION_IDLE_TTL_MS,
    'Bruno session idle TTL milliseconds'
  );
  const bundleTTLMS = positiveInteger(
    options.bundleTTLMS ?? process.env.APEXLOAD_BRUNO_BUNDLE_TTL_MS,
    DEFAULT_BUNDLE_TTL_MS,
    'Bruno bundle TTL milliseconds'
  );
  const bundleIdleTTLMS = positiveInteger(
    options.bundleIdleTTLMS ?? process.env.APEXLOAD_BRUNO_BUNDLE_IDLE_TTL_MS,
    DEFAULT_BUNDLE_IDLE_TTL_MS,
    'Bruno bundle idle TTL milliseconds'
  );
  const staleRootTTLMS = positiveInteger(
    options.staleRootTTLMS ?? process.env.APEXLOAD_BRUNO_STALE_ROOT_TTL_MS,
    DEFAULT_STALE_ROOT_TTL_MS,
    'Bruno stale runner root TTL milliseconds'
  );
  const egressBrokerURL = String(options.egressBrokerURL || process.env.APEXLOAD_BRUNO_EGRESS_BROKER_URL || '').trim();
  const allowDirectEgress = options.allowDirectEgress === true || process.env.APEXLOAD_BRUNO_ALLOW_DIRECT_EGRESS === 'true';
  const developerSessionFactory = options.developerSessionFactory;
  const sessionBindingKey = crypto.randomBytes(32);
  let cleanupFailed = false;
  const markCleanupFailed = () => {
    cleanupFailed = true;
  };
  if (egressBrokerURL) {
    const parsedBroker = new URL(egressBrokerURL);
    if (parsedBroker.protocol !== 'http:' || parsedBroker.username || parsedBroker.password) {
      throw new Error('APEXLOAD_BRUNO_EGRESS_BROKER_URL must be an HTTP CONNECT proxy origin without embedded credentials');
    }
  }
  const tempParent = path.resolve(String(options.tempParent || process.env.APEXLOAD_BRUNO_TEMP_DIR || os.tmpdir()));
  fs.mkdirSync(tempParent, { recursive: true, mode: 0o700 });
  sweepStaleRunnerRoots(tempParent, { staleRootTTLMS });
  const root = options.root
    ? path.resolve(String(options.root))
    : fs.mkdtempSync(path.join(tempParent, RUNNER_ROOT_PREFIX));
  if (options.root) fs.mkdirSync(root, { recursive: false, mode: 0o700 });
  const ownerPath = path.join(root, RUNNER_OWNER_FILE);
  fs.writeFileSync(ownerPath, JSON.stringify({
    pid: process.pid,
    instance_id: crypto.randomUUID(),
    created_at: new Date().toISOString()
  }), { mode: 0o600, flag: 'wx' });
  const ownerHeartbeat = setInterval(() => {
    const now = new Date();
    try {
      fs.utimesSync(ownerPath, now, now);
    } catch {}
  }, Math.max(1000, Math.min(30000, Math.floor(staleRootTTLMS / 3))));
  ownerHeartbeat.unref?.();
  function bundleExpiryDeadline(bundle) {
    const hardDeadline = bundle.createdAt + bundleTTLMS;
    if (bundle.sessions > 0) return hardDeadline;
    return Math.min(hardDeadline, bundle.lastActiveAt + bundleIdleTTLMS);
  }

  function bundleExpired(bundle, now = Date.now()) {
    if (!bundle) return true;
    return now >= bundle.createdAt + bundleTTLMS ||
      (bundle.sessions === 0 && now >= bundle.lastActiveAt + bundleIdleTTLMS);
  }

  function scheduleBundleExpiry(bundle) {
    if (!bundle || bundle.expiring || bundles.get(bundle.id) !== bundle) return;
    if (bundle.expiryTimer) clearTimeout(bundle.expiryTimer);
    const delay = Math.max(1, bundleExpiryDeadline(bundle) - Date.now());
    bundle.expiryTimer = setTimeout(() => {
      bundle.expiryTimer = null;
      void expireBundle(bundle).catch(() => {});
    }, delay);
    bundle.expiryTimer.unref?.();
  }

  function touchBundle(bundle) {
    if (!bundle || bundle.expiring || bundles.get(bundle.id) !== bundle) return;
    bundle.lastActiveAt = Date.now();
    scheduleBundleExpiry(bundle);
  }

  async function expireBundle(bundle) {
    if (!bundle || bundle.expiring || bundles.get(bundle.id) !== bundle) return;
    if (!bundleExpired(bundle)) {
      scheduleBundleExpiry(bundle);
      return;
    }
    bundle.expiring = true;
    if (bundle.expiryTimer) clearTimeout(bundle.expiryTimer);
    bundle.expiryTimer = null;
    bundles.delete(bundle.id);
    const retainedSessions = Array.from(sessions.values()).filter((session) => session.bundleID === bundle.id);
    await Promise.allSettled(retainedSessions.map((session) => (
      destroySession(session, 'Bruno runner bundle expired')
    )));
    await fsp.rm(path.join(root, bundle.id), { recursive: true, force: true });
  }

	function destroySession(session, terminalError = 'Bruno runner session closed') {
		if (!session) return Promise.resolve();
		if (session.destroyPromise) return session.destroyPromise;
		session.destroyPromise = destroySessionResources(
			session,
			terminalError,
			pendingDrainTimeoutMS,
			releaseSession,
			workerTerminationTimeoutMS,
			markCleanupFailed
		);
			return session.destroyPromise;
  }

  function releaseSession(session) {
    if (!session || sessions.get(session.id) !== session) return;
    if (session.expiryTimer) clearTimeout(session.expiryTimer);
    session.expiryTimer = null;
    sessions.delete(session.id);
    const bundle = bundles.get(session.bundleID);
    if (bundle) {
      bundle.sessions = Math.max(0, bundle.sessions - 1);
      touchBundle(bundle);
    }
  }

  function sessionExpiryDeadline(session) {
    const hardDeadline = session.createdAt + sessionTTLMS;
    if (session.busy) return hardDeadline;
    return Math.min(hardDeadline, session.lastActiveAt + sessionIdleTTLMS);
  }

  function scheduleSessionExpiry(session) {
    if (!session || session.closed || session.expiring || sessions.get(session.id) !== session) return;
    if (session.expiryTimer) clearTimeout(session.expiryTimer);
    const delay = Math.max(1, sessionExpiryDeadline(session) - Date.now());
    session.expiryTimer = setTimeout(() => {
      session.expiryTimer = null;
      void expireSession(session);
    }, delay);
    session.expiryTimer.unref?.();
  }

  function touchSession(session) {
    if (!session || session.closed || session.expiring) return;
    session.lastActiveAt = Date.now();
    scheduleSessionExpiry(session);
  }

  function sessionExpired(session, now = Date.now()) {
    if (!session) return true;
    return now >= session.createdAt + sessionTTLMS || (!session.busy && now >= session.lastActiveAt + sessionIdleTTLMS);
  }

  async function expireSession(session) {
    if (!session || session.expiring || sessions.get(session.id) !== session) return;
    if (!sessionExpired(session)) {
      scheduleSessionExpiry(session);
      return;
    }
    await destroySession(session, 'Bruno runner session expired');
  }

  async function executeSession(session, action, response) {
    if (session.busy) {
      json(response, 409, { error: 'Bruno runner session is already executing' });
      return;
    }
    session.busy = true;
    touchSession(session);
    response.writeHead(200, { 'content-type': 'application/x-ndjson' });
    response.flushHeaders?.();
    const requestID = safeID('exec');
    let responseEnded = false;
    let terminalQueued = false;
    let workerTerminal = false;
    let cleaned = false;
    let queuedEvents = 0;
    let emittedBytes = 0;
    let writeChain = Promise.resolve();
		const writeEvent = async (event) => {
			if (response.destroyed || response.writableEnded) return;
			const encoded = `${JSON.stringify(event)}\n`;
			const encodedBytes = Buffer.byteLength(encoded);
			if (encodedBytes > session.outputLimit - emittedBytes) {
				throw new Error('Bruno result stream exceeded its configured output limit');
			}
			emittedBytes += encodedBytes;
			if (!response.write(encoded)) {
				const drained = await waitForDrainOrClose(response, pendingDrainTimeoutMS);
				if (!drained && !response.destroyed && !response.writableEnded) {
					response.destroy();
					throw new Error('Bruno result stream backpressure deadline exceeded');
				}
			}
    };
    const cleanup = () => {
      if (cleaned) return;
      cleaned = true;
      session.busy = false;
      session.pending.delete(requestID);
      touchSession(session);
    };
    const queueEvent = (event) => {
      if (terminalQueued || response.destroyed || response.writableEnded) return false;
      if (queuedEvents >= maxPendingEvents) {
        terminalQueued = true;
        session.worker.postMessage({ type: 'cancel', requestID });
        writeChain = writeChain
          .catch(() => {})
          .then(() => writeEvent({
            type: 'error',
            error: `Bruno result stream exceeded ${maxPendingEvents} pending events; execution was cancelled`
          }))
          .finally(() => {
            responseEnded = true;
            if (!response.destroyed && !response.writableEnded) response.end();
          });
        return false;
      }
      queuedEvents += 1;
      writeChain = writeChain
        .catch(() => {})
        .then(() => writeEvent(event))
        .finally(() => {
          queuedEvents = Math.max(0, queuedEvents - 1);
        });
      return true;
    };
    const finishStream = (event) => {
      if (workerTerminal) return writeChain;
      workerTerminal = true;
      if (terminalQueued || response.destroyed || response.writableEnded) {
        cleanup();
        responseEnded = true;
        if (!response.destroyed && !response.writableEnded) response.end();
        return writeChain;
      }
      terminalQueued = true;
      queuedEvents += 1;
      writeChain = writeChain
        .catch(() => {})
        .then(() => event ? writeEvent(event) : undefined)
        .finally(() => {
          queuedEvents = Math.max(0, queuedEvents - 1);
          cleanup();
          responseEnded = true;
          if (!response.destroyed && !response.writableEnded) response.end();
        });
      return writeChain;
    };
    const onMessage = (message) => {
      if (message.requestID !== requestID) return;
      if (message.type === 'event') {
        queueEvent(message.event);
        return;
      }
      if (message.type === 'complete') {
        return finishStream({ type: 'complete', result: message.result });
      }
      if (message.type === 'error') {
        return finishStream({ type: 'error', error: message.error });
      }
      if (message.type === 'output_limit') {
        return finishStream(null);
      }
    };
    const abortResponse = () => {
      terminalQueued = true;
      responseEnded = true;
      cleanup();
      if (!response.destroyed && !response.writableEnded) response.destroy();
    };
    session.pending.set(requestID, { handle: onMessage, abort: abortResponse });
    response.once('close', async () => {
      if (!responseEnded && !session.closed) {
        responseEnded = true;
        session.worker.postMessage({ type: 'cancel', requestID });
      }
    });
    session.worker.postMessage({ type: 'execute', requestID, action });
  }

  const server = http.createServer(async (request, response) => {
    try {
      const requestURL = new URL(request.url, 'http://runner.local');
      if (request.method === 'GET' && requestURL.pathname === '/v1/health') {
        json(response, cleanupFailed ? 503 : 200, {
          status: cleanupFailed ? 'cleanup_failed' : 'ok',
          runner_version: RUNNER_VERSION,
          compatibility_version: COMPATIBILITY_VERSION
        });
        return;
      }
      if (request.method === 'GET' && requestURL.pathname === '/v1/capabilities') {
        json(response, 200, {
          runner_version: RUNNER_VERSION,
          compatibility_version: COMPATIBILITY_VERSION,
          source_formats: ['bru', 'yml', 'mixed'],
          request_types: ['http', 'graphql', 'grpc', 'websocket'],
          sandbox_modes: ['safe', 'developer'],
          streaming: true,
          session_capacity: maxSessions,
          active_sessions: sessions.size,
          max_output_bytes: maxOutputBytes,
          session_ttl_ms: sessionTTLMS,
          session_idle_ttl_ms: sessionIdleTTLMS,
          bundle_ttl_ms: bundleTTLMS,
          bundle_idle_ttl_ms: bundleIdleTTLMS,
          governed_egress_ready: Boolean(egressBrokerURL),
          direct_egress_allowed: allowDirectEgress,
          cleanup_failed: cleanupFailed,
          intentional_extensions: ['grpc_collection_execution', 'websocket_collection_execution', 'cross_protocol_run_request']
        });
        return;
      }
		if (cleanupFailed && request.method !== 'DELETE') {
			json(response, 503, { error: 'Bruno runner cleanup failed; restart is required before accepting more work' });
			return;
		}
      if (request.method === 'POST' && requestURL.pathname === '/v1/bundles/open') {
        const requestedCompatibility = request.headers['x-bruno-compatibility-version'];
        if (requestedCompatibility && requestedCompatibility !== COMPATIBILITY_VERSION) {
          json(response, 409, { error: `runner is pinned to Bruno ${COMPATIBILITY_VERSION}` });
          return;
        }
        const uploadPath = path.join(root, `${safeID('upload')}.tar`);
        const upload = await spoolRequest(request, uploadPath, maxBundleBytes);
        const digest = upload.sha256;
        const expected = String(request.headers['x-bruno-sha256'] || '').trim().toLowerCase();
        if (expected && expected !== digest) {
          await fsp.rm(uploadPath, { force: true });
          json(response, 422, { error: 'bundle sha256 mismatch' });
          return;
        }
        const bundleID = safeID('bundle');
        const bundleRoot = path.join(root, bundleID);
        await fsp.mkdir(bundleRoot, { recursive: false, mode: 0o700 });
        try {
          const extracted = await extractBundle(fs.createReadStream(uploadPath), bundleRoot, { maxBytes: maxBundleBytes });
          const detected = await detectCollectionRoot(bundleRoot);
          const now = Date.now();
          const bundle = {
            id: bundleID,
            digest,
            root: detected.root,
            relativeRoot: detected.relative,
            synthetic: detected.synthetic,
            files: detected.files,
            extracted,
            sessions: 0,
            createdAt: now,
            lastActiveAt: now,
            expiryTimer: null,
            expiring: false
          };
          bundles.set(bundleID, bundle);
          scheduleBundleExpiry(bundle);
        } catch (error) {
          await fsp.rm(bundleRoot, { recursive: true, force: true });
          throw error;
        } finally {
          await fsp.rm(uploadPath, { force: true });
        }
        json(response, 201, { bundle_id: bundleID, sha256: digest, size_bytes: upload.sizeBytes });
        return;
      }
      const bundleInspect = requestURL.pathname.match(/^\/v1\/bundles\/([^/]+)\/inspect$/);
      if (request.method === 'GET' && bundleInspect) {
        const bundle = bundles.get(bundleInspect[1]);
        if (!bundle || bundleExpired(bundle)) {
          if (bundle) await expireBundle(bundle);
          json(response, 404, { error: 'bundle not found' });
          return;
        }
        touchBundle(bundle);
        json(response, 200, {
          bundle_id: bundle.id,
          sha256: bundle.digest,
          root_path: bundle.relativeRoot,
          synthetic_collection: bundle.synthetic,
          files: bundle.files,
          compatibility_version: COMPATIBILITY_VERSION
        });
        return;
      }
      if (request.method === 'POST' && requestURL.pathname === '/v1/sessions') {
        const payload = JSON.parse((await readBody(request, 2 * 1024 * 1024)).toString('utf8'));
        const bundle = bundles.get(payload.bundle_id);
        if (!bundle || bundleExpired(bundle)) {
          if (bundle) await expireBundle(bundle);
          json(response, 404, { error: 'bundle not found' });
          return;
        }
        if (payload.compatibility_version !== COMPATIBILITY_VERSION) {
          json(response, 409, { error: `runner is pinned to Bruno ${COMPATIBILITY_VERSION}` });
          return;
        }
        const sandbox = payload.sandbox || 'safe';
        if (!['safe', 'developer'].includes(sandbox)) {
          json(response, 422, { error: 'sandbox must be safe or developer' });
          return;
        }
        if (sandbox === 'developer' && typeof developerSessionFactory !== 'function') {
          json(response, 403, { error: 'developer sessions require an isolated developer worker' });
          return;
        }
        if (!egressBrokerURL && !allowDirectEgress) {
          json(response, 503, { error: 'Bruno sessions require a governed egress broker; direct egress is disabled' });
          return;
        }
        const requestedOutputLimit = sessionOutputLimit(payload);
        const outputLimit = payload?.limits?.max_output_bytes == null
          ? Math.min(DEFAULT_MAX_OUTPUT_BYTES, maxOutputBytes)
          : requestedOutputLimit;
        if (outputLimit > maxOutputBytes) {
          const error = new Error(`Bruno max_output_bytes exceeds the operator limit of ${maxOutputBytes}`);
          error.statusCode = 422;
          throw error;
        }
        payload.limits = { ...(payload.limits || {}), max_output_bytes: outputLimit };
        const requestedSessionID = String(payload.session_id || '').trim();
        if (requestedSessionID && !/^session-[a-f0-9]{32,64}$/i.test(requestedSessionID)) {
          json(response, 422, { error: 'session_id must be a client-generated opaque session identifier' });
          return;
        }
        const sessionID = requestedSessionID || safeID('session');
        const bindingPayload = { ...payload };
        delete bindingPayload.session_id;
        // Bind retries to the complete creation request, including policy,
        // limits, environment selection, and secret material. A process-local
        // HMAC avoids retaining another plaintext copy or exposing a reusable
        // low-entropy secret digest while still detecting altered retries.
        const creationBinding = crypto.createHmac('sha256', sessionBindingKey)
          .update(JSON.stringify(bindingPayload))
          .digest('hex');
        const retained = sessions.get(sessionID);
        if (retained) {
          if (retained.creationBinding !== creationBinding) {
            json(response, 409, { error: 'session_id is already bound to different session parameters' });
            return;
          }
          if (retained.readyPromise) await retained.readyPromise;
          if (sessions.get(sessionID) !== retained || retained.closed || retained.expiring || !retained.worker) {
            json(response, 409, { error: 'idempotent session creation did not complete' });
            return;
          }
          json(response, 201, { session_id: sessionID });
          return;
        }
        if (sessions.size >= maxSessions) {
          json(response, 429, { error: `Bruno runner session capacity ${maxSessions} is exhausted` });
          return;
        }
        const now = Date.now();
        let readyResolve;
        let readyReject;
        const readyPromise = new Promise((resolve, reject) => {
          readyResolve = resolve;
          readyReject = reject;
        });
        // The route itself observes rejections, but keep an explicit handler so
        // cancellation after a disconnected create request never becomes an
        // unhandled process-level rejection.
        void readyPromise.catch(() => {});
        const session = {
          id: sessionID,
          bundleID: bundle.id,
          creationBinding,
          worker: null,
          readyPromise,
          readyResolve,
          readyReject,
          pending: new Map(),
          outputLimit,
          busy: false,
          closed: false,
          expiring: false,
          creating: true,
          provisionAbort: sandbox === 'developer' ? new AbortController() : null,
          createdAt: now,
          lastActiveAt: now,
          expiryTimer: null
        };
        // Reserve the client-generated ID and capacity before any asynchronous
        // developer factory work. A retry waits on this exact placeholder and a
        // DELETE can cancel it even if the original response connection was lost.
        sessions.set(sessionID, session);
        bundle.sessions += 1;
        touchBundle(bundle);
        scheduleSessionExpiry(session);
        let worker;
        try {
          worker = sandbox === 'developer'
            ? await provisionWorkerBounded(
              () => developerSessionFactory({
                sessionID,
                collectionPath: bundle.root,
                config: payload,
                signal: session.provisionAbort.signal
              }),
              session.provisionAbort.signal,
              workerProvisionTimeoutMS,
              workerTerminationTimeoutMS,
              markCleanupFailed
            )
            : new Worker(path.join(__dirname, 'worker.js'), {
              workerData: {
                sessionID,
                collectionPath: bundle.root,
                config: payload
              },
              env: {
                NODE_ENV: 'production',
                APEXLOAD_BRUNO_WORKER: 'true',
                APEXLOAD_BRUNO_EGRESS_BROKER_URL: egressBrokerURL,
                APEXLOAD_BRUNO_REQUIRE_EGRESS_BROKER: allowDirectEgress ? 'false' : 'true'
              },
              resourceLimits: workerResourceLimits(payload)
            });
        } catch (error) {
          if (bundles.get(bundle.id) !== bundle || bundleExpired(bundle)) {
            error.statusCode = 404;
          }
          session.closed = true;
          releaseSession(session);
          readyReject(error);
          throw error;
        }
        // The placeholder may have been deleted or its bundle may have expired
        // while an asynchronous developer factory was provisioning the worker.
        const bundleUnavailable = bundles.get(bundle.id) !== bundle || bundleExpired(bundle);
        if (sessions.get(sessionID) !== session || session.closed || session.expiring || bundleUnavailable) {
          if (!await terminateWorkerBounded(worker, workerTerminationTimeoutMS)) markCleanupFailed();
          const error = new Error('session creation was cancelled before the worker became ready');
          error.statusCode = bundleUnavailable ? 404 : 409;
          readyReject(error);
          if (sessions.get(sessionID) === session) releaseSession(session);
          throw error;
        }
        if (!worker || typeof worker.on !== 'function' ||
            typeof worker.postMessage !== 'function' || typeof worker.terminate !== 'function') {
          if (!await terminateWorkerBounded(worker, workerTerminationTimeoutMS)) markCleanupFailed();
          const error = new Error('developer worker factory returned an invalid worker');
          error.statusCode = 500;
          session.closed = true;
          readyReject(error);
          releaseSession(session);
          throw error;
        }
        session.worker = worker;
        session.creating = false;
        worker.on('message', (message) => {
          const pending = session.pending.get(message.requestID);
          if (pending) pending.handle(message);
        });
        worker.on('error', (error) => {
          for (const [requestID, pending] of session.pending) {
            pending.handle({ type: 'error', requestID, error: error.message });
          }
        });
        worker.on('exit', (code) => {
          void (async () => {
            if (!session.closed) {
              await finishPendingSession(session, `Bruno worker exited unexpectedly with code ${code}`);
            }
            session.closed = true;
            releaseSession(session);
          })();
        });
        readyResolve();
        json(response, 201, { session_id: sessionID });
        return;
      }
      const executeMatch = requestURL.pathname.match(/^\/v1\/sessions\/([^/]+)\/execute$/);
      if (request.method === 'POST' && executeMatch) {
        const session = sessions.get(executeMatch[1]);
        const bundle = session ? bundles.get(session.bundleID) : null;
		if (!session || session.closed || session.expiring || session.creating || !session.worker || sessionExpired(session) || !bundle || bundleExpired(bundle)) {
          if (bundle && bundleExpired(bundle)) await expireBundle(bundle);
          else if (session) await expireSession(session);
          json(response, 404, { error: 'session not found' });
          return;
        }
        const payload = JSON.parse((await readBody(request, 2 * 1024 * 1024)).toString('utf8'));
        const currentBundle = bundles.get(session.bundleID);
		if (sessions.get(session.id) !== session || session.closed || session.expiring || sessionExpired(session) ||
            !currentBundle || bundleExpired(currentBundle)) {
          if (currentBundle && bundleExpired(currentBundle)) await expireBundle(currentBundle);
          else if (sessions.get(session.id) === session) await expireSession(session);
          json(response, 404, { error: 'session not found' });
          return;
        }
        await executeSession(session, payload.action, response);
        return;
      }
      const cancelMatch = requestURL.pathname.match(/^\/v1\/sessions\/([^/]+)\/cancel$/);
      if (request.method === 'POST' && cancelMatch) {
        const session = sessions.get(cancelMatch[1]);
        const bundle = session ? bundles.get(session.bundleID) : null;
		if (!session || session.closed || session.expiring || session.creating || !session.worker || sessionExpired(session) || !bundle || bundleExpired(bundle)) {
          if (bundle && bundleExpired(bundle)) await expireBundle(bundle);
          else if (session) await expireSession(session);
          empty(response, 404);
          return;
        }
        touchSession(session);
        session.worker.postMessage({ type: 'cancel' });
        empty(response, 204);
        return;
      }
      const sessionDelete = requestURL.pathname.match(/^\/v1\/sessions\/([^/]+)$/);
      if (request.method === 'DELETE' && sessionDelete) {
        const session = sessions.get(sessionDelete[1]);
        if (!session) {
          empty(response, 404);
          return;
        }
        await destroySession(session, 'Bruno runner session deleted');
        empty(response, 204);
        return;
      }
      const bundleDelete = requestURL.pathname.match(/^\/v1\/bundles\/([^/]+)$/);
      if (request.method === 'DELETE' && bundleDelete) {
        const bundle = bundles.get(bundleDelete[1]);
        if (!bundle || bundleExpired(bundle)) {
          if (bundle) await expireBundle(bundle);
          empty(response, 404);
          return;
        }
        if (bundle.sessions > 0) {
          json(response, 409, { error: 'bundle still has active sessions' });
          return;
        }
        if (bundle.expiryTimer) clearTimeout(bundle.expiryTimer);
        bundle.expiryTimer = null;
        bundle.expiring = true;
        bundles.delete(bundle.id);
        await fsp.rm(path.join(root, bundle.id), { recursive: true, force: true });
        empty(response, 204);
        return;
      }
      json(response, 404, { error: 'route not found' });
    } catch (error) {
      json(response, Number(error.statusCode || 400), { error: error.message || String(error) });
    }
  });

  server.closeRunner = async () => {
    clearInterval(ownerHeartbeat);
    await Promise.allSettled(Array.from(sessions.values()).map((session) => (
      destroySession(session, 'Bruno runner shutting down')
    )));
    for (const bundle of bundles.values()) {
      if (bundle.expiryTimer) clearTimeout(bundle.expiryTimer);
    }
    bundles.clear();
    await new Promise((resolve) => server.close(resolve));
    await fsp.rm(root, { recursive: true, force: true });
  };
  server.runnerRoot = root;
  return server;
}

if (require.main === module) {
  const address = process.env.APEXLOAD_BRUNO_RUNNER_ADDR || '127.0.0.1';
  const port = Number(process.env.APEXLOAD_BRUNO_RUNNER_PORT || 8097);
  const server = createRunnerServer();
  server.listen(port, address, () => {
    process.stdout.write(`APEX Bruno runner ${RUNNER_VERSION} listening on ${address}:${port}\n`);
  });
}

module.exports = {
  COMPATIBILITY_VERSION,
  RUNNER_VERSION,
  createRunnerServer,
  detectCollectionRoot,
  extractBundle,
  positiveInteger,
  spoolRequest,
  sweepStaleRunnerRoots,
  validRelativePath,
  waitForDrainOrClose,
  finishPendingSession,
  destroySessionResources,
  sessionOutputLimit,
  workerResourceLimits,
  terminateWorkerBounded,
  provisionWorkerBounded
};
