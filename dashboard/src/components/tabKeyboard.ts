import type { KeyboardEvent } from 'react'

/** Standard horizontal tab navigation; activation uses the existing guarded click. */
export function handleTabKey(event: KeyboardEvent<HTMLElement>) {
  if (event.defaultPrevented) return
  const direction = event.key === 'ArrowRight' || event.key === 'ArrowDown' ? 1 : event.key === 'ArrowLeft' || event.key === 'ArrowUp' ? -1 : 0
  if (!direction && event.key !== 'Home' && event.key !== 'End') return
  const tabs = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="tab"]')).filter(tab => !tab.disabled && tab.closest('[role="tablist"]') === event.currentTarget && !tab.closest('[hidden], [inert]'))
  const index = tabs.indexOf(event.target as HTMLButtonElement)
  if (index < 0 || !tabs.length) return
  event.preventDefault()
  const next = event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : (index + direction + tabs.length) % tabs.length
  tabs[next].focus()
  tabs[next].click()
  tabs[next].scrollIntoView?.({ block: 'nearest', inline: 'nearest' })
}
