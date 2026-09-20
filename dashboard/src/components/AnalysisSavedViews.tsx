import { useEffect, useState } from 'react'

export type SavedAnalysisValues = Record<string, string>
interface SavedView {
  name: string
  values: SavedAnalysisValues
}
interface Props {
  storageKey: string
  values: SavedAnalysisValues
  onRestore: (values: SavedAnalysisValues) => void
}

// Saved locally, scoped to the application and analysis tab. No archive payloads
// or credentials are persisted. Storage can be disabled without breaking analysis.
export function AnalysisSavedViews({ storageKey, values, onRestore }: Props) {
  const [views, setViews] = useState<SavedView[]>([])
  const [name, setName] = useState('')
  const [selected, setSelected] = useState('')
  const [message, setMessage] = useState('')
  useEffect(() => {
    setSelected('')
    setMessage('')
    try {
      const data: unknown = JSON.parse(localStorage.getItem(storageKey) || '[]')
      setViews(
        Array.isArray(data)
          ? data
              .filter(
                (item): item is SavedView =>
                  item &&
                  typeof item.name === 'string' &&
                  item.name.length <= 80 &&
                  item.values &&
                  typeof item.values === 'object' &&
                  !Array.isArray(item.values) &&
                  Object.values(item.values).every(
                    (value) => typeof value === 'string',
                  ),
              )
              .slice(0, 20)
          : [],
      )
    } catch {
      setViews([])
    }
  }, [storageKey])
  const save = (next: SavedView[]) => {
    try {
      localStorage.setItem(storageKey, JSON.stringify(next))
      setViews(next)
      setMessage('Saved on this browser.')
    } catch {
      setMessage(
        'Browser storage is unavailable. Export the analysis to retain its context.',
      )
    }
  }
  return (
    <details className="analysis-settings">
      <summary>Saved comparisons</summary>
      <div className="analysis-controls">
        <label>
          Comparison name
          <input
            value={name}
            maxLength={80}
            onChange={(event) => setName(event.target.value)}
          />
        </label>
        <button
          type="button"
          className="btn btn-secondary btn-sm"
          disabled={
            !name.trim() ||
            (views.length >= 20 &&
              !views.some((view) => view.name === name.trim()))
          }
          onClick={() =>
            save([
              ...views.filter((view) => view.name !== name.trim()),
              { name: name.trim(), values },
            ])
          }
        >
          Save current comparison
        </button>
        <label>
          Saved comparison
          <select
            value={selected}
            onChange={(event) => setSelected(event.target.value)}
          >
            <option value="">Choose a saved comparison</option>
            {views.map((view) => (
              <option key={view.name} value={view.name}>
                {view.name}
              </option>
            ))}
          </select>
        </label>
        <button
          type="button"
          className="btn btn-secondary btn-sm"
          disabled={!views.some((view) => view.name === selected)}
          onClick={() => {
            const view = views.find((item) => item.name === selected)
            if (view) {
              onRestore(view.values)
              setMessage(
                'Comparison restored. Archived evidence is reloaded using the current analysis rules.',
              )
            }
          }}
        >
          Restore comparison
        </button>
        <button
          type="button"
          className="btn btn-secondary btn-sm"
          disabled={!selected}
          onClick={() => {
            save(views.filter((view) => view.name !== selected))
            setSelected('')
          }}
        >
          Delete saved comparison
        </button>
      </div>
      <p className="dashboard-context-note">
        Stores filters, baseline, and rules locally on this browser. Evidence is
        reloaded when restored; this is not a frozen report.
      </p>
      {message && <p role="status">{message}</p>}
    </details>
  )
}
