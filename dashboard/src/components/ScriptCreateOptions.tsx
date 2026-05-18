import { useEffect, useState } from 'react'
import { type AuthoringTemplate } from '../types'
import { type ScriptDefinition } from '../types/scripts'
import { useApi } from '../hooks/useApi'

interface Props {
  onBack: () => void
  onStartApiScript: () => void
  onStartSoapScript: () => void
  onStartJavaHttpScript: () => void
  onOpenJavaHttpImport: () => void
  onOpenSampleImport: () => void
  onStartWebScript: () => void
  onOpenRecorder: () => void
  onStartJmsScript: () => void
  onStartTemplateScript: (script: ScriptDefinition) => void
  onOpenLoadRunnerImport: () => void
  onOpenJMeterImport: () => void
  onOpenDevWebImport: () => void
  onOpenHARImport: () => void
}

export default function ScriptCreateOptions({
  onBack,
  onStartApiScript,
  onStartSoapScript,
  onStartJavaHttpScript,
  onOpenJavaHttpImport,
  onOpenSampleImport,
  onStartWebScript,
  onOpenRecorder,
  onStartJmsScript,
  onStartTemplateScript,
  onOpenLoadRunnerImport,
  onOpenJMeterImport,
  onOpenDevWebImport,
  onOpenHARImport,
}: Props) {
  const api = useApi()
  const [templates, setTemplates] = useState<AuthoringTemplate[]>([])

  useEffect(() => {
    let cancelled = false
    api.listAuthoringTemplates()
      .then(response => {
        if (!cancelled) setTemplates(response.templates || [])
      })
      .catch(() => {
        if (!cancelled) setTemplates([])
      })
    return () => {
      cancelled = true
    }
  }, [api])

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h2>Create Script</h2>
          <p className="page-subtitle">Choose how you want to create a reusable script.</p>
        </div>
        <button className="btn btn-secondary" onClick={onBack}>Back to Library</button>
      </div>
      <div className="script-create-lanes">
        {templates.length > 0 && (
          <section className="script-create-lane api">
            <div className="script-create-lane-header">
              <span className="script-create-kicker">Backend catalog</span>
              <h3>Starter Templates</h3>
              <p>Start from the controller-owned template catalog so UI choices stay aligned with validated backend DSL examples.</p>
            </div>
            <div className="script-create-grid">
              {templates.map(template => (
                <button key={template.id} className="script-create-option" type="button" onClick={() => onStartTemplateScript(template.script)}>
                  <span className="script-create-type-badge">{template.protocol.toUpperCase()}</span>
                  <h3>{template.name}</h3>
                  <p>{template.description}</p>
                  {template.recommended_scenario && (
                    <small>{template.recommended_scenario.vusers || 1} VUsers / {template.recommended_scenario.duration || 'script default'}</small>
                  )}
                </button>
              ))}
            </div>
          </section>
        )}

        <section className="script-create-lane api">
          <div className="script-create-lane-header">
            <span className="script-create-kicker">API level</span>
            <h3>HTTP/API Scripts</h3>
            <p>Exercise service endpoints directly with HTTP methods, headers, payloads, extractors, assertions, and parameter data.</p>
          </div>
          <div className="script-create-grid">
            <button className="script-create-option primary" onClick={onStartApiScript}>
              <span className="script-create-type-badge">API</span>
              <h3>API Visual Editor</h3>
              <p>Build a protocol-level script from requests and reusable action definitions.</p>
            </button>
            <button className="script-create-option" onClick={onStartSoapScript}>
              <span className="script-create-type-badge">SOAP</span>
              <h3>Web Services (SOAP)</h3>
              <p>Create a SOAP draft with WS-Security, namespaces, fault handling, and SOAPAction support.</p>
            </button>
            <button className="script-create-option" onClick={onStartJavaHttpScript}>
              <span className="script-create-type-badge">JAVA</span>
              <h3>Java over HTTP</h3>
              <p>Create a first-class Java-over-HTTP draft with the `java_serialized_object_base64` codec and choose embedded or sidecar helper execution on the HTTP transport.</p>
            </button>
            <button className="script-create-option" onClick={onOpenSampleImport}>
              <span className="script-create-type-badge">API</span>
              <h3>Build From Sample</h3>
              <p>Generate an API script from sample traffic or payload examples.</p>
            </button>
          </div>
        </section>

        <section className="script-create-lane web">
          <div className="script-create-lane-header">
            <span className="script-create-kicker">Web / HTML</span>
            <h3>Browser Scripts</h3>
            <p>Drive a rendered page with browser navigation, clicks, form fills, waits, screenshots, DOM capture, and browser replay.</p>
          </div>
          <div className="script-create-grid">
            <button className="script-create-option primary" onClick={onStartWebScript}>
              <span className="script-create-type-badge web">WEB</span>
              <h3>Web/HTML Visual Editor</h3>
              <p>Build a browser-level script that interacts with the page like a user.</p>
            </button>
            <button className="script-create-option" onClick={onOpenRecorder}>
              <span className="script-create-type-badge web">WEB</span>
              <h3>Browser Recorder</h3>
              <p>Capture a browser session and convert selected traffic into reusable Web/HTML script assets.</p>
            </button>
          </div>
        </section>

        <section className="script-create-lane jms">
          <div className="script-create-lane-header">
            <span className="script-create-kicker">Messaging</span>
            <h3>JMS Scripts</h3>
            <p>Model Solace queue/topic workloads with reusable publish, consume, and request/reply actions.</p>
          </div>
          <div className="script-create-grid">
            <button className="script-create-option primary" onClick={onStartJmsScript}>
              <span className="script-create-type-badge jms">JMS</span>
              <h3>JMS Solace Script</h3>
              <p>Create a reusable JMS script draft for Solace publish/consume flows.</p>
            </button>
          </div>
        </section>

        <section className="script-create-lane import">
          <div className="script-create-lane-header">
            <span className="script-create-kicker">Migration</span>
            <h3>Migration Imports</h3>
            <p>Bring existing LoadRunner and JMeter assets forward while preserving source metadata and conversion notes for review.</p>
          </div>
          <div className="script-create-grid">
            <button className="script-create-option primary" onClick={onOpenLoadRunnerImport}>
              <span className="script-create-type-badge import">LR</span>
              <h3>Import LoadRunner</h3>
              <p>Convert a LoadRunner ZIP into a Stormrunner script and keep unsupported functions visible for cleanup.</p>
            </button>
            <button className="script-create-option" type="button" disabled title="Use Import LoadRunner today for SOAP-style LoadRunner request assets.">
              <span className="script-create-type-badge import">SOAP</span>
              <h3>Import SOAP Assets</h3>
              <p>Dedicated SOAP import remains staged. For now, use Import LoadRunner for SOAP-style request assets and review the converted draft.</p>
            </button>
            <button
              className="script-create-option"
              onClick={onOpenJavaHttpImport}
              title="Import raw HTTP or cURL samples that already preserve base64-encoded serialized Java payloads."
            >
              <span className="script-create-type-badge import">JAVA</span>
              <h3>Import Java over HTTP</h3>
              <p>Build a Java-over-HTTP draft from raw HTTP or cURL samples that already carry opaque base64-encoded serialized payloads.</p>
            </button>
            <button className="script-create-option" onClick={onOpenJMeterImport}>
              <span className="script-create-type-badge import">JMX</span>
              <h3>Import JMeter</h3>
              <p>Convert a JMeter .jmx plan into HTTP scripts with warnings, unsupported elements, and scenario draft context.</p>
            </button>
            <button className="script-create-option" onClick={onOpenDevWebImport}>
              <span className="script-create-type-badge import">DEV</span>
              <h3>Import DevWeb</h3>
              <p>Convert DevWeb JavaScript assets into HTTP, WebSocket, or gRPC script drafts for review.</p>
            </button>
            <button className="script-create-option" onClick={onOpenHARImport}>
              <span className="script-create-type-badge import">HAR</span>
              <h3>Import HAR</h3>
              <p>Convert Chrome DevTools HAR traffic into an HTTP script with detected base URL and correlations.</p>
            </button>
          </div>
        </section>
      </div>
    </div>
  )
}
