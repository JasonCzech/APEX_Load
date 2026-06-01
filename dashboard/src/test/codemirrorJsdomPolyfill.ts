// jsdom does not implement the layout-measurement APIs CodeMirror calls
// (Range.getClientRects / getBoundingClientRect, ResizeObserver), so any
// component that embeds CodeMirror — e.g. SourceMappingReview's linked code
// panes — throws when mounted in a jsdom-based test. These no-op stubs let those
// components mount; tests don't assert on editor pixel layout, so empty rects are
// fine. Guards keep this safe for node-environment test files where the DOM
// globals are absent.

if (typeof Range !== 'undefined') {
  if (typeof Range.prototype.getClientRects !== 'function') {
    Range.prototype.getClientRects = function getClientRects() {
      return {
        length: 0,
        item: () => null,
        [Symbol.iterator]: function* emptyRects() {},
      } as unknown as DOMRectList
    }
  }
  if (typeof Range.prototype.getBoundingClientRect !== 'function') {
    Range.prototype.getBoundingClientRect = function getBoundingClientRect() {
      return {
        x: 0, y: 0, top: 0, left: 0, right: 0, bottom: 0, width: 0, height: 0,
        toJSON: () => ({}),
      } as DOMRect
    }
  }
}

if (typeof globalThis !== 'undefined' && typeof (globalThis as { ResizeObserver?: unknown }).ResizeObserver !== 'function') {
  class ResizeObserverStub {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
  ;(globalThis as { ResizeObserver?: unknown }).ResizeObserver = ResizeObserverStub
}
