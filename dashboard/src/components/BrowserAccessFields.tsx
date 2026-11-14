import type { BrowserClientCertificate, BrowserConfig } from '../types/scripts'
import { CredentialField, SelectField, TextField } from './RuntimeConfigFieldControls'
import { patchConfigBlock } from './protocolRuntimeOptions'

/**
 * Playwright browser-context identity and access settings: locale, timezone,
 * the pre-authenticated storage state, HTTP credentials, the browser's own
 * proxy and origin-scoped client certificates. Everything here writes
 * config.browser only; a browser script never reads config.http, so these
 * never share an editor with the HTTP Transport proxy, TLS or auth tabs.
 */
export function BrowserAccessFields({ browser, onChange }: {
  browser: BrowserConfig | undefined
  onChange: (browser: BrowserConfig) => void
}) {
  const current = browser || {}
  const set = (path: string, value: unknown) => onChange(patchConfigBlock(current, path, value))
  const certificates = current.client_certificates || []
  const setCertificates = (next: BrowserClientCertificate[]) => set('client_certificates', next)
  const updateCertificate = (index: number, field: keyof BrowserClientCertificate, value: string) => {
    setCertificates(certificates.map((certificate, certificateIndex) => {
      if (certificateIndex !== index) return certificate
      const nextCertificate = { ...certificate }
      if (value === '') delete nextCertificate[field]
      else nextCertificate[field] = value
      return nextCertificate
    }))
  }

  return (
    <div data-testid="browser-access-fields">
      <div className="settings-sub-group" data-canonical-group="target_identity">
        <h5>Locale &amp; timezone</h5>
        <div className="form-grid">
          <TextField label="Browser locale" path="config.browser.locale" value={current.locale} placeholder="en-US" onChange={value => set('locale', value)} />
          <TextField label="Browser timezone" path="config.browser.timezone" value={current.timezone} placeholder="America/Chicago" onChange={value => set('timezone', value)} />
        </div>
      </div>

      <div className="settings-sub-group" data-canonical-group="security_proxy_auth">
        <h5>Signed-in session</h5>
        <div className="form-grid">
          <CredentialField fullWidth label="Storage state" path="config.browser.storage_state" value={current.storage_state}
            placeholder="{{secret.CHECKOUT_STORAGE_STATE}}" onChange={value => set('storage_state', value)} />
        </div>

        <h5>Browser HTTP credentials</h5>
        <div className="form-grid">
          <TextField label="HTTP auth username" path="config.browser.http_credentials.username" value={current.http_credentials?.username} onChange={value => set('http_credentials.username', value)} />
          <CredentialField label="HTTP auth password" path="config.browser.http_credentials.password" value={current.http_credentials?.password}
            placeholder="{{secret.BROWSER_AUTH_PASSWORD}}" onChange={value => set('http_credentials.password', value)} />
          <TextField label="HTTP auth origin" path="config.browser.http_credentials.origin" value={current.http_credentials?.origin} placeholder="https://app.example.test" onChange={value => set('http_credentials.origin', value)} />
          <SelectField label="Send HTTP credentials" path="config.browser.http_credentials.send" value={current.http_credentials?.send} onChange={value => set('http_credentials.send', value)} />
        </div>

        <h5>Browser proxy</h5>
        <div className="form-grid">
          <TextField label="Browser proxy server" path="config.browser.proxy.server" value={current.proxy?.server} placeholder="http://proxy.example.test:3128" onChange={value => set('proxy.server', value)} />
          <TextField label="Browser proxy bypass" path="config.browser.proxy.bypass" value={current.proxy?.bypass} placeholder="localhost, .internal.example.test" onChange={value => set('proxy.bypass', value)} />
          <TextField label="Browser proxy username" path="config.browser.proxy.username" value={current.proxy?.username} onChange={value => set('proxy.username', value)} />
          <CredentialField label="Browser proxy password" path="config.browser.proxy.password" value={current.proxy?.password}
            placeholder="{{secret.BROWSER_PROXY_PASSWORD}}" onChange={value => set('proxy.password', value)} />
        </div>

        <div data-catalog-path="config.browser.client_certificates" role="group" aria-label="Browser client certificates">
          <h5>Client certificates</h5>
          {certificates.length === 0 && <div className="cell-muted">No client certificates defined.</div>}
          {certificates.map((certificate, index) => (
            <div key={`browser-client-certificate-${index}`} className="form-grid" data-testid="browser-client-certificate">
              <label><span>Certificate origin</span>
                <input aria-label={`Certificate ${index + 1} origin`} value={certificate.origin || ''} placeholder="https://mtls.example.test" onChange={event => updateCertificate(index, 'origin', event.target.value)} /></label>
              <label><span>Certificate file</span>
                <input aria-label={`Certificate ${index + 1} certificate file`} value={certificate.cert_path || ''} placeholder="certs/client.pem" onChange={event => updateCertificate(index, 'cert_path', event.target.value)} /></label>
              <label><span>Private key file</span>
                <input aria-label={`Certificate ${index + 1} private key file`} value={certificate.key_path || ''} placeholder="certs/client.key" onChange={event => updateCertificate(index, 'key_path', event.target.value)} /></label>
              <label><span>PFX file</span>
                <input aria-label={`Certificate ${index + 1} PFX file`} value={certificate.pfx_path || ''} placeholder="certs/client.pfx" onChange={event => updateCertificate(index, 'pfx_path', event.target.value)} /></label>
              <CredentialField label={`Certificate ${index + 1} passphrase`} path="config.browser.client_certificates.passphrase" value={certificate.passphrase}
                placeholder="{{secret.CLIENT_CERT_PASSPHRASE}}" onChange={value => updateCertificate(index, 'passphrase', value)} />
              <button type="button" className="btn btn-sm btn-ghost" onClick={() => setCertificates(certificates.filter((_, certificateIndex) => certificateIndex !== index))}>
                Remove certificate {index + 1}
              </button>
            </div>
          ))}
          <button type="button" className="btn btn-sm btn-secondary" onClick={() => setCertificates([...certificates, {}])}>Add client certificate</button>
        </div>
      </div>
    </div>
  )
}
