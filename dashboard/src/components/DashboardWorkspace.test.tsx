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
const CARD_LIBRARY_STORAGE_KEY = 'test.cardLibraryCollapsed.v1'

const cards: DashboardWorkspaceCard[] = [
  { id: 'alpha', title: 'Alpha Card', category: 'Core', description: 'Alpha description', render: () => <p>Alpha body</p> },
  { id: 'beta', title: 'Beta Card', category: 'Core', defaultWidth: 'full', render: () => <table><tbody><tr><td>Beta body</td></tr></tbody></table> },
  { id: 'gamma', title: 'Gamma Card', category: 'Extra', defaultWidth: 'natural', render: () => <p>Gamma body</p> },
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
    expect(container.textContent).toContain('Card Library')
    expect(container.textContent).toContain('Core')
    expect(container.textContent).toContain('Extra')
    expect(libraryItem('alpha').textContent).toContain('Alpha description')
    expect(cardElement('alpha').dataset.cardWidth).toBe('full')

    await click(tabButton('Details'))

    expect(selectedTabLabel()).toBe('Details')
    expect(container.textContent).toContain('Beta body')
    expect(container.textContent).not.toContain('Alpha body')
  })

  it('collapses and expands card library categories from the header', async () => {
    await renderWorkspace()

    const coreHeader = libraryGroupHeader('Core')
    const coreList = libraryGroupList(coreHeader)
    expect(coreHeader.getAttribute('aria-expanded')).toBe('true')
    expect(coreList.hidden).toBe(false)
    expect(container.querySelector('[data-library-card-id="alpha"]')).toBeTruthy()
    expect(container.querySelector('[data-library-card-id="beta"]')).toBeTruthy()
    expect(container.querySelector('[data-library-card-id="gamma"]')).toBeTruthy()

    await click(coreHeader)

    expect(coreHeader.getAttribute('aria-expanded')).toBe('false')
    expect(coreList.hidden).toBe(true)
    expect(container.querySelector('[data-library-card-id="gamma"]')).toBeTruthy()

    await click(coreHeader)

    expect(coreHeader.getAttribute('aria-expanded')).toBe('true')
    expect(coreList.hidden).toBe(false)
    expect(container.querySelector('[data-library-card-id="alpha"]')).toBeTruthy()
    expect(container.querySelector('[data-library-card-id="beta"]')).toBeTruthy()
  })

  it('keeps all cards in the sidebar and copies cards to the active tab', async () => {
    await renderWorkspace()

    await contextMenu(tabButton('Details'))
    await click(menuItem('Delete Tab'))

    expect(tabLabels()).toEqual(['Overview'])
    expect(libraryItem('beta').textContent).toContain('Beta Card')

    await click(libraryAddButton('beta'))

    expect(container.textContent).toContain('Alpha body')
    expect(container.textContent).toContain('Beta body')
    expect(libraryItem('beta').textContent).toContain('Added')
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

  it('navigates tabs with arrows and reorders tabs with shift arrows', async () => {
    await renderWorkspace()

    expect(container.querySelector('button[aria-label^="Reorder tab"]')).toBeNull()
    expect(container.querySelector('button[aria-label^="Remove tab"]')).toBeNull()

    await click(tabButton('Details'))
    await keyDown(tabButton('Details'), 'ArrowLeft')

    expect(tabLabels()).toEqual(['Overview', 'Details'])
    expect(selectedTabLabel()).toBe('Overview')

    await keyDown(tabButton('Overview'), 'ArrowRight', { shiftKey: true })

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

  it('copies sidebar cards by drag and prevents duplicate cards on one tab', async () => {
    await renderWorkspace()

    await click(tabButton('Details'))
    await click(libraryAddButton('alpha'))
    await click(libraryAddButton('alpha'))

    expect(cardCount('alpha')).toBe(1)

    const stored = readStoredDefaultProfile()
    expect(stored.tabs.find(tab => tab.id === 'overview')?.cardIds).toContain('alpha')
    expect(stored.tabs.find(tab => tab.id === 'details')?.cardIds.filter(id => id === 'alpha')).toHaveLength(1)

    const dataTransfer = createDragDataTransfer()
    const gammaLibraryItem = libraryItem('gamma')
    const panel = container.querySelector('.dashboard-workspace-panel') as HTMLElement

    await act(async () => {
      dispatchDragEvent(gammaLibraryItem, 'dragstart', dataTransfer)
      dispatchDragEvent(panel, 'drop', dataTransfer)
      dispatchDragEvent(gammaLibraryItem, 'dragend', dataTransfer)
    })

    expect(container.textContent).toContain('Gamma body')
    expect(cardCount('gamma')).toBe(1)
  })

  it('renders full-width defaults, natural-width defaults, and lets users resize them', async () => {
    await renderWorkspace()

    await click(libraryAddButton('gamma'))
    expect(cardElement('gamma').dataset.cardWidth).toBe('natural')

    await click(tabButton('Details'))

    expect(cardElement('beta').dataset.cardWidth).toBe('full')

    await click(buttonNamed('Full'))

    expect(cardElement('beta').dataset.cardWidth).toBe('half')

    await click(buttonNamed('Half'))

    expect(cardElement('beta').dataset.cardWidth).toBe('natural')

    const stored = readStoredDefaultProfile()
    expect(stored.tabs.find(tab => tab.id === 'details')?.cardWidths?.beta).toBe('natural')
  })

  it('persists card library collapse state', async () => {
    await renderWorkspace()

    await click(buttonNamed('Collapse'))

    expect(buttonNamed('Cards')).toBeTruthy()
    expect(JSON.parse(window.localStorage.getItem(CARD_LIBRARY_STORAGE_KEY) || 'false')).toBe(true)

    await act(async () => {
      root.unmount()
    })
    root = createRoot(container)
    await renderWorkspace()

    expect(buttonNamed('Cards')).toBeTruthy()

    await click(buttonNamed('Cards'))

    expect(container.textContent).toContain('Card Library')
    expect(JSON.parse(window.localStorage.getItem(CARD_LIBRARY_STORAGE_KEY) || 'true')).toBe(false)
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
    expect(libraryItem('delta').textContent).toContain('Delta Card')
  })

  it('applies one-time default card insertions without re-adding removed cards', async () => {
    const markerKey = 'test.defaultCardInsertion.v1'
    const nextCards: DashboardWorkspaceCard[] = [
      ...cards,
      { id: 'delta', title: 'Delta Card', category: 'Core', defaultWidth: 'full', render: () => <p>Delta body</p> },
    ]
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify([defaultProfile]))

    await renderWorkspace({
      cards: nextCards,
      defaultCardInsertions: [{
        key: markerKey,
        profileId: 'default',
        tabId: 'overview',
        cardId: 'delta',
        afterCardId: 'alpha',
      }],
    })

    expect(container.textContent).toContain('Delta body')
    let stored = readStoredDefaultProfile()
    expect(stored.tabs.find(tab => tab.id === 'overview')?.cardIds).toEqual(['alpha', 'delta'])
    expect(window.localStorage.getItem(markerKey)).toBe('true')

    await click(removeCardButton('Delta Card'))
    expect(container.textContent).not.toContain('Delta body')

    await act(async () => {
      root.unmount()
    })
    root = createRoot(container)
    await renderWorkspace({
      cards: nextCards,
      defaultCardInsertions: [{
        key: markerKey,
        profileId: 'default',
        tabId: 'overview',
        cardId: 'delta',
        afterCardId: 'alpha',
      }],
    })

    stored = readStoredDefaultProfile()
    expect(stored.tabs.find(tab => tab.id === 'overview')?.cardIds).toEqual(['alpha'])
    expect(container.textContent).not.toContain('Delta body')
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
          cardLibraryStorageKey={CARD_LIBRARY_STORAGE_KEY}
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

  function libraryItem(cardId: string) {
    const item = container.querySelector(`[data-library-card-id="${cardId}"]`)
    expect(item).toBeTruthy()
    return item as HTMLElement
  }

  function libraryGroupHeader(category: string) {
    const button = Array.from(container.querySelectorAll('.dashboard-workspace-library-group-header'))
      .find(button => button.textContent?.includes(category))
    expect(button).toBeTruthy()
    return button as HTMLButtonElement
  }

  function libraryGroupList(header: HTMLButtonElement) {
    const listId = header.getAttribute('aria-controls')
    expect(listId).toBeTruthy()
    const list = listId ? document.getElementById(listId) : null
    expect(list).toBeTruthy()
    return list as HTMLDivElement
  }

  function libraryAddButton(cardId: string) {
    const button = libraryItem(cardId).querySelector('button')
    expect(button).toBeTruthy()
    return button as HTMLButtonElement
  }

  function cardCount(cardId: string) {
    return container.querySelectorAll(`[data-card-id="${cardId}"]`).length
  }

  function cardElement(cardId: string) {
    const element = container.querySelector(`[data-card-id="${cardId}"]`)
    expect(element).toBeTruthy()
    return element as HTMLElement
  }

  function readStoredDefaultProfile() {
    const stored = JSON.parse(window.localStorage.getItem(STORAGE_KEY) || '[]') as DashboardWorkspaceProfile[]
    const profile = stored.find(profile => profile.id === 'default')
    expect(profile).toBeTruthy()
    return profile as DashboardWorkspaceProfile
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

  async function click(element: HTMLElement) {
    await act(async () => {
      element.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }))
    })
  }

  async function keyDown(element: HTMLElement, key: string, options: KeyboardEventInit = {}) {
    await act(async () => {
      element.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...options }))
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
