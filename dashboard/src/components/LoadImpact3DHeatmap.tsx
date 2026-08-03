import { useEffect, useMemo, useRef, useState } from 'react'
import * as THREE from 'three'

import {
  formatLoadImpactCellTitle,
  formatLoadImpactValue,
  loadImpactCellKey,
} from './loadImpactModel'
import type { LoadImpactCell, LoadImpactModel } from './loadImpactModel'
import { LoadImpactOrbitControls } from './loadImpactOrbitControls'

interface Props {
  model: LoadImpactModel
  selectedCellKey: string | null
  onSelectedCellKeyChange: (key: string) => void
}

interface SceneCell {
  cell: LoadImpactCell
  cellKey: string
  xIndex: number
  zIndex: number
}

interface MeshUserData {
  cell: LoadImpactCell
  cellKey: string
  title: string
}

interface LabelSprite {
  sprite: THREE.Sprite
  texture: THREE.CanvasTexture
  material: THREE.SpriteMaterial
}

function disposeMaterial(material: THREE.Material | THREE.Material[]) {
  if (Array.isArray(material)) {
    material.forEach(item => item.dispose())
    return
  }
  material.dispose()
}

function disposeMesh(mesh: THREE.Mesh) {
  mesh.geometry.dispose()
  disposeMaterial(mesh.material)
}

function disposeLabel(label: LabelSprite) {
  label.material.dispose()
  label.texture.dispose()
}

function disposeGrid(grid: THREE.GridHelper) {
  grid.geometry.dispose()
  disposeMaterial(grid.material)
}

function buildLoadImpact3DLayoutKey(model: LoadImpactModel): string {
  return [
    model.buckets.map(bucket => bucket.id).join('|'),
    model.rows.map(row => row.metric.id).join('|'),
  ].join('::')
}

function buildSceneCells(model: LoadImpactModel): SceneCell[] {
  return model.rows.flatMap((row, rowIndex) =>
    row.cells
      .map((cell, bucketIndex) => ({
        cell,
        cellKey: loadImpactCellKey(cell),
        xIndex: bucketIndex,
        zIndex: rowIndex,
      }))
      .filter(item => item.cell.sampleCount > 0),
  )
}

function colorForIntensity(intensity: number, selected: boolean) {
  if (selected) return new THREE.Color('#f59e0b')
  return new THREE.Color('#dbeafe').lerp(new THREE.Color('#2458d3'), Math.max(0, Math.min(1, intensity)))
}

function meshUserData(mesh: THREE.Object3D): MeshUserData | null {
  const data = mesh.userData as Partial<MeshUserData>
  return data.cell && data.cellKey && data.title ? data as MeshUserData : null
}

function truncateLabel(label: string, maxLength: number) {
  return label.length > maxLength ? `${label.slice(0, maxLength - 1)}...` : label
}

function drawRoundRect(
  context: CanvasRenderingContext2D,
  x: number,
  y: number,
  width: number,
  height: number,
  radius: number,
) {
  context.beginPath()
  context.moveTo(x + radius, y)
  context.lineTo(x + width - radius, y)
  context.quadraticCurveTo(x + width, y, x + width, y + radius)
  context.lineTo(x + width, y + height - radius)
  context.quadraticCurveTo(x + width, y + height, x + width - radius, y + height)
  context.lineTo(x + radius, y + height)
  context.quadraticCurveTo(x, y + height, x, y + height - radius)
  context.lineTo(x, y + radius)
  context.quadraticCurveTo(x, y, x + radius, y)
  context.closePath()
}

function createLabelSprite(label: string, options: { maxLength?: number; emphasis?: boolean } = {}): LabelSprite | null {
  const canvas = document.createElement('canvas')
  const context = canvas.getContext('2d')
  if (!context) return null

  const pixelRatio = 2
  const fontSize = options.emphasis ? 28 : 24
  const text = truncateLabel(label, options.maxLength ?? 18)
  const paddingX = options.emphasis ? 20 : 14
  const paddingY = options.emphasis ? 10 : 8
  context.font = `700 ${fontSize}px -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif`
  const textWidth = Math.ceil(context.measureText(text).width)
  const width = textWidth + paddingX * 2
  const height = fontSize + paddingY * 2

  canvas.width = width * pixelRatio
  canvas.height = height * pixelRatio
  context.scale(pixelRatio, pixelRatio)
  context.font = `700 ${fontSize}px -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif`
  context.textAlign = 'center'
  context.textBaseline = 'middle'

  drawRoundRect(context, 0, 0, width, height, options.emphasis ? 14 : 10)
  context.fillStyle = options.emphasis ? 'rgba(245, 158, 11, 0.92)' : 'rgba(248, 250, 252, 0.88)'
  context.fill()
  context.strokeStyle = options.emphasis ? 'rgba(255, 237, 213, 0.84)' : 'rgba(148, 163, 184, 0.58)'
  context.lineWidth = 1.2
  context.stroke()
  context.fillStyle = options.emphasis ? '#111827' : '#172033'
  context.fillText(text, width / 2, height / 2 + 1)

  const texture = new THREE.CanvasTexture(canvas)
  texture.minFilter = THREE.LinearFilter
  texture.magFilter = THREE.LinearFilter
  texture.needsUpdate = true
  const material = new THREE.SpriteMaterial({
    map: texture,
    transparent: true,
    depthTest: false,
    depthWrite: false,
  })
  const sprite = new THREE.Sprite(material)
  const heightUnits = options.emphasis ? 0.42 : 0.31
  sprite.scale.set(heightUnits * (width / height), heightUnits, 1)

  return { sprite, texture, material }
}

export default function LoadImpact3DHeatmap({
  model,
  selectedCellKey,
  onSelectedCellKeyChange,
}: Props) {
  const containerRef = useRef<HTMLDivElement | null>(null)
  const sceneRef = useRef<THREE.Scene | null>(null)
  const cameraRef = useRef<THREE.PerspectiveCamera | null>(null)
  const controlsRef = useRef<LoadImpactOrbitControls | null>(null)
  const rendererRef = useRef<THREE.WebGLRenderer | null>(null)
  const meshesRef = useRef<THREE.Mesh[]>([])
  const labelsRef = useRef<LabelSprite[]>([])
  const gridRef = useRef<THREE.GridHelper | null>(null)
  const layoutKeyRef = useRef('')
  const selectedCellChangeRef = useRef(onSelectedCellKeyChange)
  const hoveredKeyRef = useRef<string | null>(null)
  const [hoveredCell, setHoveredCell] = useState<LoadImpactCell | null>(null)
  const sceneCells = useMemo(() => buildSceneCells(model), [model])
  const selectedCell = useMemo(
    () => sceneCells.find(item => item.cellKey === selectedCellKey)?.cell ?? null,
    [sceneCells, selectedCellKey],
  )

  useEffect(() => {
    selectedCellChangeRef.current = onSelectedCellKeyChange
  }, [onSelectedCellKeyChange])

  useEffect(() => {
    const container = containerRef.current
    if (!container) return

    const width = Math.max(420, container.clientWidth || 720)
    const height = Math.max(360, container.clientHeight || 420)
    const renderer = new THREE.WebGLRenderer({
      antialias: true,
      alpha: true,
    })
    renderer.setSize(width, height)
    renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2))
    renderer.domElement.className = 'load-impact-3d-canvas'
    container.innerHTML = ''
    container.appendChild(renderer.domElement)
    rendererRef.current = renderer

    const scene = new THREE.Scene()
    sceneRef.current = scene

    const camera = new THREE.PerspectiveCamera(42, width / height, 0.1, 140)
    cameraRef.current = camera
    camera.lookAt(0, 0.45, 0)

    const controls = new LoadImpactOrbitControls(camera, renderer.domElement)
    controlsRef.current = controls
    controls.enableDamping = true
    controls.autoRotate = true
    controls.autoRotateSpeed = 0.24
    controls.enablePan = false
    controls.minDistance = 8.5
    controls.maxDistance = 42
    controls.target.set(0, 0.5, 0)

    scene.add(new THREE.AmbientLight(0xffffff, 0.74))
    const keyLight = new THREE.DirectionalLight(0xffffff, 1.12)
    keyLight.position.set(-4, 8, 6)
    scene.add(keyLight)
    const fillLight = new THREE.DirectionalLight(0x93c5fd, 0.45)
    fillLight.position.set(5, 4, -5)
    scene.add(fillLight)

    const raycaster = new THREE.Raycaster()
    const pointer = new THREE.Vector2()

    const intersectCell = (event: PointerEvent): MeshUserData | null => {
      const rect = renderer.domElement.getBoundingClientRect()
      pointer.x = ((event.clientX - rect.left) / rect.width) * 2 - 1
      pointer.y = -((event.clientY - rect.top) / rect.height) * 2 + 1
      raycaster.setFromCamera(pointer, camera)
      const hit = raycaster.intersectObjects(meshesRef.current, false)[0]
      return hit ? meshUserData(hit.object) : null
    }

    const handlePointerMove = (event: PointerEvent) => {
      const hit = intersectCell(event)
      const nextKey = hit?.cellKey ?? null
      if (hoveredKeyRef.current === nextKey) return
      hoveredKeyRef.current = nextKey
      renderer.domElement.style.cursor = hit ? 'pointer' : 'grab'
      setHoveredCell(hit?.cell ?? null)
    }

    const handlePointerLeave = () => {
      hoveredKeyRef.current = null
      renderer.domElement.style.cursor = 'grab'
      setHoveredCell(null)
    }

    const handleClick = (event: PointerEvent) => {
      const hit = intersectCell(event)
      if (hit) {
        selectedCellChangeRef.current(hit.cellKey)
      }
    }

    renderer.domElement.addEventListener('pointermove', handlePointerMove)
    renderer.domElement.addEventListener('pointerleave', handlePointerLeave)
    renderer.domElement.addEventListener('click', handleClick)

    const syncSize = (nextWidth: number, nextHeight: number) => {
      renderer.setSize(nextWidth, nextHeight)
      camera.aspect = nextWidth / nextHeight
      camera.updateProjectionMatrix()
    }

    const handleResize = () => {
      syncSize(
        Math.max(420, container.clientWidth || width),
        Math.max(360, container.clientHeight || height),
      )
    }

    const resizeObserver = typeof ResizeObserver === 'undefined'
      ? null
      : new ResizeObserver(([entry]) => {
        syncSize(
          Math.max(420, entry.contentRect.width),
          Math.max(360, entry.contentRect.height),
        )
      })
    resizeObserver?.observe(container)
    window.addEventListener('resize', handleResize)

    let animationFrame = 0
    const animate = () => {
      controls.update()
      renderer.render(scene, camera)
      animationFrame = window.requestAnimationFrame(animate)
    }
    animate()

    return () => {
      window.cancelAnimationFrame(animationFrame)
      resizeObserver?.disconnect()
      window.removeEventListener('resize', handleResize)
      renderer.domElement.removeEventListener('pointermove', handlePointerMove)
      renderer.domElement.removeEventListener('pointerleave', handlePointerLeave)
      renderer.domElement.removeEventListener('click', handleClick)
      controls.dispose()
      meshesRef.current.forEach(mesh => disposeMesh(mesh))
      meshesRef.current = []
      labelsRef.current.forEach(label => disposeLabel(label))
      labelsRef.current = []
      if (gridRef.current) {
        disposeGrid(gridRef.current)
        gridRef.current = null
      }
      renderer.dispose()
      renderer.domElement.remove()
      rendererRef.current = null
      sceneRef.current = null
      cameraRef.current = null
      controlsRef.current = null
    }
  }, [])

  useEffect(() => {
    const scene = sceneRef.current
    const camera = cameraRef.current
    const controls = controlsRef.current
    if (!scene || !camera || !controls) return

    meshesRef.current.forEach(mesh => {
      scene.remove(mesh)
      disposeMesh(mesh)
    })
    meshesRef.current = []
    labelsRef.current.forEach(label => {
      scene.remove(label.sprite)
      disposeLabel(label)
    })
    labelsRef.current = []

    if (sceneCells.length === 0) {
      hoveredKeyRef.current = null
      setHoveredCell(null)
      return
    }

    const rowCount = model.rows.length
    const bucketCount = model.buckets.length
    const xSpacing = 1.02
    const zSpacing = 0.82
    const xOffset = ((bucketCount - 1) * xSpacing) / 2
    const zOffset = ((rowCount - 1) * zSpacing) / 2
    const maxDimension = Math.max(bucketCount * xSpacing, rowCount * zSpacing)
    const layoutKey = buildLoadImpact3DLayoutKey(model)

    if (layoutKeyRef.current !== layoutKey || !gridRef.current) {
      layoutKeyRef.current = layoutKey
      if (gridRef.current) {
        scene.remove(gridRef.current)
        disposeGrid(gridRef.current)
      }
      const grid = new THREE.GridHelper(Math.max(8, maxDimension + 2), Math.max(8, bucketCount), 0xd7dfeb, 0xd7dfeb)
      grid.position.y = -0.02
      gridRef.current = grid
      scene.add(grid)
      camera.position.set(
        maxDimension * 0.9,
        Math.max(7.4, rowCount * 0.5),
        Math.max(12.5, bucketCount * 1.16),
      )
      camera.lookAt(0, 0.45, 0)
      controls.target.set(0, 0.5, 0)
      controls.update()
    }

    sceneCells.forEach(item => {
      const heightValue = 0.14 + item.cell.intensity * 2.7
      const geometry = new THREE.BoxGeometry(0.68, heightValue, 0.54)
      const material = new THREE.MeshStandardMaterial({
        color: colorForIntensity(item.cell.intensity, item.cellKey === selectedCellKey),
        roughness: 0.55,
        metalness: 0.04,
      })
      const mesh = new THREE.Mesh(geometry, material)
      mesh.position.set(item.xIndex * xSpacing - xOffset, heightValue / 2, item.zIndex * zSpacing - zOffset)
      mesh.userData = {
        cell: item.cell,
        cellKey: item.cellKey,
        title: formatLoadImpactCellTitle(item.cell),
      } satisfies MeshUserData
      meshesRef.current.push(mesh)
      scene.add(mesh)
    })

    model.buckets.forEach((bucket, bucketIndex) => {
      const label = createLabelSprite(bucket.label, { maxLength: 14 })
      if (!label) return
      label.sprite.position.set(bucketIndex * xSpacing - xOffset, 0.28, zOffset + 1.08)
      labelsRef.current.push(label)
      scene.add(label.sprite)
    })

    model.rows.forEach((row, rowIndex) => {
      const label = createLabelSprite(row.metric.label, { maxLength: 18 })
      if (!label) return
      label.sprite.position.set(xOffset + 1.22, 0.28, rowIndex * zSpacing - zOffset)
      labelsRef.current.push(label)
      scene.add(label.sprite)
    })

    const vuserAxisLabel = createLabelSprite('VUser Load', { emphasis: true, maxLength: 16 })
    if (vuserAxisLabel) {
      vuserAxisLabel.sprite.position.set(0, 0.62, zOffset + 1.86)
      labelsRef.current.push(vuserAxisLabel)
      scene.add(vuserAxisLabel.sprite)
    }

    const metricAxisLabel = createLabelSprite('Metric', { emphasis: true, maxLength: 12 })
    if (metricAxisLabel) {
      metricAxisLabel.sprite.position.set(xOffset + 2.08, 0.62, 0)
      labelsRef.current.push(metricAxisLabel)
      scene.add(metricAxisLabel.sprite)
    }

    const hoveredKey = hoveredKeyRef.current
    if (hoveredKey) {
      setHoveredCell(sceneCells.find(item => item.cellKey === hoveredKey)?.cell ?? null)
    }
  }, [model, sceneCells, selectedCellKey])

  const activeCell = hoveredCell ?? selectedCell

  return (
    <div className="load-impact-3d">
      <div
        ref={containerRef}
        className="load-impact-3d-scene"
        role="img"
        aria-label="3D load impact heatmap"
        data-testid="load-impact-3d-scene"
      />
      <div className="load-impact-3d-readout" aria-live="polite">
        {activeCell ? (
          <>
            <strong>{activeCell.metric.label} / {activeCell.bucket.label}</strong>
            <span>
              {formatLoadImpactValue(activeCell.average, activeCell.metric.unit)} · {activeCell.sampleCount} samples
            </span>
          </>
        ) : (
          <>
            <strong>{model.rows.length} metrics / {model.buckets.length} load buckets</strong>
            <span>3D load-impact surface</span>
          </>
        )}
      </div>
      <div className="load-impact-3d-axis">
        <div className="load-impact-3d-axis-group">
          {model.buckets.map(bucket => (
            <span key={bucket.id}>{bucket.label}</span>
          ))}
        </div>
        <div className="load-impact-3d-axis-group metric">
          {model.rows.slice(0, 8).map(row => (
            <span key={row.metric.id}>{row.metric.label}</span>
          ))}
          {model.rows.length > 8 && <span>+{model.rows.length - 8}</span>}
        </div>
      </div>
    </div>
  )
}
