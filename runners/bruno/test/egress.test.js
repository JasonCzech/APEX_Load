'use strict';

const assert = require('node:assert/strict');
const test = require('node:test');
const { grpcProxyConfig } = require('../src/egress');

test('gRPC proxy configuration uses only an HTTP CONNECT broker endpoint', () => {
  assert.deepEqual(grpcProxyConfig(''), { proxyUrl: null });
  assert.deepEqual(
    grpcProxyConfig('http://broker.internal:8080'),
    { proxyUrl: 'http://broker.internal:8080' }
  );
  assert.throws(
    () => grpcProxyConfig('socks5://broker.internal:1080'),
    /HTTP CONNECT endpoint/
  );
  assert.throws(
    () => grpcProxyConfig('https://broker.internal:8443'),
    /HTTP CONNECT endpoint/
  );
});
