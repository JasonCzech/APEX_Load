'use strict';

const assert = require('node:assert/strict');
const { EventEmitter } = require('node:events');
const fs = require('node:fs');
const http = require('node:http');
const net = require('node:net');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');
const tar = require('tar-stream');
const { WebSocketServer } = require('ws');
const {
  createRunnerServer,
  positiveInteger,
  sweepStaleRunnerRoots,
  validRelativePath,
  waitForDrainOrClose,
  destroySessionResources,
  sessionOutputLimit,
  workerResourceLimits,
  terminateWorkerBounded
} = require('../src/server');

class ControlledWorker extends EventEmitter {
  constructor() {
    super();
    this.messages = [];
    this.terminated = false;
  }

  postMessage(message) {
    this.messages.push(message);
    this.emit('posted', message);
  }

  terminate() {
    this.terminated = true;
    return Promise.resolve(0);
  }
}

class DeferredTerminateWorker extends ControlledWorker {
	constructor() {
		super();
		this.terminateStarted = false;
		this.releaseTermination = null;
	}

	terminate() {
		this.terminated = true;
		this.terminateStarted = true;
		return new Promise((resolve) => {
			this.releaseTermination = () => resolve(0);
		});
	}
}

async function waitUntil(predicate, description, timeoutMS = 2000) {
  const deadline = Date.now() + timeoutMS;
  while (Date.now() < deadline) {
    if (predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 5));
  }
  assert.fail(`timed out waiting for ${description}`);
}

function executePayload() {
  return Buffer.from(JSON.stringify({
    action: { name: 'Controlled execution', type: 'bruno_run', params: { selection: 'collection' } }
  }));
}

async function createControlledSession(origin, worker) {
  const opened = await request(origin, 'POST', '/v1/bundles/open', await standaloneBundle(), {
    'content-type': 'application/x-tar',
    'x-bruno-compatibility-version': '3.5.2'
  });
  assert.equal(opened.status, 201, opened.body.toString());
  const bundleID = JSON.parse(opened.body).bundle_id;
  const created = await request(origin, 'POST', '/v1/sessions', Buffer.from(JSON.stringify({
    bundle_id: bundleID,
    compatibility_version: '3.5.2',
    sandbox: 'developer'
  })), { 'content-type': 'application/json' });
  assert.equal(created.status, 201, created.body.toString());
  return { bundleID, sessionID: JSON.parse(created.body).session_id, worker };
}

function beginExecution(origin, sessionID) {
  return new Promise((resolve, reject) => {
    const target = new URL(`/v1/sessions/${sessionID}/execute`, origin);
    const requestStream = http.request(target, {
      method: 'POST',
      headers: { 'content-type': 'application/json', accept: 'application/x-ndjson' }
    }, (response) => resolve({ request: requestStream, response }));
    requestStream.on('error', reject);
    requestStream.end(executePayload());
  });
}

function collectResponse(response) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    response.on('data', (chunk) => chunks.push(chunk));
    response.on('end', () => resolve(Buffer.concat(chunks)));
    response.on('error', reject);
  });
}

function within(promise, description, timeoutMS = 2000) {
  let timer;
  return Promise.race([
    promise,
    new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error(`timed out waiting for ${description}`)), timeoutMS);
    })
  ]).finally(() => clearTimeout(timer));
}

function request(origin, method, route, body, headers = {}) {
  return new Promise((resolve, reject) => {
    const target = new URL(route, origin);
    const req = http.request(target, { method, headers }, (response) => {
      const chunks = [];
      response.on('data', (chunk) => chunks.push(chunk));
      response.on('end', () => resolve({
        status: response.statusCode,
        headers: response.headers,
        body: Buffer.concat(chunks)
      }));
    });
    req.on('error', reject);
    if (body) req.write(body);
    req.end();
  });
}

async function tarBundle(entries) {
  const pack = tar.pack();
  const chunks = [];
  pack.on('data', (chunk) => chunks.push(chunk));
  const done = new Promise((resolve, reject) => {
    pack.on('end', resolve);
    pack.on('error', reject);
  });
  for (const [name, content] of Object.entries(entries)) {
    pack.entry({ name, mode: 0o600 }, content);
  }
  pack.finalize();
  await done;
  return Buffer.concat(chunks);
}

async function standaloneBundle(url = 'http://127.0.0.1:1/health', extraEntries = {}) {
  return tarBundle({
    'request.yml': `info:
  name: Health
  type: http
  seq: 1
http:
  method: GET
  url: ${url}
`,
    ...extraEntries
  });
}

async function openSafeSession(origin, bundle, overrides = {}) {
  const opened = await request(origin, 'POST', '/v1/bundles/open', bundle, {
    'content-type': 'application/x-tar',
    'x-bruno-compatibility-version': '3.5.2'
  });
  assert.equal(opened.status, 201, opened.body.toString());
  const bundleID = JSON.parse(opened.body).bundle_id;
  const created = await request(origin, 'POST', '/v1/sessions', Buffer.from(JSON.stringify({
    bundle_id: bundleID,
    compatibility_version: '3.5.2',
    sandbox: 'safe',
    ...overrides
  })), { 'content-type': 'application/json' });
  assert.equal(created.status, 201, created.body.toString());
  return { bundleID, sessionID: JSON.parse(created.body).session_id };
}

test('validRelativePath rejects traversal and absolute paths', () => {
  assert.equal(validRelativePath('../secret'), null);
  assert.equal(validRelativePath('/etc/passwd'), null);
  assert.equal(validRelativePath('folder/request.yml'), 'folder/request.yml');
});

test('runner limits reject invalid operator configuration', () => {
  assert.equal(positiveInteger('25', 10, 'limit'), 25);
  assert.throws(
    () => createRunnerServer({ maxPendingEvents: -1 }),
    /Bruno maximum pending events must be a positive integer/
  );
	assert.throws(
		() => createRunnerServer({ maxSessions: 0 }),
		/Bruno maximum sessions must be a positive integer/
	);
  assert.throws(
    () => createRunnerServer({ pendingDrainTimeoutMS: 0 }),
    /Bruno pending response drain timeout milliseconds must be a positive integer/
  );
  assert.throws(
    () => createRunnerServer({ workerProvisionTimeoutMS: 0 }),
    /Bruno worker provision timeout milliseconds must be a positive integer/
  );
  assert.throws(
    () => createRunnerServer({ sessionTTLMS: 0 }),
    /Bruno session TTL milliseconds must be a positive integer/
  );
  assert.throws(
    () => createRunnerServer({ sessionIdleTTLMS: -1 }),
    /Bruno session idle TTL milliseconds must be a positive integer/
  );
  assert.throws(
    () => createRunnerServer({ bundleTTLMS: 0 }),
    /Bruno bundle TTL milliseconds must be a positive integer/
  );
  assert.throws(
    () => createRunnerServer({ bundleIdleTTLMS: -1 }),
    /Bruno bundle idle TTL milliseconds must be a positive integer/
  );
});

test('worker heap limits preserve the selected output envelope with finite headroom', () => {
  assert.equal(sessionOutputLimit({ limits: { max_output_bytes: 8 * 1024 * 1024 } }), 8 * 1024 * 1024);
  assert.throws(
    () => sessionOutputLimit({ limits: { max_output_bytes: 1024 * 1024 * 1024 + 1 } }),
    /max_output_bytes/
  );
  assert.throws(() => sessionOutputLimit({ limits: { max_output_bytes: 0 } }), /max_output_bytes/);
  assert.throws(() => createRunnerServer({ maxOutputBytes: 0 }), /max_output_bytes/);
  const limits = workerResourceLimits({ limits: { max_output_bytes: 64 * 1024 * 1024 } });
  assert.ok(limits.maxOldGenerationSizeMb >= 64 + 128);
  assert.ok(Number.isSafeInteger(limits.maxYoungGenerationSizeMb));
  assert.equal(limits.stackSizeMb, 8);
});

test('runner independently enforces the operator output ceiling', async (t) => {
  const runner = createRunnerServer({ allowDirectEgress: true, maxOutputBytes: 1024 });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  t.after(() => runner.closeRunner());
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const opened = await request(origin, 'POST', '/v1/bundles/open', await standaloneBundle(), {
    'content-type': 'application/x-tar',
    'x-bruno-compatibility-version': '3.5.2'
  });
  const bundleID = JSON.parse(opened.body).bundle_id;
  const created = await request(origin, 'POST', '/v1/sessions', Buffer.from(JSON.stringify({
    bundle_id: bundleID,
    compatibility_version: '3.5.2',
    sandbox: 'safe',
    limits: { max_output_bytes: 2048 }
  })), { 'content-type': 'application/json' });
  assert.equal(created.status, 422, created.body.toString());
  assert.match(JSON.parse(created.body).error, /operator limit of 1024/);
  const capabilities = JSON.parse((await request(origin, 'GET', '/v1/capabilities')).body);
  assert.equal(capabilities.max_output_bytes, 1024);
  assert.equal(capabilities.active_sessions, 0);
  assert.equal((await request(origin, 'DELETE', `/v1/bundles/${bundleID}`)).status, 204);
});

test('worker termination wait is independently bounded', async () => {
  const worker = { terminate: () => new Promise(() => {}) };
  assert.equal(await within(terminateWorkerBounded(worker, 15), 'bounded worker termination', 250), false);
});

test('worker termination rejection fails closed', async () => {
  const worker = { terminate: () => Promise.reject(new Error('termination failed')) };
  assert.equal(await terminateWorkerBounded(worker, 250), false);
});

test('stale-root sweep removes only dead owned roots and preserves active siblings', (t) => {
  const parent = fs.mkdtempSync(path.join(os.tmpdir(), 'apexload-bruno-sweep-test-'));
  t.after(() => fs.rmSync(parent, { recursive: true, force: true }));
  const nowMS = Date.now();
  const old = new Date(nowMS - 10_000);
  const recent = new Date(nowMS - 10);
  const createOwnedRoot = (suffix, pid, modifiedAt) => {
    const root = path.join(parent, `apexload-bruno-runner-${suffix}`);
    fs.mkdirSync(root, { mode: 0o700 });
    const owner = path.join(root, '.apexload-owner.json');
    fs.writeFileSync(owner, JSON.stringify({ pid }), { mode: 0o600 });
    fs.utimesSync(owner, modifiedAt, modifiedAt);
    fs.utimesSync(root, modifiedAt, modifiedAt);
    return root;
  };
  const stale = createOwnedRoot('stale', 999, old);
  const active = createOwnedRoot('active', 123, old);
  const recentDead = createOwnedRoot('recent', 999, recent);
  const unowned = path.join(parent, 'apexload-bruno-runner-unowned');
  fs.mkdirSync(unowned, { mode: 0o700 });
  fs.utimesSync(unowned, old, old);
  const target = path.join(parent, 'unrelated-target');
  fs.mkdirSync(target);
  const linked = path.join(parent, 'apexload-bruno-runner-linked');
  fs.symlinkSync(target, linked, 'dir');

  const removed = sweepStaleRunnerRoots(parent, {
    staleRootTTLMS: 100,
    nowMS,
    processAlive: (pid) => pid === 123
  });

  assert.deepEqual(removed, [stale]);
  assert.equal(fs.existsSync(stale), false);
  assert.equal(fs.existsSync(active), true);
  assert.equal(fs.existsSync(recentDead), true);
  assert.equal(fs.existsSync(unowned), true);
  assert.equal(fs.lstatSync(linked).isSymbolicLink(), true);
  assert.equal(fs.existsSync(target), true);
});

test('idle Bruno bundles expire and remove extracted material', async (t) => {
  const tempParent = fs.mkdtempSync(path.join(os.tmpdir(), 'apexload-bruno-idle-bundle-test-'));
  const runner = createRunnerServer({
    bundleTTLMS: 2000,
    bundleIdleTTLMS: 40,
    tempParent
  });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  t.after(async () => {
    await runner.closeRunner();
    fs.rmSync(tempParent, { recursive: true, force: true });
  });
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const opened = await request(origin, 'POST', '/v1/bundles/open', await standaloneBundle(), {
    'content-type': 'application/x-tar',
    'x-bruno-compatibility-version': '3.5.2'
  });
  assert.equal(opened.status, 201, opened.body.toString());
  const bundleID = JSON.parse(opened.body).bundle_id;
  const bundlePath = path.join(runner.runnerRoot, bundleID);
  assert.equal(fs.existsSync(bundlePath), true);

  await waitUntil(() => !fs.existsSync(bundlePath), 'idle bundle removal');

  const inspected = await request(origin, 'GET', `/v1/bundles/${bundleID}/inspect`);
  assert.equal(inspected.status, 404, inspected.body.toString());
});

test('hard Bruno bundle expiry terminates retained sessions', async (t) => {
  const worker = new ControlledWorker();
  const runner = createRunnerServer({
    allowDirectEgress: true,
    bundleTTLMS: 250,
    bundleIdleTTLMS: 2000,
    sessionTTLMS: 2000,
    sessionIdleTTLMS: 2000,
    developerSessionFactory: async () => worker
  });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  t.after(() => runner.closeRunner());
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const { bundleID, sessionID } = await createControlledSession(origin, worker);
  const bundlePath = path.join(runner.runnerRoot, bundleID);

  await waitUntil(() => worker.terminated && !fs.existsSync(bundlePath), 'hard bundle expiry');

  const execution = await request(origin, 'POST', `/v1/sessions/${sessionID}/execute`, executePayload(), {
    'content-type': 'application/json'
  });
  assert.equal(execution.status, 404, execution.body.toString());
  assert.equal((await request(origin, 'GET', `/v1/bundles/${bundleID}/inspect`)).status, 404);
});

test('session provisioning cannot retain a worker after bundle expiry', async (t) => {
  const worker = new ControlledWorker();
  let factoryStarted = false;
  let releaseFactory;
  const factoryGate = new Promise((resolve) => { releaseFactory = resolve; });
  const runner = createRunnerServer({
    allowDirectEgress: true,
    bundleTTLMS: 500,
    bundleIdleTTLMS: 2000,
    developerSessionFactory: async () => {
      factoryStarted = true;
      await factoryGate;
      return worker;
    }
  });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  t.after(() => runner.closeRunner());
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const opened = await request(origin, 'POST', '/v1/bundles/open', await standaloneBundle(), {
    'content-type': 'application/x-tar',
    'x-bruno-compatibility-version': '3.5.2'
  });
  assert.equal(opened.status, 201, opened.body.toString());
  const bundleID = JSON.parse(opened.body).bundle_id;
  const creating = request(origin, 'POST', '/v1/sessions', Buffer.from(JSON.stringify({
    bundle_id: bundleID,
    compatibility_version: '3.5.2',
    sandbox: 'developer'
  })), { 'content-type': 'application/json' });

  await waitUntil(() => factoryStarted, 'developer worker provisioning');
  await waitUntil(() => !fs.existsSync(path.join(runner.runnerRoot, bundleID)), 'bundle expiry during worker provisioning');
  releaseFactory();

  const created = await creating;
  assert.equal(created.status, 404, created.body.toString());
  await waitUntil(() => worker.terminated, 'late worker cleanup after bundle expiry');
  const capabilities = JSON.parse((await request(origin, 'GET', '/v1/capabilities')).body);
  assert.equal(capabilities.active_sessions, 0);
});

test('disconnect cancellation keeps a session busy until its matching worker terminal event', async (t) => {
  const worker = new ControlledWorker();
  const runner = createRunnerServer({
    allowDirectEgress: true,
    developerSessionFactory: async () => worker
  });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  t.after(() => runner.closeRunner());
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const { sessionID } = await createControlledSession(origin, worker);

  const streaming = await beginExecution(origin, sessionID);
  await waitUntil(() => worker.messages.some((message) => message.type === 'execute'), 'first execute dispatch');
  const firstExecute = worker.messages.find((message) => message.type === 'execute');
  streaming.response.on('error', () => {});
  streaming.response.destroy();
  await waitUntil(
    () => worker.messages.some((message) => message.type === 'cancel' && message.requestID === firstExecute.requestID),
    'request-scoped disconnect cancellation'
  );

  const whileCancelling = await request(origin, 'POST', `/v1/sessions/${sessionID}/execute`, executePayload(), {
    'content-type': 'application/json'
  });
  assert.equal(whileCancelling.status, 409, whileCancelling.body.toString());
  worker.emit('message', { type: 'complete', requestID: 'different-request', result: {} });
  const afterWrongTerminal = await request(origin, 'POST', `/v1/sessions/${sessionID}/execute`, executePayload(), {
    'content-type': 'application/json'
  });
  assert.equal(afterWrongTerminal.status, 409, afterWrongTerminal.body.toString());

  worker.emit('message', { type: 'complete', requestID: firstExecute.requestID, result: { success: false } });
  const resumed = request(origin, 'POST', `/v1/sessions/${sessionID}/execute`, executePayload(), {
    'content-type': 'application/json'
  });
  await waitUntil(
    () => worker.messages.filter((message) => message.type === 'execute').length === 2,
    'execution after matching terminal event'
  );
  const secondExecute = worker.messages.filter((message) => message.type === 'execute')[1];
  worker.emit('message', { type: 'complete', requestID: secondExecute.requestID, result: { success: true } });
  assert.equal((await resumed).status, 200);
});

test('deleting a busy session completes its pending NDJSON response before termination', async (t) => {
  const worker = new ControlledWorker();
  const runner = createRunnerServer({
    allowDirectEgress: true,
    developerSessionFactory: async () => worker
  });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  t.after(() => runner.closeRunner());
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const { sessionID } = await createControlledSession(origin, worker);

  const streaming = await beginExecution(origin, sessionID);
  const responseBody = collectResponse(streaming.response);
  await waitUntil(() => worker.messages.some((message) => message.type === 'execute'), 'busy execute dispatch');
  const deleted = await within(
    request(origin, 'DELETE', `/v1/sessions/${sessionID}`),
    'busy session deletion'
  );
  assert.equal(deleted.status, 204, deleted.body.toString());
  const terminal = await within(responseBody, 'deleted session terminal NDJSON');
  assert.match(terminal.toString(), /Bruno runner session deleted/);
  assert.equal(worker.terminated, true);
  assert.equal(worker.messages.some((message) => message.type === 'cancel'), true);
});

test('closeRunner completes pending NDJSON responses before terminating workers', async (t) => {
  const worker = new ControlledWorker();
  const tempParent = fs.mkdtempSync(path.join(os.tmpdir(), 'apexload-bruno-close-pending-test-'));
  const runner = createRunnerServer({
    allowDirectEgress: true,
    developerSessionFactory: async () => worker,
    tempParent
  });
  let closed = false;
  t.after(async () => {
    if (!closed) await runner.closeRunner();
    fs.rmSync(tempParent, { recursive: true, force: true });
  });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const { sessionID } = await createControlledSession(origin, worker);

  const streaming = await beginExecution(origin, sessionID);
  const responseBody = collectResponse(streaming.response);
  await waitUntil(() => worker.messages.some((message) => message.type === 'execute'), 'shutdown execute dispatch');
  await within(runner.closeRunner(), 'runner shutdown with pending execution');
  closed = true;
  const terminal = await within(responseBody, 'shutdown terminal NDJSON');
  assert.match(terminal.toString(), /Bruno runner shutting down/);
  assert.equal(worker.terminated, true);
});

test('backpressure cancellation prevents overlap until the worker stops', async (t) => {
  const worker = new ControlledWorker();
  const runner = createRunnerServer({
    allowDirectEgress: true,
    maxPendingEvents: 1,
    developerSessionFactory: async () => worker
  });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  t.after(() => runner.closeRunner());
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const { sessionID } = await createControlledSession(origin, worker);

  const overflowing = request(origin, 'POST', `/v1/sessions/${sessionID}/execute`, executePayload(), {
    'content-type': 'application/json', accept: 'application/x-ndjson'
  });
  await waitUntil(() => worker.messages.some((message) => message.type === 'execute'), 'overflow execute dispatch');
  const firstExecute = worker.messages.find((message) => message.type === 'execute');
  worker.emit('message', { type: 'event', requestID: firstExecute.requestID, event: { type: 'result', sequence: 1 } });
  worker.emit('message', { type: 'event', requestID: firstExecute.requestID, event: { type: 'result', sequence: 2 } });
  const overflowResponse = await overflowing;
  assert.equal(overflowResponse.status, 200, overflowResponse.body.toString());
  assert.match(overflowResponse.body.toString(), /execution was cancelled/);
  assert.equal(
    worker.messages.some((message) => message.type === 'cancel' && message.requestID === firstExecute.requestID),
    true
  );

  const whileCancelling = await request(origin, 'POST', `/v1/sessions/${sessionID}/execute`, executePayload(), {
    'content-type': 'application/json'
  });
  assert.equal(whileCancelling.status, 409, whileCancelling.body.toString());
  assert.equal(worker.messages.filter((message) => message.type === 'execute').length, 1);

  worker.emit('message', { type: 'complete', requestID: firstExecute.requestID, result: { success: false } });
  const resumed = request(origin, 'POST', `/v1/sessions/${sessionID}/execute`, executePayload(), {
    'content-type': 'application/json'
  });
  await waitUntil(
    () => worker.messages.filter((message) => message.type === 'execute').length === 2,
    'execution after backpressure terminal event'
  );
  const secondExecute = worker.messages.filter((message) => message.type === 'execute')[1];
  worker.emit('message', { type: 'complete', requestID: secondExecute.requestID, result: { success: true } });
  assert.equal((await resumed).status, 200);
});

test('idle Bruno sessions expire, release capacity, and terminate retained workers', async (t) => {
  const runner = createRunnerServer({
    allowDirectEgress: true,
    sessionTTLMS: 1000,
    sessionIdleTTLMS: 40
  });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  t.after(() => runner.closeRunner());
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const opened = await request(origin, 'POST', '/v1/bundles/open', await standaloneBundle(), {
    'content-type': 'application/x-tar',
    'x-bruno-compatibility-version': '3.5.2'
  });
  assert.equal(opened.status, 201, opened.body.toString());
  const bundleID = JSON.parse(opened.body).bundle_id;
  const created = await request(origin, 'POST', '/v1/sessions', Buffer.from(JSON.stringify({
    bundle_id: bundleID,
    compatibility_version: '3.5.2',
    sandbox: 'safe',
    secrets: { API_TOKEN: 'retained-only-in-worker-memory' }
  })), { 'content-type': 'application/json' });
  assert.equal(created.status, 201, created.body.toString());
  const sessionID = JSON.parse(created.body).session_id;

  await new Promise((resolve) => setTimeout(resolve, 120));

  const capabilities = JSON.parse((await request(origin, 'GET', '/v1/capabilities')).body);
  assert.equal(capabilities.active_sessions, 0);
  const execute = await request(origin, 'POST', `/v1/sessions/${sessionID}/execute`, Buffer.from('{}'), {
    'content-type': 'application/json'
  });
  assert.equal(execute.status, 404, execute.body.toString());
  assert.equal((await request(origin, 'DELETE', `/v1/bundles/${bundleID}`)).status, 204);
});

test('backpressure waiter removes all listeners after either terminal event', async () => {
  const stream = new EventEmitter();
  stream.destroyed = false;
  stream.writableEnded = false;
  const waiting = waitForDrainOrClose(stream);
  assert.equal(stream.listenerCount('drain'), 1);
  assert.equal(stream.listenerCount('close'), 1);
  assert.equal(stream.listenerCount('error'), 1);
  stream.emit('drain');
	assert.equal(await waiting, true);
	assert.equal(stream.listenerCount('drain'), 0);
	assert.equal(stream.listenerCount('close'), 0);
	assert.equal(stream.listenerCount('error'), 0);

	const stalled = new EventEmitter();
	stalled.destroyed = false;
	stalled.writableEnded = false;
	assert.equal(await waitForDrainOrClose(stalled, 10), false);
	assert.equal(stalled.listenerCount('drain'), 0);
	assert.equal(stalled.listenerCount('close'), 0);
	assert.equal(stalled.listenerCount('error'), 0);
});

test('destroying session retains capacity until worker termination completes', async (t) => {
	const worker = new DeferredTerminateWorker();
	const runner = createRunnerServer({
		allowDirectEgress: true,
		maxSessions: 1,
		developerSessionFactory: async () => worker
	});
	await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
	t.after(async () => {
		worker.releaseTermination?.();
		await runner.closeRunner();
	});
	const origin = `http://127.0.0.1:${runner.address().port}`;
	const { bundleID, sessionID } = await createControlledSession(origin, worker);

	const deleting = request(origin, 'DELETE', `/v1/sessions/${sessionID}`);
	await waitUntil(() => worker.terminateStarted, 'session worker termination');
	let capabilities = JSON.parse((await request(origin, 'GET', '/v1/capabilities')).body);
	assert.equal(capabilities.active_sessions, 1, 'destroying session must remain capacity-accounted');
	const blockedReplacement = await request(origin, 'POST', '/v1/sessions', Buffer.from(JSON.stringify({
		bundle_id: bundleID,
		compatibility_version: '3.5.2',
		sandbox: 'developer'
	})), { 'content-type': 'application/json' });
	assert.equal(blockedReplacement.status, 429, blockedReplacement.body.toString());

	worker.releaseTermination();
	assert.equal((await within(deleting, 'tracked session deletion')).status, 204);
	capabilities = JSON.parse((await request(origin, 'GET', '/v1/capabilities')).body);
	assert.equal(capabilities.active_sessions, 0);
	assert.equal((await request(origin, 'DELETE', `/v1/bundles/${bundleID}`)).status, 204);
});

test('never-settling worker termination releases accounting and fails the runner closed', async (t) => {
  const worker = new ControlledWorker();
  worker.terminate = () => new Promise(() => {});
  const runner = createRunnerServer({
    allowDirectEgress: true,
    maxSessions: 1,
    workerTerminationTimeoutMS: 15,
    developerSessionFactory: async () => worker
  });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  t.after(() => runner.closeRunner());
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const { sessionID } = await createControlledSession(origin, worker);

  const deleted = await within(
    request(origin, 'DELETE', `/v1/sessions/${sessionID}`),
    'timed-out session deletion',
    500
  );
  assert.equal(deleted.status, 204, deleted.body.toString());
  const capabilities = JSON.parse((await request(origin, 'GET', '/v1/capabilities')).body);
  assert.equal(capabilities.active_sessions, 0);
  assert.equal(capabilities.cleanup_failed, true);
  assert.equal((await request(origin, 'GET', '/v1/health')).status, 503);
});

test('runner shutdown starts all bounded worker terminations concurrently', async () => {
  const workers = [new DeferredTerminateWorker(), new DeferredTerminateWorker(), new DeferredTerminateWorker()];
  const pendingWorkers = [...workers];
  const runner = createRunnerServer({
    allowDirectEgress: true,
    maxSessions: workers.length,
    developerSessionFactory: async () => pendingWorkers.shift()
  });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  const origin = `http://127.0.0.1:${runner.address().port}`;
  for (const worker of workers) await createControlledSession(origin, worker);

  const closing = runner.closeRunner();
  let observationError;
  try {
    await waitUntil(
      () => workers.every((worker) => worker.terminateStarted),
      'concurrent worker shutdown',
      250
    );
  } catch (error) {
    observationError = error;
  } finally {
    for (const worker of workers) worker.releaseTermination?.();
    await closing;
  }
  if (observationError) throw observationError;
});

test('session destruction aborts a never-settling pending write chain before terminating the worker', async () => {
	let handlerCalled = false;
	let aborted = false;
	let released = false;
	const worker = new ControlledWorker();
	worker.terminate = () => {
		assert.equal(aborted, true, 'pending response must be aborted before worker termination');
		worker.terminated = true;
		return Promise.resolve(0);
	};
	const session = {
		id: 'never-settling-session',
		worker,
		pending: new Map([['request-1', {
			handle: () => {
				handlerCalled = true;
				return new Promise(() => {});
			},
			abort: () => { aborted = true; }
		}]]),
		closed: false,
		expiring: false,
		expiryTimer: null
	};
	await within(destroySessionResources(
		session,
		'terminal cleanup',
		15,
		() => { released = true; }
	), 'bounded never-settling session destruction', 500);
	assert.equal(handlerCalled, true);
	assert.equal(aborted, true);
	assert.equal(worker.terminated, true);
	assert.equal(released, true);
});

test('runner exposes health and opens a standalone request bundle', async (t) => {
  const server = createRunnerServer();
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(() => server.closeRunner());
  const origin = `http://127.0.0.1:${server.address().port}`;

  const health = await request(origin, 'GET', '/v1/health');
  assert.equal(health.status, 200);
  assert.equal(JSON.parse(health.body).compatibility_version, '3.5.2');

  const bundle = await standaloneBundle();
  const opened = await request(origin, 'POST', '/v1/bundles/open', bundle, {
    'content-type': 'application/x-tar',
    'x-bruno-compatibility-version': '3.5.2'
  });
  assert.equal(opened.status, 201, opened.body.toString());
  const bundleID = JSON.parse(opened.body).bundle_id;
  const inspected = await request(origin, 'GET', `/v1/bundles/${bundleID}/inspect`);
  assert.equal(inspected.status, 200);
  assert.equal(JSON.parse(inspected.body).synthetic_collection, true);

  const deleted = await request(origin, 'DELETE', `/v1/bundles/${bundleID}`);
  assert.equal(deleted.status, 204);
});

test('runner rejects traversal entries even if upstream validation is bypassed', async (t) => {
  const server = createRunnerServer();
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(() => server.closeRunner());
  const origin = `http://127.0.0.1:${server.address().port}`;
  const pack = tar.pack();
  const chunks = [];
  pack.on('data', (chunk) => chunks.push(chunk));
  const done = new Promise((resolve) => pack.on('end', resolve));
  pack.entry({ name: '../escape.yml' }, 'bad');
  pack.finalize();
  await done;
  const response = await request(origin, 'POST', '/v1/bundles/open', Buffer.concat(chunks), {
    'content-type': 'application/x-tar'
  });
  assert.equal(response.status, 400);
  assert.match(JSON.parse(response.body).error, /unsafe Bruno archive path/);
});

test('Bruno 3.5.2 worker executes a standalone OpenCollection request', async (t) => {
  const target = http.createServer((_request, response) => {
    response.writeHead(200, { 'content-type': 'application/json' });
    response.end('{"ok":true}');
  });
  await new Promise((resolve) => target.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise((resolve) => target.close(resolve)));

  const runner = createRunnerServer({ allowDirectEgress: true });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  t.after(() => runner.closeRunner());
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const targetURL = `http://127.0.0.1:${target.address().port}/health`;

  const bundle = await standaloneBundle(targetURL);
  const opened = await request(origin, 'POST', '/v1/bundles/open', bundle, {
    'content-type': 'application/x-tar',
    'x-bruno-compatibility-version': '3.5.2'
  });
  assert.equal(opened.status, 201, opened.body.toString());
  const bundleID = JSON.parse(opened.body).bundle_id;

  const session = await request(
    origin,
    'POST',
    '/v1/sessions',
    Buffer.from(JSON.stringify({
      bundle_id: bundleID,
      compatibility_version: '3.5.2',
      sandbox: 'safe'
    })),
    { 'content-type': 'application/json' }
  );
  assert.equal(session.status, 201, session.body.toString());
  const sessionID = JSON.parse(session.body).session_id;

  const execution = await request(
    origin,
    'POST',
    `/v1/sessions/${sessionID}/execute`,
    Buffer.from(JSON.stringify({
      action: {
        name: 'Run standalone request',
        type: 'bruno_run',
        params: { selection: 'collection' }
      }
    })),
    { 'content-type': 'application/json', accept: 'application/x-ndjson' }
  );
  assert.equal(execution.status, 200, execution.body.toString());
  const events = execution.body.toString('utf8').trim().split('\n').map((line) => JSON.parse(line));
  const requestResult = events.find((event) => event.type === 'result' && event.result?.operation_kind === 'request');
  const aggregate = events.find((event) => event.type === 'complete');
  assert.equal(requestResult?.result?.status_code, 200, execution.body.toString());
  assert.equal(requestResult?.result?.success, true, execution.body.toString());
  assert.equal(aggregate?.result?.success, true, execution.body.toString());

  assert.equal((await request(origin, 'DELETE', `/v1/sessions/${sessionID}`)).status, 204);
  assert.equal((await request(origin, 'DELETE', `/v1/bundles/${bundleID}`)).status, 204);
});

test('governed bindings resolve only through the secret namespace', async (t) => {
  const received = {};
  const target = http.createServer((incoming, response) => {
    received.governed = incoming.headers['x-governed'];
    received.flat = incoming.headers['x-flat'];
    received.process = incoming.headers['x-process'];
    response.writeHead(204);
    response.end();
  });
  await new Promise((resolve) => target.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise((resolve) => target.close(resolve)));

  const runner = createRunnerServer({ allowDirectEgress: true });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  t.after(() => runner.closeRunner());
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const material = 'governed-runner-secret-material';
  const bundle = await tarBundle({
    'request.yml': `info:
  name: Namespaced secret
  type: http
  seq: 1
http:
  method: GET
  url: http://127.0.0.1:${target.address().port}/secret-scope
  headers:
    - name: X-Governed
      value: '{{secret.API_TOKEN}}'
    - name: X-Flat
      value: '{{API_TOKEN}}'
    - name: X-Process
      value: '{{process.env.API_TOKEN}}'
`
  });
  const { bundleID, sessionID } = await openSafeSession(origin, bundle, {
    secrets: { API_TOKEN: material }
  });
  const execution = await request(origin, 'POST', `/v1/sessions/${sessionID}/execute`, executePayload(), {
    'content-type': 'application/json',
    accept: 'application/x-ndjson'
  });
  assert.equal(execution.status, 200, execution.body.toString());
  assert.equal(received.governed, material);
  assert.notEqual(received.flat, material);
  assert.notEqual(received.process, material);

  assert.equal((await request(origin, 'DELETE', `/v1/sessions/${sessionID}`)).status, 204);
  assert.equal((await request(origin, 'DELETE', `/v1/bundles/${bundleID}`)).status, 204);
});

test('HTTP responses are rejected inside the worker before exceeding max_output_bytes', async (t) => {
  const target = http.createServer((_request, response) => {
    response.writeHead(200, { 'content-type': 'application/octet-stream' });
    response.end(Buffer.alloc(256 * 1024, 0x61));
  });
  await new Promise((resolve) => target.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise((resolve) => target.close(resolve)));
  const runner = createRunnerServer({ allowDirectEgress: true });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  t.after(() => runner.closeRunner());
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const { bundleID, sessionID } = await openSafeSession(
    origin,
    await standaloneBundle(`http://127.0.0.1:${target.address().port}/large`),
    { limits: { max_output_bytes: 4096 } }
  );
  const execution = await request(origin, 'POST', `/v1/sessions/${sessionID}/execute`, executePayload(), {
    'content-type': 'application/json',
    accept: 'application/x-ndjson'
  });
  assert.equal(execution.status, 200, execution.body.toString());
  assert.ok(execution.body.length <= 4096, `bounded execution emitted ${execution.body.length} bytes`);
  assert.match(execution.body.toString(), /configured output limit|type":"error|success":false/);
  assert.equal((await request(origin, 'DELETE', `/v1/sessions/${sessionID}`)).status, 204);
  assert.equal((await request(origin, 'DELETE', `/v1/bundles/${bundleID}`)).status, 204);
});

test('sub-event output limits end the stream without exceeding the byte envelope', async (t) => {
  const target = http.createServer((_request, response) => response.end());
  await new Promise((resolve) => target.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise((resolve) => target.close(resolve)));
  const runner = createRunnerServer({ allowDirectEgress: true });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  t.after(() => runner.closeRunner());
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const { bundleID, sessionID } = await openSafeSession(
    origin,
    await standaloneBundle(`http://127.0.0.1:${target.address().port}/tiny`),
    { limits: { max_output_bytes: 1 } }
  );
  const execution = await within(request(origin, 'POST', `/v1/sessions/${sessionID}/execute`, executePayload(), {
    'content-type': 'application/json', accept: 'application/x-ndjson'
  }), 'sub-event bounded stream', 3000);
  assert.equal(execution.status, 200, execution.body.toString());
  assert.ok(execution.body.length <= 1, `bounded execution emitted ${execution.body.length} bytes`);
  assert.equal((await request(origin, 'DELETE', `/v1/sessions/${sessionID}`)).status, 204);
  assert.equal((await request(origin, 'DELETE', `/v1/bundles/${bundleID}`)).status, 204);
});

test('HTTP target policy blocks redirects before custom credential headers reach another host', async (t) => {
  let redirectedRequests = 0;
  let redirectedCredential = false;
  const destination = http.createServer((request, response) => {
    redirectedRequests += 1;
    redirectedCredential = Boolean(request.headers['x-api-key']);
    response.end('unexpected');
  });
  await new Promise((resolve) => destination.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise((resolve) => destination.close(resolve)));
  const source = http.createServer((_request, response) => {
    response.writeHead(302, {
      location: `http://localhost:${destination.address().port}/capture`
    });
    response.end();
  });
  await new Promise((resolve) => source.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise((resolve) => source.close(resolve)));

  const bundle = await tarBundle({
    'redirect.yml': `info:
  name: Redirect request
  type: http
  seq: 1
http:
  method: GET
  url: http://127.0.0.1:${source.address().port}/redirect
  headers:
    - name: X-API-Key
      value: redirect-policy-test-material
`
  });
  const runner = createRunnerServer({ allowDirectEgress: true });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  t.after(() => runner.closeRunner());
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const { bundleID, sessionID } = await openSafeSession(origin, bundle, {
    allowed_target_hosts: ['127.0.0.1']
  });
  const execution = await request(origin, 'POST', `/v1/sessions/${sessionID}/execute`, executePayload(), {
    'content-type': 'application/json', accept: 'application/x-ndjson'
  });
  assert.equal(execution.status, 200, execution.body.toString());
  assert.equal(redirectedRequests, 0);
  assert.equal(redirectedCredential, false);
  assert.equal((await request(origin, 'DELETE', `/v1/sessions/${sessionID}`)).status, 204);
  assert.equal((await request(origin, 'DELETE', `/v1/bundles/${bundleID}`)).status, 204);
});

test('target policy blocks collection response hooks before secondary requests can disclose secrets', async (t) => {
  let secondaryRequests = 0;
  let secondaryCredential = false;
  const destination = http.createServer((incoming, response) => {
    secondaryRequests += 1;
    secondaryCredential = incoming.headers['x-api-key'] === 'collection-hook-policy-material';
    response.end('unexpected');
  });
  await new Promise((resolve) => destination.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise((resolve) => destination.close(resolve)));

  let primaryRequests = 0;
  const primary = http.createServer((_incoming, response) => {
    primaryRequests += 1;
    response.end('ok');
  });
  await new Promise((resolve) => primary.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise((resolve) => primary.close(resolve)));

  const bundle = await tarBundle({
    'collection.bru': `meta {
  name: Policy collection hooks
  type: collection
}

script:post-response {
  await bru.sendRequest({
    method: "GET",
    url: "http://localhost:${destination.address().port}/capture",
    headers: { "X-API-Key": "collection-hook-policy-material" }
  });
}
`,
    'primary.bru': `meta {
  name: Primary request
  type: http
  seq: 1
}

get {
  url: http://127.0.0.1:${primary.address().port}/allowed
  body: none
  auth: none
}
`
  });
  const runner = createRunnerServer({ allowDirectEgress: true });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  t.after(() => runner.closeRunner());
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const { bundleID, sessionID } = await openSafeSession(origin, bundle, {
    allowed_target_hosts: ['127.0.0.1'],
    secrets: { API_TOKEN: 'collection-hook-policy-material' }
  });
  const execution = await request(origin, 'POST', `/v1/sessions/${sessionID}/execute`, executePayload(), {
    'content-type': 'application/json', accept: 'application/x-ndjson'
  });
  assert.equal(execution.status, 200, execution.body.toString());
  assert.match(execution.body.toString(), /blocks executable Bruno scripts/);
  assert.equal(primaryRequests, 0, 'policy rejection must occur before primary I/O');
  assert.equal(secondaryRequests, 0);
  assert.equal(secondaryCredential, false);
  assert.equal((await request(origin, 'DELETE', `/v1/sessions/${sessionID}`)).status, 204);
  assert.equal((await request(origin, 'DELETE', `/v1/bundles/${bundleID}`)).status, 204);
});

test('OAuth token redirects cannot forward client credentials outside the target policy', async (t) => {
  let redirectedRequests = 0;
  let redirectedCredential = false;
  const destination = http.createServer((incoming, response) => {
    const chunks = [];
    incoming.on('data', (chunk) => chunks.push(chunk));
    incoming.on('end', () => {
      redirectedRequests += 1;
      const material = `${incoming.headers.authorization || ''}\n${Buffer.concat(chunks).toString()}`;
      redirectedCredential = material.includes('oauth-redirect-policy-material');
      response.end('{"access_token":"unexpected"}');
    });
  });
  await new Promise((resolve) => destination.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise((resolve) => destination.close(resolve)));

  let tokenRequests = 0;
  const tokenSource = http.createServer((_incoming, response) => {
    tokenRequests += 1;
    response.writeHead(307, {
      location: `http://localhost:${destination.address().port}/capture`
    });
    response.end();
  });
  await new Promise((resolve) => tokenSource.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise((resolve) => tokenSource.close(resolve)));

  const primary = http.createServer((_incoming, response) => response.end('ok'));
  await new Promise((resolve) => primary.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise((resolve) => primary.close(resolve)));

  const bundle = await tarBundle({
    'collection.bru': 'meta {\n  name: OAuth redirect policy\n  type: collection\n}\n',
    'oauth.bru': `meta {
  name: OAuth request
  type: http
  seq: 1
}

get {
  url: http://127.0.0.1:${primary.address().port}/allowed
  body: none
  auth: oauth2
}

auth:oauth2 {
  grant_type: client_credentials
  access_token_url: http://127.0.0.1:${tokenSource.address().port}/token
  refresh_token_url:
  client_id: apexload-client
  client_secret: {{API_TOKEN}}
  scope:
  credentials_placement: body
  credentials_id: apexload
  token_source: access_token
  token_placement: header
  token_header_prefix: Bearer
  auto_fetch_token: true
  auto_refresh_token: false
}
`
  });
  const runner = createRunnerServer({ allowDirectEgress: true });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  t.after(() => runner.closeRunner());
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const { bundleID, sessionID } = await openSafeSession(origin, bundle, {
    allowed_target_hosts: ['127.0.0.1'],
    secrets: { API_TOKEN: 'oauth-redirect-policy-material' }
  });
  const execution = await request(origin, 'POST', `/v1/sessions/${sessionID}/execute`, executePayload(), {
    'content-type': 'application/json', accept: 'application/x-ndjson'
  });
  assert.equal(execution.status, 200, execution.body.toString());
  assert.equal(tokenRequests, 1, execution.body.toString());
  assert.equal(redirectedRequests, 0);
  assert.equal(redirectedCredential, false);
  assert.equal((await request(origin, 'DELETE', `/v1/sessions/${sessionID}`)).status, 204);
  assert.equal((await request(origin, 'DELETE', `/v1/bundles/${bundleID}`)).status, 204);
});

test('global and request-scoped cancel abort active HTTP transport work', async (t) => {
  let received = 0;
  let aborted = 0;
  const target = http.createServer((_request, response) => {
    received += 1;
    response.on('close', () => {
      if (!response.writableEnded) aborted += 1;
    });
    // Intentionally never respond. Cancellation must close the active client
    // request rather than merely setting a loop flag in the worker.
  });
  await new Promise((resolve) => target.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise((resolve) => target.close(resolve)));

  const runner = createRunnerServer({ allowDirectEgress: true });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  t.after(() => runner.closeRunner());
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const opened = await request(
    origin,
    'POST',
    '/v1/bundles/open',
    await standaloneBundle(`http://127.0.0.1:${target.address().port}/held`),
    { 'content-type': 'application/x-tar', 'x-bruno-compatibility-version': '3.5.2' }
  );
  assert.equal(opened.status, 201, opened.body.toString());
  const bundleID = JSON.parse(opened.body).bundle_id;
  const createSession = async () => {
    const created = await request(origin, 'POST', '/v1/sessions', Buffer.from(JSON.stringify({
      bundle_id: bundleID,
      compatibility_version: '3.5.2',
      sandbox: 'safe'
    })), { 'content-type': 'application/json' });
    assert.equal(created.status, 201, created.body.toString());
    return JSON.parse(created.body).session_id;
  };

  const globalSessionID = await createSession();
  const globalExecution = await beginExecution(origin, globalSessionID);
  const globalBody = collectResponse(globalExecution.response);
  await waitUntil(() => received === 1, 'globally cancelled target request');
  const cancelled = await request(origin, 'POST', `/v1/sessions/${globalSessionID}/cancel`);
  assert.equal(cancelled.status, 204, cancelled.body.toString());
  await waitUntil(() => aborted === 1, 'global transport abort');
  const globalTerminal = await within(globalBody, 'global cancellation terminal event', 5000);
  assert.match(globalTerminal.toString(), /"type":"(complete|error)"/);

  const scopedSessionID = await createSession();
  const scopedExecution = await beginExecution(origin, scopedSessionID);
  scopedExecution.response.on('error', () => {});
  await waitUntil(() => received === 2, 'request-scoped cancelled target request');
  scopedExecution.response.destroy();
  await waitUntil(() => aborted === 2, 'request-scoped transport abort');

  assert.equal((await request(origin, 'DELETE', `/v1/sessions/${globalSessionID}`)).status, 204);
  assert.equal((await request(origin, 'DELETE', `/v1/sessions/${scopedSessionID}`)).status, 204);
  assert.equal((await request(origin, 'DELETE', `/v1/bundles/${bundleID}`)).status, 204);
});

test('Bruno worker executes a classic collection.bru rooted bundle', async (t) => {
  const target = http.createServer((_request, response) => {
    response.writeHead(204);
    response.end();
  });
  await new Promise((resolve) => target.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise((resolve) => target.close(resolve)));

  const runner = createRunnerServer({ allowDirectEgress: true });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  t.after(() => runner.closeRunner());
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const bundle = await tarBundle({
    'collection.bru': 'meta {\n  name: Classic collection\n}\n',
    'health.bru': `meta {
  name: Health
  type: http
  seq: 1
}

get {
  url: http://127.0.0.1:${target.address().port}/health
  body: none
  auth: none
}
`
  });
  const opened = await request(origin, 'POST', '/v1/bundles/open', bundle, {
    'content-type': 'application/x-tar',
    'x-bruno-compatibility-version': '3.5.2'
  });
  assert.equal(opened.status, 201, opened.body.toString());
  const bundleID = JSON.parse(opened.body).bundle_id;
  const inspected = JSON.parse((await request(origin, 'GET', `/v1/bundles/${bundleID}/inspect`)).body);
  assert.equal(inspected.synthetic_collection, true);
  const session = await request(origin, 'POST', '/v1/sessions', Buffer.from(JSON.stringify({
    bundle_id: bundleID,
    compatibility_version: '3.5.2',
    sandbox: 'safe'
  })), { 'content-type': 'application/json' });
  assert.equal(session.status, 201, session.body.toString());
  const sessionID = JSON.parse(session.body).session_id;
  const execution = await request(origin, 'POST', `/v1/sessions/${sessionID}/execute`, Buffer.from(JSON.stringify({
    action: { name: 'Run classic collection', type: 'bruno_run', params: { selection: 'collection' } }
  })), { 'content-type': 'application/json', accept: 'application/x-ndjson' });
  assert.equal(execution.status, 200, execution.body.toString());
  const events = execution.body.toString('utf8').trim().split('\n').map((line) => JSON.parse(line));
  const result = events.find((event) => event.type === 'result' && event.result?.operation_kind === 'request');
  assert.equal(result?.result?.status_code, 204, execution.body.toString());
  assert.equal(events.find((event) => event.type === 'complete')?.result?.success, true, execution.body.toString());
});

test('worker honors Bruno environments, global environments, scripts, and bounded data iterations', async (t) => {
  const received = [];
  const target = http.createServer((incoming, response) => {
    received.push({
      url: incoming.url,
      preRequestHeader: incoming.headers['x-pre-request']
    });
    response.writeHead(200, { 'content-type': 'application/json' });
    response.end('{"ok":true}');
  });
  await new Promise((resolve) => target.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise((resolve) => target.close(resolve)));

  const runner = createRunnerServer({ allowDirectEgress: true });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  t.after(() => runner.closeRunner());
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const targetURL = `http://127.0.0.1:${target.address().port}/data`;
  const bundle = await tarBundle({
    'request.yml': `info:
  name: Data request
  type: http
  seq: 1
http:
  method: GET
  url: "${targetURL}?env={{envValue}}&global={{globalValue}}&row={{row}}"
runtime:
  scripts:
    - type: before-request
      code: |-
        req.setHeader("x-pre-request", "yes");
`,
    'environments/test.json': JSON.stringify({
      name: 'test',
      variables: [{ name: 'envValue', value: 'environment', enabled: true }]
    }),
    'global-environments/shared.json': JSON.stringify({
      name: 'shared',
      variables: [{ name: 'globalValue', value: 'global', enabled: true }]
    }),
    'data.csv': 'row\nalpha\nbeta\n',
    'empty.csv': 'row\n'
  });
  const opened = await request(origin, 'POST', '/v1/bundles/open', bundle, {
    'content-type': 'application/x-tar',
    'x-bruno-compatibility-version': '3.5.2'
  });
  assert.equal(opened.status, 201, opened.body.toString());
  const bundleID = JSON.parse(opened.body).bundle_id;

  const session = await request(
    origin,
    'POST',
    '/v1/sessions',
    Buffer.from(JSON.stringify({
      bundle_id: bundleID,
      compatibility_version: '3.5.2',
      sandbox: 'safe',
      limits: { max_parallel_requests: 2 }
    })),
    { 'content-type': 'application/json' }
  );
  assert.equal(session.status, 201, session.body.toString());
  const sessionID = JSON.parse(session.body).session_id;

  const execution = await request(
    origin,
    'POST',
    `/v1/sessions/${sessionID}/execute`,
    Buffer.from(JSON.stringify({
      action: {
        name: 'Run data rows',
        type: 'bruno_run',
        params: {
          selection: 'collection',
          environment: 'test',
          global_environment: 'shared',
          data_source: 'data.csv',
          execution: 'parallel',
          max_parallel: 2
        }
      }
    })),
    { 'content-type': 'application/json', accept: 'application/x-ndjson' }
  );
  assert.equal(execution.status, 200, execution.body.toString());
  const events = execution.body.toString('utf8').trim().split('\n').map((line) => JSON.parse(line));
  const requestResults = events.filter((event) => event.type === 'result' && event.result?.operation_kind === 'request');
  const aggregate = events.find((event) => event.type === 'complete');
  assert.equal(requestResults.length, 2, execution.body.toString());
  assert.deepEqual(
    requestResults.map((event) => event.result.evidence.iteration_index).sort(),
    [0, 1]
  );
  assert.equal(aggregate?.result?.success, true, execution.body.toString());
  assert.equal(aggregate?.result?.evidence?.data_iterations, 2);
  assert.equal(aggregate?.result?.evidence?.executed_iterations, 2);
  assert.deepEqual(
    received.map((entry) => entry.url).sort(),
    [
      '/data?env=environment&global=global&row=alpha',
      '/data?env=environment&global=global&row=beta'
    ]
  );
  assert.deepEqual(received.map((entry) => entry.preRequestHeader), ['yes', 'yes']);

  const emptyExecution = await request(
    origin,
    'POST',
    `/v1/sessions/${sessionID}/execute`,
    Buffer.from(JSON.stringify({
      action: { name: 'Run empty data', type: 'bruno_run', params: { selection: 'collection', data_source: 'empty.csv' } }
    })),
    { 'content-type': 'application/json', accept: 'application/x-ndjson' }
  );
  assert.equal(emptyExecution.status, 200, emptyExecution.body.toString());
  const emptyEvents = emptyExecution.body.toString('utf8').trim().split('\n').map((line) => JSON.parse(line));
  const emptyError = emptyEvents.find((event) => event.type === 'error');
  assert.match(emptyError?.error || '', /no executable rows/);

  assert.equal((await request(origin, 'DELETE', `/v1/sessions/${sessionID}`)).status, 204);
  assert.equal((await request(origin, 'DELETE', `/v1/bundles/${bundleID}`)).status, 204);
});

test('safe sessions fail closed when no governed egress broker is configured', async (t) => {
  const runner = createRunnerServer();
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  t.after(() => runner.closeRunner());
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const bundle = await standaloneBundle();
  const opened = await request(origin, 'POST', '/v1/bundles/open', bundle, {
    'content-type': 'application/x-tar',
    'x-bruno-compatibility-version': '3.5.2'
  });
  assert.equal(opened.status, 201, opened.body.toString());
  const bundleID = JSON.parse(opened.body).bundle_id;
  const session = await request(
    origin,
    'POST',
    '/v1/sessions',
    Buffer.from(JSON.stringify({
      bundle_id: bundleID,
      compatibility_version: '3.5.2',
      sandbox: 'safe'
    })),
    { 'content-type': 'application/json' }
  );
  assert.equal(session.status, 503, session.body.toString());
  assert.match(JSON.parse(session.body).error, /governed egress broker/);
  assert.equal((await request(origin, 'DELETE', `/v1/bundles/${bundleID}`)).status, 204);
});

test('runner enforces persistent session capacity without leaking released slots', async (t) => {
  const runner = createRunnerServer({ allowDirectEgress: true, maxSessions: 1 });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  t.after(() => runner.closeRunner());
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const opened = await request(origin, 'POST', '/v1/bundles/open', await standaloneBundle(), {
    'content-type': 'application/x-tar',
    'x-bruno-compatibility-version': '3.5.2'
  });
  assert.equal(opened.status, 201, opened.body.toString());
  const bundleID = JSON.parse(opened.body).bundle_id;
  const sessionPayload = Buffer.from(JSON.stringify({
    bundle_id: bundleID,
    compatibility_version: '3.5.2',
    sandbox: 'safe'
  }));
  const first = await request(origin, 'POST', '/v1/sessions', sessionPayload, { 'content-type': 'application/json' });
  assert.equal(first.status, 201, first.body.toString());
  const firstID = JSON.parse(first.body).session_id;
  const full = await request(origin, 'POST', '/v1/sessions', sessionPayload, { 'content-type': 'application/json' });
  assert.equal(full.status, 429, full.body.toString());
  assert.match(JSON.parse(full.body).error, /capacity 1 is exhausted/);
  const capabilities = JSON.parse((await request(origin, 'GET', '/v1/capabilities')).body);
  assert.equal(capabilities.session_capacity, 1);
  assert.equal(capabilities.active_sessions, 1);

  assert.equal((await request(origin, 'DELETE', `/v1/sessions/${firstID}`)).status, 204);
  const replacement = await request(origin, 'POST', '/v1/sessions', sessionPayload, { 'content-type': 'application/json' });
  assert.equal(replacement.status, 201, replacement.body.toString());
  const replacementID = JSON.parse(replacement.body).session_id;
  assert.equal((await request(origin, 'DELETE', `/v1/sessions/${replacementID}`)).status, 204);
  assert.equal((await request(origin, 'DELETE', `/v1/bundles/${bundleID}`)).status, 204);
});

test('client session IDs make concurrent create retries idempotent', async (t) => {
  const worker = new ControlledWorker();
  let factoryCalls = 0;
  let releaseFactory;
  const factoryGate = new Promise((resolve) => { releaseFactory = resolve; });
  const runner = createRunnerServer({
    allowDirectEgress: true,
    developerSessionFactory: async () => {
      factoryCalls += 1;
      await factoryGate;
      return worker;
    }
  });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  t.after(() => runner.closeRunner());
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const opened = await request(origin, 'POST', '/v1/bundles/open', await standaloneBundle(), {
    'content-type': 'application/x-tar',
    'x-bruno-compatibility-version': '3.5.2'
  });
  const bundleID = JSON.parse(opened.body).bundle_id;
  const sessionID = 'session-0123456789abcdef0123456789abcdef';
  const payload = Buffer.from(JSON.stringify({
    session_id: sessionID,
    bundle_id: bundleID,
    compatibility_version: '3.5.2',
    sandbox: 'developer'
  }));
  const first = request(origin, 'POST', '/v1/sessions', payload, { 'content-type': 'application/json' });
  await waitUntil(() => factoryCalls === 1, 'first idempotent session factory');
  const retry = request(origin, 'POST', '/v1/sessions', payload, { 'content-type': 'application/json' });
  releaseFactory();
  const [firstResponse, retryResponse] = await Promise.all([first, retry]);
  assert.equal(firstResponse.status, 201, firstResponse.body.toString());
  assert.equal(retryResponse.status, 201, retryResponse.body.toString());
  assert.equal(JSON.parse(firstResponse.body).session_id, sessionID);
  assert.equal(JSON.parse(retryResponse.body).session_id, sessionID);
  assert.equal(factoryCalls, 1);
  const altered = await request(origin, 'POST', '/v1/sessions', Buffer.from(JSON.stringify({
    session_id: sessionID,
    bundle_id: bundleID,
    compatibility_version: '3.5.2',
    sandbox: 'developer',
    secrets: { API_TOKEN: 'altered-retry-material' }
  })), { 'content-type': 'application/json' });
  assert.equal(altered.status, 409, altered.body.toString());
  assert.equal(factoryCalls, 1);
  assert.equal((await request(origin, 'DELETE', `/v1/sessions/${sessionID}`)).status, 204);
  assert.equal((await request(origin, 'DELETE', `/v1/bundles/${bundleID}`)).status, 204);
});

test('client-known ID can cancel a session while its worker is still provisioning', async (t) => {
  const worker = new ControlledWorker();
  let factoryEntered = false;
  let releaseFactory;
  const factoryGate = new Promise((resolve) => { releaseFactory = resolve; });
  const runner = createRunnerServer({
    allowDirectEgress: true,
    developerSessionFactory: async () => {
      factoryEntered = true;
      await factoryGate;
      return worker;
    }
  });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  t.after(() => runner.closeRunner());
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const opened = await request(origin, 'POST', '/v1/bundles/open', await standaloneBundle(), {
    'content-type': 'application/x-tar',
    'x-bruno-compatibility-version': '3.5.2'
  });
  const bundleID = JSON.parse(opened.body).bundle_id;
  const sessionID = 'session-fedcba9876543210fedcba9876543210';
  const creating = request(origin, 'POST', '/v1/sessions', Buffer.from(JSON.stringify({
    session_id: sessionID,
    bundle_id: bundleID,
    compatibility_version: '3.5.2',
    sandbox: 'developer'
  })), { 'content-type': 'application/json' });
  await waitUntil(() => factoryEntered, 'provisioning session placeholder');
  assert.equal((await request(origin, 'DELETE', `/v1/sessions/${sessionID}`)).status, 204);
  releaseFactory();
  const createResponse = await creating;
  assert.equal(createResponse.status, 409, createResponse.body.toString());
  await waitUntil(() => worker.terminated, 'late provisioned worker termination');
  const capabilities = JSON.parse((await request(origin, 'GET', '/v1/capabilities')).body);
  assert.equal(capabilities.active_sessions, 0);
  assert.equal((await request(origin, 'DELETE', `/v1/bundles/${bundleID}`)).status, 204);
});

test('developer worker provisioning deadline releases its placeholder capacity', async (t) => {
  const runner = createRunnerServer({
    allowDirectEgress: true,
    workerProvisionTimeoutMS: 15,
    developerSessionFactory: () => new Promise(() => {})
  });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  t.after(() => runner.closeRunner());
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const opened = await request(origin, 'POST', '/v1/bundles/open', await standaloneBundle(), {
    'content-type': 'application/x-tar',
    'x-bruno-compatibility-version': '3.5.2'
  });
  const bundleID = JSON.parse(opened.body).bundle_id;
  const sessionID = 'session-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
  const created = await within(request(origin, 'POST', '/v1/sessions', Buffer.from(JSON.stringify({
    session_id: sessionID,
    bundle_id: bundleID,
    compatibility_version: '3.5.2',
    sandbox: 'developer'
  })), { 'content-type': 'application/json' }), 'bounded developer worker provisioning', 500);
  assert.equal(created.status, 504, created.body.toString());
  const capabilities = JSON.parse((await request(origin, 'GET', '/v1/capabilities')).body);
  assert.equal(capabilities.active_sessions, 0);
  assert.equal((await request(origin, 'DELETE', `/v1/bundles/${bundleID}`)).status, 204);
});

test('invalid developer workers are rejected without retaining session capacity', async (t) => {
  let terminated = false;
  const runner = createRunnerServer({
    allowDirectEgress: true,
    developerSessionFactory: async () => ({
      terminate: async () => { terminated = true; }
    })
  });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  t.after(() => runner.closeRunner());
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const opened = await request(origin, 'POST', '/v1/bundles/open', await standaloneBundle(), {
    'content-type': 'application/x-tar',
    'x-bruno-compatibility-version': '3.5.2'
  });
  const bundleID = JSON.parse(opened.body).bundle_id;
  const created = await request(origin, 'POST', '/v1/sessions', Buffer.from(JSON.stringify({
    bundle_id: bundleID,
    compatibility_version: '3.5.2',
    sandbox: 'developer'
  })), { 'content-type': 'application/json' });
  assert.equal(created.status, 500, created.body.toString());
  assert.equal(terminated, true);
  const capabilities = JSON.parse((await request(origin, 'GET', '/v1/capabilities')).body);
  assert.equal(capabilities.active_sessions, 0);
  assert.equal((await request(origin, 'DELETE', `/v1/bundles/${bundleID}`)).status, 204);
});

test('WebSocket collection execution uses the governed CONNECT broker and waits for source close', async (t) => {
  const target = http.createServer();
  const sockets = new WebSocketServer({ server: target });
  sockets.on('connection', (socket) => {
    socket.on('message', (message) => {
      socket.send(`echo:${message.toString()}`);
      socket.close(1000, 'done');
    });
  });
  await new Promise((resolve) => target.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise((resolve) => sockets.close(() => target.close(resolve))));

  let proxyConnects = 0;
  const proxy = http.createServer();
  proxy.on('connect', (incoming, client, head) => {
    proxyConnects += 1;
    const separator = incoming.url.lastIndexOf(':');
    const host = incoming.url.slice(0, separator);
    const port = Number(incoming.url.slice(separator + 1));
    const upstream = net.connect(port, host, () => {
      client.write('HTTP/1.1 200 Connection Established\r\n\r\n');
      if (head.length > 0) upstream.write(head);
      upstream.pipe(client);
      client.pipe(upstream);
    });
    upstream.on('error', () => client.destroy());
  });
  await new Promise((resolve) => proxy.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise((resolve) => proxy.close(resolve)));

  const runner = createRunnerServer({
    egressBrokerURL: `http://127.0.0.1:${proxy.address().port}`
  });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  t.after(() => runner.closeRunner());
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const bundle = await tarBundle({
    'socket.yml': `info:
  name: Echo socket
  type: websocket
  seq: 1
websocket:
  url: ws://127.0.0.1:${target.address().port}/echo
  message:
    type: text
    data: hello
settings:
  timeout: 2000
`
  });
  const opened = await request(origin, 'POST', '/v1/bundles/open', bundle, {
    'content-type': 'application/x-tar',
    'x-bruno-compatibility-version': '3.5.2'
  });
  assert.equal(opened.status, 201, opened.body.toString());
  const bundleID = JSON.parse(opened.body).bundle_id;
  const session = await request(
    origin,
    'POST',
    '/v1/sessions',
    Buffer.from(JSON.stringify({
      bundle_id: bundleID,
      compatibility_version: '3.5.2',
      sandbox: 'safe'
    })),
    { 'content-type': 'application/json' }
  );
  assert.equal(session.status, 201, session.body.toString());
  const sessionID = JSON.parse(session.body).session_id;
  const execution = await request(
    origin,
    'POST',
    `/v1/sessions/${sessionID}/execute`,
    Buffer.from(JSON.stringify({
      action: {
        name: 'Run echo socket',
        type: 'bruno_run',
        params: { selection: 'collection', stream_timeout_ms: 2000 }
      }
    })),
    { 'content-type': 'application/json', accept: 'application/x-ndjson' }
  );
  assert.equal(execution.status, 200, execution.body.toString());
  const events = execution.body.toString('utf8').trim().split('\n').map((line) => JSON.parse(line));
  const stream = events.find((event) => event.type === 'result' && event.result?.request_type === 'websocket');
  const aggregate = events.find((event) => event.type === 'complete');
  assert.equal(stream?.result?.status_code, 101, execution.body.toString());
  assert.equal(stream?.result?.success, true, execution.body.toString());
  assert.match(stream?.result?.response_body || '', /echo:hello/);
  assert.equal(stream?.result?.evidence?.close_code, 1000);
  assert.equal(aggregate?.result?.success, true, execution.body.toString());
  assert.equal(proxyConnects, 1);

  assert.equal((await request(origin, 'DELETE', `/v1/sessions/${sessionID}`)).status, 204);
  assert.equal((await request(origin, 'DELETE', `/v1/bundles/${bundleID}`)).status, 204);
});

test('WebSocket target policy refuses cross-host redirects with custom credential headers', async (t) => {
  const destinationServer = http.createServer();
  const destinationSockets = new WebSocketServer({ server: destinationServer });
  let destinationConnections = 0;
  let destinationCredential = false;
  destinationSockets.on('connection', (socket, request) => {
    destinationConnections += 1;
    destinationCredential = Boolean(request.headers['x-api-key']);
    socket.close();
  });
  await new Promise((resolve) => destinationServer.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise((resolve) => destinationSockets.close(() => destinationServer.close(resolve))));

  const redirectServer = http.createServer((_request, response) => {
    response.writeHead(302, {
      location: `ws://localhost:${destinationServer.address().port}/capture`
    });
    response.end();
  });
  await new Promise((resolve) => redirectServer.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise((resolve) => redirectServer.close(resolve)));
  const bundle = await tarBundle({
    'redirect-socket.yml': `info:
  name: Redirect socket
  type: websocket
  seq: 1
websocket:
  url: ws://127.0.0.1:${redirectServer.address().port}/redirect
  headers:
    - name: X-API-Key
      value: websocket-redirect-policy-material
  message:
    type: text
    data: hello
settings:
  timeout: 1000
`
  });
  const runner = createRunnerServer({ allowDirectEgress: true });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  t.after(() => runner.closeRunner());
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const { bundleID, sessionID } = await openSafeSession(origin, bundle, {
    allowed_target_hosts: ['127.0.0.1']
  });
  const execution = await request(origin, 'POST', `/v1/sessions/${sessionID}/execute`, executePayload(), {
    'content-type': 'application/json', accept: 'application/x-ndjson'
  });
  assert.equal(execution.status, 200, execution.body.toString());
  assert.equal(destinationConnections, 0);
  assert.equal(destinationCredential, false);
  assert.equal((await request(origin, 'DELETE', `/v1/sessions/${sessionID}`)).status, 204);
  assert.equal((await request(origin, 'DELETE', `/v1/bundles/${bundleID}`)).status, 204);
});

test('WebSocket cumulative messages are cancelled at max_output_bytes', async (t) => {
  const target = http.createServer();
  const sockets = new WebSocketServer({ server: target });
  sockets.on('connection', (socket) => {
    socket.on('error', () => {});
    for (let index = 0; index < 32; index += 1) {
      socket.send('x'.repeat(256));
    }
  });
  await new Promise((resolve) => target.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise((resolve) => sockets.close(() => target.close(resolve))));
  const bundle = await tarBundle({
    'bounded-socket.yml': `info:
  name: Bounded socket
  type: websocket
  seq: 1
websocket:
  url: ws://127.0.0.1:${target.address().port}/stream
  message:
    type: text
    data: hello
settings:
  timeout: 2000
`
  });
  const runner = createRunnerServer({ allowDirectEgress: true });
  await new Promise((resolve) => runner.listen(0, '127.0.0.1', resolve));
  t.after(() => runner.closeRunner());
  const origin = `http://127.0.0.1:${runner.address().port}`;
  const { bundleID, sessionID } = await openSafeSession(origin, bundle, {
    limits: { max_output_bytes: 2048, default_stream_timeout_ms: 2000 }
  });
  const execution = await request(origin, 'POST', `/v1/sessions/${sessionID}/execute`, executePayload(), {
    'content-type': 'application/json', accept: 'application/x-ndjson'
  });
  assert.equal(execution.status, 200, execution.body.toString());
  assert.ok(execution.body.length <= 2048, `bounded execution emitted ${execution.body.length} bytes`);
  assert.match(execution.body.toString(), /configured output limit|type":"error|success":false/);
  assert.equal((await request(origin, 'DELETE', `/v1/sessions/${sessionID}`)).status, 204);
  assert.equal((await request(origin, 'DELETE', `/v1/bundles/${bundleID}`)).status, 204);
});
