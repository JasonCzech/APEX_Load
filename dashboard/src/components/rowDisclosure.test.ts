// @vitest-environment jsdom
import type { MouseEvent } from 'react'
import { expect, it } from 'vitest'
import { insideRowDisclosure } from './rowDisclosure'

const click = (row: HTMLElement, target: Element) => ({ currentTarget: row, target }) as unknown as MouseEvent<HTMLElement>

it('ignores only disclosures inside the clicked row', () => {
  const outer = document.createElement('details')
  outer.innerHTML = '<table><tbody><tr><td><span>Name</span><details><summary>More</summary><dl><dt>VUsers</dt></dl></details></td></tr></tbody></table>'
  const row = outer.querySelector('tr')!
  expect(insideRowDisclosure(click(row, outer.querySelector('span')!))).toBe(false)
  expect(insideRowDisclosure(click(row, row.querySelector('summary')!))).toBe(true)
  expect(insideRowDisclosure(click(row, row.querySelector('dt')!))).toBe(true)
})
