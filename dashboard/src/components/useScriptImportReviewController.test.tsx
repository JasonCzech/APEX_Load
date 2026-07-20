// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ScriptDefinition } from '../types/scripts'
import { EMPTY_SCRIPT } from './scriptEditorUtils'
import { useScriptImportReviewController } from './useScriptImportReviewController'

type Controller = ReturnType<typeof useScriptImportReviewController>

function importedScript(): ScriptDefinition {
  return {
    ...EMPTY_SCRIPT,
    actions: [{ name: 'Login', type: 'GET', target: '/login', headers: {}, body: '' }],
    data_sources: [{ name: 'users', type: 'csv', strategy: 'sequential' }],
    metadata: {
      import: {
        source: 'enterprise_archive',
        preserved: {
          parameter_file_sections: {
            'Users.prm': { Login: { Table: 'users.csv', ColumnName: 'username' } },
          },
          correlation_suggestions: [{
            name: 'token',
            source: 'body',
            expression: '$.token',
            found_at: 'Request 1',
            used_at: [],
          }],
          source_review: {
            mappings: [{ id: 'login', source_file: 'Action.c', target_path: 'actions[0]', status: 'converted' }],
          },
        },
      },
    },
  }
}

describe('useScriptImportReviewController', () => {
  let root: Root | null = null
  let container: HTMLDivElement | null = null

  afterEach(() => {
    if (root) act(() => root?.unmount())
    container?.remove()
    root = null
    container = null
  })

  function renderController(
    options: Parameters<typeof useScriptImportReviewController>[0],
  ): () => Controller {
    let latest: Controller | null = null
    function Harness() {
      latest = useScriptImportReviewController(options)
      return null
    }
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    act(() => root?.render(<Harness />))
    return () => {
      if (!latest) throw new Error('Controller did not render')
      return latest
    }
  }

  it('derives import mappings, navigation targets, and the review baseline', () => {
    const script = importedScript()
    const baseline = { ...script, actions: [{ ...script.actions[0], target: '/old-login' }] }
    const getController = renderController({
      activeCenterTabId: 'imported-metadata',
      editorHistory: { past: [], future: [] },
      importBaselineScript: baseline,
      onWorkspaceMaximizedChange: vi.fn(),
      script,
      scriptId: 'script-1',
    })

    const controller = getController()
    expect(controller.importReviewCollapsed).toBe(true)
    expect(controller.importedParameterFileMappings).toMatchObject([{
      section: { file: 'Users.prm', name: 'Login' },
      sourceName: 'users',
    }])
    expect(controller.importedCorrelationFocusTargets[0]).toMatchObject({
      foundAtActionIndex: 0,
      foundAtLabel: 'Request 1: Login',
    })
    expect(controller.reviewBaselineScript).toBe(baseline)
    expect(controller.draftDiffSummary).toMatchObject({ changed: true, actionChanges: 1 })
  })

  it('coordinates mapping-dialog and maximized-workspace UI state', () => {
    const onWorkspaceMaximizedChange = vi.fn()
    const getController = renderController({
      activeCenterTabId: 'imported-metadata',
      editorHistory: { past: [], future: [] },
      importBaselineScript: null,
      onWorkspaceMaximizedChange,
      script: importedScript(),
      scriptId: null,
    })

    act(() => {
      getController().setShowImportedParameterMappingsDialog(true)
      getController().toggleSourceMappingWorkspaceMaximized()
    })

    expect(getController().showImportedParameterMappingsDialog).toBe(true)
    expect(getController().sourceMappingWorkspaceMaximized).toBe(true)
    expect(onWorkspaceMaximizedChange).toHaveBeenCalledWith(true)
  })
})
