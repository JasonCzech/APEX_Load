import { diagnoseError, type ErrorDiagnostic as ErrorDiagnosticModel } from '../utils/errorUtils'
import './ErrorDiagnostic.css'

type ErrorDiagnosticProps = {
  diagnostic?: ErrorDiagnosticModel
  error?: unknown
  fallback?: string
  title?: string
  compact?: boolean
  className?: string
}

const severityLabel: Record<ErrorDiagnosticModel['severity'], string> = {
  critical: 'Critical',
  error: 'Error',
  warning: 'Warning',
  info: 'Info',
}

export default function ErrorDiagnostic({
  diagnostic,
  error,
  fallback = 'Something went wrong.',
  title,
  compact = false,
  className,
}: ErrorDiagnosticProps) {
  const model = diagnostic || diagnoseError(error, fallback)
  const heading = title || model.summary
  const showSummary = Boolean(title && model.summary !== title)
  const nextSteps = compact ? model.nextSteps.slice(0, 2) : model.nextSteps

  return (
    <div
      className={['error-diagnostic', `error-diagnostic--${model.severity}`, compact ? 'error-diagnostic--compact' : '', className || ''].filter(Boolean).join(' ')}
      role="alert"
    >
      <div className="error-diagnostic-heading">
        <span className="error-diagnostic-severity">{severityLabel[model.severity]}</span>
        <strong>{heading}</strong>
      </div>
      {showSummary && <p className="error-diagnostic-summary">{model.summary}</p>}
      {model.likelyCause && (
        <p className="error-diagnostic-cause">
          <span>Likely cause:</span> {model.likelyCause}
        </p>
      )}
      {nextSteps.length > 0 && (
        <div className="error-diagnostic-next">
          <span>Next steps:</span>
          <ul>
            {nextSteps.map((step) => (
              <li key={step}>{step}</li>
            ))}
          </ul>
        </div>
      )}
      {!compact && model.evidence && model.evidence.length > 0 && (
        <details className="error-diagnostic-evidence">
          <summary>Evidence</summary>
          <ul>
            {model.evidence.map((item) => (
              <li key={item}>{item}</li>
            ))}
          </ul>
        </details>
      )}
    </div>
  )
}
