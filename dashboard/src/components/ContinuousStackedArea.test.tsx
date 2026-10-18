// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { Area, ComposedChart, XAxis, YAxis } from 'recharts'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import ContinuousStackedArea from './ContinuousStackedArea'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

interface PhasePoint {
  time: number
  dns?: number | null
  connect?: number | null
}

describe('ContinuousStackedArea', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
  })

  async function renderPhases(data: PhasePoint[]) {
    await act(async () => {
      root.render(
        <ComposedChart width={600} height={300} data={data}>
          <XAxis dataKey="time" type="number" domain={[0, 2]} />
          <YAxis type="number" domain={[0, 40]} allowDataOverflow />
          <Area dataKey="dns" stackId="phases" type="linear" stroke="blue" fill="blue" connectNulls={false} shape={<ContinuousStackedArea stackKeys={['dns']} />} isAnimationActive={false} />
          <Area dataKey="connect" stackId="phases" type="linear" stroke="green" fill="green" connectNulls={false} shape={<ContinuousStackedArea stackKeys={['dns', 'connect']} />} isAnimationActive={false} />
        </ComposedChart>,
      )
    })
    return {
      strokes: Array.from(container.querySelectorAll('.recharts-area-curve'), path => path.getAttribute('d')),
      fills: Array.from(container.querySelectorAll('.recharts-area-area'), path => path.getAttribute('d')),
    }
  }

  it.each([
    { label: 'an omitted phase', middle: { time: 1, connect: 10 } },
    { label: 'a null phase', middle: { time: 1, dns: null, connect: 10 } },
    { label: 'a NaN phase', middle: { time: 1, dns: Number.NaN, connect: 10 } },
    { label: 'an infinite phase', middle: { time: 1, dns: Number.POSITIVE_INFINITY, connect: 10 } },
    { label: 'an entirely absent stack', middle: { time: 1 } },
  ])('bridges $label without adding a zero-valued point', async ({ middle }) => {
    const first = { time: 0, dns: 20, connect: 10 }
    const last = { time: 2, dns: 20, connect: 10 }
    const endpoints = await renderPhases([first, last])
    const data: PhasePoint[] = [first, middle, last]
    const originalData = structuredClone(data)
    const withGap = await renderPhases(data)

    expect(withGap.strokes).toHaveLength(2)
    expect(withGap.fills).toHaveLength(2)
    expect(withGap).toEqual(endpoints)
    expect(withGap.strokes[0]?.match(/M/g)).toHaveLength(1)
    expect(data).toEqual(originalData)
  })

  it('keeps a measured zero in the continuous curve', async () => {
    const first = { time: 0, dns: 20, connect: 10 }
    const last = { time: 2, dns: 20, connect: 10 }
    const endpoints = await renderPhases([first, last])
    const withZero = await renderPhases([first, { time: 1, dns: 0, connect: 10 }, last])

    expect(withZero.strokes[0]).not.toBe(endpoints.strokes[0])
    expect(withZero.fills[0]).not.toBe(endpoints.fills[0])
    expect(withZero.strokes[0]?.match(/M/g)).toHaveLength(1)
    expect(withZero.strokes[0]?.match(/L/g)).toHaveLength(2)
  })

  it('keeps observed lower samples when only an upper phase is missing', async () => {
    const first = { time: 0, dns: 20, connect: 10 }
    const last = { time: 2, dns: 20, connect: 10 }
    const endpoints = await renderPhases([first, last])
    const data = [first, { time: 1, dns: 5 }, last]
    const originalData = structuredClone(data)
    const withUpperGap = await renderPhases(data)

    expect(withUpperGap.strokes[0]).not.toBe(endpoints.strokes[0])
    expect(withUpperGap.fills[0]).not.toBe(endpoints.fills[0])
    expect(withUpperGap.strokes[0]?.match(/L/g)).toHaveLength(2)
    expect(withUpperGap.strokes[1]).toBe(endpoints.strokes[1])
    expect(withUpperGap.fills[1]).toBe(endpoints.fills[1])
    expect(data).toEqual(originalData)
  })
})
