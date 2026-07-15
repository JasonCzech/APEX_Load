import { useEffect, useMemo, useState } from 'react'

import {
  type SecretBindingRequirement,
  type SecretBindingSelection,
} from '../types/scriptImports'
import { getErrorMessage } from '../utils/errorUtils'
import { isValidSecretBindingName } from './secretBindingUtils'
import './ScriptImportReview.css'

interface Props {
  requirements: SecretBindingRequirement[]
  onApply: (bindings: SecretBindingSelection[]) => void | Promise<void>
}

function initialNames(requirements: SecretBindingRequirement[]): Record<string, string> {
  return Object.fromEntries(requirements.map(requirement => [requirement.id, requirement.suggested_name || '']))
}

function bindingNameError(value: string): string {
  const name = value.trim()
  if (!name) return 'Enter a logical binding name.'
  if (!isValidSecretBindingName(name)) {
    return 'Use 1–249 uppercase letters, numbers, or underscores.'
  }
  return ''
}

export default function SecretBindingReview({ requirements, onApply }: Props) {
  const [names, setNames] = useState<Record<string, string>>(() => initialNames(requirements))
  const [applying, setApplying] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    setNames(initialNames(requirements))
    setApplying(false)
    setError('')
  }, [requirements])

  const validationErrors = useMemo(
    () => Object.fromEntries(requirements.map(requirement => [
      requirement.id,
      bindingNameError(names[requirement.id] ?? ''),
    ])),
    [names, requirements],
  )
  const reuseCounts = useMemo(() => {
    const counts = new Map<string, number>()
    for (const requirement of requirements) {
      const name = (names[requirement.id] ?? '').trim()
      if (name) counts.set(name, (counts.get(name) ?? 0) + 1)
    }
    return counts
  }, [names, requirements])
  const valid = requirements.length > 0 && requirements.every(requirement => !validationErrors[requirement.id])

  if (requirements.length === 0) return null

  const updateName = (id: string, name: string) => {
    setNames(current => ({ ...current, [id]: name }))
    setError('')
  }

  const handleApply = async () => {
    if (!valid || applying) return
    setApplying(true)
    setError('')
    try {
      await onApply(requirements.map(requirement => ({
        requirement_id: requirement.id,
        secret_name: names[requirement.id].trim(),
      })))
    } catch (err: unknown) {
      setError(getErrorMessage(err, 'Failed to apply secret bindings'))
    } finally {
      setApplying(false)
    }
  }

  return (
    <section className="secret-binding-review" aria-labelledby="secret-binding-review-title">
      <div className="secret-binding-review-header">
        <div>
          <h4 id="secret-binding-review-title">Resolve Redacted Credentials ({requirements.length})</h4>
          <p>
            Choose a logical secret binding for each redacted location. The imported script will store only
            a <code>{'{{secret.NAME}}'}</code> reference, never a credential value.
          </p>
          <p className="secret-binding-review-reuse-help">
            To reuse one binding, enter the same logical name on multiple rows.
          </p>
        </div>
        <button className="btn btn-primary" type="button" onClick={handleApply} disabled={!valid || applying}>
          {applying ? 'Applying...' : `Apply Bindings (${requirements.length})`}
        </button>
      </div>

      <div className="secret-binding-review-blocked" role="status">
        Saving is disabled until every redacted credential has a valid secret binding.
      </div>
      {error && <div className="secret-binding-review-error" role="alert">{error}</div>}

      <ol className="secret-binding-review-list">
        {requirements.map((requirement, index) => {
          const inputId = `secret-binding-${requirement.id}-${index}`
          const helpId = `${inputId}-help`
          const errorId = `${inputId}-error`
          const name = names[requirement.id] ?? ''
          const nameError = validationErrors[requirement.id]
          const reused = name.trim() !== '' && (reuseCounts.get(name.trim()) ?? 0) > 1

          return (
            <li key={requirement.id} className="secret-binding-review-item">
              <div className="secret-binding-review-location">
                <div className="secret-binding-review-label-row">
                  <strong>{requirement.label}</strong>
                  {reused && <span className="secret-binding-review-reuse-badge">Reuses binding</span>}
                </div>
                {requirement.context && <p>{requirement.context}</p>}
                <code>{requirement.path}</code>
              </div>
              <div className="secret-binding-review-control">
                <label htmlFor={inputId}>Logical binding name</label>
                <input
                  id={inputId}
                  type="text"
                  value={name}
                  onChange={event => updateName(requirement.id, event.target.value)}
                  aria-describedby={`${helpId}${nameError ? ` ${errorId}` : ''}`}
                  aria-invalid={nameError ? 'true' : 'false'}
                  autoComplete="off"
                  spellCheck={false}
                />
                <span id={helpId} className="secret-binding-review-reference">
                  Script reference: <code>{name.trim() ? `{{secret.${name.trim()}}}` : '{{secret.NAME}}'}</code>
                </span>
                {nameError && <span id={errorId} className="secret-binding-review-field-error">{nameError}</span>}
              </div>
            </li>
          )
        })}
      </ol>
    </section>
  )
}
