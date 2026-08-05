import { describe, expect, it } from 'vitest'

import { collectJsonViewerVisiblePaths } from './jsonViewerModel'

describe('collectJsonViewerVisiblePaths', () => {
  it('keeps the full contents of a container whose key matches', () => {
    const paths = collectJsonViewerVisiblePaths({ headers: { authorization: 'masked', nested: ['one'] }, body: 'ok' }, 'headers')
    expect(paths).toEqual(new Set(['$.headers', '$.headers.authorization', '$.headers.nested', '$.headers.nested[0]', '$']))
  })
})
