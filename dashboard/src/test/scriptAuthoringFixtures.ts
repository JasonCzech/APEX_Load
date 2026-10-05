import type { ScriptDefinition } from '../types/scripts'
import type { ScriptAuthoringBaseline, StoredScript } from '../types/storedContent'

export function authoringFixture(script: Partial<ScriptDefinition> = {}, id = script.id || 'script-1', projectId = script.project_id || 'project-1', revisionChar = 'a'): ScriptAuthoringBaseline {
  const revision = `scr-v1-${revisionChar.repeat(64)}`
  const document = { name: 'Fixture', version: '1', protocol: 'http', ...script, id, project_id: projectId, config: { base_url: '', ...script.config }, actions: script.actions || [] } as ScriptDefinition
  return { etag: `"script-authoring-v1:${revision}"`, projection: {
    projection_version: 'apexload.script_authoring.v1', id, project_id: projectId, folder_id: document.folder_id || null,
    content_revision: revision, script: document, library_placement: { application_id: null, release_ids: [] },
  } }
}

export function savedAuthoringFixture(script: Partial<ScriptDefinition> = {}, id = script.id || 'script-1', projectId = script.project_id || 'project-1', revisionChar = 'b'): StoredScript {
  const baseline = authoringFixture(script, id, projectId, revisionChar)
  return { id, project_id: projectId, name: baseline.projection.script.name, version: baseline.projection.script.version, protocol: baseline.projection.script.protocol, created_at: '', script: baseline.projection.script,
    content_revision: baseline.projection.content_revision, authoring_projection: baseline.projection }
}

const pairedTestAdapters = new WeakMap<object, { getScriptAuthoring: (id: string, projectId?: string) => Promise<ScriptAuthoringBaseline>; getScript: (id: string) => Promise<unknown> }>()
export function pairedAuthoringTestApi(getScript: (id: string) => Promise<unknown>) {
  const existing = pairedTestAdapters.get(getScript)
  if (existing) return existing
  const pending = new Map<string, Promise<unknown>>()
  const adapter = {
    getScriptAuthoring: async (id: string, projectId?: string) => {
      const response = Promise.resolve(getScript(id))
      pending.set(id, response)
      const stored = await response as { script?: Partial<ScriptDefinition> }
      return authoringFixture(stored?.script, id, projectId)
    },
    getScript: (id: string) => {
      const response = pending.get(id)
      pending.delete(id)
      return response || getScript(id)
    },
  }
  pairedTestAdapters.set(getScript, adapter)
  return adapter
}
