// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import LibraryAssignmentDialog from './LibraryAssignmentDialog'
import { assignmentMatches } from './libraryAssignmentModel'
import { matchesAssignmentFilter } from './libraryAssignmentModel'
import { suggestedApplication, suggestedReleases } from './libraryLaunchAssignments'
import type { StoredScenario, StoredScript } from '../types'
const api = { listApplications: vi.fn(), listReleases: vi.fn(), replaceLibraryAssignment: vi.fn(), getScript: vi.fn(), getScenario: vi.fn() }
vi.mock('../hooks/useApi', () => ({ useApi: () => api }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
const assigned = { application: { id: 'a', name: 'App' }, releases: [{ id: 'r', name: 'Release', application_id: 'a' }], revision: 1 }
const item = (id: string): { id: string; name: string; library_assignment?: typeof assigned } => ({ id, name: id })
describe('library assignments', () => {
 let root: Root
 let container: HTMLDivElement
 const onClose = vi.fn(), onBusy = vi.fn(), onResult = vi.fn()
 beforeEach(() => {
  vi.resetAllMocks()
  api.listApplications.mockResolvedValue({ applications: [{ id: 'a', name: 'App', project_id: 'p' }, {id:'foreign',name:'Foreign',project_id:'other'}] })
  api.listReleases.mockResolvedValue({ releases: [{ id: 'r', name: 'Release', application_id: 'a' }, {id:'archived',name:'Old',application_id:'a',archived_at:'2026-01-01'}] })
  api.replaceLibraryAssignment.mockResolvedValue(assigned)
  container=document.createElement('div'); document.body.append(container); root=createRoot(container)
 })
 afterEach(() => { act(() => root.unmount()); container.remove() })
 const render = async (items = [item('one')], projectId='p') => { await act(async () => root.render(<LibraryAssignmentDialog key={projectId} projectId={projectId} kind="scripts" items={items} onClose={onClose} onBusy={onBusy} onResult={onResult} />)) }
 const button = (name: string) => [...document.querySelectorAll('button')].find(button => button.textContent === name)!
 const choose = async (value: string) => { await act(async () => { const select=document.querySelector('select')!; select.value=value; select.dispatchEvent(new Event('change',{bubbles:true})) }) }
 it('replaces a single assignment and excludes foreign applications and new archived releases',async()=>{
  await render(); await choose('a')
  expect(document.body.textContent).not.toContain('Foreign')
  const boxes=document.querySelectorAll<HTMLInputElement>('input[type=checkbox]')
  expect(boxes[1].disabled).toBe(true)
  await act(async()=>boxes[0].click())
  await act(async()=>button('Apply assignment').click())
  expect(api.replaceLibraryAssignment).toHaveBeenCalledWith('scripts','one','a',['r'],0)
  expect(onClose).toHaveBeenCalledOnce()
 })
 it('skips unchanged, allows clearing and cancellation',async()=>{
  await render(); expect(button('Apply assignment').disabled).toBe(true)
  await act(async()=>button('Cancel').click()); expect(api.replaceLibraryAssignment).not.toHaveBeenCalled()
  await act(async()=>root.render(<></>))
  await render([{...item('one'),library_assignment:assigned}] as ReturnType<typeof item>[])
  await choose(''); await act(async()=>button('Apply assignment').click())
  expect(api.replaceLibraryAssignment).toHaveBeenCalledWith('scripts','one',null,[],1)
 })
 it('retries only failures and fences duplicate submissions',async()=>{
  await render([item('one'),item('two')]); await choose('a')
  let finish!: (value: typeof assigned)=>void
  api.replaceLibraryAssignment.mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve})).mockRejectedValueOnce(new Error('Temporary failure'))
  await act(async()=>{button('Apply assignment').click();button('Apply assignment').click()})
  expect(api.replaceLibraryAssignment).toHaveBeenCalledTimes(2)
  await act(async()=>finish(assigned))
  expect(document.body.textContent).toContain('Updated 1; unchanged 0; failed 1')
  await act(async()=>button('Retry failed assignments').click())
  expect(api.replaceLibraryAssignment).toHaveBeenCalledTimes(3)
  expect(api.replaceLibraryAssignment.mock.calls[2][1]).toBe('two')
 })
 it('requires conflict reload before reapplying',async()=>{
  await render(); await choose('a')
  api.replaceLibraryAssignment.mockRejectedValueOnce(Object.assign(new Error('Stale revision'),{status:409}))
  await act(async()=>button('Apply assignment').click())
  expect(button('Retry failed assignments').disabled).toBe(true)
  api.getScript.mockResolvedValue({...item('one'),library_assignment:{...assigned,releases:[],revision:4}})
  await act(async()=>button('Reload conflicting assignments').click())
  expect(button('Retry failed assignments')).toBeUndefined()
  expect(button('Apply assignment').disabled).toBe(true)
 })
 it('treats a stale scenario tag like a stale assignment revision',async()=>{
  await render(); await choose('a')
  api.replaceLibraryAssignment.mockRejectedValueOnce(Object.assign(new Error('scenario authored content changed; reload before applying changes'),{status:412}))
  await act(async()=>button('Apply assignment').click())
  expect(button('Retry failed assignments').disabled).toBe(true)
  expect(button('Reload conflicting assignments')).toBeDefined()
 })
 it('offers loading recovery and ignores responses after project switches',async()=>{
  api.listApplications.mockRejectedValueOnce(new Error('Unavailable'))
  await render(); expect(document.body.textContent).toContain('Unavailable')
  await act(async()=>button('Retry loading').click()); await choose('a')
  let finish!: (value: typeof assigned)=>void
  api.replaceLibraryAssignment.mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve}))
  await act(async()=>button('Apply assignment').click())
  await render([item('new')],'other')
  await act(async()=>finish(assigned))
  expect(onResult).not.toHaveBeenCalled()
 })
 it('requires a choice for mixed applications and skips unchanged bulk items', async () => {
  await render([{ ...item('one'), library_assignment: assigned }, item('two')])
  expect(document.querySelector('select')?.value).toBe('__choose__')
  expect(button('Apply assignment').disabled).toBe(true)
  await choose('')
  await act(async () => button('Apply assignment').click())
  expect(api.replaceLibraryAssignment).toHaveBeenCalledTimes(1)
  expect(api.replaceLibraryAssignment).toHaveBeenCalledWith('scripts', 'one', null, [], 1)
  expect(onResult.mock.calls[0][1]).toContain('Updated 1; unchanged 1; failed 0')
 })
 it('retains an existing archived release when adding an active release', async () => {
  const historical = { ...assigned, releases: [{ id: 'archived', name: 'Old', application_id: 'a', archived_at: '2026-01-01' }] }
  await render([{ ...item('one'), library_assignment: historical }])
  const boxes = document.querySelectorAll<HTMLInputElement>('input[type=checkbox]')
  expect(boxes[1].checked).toBe(true)
  expect(boxes[1].disabled).toBe(false)
  await act(async () => boxes[0].click())
  await act(async () => button('Apply assignment').click())
  expect(api.replaceLibraryAssignment).toHaveBeenCalledWith('scripts', 'one', 'a', ['archived', 'r'], 1)
 })
 it('filters editable assignments and suggests unanimous script placement without archived releases',()=>{
  expect(assignmentMatches(assigned,'a',['r'])).toBe(true)
  expect(matchesAssignmentFilter(assigned,'a','r')).toBe(true)
  expect(matchesAssignmentFilter(undefined,'a','r')).toBe(false)
  const scenario={scripts:[{script_id:'one'},{script_id:'two'}]} as StoredScenario
  const scripts=[{id:'one',library_assignment:assigned},{id:'two',library_assignment:{...assigned,releases:[{...assigned.releases[0],archived_at:'2026-01-01'}]}}] as StoredScript[]
  expect(suggestedApplication(scenario,scripts)).toBe('a')
  expect([...suggestedReleases(scenario,scripts,'a')]).toEqual(['r'])
  expect([...suggestedReleases(scenario,scripts,'other')]).toEqual([])
  scripts[1].library_assignment={...assigned,application:{id:'b',name:'Other'}}
  expect(suggestedApplication(scenario,scripts)).toBeUndefined()
  scenario.library_assignment=assigned
  expect(suggestedApplication(scenario,scripts)).toBe('a')
 })
})
