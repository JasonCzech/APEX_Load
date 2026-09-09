// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import TablePaginationManager from './TablePaginationManager'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function buildTable(rowCount: number, options: { paginationOff?: boolean } = {}) {
  const host = document.createElement('div')
  host.className = 'data-table'
  if (options.paginationOff) {
    host.dataset.tablePagination = 'off'
  }
  const table = document.createElement('table')
  const tbody = document.createElement('tbody')
  for (let index = 0; index < rowCount; index += 1) {
    const row = document.createElement('tr')
    const cell = document.createElement('td')
    cell.textContent = `Row ${index + 1}`
    row.appendChild(cell)
    tbody.appendChild(row)
  }
  table.appendChild(tbody)
  host.appendChild(table)
  document.body.appendChild(host)
  return { host, table }
}

function visibleRowTexts(table: HTMLTableElement) {
  return Array.from(table.tBodies[0].rows)
    .filter(row => !row.hidden)
    .map(row => row.textContent)
}

async function flushTableScan() {
  await act(async () => {
    await new Promise(resolve => window.setTimeout(resolve, 5))
  })
}

// The MutationObserver path defers to requestAnimationFrame, so DOM changes made
// after mount need a longer wait than the initial synchronous scan.
async function flushObservedTableScan() {
  await act(async () => {
    await new Promise(resolve => window.setTimeout(resolve, 60))
  })
}

describe('TablePaginationManager', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    localStorage.clear()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    document.body.innerHTML = ''
    localStorage.clear()
  })

  it('uses the saved personal default while allowing a table override', async () => {
    localStorage.setItem('apexload.personal-preferences', JSON.stringify({ tableRows: 25 }))
    const { table } = buildTable(30)
    act(() => root.render(<TablePaginationManager />))
    await flushTableScan()
    expect(visibleRowTexts(table)).toHaveLength(25)
    const select = document.querySelector('select')!
    act(() => { select.value = '10'; select.dispatchEvent(new Event('change', { bubbles: true })) })
    expect(visibleRowTexts(table)).toHaveLength(10)
    expect(JSON.parse(localStorage.getItem('apexload.personal-preferences')!).tableRows).toBe(25)
  })

  it('shows 10 rows by default, pages forward and backward, and supports 25 and all', async () => {
    const { table } = buildTable(30)

    act(() => {
      root.render(<TablePaginationManager />)
    })
    await flushTableScan()

    expect(visibleRowTexts(table)).toHaveLength(10)
    expect(visibleRowTexts(table)[0]).toBe('Row 1')
    expect(document.querySelector('.table-pagination-summary')?.textContent).toBe('Showing 1-10 of 30')

    const nextButton = Array.from(document.querySelectorAll('button')).find(button => button.textContent === 'Next') as HTMLButtonElement
    const previousButton = Array.from(document.querySelectorAll('button')).find(button => button.textContent === 'Previous') as HTMLButtonElement
    expect(previousButton.disabled).toBe(true)
    expect(nextButton.disabled).toBe(false)

    act(() => {
      nextButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(visibleRowTexts(table)).toEqual(Array.from({ length: 10 }, (_, index) => `Row ${index + 11}`))
    expect(document.querySelector('.table-pagination-summary')?.textContent).toBe('Showing 11-20 of 30')
    expect(previousButton.disabled).toBe(false)

    const select = document.querySelector('.table-pagination-select') as HTMLSelectElement
    act(() => {
      select.value = '25'
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(visibleRowTexts(table)).toHaveLength(25)
    expect(visibleRowTexts(table)[0]).toBe('Row 1')
    expect(document.querySelector('.table-pagination-summary')?.textContent).toBe('Showing 1-25 of 30')

    act(() => {
      nextButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(visibleRowTexts(table)).toEqual(['Row 26', 'Row 27', 'Row 28', 'Row 29', 'Row 30'])
    expect(document.querySelector('.table-pagination-summary')?.textContent).toBe('Showing 26-30 of 30')
    expect(nextButton.disabled).toBe(true)

    act(() => {
      previousButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(visibleRowTexts(table)[0]).toBe('Row 1')
    expect(document.querySelector('.table-pagination-summary')?.textContent).toBe('Showing 1-25 of 30')

    act(() => {
      select.value = 'All'
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(visibleRowTexts(table)).toHaveLength(30)
    expect(document.querySelector('.table-pagination-summary')?.textContent).toBe('Showing 1-30 of 30')
    expect(previousButton.disabled).toBe(true)
    expect(nextButton.disabled).toBe(true)
  })

  it('forgets the page of a table that leaves the DOM instead of resurrecting it on re-entry', async () => {
    const { host, table } = buildTable(30)

    act(() => {
      root.render(<TablePaginationManager />)
    })
    await flushTableScan()

    const nextButton = Array.from(document.querySelectorAll('button')).find(button => button.textContent === 'Next') as HTMLButtonElement
    act(() => {
      nextButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(document.querySelector('.table-pagination-summary')?.textContent).toBe('Showing 11-20 of 30')

    // The table unmounts (e.g. the operator navigates away from the section)…
    act(() => {
      host.remove()
    })
    await flushObservedTableScan()
    expect(document.querySelector('.table-pagination-toolbar')).toBeNull()

    // …and the same element comes back. Its page bookkeeping was discarded with
    // it, so the toolbar and the rows must agree on page 1.
    act(() => {
      document.body.appendChild(host)
    })
    await flushObservedTableScan()

    expect(document.querySelector('.table-pagination-summary')?.textContent).toBe('Showing 1-10 of 30')
    expect(visibleRowTexts(table)).toEqual(Array.from({ length: 10 }, (_, index) => `Row ${index + 1}`))
  })

  it('does not add controls for short tables', async () => {
    const { table } = buildTable(10)

    act(() => {
      root.render(<TablePaginationManager />)
    })
    await flushTableScan()

    expect(visibleRowTexts(table)).toHaveLength(10)
    expect(document.querySelector('.table-pagination-toolbar')).toBeNull()
  })

  it('respects tables that opt out of global pagination', async () => {
    const { table } = buildTable(12, { paginationOff: true })

    act(() => {
      root.render(<TablePaginationManager />)
    })
    await flushTableScan()

    expect(visibleRowTexts(table)).toHaveLength(12)
    expect(document.querySelector('.table-pagination-toolbar')).toBeNull()
  })
})
