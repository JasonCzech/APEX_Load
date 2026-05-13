import { type ScriptDefinition } from '../types/scripts'

export function createApiDraft(): ScriptDefinition {
  return {
    name: '',
    version: '1.0.0',
    protocol: 'http',
    config: {
      base_url: '',
      think_time: { type: 'fixed', mean_ms: 1000 },
      pacing: { type: 'none' },
      run_logic: { iterations: 0, auto_transactions: 'none' },
      error_handling: { continue_on_error: false },
      log: { level: 'standard' },
    },
    actions: [],
  }
}

export function createWebDraft(): ScriptDefinition {
  return {
    name: '',
    version: '1.0.0',
    protocol: 'browser',
    config: {
      base_url: '',
      think_time: { type: 'fixed', mean_ms: 1000 },
      pacing: { type: 'none' },
      run_logic: { iterations: 0, auto_transactions: 'none' },
      error_handling: { continue_on_error: false },
      log: { level: 'standard' },
      browser: {
        engine: 'chromium',
        headless: true,
        capture_screenshots: 'per_action',
        viewport: { width: 1440, height: 900 },
      },
    },
    actions: [],
  }
}

export function createJmsDraft(): ScriptDefinition {
  return {
    name: '',
    version: '1.0.0',
    protocol: 'jms-solace',
    config: {
      base_url: '',
      think_time: { type: 'fixed', mean_ms: 1000 },
      pacing: { type: 'none' },
      run_logic: { iterations: 0, auto_transactions: 'none' },
      error_handling: { continue_on_error: false },
      log: { level: 'standard' },
      jms: {
        provider: 'solace',
        broker_url: '',
        message_vpn: '',
        client_id: '',
        destination: '',
        destination_type: 'queue',
        reply_to: '',
        acknowledgement: 'auto',
        delivery_mode: 'persistent',
        timeout_ms: 5000,
        mock: true,
      },
    },
    actions: [],
  }
}