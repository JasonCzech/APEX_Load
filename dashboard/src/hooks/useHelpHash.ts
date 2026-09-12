import { useSyncExternalStore } from 'react'

function subscribeHash(listener: () => void) {
  window.addEventListener('hashchange', listener)
  return () => window.removeEventListener('hashchange', listener)
}
export function useHelpHash() {
  return useSyncExternalStore(subscribeHash, () => window.location.hash, () => '')
}

