// GET /api/v1/scripts/{id}/load-runs (pkg/api/handlers_script_load_runs.go):
// finished runs of the script's own project whose groups execute it.

export interface ScriptLoadRun {
  test_id: string
  name: string
  status: string
  started_at?: string | null
  completed_at?: string | null
  archive_available: boolean
  /** The run's groups that execute this script. */
  groups: string[]
  vusers?: number
  script_shakeout?: boolean
}

export interface ScriptLoadRunsResponse {
  script_id?: string
  runs: ScriptLoadRun[]
  count: number
  limit: number
}
