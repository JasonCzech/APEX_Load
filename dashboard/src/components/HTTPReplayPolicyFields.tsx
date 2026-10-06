import type { HTTPConfig } from '../types/scripts'

type Props = {
  value?: HTTPConfig
  inherited?: HTTPConfig
  onChange: (patch: Partial<HTTPConfig>) => void
  section: 'timeouts' | 'connection' | 'session'
}

/** Shared native HTTP policies for script settings and scenario overrides. */
export function HTTPReplayPolicyFields({ value = {}, inherited, onChange, section }: Props) {
  const number = (key: 'receive_buffer_bytes' | 'max_meta_refresh' | 'content_check_error_limit' | 'page_timeout_ms' | 'cache_max_unused_iterations' | 'challenge_delay_ms', label: string, max: number) => (
    <label><span>{label}</span><input className="settings-input" type="number" min={0} max={max}
      placeholder={inherited ? `Inherited: ${inherited[key] ?? 0}` : '0 (disabled)'} value={value[key] ?? ''}
      onChange={event => onChange({ [key]: event.target.value === '' ? undefined : Number(event.target.value) })} /></label>
  )
  const list = (key: 'cache_excluded_mime_types' | 'html_excluded_mime_types', label: string) => (
    <label><span>{label}</span><textarea className="settings-input" rows={3} value={value[key]?.join('\n') ?? ''}
      placeholder={inherited?.[key]?.join('\n') || 'One exact MIME type per line'}
      onChange={event => onChange({ [key]: event.target.value.split('\n').map(item => item.trim()).filter(Boolean) })} /></label>
  )
  const toggle = (key: 'http_errors_as_warnings' | 'fail_noncritical_resources' | 'resource_timeout_as_warning' | 'legacy_html_checks' | 'dns_cache', label: string) => <label><span>{label}</span><select className="settings-input" value={value[key] == null ? '' : String(value[key])} onChange={event => onChange({ [key]: event.target.value === '' ? undefined : event.target.value === 'true' })}>
    <option value="">{inherited ? `Inherit (${inherited[key] == null ? 'default' : inherited[key] ? 'enabled' : 'disabled'})` : 'Default'}</option><option value="true">Enabled</option><option value="false">Disabled</option>
  </select></label>
  if (section === 'timeouts') return <>{number('page_timeout_ms', 'Whole-page timeout (ms)', 3600000)}{number('challenge_delay_ms', 'Digest authentication delay (ms)', 3600000)}<div className="form-hint">Covers the request, redirects, and embedded resources. Zero disables this additional deadline.</div></>
  if (section === 'connection') return <label><span>IP family</span><select className="settings-input" value={value.ip_version ?? ''}
    onChange={event => onChange({ ip_version: (event.target.value || undefined) as HTTPConfig['ip_version'] })}>
    <option value="">{inherited ? `Inherit (${inherited.ip_version || 'auto'})` : 'Default (auto)'}</option>
    <option value="auto">Automatic</option><option value="ipv4">IPv4</option><option value="ipv6">IPv6</option>
  </select><small>Controls outgoing TCP connections, including connections to proxies.</small></label>
  return <>
    {number('receive_buffer_bytes', 'Response read buffer (bytes)', 1048576)}
    {number('max_meta_refresh', 'Meta-refresh limit per URL', 50)}
    {number('content_check_error_limit', 'Content-check error match limit', 1000)}
    {toggle('http_errors_as_warnings', 'HTTP errors as warnings')}
    {toggle('fail_noncritical_resources', 'Fail on non-critical resource errors')}
    {toggle('resource_timeout_as_warning', 'Resource timeouts as warnings')}
    {toggle('legacy_html_checks', 'Legacy HTML checks')}
    {toggle('dns_cache', 'Per-VUser DNS cache')}
    <div className="form-hint">Warnings preserve explicit assertion failures. Legacy checks affect imported HTML checks only. DNS entries expire after 60 seconds; system resolver caches remain independent.</div>
    <label><span>Aura actions that failed in the recording</span><select className="settings-input" value={value.aura_recorded_failures ?? ''}
      onChange={event => onChange({ aura_recorded_failures: (event.target.value || undefined) as HTTPConfig['aura_recorded_failures'] })}>
      <option value="">{inherited ? `Inherit (${inherited.aura_recorded_failures === 'accept_recorded' ? 'accept the recorded outcome' : 'require success'})` : 'Default (require success)'}</option>
      <option value="require_success">Require success</option><option value="accept_recorded">Accept the recorded outcome</option>
    </select><small>Applies to imported Aura actions that already failed in the recording. Require success fails them and labels the failure as recorded; accepting passes an action that ends in the same state as the recording.</small></label>
    {number('cache_max_unused_iterations', 'Unused iterations before cache eviction', 1000000)}
    {list('cache_excluded_mime_types', 'Do not cache MIME types')}
    <label><span>Discover HTML resources in</span><select className="settings-input" value={value.html_parse_mode ?? ''}
      onChange={event => onChange({ html_parse_mode: (event.target.value || undefined) as HTTPConfig['html_parse_mode'] })}>
      <option value="">{inherited ? `Inherit (${inherited.html_parse_mode || 'HTML/XHTML'})` : 'Default (HTML/XHTML)'}</option>
      <option value="html">HTML only</option><option value="text">Text except text/xml</option><option value="any">Any content type</option>
    </select></label>
    {list('html_excluded_mime_types', 'Do not discover resources in MIME types')}
    <div className="form-hint">Cache rules require browser cache simulation. Discovery rules require resource downloading; recorded resource URLs remain eligible.</div>
  </>
}
