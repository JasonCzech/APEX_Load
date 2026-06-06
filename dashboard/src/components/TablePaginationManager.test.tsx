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

describe('TablePaginationManager', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    document.body.innerHTML = ''
  })

  it('shows 10 rows by default and supports 25 and all', async () => {
    const { table } = buildTable(30)

    act(() => {
      root.render(<TablePaginationManager />)
    })
    await flushTableScan()

    expect(visibleRowTexts(table)).toHaveLength(10)
    expect(document.querySelector('.table-pagination-summary')?.textContent).toBe('Showing 10 of 30')

    const select = document.querySelector('.table-pagination-select') as HTMLSelectElement
    act(() => {
      select.value = '25'
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(visibleRowTexts(table)).toHaveLength(25)
    expect(document.querySelector('.table-pagination-summary')?.textContent).toBe('Showing 25 of 30')

    act(() => {
      select.value = 'All'
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(visibleRowTexts(table)).toHaveLength(30)
    expect(document.querySelector('.table-pagination-summary')?.textContent).toBe('Showing 30 of 30')
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
