import { useContext } from 'react'

import { ApplicationContext } from './applicationContext'

export function useApplication() {
  const context = useContext(ApplicationContext)
  if (!context) {
    throw new Error('useApplication must be used within ApplicationProvider')
  }
  return context
}
