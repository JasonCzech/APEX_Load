import type { Page } from '@playwright/test'

// The dashboard states moments with formatDateTime and times of day with
// formatTimeOfDay, in the browser's own locale and zone. These format the same
// moment in the page, so a spec asserts what the dashboard shows wherever the
// suite runs.
export function pageDateTime(page: Page, iso: string): Promise<string> {
  return page.evaluate(value => new Date(value).toLocaleString(undefined, { year: 'numeric', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit', second: '2-digit', timeZoneName: 'short' }), iso)
}

export function pageClock(page: Page, iso: string): Promise<string> {
  return page.evaluate(value => new Date(value).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit', second: '2-digit' }), iso)
}
