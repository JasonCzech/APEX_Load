// @vitest-environment jsdom
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import ImportWorkflowGuide from './ImportWorkflowGuide'

describe('import workflow orientation', () => {
  it.each([
    ['source', 'Provide source'], ['review', 'Review and resolve'], ['saved', 'Debug one iteration'],
  ] as const)('identifies the next task at %s', (stage, label) => {
    const container = document.createElement('div')
    container.innerHTML = renderToStaticMarkup(<ImportWorkflowGuide stage={stage} />)
    expect(container.querySelectorAll('[aria-current="step"]')).toHaveLength(1)
    expect(container.querySelector('[aria-current="step"]')?.textContent).toContain(label)
    expect(container.textContent).toContain('Import checks do not run your script')
  })

  it('does not promise saving or execution on a review-only surface', () => {
    const container = document.createElement('div')
    container.innerHTML = renderToStaticMarkup(<ImportWorkflowGuide stage="review" reviewOnly />)
    expect(container.querySelectorAll('li')).toHaveLength(2)
    expect(container.querySelector('[aria-current="step"]')?.textContent).toContain('Review findings')
    expect(container.textContent).toContain('does not create or save a script')
  })
})
