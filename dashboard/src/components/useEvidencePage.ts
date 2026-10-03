import { useEffect, useState } from 'react'

// Bind navigation to the report identity so replaced/cleared evidence cannot
// retain a page from the previous report, including while the effect resets it.
export function useEvidencePage(identity: unknown) {
  const [selection, setSelection] = useState({ identity, page: 0 })
  useEffect(() => { setSelection({ identity, page: 0 }) }, [identity])
  return [selection.identity === identity ? selection.page : 0, (page: number) => setSelection({ identity, page })] as const
}
