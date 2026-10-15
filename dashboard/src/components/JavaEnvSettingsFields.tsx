import { useMemo, type ReactNode } from 'react'

import type { JavaVuserConfig } from '../types/scripts'
import { ExactStringListRows } from './ExactStringListRows'

/** The script-owned Java Env settings that are editable in the RTS panel and
 * overridable per scenario group. Operator-owned runner fields (enablement,
 * runner command, harness jar, work dir, capacity) are deliberately excluded.
 * classpath_assets (user-uploaded JAR bindings) round-trip through here so the
 * component can display which classpath entries have an attached dependency;
 * uploading a JAR happens in the import wizard, which has the import context. */
export type JavaEnvSettingsValue = Pick<
  JavaVuserConfig,
  | 'jdk_selection'
  | 'jdk_home'
  | 'additional_vm_parameters'
  | 'classpath'
  | 'classpath_assets'
  | 'use_xbootclasspath'
  | 'dedicated_class_loader'
  | 'skip_parameter_evaluation'
  | 'jvm_sharing'
  | 'vusers_per_jvm'
>

export interface JavaEnvSettingsFieldsProps {
  value: JavaEnvSettingsValue
  onChange: (patch: JavaEnvSettingsValue) => void
  /** Unique prefix so radio groups don't collide when several instances render (e.g. the group override editor). */
  idPrefix: string
  /** VM parameter tokens captured from an imported LoadRunner script, surfaced as a picker. */
  importedVMParameters?: string[]
  /** Overrides the default helper text under the dedicated-class-loader checkbox (lifecycle-aware in the script editor). */
  dedicatedClassLoaderNote?: ReactNode
  /** Override surfaces: emptying a list inherits the script's entries (backend is
   * non-empty-wins), it does not clear them. Shows an honest note when true. */
  listOverrideInherits?: boolean
}

/**
 * JavaEnvSettingsFields is the single source of truth for the LoadRunner-shaped
 * "Java Env Settings" controls, reused by the Script Author runtime-settings
 * panel and the scenario runtime-override editors. It renders four subsections:
 * Virtual Machine, Class Loading, Performance, and Classpath.
 */
export function JavaEnvSettingsFields({
  value,
  onChange,
  idPrefix,
  importedVMParameters,
  dedicatedClassLoaderNote,
  listOverrideInherits = false,
}: JavaEnvSettingsFieldsProps) {
  const patch = (next: Partial<JavaEnvSettingsValue>) => onChange({ ...value, ...next })
  const selection = value.jdk_selection || 'internal'
  const vmParameters = useMemo(
    () => value.additional_vm_parameters ?? [],
    [value.additional_vm_parameters],
  )
  const classpathAssets = value.classpath_assets ?? []

  const importCandidates = useMemo(() => {
    const current = new Set(vmParameters)
    return (importedVMParameters ?? []).filter(token => token.trim() !== '' && !current.has(token))
  }, [importedVMParameters, vmParameters])

  const addImportedParameter = (token: string) => {
    if (!token || vmParameters.includes(token)) return
    patch({ additional_vm_parameters: [...vmParameters, token] })
  }

  return (
    <div className="java-env-settings-fields">
      <div className="settings-sub-group">
        <h5>Virtual Machine</h5>
        <div className="runtime-checkbox-list" role="radiogroup" aria-label="Java VM selection">
          <label className="checkbox-label">
            <input
              type="radio"
              name={`${idPrefix}-jdk-selection`}
              checked={selection === 'internal'}
              onChange={() => patch({ jdk_selection: 'internal' })}
            />
            <span>Use internal logic to locate JDK</span>
          </label>
          <label className="checkbox-label">
            <input
              type="radio"
              name={`${idPrefix}-jdk-selection`}
              checked={selection === 'specified'}
              onChange={() => patch({ jdk_selection: 'specified' })}
            />
            <span>Use the specified JDK</span>
          </label>
        </div>
        <div className="form-grid">
          <label className="full-width">
            <span>JDK Home</span>
            <input
              aria-label="JDK Home"
              value={value.jdk_home || ''}
              disabled={selection !== 'specified'}
              onChange={event => patch({ jdk_home: event.target.value })}
              placeholder="/opt/jdks/temurin-21 or C:\\Java\\jdk-21"
            />
          </label>
        </div>
        <div className="cell-muted">
          Specified homes are trusted runner-local paths. The runner derives only bin/java (or bin/java.exe), requires Java 17 or newer, and never falls back silently.
        </div>

        <div className="settings-field-block">
          <span className="settings-field-label">Additional VM Parameters</span>
          {/* Listing every candidate beats a single-select picker here: a
              LoadRunner script routinely imports a dozen tokens, and the user
              needs to compare them before deciding which to carry over. */}
          {importCandidates.length > 0 && (
            <div className="java-env-import-picker">
              <div className="java-env-import-picker-header">
                <span>Imported from script ({importCandidates.length})</span>
                <button
                  type="button"
                  className="row-add"
                  onClick={() => patch({ additional_vm_parameters: [...vmParameters, ...importCandidates] })}
                >
                  Add all
                </button>
              </div>
              <ul aria-label="Imported VM parameters">
                {importCandidates.map((token, index) => (
                  <li key={`${token}:${index}`}>
                    <code>{token}</code>
                    <button type="button" className="row-add" onClick={() => addImportedParameter(token)}>
                      Add
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          )}
          <ExactStringListRows
            ariaLabel="Additional VM Parameters"
            values={vmParameters}
            placeholder="-Xmx512m"
            addLabel="Add VM parameter"
            onChange={values => patch({ additional_vm_parameters: values })}
          />
          <div className="cell-muted">
            Each entry is one exact JVM argument token; spaces stay part of that argument. Launcher takeover, argfiles, agents, native hooks, custom loaders, and command-triggering error hooks are rejected.
          </div>
          {listOverrideInherits && (
            <div className="cell-muted" role="note">
              Removing every entry inherits the script's VM parameters; an override cannot clear the list to empty.
            </div>
          )}
        </div>

        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={value.use_xbootclasspath ?? false}
            onChange={event => patch({ use_xbootclasspath: event.target.checked })}
          />
          <span>Use -Xbootclasspath parameters</span>
        </label>
        {value.use_xbootclasspath && (
          <div className="cell-muted" role="note">
            Java 9 and newer do not replay the legacy prepend behavior. APEX Load uses the configured ordinary classpath and records an explicit compatibility warning; it does not emit an Xboot flag.
          </div>
        )}
      </div>

      <div className="settings-sub-group">
        <h5>Class Loading</h5>
        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={value.dedicated_class_loader ?? false}
            onChange={event => patch({ dedicated_class_loader: event.target.checked })}
          />
          <span>Load each Vuser using a dedicated class loader</span>
        </label>
        {dedicatedClassLoaderNote && <div className="cell-muted">{dedicatedClassLoaderNote}</div>}
      </div>

      <div className="settings-sub-group">
        <h5>JVM sharing</h5>
        <div className="runtime-checkbox-list" role="radiogroup" aria-label="JVM sharing">
          {listOverrideInherits && (
            <label className="checkbox-label">
              <input
                type="radio"
                name={`${idPrefix}-jvm-sharing`}
                checked={!value.jvm_sharing}
                onChange={() => patch({ jvm_sharing: undefined, vusers_per_jvm: undefined })}
              />
              <span>Use the script's setting</span>
            </label>
          )}
          <label className="checkbox-label">
            <input
              type="radio"
              name={`${idPrefix}-jvm-sharing`}
              checked={value.jvm_sharing === 'dedicated' || (!listOverrideInherits && !value.jvm_sharing)}
              onChange={() => patch({ jvm_sharing: listOverrideInherits ? 'dedicated' : undefined, vusers_per_jvm: undefined })}
            />
            <span>One JVM per VUser</span>
          </label>
          <label className="checkbox-label">
            <input
              type="radio"
              name={`${idPrefix}-jvm-sharing`}
              checked={value.jvm_sharing === 'shared'}
              onChange={() => patch({ jvm_sharing: 'shared' })}
            />
            <span>Shared JVMs</span>
          </label>
        </div>
        {value.jvm_sharing === 'shared' && (
          <div className="form-grid">
            <label>
              <span>VUsers per JVM</span>
              <input
                aria-label="VUsers per JVM"
                type="number"
                min={2}
                max={1024}
                step={1}
                value={value.vusers_per_jvm ?? ''}
                placeholder="Runner default"
                onChange={event => {
                  const parsed = Number.parseInt(event.target.value, 10)
                  patch({ vusers_per_jvm: Number.isFinite(parsed) && parsed > 0 ? parsed : undefined })
                }}
              />
            </label>
          </div>
        )}
        <div className="cell-muted">
          Shared JVMs run many VUsers of this group in each JVM, so one load generator holds thousands of Java VUsers instead of about 25. Each VUser keeps its own Actions instance, script classes, lr context and thread. They need a runner with shared JVMs enabled, Java 17 or 21, jar-only classpaths, exactly one init, action and end call, and no native code. Otherwise the group runs one JVM per VUser and the run manifest says why.
        </div>
        {!listOverrideInherits && (
          <div className="cell-muted" role="note">
            Saving this on an approved Java script needs a new authoring approval. A scenario group can choose JVM sharing for its runs without one.
          </div>
        )}
      </div>

      <div className="settings-sub-group">
        <h5>Performance</h5>
        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={value.skip_parameter_evaluation ?? false}
            onChange={event => patch({ skip_parameter_evaluation: event.target.checked })}
          />
          <span>Skip automatic parameter evaluation</span>
        </label>
        <div className="cell-muted">
          Skips the automatic snapshot of data-source rows and named parameters that is otherwise injected before each Java action. Explicitly authored parameters are still delivered, and parameter rows are not advanced per invocation.
        </div>
      </div>

      <div className="settings-sub-group">
        <h5>Classpath</h5>
        <ExactStringListRows
          ariaLabel="Java Vuser classpath"
          values={value.classpath}
          placeholder="/opt/apexload/java-vuser/lib/application.jar"
          addLabel="Add classpath entry"
          reorderable
          onChange={values => patch({ classpath: values })}
        />
        <div className="cell-muted">
          Paths must already exist on every assigned runner and load in listed order, unless a JAR is attached below. Imported LoadRunner classpath entries are prepopulated here; Java source is not compiled.
        </div>
        {classpathAssets.length > 0 && (
          <div className="java-env-classpath-assets" role="list" aria-label="Attached classpath JARs">
            {classpathAssets.map(asset => (
              <div className="java-env-classpath-asset-chip" role="listitem" key={`${asset.entry}:${asset.sha256}`}>
                <span className="chip-file" title={asset.entry}>{asset.file_name || asset.entry}</span>
                <span className="chip-sha" title={asset.sha256}>{asset.sha256.slice(0, 8)}</span>
                <span className="chip-entry">→ {asset.entry}</span>
                <button
                  type="button"
                  className="chip-remove"
                  aria-label={`Remove attached JAR for ${asset.entry}`}
                  onClick={() => patch({ classpath_assets: classpathAssets.filter(a => a !== asset) })}
                >
                  ✕
                </button>
              </div>
            ))}
          </div>
        )}
        {listOverrideInherits && (
          <div className="cell-muted" role="note">
            Removing every entry inherits the script's classpath; an override cannot clear it to empty.
          </div>
        )}
      </div>
    </div>
  )
}
