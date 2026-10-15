import { describe, expect, it } from 'vitest'

import { parseAppViewFromHash, viewToLocationHash } from '../appRouting'
import { desktopActionView } from './desktopActions'

describe('desktopActionView', () => {
  // The home Import card and File > Import… open the importer picker, and
  // Record the recorder, rather than the script library.
  it('opens the import and record pickers', () => {
    const imports = desktopActionView('import')!
    expect(parseAppViewFromHash(viewToLocationHash(imports))).toMatchObject({ page: 'scripts', mode: 'create', workflow: 'source' })
    const record = desktopActionView('record')!
    expect(parseAppViewFromHash(viewToLocationHash(record))).toMatchObject({ page: 'scripts', mode: 'record' })
    expect(desktopActionView('new-test')).toEqual({ page: 'new-test' })
  })

  it('leaves plain page changes to the caller', () => {
    expect(desktopActionView('scripts')).toBeNull()
    expect(desktopActionView('help')).toBeNull()
    expect(desktopActionView('open-script')).toBeNull()
  })
})
