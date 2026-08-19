const credentialNouns = [
  'PASSWORD', 'PASSPHRASE', 'SECRET', 'TOKEN', 'APIKEY', 'API_KEY',
  'CREDENTIAL', 'CREDENTIALS', 'KEY', 'USERNAME', 'USER', 'COOKIE',
] as const

// Extracts the credential kind an importer-derived name ends with
// (LOGIN_A_PASSWORD -> PASSWORD). Names that end in nothing recognizable get
// no reuse offer rather than a guess.
export function credentialNoun(suggestedName: string): string {
  const normalized = suggestedName.trim().toUpperCase()
  if (!normalized) return ''
  for (const noun of credentialNouns) {
    if (normalized === noun || normalized.endsWith(`_${noun}`)) return noun
  }
  return ''
}
