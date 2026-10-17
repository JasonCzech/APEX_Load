import { createContext } from 'react'

// The service shape remains inferred from the real provider assembly. This
// type-only edge does not load service hooks into consumer route chunks.
import type { ApiServices } from './ApiProvider'
import type { useBrowserRecorderApi } from './api/useBrowserRecorderApi'

export const ApiContext = createContext<ApiServices | null>(null)

export const RecorderConnectionContext = createContext<ReturnType<typeof useBrowserRecorderApi> | null>(null)
