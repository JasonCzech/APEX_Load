import { createContext } from 'react'

// The service shape remains inferred from the real provider assembly. This
// type-only edge does not load service hooks into consumer route chunks.
import type { ApiServices } from './ApiProvider'

export const ApiContext = createContext<ApiServices | null>(null)
