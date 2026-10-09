import { useEffect, useState } from 'react'
import { parseAuthHosts } from './authHosts'

// AuthHostsField edits config.http.auth.hosts as a comma-separated list. The
// draft text is kept locally so typing a trailing comma does not reformat the
// field; the parsed list is committed on every change.
export default function AuthHostsField({ hosts, onChange }: { hosts?: string[]; onChange: (hosts: string[] | undefined) => void }) {
  const joined = (hosts || []).join(', ')
  const [draft, setDraft] = useState(joined)
  useEffect(() => {
    setDraft(current => (parseAuthHosts(current).join(', ') === joined ? current : joined))
  }, [joined])
  return (
    <label className="full-width"><span>Send credentials only to hosts</span>
      <input
        aria-label="Auth hosts"
        value={draft}
        placeholder="Every host (e.g. shop.example.com, api.example.com:8443)"
        onChange={event => {
          setDraft(event.target.value)
          const parsed = parseAuthHosts(event.target.value)
          onChange(parsed.length > 0 ? parsed : undefined)
        }}
      />
    </label>
  )
}
