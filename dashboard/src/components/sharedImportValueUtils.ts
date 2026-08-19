import { type SharedValueCandidate } from '../types/scriptImports'

export function reachLabel(candidate: SharedValueCandidate): string {
  const requests = candidate.occurrence_count || 0
  const scripts = candidate.script_names?.length || 0
  const requestText = `${requests} ${requests === 1 ? 'request' : 'requests'}`
  if (scripts <= 1) return `Used in ${requestText}`
  return `Used in ${requestText} across ${scripts} scripts`
}

// Mirrors pkg/convert/sharedvalue's parameterNamePattern. A name outside it
// produces a parameter no {{param.NAME}} template could reference.
const namePattern = /^[A-Za-z_][A-Za-z0-9_]{0,63}$/

export function sharedValueNameError(name: string): string {
  if (!name.trim()) return 'A name is required.'
  if (!namePattern.test(name)) {
    return 'Use letters, digits, and underscores, starting with a letter or underscore.'
  }
  return ''
}
