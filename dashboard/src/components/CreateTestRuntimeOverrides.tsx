import { ScenarioDraft } from '../types'
import RuntimeSettingsGroupEditor from './RuntimeSettingsGroupEditor'

type RuntimeSettingsState = ScenarioDraft['runtimeSettings']

interface Props {
  draftScope?: string
  runtimeSettings: RuntimeSettingsState
  /** Protocols of the groups already added to the test, so protocol-gated
   *  sections (HTTP transport, Web/HTTP, Java Env, Browser) stay editable when
   *  at least one group can use them. Empty/omitted shows every section. */
  groupProtocols?: Array<string | undefined>
  onReplaceRuntimeSettings: (next: RuntimeSettingsState) => void
}

/** Every protocol family with protocol-gated runtime sections; used when no
 *  group is added yet so the operator can still stage overrides. */
const ALL_RUNTIME_PROTOCOLS = ['http', 'browser', 'java_vuser']

/**
 * Test-level "Runtime Settings Overrides" for the Create Test modal. This is
 * the same editor used for per-group overrides in the scenario Run Settings —
 * one component, three surfaces — mounted against the single test-wide
 * settings object.
 */
export default function CreateTestRuntimeOverrides({ draftScope, runtimeSettings, groupProtocols, onReplaceRuntimeSettings }: Props) {
  const protocols = (groupProtocols || []).filter((protocol): protocol is string => Boolean(protocol && protocol.trim()))
  return (
    <RuntimeSettingsGroupEditor
      draftScope={draftScope}
      title="Runtime Settings Overrides"
      description="Test-level runtime overrides applied to every group in this run. Unset fields inherit each group's script defaults."
      hideApplyPanel
      groups={[{ name: 'Test overrides', scriptId: 'test-overrides' }]}
      focusedIndex={0}
      onFocusChange={() => {}}
      overrides={[runtimeSettings]}
      onChange={(_, next) => onReplaceRuntimeSettings(next ?? { log_level: '', auto_transactions: '' })}
      onApplyToGroups={() => {}}
      applicableProtocols={protocols.length > 0 ? protocols : ALL_RUNTIME_PROTOCOLS}
    />
  )
}
