/** A scenario-builder draft saved to a project, as the library lists it. */
export interface ScenarioDraftSummary {
  id: string
  project_id: string
  /** The scenario an edit draft changes; that scenario keeps its saved version meanwhile. */
  scenario_id?: string
  /** That scenario's name, absent when it no longer exists. */
  scenario_name?: string
  name: string
  revision: number
  created_by?: string
  updated_by?: string
  created_at: string
  updated_at: string
}

/** A draft with the builder's own document (a ScenarioAuthoringDraft). */
export interface ScenarioDraft extends ScenarioDraftSummary {
  document: unknown
}

export interface ScenarioDraftSave {
  name: string
  scenario_id?: string
  document: unknown
}
