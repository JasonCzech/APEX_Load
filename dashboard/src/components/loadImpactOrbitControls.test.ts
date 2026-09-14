// @vitest-environment jsdom

import { describe, expect, it, vi } from 'vitest'

import { LoadImpactOrbitControls } from './loadImpactOrbitControls'

function createHarness() {
  const element = document.createElement('canvas')
  Object.defineProperty(element, 'clientHeight', { value: 400 })
  element.setPointerCapture = vi.fn()
  element.releasePointerCapture = vi.fn()
  const camera = {
    lookAt: vi.fn(),
    position: {
      x: 0,
      y: 2,
      z: 10,
      set(x: number, y: number, z: number) {
        this.x = x
        this.y = y
        this.z = z
      },
    },
  }
  const controls = new LoadImpactOrbitControls(camera as never, element)
  return { camera, controls, element }
}

describe('LoadImpactOrbitControls', () => {
  it('orbits in response to pointer dragging and releases the pointer', () => {
    const { camera, controls, element } = createHarness()
    controls.enableDamping = false

    element.dispatchEvent(new PointerEvent('pointerdown', { button: 0, clientX: 10, clientY: 10, pointerId: 4 }))
    element.dispatchEvent(new PointerEvent('pointermove', { clientX: 110, clientY: 10, pointerId: 4 }))
    controls.update()
    element.dispatchEvent(new PointerEvent('pointerup', { pointerId: 4 }))

    expect(camera.position.x).not.toBeCloseTo(0)
    expect(element.setPointerCapture).toHaveBeenCalledWith(4)
    expect(element.releasePointerCapture).toHaveBeenCalledWith(4)
    controls.dispose()
  })

  it('zooms within configured bounds and removes interaction listeners on dispose', () => {
    const { camera, controls, element } = createHarness()
    controls.minDistance = 8
    controls.maxDistance = 11
    const wheel = new WheelEvent('wheel', { cancelable: true, deltaY: 1000 })

    element.dispatchEvent(wheel)
    controls.update()
    expect(Math.hypot(camera.position.x, camera.position.y, camera.position.z)).toBeCloseTo(11)
    expect(wheel.defaultPrevented).toBe(true)

    controls.dispose()
    const positionAfterDispose = { ...camera.position }
    element.dispatchEvent(new WheelEvent('wheel', { cancelable: true, deltaY: -1000 }))
    controls.update()
    expect(camera.position).toMatchObject(positionAfterDispose)
    expect(element.style.touchAction).toBe('')
  })

  it('supports two-pointer pinch zoom for touch interaction parity', () => {
    const { camera, controls, element } = createHarness()
    controls.minDistance = 2
    controls.maxDistance = 20
    const initialDistance = Math.hypot(camera.position.x, camera.position.y, camera.position.z)

    element.dispatchEvent(new PointerEvent('pointerdown', { button: 0, clientX: 100, clientY: 100, pointerId: 1 }))
    element.dispatchEvent(new PointerEvent('pointerdown', { button: 0, clientX: 200, clientY: 100, pointerId: 2 }))
    element.dispatchEvent(new PointerEvent('pointermove', { clientX: 250, clientY: 100, pointerId: 2 }))
    controls.update()

    expect(Math.hypot(camera.position.x, camera.position.y, camera.position.z)).toBeLessThan(initialDistance)
    controls.dispose()
  })

  it('clears pending drag and zoom motion before restoring a camera view', () => {
    const { camera, controls, element } = createHarness()
    controls.enableDamping = true
    const initialPosition = { ...camera.position }

    element.dispatchEvent(new PointerEvent('pointerdown', { button: 0, clientX: 10, clientY: 10, pointerId: 4 }))
    element.dispatchEvent(new PointerEvent('pointermove', { clientX: 110, clientY: 80, pointerId: 4 }))
    element.dispatchEvent(new WheelEvent('wheel', { cancelable: true, deltaY: -500 }))
    controls.resetMotion()
    controls.update()

    expect(camera.position.x).toBeCloseTo(initialPosition.x)
    expect(camera.position.y).toBeCloseTo(initialPosition.y)
    expect(camera.position.z).toBeCloseTo(initialPosition.z)
    controls.dispose()
  })
})
