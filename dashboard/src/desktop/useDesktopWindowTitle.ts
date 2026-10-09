// Mirrors the page title (document.title, e.g. "Results | APEX Load") onto the
// desktop window, so the Window menu, Mission Control, the taskbar and screen
// readers name the page the way a browser tab does. Inert in the browser.

import { useEffect } from 'react'

import { getWailsEventRuntime } from './wailsRuntime'

export function useDesktopWindowTitle() {
  useEffect(() => {
    const setTitle = getWailsEventRuntime()?.WindowSetTitle
    if (typeof setTitle !== 'function' || typeof document === 'undefined') return
    let last = ''
    const sync = () => {
      const title = document.title.trim()
      if (!title || title === last) return
      last = title
      setTitle(title)
    }
    sync()
    // Pages set document.title on their own schedule; follow the <title> element.
    const observer = new MutationObserver(sync)
    observer.observe(document.head, { childList: true, subtree: true, characterData: true })
    return () => observer.disconnect()
  }, [])
}
