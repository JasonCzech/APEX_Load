import { useEffect, useRef, useState } from 'react'

import { JavaCompileError } from '../hooks/api/useScriptApi'
import type { JavaCompileDiagnostic } from '../types/scriptImports'
import type { JavaClasspathAsset } from '../types/scripts'

/** The classpath entry an imported Java Vuser's own compiled classes take. */
export const JAVA_SCRIPT_CLASSES_ENTRY = 'apexload-script-classes.jar'

export interface JavaClasspathResolverProps {
  /** Exact classpath entries declared by the imported Java Vuser. */
  classpath: string[]
  /** Currently attached JAR bindings (one per entry, keyed by entry string). */
  assets: JavaClasspathAsset[]
  onChange: (assets: JavaClasspathAsset[]) => void
  /** Uploads one JAR for a classpath entry and resolves to its binding. */
  onUpload: (file: File, entry: string) => Promise<JavaClasspathAsset>
  /** The entry the script's own compiled classes take. */
  compileEntry?: string
  /** Compiles the reviewed source into a jar for compileEntry. */
  onCompile?: () => Promise<JavaClasspathAsset>
  /**
   * Classpath entries the compiler could not see when it built the jar now
   * bound to compileEntry. The caller keeps this: a new binding remounts the
   * panel.
   */
  compiledWithout?: string[]
  disabled?: boolean
}

interface CompileFailure {
  message: string
  diagnostics: JavaCompileDiagnostic[]
  unavailable: string[]
}

function formatDiagnostic(diagnostic: JavaCompileDiagnostic): string {
  const where = diagnostic.file ? `${diagnostic.file}${diagnostic.line ? `:${diagnostic.line}` : ''}: ` : ''
  return `${where}${diagnostic.message}`
}

/**
 * JavaClasspathResolver lets a user attach the JAR file for each imported Java
 * Vuser classpath entry whose dependency is not pre-provisioned on the runner.
 * Attached JARs are uploaded to the controller and materialized on the runner
 * at launch; unattached entries are assumed to already exist on every runner.
 */
// The upload endpoint is fail-closed server-side, so an operator who has not
// opted in gets a bare "not enabled" body with no remediation. Name the env var
// instead — otherwise the panel looks broken rather than switched off.
function classpathUploadErrorMessage(uploadError: unknown): string {
  const message = uploadError instanceof Error ? uploadError.message : ''
  if (message.toLowerCase().includes('classpath upload is not enabled')) {
    return 'JAR uploads are disabled on this controller. An operator must set APEXLOAD_JAVA_VUSER_CLASSPATH_UPLOAD_ENABLED=true, or provision this dependency on the runners instead.'
  }
  return message || 'Failed to upload JAR'
}

export function JavaClasspathResolver({ classpath, assets, onChange, onUpload, compileEntry, onCompile, compiledWithout, disabled }: JavaClasspathResolverProps) {
  const [busyEntry, setBusyEntry] = useState<string | null>(null)
  const [compiling, setCompiling] = useState(false)
  const [dragEntry, setDragEntry] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [compileFailure, setCompileFailure] = useState<CompileFailure | null>(null)
  const uploadInFlight = useRef(false)
  const mounted = useRef(true)
  const disabledRef = useRef(disabled)
  disabledRef.current = disabled
  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false }
  }, [])
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
    if (!file || disabledRef.current || uploadInFlight.current) return
    uploadInFlight.current = true
    setError('')
    setBusyEntry(entry)
    try {
      const asset = await onUpload(file, entry)
      if (!mounted.current) return
      onChange([...assets.filter(existing => existing.entry !== entry), asset])
    } catch (uploadError: unknown) {
      if (mounted.current) setError(classpathUploadErrorMessage(uploadError))
    } finally {
      uploadInFlight.current = false
      if (mounted.current) setBusyEntry(null)
    }
  }

  // Compiling shares the serialized-upload invariant: it merges into the same
  // captured `assets` list, so no other attach or remove may run meanwhile.
  const handleCompile = async (entry: string) => {
    if (!onCompile || disabledRef.current || uploadInFlight.current) return
    uploadInFlight.current = true
    setError('')
    setCompileFailure(null)
    setBusyEntry(entry)
    setCompiling(true)
    try {
      const asset = await onCompile()
      if (!mounted.current) return
      onChange([...assets.filter(existing => existing.entry !== entry), asset])
    } catch (compileError: unknown) {
      if (!mounted.current) return
      if (compileError instanceof JavaCompileError) {
        setCompileFailure({ message: compileError.message, diagnostics: compileError.diagnostics, unavailable: compileError.unavailableClasspath })
      } else {
        setError(compileError instanceof Error && compileError.message ? compileError.message : 'Failed to compile the script source')
      }
    } finally {
      uploadInFlight.current = false
      if (mounted.current) {
        setBusyEntry(null)
        setCompiling(false)
      }
    }
  }

  const removeAsset = (entry: string) => {
    if (disabledRef.current || uploadInFlight.current) return
    onChange(assets.filter(asset => asset.entry !== entry))
  }

  if (classpath.length === 0) {
    return <div className="cell-muted">This Java Vuser declares no classpath entries, so there are no dependencies to attach.</div>
  }

  return (
    <div className="java-classpath-resolver">
      <p className="cell-muted">
        Attach the JAR for any classpath entry your runners do not already provide. Attached JARs are uploaded to the
        controller and materialized on the runner at launch; entries you leave unattached are assumed to already exist on
        every runner. Attaching or swapping a JAR changes what code runs, so it requires re-approval before launch.
        Attached JARs run on a local runner only — a distributed run that carries them is blocked at launch, so provision
        those dependencies on the executors instead.
      </p>
      <ul className="java-classpath-resolver-list">
        {classpath.map((entry, index) => {
          const asset = assetForEntry(entry)
          const compilable = Boolean(onCompile) && entry === compileEntry
          // Dropping is refused under the same conditions as the Attach button
          // so the serialized-upload invariant above holds for both paths.
          const dropRefused = Boolean(disabled) || busyEntry !== null || Boolean(asset)
          return (
            <li
              key={`${entry}:${index}`}
              className={dragEntry === entry ? 'java-classpath-resolver-row drag-over' : 'java-classpath-resolver-row'}
              // preventDefault runs even when the drop is refused. Returning
              // early instead would leave the row a non-target, so the browser
              // would handle the dropped file itself and navigate away from the
              // import — losing the in-progress review. Refusing means claiming
              // the drop and ignoring it, signalled by dropEffect 'none'.
              onDragEnter={event => {
                event.preventDefault()
                if (dropRefused) return
                setDragEntry(entry)
              }}
              onDragOver={event => {
                event.preventDefault()
                event.dataTransfer.dropEffect = dropRefused ? 'none' : 'copy'
              }}
              onDragLeave={event => {
                if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDragEntry(null)
              }}
              onDrop={event => {
                event.preventDefault()
                setDragEntry(null)
                if (dropRefused) return
                void handleFile(entry, event.dataTransfer.files?.[0])
              }}
            >
              <code className="entry-path" title={entry}>{entry}</code>
              {asset ? (
                <span className="entry-asset attached">
                  <span className="chip-file">{asset.compiled_from ? 'Compiled from the reviewed source' : (asset.file_name || 'dependency.jar')}</span>
                  <span className="chip-sha" title={asset.sha256}>{asset.sha256.slice(0, 8)}</span>
                  <button
                    type="button"
                    className="chip-remove"
                    disabled={disabled || busyEntry !== null}
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
                    disabled={disabled || busyEntry !== null}
                    accept=".jar,application/java-archive"
                    className="java-classpath-file-input"
                    aria-label={`Attach JAR for ${entry}`}
                    onChange={event => {
                      void handleFile(entry, event.target.files?.[0])
                      event.target.value = ''
                    }}
                  />
                  {compilable && (
                    <button
                      type="button"
                      className="row-add"
                      disabled={disabled || busyEntry !== null}
                      onClick={() => void handleCompile(entry)}
                    >
                      {compiling ? 'Compiling…' : 'Compile source'}
                    </button>
                  )}
                  <button
                    type="button"
                    className="row-add"
                    disabled={disabled || busyEntry !== null}
                    onClick={() => inputs.current[entry]?.click()}
                  >
                    {busyEntry === entry && !compiling ? 'Uploading…' : 'Drop or attach JAR'}
                  </button>
                </span>
              )}
            </li>
          )
        })}
      </ul>
      {compiledWithout && compiledWithout.length > 0 && !compileFailure && (
        <p className="cell-muted java-compile-outcome" role="status">
          The source compiled without these classpath entries, which the Java runner could not provide. Attach them unless every runner provides them: {compiledWithout.join(', ')}
        </p>
      )}
      {compileFailure && (
        <div className="cell-error java-compile-outcome" role="alert">
          <p>{compileFailure.message}</p>
          {compileFailure.diagnostics.length > 0 && (
            <ul className="java-compile-diagnostics">
              {compileFailure.diagnostics.map((diagnostic, index) => (
                <li key={index}><code>{formatDiagnostic(diagnostic)}</code></li>
              ))}
            </ul>
          )}
          {compileFailure.unavailable.length > 0 && (
            <p>These classpath entries were not available to the compiler. Attach them, then compile again: {compileFailure.unavailable.join(', ')}</p>
          )}
        </div>
      )}
      {error && <div className="cell-error" role="alert">{error}</div>}
    </div>
  )
}
