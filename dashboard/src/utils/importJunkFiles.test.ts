import { describe, expect, it } from 'vitest'

import { importJunkDirectoryReason, importJunkReason } from './importJunkFiles'

describe('importJunkReason', () => {
  it('names operating-system and version-control files the way the server does', () => {
    expect(importJunkReason('Script/.DS_Store')).toBe('operating_system_file')
    expect(importJunkReason('__MACOSX/Script/._Action.c')).toBe('operating_system_file')
    expect(importJunkReason('Script/._Action.c')).toBe('operating_system_file')
    expect(importJunkReason('Script\\Thumbs.db')).toBe('operating_system_file')
    expect(importJunkReason('Script/.git/HEAD')).toBe('version_control_file')
    expect(importJunkReason('Script/.gitignore')).toBe('version_control_file')
  })

  it('keeps the script files', () => {
    for (const path of ['Script/Script.usr', 'Script/Action.c', 'Script/data/users.dat', 'Script/.env', 'Script/git.c']) {
      expect(importJunkReason(path)).toBe('')
    }
  })

  it('names bookkeeping directories a folder walk should not enter', () => {
    expect(importJunkDirectoryReason('.git')).toBe('version_control_file')
    expect(importJunkDirectoryReason('__MACOSX')).toBe('operating_system_file')
    expect(importJunkDirectoryReason('scripts')).toBe('')
  })
})
