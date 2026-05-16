import { useContext } from 'react'

import { ProjectContext } from './projectContext'

export function useProject() {
  const context = useContext(ProjectContext)
  if (!context) {
    throw new Error('useProject must be used within ProjectProvider')
  }
  return context
}
