// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '../hooks/apiClient'
import { type SecretBindingRequirement, type SecretBindingSelection } from '../types/scriptImports'
import SecretBindingReview, { type SecretBindingReviewHandle } from './SecretBindingReview'

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

  it('prefills safe references and offers optional write-only values', () => {
    render()

    expect(container.textContent).toContain('Credentials (2)')
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
      .toContain('0 to configure later')
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

    const items = Array.from(container.querySelectorAll<HTMLElement>('.secret-binding-review-item'))
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
    expect(container.textContent).toContain('encrypted_database')
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

    await act(async () => {
      expect(await reviewRef.current?.commit()).toBe(true)
    })
    expect(apiMock.migrateImportSecrets).toHaveBeenCalledTimes(2)
    expect(onApply).toHaveBeenCalledTimes(1)
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

    expect(container.textContent).toContain('Credentials (1)')
    expect(container.textContent).toContain('2 locations')
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

    expect(container.textContent).toContain('Credentials (2)')
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

    expect(container.querySelector('.credentials-card-summary')?.textContent).toContain('0 to configure later')
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
    expect(container.querySelector('.credentials-card-summary')?.textContent).toContain('0 to configure later')

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
    const migratedRow = Array.from(container.querySelectorAll<HTMLElement>('.secret-binding-review-item'))[2]
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
    act(() => applyButton?.click())

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
