// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ProjectSecretsPanel from './ProjectSecretsPanel'
import { ApiError } from '../hooks/apiClient'
import { NavigationGuardRegisterContext, type NavigationGuard } from '../hooks/NavigationGuardProvider'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = vi.hoisted(() => ({
  listProjectSecrets: vi.fn(),
  createProjectSecrets: vi.fn(),
  rotateProjectSecret: vi.fn(),
}))

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

function setInput(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
  })
}

describe('ProjectSecretsPanel', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    vi.clearAllMocks()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    apiMock.listProjectSecrets.mockResolvedValue({
      project_id: 'project-1',
      capabilities: {
        provider: 'encrypted_database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: false,
      },
      secrets: [{
        project_id: 'project-1',
        name: 'EXISTING_TOKEN',
        provider: 'encrypted_database',
        writable: true,
        created_at: '2026-07-01T12:00:00Z',
        updated_at: '2026-07-02T12:00:00Z',
        created_by: 'admin@example.test',
      }],
      count: 1,
    })
    apiMock.createProjectSecrets.mockResolvedValue({ project_id: 'project-1', secrets: [], count: 1 })
    apiMock.rotateProjectSecret.mockResolvedValue({
      project_id: 'project-1',
      secret: { name: 'EXISTING_TOKEN', provider: 'encrypted_database', writable: true },
    })
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
    vi.clearAllMocks()
  })

  it('allows editor create-only writes while reserving rotation for admins', async () => {
    await act(async () => {
      root.render(
        <ProjectSecretsPanel
          projectId="project-1"
          projectName="Checkout"
          canCreate
          canRotate={false}
        />,
      )
    })
    await flushPromises()

    expect(container.textContent).toContain('EXISTING_TOKEN')
    expect(container.textContent).toContain('encrypted_database')
    expect(container.textContent).toContain('Editors may create missing secrets')
    expect(container.textContent).not.toContain('existing-secret-value')
    expect(Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Create Secret')).toBeDefined()
    expect(container.textContent).not.toContain('Creating...')

    const createInputs = container.querySelectorAll<HTMLInputElement>('.project-secret-create input')
    act(() => {
      setInput(createInputs[0], 'checkout token')
      setInput(createInputs[1], 'new-secret-value')
    })
    const createButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Create Secret')
    await act(async () => createButton?.click())
    await flushPromises()

    expect(apiMock.createProjectSecrets).toHaveBeenCalledWith('project-1', [{
      name: 'CHECKOUT_TOKEN',
      value: 'new-secret-value',
    }])
    expect(container.textContent).not.toContain('new-secret-value')

    const disabledRotate = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Rotate')
    expect(disabledRotate?.disabled).toBe(true)

    await act(async () => {
      root.render(
        <ProjectSecretsPanel
          projectId="project-1"
          projectName="Checkout"
          canCreate
          canRotate
        />,
      )
    })
    await flushPromises()

    const rotateStart = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Rotate')
    await act(async () => rotateStart?.click())
    const rotateValue = container.querySelector<HTMLInputElement>('#rotate-secret-EXISTING_TOKEN')
    act(() => setInput(rotateValue!, 'replacement-value'))
    const rotateConfirm = container.querySelector<HTMLButtonElement>('.project-secret-rotate .btn-primary')
    await act(async () => rotateConfirm?.click())
    await flushPromises()

    expect(apiMock.rotateProjectSecret).toHaveBeenCalledWith('project-1', 'EXISTING_TOKEN', 'replacement-value')
    expect(container.textContent).not.toContain('replacement-value')
  })

  it('honors provider create and replace capabilities independently', async () => {
    apiMock.listProjectSecrets.mockImplementation(async (projectId: string) => ({
      project_id: projectId,
      capabilities: {
        provider: 'selective_provider',
        writable: true,
        create_supported: projectId === 'create-only',
        replace_supported: projectId === 'replace-only',
        source_secret_migration_policy_enabled: false,
      },
      secrets: [{
        project_id: projectId,
        name: 'EXISTING_TOKEN',
        provider: 'selective_provider',
        writable: true,
      }],
      count: 1,
    }))

    await act(async () => {
      root.render(
        <ProjectSecretsPanel
          projectId="replace-only"
          projectName="Replace only"
          canCreate
          canRotate
        />,
      )
    })
    await flushPromises()

    let createInputs = container.querySelectorAll<HTMLInputElement>('.project-secret-create input')
    act(() => {
      setInput(createInputs[0], 'NEW_TOKEN')
      setInput(createInputs[1], 'new-value')
    })
    let createButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Create Secret')
    let rotateButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Rotate')
    expect(createButton?.disabled).toBe(true)
    expect(rotateButton?.disabled).toBe(false)
    expect(container.textContent).toContain('does not support creating secrets')

    await act(async () => {
      root.render(
        <ProjectSecretsPanel
          projectId="create-only"
          projectName="Create only"
          canCreate
          canRotate
        />,
      )
    })
    await flushPromises()

    createInputs = container.querySelectorAll<HTMLInputElement>('.project-secret-create input')
    act(() => {
      setInput(createInputs[0], 'NEW_TOKEN')
      setInput(createInputs[1], 'new-value')
    })
    createButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Create Secret')
    rotateButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Rotate')
    expect(createButton?.disabled).toBe(false)
    expect(rotateButton?.disabled).toBe(true)
    expect(container.textContent).toContain('does not support rotating secrets')
  })

  it('uses each secret provider capability when offering rotation', async () => {
    apiMock.listProjectSecrets.mockResolvedValue({
      project_id: 'project-1',
      capabilities: {
        provider: 'provider_chain',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: false,
      },
      secrets: [
        {
          project_id: 'project-1',
          name: 'ENV_TOKEN',
          provider: 'environment',
          writable: false,
        },
        {
          project_id: 'project-1',
          name: 'DATABASE_TOKEN',
          provider: 'encrypted_database',
          writable: true,
        },
      ],
      count: 2,
    })

    await act(async () => {
      root.render(
        <ProjectSecretsPanel
          projectId="project-1"
          projectName="Checkout"
          canCreate
          canRotate
        />,
      )
    })
    await flushPromises()

    const rows = Array.from(container.querySelectorAll<HTMLElement>('.project-secret-row'))
    const readOnlyRow = rows.find(row => row.textContent?.includes('ENV_TOKEN'))
    const writableRow = rows.find(row => row.textContent?.includes('DATABASE_TOKEN'))
    const readOnlyRotate = readOnlyRow?.querySelector<HTMLButtonElement>('button')
    const writableRotate = writableRow?.querySelector<HTMLButtonElement>('button')

    expect(readOnlyRow?.textContent).toContain('environment · read-only')
    expect(readOnlyRotate?.disabled).toBe(true)
    expect(readOnlyRotate?.title).toContain('read-only provider')
    expect(writableRotate?.disabled).toBe(false)

    await act(async () => writableRotate?.click())
    expect(container.querySelector('#rotate-secret-DATABASE_TOKEN')).not.toBeNull()
    expect(container.querySelector('#rotate-secret-ENV_TOKEN')).toBeNull()
  })

  it('clears stale capability metadata after a load failure and offers a working retry', async () => {
    await act(async () => {
      root.render(
        <ProjectSecretsPanel
          projectId="project-1"
          projectName="Checkout"
          canCreate
          canRotate
        />,
      )
    })
    await flushPromises()
    expect(container.textContent).toContain('encrypted_database')

    apiMock.listProjectSecrets.mockRejectedValueOnce(new Error('secret metadata service unavailable'))
    await act(async () => {
      root.render(
        <ProjectSecretsPanel
          projectId="project-2"
          projectName="Billing"
          canCreate
          canRotate
        />,
      )
    })
    await flushPromises()

    expect(container.textContent).toContain('secret metadata service unavailable')
    expect(container.textContent).toContain('provider unavailable')
    expect(container.textContent).not.toContain('encrypted_database')
    const retry = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Retry secret metadata')
    expect(retry).toBeDefined()

    await act(async () => retry?.click())
    await flushPromises()

    expect(apiMock.listProjectSecrets).toHaveBeenCalledTimes(3)
    expect(container.textContent).toContain('encrypted_database')
    expect(container.textContent).toContain('EXISTING_TOKEN')
    expect(container.textContent).not.toContain('secret metadata service unavailable')
  })

  it('serializes rotation and prevents cancel or another mutation while the request is active', async () => {
    apiMock.listProjectSecrets.mockResolvedValue({
      project_id: 'project-1',
      capabilities: {
        provider: 'encrypted_database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: false,
      },
      secrets: [
        { name: 'FIRST_TOKEN', provider: 'encrypted_database', writable: true },
        { name: 'SECOND_TOKEN', provider: 'encrypted_database', writable: true },
      ],
      count: 2,
    })
    let resolveRotation: ((value: unknown) => void) | undefined
    apiMock.rotateProjectSecret.mockReturnValueOnce(new Promise(resolve => {
      resolveRotation = resolve
    }))
    await act(async () => {
      root.render(
        <ProjectSecretsPanel
          projectId="project-1"
          projectName="Checkout"
          canCreate
          canRotate
        />,
      )
    })
    await flushPromises()

    let rows = Array.from(container.querySelectorAll<HTMLElement>('.project-secret-row'))
    act(() => rows[0].querySelector<HTMLButtonElement>('button')?.click())
    const rotateValue = container.querySelector<HTMLInputElement>('#rotate-secret-FIRST_TOKEN')!
    act(() => setInput(rotateValue, 'replacement-value'))
    const confirm = container.querySelector<HTMLButtonElement>('.project-secret-rotate .btn-primary')!
    act(() => confirm.click())

    rows = Array.from(container.querySelectorAll<HTMLElement>('.project-secret-row'))
    const secondRotate = rows[1].querySelector<HTMLButtonElement>('button')!
    const cancel = container.querySelector<HTMLButtonElement>('.project-secret-rotate .btn-ghost')!
    expect(apiMock.rotateProjectSecret).toHaveBeenCalledTimes(1)
    expect(secondRotate.disabled).toBe(true)
    expect(cancel.disabled).toBe(true)
    expect(rotateValue.disabled).toBe(true)
    act(() => {
      secondRotate.click()
      cancel.click()
    })
    expect(apiMock.rotateProjectSecret).toHaveBeenCalledTimes(1)
    expect(container.querySelector('#rotate-secret-FIRST_TOKEN')).not.toBeNull()

    await act(async () => {
      resolveRotation?.({ project_id: 'project-1', secret: { name: 'FIRST_TOKEN' } })
      await Promise.resolve()
    })
    await flushPromises()
    expect(container.querySelector('#rotate-secret-FIRST_TOKEN')).toBeNull()
  })

  it('associates create-name validation with the input and announces it', async () => {
    await act(async () => {
      root.render(
        <ProjectSecretsPanel
          projectId="project-1"
          projectName="Checkout"
          canCreate
          canRotate
        />,
      )
    })
    await flushPromises()

    const nameInput = container.querySelector<HTMLInputElement>('.project-secret-create input')!
    act(() => setInput(nameInput, 'INVALID/NAME'))
    const errorId = nameInput.getAttribute('aria-describedby')

    expect(nameInput.getAttribute('aria-invalid')).toBe('true')
    expect(errorId).toBeTruthy()
    const fieldError = document.getElementById(errorId!)
    expect(fieldError?.getAttribute('role')).toBe('alert')
    expect(fieldError?.textContent).toContain('Use 1–249 uppercase letters')
  })

  it('shows timeout guidance when create secret request times out', async () => {
    apiMock.createProjectSecrets.mockRejectedValueOnce(new Error('Request timed out after 15000ms'))

    await act(async () => {
      root.render(
        <ProjectSecretsPanel
          projectId="project-1"
          projectName="Checkout"
          canCreate
          canRotate
        />,
      )
    })
    await flushPromises()

    const createInputs = container.querySelectorAll<HTMLInputElement>('.project-secret-create input')
    act(() => {
      setInput(createInputs[0], 'TIMEOUT_SECRET')
      setInput(createInputs[1], 'timeout-sensitive-value')
    })
    const createButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Create Secret')
    await act(async () => createButton?.click())
    await flushPromises()

    expect(container.textContent).toContain('Request timed out after 15000ms')
    expect(container.textContent).toContain('Secret operation timed out. The request was canceled to avoid hanging on Creating....')
    expect(container.textContent).toContain('Reconcile secret status')
  })

  it('adopts a durable create reported through a partial failure without inviting a retry', async () => {
    apiMock.createProjectSecrets.mockRejectedValueOnce(new ApiError(
      'project secret was created but its audit outcome could not be persisted; refresh secret status before retrying',
      503,
      {
        partial: true,
        created: [{
          project_id: 'project-1',
          name: 'AUDIT_CREATE',
          provider: 'encrypted_database',
          writable: true,
          updated_at: '2026-07-03T12:00:00Z',
        }],
      },
    ))

    await act(async () => root.render(<ProjectSecretsPanel projectId="project-1" projectName="Checkout" canCreate canRotate />))
    await flushPromises()
    const inputs = container.querySelectorAll<HTMLInputElement>('.project-secret-create input')
    act(() => {
      setInput(inputs[0], 'AUDIT_CREATE')
      setInput(inputs[1], 'sensitive-value')
    })
    const create = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Create Secret')
    await act(async () => create?.click())
    await flushPromises()

    expect(container.textContent).toContain('AUDIT_CREATE')
    expect(container.textContent).toContain('audit persistence warning')
    expect(inputs[0].value).toBe('')
    expect(inputs[1].value).toBe('')
    expect(container.textContent).not.toContain('Reconcile secret status')
    expect(apiMock.createProjectSecrets).toHaveBeenCalledTimes(1)
  })

  it('reconciles an ambiguous rotation from newer metadata and clears the write-only value', async () => {
    const initial = await apiMock.listProjectSecrets()
    apiMock.listProjectSecrets
      .mockResolvedValueOnce(initial)
      .mockResolvedValueOnce({
        ...initial,
        secrets: initial.secrets.map((secret: { name: string }) => secret.name === 'EXISTING_TOKEN'
          ? { ...secret, updated_at: '2026-07-04T12:00:00Z' }
          : secret),
      })
    apiMock.rotateProjectSecret.mockRejectedValueOnce(new ApiError(
      'project secret was rotated but its audit outcome could not be persisted; refresh secret status before retrying',
      503,
    ))

    await act(async () => root.render(<ProjectSecretsPanel projectId="project-1" projectName="Checkout" canCreate canRotate />))
    await flushPromises()
    const rotate = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Rotate')
    await act(async () => rotate?.click())
    const value = container.querySelector<HTMLInputElement>('#rotate-secret-EXISTING_TOKEN')!
    act(() => setInput(value, 'replacement-value'))
    const submit = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Rotate')
    await act(async () => submit?.click())
    await flushPromises()

    expect(container.textContent).toContain('operation may have committed')
    expect(container.querySelector('#rotate-secret-EXISTING_TOKEN')).toBeNull()
    expect(apiMock.rotateProjectSecret).toHaveBeenCalledTimes(1)
  })

  it('registers write-only drafts with the global navigation guard', async () => {
    let registration: { guard: NavigationGuard; dirty: boolean } | undefined
    await act(async () => {
      root.render(
        <NavigationGuardRegisterContext.Provider value={(guard, dirty) => {
          registration = { guard, dirty }
          return () => undefined
        }}>
          <ProjectSecretsPanel projectId="project-1" projectName="Checkout" canCreate canRotate />
        </NavigationGuardRegisterContext.Provider>,
      )
    })
    await flushPromises()
    const inputs = container.querySelectorAll<HTMLInputElement>('.project-secret-create input')
    act(() => setInput(inputs[1], 'write-only-draft'))
    await flushPromises()
    expect(registration?.dirty).toBe(true)

    act(() => registration?.guard.discard())
    expect(inputs[1].value).toBe('')
  })

  it('clears write-only values and operation messages when the selected project changes', async () => {
    apiMock.listProjectSecrets.mockImplementation(async (projectId: string) => ({
      project_id: projectId,
      capabilities: {
        provider: 'encrypted_database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: false,
      },
      secrets: [{
        project_id: projectId,
        name: 'EXISTING_TOKEN',
        provider: 'encrypted_database',
        writable: true,
      }],
      count: 1,
    }))

    await act(async () => {
      root.render(
        <ProjectSecretsPanel
          projectId="project-1"
          projectName="Checkout"
          canCreate
          canRotate
        />,
      )
    })
    await flushPromises()

    let createInputs = container.querySelectorAll<HTMLInputElement>('.project-secret-create input')
    act(() => {
      setInput(createInputs[0], 'TEMP_TOKEN')
      setInput(createInputs[1], 'first-sensitive-value')
    })
    const createButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Create Secret')
    await act(async () => createButton?.click())
    await flushPromises()
    expect(container.textContent).toContain('TEMP_TOKEN was created')

    createInputs = container.querySelectorAll<HTMLInputElement>('.project-secret-create input')
    const rotateStart = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Rotate')
    act(() => {
      setInput(createInputs[0], 'PENDING_TOKEN')
      setInput(createInputs[1], 'second-sensitive-value')
      rotateStart?.click()
    })
    const rotateValue = container.querySelector<HTMLInputElement>('#rotate-secret-EXISTING_TOKEN')
    act(() => setInput(rotateValue!, 'replacement-sensitive-value'))

    await act(async () => {
      root.render(
        <ProjectSecretsPanel
          projectId="project-2"
          projectName="Billing"
          canCreate
          canRotate
        />,
      )
    })
    await flushPromises()

    createInputs = container.querySelectorAll<HTMLInputElement>('.project-secret-create input')
    expect(createInputs[0].value).toBe('')
    expect(createInputs[1].value).toBe('')
    expect(container.querySelector('.project-secret-rotate')).toBeNull()
    expect(container.textContent).not.toContain('TEMP_TOKEN was created')
    expect(apiMock.listProjectSecrets).toHaveBeenLastCalledWith('project-2')
  })

  it('keeps last-good metadata and the committed secret when reconciliation fails', async () => {
    apiMock.listProjectSecrets
      .mockResolvedValueOnce({
        project_id: 'project-1',
        capabilities: {
          provider: 'encrypted_database',
          writable: true,
          create_supported: true,
          replace_supported: true,
          source_secret_migration_policy_enabled: false,
        },
        secrets: [{ name: 'EXISTING_TOKEN', provider: 'encrypted_database', writable: true }],
        count: 1,
      })
      .mockRejectedValueOnce(new Error('metadata service unavailable'))
    apiMock.createProjectSecrets.mockResolvedValueOnce({
      project_id: 'project-1',
      secrets: [{ name: 'NEW_TOKEN', provider: 'encrypted_database', writable: true }],
      count: 2,
    })

    await act(async () => {
      root.render(<ProjectSecretsPanel projectId="project-1" projectName="Checkout" canCreate canRotate />)
    })
    await flushPromises()

    const inputs = container.querySelectorAll<HTMLInputElement>('.project-secret-create input')
    act(() => {
      setInput(inputs[0], 'NEW_TOKEN')
      setInput(inputs[1], 'secret-value')
    })
    const create = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Create Secret')
    await act(async () => create?.click())
    await flushPromises()

    expect(container.textContent).toContain('EXISTING_TOKEN')
    expect(container.textContent).toContain('NEW_TOKEN')
    expect(container.textContent).toContain('metadata service unavailable')
    expect(container.textContent).toContain('Retry secret metadata')
    expect(apiMock.createProjectSecrets).toHaveBeenCalledTimes(1)
  })

  it('keeps a committed secret when the follow-up metadata response is stale', async () => {
    const staleCatalog = {
      project_id: 'project-1',
      capabilities: {
        provider: 'encrypted_database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: false,
      },
      secrets: [{ name: 'EXISTING_TOKEN', provider: 'encrypted_database', writable: true, updated_at: '2026-07-01T00:00:00Z' }],
      count: 1,
    }
    apiMock.listProjectSecrets.mockResolvedValue(staleCatalog)
    apiMock.createProjectSecrets.mockResolvedValueOnce({
      project_id: 'project-1',
      secrets: [{ name: 'NEW_TOKEN', provider: 'encrypted_database', writable: true, updated_at: '2026-07-03T00:00:00Z' }],
      count: 2,
    })
    await act(async () => root.render(<ProjectSecretsPanel projectId="project-1" projectName="Checkout" canCreate canRotate />))
    await flushPromises()

    const inputs = container.querySelectorAll<HTMLInputElement>('.project-secret-create input')
    act(() => {
      setInput(inputs[0], 'NEW_TOKEN')
      setInput(inputs[1], 'secret-value')
    })
    const create = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Create Secret')
    await act(async () => create?.click())
    await flushPromises()

    expect(apiMock.listProjectSecrets).toHaveBeenCalledTimes(2)
    expect(container.textContent).toContain('EXISTING_TOKEN')
    expect(container.textContent).toContain('NEW_TOKEN')
  })

  it('ignores an old mutation completion after an A to B to A cycle', async () => {
    let resolveOldCreate!: (value: unknown) => void
    const oldCreate = new Promise(resolve => { resolveOldCreate = resolve })
    apiMock.createProjectSecrets
      .mockReturnValueOnce(oldCreate)
      .mockRejectedValueOnce(new Error('new A write failed'))
    apiMock.listProjectSecrets.mockImplementation(async (projectId: string) => ({
      project_id: projectId,
      capabilities: {
        provider: 'encrypted_database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: false,
      },
      secrets: [{ name: 'EXISTING_TOKEN', provider: 'encrypted_database', writable: true }],
      count: 1,
    }))

    await act(async () => root.render(<ProjectSecretsPanel projectId="project-1" projectName="A" canCreate canRotate />))
    await flushPromises()
    let inputs = container.querySelectorAll<HTMLInputElement>('.project-secret-create input')
    act(() => {
      setInput(inputs[0], 'OLD_A')
      setInput(inputs[1], 'old-value')
    })
    let create = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Create Secret')
    act(() => create?.click())

    await act(async () => root.render(<ProjectSecretsPanel projectId="project-2" projectName="B" canCreate canRotate />))
    await flushPromises()
    await act(async () => root.render(<ProjectSecretsPanel projectId="project-1" projectName="A" canCreate canRotate />))
    await flushPromises()
    inputs = container.querySelectorAll<HTMLInputElement>('.project-secret-create input')
    act(() => {
      setInput(inputs[0], 'NEW_A')
      setInput(inputs[1], 'new-value')
    })
    create = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Create Secret')
    await act(async () => create?.click())
    await flushPromises()
    expect(container.textContent).toContain('new A write failed')

    await act(async () => {
      resolveOldCreate({
        project_id: 'project-1',
        secrets: [{ name: 'OLD_A', provider: 'encrypted_database', writable: true }],
        count: 2,
      })
      await Promise.resolve()
    })

    inputs = container.querySelectorAll<HTMLInputElement>('.project-secret-create input')
    expect(inputs[0].value).toBe('NEW_A')
    expect(inputs[1].value).toBe('new-value')
    expect(container.textContent).toContain('new A write failed')
    expect(container.textContent).not.toContain('OLD_A was created')
  })

  it('rejects a stale A response after an A to B to A scope cycle', async () => {
    let resolveFirstA!: (value: unknown) => void
    const firstA = new Promise(resolve => { resolveFirstA = resolve })
    let aCalls = 0
    apiMock.listProjectSecrets.mockImplementation((projectId: string) => {
      if (projectId === 'project-1') {
        aCalls += 1
        if (aCalls === 1) return firstA
        return Promise.resolve({
          project_id: projectId,
          capabilities: { provider: 'latest-provider', writable: true, create_supported: true, replace_supported: true },
          secrets: [{ name: 'LATEST_A', provider: 'latest-provider', writable: true }],
          count: 1,
        })
      }
      return Promise.resolve({
        project_id: projectId,
        capabilities: { provider: 'provider-b', writable: true, create_supported: true, replace_supported: true },
        secrets: [{ name: 'PROJECT_B', provider: 'provider-b', writable: true }],
        count: 1,
      })
    })

    await act(async () => root.render(<ProjectSecretsPanel projectId="project-1" projectName="A" canCreate canRotate />))
    await flushPromises()
    await act(async () => root.render(<ProjectSecretsPanel projectId="project-2" projectName="B" canCreate canRotate />))
    await flushPromises()
    await act(async () => root.render(<ProjectSecretsPanel projectId="project-1" projectName="A" canCreate canRotate />))
    await flushPromises()
    expect(container.textContent).toContain('LATEST_A')

    await act(async () => {
      resolveFirstA({
        project_id: 'project-1',
        capabilities: { provider: 'stale-provider', writable: true, create_supported: true, replace_supported: true },
        secrets: [{ name: 'STALE_A', provider: 'stale-provider', writable: true }],
        count: 1,
      })
      await Promise.resolve()
    })
    expect(container.textContent).toContain('LATEST_A')
    expect(container.textContent).not.toContain('STALE_A')
  })
})
