// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import DataRowGrid, { type DataGridRow } from './DataRowGrid'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

async function type(input: HTMLInputElement, value: string) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

async function key(input: HTMLInputElement, name: string) {
  await act(async () => { input.dispatchEvent(new KeyboardEvent('keydown', { key: name, bubbles: true })) })
}

describe('DataRowGrid', () => {
  let container: HTMLDivElement
  let root: Root
  const rows: DataGridRow[] = [
    { id: 'r1', values: { id: 'C-1', email: '' } },
    { id: 'r2', values: { id: 'C-2' }, status: 'consumed', details: 'version 2' },
  ]
  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })
  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
  })
  const cell = (label: string) => container.querySelector<HTMLInputElement>(`input[aria-label="${label}"]`)!

  it('shows absent and empty cells apart when read-only', async () => {
    await act(async () => root.render(<DataRowGrid label="Preview" columns={['id', 'email']} rows={rows} />))
    const table = container.querySelector('table[aria-label="Preview"]')!
    expect(Array.from(table.querySelectorAll('thead th')).map(th => th.textContent)).toEqual(['Row#', 'id', 'email'])
    const [first, second] = Array.from(table.querySelectorAll('tbody tr'))
    expect(first.querySelectorAll('td')[1].textContent).toBe('Empty')
    expect(second.querySelectorAll('td')[1].textContent).toBe('—Not set')
    expect(second.textContent).toContain('consumed')
    expect(container.querySelector('input')?.type).toBe('checkbox')
    expect(table.textContent).not.toContain('version 2')
    await act(async () => { container.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click() })
    expect(table.textContent).toContain('version 2')
  })

  it('saves a changed cell once, keeps a failed value, and reverts on Escape', async () => {
    const commit = vi.fn().mockResolvedValueOnce(false).mockResolvedValueOnce(true)
    await act(async () => root.render(<DataRowGrid label="Rows" columns={['id', 'email']} rows={rows} onCellCommit={commit} />))
    // Leaving an untouched or unchanged cell sends nothing.
    await act(async () => { cell('email, row 2').dispatchEvent(new FocusEvent('focusout', { bubbles: true })) })
    await type(cell('id, row 1'), 'C-1')
    await act(async () => { cell('id, row 1').dispatchEvent(new FocusEvent('focusout', { bubbles: true })) })
    expect(commit).not.toHaveBeenCalled()

    await type(cell('id, row 1'), 'C-9')
    await key(cell('id, row 1'), 'Enter')
    expect(commit).toHaveBeenCalledWith(rows[0], 'id', 'C-9')
    expect(cell('id, row 1').value).toBe('C-9')
    expect(cell('id, row 1').getAttribute('aria-invalid')).toBe('true')
    await key(cell('id, row 1'), 'Escape')
    expect(cell('id, row 1').value).toBe('C-1')
    expect(cell('id, row 1').getAttribute('aria-invalid')).toBeNull()

    await type(cell('email, row 2'), 'b@example.test')
    await act(async () => { cell('email, row 2').dispatchEvent(new FocusEvent('focusout', { bubbles: true })) })
    expect(commit).toHaveBeenLastCalledWith(rows[1], 'email', 'b@example.test')
    expect(cell('email, row 2').getAttribute('aria-invalid')).toBeNull()
  })

  it('adds a typed row on Enter and refuses a paste that would drop values', async () => {
    const add = vi.fn().mockResolvedValue(true)
    await act(async () => root.render(<DataRowGrid label="Rows" columns={['id', 'email']} rows={rows} onAddRows={add} />))
    await type(cell('email, new row'), 'n@example.test')
    await key(cell('email, new row'), 'Enter')
    expect(add).toHaveBeenCalledWith([{ email: 'n@example.test' }])
    expect(cell('email, new row').value).toBe('')

    const paste = (text: string) => {
      const event = new Event('paste', { bubbles: true, cancelable: true }) as Event & { clipboardData: { getData: () => string } }
      event.clipboardData = { getData: () => text }
      return event
    }
    await act(async () => { cell('email, new row').dispatchEvent(paste('a@x\tb@y\n')) })
    expect(add).toHaveBeenCalledTimes(1)
    expect(container.textContent).toContain('Nothing was pasted: 1 value(s) fall past the last column.')
    await act(async () => { cell('id, new row').dispatchEvent(paste('C-7\ta@x\nC-8\tb@y\n')) })
    expect(add).toHaveBeenLastCalledWith([{ id: 'C-7', email: 'a@x' }, { id: 'C-8', email: 'b@y' }])
  })

  it('preserves explicit empty values without filling untouched columns', async () => {
    const commit = vi.fn().mockResolvedValue(true)
    const add = vi.fn().mockResolvedValue(true)
    await act(async () => root.render(<DataRowGrid label="Rows" columns={['id', 'email', 'region']} rows={rows} onCellCommit={commit} onAddRows={add} markEmpty />))
    await type(cell('email, row 2'), 'temporary')
    await type(cell('email, row 2'), '')
    await key(cell('email, row 2'), 'Enter')
    expect(commit).toHaveBeenLastCalledWith(rows[1], 'email', '')

    await act(async () => { const empty = container.querySelector<HTMLButtonElement>('button[aria-label="Set region empty, row 1"]')!; empty.focus(); empty.click() })
    expect(document.activeElement).toBe(cell('region, row 1'))
    expect(commit).toHaveBeenLastCalledWith(rows[0], 'region', '')

    await type(cell('id, new row'), 'C-3')
    await type(cell('email, new row'), 'temporary')
    await type(cell('email, new row'), '')
    await key(cell('id, new row'), 'Enter')
    expect(add).toHaveBeenLastCalledWith([{ id: 'C-3', email: '' }])

    await act(async () => { const empty = container.querySelector<HTMLButtonElement>('button[aria-label="Set email empty, new row"]')!; empty.focus(); empty.click() })
    expect(document.activeElement).toBe(cell('email, new row'))
    await key(cell('email, new row'), 'Enter')
    expect(add).toHaveBeenLastCalledWith([{ email: '' }])
    expect(add).toHaveBeenCalledTimes(2)
  })

  it('checks a new column name before adding it', async () => {
    const addColumn = vi.fn().mockResolvedValue(true)
    await act(async () => root.render(<DataRowGrid label="Rows" columns={['id']} rows={[]} onAddColumn={addColumn} maxColumns={2} />))
    const open = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Add column')!
    await act(async () => open.click())
    const name = container.querySelector<HTMLInputElement>('.data-grid-add-column input')!
    await type(name, 'id')
    await act(async () => container.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
    expect(container.querySelector('[role="alert"]')?.textContent).toBe('There is already a column named "id".')
    expect(addColumn).not.toHaveBeenCalled()
    await type(name, ' region ')
    await act(async () => container.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
    expect(addColumn).toHaveBeenCalledWith('region')
    expect(container.querySelector('.data-grid-add-column')).toBeNull()
  })

  it('saves a cell edited again while its first save was still running', async () => {
    let finishFirst!: (saved: boolean) => void
    const commit = vi.fn().mockImplementationOnce(() => new Promise<boolean>(resolve => { finishFirst = resolve })).mockResolvedValue(true)
    const view = (current: DataGridRow[]) => <DataRowGrid label="Rows" columns={['id', 'email']} rows={current} onCellCommit={commit} />
    await act(async () => root.render(view(rows)))
    await type(cell('id, row 1'), 'C-8')
    await act(async () => { cell('id, row 1').dispatchEvent(new FocusEvent('focusout', { bubbles: true })) })
    await type(cell('id, row 1'), 'C-9')
    await act(async () => { cell('id, row 1').dispatchEvent(new FocusEvent('focusout', { bubbles: true })) })
    expect(commit).toHaveBeenCalledTimes(1)
    const saved = [{ ...rows[0], values: { ...rows[0].values, id: 'C-8' } }, rows[1]]
    await act(async () => { root.render(view(saved)); finishFirst(true) })
    expect(commit).toHaveBeenCalledTimes(2)
    expect(commit.mock.lastCall?.slice(1)).toEqual(['id', 'C-9'])
    expect(container.textContent).toContain('Saved id in row 1.')
  })

  it('describes a failed cell and refuses a block pasted into one cell', async () => {
    const commit = vi.fn().mockResolvedValue(false)
    await act(async () => root.render(<DataRowGrid label="Rows" columns={['id', 'email']} rows={rows} onCellCommit={commit} onAddRows={vi.fn()} />))
    await type(cell('id, row 1'), 'C-9')
    await act(async () => { cell('id, row 1').dispatchEvent(new FocusEvent('focusout', { bubbles: true })) })
    const description = document.getElementById(cell('id, row 1').getAttribute('aria-describedby') || '')
    expect(description?.textContent).toBe('id in row 1 was not saved. Press Enter in the cell to try again, or Escape to undo the change.')
    await key(cell('id, row 1'), 'Escape')
    expect(cell('id, row 1').getAttribute('aria-describedby')).toBeNull()
    const paste = new Event('paste', { bubbles: true, cancelable: true }) as Event & { clipboardData: { getData: () => string } }
    paste.clipboardData = { getData: () => 'C-1\ta@x' }
    await act(async () => { cell('email, row 2').dispatchEvent(paste) })
    expect(paste.defaultPrevented).toBe(true)
    expect(container.textContent).toContain('To add rows, paste them into the last line')
  })

  it('keeps cells usable while a row is being added and names live-table gaps', async () => {
    let finish!: (ok: boolean) => void
    const add = vi.fn(() => new Promise<boolean>(resolve => { finish = resolve }))
    await act(async () => root.render(<DataRowGrid label="Rows" columns={['id', 'email']} rows={rows} onAddRows={add} markEmpty />))
    await type(cell('id, new row'), 'C-3')
    await key(cell('id, new row'), 'Enter')
    expect(cell('id, new row').disabled).toBe(false)
    expect([...container.querySelectorAll('button')].find(button => button.textContent === 'Add row')?.disabled).toBe(true)
    await act(async () => finish(true))
    expect(cell('id, new row').value).toBe('')
    expect([...container.querySelectorAll('.data-grid-empty-value')].map(node => node.textContent)).toEqual(['empty', 'absent'])
  })

  it('saves once when Enter moves focus away from the edited cell', async () => {
    const commit = vi.fn().mockResolvedValue(true)
    await act(async () => root.render(<DataRowGrid label="Rows" columns={['id', 'email']} rows={rows} onCellCommit={commit} />))
    const edited = cell('id, row 1')
    await act(async () => edited.focus())
    await type(edited, 'C-9')
    await key(edited, 'Enter')
    await act(async () => { edited.dispatchEvent(new FocusEvent('focusout', { bubbles: true })) })
    await act(async () => Promise.resolve())
    expect(commit).toHaveBeenCalledTimes(1)
  })

  it('keeps the next row being typed and refuses a paste while a row is added', async () => {
    let finish!: (ok: boolean) => void
    const add = vi.fn(() => new Promise<boolean>(resolve => { finish = resolve }))
    await act(async () => root.render(<DataRowGrid label="Rows" columns={['id', 'email']} rows={rows} onAddRows={add} />))
    await type(cell('id, new row'), 'C-3')
    await type(cell('email, new row'), 'c@x')
    await key(cell('id, new row'), 'Enter')
    await type(cell('id, new row'), 'C-4')
    const paste = new Event('paste', { bubbles: true, cancelable: true }) as Event & { clipboardData: { getData: () => string } }
    paste.clipboardData = { getData: () => 'C-5\te@x\nC-6\tf@x' }
    await act(async () => { cell('email, new row').dispatchEvent(paste) })
    expect(container.textContent).toContain('Nothing was pasted: rows are still being added.')
    await act(async () => finish(true))
    expect(add).toHaveBeenCalledTimes(1)
    expect(cell('id, new row').value).toBe('C-4')
    expect(cell('email, new row').value).toBe('')
  })

  it('keeps a failed cell described by its failure and re-announces repeated saves', async () => {
    const commit = vi.fn().mockResolvedValueOnce(false).mockResolvedValue(true)
    await act(async () => root.render(<DataRowGrid label="Rows" columns={['id', 'email']} rows={rows} onCellCommit={commit} onAddRows={vi.fn()} />))
    await type(cell('id, row 1'), 'C-9')
    await act(async () => { cell('id, row 1').dispatchEvent(new FocusEvent('focusout', { bubbles: true })) })
    await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Add row')!.click())
    expect(container.textContent).toContain('Type at least one value')
    expect(document.getElementById(cell('id, row 1').getAttribute('aria-describedby') || '')?.textContent).toContain('id in row 1 was not saved')
    const live = container.querySelector('[aria-live="polite"]')!
    await type(cell('email, row 2'), 'x@y')
    await act(async () => { cell('email, row 2').dispatchEvent(new FocusEvent('focusout', { bubbles: true })) })
    const first = live.textContent
    await type(cell('email, row 2'), 'z@y')
    await act(async () => { cell('email, row 2').dispatchEvent(new FocusEvent('focusout', { bubbles: true })) })
    expect(first).toBe('Saved email in row 2.')
    expect(live.textContent).not.toBe(first)
    expect(live.textContent?.trim()).toBe('Saved email in row 2.')
  })

  it('shows the caller\'s reason for a failed save and offers removing only unused columns', async () => {
    const commit = vi.fn().mockResolvedValue('The row changed while you were editing.')
    const remove = vi.fn().mockResolvedValue(true)
    await act(async () => root.render(<DataRowGrid label="Rows" columns={['id', 'email', 'region']} rows={rows} onCellCommit={commit} onRemoveColumn={remove} />))
    await type(cell('id, row 1'), 'C-9')
    await act(async () => { cell('id, row 1').dispatchEvent(new FocusEvent('focusout', { bubbles: true })) })
    expect(document.getElementById(cell('id, row 1').getAttribute('aria-describedby') || '')?.textContent).toBe('id in row 1 was not saved. The row changed while you were editing. Press Enter in the cell to try again, or Escape to undo the change.')
    const emptyColumns = container.querySelector('.data-grid-empty-columns[role="group"]')!
    expect([...emptyColumns.querySelectorAll('button')].map(button => button.textContent)).toEqual(['Remove region'])
    expect(document.getElementById(emptyColumns.getAttribute('aria-labelledby') || '')?.textContent).toBe('Columns no row uses:')
    expect([...container.querySelectorAll('thead th')].map(th => th.textContent)).toEqual(['Row#', 'id', 'email', 'region'])
    await act(async () => emptyColumns.querySelector('button')!.click())
    expect(remove).toHaveBeenCalledWith('region')
    // A failure whose row is gone is no longer shown.
    await act(async () => root.render(<DataRowGrid label="Rows" columns={['id', 'email', 'region']} rows={[rows[1]]} onCellCommit={commit} onRemoveColumn={remove} />))
    expect(container.textContent).not.toContain('was not saved')
  })

  it('asks to press Enter again when a row is added while another is being saved', async () => {
    let finish!: (ok: boolean) => void
    const add = vi.fn(() => new Promise<boolean>(resolve => { finish = resolve }))
    await act(async () => root.render(<DataRowGrid label="Rows" columns={['id', 'email']} rows={rows} onAddRows={add} />))
    await type(cell('id, new row'), 'C-3')
    await key(cell('id, new row'), 'Enter')
    await type(cell('id, new row'), 'C-4')
    await key(cell('id, new row'), 'Enter')
    expect(add).toHaveBeenCalledTimes(1)
    expect(container.textContent).toContain('The previous row is still being added. Press Enter again once it is saved.')
    await act(async () => finish(true))
  })
})

