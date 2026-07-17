'use strict';

function grpcProxyConfig(rawURL) {
  const value = String(rawURL || '').trim();
  if (!value) return { proxyUrl: null };
  const parsed = new URL(value);
  if (parsed.protocol !== 'http:') {
    throw new Error('gRPC execution requires the governed egress broker to expose an HTTP CONNECT endpoint');
  }
  return { proxyUrl: value };
}

module.exports = {
  grpcProxyConfig
};
