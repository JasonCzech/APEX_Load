// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '../hooks/apiClient'
import { type SecretBindingRequirement, type SecretBindingSelection } from '../types/scriptImports'
import SecretBindingReview, { type SecretBindingReviewHandle } from './SecretBindingReview'
import { credentialNoun } from './secretBindingNoun'
import { createProjectSecretsInBatches } from './secretBindingBatches'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = vi.hoisted(() => ({
  getProjectSecretStatuses: vi.fn(),
  createProjectSecrets: vi.fn(),
  migrateImportSecrets: vi.fn(),
  discardImportSecretSession: vi.fn(),
}))

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

const requirements: SecretBindingRequirement[] = [
  {
    id: 'authorization-header',
    path: '/actions/0/headers/Authorization',
    label: 'Create Order — Authorization header',
    context: 'Bearer credential',
    suggested_name: 'ORDER_API_TOKEN',
    replacement: 'Bearer {{secret.ORDER_API_TOKEN}}',
  },
  {
    id: 'refresh-token',
    path: '/init/0/params/refresh_token',
    label: 'Refresh session — Refresh token',
    context: 'OAuth refresh credential',
    suggested_name: 'REFRESH_TOKEN',
    replacement: '{{secret.REFRESH_TOKEN}}',
  },
]

function setInput(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

function toggleDetails(details: HTMLDetailsElement, open: boolean) {
  details.open = open
  details.dispatchEvent(new Event('toggle'))
}

describe('SecretBindingReview', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    vi.clearAllMocks()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    apiMock.getProjectSecretStatuses.mockResolvedValue({
      project_id: 'project-1',
      capabilities: {
        provider: 'database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: false,
      },
      secrets: [],
    })
    apiMock.createProjectSecrets.mockResolvedValue({ project_id: 'project-1', secrets: [], count: 1 })
    apiMock.migrateImportSecrets.mockResolvedValue({ project_id: 'project-1', secrets: [], count: 0 })
    apiMock.discardImportSecretSession.mockResolvedValue(undefined)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  function render(onApply = vi.fn()) {
    act(() => {
      root.render(<SecretBindingReview requirements={requirements} onApply={onApply} />)
    })
    return onApply
  }

  it('probes project secret status once while the parent re-renders with equal requirements', async () => {
    // Importers rebuild their requirement arrays on every render, and unrelated
    // polling re-renders them constantly. Equal names must not trigger another
    // status request each time.
    const renderCopy = () => root.render(
      <SecretBindingReview
        projectId="project-1"
        requirements={requirements.map(requirement => ({ ...requirement }))}
        onApply={vi.fn()}
      />,
    )
    await act(async () => renderCopy())
    for (let rerender = 0; rerender < 5; rerender += 1) {
      await act(async () => renderCopy())
    }
    expect(apiMock.getProjectSecretStatuses).toHaveBeenCalledTimes(1)
    expect(apiMock.getProjectSecretStatuses).toHaveBeenCalledWith('project-1', ['ORDER_API_TOKEN', 'REFRESH_TOKEN'])
  })

  it('re-checks project secret status once when the operator returns to the page', async () => {
    // Secrets are typically configured on another page or tab; returning
    // fires both focus and visibilitychange, which must coalesce.
    await act(async () => root.render(<SecretBindingReview projectId="project-1" requirements={requirements} onApply={vi.fn()} />))
    expect(apiMock.getProjectSecretStatuses).toHaveBeenCalledTimes(1)
    await act(async () => {
      window.dispatchEvent(new Event('focus'))
      document.dispatchEvent(new Event('visibilitychange'))
    })
    expect(apiMock.getProjectSecretStatuses).toHaveBeenCalledTimes(2)
    await act(async () => root.render(<SecretBindingReview projectId="project-1" requirements={requirements.map(requirement => ({ ...requirement }))} onApply={vi.fn()} />))
    expect(apiMock.getProjectSecretStatuses).toHaveBeenCalledTimes(2)
  })

  it('keeps the known credential status visible during a background re-check', async () => {
    await act(async () => root.render(<SecretBindingReview projectId="project-1" requirements={requirements} onApply={vi.fn()} />))
    expect(container.textContent).not.toContain('Checking project secret store')
    let finishRecheck: (value: unknown) => void = () => {}
    apiMock.getProjectSecretStatuses.mockImplementationOnce(() => new Promise(resolve => { finishRecheck = resolve }))
    await act(async () => { window.dispatchEvent(new Event('focus')) })
    expect(apiMock.getProjectSecretStatuses).toHaveBeenCalledTimes(2)
    expect(container.textContent).not.toContain('Checking project secret store')
    await act(async () => finishRecheck({
      project_id: 'project-1',
      capabilities: { provider: 'database', writable: true, create_supported: true, replace_supported: true, source_secret_migration_policy_enabled: false },
      secrets: [],
    }))
    expect(container.textContent).not.toContain('Checking project secret store')
  })

  it('does not re-check credential status on focus while a commit is applying', async () => {
    const handle = { current: null as SecretBindingReviewHandle | null }
    let finishApply: () => void = () => {}
    const onApply = vi.fn(() => new Promise<void>(resolve => { finishApply = resolve }))
    await act(async () => root.render(<SecretBindingReview ref={value => { handle.current = value }} projectId="project-1" requirements={requirements} onApply={onApply} />))
    let commit: Promise<boolean> = Promise.resolve(false)
    await act(async () => { commit = handle.current!.commit() })
    expect(onApply).toHaveBeenCalledTimes(1)
    const callsDuringApply = apiMock.getProjectSecretStatuses.mock.calls.length
    await act(async () => { window.dispatchEvent(new Event('focus')) })
    expect(apiMock.getProjectSecretStatuses).toHaveBeenCalledTimes(callsDuringApply)
    await act(async () => { finishApply(); await commit })
  })

  it('edits credentials inline, retains edits across review tabs, and cancels without writing secrets', async () => {
    const onApply = vi.fn()
    const onEditingChange = vi.fn()
    const button = (label: string) => Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent === label)!
    const renderInline = (generation: string) => root.render(<SecretBindingReview inlineEditing onEditingChange={onEditingChange} reviewGeneration={generation} requirements={requirements} projectId="project-1" onApply={onApply} />)
    await act(async () => renderInline('one'))
    expect(container.querySelector<HTMLElement>('.secret-binding-review-control')?.hidden).toBe(true)
    expect(container.querySelector('.credential-inline-current')?.textContent).toContain('Not configured')
    await act(async () => button('Edit credentials').click())
    expect(onEditingChange).toHaveBeenLastCalledWith(true)
    const name = container.querySelector<HTMLInputElement>('.secret-binding-review-control input[type="text"]')!
    const value = container.querySelector<HTMLInputElement>('.secret-binding-review-control input[type="password"]')!
    await act(async () => { setInput(name, 'NEW_TOKEN'); setInput(value, 'synthetic-write-only-value') })
    await act(async () => renderInline('two'))
    expect(name.value).toBe('NEW_TOKEN')
    expect(value.value).toBe('synthetic-write-only-value')
    await act(async () => button('Cancel credentials').click())
    expect(name.value).toBe('ORDER_API_TOKEN')
    expect(value.value).toBe('')
    expect(onEditingChange).toHaveBeenLastCalledWith(false)
    expect(onApply).not.toHaveBeenCalled()
    expect(apiMock.createProjectSecrets).not.toHaveBeenCalled()
  })

  it('retains inline binding edits after failure and applies them without navigating or saving the script', async () => {
    const onApply = vi.fn().mockRejectedValueOnce(new Error('Draft changed; retry')).mockResolvedValueOnce(undefined)
    const button = (label: string) => Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent === label)!
    await act(async () => root.render(<SecretBindingReview inlineEditing requirements={requirements} projectId="project-1" onApply={onApply} />))
    await act(async () => button('Edit credentials').click())
    const name = container.querySelector<HTMLInputElement>('.secret-binding-review-control input[type="text"]')!
    await act(async () => setInput(name, 'NEW_TOKEN'))
    await act(async () => button('Apply credentials').click())
    expect(container.textContent).toContain('Draft changed; retry')
    expect(name.value).toBe('NEW_TOKEN')
    expect(container.querySelector<HTMLElement>('.secret-binding-review-control')?.hidden).toBe(false)
    await act(async () => button('Apply credentials').click())
    expect(onApply).toHaveBeenLastCalledWith(expect.arrayContaining([{ requirement_id: requirements[0].id, secret_name: 'NEW_TOKEN' }]))
    expect(container.querySelector<HTMLElement>('.secret-binding-review-control')?.hidden).toBe(true)
    expect(container.querySelector('.credential-inline-current')?.textContent).toContain('{{secret.NEW_TOKEN}}')
    expect(apiMock.createProjectSecrets).not.toHaveBeenCalled()
  })

  it('does not close a new credential edit session when an older apply finishes', async () => {
    let finishApply!: () => void
    const onApply = vi.fn(() => new Promise<void>(resolve => { finishApply = resolve }))
    const button = (label: string) => Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent === label)!
    const renderInline = (projectId: string) => root.render(<SecretBindingReview inlineEditing requirements={requirements} projectId={projectId} onApply={onApply} />)
    await act(async () => renderInline('project-1'))
    await act(async () => button('Edit credentials').click())
    await act(async () => button('Apply credentials').click())
    expect(onApply).toHaveBeenCalledOnce()
    await act(async () => renderInline('project-2'))
    await act(async () => button('Edit credentials').click())
    const name = container.querySelector<HTMLInputElement>('.secret-binding-review-control input[type="text"]')!
    await act(async () => setInput(name, 'SECOND_PROJECT_TOKEN'))
    await act(async () => finishApply())
    expect(name.value).toBe('SECOND_PROJECT_TOKEN')
    expect(container.querySelector<HTMLElement>('.secret-binding-review-control')?.hidden).toBe(false)
    expect(button('Cancel credentials')).toBeDefined()
  })

  it('reports disclosure-safe imperative commit errors and clears them on retry', async () => {
    const onErrorChange = vi.fn()
    const onApply = vi.fn()
      .mockRejectedValueOnce(new Error('Credential preparation failed: password=synthetic-write-only-value'))
      .mockResolvedValueOnce(undefined)
    const reviewRef = { current: null as SecretBindingReviewHandle | null }
    await act(async () => root.render(
      <SecretBindingReview ref={reviewRef} inlineEditing requirements={requirements} projectId="project-1" onApply={onApply} onErrorChange={onErrorChange} />,
    ))

    await act(async () => { expect(await reviewRef.current?.commit()).toBe(false) })

    expect(onErrorChange).toHaveBeenLastCalledWith('Credential preparation failed: password=[redacted]')
    expect(JSON.stringify(onErrorChange.mock.calls)).not.toContain('synthetic-write-only-value')
    expect(apiMock.createProjectSecrets).not.toHaveBeenCalled()
    expect(apiMock.migrateImportSecrets).not.toHaveBeenCalled()

    await act(async () => { expect(await reviewRef.current?.commit()).toBe(true) })

    expect(onErrorChange).toHaveBeenLastCalledWith('')
    expect(onApply).toHaveBeenCalledTimes(2)
  })

  it('does not publish an imperative commit error after the reviewed scope changes', async () => {
    let rejectApply!: (reason: Error) => void
    const onApply = vi.fn(() => new Promise<void>((_resolve, reject) => { rejectApply = reject }))
    const onErrorChange = vi.fn()
    const reviewRef = { current: null as SecretBindingReviewHandle | null }
    const renderReview = (generation: string) => root.render(
      <SecretBindingReview ref={reviewRef} reviewGeneration={generation} requirements={requirements} projectId="project-1" onApply={onApply} onErrorChange={onErrorChange} />,
    )
    await act(async () => renderReview('first-review'))
    let commitResult: boolean | undefined
    await act(async () => { void reviewRef.current?.commit().then(result => { commitResult = result }) })
    expect(onApply).toHaveBeenCalledOnce()
    onErrorChange.mockClear()

    await act(async () => renderReview('second-review'))
    await act(async () => rejectApply(new Error('Failure from the previous review')))

    expect(commitResult).toBe(false)
    expect(onErrorChange).not.toHaveBeenCalled()
    expect(container.textContent).not.toContain('Failure from the previous review')
  })

  it('filters preserved and setup groups without changing their binding choices', () => {
    act(() => root.render(<SecretBindingReview requirements={[
      { ...requirements[0], destination_value_preserved: true, value_group_id: 'preserved' },
      requirements[1],
    ]} onApply={vi.fn()} />))
    const state = Array.from(container.querySelectorAll('select')).find(select => select.parentElement?.textContent?.startsWith('Credential state'))!
    act(() => { state.value = 'setup'; state.dispatchEvent(new Event('change', { bubbles: true })) })
    expect(container.querySelectorAll('.secret-binding-review-item')).toHaveLength(1)
    expect(container.querySelector('.secret-binding-review-item')?.textContent).toContain('Refresh')
    expect(container.textContent).toContain('Credential groups (1 of 2)')
    act(() => { state.value = 'preserved'; state.dispatchEvent(new Event('change', { bubbles: true })) })
    expect(container.querySelectorAll('.secret-binding-review-item')).toHaveLength(1)
    expect(container.querySelector('.secret-binding-review-item')?.textContent).toContain('Create Order')
    act(() => { state.value = 'all'; state.dispatchEvent(new Event('change', { bubbles: true })) })
    expect(container.querySelectorAll('.secret-binding-review-item')).toHaveLength(2)
    expect(container.querySelector('button p')).toBeNull()
  })

  it('leads with the groups that need setup and keeps a row in place while its value is typed', () => {
    act(() => root.render(<SecretBindingReview requirements={[
      { ...requirements[0], destination_value_preserved: true, value_group_id: 'preserved' },
      requirements[1],
    ]} onApply={vi.fn()} />))
    expect(container.textContent?.indexOf('binding needs setup')).toBeLessThan(container.textContent?.indexOf('imported value preserved') ?? -1)
    const rows = () => Array.from(container.querySelectorAll('.secret-binding-review-item')).map(row => row.textContent || '')
    expect(rows()[0]).toContain('Refresh')
    expect(rows()[1]).toContain('Create Order')
    const value = container.querySelector<HTMLInputElement>('input[type="password"]')!
    act(() => setInput(value, 'typed-value'))
    expect(rows()[0]).toContain('Refresh')
  })

  it('keeps a row in place while its name is changed to a configured secret', async () => {
    apiMock.getProjectSecretStatuses.mockResolvedValue({
      project_id: 'project-1',
      capabilities: { provider: 'database', writable: true, create_supported: true, replace_supported: true },
      secrets: [{ name: 'CONFIGURED_TOKEN', configured: true, provider: 'database', writable: true }],
    })
    await act(async () => {
      root.render(<SecretBindingReview projectId="project-1" requirements={requirements} onApply={vi.fn()} />)
    })
    await act(async () => Promise.resolve())
    const rows = () => Array.from(container.querySelectorAll('.secret-binding-review-item')).map(row => row.textContent || '')
    expect(rows()[0]).toContain('Create Order')
    const nameInput = Array.from(container.querySelectorAll<HTMLInputElement>('input')).find(input => input.value === 'ORDER_API_TOKEN')!
    act(() => setInput(nameInput, 'CONFIGURED_TOKEN'))
    expect(rows()[0]).toContain('Create Order')
  })

  it('groups only server-proven equal preserved values and keeps their controls collapsed', () => {
    const preserved = requirements.map((requirement, index) => ({ ...requirement, id:`equal-${index}`, affected_paths:[requirement.path], destination_value_preserved:true, value_group_id:'equal' }))
    const unknown = { ...preserved[0], id:'unverified', value_group_id:undefined }
    act(() => root.render(<SecretBindingReview requirements={[...preserved, unknown]} onApply={vi.fn()} />))
    expect(container.querySelectorAll('.secret-binding-review-item')).toHaveLength(2)
    expect(container.textContent).toContain('3 credential requirements')
    expect(container.textContent).toContain('View 2 affected locations')
    expect(container.textContent).not.toContain('affected request files')
    expect(Array.from(container.querySelectorAll<HTMLDetailsElement>('.secret-binding-review-control')).every(item => !item.open)).toBe(true)
    expect(container.querySelector('.secret-binding-review-reuse-all')).toBeNull()
    const search = container.querySelector<HTMLInputElement>('input[type="search"]')!
    act(() => setInput(search, 'does-not-exist'))
    expect(container.textContent).toContain('No credential groups match')
    expect(container.querySelectorAll('.secret-binding-review-item')).toHaveLength(0)
  })

  it('prefills safe references and offers optional write-only values', () => {
    render()

    expect(container.textContent).toContain('Credential groups (2)')
    expect(container.textContent).toContain('configure it later in Project Secrets')
    expect(container.textContent).toContain('/actions/0/headers/Authorization')
    expect(container.textContent).toContain('{{secret.ORDER_API_TOKEN}}')
    const inputs = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="text"]'))
    expect(inputs.map(input => input.value)).toEqual(['ORDER_API_TOKEN', 'REFRESH_TOKEN'])
    expect(inputs.every(input => input.labels?.[0]?.textContent === 'Secret reference name')).toBe(true)
    const values = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="password"]'))
    expect(values).toHaveLength(2)
    expect(values.every(input => input.disabled)).toBe(true)
  })

  it('searches every grouped source location and its script name', () => {
    act(() => root.render(<SecretBindingReview requirements={[
      { ...requirements[0], affected_paths: ['requests/checkout.http', '/actions/42/headers/Authorization'], script_name: 'Customer checkout' },
      requirements[1],
    ]} onApply={vi.fn()} />))
    const search = container.querySelector<HTMLInputElement>('input[type="search"]')!
    for (const query of ['CHECKOUT.HTTP', '/actions/42', 'Customer checkout']) {
      act(() => setInput(search, query))
      expect(container.querySelectorAll('.secret-binding-review-item')).toHaveLength(1)
      expect(container.querySelector('.secret-binding-review-item')?.textContent).toContain('Create Order')
    }
    act(() => setInput(search, 'missing-location'))
    expect(container.querySelectorAll('.secret-binding-review-item')).toHaveLength(0)
  })

  it('keeps an unchanged suggested binding review-only when the importer preserves authored values', async () => {
    const reviewRef = { current: null as SecretBindingReviewHandle | null }
    const onApply = vi.fn().mockResolvedValue(undefined)
    await act(async () => {
      root.render(
        <SecretBindingReview
          ref={reviewRef}
          projectId="project-1"
          requirements={[{ ...requirements[0], destination_value_preserved: true }]}
          onApply={onApply}
        />,
      )
      await Promise.resolve()
    })

    expect(container.querySelector('.secret-binding-review-item .script-health-token')?.textContent)
      .toBe('Preserved in script')
    expect(container.querySelector<HTMLInputElement>('input[id$="-preserve"]')?.checked).toBe(true)
    expect(container.querySelector<HTMLInputElement>('input[type="text"]')?.disabled).toBe(true)
    expect(container.querySelector('.credentials-card-summary')?.textContent)
      .toContain('0 bindings need setup')
    await act(async () => {
      expect(await reviewRef.current?.commit()).toBe(true)
    })
    expect(onApply).not.toHaveBeenCalled()
  })

  it('emits a binding when Use project secret is selected even if the name stays suggested', async () => {
    apiMock.getProjectSecretStatuses.mockImplementation(async (_projectId: string, names: string[]) => ({
      project_id: 'project-1',
      capabilities: {
        provider: 'database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: false,
      },
      secrets: names.map(name => ({
        name,
        configured: name === 'ORDER_API_TOKEN',
        provider: 'database',
        writable: true,
      })),
    }))
    const reviewRef = { current: null as SecretBindingReviewHandle | null }
    const onApply = vi.fn().mockResolvedValue(undefined)
    await act(async () => {
      root.render(
        <SecretBindingReview
          ref={reviewRef}
          projectId="project-1"
          requirements={[{ ...requirements[0], destination_value_preserved: true }]}
          onApply={onApply}
        />,
      )
      await Promise.resolve()
    })
    await act(async () => {
      container.querySelector<HTMLInputElement>('input[id$="-project-secret"]')?.click()
      await Promise.resolve()
    })

    await act(async () => {
      expect(await reviewRef.current?.commit()).toBe(true)
    })
    expect(onApply).toHaveBeenCalledWith([
      { requirement_id: 'authorization-header', secret_name: 'ORDER_API_TOKEN' },
    ])
  })

  it('keeps preserved and unresolved occurrences in independent destination groups', async () => {
    const reviewRef = { current: null as SecretBindingReviewHandle | null }
    const onApply = vi.fn().mockResolvedValue(undefined)
    await act(async () => {
      root.render(
        <SecretBindingReview
          ref={reviewRef}
          requirements={[
            { ...requirements[0], destination_value_preserved: true },
            {
              ...requirements[0],
              id: 'authorization-header-second',
              path: '/actions/1/headers/Authorization',
            },
          ]}
          onApply={onApply}
        />,
      )
      await Promise.resolve()
    })

    const items = Array.from(container.querySelectorAll<HTMLElement>('.secret-binding-review-item')).sort((a, b) => Number(b.textContent?.includes('Preserved in script')) - Number(a.textContent?.includes('Preserved in script')))
    expect(items).toHaveLength(2)
    expect(items[0].querySelector<HTMLInputElement>('input[id$="-preserve"]')?.checked).toBe(true)
    expect(items[0].querySelector<HTMLInputElement>('input[id$="-preserve"]')?.disabled).toBe(false)
    expect(items[1].querySelector<HTMLInputElement>('input[id$="-project-secret"]')?.checked).toBe(true)
    expect(items[1].querySelector<HTMLInputElement>('input[id$="-preserve"]')?.disabled).toBe(true)
    expect(container.querySelector('.credentials-card-summary')?.textContent).toContain('1 imported value preserved')
    expect(container.textContent).toContain('server could not verify an exact imported value at every generated destination')
    await act(async () => {
      expect(await reviewRef.current?.commit()).toBe(true)
    })
    expect(onApply).toHaveBeenCalledWith([{
      requirement_id: 'authorization-header-second',
      secret_name: 'ORDER_API_TOKEN_2',
    }])
  })

  // The server cannot swap these occurrences for a project-secret reference
  // without changing other recorded bytes in the same field, and the binding
  // route refuses them. The review must never offer, default to, or send that
  // choice.
  const notRebindable = {
    rebind_unavailable_reason: 'destination_bytes_unaligned' as const,
    rebind_unavailable_description: 'APEX can’t swap this credential for a project-secret reference without changing other recorded bytes in the same field.',
  }

  it('keeps a credential a project secret cannot replace as imported and never binds it', async () => {
    const reviewRef = { current: null as SecretBindingReviewHandle | null }
    const onApply = vi.fn().mockResolvedValue(undefined)
    const onPreparedChange = vi.fn()
    await act(async () => {
      root.render(
        <SecretBindingReview
          ref={reviewRef}
          requirements={[{ ...requirements[0], destination_value_preserved: true, ...notRebindable }]}
          onApply={onApply}
          onPreparedChange={onPreparedChange}
        />,
      )
      await Promise.resolve()
    })

    const preserve = container.querySelector<HTMLInputElement>('input[id$="-preserve"]')
    const projectSecret = container.querySelector<HTMLInputElement>('input[id$="-project-secret"]')
    expect(preserve?.checked).toBe(true)
    expect(preserve?.disabled).toBe(false)
    expect(preserve?.parentElement?.textContent).toContain('Preserve imported value')
    expect(projectSecret?.disabled).toBe(true)
    const help = document.getElementById(projectSecret!.getAttribute('aria-describedby')!)
    expect(help?.textContent).toContain('without changing other recorded bytes')
    expect(container.querySelector('input[aria-label^="Secret value"]')).toBeNull()
    await act(async () => {
      projectSecret?.click()
      await Promise.resolve()
    })
    expect(preserve?.checked).toBe(true)
    expect(onPreparedChange).toHaveBeenLastCalledWith(expect.objectContaining({ valid: true, configureLaterCount: 0, selectedBindingCount: 0 }))
    await act(async () => {
      expect(await reviewRef.current?.commit()).toBe(true)
    })
    expect(onApply).not.toHaveBeenCalled()
  })

  it('keeps a credential as recorded when it can be neither preserved nor replaced', async () => {
    const reviewRef = { current: null as SecretBindingReviewHandle | null }
    const onApply = vi.fn().mockResolvedValue(undefined)
    await act(async () => {
      root.render(
        <SecretBindingReview
          ref={reviewRef}
          requirements={[{ ...requirements[0], preservation_unavailable_reason: 'source_value_unavailable', ...notRebindable }]}
          onApply={onApply}
        />,
      )
      await Promise.resolve()
    })

    const item = container.querySelector<HTMLElement>('.secret-binding-review-item')
    expect(item?.querySelector('.script-health-token')?.textContent).toBe('Kept as recorded')
    const keep = container.querySelector<HTMLInputElement>('input[id$="-preserve"]')
    expect(keep?.checked).toBe(true)
    expect(keep?.parentElement?.textContent).toContain('Keep recorded value')
    expect(container.querySelector<HTMLInputElement>('input[id$="-project-secret"]')?.disabled).toBe(true)
    expect(item?.textContent).toContain('Saving keeps the field as recorded')
    expect(item?.textContent).not.toContain('placeholder')
    const summary = container.querySelector('.credentials-card-summary')?.textContent
    expect(summary).toContain('0 imported values preserved')
    expect(summary).toContain('1 kept as recorded, to edit before running')
    // It still needs an edit, so the attention filter keeps it.
    const attention = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
      .find(input => input.parentElement?.textContent?.includes('Show bindings needing review'))
    await act(async () => {
      attention?.click()
      await Promise.resolve()
    })
    expect(container.querySelectorAll('.secret-binding-review-item')).toHaveLength(1)
    // The preservation reason's advice ("use a project secret") would point at
    // a choice the server refuses; only the combined explanation is shown.
    expect(item?.textContent).not.toContain('source did not provide one exact recoverable value')
    await act(async () => {
      expect(await reviewRef.current?.commit()).toBe(true)
    })
    expect(onApply).not.toHaveBeenCalled()
  })

  it('binds a replaceable credential without the same-named one a project secret cannot replace', async () => {
    const reviewRef = { current: null as SecretBindingReviewHandle | null }
    const onApply = vi.fn().mockResolvedValue(undefined)
    await act(async () => {
      root.render(
        <SecretBindingReview
          ref={reviewRef}
          requirements={[
            requirements[0],
            { ...requirements[0], id: 'authorization-header-second', path: '/actions/1/headers/Authorization', ...notRebindable },
          ]}
          onApply={onApply}
        />,
      )
      await Promise.resolve()
    })

    expect(container.querySelectorAll('.secret-binding-review-item')).toHaveLength(2)
    await act(async () => {
      expect(await reviewRef.current?.commit()).toBe(true)
    })
    expect(onApply).toHaveBeenCalledWith([{ requirement_id: 'authorization-header', secret_name: 'ORDER_API_TOKEN' }])
  })

  it.each([
    {
      reason: 'source_value_unavailable' as const,
      message: 'source did not provide one exact recoverable value',
    },
    {
      reason: 'existing_secret_reference' as const,
      message: 'source already uses a governed secret reference',
    },
    {
      reason: 'destination_mapping_incomplete' as const,
      message: 'could not prove it can restore every generated destination occurrence',
    },
  ])('explains why preservation is unavailable for $reason', ({ reason, message }) => {
    act(() => {
      root.render(
        <SecretBindingReview
          requirements={[{ ...requirements[0], preservation_unavailable_reason: reason }]}
          onApply={vi.fn()}
        />,
      )
    })

    const preserve = container.querySelector<HTMLInputElement>('input[id$="-preserve"]')
    expect(preserve?.disabled).toBe(true)
    expect(container.textContent).toContain(message)
    expect(preserve?.getAttribute('aria-describedby')).toBeTruthy()
    expect(document.getElementById(preserve!.getAttribute('aria-describedby')!)).not.toBeNull()
  })

  it('lists each distinct reason when one destination group has mixed preservation failures', () => {
    act(() => {
      root.render(
        <SecretBindingReview
          requirements={[
            { ...requirements[0], preservation_unavailable_reason: 'source_value_unavailable' },
            {
              ...requirements[0],
              id: 'authorization-header-second',
              path: '/actions/1/headers/Authorization',
              preservation_unavailable_reason: 'destination_mapping_incomplete',
            },
          ]}
          onApply={vi.fn()}
        />,
      )
    })

    expect(container.querySelectorAll('.secret-binding-review-item')).toHaveLength(1)
    expect(container.querySelectorAll('.secret-binding-review-preservation-reasons small')).toHaveLength(2)
    expect(container.textContent).toContain('source did not provide one exact recoverable value')
    expect(container.textContent).toContain('could not prove it can restore every generated destination occurrence')
  })

  it('explains an unresolved source variable and summarizes every affected request file', () => {
    act(() => {
      root.render(
        <SecretBindingReview
          requirements={[{
            id: 'partner-username',
            path: 'CTA/first.yml',
            label: 'Partner username',
            context: 'XML username element <mh:userName>',
            suggested_name: 'PARTNER_USERNAME',
            replacement: '{{secret.PARTNER_USERNAME}}',
            source_variable: 'partnerUserName',
            unresolved_source_variable: true,
            affected_paths: ['CTA/first.yml', 'CTA/second.yml', 'CTA/third.yml'],
            source_value_available: false,
            destination_value_preserved: false,
            preservation_unavailable_reason: 'source_value_unavailable',
          }]}
          onApply={vi.fn()}
        />,
      )
    })

    expect(container.querySelector('.secret-binding-review-label-row strong')?.textContent).toBe('Partner username')
    expect(container.querySelector('.secret-binding-review-location > p')?.textContent)
      .toBe('XML username element <mh:userName>')
    expect(container.querySelector('.secret-binding-review-source code')?.textContent).toBe('{{partnerUserName}}')
    expect(container.querySelector('.secret-binding-review-source small')?.textContent)
      .toContain('APEX mapped this authored source variable to a governed project-secret reference')
    expect(container.querySelector('.secret-binding-review-source small')?.textContent)
      .not.toContain('does not include its value')
    expect(container.querySelector('.script-health-token')?.textContent).toBe('Value needed before run')
    expect(container.querySelector('.secret-binding-review-reuse-badge')?.textContent).toBe('3 locations')
    expect(container.querySelector('.credentials-location-details summary')?.textContent)
      .toBe('View 3 affected request files')
    expect(container.querySelector('.credentials-location-scroll-region')).toBeNull()
    expect(container.querySelectorAll('.credentials-location-list li')).toHaveLength(0)
    const details = container.querySelector<HTMLDetailsElement>('.credentials-location-details')!
    act(() => toggleDetails(details, true))
    const affectedPathRegion = container.querySelector<HTMLElement>('.credentials-location-scroll-region')
    expect(affectedPathRegion?.getAttribute('role')).toBe('region')
    expect(affectedPathRegion?.getAttribute('aria-labelledby')).toBe(
      container.querySelector('.credentials-location-details summary')?.id,
    )
    expect(affectedPathRegion?.getAttribute('aria-describedby')).toBe(
      container.querySelector('.credentials-location-page-status')?.id,
    )
    expect(affectedPathRegion?.tabIndex).toBe(0)
    expect(container.querySelector('.credentials-location-page-status')?.textContent)
      .toBe('Showing 1–3 of 3 affected request files. Page 1 of 1.')
    expect(Array.from(container.querySelectorAll('.credentials-location-list code')).map(node => node.textContent))
      .toEqual(['CTA/first.yml', 'CTA/second.yml', 'CTA/third.yml'])
    expect(container.querySelector('.secret-binding-review-preservation-reasons')?.textContent)
      .toContain('No value for {{partnerUserName}} was included in the imported collection')
    expect(container.textContent?.match(/No value for \{\{partnerUserName\}\}/g)).toHaveLength(1)
  })

  it('lazily pages 10,000 affected paths while keeping the mounted DOM bounded', () => {
    const affectedPaths = Array.from(
      { length: 10_000 },
      (_, index) => `CTA/request-${String(index).padStart(5, '0')}.yml`,
    )
    act(() => {
      root.render(
        <SecretBindingReview
          requirements={[{
            ...requirements[0],
            affected_paths: affectedPaths,
          }]}
          onApply={vi.fn()}
        />,
      )
    })

    const details = container.querySelector<HTMLDetailsElement>('.credentials-location-details')!
    const summary = details.querySelector<HTMLElement>('summary')!
    expect(summary.textContent).toBe('View 10000 affected request files')
    expect(details.open).toBe(false)
    expect(container.querySelector('.credentials-location-scroll-region')).toBeNull()
    expect(container.querySelectorAll('.credentials-location-list li')).toHaveLength(0)

    act(() => toggleDetails(details, true))
    const firstPageNodes = Array.from(container.querySelectorAll<HTMLLIElement>('.credentials-location-list li'))
    expect(firstPageNodes).toHaveLength(100)
    expect(firstPageNodes[0].textContent).toBe('CTA/request-00000.yml')
    expect(firstPageNodes[99].textContent).toBe('CTA/request-00099.yml')
    expect(container.querySelector('.credentials-location-page-status')?.textContent)
      .toBe('Showing 1–100 of 10000 affected request files. Page 1 of 100.')
    const region = container.querySelector<HTMLElement>('.credentials-location-scroll-region')!
    expect(region.getAttribute('role')).toBe('region')
    expect(region.tabIndex).toBe(0)
    expect(region.getAttribute('aria-labelledby')).toBe(summary.id)
    expect(region.querySelector('ul')?.getAttribute('aria-label'))
      .toBe('Affected request files, items 1 through 100 of 10000')
    const previousButton = Array.from(region.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Previous'))!
    const nextButton = Array.from(region.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Next'))!
    expect(region.querySelector('.credentials-location-pagination')?.getAttribute('role')).toBe('group')
    expect(region.querySelector('.credentials-location-pagination')?.getAttribute('aria-label'))
      .toBe('Affected request files pages')
    expect(previousButton.disabled).toBe(true)
    expect(nextButton.disabled).toBe(false)

    act(() => nextButton.click())
    const secondPageNodes = Array.from(container.querySelectorAll<HTMLLIElement>('.credentials-location-list li'))
    expect(secondPageNodes).toHaveLength(100)
    expect(secondPageNodes[0].textContent).toBe('CTA/request-00100.yml')
    expect(secondPageNodes[99].textContent).toBe('CTA/request-00199.yml')
    expect(container.querySelector('.credentials-location-page-status')?.textContent)
      .toBe('Showing 101–200 of 10000 affected request files. Page 2 of 100.')

    const firstVisibleNode = secondPageNodes[0]
    act(() => setInput(container.querySelector<HTMLInputElement>('input[type="text"]')!, 'RENAMED_TOKEN'))
    expect(container.querySelectorAll('.credentials-location-list li')).toHaveLength(100)
    expect(container.querySelector('.credentials-location-list li')).toBe(firstVisibleNode)

    act(() => toggleDetails(details, false))
    expect(container.querySelector('.credentials-location-scroll-region')).toBeNull()
    expect(container.querySelectorAll('.credentials-location-list li')).toHaveLength(0)
    act(() => toggleDetails(details, true))
    expect(container.querySelector('.credentials-location-list li')?.textContent).toBe('CTA/request-00000.yml')
  })

  it('groups ten thousand duplicate suggestions in linear suffix order, batches probes, and renders one page', async () => {
    const manyRequirements = Array.from({ length: 10_000 }, (_, index): SecretBindingRequirement => {
      const suffix = String(index).padStart(5, '0')
      return {
        id: `credential-${suffix}`,
        path: `CTA/request-${suffix}.yml`,
        label: `Credential ${suffix}`,
        context: 'Sensitive request header',
        suggested_name: 'SHARED_TOKEN',
        replacement: '{{secret.SHARED_TOKEN}}',
        script_index: index,
        script_name: `Generated script ${suffix}`,
      }
    })
    apiMock.getProjectSecretStatuses.mockImplementation(async (_projectId: string, names: string[]) => {
      if (names.length > 256) throw new Error('status batch exceeded server limit')
      return {
        project_id: 'project-1',
        capabilities: {
          provider: 'database',
          writable: true,
          create_supported: true,
          replace_supported: true,
          source_secret_migration_policy_enabled: false,
        },
        secrets: names.map(name => ({ name, configured: false, provider: 'database', writable: true })),
      }
    })

    await act(async () => {
      root.render(
        <SecretBindingReview
          projectId="project-1"
          requirements={manyRequirements}
          onApply={vi.fn()}
        />,
      )
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })

    const probeCalls = apiMock.getProjectSecretStatuses.mock.calls
    expect(probeCalls).toHaveLength(Math.ceil(manyRequirements.length / 256))
    expect(probeCalls.every(([, names]) => names.length > 0 && names.length <= 256)).toBe(true)
    expect(new Set(probeCalls.flatMap(([, names]) => names))).toHaveLength(manyRequirements.length)
    expect(container.querySelectorAll('.secret-binding-review-item')).toHaveLength(100)
    expect(container.querySelectorAll<HTMLInputElement>('input[type="text"]')).toHaveLength(100)
    expect(container.querySelector<HTMLInputElement>('input[type="text"]')?.value).toBe('SHARED_TOKEN')
    const pagination = container.querySelector<HTMLElement>('.secret-binding-review-pagination')!
    expect(pagination.getAttribute('aria-label')).toBe('Credential review pages')
    expect(pagination.textContent).toContain('Showing credential groups 1–100 of 10000. Page 1 of 100.')
    expect(pagination.querySelector('[role="group"]')?.getAttribute('aria-label')).toBe('Credential page controls')

    const nextButton = Array.from(pagination.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Next'))!
    act(() => nextButton.click())
    expect(container.querySelectorAll('.secret-binding-review-item')).toHaveLength(100)
    expect(container.querySelector<HTMLInputElement>('input[type="text"]')?.value).toBe('SHARED_TOKEN_101')
    expect(pagination.textContent).toContain('Showing credential groups 101–200 of 10000. Page 2 of 100.')
    // This large fixture verifies bounded batching and rendering, independent
    // of scheduling delays while the full dashboard suite is running.
  }, 30_000)

  it('rechecks more than 256 credential names in bounded batches before commit', async () => {
    const batchedRequirements = Array.from({ length: 300 }, (_, index): SecretBindingRequirement => ({
      id: `credential-${index}`,
      path: `CTA/request-${index}.yml`,
      label: `Credential ${index}`,
      context: 'Sensitive request header',
      suggested_name: `TOKEN_${index}`,
      replacement: `{{secret.TOKEN_${index}}}`,
    }))
    apiMock.getProjectSecretStatuses.mockImplementation(async (_projectId: string, names: string[]) => {
      if (names.length > 256) throw new Error('status batch exceeded server limit')
      return {
        project_id: 'project-1',
        capabilities: {
          provider: 'database',
          writable: true,
          create_supported: true,
          replace_supported: true,
          source_secret_migration_policy_enabled: false,
        },
        secrets: names.map(name => ({ name, configured: false, provider: 'database', writable: true })),
      }
    })
    const reviewRef = { current: null as SecretBindingReviewHandle | null }
    const onApply = vi.fn().mockResolvedValue(undefined)
    await act(async () => {
      root.render(
        <SecretBindingReview
          ref={reviewRef}
          projectId="project-1"
          requirements={batchedRequirements}
          onApply={onApply}
        />,
      )
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })
    apiMock.getProjectSecretStatuses.mockClear()

    await act(async () => {
      expect(await reviewRef.current?.commit()).toBe(true)
    })

    expect(apiMock.getProjectSecretStatuses).toHaveBeenCalledTimes(2)
    expect(apiMock.getProjectSecretStatuses.mock.calls.every(([, names]) => names.length <= 256)).toBe(true)
    expect(onApply).toHaveBeenCalledTimes(1)
    expect(onApply.mock.calls[0][0]).toHaveLength(300)
  })

  it('writes more than 256 manually entered values in bounded create batches', async () => {
    const writes = Array.from({ length: 513 }, (_, index) => ({
      name: `MANUAL_TOKEN_${index}`,
      value: `write-only-${index}`,
    }))
    const createSecrets = vi.fn(async (_projectId: string, secrets: Array<{ name: string; value: string }>) => {
      if (secrets.length > 256) throw new Error('write batch exceeded server limit')
      return { project_id: 'project-1', secrets: [], count: secrets.length }
    })
    const committedNames: string[] = []

    expect(await createProjectSecretsInBatches(
      createSecrets,
      'project-1',
      writes,
      names => committedNames.push(...names),
    )).toBe(true)

    expect(createSecrets).toHaveBeenCalledTimes(3)
    expect(createSecrets.mock.calls.map(([, secrets]) => secrets.length)).toEqual([256, 256, 1])
    expect(createSecrets.mock.calls.flatMap(([, secrets]) => secrets)).toEqual(writes)
    expect(committedNames).toEqual(writes.map(write => write.name))
  })

  it('checkpoints only server-confirmed names from a partial secret write', async () => {
    const writes = [
      { name: 'TOKEN_A', value: 'write-only-a' },
      { name: 'TOKEN_B', value: 'write-only-b' },
      { name: 'TOKEN_C', value: 'write-only-c' },
    ]
    const partial = new ApiError('provider write failed', 502, {
      partial: true,
      created: [
        { name: 'TOKEN_A' },
        { name: 'TOKEN_A' },
        { name: 'NOT_IN_THIS_BATCH' },
        { malformed: true },
      ],
    })
    const committedNames: string[] = []

    await expect(createProjectSecretsInBatches(
      vi.fn().mockRejectedValueOnce(partial),
      'project-1',
      writes,
      names => committedNames.push(...names),
    )).rejects.toBe(partial)

    expect(committedNames).toEqual(['TOKEN_A'])
  })

  it('fails closed when batched status responses disagree about store capabilities', async () => {
    const requirementsAcrossBatches = Array.from({ length: 300 }, (_, index): SecretBindingRequirement => ({
      id: `capability-${index}`,
      path: `/actions/${index}/headers/Authorization`,
      label: `Credential ${index}`,
      context: 'Sensitive request header',
      suggested_name: `CAPABILITY_TOKEN_${index}`,
      replacement: `{{secret.CAPABILITY_TOKEN_${index}}}`,
    }))
    let responseIndex = 0
    apiMock.getProjectSecretStatuses.mockImplementation(async (_projectId: string, names: string[]) => {
      const index = responseIndex
      responseIndex += 1
      return {
        project_id: 'project-1',
        capabilities: {
          provider: index === 0 ? 'database' : 'key-vault',
          writable: true,
          create_supported: true,
          replace_supported: true,
          source_secret_migration_policy_enabled: false,
        },
        secrets: names.map(name => ({ name, configured: false })),
      }
    })

    await act(async () => {
      root.render(
        <SecretBindingReview
          projectId="project-1"
          requirements={requirementsAcrossBatches}
          onApply={vi.fn()}
        />,
      )
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.querySelector('.credentials-card-summary')?.textContent).toContain('read-only')
    expect(Array.from(container.querySelectorAll<HTMLInputElement>('input[type="password"]'))
      .every(input => input.disabled)).toBe(true)
    expect(apiMock.createProjectSecrets).not.toHaveBeenCalled()
  })

  it('recognizes that an unresolved imported variable already has a configured project secret', async () => {
    apiMock.getProjectSecretStatuses.mockImplementation(async (_projectId: string, names: string[]) => ({
      project_id: 'project-1',
      capabilities: {
        provider: 'database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: false,
      },
      secrets: names.map(name => ({ name, configured: true, provider: 'database', writable: true })),
    }))
    await act(async () => {
      root.render(
        <SecretBindingReview
          projectId="project-1"
          requirements={[{
            id: 'partner-username',
            path: 'CTA/request.yml',
            label: 'Partner username',
            context: 'XML username element <mh:userName>',
            suggested_name: 'PARTNER_USERNAME',
            replacement: '{{secret.PARTNER_USERNAME}}',
            source_variable: 'partnerUserName',
            unresolved_source_variable: true,
            source_value_available: false,
            destination_value_preserved: false,
            preservation_unavailable_reason: 'source_value_unavailable',
          }]}
          onApply={vi.fn()}
        />,
      )
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.querySelector('.script-health-token')?.textContent).toBe('Configured')
    const sourceGuidance = container.querySelector('.secret-binding-review-source small')?.textContent || ''
    const preservationGuidance = container.querySelector('.secret-binding-review-preservation-reasons')?.textContent || ''
    expect(sourceGuidance).toContain('APEX mapped this authored source variable to a governed project-secret reference')
    expect(sourceGuidance).not.toContain('selected project secret is already configured')
    expect(preservationGuidance).toContain('selected project secret is already configured')
    expect(sourceGuidance).not.toContain('Enter the value now')
    expect(preservationGuidance).not.toContain('configure it in Project Secrets')
    expect(container.textContent?.match(/selected project secret is already configured/g)).toHaveLength(1)
  })

  it('recognizes that a value for an unresolved imported variable will be created during import', async () => {
    await act(async () => {
      root.render(
        <SecretBindingReview
          projectId="project-1"
          requirements={[{
            id: 'partner-password',
            path: 'CTA/request.yml',
            label: 'Partner password',
            context: 'XML password element <mh:userPassword>',
            suggested_name: 'PARTNER_PASSWORD',
            replacement: '{{secret.PARTNER_PASSWORD}}',
            source_variable: 'partnerPassword',
            unresolved_source_variable: true,
            source_value_available: false,
            destination_value_preserved: false,
            preservation_unavailable_reason: 'source_value_unavailable',
          }]}
          onApply={vi.fn()}
        />,
      )
      await Promise.resolve()
      await Promise.resolve()
    })
    const valueInput = container.querySelector<HTMLInputElement>('input[type="password"]')!
    expect(valueInput.disabled).toBe(false)
    act(() => setInput(valueInput, 'write-only-value'))

    expect(container.querySelector('.script-health-token')?.textContent).toBe('Will be created')
    const sourceGuidance = container.querySelector('.secret-binding-review-source small')?.textContent || ''
    const preservationGuidance = container.querySelector('.secret-binding-review-preservation-reasons')?.textContent || ''
    expect(sourceGuidance).toContain('APEX mapped this authored source variable to a governed project-secret reference')
    expect(sourceGuidance).not.toContain('will be stored in Project Secrets during import')
    expect(preservationGuidance).toContain('will be stored in Project Secrets during import')
    expect(sourceGuidance).not.toContain('configure it in Project Secrets')
    expect(preservationGuidance).not.toContain('configure it in Project Secrets')
    expect(container.textContent?.match(/will be stored in Project Secrets during import/g)).toHaveLength(1)
  })

  it('fails closed when refreshed server evidence withdraws destination preservation', () => {
    act(() => {
      root.render(
        <SecretBindingReview
          requirements={[{ ...requirements[0], destination_value_preserved: true }]}
          onApply={vi.fn()}
        />,
      )
    })
    expect(container.querySelector<HTMLInputElement>('input[id$="-preserve"]')?.checked).toBe(true)

    act(() => {
      root.render(
        <SecretBindingReview
          requirements={[{ ...requirements[0], destination_value_preserved: false }]}
          onApply={vi.fn()}
        />,
      )
    })

    expect(container.querySelector<HTMLInputElement>('input[id$="-project-secret"]')?.checked).toBe(true)
    expect(container.querySelector<HTMLInputElement>('input[id$="-preserve"]')?.disabled).toBe(true)
    expect(container.querySelector<HTMLInputElement>('input[type="text"]')?.disabled).toBe(false)
  })

  it('emits exact requirement-to-name selections and permits explicit reuse', async () => {
    const onApply = render(vi.fn().mockResolvedValue(undefined))
    const inputs = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="text"]'))

    act(() => setInput(inputs[1], 'ORDER_API_TOKEN'))

    const applyButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Continue with credentials'))
    await act(async () => applyButton!.click())

    expect(onApply).toHaveBeenCalledWith([
      { requirement_id: 'authorization-header', secret_name: 'ORDER_API_TOKEN' },
      { requirement_id: 'refresh-token', secret_name: 'ORDER_API_TOKEN' },
    ])
  })

  it('blocks apply and identifies an invalid binding name', () => {
    render()
    const input = container.querySelector<HTMLInputElement>('input[type="text"]')!

    act(() => setInput(input, 'INVALID/NAME'))

    expect(input.getAttribute('aria-invalid')).toBe('true')
    expect(container.textContent).toContain('Use 1–249 uppercase letters')
    const applyButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Continue with credentials'))
    expect(applyButton?.disabled).toBe(true)
  })

  it('canonicalizes compatible dotted and hyphenated aliases before apply', () => {
    render()
    const input = container.querySelector<HTMLInputElement>('input[type="text"]')!

    act(() => setInput(input, 'orders.api-token'))

    expect(input.value).toBe('ORDERS_API_TOKEN')
    expect(input.getAttribute('aria-invalid')).toBe('false')
    expect(container.textContent).toContain('{{secret.ORDERS_API_TOKEN}}')
  })

  it('rejects names that cannot fit in a portable secret template', () => {
    render()
    const input = container.querySelector<HTMLInputElement>('input[type="text"]')!

    act(() => setInput(input, 'A'.repeat(250)))

    expect(input.getAttribute('aria-invalid')).toBe('true')
    expect(container.textContent).toContain('Use 1–249 uppercase letters')
  })

  it('announces apply failures without exposing any submitted value', async () => {
    render(vi.fn().mockRejectedValue(new Error('Binding service unavailable')))
    const applyButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Continue with credentials'))

    await act(async () => applyButton!.click())

    const alert = container.querySelector('[role="alert"]')
    expect(alert?.textContent).toContain('Binding service unavailable')
  })

  it('applies deferred references when the read-only status probe is unavailable', async () => {
    apiMock.getProjectSecretStatuses.mockRejectedValue(new Error('status probe unavailable'))
    const onApply = vi.fn().mockResolvedValue(undefined)
    await act(async () => {
      root.render(
        <SecretBindingReview
          projectId="project-1"
          requirements={[requirements[0]]}
          onApply={onApply}
        />,
      )
      await Promise.resolve()
    })

    const applyButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Continue with credentials'))
    await act(async () => applyButton?.click())

    expect(apiMock.createProjectSecrets).not.toHaveBeenCalled()
    expect(apiMock.migrateImportSecrets).not.toHaveBeenCalled()
    expect(onApply).toHaveBeenCalledWith([
      { requirement_id: 'authorization-header', secret_name: 'ORDER_API_TOKEN' },
    ])
    expect(container.textContent).toContain('Credential status unavailable')
    expect(container.textContent).toContain('Retry credential check')
  })

  it('distinguishes a transient credential probe failure from a read-only provider and retries it', async () => {
    apiMock.getProjectSecretStatuses
      .mockRejectedValueOnce(new Error('temporary status outage'))
      .mockResolvedValueOnce({
        project_id: 'project-1',
        capabilities: {
          provider: 'encrypted_database',
          writable: true,
          create_supported: true,
          replace_supported: true,
          source_secret_migration_policy_enabled: false,
        },
        secrets: [],
      })
    await act(async () => {
      root.render(
        <SecretBindingReview
          projectId="project-1"
          requirements={[requirements[0]]}
          onApply={vi.fn()}
        />,
      )
      await Promise.resolve()
    })

    const valueInput = container.querySelector<HTMLInputElement>('input[type="password"]')!
    expect(container.textContent).toContain('Credential status unavailable')
    expect(container.textContent).not.toContain('project secret store · read-only')
    expect(valueInput.disabled).toBe(true)

    const retry = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Retry credential check')
    await act(async () => {
      retry?.click()
      await Promise.resolve()
    })

    expect(apiMock.getProjectSecretStatuses).toHaveBeenCalledTimes(2)
    expect(container.textContent).toContain('Encrypted project store')
    expect(container.textContent).not.toContain('Credential status unavailable')
    expect(valueInput.disabled).toBe(false)
  })

  it('fails closed and locks credential controls when a write cannot be conflict-checked', async () => {
    const onApply = vi.fn().mockResolvedValue(undefined)
    await act(async () => {
      root.render(
        <SecretBindingReview
          projectId="project-1"
          requirements={[requirements[0]]}
          onApply={onApply}
        />,
      )
      await Promise.resolve()
    })
    const valueInput = container.querySelector<HTMLInputElement>('input[type="password"]')!
    act(() => setInput(valueInput, 'write-only-value'))

    let rejectProbe: ((cause: Error) => void) | undefined
    apiMock.getProjectSecretStatuses.mockReturnValueOnce(new Promise((_, reject) => {
      rejectProbe = reject
    }))
    const applyButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Continue with credentials'))
    act(() => applyButton?.click())

    expect(valueInput.disabled).toBe(true)
    expect(container.querySelector<HTMLInputElement>('input[type="text"]')?.disabled).toBe(true)
    await act(async () => rejectProbe?.(new Error('status probe unavailable')))

    expect(apiMock.createProjectSecrets).not.toHaveBeenCalled()
    expect(onApply).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Unable to verify the project secret store')
  })

  it('fails closed when source migration cannot be conflict-checked', async () => {
    const onApply = vi.fn().mockResolvedValue(undefined)
    await act(async () => {
      root.render(
        <SecretBindingReview
          projectId="project-1"
          requirements={[{ ...requirements[0], source_value_available: true }]}
          sourceSecretSessionId="source-session-1"
          sourceMigrationAllowed
          onApply={onApply}
        />,
      )
      await Promise.resolve()
    })
    const migrationToggle = container.querySelector<HTMLInputElement>('.credentials-source-migration input')!
    act(() => migrationToggle.click())
    apiMock.getProjectSecretStatuses.mockRejectedValueOnce(new Error('status probe unavailable'))

    const applyButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Continue with credentials'))
    await act(async () => applyButton?.click())

    expect(apiMock.migrateImportSecrets).not.toHaveBeenCalled()
    expect(onApply).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Unable to verify the project secret store')
  })

  it('expires protected source values without blocking deferred reference binding', async () => {
    const onApply = vi.fn().mockResolvedValue(undefined)
    await act(async () => {
      root.render(
        <SecretBindingReview
          projectId="project-1"
          requirements={[{ ...requirements[0], source_value_available: true }]}
          sourceSecretSessionId="expired-session"
          sourceSecretSessionExpiresAt="2026-01-01T00:00:00Z"
          sourceMigrationAllowed
          onApply={onApply}
        />,
      )
      await Promise.resolve()
    })

    const migrationToggle = container.querySelector<HTMLInputElement>('.credentials-source-migration input')!
    const rowStatus = container.querySelector('.secret-binding-review-item .script-health-token')
    expect(migrationToggle.checked).toBe(false)
    expect(migrationToggle.disabled).toBe(true)
    expect(rowStatus?.textContent).toBe('Configure later')
    expect(container.textContent).toContain('Optional source-value migration into Project Secrets has expired')
    expect(container.textContent).toContain('preserved imported destination values remain runnable')

    const applyButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Continue with credentials'))
    await act(async () => applyButton?.click())

    expect(apiMock.migrateImportSecrets).not.toHaveBeenCalled()
    expect(onApply).toHaveBeenCalledWith([
      { requirement_id: 'authorization-header', secret_name: 'ORDER_API_TOKEN' },
    ])
  })

  it('consumes partial migration failure metadata and requires remaining names to be configured manually', async () => {
    apiMock.migrateImportSecrets.mockRejectedValueOnce(new ApiError(
      'provider write failed',
      502,
      {
        partial: true,
        source_secret_session_consumed: true,
        manual_configuration_required: true,
        remaining_names: ['SECOND_TOKEN', 'NOT/A/SECRET'],
        migrated: [{ name: 'FIRST_TOKEN', provider: 'database' }],
      },
    ))
    const onApply = vi.fn().mockResolvedValue(undefined)
    await act(async () => {
      root.render(
        <SecretBindingReview
          projectId="project-1"
          requirements={[{ ...requirements[0], source_value_available: true }]}
          sourceSecretSessionId="single-use-session"
          sourceSecretSessionExpiresAt="2099-01-01T00:00:00Z"
          sourceMigrationAllowed
          onApply={onApply}
        />,
      )
      await Promise.resolve()
    })
    const migrationToggle = container.querySelector<HTMLInputElement>('.credentials-source-migration input')!
    act(() => migrationToggle.click())

    const applyButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Continue with credentials'))
    await act(async () => applyButton?.click())

    expect(apiMock.migrateImportSecrets).toHaveBeenCalledTimes(1)
    expect(apiMock.discardImportSecretSession).not.toHaveBeenCalled()
    expect(onApply).not.toHaveBeenCalled()
    expect(migrationToggle.checked).toBe(false)
    expect(migrationToggle.disabled).toBe(true)
    expect(container.textContent).toContain('Remaining credentials: SECOND_TOKEN')
    expect(container.textContent).not.toContain('NOT/A/SECRET')
    expect(container.textContent).not.toContain('FIRST_TOKEN')
    expect(container.querySelector('.secret-binding-review-item .script-health-token')?.textContent).toBe('Configure later')

    await act(async () => applyButton?.click())
    expect(apiMock.migrateImportSecrets).toHaveBeenCalledTimes(1)
    expect(onApply).toHaveBeenCalledTimes(1)
  })

  it('discards a source session when migration fails without confirmed consumption', async () => {
    apiMock.migrateImportSecrets.mockRejectedValueOnce(new Error('network interrupted'))
    const reviewRef = { current: null as SecretBindingReviewHandle | null }
    await act(async () => {
      root.render(
        <SecretBindingReview
          ref={reviewRef}
          projectId="project-1"
          requirements={[{ ...requirements[0], source_value_available: true }]}
          sourceSecretSessionId="uncertain-session"
          sourceMigrationAllowed
          onApply={vi.fn()}
        />,
      )
      await Promise.resolve()
    })
    act(() => container.querySelector<HTMLInputElement>('.credentials-source-migration input')?.click())

    await act(async () => { await reviewRef.current?.commit() })

    expect(apiMock.discardImportSecretSession).toHaveBeenCalledWith({
      project_id: 'project-1',
      source_secret_session_id: 'uncertain-session',
    })
    expect(container.textContent).toContain('Detected credential migration could not be confirmed')
  })

  it('keeps a provider-confirmed retained source session available for retry', async () => {
    apiMock.migrateImportSecrets
      .mockRejectedValueOnce(new ApiError(
        'provider write failed',
        502,
        {
          partial: true,
          session_retained: true,
          source_secret_session_consumed: false,
          committed_names: ['FIRST_TOKEN'],
          remaining_names: ['ORDER_API_TOKEN'],
        },
      ))
      .mockResolvedValueOnce({ project_id: 'project-1', secrets: [], count: 1 })
    const onApply = vi.fn().mockResolvedValue(undefined)
    const onProjectSecretsConfigured = vi.fn()
    const reviewRef = { current: null as SecretBindingReviewHandle | null }
    await act(async () => {
      root.render(
        <SecretBindingReview
          ref={reviewRef}
          projectId="project-1"
          requirements={[{ ...requirements[0], source_value_available: true }]}
          sourceSecretSessionId="retained-session"
          sourceMigrationAllowed
          onApply={onApply}
          onProjectSecretsConfigured={onProjectSecretsConfigured}
        />,
      )
      await Promise.resolve()
    })
    const migrationToggle = container.querySelector<HTMLInputElement>('.credentials-source-migration input')!
    act(() => migrationToggle.click())

    await act(async () => {
      expect(await reviewRef.current?.commit()).toBe(false)
    })

    expect(apiMock.discardImportSecretSession).not.toHaveBeenCalled()
    expect(migrationToggle.checked).toBe(true)
    expect(migrationToggle.disabled).toBe(false)
    expect(container.textContent).toContain('protected import session was retained')
    expect(container.textContent).toContain('Retained credentials: FIRST_TOKEN')
    expect(onApply).not.toHaveBeenCalled()
    expect(onProjectSecretsConfigured).toHaveBeenCalledWith(['FIRST_TOKEN'])

    await act(async () => {
      expect(await reviewRef.current?.commit()).toBe(true)
    })
    expect(apiMock.migrateImportSecrets).toHaveBeenCalledTimes(2)
    expect(onApply).toHaveBeenCalledTimes(1)
    expect(onProjectSecretsConfigured).toHaveBeenLastCalledWith(['ORDER_API_TOKEN'])
  })

  it('emits only non-sensitive preparation metadata and clears values after success', async () => {
    const onPreparedChange = vi.fn()
    const onApply = vi.fn().mockResolvedValue(undefined)
    const reviewRef = { current: null as SecretBindingReviewHandle | null }
    await act(async () => {
      root.render(
        <SecretBindingReview
          ref={reviewRef}
          projectId="project-1"
          requirements={[requirements[0]]}
          onApply={onApply}
          onPreparedChange={onPreparedChange}
        />,
      )
      await Promise.resolve()
    })
    const valueInput = container.querySelector<HTMLInputElement>('input[type="password"]')!
    act(() => setInput(valueInput, 'never-leave-the-card'))

    expect(onPreparedChange).toHaveBeenLastCalledWith({
      valid: true,
      configureLaterCount: 0,
      selectedBindingCount: 1,
    })
    expect(JSON.stringify(onPreparedChange.mock.calls)).not.toContain('never-leave-the-card')

    await act(async () => {
      await reviewRef.current?.commit()
    })

    expect(apiMock.createProjectSecrets).toHaveBeenCalledWith('project-1', [{
      name: 'ORDER_API_TOKEN',
      value: 'never-leave-the-card',
    }])
    expect(valueInput.value).toBe('')
    expect(JSON.stringify(onPreparedChange.mock.calls)).not.toContain('never-leave-the-card')
  })

  it('emits the operator-edited destination name and live configured guidance without values', async () => {
    apiMock.getProjectSecretStatuses.mockImplementation(async (_projectId: string, names: string[]) => ({
      project_id: 'project-1',
      capabilities: {
        provider: 'database',
        writable: true,
        create_supported: true,
      },
      secrets: names.map(name => ({
        name,
        configured: name === 'RENAMED_TOKEN',
        provider: 'database',
        writable: true,
      })),
    }))
    const onRequirementDestinationStateChange = vi.fn()
    await act(async () => {
      root.render(
        <SecretBindingReview
          projectId="project-1"
          requirements={[requirements[0]]}
          onApply={vi.fn()}
          onRequirementDestinationStateChange={onRequirementDestinationStateChange}
        />,
      )
      await Promise.resolve()
    })

    act(() => setInput(container.querySelector<HTMLInputElement>('input[type="text"]')!, 'RENAMED_TOKEN'))
    await act(async () => {
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(onRequirementDestinationStateChange).toHaveBeenLastCalledWith({
      'authorization-header': {
        usesProjectSecret: true,
        secretName: 'RENAMED_TOKEN',
        readyForExecution: true,
        guidance: 'The selected project secret is already configured and will be used when the imported scripts run.',
      },
    })
    expect(JSON.stringify(onRequirementDestinationStateChange.mock.calls)).not.toContain('never-leave-the-card')
  })

  it('never includes a write-only credential value in lifted destination state', async () => {
    const onRequirementDestinationStateChange = vi.fn()
    await act(async () => {
      root.render(
        <SecretBindingReview
          projectId="project-1"
          requirements={[requirements[0]]}
          onApply={vi.fn()}
          onRequirementDestinationStateChange={onRequirementDestinationStateChange}
        />,
      )
      await Promise.resolve()
      await Promise.resolve()
    })

    act(() => setInput(
      container.querySelector<HTMLInputElement>('input[type="password"]')!,
      'never-leave-destination-card',
    ))

    expect(onRequirementDestinationStateChange).toHaveBeenLastCalledWith({
      'authorization-header': {
        usesProjectSecret: true,
        secretName: 'ORDER_API_TOKEN',
        readyForExecution: true,
        guidance: 'A value will be stored in Project Secrets during import.',
      },
    })
    expect(JSON.stringify(onRequirementDestinationStateChange.mock.calls))
      .not.toContain('never-leave-destination-card')
  })

  it('reports a confirmed credential write as configured when binding application fails', async () => {
    const onRequirementDestinationStateChange = vi.fn()
    const reviewRef = { current: null as SecretBindingReviewHandle | null }
    const onApply = vi.fn().mockRejectedValue(new Error('temporary binding failure'))
    await act(async () => {
      root.render(
        <SecretBindingReview
          ref={reviewRef}
          projectId="project-1"
          requirements={[requirements[0]]}
          onApply={onApply}
          onRequirementDestinationStateChange={onRequirementDestinationStateChange}
        />,
      )
      await Promise.resolve()
      await Promise.resolve()
    })
    act(() => setInput(
      container.querySelector<HTMLInputElement>('input[type="password"]')!,
      'write-before-apply-failure',
    ))

    await act(async () => {
      expect(await reviewRef.current?.commit()).toBe(false)
    })

    expect(apiMock.createProjectSecrets).toHaveBeenCalledWith('project-1', [{
      name: 'ORDER_API_TOKEN',
      value: 'write-before-apply-failure',
    }])
    expect(container.textContent).toContain('temporary binding failure')
    expect(onRequirementDestinationStateChange).toHaveBeenLastCalledWith({
      'authorization-header': {
        usesProjectSecret: true,
        secretName: 'ORDER_API_TOKEN',
        readyForExecution: true,
        guidance: 'The selected project secret is already configured and will be used when the imported scripts run.',
      },
    })

    await act(async () => {
      root.render(
        <SecretBindingReview
          ref={reviewRef}
          projectId="project-2"
          requirements={[requirements[0]]}
          onApply={vi.fn()}
          onRequirementDestinationStateChange={onRequirementDestinationStateChange}
        />,
      )
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(container.querySelector('.script-health-token')?.textContent).toBe('Configure later')
    expect(onRequirementDestinationStateChange).toHaveBeenLastCalledWith({
      'authorization-header': {
        usesProjectSecret: true,
        secretName: 'ORDER_API_TOKEN',
        readyForExecution: false,
        guidance: 'Enter the value now or configure it in Project Secrets before running the imported scripts.',
      },
    })
  })

  it('preserves write-only edits across same-scope review generations', async () => {
    const onApply = vi.fn().mockResolvedValue(undefined)
    const renderReview = async (reviewGeneration: string, nextRequirements: SecretBindingRequirement[]) => {
      await act(async () => {
        root.render(
          <SecretBindingReview
            reviewGeneration={reviewGeneration}
            projectId="project-1"
            requirements={nextRequirements}
            onApply={onApply}
          />,
        )
        await Promise.resolve()
      })
    }
    await renderReview('generation-a', [{ ...requirements[0] }])
    const nameInput = container.querySelector<HTMLInputElement>('input[type="text"]')!
    const valueInput = container.querySelector<HTMLInputElement>('input[type="password"]')!
    act(() => {
      setInput(nameInput, 'CUSTOM_TOKEN')
      setInput(valueInput, 'write-only-value')
    })

    await renderReview('generation-a', [{ ...requirements[0] }])
    expect(container.querySelector<HTMLInputElement>('input[type="text"]')?.value).toBe('CUSTOM_TOKEN')
    expect(container.querySelector<HTMLInputElement>('input[type="password"]')?.value).toBe('write-only-value')

    await renderReview('generation-b', [{ ...requirements[0] }])
    expect(container.querySelector<HTMLInputElement>('input[type="text"]')?.value).toBe('CUSTOM_TOKEN')
    expect(container.querySelector<HTMLInputElement>('input[type="password"]')?.value).toBe('write-only-value')
  })

  it('preserves edits for a same-scope generation and clears them on project change', async () => {
    const reviewRef = { current: null as SecretBindingReviewHandle | null }
    const onApply = vi.fn().mockResolvedValue(undefined)
    const renderReview = async (reviewGeneration: string, projectId: string) => {
      await act(async () => {
        root.render(
          <SecretBindingReview
            ref={reviewRef}
            reviewGeneration={reviewGeneration}
            projectId={projectId}
            requirements={[{ ...requirements[0] }]}
            onApply={onApply}
          />,
        )
        await Promise.resolve()
      })
    }

    await renderReview('generation-a', 'project-1')
    const nameInput = container.querySelector<HTMLInputElement>('input[type="text"]')!
    const valueInput = container.querySelector<HTMLInputElement>('input[type="password"]')!
    act(() => {
      setInput(nameInput, 'CUSTOM_TOKEN')
      setInput(valueInput, 'write-only-value')
    })

    await renderReview('generation-b', 'project-1')
    expect(container.querySelector<HTMLInputElement>('input[type="text"]')?.value).toBe('CUSTOM_TOKEN')
    expect(container.querySelector<HTMLInputElement>('input[type="password"]')?.value).toBe('write-only-value')

    await act(async () => {
      await reviewRef.current?.commit()
    })
    expect(apiMock.createProjectSecrets).toHaveBeenCalledWith('project-1', [{
      name: 'CUSTOM_TOKEN',
      value: 'write-only-value',
    }])

    await renderReview('generation-c', 'project-2')
    expect(container.querySelector<HTMLInputElement>('input[type="text"]')?.value).toBe('ORDER_API_TOKEN')
    expect(container.querySelector<HTMLInputElement>('input[type="password"]')?.value).toBe('')
  })

  it('uses unique validation IDs and resolves every described-by reference', async () => {
    await act(async () => {
      root.render(
        <SecretBindingReview
          projectId="project-1"
          requirements={[
            { ...requirements[0], id: 'first', suggested_name: 'FIRST_TOKEN' },
            { ...requirements[1], id: 'second', suggested_name: 'SECOND_TOKEN' },
          ]}
          onApply={vi.fn()}
        />,
      )
      await Promise.resolve()
    })

    const nameInputs = container.querySelectorAll<HTMLInputElement>('input[type="text"]')
    const valueInputs = container.querySelectorAll<HTMLInputElement>('input[type="password"]')
    act(() => {
      setInput(nameInputs[0], 'INVALID/NAME')
      setInput(nameInputs[1], 'INVALID/NAME')
      setInput(valueInputs[0], 'first-write-only-value')
      setInput(valueInputs[1], 'second-write-only-value')
    })

    const validationMessages = Array.from(container.querySelectorAll<HTMLElement>('.secret-binding-review-field-error'))
    const validationIDs = validationMessages.map(message => message.id)
    expect(validationIDs).toHaveLength(4)
    expect(new Set(validationIDs).size).toBe(validationIDs.length)
    for (const input of Array.from(nameInputs)) {
      const describedBy = input.getAttribute('aria-describedby')?.split(/\s+/).filter(Boolean) || []
      expect(describedBy.length).toBe(3)
      for (const id of describedBy) {
        expect(document.getElementById(id)).not.toBeNull()
      }
    }
  })

  it('does not create or migrate credentials after the reviewed generation changes during the commit probe', async () => {
    apiMock.createProjectSecrets.mockClear()
    apiMock.migrateImportSecrets.mockClear()
    const onApply = vi.fn().mockResolvedValue(undefined)
    const reviewRef = { current: null as SecretBindingReviewHandle | null }
    await act(async () => {
      root.render(
        <SecretBindingReview
          ref={reviewRef}
          reviewGeneration="generation-a"
          projectId="project-1"
          requirements={[{ ...requirements[0], source_value_available: true }]}
          sourceSecretSessionId="source-a"
          sourceMigrationAllowed
          onApply={onApply}
        />,
      )
      await Promise.resolve()
    })
    const valueInput = container.querySelector<HTMLInputElement>('input[type="password"]')!
    act(() => setInput(valueInput, 'stale-write-only-value'))
    let resolveProbe: ((value: unknown) => void) | undefined
    apiMock.getProjectSecretStatuses.mockReturnValueOnce(new Promise(resolve => {
      resolveProbe = resolve
    }))

    let commitResult: boolean | undefined
    act(() => {
      void reviewRef.current?.commit().then(result => { commitResult = result })
    })
    await act(async () => Promise.resolve())
    await act(async () => {
      root.render(
        <SecretBindingReview
          ref={reviewRef}
          reviewGeneration="generation-b"
          projectId="project-2"
          requirements={[{ ...requirements[0], id: 'new-auth', suggested_name: 'NEW_TOKEN' }]}
          onApply={vi.fn()}
        />,
      )
    })
    await act(async () => {
      resolveProbe?.({
        project_id: 'project-1',
        capabilities: { provider: 'database', writable: true, create_supported: true },
        secrets: [],
      })
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(commitResult).toBe(false)
    expect(apiMock.createProjectSecrets).not.toHaveBeenCalled()
    expect(apiMock.migrateImportSecrets).not.toHaveBeenCalled()
    expect(onApply).not.toHaveBeenCalled()
  })

  it('does not leak a completed in-flight write into a different project scope', async () => {
    let resolveCreate: ((value: unknown) => void) | undefined
    apiMock.createProjectSecrets.mockReturnValueOnce(new Promise(resolve => {
      resolveCreate = resolve
    }))
    const onApply = vi.fn().mockResolvedValue(undefined)
    const onRequirementDestinationStateChange = vi.fn()
    const reviewRef = { current: null as SecretBindingReviewHandle | null }
    await act(async () => {
      root.render(
        <SecretBindingReview
          ref={reviewRef}
          reviewGeneration="generation-a"
          projectId="project-1"
          requirements={[requirements[0]]}
          onApply={onApply}
          onRequirementDestinationStateChange={onRequirementDestinationStateChange}
        />,
      )
      await Promise.resolve()
      await Promise.resolve()
    })
    act(() => setInput(
      container.querySelector<HTMLInputElement>('input[type="password"]')!,
      'project-one-only-value',
    ))

    let commitResult: boolean | undefined
    act(() => {
      void reviewRef.current?.commit().then(result => { commitResult = result })
    })
    await act(async () => {
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(apiMock.createProjectSecrets).toHaveBeenCalledWith('project-1', [{
      name: 'ORDER_API_TOKEN',
      value: 'project-one-only-value',
    }])

    await act(async () => {
      root.render(
        <SecretBindingReview
          ref={reviewRef}
          reviewGeneration="generation-b"
          projectId="project-2"
          requirements={[requirements[0]]}
          onApply={vi.fn()}
          onRequirementDestinationStateChange={onRequirementDestinationStateChange}
        />,
      )
      await Promise.resolve()
    })
    await act(async () => {
      resolveCreate?.({ project_id: 'project-1', secrets: [], count: 1 })
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(commitResult).toBe(false)
    expect(onApply).not.toHaveBeenCalled()
    expect(container.querySelector('.script-health-token')?.textContent).toBe('Configure later')
    expect(onRequirementDestinationStateChange).toHaveBeenLastCalledWith({
      'authorization-header': {
        usesProjectSecret: true,
        secretName: 'ORDER_API_TOKEN',
        readyForExecution: false,
        guidance: 'Enter the value now or configure it in Project Secrets before running the imported scripts.',
      },
    })
  })

  it('groups duplicate references and creates an optional value before applying bindings', async () => {
    const onApply = vi.fn().mockResolvedValue(undefined)
    await act(async () => {
      root.render(
        <SecretBindingReview
          projectId="project-1"
          requirements={[
            requirements[0],
            { ...requirements[0], id: 'authorization-header-retry', path: '/actions/1/headers/Authorization' },
          ]}
          onApply={onApply}
        />,
      )
    })
    await act(async () => Promise.resolve())

    expect(container.textContent).toContain('Credential groups (1)')
    expect(container.textContent).toContain('2 locations')
    expect(container.querySelector('.credentials-location-details summary')?.textContent)
      .toBe('View 2 affected locations')
    const valueInput = container.querySelector<HTMLInputElement>('input[type="password"]')!
    act(() => setInput(valueInput, 'secret-value'))
    const applyButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Continue with credentials'))
    await act(async () => applyButton?.click())

    expect(apiMock.createProjectSecrets).toHaveBeenCalledWith('project-1', [{ name: 'ORDER_API_TOKEN', value: 'secret-value' }])
    expect(onApply).toHaveBeenCalledWith([
      { requirement_id: 'authorization-header', secret_name: 'ORDER_API_TOKEN' },
      { requirement_id: 'authorization-header-retry', secret_name: 'ORDER_API_TOKEN' },
    ])
  })

  it('keeps identical suggestions from different scripts separate and assigns collision-free defaults', async () => {
    const onApply = vi.fn().mockResolvedValue(undefined)
    await act(async () => {
      root.render(
        <SecretBindingReview
          requirements={[
            { ...requirements[0], id: 'script-a-token', script_index: 0, script_name: 'Script A' },
            { ...requirements[0], id: 'script-b-token', script_index: 1, script_name: 'Script B' },
          ]}
          onApply={onApply}
        />,
      )
    })

    expect(container.textContent).toContain('Credential groups (2)')
    expect(Array.from(container.querySelectorAll('.secret-binding-review-script strong')).map(node => node.textContent))
      .toEqual(['Script A', 'Script B'])
    expect(Array.from(container.querySelectorAll<HTMLInputElement>('input[type="text"]')).map(input => input.value)).toEqual([
      'ORDER_API_TOKEN',
      'ORDER_API_TOKEN_2',
    ])
    const applyButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Continue with credentials'))
    await act(async () => applyButton?.click())

    expect(onApply).toHaveBeenCalledWith([
      { requirement_id: 'script-a-token', secret_name: 'ORDER_API_TOKEN' },
      { requirement_id: 'script-b-token', secret_name: 'ORDER_API_TOKEN_2' },
    ])
    const routedBindings = onApply.mock.calls[0][0] as SecretBindingSelection[]
    expect(routedBindings.map(binding => binding.script_index)).toEqual([0, 1])
    expect(JSON.stringify(routedBindings)).not.toContain('script_index')
  })

  it('discards an unused protected source session after applying deferred bindings', async () => {
    const onApply = vi.fn().mockResolvedValue(undefined)
    const reviewRef = { current: null as SecretBindingReviewHandle | null }
    await act(async () => {
      root.render(
        <SecretBindingReview
          ref={reviewRef}
          projectId="project-1"
          requirements={[{ ...requirements[0], source_value_available: true }]}
          sourceSecretSessionId="unused-source-session"
          sourceMigrationAllowed
          onApply={onApply}
        />,
      )
      await Promise.resolve()
    })

    await act(async () => { await reviewRef.current?.commit() })

    expect(apiMock.migrateImportSecrets).not.toHaveBeenCalled()
    expect(apiMock.discardImportSecretSession).toHaveBeenCalledWith({
      project_id: 'project-1',
      source_secret_session_id: 'unused-source-session',
    })
    expect(onApply).toHaveBeenCalledTimes(1)
  })

  it('does not discard a protected source session after successful migration consumes it', async () => {
    const reviewRef = { current: null as SecretBindingReviewHandle | null }
    await act(async () => {
      root.render(
        <SecretBindingReview
          ref={reviewRef}
          projectId="project-1"
          requirements={[{ ...requirements[0], source_value_available: true }]}
          sourceSecretSessionId="consumed-source-session"
          sourceMigrationAllowed
          onApply={vi.fn().mockResolvedValue(undefined)}
        />,
      )
      await Promise.resolve()
    })
    const migrationToggle = container.querySelector<HTMLInputElement>('.credentials-source-migration input')!
    act(() => migrationToggle.click())

    await act(async () => { await reviewRef.current?.commit() })

    expect(apiMock.migrateImportSecrets).toHaveBeenCalledTimes(1)
    expect(apiMock.discardImportSecretSession).not.toHaveBeenCalled()
  })

  it('exposes an idempotent source-session discard action for explicit cancellation', async () => {
    const reviewRef = { current: null as SecretBindingReviewHandle | null }
    await act(async () => {
      root.render(
        <SecretBindingReview
          ref={reviewRef}
          projectId="project-1"
          requirements={[{ ...requirements[0], source_value_available: true }]}
          sourceSecretSessionId="cancelled-source-session"
          sourceMigrationAllowed
          onApply={vi.fn()}
        />,
      )
    })

    await act(async () => {
      await Promise.all([
        reviewRef.current?.discardSourceSession(),
        reviewRef.current?.discardSourceSession(),
      ])
    })

    expect(apiMock.discardImportSecretSession).toHaveBeenCalledTimes(1)
  })

  it('converges explicitly reused final names before computing create and migration state', async () => {
    apiMock.migrateImportSecrets.mockClear()
    const onApply = vi.fn().mockResolvedValue(undefined)
    await act(async () => {
      root.render(
        <SecretBindingReview
          projectId="project-1"
          requirements={[
            {
              ...requirements[0],
              id: 'manual',
              label: 'Manually entered credential',
              suggested_name: 'MANUAL_TOKEN',
              source_value_available: false,
            },
            {
              ...requirements[1],
              id: 'source',
              label: 'Source-backed credential',
              suggested_name: 'SOURCE_TOKEN',
              source_value_available: true,
            },
          ]}
          sourceSecretSessionId="session-1"
          sourceMigrationAllowed
          onApply={onApply}
        />,
      )
    })
    await act(async () => Promise.resolve())

    const nameInputs = container.querySelectorAll<HTMLInputElement>('input[type="text"]')
    const valueInputs = container.querySelectorAll<HTMLInputElement>('input[type="password"]')
    await act(async () => {
      setInput(nameInputs[1], 'MANUAL_TOKEN')
      setInput(valueInputs[0], 'manual-value')
      await Promise.resolve()
    })

    expect(container.querySelector('.credentials-card-summary')?.textContent).toContain('0 bindings need setup')
    let rows = Array.from(container.querySelectorAll<HTMLElement>('.secret-binding-review-item'))
    expect(rows.map(row => row.querySelector('.script-health-token')?.textContent)).toEqual([
      'Will be created',
      'Will be created',
    ])

    const migrationToggle = container.querySelector<HTMLInputElement>('.credentials-source-migration input')
    act(() => migrationToggle?.click())
    rows = Array.from(container.querySelectorAll<HTMLElement>('.secret-binding-review-item'))
    expect(rows.map(row => row.querySelector('.script-health-token')?.textContent)).toEqual([
      'Will be created',
      'Will be created',
    ])

    const applyButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Continue with credentials'))
    await act(async () => applyButton?.click())

    expect(apiMock.createProjectSecrets).toHaveBeenCalledWith('project-1', [{
      name: 'MANUAL_TOKEN',
      value: 'manual-value',
    }])
    expect(apiMock.migrateImportSecrets).not.toHaveBeenCalled()
    expect(onApply).toHaveBeenCalledWith([
      { requirement_id: 'manual', secret_name: 'MANUAL_TOKEN' },
      { requirement_id: 'source', secret_name: 'MANUAL_TOKEN' },
    ])
  })

  it('reports externally selected source migration without invoking the generic session endpoint', async () => {
    apiMock.migrateImportSecrets.mockClear()
    const onApply = vi.fn().mockResolvedValue(undefined)
    await act(async () => {
      root.render(
        <SecretBindingReview
          projectId="project-1"
          requirements={[{ ...requirements[0], source_value_available: true }]}
          sourceMigrationAllowed
          externalSourceMigrationSelected
          onExternalSourceMigrationChange={vi.fn()}
          onApply={onApply}
        />,
      )
    })
    await act(async () => Promise.resolve())

    const row = container.querySelector<HTMLElement>('.secret-binding-review-item')
    expect(row?.querySelector('.script-health-token')?.textContent).toBe('Will be created')
    expect(container.querySelector('.credentials-card-summary')?.textContent).toContain('0 bindings need setup')

    const applyButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Continue with credentials'))
    await act(async () => applyButton?.click())

    expect(apiMock.migrateImportSecrets).not.toHaveBeenCalled()
    expect(onApply).toHaveBeenCalledWith([
      { requirement_id: 'authorization-header', secret_name: 'ORDER_API_TOKEN' },
    ])
  })

  it('allocates a fresh name for externally migrated values when existing project secrets collide', async () => {
    apiMock.getProjectSecretStatuses.mockImplementation(async (_projectId: string, names: string[]) => ({
      project_id: 'project-1',
      capabilities: {
        provider: 'database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: true,
      },
      secrets: names.map(name => ({
        name,
        configured: name === 'ORDER_API_TOKEN' || name === 'ORDER_API_TOKEN_2',
        provider: 'database',
        writable: true,
      })),
    }))
    const onApply = vi.fn().mockResolvedValue(undefined)
    await act(async () => {
      root.render(
        <SecretBindingReview
          projectId="project-1"
          requirements={[{ ...requirements[0], source_value_available: true }]}
          sourceMigrationAllowed
          externalSourceMigrationSelected
          onExternalSourceMigrationChange={vi.fn()}
          onApply={onApply}
        />,
      )
      await Promise.resolve()
    })

    expect(container.querySelector('.secret-binding-review-item .script-health-token')?.textContent)
      .toBe('Will use a new name')
    const applyButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Continue with credentials'))
    await act(async () => applyButton?.click())

    expect(apiMock.getProjectSecretStatuses).toHaveBeenCalledWith(
      'project-1',
      expect.arrayContaining(['ORDER_API_TOKEN_2', 'ORDER_API_TOKEN_3']),
    )
    expect(apiMock.migrateImportSecrets).not.toHaveBeenCalled()
    expect(onApply).toHaveBeenCalledWith([
      { requirement_id: 'authorization-header', secret_name: 'ORDER_API_TOKEN_3' },
    ])
  })

  it('reuses a collision-safe name after a transient create failure while it remains available', async () => {
    apiMock.getProjectSecretStatuses.mockImplementation(async (_projectId: string, names: string[]) => ({
      project_id: 'project-1',
      capabilities: {
        provider: 'database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: false,
      },
      secrets: names.map(name => ({
        name,
        configured: name === 'ORDER_API_TOKEN',
        provider: 'database',
        writable: true,
      })),
    }))
    apiMock.createProjectSecrets
      .mockRejectedValueOnce(new Error('temporary provider failure'))
      .mockResolvedValueOnce({ project_id: 'project-1', secrets: [], count: 1 })
    const reviewRef = { current: null as SecretBindingReviewHandle | null }
    const onApply = vi.fn().mockResolvedValue(undefined)
    await act(async () => {
      root.render(
        <SecretBindingReview
          ref={reviewRef}
          projectId="project-1"
          requirements={[requirements[0]]}
          onApply={onApply}
        />,
      )
      await Promise.resolve()
    })
    const valueInput = container.querySelector<HTMLInputElement>('input[type="password"]')!
    act(() => setInput(valueInput, 'manual-value'))

    let committed = true
    await act(async () => {
      committed = await reviewRef.current!.commit()
    })
    expect(committed).toBe(false)
    expect(apiMock.createProjectSecrets).toHaveBeenNthCalledWith(1, 'project-1', [{
      name: 'ORDER_API_TOKEN_2',
      value: 'manual-value',
    }])

    await act(async () => {
      committed = await reviewRef.current!.commit()
    })
    expect(committed).toBe(true)
    expect(apiMock.createProjectSecrets).toHaveBeenNthCalledWith(2, 'project-1', [{
      name: 'ORDER_API_TOKEN_2',
      value: 'manual-value',
    }])
    expect(onApply).toHaveBeenCalledWith([
      { requirement_id: 'authorization-header', secret_name: 'ORDER_API_TOKEN_2' },
    ])
  })

  it('retries only the unwritten subset after a partial project-secret batch failure', async () => {
    const partialRequirements: SecretBindingRequirement[] = [
      {
        ...requirements[0],
        id: 'partial-token-a',
        suggested_name: 'PARTIAL_TOKEN_A',
        replacement: '{{secret.PARTIAL_TOKEN_A}}',
      },
      {
        ...requirements[0],
        id: 'partial-token-b',
        suggested_name: 'PARTIAL_TOKEN_B',
        replacement: '{{secret.PARTIAL_TOKEN_B}}',
      },
    ]
    apiMock.getProjectSecretStatuses.mockImplementation(async (_projectId: string, names: string[]) => ({
      project_id: 'project-1',
      capabilities: {
        provider: 'database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: false,
      },
      secrets: names.map(name => ({ name, configured: false, provider: 'database', writable: true })),
    }))
    apiMock.createProjectSecrets
      .mockRejectedValueOnce(new ApiError('provider write failed', 502, {
        partial: true,
        created: [{ name: 'PARTIAL_TOKEN_A' }],
        remaining_names: ['PARTIAL_TOKEN_B'],
      }))
      .mockResolvedValueOnce({ project_id: 'project-1', secrets: [], count: 1 })
    const reviewRef = { current: null as SecretBindingReviewHandle | null }
    const onApply = vi.fn().mockResolvedValue(undefined)
    await act(async () => {
      root.render(
        <SecretBindingReview
          ref={reviewRef}
          projectId="project-1"
          requirements={partialRequirements}
          onApply={onApply}
        />,
      )
      await Promise.resolve()
    })
    act(() => {
      const inputs = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="password"]'))
      setInput(inputs[0], 'write-only-a')
      setInput(inputs[1], 'write-only-b')
    })

    await act(async () => {
      expect(await reviewRef.current?.commit()).toBe(false)
    })
    expect(apiMock.createProjectSecrets).toHaveBeenNthCalledWith(1, 'project-1', [
      { name: 'PARTIAL_TOKEN_A', value: 'write-only-a' },
      { name: 'PARTIAL_TOKEN_B', value: 'write-only-b' },
    ])

    await act(async () => {
      expect(await reviewRef.current?.commit()).toBe(true)
    })
    expect(apiMock.createProjectSecrets).toHaveBeenNthCalledWith(2, 'project-1', [
      { name: 'PARTIAL_TOKEN_B', value: 'write-only-b' },
    ])
    expect(onApply).toHaveBeenCalledWith([
      { requirement_id: 'partial-token-a', secret_name: 'PARTIAL_TOKEN_A' },
      { requirement_id: 'partial-token-b', secret_name: 'PARTIAL_TOKEN_B' },
    ])
  })

  it('probes many collision-safe names in bounded concurrent batches', async () => {
    const collisionRequirements = Array.from({ length: 12 }, (_, index): SecretBindingRequirement => ({
      ...requirements[0],
      id: `collision-${index}`,
      suggested_name: `CONFIGURED_TOKEN_${index}`,
      replacement: `{{secret.CONFIGURED_TOKEN_${index}}}`,
    }))
    let inFlight = 0
    let maxInFlight = 0
    apiMock.getProjectSecretStatuses.mockImplementation(async (_projectId: string, names: string[]) => {
      const suffixProbe = names.some(name => /_\d+$/.test(name.replace(/^CONFIGURED_TOKEN_\d+/, '')))
      if (suffixProbe) {
        inFlight += 1
        maxInFlight = Math.max(maxInFlight, inFlight)
        await Promise.resolve()
        inFlight -= 1
      }
      return {
        project_id: 'project-1',
        capabilities: {
          provider: 'database', writable: true, create_supported: true, replace_supported: true,
          source_secret_migration_policy_enabled: false,
        },
        secrets: names.map(name => ({
          name,
          configured: !suffixProbe,
          provider: 'database',
          writable: true,
        })),
      }
    })
    const reviewRef = { current: null as SecretBindingReviewHandle | null }
    await act(async () => {
      root.render(
        <SecretBindingReview
          ref={reviewRef}
          projectId="project-1"
          requirements={collisionRequirements}
          onApply={vi.fn().mockResolvedValue(undefined)}
        />,
      )
      await Promise.resolve()
    })
    act(() => {
      for (const [index, input] of Array.from(container.querySelectorAll<HTMLInputElement>('input[type="password"]')).entries()) {
        setInput(input, `manual-${index}`)
      }
    })

    await act(async () => {
      expect(await reviewRef.current?.commit()).toBe(true)
    })

    const suffixCalls = apiMock.getProjectSecretStatuses.mock.calls.filter(([, names]) => (
      names.some((name: string) => /_\d+$/.test(name.replace(/^CONFIGURED_TOKEN_\d+/, '')))
    ))
    expect(suffixCalls.length).toBeGreaterThan(1)
    expect(suffixCalls.every(([, names]) => names.length <= 256)).toBe(true)
    expect(maxInFlight).toBeGreaterThan(1)
    expect(apiMock.createProjectSecrets).toHaveBeenCalledTimes(1)
  })

  it('allocates another fresh name when a create-only race claims the speculative name', async () => {
    const configuredNames = new Set(['ORDER_API_TOKEN'])
    apiMock.getProjectSecretStatuses.mockImplementation(async (_projectId: string, names: string[]) => ({
      project_id: 'project-1',
      capabilities: {
        provider: 'database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: false,
      },
      secrets: names.map(name => ({
        name,
        configured: configuredNames.has(name),
        provider: 'database',
        writable: true,
      })),
    }))
    apiMock.createProjectSecrets
      .mockImplementationOnce(async (_projectId: string, secrets: Array<{ name: string; value: string }>) => {
        configuredNames.add(secrets[0].name)
        throw new Error('secret was created concurrently')
      })
      .mockImplementationOnce(async (_projectId: string, secrets: Array<{ name: string; value: string }>) => {
        configuredNames.add(secrets[0].name)
        return { project_id: 'project-1', secrets: [], count: 1 }
      })
    const reviewRef = { current: null as SecretBindingReviewHandle | null }
    const onApply = vi.fn().mockResolvedValue(undefined)
    await act(async () => {
      root.render(
        <SecretBindingReview
          ref={reviewRef}
          projectId="project-1"
          requirements={[requirements[0]]}
          onApply={onApply}
        />,
      )
      await Promise.resolve()
    })
    act(() => setInput(
      container.querySelector<HTMLInputElement>('input[type="password"]')!,
      'manual-value',
    ))

    let committed = true
    await act(async () => {
      committed = await reviewRef.current!.commit()
    })
    expect(committed).toBe(false)
    expect(apiMock.createProjectSecrets).toHaveBeenNthCalledWith(1, 'project-1', [{
      name: 'ORDER_API_TOKEN_2',
      value: 'manual-value',
    }])

    await act(async () => {
      committed = await reviewRef.current!.commit()
    })
    expect(committed).toBe(true)
    expect(apiMock.createProjectSecrets).toHaveBeenNthCalledWith(2, 'project-1', [{
      name: 'ORDER_API_TOKEN_2_2',
      value: 'manual-value',
    }])
    expect(onApply).toHaveBeenCalledWith([
      { requirement_id: 'authorization-header', secret_name: 'ORDER_API_TOKEN_2_2' },
    ])
  })

  it('migrates source-backed values under collision-safe names instead of reusing configured secrets', async () => {
    apiMock.getProjectSecretStatuses.mockResolvedValue({
      project_id: 'project-1',
      capabilities: {
        provider: 'database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: true,
      },
      secrets: [
        { name: 'CONFIGURED_TOKEN', configured: true, provider: 'database', writable: true },
      ],
    })
    const migrationRequirements: SecretBindingRequirement[] = [
      { ...requirements[0], id: 'manual', suggested_name: 'MANUAL_TOKEN', source_value_available: true },
      { ...requirements[0], id: 'configured', suggested_name: 'CONFIGURED_TOKEN', source_value_available: true },
      { ...requirements[0], id: 'migrate', suggested_name: 'MIGRATED_TOKEN', source_value_available: true },
      { ...requirements[0], id: 'unavailable', suggested_name: 'UNAVAILABLE_TOKEN', source_value_available: false },
    ]
    const onApply = vi.fn().mockResolvedValue(undefined)
    await act(async () => {
      root.render(
        <SecretBindingReview
          projectId="project-1"
          requirements={migrationRequirements}
          sourceSecretSessionId="session-1"
          sourceMigrationAllowed
          onApply={onApply}
        />,
      )
    })
    await act(async () => Promise.resolve())

    expect(container.textContent).toContain('Available from source')
    const migrationToggle = container.querySelector<HTMLInputElement>('.credentials-source-migration input')
    act(() => migrationToggle?.click())
    // Groups that need setup lead, so find the row by its name.
    const migratedRow = Array.from(container.querySelectorAll<HTMLElement>('.secret-binding-review-item'))
      .find(row => row.textContent?.includes('MIGRATED_TOKEN') || Array.from(row.querySelectorAll('input')).some(input => input.value === 'MIGRATED_TOKEN'))!
    expect(migratedRow.querySelector('.script-health-token')?.textContent).toBe('Will be created')
    const valueInputs = container.querySelectorAll<HTMLInputElement>('input[type="password"]')
    act(() => setInput(valueInputs[0], 'manual-value'))

    const applyButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Continue with credentials'))
    await act(async () => applyButton?.click())

    expect(apiMock.createProjectSecrets).toHaveBeenCalledWith('project-1', [{
      name: 'MANUAL_TOKEN',
      value: 'manual-value',
    }])
    expect(apiMock.migrateImportSecrets).toHaveBeenCalledWith({
      project_id: 'project-1',
      source_secret_session_id: 'session-1',
      secrets: [
        { requirement_id: 'configured', secret_name: 'CONFIGURED_TOKEN_2' },
        { requirement_id: 'migrate', secret_name: 'MIGRATED_TOKEN' },
      ],
    })
    expect(onApply).toHaveBeenCalledWith([
      { requirement_id: 'manual', secret_name: 'MANUAL_TOKEN' },
      { requirement_id: 'configured', secret_name: 'CONFIGURED_TOKEN_2' },
      { requirement_id: 'migrate', secret_name: 'MIGRATED_TOKEN' },
      { requirement_id: 'unavailable', secret_name: 'UNAVAILABLE_TOKEN' },
    ])
  })

  it('migrates archive-wide source values while applying only the active script bindings', async () => {
    const active = { ...requirements[0], id: 'active', suggested_name: 'ACTIVE_TOKEN', source_value_available: true, script_index: 0 }
    const other = { ...requirements[1], id: 'other', suggested_name: 'OTHER_TOKEN', source_value_available: true, script_index: 1 }
    const onApply = vi.fn().mockResolvedValue(undefined)
    await act(async () => {
      root.render(
        <SecretBindingReview
          projectId="project-1"
          requirements={[active]}
          sourceMigrationRequirements={[active, other]}
          sourceSecretSessionId="archive-session"
          sourceMigrationAllowed
          onApply={onApply}
        />,
      )
      await Promise.resolve()
    })
    act(() => container.querySelector<HTMLInputElement>('.credentials-source-migration input')?.click())
    const applyButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Continue with credentials'))
    await act(async () => applyButton?.click())

    expect(apiMock.migrateImportSecrets).toHaveBeenCalledWith({
      project_id: 'project-1',
      source_secret_session_id: 'archive-session',
      secrets: [
        { requirement_id: 'active', secret_name: 'ACTIVE_TOKEN' },
        { requirement_id: 'other', secret_name: 'OTHER_TOKEN' },
      ],
    })
    expect(onApply).toHaveBeenCalledWith([
      { requirement_id: 'active', secret_name: 'ACTIVE_TOKEN' },
    ])
  })

  it('does not migrate a hidden archive credential whose imported destination value is preserved', async () => {
    const active = {
      ...requirements[0],
      id: 'active',
      suggested_name: 'ACTIVE_TOKEN',
      source_value_available: true,
      script_index: 0,
    }
    const hiddenPreserved = {
      ...requirements[1],
      id: 'hidden-preserved',
      suggested_name: 'HIDDEN_TOKEN',
      source_value_available: true,
      destination_value_preserved: true,
      script_index: 1,
    }
    const onApply = vi.fn().mockResolvedValue(undefined)
    await act(async () => {
      root.render(
        <SecretBindingReview
          projectId="project-1"
          requirements={[active]}
          sourceMigrationRequirements={[active, hiddenPreserved]}
          sourceSecretSessionId="archive-session"
          sourceMigrationAllowed
          onApply={onApply}
        />,
      )
      await Promise.resolve()
    })
    act(() => container.querySelector<HTMLInputElement>('.credentials-source-migration input')?.click())
    const applyButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Continue with credentials'))
    await act(async () => applyButton?.click())

    expect(apiMock.migrateImportSecrets).toHaveBeenCalledWith({
      project_id: 'project-1',
      source_secret_session_id: 'archive-session',
      secrets: [
        { requirement_id: 'active', secret_name: 'ACTIVE_TOKEN' },
      ],
    })
    expect(onApply).toHaveBeenCalledWith([
      { requirement_id: 'active', secret_name: 'ACTIVE_TOKEN' },
    ])
  })

  it('settles a successful archive migration after a generated-script switch', async () => {
    let resolveMigration: ((value: unknown) => void) | undefined
    apiMock.migrateImportSecrets.mockReturnValueOnce(new Promise(resolve => {
      resolveMigration = resolve
    }))
    const active = { ...requirements[0], id: 'active', suggested_name: 'DUPLICATE', source_value_available: true, script_index: 0 }
    const other = { ...requirements[1], id: 'other', suggested_name: 'DUPLICATE', source_value_available: true, script_index: 1 }
    const onApply = vi.fn().mockResolvedValue(undefined)
    const onSettled = vi.fn()
    await act(async () => {
      root.render(
        <SecretBindingReview
          reviewGeneration="script-0"
          projectId="project-1"
          requirements={[active]}
          sourceMigrationRequirements={[active, other]}
          sourceSecretSessionId="archive-session"
          sourceMigrationAllowed
          onApply={onApply}
          onSourceSessionSettled={onSettled}
        />,
      )
      await Promise.resolve()
    })
    act(() => container.querySelector<HTMLInputElement>('.credentials-source-migration input')?.click())
    const applyButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Continue with credentials'))
    await act(async () => {
      applyButton?.click()
      await vi.waitFor(() => expect(apiMock.migrateImportSecrets).toHaveBeenCalledTimes(1))
    })

    await act(async () => {
      root.render(
        <SecretBindingReview
          reviewGeneration="script-1"
          projectId="project-1"
          requirements={[other]}
          sourceMigrationRequirements={[active, other]}
          sourceSecretSessionId="archive-session"
          sourceMigrationAllowed
          onApply={onApply}
          onSourceSessionSettled={onSettled}
        />,
      )
      await Promise.resolve()
    })
    await act(async () => {
      resolveMigration?.({ project_id: 'project-1', secrets: [], count: 2 })
      await Promise.resolve()
    })

    expect(onSettled).toHaveBeenCalledWith('archive-session', [
      { requirement_id: 'active', secret_name: 'DUPLICATE' },
      { requirement_id: 'other', secret_name: 'DUPLICATE_2' },
    ])
    expect(onApply).not.toHaveBeenCalled()
  })
})

// A credential shared by a whole collection is deliberately split into one
// requirement per script, so nothing is ever bound to unrelated drafts by
// accident. That leaves the operator naming the same password once per script.
// Reusing one binding across them is an explicit choice the selection contract
// already permits, so it should take one click.
describe('SecretBindingReview reuse across scripts', () => {
  let container: HTMLDivElement
  let root: Root

  // Importers derive the name from the request, so the three copies of one
  // collection password arrive as USERS_PASSWORD / ORDERS_PASSWORD /
  // INVOICES_PASSWORD — never as one repeated name.
  const perScriptRequirements: SecretBindingRequirement[] = ['USERS', 'ORDERS', 'INVOICES'].map((prefix, index) => ({
    id: `password-${index}`,
    path: '/actions/0/params/password',
    label: `${prefix} — Password`,
    context: 'Basic auth credential',
    suggested_name: `${prefix}_PASSWORD`,
    replacement: `{{secret.${prefix}_PASSWORD}}`,
    script_index: index,
    script_name: `get-${prefix.toLowerCase()}`,
  }))

  beforeEach(() => {
    vi.clearAllMocks()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    apiMock.getProjectSecretStatuses.mockResolvedValue({
      project_id: 'project-1',
      capabilities: {
        provider: 'database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: false,
      },
      secrets: [],
    })
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  function nameInputs(): HTMLInputElement[] {
    return Array.from(container.querySelectorAll<HTMLInputElement>('input[type="text"]'))
      .filter(input => input.labels?.[0]?.textContent === 'Secret reference name')
  }

  it('de-duplicates per-script names by default and reuses one on request', () => {
    act(() => {
      root.render(<SecretBindingReview requirements={perScriptRequirements} onApply={vi.fn()} />)
    })

    // Without the control, the operator faces three distinct names.
    expect(nameInputs().map(input => input.value))
      .toEqual(['USERS_PASSWORD', 'ORDERS_PASSWORD', 'INVOICES_PASSWORD'])

    const reuseButtons = Array.from(
      container.querySelectorAll<HTMLButtonElement>('.secret-binding-review-reuse-all'),
    )
    expect(reuseButtons).toHaveLength(3)
    expect(reuseButtons[0].textContent).toContain('Review reuse across 3 password groups')
    // The operator must be able to see what would be merged before merging it.
    expect(reuseButtons[0].title).toContain('USERS — Password')
    expect(reuseButtons[0].title).toContain('INVOICES — Password')

    act(() => {
      reuseButtons[0].dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(nameInputs().map(input => input.value)).toEqual(['USERS_PASSWORD', 'ORDERS_PASSWORD', 'INVOICES_PASSWORD'])
    expect(container.textContent).toContain('Only continue if these locations intentionally share one credential.')
    act(() => reuseButtons[0].click())
    expect(nameInputs().map(input => input.value))
      .toEqual(['USERS_PASSWORD', 'USERS_PASSWORD', 'USERS_PASSWORD'])
  })

  // Usernames and passwords are different credentials and must not be merged
  // into one binding by the same click.
  it('keeps different credential kinds in separate reuse sets', () => {
    const mixed: SecretBindingRequirement[] = [
      ...perScriptRequirements,
      {
        id: 'username-0',
        path: '/actions/0/params/username',
        label: 'USERS — Username',
        context: 'Basic auth identity',
        suggested_name: 'USERS_USERNAME',
        replacement: '{{secret.USERS_USERNAME}}',
        script_index: 0,
        script_name: 'get-users',
      },
    ]
    act(() => {
      root.render(<SecretBindingReview requirements={mixed} onApply={vi.fn()} />)
    })
    const buttons = Array.from(
      container.querySelectorAll<HTMLButtonElement>('.secret-binding-review-reuse-all'),
    )
    // Only the three passwords form a set; the lone username gets no offer.
    expect(buttons).toHaveLength(3)
    expect(buttons.every(button => button.textContent?.includes('password'))).toBe(true)
  })

  it('offers no reuse control when a credential appears only once', () => {
    act(() => {
      root.render(<SecretBindingReview requirements={[perScriptRequirements[0]]} onApply={vi.fn()} />)
    })
    expect(container.querySelector('.secret-binding-review-reuse-all')).toBeNull()
  })
})


describe('credentialNoun', () => {
  // The credential kind is the only part of an importer-derived name that is
  // stable across requests, so it is what identifies the same credential seen
  // again. Anything unrecognized gets no reuse offer rather than a guess.
  it('extracts the credential kind from an importer-derived name', () => {
    expect(credentialNoun('LOGIN_A_PASSWORD')).toBe('PASSWORD')
    expect(credentialNoun('PASSWORD')).toBe('PASSWORD')
    expect(credentialNoun('ORDERS_TOKEN')).toBe('TOKEN')
    expect(credentialNoun('ORDER_API_REFERENCE')).toBe('')
    expect(credentialNoun('')).toBe('')
  })
})
