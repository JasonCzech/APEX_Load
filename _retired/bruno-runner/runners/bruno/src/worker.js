'use strict';

const fs = require('node:fs');
const path = require('node:path');
const { parentPort, workerData } = require('node:worker_threads');
const collectionPath = workerData.collectionPath;
const config = workerData.config || {};
const DEFAULT_MAX_OUTPUT_BYTES = 4 * 1024 * 1024;
const ABSOLUTE_MAX_OUTPUT_BYTES = 1024 * 1024 * 1024;
const configuredOutputBytes = Number(config.limits?.max_output_bytes ?? DEFAULT_MAX_OUTPUT_BYTES);
const maxOutputBytes = Number.isSafeInteger(configuredOutputBytes) && configuredOutputBytes > 0
  ? Math.min(configuredOutputBytes, ABSOLUTE_MAX_OUTPUT_BYTES)
  : DEFAULT_MAX_OUTPUT_BYTES;
const { cloneDeep } = require('lodash');
const { parse: parseCSV } = require('csv-parse/sync');
const decomment = require('decomment');
const { HttpsProxyAgent } = require('https-proxy-agent');
const { SocksProxyAgent } = require('socks-proxy-agent');
const WebSocket = require('ws');
const { isRequestTagsIncluded } = require('@usebruno/common');
const { parseDotEnv, parseEnvironment } = require('@usebruno/filestore');
const {
  ScriptRuntime,
  TestRuntime,
  VarsRuntime,
  AssertRuntime
} = require('@usebruno/js');
const brunoRequests = require('@usebruno/requests');
const baseMakeAxiosInstance = brunoRequests.makeAxiosInstance;
// Bruno 3.5.2 passes `requestMaxRedirects` to the generic OAuth Axios factory,
// but Axios only honors `maxRedirects`. Normalize the pinned adapter contract
// before run-single-request captures the export; otherwise a token endpoint can
// replay client credentials through a 307/308 despite primary redirects being
// disabled. The response ceiling also applies to token responses.
brunoRequests.makeAxiosInstance = (options = {}) => {
  const configuredRedirects = options.requestMaxRedirects;
  const normalized = { ...options, maxContentLength: maxOutputBytes };
  delete normalized.requestMaxRedirects;
  if (Number.isSafeInteger(configuredRedirects) && configuredRedirects >= 0) {
    normalized.maxRedirects = configuredRedirects;
  }
  return baseMakeAxiosInstance(normalized);
};
const { cookies, GrpcClient } = brunoRequests;
const {
  createCollectionJsonFromPathname,
  findItemInCollection,
  getCallStack,
  FORMAT_CONFIG
} = require('@usebruno/cli/src/utils/collection');
// Bruno's pinned request runner forwards the object returned by prepareRequest
// directly to Axios. Install a worker-local wrapper before loading
// run-single-request so Axios rejects an HTTP response while reading it rather
// than buffering beyond the governed output limit first.
const prepareRequestModule = require.resolve('@usebruno/cli/src/runner/prepare-request');
const basePrepareRequest = require(prepareRequestModule);
const prepareRequest = async (...args) => {
  const request = await basePrepareRequest(...args);
  request.maxContentLength = maxOutputBytes;
  return request;
};
require.cache[prepareRequestModule].exports = prepareRequest;
// Validate the concrete, interpolated destination used by run-single-request,
// not only the separately prepared review copy. Bruno interpolation can
// evaluate dynamic template helpers, so preparing twice must not create a
// time-of-check/time-of-use window in which the second host leaves the policy.
const interpolateVarsModule = require.resolve('@usebruno/cli/src/runner/interpolate-vars');
const baseInterpolateVars = require(interpolateVarsModule);
const interpolateVars = (...args) => {
  const request = baseInterpolateVars(...args);
  enforceInterpolatedRequestPolicy(request);
  return request;
};
require.cache[interpolateVarsModule].exports = interpolateVars;
const { runSingleRequest } = require('@usebruno/cli/src/runner/run-single-request');
const { getEnvVars, getOptions } = require('@usebruno/cli/src/utils/bru');
const { parseEnvironmentJson } = require('@usebruno/cli/src/utils/environment');
const { hasExecutableTestInScript } = require('@usebruno/cli/src/utils/request');
const { grpcProxyConfig } = require('./egress');
const { withDeadline } = require('./runtime');

const sessionRuntimeVariables = {};
let activeRequestID = null;
let activeAbortController = null;
let activeOutputBudget = null;
const cancelledRequestIDs = new Set();
const activeGRPCClients = new Set();
const activeWSClients = new Set();

function normalizedAllowedHosts() {
  return (Array.isArray(config.allowed_target_hosts) ? config.allowed_target_hosts : [])
    .map((value) => String(value || '').trim().toLowerCase().replace(/\.$/, ''))
    .filter(Boolean);
}

function targetHostAllowed(hostname) {
  const host = String(hostname || '').trim().toLowerCase().replace(/\.$/, '');
  const allowed = normalizedAllowedHosts();
  if (allowed.length === 0) return true;
  return allowed.some((pattern) => (
    pattern.startsWith('*.')
      ? host.length > pattern.length - 1 && host.endsWith(pattern.slice(1))
      : host === pattern
  ));
}

function enforceTargetPolicy(rawURL, operation) {
  const allowed = normalizedAllowedHosts();
  if (allowed.length === 0) return;
  let parsed;
  try {
    parsed = new URL(String(rawURL || ''));
  } catch {
    throw new Error(`APEX target policy cannot validate ${operation} URL ${JSON.stringify(rawURL)}`);
  }
  if (!targetHostAllowed(parsed.hostname)) {
    throw new Error(`APEX target policy blocked ${operation} host ${parsed.hostname}`);
  }
}

function enforceInterpolatedRequestPolicy(request) {
  if (normalizedAllowedHosts().length === 0 || !request) return;
  let primaryURL = String(request.url || '').trim();
  // Bruno permits the conventional host:port form for gRPC requests. URL
  // parsing still needs an explicit scheme for an unambiguous hostname check.
  if (primaryURL && !/^[A-Za-z][A-Za-z0-9+.-]*:\/\//.test(primaryURL)) {
    primaryURL = `http://${primaryURL}`;
  }
  if (primaryURL) enforceTargetPolicy(primaryURL, 'interpolated request');
  for (const [label, endpoint] of [
    ['OAuth access token', request.oauth2?.accessTokenUrl],
    ['OAuth refresh token', request.oauth2?.refreshTokenUrl]
  ]) {
    if (endpoint) enforceTargetPolicy(endpoint, label);
  }
}

function assertStaticPolicyCompatibility(request) {
  if (normalizedAllowedHosts().length === 0) return;
  // prepareRequest merges collection, folder, and request hooks into these
  // three fields. Every Bruno script runtime exposes secondary request APIs
  // (for example bru.sendRequest/runRequest), including response/test hooks.
  // Until those transports accept the same policy callback as primary I/O,
  // fail closed rather than letting interpolated credentials leave through a
  // destination that was never validated.
  const configured = [request?.script?.req, request?.script?.res, request?.tests]
    .some((script) => typeof script === 'string' && script.trim().length > 0);
  if (configured) {
    throw new Error('APEX target policy blocks executable Bruno scripts because secondary script requests cannot yet be allowlist-enforced');
  }
}

class OutputLimitError extends Error {
  constructor() {
    super('Bruno execution exceeded the configured output limit');
    this.name = 'OutputLimitError';
  }
}

function serializedBytes(value) {
  try {
    return Buffer.byteLength(JSON.stringify(value));
  } catch {
    throw new OutputLimitError();
  }
}

function newOutputBudget(limit = maxOutputBytes) {
  let remaining = limit;
  return {
    consume(value) {
      const required = serializedBytes(value) + 1;
      if (required > remaining) throw new OutputLimitError();
      remaining -= required;
    },
    remaining() {
      return remaining;
    }
  };
}

function newRetentionBudget(limit = maxOutputBytes) {
  let retained = 0;
  return {
    retain(value) {
      const required = Buffer.isBuffer(value) ? value.length : serializedBytes(value);
      if (required > limit - retained) throw new OutputLimitError();
      retained += required;
    },
    retainBytes(required) {
      if (!Number.isSafeInteger(required) || required < 0 || required > limit - retained) {
        throw new OutputLimitError();
      }
      retained += required;
    }
  };
}

function emit(requestID, event) {
  activeOutputBudget?.consume(event);
  parentPort.postMessage({ type: 'event', requestID, event });
}

function complete(requestID, result) {
  activeOutputBudget?.consume({ type: 'complete', result });
  parentPort.postMessage({ type: 'complete', requestID, result });
}

function fail(requestID, error) {
  const raw = String(error?.message || error || 'Bruno execution failed');
  const budget = requestID === activeRequestID ? activeOutputBudget : newOutputBudget();
  const emptyEventBytes = serializedBytes({ type: 'error', error: '' }) + 1;
  const available = Math.max(0, (budget?.remaining() || 0) - emptyEventBytes - 3);
  const message = Buffer.from(raw).subarray(0, Math.min(4096, available)).toString('utf8');
  const event = { type: 'error', error: message };
  try {
    budget?.consume(event);
    parentPort.postMessage({ type: 'error', requestID, error: message });
  } catch {
    // No valid NDJSON terminal event fits in the remaining byte envelope. Tell
    // the server to end the stream without adding bytes; the controller then
    // fails the incomplete stream closed.
    parentPort.postMessage({ type: 'output_limit', requestID });
  }
}

function relativeItemPath(item) {
  return path.relative(collectionPath, item.pathname).split(path.sep).join('/');
}

function enabledEntries(entries) {
  return (Array.isArray(entries) ? entries : []).filter((entry) => entry?.enabled !== false);
}

function durationMS(start) {
  return Number(process.hrtime.bigint() - start) / 1e6;
}

function responseBody(value) {
  if (value == null) return '';
  if (Buffer.isBuffer(value)) return value.toString('utf8');
  if (typeof value === 'string') return value;
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}

function failedEvidence(results) {
  return (results || []).some((result) => String(result?.status).toLowerCase() === 'fail');
}

function iterationEvidence(collection) {
  const details = collection?.runnerIterationDetails || {};
  return {
    iteration_index: Number(details.iterationIndex || 0),
    total_iterations: Number(details.totalIterations || 1)
  };
}

function testChild(item, parent, evidence, operationKind, collection) {
  const success = String(evidence?.status).toLowerCase() !== 'fail';
  return {
    action_name: evidence?.description || evidence?.lhsExpr || `${operationKind} result`,
    sample_kind: 'child',
    parent_action_name: parent,
    duration_ms: 0,
    success,
    error_message: success ? '' : (evidence?.error || evidence?.message || 'Bruno test failed'),
    source_path: relativeItemPath(item),
    source_uid: item.uid || '',
    request_type: item.type,
    operation_kind: operationKind,
    evidence: {
      status: evidence?.status,
      description: evidence?.description,
      lhs_expr: evidence?.lhsExpr,
      rhs_expr: evidence?.rhsExpr,
      ...iterationEvidence(collection)
    }
  };
}

function confinedBundlePath(candidate) {
  if (typeof candidate !== 'string' || candidate.includes('\0') || path.isAbsolute(candidate)) return null;
  const absolute = path.resolve(collectionPath, candidate);
  const relative = path.relative(collectionPath, absolute);
  if (relative.startsWith('..') || path.isAbsolute(relative)) return null;
  return absolute;
}

function findBundleFile(reference, directories, extensions) {
  const normalized = String(reference || '').trim();
  if (!normalized) return null;
  const candidates = [];
  const hasExtension = path.extname(normalized) !== '';
  for (const directory of directories) {
    const relative = directory === '.' ? normalized : path.join(directory, normalized);
    candidates.push(relative);
    if (!hasExtension) {
      for (const extension of extensions) candidates.push(`${relative}${extension}`);
    }
  }
  for (const candidate of candidates) {
    const absolute = confinedBundlePath(candidate);
    if (absolute && fs.existsSync(absolute) && fs.statSync(absolute).isFile()) return absolute;
  }
  return null;
}

function parseEnvironmentFile(filePath, name) {
  const extension = path.extname(filePath).toLowerCase();
  const content = fs.readFileSync(filePath, 'utf8');
  let variables;
  if (extension === '.env') {
    variables = parseDotEnv(content);
  } else if (extension === '.json') {
    const parsed = JSON.parse(content);
    variables = Array.isArray(parsed?.variables)
      ? getEnvVars(parseEnvironmentJson(parsed))
      : parsed;
  } else if (extension === '.yml' || extension === '.yaml') {
    variables = getEnvVars(parseEnvironment(content, { format: 'yml' }));
  } else {
    variables = getEnvVars(parseEnvironment(content.replace(/\r\n/g, '\n'), { format: 'bru' }));
  }
  if (!variables || Array.isArray(variables) || typeof variables !== 'object') {
    throw new Error(`Bruno environment ${JSON.stringify(name)} must contain an object of variables`);
  }
  return { ...variables, __name__: variables.__name__ || path.basename(filePath, extension) };
}

function loadEnvironment(name, collection, globalEnvironment = false) {
  if (!name) return {};
  const preferredExtension = FORMAT_CONFIG[collection.format]?.ext || '.bru';
  const directories = globalEnvironment
    ? ['global-environments', 'global_environments', 'environments', '.']
    : ['environments', '.'];
  const environmentPath = findBundleFile(
    name,
    directories,
    [preferredExtension, '.bru', '.yml', '.yaml', '.json', '.env']
  );
  if (!environmentPath) {
    const kind = globalEnvironment ? 'global environment' : 'environment';
    throw new Error(`Bruno ${kind} ${JSON.stringify(name)} was not found in the source bundle`);
  }
  return parseEnvironmentFile(environmentPath, name);
}

function loadDataRows(reference) {
  if (!reference) return [{}];
  const dataPath = findBundleFile(reference, ['.'], ['.csv', '.json']);
  if (!dataPath) throw new Error(`Bruno data source ${JSON.stringify(reference)} was not found in the source bundle`);
  const extension = path.extname(dataPath).toLowerCase();
  if (extension === '.csv') {
    return parseCSV(fs.readFileSync(dataPath, 'utf8').replace(/^\uFEFF/, ''), {
      columns: true,
      skip_empty_lines: true,
      trim: true
    });
  }
  if (extension === '.json') {
    const parsed = JSON.parse(fs.readFileSync(dataPath, 'utf8'));
    if (!Array.isArray(parsed)) throw new Error('Bruno JSON data source must contain an array');
    if (parsed.some((row) => row == null || Array.isArray(row) || typeof row !== 'object')) {
      throw new Error('Bruno JSON data source rows must be objects');
    }
    return parsed;
  }
  throw new Error(`Bruno data source ${JSON.stringify(reference)} must be CSV or JSON`);
}

function explicitProcessEnvironment() {
  const values = {};
  const dotenvPath = path.join(collectionPath, '.env');
  if (fs.existsSync(dotenvPath)) Object.assign(values, parseDotEnv(fs.readFileSync(dotenvPath, 'utf8')));
  return values;
}

function governedSecretVariables(secrets) {
  // Bruno merges external secret variables into its interpolation scope. Keep
  // controller-managed material under one explicit namespace so a binding for
  // API_TOKEN resolves only {{secret.API_TOKEN}}. It must never become
  // {{API_TOKEN}}, {{process.env.API_TOKEN}}, or a real worker process value.
  return { secret: { ...(secrets || {}) } };
}

function configureOptions() {
  const options = getOptions();
  for (const key of Object.keys(options)) delete options[key];
  options.disableCookies = false;
  options.noproxy = false;
  options.insecure = false;
  options.ignoreTruststore = false;
  options.cacheSslSession = true;
  const broker = process.env.APEXLOAD_BRUNO_EGRESS_BROKER_URL;
  if (broker) {
    options.cachedSystemProxy = { http_proxy: broker, https_proxy: broker, no_proxy: '' };
  } else if (process.env.APEXLOAD_BRUNO_REQUIRE_EGRESS_BROKER === 'true') {
    throw new Error('Bruno execution requires the configured APEX egress broker');
  }
}

function selectedItems(action, collection) {
  const params = action.params || {};
  const selection = String(params.selection || 'collection').toLowerCase();
  const recursive = params.recursive !== false;
  let selectedPaths = Array.isArray(params.paths) ? params.paths : [];
  if (selection === 'collection' || selection === 'tag' || selectedPaths.length === 0) selectedPaths = ['.'];
  const resolved = selectedPaths.map((candidate) => {
    const absolute = path.resolve(collectionPath, candidate);
    const relative = path.relative(collectionPath, absolute);
    if (relative.startsWith('..') || path.isAbsolute(relative)) throw new Error(`selection path ${JSON.stringify(candidate)} escapes the collection`);
    return absolute;
  });
  let items = getCallStack(resolved, collection, { recursive });
  const includeTags = Array.isArray(params.include_tags) ? params.include_tags : [];
  const excludeTags = Array.isArray(params.exclude_tags) ? params.exclude_tags : [];
  items = items.filter((item) => ['http-request', 'graphql-request', 'grpc-request', 'ws-request'].includes(item.type));
  items = items.filter((item) => isRequestTagsIncluded(item.tags, includeTags, excludeTags));
  if (params.tests_only) {
    items = items.filter((item) => {
      const request = item.request || {};
      return hasExecutableTestInScript(request.tests)
        || enabledEntries(request.assertions).length > 0
        || hasExecutableTestInScript(request.script?.req)
        || hasExecutableTestInScript(request.script?.res);
    });
  }
  return items;
}

async function prepareExtendedRequest(item, state) {
  const copy = cloneDeep(item);
  const protocolRequest = cloneDeep(copy.request || {});
  const request = await prepareRequest(copy, state.collection);
  request.globalEnvironmentVariables = state.globalEnvVariables;
  request.runnerIterationDetails = state.collection.runnerIterationDetails;
  request.__bruno__executionMode = 'cli';
  request.methodType = protocolRequest.methodType;
  request.protoPath = protocolRequest.protoPath;
  request.protocolBody = protocolRequest.body;
  request.data = item.type === 'grpc-request'
    ? cloneDeep(protocolRequest.body?.grpc || [])
    : cloneDeep(protocolRequest.body?.ws || []);
  request.assertions = protocolRequest.assertions || [];
  return { request };
}

function interpolateExtendedRequest(request, state) {
  interpolateVars(
    request,
    state.envVariables,
    state.runtimeVariables,
    state.processEnvVars,
    state.secretVariables
  );
  if (request.basicAuth) {
    request.headers.Authorization = `Basic ${Buffer.from(
      `${request.basicAuth.username || ''}:${request.basicAuth.password || ''}`
    ).toString('base64')}`;
  }
}

async function runPreRequestPhase({
  request,
  state,
  runRequest
}) {
  const scriptingConfig = { ...(state.collection?.brunoConfig?.scripts || {}), runtime: state.runtime };
  const collectionName = state.collection?.brunoConfig?.name;
  const onConsoleLog = () => {};
  const evidence = { pre: [] };
  if (request.script?.req) {
    try {
      const result = await new ScriptRuntime({ runtime: state.runtime }).runRequestScript(
        decomment(request.script.req, { space: true }),
        request,
        state.envVariables,
        state.runtimeVariables,
        collectionPath,
        onConsoleLog,
        state.processEnvVars,
        scriptingConfig,
        undefined,
        state.secretVariables,
        runRequest,
        collectionName
      );
      evidence.pre = result?.results || [];
      if (result?.nextRequestName !== undefined) evidence.nextRequestName = result.nextRequestName;
      if (result?.stopExecution) evidence.stopExecution = true;
      if (result?.skipRequest) evidence.skipRequest = true;
    } catch (error) {
      evidence.pre = error?.partialResults?.results || [];
      evidence.pre.push({
        status: 'fail',
        description: 'Pre-Request Script Error',
        error: error?.message || 'An error occurred while executing the pre-request script.'
      });
      evidence.error = error?.message || 'Bruno pre-request script failed';
      if (error?.partialResults?.nextRequestName !== undefined) {
        evidence.nextRequestName = error.partialResults.nextRequestName;
      }
      if (error?.partialResults?.stopExecution) evidence.stopExecution = true;
    }
  }
  return evidence;
}

async function runPostResponsePhases({
  request,
  response,
  state,
  runRequest,
  preEvidence
}) {
  const scriptingConfig = { ...(state.collection?.brunoConfig?.scripts || {}), runtime: state.runtime };
  const collectionName = state.collection?.brunoConfig?.name;
  const onConsoleLog = () => {};
  const evidence = {
    pre: preEvidence?.pre || [],
    post: [],
    assertions: [],
    tests: [],
    nextRequestName: preEvidence?.nextRequestName,
    stopExecution: preEvidence?.stopExecution
  };
  if (request.vars?.res?.length) {
    new VarsRuntime({ runtime: state.runtime }).runPostResponseVars(
      request.vars.res,
      request,
      response,
      state.envVariables,
      state.runtimeVariables,
      collectionPath,
      state.processEnvVars,
      undefined,
      state.secretVariables
    );
  }
  if (request.script?.res) {
    try {
      const result = await new ScriptRuntime({ runtime: state.runtime }).runResponseScript(
        decomment(request.script.res, { space: true }),
        request,
        response,
        state.envVariables,
        state.runtimeVariables,
        collectionPath,
        onConsoleLog,
        state.processEnvVars,
        scriptingConfig,
        undefined,
        state.secretVariables,
        runRequest,
        collectionName
      );
      evidence.post = result?.results || [];
      if (result?.nextRequestName !== undefined) evidence.nextRequestName = result.nextRequestName;
      if (result?.stopExecution) evidence.stopExecution = true;
    } catch (error) {
      evidence.post = [
        ...(error?.partialResults?.results || []),
        {
          status: 'fail',
          description: 'Post-Response Script Error',
          error: error?.message || 'An error occurred while executing the post-response script.'
        }
      ];
      if (error?.partialResults?.nextRequestName !== undefined) {
        evidence.nextRequestName = error.partialResults.nextRequestName;
      }
      if (error?.partialResults?.stopExecution) evidence.stopExecution = true;
    }
  }
  if (request.assertions?.length) {
    evidence.assertions = new AssertRuntime({ runtime: state.runtime }).runAssertions(
      request.assertions,
      request,
      response,
      state.envVariables,
      state.runtimeVariables,
      state.processEnvVars,
      undefined,
      state.secretVariables
    );
  }
  if (typeof request.tests === 'string' && request.tests.length > 0) {
    try {
      const result = await new TestRuntime({ runtime: state.runtime }).runTests(
        decomment(request.tests, { space: true }),
        request,
        response,
        state.envVariables,
        state.runtimeVariables,
        collectionPath,
        onConsoleLog,
        state.processEnvVars,
        scriptingConfig,
        undefined,
        state.secretVariables,
        runRequest,
        collectionName
      );
      evidence.tests = result?.results || [];
      if (result?.nextRequestName !== undefined) evidence.nextRequestName = result.nextRequestName;
      if (result?.stopExecution) evidence.stopExecution = true;
    } catch (error) {
      evidence.tests = [
        ...(error?.partialResults?.results || []),
        {
          status: 'fail',
          description: 'Test Script Error',
          error: error?.message || 'An error occurred while executing the test script.'
        }
      ];
      if (error?.partialResults?.nextRequestName !== undefined) {
        evidence.nextRequestName = error.partialResults.nextRequestName;
      }
      if (error?.partialResults?.stopExecution) evidence.stopExecution = true;
    }
  }
  return evidence;
}

function emitEvidence(requestID, item, parent, evidence, collection) {
  for (const result of evidence.pre || []) emit(requestID, { type: 'result', result: testChild(item, parent, result, 'pre_request_test', collection) });
  for (const result of evidence.post || []) emit(requestID, { type: 'result', result: testChild(item, parent, result, 'post_response_test', collection) });
  for (const result of evidence.assertions || []) emit(requestID, { type: 'result', result: testChild(item, parent, result, 'assertion', collection) });
  for (const result of evidence.tests || []) emit(requestID, { type: 'result', result: testChild(item, parent, result, 'test', collection) });
}

async function executeHTTP(item, state, runRequest) {
  const executionItem = cloneDeep(item);
  if (normalizedAllowedHosts().length > 0) {
    // The pinned Bruno runner follows redirects by default and preserves
    // arbitrary request headers. Disable them under a target policy so every
    // network destination is one we validated here.
    executionItem.settings = { ...(executionItem.settings || {}), followRedirects: false, maxRedirects: 0 };
    const prepared = await prepareExtendedRequest(executionItem, state);
    assertStaticPolicyCompatibility(prepared.request);
    interpolateExtendedRequest(prepared.request, state);
    enforceTargetPolicy(prepared.request.url, executionItem.type === 'graphql-request' ? 'GraphQL' : 'HTTP');
    for (const [label, endpoint] of [
      ['OAuth access token', prepared.request.oauth2?.accessTokenUrl],
      ['OAuth refresh token', prepared.request.oauth2?.refreshTokenUrl]
    ]) {
      if (endpoint) enforceTargetPolicy(endpoint, label);
    }
  }
  const started = process.hrtime.bigint();
  const result = await runSingleRequest(
    executionItem,
    collectionPath,
    state.runtimeVariables,
    state.envVariables,
    state.processEnvVars,
    state.collection.brunoConfig,
    state.collection.root,
    state.secretVariables,
    state.runtime,
    state.collection,
    runRequest,
    state.globalEnvVariables,
    state.bailAbortSignal
  );
  const response = result.response || {};
  const evidence = {
    pre: result.preRequestTestResults || [],
    post: result.postResponseTestResults || [],
    assertions: result.assertionResults || [],
    tests: result.testResults || [],
    nextRequestName: result.nextRequestName,
    stopExecution: result.shouldStopRunnerExecution
  };
  const body = responseBody(response.data);
  const testsFailed = [evidence.pre, evidence.post, evidence.assertions, evidence.tests].some(failedEvidence);
  const success = !result.error && result.status !== 'error' && !testsFailed;
  return {
    requestResult: {
      action_name: item.name,
      sample_kind: 'child',
      duration_ms: Number(response.responseTime || durationMS(started)),
      bytes_sent: Buffer.byteLength(responseBody(result.request?.data)),
      bytes_received: Number(response.size || Buffer.byteLength(body)),
      status_code: Number(response.status) || 0,
      success,
      error_message: result.error || (testsFailed ? 'Bruno test or assertion failed' : ''),
      response_body: body,
      source_path: relativeItemPath(item),
      source_uid: item.uid || '',
      request_type: item.type === 'graphql-request' ? 'graphql' : 'http',
      operation_kind: 'request',
      dependency_type: item.type === 'graphql-request' ? 'graphql' : 'http',
      dependency_target: (() => {
        try { return new URL(result.request?.url).host; } catch { return ''; }
      })(),
      evidence: {
        status_text: response.statusText,
        headers: typeof response.headers?.toJSON === 'function' ? response.headers.toJSON() : response.headers,
        skipped: Boolean(result.skipped),
        skip_reason: result.skipReason,
        ...iterationEvidence(state.collection)
      }
    },
    evidence,
    response
  };
}

async function executeGRPC(item, state, runRequest, timeoutMS) {
  const prepared = await prepareExtendedRequest(item, state);
  const request = prepared.request;
  assertStaticPolicyCompatibility(request);
  const preEvidence = await runPreRequestPhase({ request, state, runRequest });
  if (preEvidence.error || preEvidence.skipRequest) {
    const skipped = preEvidence.skipRequest && !preEvidence.error;
    return {
      requestResult: {
        action_name: item.name,
        sample_kind: 'child',
        duration_ms: 0,
        status_code: 0,
        success: skipped,
        error_message: skipped ? '' : preEvidence.error,
        response_body: '',
        source_path: relativeItemPath(item),
        source_uid: item.uid || '',
        request_type: 'grpc',
        operation_kind: 'stream',
        dependency_type: 'grpc',
        dependency_target: request.url,
        evidence: {
          skipped,
          skip_reason: skipped ? 'pre_request_script' : '',
          ...iterationEvidence(state.collection)
        }
      },
      evidence: { ...preEvidence, post: [], assertions: [], tests: [] },
      response: { status: skipped ? 'skipped' : 'error', data: null, responseTime: 0 }
    };
  }
  interpolateExtendedRequest(request, state);
  if (!/^[A-Za-z][A-Za-z0-9+.-]*:\/\//.test(request.url)) request.url = `http://${request.url}`;
  enforceTargetPolicy(request.url, 'gRPC');
  const started = process.hrtime.bigint();
  const messages = [];
  const retained = newRetentionBudget();
  const metadata = {};
  let terminalError = null;
  let finish;
  const terminal = new Promise((resolve) => { finish = resolve; });
  const client = new GrpcClient((eventName, requestID, collectionUID, payload = {}) => {
    if (eventName === 'grpc:response' && payload.res !== undefined) {
      try {
        retained.retain(payload.res);
        messages.push(payload.res);
      } catch (error) {
        terminalError = error.message;
        client.clearAllConnections();
        finish();
        return;
      }
    }
    if (eventName === 'grpc:metadata') {
      try {
        retained.retain(payload.metadata || {});
        Object.assign(metadata, payload.metadata || {});
      } catch (error) {
        terminalError = error.message;
        client.clearAllConnections();
        finish();
        return;
      }
    }
    if (payload.error) {
      terminalError = payload.error?.message || String(payload.error);
      finish();
    }
    if (['grpc:server-end-stream', 'grpc:server-cancel-stream'].includes(eventName)) finish();
    if (eventName === 'grpc:response' && !request.methodType?.includes('stream')) finish();
  });
  activeGRPCClients.add(client);
  const requestID = item.uid || relativeItemPath(item);
  const outboundMessages = Array.isArray(request.data) ? request.data : [request.data];
  try {
    await client.startConnection({
      request: {
        uid: requestID,
        url: request.url,
        method: request.method,
        methodType: request.methodType,
        headers: request.headers,
        body: { grpc: outboundMessages },
        protoPath: request.protoPath
      },
      collection: { uid: state.collection.uid, pathname: collectionPath },
      verifyOptions: { rejectUnauthorized: true },
      channelOptions: {
        'grpc.max_receive_message_length': maxOutputBytes,
        'grpc.max_metadata_size': Math.min(maxOutputBytes, 64 * 1024)
      },
      includeDirs: enabledEntries(state.collection.brunoConfig?.protobuf?.importPaths)
        .map((entry) => confinedBundlePath(entry.path))
        .filter(Boolean),
      proxyConfig: grpcProxyConfig(process.env.APEXLOAD_BRUNO_EGRESS_BROKER_URL)
    });
    if (['client-streaming', 'bidi-streaming'].includes(request.methodType)) {
      for (const message of outboundMessages) {
        client.sendMessage(requestID, state.collection.uid, message?.content ?? message);
      }
      client.end(requestID);
    }
    try {
      await withDeadline(terminal, timeoutMS, 'gRPC stream deadline expired');
    } catch (error) {
      terminalError = error?.message || String(error);
    }
  } catch (error) {
    terminalError = error?.message || String(error);
  } finally {
    client.clearAllConnections();
    activeGRPCClients.delete(client);
  }
  const response = {
    status: terminalError ? 13 : 0,
    statusText: terminalError ? 'ERROR' : 'OK',
    headers: metadata,
    data: messages,
    responseTime: durationMS(started),
    duration: durationMS(started),
    size: Buffer.byteLength(responseBody(messages))
  };
  const evidence = await runPostResponsePhases({
    request,
    response,
    state,
    runRequest,
    preEvidence
  });
  const testsFailed = [evidence.pre, evidence.post, evidence.assertions, evidence.tests].some(failedEvidence);
  return {
    requestResult: {
      action_name: item.name,
      sample_kind: 'child',
      duration_ms: response.responseTime,
      bytes_sent: Buffer.byteLength(responseBody(request.body?.grpc)),
      bytes_received: response.size,
      status_code: response.status,
      success: !terminalError && !testsFailed,
      error_message: terminalError || (testsFailed ? 'Bruno test or assertion failed' : ''),
      response_body: responseBody(messages),
      source_path: relativeItemPath(item),
      source_uid: item.uid || '',
      request_type: 'grpc',
      operation_kind: 'stream',
      dependency_type: 'grpc',
      dependency_target: request.url,
      evidence: {
        metadata,
        message_count: messages.length,
        method: request.method,
        method_type: request.methodType,
        ...iterationEvidence(state.collection)
      }
    },
    evidence,
    response
  };
}

async function executeWebSocket(item, state, runRequest, timeoutMS) {
  const prepared = await prepareExtendedRequest(item, state);
  const request = prepared.request;
  assertStaticPolicyCompatibility(request);
  const preEvidence = await runPreRequestPhase({ request, state, runRequest });
  if (preEvidence.error || preEvidence.skipRequest) {
    const skipped = preEvidence.skipRequest && !preEvidence.error;
    return {
      requestResult: {
        action_name: item.name,
        sample_kind: 'child',
        duration_ms: 0,
        status_code: 0,
        success: skipped,
        error_message: skipped ? '' : preEvidence.error,
        response_body: '',
        source_path: relativeItemPath(item),
        source_uid: item.uid || '',
        request_type: 'websocket',
        operation_kind: 'stream',
        dependency_type: 'websocket',
        dependency_target: request.url,
        evidence: {
          skipped,
          skip_reason: skipped ? 'pre_request_script' : '',
          ...iterationEvidence(state.collection)
        }
      },
      evidence: { ...preEvidence, post: [], assertions: [], tests: [] },
      response: { status: skipped ? 'skipped' : 'error', data: null, responseTime: 0 }
    };
  }
  interpolateExtendedRequest(request, state);
  enforceTargetPolicy(request.url, 'WebSocket');
  if (request.url.startsWith('http://')) request.url = `ws://${request.url.slice('http://'.length)}`;
  if (request.url.startsWith('https://')) request.url = `wss://${request.url.slice('https://'.length)}`;
  const started = process.hrtime.bigint();
  const messages = [];
  const retained = newRetentionBudget();
  const outboundPayloads = [];
  let terminalError = null;
  let opened = false;
  let closeCode = 0;
  let closeReason = '';
  let upgradeHeaders = {};
  let socket;
  let finishOpen;
  let finishTerminal;
  const openPromise = new Promise((resolve) => { finishOpen = resolve; });
  const terminalPromise = new Promise((resolve) => { finishTerminal = resolve; });
  const client = {
    clearAllConnections() {
      if (socket && socket.readyState !== WebSocket.CLOSED) socket.terminate();
    },
    close() {
      if (socket && socket.readyState === WebSocket.OPEN) socket.close(1000);
      else if (socket && socket.readyState === WebSocket.CONNECTING) socket.terminate();
    }
  };
  activeWSClients.add(client);
  const outboundMessages = Array.isArray(request.data) ? request.data : [request.data];
  try {
    const headers = { ...(request.headers || {}) };
    const protocolHeader = headers['Sec-WebSocket-Protocol'] || headers['sec-websocket-protocol'];
    delete headers['Sec-WebSocket-Protocol'];
    delete headers['sec-websocket-protocol'];
    const protocols = String(protocolHeader || '')
      .split(',')
      .map((value) => value.trim())
      .filter(Boolean);
    const brokerURL = process.env.APEXLOAD_BRUNO_EGRESS_BROKER_URL;
    let agent;
    if (brokerURL) {
      const brokerProtocol = new URL(brokerURL).protocol;
      agent = brokerProtocol.startsWith('socks')
        ? new SocksProxyAgent(brokerURL)
        : new HttpsProxyAgent(brokerURL);
    }
    socket = new WebSocket(
      request.url,
      protocols.length > 0 ? protocols : undefined,
      {
        headers,
        handshakeTimeout: Math.min(Number(item.settings?.timeout || timeoutMS), timeoutMS),
        // Redirects are safe only when there is no application target policy.
        // Otherwise a second handshake could leave the allowlist and forward
        // custom credential headers to an unapproved origin.
        followRedirects: normalizedAllowedHosts().length === 0,
        maxRedirects: normalizedAllowedHosts().length === 0 ? 10 : 0,
        maxPayload: maxOutputBytes,
        rejectUnauthorized: true,
        agent
      }
    );
    socket.once('open', () => {
      opened = true;
      finishOpen();
    });
    socket.on('upgrade', (response) => {
      upgradeHeaders = { ...response.headers };
    });
    socket.on('message', (data, isBinary) => {
      try {
        // Account before string/base64 conversion so an oversized or
        // cumulatively excessive stream is terminated before another retained
        // representation is allocated.
        const raw = Buffer.from(data);
        retained.retainBytes(isBinary ? Math.ceil(raw.length / 3) * 4 + 32 : raw.length + 16);
        if (isBinary) {
          messages.push({ type: 'binary', base64: raw.toString('base64') });
          return;
        }
        const text = raw.toString();
        try {
          messages.push(JSON.parse(text));
        } catch {
          messages.push(text);
        }
      } catch (error) {
        terminalError = error.message;
        client.clearAllConnections();
        finishOpen();
        finishTerminal();
      }
    });
    socket.once('close', (code, reason) => {
      closeCode = Number(code || 0);
      closeReason = Buffer.from(reason || '').toString();
      finishOpen();
      finishTerminal();
    });
    socket.once('error', (error) => {
      terminalError = error?.message || String(error || 'WebSocket error');
      finishOpen();
      finishTerminal();
    });
    try {
      await withDeadline(openPromise, timeoutMS, 'WebSocket connection deadline expired');
      if (opened) {
        for (const message of outboundMessages) {
          const content = message?.content ?? message ?? '';
          const format = message?.type || 'raw';
          const payload = format === 'json' && typeof content !== 'string'
            ? JSON.stringify(content)
            : (typeof content === 'string' || Buffer.isBuffer(content) ? content : JSON.stringify(content));
          outboundPayloads.push(payload);
          await new Promise((resolve, reject) => {
            socket.send(payload, (error) => error ? reject(error) : resolve());
          });
        }
        await withDeadline(terminalPromise, timeoutMS, 'WebSocket stream deadline expired');
      }
    } catch (error) {
      terminalError = error?.message || String(error);
    }
  } catch (error) {
    terminalError = error?.message || String(error);
  } finally {
    client.close();
    activeWSClients.delete(client);
  }
  const response = {
    status: terminalError ? 500 : 101,
    statusText: terminalError ? 'ERROR' : 'SWITCHING PROTOCOLS',
    headers: {},
    data: messages,
    responseTime: durationMS(started),
    duration: durationMS(started),
    size: Buffer.byteLength(responseBody(messages))
  };
  const evidence = await runPostResponsePhases({
    request,
    response,
    state,
    runRequest,
    preEvidence
  });
  const testsFailed = [evidence.pre, evidence.post, evidence.assertions, evidence.tests].some(failedEvidence);
  return {
    requestResult: {
      action_name: item.name,
      sample_kind: 'child',
      duration_ms: response.responseTime,
      bytes_sent: outboundPayloads.reduce((total, payload) => total + Buffer.byteLength(payload), 0),
      bytes_received: response.size,
      status_code: response.status,
      success: !terminalError && !testsFailed,
      error_message: terminalError || (testsFailed ? 'Bruno test or assertion failed' : ''),
      response_body: responseBody(messages),
      source_path: relativeItemPath(item),
      source_uid: item.uid || '',
      request_type: 'websocket',
      operation_kind: 'stream',
      dependency_type: 'websocket',
      dependency_target: request.url,
      evidence: {
        message_count: messages.length,
        close_code: closeCode,
        close_reason: closeReason,
        upgrade_headers: upgradeHeaders,
        ...iterationEvidence(state.collection)
      }
    },
    evidence,
    response
  };
}

async function executeSelection(requestID, action) {
  cancelledRequestIDs.delete(requestID);
  activeRequestID = requestID;
  activeOutputBudget = newOutputBudget();
  const executionAbortController = new AbortController();
  activeAbortController = executionAbortController;
  const cancelled = () => cancelledRequestIDs.has(requestID);
  configureOptions();
  global.brunoSkippedFiles = [];
  const baseCollection = createCollectionJsonFromPathname(collectionPath);
  baseCollection.uid = baseCollection.uid || config.bundle_id || workerData.sessionID;
  const params = action.params || {};
  const persistSession = params.persist_session_across_iterations === undefined
    ? Boolean(config.persist_session)
    : Boolean(params.persist_session_across_iterations);
  if (!persistSession) {
    for (const key of Object.keys(sessionRuntimeVariables)) delete sessionRuntimeVariables[key];
    await cookies.jar().clear();
  }
  const envVariables = loadEnvironment(params.environment || config.environment, baseCollection);
  const globalEnvVariables = loadEnvironment(
    params.global_environment || config.global_environment,
    baseCollection,
    true
  );
  const processEnvVars = explicitProcessEnvironment();
  const runtime = config.sandbox === 'developer' ? 'nodevm' : 'quickjs';
  const secretVariables = governedSecretVariables(config.secrets);
  const items = selectedItems(action, baseCollection);
  if (items.length === 0) throw new Error('Bruno selection matched no executable requests');
  const dataRows = loadDataRows(params.data_source);
  if (dataRows.length === 0) throw new Error('Bruno data source contains no executable rows');
  const baseRuntimeVariables = { ...sessionRuntimeVariables };
  const maxJumps = Number(config.limits?.max_jumps || 10000);
  const maxDepth = Number(config.limits?.max_run_request_depth || 64);
  const streamTimeoutMS = Number(params.stream_timeout_ms || config.limits?.default_stream_timeout_ms || 60000);
  const delayMS = Number(params.delay_ms || 0);
  let failed = false;
  let firstError = '';
  const haltState = { halted: false };
  const iterationStates = [];
  const start = process.hrtime.bigint();

  const runIteration = async (iterationIndex) => {
    if (cancelled() || (params.bail && haltState.halted)) return null;
    const iterationData = { ...(dataRows[iterationIndex] || {}) };
    const collection = cloneDeep(baseCollection);
    collection.runnerIterationDetails = {
      iterationIndex,
      iterationData,
      totalIterations: dataRows.length
    };
    const state = {
      collection,
      envVariables: { ...envVariables },
      globalEnvVariables: { ...globalEnvVariables },
      processEnvVars,
      secretVariables: { ...secretVariables },
      runtime,
      runtimeVariables: { ...baseRuntimeVariables, ...iterationData },
      // Bruno threads this signal through HTTP and GraphQL transports. It is
      // always present so request-scoped cancellation aborts active I/O even
      // when bail mode is disabled.
      bailAbortSignal: executionAbortController.signal
    };
    const iterationItems = items.map((item) => (
      cloneDeep(findItemInCollection(collection, item.pathname) || item)
    ));
    let jumps = 0;

    const executeItem = async (item, depth = 0) => {
      if (cancelled()) throw new Error('Bruno execution cancelled');
      if (params.bail && haltState.halted) throw new Error('Bruno execution halted by bail');
      if (depth > maxDepth) throw new Error(`bru.runRequest nesting exceeds ${maxDepth}`);
      const runRequest = async (relativePath) => {
        const extension = FORMAT_CONFIG[collection.format]?.ext || '.bru';
        let pathname = confinedBundlePath(relativePath);
        if (!pathname) throw new Error(`bru.runRequest: invalid request path - ${relativePath}`);
        if (!pathname.endsWith(extension)) pathname += extension;
        const nested = cloneDeep(findItemInCollection(collection, pathname));
        if (!nested) throw new Error(`bru.runRequest: invalid request path - ${relativePath}`);
        const nestedResult = await executeItem(nested, depth + 1);
        return nestedResult.response;
      };
      let executed;
      if (item.type === 'grpc-request') {
        executed = await executeGRPC(item, state, runRequest, streamTimeoutMS);
      } else if (item.type === 'ws-request') {
        executed = await executeWebSocket(item, state, runRequest, streamTimeoutMS);
      } else {
        executed = await executeHTTP(item, state, runRequest);
      }
      emit(requestID, { type: 'result', result: executed.requestResult });
      emitEvidence(requestID, item, action.name || 'Bruno run', executed.evidence, collection);
      if (!executed.requestResult.success) {
        failed = true;
        firstError ||= executed.requestResult.error_message || `${item.name} failed`;
        if (params.bail && !haltState.halted) {
          haltState.halted = true;
          if (!executionAbortController.signal.aborted) executionAbortController.abort();
          for (const client of activeGRPCClients) client.clearAllConnections();
          for (const client of activeWSClients) client.clearAllConnections();
        }
      }
      return {
        response: executed.response || {
          status: executed.requestResult.status_code,
          data: executed.requestResult.response_body,
          responseTime: executed.requestResult.duration_ms
        },
        nextRequestName: executed.evidence?.nextRequestName,
        stopExecution: executed.evidence?.stopExecution,
        success: executed.requestResult.success
      };
    };

    let index = 0;
    while (index < iterationItems.length) {
      if (cancelled() || (params.bail && haltState.halted)) break;
      const result = await executeItem(iterationItems[index]);
      if (result.stopExecution || (params.bail && !result.success)) break;
      if (result.nextRequestName !== undefined) {
        jumps += 1;
        if (jumps > maxJumps) throw new Error(`Bruno next-request jumps exceed ${maxJumps}`);
        if (result.nextRequestName === null) break;
        const next = iterationItems.findIndex((item) => item.name === result.nextRequestName);
        index = next >= 0 ? next : index + 1;
      } else {
        index += 1;
      }
      if (delayMS > 0 && index < iterationItems.length) {
        await new Promise((resolve) => setTimeout(resolve, delayMS));
      }
    }
    return { iterationIndex, runtimeVariables: state.runtimeVariables };
  };

  if (String(params.execution || 'sequential').toLowerCase() === 'parallel' && dataRows.length > 1) {
    const operatorMaximum = Math.max(1, Number(config.limits?.max_parallel_requests || 1));
    const requestedMaximum = Math.max(1, Number(params.max_parallel || operatorMaximum));
    const maximum = Math.min(operatorMaximum, requestedMaximum, dataRows.length);
    let cursor = 0;
    await Promise.all(Array.from({ length: maximum }, async () => {
      while (!cancelled() && !(params.bail && haltState.halted)) {
        const iterationIndex = cursor++;
        if (iterationIndex >= dataRows.length) return;
        const result = await runIteration(iterationIndex);
        if (result) iterationStates.push(result);
      }
    }));
  } else {
    for (let iterationIndex = 0; iterationIndex < dataRows.length; iterationIndex += 1) {
      const result = await runIteration(iterationIndex);
      if (result) iterationStates.push(result);
      if (cancelled() || (params.bail && haltState.halted)) break;
    }
  }

  if (persistSession && iterationStates.length > 0) {
    iterationStates.sort((left, right) => left.iterationIndex - right.iterationIndex);
    const finalRuntime = iterationStates[iterationStates.length - 1].runtimeVariables;
    for (const key of Object.keys(sessionRuntimeVariables)) delete sessionRuntimeVariables[key];
    Object.assign(sessionRuntimeVariables, finalRuntime);
  }

  return {
    action_name: action.name || 'Bruno run',
    sample_kind: 'aggregate',
    duration_ms: durationMS(start),
    success: !failed && !cancelled(),
    error_message: cancelled() ? 'Bruno execution cancelled' : firstError,
    request_type: 'bruno',
    operation_kind: 'collection_run',
    evidence: {
      selected_requests: items.length,
      data_iterations: dataRows.length,
      executed_iterations: iterationStates.length,
      data_source: params.data_source || '',
      sandbox: config.sandbox || 'safe',
      compatibility_version: config.compatibility_version,
      persist_session: persistSession
    }
  };
}

parentPort.on('message', async (message) => {
  if (message.type === 'cancel') {
    const requestID = message.requestID || activeRequestID;
    if (!message.requestID || requestID === activeRequestID) {
      if (requestID) cancelledRequestIDs.add(requestID);
      if (activeAbortController && !activeAbortController.signal.aborted) activeAbortController.abort();
      for (const client of activeGRPCClients) client.clearAllConnections();
      for (const client of activeWSClients) client.clearAllConnections();
    }
    return;
  }
  if (message.type !== 'execute') return;
  if (activeRequestID) {
    fail(message.requestID, new Error('Bruno worker is already executing another request'));
    return;
  }
  try {
    const result = await executeSelection(message.requestID, message.action);
    complete(message.requestID, result);
  } catch (error) {
    fail(message.requestID, error);
  } finally {
    cancelledRequestIDs.delete(message.requestID);
    if (activeRequestID === message.requestID) {
      activeRequestID = null;
      activeAbortController = null;
      activeOutputBudget = null;
    }
  }
});
