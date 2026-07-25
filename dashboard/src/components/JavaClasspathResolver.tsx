import { useRef, useState } from 'react'

import type { JavaClasspathAsset } from '../types/scripts'

export interface JavaClasspathResolverProps {
  /** Exact classpath entries declared by the imported Java Vuser. */
  classpath: string[]
  /** Currently attached JAR bindings (one per entry, keyed by entry string). */
  assets: JavaClasspathAsset[]
  onChange: (assets: JavaClasspathAsset[]) => void
  /** Uploads one JAR for a classpath entry and resolves to its binding. */
  onUpload: (file: File, entry: string) => Promise<JavaClasspathAsset>
  disabled?: boolean
}

/**
 * JavaClasspathResolver lets a user attach the JAR file for each imported Java
 * Vuser classpath entry whose dependency is not pre-provisioned on the runner.
 * Attached JARs are uploaded to the controller and materialized on the runner
 * at launch; unattached entries are assumed to already exist on every runner.
 */
export function JavaClasspathResolver({ classpath, assets, onChange, onUpload, disabled }: JavaClasspathResolverProps) {
  const [busyEntry, setBusyEntry] = useState<string | null>(null)
  const [error, setError] = useState('')
  const inputs = useRef<Record<string, HTMLInputElement | null>>({})

  // Bindings are keyed by the exact entry string (matching the backend's
  // entry-keyed model). A classpath that legally repeats the same entry string
  // therefore shares one binding across those rows by design.
  const assetForEntry = (entry: string) => assets.find(asset => asset.entry === entry)

  // Uploads are serialized: handleFile merges into the `assets` value captured
  // in its render closure, so a second attach starting before the first resolves
  // would compute from a stale list and silently drop the earlier binding. While
  // any upload is in flight every attach/remove control is disabled, so the
  // closure can never go stale between start and merge.
  const handleFile = async (entry: string, file: File | undefined) => {
    if (!file || busyEntry !== null) return
    setError('')
    setBusyEntry(entry)
    try {
      const asset = await onUpload(file, entry)
      onChange([...assets.filter(existing => existing.entry !== entry), asset])
    } catch (uploadError: unknown) {
      setError(uploadError instanceof Error ? uploadError.message : 'Failed to upload JAR')
    } finally {
      setBusyEntry(null)
    }
  }

  const removeAsset = (entry: string) => onChange(assets.filter(asset => asset.entry !== entry))

  if (classpath.length === 0) {
    return <div className="cell-muted">This Java Vuser declares no classpath entries, so there are no dependencies to attach.</div>
  }

  return (
    <div className="java-classpath-resolver">
      <p className="cell-muted">
        Attach the JAR for any classpath entry your runners do not already provide. Attached JARs are uploaded to the
        controller and materialized on the runner at launch; entries you leave unattached are assumed to already exist on
        every runner. Attaching or swapping a JAR changes what code runs, so it requires re-approval before launch.
      </p>
      <ul className="java-classpath-resolver-list">
        {classpath.map((entry, index) => {
          const asset = assetForEntry(entry)
          return (
            <li key={`${entry}:${index}`} className="java-classpath-resolver-row">
              <code className="entry-path" title={entry}>{entry}</code>
              {asset ? (
                <span className="entry-asset attached">
                  <span className="chip-file">{asset.file_name || 'dependency.jar'}</span>
                  <span className="chip-sha" title={asset.sha256}>{asset.sha256.slice(0, 8)}</span>
                  <button
                    type="button"
                    className="chip-remove"
                    disabled={busyEntry !== null}
                    onClick={() => removeAsset(entry)}
                    aria-label={`Remove attached JAR for ${entry}`}
                  >
                    ✕
                  </button>
                </span>
              ) : (
                <span className="entry-asset unattached">
                  <input
                    ref={element => {
                      inputs.current[entry] = element
                    }}
                    type="file"
                    accept=".jar,application/java-archive"
                    style={{ display: 'none' }}
                    onChange={event => {
                      void handleFile(entry, event.target.files?.[0])
                      event.target.value = ''
                    }}
                  />
                  <button
                    type="button"
                    className="row-add"
                    disabled={disabled || busyEntry !== null}
                    onClick={() => inputs.current[entry]?.click()}
                  >
                    {busyEntry === entry ? 'Uploading…' : 'Attach JAR'}
                  </button>
                </span>
              )}
            </li>
          )
        })}
      </ul>
      {error && <div className="cell-error" role="alert">{error}</div>}
    </div>
  )
}
