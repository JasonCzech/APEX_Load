// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import LibraryToolbar from './LibraryToolbar'
import FormField from './FormField'
import FormErrorSummary from './FormErrorSummary'
import TabList from './TabList'
import PageHeader from './PageHeader'
import BulkActions from './BulkActions'
import RowActions from './RowActions'
const container = document.createElement('div')
document.body.appendChild(container)
const root = createRoot(container)
afterEach(async () => { await act(async () => root.render(null)); vi.restoreAllMocks() })
it('keeps search before actions and retains filter edits when collapsed', async () => {
  await act(async () => root.render(<LibraryToolbar search={<input aria-label="Search" />} filters={<input defaultValue="production" aria-label="Release" />} actions={<button>Create</button>} />))
  const details = container.querySelector('details')!
  const filter = container.querySelector<HTMLInputElement>('[aria-label="Release"]')!
  details.open = true; filter.value = 'next release'; details.open = false
  expect(filter.value).toBe('next release')
  expect(container.querySelector('input')!.getAttribute('aria-label')).toBe('Search')
  expect(container.querySelector('button')!.textContent).toBe('Create')
})
it('connects hints and errors without losing existing control descriptions', async () => {
  await act(async () => root.render(<FormField label="Email" htmlFor="email" hint="Work email" error="Invalid email"><input aria-describedby="scope" /></FormField>))
  const control = container.querySelector('input')!
  expect(control.id).toBe('email')
  expect(control.getAttribute('aria-describedby')).toBe('scope email-hint email-error')
  expect(control.getAttribute('aria-invalid')).toBe('true')
  await act(async () => root.render(<FormField label="Email" htmlFor="email"><input /></FormField>))
  expect(container.querySelector('[role="alert"]')).toBeNull()
  expect(container.querySelector('input')!.hasAttribute('aria-invalid')).toBe(false)
})
it('resolves summary issues through the owning workflow', async () => {
  const resolve = vi.fn()
  await act(async () => root.render(<FormErrorSummary issues={[{message:''}, {message:'Choose a script',onResolve:resolve}]} />))
  expect(container.querySelectorAll('li')).toHaveLength(1)
  await act(async () => container.querySelector('button')!.click())
  expect(resolve).toHaveBeenCalledOnce()
})
it('honors a handled key and provides shared navigation otherwise', async () => {
  const select = vi.fn()
  await act(async () => root.render(<TabList aria-label="Views" onKeyDown={event => { if(event.key === 'Home') event.preventDefault() }}><button role="tab" aria-selected="true">One</button><button role="tab" aria-selected="false" onClick={select}>Two</button></TabList>))
  const tab = container.querySelector('button')!
  await act(async () => tab.dispatchEvent(new KeyboardEvent('keydown',{key:'ArrowRight',bubbles:true,cancelable:true})))
  expect(select).toHaveBeenCalledOnce()
  await act(async () => tab.dispatchEvent(new KeyboardEvent('keydown',{key:'Home',bubbles:true,cancelable:true})))
  expect(select).toHaveBeenCalledOnce()
})
it('keeps the shared heading and route actions together', async () => {
  await act(async () => root.render(<PageHeader title="Scripts" contextual><button>Create script</button></PageHeader>))
  expect(container.querySelector('#app-page-title')!.textContent).toBe('Scripts')
  expect(container.querySelector('.main-topbar.contextual button')).not.toBeNull()
})

it('shows bulk actions only for a selection and keeps secondary row actions behind More', async () => {
  await act(async () => root.render(<><BulkActions count={0}><button>Delete selected</button></BulkActions><RowActions><button>Duplicate</button></RowActions></>))
  expect(container.querySelector('[aria-label="Selected library items"]')).toBeNull()
  expect(container.querySelector('summary')!.textContent).toBe('More')
  expect(container.querySelector('details')!.open).toBe(false)
  await act(async () => root.render(<BulkActions count={2}><button>Delete selected</button></BulkActions>))
  expect(container.querySelector('[role="status"]')!.textContent).toBe('2 selected')
})
