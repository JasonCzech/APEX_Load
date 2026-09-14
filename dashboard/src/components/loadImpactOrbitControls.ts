import type { PerspectiveCamera } from 'three'

const FULL_TURN = Math.PI * 2
const MIN_POLAR_ANGLE = 0.01
const MAX_POLAR_ANGLE = Math.PI - MIN_POLAR_ANGLE

interface OrbitTarget {
  x: number
  y: number
  z: number
  set: (x: number, y: number, z: number) => void
}

/**
 * Small orbit controller tailored to the load-impact chart. It preserves the
 * chart's drag, touch, wheel-zoom, damping, and auto-rotate interactions without
 * shipping the general-purpose OrbitControls add-on and its unused pan/key paths.
 */
export class LoadImpactOrbitControls {
  autoRotate = false
  autoRotateSpeed = 2
  enableDamping = false
  enablePan = false
  maxDistance = Number.POSITIVE_INFINITY
  minDistance = 0
  readonly target: OrbitTarget

  private azimuthDelta = 0
  private readonly pointers = new Map<number, { x: number; y: number }>()
  private pinchDistance: number | null = null
  private polarDelta = 0
  private radiusScale = 1

  constructor(
    private readonly camera: PerspectiveCamera,
    private readonly element: HTMLElement,
  ) {
    const target = { x: 0, y: 0, z: 0 } as OrbitTarget
    target.set = (x, y, z) => {
      target.x = x
      target.y = y
      target.z = z
    }
    this.target = target

    element.style.touchAction = 'none'
    element.addEventListener('pointerdown', this.handlePointerDown)
    element.addEventListener('pointermove', this.handlePointerMove)
    element.addEventListener('pointerup', this.handlePointerEnd)
    element.addEventListener('pointercancel', this.handlePointerEnd)
    element.addEventListener('wheel', this.handleWheel, { passive: false })
  }

  dispose() {
    this.element.removeEventListener('pointerdown', this.handlePointerDown)
    this.element.removeEventListener('pointermove', this.handlePointerMove)
    this.element.removeEventListener('pointerup', this.handlePointerEnd)
    this.element.removeEventListener('pointercancel', this.handlePointerEnd)
    this.element.removeEventListener('wheel', this.handleWheel)
    this.pointers.clear()
    this.pinchDistance = null
    this.element.style.removeProperty('touch-action')
  }

  resetMotion() {
    this.azimuthDelta = 0
    this.polarDelta = 0
    this.radiusScale = 1
  }

  update() {
    const position = this.camera.position
    const offsetX = position.x - this.target.x
    const offsetY = position.y - this.target.y
    const offsetZ = position.z - this.target.z
    const currentRadius = Math.max(Number.EPSILON, Math.hypot(offsetX, offsetY, offsetZ))
    const damping = this.enableDamping ? 0.08 : 1
    const autoRotateDelta = this.autoRotate && this.pointers.size === 0
      ? -(FULL_TURN / 60 / 60) * this.autoRotateSpeed
      : 0
    const azimuth = Math.atan2(offsetX, offsetZ) + this.azimuthDelta * damping + autoRotateDelta
    const polar = Math.min(
      MAX_POLAR_ANGLE,
      Math.max(MIN_POLAR_ANGLE, Math.acos(Math.min(1, Math.max(-1, offsetY / currentRadius))) + this.polarDelta * damping),
    )
    const radius = Math.min(this.maxDistance, Math.max(this.minDistance, currentRadius * this.radiusScale))
    const horizontalRadius = radius * Math.sin(polar)

    position.set(
      this.target.x + horizontalRadius * Math.sin(azimuth),
      this.target.y + radius * Math.cos(polar),
      this.target.z + horizontalRadius * Math.cos(azimuth),
    )
    this.camera.lookAt(this.target.x, this.target.y, this.target.z)

    if (this.enableDamping) {
      this.azimuthDelta *= 1 - damping
      this.polarDelta *= 1 - damping
    } else {
      this.azimuthDelta = 0
      this.polarDelta = 0
    }
    this.radiusScale = 1
  }

  private readonly handlePointerDown = (event: PointerEvent) => {
    if (event.button !== 0) return
    this.pointers.set(event.pointerId, { x: event.clientX, y: event.clientY })
    this.pinchDistance = this.measurePinchDistance()
    this.element.setPointerCapture?.(event.pointerId)
  }

  private readonly handlePointerMove = (event: PointerEvent) => {
    const previous = this.pointers.get(event.pointerId)
    if (!previous) return

    this.pointers.set(event.pointerId, { x: event.clientX, y: event.clientY })
    if (this.pointers.size === 1) {
      const interactionHeight = Math.max(1, this.element.clientHeight)
      this.azimuthDelta -= FULL_TURN * (event.clientX - previous.x) / interactionHeight
      this.polarDelta -= FULL_TURN * (event.clientY - previous.y) / interactionHeight
      return
    }

    const nextPinchDistance = this.measurePinchDistance()
    if (this.pinchDistance && nextPinchDistance) {
      this.radiusScale *= this.pinchDistance / nextPinchDistance
    }
    this.pinchDistance = nextPinchDistance
  }

  private readonly handlePointerEnd = (event: PointerEvent) => {
    if (!this.pointers.delete(event.pointerId)) return
    this.pinchDistance = this.measurePinchDistance()
    this.element.releasePointerCapture?.(event.pointerId)
  }

  private readonly handleWheel = (event: WheelEvent) => {
    event.preventDefault()
    this.radiusScale *= Math.exp(Math.min(1, Math.max(-1, event.deltaY * 0.001)))
  }

  private measurePinchDistance() {
    if (this.pointers.size < 2) return null
    const [first, second] = Array.from(this.pointers.values())
    return Math.hypot(second.x - first.x, second.y - first.y)
  }
}
