// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import DashboardWorkspace, {
  type DashboardWorkspaceCard,
  type DashboardWorkspaceProfile,
} from './DashboardWorkspace'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const STORAGE_KEY = 'test.workspaceProfiles.v1'
const ACTIVE_STORAGE_KEY = 'test.activeWorkspaceProfile.v1'

const cards: DashboardWorkspaceCard[] = [
  { id: 'alpha', title: 'Alpha Card', render: () => <p>Alpha body</p> },
  { id: 'beta', title: 'Beta Card', render: () => <p>Beta body</p> },
  { id: 'gamma', title: 'Gamma Card', render: () => <p>Gamma body</p> },
]

const defaultProfile: DashboardWorkspaceProfile = {
  id: 'default',
  name: 'Default Workspace',
  activeTabId: 'overview',
  tabs: [
    { id: 'overview', label: 'Overview', cardIds: ['alpha'] },
    { id: 'details', label: 'Details', cardIds: ['beta'] },
  ],
  unassignedCardIds: ['gamma'],
}

describe('DashboardWorkspace', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    window.localStorage.clear()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    window.localStorage.clear()
  })

  it('renders the default profile tabs and selected tab card', async () => {
    await renderWorkspace()

    expect(tabLabels()).toEqual(['Overview', 'Details'])
    expect(selectedTabLabel()).toBe('Overview')
    expect(container.textContent).toContain('Alpha body')
    expect(container.textContent).not.toContain('Beta body')

    await click(tabButton('Details'))

    expect(selectedTabLabel()).toBe('Details')
    expect(container.textContent).toContain('Beta body')
    expect(container.textContent).not.toContain('Alpha body')
  })

  it('returns removed tab cards to the available pool and adds them to the active tab', async () => {
    await renderWorkspace()

    await contextMenu(tabButton('Details'))
    await click(menuItem('Delete Tab'))

    expect(tabLabels()).toEqual(['Overview'])
    expect(addCardSelect().textContent).toContain('Beta Card')

    await changeSelect(addCardSelect(), 'beta')
    await click(buttonNamed('Add Card'))

    expect(container.textContent).toContain('Alpha body')
    expect(container.textContent).toContain('Beta body')
    expect(addCardSelect().textContent).not.toContain('Beta Card')
  })

  it('supports named profile creation, duplication, deletion, and reset', async () => {
    await renderWorkspace()

    await click(buttonNamed('New Profile'))
    expect(profileSelect().textContent).toContain('Layout 2')

    await changeInput(profileNameInput(), 'HTTP Debug')
    expect(profileSelect().textContent).toContain('HTTP Debug')

    await click(removeCardButton('Alpha Card'))
    expect(container.textContent).not.toContain('Alpha body')

    await click(buttonNamed('Reset Layout'))
    expect(container.textContent).toContain('Alpha body')

    await click(buttonNamed('Duplicate'))
    expect(profileSelect().textContent).toContain('HTTP Debug Copy')

    await click(buttonNamed('Delete Profile'))
    expect(profileSelect().textContent).not.toContain('HTTP Debug Copy')

    const stored = JSON.parse(window.localStorage.getItem(STORAGE_KEY) || '[]') as DashboardWorkspaceProfile[]
    expect(stored.some(profile => profile.name === 'HTTP Debug')).toBe(true)
  })

  it('reorders tabs with keyboard controls', async () => {
    await renderWorkspace()

    expect(container.querySelector('button[aria-label^="Reorder tab"]')).toBeNull()
    expect(container.querySelector('button[aria-label^="Remove tab"]')).toBeNull()

    await keyDown(tabButton('Details'), 'ArrowLeft')

    expect(tabLabels()).toEqual(['Details', 'Overview'])
  })

  it('moves cards across tabs with drag and drop', async () => {
    await renderWorkspace()

    const dataTransfer = createDragDataTransfer()
    const alphaCard = container.querySelector('[data-card-id="alpha"]') as HTMLElement

    await act(async () => {
      dispatchDragEvent(alphaCard, 'dragstart', dataTransfer)
      dispatchDragEvent(tabButton('Details'), 'drop', dataTransfer)
      dispatchDragEvent(alphaCard, 'dragend', dataTransfer)
    })

    expect(container.textContent).not.toContain('Alpha body')

    await click(tabButton('Details'))

    expect(container.textContent).toContain('Alpha body')
    expect(container.textContent).toContain('Beta body')
  })

  it('adds newly shipped default tabs to saved profiles', async () => {
    const nextCards: DashboardWorkspaceCard[] = [
      ...cards,
      { id: 'delta', title: 'Delta Card', render: () => <p>Delta body</p> },
    ]
    const nextDefaultProfile: DashboardWorkspaceProfile = {
      ...defaultProfile,
      tabs: [
        ...defaultProfile.tabs,
        { id: 'insights', label: 'Insights', cardIds: ['delta'] },
      ],
    }
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify([{
      ...defaultProfile,
      unassignedCardIds: ['gamma', 'delta'],
    }]))

    await renderWorkspace({
      cards: nextCards,
      defaultProfile: nextDefaultProfile,
    })

    expect(tabLabels()).toEqual(['Overview', 'Details', 'Insights'])

    await click(tabButton('Insights'))

    expect(container.textContent).toContain('Delta body')
    expect(addCardSelect().textContent).not.toContain('Delta Card')
  })

  it('auto-applies the highest-priority matching profile from context tokens', async () => {
    const matchingProfile: DashboardWorkspaceProfile = {
      id: 'browser-layout',
      name: 'Browser Layout',
      activeTabId: 'details',
      matchEnabled: true,
      matchText: 'checkout browser',
      priority: 10,
      tabs: [
        { id: 'overview', label: 'Overview', cardIds: ['alpha'] },
        { id: 'details', label: 'Details', cardIds: ['beta', 'gamma'] },
      ],
      unassignedCardIds: [],
    }
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify([defaultProfile, matchingProfile]))
    window.localStorage.setItem(ACTIVE_STORAGE_KEY, JSON.stringify({ autoApply: true, profileId: 'default' }))

    await renderWorkspace({ context: { tokens: ['storefront', 'checkout', 'browser'] } })

    expect(selectedTabLabel()).toBe('Details')
    expect(container.textContent).toContain('Beta body')
    expect(container.textContent).toContain('Gamma body')

    await changeSelect(profileSelect(), 'default')

    expect(selectedTabLabel()).toBe('Overview')
    const activeState = JSON.parse(window.localStorage.getItem(ACTIVE_STORAGE_KEY) || '{}') as { autoApply?: boolean }
    expect(activeState.autoApply).toBe(false)
  })

  async function renderWorkspace(options: Partial<Parameters<typeof DashboardWorkspace>[0]> = {}) {
    await act(async () => {
      root.render(
        <DashboardWorkspace
          ariaLabel="Test workspace"
          storageKey={STORAGE_KEY}
          activeStorageKey={ACTIVE_STORAGE_KEY}
          defaultProfile={defaultProfile}
          cards={cards}
          {...options}
        />,
      )
    })
  }

  function tabLabels() {
    return Array.from(container.querySelectorAll('.dashboard-workspace-tab > span'))
      .map(node => node.textContent || '')
  }

  function selectedTabLabel() {
    const tab = container.querySelector('.dashboard-workspace-tab[aria-selected="true"] > span')
    return tab?.textContent || ''
  }

  function buttonNamed(text: string) {
    const button = Array.from(container.querySelectorAll('button')).find(button => button.textContent === text)
    expect(button).toBeTruthy()
    return button as HTMLButtonElement
  }

  function tabButton(label: string) {
    const button = Array.from(container.querySelectorAll('[role="tab"]')).find(button => button.textContent?.includes(label))
    expect(button).toBeTruthy()
    return button as HTMLButtonElement
  }

  function removeCardButton(title: string) {
    const button = container.querySelector(`button[aria-label="Remove card ${title}"]`)
    expect(button).toBeTruthy()
    return button as HTMLButtonElement
  }

  function menuItem(label: string) {
    const button = Array.from(container.querySelectorAll('[role="menuitem"]')).find(button => button.textContent === label)
    expect(button).toBeTruthy()
    return button as HTMLButtonElement
  }

  function profileSelect() {
    const select = container.querySelector('select[aria-label="Test workspace layout profile"]')
    expect(select).toBeTruthy()
    return select as HTMLSelectElement
  }

  function profileNameInput() {
    const input = container.querySelector('input[aria-label="Test workspace profile name"]')
    expect(input).toBeTruthy()
    return input as HTMLInputElement
  }

  function addCardSelect() {
    const select = container.querySelector('select[aria-label^="Add card to"]')
    expect(select).toBeTruthy()
    return select as HTMLSelectElement
  }

  async function click(element: HTMLElement) {
    await act(async () => {
      element.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }))
    })
  }

  async function keyDown(element: HTMLElement, key: string) {
    await act(async () => {
      element.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true }))
    })
  }

  async function contextMenu(element: HTMLElement) {
    await act(async () => {
      element.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 100, clientY: 80 }))
    })
  }

  async function changeInput(input: HTMLInputElement, value: string) {
    await act(async () => {
      const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      valueSetter?.call(input, value)
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
  }

  async function changeSelect(select: HTMLSelectElement, value: string) {
    await act(async () => {
      select.value = value
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })
  }

  function createDragDataTransfer() {
    const data: Record<string, string> = {}
    return {
      effectAllowed: 'move',
      dropEffect: 'move',
      setData: (type: string, value: string) => { data[type] = value },
      getData: (type: string) => data[type] || '',
    } as unknown as DataTransfer
  }

  function dispatchDragEvent(element: HTMLElement, type: string, dataTransfer: DataTransfer) {
    const event = new Event(type, { bubbles: true, cancelable: true }) as DragEvent
    Object.defineProperty(event, 'dataTransfer', { value: dataTransfer })
    element.dispatchEvent(event)
  }
})
