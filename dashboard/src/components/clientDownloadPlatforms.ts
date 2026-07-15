export interface DownloadPlatform {
  value: string
  label: string
}

export const CLIENT_DOWNLOAD_PLATFORMS: DownloadPlatform[] = [
  { value: 'darwin-arm64', label: 'macOS (Apple Silicon)' },
  { value: 'darwin-amd64', label: 'macOS (Intel)' },
  { value: 'windows-amd64', label: 'Windows (x64)' },
  { value: 'linux-amd64', label: 'Linux (x64)' },
  { value: 'linux-arm64', label: 'Linux (ARM64)' },
]

interface NavigatorUserAgentData {
  platform?: string
  getHighEntropyValues?: (hints: string[]) => Promise<{ platform?: string; architecture?: string }>
}

export function architecturePlatform(osHint: string, architectureHint: string): string | null {
  const os = osHint.toLowerCase()
  const architecture = architectureHint.toLowerCase()
  if (os.includes('mac')) {
    if (architecture.includes('arm') || architecture.includes('aarch64')) return 'darwin-arm64'
    if (architecture.includes('x86') || architecture.includes('amd64')) return 'darwin-amd64'
    return null
  }
  if (os.includes('win')) return 'windows-amd64'
  if (os.includes('linux')) {
    if (architecture.includes('arm') || architecture.includes('aarch64')) return 'linux-arm64'
    return 'linux-amd64'
  }
  return null
}

export async function detectPlatformFromNavigator(nav?: Navigator): Promise<string | null> {
  const currentNavigator = nav || (typeof navigator === 'undefined' ? null : navigator)
  if (!currentNavigator) return null
  const uaData = (currentNavigator as Navigator & { userAgentData?: NavigatorUserAgentData }).userAgentData
  if (uaData?.getHighEntropyValues) {
    try {
      const values = await uaData.getHighEntropyValues(['architecture', 'platform'])
      const detected = architecturePlatform(values.platform || uaData.platform || '', values.architecture || '')
      if (detected) return detected
    } catch {
      // Fall back to lower-fidelity User-Agent checks below.
    }
  }
  const ua = `${currentNavigator.userAgent || ''} ${currentNavigator.platform || ''}`.toLowerCase()
  if (ua.includes('win')) return 'windows-amd64'
  if (ua.includes('mac')) return null
  if (ua.includes('linux') || ua.includes('android')) return 'linux-amd64'
  return null
}

export function platformLabel(value: string): string {
  return CLIENT_DOWNLOAD_PLATFORMS.find(platform => platform.value === value)?.label || value
}
