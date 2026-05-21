export function parseDurationToSeconds(input?: string): number {
  if (!input) return 0;
  
  // Handles multiple units like "1h 30m" or "90m"
  const regex = /(\d+)(ms|h|m|s)/g;
  let totalSeconds = 0;
  let hasMatch = false;

  const matches = input.matchAll(regex);
  for (const match of matches) {
    hasMatch = true;
    const value = parseInt(match[1], 10);
    const unit = match[2];

    if (unit === 'h') totalSeconds += value * 3600;
    else if (unit === 'm') totalSeconds += value * 60;
    else if (unit === 's') totalSeconds += value;
    else if (unit === 'ms') totalSeconds += value / 1000;
  }

  // Fallback to strict single unit parsing if no multi-matches
  if (!hasMatch) {
    const singleMatch = input.trim().match(/^(\d+)(ms|[smh])$/);
    if (!singleMatch) return 0;
    
    const val = parseInt(singleMatch[1] || '0', 10);
    const u = singleMatch[2] || 's';
    
    if (u === 'h') return val * 3600;
    if (u === 'm') return val * 60;
    if (u === 's') return val;
    if (u === 'ms') return Math.ceil(val / 1000);
  }

  return Math.ceil(totalSeconds);
}

export function formatTimeCompact(seconds: number): string {
  if (!seconds || seconds <= 0) return '0s';
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = seconds % 60;

  if (h > 0) return `${h}h${m > 0 ? `${m}m` : ''}${s > 0 ? `${s}s` : ''}`;
  if (m > 0) return `${m}m${s > 0 ? `${s}s` : ''}`;
  return `${s}s`;
}

export function formatTimeLong(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  const m = Math.floor(seconds / 60);
  if (m < 60) {
    const s = seconds % 60;
    return s > 0 ? `${m}m ${s}s` : `${m}m`;
  }
  const h = Math.floor(m / 60);
  const remM = m % 60;
  return remM > 0 ? `${h}h ${remM}m` : `${h}h`;
}
