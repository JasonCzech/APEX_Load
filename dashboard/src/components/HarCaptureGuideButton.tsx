import { useState } from 'react'
import ImportCoverageButton from './ImportCoverageButton'
import './HarCaptureGuideButton.css'

const JQ_SLIM_COMMAND =
  `jq '(.log.entries[] | select(._resourceType == "script" or ._resourceType == "stylesheet" or ._resourceType == "image" or ._resourceType == "font" or ._resourceType == "media") | .response.content.text) |= ""' raw.har > slim.har`

export default function HarCaptureGuideButton({
  triggerClassName = 'btn btn-secondary btn-sm',
  label = 'HAR capture guide',
}: {
  triggerClassName?: string
  label?: string
}) {
  const [copied, setCopied] = useState(false)

  const handleCopyCommand = async () => {
    try {
      await navigator.clipboard?.writeText(JQ_SLIM_COMMAND)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 2000)
    } catch {
      setCopied(false)
    }
  }

  return (
    <ImportCoverageButton
      label={label}
      title="Optimal browser HAR capture settings"
      subtitle="Capture every correlation, redirect, and request body needed for 2-HAR diffing and DSL conversion while keeping HAR files 85–95% smaller."
      triggerAriaLabel="HAR capture guide"
      triggerClassName={triggerClassName}
    >
      <div className="har-capture-guide">
        <p className="har-capture-guide-intro">
          In single-page applications such as Salesforce Lightning, static JavaScript, CSS, font, and media bundles account for
          <strong> 90–95% of raw HAR byte size</strong> while HTML bootstrap documents and XHR/Fetch API calls typically total under 5 MB.
          Use the browser DevTools settings below so nothing needed for correlation or DSL generation is stripped—without exporting
          hundreds of megabytes of duplicate static assets.
        </p>

        <section className="har-capture-guide-section" aria-label="Browser DevTools settings">
          <h4>1. Recommended DevTools Network settings (Chrome, Edge, Firefox)</h4>
          <div className="har-capture-guide-table-wrap" tabIndex={0} role="region" aria-label="Recommended browser DevTools HAR settings">
            <table className="har-capture-guide-table">
              <thead>
                <tr>
                  <th scope="col">DevTools setting</th>
                  <th scope="col">Recommended state</th>
                  <th scope="col">Impact on comparison &amp; DSL conversion</th>
                </tr>
              </thead>
              <tbody>
                <tr>
                  <td><strong>Preserve log</strong> (Network toolbar)</td>
                  <td><span className="har-capture-guide-badge on">Checked</span></td>
                  <td>
                    Retains multi-hop login and logout redirect chains (such as <code>302</code> → <code>frontdoor.jsp</code> → <code>VFLoginFlow</code> → <code>/one/one.app</code>) across full-page navigations.
                  </td>
                </tr>
                <tr>
                  <td><strong>Allow to generate HAR (with sensitive data)</strong> (<code>Settings ⚙ → Preferences → Network</code>, Chrome/Edge 130+)</td>
                  <td><span className="har-capture-guide-badge on">Checked</span></td>
                  <td>
                    Click-and-hold the Network <strong>Export</strong> arrow and choose <strong>Export HAR (with sensitive data)…</strong> (Firefox: right-click → <strong>Save All As HAR</strong>). Preserves <code>Cookie</code>, <code>Set-Cookie</code> (<code>__Host-ERIC_*</code>, <code>sid</code>), and <code>Authorization</code> headers for correlation verification. APEX Load automatically scrubs and binds credentials to governed project secrets on import.
                  </td>
                </tr>
                <tr>
                  <td><strong>Incognito / InPrivate window</strong> (close completely between captures)</td>
                  <td><span className="har-capture-guide-badge action">2 separate sessions</span></td>
                  <td>
                    Ensures each capture in a 2-HAR comparison starts unauthenticated and receives genuinely distinct session tokens, CSRF nonces, and Aura context values—and prevents browser extensions from injecting background traffic.
                  </td>
                </tr>
                <tr>
                  <td><strong>Clear network log</strong> (<code>🚫</code> button) right before login</td>
                  <td><span className="har-capture-guide-badge action">Clear after login page loads</span></td>
                  <td>
                    Open the login URL first, let initial static framework bundles finish downloading, click <strong>Clear network log</strong>, then submit credentials. Drops pre-login static bundle bloat while keeping the entire auth POST and redirect chain.
                  </td>
                </tr>
                <tr>
                  <td><strong>Disable cache</strong> (Network toolbar)</td>
                  <td><span className="har-capture-guide-badge off">Unchecked</span></td>
                  <td>
                    Dynamic HTML pages and XHR/Fetch API calls (<code>Cache-Control: no-store</code>) still record full wire payloads, while immutable versioned JS/CSS bundles are served from cache instead of re-downloading tens of megabytes per navigation.
                  </td>
                </tr>
                <tr>
                  <td><strong>Stop recording</strong> (<code>Ctrl+E</code> / <code>Cmd+E</code>) before export</td>
                  <td><span className="har-capture-guide-badge action">Wait 2–3 s, then stop</span></td>
                  <td>
                    Let in-flight XHR/Fetch requests finish, then stop recording before exporting. Exporting while a <code>POST</code> or <code>sendBeacon</code> request is still pending writes <code>Content-Length &gt; 0</code> with an empty <code>postData.text</code> (an unexported-body replay blocker).
                  </td>
                </tr>
                <tr>
                  <td><strong>Capture screenshots</strong> (Network settings gear)</td>
                  <td><span className="har-capture-guide-badge off">Unchecked</span></td>
                  <td>
                    Prevents base64-encoded filmstrip frames from inflating the exported HAR file.
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>

        <div className="har-capture-guide-callout" role="note">
          <strong>Important Chrome/Edge DevTools caveat:</strong> The Network filter bar (<code>Fetch/XHR</code>, <code>Doc</code>) only filters the on-screen table—clicking <strong>Export HAR</strong> still exports all recorded entries. Never use <strong>Copy → Copy all listed as HAR</strong>: clipboard HAR copies omit response bodies (<code>response.content.text</code>), which prevents correlation extractors from finding server-produced tokens.
        </div>

        <section className="har-capture-guide-section" aria-label="2-HAR recording workflow">
          <h4>2. Step-by-step 2-HAR comparison capture workflow</h4>
          <ol className="har-capture-guide-steps">
            <li>
              <strong>Capture 1 (baseline):</strong> Open a fresh Incognito/InPrivate window, open DevTools → <strong>Network</strong>, check <strong>Preserve log</strong>, uncheck <strong>Disable cache</strong>, load the login page, click <strong>Clear network log</strong>, sign in, execute the business steps, sign out, wait 2–3 seconds, stop recording, and choose <strong>Export HAR (with sensitive data)…</strong>.
            </li>
            <li>
              <strong>Capture 2 (comparison):</strong> Close the first Incognito window completely so session cookies cannot carry over. Open a new Incognito window and repeat the exact same user journey with the same settings to produce the second HAR.
            </li>
            <li>
              <strong>Import together:</strong> Drag both files (or their <code>.zip</code> archives) onto the HAR dropzone at once to populate primary and comparison captures in a single step.
            </li>
          </ol>
        </section>

        <section className="har-capture-guide-section" aria-label="Reducing existing HAR file size">
          <h4>3. Shrinking an already-recorded large HAR by 85–95%</h4>
          <ul className="har-capture-guide-steps">
            <li>
              <strong>Zero-CLI option — upload as <code>.zip</code>:</strong> Both HAR dropzones accept <code>.zip</code> archives directly (up to 300 MB per capture). Compressing a raw JSON HAR typically reduces file size by <strong>80–90%</strong> (for example, 170 MB → ~18 MB) with zero loss of request or response data.
            </li>
            <li>
              <strong>Optional CLI slimming (<code>jq</code>):</strong> Strip response body text <em>only</em> from static asset entries (<code>script</code>, <code>stylesheet</code>, <code>image</code>, <code>font</code>, <code>media</code>) while keeping every HTML document (<code>auraConfig</code>, login redirects), XHR/Fetch request and response body, cookie, header, and timing intact:
            </li>
          </ul>
          <div className="har-capture-guide-code-header">
            <span className="sample-import-muted">Preserves all <code>document</code> and <code>xhr</code>/<code>fetch</code> bodies</span>
            <button type="button" className="btn btn-secondary btn-sm" onClick={() => void handleCopyCommand()}>
              {copied ? 'Copied command' : 'Copy jq command'}
            </button>
          </div>
          <pre className="har-capture-guide-code" tabIndex={0} role="region" aria-label="jq HAR slimming command"><code>{JQ_SLIM_COMMAND}</code></pre>
        </section>
      </div>
    </ImportCoverageButton>
  )
}
