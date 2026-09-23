// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import { ImportConversionDetails, ImportEvidenceFacts } from './ImportConversionDetails'

it('keeps original counts and specialist evidence readable without implying readiness', () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  try {
    act(() => root.render(<ImportConversionDetails facts={[
      { label: 'Actions', value: 12000 },
      { label: 'Extractors', value: 0 },
      { label: 'Protocol', value: 'java_vuser', detail: 'External runner required' },
    ]}><details><summary>Original converter report</summary><p>Review payload handling.</p></details></ImportConversionDetails>))
    expect(container.querySelector('details')?.open).toBe(false)
    expect(container.querySelector('summary')?.textContent).toBe('Conversion reference')
    expect(container.textContent).toContain('not replay readiness')
    const rows = [...container.querySelectorAll('dl > div')]
    expect(rows.map(row => [row.querySelector('dt')?.textContent, row.querySelector('dd')?.textContent])).toEqual([
      ['Actions', '12000'], ['Extractors', '0'], ['Protocol', 'java_vuserExternal runner required'],
    ])
    expect(container.querySelector('dd small')?.textContent).toBe('External runner required')
    expect(container.textContent).toContain('Review payload handling.')
  } finally {
    act(() => root.unmount())
    container.remove()
  }
})

it('omits empty inventories but retains reports when no counts were supplied', () => {
  const container = document.createElement('div')
  const root = createRoot(container)
  try {
    act(() => root.render(<><ImportConversionDetails facts={[]} /><ImportEvidenceFacts facts={[]} /></>))
    expect(container.innerHTML).toBe('')
    act(() => root.render(<ImportConversionDetails title="Source details" facts={[]}><p>Source classified for review only.</p></ImportConversionDetails>))
    expect(container.querySelector('summary')?.textContent).toBe('Source details')
    expect(container.querySelector('dl')).toBeNull()
    expect(container.textContent).toContain('Source classified for review only.')
  } finally {
    act(() => root.unmount())
  }
})
