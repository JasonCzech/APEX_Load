import './EffectiveRuntimeSettingsView.css'
import { RuntimeFieldProvenanceBadge } from './ScenarioOverrideFields'
import {
  buildEffectiveRuntimeSummary,
  type EffectiveRuntimeEntry,
  type EffectiveRuntimeGroupSource,
  type StoredRuntimeRow,
} from './effectiveRuntimeSummaryModel'

// The one read-only view of the runtime settings each group runs with, shared
// by the queued scenario preview, the test manifest, the run report and the
// archive. Values come from the API's masked rows; see effectiveRuntimeSummaryModel.

export interface EffectiveRuntimeSettingsViewProps {
  groups: readonly EffectiveRuntimeGroupSource[]
  /** The scenario (run-level) override row every group inherits. */
  scenarioRow?: StoredRuntimeRow
  /** Show each group's name above its settings (off for a single group shown in its own row). */
  showGroupNames?: boolean
  title?: string
}

function EntryValue({ entry }: { entry: EffectiveRuntimeEntry }) {
  if (entry.kind === 'secret_reference') {
    return <span className="effective-runtime-value secret-reference">{entry.text} <code>{`{{secret.${entry.secretName}}}`}</code></span>
  }
  return <span className={`effective-runtime-value kind-${entry.kind}`}>{entry.text}</span>
}

function GroupSettings({ group, scenarioRow, showName }: { group: EffectiveRuntimeGroupSource; scenarioRow?: StoredRuntimeRow; showName: boolean }) {
  const summary = buildEffectiveRuntimeSummary(group, scenarioRow)
  return (
    <div className="effective-runtime-group" data-testid="effective-runtime-group" data-group-name={group.name}>
      {showName && <h5 className="effective-runtime-group-name">{group.name}{group.protocol ? ` · ${group.protocol}` : ''}</h5>}
      {summary.categories.length === 0 ? (
        <p className="empty-hint">No group or scenario runtime overrides. This group runs with its script&apos;s runtime settings.</p>
      ) : summary.categories.map(category => (
        <section key={category.id} className="effective-runtime-category" data-canonical-group={category.id} aria-label={category.label}>
          <h6>{category.label}</h6>
          <dl>
            {category.entries.map(entry => (
              <div key={entry.path} className="effective-runtime-entry" data-runtime-path={entry.path} data-provenance={entry.provenance}>
                <dt>{entry.label}</dt>
                <dd>
                  <EntryValue entry={entry} />
                  {entry.provenance && <RuntimeFieldProvenanceBadge provenance={entry.provenance} />}
                </dd>
              </div>
            ))}
          </dl>
        </section>
      ))}
      {summary.categories.length > 0 && !summary.scriptKnown && (
        <p className="effective-runtime-note">Settings not listed here run with the script&apos;s own values.</p>
      )}
    </div>
  )
}

export function EffectiveRuntimeSettingsView({ groups, scenarioRow, showGroupNames = true, title = 'Effective runtime settings' }: EffectiveRuntimeSettingsViewProps) {
  return (
    <div className="effective-runtime-settings" data-testid="effective-runtime-settings">
      <h4>{title}</h4>
      {groups.length === 0 ? (
        <p className="empty-hint">No groups were recorded.</p>
      ) : groups.map((group, index) => (
        <GroupSettings key={`${group.name}-${index}`} group={group} scenarioRow={scenarioRow} showName={showGroupNames} />
      ))}
    </div>
  )
}
