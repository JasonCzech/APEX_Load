interface Props {
  onBack: () => void
  onStartApiScript: () => void
  onOpenSampleImport: () => void
  onStartWebScript: () => void
  onOpenRecorder: () => void
  onStartJmsScript: () => void
  onOpenLoadRunnerImport: () => void
  onOpenJMeterImport: () => void
}

export default function ScriptCreateOptions({
  onBack,
  onStartApiScript,
  onOpenSampleImport,
  onStartWebScript,
  onOpenRecorder,
  onStartJmsScript,
  onOpenLoadRunnerImport,
  onOpenJMeterImport,
}: Props) {
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
            <button className="script-create-option" onClick={onOpenJMeterImport}>
              <span className="script-create-type-badge import">JMX</span>
              <h3>Import JMeter</h3>
              <p>Convert a JMeter .jmx plan into HTTP scripts with warnings, unsupported elements, and scenario draft context.</p>
            </button>
          </div>
        </section>
      </div>
    </div>
  )
}