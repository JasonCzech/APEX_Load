import { type HTMLAttributes } from 'react'
import { handleTabKey } from '../tabKeyboard'
import './ux.css'

/** Page/content navigation shares semantics; document closing and wizard steps stay separate. */
export default function TabList({ children, onKeyDown, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div tabIndex={-1} {...props} role="tablist" onKeyDown={event => { onKeyDown?.(event); if (!event.defaultPrevented) handleTabKey(event) }}>{children}</div>
}
