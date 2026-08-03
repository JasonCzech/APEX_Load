// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { LoadImpactBucket, LoadImpactMetric, LoadImpactModel } from './loadImpactModel'
import LoadImpact3DHeatmap from './LoadImpact3DHeatmap'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const threeMockState = vi.hoisted(() => ({
  rendererInstances: [] as Array<{
    dispose: ReturnType<typeof vi.fn>
    domElement: HTMLCanvasElement
    render: ReturnType<typeof vi.fn>
    setPixelRatio: ReturnType<typeof vi.fn>
    setSize: ReturnType<typeof vi.fn>
  }>,
}))

vi.mock('three', () => {
  const vector = () => ({ set: vi.fn(), x: 0, y: 0, z: 0 })

  class WebGLRenderer {
    dispose = vi.fn()
    domElement = document.createElement('canvas')
    render = vi.fn()
    setPixelRatio = vi.fn()
    setSize = vi.fn()

    constructor() {
      threeMockState.rendererInstances.push(this)
    }
  }

  class Scene {
    add = vi.fn()
    remove = vi.fn()
  }

  class PerspectiveCamera {
    aspect: number
    lookAt = vi.fn()
    position = vector()
    updateProjectionMatrix = vi.fn()

    constructor(_fov: number, aspect: number) {
      this.aspect = aspect
    }
  }

  class DirectionalLight {
    position = vector()
  }

  class GridHelper {
    geometry = { dispose: vi.fn() }
    material = { dispose: vi.fn() }
    position = { y: 0 }
  }

  class Color {
    constructor(public value: string) {}
    lerp = vi.fn(() => this)
  }

  class CanvasTexture {
    dispose = vi.fn()
    magFilter = ''
    minFilter = ''
    needsUpdate = false

    constructor(public canvas: HTMLCanvasElement) {}
  }

  class SpriteMaterial {
    dispose = vi.fn()

    constructor(public options: unknown) {}
  }

  class Sprite {
    position = vector()
    scale = vector()

    constructor(public material: SpriteMaterial) {}
  }

  class BoxGeometry {
    dispose = vi.fn()
  }

  class MeshStandardMaterial {
    dispose = vi.fn()

    constructor(public options: unknown) {}
  }

  class Mesh {
    position = vector()
    userData: Record<string, unknown> = {}

    constructor(public geometry: BoxGeometry, public material: MeshStandardMaterial) {}
  }

  class Raycaster {
    intersectObjects = vi.fn(() => [])
    setFromCamera = vi.fn()
  }

  class Vector2 {
    x = 0
    y = 0
  }

  class AmbientLight {}

  return {
    AmbientLight,
    BoxGeometry,
    CanvasTexture,
    Color,
    DirectionalLight,
    GridHelper,
    LinearFilter: 'LinearFilter',
    Mesh,
    MeshStandardMaterial,
    PerspectiveCamera,
    Raycaster,
    Scene,
    Sprite,
    SpriteMaterial,
    Vector2,
    WebGLRenderer,
  }
})

describe('LoadImpact3DHeatmap', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    threeMockState.rendererInstances.length = 0

    vi.spyOn(window, 'requestAnimationFrame').mockReturnValue(1)
    vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => {})
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({
      beginPath: vi.fn(),
      closePath: vi.fn(),
      fill: vi.fn(),
      fillText: vi.fn(),
      lineTo: vi.fn(),
      measureText: vi.fn(() => ({ width: 48 })),
      moveTo: vi.fn(),
      quadraticCurveTo: vi.fn(),
      scale: vi.fn(),
      stroke: vi.fn(),
    } as unknown as CanvasRenderingContext2D)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.restoreAllMocks()
  })

  it('keeps the WebGL renderer mounted while live load impact values refresh', async () => {
    await act(async () => {
      root.render(
        <LoadImpact3DHeatmap
          model={createModel(120)}
          selectedCellKey={null}
          onSelectedCellKeyChange={vi.fn()}
        />,
      )
    })

    const firstRenderer = threeMockState.rendererInstances[0]
    const firstCanvas = container.querySelector('canvas')
    expect(threeMockState.rendererInstances).toHaveLength(1)
    expect(firstCanvas).toBe(firstRenderer.domElement)

    await act(async () => {
      root.render(
        <LoadImpact3DHeatmap
          model={createModel(180)}
          selectedCellKey="avgMs:vusers-5"
          onSelectedCellKeyChange={vi.fn()}
        />,
      )
    })

    expect(threeMockState.rendererInstances).toHaveLength(1)
    expect(container.querySelector('canvas')).toBe(firstCanvas)
    expect(firstRenderer.dispose).not.toHaveBeenCalled()
  })
})

const metric: LoadImpactMetric = {
  id: 'avgMs',
  key: 'avgMs',
  label: 'ART',
  unit: 'ms',
  category: 'performance',
}

const bucket: LoadImpactBucket = {
  id: 'vusers-5',
  label: '5 VUsers',
  minVusers: 5,
  maxVusers: 5,
  exact: true,
  sampleCount: 1,
}

function createModel(average: number): LoadImpactModel {
  const point = {
    time: '10:00:00',
    timestampMs: 1,
    vusers: 5,
    value: average,
    source: {
      time: '10:00:00',
      timestampMs: 1,
      vusers: 5,
      avgMs: average,
      errorPct: 0,
      p50: average,
      p90: average,
      p99: average,
      tps: 1,
    },
  }

  return {
    buckets: [bucket],
    pointCount: 1,
    rows: [
      {
        metric,
        minAverage: average,
        maxAverage: average,
        cells: [
          {
            metric,
            bucket,
            average,
            min: average,
            max: average,
            sampleCount: 1,
            intensity: 0.5,
            points: [point],
          },
        ],
      },
    ],
  }
}
