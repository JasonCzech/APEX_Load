// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import GuideMarkdownArticle, { FOLLOW_IDLE_MS, FOLLOW_MAX_MS } from './GuideMarkdownArticle'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

// Each wait for the diagram renderer (the lazy renderer's and the deep link's)
// settles only when the test releases it.
const loader = vi.hoisted(() => ({ calls: 0, pending: [] as Array<() => void> }))
vi.mock('./mermaidErDiagramLoader', async importOriginal => {
  const actual = await importOriginal<typeof import('./mermaidErDiagramLoader')>()
  return {
    ...actual,
    loadMermaidErDiagram: () => {
      loader.calls++
      return new Promise(resolve => loader.pending.push(() => resolve(actual.loadMermaidErDiagram())))
    },
  }
})
// The same for the highlighted code view.
const codeLoader = vi.hoisted(() => ({ calls: 0, fail: false, pending: [] as Array<() => void> }))
vi.mock('./helpCodeViewLoader', async importOriginal => {
  const actual = await importOriginal<typeof import('./helpCodeViewLoader')>()
  return {
    ...actual,
    loadHelpCodeView: () => {
      codeLoader.calls++
      return new Promise((resolve, reject) => codeLoader.pending.push(() => codeLoader.fail ? reject(new Error('code view chunk unavailable')) : resolve(actual.loadHelpCodeView())))
    },
  }
})

const DIAGRAM_ARTICLE = [
  '# Data model',
  '## Overview',
  '```mermaid',
  'erDiagram',
  '    releases ||--o{ tests : "release_id"',
  '```',
  '## GitHub integration',
  'Repositories and their sync state.',
].join('\n')
const PLAIN_ARTICLE = ['# Guide', '## Overview', 'Text.', '## Limits', 'More text.'].join('\n')

class FakeResizeObserver {
  static live = new Map<FakeResizeObserver, Element>()
  constructor(readonly callback: ResizeObserverCallback) {}
  observe(element: Element) { FakeResizeObserver.live.set(this, element) }
  disconnect() { FakeResizeObserver.live.delete(this) }
}
// Reports a size change of the Help article (or the toolbar) to whatever still observes it.
const notifyResize = (className = 'guide-markdown-article') => {
  for (const [observer, element] of Array.from(FakeResizeObserver.live)) {
    if (element.classList.contains(className)) observer.callback([], observer as unknown as ResizeObserver)
  }
}

// The Help page's scroll owner, with a scroll position the test controls.
let scroller: HTMLDivElement
let root: Root
let frames: Map<number, FrameRequestCallback>
let nextFrame: number
let scrolled: string[]
const originalScrollIntoView = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollIntoView')
beforeEach(() => {
  frames = new Map()
  nextFrame = 0
  scrolled = []
  loader.calls = 0
  loader.pending.length = 0
  codeLoader.calls = 0
  codeLoader.fail = false
  codeLoader.pending.length = 0
  FakeResizeObserver.live.clear()
  vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => { frames.set(++nextFrame, callback); return nextFrame })
  vi.stubGlobal('cancelAnimationFrame', (handle: number) => { frames.delete(handle) })
  vi.stubGlobal('ResizeObserver', FakeResizeObserver)
  Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', { configurable: true, value(this: HTMLElement) { scrolled.push(this.id) } })
  scroller = document.createElement('div')
  scroller.className = 'main-content'
  Object.defineProperty(scroller, 'scrollTop', { configurable: true, writable: true, value: 0 })
  // The toolbar sits beside the page content, as in the app shell.
  const toolbar = document.createElement('div')
  toolbar.className = 'main-topbar'
  const page = document.createElement('div')
  scroller.append(toolbar, page)
  document.body.append(scroller)
  root = createRoot(page)
})
afterEach(async () => {
  await act(async () => root.unmount())
  // Cleanup leaves nothing observing the article or its diagrams.
  expect(FakeResizeObserver.live.size).toBe(0)
  scroller.remove()
  vi.useRealTimers()
  vi.unstubAllGlobals()
  if (originalScrollIntoView) Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', originalScrollIntoView)
  else delete (HTMLElement.prototype as Partial<HTMLElement>).scrollIntoView
})

const render = (source: string, section: string) => act(async () => root.render(<GuideMarkdownArticle source={source} section={section} />))
const runFrames = () => act(async () => {
  const due = Array.from(frames.values())
  frames.clear()
  for (const frame of due) frame(0)
})
// Settles every pending wait for the renderer, and React's reveal of it.
const releaseRenderer = async () => {
  await act(async () => {
    for (const release of loader.pending.splice(0)) release()
    await import('./MermaidErDiagram')
    await new Promise(resolve => setTimeout(resolve, 0))
  })
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })
}
// Lands a deep link below the diagram and returns once it is followed.
const landBelowDiagram = async () => {
  await render(DIAGRAM_ARTICLE, 'github-integration')
  await releaseRenderer()
  await runFrames()
  expect(scrolled).toEqual(['github-integration'])
  expect(document.activeElement?.id).toBe('github-integration')
  scrolled.length = 0
}

it('jumps to the section of an article without diagrams at once, without loading the diagram renderer', async () => {
  await render(PLAIN_ARTICLE, 'limits')
  await runFrames()
  expect(scrolled).toEqual(['limits'])
  expect(document.activeElement?.id).toBe('limits')
  expect(loader.calls).toBe(0)
  expect(codeLoader.calls).toBe(0)
})

const CODE_ARTICLE = ['# Guide', '## Setup', '```json', '{ "a": 1 }', '```', '## Limits', 'Text.'].join('\n')
// Settles every pending wait for the code view, and React's reveal of it.
const releaseCodeView = async () => {
  await act(async () => {
    for (const release of codeLoader.pending.splice(0)) release()
    await import('./HelpCodeView')
    await Promise.resolve()
  })
}

it('jumps to a section below code examples at once, and keeps it aligned while their highlighted view loads', async () => {
  await render(CODE_ARTICLE, 'limits')
  // Only the example loads the code view before the jump; no diagram renderer loads.
  expect(codeLoader.calls).toBe(1)
  expect(loader.calls).toBe(0)
  // The example shows its plain source in the same region until then.
  expect(scroller.querySelector('.markdown-code-editor .help-code-plain code')?.textContent).toBe('{ "a": 1 }')
  await runFrames()
  expect(scrolled).toEqual(['limits'])
  expect(document.activeElement?.id).toBe('limits')
  // The follow waits for the code view too.
  expect(codeLoader.calls).toBe(2)
  scrolled.length = 0

  await releaseCodeView()
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })
  expect(scroller.querySelector('.markdown-code-editor .cm-editor')).not.toBeNull()
  expect(scroller.querySelector('.help-code-plain')).toBeNull()
  // If the view changes the article's layout, the destination stays aligned.
  notifyResize()
  expect(scrolled).toEqual(['limits'])
})

it('keeps following code examples past the idle period until their view has loaded, then settles', async () => {
  // Every step below stays inside the overall cap, so only the idle period can end the follow.
  expect(4 * FOLLOW_IDLE_MS + FOLLOW_IDLE_MS + 1).toBeLessThan(FOLLOW_MAX_MS)
  await render(CODE_ARTICLE, 'limits')
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  await runFrames()
  scrolled.length = 0
  // A slow chunk: long past the idle period, and with layout changes meanwhile,
  // the destination is still followed.
  vi.advanceTimersByTime(FOLLOW_IDLE_MS * 2)
  notifyResize()
  expect(scrolled).toEqual(['limits'])
  vi.advanceTimersByTime(FOLLOW_IDLE_MS * 2)
  notifyResize()
  expect(scrolled).toEqual(['limits', 'limits'])
  // Once the view has loaded, the layout counts as settled after the idle
  // period, with no further change needed to start it.
  await releaseCodeView()
  vi.advanceTimersByTime(FOLLOW_IDLE_MS + 1)
  notifyResize()
  expect(scrolled).toHaveLength(2)
})

it('settles the follow one idle period after the code view fails to load', async () => {
  expect(2 * FOLLOW_IDLE_MS + FOLLOW_IDLE_MS + 1).toBeLessThan(FOLLOW_MAX_MS)
  codeLoader.fail = true
  await render(CODE_ARTICLE, 'limits')
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  await runFrames()
  scrolled.length = 0
  vi.advanceTimersByTime(FOLLOW_IDLE_MS * 2)
  notifyResize()
  expect(scrolled).toEqual(['limits'])
  await act(async () => {
    for (const release of codeLoader.pending.splice(0)) release()
    await Promise.resolve()
    await Promise.resolve()
  })
  vi.advanceTimersByTime(FOLLOW_IDLE_MS + 1)
  notifyResize()
  expect(scrolled).toEqual(['limits'])
})

it('keeps following after a taller toolbar re-aligns the destination', async () => {
  await render(CODE_ARTICLE, 'limits')
  await runFrames()
  expect(document.activeElement?.id).toBe('limits')
  scrolled.length = 0
  // The toolbar grows: the page re-aligns the destination, which moves the scroller.
  Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', { configurable: true, value(this: HTMLElement) { scrolled.push(this.id); scroller.scrollTop = 48 } })
  notifyResize('main-topbar')
  expect(scrolled).toEqual(['limits'])
  scroller.dispatchEvent(new Event('scroll'))
  // That scroll is the page's own, not the reader's: the follow goes on.
  notifyResize()
  expect(scrolled).toEqual(['limits', 'limits'])
  await releaseCodeView()
})

it('stops following code examples whose view never loads at the overall cap', async () => {
  await render(CODE_ARTICLE, 'limits')
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  await runFrames()
  scrolled.length = 0
  vi.advanceTimersByTime(FOLLOW_MAX_MS - 1)
  notifyResize()
  expect(scrolled).toEqual(['limits'])
  vi.advanceTimersByTime(2)
  notifyResize()
  expect(scrolled).toEqual(['limits'])
  vi.useRealTimers()
  await releaseCodeView()
})

it('jumps to a section below ER diagrams only once they have drawn, and keeps it aligned until the reader acts', async () => {
  await render(DIAGRAM_ARTICLE, 'github-integration')
  await runFrames()
  // The renderer and the deep link both wait for the diagram chunk.
  expect(loader.calls).toBe(2)
  // The diagram still shows its source, so the jump waits.
  expect(scroller.querySelector('pre code[data-language="mermaid"]')).not.toBeNull()
  expect(scrolled).toEqual([])
  expect(document.activeElement?.id).not.toBe('github-integration')

  await releaseRenderer()
  expect(scroller.querySelector('figure.entity-diagram')).not.toBeNull()
  expect(scroller.querySelector('pre code[data-language="mermaid"]')).toBeNull()
  await runFrames()
  expect(scrolled).toEqual(['github-integration'])
  expect(document.activeElement?.id).toBe('github-integration')

  // A late layout change above the destination re-aligns it.
  notifyResize()
  expect(scrolled).toEqual(['github-integration', 'github-integration'])
  // Once the reader scrolls, the article no longer moves them.
  document.dispatchEvent(new Event('wheel'))
  notifyResize()
  expect(scrolled).toEqual(['github-integration', 'github-integration'])
})

it('leaves the page to a reader who acts while the diagrams load: no jump, focus or follow', async () => {
  for (const intent of ['keydown', 'wheel', 'pointerdown', 'touchstart']) {
    await act(async () => root.unmount())
    root = createRoot(scroller)
    const reader = document.createElement('button')
    document.body.append(reader)
    reader.focus()
    await render(DIAGRAM_ARTICLE, 'github-integration')
    document.dispatchEvent(new Event(intent))
    await releaseRenderer()
    await runFrames()
    notifyResize()
    expect(scrolled, intent).toEqual([])
    expect(document.activeElement, intent).toBe(reader)
    reader.remove()
  }
})

it('never jumps to a section the page has left, or after it unmounts, while the diagrams load', async () => {
  await render(DIAGRAM_ARTICLE, 'github-integration')
  await render(DIAGRAM_ARTICLE, 'overview')
  await releaseRenderer()
  await runFrames()
  expect(scrolled).toEqual(['overview'])
  expect(document.activeElement?.id).toBe('overview')

  scrolled.length = 0
  await render(DIAGRAM_ARTICLE, 'github-integration')
  await act(async () => root.unmount())
  root = createRoot(scroller)
  await releaseRenderer()
  await runFrames()
  expect(scrolled).toEqual([])
  expect(frames.size).toBe(0)
})

it('stops following once the page scrolls other than by the alignment', async () => {
  await landBelowDiagram()
  // The alignment's own scroll event leaves the follow in place.
  scroller.dispatchEvent(new Event('scroll'))
  notifyResize()
  expect(scrolled).toEqual(['github-integration'])
  // A screen reader browsing on scrolls the page without a key event.
  scroller.scrollTop = 480
  scroller.dispatchEvent(new Event('scroll'))
  notifyResize()
  expect(scrolled).toEqual(['github-integration'])
  // Nothing follows the article any more; the diagram and the toolbar keep their own observers.
  expect(Array.from(FakeResizeObserver.live.values()).filter(element => element.classList.contains('guide-markdown-article'))).toHaveLength(0)
})

it('stops following once the layout has settled, and after the overall cap', async () => {
  await render(DIAGRAM_ARTICLE, 'github-integration')
  await releaseRenderer()
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  await runFrames()
  scrolled.length = 0
  notifyResize()
  expect(scrolled).toHaveLength(1)
  vi.advanceTimersByTime(FOLLOW_IDLE_MS + 1)
  notifyResize()
  expect(scrolled).toHaveLength(1)

  // A layout that keeps changing is followed only up to the cap.
  await act(async () => root.unmount())
  root = createRoot(scroller)
  vi.useRealTimers()
  await render(DIAGRAM_ARTICLE, 'github-integration')
  await releaseRenderer()
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  await runFrames()
  scrolled.length = 0
  // Resizes every half idle period never let the follow settle; the cap ends it.
  const step = FOLLOW_IDLE_MS / 2
  let elapsed = 0
  let followedBeforeCap = 0
  while (elapsed < FOLLOW_MAX_MS + 4 * step) {
    notifyResize()
    if (elapsed < FOLLOW_MAX_MS) followedBeforeCap = scrolled.length
    vi.advanceTimersByTime(step)
    elapsed += step
  }
  expect(followedBeforeCap).toBe(Math.ceil(FOLLOW_MAX_MS / step))
  expect(scrolled).toHaveLength(followedBeforeCap)
})
