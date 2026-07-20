export interface AuthoringHealth {
  score: number
  status: 'healthy' | 'needs_review' | 'blocked' | string
  inputs: AuthoringHealthInput[]
}

export interface AuthoringHealthInput {
  key: string
  status: 'pass' | 'info' | 'warning' | 'blocker' | string
  count?: number
  summary: string
}
