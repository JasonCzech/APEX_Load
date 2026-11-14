import { useState } from 'react'

/** One entry per non-empty line, trimmed. */
function listFromText(text: string): string[] {
  return text.split('\n').map(item => item.trim()).filter(Boolean)
}

/**
 * A one-per-line list. The typed text is kept as written while it still
 * stands for the stored list, so Enter starts a new line instead of being
 * dropped when the list is normalized; the stored list is the trimmed,
 * non-empty lines.
 */
export default function ListOverrideTextarea({ label, listed, placeholder, onList }: {
  label: string
  listed: string[] | undefined
  placeholder: string
  onList: (next: string[]) => void
}) {
  const [text, setText] = useState<string | null>(null)
  const current = listed ?? []
  const typed = text !== null && listFromText(text).join('\n') === current.join('\n') ? text : null
  return (
    <textarea
      className="settings-input"
      aria-label={label}
      rows={3}
      value={typed ?? current.join('\n')}
      placeholder={placeholder}
      onBlur={() => setText(null)}
      onChange={event => {
        setText(event.target.value)
        onList(listFromText(event.target.value))
      }}
    />
  )
}
