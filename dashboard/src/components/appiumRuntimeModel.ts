import type { AppiumConfig } from '../types/scripts'

/** One line naming the device an Appium script drives. */
export function appiumDeviceSummary(config?: AppiumConfig): string {
  const capabilities = config?.capabilities || {}
  const platform = typeof capabilities.platformName === 'string' ? capabilities.platformName : ''
  const device = typeof capabilities['appium:deviceName'] === 'string' ? capabilities['appium:deviceName'] : ''
  const where = config?.server_url ? serverHost(config.server_url) : 'server pending'
  return [platform, device, where].filter(Boolean).join(' · ')
}

function serverHost(serverURL: string): string {
  try {
    return new URL(serverURL).host || serverURL
  } catch {
    return serverURL
  }
}
