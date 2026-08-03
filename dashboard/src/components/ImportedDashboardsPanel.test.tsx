// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ImportedDashboard, ImportedDashboardImportReview, Project } from '../types'
import { NavigationGuardProvider } from '../hooks/NavigationGuardRoot'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const listImportedDashboards = vi.fn()
const importGrafanaDashboard = vi.fn()
const importGrafanaDashboardByUID = vi.fn()
const commitGrafanaDashboardImport = vi.fn()
const previewGrafanaDashboardImport = vi.fn()
const resolveGrafanaImportLabelValues = vi.fn()
const getImportedDashboardPanels = vi.fn()
const deleteImportedDashboard = vi.fn()
const patchImportedDashboard = vi.fn()

const projectA: Project = { id: 'project-a', name: 'A', created_at: '', updated_at: '' }
const projectB: Project = { id: 'project-b', name: 'B', created_at: '', updated_at: '' }

function dashboard(id: string, projectId: string): ImportedDashboard {
  return { id, project_id: projectId, name: id, enabled: true, panels: [] }
}

vi.mock('../hooks/api/useSystemApi', () => ({
  useSystemApi: () => ({
    listImportedDashboards,
    importGrafanaDashboard,
    importGrafanaDashboardByUID,
    commitGrafanaDashboardImport,
    previewGrafanaDashboardImport,
    resolveGrafanaImportLabelValues,
    getImportedDashboardPanels,
    deleteImportedDashboard,
    patchImportedDashboard,
  }),
}))

vi.mock('./ImportDropzone', () => ({
  default: ({ onFile }: { onFile: (file: File) => void }) => (
    <button type="button" onClick={() => onFile(new File(['{}'], 'infra.json', { type: 'application/json' }))}>Choose fixture</button>
  ),
}))

import ImportedDashboardsPanel from './ImportedDashboardsPanel'

function review(): ImportedDashboardImportReview {
  return {
    import_session_id: 'session-1',
    expires_at: new Date(Date.now() + 60_000).toISOString(),
    dashboard: { source_title: 'Infra', panels: [{ panel_id: 1, title: 'CPU', kind: 'line', targets: [{ ref_id: 'A', expr: 'cpu{project_id="$project_id"}' }] }] },
    stats: { panels_seen: 1, panels_converted: 1, line_charts: 1, single_value_tiles: 0, targets: 1, rows_expanded: 0, variables_resolved: 0, variables_requiring_binding: 0 },
    required_bindings: [],
    has_blockers: false,
  }
}

describe('ImportedDashboardsPanel', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    listImportedDashboards.mockReset().mockResolvedValue({ dashboards: [], count: 0 })
    importGrafanaDashboard.mockReset()
    importGrafanaDashboardByUID.mockReset()
    commitGrafanaDashboardImport.mockReset()
    previewGrafanaDashboardImport.mockReset().mockResolvedValue({ enabled: true, configured: true, generated_at: new Date().toISOString(), dashboards: [] })
    resolveGrafanaImportLabelValues.mockReset().mockResolvedValue({ variable: 'env', options: [] })
    getImportedDashboardPanels.mockReset().mockResolvedValue({ enabled: true, configured: true, generated_at: new Date().toISOString(), dashboards: [] })
    deleteImportedDashboard.mockReset()
    patchImportedDashboard.mockReset()
		vi.stubGlobal('confirm', vi.fn(() => true))
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
		vi.unstubAllGlobals()
  })

  it('serializes upload and commit operations and locks project selection', async () => {
    let resolveImport!: (value: ImportedDashboardImportReview) => void
    importGrafanaDashboard.mockImplementation(() => new Promise(resolve => { resolveImport = resolve }))
    await act(async () => {
			root.render(<ImportedDashboardsPanel projects={[projectA]} prometheusConfigured />)
      await Promise.resolve()
    })

    const choose = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Choose fixture')!
    await act(async () => {
      choose.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      choose.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    expect(importGrafanaDashboard).toHaveBeenCalledTimes(1)
    expect((container.querySelector('[aria-label="Dashboard import project"]') as HTMLSelectElement).disabled).toBe(true)

    await act(async () => { resolveImport(review()); await Promise.resolve() })
    let resolveCommit!: (value: unknown) => void
    commitGrafanaDashboardImport.mockImplementation(() => new Promise(resolve => { resolveCommit = resolve }))
    const commit = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Import dashboard')!
    await act(async () => {
      commit.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      commit.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    expect(commitGrafanaDashboardImport).toHaveBeenCalledTimes(1)
    expect((container.querySelector('[aria-label="Dashboard import project"]') as HTMLSelectElement).disabled).toBe(true)
    await act(async () => { resolveCommit({}); await Promise.resolve() })
  })

	it('selects a project loaded after mount and ignores stale project-list responses', async () => {
		let resolveA!: (value: { dashboards: ImportedDashboard[]; count: number }) => void
		let resolveB!: (value: { dashboards: ImportedDashboard[]; count: number }) => void
		listImportedDashboards.mockImplementation(({ project_id }: { project_id: string }) => new Promise(resolve => {
			if (project_id === projectA.id) resolveA = resolve
			else resolveB = resolve
		}))

		await act(async () => {
			root.render(<ImportedDashboardsPanel projects={[]} prometheusConfigured />)
			await Promise.resolve()
			root.render(<ImportedDashboardsPanel projects={[projectA, projectB]} defaultProjectId={projectA.id} prometheusConfigured />)
			await Promise.resolve()
		})
		const select = container.querySelector('[aria-label="Dashboard import project"]') as HTMLSelectElement
		expect(select.value).toBe(projectA.id)

		await act(async () => {
			select.value = projectB.id
			select.dispatchEvent(new Event('change', { bubbles: true }))
			await Promise.resolve()
			resolveB({ dashboards: [dashboard('dash-b', projectB.id)], count: 1 })
			await Promise.resolve()
		})
		expect(container.textContent).toContain('dash-b')

		await act(async () => {
			resolveA({ dashboards: [dashboard('dash-a', projectA.id)], count: 1 })
			await Promise.resolve()
		})
		expect(container.textContent).toContain('dash-b')
		expect(container.textContent).not.toContain('dash-a')
	})

  it('requires confirmation before deleting an imported dashboard', async () => {
		listImportedDashboards.mockResolvedValue({ dashboards: [dashboard('dash-a', projectA.id)], count: 1 })
		vi.mocked(window.confirm).mockReturnValue(false)
		await act(async () => {
			root.render(<ImportedDashboardsPanel projects={[projectA]} prometheusConfigured />)
			await Promise.resolve()
		})
		const remove = container.querySelector('[aria-label="Delete dash-a"]') as HTMLButtonElement
		await act(async () => remove.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(deleteImportedDashboard).not.toHaveBeenCalled()
  })

	it('renders a blocked review defensively when a legacy API returns null panels', async () => {
		const blocked = review()
		blocked.has_blockers = true
		blocked.dashboard.panels = null as unknown as ImportedDashboardImportReview['dashboard']['panels']
		importGrafanaDashboard.mockResolvedValue(blocked)
		await act(async () => {
			root.render(<ImportedDashboardsPanel projects={[projectA]} prometheusConfigured />)
			await Promise.resolve()
		})
		const choose = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Choose fixture')!
		await act(async () => {
			choose.dispatchEvent(new MouseEvent('click', { bubbles: true }))
			await Promise.resolve()
		})
		expect(container.textContent).toContain('blocking issues')
	})

	it('requires and forwards a run ID for test-scoped live previews', async () => {
		const testScoped = review()
		testScoped.dashboard.panels[0].targets = [{ ref_id: 'A', expr: 'cpu{test_id="$test_id"}' }]
		importGrafanaDashboard.mockResolvedValue(testScoped)
		await act(async () => {
			root.render(<ImportedDashboardsPanel projects={[projectA]} prometheusConfigured />)
			await Promise.resolve()
		})
		const choose = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Choose fixture')!
		await act(async () => {
			choose.dispatchEvent(new MouseEvent('click', { bubbles: true }))
			await Promise.resolve()
		})
		expect(previewGrafanaDashboardImport).not.toHaveBeenCalled()
		const runID = container.querySelector('[aria-label="Preview run ID"]') as HTMLInputElement
		await act(async () => {
			Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set?.call(runID, 'run-123')
			runID.dispatchEvent(new Event('input', { bubbles: true }))
		})
		await act(async () => { await new Promise(resolve => setTimeout(resolve, 550)) })
		expect(previewGrafanaDashboardImport).toHaveBeenCalledWith(expect.objectContaining({ test_id: 'run-123' }))
	})

	it('prompts for and forwards a run ID when testing a committed test-scoped dashboard', async () => {
		const saved = dashboard('dash-test-scoped', projectA.id)
		saved.panels = [{ panel_id: 1, title: 'CPU', kind: 'line', targets: [{ expr: 'cpu{test_id="$test_id"}' }] }]
		listImportedDashboards.mockResolvedValue({ dashboards: [saved], count: 1 })
		vi.stubGlobal('prompt', vi.fn(() => 'run-456'))
		await act(async () => {
			root.render(<ImportedDashboardsPanel projects={[projectA]} prometheusConfigured />)
			await Promise.resolve()
		})
		const testNow = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Test now')!
		await act(async () => {
			testNow.dispatchEvent(new MouseEvent('click', { bubbles: true }))
			await Promise.resolve()
		})
		expect(getImportedDashboardPanels).toHaveBeenCalledWith('dash-test-scoped', expect.objectContaining({ test_id: 'run-456' }))
	})

	it('explains when runtime credentials make an import preview unavailable', async () => {
		importGrafanaDashboard.mockResolvedValue(review())
		previewGrafanaDashboardImport.mockResolvedValue({ enabled: true, configured: false, generated_at: new Date().toISOString(), dashboards: [] })
		await act(async () => {
			root.render(<ImportedDashboardsPanel projects={[projectA]} prometheusConfigured />)
			await Promise.resolve()
		})
		const choose = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Choose fixture')!
		await act(async () => {
			choose.dispatchEvent(new MouseEvent('click', { bubbles: true }))
			await Promise.resolve()
		})
		await act(async () => { await new Promise(resolve => setTimeout(resolve, 550)) })
		expect(container.textContent).toContain('credential environment variable is not set')
	})

	it('ignores an import review that resolves after the active project changes', async () => {
		let resolveImport!: (value: ImportedDashboardImportReview) => void
		importGrafanaDashboard.mockImplementation(() => new Promise(resolve => { resolveImport = resolve }))
		await act(async () => {
			root.render(<ImportedDashboardsPanel projects={[projectA, projectB]} defaultProjectId={projectA.id} prometheusConfigured />)
			await Promise.resolve()
		})
		await act(async () => {
			Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Choose fixture')?.click()
			await Promise.resolve()
		})

		await act(async () => {
			root.render(<ImportedDashboardsPanel projects={[projectA, projectB]} defaultProjectId={projectB.id} prometheusConfigured />)
			await Promise.resolve()
			await Promise.resolve()
		})
		await act(async () => {
			resolveImport(review())
			await Promise.resolve()
		})

		expect((container.querySelector('[aria-label="Dashboard import project"]') as HTMLSelectElement).value).toBe(projectB.id)
		expect(container.textContent).not.toContain('Review import')
		expect(commitGrafanaDashboardImport).not.toHaveBeenCalled()
	})

	it('guards a parsed import review before switching its project scope', async () => {
		importGrafanaDashboard.mockResolvedValue(review())
		await act(async () => {
			root.render(
				<NavigationGuardProvider>
					<ImportedDashboardsPanel projects={[projectA, projectB]} defaultProjectId={projectA.id} prometheusConfigured />
				</NavigationGuardProvider>,
			)
			await Promise.resolve()
		})
		await act(async () => {
			Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Choose fixture')?.click()
			await Promise.resolve()
		})
		expect(container.textContent).toContain('Review import: Infra')

		const select = container.querySelector('[aria-label="Dashboard import project"]') as HTMLSelectElement
		await act(async () => {
			Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')?.set?.call(select, projectB.id)
			select.dispatchEvent(new Event('change', { bubbles: true }))
		})
		expect(document.querySelector('.unsaved-changes-modal')).toBeTruthy()
		expect(select.value).toBe(projectA.id)

		const discard = Array.from(document.querySelectorAll<HTMLButtonElement>('.unsaved-changes-modal button'))
			.find(button => button.textContent === 'Discard changes')
		await act(async () => discard?.click())
		expect(document.querySelector('.unsaved-changes-modal')).toBeFalsy()
		expect(select.value).toBe(projectB.id)
		expect(container.textContent).not.toContain('Review import')
	})
})
