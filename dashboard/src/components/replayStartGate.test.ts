import { describe, expect, it } from 'vitest'
import type { ReplayPreflight } from '../types/scripts'
import { replayReadinessIssues } from './replayReadinessIssues'
import {
  coveredByAuthoring,
  isPromotionOnlyValidationIssue,
  promotionFoldCoveredByGate,
  promotionReviewText,
  replayBlockers,
  replayGateAction,
  replayStartBlockedReason,
  replayStartGate,
  storedGateProblemCount,
  type ReplayStartGateInput,
} from './replayStartGate'

const preflight = (overrides: Partial<ReplayPreflight>): ReplayPreflight => ({ valid: false, action_count: 3, expected_action_count: 3, data_source_count: 0, parameter_count: 0, ...overrides })
const tls = (index: number) => `init[${index}] "web_set_sockets_option_${index}": execution blocker legacy_tls_replacement blocks replay until the imported behavior is resolved`
const ready: ReplayStartGateInput = { replaying: false, saving: false, hasName: true, actionCount: 2, jsonInvalid: false, secretStatus: 'known', blockers: [], storedGateIssues: [] }

describe('replayBlockers', () => {
  it('counts each readiness blocker the server refuses a start for, by source', () => {
    const cases: Array<[string, Partial<ReplayPreflight>]> = [
      ['review gate', { issues: [tls(0)] }],
      ['unresolved value', { missing_variables: ['token'], blocking_variables: ['token'], placeholder_variables: [] }],
      ['data source', { data_source_issues: ['file data source "users" requires an import file binding before replay'] }],
      ['missing secret', { missing_secrets: ['API_TOKEN'] }],
    ]
    for (const [source, facts] of cases) {
      const blockers = replayBlockers(replayReadinessIssues(preflight(facts)), [])
      expect({ source, count: blockers.length }).toEqual({ source, count: 1 })
      expect(replayStartGate({ ...ready, blockers })).toMatchObject({ kind: 'blocked', blockers })
    }
  })

  it('keeps the row id, location and repair target of a gate', () => {
    const [blocker] = replayBlockers(replayReadinessIssues(preflight({ issues: [tls(2)] })), [])
    expect(blocker).toEqual({
      id: `readiness:issue:${tls(2).toLowerCase()}`,
      title: 'Choose supported TLS settings',
      description: 'The source uses legacy TLS behavior. Choose a supported replacement and preview the migration. Debug and test runs refuse this script until it is resolved.',
      source: 'readiness',
      location: { phase: 'init', actionIndex: 2 },
      codes: ['legacy_tls_replacement'],
      actionPath: 'init[2]',
    })
  })

  it('never counts placeholders, warnings or values only sent as placeholder text', () => {
    const readiness = replayReadinessIssues(preflight({
      valid: true,
      missing_variables: ['c_storeid'],
      blocking_variables: [],
      placeholder_variables: ['c_storeid'],
      warnings: ['Parameter p references missing data source d.'],
    }))
    expect(readiness.map(issue => issue.severity)).toEqual(['warning', 'warning'])
    expect(replayBlockers(readiness, [])).toEqual([])
    expect(replayStartGate({ ...ready, blockers: replayBlockers(readiness, []) })).toEqual({ kind: 'ready' })
  })

  it('counts current server validation issues as Author Problems lists them, except promotion-only ones', () => {
    const issues = ['import fidelity blocker: Imported script is marked not promotable to executable.', 'actions[1]: target is required']
    expect(replayBlockers([], issues)).toEqual([{ id: 'server-1', title: 'actions[1]: target is required', source: 'validation', location: { phase: 'action', actionIndex: 1 } }])
    // A promotion fold alone never refuses a debug start.
    expect(replayStartGate({ ...ready, blockers: replayBlockers([], [issues[0]]) })).toEqual({ kind: 'ready' })
    expect(isPromotionOnlyValidationIssue(` ${issues[0]}`)).toBe(true)
    expect(isPromotionOnlyValidationIssue('actions[0]: import fidelity blocker in a name')).toBe(false)
    expect(promotionReviewText(issues[0])).toEqual({
      title: 'Import review: Imported script is marked not promotable to executable.',
      description: 'Promotion and load-test launch hold this imported script for review. Debug runs are not refused for it.',
    })
  })

  it('lists a static issue the editor\'s own check reports as that check, once', () => {
    const readiness = replayReadinessIssues(preflight({ issues: ['actions[2] "Login": missing target', tls(0)] }))
    const local = { id: 'action:2:target', title: 'Action target is required for this protocol step' }
    const blockers = coveredByAuthoring([...replayBlockers(readiness, []), ...replayBlockers(readiness, []).slice(0, 1)], blocker => blocker.message?.includes('missing target') ? local : undefined)
    expect(blockers.map(blocker => [blocker.id, blocker.title, blocker.source])).toEqual([
      ['action:2:target', 'Action target is required for this protocol step', 'authoring'],
      [`readiness:issue:${tls(0).toLowerCase()}`, 'Choose supported TLS settings', 'readiness'],
    ])
    expect(blockers[0].location).toEqual({ phase: 'action', actionIndex: 2 })
  })

  it('leaves out a promotion fold that restates a gate or a protected body the editor lists, as the import review does', () => {
    const codes = new Set(['legacy_tls_replacement', 'restored_body_required'])
    expect(promotionFoldCoveredByGate('import fidelity blocker: legacy_tls_replacement at init[2]', codes)).toBe(true)
    // A guided repair writes the bare code.
    expect(promotionFoldCoveredByGate('import fidelity blocker: legacy_tls_replacement', codes)).toBe(true)
    expect(promotionFoldCoveredByGate('import fidelity blocker: credential_bindings_required', new Set())).toBe(true)
    expect(promotionFoldCoveredByGate('import fidelity blocker: One or more lr_unmask request bodies require governed secret resolution before executable promotion.', codes)).toBe(true)
    expect(promotionFoldCoveredByGate('import fidelity blocker: One or more lr_unmask request bodies require governed secret resolution before executable promotion.', new Set(['legacy_tls_replacement']))).toBe(false)
    // Archive sentences and the not-promotable marker are listed as review notes.
    expect(promotionFoldCoveredByGate('import fidelity blocker: One or more response contributors or one-shot headers occur after the final request and cannot be bound faithfully.', codes)).toBe(false)
    expect(promotionFoldCoveredByGate('import fidelity blocker: Imported script is marked not promotable to executable.', codes)).toBe(false)
    expect(promotionFoldCoveredByGate('import fidelity blocker: encrypted_proxy_password at init[0]', codes)).toBe(false)
    // Only a promotion fold is ever left out.
    expect(promotionFoldCoveredByGate('init[0]: legacy_tls_replacement blocks replay', codes)).toBe(false)
  })

  it('lists the review codes a repaired report is held for once, by the codes the editor does not list already', () => {
    // pkg/internal/importreview unnamedPromotionBlocker, folded by validation.
    const held = 'import fidelity blocker: Imported script needs import review before promotion: conditional_producer:store, missing_producer:cart.'
    expect(replayBlockers([], [held])).toEqual([])
    expect(replayStartGate({ ...ready, blockers: replayBlockers([], [held]) })).toEqual({ kind: 'ready' })
    // Every code it names is a finding the editor lists: the same problems, listed once.
    expect(promotionFoldCoveredByGate(held, new Set(['missing_producer:cart', 'conditional_producer:store', 'legacy_tls_replacement']))).toBe(true)
    // A code the draft no longer carries keeps it a review note, and only that
    // code is named; a longer name is not the same code.
    const listed = new Set(['missing_producer:cart', 'conditional_producer:storefront'])
    expect(promotionFoldCoveredByGate(held, listed)).toBe(false)
    expect(promotionReviewText(held, listed)).toEqual({
      title: 'Import review: Check producer order: store',
      description: 'Promotion and load-test launch hold this imported script for review of conditional_producer:store. Debug runs are not refused for it.',
    })
    expect(promotionReviewText(held)).toEqual({
      title: 'Import review: Check producer order: store; Missing value: cart',
      description: 'Promotion and load-test launch hold this imported script for review of conditional_producer:store, missing_producer:cart. Debug runs are not refused for it.',
    })
  })
})

describe('replayStartGate', () => {
  it('is ready when nothing the server refuses for is known; authoring checks are not an input', () => {
    expect(replayStartGate(ready)).toEqual({ kind: 'ready' })
  })

  it('waits, disabled as before, while a run or save is in flight, the script is incomplete or its JSON does not parse', () => {
    expect(replayStartGate({ ...ready, replaying: true })).toMatchObject({ kind: 'waiting', cause: 'busy' })
    expect(replayStartGate({ ...ready, saving: true })).toMatchObject({ kind: 'waiting', cause: 'busy' })
    expect(replayStartGate({ ...ready, hasName: false })).toMatchObject({ kind: 'waiting', cause: 'incomplete' })
    expect(replayStartGate({ ...ready, actionCount: 0 })).toMatchObject({ kind: 'waiting', cause: 'incomplete' })
    expect(replayStartGate({ ...ready, jsonInvalid: true, blockers: replayBlockers(replayReadinessIssues(preflight({ issues: [tls(0)] })), []) })).toMatchObject({ kind: 'waiting', cause: 'json' })
  })

  it('waits for an unknown or unavailable secret status instead of counting it', () => {
    expect(replayStartGate({ ...ready, secretStatus: 'checking' })).toEqual({ kind: 'waiting', cause: 'secrets', reason: 'Checking project secret configuration before running…' })
    expect(replayStartGate({ ...ready, secretStatus: 'unavailable', secretStatusReason: 'Retry the credential check.' })).toEqual({ kind: 'waiting', cause: 'secrets', reason: 'Retry the credential check.' })
  })

  it('asks for a save when only the saved copy is gated, and blocks first when the draft is too', () => {
    expect(replayStartGate({ ...ready, storedGateIssues: [tls(0), tls(2)] })).toEqual({ kind: 'save-first', storedGateIssues: [tls(0), tls(2)] })
    const blockers = replayBlockers(replayReadinessIssues(preflight({ issues: [tls(0)] })), [])
    expect(replayStartGate({ ...ready, blockers, storedGateIssues: [tls(0), tls(2)] })).toMatchObject({ kind: 'blocked' })
  })

  it('saves first when the only blockers are review codes for values the unsaved changes supply', () => {
    const gate = (path: string) => `${path} "step": execution blocker missing_producer:sid blocks replay until the imported behavior is resolved`
    const stored = [gate('actions[0]'), gate('actions[1].control.steps[0]')]
    // The draft supplies sid, so the preflight no longer reports it missing.
    const saved = replayBlockers(replayReadinessIssues(preflight({ issues: stored, missing_variables: [], blocking_variables: [], placeholder_variables: [] })), [])
    expect(saved).toMatchObject([{ id: 'readiness:saved-review:sid', title: 'Save to update the import review for sid', clearedBySave: true }])
    const gated = replayStartGate({ ...ready, blockers: saved, storedGateIssues: stored, unsavedChanges: true })
    expect(gated).toEqual({ kind: 'save-first', storedGateIssues: stored, blockers: saved })
    // One value, counted once, however many gates name it.
    expect(replayGateAction(gated, { hasReport: false, readyTitle: '' })).toMatchObject({ intent: 'save-and-start', label: 'Save and debug', title: 'Your changes are not saved; the saved script still has 1 blocker. Save them, then debug.' })
    expect(replayStartBlockedReason(gated)).toBe('Save your changes before debugging: the saved script still has 1 blocker.')
    // Counted from the draft's rows when the saved copy has no gate to count.
    expect(replayStartBlockedReason(replayStartGate({ ...ready, blockers: saved, unsavedChanges: true }))).toBe('Save your changes before debugging: the saved script still has 1 blocker.')
    // Without unsaved changes a save sends nothing new, so it is a blocker to resolve.
    expect(replayStartGate({ ...ready, blockers: saved, storedGateIssues: stored })).toMatchObject({ kind: 'blocked', blockers: saved })
    // So it is beside any other blocker.
    const others = replayBlockers(replayReadinessIssues(preflight({ issues: [tls(0)] })), [])
    expect(replayStartGate({ ...ready, blockers: [...saved, ...others], unsavedChanges: true })).toMatchObject({ kind: 'blocked' })
    // A save the editor cannot make now waits, and says why.
    expect(replayStartGate({ ...ready, blockers: saved, unsavedChanges: true, saveUnavailableReason: 'Close Secret Bindings before saving and debugging.' })).toEqual({ kind: 'waiting', cause: 'save', reason: 'Close Secret Bindings before saving and debugging.' })
  })

  it('waits and says why when the saved copy is gated but the editor cannot save', () => {
    const reason = 'Load and review the current version of the script before saving and debugging.'
    const gate = replayStartGate({ ...ready, storedGateIssues: [tls(0)], saveUnavailableReason: reason })
    expect(gate).toEqual({ kind: 'waiting', cause: 'save', reason })
    // Disabled, titled with the reason; Step and Run to say the same.
    expect(replayGateAction(gate, { hasReport: false, readyTitle: 'Run Replay' })).toMatchObject({ intent: 'start', label: 'Debug one iteration', disabled: true, title: reason })
    expect(replayStartBlockedReason(gate)).toBe(reason)
    // Only a save-first start waits for it: a ready or blocked draft does not.
    expect(replayStartGate({ ...ready, saveUnavailableReason: reason })).toEqual({ kind: 'ready' })
    const blockers = replayBlockers(replayReadinessIssues(preflight({ issues: [tls(0)] })), [])
    expect(replayStartGate({ ...ready, blockers, storedGateIssues: [tls(0)], saveUnavailableReason: reason })).toMatchObject({ kind: 'blocked' })
  })
})

describe('replayGateAction', () => {
  const blockers = replayBlockers(replayReadinessIssues(preflight({ issues: [tls(0), tls(2)] })), [])

  it('labels each gate the way the Debug button reads', () => {
    expect(replayGateAction({ kind: 'ready' }, { hasReport: false, readyTitle: 'Run Replay' })).toEqual({ intent: 'start', label: 'Debug one iteration', compactLabel: 'Debug', title: 'Run Replay', tone: 'success', icon: 'run', disabled: false })
    expect(replayGateAction({ kind: 'ready' }, { hasReport: true, readyTitle: 'Run Replay' }).label).toBe('Debug again')
    expect(replayGateAction({ kind: 'waiting', cause: 'secrets', reason: 'Checking' }, { hasReport: false, readyTitle: 'Checking project secret configuration before running…' })).toMatchObject({ intent: 'start', disabled: true, title: 'Checking project secret configuration before running…' })
    expect(replayGateAction({ kind: 'blocked', blockers }, { hasReport: false, readyTitle: 'Run Replay' })).toEqual({
      intent: 'resolve',
      label: 'Resolve 2 blockers',
      compactLabel: 'Resolve',
      title: 'Choose supported TLS settings and 1 more. Debug and test runs refuse this script until it is resolved.',
      ariaLabel: 'Resolve 2 blockers that refuse replay',
      tone: 'danger',
      icon: 'blocker',
      disabled: false,
    })
    expect(replayGateAction({ kind: 'blocked', blockers: blockers.slice(0, 1) }, { hasReport: false, readyTitle: '' }).label).toBe('Resolve 1 blocker')
    // Speech input activates the control by its visible label, so the
    // accessible name contains it (WCAG 2.5.3 Label in Name).
    for (const gated of [blockers, blockers.slice(0, 1)]) {
      const action = replayGateAction({ kind: 'blocked', blockers: gated }, { hasReport: false, readyTitle: '' })
      expect(action.ariaLabel?.startsWith(action.label)).toBe(true)
    }
    expect(replayGateAction({ kind: 'save-first', storedGateIssues: [tls(0), tls(2)] }, { hasReport: true, readyTitle: '' })).toMatchObject({
      intent: 'save-and-start', label: 'Save and debug', tone: 'success', disabled: false,
      title: 'Your changes are not saved; the saved script still has 2 blockers. Save them, then debug.',
    })
  })

  it('counts the saved copy\'s gates per code, as the blocked state does', () => {
    // One gated step with two codes is two blockers wherever the draft's are counted.
    const twoCodes = 'actions[2] "Submit": execution blocker restored_body_required, request_body_xml_json_content_type_mismatch blocks replay until the imported behavior is resolved'
    const bodyReview = 'actions[3] "Upload": execution blocker import_review_required, restored_body_required blocks replay until the imported behavior is resolved'
    expect(replayBlockers(replayReadinessIssues(preflight({ issues: [twoCodes] })), [])).toHaveLength(2)
    expect(storedGateProblemCount([twoCodes, bodyReview, 'actions[4] "Pay": requires review'])).toBe(4)
    // A value is one problem however many gates name it, as the editor lists it.
    const value = (path: string) => `${path} "step": execution blocker missing_producer:sid blocks replay until the imported behavior is resolved`
    expect(storedGateProblemCount([value('actions[0]'), value('actions[1].control.steps[0]'), twoCodes])).toBe(3)
    const gate = replayStartGate({ ...ready, storedGateIssues: [twoCodes] })
    expect(replayStartBlockedReason(gate)).toBe('Save your changes before debugging: the saved script still has 2 blockers.')
    expect(replayGateAction(gate, { hasReport: false, readyTitle: '' }).title).toBe('Your changes are not saved; the saved script still has 2 blockers. Save them, then debug.')
  })

  it('says why the step and run-to controls cannot start a run', () => {
    expect(replayStartBlockedReason({ kind: 'ready' })).toBeUndefined()
    expect(replayStartBlockedReason({ kind: 'blocked', blockers })).toBe('Resolve 2 blockers before debugging.')
    expect(replayStartBlockedReason({ kind: 'save-first', storedGateIssues: [tls(0)] })).toBe('Save your changes before debugging: the saved script still has 1 blocker.')
    expect(replayStartBlockedReason({ kind: 'waiting', cause: 'json', reason: 'Fix the Script JSON before debugging.' })).toBe('Fix the Script JSON before debugging.')
  })
})
