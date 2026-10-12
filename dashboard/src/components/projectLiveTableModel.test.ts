import { expect, it } from 'vitest'
import { decimalInteger, projectTableCreateErrors, projectTableLifecycleBlock } from './projectLiveTableModel'
import type { ProjectLiveTableView } from '../types/projectLiveTables'

it('validates bounded declared schema and capacity', () => {
  const draft = { name: 'Work', profile: 'native_queue' as const, columns: ['id'], limits: { max_rows: 10, max_bytes: 1024 } }
  expect(projectTableCreateErrors(draft)).toEqual([])
  expect(projectTableCreateErrors({ ...draft, columns: ['id', 'id'], limits: { max_rows: 0, max_bytes: 8388609 } })).toHaveLength(3)
})
it.each(['0', '9007199254740993', '9223372036854775807', '-9223372036854775808'])('accepts int64 without coercing %s to a number', value => expect(decimalInteger(value)).toBe(true))
it.each(['', '-0', '01', '1.5', '1e3', '9223372036854775808', '-9223372036854775809'])('rejects noncanonical or overflowing integer %s', value => expect(decimalInteger(value)).toBe(false))
it('gates destructive lifecycle on explicit authority and quiescence', () => {
  const table: ProjectLiveTableView = { id: 't', project_id: 'p', name: 'Work', profile: 'native_queue', columns: ['id'], state: 'quiesced', config_revision: '1', instance_id: 'i', stats: { revision: '9007199254740993', counts: { leased: 0 } }, permissions: { can_view: true, can_mutate: true, can_admin: true }, attachments: [] }
  expect(projectTableLifecycleBlock(table, 'reset')).toBeNull()
  expect(projectTableLifecycleBlock({ ...table, state: 'active' }, 'reset')).toContain('Quiesce')
  expect(projectTableLifecycleBlock({ ...table, attachments: [{ run_id: 'r', group_name: 'g', alias: 'work', state: 'active' }] }, 'archive')).toContain('attachments')
  expect(projectTableLifecycleBlock({ ...table, attachments: [{ run_id: 'r', group_name: 'g', alias: 'work', state: 'detached' }] }, 'reset')).toContain('retry windows')
  expect(projectTableLifecycleBlock({ ...table, permissions: { ...table.permissions, can_admin: false } }, 'archive')).toContain('administrator')
})
