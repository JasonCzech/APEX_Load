import { type RuntimeBrowserSettings } from '../types'

function formatOrientation(value?: string): string | null {
  if (!value) return null
  return value.charAt(0).toUpperCase() + value.slice(1)
}

export function formatBrowserRuntimeSummary(browser?: RuntimeBrowserSettings): string | null {
  if (!browser) return null

  const parts = [
    browser.device_profile,
    formatOrientation(browser.orientation),
    browser.touch_mode === undefined ? null : browser.touch_mode ? 'Touch enabled' : 'Touch disabled',
    browser.headless === undefined ? null : browser.headless ? 'Headless' : 'Headed',
  ].filter((value): value is string => Boolean(value))

  return parts.length > 0 ? parts.join(' · ') : null
}